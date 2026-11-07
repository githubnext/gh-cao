package collectnative

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

const detectionHeader = "{\"type\":\"session.format\",\"data\":{\"version\":1},\"provenance\":{\"component\":\"collector\"}}\n"

func detectionEvent(data string) []byte {
	return []byte(detectionHeader + `{"type":"detection.result","data":` + data +
		",\"provenance\":{\"component\":\"detection\",\"phase\":\"detection\"}}\n")
}

func TestNativeDetectionPinnedVerdictsAndExecution(t *testing.T) {
	// Input/expected code pairs follow github/gh-aw@v1.0.1
	// audit_threat_detection_test.go and audit_threat_detection_session_test.go.
	for _, test := range []struct {
		name        string
		files       map[string][]byte
		jobs        []any
		want        []string
		description string
	}{
		{"unified-threat", map[string][]byte{"usage/aw_session.jsonl": detectionEvent(`{"jobResult":"failure","conclusion":"failure","reason":"threat_detected","promptInjection":true,"secretLeak":false,"maliciousPatch":true,"reasons":["PRIVATE_REASON"]}`)}, nil,
			[]string{"threat_detection_job_failed", "threat_detected"}, "prompt injection, malicious patch"},
		{"legacy-verdict", map[string][]byte{"detection/detection_result.json": []byte(`{"prompt_injection":true,"secret_leak":false,"malicious_patch":true,"reasons":["PRIVATE_REASON"]}`)},
			[]any{map[string]any{"name": "detection", "conclusion": "failure"}},
			[]string{"threat_detection_job_failed", "threat_detected"}, "prompt injection, malicious patch"},
		{"legacy-log", map[string][]byte{"detection/detection.log": []byte("output\nTHREAT_DETECTION_RESULT:{\"prompt_injection\":false,\"secret_leak\":true,\"malicious_patch\":false,\"reasons\":[]}\n")}, nil,
			[]string{"threat_detected"}, "secret leak"},
		{"missing-required-boolean", map[string][]byte{"detection/detection_result.json": []byte(`{"prompt_injection":true,"secret_leak":false}`)}, nil, nil, ""},
		{"job-failure-without-verdict", nil, []any{map[string]any{"name": "detection", "conclusion": "failure"}},
			[]string{"threat_detection_job_failed"}, "before producing a threat verdict"},
		{"job-name-normalized", nil, []any{map[string]any{"name": " Detection ", "conclusion": "failure"}},
			[]string{"threat_detection_job_failed"}, "before producing a threat verdict"},
		{"job-name-is-not-last-slash-component", nil, []any{map[string]any{"name": "reusable / detection", "conclusion": "failure"}},
			nil, ""},
		{"usage-not-started", map[string][]byte{"usage/detection/execution.json": []byte(`{"version":1,"component":"detection","run_id":303,"run_attempt":1,"state":"not_started"}`)}, nil,
			[]string{"threat_detection_job_failed"}, "did not start"},
		{"not-started-other-attempt", map[string][]byte{"usage/detection/execution.json": []byte(`{"version":1,"component":"detection","run_id":303,"run_attempt":2,"state":"not_started"}`)}, nil, nil, ""},
		{"skipped-not-failed", map[string][]byte{"usage/detection/execution.json": []byte(`{"version":1,"component":"detection","run_id":303,"run_attempt":1,"state":"not_started"}`)},
			[]any{map[string]any{"name": "detection", "conclusion": "skipped"}}, nil, ""},
		{"usage-warning", map[string][]byte{"usage/detection/detection_result.json": []byte(`{"job_result":"success","conclusion":"warning","reason":"parse_error"}`)}, nil,
			[]string{"threat_detection_job_failed"}, "warning (parse_error)"},
		{"unified-verdict-merges-legacy-result", map[string][]byte{
			"usage/aw_session.jsonl":                detectionEvent(`{"promptInjection":false,"secretLeak":true,"maliciousPatch":false}`),
			"usage/detection/detection_result.json": []byte(`{"job_result":"failure","conclusion":"failure","reason":"threat_detected"}`),
		}, nil, []string{"threat_detection_job_failed", "threat_detected"}, "secret leak"},
		{"unified-overrides-legacy", map[string][]byte{
			"usage/aw_session.jsonl":          detectionEvent(`{"jobResult":"success","promptInjection":false,"secretLeak":false,"maliciousPatch":false}`),
			"detection/detection_result.json": []byte(`{"prompt_injection":true,"secret_leak":true,"malicious_patch":true}`),
		}, nil, nil, ""},
		{"reason-without-category", map[string][]byte{"usage/detection/detection_result.json": []byte(`{"job_result":"failure","conclusion":"failure","reason":"threat_detected"}`)}, nil,
			[]string{"threat_detection_job_failed", "threat_detected"}, "no specific threat category"},
		{"arbitrary-reason-not-persisted", map[string][]byte{"usage/detection/detection_result.json": []byte(`{"job_result":"failure","conclusion":"failure","reason":"PRIVATE_REASON"}`)}, nil,
			[]string{"threat_detection_job_failed"}, ""},
		{"no-evidence-is-not-safe", nil, nil, nil, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			findings, _, err := detectionAuditFindings(t.Context(), map[string]any{"job_details": test.jobs}, test.files, testIdentity(), Limits{})
			if err != nil || !slices.Equal(modelAuditCodes(findings), test.want) {
				t.Fatalf("pinned detection discrepancy: %s %v", mustJSON(t, findings), err)
			}
			retained := string(mustJSON(t, findings))
			if strings.Contains(retained, "PRIVATE") || (test.description != "" && !strings.Contains(retained, test.description)) {
				t.Fatalf("detection classification or privacy changed: %s", retained)
			}
		})
	}
}

func TestNativeDetectionInvalidAndAmbiguousEvidence(t *testing.T) {
	valid := detectionEvent(`{"promptInjection":false,"secretLeak":true,"maliciousPatch":false}`)
	for _, test := range []struct {
		name, gap string
		content   []byte
	}{
		{"duplicate-header", "invalid-detection-session", append([]byte(detectionHeader), valid...)},
		{"malformed-tail", "invalid-detection-session", append(slices.Clone(valid), []byte("{bad\n")...)},
		{"wrong-attempt-tail", "invalid-detection-session", append(slices.Clone(valid), []byte("{\"run_attempt\":2}\n")...)},
		{"string-verdict", "invalid-detection-session", detectionEvent(`{"promptInjection":false,"secretLeak":"true","maliciousPatch":false}`)},
		{"wrong-component", "", []byte(strings.ReplaceAll(string(valid), `"component":"detection"`, `"component":"agent"`))},
		{"wrong-phase", "", []byte(strings.ReplaceAll(string(valid), `"phase":"detection"`, `"phase":"agent"`))},
		{"conflicting-unified", "invalid-detection-session", append(slices.Clone(valid), detectionEvent(`{"promptInjection":true,"secretLeak":false,"maliciousPatch":false}`)[len(detectionHeader):]...)},
	} {
		t.Run(test.name, func(t *testing.T) {
			findings, gaps, err := detectionAuditFindings(t.Context(), nil, map[string][]byte{"usage/aw_session.jsonl": test.content}, testIdentity(), Limits{})
			if err != nil || len(findings) != 0 || (test.gap != "" && !slices.Contains(gaps, test.gap)) {
				t.Fatalf("invalid source published partial verdict: %s %v %v", mustJSON(t, findings), gaps, err)
			}
		})
	}
	files := map[string][]byte{
		"detection/a/detection_result.json": []byte(`{"prompt_injection":true,"secret_leak":false,"malicious_patch":false}`),
		"detection/b/detection_result.json": []byte(`{"prompt_injection":false,"secret_leak":true,"malicious_patch":false}`),
	}
	findings, gaps, err := detectionAuditFindings(t.Context(), nil, files, testIdentity(), Limits{})
	if err != nil || len(findings) != 0 || !slices.Contains(gaps, "conflicting-detection-verdict") {
		t.Fatal("conflicting legacy verdicts were silently selected")
	}
	for _, limit := range []Limits{{Bytes: 4}, {Entries: 1}, {LineBytes: 4}} {
		if _, _, err := detectionAuditFindings(t.Context(), nil, map[string][]byte{"usage/aw_session.jsonl": valid}, testIdentity(), limit); !errors.Is(err, ErrCapacity) {
			t.Fatalf("detection budget ignored: %+v %v", limit, err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := detectionAuditFindings(ctx, nil, map[string][]byte{"usage/aw_session.jsonl": valid}, testIdentity(), Limits{}); !errors.Is(err, context.Canceled) {
		t.Fatal("detection parsing ignored cancellation")
	}
}

func TestNativeFailedJobCountIsNotAgentDiagnosticsOrAnnotationVolume(t *testing.T) {
	fields, used := map[string]any{}, 0
	job := Job{Identity: testIdentity(), ID: 91, Fields: jobFields(),
		Log: []byte(strings.Repeat("2026-10-15T10:00:00Z ##[error] PRIVATE_RUNNER_ERROR\n", 9))}
	provider := jobProvider{jobs: []Job{job}}
	gaps, err := acquireJobs(t.Context(), provider, nativeRun(), fields, Limits{}, &used)
	if err != nil || len(gaps) != 0 || engineCounter(fields["error_count"]) != 1 {
		t.Fatalf("job failure count differs from pinned parseJobsAPIResponses: %s %v", mustJSON(t, fields), err)
	}
	// The default multiple_errors rule consumes ValidationIssues, not this
	// immutable failed-job count or the count of runner annotations.
	findings, _ := defaultAuditFindings(fields)
	if len(findings) != 0 {
		t.Fatal("runner annotations fabricated agent validation findings")
	}
	job.Fields["conclusion"] = "success"
	provider.jobs = []Job{job}
	if _, err := acquireJobs(t.Context(), provider, nativeRun(), fields, Limits{}, &used); err != nil || fields["error_count"] != nil {
		t.Fatal("job metadata was replaced by annotation-derived errors")
	}
}
