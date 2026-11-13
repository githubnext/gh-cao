package server

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

type oidcTestTransport func(*http.Request) (*http.Response, error)

func (transport oidcTestTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func TestHostedActionsMCPAuthentication(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	encoded := func(value any) string {
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		return base64.RawURLEncoding.EncodeToString(data)
	}
	sign := func(claims actionsOIDCClaims) string {
		t.Helper()
		message := encoded(map[string]string{"alg": "RS256", "kid": "test-key"}) + "." + encoded(claims)
		digest := sha256.Sum256([]byte(message))
		signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
		if err != nil {
			t.Fatal(err)
		}
		return message + "." + base64.RawURLEncoding.EncodeToString(signature)
	}
	claims := actionsOIDCClaims{
		Issuer: actionsOIDCIssuer, Audience: json.RawMessage(`"https://cao.githubnext.com"`),
		Repository: "githubnext/gh-cao", RepositoryID: "1302952722", RepositoryOwnerID: "89615882",
		Subject: "repo:githubnext@89615882/gh-cao@1302952722:ref:refs/heads/main",
		Ref:     "refs/heads/main",
		Actor:   "OctoCat", IssuedAt: time.Now().Add(-time.Minute).Unix(),
		ExpiresAt: time.Now().Add(4 * time.Minute).Unix(),
	}
	denied := ""
	defaultBranch := "main"
	denyDefaultBranch := false
	probes := 0
	jwks := 0
	installationInventoryChecks := 0
	api := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/.well-known/jwks" {
			jwks++
			_ = json.NewEncoder(response).Encode(map[string]any{"keys": []any{map[string]string{
				"kid": "test-key", "kty": "RSA", "use": "sig", "alg": "RS256",
				"n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
				"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
			}}})
			return
		}
		if request.Header.Get("X-GitHub-Api-Version") != actionsGitHubAPIVersion {
			t.Errorf("GitHub API version = %q, want %q", request.Header.Get("X-GitHub-Api-Version"), actionsGitHubAPIVersion)
			response.WriteHeader(http.StatusBadRequest)
			return
		}
		if request.URL.Path == "/installation/repositories" {
			installationInventoryChecks++
			response.WriteHeader(http.StatusUnauthorized)
			return
		}
		if request.URL.Path == "/repos/githubnext/gh-cao" {
			if request.Header.Get("Authorization") != "Bearer "+testActionsToken || denyDefaultBranch {
				response.WriteHeader(http.StatusForbidden)
				return
			}
			_ = json.NewEncoder(response).Encode(map[string]any{
				"id": 1302952722, "full_name": "githubnext/gh-cao",
				"owner": map[string]any{"id": 89615882}, "default_branch": defaultBranch,
			})
			return
		}
		probes++
		if !strings.HasPrefix(request.URL.Path, "/repos/githubnext/gh-cao/") {
			t.Errorf("invalid permission probe: %s", request.URL)
			response.WriteHeader(http.StatusForbidden)
			return
		}
		if permissionForProbePath(request.URL.Path) == denied {
			response.WriteHeader(http.StatusForbidden)
		}
	}))
	defer api.Close()
	client := &http.Client{Transport: oidcTestTransport(func(request *http.Request) (*http.Response, error) {
		if request.URL.String() == actionsOIDCJWKS {
			request = request.Clone(request.Context())
			request.URL.Host = strings.TrimPrefix(api.URL, "http://")
			request.URL.Scheme = "http"
		}
		return api.Client().Do(request)
	})}
	config := Config{
		ActionsRepository: "githubnext/gh-cao", GitHubAPIURL: api.URL, ActionsHTTPClient: client,
	}
	makeRequest := func(provenance, bearer string) *http.Request {
		request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "https://cao.githubnext.com/mcp", nil)
		request.Header.Set("Authorization", "Bearer "+bearer)
		request.Header.Set("X-GitHub-OIDC-Token", provenance)
		return request
	}
	for _, test := range []struct {
		name       string
		change     func(*actionsOIDCClaims)
		bearer     string
		permission string
		valid      bool
	}{
		{"valid immutable subject", nil, testActionsToken, "", true},
		{"valid legacy subject", func(c *actionsOIDCClaims) {
			c.Subject = "repo:githubnext/gh-cao:ref:refs/heads/main"
		}, testActionsToken, "", true},
		{"wrong audience", func(c *actionsOIDCClaims) { c.Audience = json.RawMessage(`"https://other.example"`) }, testActionsToken, "", false},
		{"multiple audiences", func(c *actionsOIDCClaims) {
			c.Audience = json.RawMessage(`["https://cao.githubnext.com","https://other.example"]`)
		}, testActionsToken, "", false},
		{"wrong issuer", func(c *actionsOIDCClaims) { c.Issuer = "https://other.example" }, testActionsToken, "", false},
		{"wrong repository", func(c *actionsOIDCClaims) { c.Repository = "attacker/repo" }, testActionsToken, "", false},
		{"wrong repository id", func(c *actionsOIDCClaims) { c.RepositoryID = "1" }, testActionsToken, "", false},
		{"wrong repository owner id", func(c *actionsOIDCClaims) { c.RepositoryOwnerID = "1" }, testActionsToken, "", false},
		{"wrong subject", func(c *actionsOIDCClaims) { c.Subject = "repo:attacker/repo:ref:refs/heads/main" }, testActionsToken, "", false},
		{"pull request subject", func(c *actionsOIDCClaims) { c.Subject = "repo:githubnext/gh-cao:pull_request" }, testActionsToken, "", false},
		{"environment subject", func(c *actionsOIDCClaims) { c.Subject = "repo:githubnext/gh-cao:environment:production" }, testActionsToken, "", false},
		{"branch subject", func(c *actionsOIDCClaims) { c.Subject = "repo:githubnext/gh-cao:ref:refs/heads/feature" }, testActionsToken, "", false},
		{"branch ref", func(c *actionsOIDCClaims) { c.Ref = "refs/heads/feature" }, testActionsToken, "", false},
		{"missing ref", func(c *actionsOIDCClaims) { c.Ref = "" }, testActionsToken, "", false},
		{"expired", func(c *actionsOIDCClaims) { c.ExpiresAt = time.Now().Add(-time.Minute).Unix() }, testActionsToken, "", false},
		{"future", func(c *actionsOIDCClaims) { c.NotBefore = time.Now().Add(time.Minute).Unix() }, testActionsToken, "", false},
		{"invalid permissions", nil, testActionsToken, "issues", false},
		{"invalid bearer on public repo", nil, "wrong-token", "", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			modified := claims
			if test.change != nil {
				test.change(&modified)
			}
			denied = test.permission
			actor, err := verifyHostedActionsMCP(t.Context(), config, makeRequest(sign(modified), test.bearer))
			if (err == nil) != test.valid {
				t.Fatalf("actor=%q error=%v, want valid=%t", actor, err, test.valid)
			}
			if test.valid && actor != "octocat" {
				t.Fatalf("actor=%q", actor)
			}
			if !test.valid {
				code := "oidc_invalid"
				switch test.name {
				case "wrong repository id", "wrong repository owner id", "wrong subject",
					"pull request subject", "environment subject", "branch subject", "branch ref", "missing ref":
					code = "provenance_mismatch"
				case "invalid permissions":
					code = "permissions_denied"
				case "invalid bearer on public repo":
					code = "repository_unavailable"
				}
				var refusal *hostedMCPRefusal
				if !errors.As(err, &refusal) || refusal.code != code {
					t.Fatalf("refusal = %v, want code %s", err, code)
				}
			}
		})
	}
	if jwks == 0 || probes == 0 {
		t.Fatalf("expected JWKS and permission probes: jwks=%d probes=%d", jwks, probes)
	}
	if installationInventoryChecks != 0 {
		t.Fatalf("made %d unsupported installation inventory checks", installationInventoryChecks)
	}
	for _, test := range []struct {
		name          string
		defaultBranch string
		deny          bool
		change        func(*actionsOIDCClaims)
		valid         bool
	}{
		{"non-main default branch", "trunk", false, func(c *actionsOIDCClaims) {
			c.Ref = "refs/heads/trunk"
			c.Subject = "repo:githubnext@89615882/gh-cao@1302952722:ref:refs/heads/trunk"
		}, true},
		{"missing default branch", "", false, nil, false},
		{"mismatched signed ref", "trunk", false, nil, false},
		{"mismatched signed subject", "trunk", false, func(c *actionsOIDCClaims) {
			c.Ref = "refs/heads/trunk"
		}, false},
		{"denied branch lookup", "main", true, nil, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			defaultBranch, denyDefaultBranch = test.defaultBranch, test.deny
			modified := claims
			if test.change != nil {
				test.change(&modified)
			}
			actor, err := verifyHostedActionsMCP(t.Context(), config, makeRequest(sign(modified), testActionsToken))
			if (err == nil) != test.valid {
				t.Fatalf("actor=%q error=%v, want valid=%t", actor, err, test.valid)
			}
		})
	}
	denied = ""
	defaultBranch, denyDefaultBranch = "main", false
	good := sign(claims)
	tampered := good[:len(good)-3] + "abc"
	for _, test := range []struct{ name, provenance, bearer string }{
		{"missing provenance", "", testActionsToken},
		{"missing bearer", good, ""},
		{"tampered signature", tampered, testActionsToken},
		{"malformed token", "x.y.z", testActionsToken},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := verifyHostedActionsMCP(t.Context(), config, makeRequest(test.provenance, test.bearer)); err == nil {
				t.Fatal("invalid request was authorized")
			}
		})
	}
	app := &App{
		config: Config{
			ActionsRepository: config.ActionsRepository,
			GitHubAPIURL:      config.GitHubAPIURL, ActionsHTTPClient: config.ActionsHTTPClient,
			Proxy: ProxyPolicy{AllowedHosts: []string{"cao.githubnext.com"}, RequireHTTPS: true},
		},
		oauth: &githubOAuth{}, mcp: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}),
	}
	previousProvider := otel.GetTracerProvider()
	provider := sdktrace.NewTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() {
		otel.SetTracerProvider(previousProvider)
		_ = provider.Shutdown(t.Context())
	})
	handler := app.requireGitHubAccess(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Context().Value(githubActionsActorContextKey{}) != "octocat" {
			t.Error("signed actor was not propagated as the rate-limit identity")
		}
		response.WriteHeader(http.StatusNoContent)
	}))
	authorized := makeRequest(good, testActionsToken)
	authorized.TLS = &tls.ConnectionState{}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, authorized)
	if response.Code != http.StatusNoContent {
		t.Fatalf("hosted MCP request returned %d, want 204", response.Code)
	}
	unauthorized := makeRequest("", testActionsToken)
	unauthorized.TLS = &tls.ConnectionState{}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, unauthorized)
	if response.Code != http.StatusUnauthorized || response.Header().Get("Location") != "" {
		t.Fatalf("unauthorized hosted MCP returned %d (redirect=%q), want 401 without OAuth redirect",
			response.Code, response.Header().Get("Location"))
	}
	var missingCredentialBody map[string]string
	if err := json.Unmarshal(response.Body.Bytes(), &missingCredentialBody); err != nil {
		t.Fatal(err)
	}
	if missingCredentialBody["code"] != "credentials_missing" ||
		missingCredentialBody["traceId"] == "" ||
		missingCredentialBody["traceId"] != response.Header().Get("X-Trace-Id") {
		t.Fatalf("missing credentials 401 diagnostic = %v; trace header = %q",
			missingCredentialBody, response.Header().Get("X-Trace-Id"))
	}
	invalidBearer := makeRequest(good, "invalid-token")
	invalidBearer.TLS = &tls.ConnectionState{}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, invalidBearer)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("invalid bearer on a public repository returned %d, want 401", response.Code)
	}
	var invalidBearerBody map[string]string
	if err := json.Unmarshal(response.Body.Bytes(), &invalidBearerBody); err != nil {
		t.Fatal(err)
	}
	if invalidBearerBody["code"] != "repository_unavailable" ||
		invalidBearerBody["traceId"] == "" ||
		invalidBearerBody["traceId"] != response.Header().Get("X-Trace-Id") {
		t.Fatalf("invalid bearer 401 diagnostic = %v; trace header = %q",
			invalidBearerBody, response.Header().Get("X-Trace-Id"))
	}

	t.Setenv("CAO_SERVER_LOGS_ENABLED", "true")
	fullApp := newMCPTestApp(t, true)
	fullApp.oauth = &githubOAuth{}
	fullApp.config.HostProfile = hostedHostProfile()
	fullApp.config.ActionsHTTPClient = client
	fullApp.config.GitHubAPIURL = api.URL
	fullApp.config.Proxy = ProxyPolicy{
		AllowedHosts: []string{"cao.githubnext.com"}, RequireHTTPS: true, TrustForwarded: true,
	}
	httpServer := httptest.NewServer(fullApp.Handler())
	defer httpServer.Close()
	mcpClient := mcp.NewClient(&mcp.Implementation{Name: "hosted-test", Version: "1"}, nil)
	session, err := mcpClient.Connect(t.Context(), &mcp.StreamableClientTransport{
		Endpoint: httpServer.URL + "/mcp",
		HTTPClient: &http.Client{Transport: oidcTestTransport(func(request *http.Request) (*http.Response, error) {
			authenticated := request.Clone(request.Context())
			authenticated.Header.Set("Authorization", "Bearer "+testActionsToken)
			authenticated.Header.Set("X-GitHub-OIDC-Token", good)
			authenticated.Header.Set("X-Forwarded-Host", "cao.githubnext.com")
			authenticated.Header.Set("X-Forwarded-Proto", "https")
			return http.DefaultTransport.RoundTrip(authenticated)
		})},
	}, &mcp.ClientSessionOptions{ProtocolVersion: "2026-07-28"})
	if err != nil {
		t.Fatalf("hosted MCP initialize: %v", err)
	}
	defer func() { _ = session.Close() }()
	listed, err := session.ListTools(t.Context(), nil)
	if err != nil || listed == nil || len(listed.Tools) != 3 {
		t.Fatalf("hosted MCP tools/list: tools=%v err=%v", listed, err)
	}
	logs, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "cao_logs", Arguments: map[string]any{}})
	if err != nil || logs == nil || logs.IsError {
		t.Fatalf("hosted MCP logs: result=%v err=%v", logs, err)
	}
	logsPayload, err := json.Marshal(logs.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var snapshot serverLogSnapshot
	if err := json.Unmarshal(logsPayload, &snapshot); err != nil || snapshot.Logs == nil || snapshot.Operational.Status == "" {
		t.Fatalf("invalid hosted logs: %s (%v)", logsPayload, err)
	}

	t.Run("named queries use database", func(t *testing.T) {
		database := integrationDatabase(t)
		seedDatabase(t, database, nil)
		fullApp.database = database
		for _, call := range []mcp.CallToolParams{
			{Name: "cao_catalog", Arguments: map[string]any{"kind": "pages"}},
			{Name: "cao_query", Arguments: map[string]any{"id": "campaign-runs", "limit": 1}},
		} {
			result, callErr := session.CallTool(t.Context(), &call)
			if callErr != nil || result == nil || result.IsError {
				t.Fatalf("hosted MCP tools/call %s: result=%v err=%v", call.Name, result, callErr)
			}
		}
	})
}
