package collectnative

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"time"
)

func TestNativeStructuredAgentReportsAndGitHubUsage(t *testing.T) {
	files := map[string][]byte{
		"usage/agent_output.json": []byte(`{"items":[
			{"type":"report_missing_tool","tool":"github.search","timestamp":"2026-10-15T10:00:00Z","reason":"PRIVATE_REASON"},
			{"type":"missing_tool","tool":"filesystem.read"},
			{"type":"report_missing_data","data_type":"repository.metrics","context":"PRIVATE_CONTEXT"},
			{"type":"noop","message":"PRIVATE_MESSAGE"},
			{"type":"create_issue","body":"PRIVATE_BODY"}]}`),
		"usage/github_rate_limits.jsonl": []byte(
			"{\"source\":\"rate_limit_api\",\"used\":900}\n" +
				"{\"source\":\"response_headers\",\"operation\":\"PRIVATE_OPERATION\",\"resource\":\"core\",\"used\":901}\n" +
				"{\"source\":\"response_headers\",\"resource\":\"graphql\",\"used\":1}\n"),
	}
	fields := map[string]any{"conclusion": "failure"}
	gaps, err := enrichAudit(t.Context(), fields, files, testIdentity(), Limits{})
	if err != nil || !slices.Contains(gaps, "agent-report-text-unavailable") {
		t.Fatalf("safe report text loss was not explicit: %v %v", gaps, err)
	}
	audit := object(fields["audit"])
	if len(array(audit["missing_tools"])) != 2 || len(array(audit["missing_data"])) != 1 ||
		len(array(audit["noops"])) != 1 || !slices.Equal(modelAuditCodes(array(audit["key_findings"])), []string{"workflow_failed", "tools_not_available"}) ||
		fields["github_api_calls"] != 2 {
		t.Fatal("structured reports, basic findings or response-only API accounting were lost")
	}
	retained, _ := json.Marshal(fields)
	if bytes.Contains(retained, []byte("PRIVATE")) {
		t.Fatal("report free text or raw rate-limit operation persisted")
	}
}

func TestNativeStructuredAuditSourcesRejectPartialAndWrongAttempt(t *testing.T) {
	for _, test := range []struct {
		name, file, content, field, gap string
	}{
		{"missing-items", "agent_output.json", `{}`, "audit", "invalid-agent-reports"},
		{"wrong-attempt-report", "agent_output.json", `{"items":[{"type":"missing_tool","tool":"github"},{"type":"noop","run_attempt":2}]}`, "audit", "invalid-agent-reports"},
		{"unsafe-identifier", "agent_output.json", `{"items":[{"type":"missing_tool","tool":"PRIVATE PROMPT"}]}`, "audit", "invalid-agent-reports"},
		{"wrong-attempt-usage", "github_rate_limits.jsonl", "{\"source\":\"response_headers\"}\n{\"source\":\"response_headers\",\"run_attempt\":2}", "github_api_calls", "invalid-github-api-usage"},
		{"malformed-usage", "github_rate_limits.jsonl", "{\"source\":\"response_headers\"}\n{", "github_api_calls", "invalid-github-api-usage"},
		{"unknown-usage", "github_rate_limits.jsonl", `{"source":"future"}`, "github_api_calls", "invalid-github-api-usage"},
		{"empty-usage", "github_rate_limits.jsonl", "", "github_api_calls", "invalid-github-api-usage"},
	} {
		t.Run(test.name, func(t *testing.T) {
			fields := map[string]any{}
			gaps, err := enrichAudit(t.Context(), fields, map[string][]byte{"usage/" + test.file: []byte(test.content)}, testIdentity(), Limits{})
			if err != nil || !slices.Contains(gaps, test.gap) || fields[test.field] != nil {
				t.Fatalf("invalid source partially published: %v %+v %v", gaps, fields, err)
			}
		})
	}
	files := map[string][]byte{"usage/agent_output.json": []byte(`{"items":[{"type":"noop"},{"type":"noop"}]}`)}
	for _, limits := range []Limits{{Bytes: 4}, {Entries: 1}, {LineBytes: 4}} {
		if _, err := enrichAudit(t.Context(), map[string]any{}, files, testIdentity(), limits); !errors.Is(err, ErrCapacity) {
			t.Fatalf("structured reports exceeded bound %+v: %v", limits, err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := agentAuditReports(ctx, nil, testIdentity(), Limits{}); !errors.Is(err, context.Canceled) {
		t.Fatal("report parser ignored cancellation")
	}
}

func TestNativeAuditPinnedModelResolutionNeedsStopEvidence(t *testing.T) {
	// Verbatim session from github/gh-aw@v1.0.1:
	// pkg/cli/testdata/model_resolution_startup/39499464895/aw_session.jsonl.
	// TestAuditModelAvailabilityRoutedCopilotSDKWithoutMainRecord expects one
	// high/configuration model_unavailable finding with the original stdio and
	// reflection. Without those sources, only the low advisory is justified.
	raw := jobFixture(t, "gh-aw-v1.0.1-model-resolution.jsonl")
	raw = bytes.ReplaceAll(raw, []byte(`"provider":"copilot"`),
		[]byte(`"provider":"copilot","message":"PRIVATE_MODEL_ERROR","stack":"PRIVATE_STACK"`))
	files := fixtureFiles(t)
	files["aw_session.jsonl"] = raw
	collector := Collector{Provider: staticProvider{artifacts: []Artifact{{ID: 1, Name: "usage", Files: files}}},
		Store: &testEvidenceStore{}}
	evidence, err := collector.extract(t.Context(), nativeRun())
	if err != nil || slices.Contains(evidence.Gaps, "model-resolution-audit-unavailable") ||
		slices.Contains(evidence.Gaps, "legacy-audit-unavailable") ||
		!slices.Contains(evidence.Gaps, "job-logs-unavailable") {
		t.Fatalf("supported partial model evidence was not handled: %+v %v", evidence.Gaps, err)
	}
	serialized, _, _ := evidence.Bytes()
	if bytes.Contains(serialized, []byte("PRIVATE")) {
		t.Fatal("raw unsupported audit payload escaped the safe projection")
	}
	if !bytes.Contains(serialized, []byte("gpt-5.4-mini")) || !bytes.Contains(serialized, []byte("model_not_supported_error")) {
		t.Fatal("safe model resolution identifiers and execution categories were discarded")
	}
	projection, err := ProjectCanonical(t.Context(), evidence, time.Now(), Limits{})
	if err != nil {
		t.Fatal(err)
	}
	run := projection.Collections["runs"][0]
	if run["highPriorityAuditItems"] != nil || run["mediumPriorityAuditItems"] != nil {
		t.Fatal("partial native audit findings became complete immutable zero totals")
	}
	for _, audit := range projection.Collections["audits"] {
		if audit["type"] == "audit.finding" && audit["code"] == "model_unavailable" {
			t.Fatal("model resolution alone was promoted to the unsupported upstream causal finding")
		}
		found := false
		for _, audit := range projection.Collections["audits"] {
			found = found || audit["code"] == "model_resolution_failed"
		}
		if !found {
			t.Fatal("model resolution without stop evidence lost its supported advisory")
		}
	}
}

func TestNativeAuditSessionGapsValidateEntireExactAttempt(t *testing.T) {
	for _, content := range []string{
		"null",
		`{"type":"model.resolution","run_attempt":2,"data":{"status":"failed"}}`,
		`{"type":"model.resolution","data":{"run_id":404,"status":"failed"}}`,
		"{\"type\":\"model.resolution\",\"data\":{\"status\":\"failed\"}}\n{",
	} {
		gaps, err := enrichAuditReports(t.Context(), map[string]any{},
			map[string][]byte{"usage/aw_session.jsonl": []byte(content)}, testIdentity(), Limits{})
		if err != nil || !slices.Equal(gaps, []string{"invalid-audit-session"}) {
			t.Fatalf("invalid source partially published its causal gap: %v %v", gaps, err)
		}
	}
	content := []byte("{\"type\":\"model.resolution\",\"data\":{\"status\":\"failed\"}}\n" +
		"{\"type\":\"detection.result\",\"data\":{\"conclusion\":\"success\"}}\n")
	gaps, err := enrichAuditReports(t.Context(), map[string]any{},
		map[string][]byte{"usage/aw_session.jsonl": content}, testIdentity(), Limits{})
	if err != nil || len(gaps) != 0 {
		t.Fatalf("supported detection was marked unavailable: %v %v", gaps, err)
	}
	for _, limits := range []Limits{{Bytes: 4}, {Entries: 1}, {LineBytes: 4}} {
		if _, err := enrichAuditReports(t.Context(), map[string]any{},
			map[string][]byte{"usage/aw_session.jsonl": content}, testIdentity(), limits); !errors.Is(err, ErrCapacity) {
			t.Fatalf("audit session budget ignored: %+v %v", limits, err)
		}
	}
}

func TestNativeAuditPinnedCachedDurationFormatting(t *testing.T) {
	// github/gh-aw@v1.0.1 pkg/timeutil/format_test.go and
	// logs_cached_json.go project formatted RunData, not duration nanoseconds.
	for _, test := range []struct {
		duration time.Duration
		want     string
	}{
		{1, "1ns"}, {1500, "1µs"}, {time.Millisecond, "1ms"},
		{time.Second, "1.0s"}, {59999 * time.Millisecond, "1.0m"},
		{61 * time.Second, "1.0m"}, {3599999 * time.Millisecond, "1.0h"}, {90 * time.Minute, "1.5h"},
	} {
		if got := auditDuration(test.duration); got != test.want {
			t.Errorf("%s formatted as %q, want %q", test.duration, got, test.want)
		}
	}
	fields := map[string]any{"started_at": "2026-10-15T10:00:00Z", "updated_at": "2026-10-15T10:01:01Z"}
	if _, err := enrichAudit(t.Context(), fields, nil, testIdentity(), Limits{}); err != nil ||
		fields["duration"] != "1.0m" || fields["action_minutes"] != float64(2) {
		t.Fatalf("cached run duration/billing contract differs: %+v %v", fields, err)
	}
}
