package doctor

import (
	"testing"
	"time"
)

func TestTallyStatusesCountsEachOutcome(t *testing.T) {
	checks := []Check{
		{Status: StatusPass},
		{Status: StatusPass},
		{Status: StatusWarn},
		{Status: StatusFail},
		{Status: StatusSkip},
	}
	pass, warn, fail, skip, worst := tallyStatuses(checks)
	if pass != 2 || warn != 1 || fail != 1 || skip != 1 {
		t.Fatalf("tallyStatuses = pass=%d warn=%d fail=%d skip=%d, want 2,1,1,1", pass, warn, fail, skip)
	}
	if worst != StatusFail {
		t.Fatalf("worst = %s, want %s", worst, StatusFail)
	}
}

func TestTallyStatusesSkipNeverBecomesWorst(t *testing.T) {
	checks := []Check{
		{Status: StatusPass},
		{Status: StatusSkip},
	}
	_, _, _, skip, worst := tallyStatuses(checks)
	if skip != 1 {
		t.Fatalf("skip = %d, want 1", skip)
	}
	// A lone skip must not outrank the passing check even though
	// StatusSkip.severity() is greater than StatusPass.severity().
	if worst != StatusPass {
		t.Fatalf("worst = %s, want %s", worst, StatusPass)
	}
}

func TestTallyStatusesEmptyChecksIsAllPass(t *testing.T) {
	pass, warn, fail, skip, worst := tallyStatuses(nil)
	if pass != 0 || warn != 0 || fail != 0 || skip != 0 {
		t.Fatalf("tallyStatuses(nil) = pass=%d warn=%d fail=%d skip=%d, want all 0", pass, warn, fail, skip)
	}
	if worst != StatusPass {
		t.Fatalf("worst = %s, want %s", worst, StatusPass)
	}
}

func TestSummarizePopulatesCountsStatusAndDuration(t *testing.T) {
	checks := []Check{
		{Status: StatusPass},
		{Status: StatusWarn},
		{Status: StatusSkip},
	}
	summary := summarize(checks, 42*time.Millisecond)
	if summary.Total != 3 {
		t.Fatalf("Total = %d, want 3", summary.Total)
	}
	if summary.Pass != 1 || summary.Warn != 1 || summary.Skip != 1 || summary.Fail != 0 {
		t.Fatalf("counts = pass=%d warn=%d skip=%d fail=%d, want 1,1,1,0", summary.Pass, summary.Warn, summary.Skip, summary.Fail)
	}
	if summary.Status != StatusWarn {
		t.Fatalf("Status = %s, want %s", summary.Status, StatusWarn)
	}
	if summary.DurationMS != 42 {
		t.Fatalf("DurationMS = %d, want 42", summary.DurationMS)
	}
}

func TestAreaRankOrdersKnownAreasBeforeUnknown(t *testing.T) {
	if got, want := areaRank("runtime"), 0; got != want {
		t.Fatalf("areaRank(runtime) = %d, want %d", got, want)
	}
	if got, want := areaRank("collect"), 4; got != want {
		t.Fatalf("areaRank(collect) = %d, want %d", got, want)
	}
	if areaRank("unknown-area") != len(areaOrder) {
		t.Fatalf("areaRank(unknown-area) = %d, want %d", areaRank("unknown-area"), len(areaOrder))
	}
}

func TestSortChecksOrdersByAreaThenID(t *testing.T) {
	checks := []Check{
		{ID: "collect.b", Area: "collect"},
		{ID: "runtime.b", Area: "runtime"},
		{ID: "runtime.a", Area: "runtime"},
		{ID: "unknown.z", Area: "made-up"},
	}
	sortChecks(checks)
	wantOrder := []string{"runtime.a", "runtime.b", "collect.b", "unknown.z"}
	for index, want := range wantOrder {
		if checks[index].ID != want {
			t.Fatalf("checks[%d].ID = %s, want %s", index, checks[index].ID, want)
		}
	}
}

func TestReportFailedRespectsStrictFlag(t *testing.T) {
	warnOnly := Report{Summary: Summary{Warn: 1}}
	if warnOnly.Failed(false) {
		t.Fatalf("Failed(false) = true for warn-only report, want false")
	}
	if !warnOnly.Failed(true) {
		t.Fatalf("Failed(true) = false for warn-only report, want true")
	}

	failing := Report{Summary: Summary{Fail: 1}}
	if !failing.Failed(false) {
		t.Fatalf("Failed(false) = false for failing report, want true")
	}
}

func TestReportCheckFindsByID(t *testing.T) {
	report := Report{Checks: []Check{
		{ID: "operational.ping", Summary: "ok"},
		{ID: "data.generation", Summary: "current"},
	}}
	found, ok := report.Check("data.generation")
	if !ok {
		t.Fatalf("Check(data.generation) not found")
	}
	if found.Summary != "current" {
		t.Fatalf("Summary = %s, want current", found.Summary)
	}
	if _, ok := report.Check("missing.check"); ok {
		t.Fatalf("Check(missing.check) unexpectedly found")
	}
}

func TestDetailTrimsValue(t *testing.T) {
	got := detail("namespace", "  cao:test  \n")
	if got.Name != "namespace" {
		t.Fatalf("Name = %s, want namespace", got.Name)
	}
	if got.Value != "cao:test" {
		t.Fatalf("Value = %q, want %q", got.Value, "cao:test")
	}
}
