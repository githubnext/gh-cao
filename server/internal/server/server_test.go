package server

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/model"
	"github.com/githubnext/gh-cao/server/internal/query"
	"github.com/githubnext/gh-cao/server/internal/telemetry"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

const testAccessToken = "0123456789abcdef0123456789abcdef"

func TestQueryPlanLimitErrorResponse(t *testing.T) {
	response := httptest.NewRecorder()
	writeQueryError(t.Context(), response, http.StatusBadRequest, &query.PlanLimitError{
		QueryID: "campaign-repository-coverage", Boundary: "retained_bytes",
	})
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", response.Code)
	}
	var payload map[string]string
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["code"] != "query_plan_too_large" || payload["queryId"] != "campaign-repository-coverage" ||
		payload["boundary"] != "retained_bytes" || !strings.Contains(payload["error"], "campaign-repository-coverage") ||
		strings.Contains(payload["error"], "536870912") || strings.Contains(payload["error"], "512") {
		t.Fatalf("unexpected limit response: %v", payload)
	}
}

func TestValidateListenSafety(t *testing.T) {
	for _, address := range []string{"127.0.0.1:8443", "localhost:8443", "[::1]:8443"} {
		if err := ValidateListen(address, "", ""); err != nil {
			t.Errorf("loopback %s rejected: %v", address, err)
		}
	}
	if err := ValidateListen("0.0.0.0:8443", "cert.pem", "key.pem"); err == nil {
		t.Fatal("expected non-loopback address rejection")
	}
	if err := ValidateListen("127.0.0.1:8443", "cert.pem", ""); err == nil {
		t.Fatal("expected incomplete certificate pair rejection")
	}
}

func TestValidateHostedListenRequiresTLSOutsideLoopback(t *testing.T) {
	if err := validateHostedListen("127.0.0.1:8080", "", "", false); err != nil {
		t.Fatalf("loopback hosted listener rejected: %v", err)
	}
	if err := validateHostedListen("0.0.0.0:8080", "", "", false); err == nil {
		t.Fatal("expected non-loopback hosted listener without TLS to be rejected")
	}
	if err := validateHostedListen("0.0.0.0:8443", "cert.pem", "key.pem", false); err != nil {
		t.Fatalf("TLS-protected hosted listener rejected: %v", err)
	}
	if err := validateHostedListen("0.0.0.0:8080", "", "", true); err != nil {
		t.Fatalf("trusted proxy hosted listener rejected: %v", err)
	}
}

func TestHostedProxyHeadersAreTrustedOnlyOnLoopbackBoundary(t *testing.T) {
	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "https://internal.example.test/", nil)
	request.Host = "internal.example.test"
	request.Header.Set("X-Forwarded-Host", "dashboard.example.com")
	request.Header.Set("X-Forwarded-Proto", "https")

	direct := ProxyPolicy{AllowedHosts: []string{"dashboard.example.com"}, RequireHTTPS: true}
	if validProxyRequest(request, direct) {
		t.Fatal("direct hosted listener trusted caller-supplied forwarded headers")
	}

	loopbackProxy := direct
	loopbackProxy.TrustForwarded = true
	loopbackProxy.TrustedProxyPrefixes = loopbackProxyPrefixes()
	request.RemoteAddr = "127.0.0.1:12345"
	if !validProxyRequest(request, loopbackProxy) {
		t.Fatal("loopback proxy boundary rejected trusted forwarded headers")
	}
	request.RemoteAddr = "203.0.113.10:12345"
	if validProxyRequest(request, loopbackProxy) {
		t.Fatal("hosted boundary trusted forwarded headers from an untrusted peer")
	}
}

func TestHostedProxyCIDRsFailClosed(t *testing.T) {
	prefixes, err := parseTrustedProxyPrefixes("10.42.0.0/24,fd00:42::/64")
	if err != nil {
		t.Fatal(err)
	}
	if len(prefixes) != 2 {
		t.Fatalf("got %d proxy prefixes, want 2", len(prefixes))
	}
	for _, value := range []string{"0.0.0.0/0", "203.0.113.0/24", "not-a-cidr"} {
		if _, err := parseTrustedProxyPrefixes(value); err == nil {
			t.Fatalf("accepted unsafe trusted proxy CIDR %q", value)
		}
	}
}

func TestHostedModesCannotDisableHTTPS(t *testing.T) {
	config := Config{
		HostProfile: hostedHostProfile(),
		Listen:      "127.0.0.1:8080",
		Proxy:       ProxyPolicy{AllowedHosts: []string{"dashboard.example.com"}},
		GitHubOAuth: validOAuthConfig("https://github.test"),
	}
	if err := validateHostedMode(newTestOperationalStore(t), &config); err == nil {
		t.Fatal("hosted mode accepted disabled HTTPS enforcement")
	}

	config.HostProfile = platformHostProfile()
	config.Listen = ""
	config.Proxy = ProxyPolicy{AllowedHosts: []string{"dashboard.example.com"}}
	if err := validateHostedMode(newTestOperationalStore(t), &config); err == nil {
		t.Fatal("platform mode accepted disabled HTTPS enforcement")
	}
}

func TestExternallyHostedModeRequiresExplicitForwardedPeer(t *testing.T) {
	config := Config{
		HostProfile: func() HostProfile {
			profile := hostedHostProfile()
			profile.Listener = HostListenerExternal
			return profile
		}(),
		Proxy:       ProxyPolicy{AllowedHosts: []string{"dashboard.example.com"}, RequireHTTPS: true, TrustForwarded: true},
		GitHubOAuth: validOAuthConfig("https://github.test"),
	}
	if err := validateHostedMode(newTestOperationalStore(t), &config); err == nil ||
		!strings.Contains(err.Error(), "explicit trusted proxy CIDRs") {
		t.Fatalf("external handler accepted untrusted forwarded headers: %v", err)
	}
}

func TestExternallyHostedHandlerRequiresActiveLifecycle(t *testing.T) {
	database := integrationDatabase(t)
	profile := hostedHostProfile()
	profile.Listener = HostListenerExternal
	app, err := New(t.Context(), newTestOperationalStore(t), Config{
		Database:    database,
		HostProfile: profile, SiteDirectory: hostedTestSite(t),
		Proxy:       ProxyPolicy{AllowedHosts: []string{"dashboard.example.com"}, RequireHTTPS: true},
		GitHubOAuth: validOAuthConfig("https://github.test"),
		CORS:        CORSPolicy{AllowedOrigins: []string{"https://tools.example.com"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := app.Handler()
	request := func(host string) *http.Request {
		return httptest.NewRequestWithContext(t.Context(), http.MethodGet, "https://"+host+"/api/health", nil)
	}
	respond := func(req *http.Request) int {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response.Code
	}
	if status := respond(request("dashboard.example.com")); status != http.StatusServiceUnavailable {
		t.Fatalf("handler served before startup: %d", status)
	}
	preflight := httptest.NewRequestWithContext(t.Context(), http.MethodOptions, "https://dashboard.example.com/api/v1/events", nil)
	preflight.Header.Set("Origin", "https://tools.example.com")
	preflight.Header.Set("Access-Control-Request-Method", http.MethodGet)
	if status := respond(preflight); status != http.StatusServiceUnavailable {
		t.Fatalf("CORS preflight bypassed startup: %d", status)
	}
	if err := app.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := app.Stop(t.Context()); err != nil {
			t.Errorf("Stop failed: %v", err)
		}
	}()
	if status := respond(preflight); status != http.StatusNoContent {
		t.Fatalf("CORS preflight did not work after startup: %d", status)
	}
	if status := respond(request("dashboard.example.com")); status != http.StatusOK {
		t.Fatalf("handler did not serve after startup: %d", status)
	}
	if status := respond(request("wrong.example.com")); status != http.StatusMisdirectedRequest {
		t.Fatalf("handler accepted a different host: %d", status)
	}
	if status := respond(httptest.NewRequestWithContext(t.Context(), http.MethodGet, "http://dashboard.example.com/api/health", nil)); status != http.StatusMisdirectedRequest {
		t.Fatalf("handler accepted plaintext request: %d", status)
	}
	if err := app.Start(t.Context()); err == nil {
		t.Fatal("service started twice")
	}
	if err := app.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
	if status := respond(request("dashboard.example.com")); status != http.StatusServiceUnavailable {
		t.Fatalf("handler served after shutdown: %d", status)
	}
	if status := respond(preflight); status != http.StatusServiceUnavailable {
		t.Fatalf("CORS preflight bypassed shutdown: %d", status)
	}
}

func TestStartRejectsCanceledContext(t *testing.T) {
	app := &App{config: Config{HostProfile: hostedHostProfile()}}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := app.Start(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("starting canceled service returned %v", err)
	}
	if app.startContext != nil {
		t.Fatal("canceled service recorded an active lifecycle")
	}
}

func TestEventStreamOutlivesHTTPWriteTimeout(t *testing.T) {
	database := integrationDatabase(t)
	app := &App{services: newTestOperationalStore(t).OperationalServices(), hub: newEventHub(),
		database:    database,
		config:      Config{HostProfile: localHostProfile()},
		accessToken: testAccessToken,
	}
	server := httptest.NewUnstartedServer(app.Handler())
	server.Config.WriteTimeout = 50 * time.Millisecond
	server.Start()
	defer server.Close()

	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL+"/api/v1/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+testAccessToken)
	remote := &http.Client{Timeout: 2 * time.Second}
	response, err := remote.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("stream returned %d", response.StatusCode)
	}
	reader := bufio.NewReader(response.Body)
	if _, err := reader.ReadString('\n'); err != nil {
		t.Fatal(err)
	}
	time.Sleep(125 * time.Millisecond)
	app.hub.Broadcast(2)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("event stream ended after HTTP write timeout: %v", err)
		}
		if strings.Contains(line, `"revision":2`) {
			break
		}
	}
}

func TestEventStreamSupportsWriterWithoutDeadlineControl(t *testing.T) {
	database := integrationDatabase(t)
	app := &App{services: newTestOperationalStore(t).OperationalServices(), hub: newEventHub(),
		database:    database,
		config:      Config{HostProfile: localHostProfile()},
		accessToken: testAccessToken,
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	request := httptest.NewRequestWithContext(ctx, http.MethodGet, "http://localhost/api/v1/events", nil)
	authorize(request)
	response := &flushNotifyingRecorder{ResponseRecorder: httptest.NewRecorder(), flushed: make(chan struct{}, 1)}
	done := make(chan struct{})
	go func() {
		defer close(done)
		app.Handler().ServeHTTP(response, request)
	}()
	select {
	case <-response.flushed:
	case <-time.After(2 * time.Second):
		t.Fatal("writer without deadline control did not flush an event")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("event stream did not stop after cancellation")
	}
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "data:") {
		t.Fatalf("writer without deadline control did not stream: status=%d", response.Code)
	}
}

type flushNotifyingRecorder struct {
	*httptest.ResponseRecorder
	flushed chan struct{}
}

func (recorder *flushNotifyingRecorder) Flush() {
	recorder.ResponseRecorder.Flush()
	select {
	case recorder.flushed <- struct{}{}:
	default:
	}
}

func TestCapabilityURLUsesConfiguredTransport(t *testing.T) {
	app := &App{config: Config{Listen: "127.0.0.1:8443"}, accessToken: testAccessToken}
	if got := app.capabilityURL(); !strings.HasPrefix(got, "http://") {
		t.Fatalf("local debugging must default to HTTP: %s", got)
	}

	app.config.CertFile = "localhost.pem"
	app.config.KeyFile = "localhost-key.pem"
	if got := app.capabilityURL(); !strings.HasPrefix(got, "https://") {
		t.Fatalf("explicit certificate must enable HTTPS: %s", got)
	}
}

func TestCurrentDashboardQueriesAreAccepted(t *testing.T) {
	definitions, err := ParseDashboardQueries("../../../dashboard/site/dashboard.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := query.Validate(definitions); err != nil {
		t.Fatalf("current dashboard queries are not supported: %v", err)
	}
}

func TestValueOverviewResolvesOneCanonicalPageBoundary(t *testing.T) {
	database, err := ParseDashboardQueries("../../../dashboard/site/src/data/queries/database.json")
	if err != nil {
		t.Fatal(err)
	}
	value, err := ParseDashboardQueries("../../../dashboard/site/dashboard-fragments/workflow-value.json")
	if err != nil {
		t.Fatal(err)
	}
	plan, err := query.ResolvePagePlan(append(database, value...), "value-workflow-overview")
	if err != nil {
		t.Fatal(err)
	}
	if plan == nil || plan.Boundary != "workflow-subject-page" || plan.Root != "$workflows" {
		t.Fatalf("Value overview did not resolve its canonical workflow page: %+v", plan)
	}
}

func TestAPINeverReturnsOperationalCredentials(t *testing.T) {
	database := integrationDatabase(t)
	site, err := os.MkdirTemp(".", ".test-site-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(site); err != nil {
			t.Errorf("remove test site: %v", err)
		}
	})
	if err := os.WriteFile(site+"/index.html", []byte("<html><head></head><body></body></html>"), 0o600); err != nil {
		t.Fatal(err)
	}
	app, err := New(context.Background(), newTestOperationalStore(t), Config{
		Database: database,
		Listen:   "127.0.0.1:8443", SiteDirectory: site, AccessToken: testAccessToken,
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "https://localhost/api/v1/health", nil)
	authorize(request)
	response := httptest.NewRecorder()
	app.Handler().ServeHTTP(response, request)
	body := response.Body.String()
	for _, forbidden := range []string{"super-secret", "postgres://"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("API response leaked %q: %s", forbidden, body)
		}
	}
}

func TestAPIResponsesCarryStandardizedTraceIdentifiers(t *testing.T) {
	database := integrationDatabase(t)
	previousProvider := otel.GetTracerProvider()
	t.Cleanup(func() { otel.SetTracerProvider(previousProvider) })
	t.Setenv("OTEL_SDK_DISABLED", "")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://127.0.0.1:4318")
	shutdown, err := telemetry.Setup(t.Context(), "test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 0)
		defer cancel()
		_ = shutdown(ctx)
	})
	site, err := os.MkdirTemp(".", ".test-site-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(site); err != nil {
			t.Errorf("remove test site: %v", err)
		}
	})
	if err := os.WriteFile(site+"/index.html", []byte("<html><head></head><body></body></html>"), 0o600); err != nil {
		t.Fatal(err)
	}
	app, err := New(context.Background(), newTestOperationalStore(t), Config{
		Database: database,
		Listen:   "127.0.0.1:8443", SiteDirectory: site, AccessToken: testAccessToken,
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "https://localhost/api/v1/health", nil)
	authorize(request)
	response := httptest.NewRecorder()
	app.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("health returned %d: %s", response.Code, response.Body.String())
	}
	traceID := response.Header().Get("X-Trace-Id")
	spanID := response.Header().Get("X-Span-Id")
	if len(traceID) != 32 {
		t.Fatalf("expected a 32-character W3C trace id, got %q", traceID)
	}
	if len(spanID) != 16 {
		t.Fatalf("expected a 16-character W3C span id, got %q", spanID)
	}

	errorRequest := httptest.NewRequestWithContext(
		t.Context(), http.MethodGet, "https://localhost/api/v1/diagnostics", nil,
	)
	errorResponse := httptest.NewRecorder()
	app.Handler().ServeHTTP(errorResponse, errorRequest)
	if errorResponse.Code != http.StatusUnauthorized {
		t.Fatalf("diagnostics returned %d: %s", errorResponse.Code, errorResponse.Body.String())
	}
	var errorBody map[string]string
	if err := json.Unmarshal(errorResponse.Body.Bytes(), &errorBody); err != nil {
		t.Fatal(err)
	}
	if errorBody["traceId"] == "" ||
		errorBody["traceId"] != errorResponse.Header().Get(telemetry.TraceIDHeader) {
		t.Fatalf("error body trace id does not match response: %#v", errorBody)
	}
	if errorBody["spanId"] == "" ||
		errorBody["spanId"] != errorResponse.Header().Get(telemetry.SpanIDHeader) {
		t.Fatalf("error body span id does not match response: %#v", errorBody)
	}
}

func TestAccessFailuresCarryStandardizedTraceIdentifiersWithoutDatabase(t *testing.T) {
	previousProvider := otel.GetTracerProvider()
	provider := sdktrace.NewTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() {
		_ = provider.Shutdown(context.Background())
		otel.SetTracerProvider(previousProvider)
	})

	app := &App{
		config:      Config{HostProfile: localHostProfile()},
		accessToken: testAccessToken,
	}
	request := httptest.NewRequestWithContext(
		t.Context(), http.MethodGet, "https://localhost/api/v1/diagnostics", nil,
	)
	response := httptest.NewRecorder()
	app.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("diagnostics returned %d: %s", response.Code, response.Body.String())
	}
	var payload map[string]string
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	traceID := response.Header().Get(telemetry.TraceIDHeader)
	spanID := response.Header().Get(telemetry.SpanIDHeader)
	if len(traceID) != 32 || payload["traceId"] != traceID {
		t.Fatalf("error trace id does not match response header: %#v", payload)
	}
	if len(spanID) != 16 || payload["spanId"] != spanID {
		t.Fatalf("error span id does not match response header: %#v", payload)
	}
}

func TestRefreshAndQueryReturnAuthoritativeEvaluatedAt(t *testing.T) {
	database := integrationDatabase(t)
	seedDatabase(t, database, map[string]model.Source{
		"$runs": {Source: "$runs", Rows: []model.Row{}},
	})
	site, err := os.MkdirTemp(".", ".test-site-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(site); err != nil {
			t.Errorf("remove test site: %v", err)
		}
	})
	if err := os.WriteFile(site+"/index.html", []byte("<html><head></head></html>"), 0o600); err != nil {
		t.Fatal(err)
	}
	app, err := New(context.Background(), newTestOperationalStore(t), Config{
		Database: database,
		Listen:   "127.0.0.1:8443", SiteDirectory: site, AccessToken: testAccessToken,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		path string
		body string
	}{
		{path: "/api/v1/refresh", body: ""},
		{path: "/api/v1/query", body: `{"sourceNames":["$runs"],"evaluatedAt":"2099-01-01T00:00:00Z"}`},
	} {
		request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "https://localhost"+test.path, strings.NewReader(test.body))
		if test.body != "" {
			request.Header.Set("Content-Type", "application/json")
		}
		authorize(request)
		response := httptest.NewRecorder()
		app.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("%s returned %d: %s", test.path, response.Code, response.Body.String())
		}
		if !strings.Contains(response.Body.String(), `"evaluatedAt":"2026-02-03T04:05:06Z"`) {
			t.Fatalf("%s did not return authoritative evaluatedAt: %s", test.path, response.Body.String())
		}
		if strings.Contains(response.Body.String(), "2099-01-01") {
			t.Fatalf("%s trusted caller evaluatedAt: %s", test.path, response.Body.String())
		}
	}
}

func TestHealthAndReadinessExposeAuthoritativeEvaluatedAt(t *testing.T) {
	database := integrationDatabase(t)
	seedDatabase(t, database, map[string]model.Source{
		"$runs": {Source: "$runs", Rows: []model.Row{}},
	})
	app := &App{services: newTestOperationalStore(t).OperationalServices(), database: database}
	for _, path := range []string{"/api/v1/health", "/api/readiness"} {
		request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "https://localhost"+path, nil)
		response := httptest.NewRecorder()
		app.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("%s returned %d: %s", path, response.Code, response.Body.String())
		}
		if !strings.Contains(response.Body.String(), `"evaluatedAt":"2026-02-03T04:05:06Z"`) {
			t.Fatalf("%s did not expose authoritative evaluatedAt: %s", path, response.Body.String())
		}
	}
}

func TestQueryAllowsEmptyReadinessProbe(t *testing.T) {
	database := integrationDatabase(t)
	seedDatabase(t, database, nil)
	site, err := os.MkdirTemp(".", ".test-site-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(site); err != nil {
			t.Errorf("remove test site: %v", err)
		}
	})
	if err := os.WriteFile(site+"/index.html", []byte("<html><head></head></html>"), 0o600); err != nil {
		t.Fatal(err)
	}
	app, err := New(context.Background(), newTestOperationalStore(t), Config{
		Database: database,
		Listen:   "127.0.0.1:8443", SiteDirectory: site, AccessToken: testAccessToken,
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodPost,
		"https://localhost/api/v1/query",
		strings.NewReader(`{"sourceNames":[]}`),
	)
	authorize(request)
	response := httptest.NewRecorder()
	app.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("readiness probe returned %d: %s", response.Code, response.Body.String())
	}
	var result struct {
		Revision    int64                   `json:"revision"`
		EvaluatedAt string                  `json:"evaluatedAt"`
		Sources     map[string]model.Source `json:"sources"`
		Metrics     model.Metrics           `json:"metrics"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Revision != 1 || result.EvaluatedAt != "2026-02-03T04:05:06Z" {
		t.Fatalf("unexpected readiness metadata: %#v", result)
	}
	if len(result.Sources) != 0 {
		t.Fatalf("readiness probe returned sources: %#v", result.Sources)
	}
	if result.Metrics.DurationMS != 0 || result.Metrics.Operations != 0 || result.Metrics.OutputRows != 0 ||
		len(result.Metrics.PushedDown) != 0 {
		t.Fatalf("readiness probe returned non-zero metrics: %#v", result.Metrics)
	}
}

func TestStaticIndexInjectsBackendMeta(t *testing.T) {
	site, err := os.MkdirTemp(".", ".test-site-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(site); err != nil {
			t.Errorf("remove test site: %v", err)
		}
	})
	if err := os.WriteFile(site+"/index.html", []byte("<html><head><title>CAO</title></head><body></body></html>"), 0o600); err != nil {
		t.Fatal(err)
	}
	app, err := New(context.Background(), newTestOperationalStore(t), Config{
		Database: constructorDatabase(),
		Listen:   "127.0.0.1:8443", SiteDirectory: site, AccessToken: testAccessToken,
	})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "https://localhost/route", nil)
	authorize(request)
	app.Handler().ServeHTTP(response, request)
	if !strings.Contains(response.Body.String(), `<meta name="dashboard-data-backend" content="server-http">`) {
		t.Fatalf("backend meta was not injected: %s", response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `<meta name="dashboard-storage-backend" content="postgres">`) {
		t.Fatal("PostgreSQL storage meta was not injected")
	}
}

func TestCapabilityTokenProtectsStaticAssetsAndAPI(t *testing.T) {
	database := integrationDatabase(t)
	seedDatabase(t, database, nil)
	site := t.TempDir()
	if err := os.WriteFile(site+"/index.html", []byte("<html><head></head><body></body></html>"), 0o600); err != nil {
		t.Fatal(err)
	}
	app, err := New(context.Background(), newTestOperationalStore(t), Config{
		Database: database,
		Listen:   "127.0.0.1:8443", SiteDirectory: site, AccessToken: testAccessToken,
	})
	if err != nil {
		t.Fatal(err)
	}

	unauthorizedAPI := httptest.NewRecorder()
	app.Handler().ServeHTTP(
		unauthorizedAPI,
		httptest.NewRequestWithContext(t.Context(), http.MethodPost, "https://localhost/api/v1/refresh", nil),
	)
	if unauthorizedAPI.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized API returned %d", unauthorizedAPI.Code)
	}

	bootstrap := httptest.NewRecorder()
	app.Handler().ServeHTTP(
		bootstrap,
		httptest.NewRequestWithContext(
			t.Context(),
			http.MethodGet,
			"https://localhost/?access_token="+testAccessToken,
			nil,
		),
	)
	if bootstrap.Code != http.StatusOK {
		t.Fatalf("bootstrap returned %d: %s", bootstrap.Code, bootstrap.Body.String())
	}
	cookies := bootstrap.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != "cao_github_local" ||
		cookies[0].Value == testAccessToken || !cookies[0].HttpOnly ||
		cookies[0].Path != "/api/v1/github/" {
		t.Fatal("bootstrap must issue only a distinct, HttpOnly, read-only resource cookie")
	}
	if !strings.Contains(bootstrap.Body.String(), `localStorage.setItem("cao-dashboard-access-token"`) {
		t.Fatalf("bootstrap did not initialize origin-scoped storage: %s", bootstrap.Body.String())
	}
	if !strings.Contains(bootstrap.Body.String(), `history.replaceState`) ||
		strings.Contains(bootstrap.Body.String(), `location.replace`) {
		t.Fatalf("bootstrap must remove the token without navigation: %s", bootstrap.Body.String())
	}

	authorizedAPI := httptest.NewRecorder()
	request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "https://localhost/api/v1/refresh", nil)
	authorize(request)
	app.Handler().ServeHTTP(authorizedAPI, request)
	if authorizedAPI.Code != http.StatusOK {
		t.Fatalf("authorized API returned %d: %s", authorizedAPI.Code, authorizedAPI.Body.String())
	}

	invalidHost := httptest.NewRecorder()
	request = httptest.NewRequestWithContext(t.Context(), http.MethodGet, "https://attacker.example/", nil)
	request.Host = "attacker.example"
	authorize(request)
	app.Handler().ServeHTTP(invalidHost, request)
	if invalidHost.Code != http.StatusMisdirectedRequest {
		t.Fatalf("invalid host returned %d", invalidHost.Code)
	}
}

func authorize(request *http.Request) {
	request.Header.Set("Authorization", "Bearer "+testAccessToken)
}

func TestReadCommandConsumesLargeBulkStrings(t *testing.T) {
	payload := strings.Repeat("script\n", 2048)
	input := fmt.Sprintf("*2\r\n$4\r\nEVAL\r\n$%d\r\n%s\r\n", len(payload), payload)
	command, err := readCommand(bufio.NewReaderSize(strings.NewReader(input), 64))
	if err != nil || len(command) != 2 || command[0] != "EVAL" || command[1] != payload {
		t.Fatalf("large RESP bulk string was not consumed exactly: fields=%d err=%v", len(command), err)
	}
}

func readCommand(reader *bufio.Reader) ([]string, error) {
	var count int
	if _, err := fmt.Fscanf(reader, "*%d\r\n", &count); err != nil {
		return nil, err
	}
	command := make([]string, count)
	for i := range command {
		var length int
		if _, err := fmt.Fscanf(reader, "$%d\r\n", &length); err != nil {
			return nil, err
		}
		value := make([]byte, length+2)
		if _, err := io.ReadFull(reader, value); err != nil {
			return nil, err
		}
		command[i] = string(value[:length])
	}
	return command, nil
}
