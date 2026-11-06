package collectnative_test

import (
	"bytes"
	"context"
	"os"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/collectnative"
	"github.com/githubnext/gh-cao/server/internal/postgresx"
	"github.com/githubnext/gh-cao/server/internal/query"
	"github.com/githubnext/gh-cao/server/internal/testutil"
)

type modelAuditProvider struct {
	run   collectnative.Run
	files map[string][]byte
}

func (p modelAuditProvider) Runs(context.Context, string, int64, time.Time, func(collectnative.Run) error) error {
	return collectnative.ErrUnsupported
}

func (p modelAuditProvider) Attempt(_ context.Context, identity collectnative.Identity) (collectnative.Run, error) {
	if identity != p.run.Identity {
		return collectnative.Run{}, collectnative.ErrIdentity
	}
	return p.run, nil
}

func (p modelAuditProvider) Artifacts(_ context.Context, run collectnative.Run, visit func(collectnative.Artifact) error) error {
	if run.Identity != p.run.Identity {
		return collectnative.ErrIdentity
	}
	return visit(collectnative.Artifact{ID: 1, Name: "usage", Files: p.files})
}

func TestNativeModelAuditPostgresPublicationPreservesPartialEvidence(t *testing.T) {
	first, config := testutil.PostgresWithConfig(t, t.Context(), "CAO_TEST_POSTGRES_URL")
	second, err := postgresx.NewConfig(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Close() })
	fixtures, err := os.OpenRoot("testdata")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = fixtures.Close() }()
	files := map[string][]byte{"aw_info.json": []byte(`{"repository":"octo/api","run_id":303,"run_attempt":1}`)}
	for location, fixture := range map[string]string{
		"aw_session.jsonl":                  "gh-aw-v1.0.1-model-resolution.jsonl",
		"agent-stdio.log":                   "gh-aw-v1.0.1-model-stdio.log",
		"sandbox/firewall/awf-reflect.json": "gh-aw-v1.0.1-model-reflection.json",
	} {
		content, err := fixtures.ReadFile(fixture)
		if err != nil {
			t.Fatal(err)
		}
		files[location] = content
	}
	files["agent-stdio.log"] = append(files["agent-stdio.log"], []byte("PRIVATE_STDIO PRIVATE_TOKEN\n")...)
	identity := collectnative.Identity{Repository: "octo/api", InstallationID: 7, RunID: 303, Attempt: 1}
	provider := modelAuditProvider{files: files, run: collectnative.Run{
		Identity: identity, WorkflowPath: ".github/workflows/test.lock.yml",
		Fields: map[string]any{"status": "completed", "conclusion": "failure",
			"created_at": time.Now().Add(-time.Hour).UTC().Format(time.RFC3339),
			"updated_at": time.Now().UTC().Format(time.RFC3339)},
	}}
	source := first.Acquisition()
	if err := source.SetEnrollment(t.Context(), identity.Repository, identity.InstallationID); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", "/nonexistent")
	c := collectnative.Collector{Provider: provider, Store: source}
	if err := c.CollectAttempt(t.Context(), identity); err != nil {
		t.Fatal(err)
	}
	evidence, digest, err := second.Acquisition().Pending(t.Context(), identity)
	if err != nil {
		t.Fatal(err)
	}
	raw, _, err := evidence.Bytes()
	if err != nil || bytes.Contains(raw, []byte("PRIVATE")) || bytes.Contains(raw, []byte("[copilot-harness]")) ||
		bytes.Contains(raw, []byte(`"models_fetch_complete"`)) {
		t.Fatal("durable native outbox retained raw model audit inputs")
	}
	scope, err := source.CurrentScope(t.Context(), identity.Repository)
	if err != nil || scope.InstallationID != identity.InstallationID {
		t.Fatalf("read native enrollment: %v", err)
	}
	changed, err := second.Acquisition().Activate(t.Context(), postgresx.AcquisitionPublication{
		Identity: identity, Digest: digest, Epoch: scope.Epoch,
	})
	if err != nil || !changed {
		t.Fatalf("activate native model audit: changed=%v err=%v", changed, err)
	}
	result, _, err := first.ExecuteSQLPlan(t.Context(), []query.Definition{
		{Name: "audits", From: "$audits"},
		{Name: "runs", From: "$runs", Select: []query.SelectedField{
			{Field: "highPriorityAuditItems"}, {Field: "mediumPriorityAuditItems"},
		}},
	}, []string{"audits", "runs"})
	if err != nil {
		t.Fatal(err)
	}
	found, partial := false, false
	for _, row := range result["audits"].Rows {
		if row["type"] == "audit.finding" && row["code"] == "model_unavailable" && row["status"] == "high" {
			found = true
		}
		if row["code"] == "legacy-audit-unavailable" {
			t.Fatal("native default audit retained the retired blanket gap")
		}
		if row["type"] == "collection.coverage" && row["code"] == "job-logs-unavailable" {
			partial = true
		}
	}
	if !found || !partial || len(result["runs"].Rows) != 1 ||
		result["runs"].Rows[0]["highPriorityAuditItems"] != nil || result["runs"].Rows[0]["mediumPriorityAuditItems"] != nil {
		t.Fatal("canonical publication lost the supported finding or implied complete immutable audit totals")
	}
}
