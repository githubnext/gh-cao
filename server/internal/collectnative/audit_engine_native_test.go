package collectnative

import (
	"encoding/json"
	"errors"
	"math"
	"slices"
	"strings"
	"testing"
)

func TestNativeEngineAdditionalPinnedAccounting(t *testing.T) {
	// Independent expected values from github/gh-aw@v1.0.1
	// codex_logs_jsonl_test.go, pi_logs_v1_test.go, gemini_logs_test.go.
	for _, test := range []struct {
		name, engine, content string
		tokens, turns         int
		cost                  float64
	}{
		{"codex-turn-dedup-and-snapshots", "codex", `{"type":"thread.started"}
{"type":"turn.completed","turn_id":"turn","usage":{"input_tokens":100,"cached_input_tokens":80,"output_tokens":10,"reasoning_output_tokens":5}}
{"type":"turn.completed","turn_id":"turn","usage":{"input_tokens":100,"output_tokens":10}}
{"type":"thread.started"}
{"type":"turn.completed","turn_id":"turn","usage":{"input_tokens":200,"output_tokens":20}}
{"type":"result","num_turns":2,"usage":{"input_tokens":300,"output_tokens":30,"cache_read_input_tokens":260}}`, 330, 2, 0},
		{"codex-partial-zero-snapshot", "codex", `[
{"type":"session.result","data":{"numTurns":2,"usage":{"input_tokens":10,"output_tokens":5,"cache_read_input_tokens":20,"input_tokens_include_cache":false}}},
{"type":"session.result","data":{"usage":{"input_tokens":0}}}
]`, 25, 2, 0},
		{"codex-overflow-clears-field", "codex", `{"type":"turn.completed","usage":{"input_tokens":10,"output_tokens":2}}
{"type":"session.result","data":{"usage":{"overflowedTokens":["inputTokens"]}}}`, 2, 1, 0},
		{"codex-canonical-overflow-present", "codex", `{"type":"session.result","data":{"usage":{"inputTokens":10,"outputTokens":2,"overflowed_tokens":[],"overflowedTokens":["inputTokens"]}}}`, 12, 0, 0},
		{"codex-observed-total", "codex", `{"type":"turn.completed","usage":{"total_tokens":100,"input_tokens":70,"output_tokens":20}}`, 100, 1, 0},
		{"codex-unsafe-ignored", "codex", `{"type":"turn.completed","usage":{"input_tokens":9007199254740992,"output_tokens":1.5}}`, 0, 1, 0},
		{"gemini-sums-lines-not-last-response", "gemini", `not-json
{"response":"first response","stats":{"models":{"model-a":{"input_tokens":10,"output_tokens":5}},"tools":{"bash":{},"github.search":{}}}}
{"response":"","stats":{"models":{"model-a":{"input_tokens":1,"output_tokens":1}},"tools":{"bash":{}}}}`, 17, 1, 0},
		{"pi-update-not-double-counted", "pi", `{"type":"message_end","message":{"role":"assistant","timestamp":2,"content":[],"usage":{"input":100,"output":10,"cacheRead":20,"cacheWrite":5,"cost":{"total":0.1}}}}
{"type":"turn_end","message":{"role":"assistant","timestamp":2,"content":[],"usage":{"input":100,"output":10,"cacheRead":20,"cacheWrite":5,"cost":{"total":0.1}}}}
{"type":"message_end","message":{"role":"toolResult","toolCallId":"call-1","usage":{"input":50,"output":2,"cost":{"total":0.2}}}}
{"type":"compaction_end","result":{"firstKeptEntryId":"entry-1","usage":{"input":200,"output":20,"cost":{"total":0.3}}}}
{"type":"agent_end","messages":[{"role":"assistant","timestamp":2,"content":[],"usage":{"input":100,"output":10,"cacheRead":20,"cacheWrite":5,"cost":{"total":0.1}}},{"role":"toolResult","toolCallId":"call-1","usage":{"input":50,"output":2,"cost":{"total":0.2}}}]}`, 407, 1, .6},
		{"pi-updated-usage", "pi", `{"type":"message_end","message":{"role":"assistant","id":"a","usage":{"input":100,"output":10}}}
{"type":"turn_end","message":{"role":"assistant","id":"a","usage":{"totalTokens":0,"input":1000}}}`, 0, 1, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			fields := map[string]any{"engine_id": test.engine}
			gaps, err := enrichEngineAudit(t.Context(), fields, map[string][]byte{"agent/agent-stdio.log": []byte(test.content)}, testIdentity(), Limits{})
			if err != nil || len(gaps) != 0 || engineCounter(fields["token_usage"]) != test.tokens || engineCounter(fields["turns"]) != test.turns {
				t.Fatalf("pinned accounting mismatch: fields=%s gaps=%v err=%v", mustJSON(t, fields), gaps, err)
			}
			if test.cost > 0 {
				cost, _ := fields["estimated_cost"].(float64)
				if math.Abs(cost-test.cost) > .00001 {
					t.Fatalf("cost=%v want %v", cost, test.cost)
				}
			}
			if strings.Contains(string(mustJSON(t, fields)), "first response") {
				t.Fatal("response escaped transient metric parser")
			}
		})
	}
}

func TestNativeEngineAdditionalNestedWorkAndOverflow(t *testing.T) {
	for _, engine := range []string{"pi", "gemini", "codex"} {
		content := []byte(`{"type":"message_end","message":{"role":"assistant","usage":{"input":9007199254740991,"output":9007199254740991}}}`)
		if engine == "gemini" {
			content = []byte(`{"response":"x","stats":{"models":{"m":{"input_tokens":9007199254740991,"output_tokens":9007199254740991}}}}`)
		}

		if engine == "codex" {
			content = []byte(`{"type":"turn.completed","usage":{"input_tokens":9007199254740991,"output_tokens":9007199254740991}}`)
		}
		_, err := enrichEngineAudit(t.Context(), map[string]any{"engine_id": engine}, map[string][]byte{"agent/a.log": content}, testIdentity(), Limits{})
		if !errors.Is(err, ErrCapacity) {
			t.Fatalf("%s aggregate overflow accepted: %v", engine, err)
		}
	}
	record := map[string]any{"type": "agent_end", "messages": []any{
		map[string]any{"role": "assistant", "id": "a"}, map[string]any{"role": "assistant", "id": "b"},
	}}
	raw, _ := json.Marshal(record)
	_, err := enrichEngineAudit(t.Context(), map[string]any{"engine_id": "pi"},
		map[string][]byte{"agent/a.log": raw}, testIdentity(), Limits{Entries: 1})
	if !errors.Is(err, ErrCapacity) {
		t.Fatal("nested message work was not bounded")
	}
}

func TestNativeLegacyCodexPinnedMetrics(t *testing.T) {
	// Independent expectations from the pinned codex_logs_test.go legacy form.
	fields := map[string]any{"engine_id": "codex"}
	gaps, err := enrichEngineAudit(t.Context(), fields, map[string][]byte{
		"agent/agent-stdio.log": []byte("[2025-08-31T12:37:47] thinking\n" +
			"[2025-08-31T12:37:49] tool github.list_pull_requests({\"owner\":\"githubnext\"})\n" +
			"[2025-08-31T12:38:20] tokens used: 5000\n"),
	}, testIdentity(), Limits{})
	if err != nil || len(gaps) != 0 || engineCounter(fields["token_usage"]) != 5000 ||
		engineCounter(fields["turns"]) != 1 || engineCounter(object(fields["audit"])["native_tool_types"]) != 1 {
		t.Fatalf("pinned legacy metrics: fields=%#v gaps=%v err=%v", fields, gaps, err)
	}
}

func TestNativeNetworkPinnedRawPrecedenceAndFiltering(t *testing.T) {
	// Pinned firewall_log.go/test.go: HTTP status wins decision, only internal
	// Squid placeholder entries are excluded, destination IPs remain actionable.
	log := `# header
1761332530.474 172.30.0.20:35288 allowed.example:443 140.82.112.22:443 1.1 CONNECT 200 TCP_DENIED allowed.example:443 "private user agent"
1761332531.123 172.30.0.20:35289 blocked.example:443 140.82.112.23:443 1.1 CONNECT 403 TCP_TUNNEL blocked.example:443 "-"
1773003472.027 ::1:52010 - -:- 0.0 - 0 NONE_NONE:HIER_NONE error:transaction-end-before-headers "-"
1773003473.027 172.30.0.20:1 - 127.0.0.1:443 1.1 GET 407 TCP_HIT secret_url "-"
1773003474.027 172.30.0.20:1 - -:- 1.1 GET 0 NONE_NONE secret_url "-"
`
	fields := map[string]any{"firewall_analysis": map[string]any{"blocked_requests": json.Number("999")}}
	gaps, err := enrichNetworkAudit(t.Context(), fields, map[string][]byte{
		"agent/sandbox/firewall/logs/squid-logs/access.log": []byte(log),
		"agent/squid-logs-older/access.log":                 []byte(log),
	}, Limits{})
	firewall := object(fields["firewall_analysis"])
	if err != nil || len(gaps) != 0 || engineCounter(firewall["total_requests"]) != 4 ||
		engineCounter(firewall["blocked_requests"]) != 3 || engineCounter(firewall["allowed_requests"]) != 1 {
		t.Fatalf("raw precedence/count mismatch: %s %v %v", mustJSON(t, fields), gaps, err)
	}
	findings, _ := defaultAuditFindings(fields)
	if len(findings) != 1 || object(findings[0])["description"] != "Agent attempted to access blocked domains: 127.0.0.1:443, blocked.example:443" {
		t.Fatalf("pinned actionable-domain behavior: %s", mustJSON(t, findings))
	}
	if strings.Contains(string(mustJSON(t, fields)), "secret_url") || strings.Contains(string(mustJSON(t, fields)), "private user agent") {
		t.Fatal("raw network content persisted")
	}
	gaps, err = enrichNetworkAudit(t.Context(), map[string]any{}, map[string][]byte{
		"a/squid-logs/access.log": []byte(log), "b/squid-logs/access.log": []byte(log),
	}, Limits{})
	if err != nil || !slices.Contains(gaps, "ambiguous-firewall-logs") {
		t.Fatal("ambiguous raw network evidence silently selected")
	}
}
