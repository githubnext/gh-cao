package operationalvalue

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGoWorkflowPolicyComposesImportsAndBindsRegistrations(t *testing.T) {
	dir := t.TempDir()
	base := `{"version":1,"gh-aw-version":"v0.93.1","control-plane":{"scope":{
		"allowed-owners":["org"],"allowed-repositories":["org/repo"]},
		"activity":{"repositories":["org/repo"]},
		"campaigns":{"activity":{"workflow-values":[
			{"repository":"org/repo","workflow":"worker","collector":"reviewed.mjs","api-requests":5,
			"acquisition":{"requirements":[{"source":"github","pool":"core",
				"primitive":"github.actions-run-provenance.v1","scope":"worker","cache":"invocation"}]}}]}}}}`
	if err := os.WriteFile(filepath.Join(dir, "base.json"), []byte(base), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "cao.json")
	if err := os.WriteFile(path, []byte(`{"extends":"base.json","control-plane":{"web":{"host":{
		"target":{"module":"container"}}}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot, err := ResolveWorkflowPolicy(context.Background(), Config{NodeBinary: "/nonexistent-node"}, path)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Registrations) != 1 || snapshot.Registrations[0].Collector != "reviewed.mjs" ||
		strings.Contains(string(snapshot.Document), "extends") ||
		!strings.Contains(string(snapshot.Document), `"allowed-owners":["org"]`) ||
		!strings.Contains(string(snapshot.Document), `"host":{"target":{"module":"container"}}`) {
		t.Fatalf("composed reviewed authority was not retained: %+v", snapshot)
	}
	expected := `{"version":1,"gh-aw-version":"v0.93.1","control-plane":{"scope":{` +
		`"allowed-owners":["org"],"allowed-repositories":["org/repo"]},` +
		`"activity":{"repositories":["org/repo"]},` +
		`"campaigns":{"activity":{"workflow-values":[{"repository":"org/repo","workflow":"worker",` +
		`"collector":"reviewed.mjs","api-requests":5,"acquisition":{"requirements":[{` +
		`"source":"github","pool":"core","primitive":"github.actions-run-provenance.v1",` +
		`"scope":"worker","cache":"invocation"}]}}]}},"web":{"host":{"target":{"module":"container"}}}}}`
	if string(snapshot.Document) != expected {
		t.Fatalf("composed JSON diverged from JSON.stringify policy order:\n%s\n%s",
			snapshot.Document, expected)
	}
	if err := os.WriteFile(filepath.Join(dir, "base.json"), []byte(strings.Replace(base,
		`"allowed-repositories":["org/repo"]`, `"allowed-repositories":["else/target"]`, 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveWorkflowPolicy(context.Background(), Config{}, path); err == nil {
		t.Fatal("reviewed base-scope revocation was ignored")
	}
}

func TestGoWorkflowPolicyRejectsUnreviewedAuthority(t *testing.T) {
	valid := `{"version":1,"gh-aw-version":"v0.93.1","control-plane":{
		"scope":{"allowed-repositories":["org/repo"]},"activity":{"repositories":["org/repo"]},
		"campaigns":{"activity":{"workflow-values":[
			{"repository":"org/repo","workflow":"worker","collector":"reviewed.mjs"}]}}}}`
	for name, content := range map[string]string{
		"duplicate key": strings.Replace(valid, `"version":1`, `"version":1,"version":1`, 1),
		"unreviewed":    strings.Replace(valid, `"collector":"reviewed.mjs"`, `"collector":"../unreviewed.mjs"`, 1),
		"expression":    strings.Replace(valid, `"collector":"reviewed.mjs"`, `"collector":"${{ secrets.TOKEN }}"`, 1),
		"unsupported manifest": strings.Replace(valid, `"collector":"reviewed.mjs"`,
			`"collector":"reviewed.mjs","acquisition":{"requirements":[{"source":"git","pool":"core",`+
				`"primitive":"git.commit-inventory.v1","scope":"worker","cache":"immutable"}]}`, 1),
		"duplicate identity": strings.Replace(valid,
			`{"repository":"org/repo","workflow":"worker","collector":"reviewed.mjs"}`,
			`{"repository":"org/repo","workflow":"worker","collector":"reviewed.mjs"},`+
				`{"repository":"org/repo","workflow":"worker","collector":"reviewed.mjs"}`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "cao.json")
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}

			if _, err := ResolveWorkflowPolicy(t.Context(), Config{}, path); err == nil {
				t.Fatal("accepted unreviewed Value policy")
			}
		})
	}
}

func TestGoWorkflowPolicyResolvesCurrentCatalogAndHostedProfile(t *testing.T) {
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{"cao.json", "cao.azure.json"} {
		t.Run(file, func(t *testing.T) {
			path := filepath.Join(root, ".github", "workflows", file)
			snapshot, err := ResolveWorkflowPolicy(t.Context(),
				Config{NodeBinary: "/nonexistent-node"}, path)
			if err != nil {
				t.Fatal(err)
			}
			if len(snapshot.Registrations) != 2 ||
				snapshot.Registrations[0].Repository != "github/gh-aw" ||
				snapshot.Registrations[0].Workflow != "daily-file-diet" ||
				snapshot.Registrations[1].Repository != "github/gh-aw" ||
				snapshot.Registrations[1].Workflow != "dependabot-update-planner" ||
				!strings.Contains(string(snapshot.Document), `"git.commit-inventory.v1"`) ||
				!strings.Contains(string(snapshot.Document), `"github.dependabot-alert-lifecycle.v1"`) ||
				strings.Contains(string(snapshot.Document), `"extends"`) {
				t.Fatalf("checked-in reviewed registration was not resolved: %+v", snapshot.Registrations)
			}
		})
	}
}
