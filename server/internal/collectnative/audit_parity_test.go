package collectnative

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
)

var nativeEngineParityVectors = []struct {
	name, engine, content string
	tokens, turns, tools  int
	cost                  float64
}{
	{"codex-incremental", "codex", "thinking\nthinking\ntool github.read({})\nthinking\ntokens used: 1,000\ntokens used: 2,000", 3000, 2, 1, 0},
	{"codex-pretty-snapshot", "codex", "tokens used: 10\ntokens used\n12,507", 12507, 0, 0, 0},
	{"codex-pretty-blank-is-not-a-snapshot", "codex", "tokens used: 10\ntokens used\n\n12,507", 10, 0, 0, 0},
	{"codex-trace-snapshot", "codex", "tokens used: 10\n2026-01-01T10:00:00Z INFO codex: TokenCount { total_tokens: 60,001 }", 60001, 0, 0, 0},
	{"codex-explicit-zero", "codex", "thinking\ntokens used: 70,000\n" + `{"type":"session.result","data":{"numTurns":0,"totalCostUsd":0.25,"usage":{"totalTokens":0}}}`, 0, 0, 0, .25},
	{"codex-command-shapes", "codex", "exec echo hello in /workspace\ntool mcp__github__read({})\nToolCall: github__search {}\n", 0, 0, 3, 0},
	{"codex-old-tool-names", "codex", "[2026-01-01] tool namespace/read({})\ntool namespace:write({})\n", 0, 0, 2, 0},
	{"codex-tool-output-is-not-agent-usage", "codex", "thinking\ntool github.read({})\ngithub.read({}) success in 12ms:\n" +
		"[\n" + `{"type":"turn.completed","usage":{"total_tokens":999999}},` + "\n" +
		`{"type":"item.completed","item":{"type":"mcp_tool_call","id":"fake","server":"fake","tool":"fake","arguments":{}}}` +
		"\n]\ntokens used: 20", 20, 1, 1, 0},
	{"claude-noisy-stream", "claude", "prefix {'usage':{'input_tokens':100,'output_tokens':20},'billing':{'amount':0.25}}\n" +
		`{"usage":{"input_tokens":10,"output_tokens":5},"cost":0.5}`, 120, 0, 0, .25},
	{"claude-mixed-array-results", "claude", "noise\n[\n" +
		`{"type":"result","session_id":"a","num_turns":3,"usage":{"input_tokens":10,"cache_read_input_tokens":20},"total_cost_usd":0.4}` +
		"\n]\nnoise\n" + `{"type":"result","session_id":"b","num_turns":2,"usage":{"output_tokens":4},"total_cost_usd":0.2}`, 34, 5, 0, .6},
	{"copilot-wire-orphan", "copilot",
		`2026-01-01T00:00:00Z [DEBUG] Wire request: {"input":[{"type":"function_call","call_id":"c","name":"read"},{"type":"function_call_output","call_id":"c","output":"PRIVATE_TOOL_OUTPUT"}]}` +
			"\n2026-01-01T00:00:01Z [DEBUG] Wire request: " + `{"input":[{"type":"function_call","call_id":"c","name":"read"},{"type":"function_call_output","call_id":"c","output":"PRIVATE_TOOL_OUTPUT"}]}`,
		0, 0, 1, 0},
	{"copilot-wire-stub-later-input", "copilot",
		`2026-01-01T00:00:00Z [DEBUG] Wire request: {"input":[{"type":"function_call","call_id":"c","name":"read"},{"type":"function_call_output","call_id":"c","output":"PRIVATE_TOOL_OUTPUT"}]}` +
			"\n2026-01-01T00:00:01Z [DEBUG] data:\n" + `2026-01-01T00:00:01Z [DEBUG] {"usage":{"input_tokens":10,"output_tokens":2},"message":{"tool_calls":[{"function":{"name":"read","arguments":"{}"}}]}}`,
		12, 1, 1, 0},
	{"copilot-debug-cost", "copilot", "2026-01-01T00:00:00Z [DEBUG] data:\n" +
		`2026-01-01T00:00:00Z [DEBUG] {"usage":{"input_tokens":10,"output_tokens":2},"billing":{"amount":0.2}}`,
		12, 1, 0, .2},
}

func TestNativeLegacyEnginePinnedVectors(t *testing.T) {
	for _, test := range nativeEngineParityVectors {
		t.Run(test.name, func(t *testing.T) {
			fields := map[string]any{"engine_id": test.engine}
			gaps, err := enrichEngineAudit(t.Context(), fields, map[string][]byte{"agent-stdio.log": []byte(test.content)}, testIdentity(), Limits{})
			if err != nil || len(gaps) != 0 || engineCounter(fields["token_usage"]) != test.tokens ||
				engineCounter(fields["turns"]) != test.turns || engineCounter(object(fields["audit"])["native_tool_types"]) != test.tools {
				t.Fatalf("pinned engine vector: fields=%#v gaps=%v err=%v", fields, gaps, err)
			}
			if test.cost > 0 {
				cost, _ := fields["estimated_cost"].(float64)
				if cost < test.cost-.00001 || cost > test.cost+.00001 {
					t.Fatalf("cost=%v want=%v", cost, test.cost)
				}
			}
		})
	}
}

func TestNativePinnedEngineIndependentOracle(t *testing.T) {
	oracle := os.Getenv("CAO_UPSTREAM_ENGINE_ORACLE")
	if oracle == "" {
		t.Skip("set CAO_UPSTREAM_ENGINE_ORACLE to the independent pinned upstream Go executable")
	}
	inputs := make([]map[string]string, 0, len(nativeEngineParityVectors))
	for _, test := range nativeEngineParityVectors {
		inputs = append(inputs, map[string]string{"engine": test.engine, "log": test.content})
	}
	input, _ := json.Marshal(inputs)
	command := exec.CommandContext(t.Context(), oracle) //nolint:gosec // Explicitly selected, test-only independent upstream oracle; never a runtime dependency.
	command.Stdin = strings.NewReader(string(input))
	raw, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("independent oracle failed: %v %s", err, raw)
	}
	var rows []struct {
		TokenUsage, Turns int
		EstimatedCost     float64
		ToolCalls         []struct {
			Name      string
			CallCount int
		}
	}
	if err := json.Unmarshal(raw, &rows); err != nil || len(rows) != len(nativeEngineParityVectors) {
		t.Fatalf("oracle output: %v %s", err, raw)
	}
	for i, row := range rows {
		test := nativeEngineParityVectors[i]
		if row.TokenUsage != test.tokens || row.Turns != test.turns || len(row.ToolCalls) != test.tools ||
			row.EstimatedCost < test.cost-.00001 || row.EstimatedCost > test.cost+.00001 {
			t.Errorf("%s independent upstream disagrees: %#v", test.name, row)
		}
		records := []map[string]any{}
		if err := agentLogRecords(t.Context(), []byte(test.content), testIdentity(), Limits{}, func(record map[string]any) error {
			records = append(records, record)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		var actual engineAuditMetrics
		var err error
		if test.engine == "copilot" {
			actual, err = copilotLogMetrics(t.Context(), []byte(test.content), records, Limits{})
		} else {
			actual, err = legacyEngineMetrics(t.Context(), test.engine, []byte(test.content), records, testIdentity(), Limits{})
		}
		expected := map[string]int{}
		for _, tool := range row.ToolCalls {
			expected[tool.Name] = tool.CallCount
		}
		if err != nil || !maps.Equal(actual.tools, expected) {
			t.Errorf("%s independent tool accounting: native=%v upstream=%v err=%v", test.name, actual.tools, expected, err)
		}
	}
}

func TestNativeGatewayUsageSummaryFallbackAndBackfill(t *testing.T) {
	fields, summary := map[string]any{}, map[string]any{}
	if err := decode([]byte(`{"gateway":{"tools":[
		{"server_name":"github","tool_name":"read","call_count":7,"failed_calls":1,"total_input_size":70,"max_input_size":30,"avg_duration_ms":15},
		{"server_name":"stale","tool_name":"never_add","call_count":100}
	],"servers":[{"server_name":"github","request_count":8,"tool_call_count":7,"failed_calls":1}],
	"tool_calls":[{"server_name":"github","tool_name":"read","request_size":10,"duration_ms":15,"outcome":"success"}]}}`), &summary); err != nil {
		t.Fatal(err)
	}
	if err := applyAuditSummary(t.Context(), fields, summary, Limits{}); err != nil {
		t.Fatal(err)
	}
	if got := mergedAuditTools(fields, nil); got["github.read"] != 7 || got["stale.never_add"] != 100 {
		t.Fatalf("usage tools summary discarded before serialization: %v", got)
	}
	call := object(array(object(fields["mcp_tool_usage"])["tool_calls"])[0])
	if call["duration"] != "15ms" || engineCounter(call["output_size"]) != 0 {
		t.Fatalf("summary call duration/default counters lost: %v", call)
	}
	files := map[string][]byte{"gateway.jsonl": []byte(`{"event":"tool_call","server_name":"github","tool_name":"read","input_size":10}`)}
	if gaps, err := enrichGatewayAudit(t.Context(), fields, files, testIdentity(), Limits{}); err != nil || len(gaps) != 0 {
		t.Fatalf("raw gateway: %v %v", gaps, err)
	}
	usage := object(fields["mcp_tool_usage"])
	got := mergedAuditTools(fields, nil)
	if len(got) != 1 || got["github.read"] != 1 {
		t.Fatalf("raw tool summary did not own precedence: %v", got)
	}
	tool := object(array(usage["summary"])[0])
	if engineCounter(tool["avg_input_size"]) != 10 || engineCounter(tool["max_input_size"]) != 10 ||
		tool["avg_duration"] != "15ms" || engineCounter(tool["error_count"]) != 0 {
		t.Fatalf("matching numeric-only backfill overwrote actual counters: %v", tool)
	}
	if err := applyAuditSummary(t.Context(), fields, summary, Limits{}); err != nil {
		t.Fatal(err)
	}
	if gaps, err := enrichGatewayAudit(t.Context(), fields, map[string][]byte{
		"gateway.jsonl": []byte(`{"event":"heartbeat","server_name":"unused"}`),
	}, testIdentity(), Limits{}); err != nil || len(gaps) != 0 || mergedAuditTools(fields, nil)["github.read"] != 7 {
		t.Fatalf("irrelevant raw events suppressed summary fallback: fields=%v gaps=%v err=%v", fields, gaps, err)
	}
}

func TestNativeGatewayRPCV2SequentialMetrics(t *testing.T) {
	fields := map[string]any{}
	content := `{"event":"rpc_response","direction":"IN","server_id":"github","timestamp":"2026-01-01T00:00:02Z","payload":{"id":"early","result":{}}}
{"event":"rpc_request","direction":"OUT","server_id":"github","timestamp":"2026-01-01T00:00:01Z","payload":{"id":"early","method":"tools/call","params":{"name":"read"}}}
{"event":"rpc_request","direction":"OUT","server_id":"github","timestamp":"2026-01-01T00:00:02Z","payload":{"id":"later","method":"tools/call","params":{"name":"read"}}}
{"event":"rpc_response","direction":"IN","server_id":"github","timestamp":"2026-01-01T00:00:03Z","payload":{"id":"later","error":{"code":-32001,"message":"PRIVATE_RPC_ERROR"}}}
{"event":"difc_filtered","server_id":"github","tool_name":"read"}
`
	gaps, err := enrichGatewayAudit(t.Context(), fields, map[string][]byte{"rpc-messages.jsonl": []byte(content)}, testIdentity(), Limits{})
	usage := object(fields["mcp_tool_usage"])
	servers, tools, calls := array(usage["servers"]), array(usage["summary"]), array(usage["tool_calls"])
	if err != nil || len(gaps) != 0 || len(servers) != 1 || len(tools) != 1 || len(calls) != 2 {
		t.Fatalf("production RPC telemetry missing: fields=%v gaps=%v err=%v", fields, gaps, err)
	}
	server, tool := object(servers[0]), object(tools[0])
	if engineCounter(server["request_count"]) != 2 || engineCounter(server["error_count"]) != 1 ||
		engineCounter(server["difc_filtered_count"]) != 1 || engineCounter(server["guard_policy_blocked_count"]) != 1 ||
		tool["avg_duration"] != "500ms" || engineCounter(tool["error_count"]) != 1 {
		t.Fatalf("RPC sequential metrics differ from two-pass call pairing: server=%v tool=%v", server, tool)
	}
	if strings.Contains(string(mustJSON(t, fields)), "PRIVATE") {
		t.Fatal("RPC error escaped transient parsing")
	}
}

func TestNativeGatewayRejectsPartialOrUnsafeEvidence(t *testing.T) {
	for _, content := range []string{
		"{\"event\":\"tool_call\",\"server_name\":\"github\",\"tool_name\":\"read\"}\n{bad-tail",
		`{"event":"tool_call","server_name":"github","tool_name":"read","status":"PRIVATE_STATUS"}`,
		`{"event":"tool_call","server_name":"github","tool_name":"read","input_size":-1}`,
		`{"event":"tool_call","server_name":"github","tool_name":"read","duration":-1}`,
	} {
		fields := map[string]any{}
		gaps, err := enrichGatewayAudit(t.Context(), fields, map[string][]byte{"gateway.jsonl": []byte(content)}, testIdentity(), Limits{})
		if err != nil || len(gaps) == 0 || fields["mcp_tool_usage"] != nil {
			t.Fatalf("malformed source partially published: fields=%v gaps=%v err=%v", fields, gaps, err)
		}
	}
	for _, suffix := range []string{`"outcome":"PRIVATE_STATUS"`, `"timestamp":"PRIVATE_TIMESTAMP"`, `"tool_name":"PRIVATE NAME"`} {
		var summary map[string]any
		if err := decode([]byte(`{"gateway":{"tool_calls":[{`+suffix+`}]}}`), &summary); err != nil {
			t.Fatal(err)
		}
		fields := map[string]any{}
		if err := applyAuditSummary(t.Context(), fields, summary, Limits{}); err == nil || fields["mcp_tool_usage"] != nil {
			t.Fatalf("unsafe gateway summary partially published: fields=%v err=%v", fields, err)
		}
	}
}

func TestNativeGatewayRawAndSummaryPrecedence(t *testing.T) {
	fields := map[string]any{"engine_id": "copilot", "mcp_tool_usage": map[string]any{"tool_calls": []any{
		map[string]any{"server_name": "summary", "tool_name": "stale"},
	}}}
	raw := `{"event":"request","server_name":"github","method":"tools/list"}
{"event":"tool_call","server_name":"github","tool_name":"read","input_size":10,"output_size":20}
{"event":"request","method":"tools/call","server_name":"github","tool_name":"read","level":"error","error":"PRIVATE_ERROR"}
{"event":"response","server_name":"github","tool_name":"failed_only","status":"error"}
{"event":"tool_call","server_name":"github","tool_name":"search","duration":12.5}`
	gaps, err := enrichGatewayAudit(t.Context(), fields, map[string][]byte{
		"agent/gateway.jsonl":               []byte(raw),
		"agent/mcp-logs/gateway.jsonl":      []byte(`{"event":"tool_call","server_name":"ignored","tool_name":"ignored"}`),
		"agent/mcp-logs/rpc-messages.jsonl": []byte(`{"direction":"OUT","type":"REQUEST","server_id":"ignored"}`),
	}, testIdentity(), Limits{})
	usage := object(fields["mcp_tool_usage"])
	if err != nil || len(gaps) != 0 || len(array(usage["tool_calls"])) != 3 {
		t.Fatalf("raw source/call precedence: %#v %v %v", usage, gaps, err)
	}
	tools := mergedAuditTools(fields, map[string]int{"github.read": 1})
	if len(tools) != 3 || tools["github.read"] != 3 || tools["github.search"] != 1 {
		t.Fatalf("summary owns cardinality/counts, even error-only zero-call tool: %#v", tools)
	}
	server := object(array(usage["servers"])[0])
	if engineCounter(server["request_count"]) != 4 || engineCounter(server["error_count"]) != 2 {
		t.Fatalf("protocol request and error health counters: %#v", server)
	}
	if strings.Contains(string(mustJSON(t, fields)), "PRIVATE_ERROR") {
		t.Fatal("gateway payload/error leaked")
	}
	_, err = enrichEngineAudit(t.Context(), fields, map[string][]byte{"agent-stdio.log": []byte(`{"type":"result","num_turns":1,"usage":{"input_tokens":1}}`)}, testIdentity(), Limits{})
	if err != nil || engineCounter(object(fields["audit"])["native_tool_types"]) != 3 {
		t.Fatalf("gateway tools not used by assessments: %#v %v", fields, err)
	}
}

func TestNativeGatewayRPCIdentityAndPrivacy(t *testing.T) {
	fields := map[string]any{}
	raw := `{"direction":"OUT","type":"REQUEST","server_id":"github","timestamp":"2026-01-01T00:00:00Z","payload":{"id":1,"method":"tools/call","params":{"name":"read","arguments":{"secret":"PRIVATE"}}}}
{"direction":"OUT","type":"REQUEST","server_id":"other","timestamp":"2026-01-01T00:00:00Z","payload":{"id":1,"method":"tools/call","params":{"name":"search"}}}
{"direction":"IN","type":"RESPONSE","server_id":"github","timestamp":"2026-01-01T00:00:01Z","payload":{"id":1,"error":{"code":-32001,"message":"PRIVATE"}}}`
	gaps, err := enrichGatewayAudit(t.Context(), fields, map[string][]byte{"mcp-logs/rpc-messages.jsonl": []byte(raw)}, testIdentity(), Limits{})
	calls := array(object(fields["mcp_tool_usage"])["tool_calls"])
	if err != nil || len(gaps) != 0 || len(calls) != 2 || object(calls[0])["status"] != "error" ||
		object(calls[0])["duration"] != "1s" || object(calls[1])["status"] != "unknown" {
		t.Fatalf("RPC server-scoped pairing: %#v %v %v", calls, gaps, err)
	}
	if strings.Contains(string(mustJSON(t, fields)), "PRIVATE") {
		t.Fatal("RPC payload/error leaked")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := enrichGatewayAudit(ctx, nil, nil, testIdentity(), Limits{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
}

func TestNativeSubagentDeclaredAliasAndPiVectors(t *testing.T) {
	for _, test := range []struct {
		name, requested, observed, provider string
		patterns                            []string
		want                                bool
	}{
		{"alias-dated", "haiku", "claude-haiku-4-5-20251001", "anthropic", nil, false},
		{"alias-provider-mismatch", "haiku", "claude-haiku-4.5", "openai", nil, true},
		{"custom-alias", "tiny", "gpt-5.3-mini", "openai", []string{"mini"}, false},
		{"nested-alias", "detection", "claude-haiku-4.5", "anthropic", nil, false},
		{"not-observed", "haiku", "gpt-5.3", "openai", nil, true},
		{"qualified-sdk-provider", "haiku", "copilot/claude-haiku-4.5:copilot", "copilot-responses-1", nil, false},
		{"inherited", "inherit", "claude-sonnet-4-5-20250929", "anthropic", nil, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			patterns := make([]any, len(test.patterns))
			for i, value := range test.patterns {
				patterns[i] = value
			}
			info, _ := json.Marshal(map[string]any{"model": "sonnet", "sub_agent_models": []any{
				map[string]any{"name": "research", "model": test.requested, "patterns": patterns},
			}})
			fields := map[string]any{"token_usage_summary": map[string]any{"by_model": map[string]any{
				test.observed: map[string]any{"requests": 1, "provider": test.provider},
			}}}
			findings, _, gaps, err := subagentAuditFindings(t.Context(), fields, map[string][]byte{"aw_info.json": info}, testIdentity(), Limits{})
			if err != nil || len(gaps) != 0 || slices.Contains(modelAuditCodes(findings), "subagent_model_not_observed") != test.want {
				t.Fatalf("alias oracle mismatch: findings=%#v gaps=%v err=%v", findings, gaps, err)
			}
		})
	}
	info := []byte(`{"sub_agent_models":[{"name":"research","model":"haiku"}]}`)
	content := subagentAuditHeader +
		`{"type":"session.init","timestamp":"2026-01-01T00:00:00Z","data":{"sessionId":"old","sourceEngine":"pi"},"provenance":{"component":"agent","phase":"agent"}}` + "\n" +
		`{"type":"pi.subagent_dispatch","data":{"invocationId":"old","agent":"research","requestedModel":"haiku"},"provenance":{"component":"agent","phase":"agent"}}` + "\n" +
		`{"type":"pi.subagent_result","data":{"invocationId":"old","agent":"research","outcome":"failed"},"provenance":{"component":"agent","phase":"agent"}}` + "\n" +
		`{"type":"session.init","timestamp":"2026-01-02T00:00:00Z","data":{"sessionId":"new","sourceEngine":"pi"},"provenance":{"component":"agent","phase":"agent"}}` + "\n" +
		`{"type":"pi.subagent_dispatch","data":{"invocationId":"new","agent":"research","requestedModel":"haiku"},"provenance":{"component":"agent","phase":"agent"}}` + "\n" +
		`{"type":"pi.subagent_event","data":{"invocationId":"new","agent":"research","event":{"message":{"responseModel":"anthropic/claude-haiku-4.5","usage":{"input":10}}}},"provenance":{"component":"agent","phase":"agent"}}` + "\n" +
		`{"type":"pi.subagent_result","data":{"invocationId":"new","agent":"research","outcome":"completed"},"provenance":{"component":"agent","phase":"agent"}}`
	findings, recommendations, gaps, err := subagentAuditFindings(t.Context(), map[string]any{}, map[string][]byte{
		"aw_info.json": info, "aw_session.jsonl": []byte(content),
	}, testIdentity(), Limits{})
	if err != nil || len(gaps) != 0 || len(findings) != 0 || len(recommendations) != 0 {
		t.Fatalf("latest Pi invocation/model evidence: findings=%#v gaps=%v err=%v", findings, gaps, err)
	}
}
