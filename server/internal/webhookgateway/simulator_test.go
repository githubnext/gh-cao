package webhookgateway_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/githubnext/gh-cao/server/internal/simulator"
	wg "github.com/githubnext/gh-cao/server/internal/webhookgateway"
	"github.com/githubnext/gh-cao/server/internal/webhookgatewaylocal"
)

type simulatorGateway struct {
	simulation *webhookgatewaylocal.Simulation
	snapshot   wg.PolicySnapshot
	storage    simulatorStorage
	queue      wg.Queue
	closeQueue func() error
	server     *httptest.Server
	secret     string
	deliveries []simulator.Delivery
	trace      *simulator.Trace
	response   wg.DispatchResponse
	calls      atomic.Int32
	mu         sync.Mutex
	dispatched map[string]int
}

func (g *simulatorGateway) Load(context.Context) (wg.PolicySnapshot, error) {
	return g.snapshot, nil
}

// Only the GitHub write boundary is synthetic; ingress, authorization checks,
// batching, fencing, persistence and response classification are production code.
func (g *simulatorGateway) Dispatch(ctx context.Context, sub wg.Subscription, payload json.RawMessage) wg.DispatchResponse {
	g.calls.Add(1)
	if response := g.simulation.Dispatch(ctx, sub, payload); response.StatusCode != http.StatusNoContent {
		return response
	}
	var request struct {
		ClientPayload struct {
			DeliveryIDs []string `json:"delivery_ids"`
		} `json:"client_payload"`
	}
	if json.Unmarshal(payload, &request) != nil || len(request.ClientPayload.DeliveryIDs) == 0 {
		return wg.DispatchResponse{TransportOutcome: "not-sent"}
	}
	g.mu.Lock()
	for _, id := range request.ClientPayload.DeliveryIDs {
		g.dispatched[id]++
	}
	g.mu.Unlock()
	return g.response
}

func newSimulatorGateway(t *testing.T, storage func(*testing.T, wg.QueueOptions) simulatorStorage, rows int64) *simulatorGateway {
	t.Helper()
	repository := webhookgatewaylocal.Repository{ID: 1001, FullName: "simulator/repo-00001"}
	repository.Owner.ID, repository.Owner.Type = 42, "Organization"
	simulation, err := webhookgatewaylocal.NewSimulation(repository, []string{"issues"})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := simulation.Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if rows > 0 {
		snapshot.Policy.Queue.MaxRows = rows
		snapshot.Policy.Queue.MaxTenantRows = rows
		snapshot.Policy.Queue.MaxSourceRows = rows
		snapshot.Policy.Queue.MaxDestinationRows = rows
	}
	secret, _, err := simulation.WebhookSecrets(t.Context(), "local")
	if err != nil {
		t.Fatal(err)
	}
	g := &simulatorGateway{
		simulation: simulation, snapshot: snapshot, secret: string(secret), trace: &simulator.Trace{},
		storage:    storage(t, snapshot.Policy.Queue),
		response:   wg.DispatchResponse{StatusCode: http.StatusNoContent, TransportOutcome: "response"},
		dispatched: make(map[string]int),
	}
	g.queue, g.closeQueue = g.storage.open(t)
	t.Cleanup(func() {
		if err := g.closeQueue(); err != nil {
			t.Error(err)
		}
	})
	g.serve(t)
	deliveries, err := (simulator.Scenario{
		Name: "webhook-gateway", Repositories: 1, EventsPerRepository: 1,
		IssueEventsPerRepository: 2, Seed: 17,
	}).Generate()
	if err != nil {
		t.Fatal(err)
	}
	for _, delivery := range deliveries {
		if delivery.Event != "issues" {
			continue
		}
		// Collection scenarios identify repositories by name; gateway authority
		// additionally requires the signed immutable repository and owner IDs.
		var payload map[string]any
		if err := json.Unmarshal(delivery.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		payload["repository"] = map[string]any{"id": repository.ID, "full_name": repository.FullName,
			"owner": map[string]any{"id": repository.Owner.ID}}
		payload["organization"] = map[string]any{"id": repository.Owner.ID}
		payload["private_body"] = "never-persist-simulator-raw-body"
		delivery.Payload, err = json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		g.deliveries = append(g.deliveries, delivery)
	}
	if len(g.deliveries) != 2 {
		t.Fatal("simulator did not generate both issue deliveries")
	}
	return g
}

func (g *simulatorGateway) serve(t *testing.T) {
	t.Helper()
	g.server = httptest.NewServer(&wg.Ingress{
		SourceID: "local", Secrets: g.simulation, Policies: g, Queue: g.queue, Authorizer: g.simulation,
	})
	t.Cleanup(g.server.Close)
}

func (g *simulatorGateway) restart(t *testing.T) {
	t.Helper()
	g.server.Close()
	if err := g.closeQueue(); err != nil {
		t.Fatal(err)
	}
	g.queue, g.closeQueue = g.storage.open(t)
	g.serve(t)
}

func (g *simulatorGateway) deliver(t *testing.T, delivery simulator.Delivery, secret string, want int) {
	t.Helper()
	status, err := (simulator.Actions{Trace: g.trace}).DeliverWebhook(t.Context(), g.server.URL+"/webhooks/local", secret, delivery)
	if status != want || (err == nil) != (want < 300) {
		t.Fatalf("webhook status=%d want=%d error=%v", status, want, err)
	}
}

func (g *simulatorGateway) worker(queue wg.Queue, owner string) *wg.Worker {
	return &wg.Worker{Policies: g, Queue: queue, Authorizer: g.simulation, Dispatcher: g,
		Owner: owner, HTTPTimeout: time.Second, LeaseDuration: time.Minute}
}

func (g *simulatorGateway) run(t *testing.T, want bool) {
	t.Helper()
	worked, err := g.worker(g.queue, "simulator-worker").RunOnce(t.Context())
	if err != nil || worked != want {
		t.Fatalf("worker worked=%t want=%t error=%v", worked, want, err)
	}
}

func (g *simulatorGateway) assertJobs(t *testing.T, count int, state wg.JobState, attempts uint32) {
	t.Helper()
	jobs := g.storage.jobs(t)
	if len(jobs) != count {
		t.Fatalf("durable jobs=%d want=%d", len(jobs), count)
	}
	for _, job := range jobs {
		if job.State != state || job.Attempt != attempts {
			t.Fatalf("durable job state=%s attempt=%d want=%s/%d", job.State, job.Attempt, state, attempts)
		}
		if state == wg.Succeeded && job.AcceptedStatus != http.StatusNoContent {
			t.Fatalf("acceptance status not persisted: %d", job.AcceptedStatus)
		}
		data, err := json.Marshal(job)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(data, []byte("never-persist-simulator-raw-body")) {
			t.Fatal("raw webhook body persisted")
		}
		var request struct {
			ClientPayload map[string]json.RawMessage `json:"client_payload"`
		}
		if err := json.Unmarshal(job.Payload, &request); err != nil ||
			string(request.ClientPayload["simulation"]) != "true" || len(request.ClientPayload["_gateway"]) == 0 {
			t.Fatal("durable job lost its allowlisted payload or gateway provenance", err)
		}
	}
}

func TestGitHubSimulatorGatewayPostgres(t *testing.T) {
	dsn := simulatorPostgresDSN(t)
	t.Run("isolated_schema", func(t *testing.T) {
		otherDSN := simulatorPostgresDSN(t)
		var schemas [2]string
		for i, dsn := range []string{dsn, otherDSN} {
			conn, err := pgx.Connect(t.Context(), dsn)
			if err != nil {
				t.Fatal("connect to PostgreSQL simulator schema")
			}
			err = conn.QueryRow(t.Context(), "SELECT current_schema()").Scan(&schemas[i])
			closeErr := conn.Close(t.Context())
			if err != nil || closeErr != nil {
				t.Fatal("inspect PostgreSQL simulator schema")
			}
		}
		if schemas[0] == schemas[1] || schemas[0] == "public" || schemas[1] == "public" {
			t.Fatal("PostgreSQL simulator suites share migration tables")
		}
	})
	runGatewaySimulatorSuite(t, func(t *testing.T, options wg.QueueOptions) simulatorStorage {
		return postgresSimulatorStorage(t, dsn, options)
	})
}

func runGatewaySimulatorSuite(t *testing.T, storage func(*testing.T, wg.QueueOptions) simulatorStorage) {
	t.Helper()
	t.Run("signed_delivery_replay_and_restart", func(t *testing.T) {
		g := newSimulatorGateway(t, storage, 0)
		delivery := g.deliveries[0]
		g.deliver(t, delivery, "incorrect-simulator-secret", http.StatusUnauthorized)
		unrelated := delivery
		unrelated.Payload = bytes.ReplaceAll(delivery.Payload, []byte(`"id":1001`), []byte(`"id":9999`))
		g.deliver(t, unrelated, g.secret, http.StatusNoContent)
		g.assertJobs(t, 0, wg.Ready, 0)
		g.deliver(t, delivery, g.secret, http.StatusAccepted)
		g.deliver(t, delivery, g.secret, http.StatusAccepted)
		conflict := delivery
		conflict.Payload = bytes.ReplaceAll(delivery.Payload, []byte(`"opened"`), []byte(`"edited"`))
		g.deliver(t, conflict, g.secret, http.StatusServiceUnavailable)
		g.assertJobs(t, 1, wg.Ready, 0)
		g.restart(t)
		g.run(t, true)
		g.assertJobs(t, 1, wg.Succeeded, 1)
		g.restart(t)
		g.deliver(t, delivery, g.secret, http.StatusAccepted)
		g.run(t, false)
		g.assertJobs(t, 1, wg.Succeeded, 1)
		if g.calls.Load() != 1 {
			t.Fatal("replay dispatched more than once")
		}
	})

	t.Run("concurrent_replays_and_replica_workers", func(t *testing.T) {
		g := newSimulatorGateway(t, storage, 0)
		var deliveries sync.WaitGroup
		for range 12 {
			deliveries.Go(func() {
				g.deliver(t, g.deliveries[0], g.secret, http.StatusAccepted)
			})
		}
		deliveries.Wait()
		g.deliver(t, g.deliveries[1], g.secret, http.StatusAccepted)
		g.assertJobs(t, 2, wg.Ready, 0)
		replica, closeReplica := g.storage.open(t)
		defer func() {
			if err := closeReplica(); err != nil {
				t.Error(err)
			}
		}()
		var workers sync.WaitGroup
		for i, queue := range []wg.Queue{g.queue, replica} {
			workers.Go(func() {
				owner := []string{"simulator-first", "simulator-replica"}[i]
				if _, err := g.worker(queue, owner).RunOnce(t.Context()); err != nil {
					t.Error(err)
				}
			})
		}
		workers.Wait()
		// A replica can observe the first worker's active batch and defer.
		for range 2 {
			if _, err := g.worker(replica, "simulator-drain").RunOnce(t.Context()); err != nil {
				t.Fatal(err)
			}
		}
		g.assertJobs(t, 2, wg.Succeeded, 1)
		g.mu.Lock()
		defer g.mu.Unlock()
		if len(g.dispatched) != 2 {
			t.Fatalf("dispatched deliveries=%d want=2", len(g.dispatched))
		}
		for _, count := range g.dispatched {
			if count != 1 {
				t.Fatalf("delivery dispatched %d times instead of once", count)
			}
		}
	})

	t.Run("capacity_and_database_outage_fail_closed", func(t *testing.T) {
		// Capacity includes both the durable delivery receipt and its job.
		g := newSimulatorGateway(t, storage, 2)
		g.deliver(t, g.deliveries[0], g.secret, http.StatusAccepted)
		g.deliver(t, g.deliveries[1], g.secret, http.StatusServiceUnavailable)
		g.assertJobs(t, 1, wg.Ready, 0)
		if err := g.closeQueue(); err != nil {
			t.Fatal(err)
		}
		g.deliver(t, g.deliveries[0], g.secret, http.StatusServiceUnavailable)
		if worked, err := g.worker(g.queue, "outage").RunOnce(t.Context()); err == nil || worked {
			t.Fatal("closed database did not fail worker polling")
		}
		g.restart(t)
		g.deliver(t, g.deliveries[0], g.secret, http.StatusAccepted)
		g.run(t, true)
		g.assertJobs(t, 1, wg.Succeeded, 1)
		if g.calls.Load() != 1 {
			t.Fatal("outage caused missing or duplicate dispatch")
		}
	})

	t.Run("policy_disable_cancels_pending_work", func(t *testing.T) {
		g := newSimulatorGateway(t, storage, 0)
		g.deliver(t, g.deliveries[0], g.secret, http.StatusAccepted)
		g.snapshot.Revision = wg.QueueRevision{Revision: 2, Digest: wg.MetadataDigest([]byte("disabled")), Enabled: false}
		g.run(t, false)
		g.assertJobs(t, 1, wg.Cancelled, 0)
		g.restart(t)
		g.deliver(t, g.deliveries[1], g.secret, http.StatusServiceUnavailable)
		g.run(t, false)
		g.assertJobs(t, 1, wg.Cancelled, 0)
		if g.calls.Load() != 0 {
			t.Fatal("disabled policy dispatched queued work")
		}
	})

	for _, test := range []struct {
		name     string
		response wg.DispatchResponse
		state    wg.JobState
	}{
		{"rejected", wg.DispatchResponse{StatusCode: 422, TransportOutcome: "response"}, wg.Rejected},
		{"ambiguous_server_error", wg.DispatchResponse{StatusCode: 500, TransportOutcome: "response"}, wg.Ambiguous},
		{"ambiguous_transport", wg.DispatchResponse{TransportOutcome: "unknown"}, wg.Ambiguous},
		{"rate_limited_retry", wg.DispatchResponse{StatusCode: 429, RetryAfter: "0", TransportOutcome: "response"}, wg.Ready},
	} {
		t.Run(test.name, func(t *testing.T) {
			g := newSimulatorGateway(t, storage, 0)
			g.response = test.response
			g.deliver(t, g.deliveries[0], g.secret, http.StatusAccepted)
			g.run(t, true)
			g.assertJobs(t, 1, test.state, 1)
			g.restart(t)
			g.deliver(t, g.deliveries[0], g.secret, http.StatusAccepted)
			if test.state != wg.Ready {
				g.run(t, false)
				g.assertJobs(t, 1, test.state, 1)
			} else {
				g.response = wg.DispatchResponse{StatusCode: 204, TransportOutcome: "response"}
				g.run(t, false)
				deadline := time.NewTimer(5 * time.Second)
				defer deadline.Stop()
				ticker := time.NewTicker(20 * time.Millisecond)
				defer ticker.Stop()
			retry:
				for {
					select {
					case <-t.Context().Done():
						t.Fatal(t.Context().Err())
					case <-deadline.C:
						t.Fatal("persisted rate-limited job never became eligible")
					case <-ticker.C:
						worked, err := g.worker(g.queue, "retry").RunOnce(t.Context())
						if err != nil {
							t.Fatal(err)
						}
						if worked {
							break retry
						}
					}
				}
				g.assertJobs(t, 1, wg.Succeeded, 2)
			}
			want := int32(1)
			if test.state == wg.Ready {
				want = 2
			}
			if g.calls.Load() != want {
				t.Fatalf("dispatches=%d want=%d", g.calls.Load(), want)
			}
		})
	}
}
