package collectnative

import (
	"bytes"
	"errors"
	"slices"
	"testing"
)

func TestNativeDrain3PinnedIndependentDefaultWeights(t *testing.T) {
	// Executed independently against unmodified github/gh-aw@v1.0.1
	// pkg/agentdrain Go sources and its embedded default_weights.json:
	// basic tools => 1 anomalous new template; MCP + missing tool => 2.
	for _, test := range []struct {
		name, title, severity string
		fields                map[string]any
	}{
		{"basic", "1 anomalous event pattern(s) detected", "low", map[string]any{
			"conclusion": "success", "turns": 5, "token_usage": 1200,
			"audit": map[string]any{"native_tools": transientAuditTools{tools: map[string]int{"bash": 3, "github_issue_read": 2}}},
		}},
		{"errors", "2 anomalous event pattern(s) detected", "medium", map[string]any{
			"conclusion": "failure", "turns": 8, "error_count": 2,
			"audit": map[string]any{
				"mcp_failures": []any{map[string]any{"server_name": "github", "status": "connection_refused"},
					map[string]any{"server_name": "search", "status": "timeout"}},
				"native_reports": transientAuditReports{items: []map[string]any{
					{"type": "missing_tool", "tool": "terraform", "reason": "not installed"},
				}},
			},
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			insights, err := drainAuditInsights(t.Context(), test.fields, Limits{})
			if err != nil || len(insights) != 3 || object(insights[1])["title"] != test.title || object(insights[1])["severity"] != test.severity ||
				object(insights[0])["title"] != "Log template patterns mined" || object(insights[2])["title"] != "Agent stage sequence" {
				t.Fatalf("independent upstream default weights differ: %s %v", mustJSON(t, insights), err)
			}
		})
	}
}

func TestNativeDrain3TransientInputsAndLimits(t *testing.T) {
	fields := map[string]any{"conclusion": "success", "audit": map[string]any{
		"native_tools": transientAuditTools{tools: map[string]int{"bash_PRIVATE_COMMAND": 1}},
		"native_reports": transientAuditReports{items: []map[string]any{
			{"type": "noop", "message": "PRIVATE_PROMPT_OR_URL"},
		}},
	}}
	raw := mustJSON(t, fields)
	if bytes.Contains(raw, []byte("PRIVATE")) {
		t.Fatal("transient parser types can serialize raw inputs")
	}
	insights, err := drainAuditInsights(t.Context(), fields, Limits{})
	if err != nil || bytes.Contains(mustJSON(t, insights), []byte("PRIVATE")) {
		t.Fatal("templates or raw messages crossed the safe insight boundary")
	}
	_, err = drainAuditInsights(t.Context(), fields, Limits{Entries: 1})
	if !errors.Is(err, ErrCapacity) {
		t.Fatal("template work exceeded its declared capacity")
	}
	empty, err := drainAuditInsights(t.Context(), map[string]any{}, Limits{})
	if err != nil || len(empty) != 0 {
		t.Fatal("empty run fabricated template observations")
	}
	gaps, err := enrichAudit(t.Context(), map[string]any{"engine_id": "copilot"}, map[string][]byte{
		"agent/agent-stdio.log": []byte(`{"type":"assistant"}`),
	}, testIdentity(), Limits{})
	if err != nil || slices.Contains(gaps, "drain3-audit-unavailable") {
		t.Fatal("supported default templates still reported unavailable")
	}
}
