package cli

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDashboardCheckoutWorkflowEngineProjection(t *testing.T) {
	root := t.TempDir()
	path := ".github/workflows/test-quality-sentinel.lock.yml"
	if err := os.MkdirAll(filepath.Join(root, ".github", "workflows"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, path), []byte("# gh-aw-metadata: {\"agent_id\":\"pi\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	workflows := []dashboardWorkflow{
		{ID: 1, Name: "Sentinel", Path: path, State: "active"},
		{ID: 2, Name: "Remote only", Path: ".github/workflows/remote.lock.yml", State: "active"},
	}
	now := time.Now().UTC()
	batch, err := projectDashboardEvidence([]dashboardRepositoryEvidence{{
		Repository: "octo/api", Workflows: workflows, WorkflowEngines: dashboardCheckoutEngines(root, workflows),
	}}, now.Add(-time.Hour), now)
	if err != nil {
		t.Fatal(err)
	}
	if got := batch["workflows"][0]["workflowEngine"]; got != "pi" {
		t.Fatalf("sentinel engine = %v, want pi", got)
	}
	if _, known := batch["workflows"][1]["workflowEngine"]; known {
		t.Fatal("missing local lockfile was assigned an engine")
	}
}
