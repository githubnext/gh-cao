package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestCORSPolicyNormalizesExactOrigins(t *testing.T) {
	policy, err := CORSPolicy{AllowedOrigins: []string{
		"https://Dashboard.Example.com",
		"https://dashboard.example.com:443/",
		"https://tools.example.com:8443",
		"http://localhost:5173",
		"http://[::1]:8080",
	}}.normalize()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"https://dashboard.example.com",
		"https://tools.example.com:8443",
		"http://localhost:5173",
		"http://[::1]:8080",
	}
	if !slices.Equal(policy.AllowedOrigins, want) || policy.MaxAge != defaultCORSMaxAge {
		t.Fatalf("unexpected normalized policy: %+v", policy)
	}
}

func TestCORSPolicyRejectsUnsafeOrigins(t *testing.T) {
	for _, origin := range []string{
		"*", "null", "https://*.example.com", "http://dashboard.example.com",
		"https://dashboard.example.com/path", "https://user@dashboard.example.com",
		"https://dashboard.example.com?x=1", "https://dashboard.example.com#x",
		"ftp://dashboard.example.com", "dashboard.example.com", "",
		"https://bücher.example", "https://example.com.", "https://exa_mple.com",
		"https://a..example.com", "https://.example.com",
	} {
		if _, err := (CORSPolicy{AllowedOrigins: []string{origin}}).normalize(); err == nil {
			t.Errorf("origin %q was accepted", origin)
		}
	}
	if _, err := (CORSPolicy{AllowedOrigins: []string{"https://a.example"}, MaxAge: maxCORSMaxAge + 1}).normalize(); err == nil {
		t.Error("oversized max-age was accepted")
	}
}

func TestClassifyCORSRequestNotCrossOrigin(t *testing.T) {
	policy, err := (CORSPolicy{AllowedOrigins: []string{"https://tools.example.com"}}).normalize()
	if err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*http.Request){
		"no origin header":   func(*http.Request) {},
		"origin not allowed": func(request *http.Request) { request.Header.Set("Origin", "https://evil.example") },
		"invalid host":       func(request *http.Request) { request.Header.Set("Origin", "https://tools.example.com") },
	} {
		t.Run(name, func(t *testing.T) {
			request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/health", nil)
			mutate(request)
			validHost := name != "invalid host"
			if outcome := classifyCORSRequest(request, policy, validHost); outcome != corsOutcomeNotCrossOrigin {
				t.Fatalf("outcome = %s, want %s", outcome, corsOutcomeNotCrossOrigin)
			}
		})
	}
}

func TestClassifyCORSRequestPreflight(t *testing.T) {
	policy, err := (CORSPolicy{AllowedOrigins: []string{"https://tools.example.com"}}).normalize()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name            string
		requestedMethod string
		want            corsOutcome
	}{
		{"get allowed", http.MethodGet, corsOutcomePreflightAllowed},
		{"head allowed", http.MethodHead, corsOutcomePreflightAllowed},
		{"post rejected", http.MethodPost, corsOutcomePreflightRejected},
		{"delete rejected", http.MethodDelete, corsOutcomePreflightRejected},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequestWithContext(context.Background(), http.MethodOptions, "/api/v1/query", nil)
			request.Header.Set("Origin", "https://tools.example.com")
			request.Header.Set("Access-Control-Request-Method", tc.requestedMethod)
			if outcome := classifyCORSRequest(request, policy, true); outcome != tc.want {
				t.Fatalf("outcome = %s, want %s", outcome, tc.want)
			}
		})
	}
}

func TestClassifyCORSRequestSimpleAllowed(t *testing.T) {
	policy, err := (CORSPolicy{AllowedOrigins: []string{"https://tools.example.com"}}).normalize()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		method string
	}{
		{"get request", http.MethodGet},
		{"options without requested method", http.MethodOptions},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequestWithContext(context.Background(), tc.method, "/api/v1/health", nil)
			request.Header.Set("Origin", "https://tools.example.com")
			if outcome := classifyCORSRequest(request, policy, true); outcome != corsOutcomeSimpleAllowed {
				t.Fatalf("outcome = %s, want %s", outcome, corsOutcomeSimpleAllowed)
			}
		})
	}
}

func TestHostPolicyReadsCORSFromCaoJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cao.json")
	document := `{
		"version": 1,
		"control-plane": {
			"scope": {"allowed-owners": ["example"]},
			"web": {
				"host": {
					"target": {"module": "container", "name": "render"},
					"operational-store": {"backend": "postgres"},
					"cors": {
						"allowed-origins": ["https://Tools.Example.com"],
						"max-age": 120
					}
				}
			}
		}
	}`
	if err := os.WriteFile(path, []byte(document), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CAO_POLICY_PATH", path)
	t.Setenv("CAO_POSTGRES_URL", "postgres://127.0.0.1/db?sslmode=disable")
	resolved, err := loadHostPolicyFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(resolved.CORS.AllowedOrigins, []string{"https://tools.example.com"}) ||
		resolved.CORS.MaxAge != 120 {
		t.Fatalf("unexpected CORS policy: %+v", resolved.CORS)
	}

	zeroAge := strings.Replace(document, `"max-age": 120`, `"max-age": 0`, 1)
	if err := os.WriteFile(path, []byte(zeroAge), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadHostPolicyFromEnv(); err == nil || !strings.Contains(err.Error(), "max-age") {
		t.Fatalf("expected explicit zero max-age rejection, got %v", err)
	}

	credentialed := strings.Replace(document, `"max-age": 120`, `"max-age": 120, "allow-credentials": true`, 1)
	if err := os.WriteFile(path, []byte(credentialed), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadHostPolicyFromEnv(); err == nil {
		t.Fatal("credentialed CORS policy was accepted")
	}

	invalid := strings.Replace(document, `"https://Tools.Example.com"`, `"*"`, 1)
	if err := os.WriteFile(path, []byte(invalid), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadHostPolicyFromEnv(); err == nil || !strings.Contains(err.Error(), "cors") {
		t.Fatalf("expected wildcard CORS rejection, got %v", err)
	}
}

func TestSameOriginDefaultEmitsNoCORSHeaders(t *testing.T) {
	github := fakeGitHub(t, fakeGitHubOptions{membershipState: "active", accessExpiresIn: 3600})
	app := newAzureTestApp(t, github.URL)
	app.database = integrationDatabase(t)
	request := azureRequest(t, http.MethodGet, "/api/v1/health")
	request.Header.Set("Origin", "https://evil.example")
	response := httptest.NewRecorder()
	app.Handler().ServeHTTP(response, request)
	for _, header := range []string{"Access-Control-Allow-Origin", "Access-Control-Allow-Credentials"} {
		if response.Header().Get(header) != "" {
			t.Fatalf("same-origin default emitted %s", header)
		}
	}
}

func TestConfiguredCORSAllowsOnlyListedOrigins(t *testing.T) {
	github := fakeGitHub(t, fakeGitHubOptions{membershipState: "active", accessExpiresIn: 3600})
	app := newAzureTestApp(t, github.URL)
	app.config.CORS = CORSPolicy{
		AllowedOrigins: []string{"https://tools.example.com"},
		MaxAge:         120,
	}

	preflight := azureRequest(t, http.MethodOptions, "/api/v1/query")
	preflight.Header.Set("Origin", "https://tools.example.com")
	preflight.Header.Set("Access-Control-Request-Method", http.MethodGet)
	preflight.Header.Set("Access-Control-Request-Headers", "traceparent")
	response := httptest.NewRecorder()
	app.Handler().ServeHTTP(response, preflight)
	if response.Code != http.StatusNoContent ||
		response.Header().Get("Access-Control-Allow-Origin") != "https://tools.example.com" ||
		response.Header().Get("Access-Control-Allow-Credentials") != "" ||
		response.Header().Get("Access-Control-Max-Age") != "120" ||
		response.Header().Get("Access-Control-Allow-Methods") != "GET, HEAD" ||
		strings.Contains(response.Header().Get("Access-Control-Allow-Headers"), "X-CSRF-Token") {
		t.Fatalf("unexpected preflight response %d: %v", response.Code, response.Header())
	}

	unsupported := azureRequest(t, http.MethodOptions, "/api/v1/query")
	unsupported.Header.Set("Origin", "https://tools.example.com")
	for _, method := range []string{http.MethodPost, http.MethodDelete} {
		unsupported.Header.Set("Access-Control-Request-Method", method)
		response = httptest.NewRecorder()
		app.Handler().ServeHTTP(response, unsupported)
		if response.Header().Get("Access-Control-Allow-Methods") != "" {
			t.Fatalf("unsupported preflight method %s was allowed: %v", method, response.Header())
		}
	}

	misdirected := azureRequest(t, http.MethodOptions, "/api/v1/query")
	misdirected.Host = "attacker.example"
	misdirected.Header.Set("X-Forwarded-Host", "attacker.example")
	misdirected.Header.Set("Origin", "https://tools.example.com")
	misdirected.Header.Set("Access-Control-Request-Method", http.MethodPost)
	response = httptest.NewRecorder()
	app.Handler().ServeHTTP(response, misdirected)
	if response.Code != http.StatusMisdirectedRequest ||
		response.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("misdirected preflight was answered: %d %v", response.Code, response.Header())
	}

	denied := azureRequest(t, http.MethodOptions, "/api/v1/query")
	denied.Header.Set("Origin", "https://evil.example")
	denied.Header.Set("Access-Control-Request-Method", http.MethodPost)
	response = httptest.NewRecorder()
	app.Handler().ServeHTTP(response, denied)
	if response.Header().Get("Access-Control-Allow-Origin") != "" ||
		!slices.Contains(response.Header().Values("Vary"), "Origin") {
		t.Fatalf("unlisted origin received CORS headers: %v", response.Header())
	}

	unauthenticated := azureRequest(t, http.MethodGet, "/api/v1/diagnostics")
	unauthenticated.Header.Set("Origin", "https://tools.example.com")
	unauthenticated.Header.Set("Sec-Fetch-Mode", "cors")
	response = httptest.NewRecorder()
	app.Handler().ServeHTTP(response, unauthenticated)
	if response.Code != http.StatusUnauthorized ||
		response.Header().Get("Access-Control-Allow-Origin") != "https://tools.example.com" {
		t.Fatalf("expected readable 401 for allowed origin, got %d: %v", response.Code, response.Header())
	}
}

func TestConfiguredCORSNeverSharesCredentialedResponses(t *testing.T) {
	github := fakeGitHub(t, fakeGitHubOptions{membershipState: "active", accessExpiresIn: 3600})
	app := newAzureTestApp(t, github.URL)
	app.database = integrationDatabase(t)
	app.config.CORS = CORSPolicy{AllowedOrigins: []string{"https://tools.example.com"}, MaxAge: 120}
	for _, path := range []string{"/api/auth/session", "/api/v1/health"} {
		request := azureRequest(t, http.MethodGet, path)
		request.Header.Set("Origin", "https://tools.example.com")
		request.Header.Set("Sec-Fetch-Mode", "cors")
		request.AddCookie(&http.Cookie{
			Name: sessionCookieName, Value: "any-session", Secure: true,
			HttpOnly: true, SameSite: http.SameSiteLaxMode,
		})
		response := httptest.NewRecorder()
		app.Handler().ServeHTTP(response, request)
		if response.Header().Get("Access-Control-Allow-Credentials") != "" {
			t.Fatalf("%s granted credentialed CORS access: %v", path, response.Header())
		}
	}
}

func TestUnauthenticatedSubresourceIsNotRedirectedToGitHub(t *testing.T) {
	github := fakeGitHub(t, fakeGitHubOptions{membershipState: "active", accessExpiresIn: 3600})
	app := newAzureTestApp(t, github.URL)

	for _, mode := range []string{"cors", "no-cors", "same-origin"} {
		request := azureRequest(t, http.MethodGet, "/manifest.webmanifest")
		request.Header.Set("Sec-Fetch-Mode", mode)
		response := httptest.NewRecorder()
		app.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusUnauthorized || response.Header().Get("Location") != "" {
			t.Fatalf("mode %s: expected 401 without redirect, got %d location=%q",
				mode, response.Code, response.Header().Get("Location"))
		}
	}

	for _, mode := range []string{"navigate", ""} {
		request := azureRequest(t, http.MethodGet, "/")
		if mode != "" {
			request.Header.Set("Sec-Fetch-Mode", mode)
		}
		response := httptest.NewRecorder()
		app.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusFound || response.Header().Get("Location") != "/auth/login" {
			t.Fatalf("mode %q: expected login redirect, got %d location=%q",
				mode, response.Code, response.Header().Get("Location"))
		}
	}

	for _, destination := range []string{"iframe", "frame", "object", "embed"} {
		for _, path := range []string{"/", "/auth/login"} {
			request := azureRequest(t, http.MethodGet, path)
			request.Header.Set("Sec-Fetch-Mode", "navigate")
			request.Header.Set("Sec-Fetch-Dest", destination)
			response := httptest.NewRecorder()
			app.Handler().ServeHTTP(response, request)
			if response.Code != http.StatusUnauthorized || response.Header().Get("Location") != "" ||
				len(response.Result().Cookies()) != 0 {
				t.Fatalf("%s with destination %s: expected 401 without redirect or state cookie, got %d headers=%v",
					path, destination, response.Code, response.Header())
			}
		}
	}

	for _, path := range []string{"/", "/auth/login"} {
		request := azureRequest(t, http.MethodGet, path)
		request.Header.Set("Sec-Fetch-Mode", "navigate")
		request.Header.Set("Sec-Fetch-Dest", "document")
		response := httptest.NewRecorder()
		app.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusFound {
			t.Fatalf("%s top-level navigation returned %d, want redirect", path, response.Code)
		}
	}
}
