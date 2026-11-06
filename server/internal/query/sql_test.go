package query

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func sqlTestResolver(name string) (SQLRelation, error) {
	if name != "facts" && name != "owners" {
		return SQLRelation{}, errors.New("unregistered SQL source")
	}
	return SQLRelation{
		SQL:    "(SELECT * FROM " + SQLIdentifier(name) + " WHERE namespace = {}) AS source",
		Params: []any{"tenant"},
		Order:  "ordinal",
		Columns: map[string]SQLColumn{
			"id":    {Expression: "id", Presence: "TRUE", Kind: SQLText},
			"owner": {Expression: "owner", Presence: "TRUE", Kind: SQLText},
			"value": {Expression: "value", Presence: "TRUE", Kind: SQLNumber},
		},
	}, nil
}

func TestCompileRelationalSQLDAG(t *testing.T) {
	valueField := "value"
	definitions := []Definition{
		{Name: "selected", From: "facts", Filter: &Filter{Predicates: []Predicate{{Field: "owner", Equals: `owner' OR TRUE --`}}},
			Compute: []ComputedField{{As: "double", Function: "product", Args: []Argument{{Field: &valueField}, {Value: 2}}}}},
		{Name: "totals", From: "selected", Aggregate: &Aggregate{By: []string{"owner"}, Values: []AggregateValue{{Field: "double", As: "total", Reducer: "sum"}}},
			Select: []SelectedField{{Field: "owner"}, {Field: "total"}}, OrderBy: []OrderField{{Field: "total", Direction: "desc"}}},
	}

	plan, err := CompileSQL(definitions, []string{"totals"}, sqlTestResolver)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(plan.CTEs, `owner' OR TRUE --`) || !strings.Contains(plan.CTEs, "GROUP BY") ||
		!strings.Contains(plan.CTEs, "MATERIALIZED") || plan.Outputs["totals"].Columns["total"].Kind != SQLNumber {
		t.Fatalf("unexpected SQL plan: %+v", plan)
	}
	if len(plan.Args) != 3 || plan.Args[0] != "tenant" || plan.Args[1] != `owner' OR TRUE --` || plan.Args[2] != 2 {
		t.Fatalf("unbound or reordered source/query parameters: %#v", plan.Args)
	}
	for _, step := range plan.Steps {
		if step.Operation == "filter" && step.Weight != 1 {
			t.Fatal("filter must charge unfiltered input")
		}
	}
}

func TestCompileSQLPageSeparatesBoundaryLookaheadFromConsumers(t *testing.T) {
	definitions := []Definition{
		{Name: "boundary", From: "facts"},
		{Name: "visible", From: "boundary", Filter: &Filter{Predicates: []Predicate{{Field: "id", Equals: "second"}}}},
	}
	plan, err := CompileSQLPage(definitions, []string{"boundary", "visible"}, sqlTestResolver, "boundary", 1)
	if err != nil {
		t.Fatal(err)
	}
	var bounded SQLStep
	for _, step := range plan.Steps {
		if step.Operation == "page" {
			bounded = step
		}
	}
	if bounded.Relation == "" || plan.Outputs["boundary"].SQL == SQLIdentifier(bounded.Relation) {
		t.Fatalf("boundary output discarded lookahead or downstream page was unbounded: %+v", plan)
	}
	if !strings.Contains(plan.CTEs, "FROM "+SQLIdentifier(bounded.Relation)) {
		t.Fatalf("downstream filter bypassed the bounded page: %s", plan.CTEs)
	}
	for _, testcase := range []struct {
		boundary string
		limit    int
	}{{"", 1}, {"missing", 1}, {"boundary", 0}, {"boundary", 33}} {
		if _, err := CompileSQLPage(definitions, []string{"visible"}, sqlTestResolver, testcase.boundary, testcase.limit); err == nil {
			t.Fatalf("invalid page accepted: %+v", testcase)
		}
	}
}

func TestCompileCalendarWeekBooleanInclusion(t *testing.T) {
	started := "started"
	for _, included := range []bool{true, false} {
		definition := Definition{
			Name: "rhythm", From: "facts",
			Compute: []ComputedField{{
				As: "point", Function: "calendar-week-point",
				Args: []Argument{{Field: &started}, {Value: "2026-09-24T00:00:00Z"}, {Value: included}},
			}},
		}
		plan, err := CompileSQL([]Definition{definition}, []string{"rhythm"}, func(string) (SQLRelation, error) {
			return SQLRelation{
				SQL: "(SELECT '2026-09-23T18:00:00Z'::timestamptz AS started) AS facts",
				Columns: map[string]SQLColumn{
					"started": {Expression: "started", Presence: "TRUE", Kind: SQLTimestamp},
				},
			}, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		point := plan.Outputs["rhythm"].Columns["point"]
		if !strings.Contains(plan.CTEs, "COALESCE(") || strings.Contains(plan.CTEs, " = 'success'") || point.Point != "calendar" {
			t.Fatalf("calendar inclusion must preserve the boolean selector: %+v", plan)
		}
		if len(plan.Args) != 2 || plan.Args[1] != included {
			t.Fatalf("calendar inclusion must bind the boolean unchanged: %#v", plan.Args)
		}
	}
}

func TestCompileSQLJoinsAndUnions(t *testing.T) {
	definitions := []Definition{
		{Name: "combined", From: "facts", Union: []string{"owners"}},
		{Name: "joined", From: "combined", Joins: []Join{{Source: "owners", Type: "left",
			On: []JoinKey{{Left: "owner", Right: "id"}}, Fields: []SelectedField{{Field: "value", As: "owner-value"}}}}},
	}
	plan, err := CompileSQL(definitions, []string{"joined"}, sqlTestResolver)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan.CTEs, "UNION ALL") || !strings.Contains(plan.CTEs, "LEFT JOIN") ||
		!strings.Contains(plan.CTEs, "trim(") || len(plan.JoinKeys) != 1 ||
		!strings.Contains(plan.JoinKeys[0], "HAVING count(*) > 1") {
		t.Fatalf("bounded join/ordered union contract is missing: %+v", plan)
	}
	if plan.Outputs["joined"].Columns["owner-value"].Kind != SQLNumber {
		t.Fatal("join must preserve the imported type")
	}
}

func TestSQLCompilationFailsClosed(t *testing.T) {
	for _, definition := range []Definition{
		{Name: "bad", From: "unregistered"},
		{Name: "bad", From: "facts", Compute: []ComputedField{{As: "a", Function: "literal", Args: []Argument{{Parameter: "unresolved"}}}}},
		{Name: "bad", From: "facts", Joins: []Join{{Source: "owners", On: []JoinKey{{Left: "id", Right: "id"}}, Fields: []SelectedField{{Field: "value"}}}}},
	} {
		if _, err := CompileSQL([]Definition{definition}, []string{"bad"}, sqlTestResolver); err == nil {
			t.Fatalf("invalid SQL query was admitted: %+v", definition)
		}
	}
}

func TestCompileSQLExplicitManyJoin(t *testing.T) {
	definitions := []Definition{{Name: "matrix", From: "facts", Joins: []Join{{
		Source: "owners", Type: "inner-many",
		On:     []JoinKey{{Left: "owner", Right: "owner"}},
		Fields: []SelectedField{{Field: "id", As: "matched-id"}},
	}}}}
	if err := Validate(definitions); err != nil {
		t.Fatal(err)
	}
	plan, err := CompileSQL(definitions, []string{"matrix"}, sqlTestResolver)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.JoinKeys) != 0 || !strings.Contains(plan.CTEs, "INNER JOIN") ||
		!strings.Contains(plan.CTEs, `row_number() OVER (ORDER BY l."__order",r."__order")`) {
		t.Fatalf("explicit expansion must preserve ordered matches without uniqueness guard: %s", plan.CTEs)
	}
}

func TestValidateRejectsPrediction(t *testing.T) {
	err := Validate([]Definition{{Name: "prediction", From: "facts", Predict: []json.RawMessage{json.RawMessage(`{}`)}}})
	if err == nil || !strings.Contains(err.Error(), "prediction") {
		t.Fatalf("prediction query validation error = %v", err)
	}
}

func TestDependenciesRejectCycles(t *testing.T) {
	definitions := []Definition{
		{Name: "first", From: "second"},
		{Name: "second", From: "first"},
	}
	if _, err := Dependencies(definitions, []string{"first"}); err == nil {
		t.Fatal("cyclic query dependency graph was accepted")
	}
}

func TestSQLCompilationTreatsUndeclaredFieldsAsMissing(t *testing.T) {
	hostile := `id"; DROP TABLE facts; --`
	plan, err := CompileSQL([]Definition{{Name: "missing", From: "facts", Select: []SelectedField{{Field: hostile}}}},
		[]string{"missing"}, sqlTestResolver)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(plan.CTEs, "DROP TABLE") || !strings.Contains(plan.CTEs, "NULL::text") {
		t.Fatalf("undeclared field must compile to a NULL column: %s", plan.CTEs)
	}
}

func TestComputeReferencesBatchField(t *testing.T) {
	field := "double"
	other := "owner"
	computed := ComputedField{As: "triple", Function: "product", Args: []Argument{{Field: &field}, {Value: 3}}}

	if computeReferencesBatchField(computed, map[string]bool{}) {
		t.Fatal("expected no batch reference for an empty batch")
	}
	if computeReferencesBatchField(computed, map[string]bool{other: true}) {
		t.Fatal("expected no batch reference when the batch holds an unrelated field")
	}
	if !computeReferencesBatchField(computed, map[string]bool{field: true}) {
		t.Fatal("expected a batch reference when the batch holds the argument field")
	}

	literalOnly := ComputedField{As: "constant", Function: "literal", Args: []Argument{{Value: 1}}}
	if computeReferencesBatchField(literalOnly, map[string]bool{field: true}) {
		t.Fatal("a literal-only argument must never report a batch reference")
	}
}

func TestCompileSQLSplitsComputeBatchOnSelfReference(t *testing.T) {
	valueField := "value"
	doubleField := "double"
	definitions := []Definition{
		{Name: "chained", From: "facts",
			Compute: []ComputedField{
				{As: "double", Function: "product", Args: []Argument{{Field: &valueField}, {Value: 2}}},
				{As: "quadruple", Function: "product", Args: []Argument{{Field: &doubleField}, {Value: 2}}},
			}},
	}
	plan, err := CompileSQL(definitions, []string{"chained"}, sqlTestResolver)
	if err != nil {
		t.Fatal(err)
	}
	computeOutputs := 0
	for _, step := range plan.Steps {
		if step.Operation == "compute-output" {
			computeOutputs++
		}
	}
	if computeOutputs == 0 {
		t.Fatalf("expected the compute batch to materialize once the second compute read the first's output: %+v", plan.Steps)
	}
}

func TestSQLBindsNULStringsAsNonMatchingText(t *testing.T) {
	compiler := &sqlCompiler{}
	if placeholder := compiler.bind("a\x00b"); placeholder != "$1" {
		t.Fatalf("placeholder = %q", placeholder)
	}
	if got := compiler.args[0]; got != "a\uFFFDb" {
		t.Fatalf("bound value = %q", got)
	}
}

func TestSQLWindowsMaterializeAndChargeEachEntry(t *testing.T) {
	frame := 3
	definition := Definition{Name: "trend", From: "facts",
		Window: []WindowField{
			{Operation: "rolling", Field: "value", As: "average", Frame: &frame, Alignment: "centered",
				OrderBy: []OrderField{{Field: "id"}}, GroupBy: []string{"owner"}},
			{Operation: "change", Field: "average", As: "delta", Mode: "percentage",
				OrderBy: []OrderField{{Field: "id"}}},
		},
		Select: []SelectedField{{Field: "delta"}}}
	plan, err := CompileSQL([]Definition{definition}, []string{"trend"}, sqlTestResolver)
	if err != nil {
		t.Fatal(err)
	}
	var weights []int
	for _, step := range plan.Steps {
		if step.Operation == "window" {
			weights = append(weights, step.Weight)
		}
	}
	if len(weights) != 2 || weights[0] != 35 || weights[1] != 33 ||
		!strings.Contains(plan.CTEs, "PRECEDING AND 1 FOLLOWING") ||
		!strings.Contains(plan.CTEs, "lag(") ||
		plan.Outputs["trend"].Columns["delta"].Kind != SQLNumber {
		t.Fatalf("window materialization/charge: weights=%v plan=%+v", weights, plan)
	}
}

func TestSQLWindowRejectsStructuredFields(t *testing.T) {
	frame := 2
	resolver := func(string) (SQLRelation, error) {
		return SQLRelation{SQL: "(VALUES (1, '{}'::jsonb)) AS facts(ordinal, payload)", Order: "ordinal",
			Columns: map[string]SQLColumn{"payload": {Expression: "payload", Presence: "TRUE", Kind: SQLStructured}}}, nil
	}

	for _, field := range []string{"value", "group", "order"} {
		entry := WindowField{Operation: "rolling", Field: "missing", As: "result", Frame: &frame,
			OrderBy: []OrderField{{Field: "missing"}}}
		switch field {
		case "value":
			entry.Field = "payload"
		case "group":
			entry.GroupBy = []string{"payload"}
		case "order":
			entry.OrderBy[0].Field = "payload"
		}
		if _, err := CompileSQL([]Definition{{Name: "trend", From: "facts", Window: []WindowField{entry}}},
			[]string{"trend"}, resolver); err == nil {
			t.Fatalf("structured window %s was accepted", field)
		}
	}
}

func TestSQLWindowMissingFieldsCannotInjectSQL(t *testing.T) {
	hostile := `value"); DROP TABLE facts; --`
	frame := 1
	definition := Definition{Name: "window", From: "facts", Window: []WindowField{
		{Operation: "rolling", Field: hostile, As: hostile, Frame: &frame,
			OrderBy: []OrderField{{Field: hostile}}, GroupBy: []string{hostile}},
	}}
	plan, err := CompileSQL([]Definition{definition}, []string{"window"}, sqlTestResolver)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(plan.CTEs, "DROP TABLE") || plan.Outputs["window"].Columns[hostile].Kind != SQLNumber {
		t.Fatalf("window field escaped the declared schema: %+v", plan)
	}
}
