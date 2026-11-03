package auditlifecycle

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/operational"
)

var observed = time.Date(2026, 10, 22, 1, 0, 0, 0, time.UTC)

type eventReader struct {
	events []operational.AuditEvent
	reads  int
	rows   int
	failAt int
}

func (r *eventReader) ListAudit(ctx context.Context, filter operational.AuditFilter) ([]operational.AuditEvent, error) {
	r.reads++
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if r.failAt == r.reads {
		return nil, errors.New("storage failure with sensitive detail")
	}
	if filter.Event != "" || filter.Actor != "" {
		return nil, errors.New("lifecycle scanner must read unfiltered sequences")
	}
	var result []operational.AuditEvent
	for _, event := range r.events {
		if event.Sequence > filter.AfterSequence {
			result = append(result, event)
			if len(result) == filter.Limit {
				break
			}
		}
	}
	r.rows += len(result)
	return result, nil
}

func rebuildEvent(name, state string, age time.Duration) operational.AuditEvent {
	outcome := "success"
	if name == "completed" && state != "succeeded" {
		outcome = "failure"
	}
	return operational.AuditEvent{
		Event: "admin.rebuild." + name, Outcome: outcome, OccurredAt: observed.Add(-age),
		Attributes: map[string]string{"rebuild_id": strings.Repeat("a", 64), "state": state},
	}
}

func scanEvents(t *testing.T, events ...operational.AuditEvent) Result {
	t.Helper()
	for i := range events {
		events[i].Sequence = int64(i + 1)
	}
	result, err := Scan(t.Context(), &eventReader{events: events}, Options{
		MaxEvents: 10000, Grace: DefaultGrace, Now: observed,
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestRebuildOutcomesAndGrace(t *testing.T) {
	for _, state := range []string{"succeeded", "failed", "canceled"} {
		t.Run(state, func(t *testing.T) {
			result := scanEvents(t,
				rebuildEvent("admitted", "running", 2*time.Hour),
				rebuildEvent("started", "running", 2*time.Hour),
				rebuildEvent("completed", state, time.Hour))
			if result.Status != "ok" || result.Completed != 1 || len(result.Findings) != 0 {
				t.Fatalf("%+v", result)
			}
		})
	}
	for _, age := range []time.Duration{DefaultGrace - time.Nanosecond, DefaultGrace, 2 * time.Hour} {
		t.Run(age.String(), func(t *testing.T) {
			result := scanEvents(t, rebuildEvent("admitted", "running", age))
			stale := age >= DefaultGrace
			if stale && (result.Status != "attention" || result.MissingTerminal != 1) ||
				!stale && (result.Status != "ok" || result.Pending != 1) {
				t.Fatalf("%+v", result)
			}
		})
	}
	// A failed admission can finish without a start. A missing start is not
	// evidence of a missing terminal outcome.
	result := scanEvents(t, rebuildEvent("admitted", "running", 2*time.Hour),
		rebuildEvent("completed", "failed", time.Hour))
	if result.Status != "ok" || result.Completed != 1 {
		t.Fatalf("%+v", result)
	}
}

func TestRebuildUncertainty(t *testing.T) {
	admitted := func() operational.AuditEvent { return rebuildEvent("admitted", "running", 2*time.Hour) }
	started := func() operational.AuditEvent { return rebuildEvent("started", "running", 2*time.Hour) }
	complete := func() operational.AuditEvent { return rebuildEvent("completed", "succeeded", time.Hour) }
	tests := []struct {
		name    string
		events  []operational.AuditEvent
		issue   string
		missing int
	}{
		{"process_death", []operational.AuditEvent{admitted(), started()}, "", 1},
		{"duplicate_admission_no_age_reset", []operational.AuditEvent{admitted(), rebuildEvent("admitted", "running", time.Minute)}, "duplicate_admission", 1},
		{"duplicate_start", []operational.AuditEvent{admitted(), started(), started()}, "duplicate_start", 1},
		{"uncertain_duplicate_completion", []operational.AuditEvent{admitted(), complete(), complete()}, "duplicate_terminal", 0},
		{"conflicting_completion", []operational.AuditEvent{admitted(), complete(), rebuildEvent("completed", "canceled", time.Hour)}, "conflicting_terminal", 0},
		{"invalid_completion", []operational.AuditEvent{admitted(), rebuildEvent("completed", "running", time.Hour)}, "invalid_terminal", 1},
		{"status_failure_is_not_completion", []operational.AuditEvent{admitted(), rebuildEvent("status_failed", "succeeded", time.Hour)}, "status_write_failed", 1},
		{"completed_status_failure", []operational.AuditEvent{admitted(), complete(), rebuildEvent("status_failed", "succeeded", time.Hour)}, "status_write_failed", 0},
		{"orphan_start", []operational.AuditEvent{started()}, "admission_not_observed", 1},
		{"out_of_order", []operational.AuditEvent{admitted(), complete(), started()}, "unexpected_order", 0},
		{"future_clock", []operational.AuditEvent{rebuildEvent("admitted", "running", -time.Hour)}, "clock_uncertain", 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := scanEvents(t, test.events...)
			if result.Status != "attention" || result.MissingTerminal != test.missing || len(result.Findings) != 1 {
				t.Fatalf("%+v", result)
			}
			finding := result.Findings[0]
			if test.issue != "" && !slices.Contains(finding.Issues, test.issue) {
				t.Fatalf("missing %s: %+v", test.issue, finding)
			}
			if test.name == "conflicting_completion" && finding.TerminalState != "" {
				t.Fatalf("conflicting outcomes must not select a winner: %+v", finding)
			}
		})
	}
}

func TestRebuildMalformedEvidence(t *testing.T) {
	event := rebuildEvent("admitted", "running", 2*time.Hour)
	event.Attributes["rebuild_id"] = "credential-like-untrusted-value"
	result := scanEvents(t, event)
	if result.Status != "incomplete" || !slices.Contains(result.Issues, "invalid_rebuild_id") || len(result.Findings) != 0 {
		t.Fatalf("%+v", result)
	}
	event = rebuildEvent("admitted", "running", 2*time.Hour)
	event.OccurredAt = time.Time{}
	result = scanEvents(t, event)
	if result.Status != "attention" || result.MissingTerminal != 0 || result.Findings[0].Classification != "unknown" {
		t.Fatalf("%+v", result)
	}
	event = rebuildEvent("completed", "succeeded", time.Hour)
	event.Outcome = "failure"
	result = scanEvents(t, rebuildEvent("admitted", "running", 2*time.Hour), event)
	if result.Completed != 0 || result.MissingTerminal != 1 {
		t.Fatalf("%+v", result)
	}
}

func TestRebuildBoundedPagination(t *testing.T) {
	for _, total := range []int{999, 1000, 1001, 2001} {
		t.Run(fmt.Sprint(total), func(t *testing.T) {
			events := make([]operational.AuditEvent, total)
			for i := range events {
				events[i] = operational.AuditEvent{Sequence: int64(i + 1), Event: "unrelated"}
			}
			events[0] = rebuildEvent("admitted", "running", 2*time.Hour)
			events[0].Sequence = 1
			events[total-1] = rebuildEvent("completed", "succeeded", time.Hour)
			events[total-1].Sequence = int64(total)
			reader := &eventReader{events: events}
			options := Options{MaxEvents: total, Grace: DefaultGrace, Now: observed}
			result, err := Scan(t.Context(), reader, options)
			if err != nil || result.Status != "ok" || result.Completed != 1 || reader.rows != total ||
				reader.reads > total/operational.AuditListLimit+2 {
				t.Fatalf("result=%+v reads=%d rows=%d err=%v", result, reader.reads, reader.rows, err)
			}
			reader = &eventReader{events: events}
			options.MaxEvents = total - 1
			result, err = Scan(t.Context(), reader, options)
			if err != nil || result.Status != "incomplete" || result.TailReached || result.MissingTerminal != 0 ||
				result.Findings[0].Classification != "unknown" || reader.rows != options.MaxEvents+1 {
				t.Fatalf("truncated result=%+v rows=%d err=%v", result, reader.rows, err)
			}
		})
	}
}

func TestRebuildRetainedSuffixAndGaps(t *testing.T) {
	for _, test := range []struct {
		name      string
		after     int64
		sequences []int64
		issue     string
		missing   int
	}{
		{"retention_prefix", 0, []int64{20, 21}, "retained_prefix", 1},
		{"selected_suffix", 19, []int64{20, 21}, "selected_suffix", 1},
		{"interior_gap", 0, []int64{1, 3}, "history_gap", 0},
		{"gap_after_cursor", 10, []int64{12, 13}, "history_gap", 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			events := []operational.AuditEvent{rebuildEvent("admitted", "running", 2*time.Hour), rebuildEvent("started", "running", 2*time.Hour)}
			for i := range events {
				events[i].Sequence = test.sequences[i]
			}
			result, err := Scan(t.Context(), &eventReader{events: events}, Options{
				AfterSequence: test.after, MaxEvents: 1000, Grace: DefaultGrace, Now: observed,
			})
			if err != nil || result.HistoryComplete || !result.TailReached || result.Status != "incomplete" ||
				!slices.Contains(result.Issues, test.issue) || result.MissingTerminal != test.missing {
				t.Fatalf("%+v err=%v", result, err)
			}
		})
	}
}

func TestRebuildReadFailureAndCancellation(t *testing.T) {
	reader := &eventReader{failAt: 1}
	options := Options{MaxEvents: 10000, Grace: DefaultGrace, Now: observed}
	result, err := Scan(t.Context(), reader, options)
	if err == nil || strings.Contains(err.Error(), "sensitive") || result.Status != "incomplete" ||
		!slices.Contains(result.Issues, "read_failed") {
		t.Fatalf("%+v err=%v", result, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	reader = &eventReader{}
	result, err = Scan(ctx, reader, options)
	if !errors.Is(err, context.Canceled) || reader.reads != 0 || result.Status != "incomplete" {
		t.Fatalf("%+v reads=%d err=%v", result, reader.reads, err)
	}
}

func TestRebuildOptionsAndEmptyTrail(t *testing.T) {
	result := scanEvents(t)
	if result.Status != "ok" || !result.HistoryComplete || result.Scanned != 0 {
		t.Fatalf("%+v", result)
	}
	for _, options := range []Options{
		{MaxEvents: 0, Grace: DefaultGrace, Now: observed},
		{MaxEvents: MaxEvents + 1, Grace: DefaultGrace, Now: observed},
		{MaxEvents: 1, Grace: MinimumGrace - 1, Now: observed},
		{MaxEvents: 1, Grace: 7*24*time.Hour + 1, Now: observed},
		{MaxEvents: 1, Grace: DefaultGrace},
		{MaxEvents: 1, Grace: DefaultGrace, Now: observed, AfterSequence: -1},
	} {
		if _, err := Scan(t.Context(), &eventReader{}, options); err == nil {
			t.Fatalf("invalid options accepted: %+v", options)
		}
	}
}

type invalidReader struct {
	events []operational.AuditEvent
}

func (r invalidReader) ListAudit(context.Context, operational.AuditFilter) ([]operational.AuditEvent, error) {
	return r.events, nil
}

func TestRebuildInvalidReaderCannotLoopOrOverrun(t *testing.T) {
	for _, events := range [][]operational.AuditEvent{
		{{Sequence: 0}},
		{{Sequence: 1}, {Sequence: 1}},
		{{Sequence: 2}, {Sequence: 1}},
		{{Sequence: 1}, {Sequence: 2}, {Sequence: 3}, {Sequence: 4}},
	} {
		result, err := Scan(t.Context(), invalidReader{events}, Options{
			MaxEvents: 2, Grace: DefaultGrace, Now: observed,
		})
		if err == nil || result.Status != "incomplete" || result.TailReached {
			t.Fatalf("invalid reader accepted: %+v, err=%v", result, err)
		}
	}
}

func TestRebuildMultipleIDsSortedAndUnrelatedEventsIgnored(t *testing.T) {
	events := []operational.AuditEvent{
		rebuildEvent("admitted", "running", 2*time.Hour),
		rebuildEvent("admitted", "running", 2*time.Hour),
		{Event: "audit.verified"},
		rebuildEvent("completed", "succeeded", time.Hour),
		rebuildEvent("started", "running", 2*time.Hour),
	}
	events[1].Attributes["rebuild_id"] = strings.Repeat("b", 64)
	events[4].Attributes["rebuild_id"] = strings.Repeat("b", 64)
	result := scanEvents(t, events...)
	if result.Completed != 1 || result.MissingTerminal != 1 || len(result.Findings) != 1 ||
		result.Findings[0].RebuildID != strings.Repeat("b", 64) || result.Findings[0].FirstSequence != 2 {
		t.Fatalf("%+v", result)
	}
	events[3] = rebuildEvent("admitted", "running", 2*time.Hour)
	events[3].Attributes["rebuild_id"] = strings.Repeat("c", 64)
	result = scanEvents(t, events...)
	if result.MissingTerminal != 3 || len(result.Findings) != 3 ||
		result.Findings[0].FirstSequence != 1 || result.Findings[1].FirstSequence != 2 || result.Findings[2].FirstSequence != 4 {
		t.Fatalf("%+v", result)
	}
}

func TestScanStatus(t *testing.T) {
	attention := []Finding{{Issues: []string{"duplicate_start"}}}
	for name, tc := range map[string]struct {
		result Result
		want   string
	}{
		"clean complete history": {Result{HistoryComplete: true}, "ok"},
		"missing terminal":       {Result{HistoryComplete: true, MissingTerminal: 1}, "attention"},
		"finding with issues":    {Result{HistoryComplete: true, Findings: attention}, "attention"},
		"finding without issues": {Result{HistoryComplete: true, Findings: []Finding{{}}}, "ok"},
		"incomplete history":     {Result{MissingTerminal: 1}, "incomplete"},
		"result issues":          {Result{HistoryComplete: true, Issues: []string{"selected_suffix"}}, "incomplete"},
	} {
		t.Run(name, func(t *testing.T) {
			if got := scanStatus(tc.result); got != tc.want {
				t.Fatalf("scanStatus() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestClassifyTalliesAndSortsFindings(t *testing.T) {
	now := observed
	ids := map[string]*lifecycle{
		"done": {Finding: Finding{RebuildID: "done", FirstSequence: 1, FirstSeenAt: now.Add(-3 * time.Hour), Issues: []string{}}, admitted: 1, completed: 1},
		"late": {Finding: Finding{RebuildID: "late", FirstSequence: 5, FirstSeenAt: now.Add(-3 * time.Hour), Issues: []string{}}, admitted: 1},
		"new":  {Finding: Finding{RebuildID: "new", FirstSequence: 3, FirstSeenAt: now.Add(-time.Minute), Issues: []string{}}, admitted: 1},
		"orph": {Finding: Finding{RebuildID: "orph", FirstSequence: 2, FirstSeenAt: now.Add(-3 * time.Hour), Issues: []string{}}, completed: 1},
	}
	result := Result{TailReached: true, Issues: []string{"b_issue", "a_issue"}}
	classify(&result, ids, Options{Now: now, Grace: DefaultGrace})

	if result.Completed != 2 || result.Pending != 1 || result.MissingTerminal != 1 {
		t.Fatalf("counters = %+v", result)
	}
	if !slices.Equal(result.Issues, []string{"a_issue", "b_issue"}) {
		t.Fatalf("issues = %v", result.Issues)
	}
	var order []string
	for _, finding := range result.Findings {
		order = append(order, finding.RebuildID)
	}
	if want := []string{"orph", "new", "late"}; !slices.Equal(order, want) {
		t.Fatalf("finding order = %v, want %v", order, want)
	}
	if got := ids["orph"].Issues; !slices.Equal(got, []string{"admission_not_observed"}) {
		t.Fatalf("orphan issues = %v", got)
	}
}

func TestClassifyUnknownWhenTailNotReached(t *testing.T) {
	ids := map[string]*lifecycle{
		"x": {Finding: Finding{RebuildID: "x", FirstSequence: 1, FirstSeenAt: observed.Add(-5 * time.Hour), Issues: []string{}}, admitted: 1},
	}
	result := Result{}
	classify(&result, ids, Options{Now: observed, Grace: DefaultGrace})
	if ids["x"].Classification != "unknown" || result.MissingTerminal != 0 || result.Pending != 0 {
		t.Fatalf("classification = %q result = %+v", ids["x"].Classification, result)
	}
}
