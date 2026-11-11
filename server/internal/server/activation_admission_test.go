package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/baggage"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/githubnext/gh-cao/server/internal/admission"
	"github.com/githubnext/gh-cao/server/internal/operational"
	"github.com/githubnext/gh-cao/server/internal/operational/memory"
	"github.com/githubnext/gh-cao/server/internal/telemetry"
)

type admissionTraceLimiter struct {
	operational.RequestLimiter
	parent       trace.SpanContext
	baggageCount int
}

func (l *admissionTraceLimiter) TakeRateLimitToken(ctx context.Context, key string, capacity int, window time.Duration) (operational.RateLimitResult, error) {
	l.parent = trace.SpanContextFromContext(ctx)
	l.baggageCount = baggage.FromContext(ctx).Len()
	ctx, span := telemetry.Tracer().Start(ctx, "admission.test.dependency")
	defer span.End()
	return l.RequestLimiter.TakeRateLimitToken(ctx, key, capacity, window)
}

func TestAdmissionClientTraceparentExportedThroughFullHandler(t *testing.T) {
	for _, path := range []string{"/api/admission", "/api/conclusion", tokenBrokerPath} {
		t.Run(path, func(t *testing.T) { testAdmissionClientTraceparent(t, path) })
	}
}

func testAdmissionClientTraceparent(t *testing.T, path string) {
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.AlwaysSample())))
	previous, previousPropagation := otel.GetTracerProvider(), otel.GetTextMapPropagator()
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
	defer func() {
		otel.SetTracerProvider(previous)
		otel.SetTextMapPropagator(previousPropagation)
		_ = provider.Shutdown(context.Background())
	}()
	store, err := memory.New(memory.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	services := store.OperationalServices()
	limiter := &admissionTraceLimiter{RequestLimiter: services.RequestLimiter}
	services.RequestLimiter = limiter
	verifier, err := admission.NewVerifier("https://admission.example/api/admission")
	if err != nil {
		t.Fatal(err)
	}
	app := &App{services: services,
		config: Config{HostProfile: hostedHostProfile(), Proxy: ProxyPolicy{
			AllowedHosts: []string{"admission.example"}, RequireHTTPS: true,
		}},
		admission: &admission.Service{Verifier: verifier},
	}
	if path == tokenBrokerPath {
		app.tokenBroker = newDeniedTokenBroker(t)
	}
	handler := app.Handler()
	const clientTrace = "4bf92f3577b34da6a3ce929d0e0e4736"
	const clientSpan = "00f067aa0ba902b7"
	for _, test := range []struct {
		name       string
		headers    []string
		correlated bool
	}{
		{"sampled client", []string{"00-" + clientTrace + "-" + clientSpan + "-01"}, true},
		{"unsampled client", []string{"00-" + clientTrace + "-" + clientSpan + "-00"}, true},
		{"retry client span", []string{"00-" + clientTrace + "-1234567890abcdef-01"}, true},
		{"missing", nil, false},
		{"malformed", []string{"not-a-traceparent"}, false},
		{"zero trace", []string{"00-" + strings.Repeat("0", 32) + "-" + clientSpan + "-01"}, false},
		{"oversized", []string{strings.Repeat("a", 8192)}, false},
		{"duplicate", []string{"00-" + clientTrace + "-" + clientSpan + "-01", "00-" + clientTrace + "-" + clientSpan + "-01"}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			exporter.Reset()
			inheritedTrace, _ := trace.TraceIDFromHex("11111111111111111111111111111111")
			inheritedSpan, _ := trace.SpanIDFromHex("2222222222222222")
			ctx := trace.ContextWithSpanContext(t.Context(), trace.NewSpanContext(trace.SpanContextConfig{
				TraceID: inheritedTrace, SpanID: inheritedSpan, TraceFlags: trace.FlagsSampled,
			}))
			member, err := baggage.NewMember("private", "must-not-export")
			if err != nil {
				t.Fatal(err)
			}
			bag, err := baggage.New(member)
			if err != nil {
				t.Fatal(err)
			}
			ctx = baggage.ContextWithBaggage(ctx, bag)
			body, status := "{}", http.StatusForbidden
			if path == tokenBrokerPath {
				body = `{"token_type":"copilot-request","provider":"github","oidc_token":"invalid"}`
				status = http.StatusUnauthorized
			}
			request := httptest.NewRequestWithContext(ctx, "POST", "https://admission.example"+path, strings.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Authorization", "Bearer invalid-synthetic-oidc")
			request.Header.Set("Tracestate", "private=must-not-export")
			request.Header.Set("Baggage", "private=must-not-export")
			for _, value := range test.headers {
				request.Header.Add("Traceparent", value)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != status || response.Body.Len() != 0 {
				t.Fatalf("tracing changed decision: %d %s", response.Code, response.Body)
			}
			serverSpan := findSpan(t, exporter.GetSpans(), "POST "+path)
			dependency := findSpan(t, exporter.GetSpans(), "admission.test.dependency")
			if serverSpan.SpanKind != trace.SpanKindServer || !serverSpan.SpanContext.IsValid() {
				t.Fatal("missing valid exported server span")
			}
			if test.correlated {
				if serverSpan.SpanContext.TraceID().String() != clientTrace ||
					serverSpan.Parent.SpanID().String() != strings.Split(test.headers[0], "-")[2] || !serverSpan.Parent.IsRemote() {
					t.Fatalf("client parent lost: span=%v parent=%v", serverSpan.SpanContext, serverSpan.Parent)
				}
			} else if serverSpan.Parent.IsValid() || serverSpan.SpanContext.TraceID().String() == clientTrace ||
				serverSpan.SpanContext.TraceID() == inheritedTrace {
				t.Fatal("invalid/missing context reused an untrusted parent")
			}
			if dependency.SpanContext.TraceID() != serverSpan.SpanContext.TraceID() ||
				dependency.Parent.SpanID() != serverSpan.SpanContext.SpanID() ||
				limiter.parent.SpanID() != serverSpan.SpanContext.SpanID() {
				t.Fatal("downstream dependency detached from admission trace")
			}
			if limiter.baggageCount != 0 || limiter.parent.TraceState().Len() != 0 {
				t.Fatal("private propagation metadata reached dependency")
			}
			if !serverSpan.SpanContext.IsSampled() {
				t.Fatal("remote sampling flag overrode local admission sampling")
			}
			for name := range response.Header() {
				if strings.Contains(strings.ToLower(name), "trace") || strings.Contains(strings.ToLower(name), "span") {
					t.Fatalf("response exposed trace identifier: %s", name)
				}
			}
		})
	}
}

func TestActivationAdmissionFullHandlerBoundary(t *testing.T) {
	for _, path := range []string{"/api/admission", "/api/conclusion"} {
		t.Run(path, func(t *testing.T) { testAdmissionFullHandlerBoundary(t, path) })
	}
}

func testAdmissionFullHandlerBoundary(t *testing.T, path string) {
	store, err := memory.New(memory.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	provider := sdktrace.NewTracerProvider()
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	defer func() {
		otel.SetTracerProvider(previous)
		_ = provider.Shutdown(context.Background())
	}()
	verifier, err := admission.NewVerifier("https://admission.example/api/admission")
	if err != nil {
		t.Fatal(err)
	}
	app := &App{
		services: store.OperationalServices(),
		config: Config{HostProfile: hostedHostProfile(), Proxy: ProxyPolicy{
			AllowedHosts: []string{"admission.example"}, RequireHTTPS: true,
		}},
		admission: &admission.Service{Verifier: verifier},
	}
	handler := app.Handler()
	for _, method := range []string{"GET", "HEAD", "POST", "PUT", "OPTIONS"} {
		r := httptest.NewRequestWithContext(t.Context(), method, "https://admission.example"+path, strings.NewReader(`{}`))
		r.Header.Set("Authorization", "Bearer ghs_not_oidc")
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
		r.Header.Set("Baggage", "secret=not-for-export")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != 403 || w.Body.Len() != 0 || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s bypassed uniform denial: %d %s", method, w.Code, w.Body)
		}
		for name := range w.Header() {
			lower := strings.ToLower(name)
			if strings.Contains(lower, "trace") || strings.Contains(lower, "span") ||
				strings.Contains(lower, "ratelimit") || lower == "retry-after" || lower == "location" {
				t.Fatalf("header leaked: %s", name)
			}
		}
	}
	for n := 0; n < 61; n++ {
		r := httptest.NewRequestWithContext(t.Context(), "POST", "https://admission.example"+path, nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if n == 60 && (w.Code != 429 || w.Body.Len() != 0 || w.Header().Get("Retry-After") != "") {
			t.Fatalf("probe limit leaked: %d %v", w.Code, w.Header())
		}
	}
	app.Drain()
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), "POST", "https://admission.example"+path, nil))
	if w.Code != 503 || w.Body.Len() != 0 {
		t.Fatalf("draining: %d %s", w.Code, w.Body)
	}
	app.admission = nil
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), "POST", "https://admission.example"+path, nil))
	if w.Code != 403 || w.Body.Len() != 0 {
		t.Fatalf("disabled service fell through authentication: %d %s", w.Code, w.Body)
	}
}

func TestAdmissionTimeoutResponseAndConfiguration(t *testing.T) {
	completed := httptest.NewRecorder()
	completion := &admissionResponse{ResponseWriter: completed, headers: make(http.Header)}
	completion.Header().Set("X-Trace-Id", "must-not-escape")
	completion.WriteHeader(http.StatusNoContent)
	if _, err := completion.Write([]byte("must-not-escape")); err != nil {
		t.Fatal(err)
	}
	if completed.Code != http.StatusNoContent || completed.Body.Len() != 0 || len(completed.Header()) != 0 {
		t.Fatal("completion acknowledgement exposed headers or body")
	}
	success := httptest.NewRecorder()
	approved := &admissionResponse{ResponseWriter: success, headers: make(http.Header)}
	approved.Header().Set("X-Trace-Id", "must-not-escape")
	approved.WriteHeader(http.StatusOK)
	if _, err := approved.Write([]byte(`{"max_ai_credits":1}`)); err != nil {
		t.Fatal(err)
	}
	if success.Body.String() != `{"max_ai_credits":1}` || success.Header().Get("Content-Type") != "application/json" ||
		success.Header().Get("X-Trace-Id") != "" {
		t.Fatalf("success wire changed: %s %v", success.Body, success.Header())
	}
	w := httptest.NewRecorder()
	response := &admissionResponse{ResponseWriter: w, headers: make(http.Header)}
	http.TimeoutHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(20 * time.Millisecond)
		w.Header().Set("X-Trace-Id", "must-not-escape")
		_, _ = w.Write([]byte(`{"max_ai_credits":1}`))
	}), time.Millisecond, "").ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), "POST", "/api/admission", nil))
	if w.Code != 503 || w.Body.Len() != 0 || w.Header().Get("X-Trace-Id") != "" {
		t.Fatalf("timeout: %d %s %v", w.Code, w.Body, w.Header())
	}
	for _, test := range []struct {
		file, audience string
		valid          bool
	}{
		{"", "", true}, {"snapshot.json", "", false}, {"", "audience", false}, {"snapshot.json", "audience", true},
	} {
		t.Setenv("CAO_ADMISSION_POLICY_FILE", test.file)
		t.Setenv("CAO_ADMISSION_AUDIENCE", test.audience)
		if _, err := admissionConfigFromEnv(); (err == nil) != test.valid {
			t.Fatalf("partial config accepted: %v", err)
		}
	}
	store, err := memory.New(memory.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	if _, err := newActivationAdmission(store, Config{Admission: &AdmissionConfig{Audience: "audience", PolicyFile: "snapshot.json"}}); err == nil {
		t.Fatal("volatile/local gate enabled")
	}
}
