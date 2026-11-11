package tokenbroker

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	otelLog "go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/log/global"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/githubnext/gh-cao/server/internal/telemetry"
)

type logExporter struct {
	records []sdklog.Record
}

func (e *logExporter) Export(_ context.Context, records []sdklog.Record) error {
	for _, record := range records {
		e.records = append(e.records, record.Clone())
	}
	return nil
}

func (*logExporter) Shutdown(context.Context) error   { return nil }
func (*logExporter) ForceFlush(context.Context) error { return nil }

func TestBrokerTelemetryCorrelatesSafeOperationOutcomes(t *testing.T) {
	spans := tracetest.NewInMemoryExporter()
	tracer := sdktrace.NewTracerProvider(sdktrace.WithSyncer(spans))
	logs := &logExporter{}
	provider := sdklog.NewLoggerProvider(sdklog.WithProcessor(sdklog.NewSimpleProcessor(logs)))
	previousTracer, previousLogger := otel.GetTracerProvider(), global.GetLoggerProvider()
	otel.SetTracerProvider(tracer)
	global.SetLoggerProvider(provider)
	t.Cleanup(func() {
		_ = tracer.Shutdown(t.Context())
		_ = provider.Shutdown(t.Context())
		otel.SetTracerProvider(previousTracer)
		global.SetLoggerProvider(previousLogger)
	})
	for _, test := range []struct {
		name, body, outcome, lastOperation string
		status                             int
		prepare                            func(*fixture)
	}{
		{"issue", exchangeBody, "success", telemetry.SpanTokenBrokerIssue, 200, nil},
		{"identity denied", strings.Replace(exchangeBody, "valid", "PRIVATE_OIDC", 1), "denied", telemetry.SpanTokenBrokerIdentity, 401, nil},
		{"policy denied", exchangeBody, "denied", telemetry.SpanTokenBrokerPolicy, 403, func(f *fixture) {
			f.identity.WorkflowSHA = strings.Repeat("b", 40)
		}},
		{"identity unavailable", strings.Replace(exchangeBody, "valid", "unavailable", 1), "unavailable", telemetry.SpanTokenBrokerIdentity, 503, nil},
		{"issuer unavailable", exchangeBody, "unavailable", telemetry.SpanTokenBrokerIssue, 503, func(f *fixture) {
			f.service.keyReader = keyFunc(func(context.Context) ([]byte, error) {
				return nil, errors.New("PRIVATE_KEY_ERROR")
			})
		}},
		{"invalidate", invalidateBody, "success", telemetry.SpanTokenBrokerRevoke, 204, func(f *fixture) {
			if response := f.call(exchangeBody); response.Code != 200 {
				t.Fatal(response.Code)
			}
		}},
		{"cleanup unavailable", invalidateBody, "unavailable", telemetry.SpanTokenBrokerRevoke, 503, func(f *fixture) {
			if response := f.call(exchangeBody); response.Code != 200 {
				t.Fatal(response.Code)
			}
			f.revokeFails = true
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newFixture(t)
			if test.prepare != nil {
				test.prepare(f)
			}
			spans.Reset()
			logs.records = nil
			ctx, parent := telemetry.Tracer().Start(t.Context(), "test.request")
			defer parent.End()
			request := httptest.NewRequestWithContext(ctx, http.MethodPost, "/api/token-broker", strings.NewReader(test.body))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			f.service.ServeHTTP(response, request)
			if response.Code != test.status {
				t.Fatalf("status = %d, want %d", response.Code, test.status)
			}
			exported := spans.GetSpans()
			if len(logs.records) == 0 || len(logs.records) != len(exported) {
				t.Fatalf("missing operation logs or spans: logs=%d spans=%d", len(logs.records), len(exported))
			}
			for index, record := range logs.records {
				span := exported[index]
				if span.Parent.SpanID() != parent.SpanContext().SpanID() ||
					record.TraceID() != parent.SpanContext().TraceID() || record.SpanID() != span.SpanContext.SpanID() {
					t.Fatal("operation logs and spans lost request correlation")
				}
				if record.Body().AsString() != "token broker operation completed" {
					t.Fatal("operation log captured a non-allowlisted message")
				}
				attributes := map[string]string{}
				record.WalkAttributes(func(attribute attribute.KeyValue) bool {
					attributes[string(attribute.Key)] = attribute.Value.AsString()
					return true
				})
				if len(attributes) != 2 || attributes["operation"] != span.Name {
					t.Fatal("operation log contains unexpected dimensions")
				}
				expectedOutcome := "success"
				if index == len(logs.records)-1 {
					expectedOutcome = test.outcome
					if span.Name != test.lastOperation {
						t.Fatalf("last operation = %s, want %s", span.Name, test.lastOperation)
					}
				}
				if attributes["outcome"] != expectedOutcome {
					t.Fatalf("outcome = %s, want %s", attributes["outcome"], expectedOutcome)
				}
				expectedCode, expectedSeverity := codes.Unset, otelLog.SeverityInfo
				if expectedOutcome == "unavailable" {
					expectedCode, expectedSeverity = codes.Error, otelLog.SeverityError
				}
				if span.Status.Code != expectedCode || record.Severity() != expectedSeverity ||
					len(span.Attributes) != 0 || len(span.Events) != 0 ||
					(span.Status.Description != "" && span.Status.Description != "token broker dependency unavailable") {
					t.Fatal("operation telemetry exposed private state or an incorrect outcome")
				}
			}
		})
	}
}
