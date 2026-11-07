package collectnative

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

const subagentAuditHeader = "{\"type\":\"session.format\",\"data\":{\"version\":1},\"provenance\":{\"component\":\"collector\"}}\n"

func TestNativeDeclaredSubagentModelNotObserved(t *testing.T) {
	fields := map[string]any{"conclusion": "success", "token_usage_summary": map[string]any{
		"by_model": map[string]any{"gpt-5.3": map[string]any{"requests": 1}}}}
	gaps, err := enrichAudit(t.Context(), fields, map[string][]byte{
		"usage/aw_info.json": []byte(`{"engine_id":"copilot","sub_agent_models":[{"name":"research","model":"claude-haiku-4.5","patterns":["haiku"]}]}`),
	}, testIdentity(), Limits{})
	if err != nil || slices.Contains(gaps, "declared-subagent-model-attribution-unavailable") ||
		!slices.Contains(modelAuditCodes(array(object(fields["audit"])["key_findings"])), "subagent_model_not_observed") {
		t.Fatalf("declared-model finding missing: fields=%#v gaps=%v err=%v", fields, gaps, err)
	}
}

func TestNativeSubagentPinnedUnavailableIsWarningNotFailure(t *testing.T) {
	// github/gh-aw@v1.0.1 audit_subagent_model_test.go:
	// duplicate complete descriptors produce one tooling warning, not traffic
	// attribution or a failed delegation.
	event := `{"type":"subagent.model_unavailable","data":{"agentName":"research","declaredModel":"claude-haiku-4.5","model":"anthropic/claude-opus-4.8"},"provenance":{"component":"agent","phase":"agent"}}`
	fields := map[string]any{"conclusion": "success"}
	findings, recommendations, gaps, err := subagentAuditFindings(t.Context(), fields,
		map[string][]byte{"usage/aw_session.jsonl": []byte(subagentAuditHeader + event + "\n" + event)}, testIdentity(), Limits{})
	if err != nil || len(gaps) != 0 || !slices.Equal(modelAuditCodes(findings), []string{"subagent_model_unavailable"}) ||
		len(recommendations) != 0 || object(findings[0])["severity"] != "medium" || object(findings[0])["category"] != "tooling" {
		t.Fatalf("pinned unavailable contract mismatch: %s %v %v", mustJSON(t, findings), gaps, err)
	}
	if fields["subagent_failure_count"] != nil {
		t.Fatal("availability warning fabricated delegated failure")
	}
	for _, invalid := range []string{
		strings.ReplaceAll(event, `"component":"agent"`, `"component":"detection"`),
		strings.ReplaceAll(event, `"phase":"agent"`, `"phase":"detection"`),
		strings.ReplaceAll(event, `"model":"anthropic/claude-opus-4.8"`, `"model":42`),
		event + "\n{bad-tail",
	} {
		findings, _, _, err := subagentAuditFindings(t.Context(), map[string]any{},
			map[string][]byte{"usage/aw_session.jsonl": []byte(subagentAuditHeader + invalid)}, testIdentity(), Limits{})
		if err != nil || len(findings) != 0 {
			t.Fatal("invalid/wrong-phase source established a warning")
		}
	}
}

func TestNativeSubagentPinnedLifecycleFailureAggregation(t *testing.T) {
	events := make([]map[string]any, 0, 6)
	for _, id := range []string{"a", "b"} {
		for _, kind := range []string{"subagent.started", "subagent.failed", "subagent.completed"} {
			events = append(events, map[string]any{"type": kind, "agentId": id, "provenance": map[string]any{"component": "agent", "phase": "agent"},
				"data": map[string]any{"agentName": "research", "model": "claude-haiku-4.5", "cancelled": true, "errorMessage": "PRIVATE_DELEGATION_ERROR"}})
		}
	}
	var content bytes.Buffer
	content.WriteString(subagentAuditHeader)
	for _, event := range events {
		raw, _ := json.Marshal(event)
		content.Write(raw)
		content.WriteByte('\n')
	}
	fields := map[string]any{"conclusion": "success"}
	findings, recommendations, gaps, err := subagentAuditFindings(t.Context(), fields,
		map[string][]byte{"usage/aw_session.jsonl": content.Bytes()}, testIdentity(), Limits{})
	if err != nil || len(gaps) != 0 || len(findings) != 1 || object(findings[0])["code"] != "subagent_failed" ||
		object(findings[0])["severity"] != "high" || object(findings[0])["description"] != "Sub-agent research failed 2 invocation(s)" ||
		engineCounter(object(fields["audit"])["subagent_failure_count"]) != 2 || len(recommendations) != 1 {
		t.Fatalf("delegation failure aggregation differs: %s %v %v", mustJSON(t, fields), gaps, err)
	}
	if bytes.Contains(mustJSON(t, findings), []byte("PRIVATE")) {
		t.Fatal("delegation diagnostics escaped transient parsing")
	}
}
