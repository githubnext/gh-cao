package cli

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestCampaignStandaloneWorkflowInstallation(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "cao.json")
	writeTestFile(t, path, `{"campaign":"demo","orchestrator":"demo","workers":{"task":"demo-task"},"standalone-workflows":["demo-system"]}`)
	declaration, err := readCampaignDeclaration(directory, "demo")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(declaration.StandaloneWorkflows, []string{"demo-system"}) {
		t.Fatalf("standalone workflows lost: %+v", declaration)
	}
	policy := map[string]any{}
	mergeCampaignDeclaration(policy, declaration)
	control := policy["control-plane"].(map[string]any)
	workflow := control["workflows"].(map[string]any)["demo-system"].(map[string]any)
	if workflow["enabled"] != false || workflow["mode"] != "review" || workflow["max-mode"] != "review" {
		t.Fatalf("unsafe standalone defaults: %+v", workflow)
	}
	workflow["max-repositories"] = 4
	mergeCampaignDeclaration(policy, declaration)
	if workflow["max-repositories"] != 4 {
		t.Fatal("reinstallation replaced operator policy")
	}
}

func TestCampaignStandaloneWorkflowDeclarationRejectsAmbiguity(t *testing.T) {
	for _, workflows := range [][]string{{"demo-task"}, {"demo"}, {"demo-system", "demo-system"}, {"../escape"}} {
		t.Run(strings.Join(workflows, ","), func(t *testing.T) {
			directory := t.TempDir()
			encoded, err := json.Marshal(workflows)
			if err != nil {
				t.Fatal(err)
			}
			writeTestFile(t, filepath.Join(directory, "cao.json"),
				`{"campaign":"demo","orchestrator":"demo","workers":{"task":"demo-task"},"standalone-workflows":`+string(encoded)+`}`)
			if _, err := readCampaignDeclaration(directory, "demo"); err == nil {
				t.Fatal("ambiguous standalone workflow accepted")
			}
		})
	}
}
