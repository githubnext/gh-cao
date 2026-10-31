package tokenbroker

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v4"

	"github.com/githubnext/gh-cao/server/internal/admission"
)

type verifierFunc func(context.Context, string) (admission.Identity, error)

func (f verifierFunc) Verify(ctx context.Context, raw string) (admission.Identity, error) {
	return f(ctx, raw)
}

type keyFunc func(context.Context) ([]byte, error)

func (f keyFunc) PrivateKey(ctx context.Context) ([]byte, error) { return f(ctx) }

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type fixture struct {
	service      *Service
	identity     admission.Identity
	policy       string
	now          time.Time
	mints        int
	keyReads     int
	apiReads     int
	revoked      []string
	revokeFails  bool
	installation string
	result       func() string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	privateKey := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	f := &fixture{
		now: time.Now().UTC().Truncate(time.Second),
		identity: admission.Identity{
			Issuer: admission.Issuer, Subject: "repo:acme/project:ref:refs/heads/main",
			Repository: "acme/project", RepositoryID: "1001", OwnerID: "42",
			RunID: "1234", RunAttempt: "2", CheckRunID: "8001", Ref: "refs/heads/main",
			Event: "workflow_dispatch", Actor: "octocat", ActorID: "101",
			WorkflowRef: "acme/project/.github/workflows/triage.lock.yml@refs/heads/main",
			WorkflowSHA: strings.Repeat("a", 40),
		},
		installation: `{"app_id":7,"account":{"id":42,"type":"Organization"},"suspended_at":null,"permissions":{"copilot_requests":"read","issues":"write"}}`,
	}
	var suffix [16]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		t.Fatal(err)
	}
	f.policy = ".tokenbroker-test-" + hex.EncodeToString(suffix[:]) + ".json"
	t.Cleanup(func() { _ = os.Remove(f.policy) })
	f.writePolicy(t, f.identity)
	f.result = func() string {
		body, _ := json.Marshal(map[string]any{
			"token":        "test-issued-" + strings.Repeat("x", f.mints),
			"expires_at":   f.now.Add(50 * time.Minute).Format(time.RFC3339),
			"permissions":  map[string]string{"copilot_requests": "read", "metadata": "read"},
			"repositories": []any{map[string]any{"id": 1001, "owner": map[string]any{"id": 42}}},
		})
		return string(body)
	}
	client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Scheme != "https" || r.URL.Host != "api.github.com" {
			t.Errorf("untrusted upstream destination: %s", r.URL)
		}
		if r.Header.Get("X-GitHub-Api-Version") != "2026-03-10" {
			t.Error("issuer did not use the approved GitHub API version")
		}
		status, body := 200, f.installation
		if r.Method == http.MethodDelete {
			if r.URL.Path != "/installation/token" {
				t.Error("invalid revocation endpoint")
			}
			if f.revokeFails {
				status = 503
			} else {
				status = 204
				f.revoked = append(f.revoked, strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
			}
			return testResponse(status, ""), nil
		}
		f.apiReads++
		raw := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		parsed, err := jwt.Parse(raw, func(*jwt.Token) (any, error) { return &key.PublicKey, nil }, jwt.WithValidMethods([]string{"RS256"}))
		if err != nil || !parsed.Valid || parsed.Claims.(jwt.MapClaims)["iss"] != "7" {
			t.Error("issuer did not use configured App-signed JWT")
		}
		if r.Method == http.MethodPost {
			if r.URL.Path != "/app/installations/11/access_tokens" {
				t.Error("invalid token endpoint")
			}
			payload, err := io.ReadAll(r.Body)
			if err != nil || string(payload) != `{"permissions":{"copilot_requests":"read"},"repository_ids":[1001]}` {
				t.Errorf("excessive or invalid mint scope: %s", payload)
			}
			f.mints++
			status, body = 201, f.result()
		} else if r.URL.Path != "/app/installations/11" {
			t.Error("invalid installation endpoint")
		}
		return testResponse(status, body), nil
	})}
	config := Config{
		Audience: "https://broker.example/api/token-broker", PolicyFile: f.policy,
		AppID: 7, InstallationID: 11, OwnerID: "42", HTTPClient: client,
		Verifier: verifierFunc(func(_ context.Context, raw string) (admission.Identity, error) {
			if raw == "unavailable" {
				return admission.Identity{}, admission.ErrUnavailable
			}
			if raw != "valid" {
				return admission.Identity{}, admission.ErrDenied
			}
			return f.identity, nil
		}),
		KeyReader: keyFunc(func(context.Context) ([]byte, error) {
			f.keyReads++
			return append([]byte(nil), privateKey...), nil
		}),
		Now: func() time.Time { return f.now },
	}
	f.service, err = New(config)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func testResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
}

func (f *fixture) writePolicy(t *testing.T, i admission.Identity) {
	t.Helper()
	rule := workflowRule{i.RepositoryID, i.OwnerID, i.WorkflowRef, i.WorkflowSHA}
	data, _ := json.Marshal(map[string]any{"control-plane": map[string]any{"token-broker": map[string]any{"copilot-requests": map[string]any{"workflows": []workflowRule{rule}}}}})
	if err := os.WriteFile(f.policy, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func (f *fixture) call(body string) *httptest.ResponseRecorder {
	r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/token-broker", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	f.service.ServeHTTP(w, r)
	return w
}

const exchangeBody = `{"token_type":"copilot-request","provider":"github","oidc_token":"valid"}`
const invalidateBody = `{"operation":"invalidate","oidc_token":"valid"}`

func TestExchangeAndInvalidation(t *testing.T) {
	f := newFixture(t)
	w := f.call(exchangeBody)
	if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("exchange status/headers: %d %v", w.Code, w.Header())
	}
	var fields map[string]string
	if json.Unmarshal(w.Body.Bytes(), &fields) != nil || len(fields) != 2 || !safeToken(fields["token"]) || fields["expires_at"] == "" {
		t.Fatal("invalid success response")
	}
	if f.mints != 1 || f.keyReads != 1 {
		t.Fatal("missing mint or fresh key read")
	}
	key := executionKey(f.identity)
	if strings.Contains(string(f.service.contexts[key].tokens[0].sealed), fields["token"]) {
		t.Fatal("credential stored unencrypted")
	}
	if w = f.call(invalidateBody); w.Code != 204 || w.Body.Len() != 0 || len(f.revoked) != 1 || f.revoked[0] != fields["token"] {
		t.Fatal("exact credential invalidation failed")
	}
	if w = f.call(invalidateBody); w.Code != 204 || len(f.revoked) != 1 {
		t.Fatal("invalidation not idempotent")
	}
	if w = f.call(exchangeBody); w.Code != 403 || f.mints != 1 {
		t.Fatal("invalidated job may mint again")
	}
}

func TestProtocolRejectsBeforeIssuance(t *testing.T) {
	f := newFixture(t)
	tests := []struct {
		body string
		code int
	}{
		{`{`, 400},
		{`[]`, 400},
		{`null`, 400},
		{`{"token_type":"copilot-task","oidc_token":"valid","provider":"github"}`, 400},
		{`{"token_type":"copilot-request","oidc_token":"valid"}`, 400},
		{`{"token_type":"copilot-request","provider":5,"oidc_token":"valid"}`, 400},
		{`{"token_type":"copilot-request","provider":"github","oidc_token":"valid","extra":""}`, 400},
		{`{"token_type":"copilot-task","oidc_token":"valid","oidc_token":"valid"}`, 400},
		{`{"operation":"invalidate","oidc_token":"valid","token_type":"copilot-task"}`, 400},
		{`{"operation":"unknown","oidc_token":"valid"}`, 400},
		{`{"token_type":"unknown","oidc_token":"valid"}`, 422},
		{`{"token_type":"unknown","oidc_token":4}`, 400},
		{`{"token_type":"unknown","oidc_token":"valid","extra":""}`, 400},
		{`{"token_type":"copilot-task","oidc_token":"invalid"}`, 401},
		{`{"token_type":"copilot-task","oidc_token":"valid"}`, 501},
		{`{"token_type":"copilot-task","oidc_token":"unavailable"}`, 503},
		{`{"token_type":"copilot-request","provider":"https://evil.example","oidc_token":"valid"}`, 403},
		{`{"token_type":"copilot-task","oidc_token":"` + string([]byte{0xff}) + `"}`, 400},
		{strings.Repeat(" ", bodyLimit+1), 413},
	}
	for _, tt := range tests {
		w := f.call(tt.body)
		if w.Code != tt.code || w.Body.Len() != 0 || w.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("rejection: got %d want %d", w.Code, tt.code)
		}
	}
	if f.mints != 0 || f.apiReads != 0 || f.keyReads != 0 {
		t.Fatal("pre-issuance rejection contacted issuer")
	}
	for _, media := range []string{"", "text/plain", "application/json; charset=utf-16"} {
		r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/token-broker", strings.NewReader(exchangeBody))
		r.Header.Set("Content-Type", media)
		w := httptest.NewRecorder()
		f.service.ServeHTTP(w, r)
		if w.Code != 415 {
			t.Errorf("media rejected with %d", w.Code)
		}
	}
}

func TestEndpointAndAuthenticationHeaderCannotSubstituteIdentity(t *testing.T) {
	f := newFixture(t)
	tests := []struct {
		method, path, body string
		status             int
	}{
		{http.MethodGet, "/api/token-broker", "", 405},
		{http.MethodPost, "/api/token-broker/", exchangeBody, 404},
		{http.MethodPost, "/another-route", exchangeBody, 404},
		{http.MethodPost, "/api/token-broker", `{"token_type":"copilot-task","oidc_token":""}`, 400},
	}
	for _, tt := range tests {
		r := httptest.NewRequestWithContext(context.Background(), tt.method, tt.path, strings.NewReader(tt.body))
		r.Header.Set("Authorization", "******")
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		f.service.ServeHTTP(w, r)
		if w.Code != tt.status || w.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("endpoint: got %d want %d", w.Code, tt.status)
		}
	}
	if f.mints != 0 || f.keyReads != 0 {
		t.Fatal("invalid route or substituted identity minted")
	}
}

func TestJobAndPolicyBinding(t *testing.T) {
	changes := []struct {
		name string
		edit func(*admission.Identity)
	}{
		{"missing-job", func(i *admission.Identity) { i.CheckRunID = "" }},
		{"invalid-job", func(i *admission.Identity) { i.CheckRunID = "job-name" }},
		{"owner", func(i *admission.Identity) { i.OwnerID = "99" }},
		{"repository", func(i *admission.Identity) { i.RepositoryID = "99" }},
		{"sha", func(i *admission.Identity) { i.WorkflowSHA = strings.Repeat("b", 40) }},
		{"ref", func(i *admission.Identity) { i.Ref = "refs/heads/evil" }},
		{"subject", func(i *admission.Identity) { i.Subject = "repo:acme/project:environment:prod" }},
		{"environment", func(i *admission.Identity) { i.EnvironmentPresent = true }},
		{"pull-request", func(i *admission.Identity) { i.Event = "pull_request" }},
		{"pull-request-target", func(i *admission.Identity) { i.Event = "pull_request_target" }},
		{"run", func(i *admission.Identity) { i.RunID = "" }},
		{"attempt", func(i *admission.Identity) { i.RunAttempt = "0" }},
		{"repository-name", func(i *admission.Identity) { i.Repository = "other/project" }},
		{"reusable-path", func(i *admission.Identity) {
			i.WorkflowRef = "other/repo/.github/workflows/triage.lock.yml@refs/heads/main"
		}},
	}
	for _, tt := range changes {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			tt.edit(&f.identity)
			if w := f.call(exchangeBody); w.Code != 403 || f.keyReads != 0 {
				t.Fatalf("bad binding minted: %d", w.Code)
			}
		})
	}
	f := newFixture(t)
	if w := f.call(exchangeBody); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if err := os.WriteFile(f.policy, []byte(`{"control-plane":{"token-broker":{"copilot-requests":{"workflows":[]}}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if w := f.call(exchangeBody); w.Code != 403 || f.mints != 1 {
		t.Fatal("policy change did not revoke issuance authority")
	}
	if err := os.Remove(f.policy); err != nil {
		t.Fatal(err)
	}
	if w := f.call(exchangeBody); w.Code != 403 {
		t.Fatal("missing policy allowed mint")
	}
}

func TestRevocationIsolationAndRetry(t *testing.T) {
	f := newFixture(t)
	if w := f.call(exchangeBody); w.Code != 200 {
		t.Fatal(w.Code)
	}
	job := f.identity.CheckRunID
	f.identity.CheckRunID = "8002"
	if w := f.call(invalidateBody); w.Code != 204 || len(f.revoked) != 0 {
		t.Fatal("another job revoked credential")
	}
	f.identity.CheckRunID = job
	f.revokeFails = true
	if w := f.call(invalidateBody); w.Code != 503 || len(f.service.contexts[executionKey(f.identity)].tokens) != 1 {
		t.Fatal("failed revocation discarded credential")
	}
	f.revokeFails = false
	if w := f.call(invalidateBody); w.Code != 204 || len(f.revoked) != 1 {
		t.Fatal("failed revocation not retryable")
	}
}

func TestCloseRetriesAndStopsIssuance(t *testing.T) {
	f := newFixture(t)
	if w := f.call(exchangeBody); w.Code != 200 {
		t.Fatal(w.Code)
	}
	f.revokeFails = true
	if f.service.Close(context.Background()) == nil {
		t.Fatal("shutdown hid failed revocation")
	}
	f.revokeFails = false
	if err := f.service.Close(context.Background()); err != nil || len(f.revoked) != 1 {
		t.Fatal("shutdown retry failed")
	}
	if w := f.call(exchangeBody); w.Code != 503 || f.mints != 1 {
		t.Fatal("closed broker issued credential")
	}
}

func TestConcurrentMintAndInvalidate(t *testing.T) {
	f := newFixture(t)
	var wg sync.WaitGroup
	for range 6 {
		wg.Go(func() {
			w := f.call(exchangeBody)
			if w.Code != 200 && w.Code != 403 && w.Code != 429 {
				t.Errorf("concurrent issuance: %d", w.Code)
			}
		})
	}
	wg.Go(func() {
		if w := f.call(invalidateBody); w.Code != 204 {
			t.Errorf("concurrent invalidation: %d", w.Code)
		}
	})
	wg.Wait()
	if len(f.revoked) != f.mints || len(f.service.contexts[executionKey(f.identity)].tokens) != 0 {
		t.Fatal("mint raced cleanup and orphaned a credential")
	}
}

func TestRateBoundsAndFreshKeys(t *testing.T) {
	f := newFixture(t)
	for range 6 {
		if w := f.call(exchangeBody); w.Code != 200 {
			t.Fatal(w.Code)
		}
	}
	if w := f.call(exchangeBody); w.Code != 429 || f.mints != 6 || f.keyReads != 6 {
		t.Fatal("rate or fresh-key requirements violated")
	}
	f.now = f.now.Add(time.Minute)
	if w := f.call(exchangeBody); w.Code != 200 {
		t.Fatal("rate window did not reset")
	}
	f.now = f.now.Add(2 * time.Hour)
	f.service.prune(f.now)
	if len(f.service.contexts) != 0 {
		t.Fatal("expired state was not pruned")
	}
}

func TestNewFailsClosed(t *testing.T) {
	for _, config := range []Config{
		{}, {Audience: "http://broker.example/api/token-broker"},
		{Audience: "https://user@broker.example/api/token-broker"},
		{Audience: "https://broker.example/api/token-broker#"},
	} {
		if _, err := New(config); err == nil {
			t.Fatal("invalid configuration accepted")
		}
	}
	f := newFixture(t)
	f.service.keyReader = keyFunc(func(context.Context) ([]byte, error) { return nil, errors.New("private upstream error") })
	if w := f.call(exchangeBody); w.Code != 503 || w.Body.Len() != 0 || f.apiReads != 0 {
		t.Fatal("key failure leaked or failed open")
	}
}

func TestSafeTokenAlphabet(t *testing.T) {
	for _, token := range []string{"", "test token", "test\n", "test\r", "\x00", "test-é", "\x7f", strings.Repeat("x", bodyLimit)} {
		if safeToken(token) {
			t.Fatal("unsafe credential accepted")
		}
	}
	if !safeToken("!test-token~") {
		t.Fatal("safe opaque credential denied")
	}
}

func TestNewRejectsNoncanonicalEndpoint(t *testing.T) {
	f := newFixture(t)
	for _, audience := range []string{
		"https://broker.example/api/token-broker?tenant=42",
		"https://broker.example/api/token-broker?",
		"https://broker.example/api/token-broker#",
		"https://user@broker.example/api/token-broker",
		"https://broker.example/api/token-broker/",
		"https://broker.example/api/another-route",
		"http://broker.example/api/token-broker",
	} {
		config := f.service.config
		config.Audience = audience
		if _, err := New(config); err == nil {
			t.Errorf("unsafe public endpoint accepted: %s", audience)
		}
	}
}

func TestNewRejectsEmptyVaultFragment(t *testing.T) {
	f := newFixture(t)
	config := f.service.config
	config.KeyReader = nil
	config.KeyVaultSecretURL = "https://cao-broker.vault.azure.net/secrets/signing-key"
	if _, err := New(config); err != nil {
		t.Fatal("valid protected key URL rejected")
	}
	config.KeyVaultSecretURL += "#"
	if _, err := New(config); err == nil {
		t.Fatal("empty Key Vault fragment accepted")
	}
}

func TestImmutableSubjectMatchesSameExactJobContext(t *testing.T) {
	f := newFixture(t)
	if w := f.call(exchangeBody); w.Code != 200 {
		t.Fatal("legacy subject exchange denied")
	}
	key := executionKey(f.identity)
	f.identity.Subject = "repo:acme@42/project@1001:ref:refs/heads/main"
	if executionKey(f.identity) != key {
		t.Fatal("equivalent subject profiles split exact-job cleanup state")
	}
	if w := f.call(invalidateBody); w.Code != 204 || len(f.revoked) != 1 {
		t.Fatal("immutable subject could not invalidate its exact job")
	}
	f.identity.Subject = "repo:acme@99/project@1001:ref:refs/heads/main"
	if w := f.call(invalidateBody); w.Code != 403 {
		t.Fatal("mismatched immutable owner subject was accepted")
	}
	f.identity.Subject = "repo:acme@42/project@1002:ref:refs/heads/main"
	if w := f.call(invalidateBody); w.Code != 403 {
		t.Fatal("mismatched immutable repository subject was accepted")
	}
}
