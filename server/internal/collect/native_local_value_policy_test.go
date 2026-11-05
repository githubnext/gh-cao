package collect

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/githubnext/gh-cao/server/internal/query"
)

func TestNativeLocalValuesTracksComposedBasePolicyAuthority(t *testing.T) {
	p, root := nativeLocalValueFixture(t)
	base := filepath.Join(root, "base.json")
	if err := os.Rename(p.PolicyPath, base); err != nil {
		t.Fatal(err)
	}
	profile := []byte(`{"extends":"base.json","control-plane":{"web":{"host":{
		"target":{"module":"container","name":"fixture"},"redis":{"module":"generic"}}}}}`)
	if err := os.WriteFile(p.PolicyPath, profile, 0o600); err != nil { // #nosec G703 -- generated profile in the test-owned catalog.
		t.Fatal(err)
	}
	resolved, err := p.resolvedNativeValuePolicy(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(resolved, &document); err != nil {
		t.Fatal(err)
	}
	if document["extends"] != nil || document["control-plane"].(map[string]any)["scope"] == nil {
		t.Fatal("local Value snapshot lost composed rollout authority")
	}
	if err := p.CollectNativeLocalValues(t.Context()); err != nil {
		t.Fatalf("composed reviewed policy could not prepare private local inputs: %v", err)
	}
	completed, err := p.State.OperationalState(t.Context(), nativeLocalValueCompletedKey)
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(base) // #nosec G304 -- generated reviewed base in the test-owned catalog.
	if err != nil {
		t.Fatal(err)
	}
	var baseDocument map[string]any
	if err := json.Unmarshal(content, &baseDocument); err != nil {
		t.Fatal(err)
	}
	control := baseDocument["control-plane"].(map[string]any)
	control["campaigns"].(map[string]any)["activity"].(map[string]any)["enabled"] = false
	content, err = json.Marshal(baseDocument)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(base, content, 0o600); err != nil { // #nosec G703 -- generated reviewed base in the test-owned catalog.
		t.Fatal(err)
	}
	if err := p.nativeLocalPolicyCurrent(t.Context(), resolved); err == nil {
		t.Fatal("unchanged profile concealed a revoked base-policy authority")
	}
	if err := p.CollectNativeLocalValues(t.Context()); err != nil {
		t.Fatal(err)
	}
	current, err := p.State.OperationalState(t.Context(), nativeLocalValueCompletedKey)
	if err != nil || bytes.Equal(completed, current) {
		t.Fatalf("raw-profile cache concealed the base-policy change: %q %v", current, err)
	}
	result, _, err := p.Data.ExecuteSQLPlan(t.Context(),
		[]query.Definition{{Name: "health", From: "$workflowValueCollections"}}, []string{"health"})
	if err != nil || len(result["health"].Rows) != 1 || result["health"].Rows[0]["reasonCode"] != "activity-disabled" {
		t.Fatalf("changed composed authority did not refresh collection health: %+v %v", result, err)
	}
}
