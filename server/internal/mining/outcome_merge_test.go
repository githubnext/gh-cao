package mining

import (
	"testing"
	"time"
)

func TestOutcomeMergePreservesEvidenceClocksAndConflicts(t *testing.T) {
	now := time.Now().UTC()
	original := Outcome{Repository: "o/r", Number: 1, URL: "https://github.com/o/r/issues/1",
		State: "closed", StateReason: "not_planned", UpdatedAt: now, ObservedAt: now, Source: "github-webhook"}
	for _, test := range []struct {
		name     string
		change   func(*Outcome)
		conflict bool
		actor    string
	}{
		{name: "duplicate", change: func(o *Outcome) { o.ObservedAt = now.Add(time.Hour) }},
		{name: "older", change: func(o *Outcome) { o.UpdatedAt = now.Add(-time.Hour); o.State = "open" }},
		{name: "actor enrichment", change: func(o *Outcome) { o.CloseActor = "User"; o.ObservedAt = now.Add(time.Hour) }, actor: "User"},
		{name: "same-time conflict", change: func(o *Outcome) { o.State = "open" }, conflict: true},
		{name: "different owner", change: func(o *Outcome) { o.Repository = "o/other" }, conflict: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			next := original
			test.change(&next)
			merged, err := original.Merge(next)
			if (err != nil) != test.conflict {
				t.Fatalf("conflict=%t err=%v", test.conflict, err)
			}
			if err == nil && (!merged.ObservedAt.Equal(original.ObservedAt) || merged.CloseActor != test.actor ||
				!merged.UpdatedAt.Equal(original.UpdatedAt) || merged.State != original.State) {
				t.Fatalf("replayed observation changed evidence: %+v", merged)
			}
		})
	}
	later := original
	later.UpdatedAt, later.ObservedAt, later.State, later.StateReason = now.Add(time.Hour), now.Add(time.Hour), "open", "reopened"
	if merged, err := original.Merge(later); err != nil || merged != later {
		t.Fatalf("newer observation did not win: %+v %v", merged, err)
	}
}
