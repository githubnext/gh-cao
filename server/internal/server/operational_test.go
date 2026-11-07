package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/githubnext/gh-cao/server/internal/operational/memory"
)

func memoryOAuthApp(t *testing.T, githubURL string) *App {
	t.Helper()
	store, err := memory.New(memory.Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	profile := hostedHostProfile()
	profile.SingleProcess = true
	profile.SingleReplica = true
	profile.RequiresRedis = false
	app, err := New(t.Context(), focusedTestStore(store), Config{
		Database: constructorDatabase(), SiteDirectory: hostedTestSite(t),
		HostProfile: profile, Listen: "0.0.0.0:8443",
		CertFile: "certificate.pem", KeyFile: "key.pem",
		SingleReplicaConfirmed: true, AllowVolatile: true,
		Proxy:       ProxyPolicy{AllowedHosts: []string{"dashboard.example.com"}, RequireHTTPS: true},
		GitHubOAuth: validOAuthConfig(githubURL),
	})
	if err != nil {
		t.Fatal(err)
	}
	return app
}

func TestMemoryRestartInvalidatesSessionsCSRFAndPendingLogin(t *testing.T) {
	t.Setenv("CAO_REDIS_MAX_BYTES", "irrelevant-invalid-Redis-setting")
	github := fakeGitHub(t, fakeGitHubOptions{membershipState: "active", accessExpiresIn: 3600})
	first := memoryOAuthApp(t, github.URL)
	session, csrf := callbackSession(t, first)
	pendingCookie, pendingState := loginState(t, first)
	second := memoryOAuthApp(t, github.URL)
	request := azureRequest(t, http.MethodGet, "/api/auth/session")
	request.AddCookie(session)
	response := httptest.NewRecorder()
	second.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("old session accepted: %d", response.Code)
	}
	request = azureRequest(t, http.MethodPost, "/auth/logout")
	request.AddCookie(session)
	request.AddCookie(csrf)
	request.Header.Set("X-CSRF-Token", csrf.Value)
	response = httptest.NewRecorder()
	second.Handler().ServeHTTP(response, request)
	if response.Code >= 200 && response.Code < 300 {
		t.Fatalf("old CSRF accepted: %d", response.Code)
	}
	request = azureRequest(t, http.MethodGet, "/auth/callback?code=stale&state="+url.QueryEscape(pendingState))
	request.AddCookie(pendingCookie)
	response = httptest.NewRecorder()
	second.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("pre-restart login callback accepted: %d: %s", response.Code, response.Body.String())
	}
	if _, err := second.oauth.loadSession(t.Context(), session.Value); err == nil {
		t.Fatal("old session was restored")
	}
	if len(github.revoked) != 0 {
		t.Fatal("restart claimed GitHub token revocation")
	}
	newSession, _ := callbackSession(t, second)
	if newSession.Value == session.Value {
		t.Fatal("session identity reused after restart")
	}
}

func TestMemoryRequiresOAuthAndExplicitVolatility(t *testing.T) {
	store, err := memory.New(memory.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	config := Config{
		Database: constructorDatabase(), Listen: "127.0.0.1:8443",
		SiteDirectory: t.TempDir(), AccessToken: strings.Repeat("x", 32),
		SingleReplicaConfirmed: true, AllowVolatile: true,
	}
	if _, err := New(t.Context(), store, config); err == nil {
		t.Fatal("memory accepted bearer-only security")
	}
	config.HostProfile = hostedHostProfile()
	config.HostProfile.SingleProcess = true
	config.HostProfile.SingleReplica = true
	config.AccessToken = ""
	config.GitHubOAuth = validOAuthConfig("https://api.example.com")
	config.Proxy = ProxyPolicy{AllowedHosts: []string{"dashboard.example.com"}, RequireHTTPS: true}
	config.AllowVolatile = false
	if _, err := New(t.Context(), store, config); err == nil {
		t.Fatal("restart loss accepted implicitly")
	}
}

func TestMemoryCannotAdmitNativeCollectionWithoutDurableScope(t *testing.T) {
	store, err := memory.New(memory.Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	_, err = NewCollector(t.Context(), focusedTestStore(store), integrationDatabase(t),
		CollectorConfig{NativeAcquisition: true, AppID: 1, AdmitOnly: true}, "")
	if err == nil || !strings.Contains(err.Error(), "volatile collection requires co-resident workers") {
		t.Fatalf("native admission accepted volatile scope without durable recovery: %v", err)
	}
}
