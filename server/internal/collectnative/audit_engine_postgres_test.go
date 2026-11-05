package collectnative_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/collectnative"
	"github.com/githubnext/gh-cao/server/internal/postgresx"
	"github.com/githubnext/gh-cao/server/internal/query"
	"github.com/githubnext/gh-cao/server/internal/testutil"
)

type engineAuditProvider struct{ modelAuditProvider }

func (engineAuditProvider) Jobs(_ context.Context, run collectnative.Run, visit func(collectnative.Job) error) error {
	return visit(collectnative.Job{Identity: run.Identity, ID: 91,
		Fields: map[string]any{"id": json.Number("91"), "run_id": json.Number("303"),
			"run_attempt": json.Number("1"), "name": "detection", "status": "completed", "conclusion": "failure"},
		Log: []byte("2026-10-15T10:00:00Z ##[error] PRIVATE_DETECTION_LOG\n")})
}

func TestNativeLegacyEngineAndGatewayPostgresPublication(t *testing.T) {
	for _, test := range []struct{ engine, content string }{
		{"codex", "thinking\ntool github.read({})\ntokens used\n60,001"},
		{"claude", `prefix {'usage':{'input_tokens':60000,'output_tokens':1},'cost':0.25}`},
	} {
		t.Run(test.engine, func(t *testing.T) {
			first, config := testutil.PostgresWithConfig(t, t.Context(), "CAO_TEST_POSTGRES_URL")
			second, err := postgresx.NewConfig(t.Context(), config)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = second.Close() })
			identity := collectnative.Identity{Repository: "octo/api", InstallationID: 7, RunID: 303, Attempt: 1}
			info, _ := json.Marshal(map[string]any{"repository": "octo/api", "run_id": 303, "run_attempt": 1, "engine_id": test.engine})
			provider := modelAuditProvider{files: map[string][]byte{
				"aw_info.json": info, "agent-stdio.log": []byte(test.content),
				"gateway.jsonl": []byte(`{"event":"tool_call","server_name":"github","tool_name":"read","input_size":10,"output_size":20,"status":"success"}`),
			}, run: collectnative.Run{Identity: identity, WorkflowPath: ".github/workflows/test.lock.yml",
				Fields: map[string]any{"status": "completed", "conclusion": "success",
					"created_at": time.Now().Add(-time.Hour).UTC().Format(time.RFC3339),
					"updated_at": time.Now().UTC().Format(time.RFC3339)}}}
			source := first.Acquisition()
			if err := source.SetEnrollment(t.Context(), identity.Repository, identity.InstallationID); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", "/nonexistent")
			collector := collectnative.Collector{Provider: provider, Store: source}
			if err := collector.CollectAttempt(t.Context(), identity); err != nil {
				t.Fatal(err)
			}
			evidence, digest, err := second.Acquisition().Pending(t.Context(), identity)
			if err != nil {
				t.Fatal(err)
			}
			projection, err := collectnative.ProjectCanonical(t.Context(), evidence, time.Now(), collectnative.Limits{})
			if err != nil {
				t.Fatal(err)
			}
			projectedRun := projection.Collections["runs"][0]
			calls, _ := json.Marshal(projectedRun["mcpToolCalls"])
			response, _ := json.Marshal(projectedRun["mcpResponseBytes"])
			if string(calls) != "1" || string(response) != "20" {
				t.Fatalf("raw gateway did not preserve canonical Run aggregates: %v", projectedRun)
			}
			scope, err := source.CurrentScope(t.Context(), identity.Repository)
			if err != nil {
				t.Fatal(err)
			}
			changed, err := second.Acquisition().Activate(t.Context(), postgresx.AcquisitionPublication{
				Identity: identity, Digest: digest, Epoch: scope.Epoch})
			if err != nil || !changed {
				t.Fatalf("activate legacy engine: changed=%v err=%v", changed, err)
			}
			result, _, err := first.ExecuteSQLPlan(t.Context(), []query.Definition{
				{Name: "audits", From: "$audits"},
				{Name: "runs", From: "$runs"},
				{Name: "tools", From: "$tools"},
			}, []string{"audits", "runs", "tools"})
			if err != nil || len(result["runs"].Rows) != 1 {
				t.Fatalf("query legacy canonical publication: %v", err)
			}
			highToken, partial := false, false
			for _, row := range result["audits"].Rows {
				highToken = highToken || row["type"] == "audit.finding" && row["code"] == "high_token_usage"
				if row["code"] == "legacy-audit-unavailable" {
					t.Fatal("native default audit retained the retired blanket gap")
				}
				partial = partial || row["type"] == "collection.coverage"
			}
			row := result["runs"].Rows[0]
			toolPublished := false
			for _, tool := range result["tools"].Rows {
				toolPublished = toolPublished || tool["name"] == "read"
			}
			if !highToken || !partial || row["inputTokens"] != nil ||
				row["highPriorityAuditItems"] != nil || row["mediumPriorityAuditItems"] != nil ||
				!toolPublished {
				t.Fatalf("legacy thresholds/raw gateway tool lost or fabricated structured totals: high=%v partial=%v row=%v", highToken, partial, row)
			}
		})
	}
}

func TestNativeEngineDetectionPostgresPublicationPreservesCanonicalGaps(t *testing.T) {
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
	events, err := fixtures.ReadFile("gh-aw-v1.0.1-copilot-events.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	identity := collectnative.Identity{Repository: "octo/api", InstallationID: 7, RunID: 303, Attempt: 1}
	provider := engineAuditProvider{modelAuditProvider{files: map[string][]byte{
		"aw_info.json": []byte(`{"repository":"octo/api","run_id":303,"run_attempt":1,"engine_id":"copilot"}`),
		"sandbox/agent/logs/copilot-session-state/id/events.jsonl": events,
		"agent-stdio.log": []byte(`{"type":"system","subtype":"init","mcp_servers":[{"name":"github","status":"failed","error":"PRIVATE_MCP_ERROR"}]}
{"type":"user","message":{"content":"PRIVATE_PROMPT"}}`),
		"detection/detection_result.json": []byte(`{"prompt_injection":true,"secret_leak":false,"malicious_patch":false,"reasons":["PRIVATE_REASON"]}`),
	}, run: collectnative.Run{Identity: identity, WorkflowPath: ".github/workflows/test.lock.yml",
		Fields: map[string]any{"status": "completed", "conclusion": "failure",
			"created_at": time.Now().Add(-time.Hour).UTC().Format(time.RFC3339),
			"updated_at": time.Now().UTC().Format(time.RFC3339)},
	}}}
	source := first.Acquisition()
	if err := source.SetEnrollment(t.Context(), identity.Repository, identity.InstallationID); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", "/nonexistent")
	collector := collectnative.Collector{Provider: provider, Store: source}
	if err := collector.CollectAttempt(t.Context(), identity); err != nil {
		t.Fatal(err)
	}
	evidence, digest, err := second.Acquisition().Pending(t.Context(), identity)
	if err != nil {
		t.Fatal(err)
	}
	raw, _, err := evidence.Bytes()
	if err != nil || bytes.Contains(raw, []byte("PRIVATE")) || bytes.Contains(raw, []byte("copilotVersion")) ||
		!bytes.Contains(raw, []byte("workflow_failed")) {
		t.Fatal("safe outbox retained transient engine or detection inputs")
	}
	scope, err := source.CurrentScope(t.Context(), identity.Repository)
	if err != nil {
		t.Fatal(err)
	}
	changed, err := second.Acquisition().Activate(t.Context(), postgresx.AcquisitionPublication{
		Identity: identity, Digest: digest, Epoch: scope.Epoch,
	})
	if err != nil || !changed {
		t.Fatalf("publish native audit: changed=%v err=%v", changed, err)
	}
	result, _, err := first.ExecuteSQLPlan(t.Context(), []query.Definition{
		{Name: "audits", From: "$audits"},
		{Name: "runs", From: "$runs", Select: []query.SelectedField{
			{Field: "errorCount"}, {Field: "inputTokens"},
			{Field: "highPriorityAuditItems"}, {Field: "mediumPriorityAuditItems"},
		}},
	}, []string{"audits", "runs"})
	if err != nil {
		t.Fatal(err)
	}
	codes, partial := []string{}, false
	for _, row := range result["audits"].Rows {
		if row["type"] == "audit.finding" {
			code, _ := row["code"].(string)
			codes = append(codes, code)
		}
		if row["code"] == "legacy-audit-unavailable" {
			t.Fatal("native default audit retained the retired blanket gap")
		}
		partial = partial || row["type"] == "collection.coverage"
	}
	slices.Sort(codes)
	// Production curation intentionally drops the redundant workflow_failed
	// marker after its conclusion is represented on the canonical Run.
	if !slices.Equal(codes, []string{"high_token_usage", "mcp_server_failures", "threat_detected", "threat_detection_job_failed"}) ||
		!partial || len(result["runs"].Rows) != 1 {
		t.Fatalf("supported native findings or coverage lost at publication: %v partial=%v", codes, partial)
	}
	row := result["runs"].Rows[0]
	failedJobs, err := json.Marshal(row["errorCount"])
	if row["highPriorityAuditItems"] != nil || row["mediumPriorityAuditItems"] != nil || row["inputTokens"] != nil ||
		err != nil || !bytes.Equal(failedJobs, []byte("1")) {
		t.Fatalf("published raw totals fabricated input tokens/AIC/completeness or lost job diagnostics: %+v", row)
	}
}
