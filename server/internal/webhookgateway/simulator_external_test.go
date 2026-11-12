package webhookgateway_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/githubapp"
	"github.com/githubnext/gh-cao/server/internal/simulator"
	wg "github.com/githubnext/gh-cao/server/internal/webhookgateway"
	"github.com/githubnext/gh-cao/server/internal/webhookgatewayhost"
)

func newExternalSimulator(t *testing.T, storage func(*testing.T, wg.QueueOptions) simulatorStorage) (*discoverySimulator, map[string]*simulator.IssueService) {
	t.Helper()
	data, err := os.ReadFile("../../../specs/fixtures/webhook-manifest-external-v2.json")
	if err != nil {
		t.Fatal(err)
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, data); err != nil {
		t.Fatal(err)
	}
	g := newDiscoverySimulatorWithManifest(t, storage, append([]byte("# gh-aw-manifest: "), compact.Bytes()...))
	g.base.snapshot.Policy.Discovery.ActivateSecurity = false
	services := make(map[string]*simulator.IssueService)
	for id, provider := range map[string]string{"jira": "jira", "linear": "linear", "azure": "azure-devops"} {
		service, err := simulator.NewIssueService(provider)
		if err != nil {
			t.Fatal(err)
		}
		services[id] = service
		g.base.snapshot.Policy.ExternalSources = append(g.base.snapshot.Policy.ExternalSources, wg.ExternalSource{
			ID: id, Event: service.Event, Scope: service.Scope, CollectionID: service.CollectionID,
			ResourceURLPrefix: service.ResourceURLPrefix, SecretEnv: "CAO_GATEWAY_SOURCE_" + strings.ToUpper(id),
			DestinationRepositoryIDs: []int64{simulator.GatewayRepositoryID}, ExpiresAt: time.Now().Add(time.Hour),
		})
	}
	g.policies = g.base
	if registry, ok := g.queue.(wg.RegistryStore); ok {
		g.policies = webhookgatewayhost.RegistryLoader{Base: g.base, Store: registry}
	}
	if err := g.discover(t.Context()); err != nil {
		t.Fatal(err)
	}
	policy, err := g.policies.Load(t.Context())
	if err != nil || len(policy.Subscriptions) != 3 {
		t.Fatal("external workflow discovery did not activate all three providers", err)
	}
	return g, services
}

func serveExternalSimulator(t *testing.T, g *discoverySimulator, id string, service *simulator.IssueService) *httptest.Server {
	t.Helper()
	authority := g.authority()
	// No receiving GitHub credential is needed for an external source.
	authority.Receiving = nil
	server := httptest.NewServer(&wg.Ingress{SourceID: id, External: true,
		Policies: g.policies, Queue: g.queue, Authorizer: authority, Secrets: service})
	t.Cleanup(server.Close)
	return server
}

func deliverExternalSimulator(t *testing.T, service *simulator.IssueService, server *httptest.Server, delivery simulator.Delivery, want int) {
	t.Helper()
	status, err := service.DeliverWebhook(t.Context(), server.URL, delivery)
	if status != want || (err == nil) != (want < 300) {
		t.Fatalf("%s webhook HTTP %d want %d error=%v", service.Provider, status, want, err)
	}
}

func TestIssueServiceSimulatorGatewayPostgres(t *testing.T) {
	dsn := simulatorPostgresDSN(t)
	runExternalSimulatorSuite(t, func(t *testing.T, options wg.QueueOptions) simulatorStorage {
		return postgresSimulatorStorage(t, dsn, options)
	})
}

func runExternalSimulatorSuite(t *testing.T, storage func(*testing.T, wg.QueueOptions) simulatorStorage) {
	t.Helper()
	t.Run("native_lifecycle_replay_restart_and_dispatch", func(t *testing.T) {
		g, services := newExternalSimulator(t, storage)
		expected := make(map[string]map[string][]string)
		deliveries := make(map[string][]simulator.Delivery)
		total := 0
		for _, id := range []string{"jira", "linear", "azure"} {
			service := services[id]
			server := serveExternalSimulator(t, g, id, service)
			activities := []string{"opened", "edited", "closed", "reopened", "labeled", "unlabeled", "deleted"}
			if id == "azure" {
				activities = append(activities, "restored")
			}
			expected[service.Event] = make(map[string][]string)
			for _, activity := range activities {
				delivery, err := service.Delivery(activity, 42, activity, time.Now())
				if err != nil {
					t.Fatal(err)
				}
				deliveries[id] = append(deliveries[id], delivery)
				want := http.StatusAccepted
				if id == "linear" {
					want = http.StatusOK
				}
				deliverExternalSimulator(t, service, server, delivery, want)
				deliverExternalSimulator(t, service, server, delivery, want)
				types := []string{activity}
				if slices.Contains([]string{"closed", "reopened", "labeled", "unlabeled"}, activity) {
					types = append(types, "edited")
					slices.Sort(types)
				}
				expected[service.Event][wg.MetadataDigest([]byte(id+"\x00"+activity))] = types
				total++
			}
			server.Close()
		}
		if jobs := g.storage.jobs(t); len(jobs) != total {
			t.Fatalf("distinct native deliveries=%d durable jobs=%d", total, len(jobs))
		}
		g.restart(t)
		for range 3 {
			g.run(t, true)
		}
		g.run(t, false)
		for _, job := range g.storage.jobs(t) {
			data, err := json.Marshal(job)
			if err != nil || bytes.Contains(data, []byte("MUST-NOT-PERSIST")) ||
				job.State != wg.Succeeded || job.Attempt != 1 || job.AcceptedStatus != http.StatusNoContent ||
				job.Event.RepositoryID != 0 || job.Event.InstallationID != 0 {
				t.Fatal("external job lost minimal evidence, external identity, or durable acceptance", err)
			}
		}
		dispatches := g.api.Dispatches()
		if len(dispatches) != 3 {
			t.Fatalf("provider batches mixed or lost: dispatches=%d", len(dispatches))
		}
		for _, dispatch := range dispatches {
			assertExternalSimulatorDispatch(t, dispatch, services, expected)
		}
		if len(expected) != 0 {
			t.Fatal("missing provider dispatches", expected)
		}
		g.restart(t)
		for _, id := range []string{"jira", "linear", "azure"} {
			service := services[id]
			server := serveExternalSimulator(t, g, id, service)
			want := http.StatusAccepted
			if id == "linear" {
				want = http.StatusOK
			}
			deliverExternalSimulator(t, service, server, deliveries[id][0], want)
		}
		g.run(t, false)
		if len(g.storage.jobs(t)) != total || len(g.api.Dispatches()) != 3 {
			t.Fatal("restart lost deduplication or replayed a successful dispatch")
		}
	})

	t.Run("invalid_native_evidence_never_queues", func(t *testing.T) {
		g, services := newExternalSimulator(t, storage)
		for _, id := range []string{"jira", "linear", "azure"} {
			service := services[id]
			server := serveExternalSimulator(t, g, id, service)
			bad := *service
			bad.Secret = strings.Repeat("wrong", 8)
			delivery, err := service.Delivery("bad-auth", 42, "opened", time.Now())
			if err != nil {
				t.Fatal(err)
			}
			deliverExternalSimulator(t, &bad, server, delivery, http.StatusUnauthorized)
			bad = *service
			bad.Scope = "not-enrolled"
			delivery, err = bad.Delivery("bad-scope", 42, "opened", time.Now())
			if err != nil {
				t.Fatal(err)
			}
			deliverExternalSimulator(t, service, server, delivery, http.StatusUnauthorized)
			bad = *service
			bad.ResourceURLPrefix = strings.Replace(service.ResourceURLPrefix, "/simulator/", "/different/", 1)
			if id == "jira" {
				bad.ResourceURLPrefix = "https://different.atlassian.net/rest/api/3/issue/"
			}
			if id == "azure" {
				// Azure references are reconstructed from enrolled collection and work item IDs.
				bad.CollectionID = "not-enrolled"
			}
			delivery, err = bad.Delivery("bad-resource", 42, "opened", time.Now())
			if err != nil {
				t.Fatal(err)
			}
			deliverExternalSimulator(t, service, server, delivery, http.StatusUnauthorized)
			if id == "linear" {
				for _, offset := range []time.Duration{-2 * time.Minute, 2 * time.Minute} {
					delivery, err := service.Delivery("stale", 42, "opened", time.Now().Add(offset))
					if err != nil {
						t.Fatal(err)
					}
					deliverExternalSimulator(t, service, server, delivery, http.StatusUnauthorized)
				}
			}
		}
		if len(g.storage.jobs(t)) != 0 || len(g.api.Dispatches()) != 0 {
			t.Fatal("invalid external evidence reached durable admission or dispatch")
		}
	})

	t.Run("changed_workflow_revokes_all_providers", func(t *testing.T) {
		g, services := newExternalSimulator(t, storage)
		for _, id := range []string{"jira", "linear", "azure"} {
			service := services[id]
			delivery, err := service.Delivery("stale-workflow", 42, "opened", time.Now())
			if err != nil {
				t.Fatal(err)
			}
			want := http.StatusAccepted
			if id == "linear" {
				want = http.StatusOK
			}
			deliverExternalSimulator(t, service, serveExternalSimulator(t, g, id, service), delivery, want)
		}
		if err := g.api.Publish(strings.Repeat("b", 40), map[string][]byte{
			".github/workflows/test.lock.yml": append(bytes.Clone(g.lock), []byte("\n# changed\n")...),
		}); err != nil {
			t.Fatal(err)
		}
		for range 3 {
			g.run(t, true)
		}
		jobs := g.storage.jobs(t)
		if len(jobs) != 3 || len(g.api.Dispatches()) != 0 {
			t.Fatal("stale external workflow dispatched or lost jobs")
		}
		for _, job := range jobs {
			if job.State != wg.Cancelled || job.Attempt != 0 {
				t.Fatal("workflow change did not revoke external dispatch")
			}
		}
	})
}

func assertExternalSimulatorDispatch(t *testing.T, dispatch simulator.GatewayDispatch, services map[string]*simulator.IssueService, expected map[string]map[string][]string) {
	t.Helper()
	var request struct {
		Payload struct {
			Context struct {
				Webhook struct {
					Event      string `json:"event"`
					Deliveries []struct {
						ID      string `json:"delivery_id"`
						Payload struct {
							URL     string              `json:"url"`
							Types   []string            `json:"type"`
							Filters map[string][]string `json:"filters"`
						} `json:"payload"`
					} `json:"deliveries"`
				} `json:"webhook"`
			} `json:"aw_context"`
		} `json:"client_payload"`
	}
	if err := json.Unmarshal(dispatch.Payload, &request); err != nil {
		t.Fatal(err)
	}
	webhook := request.Payload.Context.Webhook
	want, ok := expected[webhook.Event]
	if !ok || len(webhook.Deliveries) != len(want) || dispatch.APIVersion != githubapp.WebhookGatewayAPIVersion ||
		bytes.Contains(dispatch.Payload, []byte("MUST-NOT-PERSIST")) || bytes.Contains(dispatch.Payload, []byte("_gateway")) {
		t.Fatal("invalid external dispatch shape or batch identity")
	}
	var service *simulator.IssueService
	for _, candidate := range services {
		if candidate.Event == webhook.Event {
			service = candidate
		}
	}
	for _, delivery := range webhook.Deliveries {
		types, found := want[delivery.ID]
		if !found || !reflect.DeepEqual(delivery.Payload.Types, types) {
			t.Fatalf("native transition normalized incorrectly: got %v want %v", delivery.Payload.Types, types)
		}
		url := service.ResourceURLPrefix + "42"
		if service.Provider == "linear" {
			url = service.ResourceURLPrefix + "APP-42"
		}
		filter := map[string]string{"jira": "project", "linear": "organization-id", "azure-devops": "project_id"}[service.Provider]
		filters := map[string][]string{filter: {service.Scope}}
		if service.Provider == "azure-devops" && (slices.Contains(types, "closed") || slices.Contains(types, "reopened")) {
			filters["closed-states"] = []string{"Done"}
		}
		if delivery.Payload.URL != url || !reflect.DeepEqual(delivery.Payload.Filters, filters) {
			t.Fatal("incorrect canonical resource identity or filter evidence", delivery.Payload)
		}
		delete(want, delivery.ID)
	}
	delete(expected, webhook.Event)
}
