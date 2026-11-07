package collectnative

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"
)

func TestNativeDefaultAuditContractAndPartialCoverage(t *testing.T) {
	collector := Collector{Provider: staticProvider{}, Store: &testEvidenceStore{}}
	evidence, err := collector.extract(t.Context(), nativeRun())
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(evidence.Gaps, "legacy-audit-unavailable") ||
		!slices.Contains(evidence.Gaps, "audit-summary-unavailable") ||
		!slices.Contains(evidence.Gaps, "session-unavailable") {
		t.Fatalf("default execution lost source-specific coverage: %v", evidence.Gaps)
	}
	evidence.Run["audit"] = map[string]any{
		"key_findings":    []any{map[string]any{"code": "model_unavailable", "severity": "high"}},
		"recommendations": []any{map[string]any{"priority": "medium", "action": "Check model"}},
	}
	for _, gaps := range [][]string{nil, {"invalid-engine-log"}, {"legacy-audit-unavailable"}} {
		evidence.Gaps = gaps
		projection, err := ProjectCanonical(t.Context(), evidence, time.Now(), Limits{})
		if err != nil {
			t.Fatal(err)
		}
		run := projection.Collections["runs"][0]
		if len(gaps) == 0 {
			if run["highPriorityAuditItems"] != 1 || run["mediumPriorityAuditItems"] != 1 {
				t.Fatalf("complete default audit totals missing: %v", run)
			}
		} else if run["highPriorityAuditItems"] != nil || run["mediumPriorityAuditItems"] != nil {
			t.Fatalf("partial or historical audit implied complete totals: %v", gaps)
		}
	}
}

func TestNativeUnsupportedAuditRequestsFailBeforeAcquisition(t *testing.T) {
	for _, request := range []AuditRequest{
		{ExpandedReport: true}, {Training: true}, {Baseline: "previous-run"},
		{Weights: map[string]float64{}}, {Weights: map[string]float64{"tool": 1}},
	} {
		store := &testEvidenceStore{}
		collector := Collector{Provider: staticProvider{}, Store: store, Audit: request,
			Destinations: []Destination{DestinationFunc(func(context.Context, Run, []byte, string) error {
				t.Fatal("unsupported audit request acquired archive content")
				return nil
			})}}
		err := collector.Collect(t.Context(), nativeRun().Repository, nativeRun().InstallationID, time.Time{})
		if !errors.Is(err, ErrUnsupported) || len(store.published) != 0 {
			t.Fatalf("unsupported request published evidence: %+v %v", request, err)
		}
	}
}

func TestNativeEncounteredUnsupportedAuditSemanticsDoNotPublish(t *testing.T) {
	for _, item := range []struct {
		name, info, report string
	}{
		{"custom-engine-without-logs", `,"engine_id":"custom"`, ""},
		{"unknown-engine-without-logs", `,"engine_id":"future"`, ""},
		{"behavior-engine", `,"engine_id":{"behavior":"custom"}`, ""},
		{"expanded-report", "", `{"items":[{"type":"expanded_report"}]}`},
		{"future-report", "", `{"items":[{"type":"report_custom_intelligence"}]}`},
		{"baseline-report-after-valid-prefix", "", `{"items":[{"type":"missing_tool","tool":"github"},{"type":"baseline_report"}]}`},
	} {
		t.Run(item.name, func(t *testing.T) {
			files := map[string][]byte{"aw_info.json": []byte(
				`{"repository":"octo/api","run_id":303,"run_attempt":1` + item.info + `}`)}
			if item.report != "" {
				files["agent_output.json"] = []byte(item.report)
			}
			store := &testEvidenceStore{}
			collector := Collector{Provider: staticProvider{artifacts: []Artifact{{ID: 1, Name: "usage", Files: files}}}, Store: store}
			err := collector.collectRun(t.Context(), nativeRun())
			if !errors.Is(err, ErrUnsupported) || len(store.published) != 0 || store.released != 1 {
				t.Fatalf("unsupported encountered semantics published or kept claim: %v %+v", err, store)
			}
		})
	}
}

func TestNativeDefaultOptionalSummarySectionsAreCoverageNotRequests(t *testing.T) {
	fields := map[string]any{"conclusion": "success"}
	gaps, err := enrichAudit(t.Context(), fields, map[string][]byte{
		"usage/activity/summary.json": []byte(`{"schema":"usage-activity-summary/v1","friction":{},"ledger":{},"integrity":{},"steering":{}}`),
	}, testIdentity(), Limits{})
	if err != nil {
		t.Fatal(err)
	}

	for _, source := range []string{"friction", "ledger", "integrity", "steering"} {
		if !slices.Contains(gaps, "legacy-"+source+"-unavailable") {
			t.Fatalf("optional source section silently appeared evaluated: %v", gaps)
		}
	}
	if !slices.Contains(modelAuditCodes(array(object(fields["audit"])["key_findings"])), "workflow_succeeded") {
		t.Fatal("optional expanded inputs prevented supported default audit execution")
	}
}

func TestNativeEngineCapabilityPrecedesPrimaryEventsAndDoesNotWidenIdentity(t *testing.T) {
	for _, engine := range []any{"copilot", "claude", "codex", "pi", "gemini", nil, ""} {
		if err := validateAuditEngine(engine); err != nil {
			t.Fatalf("supported default engine rejected: %v %v", engine, err)
		}
	}
	files := map[string][]byte{
		"agent/events.jsonl": jobFixture(t, "gh-aw-v1.0.1-copilot-events.jsonl"),
	}
	if _, err := enrichEngineAudit(t.Context(), map[string]any{"engine_id": "custom"}, files, testIdentity(), Limits{}); !errors.Is(err, ErrUnsupported) {
		t.Fatal("primary events silently overrode an unsupported declared engine")
	}
	files = map[string][]byte{"aw_info.json": []byte(
		`{"repository":"octo/api","run_id":303,"run_attempt":2,"engine_id":"custom"}`)}
	store := &testEvidenceStore{}
	collector := Collector{Provider: staticProvider{artifacts: []Artifact{{ID: 1, Name: "usage", Files: files}}}, Store: store}
	if err := collector.collectRun(t.Context(), nativeRun()); err != nil || len(store.published) != 1 {
		t.Fatalf("another attempt's unsupported declaration affected current acquisition: %v", err)
	}
}
