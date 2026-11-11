package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/githubnext/gh-cao/server/internal/admission"
	"github.com/githubnext/gh-cao/server/internal/operational/memory"
)

func TestAdmissionHTTPMetricsAndSpansStayBounded(t *testing.T) {
	for _, path := range []string{"/api/admission", "/api/conclusion", tokenBrokerPath} {
		t.Run(path, func(t *testing.T) { testAdmissionHTTPMetricsAndSpans(t, path) })
	}
}

func testAdmissionHTTPMetricsAndSpans(t *testing.T, path string) {
	reader := sdkmetric.NewManualReader()
	meter := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	exporter := tracetest.NewInMemoryExporter()
	tracer := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	previousTracer, previousMeter := otel.GetTracerProvider(), otel.GetMeterProvider()
	otel.SetTracerProvider(tracer)
	otel.SetMeterProvider(meter)
	defer func() {
		_ = tracer.Shutdown(context.Background())
		_ = meter.Shutdown(context.Background())
		otel.SetTracerProvider(previousTracer)
		otel.SetMeterProvider(previousMeter)
	}()
	store, err := memory.New(memory.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	verifier, err := admission.NewVerifier("https://admission.example/api/admission")
	if err != nil {
		t.Fatal(err)
	}
	app := &App{services: store.OperationalServices(),
		config: Config{HostProfile: hostedHostProfile(), Proxy: ProxyPolicy{
			AllowedHosts: []string{"admission.example"}, RequireHTTPS: true,
		}}, admission: &admission.Service{Verifier: verifier},
	}
	deniedStatus := int64(403)
	if path == tokenBrokerPath {
		app.tokenBroker = newDeniedTokenBroker(t)
		deniedStatus = 400
	}
	handler := app.Handler()
	for n := 0; n < 62; n++ {
		if n == 61 {
			app.Drain()
		}
		request := httptest.NewRequestWithContext(t.Context(), "POST", "https://admission.example"+path+"?PRIVATE_QUERY=private-value", strings.NewReader(`{"private":"PRIVATE_BODY"}`))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Authorization", "Bearer PRIVATE_CREDENTIAL")
		request.Header.Set("Baggage", "private=PRIVATE_BAGGAGE")
		request.Header.Set("Traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
	}
	var metrics metricdata.ResourceMetrics
	if err := reader.Collect(t.Context(), &metrics); err != nil {
		t.Fatal(err)
	}
	counts := map[int64]uint64{}
	for _, scope := range metrics.ScopeMetrics {
		for _, instrument := range scope.Metrics {
			if instrument.Name != "http.server.request.duration" {
				continue
			}
			for _, point := range instrument.Data.(metricdata.Histogram[float64]).DataPoints {
				attrs := spanAttributes(point.Attributes.ToSlice())
				if attrs["http.route"] != path || attrs["http.request.method"] != "POST" {
					t.Fatalf("unbounded or missing route dimensions: %v", attrs)
				}
				status, ok := attrs["http.response.status_code"].(int64)
				if !ok {
					t.Fatalf("HTTP outcome missing: %v", attrs)
				}
				counts[status] += point.Count
			}
		}
	}
	if counts[deniedStatus] != 60 || counts[429] != 1 || counts[503] != 1 {
		t.Fatalf("HTTP telemetry missed denial/throttle/unavailability: %v", counts)
	}
	encoded, err := json.Marshal(struct {
		Spans   tracetest.SpanStubs
		Metrics metricdata.ResourceMetrics
	}{exporter.GetSpans(), metrics})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "PRIVATE") || strings.Contains(string(encoded), "private-value") {
		t.Fatal("HTTP observability captured sensitive headers, URL or body")
	}
	for _, scope := range metrics.ScopeMetrics {
		for _, instrument := range scope.Metrics {
			if histogram, ok := instrument.Data.(metricdata.Histogram[float64]); ok {
				for _, point := range histogram.DataPoints {
					for _, attr := range point.Attributes.ToSlice() {
						if strings.Contains(string(attr.Key), "trace") || strings.Contains(string(attr.Key), "actor") ||
							strings.Contains(string(attr.Key), "repository") {
							t.Fatal("high-cardinality private metric dimension")
						}
					}
				}
			}
		}
	}
}
