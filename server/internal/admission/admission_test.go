package admission

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v4"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type fixture struct {
	key      *rsa.PrivateKey
	verifier *Verifier
	claims   jwt.MapClaims
	request  Request
	keysRead int
	outage   bool
	kid      string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := NewVerifier("https://admission.example/api/admission")
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{key: key, verifier: verifier, kid: "test"}
	f.verifier.client.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		f.keysRead++
		if r.URL.String() != Issuer+"/.well-known/jwks" || r.Header.Get("Authorization") != "" {
			t.Fatal("untrusted key source or credential forwarding")
		}
		if f.outage {
			return nil, errors.New("synthetic dependency outage")
		}
		data, _ := json.Marshal(map[string]any{"keys": []any{map[string]string{
			"kid": f.kid, "kty": "RSA", "use": "sig", "alg": "RS256",
			"n": base64.RawURLEncoding.EncodeToString(f.key.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(f.key.E)).Bytes()),
		}}})
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(data))), Header: make(http.Header)}, nil
	})
	now := time.Now().Unix()
	f.claims = jwt.MapClaims{
		"iss": Issuer, "aud": verifier.audience, "sub": "repo:acme/project:ref:refs/heads/main",
		"repository": "acme/project", "repository_id": "1001", "repository_owner_id": "42",
		"run_id": "1234", "run_attempt": "2", "ref": "refs/heads/main", "event_name": "workflow_dispatch",
		"actor": "octocat", "actor_id": "101", "workflow_ref": "acme/project/.github/workflows/triage.lock.yml@refs/heads/main",
		"workflow_sha": strings.Repeat("a", 40), "iat": now - 30, "nbf": now - 30, "exp": now + 300,
	}
	f.request = Request{
		Repo: "acme/project", WorkflowID: f.claims["workflow_ref"].(string),
		WorkflowHash: strings.Repeat("a", 64) + ":" + strings.Repeat("b", 64),
		RunID:        "1234", RunAttempt: "2", Ref: "refs/heads/main",
		EventType: "workflow_dispatch", Actor: "octocat", TriggeringActor: "hubot",
	}
	f.request.WorkflowCallID = "1234-2:" + f.request.WorkflowID
	return f
}

func (f *fixture) token(t *testing.T, claims jwt.MapClaims) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = f.kid
	value, err := token.SignedString(f.key)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

type policyFunc func(context.Context, Identity) (Allowances, error)

func TestVerifierExposesSignedBrokerJobClaimsWithoutChangingAdmissionProfile(t *testing.T) {
	f := newFixture(t)
	f.claims["check_run_id"] = "987654321"
	identity, err := f.verifier.Verify(context.Background(), f.token(t, f.claims))
	if err != nil || identity.CheckRunID != "987654321" || identity.EnvironmentPresent {
		t.Fatal("signed job claim was not preserved")
	}
	key := identity.Key()
	f.claims["check_run_id"] = "987654322"
	f.claims["environment"] = "production"
	identity, err = f.verifier.Verify(context.Background(), f.token(t, f.claims))
	if err != nil || identity.CheckRunID != "987654322" || !identity.EnvironmentPresent || identity.Key() != key {
		t.Fatal("broker claim extraction changed admission identity semantics")
	}
}

func (f policyFunc) Evaluate(ctx context.Context, i Identity) (Allowances, error) { return f(ctx, i) }

type decisionsFunc func(context.Context, string, *Allowances) (Allowances, error)

func (f decisionsFunc) ResolveAdmission(ctx context.Context, key string, a *Allowances) (Allowances, error) {
	return f(ctx, key, a)
}

func (f decisionsFunc) CheckAdmission(context.Context, string) error { return nil }

func TestIdentityClaimMatrix(t *testing.T) {
	f := newFixture(t)
	for _, test := range []struct {
		name  string
		key   string
		value any
	}{
		{"wrong issuer", "iss", "https://attacker.example"},
		{"wrong audience", "aud", "other"},
		{"singleton audience array", "aud", []string{f.verifier.audience}},
		{"multiple audiences", "aud", []string{f.verifier.audience, "other"}},
		{"numeric audience", "aud", 1},
		{"missing audience", "aud", nil},
		{"expired", "exp", time.Now().Unix() - 1},
		{"future nbf", "nbf", time.Now().Unix() + 60},
		{"future issued at", "iat", time.Now().Unix() + 60},
		{"missing issued at", "iat", nil},
		{"string issued at", "iat", strconv.FormatInt(time.Now().Unix()-30, 10)},
		{"string not before", "nbf", strconv.FormatInt(time.Now().Unix()-30, 10)},
		{"string current expiry", "exp", strconv.FormatInt(time.Now().Unix()+60, 10)},
		{"unbounded lifetime", "exp", time.Now().Unix() + 1000},
		{"string expiry", "exp", "9999999999"},
		{"numeric repository id", "repository_id", 1001},
		{"missing owner", "repository_owner_id", nil},
		{"missing actor id", "actor_id", nil},
		{"leading zero run", "run_id", "01234"},
		{"numeric run", "run_id", 1234},
		{"exponent run", "run_id", "1e3"},
		{"signed attempt", "run_attempt", "+2"},
		{"unicode attempt", "run_attempt", "２"},
		{"zero attempt", "run_attempt", "0"},
		{"missing sha", "workflow_sha", nil},
		{"bad sha", "workflow_sha", "main"},
		{"foreign workflow", "workflow_ref", "other/repo/.github/workflows/triage.yml@main"},
		{"reusable workflow", "job_workflow_ref", "other/repo/.github/workflows/reuse.yml@main"},
		{"partial reusable identity", "job_workflow_sha", strings.Repeat("b", 40)},
	} {
		t.Run(test.name, func(t *testing.T) {
			claims := make(jwt.MapClaims)
			for key, value := range f.claims {
				claims[key] = value
			}
			claims[test.key] = test.value
			if _, err := f.verifier.Verify(t.Context(), f.token(t, claims)); !errors.Is(err, ErrDenied) {
				t.Fatalf("wanted denial, got %v", err)
			}
		})
	}
	identity, err := f.verifier.Verify(t.Context(), f.token(t, f.claims))
	if err != nil || identity.ActorID != "101" {
		t.Fatalf("valid identity: %v", err)
	}
	if f.keysRead != 1 {
		t.Fatalf("keys were not cached: %d", f.keysRead)
	}
	renewed := make(jwt.MapClaims)
	for key, value := range f.claims {
		renewed[key] = value
	}
	renewed["iat"] = time.Now().Unix() - 10
	other, err := f.verifier.Verify(t.Context(), f.token(t, renewed))
	if err != nil || other.Key() != identity.Key() {
		t.Fatalf("renewal changed identity: %v", err)
	}
	other.RunAttempt = "3"
	if other.Key() == identity.Key() {
		t.Fatal("attempts shared decision key")
	}
	large := strings.Repeat("9", 100)
	renewed["run_id"] = large
	other, err = f.verifier.Verify(t.Context(), f.token(t, renewed))
	if err != nil || other.RunID != large {
		t.Fatalf("large identifier lost: %v", err)
	}
	for _, raw := range []string{"", "ghs_synthetic", strings.Repeat("x", 17000)} {
		if _, err := f.verifier.Verify(t.Context(), raw); !errors.Is(err, ErrDenied) {
			t.Fatalf("invalid credential: %v", err)
		}
	}
}

func TestKeyRotationAndOutage(t *testing.T) {
	f := newFixture(t)
	if _, err := f.verifier.Verify(t.Context(), f.token(t, f.claims)); err != nil {
		t.Fatal(err)
	}
	rotated, _ := rsa.GenerateKey(rand.Reader, 2048)
	f.key = rotated
	// A known key with a bad signature is denied without network fallback.
	if _, err := f.verifier.Verify(t.Context(), f.token(t, f.claims)); !errors.Is(err, ErrDenied) {
		t.Fatal(err)
	}
	f.kid = "rotated"
	f.verifier.lastRefresh = time.Now().Add(-2 * time.Second)
	if _, err := f.verifier.Verify(t.Context(), f.token(t, f.claims)); err != nil {
		t.Fatalf("unknown rotated key was not refreshed: %v", err)
	}
	f.verifier.refreshed = time.Now().Add(-6 * time.Minute)
	f.verifier.lastRefresh = time.Time{}
	if _, err := f.verifier.Verify(t.Context(), f.token(t, f.claims)); err != nil {
		t.Fatal(err)
	}
	f.outage = true
	f.verifier.refreshed = time.Time{}
	f.verifier.lastRefresh = time.Time{}
	if _, err := f.verifier.Verify(t.Context(), f.token(t, f.claims)); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	f.verifier.gate <- struct{}{}
	cancel()
	if _, err := f.verifier.Verify(cancelled, f.token(t, f.claims)); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	<-f.verifier.gate
}

func TestProtocolUnicodeAndDuplicates(t *testing.T) {
	for _, data := range []string{
		`{"x":"\ud800"}`, `{"x":"\udfff"}`, `{"\ud800":"x"}`,
		`{"x":1,"\u0078":2}`, `{"x":1} {}`, `{"x":[{"a":1,"a":2}]}`,
		string([]byte{'{', '"', 'x', '"', ':', '"', 0xff, '"', '}'}),
	} {
		var value any
		if strictJSON([]byte(data), &value) == nil {
			t.Fatalf("accepted %s", data)
		}
	}
	for _, data := range []string{`{"x":"\ud83d\ude00"}`, `{"x":"\\ud800"}`, `{"x":"réf/😀"}`} {
		var value any
		if err := strictJSON([]byte(data), &value); err != nil {
			t.Fatalf("valid scalar rejected: %s", data)
		}
	}
}

func TestServiceWireContract(t *testing.T) {
	f := newFixture(t)
	token := f.token(t, f.claims)
	body, _ := json.Marshal(f.request)
	evaluations := 0
	proposal := Allowances{Primary: 1000}
	policyErr, storageErr := error(nil), error(nil)
	service := &Service{Verifier: f.verifier,
		Policies: policyFunc(func(_ context.Context, identity Identity) (Allowances, error) {
			evaluations++
			if identity.ActorID != "101" {
				t.Fatal("unverified policy identity")
			}
			return proposal, policyErr
		}),
		Decisions: decisionsFunc(func(_ context.Context, key string, a *Allowances) (Allowances, error) {
			if storageErr != nil {
				return Allowances{}, storageErr
			}
			if len(key) != 64 {
				t.Fatal("invalid key")
			}
			if a == nil {
				return Allowances{}, ErrDenied
			}
			return *a, nil
		}),
	}
	call := func(method, target, value, credential string, modify func(*http.Request)) *httptest.ResponseRecorder {
		r := httptest.NewRequestWithContext(t.Context(), method, target, strings.NewReader(value))
		r.Header.Set("Authorization", "bearer "+credential)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Cache-Control", "no-store")
		if modify != nil {
			modify(r)
		}
		w := httptest.NewRecorder()
		service.ServeHTTP(w, r)
		return w
	}
	for _, optionals := range []struct{ detection, evals *int32 }{
		{}, {detection: int32ptr(1)}, {evals: int32ptr(2147483647)}, {int32ptr(100), int32ptr(50)},
	} {
		proposal.ThreatDetection, proposal.Evals = optionals.detection, optionals.evals
		w := call("POST", "/api/admission", string(body), token, nil)
		if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" ||
			w.Header().Get("Content-Type") != "application/json" {
			t.Fatalf("%d %s", w.Code, w.Body)
		}
		expected, _ := json.Marshal(proposal)
		if w.Body.String() != string(expected) {
			t.Fatalf("allowances changed: %s", w.Body)
		}
	}
	for _, primary := range []int32{1, 2147483647} {
		proposal.Primary = primary
		w := call("POST", "/api/admission", string(body), token, nil)
		if w.Code != 200 {
			t.Fatalf("primary boundary %d: %d", primary, w.Code)
		}
	}
	for _, modify := range []func(*http.Request){
		func(r *http.Request) { r.Header.Set("Traceparent", strings.Repeat("x", 40<<10)) },
		func(r *http.Request) {
			r.Header.Add("Traceparent", "malformed")
			r.Header.Add("Traceparent", "duplicate")
		},
		func(r *http.Request) {
			r.Header.Set("Baggage", strings.Repeat("x", 40<<10))
			r.Header.Set("Tracestate", "ignored")
		},
	} {
		if w := call("POST", "/api/admission", string(body), token, modify); w.Code != 200 {
			t.Fatalf("observability changed admission: %d", w.Code)
		}
	}
	escaped := strings.ReplaceAll(string(body), "/", `\/`)
	if w := call("POST", "/api/admission", escaped, token, nil); w.Code != 200 {
		t.Fatalf("decoded slash mismatch: %d", w.Code)
	}
	for _, test := range []struct {
		name, method, target, body, token string
		change                            func(*http.Request)
	}{
		{"get", "GET", "/api/admission", string(body), token, nil},
		{"query", "POST", "/api/admission?x=1", string(body), token, nil},
		{"empty query", "POST", "/api/admission?", string(body), token, nil},
		{"array", "POST", "/api/admission", "[]", token, nil},
		{"null", "POST", "/api/admission", "null", token, nil},
		{"unknown", "POST", "/api/admission", strings.TrimSuffix(string(body), "}") + `,"extra":"x"}`, token, nil},
		{"duplicate", "POST", "/api/admission", strings.TrimSuffix(string(body), "}") + `,"repo":"acme/project"}`, token, nil},
		{"missing", "POST", "/api/admission", `{}`, token, nil},
		{"incompatible v1.0 nine-field request", "POST", "/api/admission",
			strings.Replace(string(body), `"workflow_hash":"`+f.request.WorkflowHash+`",`, "", 1), token, nil},
		{"numeric run", "POST", "/api/admission", strings.Replace(string(body), `"run_id":"1234"`, `"run_id":1234`, 1), token, nil},
		{"case-insensitive field", "POST", "/api/admission", strings.Replace(string(body), `"repo":`, `"Repo":`, 1), token, nil},
		{"forged repo", "POST", "/api/admission", strings.Replace(string(body), `"repo":"acme/project"`, `"repo":"other/project"`, 1), token, nil},
		{"forged actor", "POST", "/api/admission", strings.Replace(string(body), `"actor":"octocat"`, `"actor":"hubot"`, 1), token, nil},
		{"forged ref", "POST", "/api/admission", strings.Replace(string(body), `"ref":"refs/heads/main"`, `"ref":"refs/heads/other"`, 1), token, nil},
		{"invalid hash", "POST", "/api/admission", strings.Replace(string(body), strings.Repeat("b", 64), "bad", 1), token, nil},
		{"oversized", "POST", "/api/admission", strings.Repeat(" ", 8193), token, nil},
		{"api credential", "POST", "/api/admission", string(body), "ghs_synthetic", nil},
		{"content type", "POST", "/api/admission", string(body), token, func(r *http.Request) { r.Header.Set("Content-Type", "text/plain") }},
		{"duplicate credential", "POST", "/api/admission", string(body), token, func(r *http.Request) { r.Header.Add("Authorization", "bearer other") }},
	} {
		t.Run(test.name, func(t *testing.T) {
			before := evaluations
			w := call(test.method, test.target, test.body, test.token, test.change)
			if w.Code != 403 || w.Body.Len() != 0 || len(w.Header()) != 1 || evaluations != before {
				t.Fatalf("not uniform denial: %d %s %v", w.Code, w.Body, w.Header())
			}
		})
	}
	for _, err := range []error{ErrDenied, ErrUnavailable} {
		policyErr = err
		w := call("POST", "/api/admission", string(body), token, nil)
		want := 403
		if errors.Is(err, ErrUnavailable) {
			want = 503
		}
		if w.Code != want || w.Body.Len() != 0 || len(w.Header()) != 1 {
			t.Fatalf("failure leaked: %d %s %v", w.Code, w.Body, w.Header())
		}
	}
	policyErr, storageErr = nil, ErrUnavailable
	if w := call("POST", "/api/admission", string(body), token, nil); w.Code != 503 {
		t.Fatalf("storage outage: %d", w.Code)
	}
	policyErr = ErrDenied
	if w := call("POST", "/api/admission", string(body), token, nil); w.Code != 403 {
		t.Fatalf("durable budget denial became outage: %d", w.Code)
	}
}

func int32ptr(n int32) *int32 { return &n }

func TestProtectedSnapshotFreshnessAndSpendPrivacy(t *testing.T) {
	f := newFixture(t)
	i, err := f.verifier.Verify(t.Context(), f.token(t, f.claims))
	if err != nil {
		t.Fatal(err)
	}
	used := int64(10)
	entry := Enrollment{
		Repository: i.Repository, RepositoryID: i.RepositoryID, OwnerID: i.OwnerID, Enabled: true,
		Subjects: []string{i.Subject},
		Workflows: []Policy{{WorkflowRef: i.WorkflowRef, WorkflowSHA: i.WorkflowSHA,
			Events: []string{i.Event}, ActorIDs: []string{i.ActorID},
			Allowances: Allowances{Primary: 1000, Evals: int32ptr(50)}}},
		Usage: Usage{Unit: "ai-credits", ObservedAt: time.Now(), WindowStart: time.Now().Add(-time.Hour),
			WindowEnd: time.Now().Add(time.Hour), Used: &used, Limit: 10000},
	}
	file := FilePolicy{Path: filepath.Join(t.TempDir(), "snapshot.json")}
	save := func() {
		t.Helper()
		data, _ := json.Marshal(Snapshot{Version: 1, Repositories: []Enrollment{entry}})
		if err := os.WriteFile(file.Path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	save()
	a, err := file.Evaluate(t.Context(), i)
	if err != nil {
		t.Fatal(err)
	}
	used = 9999
	save()
	b, err := file.Evaluate(t.Context(), i)
	ab, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	if err != nil || string(ab) != string(bb) {
		t.Fatal("spend leaked through allowance")
	}
	used = 10000
	save()
	if _, err := file.Evaluate(t.Context(), i); !errors.Is(err, ErrDenied) {
		t.Fatalf("budget denial: %v", err)
	}
	entry.Usage.ObservedAt = time.Now().Add(-3 * time.Minute)
	save()
	if _, err := file.Evaluate(t.Context(), i); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("stale accounting: %v", err)
	}
	entry.Usage.Used = nil
	save()
	if _, err := file.Evaluate(t.Context(), i); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("missing usage became zero: %v", err)
	}
	entry.Usage.Used = &used
	entry.Usage.ObservedAt = time.Now()
	entry.Enabled = false
	save()
	if _, err := file.Evaluate(t.Context(), i); !errors.Is(err, ErrDenied) {
		t.Fatalf("disabled enrollment: %v", err)
	}
	if err := os.Remove(file.Path); err != nil {
		t.Fatal(err)
	}
	if _, err := file.Evaluate(t.Context(), i); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("missing file: %v", err)
	}
}
