package webhookgateway_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/testutil"
	wg "github.com/githubnext/gh-cao/server/internal/webhookgateway"
	"github.com/githubnext/gh-cao/server/internal/webhookgatewaylocal"
)

type batchAuthority struct {
	snapshot wg.PolicySnapshot
	calls    atomic.Int64
}

func (a *batchAuthority) Authorize(ctx context.Context, job wg.Job) (wg.AuthorizationSnapshot, error) {
	a.calls.Add(1)
	snapshot, err := (boundaryAuthorization{a.snapshot.Revisions[job.Subscription.ID]}).Authorize(ctx, job)
	snapshot.CheckedAt = snapshot.CheckedAt.Truncate(time.Millisecond)
	return snapshot, err
}

func (a *batchAuthority) AuthorizeBatch(ctx context.Context, jobs []wg.Job) ([]wg.AuthorizationSnapshot, error) {
	return wg.AuthorizeCompatibleBatch(ctx, a, jobs)
}

type batchDispatch func(context.Context, wg.Subscription, json.RawMessage) wg.DispatchResponse

func (fn batchDispatch) Dispatch(ctx context.Context, s wg.Subscription, payload json.RawMessage) wg.DispatchResponse {
	return fn(ctx, s, payload)
}

type batchRig struct {
	queue      wg.BatchQueue
	policy     boundaryPolicy
	auth       *batchAuthority
	simulation *webhookgatewaylocal.Simulation
	next       int
}

func newBatchRig(t *testing.T, backend string, configure func(*wg.PolicySnapshot)) *batchRig {
	t.Helper()
	if backend == "postgres" && os.Getenv("POSTGRES_URL") == "" {
		t.Skip("POSTGRES_URL is required for batch PostgreSQL conformance")
	}
	repository := webhookgatewaylocal.Repository{ID: 42, FullName: "octo/repo"}
	repository.Owner.ID, repository.Owner.Type = 7, "User"
	simulation, err := webhookgatewaylocal.NewSimulation(repository, []string{"issues", "issue_comment"})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := simulation.Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	number := "/issue/number"
	for i := range snapshot.Subscriptions {
		snapshot.Subscriptions[i].Destination.Fields = []wg.PayloadField{{Name: "number", Path: &number, Required: true}}
	}
	if configure != nil {
		configure(&snapshot)
	}
	for _, sub := range snapshot.Subscriptions {
		metadata, err := json.Marshal(sub)
		if err != nil {
			t.Fatal(err)
		}
		revision := snapshot.Revisions[sub.ID]
		revision.Digest = wg.MetadataDigest(metadata)
		snapshot.Revisions[sub.ID] = revision
	}
	rig := &batchRig{policy: boundaryPolicy{snapshot}, simulation: simulation, auth: &batchAuthority{snapshot: snapshot}}
	options := snapshot.Policy.Queue
	store, _, _ := testutil.OperationalPostgres(t)
	queue, err := store.OpenWebhookQueue(t.Context(), "batches", options)
	if err != nil {
		t.Fatal(err)
	}
	rig.queue = queue.(wg.BatchQueue)
	if err := rig.queue.ApplyRevision(t.Context(), snapshot.Revision); err != nil {
		t.Fatal(err)
	}
	return rig
}

func (r *batchRig) admit(t *testing.T, kind string, number int) {
	t.Helper()
	r.next++
	body := []byte(fmt.Sprintf(`{"action":"opened","installation":{"id":1},"repository":{"id":42},
		"issue":{"number":%d},"secret":"MUST-NOT-PERSIST"}`, number))
	request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-GitHub-Event", kind)
	request.Header.Set("X-GitHub-Delivery", fmt.Sprintf("delivery-%04d", r.next))
	secret, _, err := r.simulation.WebhookSecrets(t.Context(), "local")
	if err != nil {
		t.Fatal(err)
	}
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write(body)
	request.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	response := httptest.NewRecorder()
	ingress := wg.Ingress{SourceID: "local", Policies: r.policy, Queue: r.queue, Authorizer: r.auth, Secrets: r.simulation}
	ingress.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("signed batch member admission: HTTP %d", response.Code)
	}
}

func (r *batchRig) claim(t *testing.T, window time.Duration) []wg.Job {
	t.Helper()
	jobs, err := r.queue.ClaimBatch(t.Context(), wg.ClaimRequest{Owner: "test-worker",
		LeaseDuration: r.policy.snapshot.Policy.Queue.LeaseDuration, BatchWindow: window})
	if err != nil {
		t.Fatal(err)
	}
	return jobs
}

func (r *batchRig) evidence(t *testing.T, jobs []wg.Job) []wg.Authorization {
	t.Helper()
	snapshots, err := r.auth.AuthorizeBatch(t.Context(), jobs)
	if err != nil {
		t.Fatal(err)
	}
	result := make([]wg.Authorization, len(jobs))
	for i, job := range jobs {
		result[i] = wg.Authorization{Revision: job.QueueRevision,
			ApprovedSubscriptionDigest: job.SubscriptionDigest, Snapshot: snapshots[i]}
	}
	return result
}

func TestSpecBatchVectors(t *testing.T) {
	var vectors struct {
		Version int `json:"version"`
		Cases   []struct {
			ID     string `json:"id"`
			Events []struct {
				Kind   string `json:"kind"`
				Number int    `json:"number"`
			} `json:"events"`
			Contexts []int `json:"contexts"`
		} `json:"cases"`
	}
	data, err := os.ReadFile("../../../specs/fixtures/webhook-batch-conformance.json")
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&vectors); err != nil || vectors.Version != 1 || len(vectors.Cases) != 3 {
		t.Fatal("invalid batch conformance vectors", err)
	}
	for _, backend := range []string{"postgres"} {
		for _, vector := range vectors.Cases {
			t.Run(backend+"/"+vector.ID, func(t *testing.T) {
				rig := newBatchRig(t, backend, nil)
				for _, event := range vector.Events {
					rig.admit(t, event.Kind, event.Number)
				}
				rig.auth.calls.Store(0)
				var counts []int
				dispatch := batchDispatch(func(_ context.Context, s wg.Subscription, payload json.RawMessage) wg.DispatchResponse {
					if wg.ValidateBatchDispatchPayload(s, payload, wg.DefaultLimits()) != nil || strings.Contains(string(payload), "MUST-NOT-PERSIST") {
						t.Fatal("unsafe or invalid outbound batch")
					}
					var envelope struct {
						Payload struct {
							Contexts []json.RawMessage `json:"aw_context"`
						} `json:"client_payload"`
					}
					if err := json.Unmarshal(payload, &envelope); err != nil {
						t.Fatal(err)
					}
					counts = append(counts, len(envelope.Payload.Contexts))
					return wg.DispatchResponse{StatusCode: 204, TransportOutcome: "response"}
				})
				worker := wg.Worker{Queue: rig.queue, Policies: rig.policy, Authorizer: rig.auth, Dispatcher: dispatch,
					Owner: "batch-worker", HTTPTimeout: time.Second, LeaseDuration: time.Minute}
				for range 10 {
					worked, err := worker.RunOnce(t.Context())
					if err != nil {
						t.Fatal(err)
					}
					if !worked {
						break
					}
				}
				slices.Sort(counts)
				expected := slices.Clone(vector.Contexts)
				slices.Sort(expected)
				if !slices.Equal(counts, expected) || rig.auth.calls.Load() != int64(len(counts)) {
					t.Fatalf("dispatch contexts=%v expected=%v; fresh authority reads=%d", counts, expected, rig.auth.calls.Load())
				}
				depth, err := rig.queue.(wg.QueueDepthProvider).QueueDepth(t.Context())
				if err != nil || depth != 0 {
					t.Fatalf("batch members left pending: depth=%d err=%v", depth, err)
				}
			})
		}
	}
}
