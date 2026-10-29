package auditlifecycle

import (
	"testing"
	"time"
)

func TestClassify(t *testing.T) {
	options := Options{Now: observed, Grace: time.Hour}
	old := observed.Add(-2 * time.Hour)
	recent := observed.Add(-time.Minute)
	tests := []struct {
		name      string
		result    Result
		entry     lifecycle
		want      string
		completed int
		pending   int
		missing   int
	}{
		{"terminal", Result{TailReached: true}, lifecycle{completed: 1, Finding: Finding{FirstSeenAt: old}}, "terminal_observed", 1, 0, 0},
		{"partial history", Result{}, lifecycle{Finding: Finding{FirstSeenAt: old}}, "unknown", 0, 0, 0},
		{"history gap", Result{TailReached: true, Issues: []string{"history_gap"}}, lifecycle{Finding: Finding{FirstSeenAt: old}}, "unknown", 0, 0, 0},
		{"zero time", Result{TailReached: true}, lifecycle{}, "unknown", 0, 0, 0},
		{"clock uncertain", Result{TailReached: true}, lifecycle{Finding: Finding{FirstSeenAt: old, Issues: []string{"clock_uncertain"}}}, "unknown", 0, 0, 0},
		{"within grace", Result{TailReached: true}, lifecycle{Finding: Finding{FirstSeenAt: recent}}, "pending", 0, 1, 0},
		{"past grace", Result{TailReached: true}, lifecycle{Finding: Finding{FirstSeenAt: old}}, "missing_terminal", 0, 0, 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			classify(&test.result, &test.entry, options)
			if test.entry.Classification != test.want || test.result.Completed != test.completed ||
				test.result.Pending != test.pending || test.result.MissingTerminal != test.missing {
				t.Fatalf("classify = %q %+v, want %q", test.entry.Classification, test.result, test.want)
			}
		})
	}
}

func TestResultStatus(t *testing.T) {
	tests := []struct {
		name   string
		result Result
		want   string
	}{
		{"clean", Result{HistoryComplete: true}, "ok"},
		{"incomplete history", Result{}, "incomplete"},
		{"scan issue wins over missing terminal", Result{HistoryComplete: true, Issues: []string{"event_limit"}, MissingTerminal: 1}, "incomplete"},
		{"missing terminal", Result{HistoryComplete: true, MissingTerminal: 1}, "attention"},
		{"finding issue", Result{HistoryComplete: true, Findings: []Finding{{Issues: []string{"duplicate_start"}}}}, "attention"},
		{"finding without issues", Result{HistoryComplete: true, Findings: []Finding{{}}}, "ok"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := resultStatus(test.result); got != test.want {
				t.Fatalf("resultStatus = %q, want %q", got, test.want)
			}
		})
	}
}
