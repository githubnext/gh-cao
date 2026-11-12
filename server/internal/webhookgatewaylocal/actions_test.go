package webhookgatewaylocal

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/simulator"
	"github.com/githubnext/gh-cao/server/internal/testutil"
	wg "github.com/githubnext/gh-cao/server/internal/webhookgateway"
)

func TestLocalActionsSignedDeliveryPostgres(t *testing.T) {
	repository := Repository{ID: 1001, FullName: "simulator/repo-00001"}
	repository.Owner.ID, repository.Owner.Type = 42, "Organization"
	simulation, err := NewSimulation(repository, []string{"issues"})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := simulation.Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	store, _, _ := testutil.OperationalPostgres(t)
	queue, err := store.OpenWebhookQueue(t.Context(), policy.Policy.Namespace, policy.Policy.Queue)
	if err != nil {
		t.Fatal(err)
	}
	if err := queue.ApplyRevision(t.Context(), policy.Revision); err != nil {
		t.Fatal(err)
	}
	gateway := httptest.NewServer(&wg.Ingress{SourceID: "local", Secrets: simulation, Policies: simulation, Queue: queue, Authorizer: simulation})
	defer gateway.Close()
	secret, _, err := simulation.WebhookSecrets(t.Context(), "local")
	if err != nil {
		t.Fatal(err)
	}
	action := simulator.Actions{Trace: &simulator.Trace{}}
	delivery := simulator.Delivery{ID: "local-actions-completed", Event: "issues",
		Payload: []byte(`{"action":"opened","repository":{"id":1001,"full_name":"simulator/repo-00001","owner":{"id":42}},"organization":{"id":42},"installation":{"id":1},"issue":{"number":1}}`)}
	for _, signingKey := range []string{"wrong-signing-key", string(secret), string(secret)} {
		status, err := action.DeliverWebhook(t.Context(), gateway.URL+"/webhooks/local", signingKey, delivery)
		if signingKey == "wrong-signing-key" && (err == nil || status != http.StatusUnauthorized) ||
			signingKey != "wrong-signing-key" && (err != nil || status != http.StatusAccepted && status != http.StatusNoContent) {
			t.Fatalf("gateway HTTP status=%d err=%v", status, err)
		}
	}
	worker := &wg.Worker{Policies: simulation, Queue: queue, Authorizer: simulation, Dispatcher: simulation,
		Owner: "local-actions", HTTPTimeout: time.Second, LeaseDuration: time.Minute}
	if worked, err := worker.RunOnce(t.Context()); err != nil || !worked {
		t.Fatalf("local gateway did not dispatch: worked=%t err=%v", worked, err)
	}
	if depth, err := queue.(wg.QueueDepthProvider).QueueDepth(t.Context()); err != nil || depth != 0 {
		t.Fatalf("signed replay left pending work: depth=%d err=%v", depth, err)
	}
}
