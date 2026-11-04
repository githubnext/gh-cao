package postgresx

import (
	"strings"
	"testing"

	"github.com/githubnext/gh-cao/server/internal/dashboardstore"
	"github.com/githubnext/gh-cao/server/internal/query"
)

func TestNativeEntityFilterUsesBoundValuesAndWhitelistedIdentifiers(t *testing.T) {
	namespace := `tenant' OR TRUE --`
	predicate := `run' OR TRUE --`
	reader := &readTransaction{store: &Store{namespace: namespace}}
	plan, err := query.CompileSQL([]query.Definition{{Name: "picked", From: "$tools",
		Filter: &query.Filter{Predicates: []query.Predicate{{Field: "runId", Equals: predicate}}}}}, []string{"picked"}, reader.entitySQLSource)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(plan.CTEs, namespace) || strings.Contains(plan.CTEs, predicate) {
		t.Fatal("SQL interpolated untrusted values")
	}
	found := false
	for _, value := range plan.Args {
		if value == predicate {
			found = true
		}
	}
	if !found {
		t.Fatal("predicate was not bound")
	}
}

func TestWorkflowSubjectPageScopesValueRootsByIndexedIdentity(t *testing.T) {
	reader := &readTransaction{store: &Store{namespace: "tenant"}}
	page := SQLPage{Limit: 25, Keyset: &dashboardstore.KeysetPage{
		Boundary: "workflow-subjects", Comparator: "utf8-bytewise-v1",
		Keys: []string{"repositoryId", "path", "id"},
	}}
	workflows, err := reader.entitySQLSource("$workflows")
	if err != nil {
		t.Fatal(err)
	}
	workflows, err = workflowSubjectPageSQL(workflows, page)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(workflows.SQL, `repository_id COLLATE "C"`) ||
		!strings.Contains(workflows.SQL, `LIMIT {}`) ||
		workflows.Params[len(workflows.Params)-1] != 26 {
		t.Fatalf("workflow root is not a size-plus-one keyset page: %s %#v", workflows.SQL, workflows.Params)
	}
	values, err := reader.entitySQLSource("$operationalValues")
	if err != nil {
		t.Fatal(err)
	}
	values, err = workflowSubjectScopedSQL(values, "$operationalValues", "tenant", page)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(values.SQL, "JOIN LATERAL") ||
		!strings.Contains(values.SQL, `t."repository_id"=visible.repository_id`) ||
		!strings.Contains(values.SQL, `visible.path=coalesce(t."workflow",'')`) {
		t.Fatalf("native Value reads are not driven by the visible workflow page: %s", values.SQL)
	}
	evidence := reader.operationalValueEvidenceSQLSource()
	evidence, err = workflowSubjectScopedSQL(evidence, "$operationalValueEvidence", "tenant", page)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(evidence.SQL, "t.__workflow_id IN (SELECT id") {
		t.Fatalf("rich Value evidence is not scoped by visible workflow identity: %s", evidence.SQL)
	}
}
