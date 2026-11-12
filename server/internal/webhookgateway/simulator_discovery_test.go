package webhookgateway_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/githubapp"
	"github.com/githubnext/gh-cao/server/internal/githubquota"
	"github.com/githubnext/gh-cao/server/internal/simulator"
	wg "github.com/githubnext/gh-cao/server/internal/webhookgateway"
	"github.com/githubnext/gh-cao/server/internal/webhookgatewayhost"
	"github.com/githubnext/gh-cao/server/internal/webhookgatewaylocal"
)

type discoverySimulator struct {
	api                 *simulator.GatewayAPI
	faults              *simulator.FaultProxy
	receiving, dispatch *githubapp.Client
	lock                []byte
	base                boundaryPolicy
	policies            wg.PolicyProvider
	storage             simulatorStorage
	queue               wg.Queue
	closeQueue          func() error
}

func newDiscoverySimulator(t *testing.T, storage func(*testing.T, wg.QueueOptions) simulatorStorage) *discoverySimulator {
	t.Helper()
	data, err := os.ReadFile("../../../specs/fixtures/webhook-manifest-v2.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Version       int                  `json:"version"`
		Subscriptions []wg.CompilerTrigger `json:"webhook_subscriptions"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	triggers := make([]wg.CompilerTrigger, 0, 1)
	for _, trigger := range manifest.Subscriptions {
		if trigger.Event == "dependabot_alert" {
			triggers = append(triggers, trigger)
		}
	}
	if len(triggers) != 1 {
		t.Fatal("expected exactly one security workflow declaration")
	}
	manifest.Subscriptions = triggers
	data, err = json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	lock := append(append([]byte("# gh-aw-manifest: "), data...), []byte("\nname: synthetic-agentic-workflow\n")...)
	return newDiscoverySimulatorWithManifest(t, storage, lock)
}

func newDiscoverySimulatorWithManifest(t *testing.T, storage func(*testing.T, wg.QueueOptions) simulatorStorage, lock []byte) *discoverySimulator {
	t.Helper()
	api, err := simulator.NewGatewayAPI(map[string][]byte{".github/workflows/test.lock.yml": lock})
	if err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewServer(api)
	t.Cleanup(upstream.Close)
	faults, err := simulator.NewFaultProxy(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(faults.Close)
	server := httptest.NewServer(faults)
	t.Cleanup(server.Close)
	transport, err := simulator.NewLocalTransport(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(transport.CloseIdleConnections)
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	client := func(appID int64) *githubapp.Client {
		t.Helper()
		client, err := githubapp.New(githubapp.Config{
			AppID: appID, BaseURL: server.URL, Transport: transport, RequestTimeout: time.Second,
			PrivateKeyPEM: pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}),
		})
		if err != nil {
			t.Fatal(err)
		}
		return client
	}
	repository := webhookgatewaylocal.Repository{ID: simulator.GatewayRepositoryID, FullName: simulator.GatewayRepositoryName}
	repository.Owner.ID, repository.Owner.Type = simulator.GatewayOwnerID, "Organization"
	simulation, err := webhookgatewaylocal.NewSimulation(repository, []string{"issues"})
	if err != nil {
		t.Fatal(err)
	}
	base, err := simulation.Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	base.Subscriptions, base.Revisions = nil, nil
	base.AllowedOwners = []string{"simulator"}
	base.Policy.Discovery = &wg.DiscoveryPolicy{
		ActivateSecurity: true, IntervalSeconds: 30,
		Repositories: []wg.DiscoveryRepository{{
			Repository: simulator.GatewayRepositoryName, RepositoryID: simulator.GatewayRepositoryID,
			ReceivingInstallationID: 1, DispatchInstallationID: 2,
		}},
	}
	g := &discoverySimulator{api: api, faults: faults, receiving: client(1), dispatch: client(2),
		lock: lock, base: boundaryPolicy{base}, policies: boundaryPolicy{base}, storage: storage(t, base.Policy.Queue)}
	g.open(t)
	t.Cleanup(func() {
		if err := g.closeQueue(); err != nil {
			t.Error(err)
		}
	})
	return g
}

func (g *discoverySimulator) open(t *testing.T) {
	t.Helper()
	g.queue, g.closeQueue = g.storage.open(t)
	if registry, ok := g.queue.(wg.RegistryStore); ok {
		g.policies = webhookgatewayhost.RegistryLoader{Base: g.base, Store: registry}
	}
}

func (g *discoverySimulator) restart(t *testing.T) {
	t.Helper()
	if err := g.closeQueue(); err != nil {
		t.Fatal(err)
	}
	g.open(t)
}

func (g *discoverySimulator) discover(ctx context.Context) error {
	if registry, ok := g.queue.(wg.RegistryStore); ok {
		worker := webhookgatewayhost.DiscoveryWorker{Policies: g.base, Registry: registry, GitHub: g.receiving}
		return worker.RunOnce(ctx)
	}
	// Exercise the real HTTP
	// discovery client and policy compiler, retaining inventory in this test only.
	base := g.base.snapshot
	binding := base.Policy.Discovery.Repositories[0]
	record, scanErr := g.receiving.DiscoverWebhookSubscriptions(ctx, binding, func(githubquota.ResponseQuota) {})
	now := time.Now().UTC()
	if scanErr != nil {
		record = wg.RepositoryInventory{RepositoryID: binding.RepositoryID, Dirty: true}
	}
	record.CheckedAt, record.AttemptedAt = now, now
	records := []wg.RepositoryInventory{record}
	digest, err := wg.RegistryDigest(base.Revision, records)
	if err != nil {
		return err
	}
	snapshot, err := wg.RegistryPolicy(base, wg.RegistrySnapshot{
		Version: 1, Control: base.Revision,
		Revision: wg.QueueRevision{Revision: 2, Digest: digest, Enabled: true}, Records: records,
	}, now)
	if err != nil {
		return err
	}
	g.policies = boundaryPolicy{snapshot}
	return scanErr
}

func (g *discoverySimulator) authority() webhookgatewayhost.LiveAuthority {
	return webhookgatewayhost.LiveAuthority{Loader: g.policies, Queue: g.queue, Receiving: g.receiving, Dispatch: g.dispatch}
}

func (g *discoverySimulator) serve(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(&wg.Ingress{SourceID: wg.CompilerSourceID,
		Secrets: boundarySecrets{}, Policies: g.policies, Queue: g.queue, Authorizer: g.authority()})
	t.Cleanup(server.Close)
	return server
}

func discoveryDelivery(t *testing.T, id string, number int) simulator.Delivery {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"action": "created", "installation": map[string]int{"id": 1},
		"organization": map[string]int{"id": simulator.GatewayOwnerID},
		"repository": map[string]any{"id": simulator.GatewayRepositoryID, "full_name": simulator.GatewayRepositoryName,
			"private": true, "visibility": "private"},
		"alert": map[string]any{"number": number,
			"url":               "https://api.github.com/repos/" + simulator.GatewayRepositoryName + "/dependabot/alerts/" + strconv.Itoa(number),
			"security_advisory": map[string]string{"severity": "critical"}, "secret": "MUST-NOT-PERSIST"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return simulator.Delivery{ID: id, Event: "dependabot_alert", Payload: payload}
}

func deliverDiscovered(t *testing.T, server *httptest.Server, delivery simulator.Delivery, secret string, want int) {
	t.Helper()
	status, err := (simulator.Actions{Trace: &simulator.Trace{}}).DeliverWebhook(t.Context(), server.URL, secret, delivery)
	if status != want || (err == nil) != (want < 300) {
		t.Fatalf("discovered webhook status=%d want=%d error=%v", status, want, err)
	}
}

func (g *discoverySimulator) run(t *testing.T, want bool) {
	t.Helper()
	worker := wg.Worker{Policies: g.policies, Queue: g.queue, Authorizer: g.authority(),
		Dispatcher: githubapp.WebhookGatewayDispatcher{Client: g.dispatch, Receiving: g.receiving},
		Owner:      "discovered-simulator", HTTPTimeout: time.Second, LeaseDuration: time.Minute}
	worked, err := worker.RunOnce(t.Context())
	if err != nil || worked != want {
		t.Fatalf("discovered worker worked=%t want=%t error=%v", worked, want, err)
	}
}

func (g *discoverySimulator) assertJobs(t *testing.T, count int, state wg.JobState, attempts uint32) {
	t.Helper()
	jobs := g.storage.jobs(t)
	if len(jobs) != count {
		t.Fatalf("discovered jobs=%d want=%d", len(jobs), count)
	}
	for _, job := range jobs {
		if job.State != state || job.Attempt != attempts || job.Subscription.Compiler == nil ||
			job.SubscriptionDigest != wg.MetadataDigest(g.lock) || job.Event.SourceID != wg.CompilerSourceID {
			t.Fatalf("discovered job state=%s attempt=%d want=%s/%d", job.State, job.Attempt, state, attempts)
		}
		if state == wg.Succeeded && job.AcceptedStatus != 204 {
			t.Fatal("GitHub dispatch acceptance was not persisted")
		}
		data, err := json.Marshal(job)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(data, []byte("MUST-NOT-PERSIST")) {
			t.Fatal("discovered job retained an unprojected webhook field")
		}
	}
}

func TestGitHubSimulatorDiscoveryPostgres(t *testing.T) {
	dsn := simulatorPostgresDSN(t)
	runDiscoverySimulatorSuite(t, func(t *testing.T, options wg.QueueOptions) simulatorStorage {
		return postgresSimulatorStorage(t, dsn, options)
	})
}

func runDiscoverySimulatorSuite(t *testing.T, storage func(*testing.T, wg.QueueOptions) simulatorStorage) {
	t.Helper()
	t.Run("discover_receive_and_dispatch", func(t *testing.T) {
		g := newDiscoverySimulator(t, storage)
		before := g.serve(t)
		deliverDiscovered(t, before, discoveryDelivery(t, "before-discovery", 1), "boundary-webhook-key", 204)
		if err := g.discover(t.Context()); err != nil {
			t.Fatal("discover agentic workflow", err)
		}
		policy, err := g.policies.Load(t.Context())
		if err != nil || len(policy.Subscriptions) != 1 {
			t.Fatal("discovery did not activate the reviewed security subscription", err)
		}
		server := g.serve(t)
		first, second := discoveryDelivery(t, "discovered-first", 1), discoveryDelivery(t, "discovered-second", 2)
		deliverDiscovered(t, server, first, "incorrect-secret", 401)
		deliverDiscovered(t, server, first, "boundary-webhook-key", 202)
		deliverDiscovered(t, server, first, "boundary-webhook-key", 202)
		deliverDiscovered(t, server, second, "boundary-webhook-key", 202)
		g.assertJobs(t, 2, wg.Ready, 0)
		server.Close()
		before.Close()
		g.restart(t)
		g.run(t, true)
		g.assertJobs(t, 2, wg.Succeeded, 1)
		g.restart(t)
		restarted := g.serve(t)
		deliverDiscovered(t, restarted, first, "boundary-webhook-key", 202)
		g.run(t, false)
		g.assertJobs(t, 2, wg.Succeeded, 1)
		dispatches := g.api.Dispatches()
		if len(dispatches) != 1 || dispatches[0].APIVersion != githubapp.WebhookGatewayAPIVersion ||
			dispatches[0].EventType != policy.Subscriptions[0].Destination.EventType {
			t.Fatal("expected one version-pinned repository dispatch to the discovered workflow")
		}
		if err := wg.ValidateBatchDispatchPayload(policy.Subscriptions[0], dispatches[0].Payload, wg.DefaultLimits()); err != nil {
			t.Fatal("GitHub received an invalid compiler envelope", err)
		}
		var request struct {
			Payload struct {
				Context struct {
					Webhook struct {
						Event      string `json:"event"`
						Deliveries []struct {
							ID string `json:"delivery_id"`
						} `json:"deliveries"`
					} `json:"webhook"`
				} `json:"aw_context"`
			} `json:"client_payload"`
		}
		if err := json.Unmarshal(dispatches[0].Payload, &request); err != nil {
			t.Fatal(err)
		}
		webhook := request.Payload.Context.Webhook
		if webhook.Event != "dependabot_alert" || len(webhook.Deliveries) != 2 ||
			webhook.Deliveries[0].ID != first.ID || webhook.Deliveries[1].ID != second.ID ||
			bytes.Contains(dispatches[0].Payload, []byte("MUST-NOT-PERSIST")) {
			t.Fatal("GitHub dispatch lost delivery identity, batching, or minimal projection")
		}
		stats := g.api.Stats()
		if stats.WorkflowLists == 0 || stats.HeadReads < 2 || stats.WorkflowReads < 2 || stats.TokenMints < 2 {
			t.Fatal("lifecycle bypassed HTTP discovery or fresh compiler preflight")
		}
	})

	t.Run("changed_workflow_revokes_queued_dispatch", func(t *testing.T) {
		g := newDiscoverySimulator(t, storage)
		if err := g.discover(t.Context()); err != nil {
			t.Fatal(err)
		}
		server := g.serve(t)
		deliverDiscovered(t, server, discoveryDelivery(t, "changed-workflow", 1), "boundary-webhook-key", 202)
		if err := g.api.Publish(strings.Repeat("b", 40), map[string][]byte{
			".github/workflows/test.lock.yml": append(bytes.Clone(g.lock), []byte("# changed after admission\n")...),
		}); err != nil {
			t.Fatal(err)
		}
		g.run(t, true)
		g.assertJobs(t, 1, wg.Cancelled, 0)
		if len(g.api.Dispatches()) != 0 {
			t.Fatal("stale discovered artifact reached GitHub dispatch")
		}
	})

	t.Run("discovery_api_failure_does_not_activate", func(t *testing.T) {
		g := newDiscoverySimulator(t, storage)
		if err := g.faults.Inject(simulator.APIFault{
			Path:  "/repos/" + simulator.GatewayRepositoryName + "/contents/.github/workflows",
			Count: 1, Mode: "service-unavailable",
		}); err != nil {
			t.Fatal(err)
		}
		if err := g.discover(t.Context()); err == nil {
			t.Fatal("unavailable workflow listing was treated as complete discovery")
		}
		policy, err := g.policies.Load(t.Context())
		if err != nil || len(policy.Subscriptions) != 0 {
			t.Fatal("failed discovery activated subscriptions", err)
		}
		server := g.serve(t)
		deliverDiscovered(t, server, discoveryDelivery(t, "unavailable-discovery", 1), "boundary-webhook-key", 204)
		g.run(t, false)
		g.assertJobs(t, 0, wg.Ready, 0)
		if len(g.api.Dispatches()) != 0 {
			t.Fatal("failed discovery dispatched work")
		}
	})

	t.Run("dispatch_server_error_is_not_replayed", func(t *testing.T) {
		g := newDiscoverySimulator(t, storage)
		if err := g.discover(t.Context()); err != nil {
			t.Fatal(err)
		}
		server := g.serve(t)
		delivery := discoveryDelivery(t, "ambiguous-http-dispatch", 1)
		deliverDiscovered(t, server, delivery, "boundary-webhook-key", 202)
		path := "/repos/" + simulator.GatewayRepositoryName + "/dispatches"
		if err := g.faults.Inject(simulator.APIFault{Path: path, Count: 1, Mode: "internal-error"}); err != nil {
			t.Fatal(err)
		}
		g.run(t, true)
		g.assertJobs(t, 1, wg.Ambiguous, 1)
		server.Close()
		g.restart(t)
		deliverDiscovered(t, g.serve(t), delivery, "boundary-webhook-key", 202)
		g.run(t, false)
		g.assertJobs(t, 1, wg.Ambiguous, 1)
		if g.faults.RequestCount(path, 0) != 1 {
			t.Fatal("ambiguous HTTP dispatch was retried")
		}
	})
}
