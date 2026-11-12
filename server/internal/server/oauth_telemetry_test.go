package server

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/githubnext/gh-cao/server/internal/redisx"
)

func TestOAuthCallbackTelemetryExcludesCredentialsAndIdentifiers(t *testing.T) {
	previousTracer := otel.GetTracerProvider()
	previousMeter := otel.GetMeterProvider()
	previousPropagator := otel.GetTextMapPropagator()
	traces := tracetest.NewInMemoryExporter()
	traceProvider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(traces))
	reader := sdkmetric.NewManualReader()
	meterProvider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	otel.SetTracerProvider(traceProvider)
	otel.SetMeterProvider(meterProvider)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() {
		_ = meterProvider.Shutdown(t.Context())
		_ = traceProvider.Shutdown(t.Context())
		otel.SetTracerProvider(previousTracer)
		otel.SetMeterProvider(previousMeter)
		otel.SetTextMapPropagator(previousPropagator)
	})

	github := fakeGitHub(t, fakeGitHubOptions{membershipState: "active", accessExpiresIn: 3600})
	app := newAzureTestApp(t, github.URL)
	stateCookie, state := loginState(t, app)
	firstState, firstCookieValue := state, stateCookie.Value
	traces.Reset()

	callbackCode := "private-oauth-code"
	request := azureRequest(t, http.MethodGet, "/auth/callback?code="+callbackCode+"&state="+url.QueryEscape(state))
	request.Header.Set("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	request.Header.Set("Tracestate", "vendor=private-tracestate")
	request.Header.Set("Baggage", "account=private-baggage")
	request.Header.Set("User-Agent", "private-user-agent")
	request.Header.Set("X-Forwarded-For", "192.0.2.89")
	request.AddCookie(stateCookie)
	success := httptest.NewRecorder()
	app.Handler().ServeHTTP(success, request)
	if success.Code != http.StatusFound {
		t.Fatalf("success status = %d", success.Code)
	}
	if success.Header().Get("X-Trace-Id") != "4bf92f3577b34da6a3ce929d0e0e4736" ||
		success.Header().Get("X-Span-Id") == "" {
		t.Fatal("callback must return its W3C trace and span identifiers")
	}

	invalid := httptest.NewRecorder()
	app.Handler().ServeHTTP(invalid, azureRequest(t, http.MethodGet, "/auth/callback?code="+callbackCode+"&state=private-invalid-state"))
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid state status = %d", invalid.Code)
	}

	stateCookie, state = loginState(t, app)
	missing := httptest.NewRecorder()
	request = azureRequest(t, http.MethodGet, "/auth/callback?state="+url.QueryEscape(state))
	request.AddCookie(stateCookie)
	app.Handler().ServeHTTP(missing, request)
	if missing.Code != http.StatusBadRequest {
		t.Fatalf("missing code status = %d", missing.Code)
	}

	stateCookie, state = loginState(t, app)
	denied := httptest.NewRecorder()
	request = azureRequest(t, http.MethodGet, "/auth/callback?error=private-provider-message&state="+url.QueryEscape(state))
	request.Header.Set("User-Agent", "private-user-agent")
	request.AddCookie(stateCookie)
	app.Handler().ServeHTTP(denied, request)
	if denied.Code != http.StatusBadRequest {
		t.Fatalf("provider denial status = %d", denied.Code)
	}

	stateCookie, state = loginState(t, app)
	app.oauth.config.TokenURL = github.URL + "/unavailable"
	exchange := httptest.NewRecorder()
	request = azureRequest(t, http.MethodGet, "/auth/callback?code="+callbackCode+"&state="+url.QueryEscape(state))
	request.AddCookie(stateCookie)
	app.Handler().ServeHTTP(exchange, request)
	if exchange.Code != http.StatusUnauthorized {
		t.Fatalf("exchange failure status = %d", exchange.Code)
	}

	deniedGitHub := fakeGitHub(t, fakeGitHubOptions{membershipState: "pending", accessExpiresIn: 3600})
	app = newAzureTestApp(t, deniedGitHub.URL)
	stateCookie, state = loginState(t, app)
	unauthorized := httptest.NewRecorder()
	request = azureRequest(t, http.MethodGet, "/auth/callback?code="+callbackCode+"&state="+url.QueryEscape(state))
	request.AddCookie(stateCookie)
	app.Handler().ServeHTTP(unauthorized, request)
	if unauthorized.Code != http.StatusForbidden {
		t.Fatalf("authorization failure status = %d", unauthorized.Code)
	}
	if unauthorized.Header().Get("Content-Type") != "text/html; charset=utf-8" ||
		unauthorized.Header().Get("Cache-Control") != "no-store" ||
		unauthorized.Header().Get("Referrer-Policy") != "no-referrer" ||
		!strings.Contains(unauthorized.Header().Get("Content-Security-Policy"), "script-src 'sha256-") {
		t.Fatalf("callback failure missing safe response headers: %#v", unauthorized.Header())
	}
	if !strings.Contains(unauthorized.Body.String(), "account, organization, or team approved") ||
		!strings.Contains(unauthorized.Body.String(), "allowed users, organizations, and teams") ||
		!strings.Contains(unauthorized.Body.String(), "profile menu to add or switch") ||
		!strings.Contains(unauthorized.Body.String(), `href="`+deniedGitHub.URL+`/" target="_blank" rel="noreferrer noopener"`) ||
		!strings.Contains(unauthorized.Body.String(), "Request ID:") {
		t.Fatal("authorization failure must offer actionable, privacy-preserving help")
	}
	if strings.Contains(exchange.Body.String(), "check your membership") {
		t.Fatal("exchange failures must not claim an authorization or membership issue")
	}
	_, inlineScript, found := strings.Cut(unauthorized.Body.String(), "<script>")
	if !found {
		t.Fatal("OAuth recovery script is not inlined")
	}
	inlineScript, _, found = strings.Cut(inlineScript, "</script>")
	if !found {
		t.Fatal("OAuth recovery script is unterminated")
	}
	sum := sha256.Sum256([]byte(inlineScript))
	if !strings.Contains(unauthorized.Header().Get("Content-Security-Policy"),
		"'sha256-"+base64.StdEncoding.EncodeToString(sum[:])+"'") {
		t.Fatal("CSP must allow only the embedded recovery script")
	}
	_, inlineStyle, found := strings.Cut(unauthorized.Body.String(), "<style>")
	if !found {
		t.Fatal("OAuth recovery styles are not inlined")
	}
	inlineStyle, _, found = strings.Cut(inlineStyle, "</style>")
	if !found {
		t.Fatal("OAuth recovery styles are unterminated")
	}
	sum = sha256.Sum256([]byte(inlineStyle))
	csp := unauthorized.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "style-src 'sha256-"+base64.StdEncoding.EncodeToString(sum[:])+"'") ||
		!strings.Contains(unauthorized.Body.String(), `class="brand"`) ||
		!strings.Contains(unauthorized.Body.String(), `class="error-card"`) ||
		!strings.Contains(inlineStyle, "@media (prefers-color-scheme: dark)") {
		t.Fatal("OAuth recovery page must be branded, theme-aware, and allow only its embedded styles")
	}

	app = newAzureTestApp(t, github.URL)
	stateCookie, state = loginState(t, app)
	unavailable, err := redisx.New("redis://127.0.0.1:1")
	if err != nil {
		t.Fatal(err)
	}
	unavailableStore := redisx.NewStore(unavailable, "test")
	app.oauth.sessions, app.oauth.invalidator, app.oauth.revocations = unavailableStore, unavailableStore, unavailableStore
	failedSave := httptest.NewRecorder()
	request = azureRequest(t, http.MethodGet, "/auth/callback?code="+callbackCode+"&state="+url.QueryEscape(state))
	request.AddCookie(stateCookie)
	app.Handler().ServeHTTP(failedSave, request)
	if failedSave.Code != http.StatusServiceUnavailable {
		t.Fatalf("session save failure status = %d", failedSave.Code)
	}
	if !github.sawRevocation("access-old") || !github.sawRevocation("refresh-old") {
		t.Fatal("failed session persistence left issued credentials active")
	}

	var spans tracetest.SpanStubs
	for _, span := range traces.GetSpans() {
		if span.Name == "GET /auth/callback" {
			spans = append(spans, span)
		}
	}
	if len(spans) != 7 {
		t.Fatalf("callback produced %d spans, want exactly one per callback", len(spans))
	}
	for _, span := range spans {
		if span.Name != "GET /auth/callback" || span.SpanKind != trace.SpanKindServer {
			t.Fatalf("unexpected callback span: %s (%v)", span.Name, span.SpanKind)
		}
		for _, event := range span.Events {
			if event.Name != "cao_dashboard.auth.decision" {
				t.Fatalf("callback span recorded unexpected event %q", event.Name)
			}
			attrs := spanAttributes(event.Attributes)
			if attrs["cao_dashboard.auth.operation"] != "callback_cleanup" ||
				attrs["cao_dashboard.auth.outcome"] != "revoked" {
				t.Fatalf("callback cleanup event has unexpected attributes: %#v", attrs)
			}
		}
	}
	if spans[6].Status.Code != codes.Error {
		t.Fatal("session persistence failure must mark the callback span as a server error")
	}
	for i, span := range spans {
		want := 0
		if i == 5 || i == 6 {
			want = 1
		}
		if len(span.Events) != want {
			t.Fatalf("callback span %d recorded %d cleanup decisions, want %d", i, len(span.Events), want)
		}
	}
	for i, response := range []*httptest.ResponseRecorder{invalid, missing, denied, exchange, unauthorized, failedSave} {
		span := spans[i+1]
		body := response.Body.String()
		if !strings.Contains(body, "We couldn’t complete your sign-in") ||
			!strings.Contains(body, `<button type="button" id="sign-out">`) ||
			!strings.Contains(body, "#oauth-sign-in-troubleshooting") ||
			!strings.Contains(body, span.SpanContext.TraceID().String()) ||
			response.Header().Get("X-Trace-Id") != span.SpanContext.TraceID().String() ||
			response.Header().Get("X-Span-Id") != span.SpanContext.SpanID().String() {
			t.Fatalf("callback error %d missing help or matching trace ID", i)
		}
		for _, private := range []string{callbackCode, firstState, firstCookieValue, "private-invalid-state",
			"private-provider-message", "private-user-agent", "access-old", "refresh-old", "octocat"} {
			if strings.Contains(body, private) {
				t.Fatalf("callback error %d exposed private value %q", i, private)
			}
		}
	}
	if spans[0].Parent.TraceID().String() != "4bf92f3577b34da6a3ce929d0e0e4736" ||
		!spans[0].Parent.IsRemote() {
		t.Fatal("callback did not preserve the remote W3C parent")
	}

	for i, expected := range []struct{ outcome, errorType string }{
		{"success", ""}, {"failure", "invalid_state"}, {"failure", "missing_code"},
		{"failure", "provider_denied"}, {"failure", "exchange_failed"}, {"failure", "authorization_failed"},
		{"failure", "session_save_failed"},
	} {
		if i < 6 && spans[i].Status.Code != codes.Unset {
			t.Fatalf("client outcome %d must not be marked as a server error", i)
		}

		attrs := spanAttributes(spans[i].Attributes)
		if attrs["http.route"] != "/auth/callback" || attrs["http.request.method"] != "GET" ||
			attrs["cao_dashboard.auth.callback.outcome"] != expected.outcome {
			t.Fatalf("unexpected callback attributes: %#v", attrs)
		}
		if expected.errorType == "" {
			if _, ok := attrs["error.type"]; ok {
				t.Fatal("successful callback has an error.type")
			}
		} else if attrs["error.type"] != expected.errorType {
			t.Fatalf("error type = %#v, want %s", attrs["error.type"], expected.errorType)
		}
	}

	var data metricdata.ResourceMetrics
	if err := reader.Collect(t.Context(), &data); err != nil {
		t.Fatal(err)
	}
	var outcomes []string
	var cleanupDecisions int64
	for _, scope := range data.ScopeMetrics {
		for _, instrument := range scope.Metrics {
			if instrument.Name == "cao_dashboard.auth.decision.count" {
				for _, point := range instrument.Data.(metricdata.Sum[int64]).DataPoints {
					attrs := spanAttributes(point.Attributes.ToSlice())
					if attrs["cao_dashboard.auth.operation"] != "callback_cleanup" ||
						attrs["cao_dashboard.auth.outcome"] != "revoked" {
						t.Fatalf("unexpected cleanup decision: %#v", attrs)
					}
					cleanupDecisions += point.Value
				}
			}
			if instrument.Name != "cao_dashboard.auth.callback.count" {
				continue
			}
			for _, point := range instrument.Data.(metricdata.Sum[int64]).DataPoints {
				attrs := spanAttributes(point.Attributes.ToSlice())
				if point.Value != 1 {
					t.Fatalf("callback count = %d, want 1", point.Value)
				}
				outcome := attrs["cao_dashboard.auth.callback.outcome"].(string) + "/"
				if errorType, ok := attrs["error.type"].(string); ok {
					outcome += errorType
				}
				outcomes = append(outcomes, outcome)
			}
		}
	}
	for _, expected := range []string{"success/", "failure/invalid_state", "failure/missing_code",
		"failure/provider_denied", "failure/exchange_failed", "failure/authorization_failed",
		"failure/session_save_failed"} {
		found := false
		for _, outcome := range outcomes {
			found = found || outcome == expected
		}
		if !found {
			t.Fatalf("missing metric outcome %q in %v", expected, outcomes)
		}
	}
	if len(outcomes) != 7 {
		t.Fatalf("unexpected metric outcomes: %v", outcomes)
	}
	if cleanupDecisions != 2 {
		t.Fatalf("callback cleanup decisions = %d, want 2", cleanupDecisions)
	}

	encoded, err := json.Marshal(struct {
		Spans   tracetest.SpanStubs
		Metrics metricdata.ResourceMetrics
	}{spans, data})
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{callbackCode, firstState, firstCookieValue, state, stateCookie.Value,
		"private-invalid-state", "private-provider-message",
		"private-user-agent", "private-tracestate", "private-baggage", "192.0.2.89", "access-old", "refresh-old", "test-user"} {
		if strings.Contains(string(encoded), private) {
			t.Fatalf("OAuth telemetry exposed private value %q", private)
		}
	}
}

func TestOAuthRevalidationTelemetryExcludesIdentity(t *testing.T) {
	previousTracer := otel.GetTracerProvider()
	previousMeter := otel.GetMeterProvider()
	traces := tracetest.NewInMemoryExporter()
	traceProvider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(traces))
	reader := sdkmetric.NewManualReader()
	meterProvider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	otel.SetTracerProvider(traceProvider)
	otel.SetMeterProvider(meterProvider)
	t.Cleanup(func() {
		_ = meterProvider.Shutdown(t.Context())
		_ = traceProvider.Shutdown(t.Context())
		otel.SetTracerProvider(previousTracer)
		otel.SetMeterProvider(previousMeter)
	})

	github := fakeGitHub(t, fakeGitHubOptions{membershipState: "active", accessExpiresIn: 3600})
	app := newAzureTestApp(t, github.URL)
	cookie, _ := callbackSession(t, app)
	traces.Reset()
	for _, outcome := range []string{"accepted", "rejected"} {
		session, err := app.oauth.loadSession(t.Context(), cookie.Value)
		if err != nil {
			t.Fatal(err)
		}
		session.AuthorizedAt = time.Now().Add(-authorizationRecheckInterval)
		if err := app.oauth.saveSession(t.Context(), session); err != nil {
			t.Fatal(err)
		}
		if outcome == "rejected" {
			github.membershipState = "inactive"
		}
		request := azureRequest(t, http.MethodGet, "/api/auth/session")
		request.Header.Set("Traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
		request.Header.Set("Tracestate", "vendor=private-tracestate")
		request.Header.Set("Baggage", "account=private-baggage")
		request.AddCookie(cookie)
		response := httptest.NewRecorder()
		app.Handler().ServeHTTP(response, request)
		wantStatus := http.StatusOK
		if outcome == "rejected" {
			wantStatus = http.StatusUnauthorized
		}
		if response.Code != wantStatus {
			t.Fatalf("revalidation %s returned %d", outcome, response.Code)
		}
	}

	var decisions []string
	for _, span := range traces.GetSpans() {
		for _, event := range span.Events {
			if event.Name != "cao_dashboard.auth.decision" {
				t.Fatalf("unexpected revalidation event %q", event.Name)
			}
			attrs := spanAttributes(event.Attributes)
			if attrs["cao_dashboard.auth.operation"] != "session_revalidation" {
				t.Fatalf("unexpected operation in revalidation event: %#v", attrs)
			}
			decisions = append(decisions, attrs["cao_dashboard.auth.outcome"].(string))
		}
	}
	if len(decisions) != 2 || decisions[0] != "accepted" || decisions[1] != "rejected" {
		t.Fatalf("unexpected revalidation decisions: %v", decisions)
	}
	var metrics metricdata.ResourceMetrics
	if err := reader.Collect(t.Context(), &metrics); err != nil {
		t.Fatal(err)
	}
	seen := map[string]int64{}
	for _, scope := range metrics.ScopeMetrics {
		for _, instrument := range scope.Metrics {
			if instrument.Name != "cao_dashboard.auth.decision.count" {
				continue
			}
			for _, point := range instrument.Data.(metricdata.Sum[int64]).DataPoints {
				attrs := spanAttributes(point.Attributes.ToSlice())
				if attrs["cao_dashboard.auth.operation"] == "session_revalidation" {
					seen[attrs["cao_dashboard.auth.outcome"].(string)] += point.Value
				}
			}
		}
	}
	if seen["accepted"] != 1 || seen["rejected"] != 1 || len(seen) != 2 {
		t.Fatalf("unexpected revalidation metrics: %v", seen)
	}
	encoded, err := json.Marshal(struct {
		Spans   tracetest.SpanStubs
		Metrics metricdata.ResourceMetrics
	}{traces.GetSpans(), metrics})
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{cookie.Value, "octocat", "access-old", "refresh-old", "private-tracestate", "private-baggage", github.URL} {
		if strings.Contains(string(encoded), private) {
			t.Fatalf("revalidation telemetry contains private value %q", private)
		}
	}
}

func TestOAuthCallbackQueuedRevocationTelemetry(t *testing.T) {
	previousTracer := otel.GetTracerProvider()
	previousMeter := otel.GetMeterProvider()
	traces := tracetest.NewInMemoryExporter()
	traceProvider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(traces))
	reader := sdkmetric.NewManualReader()
	meterProvider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	otel.SetTracerProvider(traceProvider)
	otel.SetMeterProvider(meterProvider)
	t.Cleanup(func() {
		_ = meterProvider.Shutdown(t.Context())
		_ = traceProvider.Shutdown(t.Context())
		otel.SetTracerProvider(previousTracer)
		otel.SetMeterProvider(previousMeter)
	})

	github := fakeGitHub(t, fakeGitHubOptions{membershipState: "inactive", rejectRevocation: true})
	app := newAzureTestApp(t, github.URL)
	stateCookie, state := loginState(t, app)
	request := azureRequest(t, http.MethodGet, "/auth/callback?code=code-1&state="+url.QueryEscape(state))
	request.AddCookie(stateCookie)
	response := httptest.NewRecorder()
	app.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("denied callback returned %d", response.Code)
	}
	var metrics metricdata.ResourceMetrics
	if err := reader.Collect(t.Context(), &metrics); err != nil {
		t.Fatal(err)
	}
	queued := int64(0)
	for _, scope := range metrics.ScopeMetrics {
		for _, instrument := range scope.Metrics {
			if instrument.Name != "cao_dashboard.auth.decision.count" {
				continue
			}
			for _, point := range instrument.Data.(metricdata.Sum[int64]).DataPoints {
				attrs := spanAttributes(point.Attributes.ToSlice())
				if attrs["cao_dashboard.auth.operation"] == "callback_cleanup" &&
					attrs["cao_dashboard.auth.outcome"] == "queued" {
					queued += point.Value
				}
			}
		}
	}
	if queued != 1 {
		t.Fatalf("callback cleanup queued decisions = %d, want 1", queued)
	}
	encoded, err := json.Marshal(struct {
		Spans   tracetest.SpanStubs
		Metrics metricdata.ResourceMetrics
	}{traces.GetSpans(), metrics})
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{state, stateCookie.Value, "access-old", "refresh-old", "octocat"} {
		if strings.Contains(string(encoded), private) {
			t.Fatalf("queued cleanup telemetry contains private value %q", private)
		}
	}
}

func TestOAuthRecoveryUsesExistingProtectedLogout(t *testing.T) {
	github := fakeGitHub(t, fakeGitHubOptions{membershipState: "active", accessExpiresIn: 3600})
	app := newAzureTestApp(t, github.URL)
	sessionCookie, csrfCookie := callbackSession(t, app)

	page := httptest.NewRecorder()
	app.Handler().ServeHTTP(page, azureRequest(t, http.MethodGet, "/auth/callback?state=invalid"))
	if page.Code != http.StatusBadRequest || page.Header().Get("Cache-Control") != "no-store" ||
		!strings.Contains(page.Body.String(), "<script>") ||
		!strings.Contains(page.Body.String(), "fetch('/auth/logout'") ||
		!strings.Contains(page.Body.String(), "'X-CSRF-Token'") ||
		!strings.Contains(page.Body.String(), "response.status === 204 || response.status === 401") {
		t.Fatal("embedded recovery page must use the existing CSRF-protected logout and handle missing sessions")
	}

	unprotected := httptest.NewRecorder()
	request := azureRequest(t, http.MethodPost, "/auth/logout")
	request.AddCookie(sessionCookie)
	app.Handler().ServeHTTP(unprotected, request)
	if unprotected.Code != http.StatusForbidden || github.sawRevocation("access-old") {
		t.Fatal("recovery must not bypass logout CSRF validation")
	}
	logout := httptest.NewRecorder()
	request = azureRequest(t, http.MethodPost, "/auth/logout")
	request.AddCookie(sessionCookie)
	request.AddCookie(csrfCookie)
	request.Header.Set("X-CSRF-Token", csrfCookie.Value)
	app.Handler().ServeHTTP(logout, request)
	if logout.Code != http.StatusNoContent || !github.sawRevocation("access-old") {
		t.Fatal("recovery must revoke the existing session before restarting login")
	}
	loggedOut := httptest.NewRecorder()
	app.Handler().ServeHTTP(loggedOut, azureRequest(t, http.MethodGet, "/auth/logged-out"))
	if loggedOut.Code != http.StatusOK || !strings.Contains(loggedOut.Body.String(), `href="/auth/login?select_account=1"`) {
		t.Fatal("recovery landing page must offer explicit sign-in with account selection")
	}
	_, remaining, found := strings.Cut(loggedOut.Body.String(), "<script>")
	if !found {
		t.Fatal("signed-out page must clear browser data")
	}
	script, _, found := strings.Cut(remaining, "</script>")
	if !found {
		t.Fatal("signed-out page script must be complete")
	}
	hash := sha256.Sum256([]byte(script))
	expectedCSP := "default-src 'none'; style-src 'sha256-" + oauthLoggedOutStyleHash + "'; script-src 'sha256-" + base64.StdEncoding.EncodeToString(hash[:]) + "'; base-uri 'none'; form-action 'none'"
	if loggedOut.Header().Get("Content-Security-Policy") != expectedCSP ||
		loggedOut.Header().Get("Cache-Control") != "no-store" ||
		!strings.Contains(loggedOut.Body.String(), `<section class="signed-out-card"`) ||
		!strings.Contains(script, "indexedDB.deleteDatabase('gh-aw-cao-dashboard-data')") ||
		!strings.Contains(loggedOut.Body.String(), `id="sign-in" href="/auth/login?select_account=1" hidden`) {
		t.Fatal("signed-out page must protect its cleanup script and gate sign-in on database deletion")
	}
	foundClearedState := false
	for _, cookie := range loggedOut.Result().Cookies() {
		foundClearedState = foundClearedState || cookie.Name == "cao_oauth_state" && cookie.MaxAge < 0
	}
	if !foundClearedState {
		t.Fatal("recovery landing page must clear stale OAuth state")
	}
}

func TestHTTPServerTelemetryExcludesClientIdentifiers(t *testing.T) {
	previous := otel.GetTracerProvider()
	previousMeter := otel.GetMeterProvider()
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	reader := sdkmetric.NewManualReader()
	meterProvider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	otel.SetTracerProvider(provider)
	otel.SetMeterProvider(meterProvider)
	t.Cleanup(func() {
		_ = provider.Shutdown(t.Context())
		_ = meterProvider.Shutdown(t.Context())
		otel.SetTracerProvider(previous)
		otel.SetMeterProvider(previousMeter)
	})

	app := newAzureTestApp(t, fakeGitHub(t, fakeGitHubOptions{membershipState: "active"}).URL)
	for _, input := range []struct{ method, path string }{
		{http.MethodGet, "/auth/login"},
		{http.MethodGet, "/auth/login/private-user-path"},
		{"PRIVATE-192.0.2.50", "/auth/login/private-user-path"},
	} {
		request := azureRequest(t, input.method, input.path)
		request.RemoteAddr = "192.0.2.50:1234"
		request.Header.Set("X-Forwarded-For", "198.51.100.90")
		request.Header.Set("User-Agent", "private-user-agent")
		request.Header.Set("Traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
		request.Header.Set("Tracestate", "vendor=private-tracestate")
		request.Header.Set("Baggage", "account=private-baggage")
		response := httptest.NewRecorder()
		app.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusFound && response.Code != http.StatusOK && response.Code != http.StatusMethodNotAllowed {
			t.Fatalf("request %s returned %d", input.path, response.Code)
		}
	}
	var spans tracetest.SpanStubs
	allSpans := exporter.GetSpans()
	for _, span := range allSpans {
		if span.SpanKind == trace.SpanKindServer {
			spans = append(spans, span)
		}
	}
	if len(spans) != 3 || spans[0].Name != "GET /auth/login" || spans[1].Name != "GET /*" || spans[2].Name != " /*" {
		t.Fatalf("unexpected HTTP spans: %#v", spans)
	}
	for i, span := range spans {
		attrs := spanAttributes(span.Attributes)
		_, routePresent := attrs["http.route"]
		if (i == 0 && attrs["http.route"] != "/auth/login") || (i != 0 && routePresent) {
			t.Fatalf("HTTP route must use a registered, fixed path only: %v", attrs)
		}
	}
	var metrics metricdata.ResourceMetrics
	if err := reader.Collect(t.Context(), &metrics); err != nil {
		t.Fatal(err)
	}
	var routedRequests uint64
	for _, scope := range metrics.ScopeMetrics {
		for _, recorded := range scope.Metrics {
			if recorded.Name != "http.server.request.duration" {
				continue
			}
			for _, point := range recorded.Data.(metricdata.Histogram[float64]).DataPoints {
				if route, present := point.Attributes.Value(attribute.Key("http.route")); present {
					if route.AsString() != "/auth/login" {
						t.Fatalf("unexpected metric route: %s", route.AsString())
					}
					routedRequests += point.Count
				}
			}
		}
	}
	if routedRequests != 1 {
		t.Fatalf("route-labeled HTTP duration samples = %d, want 1", routedRequests)
	}
	encoded, err := json.Marshal(struct {
		Spans   tracetest.SpanStubs
		Metrics metricdata.ResourceMetrics
	}{allSpans, metrics})
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"192.0.2.50", "198.51.100.90", "private-user-agent", "private-user-path", "private-tracestate", "private-baggage"} {
		if strings.Contains(string(encoded), private) {
			t.Fatalf("HTTP telemetry exposed client data %q", private)
		}
	}
}
