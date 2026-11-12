package admission

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/simulator"
)

// Only this test-owned verifier routes the hard-coded production JWKS URL to
// the ephemeral broker. Production constructors and issuer trust are unchanged.
func localBrokerVerifier(t *testing.T, audience, broker string) *Verifier {
	t.Helper()
	v, err := NewVerifier(audience)
	if err != nil {
		t.Fatal(err)
	}
	local, err := simulator.NewLocalTransport(broker)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(local.CloseIdleConnections)
	base, err := url.Parse(broker)
	if err != nil {
		t.Fatal(err)
	}
	v.client.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != Issuer+"/.well-known/jwks" || r.Header.Get("Authorization") != "" {
			return nil, errors.New("simulation blocked unexpected key request")
		}
		r = r.Clone(r.Context())
		r.URL.Scheme, r.URL.Host = base.Scheme, base.Host
		return local.RoundTrip(r)
	})
	return v
}

func localActionIdentity() (map[string]string, Request) {
	repo := "simulator/repo-00001"
	workflow := repo + "/.github/workflows/triage.lock.yml@refs/heads/main"
	return map[string]string{
		"repository": repo, "repository_id": "1001", "repository_owner_id": "42",
		"sub": "repo:" + repo + ":ref:refs/heads/main", "run_id": "1234", "run_attempt": "1",
		"ref": "refs/heads/main", "event_name": "workflow_dispatch", "actor": "simulator", "actor_id": "101",
		"workflow_ref": workflow, "workflow_sha": strings.Repeat("a", 40),
	}, Request{
		Repo: repo, WorkflowID: workflow, WorkflowHash: strings.Repeat("a", 64) + ":" + strings.Repeat("b", 64),
		WorkflowCallID: "1234-1:" + workflow, RunID: "1234", RunAttempt: "1", Ref: "refs/heads/main",
		EventType: "workflow_dispatch", Actor: "simulator", TriggeringActor: "simulator",
	}
}

func simulatorJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestLocalActionsAdmissionConclusionGateway(t *testing.T) {
	trace := &simulator.Trace{}
	now := time.Now().UTC().Truncate(time.Second)
	api, err := simulator.NewActionsAPI(simulator.Scenario{Name: "local-actions", Repositories: 1, Seed: 1},
		1, func() time.Time { return now }, trace)
	if err != nil {
		t.Fatal(err)
	}
	claims, input := localActionIdentity()
	bootstrap, err := api.OIDC.Register(claims)
	if err != nil {
		t.Fatal(err)
	}
	issuer := httptest.NewServer(api)
	defer issuer.Close()
	var mu sync.Mutex
	policyCalls, approvals, receipts, publications := 0, 0, 0, 0
	denied := false
	decisions := map[string]Allowances{}
	completed := false
	var recorded []byte
	service := &Service{
		Policies: policyFunc(func(_ context.Context, i Identity) (Allowances, error) {
			mu.Lock()
			defer mu.Unlock()
			policyCalls++
			if i.RepositoryID != claims["repository_id"] || i.ActorID != claims["actor_id"] {
				return Allowances{}, ErrDenied
			}
			if denied {
				return Allowances{}, ErrDenied
			}
			return Allowances{Primary: 7}, nil
		}),
		Decisions: decisionsFunc(func(_ context.Context, key string, proposal *Allowances) (Allowances, error) {
			mu.Lock()
			defer mu.Unlock()
			if proposal == nil {
				delete(decisions, key)
				return Allowances{}, ErrDenied
			}
			if value, ok := decisions[key]; ok {
				return value, nil
			}
			approvals++
			decisions[key] = *proposal
			return *proposal, nil
		}),
		ConclusionPolicies: conclusionPolicyFunc(func(_ context.Context, i Identity) error {
			if i.RepositoryID != claims["repository_id"] {
				return ErrDenied
			}
			return nil
		}),
		Conclusions: conclusionPersistence{
			record: func(_ context.Context, _ Identity, r ConclusionRequest) (ConclusionReceipt, error) {
				mu.Lock()
				defer mu.Unlock()
				data, err := json.Marshal(r)
				if err != nil {
					return ConclusionReceipt{}, err
				}
				if recorded != nil && string(recorded) != string(data) {
					return ConclusionReceipt{}, ErrDenied
				}
				if recorded == nil {
					recorded = data
					receipts++
				}
				return ConclusionReceipt{AcceptedAt: now, Populated: completed}, nil
			},
			complete: func(context.Context, Identity) error {
				mu.Lock()
				defer mu.Unlock()
				completed = true
				return nil
			},
		},
		ConclusionRuns: concludedRunsFunc(func(context.Context, Identity, ConclusionRequest, time.Time) error {
			mu.Lock()
			defer mu.Unlock()
			publications++
			return nil
		}),
	}
	mux := http.NewServeMux()
	mux.Handle("/api/admission", service)
	mux.HandleFunc("/api/conclusion", service.Conclude)
	server := httptest.NewUnstartedServer(mux)
	audience := "http://" + server.Listener.Addr().String() + "/api/admission"
	service.Verifier = localBrokerVerifier(t, audience, issuer.URL)
	server.Start()
	defer server.Close()
	action := simulator.Actions{TokenURL: issuer.URL + "/token", Bootstrap: bootstrap, Audience: audience, Trace: trace}
	conclusion := ConclusionRequest{
		Request: input, Session: "{\"type\":\"workflow.job_result\",\"data\":{\"jobId\":\"agent\",\"result\":\"success\"}}\n",
		SessionCompaction: SessionCompaction{Version: 1, IncludedEvents: 1}, UsageArtifactIDs: []string{},
	}
	if err := action.Run(t.Context(), simulator.ActionJob{
		AdmissionURL: audience, ConclusionURL: server.URL + "/api/conclusion",
		Admission: simulatorJSON(t, input), Conclusion: simulatorJSON(t, conclusion),
		Work: func(context.Context) error { return nil },
	}); err != nil {
		t.Fatal(err)
	}
	result, err := action.Post(t.Context(), "admission", audience, simulatorJSON(t, input))
	if err != nil || result.Status != 200 || string(result.Body) != `{"max_ai_credits":7}` {
		t.Fatalf("local admission retry: status=%d err=%v", result.Status, err)
	}
	bad := input
	bad.RunAttempt = "2"
	result, err = action.Post(t.Context(), "admission", audience, simulatorJSON(t, bad))
	if err == nil || result.Status != 403 {
		t.Fatal("unsigned run identity mismatch was admitted")
	}
	mu.Lock()
	denied = true
	mu.Unlock()
	result, err = action.Post(t.Context(), "admission", audience, simulatorJSON(t, input))
	if err == nil || result.Status != 403 {
		t.Fatal("policy change did not revoke admission")
	}
	for range 2 {
		result, err := action.Post(t.Context(), "conclusion_admission", server.URL+"/api/conclusion", simulatorJSON(t, conclusion))
		if err != nil || result.Status != 204 {
			t.Fatalf("local conclusion: status=%d err=%v", result.Status, err)
		}
	}
	mu.Lock()
	if policyCalls != 3 || approvals != 1 || receipts != 1 || publications != 1 {
		t.Errorf("unexpected replay/policy counts: policies=%d approvals=%d receipts=%d publications=%d",
			policyCalls, approvals, receipts, publications)
	}
	mu.Unlock()
	var outcomes []int
	for _, event := range trace.Events() {
		if event.Stage == "actions.admission" || event.Stage == "actions.conclusion_admission" {
			outcomes = append(outcomes, event.Status)
		}
	}
	if !reflect.DeepEqual(outcomes, []int{200, 204, 200, 403, 403, 204, 204}) {
		t.Fatalf("unexpected lifecycle trace: %v", outcomes)
	}
	t.Logf("loopback Actions/OIDC lifecycle: %s", simulatorJSON(t, trace.Events()))
}

func TestLocalActionsBrokenOIDC(t *testing.T) {
	for _, scenario := range []string{"malformed-token", "wrong-signature", "key-outage", "bootstrap-denied"} {
		t.Run(scenario, func(t *testing.T) {
			trace := &simulator.Trace{}
			broker, err := simulator.NewOIDCBroker(time.Now, trace)
			if err != nil {
				t.Fatal(err)
			}
			claims, input := localActionIdentity()
			bootstrap, err := broker.Register(claims)
			if err != nil {
				t.Fatal(err)
			}
			other, err := simulator.NewOIDCBroker(time.Now, trace)
			if err != nil {
				t.Fatal(err)
			}
			issuer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case scenario == "malformed-token" && r.URL.Path == "/token":
					_, _ = w.Write([]byte(`{"value":"not-a-jwt"}`))
				case scenario == "wrong-signature" && r.URL.Path == "/.well-known/jwks":
					other.ServeHTTP(w, r)
				case scenario == "key-outage" && r.URL.Path == "/.well-known/jwks":
					w.WriteHeader(http.StatusServiceUnavailable)
				default:
					broker.ServeHTTP(w, r)
				}
			}))
			defer issuer.Close()
			service := &Service{Verifier: localBrokerVerifier(t, "expected", issuer.URL)}
			server := httptest.NewServer(service)
			defer server.Close()
			if scenario == "bootstrap-denied" {
				bootstrap = "unregistered-bootstrap"
			}
			action := simulator.Actions{TokenURL: issuer.URL + "/token", Bootstrap: bootstrap, Audience: "expected", Trace: trace}
			result, err := action.Post(t.Context(), "admission", server.URL, simulatorJSON(t, input))
			expected := http.StatusForbidden
			switch scenario {
			case "key-outage":
				expected = http.StatusServiceUnavailable
			case "bootstrap-denied":
				expected = 0 // No admission POST is made when token exchange fails.
			}
			if err == nil || result.Status != expected {
				t.Fatalf("broken OIDC did not fail closed: status=%d err=%v", result.Status, err)
			}
		})
	}
}

func TestLocalActionsOIDCFailures(t *testing.T) {
	for _, test := range []struct {
		name     string
		age      time.Duration
		audience string
	}{
		{"expired", -time.Hour, "expected"}, {"future", time.Hour, "expected"}, {"wrong-audience", 0, "wrong"},
	} {
		t.Run(test.name, func(t *testing.T) {
			broker, err := simulator.NewOIDCBroker(func() time.Time { return time.Now().Add(test.age) }, &simulator.Trace{})
			if err != nil {
				t.Fatal(err)
			}
			claims, input := localActionIdentity()
			bootstrap, err := broker.Register(claims)
			if err != nil {
				t.Fatal(err)
			}
			issuer := httptest.NewServer(broker)
			defer issuer.Close()
			service := &Service{Verifier: localBrokerVerifier(t, "expected", issuer.URL)}
			server := httptest.NewServer(service)
			defer server.Close()
			action := simulator.Actions{TokenURL: issuer.URL + "/token", Bootstrap: bootstrap, Audience: test.audience}
			result, err := action.Post(t.Context(), "admission", server.URL, simulatorJSON(t, input))
			if err == nil || result.Status != http.StatusForbidden {
				t.Fatalf("invalid OIDC identity accepted: status=%d err=%v", result.Status, err)
			}
		})
	}
}
