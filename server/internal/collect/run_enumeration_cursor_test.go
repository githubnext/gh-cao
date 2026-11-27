package collect

import (
	"reflect"
	"testing"
	"time"
)

func TestEnumerationWindowFreezesUTCSecondBoundaries(t *testing.T) {
	zone := time.FixedZone("offset", 5*3600)
	before := time.Date(2026, 3, 10, 12, 30, 15, 987654321, zone)

	after, frozen := enumerationWindow(before, 7)
	wantBefore := time.Date(2026, 3, 10, 7, 30, 15, 0, time.UTC)
	if !frozen.Equal(wantBefore) || frozen.Location() != time.UTC || frozen.Nanosecond() != 0 {
		t.Fatalf("before = %v, want %v in UTC", frozen, wantBefore)
	}
	if want := wantBefore.Add(-7 * 24 * time.Hour); !after.Equal(want) {
		t.Fatalf("after = %v, want %v", after, want)
	}

	after, _ = enumerationWindow(before, 0)
	if !after.Equal(time.Unix(0, 0)) {
		t.Fatalf("unbounded window after = %v, want epoch", after)
	}
}

func TestValidNextPage(t *testing.T) {
	for _, tc := range []struct {
		name       string
		next, page int
		want       bool
	}{
		{"end of listing", 0, 5, true},
		{"advances", 6, 5, true},
		{"upper bound", 10000, 5, true},
		{"repeats page", 5, 5, false},
		{"regresses", 3, 5, false},
		{"beyond bound", 10001, 5, false},
	} {
		if got := validNextPage(tc.next, tc.page); got != tc.want {
			t.Errorf("%s: validNextPage(%d, %d) = %t, want %t", tc.name, tc.next, tc.page, got, tc.want)
		}
	}
}

func TestAdvanceEnumeration(t *testing.T) {
	base := RunEnumeration{WorkflowID: 10, Page: 2, WindowDays: 30}

	t.Run("continues current listing", func(t *testing.T) {
		got := advanceEnumeration(base, 3)
		want := base
		want.Page = 3
		if got == nil || !reflect.DeepEqual(*got, want) {
			t.Fatalf("cursor = %+v, want %+v", got, want)
		}
	})

	t.Run("moves to next remaining workflow", func(t *testing.T) {
		current := base
		current.RemainingWorkflows = []int64{20, 30}
		got := advanceEnumeration(current, 0)
		if got == nil || got.WorkflowID != 20 || got.Page != 1 || !reflect.DeepEqual(got.RemainingWorkflows, []int64{30}) {
			t.Fatalf("cursor = %+v, want workflow 20 page 1 remaining [30]", got)
		}
		if len(current.RemainingWorkflows) != 2 {
			t.Fatalf("input cursor was modified: %+v", current)
		}
	})

	t.Run("resumes workflow listing", func(t *testing.T) {
		current := base
		current.NextWorkflowPage = 4
		got := advanceEnumeration(current, 0)
		if got == nil || got.WorkflowID != 0 || got.Page != 4 || got.NextWorkflowPage != 0 {
			t.Fatalf("cursor = %+v, want workflow listing page 4", got)
		}
	})

	t.Run("exhausted", func(t *testing.T) {
		if got := advanceEnumeration(base, 0); got != nil {
			t.Fatalf("cursor = %+v, want nil", got)
		}
	})
}
