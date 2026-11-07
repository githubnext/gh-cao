package operationalvalue

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/model"
	"github.com/githubnext/gh-cao/server/internal/query"
)

func TestLocalCollectionHealthNeedsNeitherCollectorNorCredential(t *testing.T) {
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	snapshot := t.TempDir()
	policy := filepath.Join(snapshot, "cao.json")
	content := `{"version":1,"gh-aw-version":"v0.93.1","control-plane":{
		"scope":{"allowed-repositories":["org/repo"]},"activity":{"repositories":["org/repo"]},
		"campaigns":{"activity":{"workflow-values":[
			{"repository":"org/repo","workflow":"worker","collector":"not-present.mjs"}
		]}}}}`
	if err := os.WriteFile(policy, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(snapshot, "inventory-sources.json"),
		[]byte(`{"repositories":{"rows":[]},"workflows":{"rows":[]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	config := Config{Root: root, Repositories: []string{"org/repo"}, Environment: []string{
		"PATH=" + os.Getenv("PATH"), "GH_TOKEN=test-only", "GITHUB_TOKEN=test-only",
	}}
	if err := WriteCollectionHealth(t.Context(), config, policy, snapshot); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(snapshot, "gh-aw-logs-shards", "workflow-values-collections.jsonl")) // #nosec G304 -- fixed test-owned temporary output.
	if err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		Record map[string]any `json:"workflow_value_collection"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Record["registration"] != "enabled" || envelope.Record["state"] != "deferred" ||
		envelope.Record["reasonCode"] != "shared-budget-unavailable" || envelope.Record["lastAttemptAt"] != nil {
		t.Fatalf("hosted health did not preserve registration without an attempt: %v", envelope.Record)
	}
	if _, err := os.Stat(filepath.Join(snapshot, "workflow-value-workspaces")); !os.IsNotExist(err) {
		t.Fatal("health publication must not start acquisition or create proof workspaces")
	}
}

func TestHostedHealthPreservesActualAttemptAcrossPreparation(t *testing.T) {
	root := t.TempDir()
	policy := filepath.Join(root, "cao.json")
	content := `{"version":1,"gh-aw-version":"v0.93.1","control-plane":{
		"scope":{"allowed-repositories":["org/repo"]},"activity":{"repositories":["org/repo"]},
		"campaigns":{"activity":{"workflow-values":[
			{"repository":"org/repo","workflow":"worker","collector":"reviewed.mjs"}]}}}}`
	if err := os.WriteFile(policy, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot := filepath.Join(root, "transport")
	if err := os.MkdirAll(snapshot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(snapshot, "inventory-sources.json"),
		[]byte(`{"repositories":{"rows":[]},"workflows":{"rows":[]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	config := Config{Root: root, Repositories: []string{"org/repo"}, NodeBinary: "/nonexistent-node"}
	if err := WriteCollectionHealth(t.Context(), config, policy, snapshot); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(snapshot, "gh-aw-logs-shards", "workflow-values-collections.jsonl")
	data, err := os.ReadFile(file) // #nosec G304 -- file is under this test's TempDir.
	if err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		Record model.Row `json:"workflow_value_collection"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		t.Fatal(err)
	}
	attempted := time.Now().UTC().Truncate(time.Millisecond)
	record, err := CollectionAttempt(envelope.Record, attempted, "available", "captured")
	if err != nil {
		t.Fatal(err)
	}
	envelopeData, err := json.Marshal(map[string]any{
		"schema_version": 2, "kind": "workflow_value_collection",
		"workflow_value_collection": record,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, append(envelopeData, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteCollectionHealth(t.Context(), config, policy, snapshot); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(file) // #nosec G304 -- file is under this test's TempDir.
	if err != nil || json.Unmarshal(data, &envelope) != nil ||
		envelope.Record["state"] != "available" || envelope.Record["reasonCode"] != "captured" ||
		envelope.Record["lastAttemptAt"] != optimizerTime(attempted) {
		t.Fatalf("preparation concealed a real collector attempt: %v %s", err, data)
	}
}

func TestHostedSnapshotQueriesCanonicalDataWithoutSQLite(t *testing.T) {
	root := t.TempDir()
	policy := filepath.Join(root, "cao.json")
	if err := os.WriteFile(policy, []byte(`{"version":1,"gh-aw-version":"v0.93.1",
		"control-plane":{"scope":{"allowed-repositories":["githubnext/gh-cao"]},
		"activity":{"repositories":["githubnext/gh-cao"]}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot := filepath.Join(root, "snapshot")
	if err := os.MkdirAll(snapshot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(snapshot, "inventory-sources.json"),
		[]byte(`{"repositories":{"rows":[]},"workflows":{"rows":[]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	observed, _ := time.Parse(time.RFC3339, "2026-11-05T20:00:00Z")
	config := Config{Root: root, Repositories: []string{"githubnext/gh-cao"},
		NodeBinary: "/nonexistent-node", ObservedAt: observed}
	if err := WriteCollectionHealth(t.Context(), config, policy, snapshot); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "operational-values.jsonl")
	queries := 0
	err := ComputeHostedSnapshot(t.Context(), config, policy, snapshot, output,
		func(_ context.Context, definitions []query.Definition, requested []string) (map[string]model.Source, error) {
			queries++
			if len(definitions) != 1 || definitions[0].Name != "optimization-repository-efficiency" ||
				len(definitions[0].Joins) != 1 || definitions[0].Aggregate == nil ||
				len(requested) != 1 || requested[0] != "optimization-repository-efficiency" {
				t.Fatalf("unexpected canonical query: %#v %#v", definitions, requested)
			}
			return map[string]model.Source{requested[0]: {
				Source: requested[0], Rows: []model.Row{{"owner": "githubnext", "repository": "gh-cao",
					"declaredWorkflowPath": ".github/workflows/worker.md", "concludedRuns": 3,
					"successfulRuns": 3, "successfulRunsWithAic": 3, "aicPerSuccessfulRun": 2.5,
					"failedRuns": 0, "cancelledRuns": 0}},
				Metadata: model.Metadata{"availability": "available", "canonical-revision": int64(42)},
			}}, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if queries != 1 {
		t.Fatalf("canonical query count = %d, want 1", queries)
	}
	rows, err := ReadMeasurements(output)
	if err != nil || len(rows) != 3 || rows[0]["repository"] != "githubnext/gh-cao" ||
		rows[0]["value"] != json.Number("2.5") ||
		rows[0]["evidence-digest"] != "33fb3591bb9abb77f977f73e85b162b3d9035554e4374334698e82fc5978d136" {
		t.Fatalf("hosted result is not a canonical native assessment: %+v %v", rows, err)
	}
	for _, row := range rows {
		if row["coverage-status"] != "unknown" || row["evaluator-digest"] == nil {
			t.Fatalf("hosted Value promoted unverified coverage: %+v", row)
		}
	}
	if _, err := os.Stat(output); err != nil {
		t.Fatal(err)
	}
	if err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err == nil && strings.HasSuffix(strings.ToLower(path), ".sqlite") {
			t.Fatalf("hosted execution created SQLite file %s", path)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	missingOutput := filepath.Join(t.TempDir(), "missing.jsonl")
	if err := ComputeHostedSnapshot(t.Context(), config, policy, snapshot, missingOutput,
		func(_ context.Context, definitions []query.Definition, requested []string) (map[string]model.Source, error) {
			return map[string]model.Source{requested[0]: {Metadata: model.Metadata{
				"availability": "available", "canonical-revision": int64(42),
			}}}, nil
		}); err != nil {
		t.Fatal(err)
	}
	missing, err := ReadMeasurements(missingOutput)
	if err != nil || len(missing) != 3 {
		t.Fatalf("missing canonical opportunity was not evaluated: %+v %v", missing, err)
	}
	for _, row := range missing {
		if row["value"] != nil || row["maturity-status"] != "unavailable" ||
			row["missing-reason"] != "no-eligible-opportunities" {
			t.Fatalf("missing opportunities became a fabricated zero: %+v", row)
		}
	}
	if err := ComputeHostedSnapshot(t.Context(), config, policy, snapshot,
		filepath.Join(t.TempDir(), "unavailable.jsonl"),
		func(_ context.Context, definitions []query.Definition, requested []string) (map[string]model.Source, error) {
			return map[string]model.Source{requested[0]: {Metadata: model.Metadata{
				"availability": "unavailable", "canonical-revision": int64(42),
			}}}, nil
		}); err == nil {
		t.Fatal("unavailable canonical query produced a current Value result")
	}
}

func TestHostedDependabotWithoutJSReportsMissingEvidence(t *testing.T) {
	tests := []struct {
		name, campaign, repository string
		missing                    bool
	}{
		{"enabled", `"dependabot":{"targets":{"github/gh-aw":{"mode":"live"}},"workers":{"update-planner":{"workflow":"dependabot-update-planner"}}}`, "github/gh-aw", true},
		{"disabled campaign", `"dependabot":{"enabled":false,"targets":{"github/gh-aw":{"mode":"live"}},"workers":{"update-planner":{"workflow":"dependabot-update-planner"}}}`, "github/gh-aw", false},
		{"disabled worker", `"dependabot":{"targets":{"github/gh-aw":{"mode":"live"}},"workers":{"update-planner":{"workflow":"dependabot-update-planner","enabled":false}}}`, "github/gh-aw", false},
		{"out of scope", `"dependabot":{"targets":{"github/gh-aw":{"mode":"live"}},"workers":{"update-planner":{"workflow":"dependabot-update-planner"}}}`, "githubnext/gh-cao", false},
		{"remote registration alone", "", "github/gh-aw", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			campaigns := `"activity":{"workflow-values":[{"repository":"github/gh-aw","workflow":"daily-file-diet",` +
				`"collector":".github/value-collectors/daily-file-diet.mjs"}]}`
			if tt.campaign != "" {
				campaigns += "," + tt.campaign
			}
			policy := filepath.Join(root, "cao.json")
			content := `{"version":1,"gh-aw-version":"v0.93.1","control-plane":{` +
				`"scope":{"allowed-repositories":["github/gh-aw","githubnext/gh-cao"]},` +
				`"activity":{"repositories":["github/gh-aw","githubnext/gh-cao"]},` +
				`"campaigns":{` + campaigns + `}}}`
			if err := os.WriteFile(policy, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			snapshot := filepath.Join(root, "snapshot")
			if err := os.MkdirAll(snapshot, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(snapshot, "inventory-sources.json"),
				[]byte(`{"repositories":{"rows":[]},"workflows":{"rows":[]}}`), 0o600); err != nil {
				t.Fatal(err)
			}
			observed, _ := time.Parse(time.RFC3339, "2026-11-05T20:00:00Z")
			config := Config{Root: root, Repositories: []string{tt.repository},
				NodeBinary: "/nonexistent-node", ObservedAt: observed}
			if err := WriteCollectionHealth(t.Context(), config, policy, snapshot); err != nil {
				t.Fatal(err)
			}
			output := filepath.Join(root, "values.jsonl")
			queries := 0
			if err := ComputeHostedSnapshot(t.Context(), config, policy, snapshot, output,
				func(_ context.Context, definitions []query.Definition, requested []string) (map[string]model.Source, error) {
					queries++
					return map[string]model.Source{requested[0]: {
						Source: requested[0],
						Rows: []model.Row{{"declaredWorkflowPath": ".github/workflows/worker.md",
							"concludedRuns": 3, "successfulRuns": 3, "successfulRunsWithAic": 3,
							"aicPerSuccessfulRun": 2.5, "failedRuns": 0, "cancelledRuns": 0}},
						Metadata: model.Metadata{"availability": "available", "canonical-revision": int64(42)},
					}}, nil
				}); err != nil {
				t.Fatal(err)
			}
			if queries != 1 {
				t.Fatalf("canonical query count = %d, want 1", queries)
			}
			rows, err := ReadMeasurements(output)
			if err != nil {
				t.Fatal(err)
			}
			want := 3
			if tt.missing {
				want += 3
			}
			if len(rows) != want {
				t.Fatalf("assessments = %d, want %d: %+v", len(rows), want, rows)
			}
			for _, row := range rows {
				if row["campaign"] != "dependabot" {
					if row["value"] == nil {
						t.Fatalf("supported Optimization was not evaluated: %+v", row)
					}
					continue
				}
				if row["value"] != nil || row["maturity-status"] != "unavailable" ||
					row["coverage-status"] != "missing" ||
					row["missing-reason"] != "remote-acquisition-unavailable" ||
					row["measurement-kind"] != "snapshot" ||
					row["evidence-digest"] != nil || row["rollup-numerator"] != nil {
					t.Fatalf("unsupported Dependabot became a successful assessment: %+v", row)
				}
			}
		})
	}
}

func TestHostedLocalEvaluationHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := ComputeHostedSnapshot(ctx, Config{}, "unused", "unused", "unused",
		func(context.Context, []query.Definition, []string) (map[string]model.Source, error) {
			t.Fatal("canceled hosted Value evaluation queried the canonical database")
			return map[string]model.Source{}, nil
		}); err == nil {
		t.Fatal("canceled hosted Value evaluation succeeded")
	}
}

func TestHostedPinnedOptimizationAdapterMatchesReviewedSource(t *testing.T) {
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyHostedLocalAdapters(root, nil); err != nil {
		t.Fatalf("checked-in JS oracle changed without a Go evaluator review: %v", err)
	}
}

func TestHostedReviewedEvidenceImportUsesPinnedOriginalPacket(t *testing.T) {
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	snapshot := t.TempDir()
	if err := os.WriteFile(filepath.Join(snapshot, "inventory-sources.json"),
		[]byte(`{"repositories":{"rows":[]},"workflows":{"rows":[]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	policy := filepath.Join(root, ".github", "workflows", "cao.json")
	if err := WriteCollectionHealth(t.Context(),
		Config{Root: root, NodeBinary: "/nonexistent-node", Repositories: []string{"github/gh-aw"}},
		policy, snapshot); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(snapshot, "gh-aw-logs-shards", "workflow-values.jsonl")) // #nosec G304 -- snapshot is a test-owned TempDir.
	if err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		SchemaVersion int       `json:"schema_version"`
		Kind          string    `json:"kind"`
		Packet        model.Row `json:"operational_value_evidence"`
	}
	if err := json.Unmarshal(content, &envelope); err != nil || envelope.SchemaVersion != 2 ||
		envelope.Kind != "operational_value_evidence" ||
		envelope.Packet["repository"] != "github/gh-aw" ||
		envelope.Packet["workflow"] != "daily-file-diet" {
		t.Fatalf("pinned reviewed evidence was not imported: %v %v", err, envelope.Packet)
	}
}

func TestReadMeasurementsPreservesMissingEvidence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "values.jsonl")
	content := `{"schema_version":2,"kind":"operational_value","operational_value":{` +
		`"timestamp":"2026-01-01T00:00:00Z","repository":"Octo/API","campaign":"optimization",` +
		`"campaign_id":"campaign:optimization","value_id":"worker.efficiency","value":null,` +
		`"maturity_status":"unavailable","window_start":"2025-12-01T00:00:00Z",` +
		`"window_end":"2026-01-01T00:00:00Z","measurement_kind":"interval",` +
		`"coverage_status":"missing","missing_reason":"no-eligible-runs"}}` + "\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	rows, err := ReadMeasurements(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0]["value"] != nil || rows[0]["coverage-status"] != "missing" ||
		rows[0]["missing-reason"] != "no-eligible-runs" ||
		rows[0]["repositoryId"] != "repository:octo%2Fapi" {
		t.Fatalf("canonical missing measurement = %#v", rows)
	}
}

func TestReadMeasurementsRejectsInvalidMissingEvidence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "values.jsonl")
	content := `{"schema_version":2,"kind":"operational_value","operational_value":{` +
		`"timestamp":"2026-01-01T00:00:00Z","repository":"octo/api","campaign":"optimization",` +
		`"value_id":"worker.efficiency","value":null,"maturity_status":"unavailable",` +
		`"coverage_status":"missing","missing_reason":"no-eligible-runs"}}` + "\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadMeasurements(path); err == nil {
		t.Fatal("invalid unavailable measurement was accepted")
	}
}
