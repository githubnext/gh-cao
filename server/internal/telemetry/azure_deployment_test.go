package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	logspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	metricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	tracespb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/protobuf/proto"

	"github.com/githubnext/gh-cao/server/internal/logger"
)

const azureTelemetryHelper = "CAO_TEST_AZURE_TELEMETRY"

func TestAzureDeploymentTelemetryReceipts(t *testing.T) {
	// Consume the generated deployment settings, not a parallel hand-written
	// OTEL configuration. Template contracts verify all three runtime consumers.
	content, err := os.ReadFile("../../azure/main.json")
	if err != nil {
		t.Fatal(err)
	}
	var template struct {
		Variables struct {
			Environment []struct {
				Name  string `json:"name"`
				Value string `json:"value"`
			} `json:"otlpEnvironment"`
		} `json:"variables"`
	}
	if err := json.Unmarshal(content, &template); err != nil {
		t.Fatal(err)
	}
	if len(template.Variables.Environment) != 3 {
		t.Fatal("expected the deployment endpoint, protocol and log opt-in")
	}
	for _, scenario := range []struct {
		name     string
		endpoint bool
		logs     bool
		disabled bool
		reject   bool
	}{
		{name: "template defaults do not export"},
		{name: "TLS traces and metrics", endpoint: true},
		{name: "TLS all signals with explicit log opt-in", endpoint: true, logs: true},
		{name: "SDK disable overrides deployment", endpoint: true, logs: true, disabled: true},
		{name: "unavailable exporter does not gate requests", endpoint: true, logs: true, reject: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			t.Parallel()
			var mu sync.Mutex
			receipts := map[string]int{}
			receiver := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/x-protobuf" ||
					r.Header.Get("Authorization") != "Bearer synthetic-azure-otlp" {
					t.Error("export did not use authenticated OTLP/HTTP protobuf")
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				if scenario.reject {
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error("read synthetic OTLP request")
					return
				}
				var records int
				switch r.URL.Path {
				case "/otlp/v1/traces":
					var request tracespb.ExportTraceServiceRequest
					if err := proto.Unmarshal(body, &request); err != nil {
						t.Error(err)
					}
					for _, resource := range request.GetResourceSpans() {
						for _, scope := range resource.GetScopeSpans() {
							for _, span := range scope.GetSpans() {
								if span.GetName() == "azure.synthetic.request" {
									records++
								}
							}
						}
					}
				case "/otlp/v1/metrics":
					var request metricspb.ExportMetricsServiceRequest
					if err := proto.Unmarshal(body, &request); err != nil {
						t.Error(err)
					}
					for _, resource := range request.GetResourceMetrics() {
						for _, scope := range resource.GetScopeMetrics() {
							for _, metric := range scope.GetMetrics() {
								if metric.GetName() == "azure.synthetic.requests" {
									records += len(metric.GetSum().GetDataPoints())
								}
							}
						}
					}
				case "/otlp/v1/logs":
					var request logspb.ExportLogsServiceRequest
					if err := proto.Unmarshal(body, &request); err != nil {
						t.Error(err)
					}
					for _, resource := range request.GetResourceLogs() {
						for _, scope := range resource.GetScopeLogs() {
							for _, record := range scope.GetLogRecords() {
								if record.GetBody().GetStringValue() == "synthetic request status=200" {
									records++
								}
							}
						}
					}
				default:
					t.Error("export used an unexpected signal path")
				}
				mu.Lock()
				receipts[r.URL.Path] += records
				mu.Unlock()
				w.Header().Set("Content-Type", "application/x-protobuf")
			}))
			defer receiver.Close()
			certificate := filepath.Join(t.TempDir(), "collector.pem")
			if err := os.WriteFile(certificate, pem.EncodeToMemory(&pem.Block{
				Type: "CERTIFICATE", Bytes: receiver.Certificate().Raw,
			}), 0o600); err != nil {
				t.Fatal(err)
			}
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, executable, "-test.run=^TestAzureTelemetryHelper$") // #nosec G204 -- this test binary and fixed helper.
			for _, entry := range os.Environ() {
				key, _, _ := strings.Cut(entry, "=")
				if strings.HasPrefix(key, "OTEL_") || key == "DEBUG" || key == "ACTIONS_RUNNER_DEBUG" ||
					key == "CAO_OTEL_LOGS_ENABLED" || key == azureTelemetryHelper {
					continue
				}
				command.Env = append(command.Env, entry)
			}
			for _, setting := range template.Variables.Environment {
				value := setting.Value
				switch value {
				case "[parameters('otlpEndpoint')]":
					value = ""
					if scenario.endpoint {
						value = receiver.URL + "/otlp"
					}
				case "[string(parameters('otlpLogsEnabled'))]":
					value = fmt.Sprint(scenario.logs)
				case "http/protobuf":
				default:
					t.Fatal("unrecognized generated telemetry setting")
				}
				command.Env = append(command.Env, setting.Name+"="+value)
			}
			command.Env = append(command.Env,
				azureTelemetryHelper+"=1",
				"CAO_TEST_EXPORT_REJECTED="+fmt.Sprint(scenario.reject),
				"OTEL_SDK_DISABLED="+fmt.Sprint(scenario.disabled),
				"OTEL_EXPORTER_OTLP_CERTIFICATE="+certificate,
				"OTEL_EXPORTER_OTLP_HEADERS=Authorization=Bearer%20synthetic-azure-otlp",
				"DEBUG=cao:server,cao:telemetry", "DEBUG_COLORS=0",
			)
			var output bytes.Buffer
			command.Stdout, command.Stderr = &output, &output
			if err := command.Run(); err != nil {
				t.Fatalf("deployment helper: %v\n%s", err, output.String())
			}
			if strings.Contains(output.String(), "synthetic-azure-otlp") {
				t.Fatal("exporter credentials appeared in process diagnostics")
			}
			mu.Lock()
			defer mu.Unlock()
			for _, signal := range []string{"traces", "metrics", "logs"} {
				want := scenario.endpoint && !scenario.disabled && !scenario.reject && (signal != "logs" || scenario.logs)
				if (receipts["/otlp/v1/"+signal] > 0) != want {
					t.Errorf("%s receipt present=%t, want %t", signal, receipts["/otlp/v1/"+signal] > 0, want)
				}
			}
		})
	}
}

func TestAzureTelemetryHelper(t *testing.T) {
	if os.Getenv(azureTelemetryHelper) != "1" {
		return
	}
	shutdown, err := Setup(t.Context(), "synthetic")
	if err != nil {
		t.Fatal(err)
	}
	counter, err := otel.Meter("azure.synthetic").Int64Counter("azure.synthetic.requests")
	if err != nil {
		t.Fatal(err)
	}
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, span := Tracer().Start(r.Context(), "azure.synthetic.request")
		defer span.End()
		counter.Add(ctx, 1)
		logger.New("cao:server").Printf("synthetic request status=%d", http.StatusOK)
		w.WriteHeader(http.StatusOK)
	}))
	defer app.Close()
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, app.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := app.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if err := response.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatal("telemetry backend gated application availability")
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	err = shutdown(ctx)
	if (err != nil) != (os.Getenv("CAO_TEST_EXPORT_REJECTED") == "true") {
		t.Fatalf("unexpected exporter shutdown outcome: %v", err)
	}
}
