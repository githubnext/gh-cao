package telemetry

import (
	"bytes"
	"context"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	otelLog "go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/trace"
	logspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	logsv1 "go.opentelemetry.io/proto/otlp/logs/v1"
	"google.golang.org/protobuf/proto"

	"github.com/githubnext/gh-cao/server/internal/logger"
)

const (
	logHelperEnv     = "CAO_TEST_OTLP_LOG_HELPER"
	logAuthorization = "Bearer test-log-credential"
	logTraceID       = "4bf92f3577b34da6a3ce929d0e0e4736"
	logSpanID        = "00f067aa0ba902b7"
)

func TestLoggerExportsToConfiguredEndpoint(t *testing.T) {
	for _, test := range []struct {
		name           string
		specific       bool
		shared         bool
		enabled        string
		disabled       string
		debug          string
		wantExport     bool
		wantLogHeaders bool
	}{
		{name: "log endpoint", specific: true, enabled: "true", debug: "cao:log-test", wantExport: true, wantLogHeaders: true},
		{name: "shared endpoint", shared: true, enabled: "true", debug: "cao:log-test", wantExport: true},
		{name: "log endpoint inherits shared headers", specific: true, enabled: "true", debug: "cao:log-test", wantExport: true},
		{name: "log endpoint and headers override shared", specific: true, shared: true, enabled: "true", debug: "cao:log-test", wantExport: true, wantLogHeaders: true},
		{name: "explicit exclusion", specific: true, enabled: "true", debug: "cao:*,-cao:log-blocked", wantExport: true, wantLogHeaders: true},
		{name: "opt in required", specific: true, debug: "cao:log-test"},
		{name: "SDK disabled", specific: true, enabled: "true", disabled: "true", debug: "cao:log-test"},
		{name: "namespace selection required", specific: true, enabled: "true"},
		{name: "endpoint required", enabled: "true", debug: "cao:log-test"},
	} {
		t.Run(test.name, func(t *testing.T) {
			requests := make(chan *logspb.ExportLogsServiceRequest, 8)
			logPath := "/shared/v1/logs"
			if test.specific {
				logPath = "/custom/logs"
			}
			receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/shared/v1/traces" || r.URL.Path == "/shared/v1/metrics" {
					w.WriteHeader(http.StatusOK)
					return
				}
				if r.URL.Path != logPath || r.Method != http.MethodPost {
					t.Errorf("unexpected OTLP request: %s %s", r.Method, r.URL.Path)
					http.Error(w, "unexpected request", http.StatusNotFound)
					return
				}
				wantAuthorization := "Bearer test-shared-credential"
				if test.wantLogHeaders {
					wantAuthorization = logAuthorization
				}
				if r.Header.Get("Authorization") != wantAuthorization {
					t.Error("log exporter did not use the configured authentication header")
				}
				if r.Header.Get("Content-Type") != "application/x-protobuf" {
					t.Errorf("unexpected content type: %q", r.Header.Get("Content-Type"))
				}
				data, err := io.ReadAll(r.Body)
				if err != nil {
					t.Errorf("read OTLP logs: %v", err)
					http.Error(w, "invalid body", http.StatusBadRequest)
					return
				}
				var request logspb.ExportLogsServiceRequest
				if err := proto.Unmarshal(data, &request); err != nil {
					t.Errorf("decode OTLP logs: %v", err)
					http.Error(w, "invalid protobuf", http.StatusBadRequest)
					return
				}
				requests <- &request
				w.WriteHeader(http.StatusOK)
			}))
			defer receiver.Close()

			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
			defer cancel()
			// DEBUG is read during package initialization, so exercise the real
			// logger in a fresh process with deployment-like configuration.
			command := exec.CommandContext(ctx, executable, "-test.run=^TestOTLPLogHelper$") // #nosec G204 -- rerun this exact test binary with a fixed helper name.
			for _, value := range os.Environ() {
				key, _, _ := strings.Cut(value, "=")
				if strings.HasPrefix(key, "OTEL_") || key == "DEBUG" || key == "ACTIONS_RUNNER_DEBUG" ||
					key == "CAO_OTEL_LOGS_ENABLED" || key == logHelperEnv {
					continue
				}
				command.Env = append(command.Env, value)
			}
			command.Env = append(command.Env,
				logHelperEnv+"=1",
				"CAO_OTEL_LOGS_ENABLED="+test.enabled,
				"OTEL_SDK_DISABLED="+test.disabled,
				"DEBUG="+test.debug,
				"DEBUG_COLORS=0",
				"OTEL_SERVICE_NAME=cao-log-test",
				"OTEL_RESOURCE_ATTRIBUTES=deployment.environment.name=test",
				"OTEL_EXPORTER_OTLP_HEADERS=Authorization=Bearer%20test-shared-credential",
			)
			if test.specific {
				command.Env = append(command.Env, "OTEL_EXPORTER_OTLP_LOGS_ENDPOINT="+receiver.URL+logPath)
			}
			if test.wantLogHeaders {
				command.Env = append(command.Env, "OTEL_EXPORTER_OTLP_LOGS_HEADERS=Authorization=Bearer%20test-log-credential")
			}
			if test.shared {
				command.Env = append(command.Env, "OTEL_EXPORTER_OTLP_ENDPOINT="+receiver.URL+"/shared")
			}
			var stderr bytes.Buffer
			command.Stderr = &stderr
			if err := command.Run(); err != nil {
				t.Fatalf("log helper failed: %v\n%s", err, stderr.String())
			}
			output := stderr.String()
			if test.debug != "" && (!strings.Contains(output, "processed 3 items") ||
				!strings.Contains(output, "request complete status=200")) {
				t.Fatalf("stderr logging was not preserved: %s", output)
			}
			if strings.Contains(output, "not selected") || strings.Contains(output, "test-log-credential") ||
				strings.Contains(output, "test-shared-credential") || strings.Contains(output, receiver.URL) {
				t.Fatal("stderr contains an excluded record or exporter configuration")
			}

			var count int
			severities := make(map[int32]int)
			for len(requests) > 0 {
				request := <-requests
				for _, resourceLogs := range request.GetResourceLogs() {
					attributes := make(map[string]string)
					for _, attribute := range resourceLogs.GetResource().GetAttributes() {
						attributes[attribute.GetKey()] = attribute.GetValue().GetStringValue()
					}
					if attributes["service.name"] != "cao-log-test" || attributes["service.version"] != "log-test" ||
						attributes["deployment.environment.name"] != "test" {
						t.Errorf("unexpected log resource attributes: %v", attributes)
					}
					for _, scope := range resourceLogs.GetScopeLogs() {
						if scope.GetScope().GetName() != "cao:log-test" {
							t.Errorf("unexpected log namespace: %q", scope.GetScope().GetName())
						}
						for _, record := range scope.GetLogRecords() {
							count++
							severities[int32(record.GetSeverityNumber())]++
							checkExportedLogRecord(t, record)
						}
					}
				}
			}
			wantCount := 0
			if test.wantExport {
				wantCount = 2
			}
			if count != wantCount {
				t.Fatalf("exported %d records after shutdown, want %d", count, wantCount)
			}
			if test.wantExport && (severities[int32(otelLog.SeverityDebug)] != 1 ||
				severities[int32(otelLog.SeverityInfo)] != 1) {
				t.Fatalf("expected one debug and one info record, got %v", severities)
			}
		})
	}
}

func checkExportedLogRecord(t *testing.T, record *logsv1.LogRecord) {
	t.Helper()
	if record.GetTimeUnixNano() == 0 {
		t.Error("exported log has no timestamp")
	}
	switch int32(record.GetSeverityNumber()) {
	case int32(otelLog.SeverityDebug):
		if record.GetBody().GetStringValue() != "processed 3 items" {
			t.Error("debug log body was not preserved")
		}
	case int32(otelLog.SeverityInfo):
		if !strings.HasSuffix(record.GetBody().GetStringValue(), "request complete status=200") {
			t.Error("slog message and attributes were not preserved")
		}
	default:
		t.Errorf("unexpected log severity: %v", record.GetSeverityNumber())
	}
	if hex.EncodeToString(record.GetTraceId()) != logTraceID || hex.EncodeToString(record.GetSpanId()) != logSpanID {
		t.Error("log trace/span context was not preserved")
	}
}

func TestOTLPLogHelper(t *testing.T) {
	if os.Getenv(logHelperEnv) != "1" {
		t.Skip("subprocess helper for configured logger export")
	}
	shutdown, err := Setup(t.Context(), "log-test")
	if err != nil {
		t.Fatal(err)
	}
	traceID, err := trace.TraceIDFromHex(logTraceID)
	if err != nil {
		t.Fatal(err)
	}
	spanID, err := trace.SpanIDFromHex(logSpanID)
	if err != nil {
		t.Fatal(err)
	}
	ctx := trace.ContextWithSpanContext(t.Context(), trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: traceID, SpanID: spanID, TraceFlags: trace.FlagsSampled,
	}))
	logger.New("cao:log-test").PrintfContext(ctx, "processed %d items", 3)
	logger.NewSlogLoggerWithHandler(logger.New("cao:log-test")).InfoContext(ctx, "request complete", "status", 200)
	logger.New("cao:log-blocked").Print("not selected")
	shutdownCtx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := shutdown(shutdownCtx); err != nil {
		t.Fatal(err)
	}
}
