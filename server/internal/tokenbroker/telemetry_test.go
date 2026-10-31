package tokenbroker

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/baggage"
	"go.opentelemetry.io/otel/codes"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

type telemetryCapture struct {
	exporter *tracetest.InMemoryExporter
	reader   *sdkmetric.ManualReader
}

func captureTelemetry(t *testing.T) telemetryCapture {
	t.Helper()
	previousTrace, previousMeter := otel.GetTracerProvider(), otel.GetMeterProvider()
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	reader := sdkmetric.NewManualReader()
	meter := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	otel.SetTracerProvider(provider)
	otel.SetMeterProvider(meter)
	t.Cleanup(func() {
		_ = provider.Shutdown(context.Background())
		_ = meter.Shutdown(context.Background())
		otel.SetTracerProvider(previousTrace)
		otel.SetMeterProvider(previousMeter)
	})
	return telemetryCapture{exporter: exporter, reader: reader}
}

func telemetryKey(name string, op operation, result outcome) string {
	return name + "/" + string(op) + "/" + string(result)
}

// Strictly allowlist every exported dimension instead of checking a handful of
// secrets: identities, digests, arbitrary errors and request fields all fail.
func checkTelemetryAttributes(t *testing.T, attrs []attribute.KeyValue) (operation, outcome) {
	t.Helper()
	if len(attrs) != 2 {
		t.Fatalf("unexpected telemetry attributes: %v", attrs)
	}
	var op operation
	var result outcome
	for _, attr := range attrs {
		switch attr.Key {
		case attributeOperation:
			op = operation(attr.Value.AsString())
			switch op {
			case operationExchange, operationInvalidate, operationIssue, operationCleanup, operationShutdown:
			default:
				t.Fatalf("unbounded operation: %q", op)
			}
		case attributeOutcome:
			result = outcome(attr.Value.AsString())
			switch result {
			case outcomeSuccess, outcomeInvalidRequest, outcomeUnauthenticated, outcomeDenied,
				outcomeUnsupported, outcomeThrottled, outcomeUnavailable, outcomeDeliveryFailed:
			default:
				t.Fatalf("unbounded outcome: %q", result)
			}
		default:
			t.Fatalf("unexpected attribute: %s", attr.Key)
		}
		if attr.Value.Type() != attribute.STRING {
			t.Fatalf("unexpected attribute type: %s", attr.Key)
		}
	}
	if op == "" || result == "" {
		t.Fatal("missing operation or outcome")
	}
	return op, result
}

func (c telemetryCapture) check(t *testing.T, want map[string]int64) {
	t.Helper()
	counts := map[string]int64{}
	durations := map[string]uint64{}
	var data metricdata.ResourceMetrics
	if err := c.reader.Collect(t.Context(), &data); err != nil {
		t.Fatal(err)
	}
	for _, scope := range data.ScopeMetrics {
		if scope.Scope.Name != instrumentationName || len(scope.Scope.Attributes.ToSlice()) != 0 {
			t.Fatalf("unexpected metric scope: %+v", scope.Scope)
		}
		for _, m := range scope.Metrics {
			switch m.Name {
			case metricExchangeCount, metricTokenCount, metricShutdownCount:
				sum, ok := m.Data.(metricdata.Sum[int64])
				if !ok || !sum.IsMonotonic {
					t.Fatalf("unexpected counter data: %s", m.Name)
				}
				for _, point := range sum.DataPoints {
					op, result := checkTelemetryAttributes(t, point.Attributes.ToSlice())
					counts[telemetryKey(m.Name, op, result)] += point.Value
				}
			case metricExchangeDuration, metricTokenDuration, metricShutdownDuration:
				histogram, ok := m.Data.(metricdata.Histogram[float64])
				if !ok || m.Unit != "s" {
					t.Fatalf("unexpected duration data: %s", m.Name)
				}
				for _, point := range histogram.DataPoints {
					op, result := checkTelemetryAttributes(t, point.Attributes.ToSlice())
					if point.Sum < 0 || point.Count == 0 {
						t.Fatal("invalid measured duration")
					}
					durations[telemetryKey(strings.TrimSuffix(m.Name, ".duration")+".count", op, result)] += point.Count
				}
			default:
				t.Fatalf("unexpected metric: %s", m.Name)
			}
		}
	}
	if len(counts) != len(want) {
		t.Fatalf("counts = %v, want %v", counts, want)
	}
	for key, count := range want {
		if count < 0 {
			t.Fatal("negative expected count")
			return
		}
		if counts[key] != count || durations[key] != uint64(count) {
			t.Fatalf("%s: count=%d duration count=%d, want %d", key, counts[key], durations[key], count)
		}
	}
	spans := map[string]int64{}
	for _, span := range c.exporter.GetSpans() {
		if span.InstrumentationScope.Name != instrumentationName ||
			len(span.InstrumentationScope.Attributes.ToSlice()) != 0 ||
			len(span.Events) != 0 || len(span.Links) != 0 ||
			span.SpanContext.TraceState().Len() != 0 || span.Parent.TraceState().Len() != 0 {
			t.Fatalf("unexpected exported span metadata: %s", span.Name)
		}
		op, result := checkTelemetryAttributes(t, span.Attributes)
		countName := ""
		switch span.Name {
		case spanExchange:
			countName = metricExchangeCount
		case spanIssue, spanRevoke:
			countName = metricTokenCount
		case spanShutdown:
			countName = metricShutdownCount
		default:
			t.Fatalf("unexpected span name: %s", span.Name)
		}
		isError := result == outcomeUnavailable || result == outcomeDeliveryFailed
		if isError && (span.Status.Code != codes.Error || span.Status.Description != "failed") ||
			!isError && (span.Status.Code != codes.Unset || span.Status.Description != "") {
			t.Fatalf("unexpected status for %s/%s: %+v", span.Name, result, span.Status)
		}
		spans[telemetryKey(countName, op, result)]++
	}
	for key, count := range want {
		if spans[key] != count {
			t.Fatalf("%s: spans=%d, want %d", key, spans[key], count)
		}
	}
}

func TestExchangeTelemetryOutcomes(t *testing.T) {
	tests := []struct {
		name   string
		body   string
		status int
		result outcome
		setup  func(*fixture)
	}{
		{name: "invalid", body: `{"oidc_token":"private-input"}`, status: 400, result: outcomeInvalidRequest},
		{name: "unauthenticated", body: strings.ReplaceAll(exchangeBody, "valid", "private-oidc"), status: 401, result: outcomeUnauthenticated},
		{name: "identity-unavailable", body: strings.ReplaceAll(exchangeBody, "valid", "unavailable"), status: 503, result: outcomeUnavailable},
		{name: "denied", body: exchangeBody, status: 403, result: outcomeDenied, setup: func(f *fixture) { f.identity.WorkflowSHA = strings.Repeat("b", 40) }},
		{name: "unsupported", body: `{"token_type":"copilot-task","oidc_token":"valid"}`, status: 501, result: outcomeUnsupported},
		{name: "provider", body: strings.ReplaceAll(exchangeBody, "github", "private-provider"), status: 403, result: outcomeDenied},
		{name: "closed", body: exchangeBody, status: 503, result: outcomeUnavailable, setup: func(f *fixture) { f.service.closed = true }},
		{name: "throttled", body: exchangeBody, status: 429, result: outcomeThrottled, setup: func(f *fixture) {
			f.service.tenantWindow, f.service.tenantMints = f.now, 60
		}},
		{name: "gate-canceled", body: exchangeBody, status: 503, result: outcomeUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			capture := captureTelemetry(t)
			f := newFixture(t)
			if tt.setup != nil {
				tt.setup(f)
			}
			ctx := t.Context()
			if tt.name == "gate-canceled" {
				f.service.gate <- struct{}{}
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			r := httptest.NewRequestWithContext(ctx, http.MethodPost, "/api/token-broker", strings.NewReader(tt.body))
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			f.service.ServeHTTP(w, r)
			if w.Code != tt.status || f.mints != 0 || f.keyReads != 0 {
				t.Fatalf("status=%d mints=%d key reads=%d", w.Code, f.mints, f.keyReads)
			}
			capture.check(t, map[string]int64{telemetryKey(metricExchangeCount, operationExchange, tt.result): 1})
		})
	}
}

func TestTokenTelemetryPerTokenRevocationAndRetry(t *testing.T) {
	capture := captureTelemetry(t)
	f := newFixture(t)
	for range 2 {
		if w := f.call(exchangeBody); w.Code != 200 {
			t.Fatal(w.Code)
		}
	}
	f.revokeFails = true
	if w := f.call(invalidateBody); w.Code != 503 || len(f.service.contexts[executionKey(f.identity)].tokens) != 2 {
		t.Fatal("failed invalidation did not retain both credentials")
	}
	f.revokeFails = false
	if w := f.call(invalidateBody); w.Code != 204 || len(f.revoked) != 2 {
		t.Fatal("invalidation retry did not revoke both credentials")
	}
	if w := f.call(invalidateBody); w.Code != 204 || len(f.revoked) != 2 {
		t.Fatal("invalidation not idempotent")
	}
	if err := f.service.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	capture.check(t, map[string]int64{
		telemetryKey(metricExchangeCount, operationExchange, outcomeSuccess):       2,
		telemetryKey(metricExchangeCount, operationInvalidate, outcomeUnavailable): 1,
		telemetryKey(metricExchangeCount, operationInvalidate, outcomeSuccess):     2,
		telemetryKey(metricTokenCount, operationIssue, outcomeSuccess):             2,
		telemetryKey(metricTokenCount, operationInvalidate, outcomeUnavailable):    2,
		telemetryKey(metricTokenCount, operationInvalidate, outcomeSuccess):        2,
		telemetryKey(metricShutdownCount, operationShutdown, outcomeSuccess):       1,
	})
}

func TestTokenTelemetryFailedIssuanceCleanupAndShutdown(t *testing.T) {
	capture := captureTelemetry(t)
	f := newFixture(t)
	f.result = func() string {
		return fmt.Sprintf(`{"token":"private-returned-token","expires_at":%q,"permissions":{"copilot_requests":"write"}}`, f.now.Add(50*time.Minute).Format(time.RFC3339))
	}
	f.revokeFails = true
	if w := f.call(exchangeBody); w.Code != 503 || len(f.service.contexts[executionKey(f.identity)].tokens) != 1 {
		t.Fatal("failed issuance cleanup lost the credential")
	}
	if err := f.service.Close(t.Context()); err == nil {
		t.Fatal("failed shutdown reported success")
	}
	f.revokeFails = false
	if err := f.service.Close(t.Context()); err != nil || len(f.revoked) != 1 || f.revoked[0] != "private-returned-token" {
		t.Fatal("shutdown retry lost the credential")
	}
	capture.check(t, map[string]int64{
		telemetryKey(metricExchangeCount, operationExchange, outcomeUnavailable): 1,
		telemetryKey(metricTokenCount, operationIssue, outcomeUnavailable):       1,
		telemetryKey(metricTokenCount, operationCleanup, outcomeUnavailable):     1,
		telemetryKey(metricTokenCount, operationShutdown, outcomeUnavailable):    1,
		telemetryKey(metricTokenCount, operationShutdown, outcomeSuccess):        1,
		telemetryKey(metricShutdownCount, operationShutdown, outcomeUnavailable): 1,
		telemetryKey(metricShutdownCount, operationShutdown, outcomeSuccess):     1,
	})
}

func TestTokenTelemetryPrivacyAndDeliveryFailure(t *testing.T) {
	capture := captureTelemetry(t)
	f := newFixture(t)
	f.service.keyReader = keyFunc(func(ctx context.Context) ([]byte, error) {
		if baggage.FromContext(ctx).Len() != 0 || trace.SpanContextFromContext(ctx).TraceState().Len() != 0 {
			t.Fatal("inherited metadata reached issuer")
		}
		return nil, errors.New("private-key-error https://private.example/private-path")
	})
	member, err := baggage.NewMember("private-identity", "private-baggage")
	if err != nil {
		t.Fatal(err)
	}
	bag, err := baggage.New(member)
	if err != nil {
		t.Fatal(err)
	}
	state, err := trace.ParseTraceState("private=private-state")
	if err != nil {
		t.Fatal(err)
	}
	parent := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: trace.TraceID{1}, SpanID: trace.SpanID{2}, TraceState: state,
		TraceFlags: trace.FlagsSampled, Remote: true,
	})
	ctx := baggage.ContextWithBaggage(trace.ContextWithRemoteSpanContext(t.Context(), parent), bag)
	r := httptest.NewRequestWithContext(ctx, http.MethodPost, "/api/token-broker", strings.NewReader(exchangeBody))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "******")
	w := httptest.NewRecorder()
	f.service.ServeHTTP(w, r)
	if w.Code != 503 || f.mints != 0 {
		t.Fatal("key failure did not fail closed")
	}
	// A separate service still exports through the same provider and retains a
	// token on a partial delivery exactly as the uninstrumented broker does.
	g := newFixture(t)
	r = httptest.NewRequestWithContext(ctx, http.MethodPost, "/api/token-broker", strings.NewReader(exchangeBody))
	r.Header.Set("Content-Type", "application/json")
	g.service.ServeHTTP(failingTelemetryWriter{httptest.NewRecorder()}, r)
	if len(g.service.contexts[executionKey(g.identity)].tokens) != 1 {
		t.Fatal("delivery failure lost the credential")
	}
	if err := g.service.Close(ctx); err != nil || len(g.revoked) != 1 {
		t.Fatal("delivery failure credential could not be revoked")
	}
	capture.check(t, map[string]int64{
		telemetryKey(metricExchangeCount, operationExchange, outcomeUnavailable):    1,
		telemetryKey(metricExchangeCount, operationExchange, outcomeDeliveryFailed): 1,
		telemetryKey(metricTokenCount, operationIssue, outcomeUnavailable):          1,
		telemetryKey(metricTokenCount, operationIssue, outcomeSuccess):              1,
		telemetryKey(metricTokenCount, operationShutdown, outcomeSuccess):           1,
		telemetryKey(metricShutdownCount, operationShutdown, outcomeSuccess):        1,
	})
	for _, span := range capture.exporter.GetSpans() {
		if span.SpanContext.TraceID() != parent.TraceID() {
			t.Fatal("trace correlation was not retained")
		}
	}
}

type failingTelemetryWriter struct{ *httptest.ResponseRecorder }

func (failingTelemetryWriter) Write([]byte) (int, error) {
	return 0, errors.New("private-response-write-error")
}

func TestTokenTelemetryShutdownGateFailure(t *testing.T) {
	capture := captureTelemetry(t)
	f := newFixture(t)
	f.service.gate <- struct{}{}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := f.service.Close(ctx); err == nil || f.service.closed {
		t.Fatal("shutdown gate failure changed service state")
	}
	capture.check(t, map[string]int64{
		telemetryKey(metricShutdownCount, operationShutdown, outcomeUnavailable): 1,
	})
}

func TestTokenTelemetryEncryptedCredentialCleanupFailure(t *testing.T) {
	capture := captureTelemetry(t)
	f := newFixture(t)
	if w := f.call(exchangeBody); w.Code != 200 {
		t.Fatal(w.Code)
	}
	held := f.service.contexts[executionKey(f.identity)]
	held.tokens[0].sealed[len(held.tokens[0].sealed)-1] ^= 1
	if w := f.call(invalidateBody); w.Code != 503 || len(held.tokens) != 1 || len(f.revoked) != 0 {
		t.Fatal("failed credential authentication changed cleanup behavior")
	}
	if err := f.service.Close(t.Context()); err == nil || len(held.tokens) != 1 {
		t.Fatal("shutdown discarded a credential after local cleanup failure")
	}
	capture.check(t, map[string]int64{
		telemetryKey(metricExchangeCount, operationExchange, outcomeSuccess):       1,
		telemetryKey(metricExchangeCount, operationInvalidate, outcomeUnavailable): 1,
		telemetryKey(metricTokenCount, operationIssue, outcomeSuccess):             1,
		telemetryKey(metricTokenCount, operationInvalidate, outcomeUnavailable):    1,
		telemetryKey(metricTokenCount, operationShutdown, outcomeUnavailable):      1,
		telemetryKey(metricShutdownCount, operationShutdown, outcomeUnavailable):   1,
	})
}

func TestTokenTelemetryConcurrentRequests(t *testing.T) {
	capture := captureTelemetry(t)
	f := newFixture(t)
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			if w := f.call(exchangeBody); w.Code != 200 {
				t.Errorf("exchange status: %d", w.Code)
			}
		})
	}
	wg.Wait()
	if err := f.service.Close(t.Context()); err != nil || len(f.revoked) != 4 {
		t.Fatal("concurrent issuance or shutdown failed")
	}
	capture.check(t, map[string]int64{
		telemetryKey(metricExchangeCount, operationExchange, outcomeSuccess): 4,
		telemetryKey(metricTokenCount, operationIssue, outcomeSuccess):       4,
		telemetryKey(metricTokenCount, operationShutdown, outcomeSuccess):    4,
		telemetryKey(metricShutdownCount, operationShutdown, outcomeSuccess): 1,
	})
}
