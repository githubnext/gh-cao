package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

func TestHTTPServerTelemetryUsesRouteTemplatesAndHeadMethods(t *testing.T) {
	previousTracer, previousMeter := otel.GetTracerProvider(), otel.GetMeterProvider()
	exporter := tracetest.NewInMemoryExporter()
	tracer := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	reader := sdkmetric.NewManualReader()
	meter := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	otel.SetTracerProvider(tracer)
	otel.SetMeterProvider(meter)
	t.Cleanup(func() {
		_ = tracer.Shutdown(t.Context())
		_ = meter.Shutdown(t.Context())
		otel.SetTracerProvider(previousTracer)
		otel.SetMeterProvider(previousMeter)
	})
	app, err := New(t.Context(), newTestOperationalStore(t), Config{
		Database: constructorDatabase(), SiteDirectory: t.TempDir(), AccessToken: testAccessToken,
		Listen: "127.0.0.1:8443",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/memory/PRIVATE-private-user-path"},
		{http.MethodHead, "/api/v1/memory/PRIVATE-private-user-path"},
		{http.MethodGet, "/api/v1/memory/PRIVATE-private-user-path/content?path=private-content"},
	} {
		request := httptest.NewRequestWithContext(t.Context(), input.method, "http://127.0.0.1"+input.path, nil)
		request.Header.Set("Authorization", "Bearer "+testAccessToken)
		response := httptest.NewRecorder()
		app.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("parameterized handler returned %d, want 400", response.Code)
		}
	}
	wantNames := map[string]bool{
		"GET /api/v1/memory/{campaign}": false, "HEAD /api/v1/memory/{campaign}": false,
		"GET /api/v1/memory/{campaign}/content": false,
	}
	for _, span := range exporter.GetSpans() {
		if span.SpanKind != trace.SpanKindServer {
			continue
		}
		if _, exists := wantNames[span.Name]; !exists {
			t.Fatalf("unexpected server span: %s", span.Name)
		}
		wantNames[span.Name] = true
	}
	for name, found := range wantNames {
		if !found {
			t.Fatalf("missing route-template span: %s", name)
		}
	}
	var measured metricdata.ResourceMetrics
	if err := reader.Collect(t.Context(), &measured); err != nil {
		t.Fatal(err)
	}
	var routedRequests uint64
	for _, scope := range measured.ScopeMetrics {
		for _, recorded := range scope.Metrics {
			if recorded.Name != "http.server.request.duration" {
				continue
			}
			for _, point := range recorded.Data.(metricdata.Histogram[float64]).DataPoints {
				attrs := spanAttributes(point.Attributes.ToSlice())
				method, methodPresent := attrs["http.request.method"].(string)
				route, routePresent := attrs["http.route"].(string)
				if !methodPresent || !routePresent || !wantNames[method+" "+route] {
					t.Fatalf("HTTP duration omitted its normalized route: %v", attrs)
				}
				routedRequests += point.Count
			}
		}
	}
	if routedRequests != 3 {
		t.Fatalf("route-labeled HTTP duration samples = %d, want 3", routedRequests)
	}
	encoded, err := json.Marshal(struct {
		Spans   tracetest.SpanStubs
		Metrics metricdata.ResourceMetrics
	}{exporter.GetSpans(), measured})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "private-") || strings.Contains(string(encoded), testAccessToken) {
		t.Fatal("route telemetry exposed path parameters, query strings or credentials")
	}
}
