package collectnative

import (
	"encoding/json"
	"slices"
	"testing"
)

func TestNativeAgenticPinnedAssessments(t *testing.T) {
	// Independent upstream audit_agentic_analysis_test.go vectors: lean triage,
	// heavy research, moderate reducible read-only and moderate simple domains.
	for _, test := range []struct {
		name      string
		fields    map[string]any
		tools     int
		want      []string
		delegated bool
		friction  int
	}{
		{"lean-triage", map[string]any{"workflow_name": "Issue Triage", "turns": 2}, 1, []string{"agentic_overkill_for_agentic"}, false, 0},
		{"heavy-research", map[string]any{"workflow_name": "Deep Research", "turns": 15, "safe_items_count": 4,
			"started_at": "2026-10-15T10:00:00Z", "updated_at": "2026-10-15T10:22:00Z"}, 7,
			[]string{"agentic_resource_heavy_for_domain", "agentic_partially_reducible"}, false, 0},
		{"moderate-simple", map[string]any{"workflow_name": "Issue Triage", "turns": 7}, 1,
			[]string{"agentic_partially_reducible", "agentic_model_downgrade_available"}, false, 0},
		{"poor-control", map[string]any{"workflow_name": "Research", "turns": 2}, 1, []string{"agentic_poor_agentic_control"}, false, 4},
		{"delegated", map[string]any{"workflow_name": "Worker", "turns": 2}, 1, []string{"agentic_delegated_context_present"}, true, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			test.fields["audit"] = map[string]any{"native_tool_types": test.tools}
			missing := make([]any, test.friction)
			object(test.fields["audit"])["missing_tools"] = missing
			files := map[string][]byte{}
			if test.delegated {
				files["usage/aw_info.json"] = []byte(`{"context":{}}`)
			}
			findings, recommendations, insights, gaps, err := agenticAudit(t.Context(), test.fields, files, testIdentity(), Limits{})
			if err != nil || !slices.Equal(modelAuditCodes(findings), test.want) || len(insights) < 2 ||
				len(gaps) != 0 {
				t.Fatalf("assessment mismatch: %s %v %v", mustJSON(t, findings), gaps, err)
			}
			wantRecommendations := len(test.want)
			if test.delegated {
				wantRecommendations--
			}
			if len(recommendations) != wantRecommendations {
				t.Fatal("assessment recommendation ordering/count differs")
			}
		})
	}
}

func TestNativeInferredAuditMetricsDoNotRewriteRunAccounting(t *testing.T) {
	fields := map[string]any{"engine_id": "copilot"}
	_, err := enrichEngineAudit(t.Context(), fields, map[string][]byte{
		"agent/agent-stdio.log": []byte(`{"response":"PRIVATE_RESPONSE","stats":{"models":{"m":{"input_tokens":50000,"output_tokens":1}}}}`),
	}, testIdentity(), Limits{})
	if err != nil {
		t.Fatal(err)
	}

	metrics := object(object(fields["audit"])["metrics"])
	if engineCounter(metrics["token_usage"]) != 50001 || engineCounter(metrics["turns"]) != 1 ||
		engineCounter(fields["token_usage"]) != 0 || engineCounter(fields["turns"]) != 0 ||
		object(fields["audit"])["inferred_engine"] != "gemini" {
		t.Fatalf("inferred audit metrics rewrote immutable Run accounting: %s", mustJSON(t, fields))
	}
	findings, _ := defaultAuditFindings(fields)
	if !slices.Equal(modelAuditCodes(findings), []string{"high_token_usage"}) {
		t.Fatal("performance rule ignored audit-only fallback")
	}
	// Canonical field presence wins an alias even when the value is null.
	var record map[string]any
	_ = json.Unmarshal([]byte(`{"input_tokens":null,"inputTokens":7}`), &record)
	if nativeAlias(record, "input_tokens", "inputTokens") != nil {
		t.Fatal("explicit null was treated as absent")
	}
}

func TestNativeInferredTurnsDoNotRetainMissingFallbackGap(t *testing.T) {
	fields := map[string]any{"token_usage_summary": map[string]any{"total_input_tokens": 123, "total_output_tokens": 2}}
	gaps, err := enrichEngineAudit(t.Context(), fields, map[string][]byte{
		"agent/agent-stdio.log": []byte(`{"response":"PRIVATE_RESPONSE","stats":{"models":{"m":{"input_tokens":10,"output_tokens":1}}}}`),
	}, testIdentity(), Limits{})
	if err != nil || slices.Contains(gaps, "legacy-engine-fallback-unavailable") ||
		!slices.Contains(gaps, "engine-identity-unavailable") {
		t.Fatalf("fallback gap precedence: %v %v", gaps, err)
	}
	metrics := object(object(fields["audit"])["metrics"])
	if engineCounter(metrics["token_usage"]) != 125 || engineCounter(metrics["turns"]) != 1 {
		t.Fatalf("proxy precedes inferred tokens, inference recovers turns: %#v", metrics)
	}
	if _, present := fields["turns"]; present {
		t.Fatal("audit-only inferred turns changed immutable Run")
	}
}
