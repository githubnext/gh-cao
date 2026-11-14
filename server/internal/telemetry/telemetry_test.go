package telemetry

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/baggage"
	otelLog "go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	logspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	metricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	"google.golang.org/protobuf/proto"
)

const testSpanName = "test-span"

func TestResolveExporterDecision(t *testing.T) {
	tests := []struct {
		name              string
		sdkDisabledEnv    string
		tracesEndpointEnv string
		endpointEnv       string
		wantEndpoint      string
		wantDecision      exporterDecision
	}{
		{
			name:           "sdk disabled overrides a configured endpoint",
			sdkDisabledEnv: "true",
			endpointEnv:    "http://127.0.0.1:4318",
			wantEndpoint:   "",
			wantDecision:   exporterDecisionDisabled,
		},
		{
			name:           "sdk disabled comparison is case-insensitive and trims whitespace",
			sdkDisabledEnv: " True ",
			endpointEnv:    "http://127.0.0.1:4318",
			wantEndpoint:   "",
			wantDecision:   exporterDecisionDisabled,
		},
		{
			name:         "no endpoint configured stays noop",
			wantEndpoint: "",
			wantDecision: exporterDecisionNoEndpoint,
		},
		{
			name:              "traces endpoint takes priority over the general endpoint",
			tracesEndpointEnv: "http://traces.example:4318",
			endpointEnv:       "http://general.example:4318",
			wantEndpoint:      "http://traces.example:4318",
			wantDecision:      exporterDecisionConfigured,
		},
		{
			name:         "general endpoint is used when no traces endpoint is set",
			endpointEnv:  "http://general.example:4318",
			wantEndpoint: "http://general.example:4318",
			wantDecision: exporterDecisionConfigured,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotEndpoint, gotDecision := resolveExporterDecision(tt.sdkDisabledEnv, tt.tracesEndpointEnv, tt.endpointEnv)
			if gotEndpoint != tt.wantEndpoint {
				t.Errorf("endpoint = %q, want %q", gotEndpoint, tt.wantEndpoint)
			}
			if gotDecision != tt.wantDecision {
				t.Errorf("decision = %q, want %q", gotDecision, tt.wantDecision)
			}
		})
	}
}

func TestSetupWithoutEndpointStaysNoop(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", "")
	t.Setenv("OTEL_SDK_DISABLED", "")
	shutdown, err := Setup(context.Background(), "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if shutdown == nil {
		t.Fatal("Setup must always return a non-nil shutdown function")
	}
	if err := shutdown(context.Background()); err != nil {
		t.Fatalf("no-op shutdown must not fail: %v", err)
	}
	_, span := Tracer().Start(context.Background(), testSpanName)
	defer span.End()
	if span.SpanContext().IsValid() {
		t.Fatal("the default no-op tracer provider must not produce an exported span context")
	}
}

func TestSetupDisabledSkipsExporter(t *testing.T) {
	t.Setenv("OTEL_SDK_DISABLED", "true")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://127.0.0.1:4318")
	shutdown, err := Setup(context.Background(), "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := shutdown(context.Background()); err != nil {
		t.Fatalf("no-op shutdown must not fail: %v", err)
	}
}

func TestSetupWithHTTPEndpointConfiguresProvider(t *testing.T) {
	previousProvider := otel.GetTracerProvider()
	t.Cleanup(func() { otel.SetTracerProvider(previousProvider) })
	t.Setenv("OTEL_SDK_DISABLED", "")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://127.0.0.1:4318")
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "")
	shutdown, err := Setup(context.Background(), "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer func() {
		// The exporter never dials during Setup, so shutdown must not
		// hang or fail even though nothing is listening on the endpoint.
		ctx, cancel := context.WithTimeout(context.Background(), 0)
		defer cancel()
		_ = shutdown(ctx)
	}()
	provider := otel.GetTracerProvider()
	if provider == nil {
		t.Fatal("expected a global tracer provider to be installed")
	}
	carrier := propagation.MapCarrier{
		"traceparent": "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
		"baggage":     "account=private-identifier",
	}
	extracted := otel.GetTextMapPropagator().Extract(context.Background(), carrier)
	if !trace.SpanContextFromContext(extracted).IsValid() {
		t.Fatal("traceparent must still be propagated")
	}
	if baggage.FromContext(extracted).Len() != 0 {
		t.Fatal("untrusted baggage must not be propagated")
	}
}

func TestSetupExportsTraceToConfiguredOpenObserveEndpoint(t *testing.T) {
	const (
		tracePath           = "/api/default/v1/traces"
		authorizationHeader = "Basic test-credential"
	)
	type exportedRequest struct {
		method        string
		path          string
		authorization string
		contentType   string
		bodySize      int
		readErr       error
	}
	requests := make(chan exportedRequest, 1)
	receiver := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		body, err := io.ReadAll(request.Body)
		requests <- exportedRequest{
			method:        request.Method,
			path:          request.URL.Path,
			authorization: request.Header.Get("Authorization"),
			contentType:   request.Header.Get("Content-Type"),
			bodySize:      len(body),
			readErr:       err,
		}
		response.Header().Set("Content-Type", "application/x-protobuf")
		response.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(receiver.Close)

	previousProvider := otel.GetTracerProvider()
	t.Cleanup(func() { otel.SetTracerProvider(previousProvider) })
	t.Setenv("OTEL_SDK_DISABLED", "")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_HEADERS", "")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", receiver.URL+tracePath)
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_HEADERS", "Authorization="+authorizationHeader)
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_HEADERS", "")
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "")

	shutdown, err := Setup(t.Context(), "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_, span := Tracer().Start(t.Context(), testSpanName)
	span.End()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := shutdown(shutdownCtx); err != nil {
		t.Fatalf("shutdown exporter: %v", err)
	}

	select {
	case request := <-requests:
		if request.readErr != nil {
			t.Fatalf("read exported trace: %v", request.readErr)
		}
		if request.method != http.MethodPost {
			t.Errorf("method = %q, want %q", request.method, http.MethodPost)
		}
		if request.path != tracePath {
			t.Errorf("path = %q, want %q", request.path, tracePath)
		}
		if request.authorization != authorizationHeader {
			t.Errorf("authorization header = %q, want %q", request.authorization, authorizationHeader)
		}
		if request.contentType != "application/x-protobuf" {
			t.Errorf("content type = %q, want application/x-protobuf", request.contentType)
		}
		if request.bodySize == 0 {
			t.Error("exported trace body must not be empty")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for exported trace")
	}
}

func TestSetupWithMetricsEndpointConfiguresMeterProviderOnly(t *testing.T) {
	previousMeterProvider := otel.GetMeterProvider()
	previousTracerProvider := otel.GetTracerProvider()
	t.Cleanup(func() {
		otel.SetMeterProvider(previousMeterProvider)
		otel.SetTracerProvider(previousTracerProvider)
	})
	t.Setenv("OTEL_SDK_DISABLED", "")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", "http://127.0.0.1:4318")

	shutdown, err := Setup(context.Background(), "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 0)
		defer cancel()
		_ = shutdown(ctx)
	}()
	if otel.GetMeterProvider() == previousMeterProvider {
		t.Fatal("expected a global meter provider to be installed")
	}
	if otel.GetTracerProvider() != previousTracerProvider {
		t.Fatal("metrics-only configuration must not replace the tracer provider")
	}
}

func TestSetupExportsGoRuntimeMetrics(t *testing.T) {
	requests := make(chan *metricspb.ExportMetricsServiceRequest, 1)
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/metrics" {
			http.Error(w, "unexpected path", http.StatusNotFound)
			return
		}
		data, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "invalid body", http.StatusBadRequest)
			return
		}
		var request metricspb.ExportMetricsServiceRequest
		if err := proto.Unmarshal(data, &request); err != nil {
			http.Error(w, "invalid protobuf", http.StatusBadRequest)
			return
		}
		requests <- &request
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(receiver.Close)

	previousMeterProvider := otel.GetMeterProvider()
	t.Cleanup(func() { otel.SetMeterProvider(previousMeterProvider) })
	t.Setenv("OTEL_SDK_DISABLED", "")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", receiver.URL+"/v1/metrics")
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "")

	shutdown, err := Setup(t.Context(), "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := shutdown(shutdownCtx); err != nil {
		t.Fatalf("shutdown exporter: %v", err)
	}

	select {
	case request := <-requests:
		names := make(map[string]bool)
		for _, resourceMetrics := range request.GetResourceMetrics() {
			for _, scopeMetrics := range resourceMetrics.GetScopeMetrics() {
				for _, metric := range scopeMetrics.GetMetrics() {
					names[metric.GetName()] = true
				}
			}
		}
		for _, name := range []string{"go.memory.allocated", "go.goroutine.count"} {
			if !names[name] {
				t.Errorf("exported metrics do not include %q: %v", name, names)
			}
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for exported metrics")
	}
}

func TestSetupExportsLogsOnlyWhenEnabled(t *testing.T) {
	previousProvider := otel.GetLoggerProvider()
	t.Cleanup(func() { otel.SetLoggerProvider(previousProvider) })
	requests := make(chan *logspb.ExportLogsServiceRequest, 1)
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/logs" {
			http.Error(w, "unexpected path", http.StatusNotFound)
			return
		}
		data, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "invalid body", http.StatusBadRequest)
			return
		}
		var request logspb.ExportLogsServiceRequest
		if err := proto.Unmarshal(data, &request); err != nil {
			http.Error(w, "invalid protobuf", http.StatusBadRequest)
			return
		}
		requests <- &request
		w.WriteHeader(http.StatusOK)
	}))
	defer receiver.Close()
	t.Setenv("OTEL_SDK_DISABLED", "")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_LOGS_ENDPOINT", receiver.URL+"/v1/logs")

	t.Setenv("CAO_OTEL_LOGS_ENABLED", "")
	shutdown, err := Setup(context.Background(), "test")
	if err != nil {
		t.Fatal(err)
	}
	if otel.GetLoggerProvider() != previousProvider {
		t.Fatal("log endpoint alone must not enable export")
	}
	if err := shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}

	t.Setenv("CAO_OTEL_LOGS_ENABLED", "true")
	t.Setenv("OTEL_SDK_DISABLED", "true")
	shutdown, err = Setup(context.Background(), "test")
	if err != nil {
		t.Fatal(err)
	}
	if otel.GetLoggerProvider() != previousProvider {
		t.Fatal("OTEL_SDK_DISABLED must override log export")
	}
	if err := shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}

	t.Setenv("OTEL_SDK_DISABLED", "")
	shutdown, err = Setup(context.Background(), "test")
	if err != nil {
		t.Fatal(err)
	}
	if otel.GetLoggerProvider() == previousProvider {
		t.Fatal("enabled logs must install a log provider")
	}
	var record otelLog.Record
	record.SetBody(attribute.StringValue("safe diagnostic"))
	record.SetSeverity(otelLog.SeverityError)
	otel.GetLoggerProvider().Logger("cao:test").Emit(context.Background(), record)
	if err := shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case request := <-requests:
		logs := request.GetResourceLogs()
		if len(logs) != 1 || len(logs[0].GetScopeLogs()) != 1 ||
			len(logs[0].GetScopeLogs()[0].GetLogRecords()) != 1 {
			t.Fatalf("unexpected OTLP log batch: %v", request)
		}
		got := logs[0].GetScopeLogs()[0].GetLogRecords()[0]
		if got.GetBody().GetStringValue() != "safe diagnostic" ||
			got.GetSeverityNumber() != 17 ||
			logs[0].GetScopeLogs()[0].GetScope().GetName() != "cao:test" {
			t.Fatalf("unexpected OTLP record: %v", got)
		}
	default:
		t.Fatal("shutdown did not flush the log batch")
	}
}

func TestShutdownExportersDoesNotBlockHealthySignals(t *testing.T) {
	finished := make(chan struct{})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	stalled := errors.New("collector timed out")
	err := shutdownExporters(ctx,
		func(ctx context.Context) error {
			<-ctx.Done()
			return stalled
		},
		func(ctx context.Context) error {
			if ctx.Err() != nil {
				t.Error("healthy exporter received an expired deadline")
			}
			close(finished)
			return nil
		},
	)
	select {
	case <-finished:
	default:
		t.Fatal("healthy exporter did not flush")
	}
	if !errors.Is(err, stalled) {
		t.Fatalf("shutdown error = %v, want stalled exporter error", err)
	}
}

func TestSetResponseTraceHeadersOnlyWritesValidSpanContext(t *testing.T) {
	recorder := httptest.NewRecorder()
	SetResponseTraceHeaders(recorder, trace.SpanContext{})
	if recorder.Header().Get(TraceIDHeader) != "" {
		t.Fatal("an invalid span context must not set trace headers")
	}

	traceID, _ := trace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
	spanID, _ := trace.SpanIDFromHex("00f067aa0ba902b7")
	spanContext := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     spanID,
		TraceFlags: trace.FlagsSampled,
	})
	SetResponseTraceHeaders(recorder, spanContext)
	if got := recorder.Header().Get(TraceIDHeader); got != traceID.String() {
		t.Fatalf("trace id header = %q, want %q", got, traceID.String())
	}
	if got := recorder.Header().Get(SpanIDHeader); got != spanID.String() {
		t.Fatalf("span id header = %q, want %q", got, spanID.String())
	}
}
