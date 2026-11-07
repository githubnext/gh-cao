package postgresx

import (
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/githubnext/gh-cao/server/internal/model"
	"github.com/githubnext/gh-cao/server/internal/query"
)

func valueViewDefinitions(t *testing.T) []query.Definition {
	t.Helper()
	content, err := os.ReadFile("../../../dashboard/site/dashboard-fragments/workflow-value.json")
	if err != nil {
		t.Fatal(err)
	}
	var fragment struct {
		Queries []query.Definition `json:"queries"`
	}
	if err := json.Unmarshal(content, &fragment); err != nil {
		t.Fatal(err)
	}
	return append(canonicalProjectionDefinitions(t), fragment.Queries...)
}

func TestValueViewsExecuteAgainstHostedEvidence(t *testing.T) {
	definitions := valueViewDefinitions(t)
	names := []string{"value-workflow-overview", "value-collection-health", "value-report-summary", "value-review-overview"}
	reader := &readTransaction{store: &Store{namespace: "value-views"}}
	if _, err := query.CompileSQL(definitions, names, reader.entitySQLSource); err != nil {
		t.Fatal(err)
	}

	store, _ := nativeTestStore(t)
	packet := evidencePacket(t)
	health := collectionFixture(t)
	nativeSeed(t.Context(), t, store, map[string][]model.Row{
		"$repositories": {
			{"id": packet["repositoryId"], "owner": "octo", "name": "example"},
		},
		"$workflows": {
			{"id": packet["workflowId"], "repositoryId": packet["repositoryId"],
				"path": packet["sourcePath"], "name": "Verify bugs", "role": "standalone", "state": "unknown"},
		},
		"$operationalValueEvidence": {packet},
		"$workflowValueCollections": {health},
	})
	sources, metrics, err := store.ExecuteSQLPlan(t.Context(), definitions, names)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(metrics.PushedDown, "sql-plan") || len(metrics.FallbackOperations) != 0 {
		t.Fatalf("Value view queries must execute in hosted SQL: %+v", metrics)
	}
	route := "octo/example:.github/workflows/verify-bugs.md"
	for _, name := range names {
		if sources[name].Metadata["availability"] == "unavailable" {
			t.Fatalf("%s is unavailable: %+v", name, sources[name].Metadata)
		}
	}
	var overview model.Row
	for _, row := range sources["value-workflow-overview"].Rows {
		if row["workflow-route"] == route {
			overview = row
		}
	}
	href, _ := overview["overview-href"].(string)
	if !strings.HasPrefix(href, "#page-value-report?report=") ||
		overview["overview-state"] != "deferred" || overview["trend-movement"] != "Better" {
		t.Fatalf("hosted Value card lost the canonical evidence and collection state: %+v", overview)
	}
	healthRows := sources["value-collection-health"].Rows
	if len(healthRows) != 1 || healthRows[0]["workflow-route"] != route ||
		healthRows[0]["collection-state"] != "deferred" ||
		healthRows[0]["last-attempt-outcome"] != "failed" {
		t.Fatalf("collection decision changed the accepted capture clocks: %+v", healthRows)
	}
	for projected, original := range map[string]string{
		"captured-at": "observedAt", "evidence-cutoff": "windowEnd",
		"checked-at": "observedAt", "last-attempt-at": "lastAttemptAt",
	} {
		got, gotErr := evidenceTime(healthRows[0][projected])
		source := packet
		if projected == "checked-at" || projected == "last-attempt-at" {
			source = health
		}
		want, wantErr := evidenceTime(source[original])
		if gotErr != nil || wantErr != nil || !got.Equal(want) {
			t.Fatalf("hosted %s differs from the accepted evidence: %+v", projected, healthRows[0])
		}
	}
	if healthRows[0]["captured-at"] == healthRows[0]["last-attempt-at"] {
		t.Fatalf("hosted capture and failed attempt must stay distinct: %+v", healthRows[0])
	}
	report := sources["value-report-summary"].Rows
	if len(report) != 1 || report[0]["movement"] != "Better" ||
		report[0]["report-href"] != overview["overview-href"] {
		t.Fatalf("hosted report differs from the Value card: %+v", report)
	}
	review := sources["value-review-overview"].Rows
	if !slices.ContainsFunc(review, func(row model.Row) bool { return row["workflow-route"] == route }) {
		t.Fatal("deferred collection with a retained result must still need review")
	}
}

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
			{"id": "mixed-case-repository", "owner": "Example", "name": "Service"},
			{"id": "unknown-repository"},
		},
		"$operationalValues": {
			measurement("zero", "repository", "incident-response", 0), missing, mismatch,
			measurement("other-workflow", "repository", "another-workflow", 5),
			measurement("other-repository", "other-repository", "incident-response", 6),
			measurement("mixed-case", "mixed-case-repository", "incident-response", 8),
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
		{"workflow-route", "Example/Service:.github/workflows/incident-response.md", 1},
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
			case "mixed-case":
				if row["repository"] != "Example/Service" ||
					row["workflow-route"] != "Example/Service:.github/workflows/incident-response.md" {
					t.Fatalf("repository parent casing changed: %+v", row)
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
