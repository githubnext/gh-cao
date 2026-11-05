package collectnative

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"
)

// Expected counters follow github/gh-aw@v1.0.1 logs_usage_activity.go
// (buildUsageActivityToolCalls) and token_usage_parse.go/token_usage_aic.go:
// request IDs deduplicate, counters sum, chronological cumulative AIC wins.
func TestNativeAuditPinnedSummaryAndAccounting(t *testing.T) {
	fields := map[string]any{}
	files := map[string][]byte{"usage/activity/summary.json": jobFixture(t, "usage-activity-summary.json"),
		"usage/token-usage.jsonl": jobFixture(t, "token-usage.jsonl")}
	gaps, err := enrichAudit(t.Context(), fields, files, testIdentity(), Limits{})
	if err != nil || !slices.Equal(gaps, []string{"legacy-engine-fallback-unavailable", "agentic-tool-metrics-unavailable"}) {
		t.Fatalf("supported structured evidence failed: %v %v", gaps, err)
	}
	content, _ := json.Marshal(fields)
	var retained map[string]any
	if err := decode(content, &retained); err != nil {
		t.Fatal(err)
	}
	usage := object(retained["token_usage_summary"])
	for name, want := range map[string]string{"total_input_tokens": "30", "total_output_tokens": "7",
		"total_cache_read_tokens": "11", "total_cache_write_tokens": "3", "total_aic": "0.9"} {
		if scalar(usage[name]) != want {
			t.Errorf("%s=%v want %s", name, usage[name], want)
		}
	}
	if scalar(object(object(usage["by_model"])["test-model"])["reasoning_tokens"]) != "3" {
		t.Fatal("reasoning tokens were dropped")
	}
	calls := array(object(retained["mcp_tool_usage"])["tool_calls"])
	if len(calls) != 2 || scalar(object(calls[0])["input_size"]) != "5" ||
		scalar(object(calls[0])["output_size"]) != "17" || object(calls[0])["status"] != "success" ||
		object(calls[1])["status"] != "error" {
		t.Fatal("upstream request/response sizes or observed outcomes changed")
	}
	if bytes.Contains(content, []byte("PRIVATE")) {
		t.Fatal("non-allowlisted summary fields persisted")
	}
}

func TestNativeAuditUnavailableAmbiguousAndMalformedSources(t *testing.T) {
	for _, test := range []struct {
		name  string
		files map[string][]byte
		gap   string
	}{
		{"missing", nil, "audit-summary-unavailable"},
		{"schema", map[string][]byte{"usage/activity/summary.json": []byte(`{"schema":"future/v2"}`)}, "invalid-audit-summary"},
		{"malformed", map[string][]byte{"usage/activity/summary.json": []byte(`{`)}, "invalid-audit-summary"},
		{"section", map[string][]byte{"usage/activity/summary.json": []byte(`{"schema":"usage-activity-summary/v1","gateway":{"tool_calls":"bad"}}`)}, "invalid-audit-summary"},
		{"counter", map[string][]byte{"usage/activity/summary.json": []byte(`{"schema":"usage-activity-summary/v1","firewall":{"requests_by_domain":{"host":{"allowed":-1,"blocked":0}}}}`)}, "invalid-audit-summary"},
		{"ambiguous-summary", map[string][]byte{"a/activity/summary.json": jobFixture(t, "usage-activity-summary.json"), "b/activity/summary.json": jobFixture(t, "usage-activity-summary.json")}, "ambiguous-audit-summary"},
		{"ambiguous-token", map[string][]byte{"a/token-usage.jsonl": []byte("{}"), "b/token-usage.jsonl": []byte("{}")}, "ambiguous-token-usage"},
		{"wrong-attempt-token", map[string][]byte{"a/token-usage.jsonl": []byte(`{"run_attempt":2,"input_tokens":999}`)}, "invalid-token-usage"},
		{"unpriced", map[string][]byte{"a/token-usage.jsonl": []byte(`{"input_tokens":10,"output_tokens":5}`)}, "legacy-aic-unavailable"},
		{"wrong-attempt-output", map[string][]byte{"a/safe-output-items.jsonl": []byte(`{"type":"create_issue","run_attempt":2}`)}, "invalid-safe-outputs"},
		{"invalid-output", map[string][]byte{"a/safe-output-items.jsonl": []byte(`{"body":"PRIVATE_BODY"}`)}, "invalid-safe-outputs"},
	} {
		t.Run(test.name, func(t *testing.T) {
			fields := map[string]any{}
			gaps, err := enrichAudit(t.Context(), fields, test.files, testIdentity(), Limits{})
			if err != nil || !slices.Contains(gaps, test.gap) {
				t.Fatalf("unsupported source became complete: %v %v", gaps, err)
			}
			if usage := object(fields["token_usage_summary"]); test.gap == "legacy-aic-unavailable" && usage["total_aic"] != nil {
				t.Fatal("unpriced traffic became zero-cost success")
			}
			if test.gap == "invalid-audit-summary" && (fields["firewall_analysis"] != nil || fields["mcp_tool_usage"] != nil) {
				t.Fatal("invalid summary partially mutated the projection")
			}
		})
	}
}

func TestNativeAuditSafeOutputManifestAndBounds(t *testing.T) {
	files := map[string][]byte{"usage/safe-output-items.jsonl": []byte(
		"{\"type\":\"create_issue\",\"number\":0,\"url\":\"https://github.com/octo/api/issues/1?token=PRIVATE_TOKEN\",\"metadata\":{\"prompt\":\"PRIVATE_PROMPT\"}}\n")}
	fields := map[string]any{}
	gaps, err := enrichAudit(t.Context(), fields, files, testIdentity(), Limits{})
	if err != nil || slices.Contains(gaps, "invalid-safe-outputs") {
		t.Fatal(err)
	}
	content, _ := json.Marshal(fields)
	if bytes.Contains(content, []byte("PRIVATE")) || bytes.Contains(content, []byte(`"number":0`)) ||
		!bytes.Contains(content, []byte(`"safe_items_count":1`)) {
		t.Fatal("unsafe or fabricated output identity was persisted")
	}
	files = map[string][]byte{"usage/token-usage.jsonl": jobFixture(t, "token-usage.jsonl")}
	for _, limits := range []Limits{{Bytes: 10}, {LineBytes: 10}, {Entries: 1}} {
		if _, err := enrichAudit(t.Context(), map[string]any{}, files, testIdentity(), limits); !errors.Is(err, ErrCapacity) {
			t.Fatalf("token usage budget ignored: %+v %v", limits, err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := enrichAudit(ctx, map[string]any{}, files, testIdentity(), Limits{}); !errors.Is(err, context.Canceled) {
		t.Fatal("audit ignored cancellation")
	}
	var summary map[string]any
	if err := decode(jobFixture(t, "usage-activity-summary.json"), &summary); err != nil {
		t.Fatal(err)
	}
	if err := applyAuditSummary(t.Context(), map[string]any{}, summary, Limits{Entries: 1}); !errors.Is(err, ErrCapacity) {
		t.Fatal("summary item budget ignored")
	}
}

func TestNativeImmutableLegacyAggregateMissingNullZeroAndPrecedence(t *testing.T) {
	for _, test := range []struct {
		name  string
		input string
		want  any
	}{
		{"missing", `{}`, nil},
		{"null", `{"firewall_analysis":null,"audit":{"firewall_analysis":{"requests_by_domain":{"host":{"allowed":4}}}}}`, nil},
		{"zero", `{"firewall_analysis":{}}`, float64(0)},
		{"precedence", `{"firewall_analysis":{"requests_by_domain":{"host":{"allowed":2}}},"audit":{"firewall_analysis":{"requests_by_domain":{"host":{"allowed":4}}}}}`, float64(2)},
		{"fallback", `{"audit":{"firewall_analysis":{"requests_by_domain":{"host":{"allowed":4}}}}}`, float64(4)},
	} {
		t.Run(test.name, func(t *testing.T) {
			var fields map[string]any
			if err := decode([]byte(test.input), &fields); err != nil {
				t.Fatal(err)
			}

			run := map[string]any{}
			legacyAggregates(run, fields)
			if run["firewallAllowedCalls"] != test.want {
				t.Fatalf("missing/null/zero precedence lost: got %v want %v", run["firewallAllowedCalls"], test.want)
			}
		})
	}
}

func TestNativeAuditMetadataWithoutEpisodeOrSessionAndWorkingSetWithoutOutputs(t *testing.T) {
	files := map[string][]byte{"aw_info.json": []byte(
		`{"repository":"octo/api","run_id":303,"run_attempt":1,"engine_id":"copilot","engine_name":"Copilot","agent_version":"agent-1","version":"fallback-2","firewall_version":"firewall-1","awf_version":"fallback-3","awmg_version":"gateway-1","agent_runtime":"container","prompt":"PRIVATE_PROMPT"}`)}
	c := Collector{Provider: staticProvider{artifacts: []Artifact{{ID: 1, Name: "usage", Files: files}}}, Store: &testEvidenceStore{}}
	evidence, err := c.extract(t.Context(), nativeRun())
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"engine_id": "copilot", "engine": "Copilot", "agent_version": "agent-1",
		"engine_version": "agent-1", "firewall_version": "firewall-1", "gateway_version": "gateway-1", "agent_runtime": "container"} {
		if evidence.Run[name] != want {
			t.Errorf("%s=%v want %s", name, evidence.Run[name], want)
		}
	}
	content, _, _ := evidence.Bytes()
	if bytes.Contains(content, []byte("PRIVATE")) {
		t.Fatal("aw_info raw metadata persisted")
	}
	var summary map[string]any
	if err := decode([]byte(`{"working_set":{"measurement_state":"measured","cumulative_input_tokens":9,"peak_input_tokens":3,"rebuild_excess_tokens":6,"invocations":3,"rebuild_factor":3}}`), &summary); err != nil {
		t.Fatal(err)
	}
	fields := map[string]any{}
	if err := applyAuditSummary(t.Context(), fields, summary, Limits{}); err != nil || object(fields["working_set"])["measurement_state"] != "measured" {
		t.Fatal("independent working set evidence was coupled to safe outputs")
	}
}
