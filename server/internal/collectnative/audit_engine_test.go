package collectnative

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestEngineCounterBounds(t *testing.T) {
	for _, value := range []any{
		json.Number("-1"), json.Number("1.5"), json.Number("9007199254740992"),
		json.Number("9223372036854775807"), json.Number("18446744073709551615"),
		math.NaN(), math.Inf(1), math.Inf(-1), float64(maxEngineCounter) + 1,
	} {
		if got := engineCounter(value); got != 0 {
			t.Fatalf("invalid counter %v admitted as %d", value, got)
		}
	}
	for _, value := range []any{json.Number("42"), json.Number("4.2e1"), float64(42), 42} {
		if got := engineCounter(value); got != 42 {
			t.Fatalf("valid counter %v changed to %d", value, got)
		}
	}
	if got := engineCounter(json.Number(fmt.Sprint(maxEngineCounter))); got != maxEngineCounter {
		t.Fatalf("maximum counter changed to %d", got)
	}
	total := maxEngineCounter - 1
	if err := addEngineCounter(&total, 1); err != nil || total != maxEngineCounter {
		t.Fatalf("valid aggregate rejected: total=%d err=%v", total, err)
	}
	if err := addEngineCounter(&total, 1); !errors.Is(err, ErrCapacity) || total != maxEngineCounter {
		t.Fatalf("overflow changed aggregate: total=%d err=%v", total, err)
	}
}

func TestNativeEnginePinnedPrimaryAndDebugMetrics(t *testing.T) {
	// github/gh-aw@v1.0.1 copilot_events_jsonl_test.go full session:
	// 799195 + 6148 = 805343, two user turns, cache tokens excluded.
	// workflow/copilot_engine_logs_test.go multiline debug: 3325 tokens.
	for _, test := range []struct {
		name                 string
		files                map[string][]byte
		wantTokens, wantTurn int
	}{
		{"primary-events", map[string][]byte{
			"agent/copilot-session-state/session/events.jsonl": jobFixture(t, "gh-aw-v1.0.1-copilot-events.jsonl"),
			"agent/agent-stdio.log":                            jobFixture(t, "gh-aw-v1.0.1-copilot-debug.log"),
		}, 805343, 2},
		{"debug", map[string][]byte{
			"agent/agent-stdio.log": jobFixture(t, "gh-aw-v1.0.1-copilot-debug.log"),
		}, 3325, 2},
		{"raw-files-sum-tokens-max-turns", map[string][]byte{
			"agent/agent-stdio.log": jobFixture(t, "gh-aw-v1.0.1-copilot-debug.log"),
			"agent/debug.log":       jobFixture(t, "gh-aw-v1.0.1-copilot-debug.log"),
			"workflow-logs/a.log":   jobFixture(t, "gh-aw-v1.0.1-copilot-debug.log"),
			"detection/agent.log":   jobFixture(t, "gh-aw-v1.0.1-copilot-debug.log"),
		}, 6650, 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			fields := map[string]any{"engine_id": "copilot"}
			gaps, err := enrichEngineAudit(t.Context(), fields, test.files, testIdentity(), Limits{})
			if err != nil || len(gaps) != 0 || engineCounter(fields["token_usage"]) != test.wantTokens ||
				engineCounter(fields["turns"]) != test.wantTurn {
				t.Fatalf("pinned metric discrepancy: %s gaps=%v err=%v", mustJSON(t, fields), gaps, err)
			}
			if fields["token_usage_summary"] != nil {
				t.Fatal("total usage fabricated a structured input/output/AIC report")
			}
		})
	}
}

func TestNativeEnginePinnedFallbackAndSnapshots(t *testing.T) {
	for _, test := range []struct {
		name, engine, content string
		tokens, turns         int
	}{
		// Copilot event fallback examples from pinned copilot_events_jsonl_test.go.
		{"events-fallback", "events", `{"type":"user.message","data":{}}
{"type":"assistant.message","data":{"usage":{"input_tokens":400,"output_tokens":50,"cache_read_input_tokens":9000}}}
{"type":"assistant.message","data":{"usage":{"input_tokens":100,"output_tokens":10}}}`, 560, 1},
		{"events-explicit-zero", "events", `{"type":"user.message","data":{}}
{"type":"assistant.message","data":{"usage":{"input_tokens":400,"output_tokens":50}}}
{"type":"session.shutdown","data":{"modelMetrics":{}}}`, 0, 1},
		{"events-aliases", "events", `{"type":"assistant.message","data":{"usage":{"prompt_tokens":50,"completion_tokens":2}}}
{"type":"assistant.message","data":{"input_tokens":30,"output_tokens":3}}
{"type":"assistant.message","usage_input_tokens":20,"usage_output_tokens":4}
{"type":"assistant.message","usageInputTokens":10,"usageOutputTokens":5}`, 124, 0},
		{"copilot-result-replaces", "copilot", `{"type":"assistant"}
{"type":"result","num_turns":12,"usage":{"input_tokens":50001,"output_tokens":4,"cache_read_input_tokens":1000}}`, 50005, 12},
		{"claude-session-replaces-and-child-excluded", "claude", `[{"type":"result","session_id":"one","num_turns":1,"usage":{"input_tokens":100}},
{"type":"result","session_id":"one","num_turns":12,"total_cost_usd":0.5,"usage":{"input_tokens":25,"output_tokens":832,"cache_creation_input_tokens":34686,"cache_read_input_tokens":120119}},
{"type":"result","session_id":"one","parent_tool_use_id":"child","num_turns":99,"usage":{"input_tokens":900000}},
{"type":"result","session_id":"two","num_turns":2,"usage":{"input_tokens":5}}]`, 155667, 14},
	} {
		t.Run(test.name, func(t *testing.T) {
			name := "agent/agent-stdio.log"
			if test.engine == "events" {
				name = "agent/events.jsonl"
			}
			fields := map[string]any{"engine_id": test.engine}
			if test.engine == "events" {
				fields["engine_id"] = "copilot"
			}
			gaps, err := enrichEngineAudit(t.Context(), fields, map[string][]byte{name: []byte(test.content)}, testIdentity(), Limits{})
			if err != nil || len(gaps) != 0 || engineCounter(fields["token_usage"]) != test.tokens || engineCounter(fields["turns"]) != test.turns {
				t.Fatalf("snapshot discrepancy: %s gaps=%v err=%v", mustJSON(t, fields), gaps, err)
			}
		})
	}
}

func TestNativeEngineInvalidEvidenceAndWorkBounds(t *testing.T) {
	valid := jobFixture(t, "gh-aw-v1.0.1-copilot-events.jsonl")
	for _, content := range [][]byte{
		append(bytes.Clone(valid), []byte("{bad\n")...),
		append(bytes.Clone(valid), []byte("{\"run_attempt\":2}\n")...),
		append(bytes.Clone(valid), []byte("{\"type\":\"session.shutdown\",\"data\":{\"modelMetrics\":{\"m\":{\"usage\":{\"inputTokens\":\"50001\"}}}}}\n")...),
	} {
		fields := map[string]any{"engine_id": "copilot"}
		gaps, err := enrichEngineAudit(t.Context(), fields, map[string][]byte{"agent/events.jsonl": content}, testIdentity(), Limits{})
		if err != nil || !slices.Contains(gaps, "invalid-engine-events") || fields["token_usage"] != nil {
			t.Fatalf("invalid tail published partial counters: %s %v %v", mustJSON(t, fields), gaps, err)
		}
	}
	for _, limit := range []Limits{{Bytes: 4}, {Entries: 1}, {LineBytes: 4}} {
		_, err := enrichEngineAudit(t.Context(), map[string]any{"engine_id": "copilot"},
			map[string][]byte{"agent/events.jsonl": valid}, testIdentity(), limit)
		if !errors.Is(err, ErrCapacity) {
			t.Fatalf("engine metric budget ignored: %+v %v", limit, err)
		}
	}
	// Two arrays can individually fit a record budget but exceed total work.
	_, err := enrichEngineAudit(t.Context(), map[string]any{"engine_id": "claude"},
		map[string][]byte{"agent/a.log": []byte(`[{},{}]`), "agent/b.log": []byte(`[{},{}]`)},
		testIdentity(), Limits{Entries: 3})
	if !errors.Is(err, ErrCapacity) {
		t.Fatal("cross-file array work was unbounded")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := enrichEngineAudit(ctx, map[string]any{}, map[string][]byte{"agent/a.log": valid}, testIdentity(), Limits{}); !errors.Is(err, context.Canceled) {
		t.Fatal("engine parsing ignored cancellation")
	}
	fields := map[string]any{"engine_id": "unsupported"}
	gaps, err := enrichEngineAudit(t.Context(), fields, map[string][]byte{"agent/agent-stdio.log": []byte(`{"type":"agent_end"}`)}, testIdentity(), Limits{})
	if !errors.Is(err, ErrUnsupported) || len(gaps) != 0 || fields["turns"] != nil {
		t.Fatal("unsupported engine was reported as a successful zero")
	}
}

func TestNativeDefaultAuditThresholdsAndRecommendationTriggers(t *testing.T) {
	for _, test := range []struct {
		tokens, turns string
		want          []string
	}{
		{"50000", "10", nil},
		{"50001", "10", []string{"high_token_usage"}},
		{"50000", "11", []string{"many_iterations"}},
	} {
		findings, recommendations := defaultAuditFindings(map[string]any{
			"token_usage": json.Number(test.tokens), "turns": json.Number(test.turns),
		})
		if !slices.Equal(modelAuditCodes(findings), test.want) {
			t.Fatalf("pinned thresholds changed: %s", mustJSON(t, findings))
		}
		if (test.turns == "10" && len(recommendations) != 0) || (test.turns == "11" && len(recommendations) != 1) {
			t.Fatal("high-token performance finding became a cost-category recommendation")
		}
	}
}

func TestNativeAuditDefaultFindingsIndependentCanonicalOracle(t *testing.T) {
	fields := map[string]any{"repository": "octo/api", "organization": "octo", "run_id": 303, "run_attempt": 1,
		"workflow_name": "Test", "workflow_path": ".github/workflows/test.lock.yml", "engine_id": "copilot",
		"status": "completed", "conclusion": "success", "created_at": "2026-10-15T10:00:00Z",
		"started_at": "2026-10-15T10:00:00Z", "updated_at": "2026-10-15T11:00:00Z"}
	content := append(bytes.Repeat([]byte("{\"type\":\"user.message\",\"data\":{}}\n"), 11),
		jobFixture(t, "gh-aw-v1.0.1-copilot-events.jsonl")...)
	files := map[string][]byte{
		"agent/events.jsonl":     content,
		"usage/aw_session.jsonl": detectionEvent(`{"jobResult":"failure","conclusion":"failure","reason":"threat_detected","promptInjection":true,"secretLeak":false,"maliciousPatch":true}`),
		"agent/agent-stdio.log": []byte(`{"type":"system","subtype":"init","timestamp":"2026-10-15T10:01:00Z","mcp_servers":[{"name":"github","status":"failed","error":"PRIVATE_MCP_ERROR"},{"name":"healthy","status":"connected"}]}
{"type":"user","message":{"content":"PRIVATE_PROMPT"}}`),
		"usage/agent_output.json":     []byte(`{"items":[{"type":"report_missing_tool","tool":"github.search","reason":"PRIVATE_REASON"}]}`),
		"usage/activity/summary.json": jobFixture(t, "usage-activity-summary.json"),
	}
	gaps, err := enrichAudit(t.Context(), fields, files, testIdentity(), Limits{})
	if err != nil || len(gaps) != 0 {
		t.Fatalf("supported source family incomplete: %v %v", gaps, err)
	}
	evidence := Evidence{Version: 1, Identity: testIdentity(), Run: fields, Gaps: []string{"legacy-audit-unavailable"}}
	input, _, err := evidence.Bytes()
	if err != nil || bytes.Contains(input, []byte("PRIVATE")) {
		t.Fatalf("raw log or report text persisted: %v", err)
	}
	// Expected codes/titles/priorities are pinned generatePerformanceFindings,
	// generateToolingFindings, generateFirewallFindings and recommendations,
	// not a replay of the native finding output.
	want := oracle(t, `
import { adaptCachedGhAwJsonl } from './dashboard/site/src/data/adapters/gh-aw-logs.js';
import { normalize } from './dashboard/site/src/data/normalize/index.js';
let raw=''; for await(const chunk of process.stdin) raw+=chunk;
const e=JSON.parse(raw), run=e.run;
run.audit.key_findings=[
 {code:'threat_detection_job_failed',category:'security',severity:'high',title:'Threat Detection Job Failed'},
 {code:'threat_detected',category:'security',severity:'critical',title:'Security Threat Detected'},
 {code:'high_token_usage',category:'performance',severity:'medium',title:'High Token Usage'},
 {code:'many_iterations',category:'performance',severity:'medium',title:'Many Iterations'},
 {code:'mcp_server_failures',category:'tooling',severity:'high',title:'MCP Server Failures'},
 {code:'tools_not_available',category:'tooling',severity:'medium',title:'Tools Not Available'},
 {code:'blocked_network_requests',category:'network',severity:'medium',title:'Blocked Network Requests'},
 {code:'workflow_succeeded',category:'success',severity:'info',title:'Workflow Completed Successfully'},
 {code:'agentic_resource_heavy_for_domain',category:'performance',severity:'high',title:'Resource Heavy For Domain'},
 {code:'agentic_partially_reducible',category:'optimization',severity:'medium',title:'Partially Reducible To Deterministic'}
];
run.audit.recommendations=[
 {priority:'high',action:'Review error logs to identify root cause of failure'},
 {priority:'medium',action:'Clarify workflow instructions or break into smaller tasks'},
 {priority:'medium',action:'Add missing tools to workflow configuration'},
 {priority:'high',action:'Fix MCP server configuration or dependencies'},
 {priority:'medium',action:'Add blocked domains to the workflow network allow-list'},
 {priority:'high',action:'Compare this run to similar successful runs and trim unnecessary turns, tools, or write actions.'},
 {priority:'medium',action:'Move data-fetching work to frontmatter steps: (pre-agent) writing to /tmp/gh-aw/agent/ or post-steps: (post-agent) to reduce inference cost. See the DeterministicOps guide.'}
];
run.audit.observability_insights=[
 {category:'execution',severity:'medium',title:'Exploratory execution path'},
 {category:'actuation',severity:'info',title:'Write path executed'},
 {category:'tooling',severity:'high',title:'Capability friction detected'},
 {category:'network',severity:'medium',title:'Network friction detected'},
 {category:'execution',severity:'info',title:'Log template patterns mined'},
 {category:'reliability',severity:'high',title:'3 anomalous event pattern(s) detected'},
 {category:'execution',severity:'info',title:'Agent stage sequence'}
];
const batch=normalize(adaptCachedGhAwJsonl(JSON.stringify({schema_version:5,kind:'run',run})).observations);
const rows=[...batch.audits].sort((a,b)=>a.id.localeCompare(b.id));
for(const row of rows) delete row.sequence;
console.log(JSON.stringify(rows));`, input)
	projection, err := ProjectCanonical(t.Context(), evidence, time.Now(), Limits{})
	if err != nil {
		t.Fatal(err)
	}
	got := []any{}
	for _, collection := range []string{"audits"} {
		for _, row := range projection.Collections[collection] {
			if row["source"] != "cao-native" {
				got = append(got, comparable(t, row))
			}
		}
	}
	slices.SortFunc(got, func(a, b any) int { return strings.Compare(text(object(a)["id"]), text(object(b)["id"])) })
	var expected []any
	if err := json.Unmarshal(want, &expected); err != nil {
		t.Fatal(err)
	}
	// JS locale collation is not source identity ordering.
	slices.SortFunc(expected, func(a, b any) int { return strings.Compare(text(object(a)["id"]), text(object(b)["id"])) })
	if !reflect.DeepEqual(got, expected) {
		t.Fatalf("default audit canonical discrepancy:\n%s\n%s", mustJSON(t, got), mustJSON(t, expected))
	}
	if projection.Collections["runs"][0]["highPriorityAuditItems"] != nil || projection.Collections["runs"][0]["mediumPriorityAuditItems"] != nil {
		t.Fatal("partial findings established complete immutable audit totals")
	}
}

func TestGitHubNativeEngineAndDetectionArchiveAcquisition(t *testing.T) {
	for _, attempt := range []string{"1", "2"} {
		t.Run(attempt, func(t *testing.T) {
			info := []byte(fmt.Sprintf(`{"repository":"octo/api","run_id":303,"run_attempt":"%s","engine_id":"copilot"}`, attempt))
			archive := zipArtifact(t, map[string][]byte{
				"runtime/aw_info.json": info, "sandbox/agent/logs/copilot-session-state/id/events.jsonl": jobFixture(t, "gh-aw-v1.0.1-copilot-events.jsonl"),
				"agent-stdio.log":                             []byte(`{"type":"system","subtype":"init","mcp_servers":[{"name":"github","status":"failed"}]}`),
				"detection/detection_result.json":             []byte(`{"prompt_injection":true,"secret_leak":false,"malicious_patch":false,"reasons":["PRIVATE_DETECTION_REASON"]}`),
				"sandbox/firewall/logs/squid-logs/access.log": []byte("1761332531.123 172.30.0.20:1 blocked.example:443 140.82.112.23:443 1.1 CONNECT 403 TCP_TUNNEL PRIVATE_URL \"PRIVATE_USER_AGENT\""),
				"workflow-logs/duplicate.log":                 []byte("PRIVATE_RUNNER_LOG"),
				"agent-session.jsonl":                         []byte(`{"type":"session.start","data":{"sessionId":"native-session"}}`),
				"gateway.jsonl":                               []byte(`{"event":"tool_call","server_name":"github","tool_name":"read","input_size":10,"output_size":20,"status":"error","error":"PRIVATE_GATEWAY_ERROR"}`),
				"mcp-logs/rpc-messages.jsonl":                 []byte(`{"event":"rpc_request","direction":"OUT","server_id":"ignored","payload":{"method":"tools/call","params":{"name":"never_add"}}}`),
			})
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") == "" {
					t.Error("acquisition lacked installation authorization")
				}
				switch r.URL.Path {
				case "/repos/octo/api/actions/runs/303/artifacts":
					_, _ = w.Write([]byte(`{"artifacts":[{"id":100,"name":"agent","workflow_run":{"id":303}}]}`))
				case "/repos/octo/api/actions/artifacts/100/zip":
					_, _ = w.Write(archive)
				case "/repos/octo/api/actions/runs/303/attempts/1/jobs":
					_, _ = w.Write([]byte(`{"total_count":0,"jobs":[]}`))
				default:
					t.Errorf("unexpected native endpoint: %s", r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer api.Close()
			run := nativeRun()
			run.Fields["conclusion"] = "success"
			collector := Collector{Provider: GitHub{BaseURL: api.URL, Tokens: testTokens{}, Quota: testQuota(t), App: "native-test"}}
			evidence, err := collector.extract(t.Context(), run)
			if err != nil {
				t.Fatal(err)
			}
			codes := modelAuditCodes(array(object(evidence.Run["audit"])["key_findings"]))
			if attempt == "1" {
				if !slices.Equal(codes, []string{"threat_detected", "high_token_usage", "mcp_server_failures", "blocked_network_requests", "workflow_succeeded"}) ||
					engineCounter(evidence.Run["token_usage"]) != 805343 ||
					len(array(object(evidence.Run["mcp_tool_usage"])["tool_calls"])) != 1 ||
					engineCounter(object(evidence.Run["audit"])["native_tool_types"]) != 3 {
					t.Fatalf("actual acquired engine/detection sources were lost: %v %s", codes, mustJSON(t, evidence.Run))
				}
			} else if !slices.Equal(codes, []string{"workflow_succeeded"}) || evidence.Run["token_usage"] != nil {
				t.Fatal("other-attempt archive established native audit results")
			}
			retained, _, err := evidence.Bytes()
			if err != nil || bytes.Contains(retained, []byte("PRIVATE")) || bytes.Contains(retained, []byte("copilotVersion")) {
				t.Fatal("raw source text crossed safe evidence boundary")
			}
		})
	}
}
