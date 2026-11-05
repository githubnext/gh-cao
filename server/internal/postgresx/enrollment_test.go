package postgresx

import (
	"context"
	"errors"
	"testing"

	"github.com/githubnext/gh-cao/server/internal/admission"
	"github.com/githubnext/gh-cao/server/internal/collectnative"
	"github.com/githubnext/gh-cao/server/internal/query"
)

func TestNativeEnrollmentWithdrawalErasesLinkedAndFrozenEvidence(t *testing.T) {
	store, _ := nativeTestStore(t)
	t.Cleanup(func() { _ = store.Close() })
	publishStatisticalFixture(t, store, "withdrawal-fixture", "")
	if err := store.RecordAdmittedRun(t.Context(), admission.Identity{
		Repository: "o/kept", RunID: "601", WorkflowRef: "o/kept/.github/workflows/test.md@refs/heads/main",
	}); err != nil {
		t.Fatal(err)
	}
	authority := store.NativeEnrollment()
	if err := authority.WithMutation(t.Context(), func(ctx context.Context) error {
		return authority.Enroll(ctx, 7, []string{"o/r", "o/kept"})
	}); err != nil {
		t.Fatal(err)
	}
	if rows := statisticalResults(t, store)["statistical-runs"].Rows; len(rows) == 0 {
		t.Fatal("fixture has no frozen evidence")
	}
	if err := authority.WithMutation(t.Context(), func(ctx context.Context) error {
		removed, err := authority.Withdraw(ctx, 7, []string{"o/r"})
		if err == nil && len(removed) != 1 {
			t.Errorf("withdrawal omitted enrolled repository: %v", removed)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	sources := []string{"$runs", "$repositories", "$workflows", "$tools", "$domains", "$skills", "$audits",
		"$friction", "$issues", "$experiments", "$experimentAssignments", "$graders", "$graderObservations", "$evals", "$evalObservations"}
	for _, source := range sources {
		results, _, err := store.ExecuteSQLPlan(t.Context(), []query.Definition{{Name: "retained", From: source}}, []string{"retained"})
		if err != nil {
			t.Fatal(err)
		}
		expected := 0
		if source == "$runs" || source == "$repositories" || source == "$workflows" {
			expected = 1
		}
		if rows := results["retained"].Rows; len(rows) != expected {
			t.Fatalf("withdrawal retained %s or removed another repository: %v", source, rows)
		}
	}
	for _, source := range []string{"statistical-runs", "statistical-tools", "statistical-audits"} {
		value := statisticalResults(t, store)[source]
		if len(value.Rows) != 0 || value.Metadata["availability"] != "unavailable" {
			t.Fatalf("withdrawal retained frozen evidence or fell back to live rows: %s %v", source, value)
		}
	}
	for _, table := range statisticalTables {
		var count int
		if err := store.db.QueryRowContext(t.Context(), "SELECT count(*) FROM "+query.SQLIdentifier(table.name)+
			" WHERE namespace=$1", store.namespace).Scan(&count); err != nil || count != 0 {
			t.Fatalf("withdrawal retained private frozen table %s: %d %v", table.name, count, err)
		}
	}
	for _, table := range []string{"run_count_contributions", "daily_run_counts"} {
		var count int
		if err := store.db.QueryRowContext(t.Context(), "SELECT count(*) FROM "+query.SQLIdentifier(table)+
			" WHERE namespace=$1 AND repository_id='repo'", store.namespace).Scan(&count); err != nil || count != 0 {
			t.Fatalf("withdrawal retained daily evidence in %s: %d %v", table, count, err)
		}
	}
}

func TestNativeEnrollmentDoesNotPublishPredictedEpoch(t *testing.T) {
	store, _ := nativeTestStore(t)
	t.Cleanup(func() { _ = store.Close() })
	authority := store.NativeEnrollment()
	if err := authority.WithMutation(t.Context(), func(ctx context.Context) error {
		if err := authority.Enroll(ctx, 7, []string{"o/r"}); err != nil {
			return err
		}
		if _, err := authority.CurrentScope(ctx, "o/r"); !errors.Is(err, collectnative.ErrBusy) {
			t.Fatalf("uncommitted epoch escaped to task admission: %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	scope, err := authority.CurrentScope(t.Context(), "o/r")
	if err != nil || scope.Epoch < 1 {
		t.Fatalf("committed scope unavailable: %v %v", scope, err)
	}
}
