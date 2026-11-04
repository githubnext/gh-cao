package postgresx_test

import (
	"bytes"
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/collectnative"
	"github.com/githubnext/gh-cao/server/internal/query"
	"github.com/githubnext/gh-cao/server/internal/testutil"
)

func TestNativeAcquisitionSharedEvaluationDefinitionsActivityOracle(t *testing.T) {
	store := testutil.Postgres(t, t.Context(), "CAO_TEST_POSTGRES_URL")
	acquisition := store.Acquisition()
	identity := acquisitionIdentity()
	if err := acquisition.SetEnrollment(t.Context(), identity.Repository, identity.InstallationID); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("../../../dashboard/site/test/fixtures/evaluation-session.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	clock := time.Now().Add(-time.Hour).UTC().Truncate(time.Millisecond)
	evidence := make([]collectnative.Evidence, 0, 3)
	for _, offset := range []int{2, 0, 1} {
		id := identity
		id.RunID += int64(offset)
		at := clock.Add(time.Duration(offset) * time.Minute).Format(time.RFC3339Nano)
		content := bytes.ReplaceAll(raw, []byte(`"303"`), []byte(strconv.Quote(strconv.FormatInt(id.RunID, 10))))
		content = bytes.ReplaceAll(content, []byte("2026-10-15T10:00:40Z"), []byte(at))
		if offset == 2 {
			content = bytes.ReplaceAll(content, []byte(`"unit":"ratio",`), nil)
			content = bytes.ReplaceAll(content, []byte(`"threshold":0.8`), []byte(`"threshold":0.95`))
		}
		session, _, err := collectnative.ProjectSession(t.Context(), content, "usage/aw_session.jsonl", id, collectnative.Limits{})
		if err != nil {
			t.Fatal(err)
		}
		record := acquisitionEvidence(id)
		record.Run["created_at"], record.Run["updated_at"] = at, at
		record.Run["cao_unified_session"] = session
		evidence = append(evidence, record)
	}
	expected := nativeOracleBatch(t, evidence)
	t.Setenv("PATH", "/nonexistent")
	for _, record := range evidence {
		publication := publishNative(t, acquisition, record)
		if _, err := acquisition.Activate(t.Context(), publication); err != nil {
			t.Fatal(err)
		}
	}
	columns := map[string][]string{
		"graders":               {"id", "name", "unit", "threshold", "firstObservedAt", "lastObservedAt"},
		"evals":                 {"id", "name", "firstObservedAt", "lastObservedAt"},
		"experiments":           {"id", "name", "firstObservedAt", "lastObservedAt"},
		"graderObservations":    {"id", "runId", "graderId", "auditId", "experimentId", "variant", "value", "status"},
		"evalObservations":      {"id", "runId", "evalId", "auditId", "experimentId", "variant", "status"},
		"experimentAssignments": {"id", "runId", "experimentId", "variant"},
	}
	definitions := make([]query.Definition, 0, len(columns))
	names := make([]string, 0, len(columns))
	for collection, fields := range columns {
		selected := make([]query.SelectedField, 0, len(fields))
		for _, field := range fields {
			selected = append(selected, query.SelectedField{Field: field})
		}
		definitions = append(definitions, query.Definition{Name: collection, From: "$" + collection, Select: selected})
		names = append(names, collection)
	}
	results, _, err := store.ExecuteSQLPlan(t.Context(), definitions, names)
	if err != nil {
		t.Fatal(err)
	}
	for collection, fields := range columns {
		if len(results[collection].Rows) != len(expected[collection]) || len(expected[collection]) == 0 {
			t.Errorf("%s count: got=%d want=%d", collection, len(results[collection].Rows), len(expected[collection]))
			continue
		}
		byID := map[string]map[string]any{}
		for _, row := range expected[collection] {
			byID[fmt.Sprint(row["id"])] = row
		}
		for _, row := range results[collection].Rows {
			want := byID[fmt.Sprint(row["id"])]
			if want == nil {
				t.Errorf("unexpected %s identity: %v", collection, row["id"])
				continue
			}
			for _, field := range fields {
				if !canonicalValuesEqual(row[field], want[field]) {
					t.Errorf("%s %v.%s: got=%v want=%v", collection, row["id"], field, row[field], want[field])
				}
			}
		}
	}
}
