package collectnative

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

func TestNativeSubagentPinnedIdentityBridge(t *testing.T) {
	start := `{"type":"subagent.started","timestamp":"2026-01-01T00:00:00Z","data":{"toolCallId":"tool-1","agentName":"research","model":"sonnet"},"provenance":{"component":"agent","phase":"agent","native":{"persistedPath":"agent-session.jsonl","index":42}}}`
	done := `{"type":"subagent.completed","data":{"toolCallId":"tool-1","agentName":"research"},"provenance":{"component":"agent","phase":"agent"}}`
	metrics := `{"type":"session.shutdown","data":{"agentMetrics":{"sdk-1":{"agentName":"research","modelMetrics":{"anthropic/claude-sonnet-4-5":{"requests":{"count":2}}}}}},"provenance":{"component":"agent","phase":"agent"}}`
	native := `{"type":"subagent.started","agentId":"sdk-1","timestamp":"2026-01-01T00:00:00Z","data":{"toolCallId":"tool-1","agentName":"research","model":"sonnet"},"provenance":{"index":42}}`
	for _, test := range []struct {
		name, unified, native string
		want                  bool
	}{
		{"unique-native-proof", start + "\n" + done + "\n" + metrics, native, false},
		{"no-native-proof", start + "\n" + done + "\n" + metrics, "", true},
		{"timestamp-conflict", start + "\n" + done + "\n" + metrics, strings.ReplaceAll(native, "00:00:00Z", "00:00:01Z"), true},
		{"name-conflict", start + "\n" + done + "\n" + metrics, strings.ReplaceAll(native, "research", "other"), true},
		{"index-conflict", start + "\n" + done + "\n" + metrics, strings.ReplaceAll(native, `"index":42`, `"index":43`), true},
		{"persisted-path-conflict", strings.ReplaceAll(start, `"persistedPath":"agent-session.jsonl"`, `"persistedPath":"other/agent-session.jsonl"`) + "\n" + done + "\n" + metrics, native, true},
		{"duplicate-unified-start", start + "\n" + start + "\n" + done + "\n" + metrics, native, true},
		{"multiple-native-claims", start + "\n" + done + "\n" + metrics, native + "\n" + strings.ReplaceAll(native, "sdk-1", "sdk-2"), true},
	} {
		t.Run(test.name, func(t *testing.T) {
			files := map[string][]byte{
				"aw_info.json":     []byte(`{"sub_agent_models":[{"name":"research","model":"sonnet"}]}`),
				"aw_session.jsonl": []byte(subagentAuditHeader + test.unified),
			}
			if test.native != "" {
				files["agent-session.jsonl"] = []byte(test.native)
			}
			findings, _, gaps, err := subagentAuditFindings(t.Context(), map[string]any{}, files, testIdentity(), Limits{})
			if err != nil || len(gaps) != 0 ||
				slices.Contains(modelAuditCodes(findings), "subagent_model_not_observed") != test.want {
				t.Fatalf("strict identity bridge differs: findings=%v gaps=%v err=%v", findings, gaps, err)
			}
		})
	}
}

func TestNativeSubagentEffectiveGroupAndSnapshotReplacement(t *testing.T) {
	for _, test := range []struct {
		name   string
		models []string
		want   bool
	}{
		{"same-effective-model", []string{"anthropic/claude-haiku-4.5", "anthropic/claude-haiku-4.5"}, false},
		{"mixed-effective-model-is-not-proof", []string{"anthropic/claude-haiku-4.5", "gpt-5.3"}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			events := []string{subagentAuditHeader}
			for i, model := range test.models {
				id := []string{"a", "b"}[i]
				for _, kind := range []string{"subagent.started", "subagent.request", "subagent.completed"} {
					requested := "haiku"
					if kind == "subagent.request" {
						requested = model
					}
					raw, _ := json.Marshal(map[string]any{"type": kind, "agentId": id,
						"data":       map[string]any{"agentName": "research", "model": requested},
						"provenance": map[string]any{"component": "agent", "phase": "agent"}})
					events = append(events, string(raw))
				}
			}
			files := map[string][]byte{"aw_info.json": []byte(`{"sub_agent_models":[{"name":"research","model":"haiku?effort=high"}]}`),
				"aw_session.jsonl": []byte(strings.Join(events, "\n"))}
			findings, _, gaps, err := subagentAuditFindings(t.Context(), map[string]any{}, files, testIdentity(), Limits{})
			if err != nil || len(gaps) != 0 || slices.Contains(modelAuditCodes(findings), "subagent_model_not_observed") != test.want {
				t.Fatalf("effective model grouping differs: findings=%v gaps=%v err=%v", findings, gaps, err)
			}
			files["aw_session.jsonl"] = append(files["aw_session.jsonl"], []byte("\n"+`{"type":"session.shutdown","data":{"agentMetrics":{}},"provenance":{"component":"agent","phase":"agent"}}`)...)
			findings, _, gaps, err = subagentAuditFindings(t.Context(), map[string]any{}, files, testIdentity(), Limits{})
			if err != nil || len(gaps) != 0 || !slices.Contains(modelAuditCodes(findings), "subagent_model_not_observed") {
				t.Fatalf("SDK final snapshot did not replace earlier observed requests: findings=%v gaps=%v err=%v", findings, gaps, err)
			}
		})
	}
}
