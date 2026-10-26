package postgresx

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/githubnext/gh-cao/server/internal/model"
	"github.com/githubnext/gh-cao/server/internal/query"
)

func TestNativeMeasurementSourceContract(t *testing.T) {
	reader := &readTransaction{store: &Store{namespace: "measurement-projections"}}
	if _, err := query.CompileSQL(canonicalProjectionDefinitions(t), []string{"workflow-value-measurements"},
		reader.entitySQLSource); err != nil {
		t.Fatal(err)
	}
}

func TestNativeMeasurementRepositoryProjection(t *testing.T) {
	store, _ := nativeTestStore(t)
	measurement := func(id, repository, workflow string, value any) model.Row {
		return model.Row{
			"id": id, "repositoryId": repository, "workflow-slug": workflow,
			"valueId": workflow + ".response-hours", "value": value,
			"operational-value-role": "primary", "operational-value-unit": "hours",
			"timestamp": "2026-10-15T00:00:00Z", "measurement-kind": "interval",
		}
	}
	missing := measurement("missing", "repository", "incident-response", nil)
	missing["maturity-status"] = "unavailable"
	missing["coverage-status"] = "partial"
	missing["missing-reason"] = "Incomplete retained deliveries"
	mismatch := measurement("mismatch", "repository", "incident-response", 4)
	mismatch["valueId"] = "another-workflow.response-hours"
	nativeSeed(t.Context(), t, store, map[string][]model.Row{
		"$repositories": {
			{"id": "other-repository", "owner": "octo", "name": "other"},
			{"id": "unknown-repository"},
		},
		"$operationalValues": {
			measurement("zero", "repository", "incident-response", 0), missing, mismatch,
			measurement("other-workflow", "repository", "another-workflow", 5),
			measurement("other-repository", "other-repository", "incident-response", 6),
			measurement("unknown-repository", "unknown-repository", "incident-response", 7),
		},
	})
	for _, selection := range []struct {
		field string
		value string
		want  int
	}{
		{"workflow-route", "octo/api:.github/workflows/incident-response.md", 2},
		{"workflow-route", "octo/other:.github/workflows/incident-response.md", 1},
		{"id", "zero", 1},
		{"id", "unknown-repository", 1},
	} {
		definitions := append(canonicalProjectionDefinitions(t), query.Definition{
			Name: "selected-measurements", From: "workflow-value-measurements",
			Filter: &query.Filter{Predicates: []query.Predicate{{Field: selection.field, Equals: selection.value}}},
		})
		sources, metrics, err := store.ExecuteSQLPlan(t.Context(), definitions, []string{"selected-measurements"})
		if err != nil {
			t.Fatal(err)
		}
		rows := sources["selected-measurements"].Rows
		if len(rows) != selection.want || metrics.PeakWorkingRows > selection.want {
			t.Fatalf("measurement scope changed or expanded unrelated rows: %+v, %+v", rows, metrics)
		}
		for _, row := range rows {
			switch row["id"] {
			case "zero":
				if row["value"] != json.Number("0") || row["repository"] != "octo/api" {
					t.Fatalf("lost known zero or typed repository relationship: %+v", row)
				}
			case "missing":
				if row["value"] != nil || row["coverage-status"] != "partial" ||
					row["missing-reason"] != "Incomplete retained deliveries" {
					t.Fatalf("lost unavailable assessment: %+v", row)
				}
			case "unknown-repository":
				if row["repository"] != nil || row["workflow-route"] != nil {
					t.Fatalf("unknown repository coordinates became a route: %+v", row)
				}
			}
		}
	}
}

func TestValueScopePrecedesSQLResourceStatistics(t *testing.T) {
	reader := &readTransaction{store: &Store{namespace: "isolated-value-scope"}}
	definitions := []query.Definition{
		{Name: "operational-value-evidence", From: "$operationalValueEvidence"},
		{Name: "one", From: "operational-value-evidence", Filter: &query.Filter{Predicates: []query.Predicate{{Field: "report-id", Equals: "retained-one"}}}},
		{Name: "two", From: "operational-value-evidence", Filter: &query.Filter{Predicates: []query.Predicate{{Field: "workflow-route", Equals: "octo/example:.github/workflows/verify-bugs.md"}}}},
	}

	selections := query.SourceEqualities(definitions, []string{"one", "two"}, "operational-value-evidence", "$operationalValueEvidence", "report-id", "workflow-route")
	plan, err := query.CompileSQL(definitions, []string{"one", "two"}, func(string) (query.SQLRelation, error) {
		return reader.operationalValueEvidenceSQLSource(selections...), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	statement, args, err := plan.StatisticsStatement()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(statement, "p.id=") || !strings.Contains(statement, "p.workflow_id=") ||
		!slices.Contains(args, any("retained-one")) ||
		!slices.Contains(args, any("workflow:octo%2Fexample%3A.github%2Fworkflows%2Fverify-bugs.md")) {
		t.Fatal("SQL budget checks must read scoped typed facts, not the whole namespace")
	}
	definitions[2].Filter = nil
	if query.SourceEqualities(definitions, []string{"one", "two"}, "operational-value-evidence", "$operationalValueEvidence", "report-id", "workflow-route") != nil {
		t.Fatal("an unscoped sibling must not be narrowed")
	}
}

func TestSnapshotOnlyScopePrecedesSQLResourceStatistics(t *testing.T) {
	reader := &readTransaction{store: &Store{namespace: "isolated-snapshot-scope"}}
	definitions := []query.Definition{
		{Name: "operational-value-evidence", From: "$operationalValueEvidence"},
		{Name: "snapshot-only", From: "operational-value-evidence",
			Filter: &query.Filter{Predicates: []query.Predicate{{Field: "snapshot-route", Equals: "packet|snapshot"}}}},
	}
	selections := query.OperationalValueEvidenceSelections(definitions, []string{"snapshot-only"})
	plan, err := query.CompileSQL(definitions, []string{"snapshot-only"}, func(string) (query.SQLRelation, error) {
		return reader.operationalValueEvidenceSQLSource(selections...), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	statement, args, err := plan.StatisticsStatement()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(statement, "p.id=") || !slices.Contains(args, any("packet")) ||
		!strings.Contains(statement, "f.parent_id") {
		t.Fatal("snapshot-only queries must bind the parent packet before evaluating snapshot/item identities")
	}
}
