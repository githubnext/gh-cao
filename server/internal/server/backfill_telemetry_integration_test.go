package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/githubnext/gh-cao/server/internal/telemetry"
)

func TestBackfillTelemetryWithoutOpenObservePreservesTraceContext(t *testing.T) {
	t.Setenv("CAO_BACKFILL_SKIP_OPENOBSERVE", "1")
	if flush := backfillTelemetry(t, t.Context(), "backfill-without-openobserve"); flush != nil {
		t.Fatal("rate-limited OpenObserve must not configure exporters")
	}
	ctx, span := otel.Tracer("backfill-test").Start(t.Context(), "local-correlation")
	defer span.End()
	if !span.SpanContext().IsValid() {
		t.Fatal("local backfill tracing must preserve valid trace and span IDs")
	}
	carrier := propagation.MapCarrier{}
	otel.GetTextMapPropagator().Inject(ctx, carrier)
	if carrier.Get("traceparent") == "" {
		t.Fatal("local backfill tracing must propagate W3C trace context")
	}
}

func backfillTelemetry(t *testing.T, ctx context.Context, service string) func(*testing.T) {
	t.Helper()
	previousTracer := otel.GetTracerProvider()
	previousMeter := otel.GetMeterProvider()
	previousPropagator := otel.GetTextMapPropagator()
	previousLogs := otel.GetLoggerProvider()
	if os.Getenv("CAO_BACKFILL_SKIP_OPENOBSERVE") == "1" {
		tracer := sdktrace.NewTracerProvider()
		otel.SetTracerProvider(tracer)
		otel.SetTextMapPropagator(propagation.TraceContext{})
		t.Cleanup(func() {
			if err := tracer.Shutdown(context.WithoutCancel(ctx)); err != nil {
				t.Error("shut down local trace provider")
			}
			otel.SetTracerProvider(previousTracer)
			otel.SetTextMapPropagator(previousPropagator)
		})
		t.Log("OpenObserve image pull was rate-limited; trace correlation remains enabled without exporters")
		return nil
	}
	t.Setenv("OTEL_SDK_DISABLED", "false")
	t.Setenv("OTEL_SERVICE_NAME", service)
	t.Setenv("OTEL_TRACES_SAMPLER", "always_on")
	t.Setenv("CAO_OTEL_LOGS_ENABLED", "false")
	if os.Getenv("CAO_BACKFILL_OTEL_LOGS") == "1" {
		t.Setenv("CAO_OTEL_LOGS_ENABLED", "true")
	}
	shutdown, err := telemetry.Setup(ctx, "backfill-integration")
	if err != nil {
		t.Fatal("initialize local OTLP exporters")
	}
	t.Cleanup(func() {
		stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		if provider, ok := otel.GetTracerProvider().(*sdktrace.TracerProvider); ok {
			if err := provider.ForceFlush(stopCtx); err != nil {
				t.Error("flush diagnostic traces")
			}
		}
		if provider, ok := otel.GetLoggerProvider().(*sdklog.LoggerProvider); ok {
			if err := provider.ForceFlush(stopCtx); err != nil {
				t.Error("flush diagnostic logs")
			}
		}
		if provider, ok := otel.GetMeterProvider().(*sdkmetric.MeterProvider); ok {
			if err := provider.ForceFlush(stopCtx); err != nil {
				t.Error("flush diagnostic metrics")
			}
		}
		downloadBackfillTelemetry(t, stopCtx, service)
		if err := shutdown(stopCtx); err != nil {
			t.Error("shut down local OTLP exporters")
		}
		otel.SetTracerProvider(previousTracer)
		otel.SetMeterProvider(previousMeter)
		otel.SetTextMapPropagator(previousPropagator)
		otel.SetLoggerProvider(previousLogs)
	})
	tracer, ok := otel.GetTracerProvider().(*sdktrace.TracerProvider)
	if !ok {
		t.Fatal("OTLP trace provider is not configured")
	}
	meter, ok := otel.GetMeterProvider().(*sdkmetric.MeterProvider)
	if !ok {
		t.Fatal("OTLP metric provider is not configured")
	}
	return func(t *testing.T) {
		t.Helper()
		if err := tracer.ForceFlush(ctx); err != nil {
			t.Fatal("OpenObserve rejected trace export")
		}
		if err := meter.ForceFlush(ctx); err != nil {
			t.Fatal("OpenObserve rejected metric export")
		}
		if provider, ok := otel.GetLoggerProvider().(*sdklog.LoggerProvider); ok {
			if err := provider.ForceFlush(ctx); err != nil {
				t.Fatal("OpenObserve rejected log export")
			}
		}
	}
}

type backfillObserveResult struct {
	Hits []map[string]any `json:"hits"`
}

func backfillObserveRequest(t *testing.T, ctx context.Context, path string, input, output any) {
	t.Helper()
	method := http.MethodGet
	var body bytes.Buffer
	if input != nil {
		method = http.MethodPost
		if err := json.NewEncoder(&body).Encode(input); err != nil {
			t.Fatal(err)
		}
	}
	// #nosec G704 -- the test harness supplies the isolated loopback OpenObserve endpoint.
	request, err := http.NewRequestWithContext(ctx, method,
		os.Getenv("CAO_BACKFILL_OPENOBSERVE_URL")+"/api/default/"+path, &body)
	if err != nil {
		t.Fatal("create OpenObserve verification request")
	}
	request.SetBasicAuth(os.Getenv("CAO_LOCAL_OTEL_EMAIL"), os.Getenv("CAO_LOCAL_OTEL_PASSWORD"))
	request.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 10 * time.Second}
	response, err := client.Do(request) // #nosec G704 -- the request targets the harness's local OpenObserve container.
	if err != nil {
		t.Fatal("local OpenObserve is unavailable")
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("OpenObserve verification returned HTTP %d", response.StatusCode)
	}
	if err := json.NewDecoder(response.Body).Decode(output); err != nil {
		t.Fatal("decode OpenObserve verification response")
	}
}

func backfillObserveSearch(t *testing.T, ctx context.Context, signal, sql string, started time.Time) []map[string]any {
	t.Helper()
	var result backfillObserveResult
	backfillObserveRequest(t, ctx, "_search?type="+signal, map[string]any{
		"query": map[string]any{
			"sql": sql, "start_time": started.Add(-time.Second).UnixMicro(),
			"end_time": time.Now().Add(time.Second).UnixMicro(), "size": 1000,
		},
	}, &result)
	return result.Hits
}

func assertBackfillTelemetry(t *testing.T, ctx context.Context, service, queryTrace string, started time.Time) {
	t.Helper()
	deadline := time.NewTimer(45 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	wantSpans := map[string]bool{
		telemetry.SpanIngestRun: false, telemetry.SpanPostgresQuery: false,
		telemetry.SpanQueryExecute: false,
		telemetry.SpanBackfillRun:  false,
	}
	correlated := map[string]bool{
		"POST /api/v1/query": false, telemetry.SpanQueryExecute: false, telemetry.SpanPostgresQuery: false,
	}
	for {
		rows := backfillObserveSearch(t, ctx, "traces",
			`SELECT * FROM "default" WHERE service_name = '`+service+
				`' AND (operation_name IN ('`+telemetry.SpanIngestRun+`', '`+telemetry.SpanBackfillRun+`', '`+telemetry.SpanQueryExecute+
				`') OR trace_id = '`+queryTrace+`')`, started)
		for _, row := range rows {
			name, _ := row["operation_name"].(string)
			if _, ok := wantSpans[name]; ok {
				wantSpans[name] = true
			}
			if row["trace_id"] == queryTrace {
				if _, ok := correlated[name]; ok {
					correlated[name] = true
				}
			}
		}
		if wantSpans[telemetry.SpanIngestRun] && wantSpans[telemetry.SpanPostgresQuery] &&
			wantSpans[telemetry.SpanQueryExecute] && wantSpans[telemetry.SpanBackfillRun] && correlated["POST /api/v1/query"] &&
			correlated[telemetry.SpanQueryExecute] && correlated[telemetry.SpanPostgresQuery] {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("backfill telemetry verification timed out")
		case <-deadline.C:
			t.Fatalf("OpenObserve lacks production backfill/query/Postgres traces or HTTP correlation: spans=%v correlated=%v",
				wantSpans, correlated)
		case <-tick.C:
		}
	}
	for _, metric := range []string{
		"cao_dashboard.postgres.query.count",
		"cao_dashboard.collection.backfill.run_task_queued.count",
		"cao_dashboard.collection.backfill.run_task_deduplicated.count",
	} {
		assertBackfillMetricExport(t, ctx, metric, started)
	}
}

func assertBackfillMetricExport(t *testing.T, ctx context.Context, metric string, started time.Time) {
	t.Helper()
	var streams struct {
		List []struct {
			Name string `json:"name"`
		} `json:"list"`
	}
	backfillObserveRequest(t, ctx, "streams?type=metrics", nil, &streams)
	stream := ""
	for _, candidate := range streams.List {
		if candidate.Name == metric || candidate.Name == strings.ReplaceAll(metric, ".", "_") {
			stream = candidate.Name
			break
		}
	}
	if stream == "" {
		t.Fatalf("OpenObserve did not persist metric %s", metric)
	}
	rows := backfillObserveSearch(t, ctx, "metrics", fmt.Sprintf(`SELECT * FROM "%s"`, stream), started)
	for _, row := range rows {
		if value, ok := row["value"].(float64); ok && value > 0 {
			return
		}
	}
	t.Fatalf("OpenObserve metric %s has no positive samples", metric)
}
