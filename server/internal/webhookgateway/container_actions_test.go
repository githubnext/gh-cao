package webhookgateway_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/githubnext/gh-cao/server/internal/githubapp"
	"github.com/githubnext/gh-cao/server/internal/simulator"
	wg "github.com/githubnext/gh-cao/server/internal/webhookgateway"
)

// The Node integration owns the Podman container and compilation. This bridge exercises the
// production ingress, durable queue, worker and HTTP dispatcher before handing
// the exact accepted repository_dispatch event to the lightweight runner.
func TestGitHubSimulatorContainerDispatch(t *testing.T) {
	directory := os.Getenv("CAO_CONTAINER_ACTIONS_WORKSPACE")
	eventFile := os.Getenv("CAO_CONTAINER_ACTIONS_EVENT")
	if directory == "" || eventFile == "" {
		t.Skip("run npm run test:integration:actions-runtime")
	}
	workspace, err := os.OpenRoot(directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = workspace.Close() })
	gateway := newDiscoverySimulator(t, localSimulatorStorage)
	// The pinned compiler's webhook manifest conformance fixture supplies the
	// subscription; the sibling sample locks consume its repository_dispatch.
	files := map[string][]byte{".github/workflows/test.lock.yml": gateway.lock}
	for _, name := range []string{"sample", "cao"} {
		filename := ".github/workflows/" + name + ".lock.yml"
		data, err := workspace.ReadFile(filename)
		if err != nil {
			t.Fatal(err)
		}
		files[filename] = data
	}
	if err := gateway.api.Publish(strings.Repeat("b", 40), files); err != nil {
		t.Fatal(err)
	}
	if err := gateway.discover(t.Context()); err != nil {
		t.Fatal(err)
	}
	server := gateway.serve(t)
	delivery := discoveryDelivery(t, "container-actions-delivery", 1)
	deliverDiscovered(t, server, delivery, "incorrect-key", 401)
	deliverDiscovered(t, server, delivery, "boundary-webhook-key", 202)
	deliverDiscovered(t, server, delivery, "boundary-webhook-key", 202)
	gateway.run(t, true)
	gateway.assertJobs(t, 1, wg.Succeeded, 1)
	gateway.restart(t)
	server = gateway.serve(t)
	deliverDiscovered(t, server, delivery, "boundary-webhook-key", 202)
	gateway.run(t, false)
	dispatches := gateway.api.Dispatches()
	if len(dispatches) != 1 || dispatches[0].APIVersion != githubapp.WebhookGatewayAPIVersion {
		t.Fatal("expected exactly one version-pinned HTTP repository dispatch")
	}
	var dispatch struct {
		EventType string          `json:"event_type"`
		Payload   json.RawMessage `json:"client_payload"`
	}
	if err := json.Unmarshal(dispatches[0].Payload, &dispatch); err != nil {
		t.Fatal(err)
	}
	event := map[string]any{
		"action": dispatch.EventType, "client_payload": dispatch.Payload,
		"repository": map[string]any{
			"id": simulator.GatewayRepositoryID, "name": "repo-00001", "full_name": simulator.GatewayRepositoryName,
			"private": true, "visibility": "private", "archived": false, "disabled": false, "default_branch": "main",
			"owner": map[string]any{"id": simulator.GatewayOwnerID, "login": "simulator", "type": "Organization"},
		},
		"sender": map[string]any{"id": 7, "login": "developer", "type": "User"},
	}
	data, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	output, err := os.OpenRoot(filepath.Dir(eventFile))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = output.Close() })
	if err := output.WriteFile(filepath.Base(eventFile), data, 0o600); err != nil {
		t.Fatal(err)
	}
}
