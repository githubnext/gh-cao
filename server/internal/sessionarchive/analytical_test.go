package sessionarchive

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestDuckDBAnalyticalCorpusIntegration(t *testing.T) {
	content, err := os.ReadFile("testdata/analytical.json")
	if err != nil {
		t.Fatal(err)
	}
	var corpus struct {
		Sessions []struct {
			RunID    string   `json:"runId"`
			Attempt  int64    `json:"attempt"`
			EventIDs []string `json:"eventIds"`
		}
		Cases []struct {
			Name    string
			Clauses json.RawMessage
			Rows    []map[string]any
			Limited bool
		}
	}
	decoder := json.NewDecoder(strings.NewReader(string(content)))
	decoder.UseNumber()
	if err := decoder.Decode(&corpus); err != nil {
		t.Fatal(err)
	}
	c := testConfig(t)
	a := openTestArchive(t, c, nil)
	for _, session := range corpus.Sessions {
		var raw strings.Builder
		for i, event := range []map[string]any{
			{"type": "session.format", "data": map[string]any{"version": 1}},
			{"type": "session.collection", "data": map[string]any{"complete": true}},
			{"type": "user.message", "data": map[string]any{"content": "approved"}},
		} {
			event["id"] = session.EventIDs[i]
			encoded, err := json.Marshal(event)
			if err != nil {
				t.Fatal(err)
			}
			raw.Write(encoded)
			raw.WriteByte('\n')
		}
		p, err := Project(c.traceConfig(), c.Namespace, "octo/api", session.RunID, "2026-10-25", session.Attempt, []byte(raw.String()), time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
		for i := range p.Events {
			p.Events[i].Gaps = []string{"synthetic-omission"}
		}
		if err := a.Admit(t.Context(), p); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	snapshot := pinTestArchive(t, a)
	for _, test := range corpus.Cases {
		t.Run(test.Name, func(t *testing.T) {
			definition := analyticalDefinition(t, strings.TrimSuffix(strings.TrimPrefix(string(test.Clauses), "{"), "}"))
			result, err := snapshot.Analytical(t.Context(), definition)
			if err != nil || !reflect.DeepEqual(result.Rows, test.Rows) || result.Metadata.Limited != test.Limited ||
				result.Metadata.TracesWithGaps != 2 || result.Metadata.UpstreamCoverage != "unknown" {
				t.Fatalf("shared analytical corpus mismatch: %#v %v; expected %#v", result, err, test.Rows)
			}
		})
	}
}

func TestAnalyticalPlanCapabilities(t *testing.T) {
	definition := analyticalDefinition(t, `"aggregate":{"values":[{"field":"RunId","as":"traces","reducer":"distinct-count"}]}`)
	plan, err := definition.Plan()
	if err != nil {
		t.Fatal(err)
	}
	capabilities := AnalyticalCapabilities(4096)
	capabilities.Reducers = []string{"count"}
	if !errors.Is(plan.Require(capabilities), ErrQuery) {
		t.Fatal("missing exact-distinct capability was silently emulated")
	}
	plan.Limit, plan.ExplicitLimit = 4097, true
	if !errors.Is(plan.Require(AnalyticalCapabilities(4096)), ErrLimit) {
		t.Fatal("explicit backend row budget silently lowered")
	}
	plan.ExplicitLimit = false
	if err := plan.Require(AnalyticalCapabilities(4096)); err != nil || plan.Limit != 4096 {
		t.Fatalf("implicit complete-result backend budget lost: %#v %v", plan, err)
	}
}

func TestAnalyticalScalarValidation(t *testing.T) {
	base := `"aggregate":{"values":[{"field":"InputTokens","as":"n","reducer":"sum"}]}`
	for _, expression := range []string{
		`{"as":"score","function":"coalesce","args":[{"field":"n"},{"value":null,"type":"number"}]}`,
		`{"as":"score","function":"quotient","args":[{"field":"n"},{"value":0}]}`,
	} {
		definition := analyticalDefinition(t, base+`,"post-aggregate":[`+expression+`]`)
		plan, err := definition.Plan()
		if err != nil {
			t.Fatal(err)
		}
		capabilities := AnalyticalCapabilities(4096)
		capabilities.Functions = nil
		if !errors.Is(plan.Require(capabilities), ErrQuery) {
			t.Fatal("missing computation capability silently accepted")
		}
	}
	for _, clauses := range []string{
		base + `,"post-aggregate":[{"as":"score","function":"coalesce","args":[{"field":"DurationMs"},{"value":1}]}]`,
		base + `,"post-aggregate":[{"as":"score","function":"coalesce","args":[{"field":"later"},{"value":1}]},{"as":"later","function":"sum","args":[{"field":"n"},{"value":1}]}]`,
		base + `,"post-aggregate":[{"as":"score","function":"if","args":[{"field":"n"},{"value":1},{"value":0}]}]`,
		base + `,"post-aggregate":[{"as":"score","function":"coalesce","args":[{"field":"n","value":1},{"value":0}]}]`,
		base + `,"post-aggregate":[{"as":"score","function":"coalesce","args":[{"field":"" ,"value":1},{"value":0}]}]`,
		base + `,"post-aggregate":[{"as":"score","function":"coalesce","args":[{"field":"n"},{"value":null}]}]`,
		base + `,"post-aggregate":[{"as":"score","function":"coalesce","args":[{"field":"n"},{"value":null,"type":"string"}]}]`,
		base + `,"post-aggregate":[{"as":"score","function":"coalesce","args":[{"field":"n"},null]}]`,
		base + `,"post-aggregate":[{"as":"score","function":"coalesce","args":[{"field":"n"},{"value":0,"payload":"$.x"}]}]`,
		`"aggregate":{"values":[{"field":"InputTokens","as":"n","reducer":"count","filter":null}]}`,
		`"aggregate":{"values":[{"field":"InputTokens","as":"n","reducer":"first","order-by":[{"field":"Timestamp"}]}]}`,
		`"aggregate":{"values":[{"field":"DurationMs","as":"n","reducer":"sum"}]}`,
		`"filter":{"predicates":[{"field":"PayloadJSON","state":"present"}]},"select":[{"field":"Sequence"}]`,
		`"filter":{"predicates":[{"field":"Sequence","state":"valid"}]},"select":[{"field":"Sequence"}]`,
		`"filter":{"predicates":[{"field":"InputTokens","state":"valid","equals":1}]},"select":[{"field":"Sequence"}]`,
	} {
		var definition Definition
		content := `{"name":"invalid","subject":"Invalid analytical operation.","from":"archive-events",` + clauses + `}`
		if err := json.Unmarshal([]byte(content), &definition); err == nil && definition.Validate() == nil {
			t.Fatalf("invalid scalar operation accepted: %s", clauses)
		}
	}
}

func TestDuckDBScalarStatesIntegration(t *testing.T) {
	c := testConfig(t)
	content, err := os.ReadFile("testdata/evidence-policy.json")
	if err != nil || decode(content, &c.Policy) != nil {
		t.Fatal("missing evidence policy")
	}
	c.Policy.RedactPatterns = append(c.Policy.RedactPatterns, "token-secret")
	a := openTestArchive(t, c, nil)
	raw := usageEvent("firewall.token_usage", `null`, `1`, `0`) +
		usageEvent("firewall.token_usage", `1.5`, `1`, `0`) +
		usageEvent("firewall.token_usage", `"token-secret"`, `1`, `0`) +
		usageEvent("firewall.token_usage", `0`, `1`, `0`) +
		`{"type":"firewall.token_usage","provenance":{"phase":"agent"},"data":{"usage":{"outputTokens":1},"durationMs":0}}` + "\n"
	p, err := Project(c.traceConfig(), c.Namespace, "octo/api", "123", "2026-11-15", 1, []byte(raw), time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Admit(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	if err := a.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	s := pinTestArchive(t, a)
	for state, want := range map[string]int{"present": 4, "absent": 1, "null": 1, "invalid": 1, "redacted": 1, "excluded": 0, "valid": 1} {
		definition := analyticalDefinition(t, `"filter":{"predicates":[{"field":"InputTokens","state":"`+state+`"}]},"aggregate":{"values":[{"field":"Sequence","as":"n","reducer":"count"}]}`)
		out, err := s.Analytical(t.Context(), definition)
		if err != nil || out.Rows[0]["n"] != json.Number(strconv.Itoa(want)) {
			t.Fatalf("%s field state differs: %#v %v", state, out, err)
		}
	}
	definition := analyticalDefinition(t, `"aggregate":{"by":["Namespace","Repository","RunId","RunAttempt","TraceId","ProjectionId"],"values":[{"field":"InputTokens","as":"first","reducer":"first","order-by":[{"field":"Sequence"}]}]}`)
	out, err := s.Analytical(t.Context(), definition)
	if err != nil || out.Rows[0]["first"] != nil {
		t.Fatalf("null first was skipped: %#v %v", out, err)
	}
}

func TestDuckDBUsageCorrectionIntegration(t *testing.T) {
	c := testConfig(t)
	policy, err := os.ReadFile("testdata/evidence-policy.json")
	if err != nil || decode(policy, &c.Policy) != nil {
		t.Fatal("missing evidence policy")
	}
	a := openTestArchive(t, c, nil)
	admit := func(raw string) {
		p, err := Project(c.traceConfig(), c.Namespace, "octo/api", "123", "2026-11-15", 1, []byte(raw), time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
		if err := a.Admit(t.Context(), p); err != nil {
			t.Fatal(err)
		}
		if err := a.Flush(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	admit(usageEvent("usage.report", `10`, `5`, `1`))
	old := pinTestArchive(t, a)
	admit(usageEvent("firewall.token_usage", `100`, `20`, `0.5`) + usageEvent("firewall.token_usage", `200`, `30`, `1.25`))
	current := pinTestArchive(t, a)
	for _, name := range []string{"execution-step-count", "execution-duration", "working-set-rebuild-factor", "context-growth"} {
		// #nosec G304 -- Fixed reviewed grader names, never request input.
		content, err := os.ReadFile("testdata/graders/" + name + ".json")
		if err != nil {
			t.Fatal(err)
		}
		var definition Definition
		if err := json.Unmarshal(content, &definition); err != nil {
			t.Fatal(err)
		}
		for _, s := range []*Snapshot{old, current} {
			out, err := s.Analytical(t.Context(), definition)
			if err != nil || len(out.Rows) != 1 {
				t.Fatalf("correction merged projections: %#v %v", out, err)
			}
			wantFamily := "usage.report"
			if s == current {
				wantFamily = "firewall.token_usage"
			}
			if out.Rows[0]["family"] != wantFamily {
				t.Fatalf("incorrect active/pinned family: %#v", out)
			}
		}
	}
	if err := a.Withdraw(t.Context(), "octo/api", "123", 1); err != nil {
		t.Fatal(err)
	}
	for _, s := range []*Snapshot{old, current} {
		if !errors.Is(s.Deliver(func() error { t.Fatal("withdrawn scalar result delivered"); return nil }), ErrWithdrawn) {
			t.Fatal("scalar delivery bypassed withdrawal")
		}
	}
}

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
		plan, err := definition.Plan()
		if err != nil || slices.Contains(plan.Inputs, "PayloadJSON") || slices.Contains(plan.Inputs, "Gaps") {
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
"aggregate":{"values":[{"field":"Type","as":"events","reducer":"sum"}]}}`,
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

func itemDefinition(t *testing.T, source, clauses string) Definition {
	t.Helper()
	var definition Definition
	content := `{"name":"test-items","subject":"Inspect approved trajectory items.","from":"` + source + `",` + clauses + `}`
	if err := json.Unmarshal([]byte(content), &definition); err != nil {
		t.Fatal(err)
	}
	return definition
}

func TestAnalyticalItemSourcePlans(t *testing.T) {
	plan, err := itemDefinition(t, "archive-objectives", `"aggregate":{"values":[{"field":"SatisfiedAtEventIndex","as":"n","reducer":"count","filter":{"predicates":[{"field":"SatisfiedAtEventIndex","state":"invalid"}]}}]},`+
		`"post-aggregate":[{"as":"gap","function":"difference","args":[{"field":"n"},{"value":1}]},{"as":"area","function":"product","args":[{"field":"gap"},{"value":2}]}]`).Plan()
	if err != nil || plan.Source != "archive-objectives" || len(plan.Predicates) != 0 {
		t.Fatalf("item source plan rejected: %#v %v", plan, err)
	}
	if plan.Values[0].Predicates[0].Path != "data.objectives" {
		t.Fatalf("item field state not qualified by its collection: %#v", plan.Values[0].Predicates)
	}
	capabilities := AnalyticalCapabilities(4096)
	capabilities.Sources = nil
	if !errors.Is(plan.Require(capabilities), ErrQuery) {
		t.Fatal("a backend without the item source silently accepted it")
	}
	if err := plan.Require(AnalyticalCapabilities(4096)); err != nil {
		t.Fatalf("item source capability rejected: %v", err)
	}
	for _, definition := range []Definition{
		itemDefinition(t, "archive-objectives", `"select":[{"field":"Type"}]`),
		itemDefinition(t, "archive-objectives", `"select":[{"field":"ValidAtIssueTime"}]`),
		itemDefinition(t, "archive-states", `"select":[{"field":"PayloadJSON"}]`),
		itemDefinition(t, "archive-states", `"aggregate":{"values":[{"field":"Id","as":"first","reducer":"first","order-by":[{"field":"Ordinal"}]}]}`),
		itemDefinition(t, "archive-actions", `"filter":{"predicates":[{"field":"Kind","state":"valid"}]},"select":[{"field":"Kind"}]`),
		itemDefinition(t, "archive-tool-calls", `"select":[{"field":"Ordinal"}]`),
	} {
		if _, err := definition.Plan(); !errors.Is(err, ErrQuery) {
			t.Fatalf("invalid item-source plan accepted: %v", err)
		}
	}
}

func TestDuckDBAnalyticalItemSourceIntegration(t *testing.T) {
	c := testConfig(t)
	policy, err := os.ReadFile("testdata/evidence-policy.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := decode(policy, &c.Policy); err != nil {
		t.Fatal(err)
	}
	a := openTestArchive(t, c, nil)
	now := time.Now().UTC()
	ir := func(phase, data string) string {
		return `{"type":"trajectory.ir","provenance":{"component":"agent","phase":"` + phase + `"},"data":` + data + "}\n"
	}
	header := "{\"type\":\"session.format\",\"provenance\":{},\"data\":{\"version\":1}}\n"
	raw := header + ir("agent", `{"objectives":[{"satisfiedAtEventIndex":9}]}`) +
		ir("agent", `{"objectives":[{"satisfiedAtEventIndex":0},{"satisfiedAtEventIndex":null},null,"s",{"satisfiedAtEventIndex":1e3},{"satisfiedAtEventIndex":0.5},{"satisfiedAtEventIndex":true},{"satisfiedAtEventIndex":"1"},{}],"states":["a","",{"id":"a"},{"id":""},{"id":1},7]}`) +
		ir("detection", `{"objectives":[]}`)
	for run, content := range map[string]string{"1": raw, "2": header} {
		p, err := Project(c.traceConfig(), c.Namespace, "octo/api", run, now.Format(time.DateOnly), 1, []byte(content), now)
		if err != nil {
			t.Fatal(err)
		}
		if err := a.Admit(t.Context(), p); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	s := pinTestArchive(t, a)
	out, err := s.Analytical(t.Context(), itemDefinition(t, "archive-objectives", `"select":[{"field":"RunId"},{"field":"Sequence"},{"field":"Ordinal"},{"field":"Items"},{"field":"Kind"},{"field":"SatisfiedAtEventIndex"}]`))
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(out.Rows))
	for _, row := range out.Rows {
		got = append(got, fmt.Sprint(row["RunId"], "/", row["Sequence"], "/", row["Ordinal"], "/", row["Items"], "/", row["Kind"], "/", row["SatisfiedAtEventIndex"]))
	}
	want := []string{"1/2/0/9/object/0", "1/2/1/9/object/<nil>", "1/2/2/9/null/<nil>", "1/2/3/9/string/<nil>", "1/2/4/9/object/1000",
		"1/2/5/9/object/<nil>", "1/2/6/9/object/<nil>", "1/2/7/9/object/<nil>", "1/2/8/9/object/<nil>", "2/<nil>/<nil>/<nil>//<nil>"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("last agent-phase IR not flattened with complete trace anchors:\n%q\n%q", got, want)
	}
	states := map[string]string{"invalid": "5,6,7", "null": "1", "valid": "0,4", "present": "0,1,4,5,6,7"}
	for state, ordinals := range states {
		out, err := s.Analytical(t.Context(), itemDefinition(t, "archive-objectives", `"filter":{"predicates":[{"field":"SatisfiedAtEventIndex","state":"`+state+`"}]},"select":[{"field":"Ordinal"}]`))
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, row := range out.Rows {
			got = append(got, fmt.Sprint(row["Ordinal"]))
		}
		if strings.Join(got, ",") != ordinals {
			t.Fatalf("%s item field state = %v, want %s", state, got, ordinals)
		}
	}
	out, err = s.Analytical(t.Context(), itemDefinition(t, "archive-states", `"filter":{"predicates":[{"field":"RunId","equals":"1"}]},"select":[{"field":"Kind"},{"field":"Id"}]`))
	if err != nil {
		t.Fatal(err)
	}
	got = make([]string, 0, len(out.Rows))
	for _, row := range out.Rows {
		got = append(got, fmt.Sprint(row["Kind"], ":", row["Id"]))
	}
	if strings.Join(got, ",") != "string:a,string:,object:a,object:,object:,number:" {
		t.Fatalf("state visit identities changed: %v", got)
	}
}

const chainBinding = `"Namespace","Repository","RunId","RunAttempt","TraceId","ProjectionId"`

func chainedDefinition(t *testing.T, stages, clauses string) Definition {
	t.Helper()
	var definition Definition
	content := `{"name":"test-chain","subject":"Aggregate approved items in two stages.","queries":[` + stages + `],` + clauses + `}`
	if err := json.Unmarshal([]byte(content), &definition); err != nil {
		t.Fatal(err)
	}
	return definition
}

const kindStage = `{"name":"per-kind","subject":"S.","from":"archive-states","aggregate":{"by":[` + chainBinding + `,"Kind"],"values":[{"field":"Ordinal","as":"n","reducer":"count"}]},` +
	`"post-aggregate":[{"as":"half","function":"quotient","args":[{"field":"n"},{"value":2}]}]}`

const traceAggregate = `"from":"per-kind","aggregate":{"by":[` + chainBinding + `],"values":[{"field":"n","as":"visits","reducer":"sum"},` +
	`{"field":"Kind","as":"kinds","reducer":"count"},{"field":"n","as":"most","reducer":"max"},` +
	`{"field":"half","as":"halves","reducer":"sum","order-by":[{"field":"Kind"}]}]},"select":[{"field":"RunId"},{"field":"visits"},{"field":"kinds"},{"field":"most"},{"field":"halves"}]`

func TestAnalyticalChainedPlans(t *testing.T) {
	plan, err := chainedDefinition(t, kindStage, traceAggregate).Plan()
	if err != nil || plan.Stages() != 1 || plan.Base().Source != "archive-states" || plan.Input.Columns[len(plan.Input.Columns)-1].Type != "number" {
		t.Fatalf("chained plan rejected: %#v %v", plan, err)
	}
	capabilities := AnalyticalCapabilities(4096)
	capabilities.Chaining = 0
	if !errors.Is(plan.Require(capabilities), ErrQuery) {
		t.Fatal("a backend without chaining silently accepted a chained query")
	}
	if err := plan.Require(AnalyticalCapabilities(4096)); err != nil {
		t.Fatalf("chaining capability rejected: %v", err)
	}
	events := `{"name":"per-trace","subject":"S.","from":"archive-events","aggregate":{"by":[` + chainBinding + `],"values":[{"field":"Sequence","as":"n","reducer":"count"}]}}`
	if _, err := chainedDefinition(t, events, `"from":"per-trace","select":[{"field":"TraceId"},{"field":"n"}]`).Plan(); err != nil {
		t.Fatalf("selection over an earlier stage rejected: %v", err)
	}
	stage := func(name, from, rest string) string {
		return `{"name":"` + name + `","subject":"S.","from":"` + from + `","aggregate":{"by":[` + chainBinding + `],"values":[{"field":"` + rest + `","as":"n","reducer":"count"}]}}`
	}
	for name, test := range map[string]struct{ stages, clauses string }{
		"unknown":       {kindStage, `"from":"missing","select":[{"field":"n"}]`},
		"skips stage":   {stage("a", "archive-events", "Sequence") + "," + stage("b", "a", "n"), `"from":"a","select":[{"field":"n"}]`},
		"forward read":  {stage("a", "b", "n") + "," + stage("b", "archive-events", "Sequence"), `"from":"b","select":[{"field":"n"}]`},
		"self read":     {stage("a", "a", "n"), `"from":"a","select":[{"field":"n"}]`},
		"duplicate":     {stage("a", "archive-events", "Sequence") + "," + stage("a", "a", "n"), `"from":"a","select":[{"field":"n"}]`},
		"outer name":    {stage("test-chain", "archive-events", "Sequence"), `"from":"test-chain","select":[{"field":"n"}]`},
		"shadow source": {stage("archive-states", "archive-events", "Sequence"), `"from":"archive-states","select":[{"field":"n"}]`},
		"too deep": {stage("a", "archive-events", "Sequence") + "," + stage("b", "a", "n") + "," + stage("c", "b", "n"),
			`"from":"c","select":[{"field":"n"}]`},
		"unbound stage":    {`{"name":"a","subject":"S.","from":"archive-events","aggregate":{"by":["TraceId"],"values":[{"field":"Sequence","as":"n","reducer":"count"}]}}`, `"from":"a","select":[{"field":"n"}]`},
		"row stage":        {`{"name":"a","subject":"S.","from":"archive-events","select":[{"field":"Sequence"}]}`, `"from":"a","select":[{"field":"Sequence"}]`},
		"stage limit":      {`{"name":"a","subject":"S.","from":"archive-events","aggregate":{"by":[` + chainBinding + `],"values":[{"field":"Sequence","as":"n","reducer":"count"}]},"limit":1}`, `"from":"a","select":[{"field":"n"}]`},
		"nested":           {`{"name":"a","subject":"S.","queries":[` + stage("b", "archive-events", "Sequence") + `],"from":"b","aggregate":{"by":[` + chainBinding + `],"values":[{"field":"n","as":"m","reducer":"count"}]}}`, `"from":"a","select":[{"field":"m"}]`},
		"unbound outer":    {kindStage, `"from":"per-kind","aggregate":{"by":["TraceId"],"values":[{"field":"n","as":"m","reducer":"sum"}]}`},
		"state predicate":  {kindStage, `"from":"per-kind","filter":{"predicates":[{"field":"n","state":"valid"}]},"select":[{"field":"n"}]`},
		"first":            {kindStage, `"from":"per-kind","aggregate":{"by":[` + chainBinding + `],"values":[{"field":"n","as":"m","reducer":"first","order-by":[{"field":"Kind"}]}]}`},
		"unordered sum":    {kindStage, `"from":"per-kind","aggregate":{"by":[` + chainBinding + `],"values":[{"field":"half","as":"m","reducer":"sum"}]}`},
		"wrong order":      {kindStage, `"from":"per-kind","aggregate":{"by":[` + chainBinding + `],"values":[{"field":"half","as":"m","reducer":"sum","order-by":[{"field":"Kind","direction":"desc"}]}]}`},
		"hidden source":    {kindStage, `"from":"per-kind","select":[{"field":"Ordinal"}]`},
		"base source read": {kindStage, `"from":"archive-states","select":[{"field":"Kind"}]`},
	} {
		if _, err := chainedDefinition(t, test.stages, test.clauses).Plan(); !errors.Is(err, ErrQuery) {
			t.Fatalf("%s: invalid chained plan accepted: %v", name, err)
		}
	}
}

func TestDuckDBAnalyticalChainingIntegration(t *testing.T) {
	c := testConfig(t)
	policy, err := os.ReadFile("testdata/evidence-policy.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := decode(policy, &c.Policy); err != nil {
		t.Fatal(err)
	}
	a := openTestArchive(t, c, nil)
	now := time.Now().UTC()
	header := "{\"type\":\"session.format\",\"provenance\":{},\"data\":{\"version\":1}}\n"
	raw := header + `{"type":"trajectory.ir","provenance":{"component":"agent","phase":"agent"},"data":{"states":["a","",{"id":"a"},{"id":""},{"id":1},7]}}` + "\n"
	for run, content := range map[string]string{"1": raw, "2": header} {
		p, err := Project(c.traceConfig(), c.Namespace, "octo/api", run, now.Format(time.DateOnly), 1, []byte(content), now)
		if err != nil {
			t.Fatal(err)
		}
		if err := a.Admit(t.Context(), p); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	s := pinTestArchive(t, a)
	definition := chainedDefinition(t, kindStage, traceAggregate)
	out, err := s.Analytical(t.Context(), definition)
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(out.Rows))
	for _, row := range out.Rows {
		got = append(got, fmt.Sprint(row["RunId"], ":", row["visits"], "/", row["kinds"], "/", row["most"], "/", row["halves"]))
	}
	// Run 2's anchor row keeps its trace in both stages with zero visits.
	if strings.Join(got, ",") != "1:6.0/3/3/3.0,2:0.0/0/0/0.0" {
		t.Fatalf("two-stage aggregation lost trace identity or values: %v", got)
	}
	if out.Metadata.QueryDigest == analyticalDefinition(t, `"select":[{"field":"Sequence"}]`).Metadata(false, 0, 0).QueryDigest {
		t.Fatal("query digest ignored earlier stages")
	}
}
