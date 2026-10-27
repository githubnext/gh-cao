package cli

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

const nativeCollectorFixture = `export const definition={schemaVersion:3,repository:"owner/repo",slug:"daily-review",sourcePath:".github/workflows/daily-review.md",adoption:{adoptedAt:"2026-10-01T00:00:00.000Z"},evidence:{repositories:["owner/repo"],window:{cadenceDays:1,maturationDays:0}}}; export async function collectValueEvidence(){throw new Error("must not collect during enable")}`

func TestNativeValueOptInOnlyStagesExactLocalPolicy(t *testing.T) {
	nativeManagementDirectory(t)
	writeTestFile(t, policyPath, nativeManagementPolicy)
	writeTestFile(t, "collectors/value.mjs", nativeCollectorFixture)
	cmd, output := nativeManagementCommand()
	cmd.Flags().String("collector", "collectors/value.mjs", "")
	cmd.Flags().String("api-requests", "25", "")
	if err := nativeValueConfigure(cmd, []string{"owner/repo", "daily-review"}, true); err != nil {
		t.Fatal(err)
	}
	policy, err := nativeReadValidatedPolicy()
	if err != nil {
		t.Fatal(err)
	}
	var before map[string]any
	_ = json.Unmarshal([]byte(nativeManagementPolicy), &before)
	control := policy["control-plane"].(map[string]any)
	entries := control["campaigns"].(map[string]any)["activity"].(map[string]any)["workflow-values"].([]any)
	if len(entries) != 1 || entries[0].(map[string]any)["collector"] != "collectors/value.mjs" ||
		entries[0].(map[string]any)["api-requests"] != json.Number("25") {
		t.Fatalf("unexpected opt-in: %+v", entries)
	}
	delete(control["campaigns"].(map[string]any), "activity")
	left, _ := json.Marshal(policy)
	right, _ := json.Marshal(before)
	if string(left) != string(right) {
		t.Fatal("value enable changed unrelated policy or repository scope")
	}
	if !strings.Contains(output.String(), "deploy/push") {
		t.Fatal("missing reviewed activation requirement")
	}
	if err := nativeValueConfigure(cmd, []string{"owner/repo", "daily-review"}, false); err != nil {
		t.Fatal(err)
	}
	policy, _ = nativeReadValidatedPolicy()
	entries = policy["control-plane"].(map[string]any)["campaigns"].(map[string]any)["activity"].(map[string]any)["workflow-values"].([]any)
	if len(entries) != 1 || entries[0].(map[string]any)["enabled"] != false {
		t.Fatal("value disable did not persist exact revocation")
	}
	content, _ := os.ReadFile(policyPath)
	cmd.Flags().Bool("if-absent", true, "")
	if err := nativeValueConfigure(cmd, []string{"owner/repo", "daily-review"}, true); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(policyPath)
	if string(after) != string(content) {
		t.Fatal("automatic registration overrode explicit disable")
	}
}

func TestNativeValueAutomaticRegistrationPreservesExistingPolicy(t *testing.T) {
	nativeManagementDirectory(t)
	writeTestFile(t, policyPath, nativeManagementPolicy)
	writeTestFile(t, "collectors/value.mjs", nativeCollectorFixture)
	cmd, _ := nativeManagementCommand()
	cmd.Flags().String("collector", "collectors/value.mjs", "")
	cmd.Flags().Bool("if-absent", true, "")
	if err := nativeValueConfigure(cmd, []string{"owner/repo", "daily-review"}, true); err != nil {
		t.Fatal(err)
	}

	before, _ := os.ReadFile(policyPath)
	if err := nativeValueConfigure(cmd, []string{"owner/repo", "daily-review"}, true); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(policyPath)
	if string(before) != string(after) {
		t.Fatal("automatic registration rewrote an existing entry")
	}
	if err := nativeValueConfigure(cmd, []string{"owner/repo", "other-workflow"}, false); err != nil {
		t.Fatal(err)
	}
	if err := nativeValueConfigure(cmd, []string{"owner/repo", "other-workflow"}, true); err != nil {
		t.Fatal(err)
	}
	policy, err := nativeReadValidatedPolicy()
	if err != nil {
		t.Fatal(err)
	}
	entries := policy["control-plane"].(map[string]any)["campaigns"].(map[string]any)["activity"].(map[string]any)["workflow-values"].([]any)
	if len(entries) != 2 || entries[1].(map[string]any)["enabled"] != false {
		t.Fatal("automatic registration ignored an advance explicit disable")
	}
}

func TestNativeValuePolicyAcceptsAndPreservesReviewedEvidence(t *testing.T) {
	nativeManagementDirectory(t)
	var policy map[string]any
	if err := json.Unmarshal([]byte(nativeManagementPolicy), &policy); err != nil {
		t.Fatal(err)
	}
	control := policy["control-plane"].(map[string]any)
	control["campaigns"] = map[string]any{"activity": map[string]any{"workflow-values": []any{
		map[string]any{
			"repository": "owner/repo", "workflow": "daily-review", "collector": "collectors/value.mjs",
			"api-requests": float64(25),
			"evidence": map[string]any{
				"path":   ".github/value-evidence/daily-review.jsonl.gz",
				"sha256": strings.Repeat("a", 64),
			},
		},
	}}}
	content, _ := json.Marshal(policy)
	writeTestFile(t, policyPath, string(content))
	writeTestFile(t, "collectors/value.mjs", nativeCollectorFixture)
	cmd, _ := nativeManagementCommand()
	cmd.Flags().String("collector", "collectors/value.mjs", "")
	if err := nativeValueConfigure(cmd, []string{"owner/repo", "daily-review"}, true); err != nil {
		t.Fatal(err)
	}
	updated, err := nativeReadValidatedPolicy()
	if err != nil {
		t.Fatal(err)
	}
	entry := updated["control-plane"].(map[string]any)["campaigns"].(map[string]any)["activity"].(map[string]any)["workflow-values"].([]any)[0].(map[string]any)
	if entry["evidence"].(map[string]any)["sha256"] != strings.Repeat("a", 64) {
		t.Fatal("value enable discarded reviewed evidence")
	}
	if entry["api-requests"] != json.Number("25") {
		t.Fatal("value enable discarded reviewed API request authority")
	}
}

func TestNativeValueOptInFailsClosedWithoutEnrollmentIdentityOrConfinedModule(t *testing.T) {
	nativeManagementDirectory(t)
	writeTestFile(t, policyPath, nativeManagementPolicy)
	writeTestFile(t, "collectors/value.mjs", nativeCollectorFixture)
	if err := os.Symlink("collectors", "linked"); err != nil {
		t.Fatal(err)
	}
	for _, trial := range []struct{ repository, workflow, collector string }{
		{"outside/repo", "daily-review", "collectors/value.mjs"},
		{"owner/repo", "other", "collectors/value.mjs"},
		{"owner/repo", "daily-review", "../value.mjs"},
		{"owner/repo", "daily-review", "linked/value.mjs"},
		{"owner/repo", "daily-review", "https://host/value.mjs"},
		{"owner/repo", "daily-review", "missing.mjs"},
	} {
		cmd, _ := nativeManagementCommand()
		cmd.Flags().String("collector", trial.collector, "")
		if err := nativeValueConfigure(cmd, []string{trial.repository, trial.workflow}, true); err == nil {
			t.Fatalf("accepted unsafe registration: %+v", trial)
		}
		content, _ := os.ReadFile(policyPath)
		if string(content) != nativeManagementPolicy {
			t.Fatal("rejected opt-in rewrote policy")
		}
	}
}

func TestNativeValueOptInRejectsInvalidAPIRequestCeiling(t *testing.T) {
	nativeManagementDirectory(t)
	writeTestFile(t, policyPath, nativeManagementPolicy)
	writeTestFile(t, "collectors/value.mjs", nativeCollectorFixture)
	for _, value := range []string{"0", "1001", "1.5", "many"} {
		cmd, _ := nativeManagementCommand()
		cmd.Flags().String("collector", "collectors/value.mjs", "")
		cmd.Flags().String("api-requests", value, "")
		if err := nativeValueConfigure(cmd, []string{"owner/repo", "daily-review"}, true); err == nil {
			t.Fatalf("accepted invalid API request ceiling %q", value)
		}
	}
}
