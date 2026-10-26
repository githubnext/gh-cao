package sessionarchive

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

func analyticalDefinition(t *testing.T, clauses string) Definition {
	t.Helper()
	var definition Definition
	content := `{"name":"test-events","subject":"Inspect retained approved events.","from":"archive-events",` + clauses + `}`
	if err := json.Unmarshal([]byte(content), &definition); err != nil {
		t.Fatal(err)
	}
	if err := definition.Validate(); err != nil {
		t.Fatal(err)
	}
	return definition
}

func TestSessionArchiveAnalyticalValidation(t *testing.T) {
	for _, clauses := range []string{
		`"select":[{"field":"Sequence","as":"sequence"},{"field":"Type"}]`,
		`"aggregate":{"values":[{"field":"Sequence","as":"events","reducer":"count"}]}`,
		`"filter":{"predicates":[{"field":"RunAttempt","gte":1,"lt":3},{"field":"Type","in":["session.format","session.collection"]}]},"select":[{"field":"Type"}]`,
		`"filter":{"predicates":[{"field":"RunAttempt","equals":9007199254740993}]},"select":[{"field":"RunAttempt"}]`,
	} {
		definition := analyticalDefinition(t, clauses)
		plan, err := definition.compile()
		if err != nil || slices.Contains(plan.inputs, "PayloadJSON") || slices.Contains(plan.inputs, "Gaps") {
			t.Fatalf("unbounded or content-reading plan: %#v %v", plan, err)
		}
	}
	valid := `{"name":"test","subject":"Inspect events.","from":"archive-events","select":[{"field":"Sequence"}]}`
	for name, content := range map[string]string{
		"source":       strings.Replace(valid, `"archive-events"`, `"read_parquet('/etc/passwd')"`, 1),
		"unknown-key":  strings.Replace(valid, `"select":`, `"sql":"SELECT 1","select":`, 1),
		"payload":      strings.Replace(valid, `"Sequence"`, `"PayloadJSON"`, 1),
		"array":        strings.Replace(valid, `"Sequence"`, `"Gaps"`, 1),
		"field":        strings.Replace(valid, `"Sequence"`, `"Sequence); DROP TABLE active; --"`, 1),
		"alias":        strings.Replace(valid, `"Sequence"`, `"Sequence","as":"value; SELECT 1"`, 1),
		"null":         strings.Replace(valid, `"select":`, `"filter":null,"select":`, 1),
		"empty-select": strings.Replace(valid, `[{"field":"Sequence"}]`, `[]`, 1),
		"no-select":    `{"name":"test","subject":"Inspect events.","from":"archive-events"}`,
		"subject":      strings.Replace(valid, `"Inspect events."`, `""`, 1),
		"annotations":  strings.Replace(valid, `"Inspect events."`, `"`+strings.Repeat("x", 513)+`"`, 1),
		"limit-zero":   strings.Replace(valid, `"select":`, `"limit":0,"select":`, 1),
		"limit-large":  strings.Replace(valid, `"select":`, `"limit":10001,"select":`, 1),
		"duplicate-output": strings.Replace(valid, `[{"field":"Sequence"}]`,
			`[{"field":"Sequence","as":"value"},{"field":"Type","as":"Value"}]`, 1),
		"missing-order-field": strings.Replace(valid, `"select":`,
			`"order-by":[{"field":"Type"}],"select":`, 1),
		"order-injection": strings.Replace(valid, `"select":`,
			`"order-by":[{"field":"Sequence","direction":"asc; SELECT 1"}],"select":`, 1),
		"unknown-select-key": strings.Replace(valid, `"Sequence"`, `"Sequence","expression":"1"`, 1),
		"reducer": `{"name":"test","subject":"Inspect events.","from":"archive-events",
"aggregate":{"values":[{"field":"Sequence","as":"events","reducer":"sum"}]}}`,
		"group-field": `{"name":"test","subject":"Inspect events.","from":"archive-events",
"aggregate":{"by":["PayloadJSON"],"values":[{"field":"Sequence","as":"events","reducer":"count"}]}}`,
		"group-duplicate": `{"name":"test","subject":"Inspect events.","from":"archive-events",
"aggregate":{"by":["Type","Type"],"values":[{"field":"Sequence","as":"events","reducer":"count"}]}}`,
		"aggregate-alias": `{"name":"test","subject":"Inspect events.","from":"archive-events",
"aggregate":{"values":[{"field":"Sequence","as":"Type","reducer":"count"}]}}`,
	} {
		t.Run(name, func(t *testing.T) {
			var definition Definition
			if err := json.Unmarshal([]byte(content), &definition); err == nil && definition.Validate() == nil {
				t.Fatal("unsupported query accepted")
			}
		})
	}
	for _, predicate := range []string{
		`{"field":"Type"}`,
		`{"field":"Type","equals":"x","in":["y"]}`,
		`{"field":"Type","equals":"x","gte":"y"}`,
		`{"field":"Type","equals":null}`,
		`{"field":"Type","equals":true}`,
		`{"field":"Type","equals":1}`,
		`{"field":"Type","equals":{"parameter":"value"}}`,
		`{"field":"Type","in":[]}`,
		`{"field":"Type","in":null}`,
		`{"field":"Type","in":["x",null]}`,
		`{"field":"RunAttempt","equals":"1"}`,
		`{"field":"RunAttempt","equals":1.5}`,
		`{"field":"RunAttempt","equals":9223372036854775808}`,
		`{"field":"RunAttempt","equals":1e0}`,
		`{"field":"Type","equals":"\u0000"}`,
		`{"field":"Type","includes":"x"}`,
		`{"field":"Type","in":[` + strings.Repeat(`"x",`, 32) + `"x"]}`,
	} {
		content := strings.Replace(valid, `"select":`, `"filter":{"predicates":[`+predicate+`]},"select":`, 1)
		var definition Definition
		if err := json.Unmarshal([]byte(content), &definition); err == nil && definition.Validate() == nil {
			t.Fatalf("unsupported predicate accepted: %s", predicate)
		}
	}
}

func TestDuckDBAnalyticalIntegration(t *testing.T) {
	c := testConfig(t)
	c.Repositories = append(c.Repositories, "octo/other")
	a := openTestArchive(t, c, nil)
	p, q := testProjection(t, c, "123", "original"), testProjection(t, c, "9007199254740993", "another")
	q.Gaps = []string{"source-partial"}
	for i := range q.Events {
		q.Events[i].Gaps = slices.Clone(q.Gaps)
	}
	other, err := Project(c.traceConfig(), c.Namespace, "octo/other", "999", p.Day, 1,
		[]byte("{\"type\":\"session.format\",\"data\":{\"version\":1}}\n"), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, projection := range []Projection{p, q, other} {
		if err := a.Admit(t.Context(), projection); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	old, err := a.Pin(t.Context(), Scope{c.Namespace, []string{"octo/api"}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = old.Close() }()
	if err := a.Admit(t.Context(), testProjection(t, c, "123", "corrected")); err != nil {
		t.Fatal(err)
	}
	if err := a.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	snapshot, err := a.Pin(t.Context(), Scope{c.Namespace, []string{"octo/api"}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = snapshot.Close() }()
	counts := analyticalDefinition(t, `"aggregate":{"by":["Type"],"values":[{"field":"Sequence","as":"events","reducer":"count"}]},
"order-by":[{"field":"events","direction":"desc"},{"field":"Type"}]`)
	result, err := snapshot.Analytical(t.Context(), counts)
	if err != nil || len(result.Rows) != 4 {
		t.Fatalf("real analytical query: %#v %v", result, err)
	}
	for _, row := range result.Rows {
		if row["events"] != json.Number("2") {
			t.Fatalf("superseded or out-of-scope packed rows counted: %#v", result.Rows)
		}
	}
	wantGaps := 0
	for _, projection := range []Projection{p, q} {
		if len(projection.Gaps) > 0 {
			wantGaps++
		}
	}
	if result.Metadata.Limited || result.Metadata.UpstreamCoverage != "unknown" || result.Metadata.TracesWithGaps != wantGaps {
		t.Fatalf("coverage lost or invented: %#v", result.Metadata)
	}
	if !reflect.DeepEqual(result.Columns, []Column{{"Type", "string"}, {"events", "integer"}}) {
		t.Fatalf("static result schema changed: %#v", result.Columns)
	}
	top := counts
	top.Limit = new(2)
	topResult, err := snapshot.Analytical(t.Context(), top)
	if err != nil || !topResult.Metadata.Limited || !reflect.DeepEqual(topResult.Rows, result.Rows[:2]) {
		t.Fatalf("top-N ordering or limit disclosure changed: %#v %v", topResult, err)
	}
	for _, test := range []struct {
		name, clauses string
		rows          int
	}{
		{"typed-range", `"filter":{"predicates":[{"field":"Sequence","gte":1,"lt":3}]},
"select":[{"field":"RunId"},{"field":"Sequence"}],"order-by":[{"field":"Sequence","direction":"desc"}]`, 4},
		{"run-and-type", `"filter":{"predicates":[{"field":"RunId","equals":"9007199254740993"},{"field":"Type","in":["session.format","session.collection"]}]},
"select":[{"field":"RunId"},{"field":"Sequence"}]`, 2},
		{"unknown", `"filter":{"predicates":[{"field":"Type","equals":"missing"}]},"select":[{"field":"Type"}]`, 0},
		{"injection-is-data", `"filter":{"predicates":[{"field":"Type","equals":"x' OR true --"}]},"select":[{"field":"Type"}]`, 0},
		{"day-boundary", `"filter":{"predicates":[{"field":"CompletionDay","gte":"` + p.Day + `","lt":"` + p.Day + `"}]},"select":[{"field":"Type"}]`, 0},
		{"distinct", `"aggregate":{"values":[{"field":"RunId","as":"traces","reducer":"distinct-count"},{"field":"Timestamp","as":"timed","reducer":"count"}]}`, 1},
		{"aggregate-select", `"aggregate":{"by":["Type"],"values":[{"field":"Sequence","as":"events","reducer":"count"}]},
"select":[{"field":"events","as":"n"}]`, 4},
	} {
		t.Run(test.name, func(t *testing.T) {
			out, err := snapshot.Analytical(t.Context(), analyticalDefinition(t, test.clauses))
			if err != nil || len(out.Rows) != test.rows || out.Rows == nil {
				t.Fatalf("analytical selection: %#v %v", out, err)
			}
			if test.name == "distinct" && (out.Rows[0]["traces"] != json.Number("2") || out.Rows[0]["timed"] != json.Number("0")) {
				t.Fatalf("count grain or empty-string semantics lost: %#v", out.Rows)
			}
			if test.name == "run-and-type" && out.Rows[0]["RunId"] != "9007199254740993" {
				t.Fatal("GitHub run ID lost precision")
			}
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := snapshot.Analytical(ctx, counts); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation ignored: %v", err)
	}
	if err := a.Withdraw(t.Context(), "octo/api", "123", 1); err != nil {
		t.Fatal(err)
	}
	committed := false
	for _, pinned := range []*Snapshot{old, snapshot} {
		if err := pinned.Deliver(func() error { committed = true; return nil }); !errors.Is(err, ErrWithdrawn) || committed {
			t.Fatalf("withdrawn response committed: %v", err)
		}
		if _, err := pinned.Analytical(t.Context(), counts); !errors.Is(err, ErrWithdrawn) {
			t.Fatalf("withdrawn generation remained queryable: %v", err)
		}
	}
}

func TestDuckDBAnalyticalEmptyIntegration(t *testing.T) {
	a := openTestArchive(t, testConfig(t), nil)
	snapshot := pinTestArchive(t, a)
	for _, clauses := range []string{
		`"select":[{"field":"Sequence"}]`,
		`"aggregate":{"by":["Type"],"values":[{"field":"Sequence","as":"events","reducer":"count"}]}`,
		`"aggregate":{"values":[{"field":"Sequence","as":"events","reducer":"count"}]}`,
	} {
		out, err := snapshot.Analytical(t.Context(), analyticalDefinition(t, clauses))
		if err != nil || out.Rows == nil || out.Metadata.UpstreamCoverage != "unknown" {
			t.Fatalf("empty archive unavailable or falsely complete: %#v %v", out, err)
		}
		if strings.Contains(clauses, `"by"`) || strings.Contains(clauses, `"select"`) {
			if len(out.Rows) != 0 {
				t.Fatalf("empty rows fabricated: %#v", out)
			}
		} else if len(out.Rows) != 1 || out.Rows[0]["events"] != json.Number("0") {
			t.Fatalf("ungrouped empty count is not zero: %#v", out)
		}
	}
}

func TestDuckDBAnalyticalBudgetIntegration(t *testing.T) {
	c := testConfig(t)
	a := openTestArchive(t, c, nil)
	raw := "{\"type\":\"session.format\",\"data\":{\"version\":1}}\n" +
		strings.Repeat("{\"type\":\"user.message\",\"data\":{\"content\":\"small\"}}\n", 10000)
	p, err := Project(c.traceConfig(), c.Namespace, "octo/api", "123", "", 1, []byte(raw), time.Now())
	if err != nil || len(p.Events) != 10001 {
		t.Fatalf("fixed-cardinality fixture: %d %v", len(p.Events), err)
	}
	if err := a.Admit(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	if err := a.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	snapshot := pinTestArchive(t, a)
	definition := analyticalDefinition(t, `"select":[{"field":"Sequence"}]`)
	if _, err := snapshot.Analytical(t.Context(), definition); !errors.Is(err, ErrLimit) {
		t.Fatalf("10001 output rows silently truncated: %v", err)
	}
	definition.Limit = new(10000)
	out, err := snapshot.Analytical(t.Context(), definition)
	if err != nil || len(out.Rows) != 10000 || !out.Metadata.Limited || out.Rows[9999]["Sequence"] != json.Number("9999") {
		t.Fatalf("explicit 10000-row selection failed: %d %#v %v", len(out.Rows), out.Metadata, err)
	}
}
