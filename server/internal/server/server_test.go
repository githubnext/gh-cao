package server

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/githubnext/gh-cao/server/internal/model"
	"github.com/githubnext/gh-cao/server/internal/query"
	"github.com/githubnext/gh-cao/server/internal/redisx"
	"github.com/githubnext/gh-cao/server/internal/telemetry"
)

const testAccessToken = "0123456789abcdef0123456789abcdef"

func TestQueryPlanLimitErrorResponse(t *testing.T) {
	response := httptest.NewRecorder()
	writeQueryError(response, http.StatusBadRequest, &query.PlanLimitError{
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

func TestHostedRedisRequiresTLSUnlessPrivatePlaintextIsExplicit(t *testing.T) {
	if err := validateHostedRedisURL("redis://redis:6379/0", false, false); err == nil {
		t.Fatal("hosted mode accepted plaintext Redis without opt-in")
	}
	if err := validateHostedRedisURL("redis://redis:6379/0", true, false); err != nil {
		t.Fatalf("hosted mode rejected explicitly allowed private Redis: %v", err)
	}
	if err := validateHostedRedisURL("redis://redis.railway.internal:6379/0", true, false); err != nil {
		t.Fatalf("hosted mode rejected explicitly allowed Railway private Redis: %v", err)
	}
	if err := validateHostedRedisURL("redis://redis.example.com:6379/0", true, false); err == nil {
		t.Fatal("hosted mode accepted a public plaintext Redis hostname")
	}
	if err := validateHostedRedisURL("rediss://redis.example.com:6380/0", false, false); err != nil {
		t.Fatalf("hosted mode rejected TLS Redis: %v", err)
	}
}

func TestUpstashRevocationKeyPrefixIsStableAndModeSpecific(t *testing.T) {
	first, err := durableRevocationKeyPrefix(true, "dashboard")
	if err != nil {
		t.Fatal(err)
	}
	second, err := durableRevocationKeyPrefix(true, " cao:DASHBOARD ")
	if err != nil {
		t.Fatal(err)
	}
	if first == "" || first != second {
		t.Fatalf("upstash revocation prefix must be stable: %q, %q", first, second)
	}
	other, err := durableRevocationKeyPrefix(true, "other")
	if err != nil {
		t.Fatal(err)
	}
	if first == other {
		t.Fatal("different configured namespaces shared an Upstash revocation prefix")
	}
	if value, err := durableRevocationKeyPrefix(false, "invalid namespace!"); err != nil || value != "" {
		t.Fatalf("standard mode unexpectedly received a revocation prefix: %q", value)
	}
}

func TestAzureRedisLocalSimulationIsLoopbackOnly(t *testing.T) {
	for _, endpoint := range []string{
		"redis://127.0.0.1:6379/0",
		"redis://[::1]:6379/0",
		"redis://localhost:6379/0",
		"rediss://redis.example.com:6380/0",
	} {
		if err := validateAzureRedisURL(endpoint, true); err != nil {
			t.Errorf("local simulation rejected %s: %v", endpoint, err)
		}
	}
	for _, endpoint := range []string{
		"redis://redis.example.com:6379/0",
		"http://127.0.0.1:6379/0",
	} {
		if err := validateAzureRedisURL(endpoint, true); err == nil {
			t.Errorf("local simulation accepted %s", endpoint)
		}
	}
	if err := validateAzureRedisURL("redis://127.0.0.1:6379/0", false); err == nil {
		t.Fatal("production Azure mode accepted plaintext loopback Redis")
	}
}

func TestAzureLocalSimulationFlagFailsClosed(t *testing.T) {
	t.Setenv("CAO_AZURE_LOCAL_SIMULATION", "true")
	if _, err := azureLocalSimulationFromEnv(); err == nil {
		t.Fatal("malformed local simulation flag was accepted")
	}
	t.Setenv("CAO_AZURE_LOCAL_SIMULATION", "1")
	if enabled, err := azureLocalSimulationFromEnv(); err != nil || !enabled {
		t.Fatalf("explicit local simulation flag was not accepted: enabled=%t err=%v", enabled, err)
	}
}

func TestAzureLocalSimulationRequiresLoopbackHTTPBoundary(t *testing.T) {
	if err := validateAzureLocalEndpoints(
		true,
		[]string{"localhost", "127.0.0.1", "::1"},
		"http://127.0.0.1:7071/auth/callback",
	); err != nil {
		t.Fatalf("loopback local endpoints were rejected: %v", err)
	}
	for _, test := range []struct {
		hosts    []string
		redirect string
	}{
		{hosts: []string{"dashboard.example.com"}, redirect: "http://127.0.0.1:7071/auth/callback"},
		{hosts: []string{"localhost"}, redirect: "http://dashboard.example.com/auth/callback"},
	} {
		if err := validateAzureLocalEndpoints(true, test.hosts, test.redirect); err == nil {
			t.Fatalf("non-loopback local endpoints were accepted: %#v", test)
		}
	}
	if err := validateAzureLocalEndpoints(
		false,
		[]string{"dashboard.example.com"},
		"https://dashboard.example.com/auth/callback",
	); err != nil {
		t.Fatalf("production endpoints were changed by local validation: %v", err)
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
	if err := validateHostedMode(&redisx.Store{}, &config); err == nil {
		t.Fatal("hosted mode accepted disabled HTTPS enforcement")
	}

	config.HostProfile = azureFunctionsHostProfile()
	config.Listen = ""
	config.Proxy = ProxyPolicy{AllowedHosts: []string{"dashboard.example.com"}}
	if err := validateHostedMode(&redisx.Store{}, &config); err == nil {
		t.Fatal("Azure Functions mode accepted disabled HTTPS enforcement")
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
	if err := validateHostedMode(&redisx.Store{}, &config); err == nil ||
		!strings.Contains(err.Error(), "explicit trusted proxy CIDRs") {
		t.Fatalf("external handler accepted untrusted forwarded headers: %v", err)
	}
}

func TestExternallyHostedHandlerRequiresActiveLifecycle(t *testing.T) {
	database := integrationDatabase(t)
	address, closeRedis := fakeRedis(t)
	defer closeRedis()
	client, err := redisx.New("redis://" + address)
	if err != nil {
		t.Fatal(err)
	}
	profile := hostedHostProfile()
	profile.Listener = HostListenerExternal
	app, err := New(t.Context(), redisx.NewStore(client, "external-test"), Config{
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
	address, closeRedis := fakeRedis(t)
	defer closeRedis()
	client, err := redisx.New("redis://" + address)
	if err != nil {
		t.Fatal(err)
	}
	app := &App{services: redisx.NewStore(client, "stream-test").OperationalServices(), hub: newEventHub(),
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
	address, closeRedis := fakeRedis(t)
	defer closeRedis()
	client, err := redisx.New("redis://" + address)
	if err != nil {
		t.Fatal(err)
	}
	app := &App{services: redisx.NewStore(client, "stream-test").OperationalServices(), hub: newEventHub(),
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

func TestAPINeverReturnsRedisCredentials(t *testing.T) {
	database := integrationDatabase(t)
	address, closeServer := fakeRedis(t)
	defer closeServer()
	client, err := redisx.New("redis://default:super-secret@" + address + "/0")
	if err != nil {
		t.Fatal(err)
	}
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
	app, err := New(context.Background(), redisx.NewStore(client, "test"), Config{
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
	for _, forbidden := range []string{"super-secret", "redis://", address} {
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
	address, closeServer := fakeRedis(t)
	defer closeServer()
	client, err := redisx.New("redis://" + address)
	if err != nil {
		t.Fatal(err)
	}
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
	app, err := New(context.Background(), redisx.NewStore(client, "test"), Config{
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

func TestProcessTelemetryClose(t *testing.T) {
	var p processTelemetry
	if err := p.close(t.Context()); err != nil {
		t.Fatalf("unconfigured telemetry shutdown: %v", err)
	}
	called := false
	p.shutdown = func(ctx context.Context) error {
		called = true
		if ctx.Err() != nil {
			t.Fatal("telemetry shutdown received a cancelled context")
		}
		return nil
	}
	if err := p.close(t.Context()); err != nil || !called {
		t.Fatalf("process telemetry shutdown: called=%t err=%v", called, err)
	}
}

func TestAzureFunctionsHandlerLogsTelemetryFailureOnceAndKeepsServing(t *testing.T) {
	// NewAzureFunctionsHandlerFromEnv is the only caller of azureProcessTelemetry
	// in the process, and this is its only test, so the shared sync.Once
	// has not fired yet; sync.Once cannot be copied/reset, so no
	// save/restore is attempted here.
	previousErr := azureProcessTelemetry.err
	t.Cleanup(func() { azureProcessTelemetry.err = previousErr })

	// An OTLP endpoint plus a malformed OTEL_RESOURCE_ATTRIBUTES value
	// forces telemetry.Setup to fail while building the OpenTelemetry
	// resource, without needing a reachable collector.
	t.Setenv("OTEL_SDK_DISABLED", "")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://127.0.0.1:4318")
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "not-a-valid-key-value-list")
	policyPath := t.TempDir() + "/cao.json"
	if err := os.WriteFile(policyPath, []byte(`{
		"control-plane": {
			"web": {
				"host": {
					"target": {"module": "azure-functions"},
					"redis": {
						"module": "local",
						"url-env": "CAO_REDIS_URL",
						"allow-private-plaintext": true,
						"tls": {"mode": "disabled"}
					}
				}
			}
		}
	}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CAO_POLICY_PATH", policyPath)
	t.Setenv("CAO_REDIS_URL", "")

	var logOutput strings.Builder
	logger := log.New(&logOutput, "", 0)

	for i := 0; i < 2; i++ {
		_, err := NewAzureFunctionsHandlerFromEnv(t.Context(), t.TempDir(), "", logger)
		if err == nil || !strings.Contains(err.Error(), "CAO_REDIS_URL") {
			t.Fatalf("call %d: expected the missing CAO_REDIS_URL error, got %v", i, err)
		}
	}

	occurrences := strings.Count(logOutput.String(), "telemetry configuration failed")
	if occurrences != 1 {
		t.Fatalf("expected exactly one telemetry failure log line across repeated invocations, got %d: %q", occurrences, logOutput.String())
	}
}

func TestRefreshAndQueryReturnAuthoritativeEvaluatedAt(t *testing.T) {
	database := integrationDatabase(t)
	seedDatabase(t, database, map[string]model.Source{
		"$runs": {Source: "$runs", Rows: []model.Row{}},
	})
	address, closeServer := fakeRedis(t)
	defer closeServer()
	client, err := redisx.New("redis://" + address)
	if err != nil {
		t.Fatal(err)
	}
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
	app, err := New(context.Background(), redisx.NewStore(client, "test"), Config{
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
	app := &App{services: redisx.NewStore(emptyRedisClient{}, "empty-test").OperationalServices(), database: database}
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
	address, closeServer := fakeRedis(t)
	defer closeServer()
	client, err := redisx.New("redis://" + address)
	if err != nil {
		t.Fatal(err)
	}
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
	app, err := New(context.Background(), redisx.NewStore(client, "test"), Config{
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
	address, closeServer := fakeRedis(t)
	defer closeServer()
	client, _ := redisx.New("redis://" + address)
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
	app, err := New(context.Background(), redisx.NewStore(client, "test"), Config{
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
	address, closeServer := fakeRedis(t)
	defer closeServer()
	client, err := redisx.New("redis://" + address)
	if err != nil {
		t.Fatal(err)
	}
	site := t.TempDir()
	if err := os.WriteFile(site+"/index.html", []byte("<html><head></head><body></body></html>"), 0o600); err != nil {
		t.Fatal(err)
	}
	app, err := New(context.Background(), redisx.NewStore(client, "test"), Config{
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

func fakeRedis(t *testing.T) (string, func()) {
	t.Helper()
	var listenConfig net.ListenConfig
	listener, err := listenConfig.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	var mu sync.Mutex
	values := map[string]string{}
	sets := map[string]map[string]bool{}
	go func() {
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() {
					_ = connection.Close()
				}()
				reader := bufio.NewReader(connection)
				for {
					command, err := readCommand(reader)
					if err != nil {
						return
					}
					switch command[0] {
					case "AUTH", "SELECT":
						_, _ = fmt.Fprint(connection, "+OK\r\n")
					case "PING":
						_, _ = fmt.Fprint(connection, "+PONG\r\n")
					case "INFO":
						_, _ = fmt.Fprintf(connection, "$%d\r\n%s\r\n", len(cacheTestMemoryInfo), cacheTestMemoryInfo)
					case "SET":
						mu.Lock()
						_, exists := values[command[1]]
						if len(command) < 4 || command[3] != "NX" || !exists {
							values[command[1]] = command[2]
						}
						mu.Unlock()
						if len(command) >= 4 && command[3] == "NX" && exists {
							_, _ = fmt.Fprint(connection, "$-1\r\n")
						} else {
							_, _ = fmt.Fprint(connection, "+OK\r\n")
						}
					case "GET":
						mu.Lock()
						value, ok := values[command[1]]
						mu.Unlock()
						if !ok {
							_, _ = fmt.Fprint(connection, "$-1\r\n")
						} else {
							_, _ = fmt.Fprintf(connection, "$%d\r\n%s\r\n", len(value), value)
						}
					case "DEL":
						mu.Lock()
						_, ok := values[command[1]]
						delete(values, command[1])
						mu.Unlock()
						if ok {
							_, _ = fmt.Fprint(connection, ":1\r\n")
						} else {
							_, _ = fmt.Fprint(connection, ":0\r\n")
						}
					case "SADD":
						mu.Lock()
						if sets[command[1]] == nil {
							sets[command[1]] = map[string]bool{}
						}
						added := 0
						for _, member := range command[2:] {
							if !sets[command[1]][member] {
								added++
							}
							sets[command[1]][member] = true
						}
						mu.Unlock()
						_, _ = fmt.Fprintf(connection, ":%d\r\n", added)
					case "SISMEMBER":
						mu.Lock()
						member := sets[command[1]][command[2]]
						mu.Unlock()
						if member {
							_, _ = fmt.Fprint(connection, ":1\r\n")
						} else {
							_, _ = fmt.Fprint(connection, ":0\r\n")
						}
					case "HSET":
						_, _ = fmt.Fprint(connection, ":1\r\n")
					case "XADD":
						_, _ = fmt.Fprint(connection, "$3\r\n1-0\r\n")
					case "EVAL":
						if strings.Contains(command[1], `return {probe("INFO"`) {
							_, _ = fmt.Fprintf(connection, "*2\r\n*2\r\n:1\r\n$%d\r\n%s\r\n*2\r\n:1\r\n$-1\r\n",
								len(cacheTestMemoryInfo), cacheTestMemoryInfo)
							continue
						}
						if strings.Contains(command[1], `redis.call("STRLEN", KEYS[1])`) {
							mu.Lock()
							value, ok := values[command[3]]
							mu.Unlock()
							if !ok {
								_, _ = fmt.Fprint(connection, "$-1\r\n")
							} else {
								_, _ = fmt.Fprintf(connection, "$%d\r\n%s\r\n", len(value), value)
							}
							continue
						}
						// Cache semantics are covered against real Redis; this fixture always misses.
						if len(command) == 13 && strings.HasSuffix(command[3], "{query-cache:v1}:entries") {
							result := "$-1\r\n"
							if command[5] == "put" {
								result = ":1\r\n"
							}
							_, _ = fmt.Fprint(connection, "*5\r\n"+result+":0\r\n:0\r\n:0\r\n:0\r\n")
							continue
						}
						mu.Lock()
						result := 1
						bulkResult := ""
						rateLimitResult := false
						switch {
						// Session expiry/Lua semantics are exercised against real Redis.
						case strings.Contains(command[1], "-- cao-session-put"):
							values[command[3]] = command[6]
						case strings.Contains(command[1], "-- cao-session-compare"):
							if values[command[3]] == command[6] {
								values[command[3]] = command[7]
							} else {
								result = 0
							}
						case strings.Contains(command[1], "-- cao-session-expire"):
							result = 0
						case strings.Contains(command[1], "return session.value") && !strings.Contains(command[1], "-- cao-session-invalidate"):
							bulkResult = values[command[3]]
							if bulkResult == "" {
								result = 0
							}
						case strings.Contains(command[1], `local current = redis.call("TIME")`):
							rateLimitResult = true
						case len(command) >= 6 && strings.Contains(command[1], `redis.call("SADD"`) && strings.Contains(command[1], `redis.call("DEL"`):
							bulkResult = values[command[3]]
							if bulkResult == "" {
								result = 0
								break
							}
							if len(command) >= 7 && command[6] != "" && bulkResult != command[6] {
								bulkResult = "superseded"
								break
							}
							values[command[4]] = bulkResult
							if sets[command[5]] == nil {
								sets[command[5]] = map[string]bool{}
							}
							sets[command[5]][command[4]] = true
							delete(values, command[3])
						case len(command) >= 6 && strings.Contains(command[1], `redis.call("SADD"`):
							values[command[3]] = command[5]
							if sets[command[4]] == nil {
								sets[command[4]] = map[string]bool{}
							}
							sets[command[4]][command[3]] = true
						case len(command) >= 6 && strings.Contains(command[1], `redis.call("SREM"`):
							if values[command[3]] == command[5] {
								delete(values, command[3])
								delete(sets[command[4]], command[3])
							} else {
								result = 0
							}
						case len(command) >= 7 && strings.Contains(command[1], `ARGV[1]`):
							if values[command[3]] == command[4] {
								values[command[3]] = command[5]
							} else {
								result = 0
							}
						case len(command) >= 5 && strings.Contains(command[1], `redis.call("DEL"`):
							if values[command[3]] == command[4] {
								delete(values, command[3])
							}
						}
						mu.Unlock()
						switch {
						case rateLimitResult:
							remaining := 119
							switch command[4] {
							case "30":
								remaining = 29
							case "10":
								remaining = 9
							}
							_, _ = fmt.Fprintf(connection, "*4\r\n:1\r\n:%d\r\n:0\r\n:500\r\n", remaining)
						case bulkResult != "":
							_, _ = fmt.Fprintf(connection, "$%d\r\n%s\r\n", len(bulkResult), bulkResult)
						default:
							_, _ = fmt.Fprintf(connection, ":%d\r\n", result)
						}
					case "SRANDMEMBER":
						mu.Lock()
						member := ""
						for candidate := range sets[command[1]] {
							member = candidate
							break
						}
						mu.Unlock()
						if member == "" {
							_, _ = fmt.Fprint(connection, "$-1\r\n")
						} else {
							_, _ = fmt.Fprintf(connection, "$%d\r\n%s\r\n", len(member), member)
						}
					case "SREM":
						mu.Lock()
						removed := sets[command[1]][command[2]]
						delete(sets[command[1]], command[2])
						mu.Unlock()
						if removed {
							_, _ = fmt.Fprint(connection, ":1\r\n")
						} else {
							_, _ = fmt.Fprint(connection, ":0\r\n")
						}
					case "HGETALL":
						_, _ = fmt.Fprint(connection, "*10\r\n$10\r\ngeneration\r\n$2\r\ng1\r\n$8\r\nrevision\r\n$1\r\n1\r\n$6\r\ncounts\r\n$2\r\n{}\r\n$11\r\nactivatedAt\r\n$20\r\n2026-01-01T00:00:00Z\r\n$11\r\nevaluatedAt\r\n$20\r\n2026-02-03T04:05:06Z\r\n")
					case "HGET":
						// Source metadata; rows come from SMEMBERS.
						_, _ = fmt.Fprint(connection, "$28\r\n{\"availability\":\"available\"}\r\n")
					case "SMEMBERS":
						_, _ = fmt.Fprint(connection, "*0\r\n")
					default:
						_, _ = fmt.Fprint(connection, "-ERR unsupported\r\n")
					}
				}
			}()
			select {
			case <-ctx.Done():
				return
			default:
			}
		}
	}()
	return listener.Addr().String(), func() {
		cancel()
		_ = listener.Close()
	}
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
