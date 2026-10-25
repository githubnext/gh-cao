package postgresx

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/query"
)

func assertMonthlyStoredEvidence(t *testing.T, ctx context.Context, store *Store, days []monthlyBudgetDay, asOf time.Time) {
	t.Helper()
	runCutoff := dayStart(asOf.AddDate(0, 0, -defaultRunRetentionDays))
	linkedCutoff := dayStart(asOf.AddDate(0, 0, -defaultLinkedRetentionDays))
	observed := 0
	for _, day := range days {
		observed += day.Runs
	}
	firstID := max(0, observed-store.capacity().rows)
	end, err := time.Parse(time.DateOnly, days[0].Date)
	if err != nil {
		t.Fatal(err)
	}
	end = end.AddDate(0, 0, monthlyDays)
	retained := []monthlyBudgetDay{}
	expectedRuns, expectedAudits := 0, 0
	monthlyBudgetWorkload(end, func(run monthlyBudgetRun) {
		if run.ID <= firstID || run.ID > observed || run.At.Before(runCutoff) {
			return
		}
		date := run.At.Format(time.DateOnly)
		if len(retained) == 0 || retained[len(retained)-1].Date != date {
			retained = append(retained, monthlyBudgetDay{Date: date})
		}
		day := &retained[len(retained)-1]
		day.Runs++
		day.AicHundredths += int64(run.AicHundredths)
		expectedRuns++
		if !run.At.Before(linkedCutoff) {
			expectedAudits++
		}
	})
	state, err := store.State(ctx)
	if err != nil || state.Counts["$runs"] != expectedRuns || state.Counts["$audits"] != expectedAudits {
		t.Fatalf("retention lost fresh rows or resurrected expired evidence: counts=%v wantRuns=%d wantAudits=%d err=%v",
			state.Counts, expectedRuns, expectedAudits, err)
	}
	expectedDaily := 0
	for _, day := range days {
		if day.Date >= dailyCutoff(asOf).Format(time.DateOnly) &&
			day.Date < dailyCutoff(asOf).AddDate(0, 0, 21).Format(time.DateOnly) {
			expectedDaily += day.Runs
		}
	}
	sources, _, err := store.ExecuteSQLPlan(ctx, []query.Definition{{
		Name: "retained-health", From: dailySource,
		Aggregate: &query.Aggregate{Values: []query.AggregateValue{{Field: "total", As: "runs", Reducer: "sum"}}},
	}}, []string{"retained-health"})
	if err != nil || len(sources["retained-health"].Rows) != 1 {
		t.Fatalf("bounded additive health query became unusable: %v %v", sources, err)
	}
	value := sources["retained-health"].Rows[0]["runs"]
	if expectedDaily > 0 && fmt.Sprint(value) != fmt.Sprint(expectedDaily) ||
		expectedDaily == 0 && value != nil && fmt.Sprint(value) != "0" {
		t.Fatalf("daily health lost retention provenance or counted detail twice: got=%v want=%d", value, expectedDaily)
	}
	// Independent SQL checks the full retained population, never weighted
	// sample counts or a JavaScript reimplementation of Dashboard Language.
	rows, err := store.db.QueryContext(ctx, `SELECT
		to_char(created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD'), count(*),
		count(DISTINCT repository_id), (sum(aic_total)*100)::bigint
		FROM runs WHERE namespace=$1 GROUP BY 1 ORDER BY 1`, store.namespace)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	index := 0
	for rows.Next() {
		var date string
		var count, repositories int
		var aic int64
		if err := rows.Scan(&date, &count, &repositories, &aic); err != nil {
			t.Fatal(err)
		}
		if index >= len(retained) || date != retained[index].Date || count != retained[index].Runs ||
			repositories != min(monthlyRepositories, retained[index].Runs) || aic != retained[index].AicHundredths {
			t.Fatalf("stored day lost fleet coverage, runs or decimal AIC: day=%s runs=%d repos=%d aic=%d", date, count, repositories, aic)
		}
		index++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if index != len(retained) {
		t.Fatalf("stored UTC days=%d, want %d", index, len(retained))
	}
	for name, statement := range map[string]string{
		"run structural parents": `SELECT count(*) FROM runs r
			LEFT JOIN workflows w ON w.namespace=r.namespace AND w.id=r.workflow_id
			LEFT JOIN repositories p ON p.namespace=r.namespace AND p.id=r.repository_id
			WHERE r.namespace=$1 AND (w.id IS NULL OR p.id IS NULL OR w.repository_id<>r.repository_id)`,
		"run-owned audit bundles": `SELECT count(*) FROM (
			SELECT r.id FROM runs r LEFT JOIN audits a ON a.namespace=r.namespace AND a.run_id=r.id
			WHERE r.namespace=$1 GROUP BY r.id,r.run_at
			HAVING count(a.id)<>CASE WHEN r.run_at >= $2 THEN 1 ELSE 0 END) broken`,
		"orphan audit records": `SELECT count(*) FROM audits a
			LEFT JOIN runs r ON r.namespace=a.namespace AND r.id=a.run_id
			WHERE a.namespace=$1 AND r.id IS NULL`,
		"old evicted identities": `SELECT count(*) FROM runs
			WHERE namespace=$1 AND substring(id FROM 5)::bigint<=$2`,
	} {
		var broken int
		args := []any{store.namespace}
		if name == "run-owned audit bundles" {
			args = append(args, linkedCutoff)
		}
		if name == "old evicted identities" {
			args = append(args, firstID)
		}
		if err := store.db.QueryRowContext(ctx, statement, args...).Scan(&broken); err != nil {
			t.Fatal(err)
		}
		if broken != 0 {
			t.Fatalf("%s: %d broken relationships", name, broken)
		}
	}
}
