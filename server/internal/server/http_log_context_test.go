package server

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/log/global"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/githubnext/gh-cao/server/internal/redisx"
	"github.com/githubnext/gh-cao/server/internal/telemetry"
)

const httpLogHelperEnv = "CAO_TEST_HTTP_LOG_CONTEXT"

func TestHTTPLogsCarryRequestTraceContext(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	// DEBUG is read at package initialization, before the test can set it.
	command := exec.CommandContext(ctx, executable, "-test.run=^TestHTTPLogContextHelper$") // #nosec G204 -- this test binary with a fixed helper.
	for _, value := range os.Environ() {
		key, _, _ := strings.Cut(value, "=")
		if key != "DEBUG" && key != "DEBUG_COLORS" && key != httpLogHelperEnv {
			command.Env = append(command.Env, value)
		}
	}
	command.Env = append(command.Env, "DEBUG=cao:server", "DEBUG_COLORS=0", httpLogHelperEnv+"=1")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("HTTP log context helper failed: %v\n%s", err, output)
	}
}

type httpLogExporter struct {
	records []sdklog.Record
}

func (exporter *httpLogExporter) Export(_ context.Context, records []sdklog.Record) error {
	for _, record := range records {
		exporter.records = append(exporter.records, record.Clone())
	}
	return nil
}

func (*httpLogExporter) Shutdown(context.Context) error   { return nil }
func (*httpLogExporter) ForceFlush(context.Context) error { return nil }

func TestHTTPLogContextHelper(t *testing.T) {
	if os.Getenv(httpLogHelperEnv) != "1" {
		t.Skip("subprocess helper for request log context")
	}
	previousTracer, previousLogger := otel.GetTracerProvider(), global.GetLoggerProvider()
	spans := tracetest.NewInMemoryExporter()
	tracer := sdktrace.NewTracerProvider(sdktrace.WithSyncer(spans))
	logs := &httpLogExporter{}
	provider := sdklog.NewLoggerProvider(sdklog.WithProcessor(sdklog.NewSimpleProcessor(logs)))
	otel.SetTracerProvider(tracer)
	global.SetLoggerProvider(provider)
	t.Cleanup(func() {
		_ = tracer.Shutdown(t.Context())
		_ = provider.Shutdown(t.Context())
		otel.SetTracerProvider(previousTracer)
		global.SetLoggerProvider(previousLogger)
	})
	address, closeServer := fakeRedis(t)
	t.Cleanup(closeServer)
	client, err := redisx.New("redis://" + address)
	if err != nil {
		t.Fatal(err)
	}
	app, err := New(t.Context(), redisx.NewStore(client, "http-log-context"), Config{
		Database: constructorDatabase(), SiteDirectory: t.TempDir(), AccessToken: testAccessToken,
		Listen: "127.0.0.1:8443",
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := app.Handler()
	seenTraces := map[trace.TraceID]bool{}
	for _, input := range []struct {
		body       string
		authorized bool
		status     int
	}{
		{body: `{}`, status: http.StatusUnauthorized},
		{body: `{PRIVATE-invalid-json`, authorized: true, status: http.StatusBadRequest},
		{body: `{"sourceNames":[""],"private":"PRIVATE-body"}`, authorized: true, status: http.StatusBadRequest},
	} {
		request := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
			"http://127.0.0.1/api/v1/query?private=PRIVATE-query", strings.NewReader(input.body))
		if input.authorized {
			request.Header.Set("Authorization", "Bearer "+testAccessToken)
		}
		response := httptest.NewRecorder()
		firstLog := len(logs.records)
		handler.ServeHTTP(response, request)
		if response.Code != input.status {
			t.Fatalf("status = %d, want %d", response.Code, input.status)
		}
		traceID, err := trace.TraceIDFromHex(response.Header().Get(telemetry.TraceIDHeader))
		if err != nil || seenTraces[traceID] {
			t.Fatal("request did not receive a distinct valid trace ID")
		}
		seenTraces[traceID] = true
		errorLogs, queryLogs := 0, 0
		for _, record := range logs.records[firstLog:] {
			if record.TraceID() != traceID || !record.SpanID().IsValid() {
				t.Fatal("request log lost its native trace/span context")
			}
			matchedSpan := false
			for _, span := range spans.GetSpans() {
				if span.SpanContext.TraceID() == traceID && span.SpanContext.SpanID() == record.SpanID() {
					matchedSpan = true
				}
			}
			if !matchedSpan {
				t.Fatal("log context does not identify a span from the same request")
			}
			body := record.Body().AsString()
			if strings.Contains(body, "PRIVATE-") || strings.Contains(body, testAccessToken) {
				t.Fatal("request logging exposed credentials, query strings, or payloads")
			}
			if strings.HasPrefix(body, fmt.Sprintf("http error status=%d ", input.status)) {
				errorLogs++
			}
			if strings.HasPrefix(body, "query received ") {
				queryLogs++
			}
		}
		if errorLogs != 1 {
			t.Fatalf("request produced %d correlated error logs, want 1", errorLogs)
		}
		if strings.Contains(input.body, "sourceNames") && queryLogs != 1 {
			t.Fatal("decoded query did not produce a correlated query log")
		}
	}
}
