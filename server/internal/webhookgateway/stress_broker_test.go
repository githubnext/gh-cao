package webhookgateway_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v4"

	"github.com/githubnext/gh-cao/server/internal/admission"
	"github.com/githubnext/gh-cao/server/internal/simulator"
	"github.com/githubnext/gh-cao/server/internal/tokenbroker"
)

type stressExecution struct {
	id        string
	remaining int
	minted    bool
}

type stressBrokerReport struct {
	Jobs          int `json:"jobs"`
	Exchanges     int `json:"exchanges"`
	Throttled     int `json:"throttled"`
	Invalidations int `json:"invalidations"`
	Revocations   int `json:"revocations"`
}

// Only identity verification and GitHub issuance are synthetic. The production
// broker owns policy checks, rate limits, encrypted contexts and invalidation.
type stressBroker struct {
	t       *testing.T
	service *tokenbroker.Service
	key     []byte
	public  *rsa.PublicKey
	now     func() time.Time
	jobs    map[string]*stressExecution
	tokens  map[string]bool
	report  stressBrokerReport
}

func newStressBroker(t *testing.T, now func() time.Time) *stressBroker {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	b := &stressBroker{
		t: t, now: now, public: &key.PublicKey,
		key:  pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}),
		jobs: make(map[string]*stressExecution), tokens: make(map[string]bool),
	}
	policy := filepath.Join(t.TempDir(), "cao.json")
	body := fmt.Sprintf(`{"control-plane":{"token-broker":{"copilot-requests":{"workflows":[{
		"repository-id":"1001","owner-id":"42",
		"workflow-ref":"simulator/repo-01001/.github/workflows/test.lock.yml@refs/heads/main",
		"workflow-sha":"%s"}]}}}}`, strings.Repeat("a", 40))
	if err := os.WriteFile(policy, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	b.service, err = tokenbroker.New(tokenbroker.Config{
		Audience: "https://broker.example/api/token-broker", PolicyFile: policy,
		AppID: 7, InstallationID: 11, OwnerID: "42", Now: now,
		Verifier: b, KeyReader: b, HTTPClient: &http.Client{Transport: b},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := b.service.Close(context.Background()); err != nil {
			t.Error(err)
		}
		clear(b.key)
	})
	return b
}

func (b *stressBroker) execution(size int) *stressExecution {
	b.report.Jobs++
	job := &stressExecution{id: strconv.Itoa(b.report.Jobs), remaining: size}
	b.jobs[job.id] = job
	return job
}

func (b *stressBroker) Verify(_ context.Context, raw string) (admission.Identity, error) {
	if b.jobs[raw] == nil {
		return admission.Identity{}, admission.ErrDenied
	}
	// A dispatched batch executes in the control repository, not the PR's
	// target repository. Each batch has a distinct run and signed-job identity.
	return admission.Identity{
		Issuer: admission.Issuer, Subject: "repo:simulator/repo-01001:ref:refs/heads/main",
		Repository: "simulator/repo-01001", RepositoryID: "1001", OwnerID: "42",
		RunID: raw, RunAttempt: "1", CheckRunID: raw, Ref: "refs/heads/main",
		Event: "workflow_dispatch", Actor: "simulator", ActorID: "101",
		WorkflowRef: "simulator/repo-01001/.github/workflows/test.lock.yml@refs/heads/main",
		WorkflowSHA: strings.Repeat("a", 40),
	}, nil
}

func (b *stressBroker) PrivateKey(context.Context) ([]byte, error) {
	return append([]byte(nil), b.key...), nil
}

func (b *stressBroker) RoundTrip(r *http.Request) (*http.Response, error) {
	b.t.Helper()
	if r.URL.Scheme != "https" || r.URL.Host != "api.github.com" || r.URL.User != nil ||
		r.URL.RawQuery != "" || r.Header.Get("X-GitHub-Api-Version") != "2026-03-10" {
		return nil, fmt.Errorf("simulation blocked unexpected broker upstream")
	}
	raw := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	response := httptest.NewRecorder()
	if r.Method == http.MethodDelete && r.URL.Path == "/installation/token" {
		if !b.tokens[raw] {
			b.t.Fatal("broker revoked an unknown or already revoked fixture credential")
		}
		delete(b.tokens, raw)
		b.report.Revocations++
		response.WriteHeader(http.StatusNoContent)
		return response.Result(), nil
	}
	signed, err := jwt.Parse(raw, func(*jwt.Token) (any, error) { return b.public, nil },
		jwt.WithValidMethods([]string{"RS256"}))
	if err != nil || !signed.Valid || signed.Claims.(jwt.MapClaims)["iss"] != "7" {
		b.t.Fatal("broker issuer did not authenticate with the fixture App")
	}
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/app/installations/11":
		_, _ = response.WriteString(`{"app_id":7,"account":{"id":42,"type":"Organization"},
			"suspended_at":null,"permissions":{"copilot_requests":"read"}}`)
	case r.Method == http.MethodPost && r.URL.Path == "/app/installations/11/access_tokens":
		body, err := io.ReadAll(io.LimitReader(r.Body, 1024))
		if err != nil || string(body) != `{"permissions":{"copilot_requests":"read"},"repository_ids":[1001]}` {
			b.t.Fatal("broker broadened the fixture credential scope")
		}
		b.report.Exchanges++
		token := fmt.Sprintf("simulator-copilot-%d", b.report.Exchanges)
		b.tokens[token] = true
		response.WriteHeader(http.StatusCreated)
		_, _ = fmt.Fprintf(response, `{"token":%q,"expires_at":%q,
			"permissions":{"copilot_requests":"read","metadata":"read"},
			"repositories":[{"id":1001,"owner":{"id":42}}]}`,
			token, b.now().Add(50*time.Minute).Format(time.RFC3339))
	default:
		return nil, fmt.Errorf("simulation blocked unexpected broker endpoint")
	}
	return response.Result(), nil
}

func (b *stressBroker) call(job *stressExecution, invalidate bool) *httptest.ResponseRecorder {
	b.t.Helper()
	payload := map[string]string{"oidc_token": job.id}
	if invalidate {
		payload["operation"] = "invalidate"
	} else {
		payload["token_type"], payload["provider"] = "copilot-request", "github"
	}
	body, err := json.Marshal(payload)
	if err != nil {
		b.t.Fatal(err)
	}
	request := httptest.NewRequestWithContext(b.t.Context(), http.MethodPost, "/api/token-broker", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	b.service.ServeHTTP(response, request)
	if response.Header().Get("Cache-Control") != "no-store" {
		b.t.Fatal("broker response permitted credential caching")
	}
	return response
}

func (b *stressBroker) exchange(job *stressExecution) bool {
	b.t.Helper()
	if job.minted {
		return true
	}
	response := b.call(job, false)
	if response.Code == http.StatusTooManyRequests {
		b.report.Throttled++
		if response.Body.Len() != 0 {
			b.t.Fatal("broker throttle returned a response body")
		}
		return false
	}
	var credential struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &credential) != nil ||
		!b.tokens[credential.Token] || !credential.ExpiresAt.After(b.now()) {
		b.t.Fatalf("broker exchange failed: status=%d", response.Code)
	}
	job.minted = true
	return true
}

func (b *stressBroker) complete(job *stressExecution) {
	b.t.Helper()
	job.remaining--
	if job.remaining != 0 {
		return
	}
	for range 2 {
		response := b.call(job, true)
		if response.Code != http.StatusNoContent || response.Body.Len() != 0 {
			b.t.Fatalf("broker invalidation failed: status=%d", response.Code)
		}
	}
	response := b.call(job, false)
	if response.Code != http.StatusForbidden || response.Body.Len() != 0 {
		b.t.Fatal("completed broker job minted again")
	}
	b.report.Invalidations++
	delete(b.jobs, job.id)
}

func TestFleetTokenBrokerLifecycleAndThrottling(t *testing.T) {
	clock := simulator.NewManualClock(time.Now().UTC().Truncate(time.Second))
	broker := newStressBroker(t, clock.Now)
	jobs := make([]*stressExecution, 0, 61)
	for range 61 {
		jobs = append(jobs, broker.execution(2))
	}
	for _, job := range jobs[:60] {
		if !broker.exchange(job) {
			t.Fatal("broker failed a batched execution")
		}
		before := broker.report.Exchanges
		if !broker.exchange(job) || broker.report.Exchanges != before {
			t.Fatal("broker reminted a batched execution")
		}
	}
	if broker.exchange(jobs[60]) || broker.report.Exchanges != 60 || broker.report.Throttled != 1 {
		t.Fatal("broker tenant mint ceiling was bypassed")
	}
	broker.complete(jobs[0])
	if broker.report.Revocations != 0 {
		t.Fatal("broker cleaned up before every review in the batch completed")
	}
	broker.complete(jobs[0])
	if broker.report.Revocations != 1 || len(broker.tokens) != 59 {
		t.Fatal("one job's cleanup revoked another execution")
	}
	clock.Advance(time.Minute)
	if !broker.exchange(jobs[60]) {
		t.Fatal("broker did not recover after the mint window reset")
	}
	for _, job := range jobs[1:] {
		broker.complete(job)
		broker.complete(job)
	}
	if broker.report.Exchanges != 61 || broker.report.Invalidations != 61 ||
		broker.report.Revocations != 61 || len(broker.jobs) != 0 || len(broker.tokens) != 0 {
		t.Fatalf("broker retained completed executions: %+v", broker.report)
	}
}
