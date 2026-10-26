package postgresx

import (
	"slices"
	"strings"
	"testing"

	"github.com/githubnext/gh-cao/server/internal/query"
)

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
