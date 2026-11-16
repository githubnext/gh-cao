package sessionarchive

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The lowering evidence executes reference DuckDB lowerings of upstream
// trajectory graders against outcomes generated from the pinned upstream
// sources by scripts/generate-session-query-lowering-cases.mjs.

type loweringCases struct {
	UpstreamRepository string `json:"upstreamRepository"`
	UpstreamRevision   string `json:"upstreamRevision"`
	UpstreamPatterns   struct {
		Retries string `json:"retries"`
		Guard   string `json:"guard"`
	} `json:"upstreamPatterns"`
	Recurrence []recurrenceCase `json:"recurrence"`
	Keywords   []keywordCase    `json:"keywords"`
}

type recurrenceCase struct {
	ID       string                     `json:"id"`
	States   []json.RawMessage          `json:"states"`
	Sequence string                     `json:"sequence"`
	Events   []json.RawMessage          `json:"events"`
	Expected map[string]loweringOutcome `json:"expected"`
	Upstream map[string]loweringOutcome `json:"upstream"`
}

type loweringOutcome struct {
	Status string   `json:"status"`
	Value  *float64 `json:"value"`
}

type keywordCase struct {
	ID      string `json:"id"`
	Text    string `json:"text"`
	Retries bool   `json:"retries"`
	Guard   bool   `json:"guard"`
}

type recurrenceRow struct {
	Trace     string   `json:"trace"`
	RRStatus  string   `json:"rr_status"`
	RR        *float64 `json:"rr"`
	RQAStatus string   `json:"rqa_status"`
	DET       *float64 `json:"det"`
	LAM       *float64 `json:"lam"`
	TT        *float64 `json:"tt"`
}

func TestDuckDBLoweringEvidenceIntegration(t *testing.T) {
	converter := testConverter(t)
	cases := readLoweringCases(t)
	t.Run("recurrence", func(t *testing.T) {
		var rows []recurrenceRow
		if err := converter.query(t.Context(), recurrenceLoweringSQL(t, cases.Recurrence), &rows); err != nil {
			t.Fatal(err)
		}
		if len(rows) != len(cases.Recurrence) {
			t.Fatalf("lowering returned %d traces, want %d", len(rows), len(cases.Recurrence))
		}
		byTrace := map[string]recurrenceRow{}
		for _, row := range rows {
			byTrace[row.Trace] = row
		}
		divergences := 0
		for _, c := range cases.Recurrence {
			row, ok := byTrace[c.ID]
			if !ok {
				t.Errorf("%s: trace missing from lowering result", c.ID)
				continue
			}
			actual := map[string]loweringOutcome{
				"recurrence-rate":          {row.RRStatus, row.RR},
				"recurrence-determinism":   {row.RQAStatus, row.DET},
				"recurrence-laminarity":    {row.RQAStatus, row.LAM},
				"recurrence-trapping-time": {row.RQAStatus, row.TT},
			}
			if len(c.Expected) != len(actual) {
				t.Errorf("%s: fixture grades %d graders, lowering grades %d", c.ID, len(c.Expected), len(actual))
			}
			for grader, want := range c.Expected {
				if got := actual[grader]; !sameOutcome(got, want) {
					t.Errorf("%s %s: lowering %s, upstream-derived %s", c.ID, grader, formatOutcome(got), formatOutcome(want))
				}
			}
			if c.Upstream != nil {
				divergences++
				for grader, outcome := range c.Upstream {
					if c.Expected[grader].Status != "unavailable" || outcome.Status == "unavailable" {
						t.Errorf("%s %s: declared divergence must fail closed where upstream measures", c.ID, grader)
					}
				}
			}
		}
		if divergences != 3 {
			t.Errorf("declared S1 typing divergences = %d, want 3", divergences)
		}
	})
	t.Run("keywords", func(t *testing.T) {
		var rows []struct {
			ID      string `json:"id"`
			Retries bool   `json:"retries"`
			Guard   bool   `json:"guard"`
		}
		if err := converter.query(t.Context(), keywordLoweringSQL(t, cases.Keywords), &rows); err != nil {
			t.Fatal(err)
		}
		if len(rows) != len(cases.Keywords) {
			t.Fatalf("lowering returned %d texts, want %d", len(rows), len(cases.Keywords))
		}
		want := map[string]keywordCase{}
		for _, c := range cases.Keywords {
			want[c.ID] = c
		}
		for _, row := range rows {
			c := want[row.ID]
			if row.Retries != c.Retries || row.Guard != c.Guard {
				t.Errorf("%s %q: lowering retries=%t guard=%t, upstream retries=%t guard=%t", row.ID, c.Text, row.Retries, row.Guard, c.Retries, c.Guard)
			}
		}
	})
}

func readLoweringCases(t *testing.T) loweringCases {
	t.Helper()
	var cases loweringCases
	readJSONFile(t, filepath.Join("testdata", "lowering", "cases.json"), &cases)
	var pins struct {
		UpstreamRepository string `json:"upstreamRepository"`
		UpstreamRevision   string `json:"upstreamRevision"`
	}
	readJSONFile(t, filepath.Join("testdata", "grader-source-pins.json"), &pins)
	if cases.UpstreamRepository != pins.UpstreamRepository || cases.UpstreamRevision != pins.UpstreamRevision {
		t.Fatalf("lowering cases generated from %s@%s, sources pinned at %s@%s; regenerate cases.json",
			cases.UpstreamRepository, cases.UpstreamRevision, pins.UpstreamRepository, pins.UpstreamRevision)
	}
	// keywords.sql spells these closed patterns out as ASCII classes.
	if cases.UpstreamPatterns.Retries != `/\bretry(?:ing)?\b/i` ||
		cases.UpstreamPatterns.Guard != `/\b(check|verify|verification|policy|approval|approve|guard|confirm|authorize|authorization)\b/i` {
		t.Fatalf("upstream keyword patterns changed; update keywords.sql: %+v", cases.UpstreamPatterns)
	}
	if len(cases.Recurrence) == 0 || len(cases.Keywords) == 0 {
		t.Fatal("lowering cases are empty")
	}
	return cases
}

func readJSONFile(t *testing.T, path string, target any) {
	t.Helper()
	content, err := os.ReadFile(path) // #nosec G304 -- package-owned test fixture.
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(content, target); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
}

func loweringTemplate(t *testing.T, name string, rowSets map[string]string) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join("testdata", "lowering", name)) // #nosec G304 -- package-owned test fixture.
	if err != nil {
		t.Fatal(err)
	}
	var statement []string
	for _, line := range strings.Split(string(content), "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "--") {
			statement = append(statement, line)
		}
	}
	sql := strings.TrimSpace(strings.Join(statement, "\n"))
	for placeholder, rows := range rowSets {
		if !strings.Contains(sql, placeholder) {
			t.Fatalf("%s: placeholder %s missing", name, placeholder)
		}
		sql = strings.Replace(sql, placeholder, rows, 1)
	}
	return sql
}

// rowSet renders typed rows; an empty set keeps its column types.
func rowSet(columns []string, types []string, rows [][]string) string {
	casts := make([]string, len(columns))
	for i, column := range columns {
		casts[i] = "CAST(" + column + " AS " + types[i] + ") AS " + column
	}
	if len(rows) == 0 {
		empty := make([]string, len(columns))
		for i := range columns {
			empty[i] = "NULL"
		}
		return "SELECT " + strings.Join(casts, ", ") + " FROM (VALUES (" + strings.Join(empty, ", ") + ")) AS v(" + strings.Join(columns, ", ") + ") WHERE false"
	}
	values := make([]string, len(rows))
	for i, row := range rows {
		values[i] = "(" + strings.Join(row, ", ") + ")"
	}
	return "SELECT " + strings.Join(casts, ", ") + " FROM (VALUES " + strings.Join(values, ", ") + ") AS v(" + strings.Join(columns, ", ") + ")"
}

func recurrenceLoweringSQL(t *testing.T, cases []recurrenceCase) string {
	t.Helper()
	traces := make([][]string, 0, len(cases))
	var states, events [][]string
	for _, c := range cases {
		trace := literal(c.ID)
		traces = append(traces, []string{trace})
		elements := c.States
		if c.Sequence != "" {
			if elements != nil {
				t.Fatalf("%s: states and sequence are exclusive", c.ID)
			}
			for _, id := range c.Sequence {
				elements = append(elements, json.RawMessage(strconv.Quote(string(id))))
			}
		}
		for position, element := range elements {
			id, index, kind := s1State(t, c.ID, element)
			states = append(states, []string{trace, strconv.Itoa(position), id, index, literal(kind)})
		}
		for position, element := range c.Events {
			index, kind := s1EventIndex(t, c.ID, element)
			events = append(events, []string{trace, strconv.Itoa(position), index, literal(kind)})
		}
	}
	return loweringTemplate(t, "recurrence.sql", map[string]string{
		"{{traces}}": rowSet([]string{"trace"}, []string{"VARCHAR"}, traces),
		"{{states}}": rowSet([]string{"trace", "position", "id", "first_event_index", "first_event_index_kind"},
			[]string{"VARCHAR", "BIGINT", "VARCHAR", "BIGINT", "VARCHAR"}, states),
		"{{events}}": rowSet([]string{"trace", "position", "event_index", "event_index_kind"},
			[]string{"VARCHAR", "BIGINT", "BIGINT", "VARCHAR"}, events),
	})
}

// s1State applies strict S1 typing: a state is a string id or an object with a
// non-empty string id; anything else keeps its position but has no id. An
// order index is an exact JSON safe integer; any other present value is untyped.
func s1State(t *testing.T, trace string, element json.RawMessage) (id, index, kind string) {
	t.Helper()
	var value any
	decodeJSONNumber(t, trace, element, &value)
	switch state := value.(type) {
	case string:
		return literal(state), "NULL", "absent"
	case map[string]any:
		name, ok := state["id"].(string)
		if !ok || name == "" {
			return "NULL", "NULL", "absent"
		}
		raw, present := state["firstEventIndex"]
		if !present {
			return literal(name), "NULL", "absent"
		}
		if integer, ok := safeInteger(raw); ok {
			return literal(name), integer, "integer"
		}
		return literal(name), "NULL", "other"
	default:
		return "NULL", "NULL", "absent"
	}
}

func s1EventIndex(t *testing.T, trace string, element json.RawMessage) (index, kind string) {
	t.Helper()
	var value any
	decodeJSONNumber(t, trace, element, &value)
	event, ok := value.(map[string]any)
	if !ok {
		return "NULL", "absent"
	}
	raw, present := event["index"]
	if !present {
		return "NULL", "absent"
	}
	if integer, ok := safeInteger(raw); ok {
		return integer, "integer"
	}
	return "NULL", "other"
}

func decodeJSONNumber(t *testing.T, trace string, raw json.RawMessage, target *any) {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(target); err != nil {
		t.Fatalf("%s: %v", trace, err)
	}
}

func safeInteger(value any) (string, bool) {
	number, ok := value.(json.Number)
	if !ok {
		return "", false
	}
	integer, err := strconv.ParseInt(number.String(), 10, 64)
	if err != nil || integer > 1<<53-1 || integer < -(1<<53-1) {
		return "", false
	}
	return strconv.FormatInt(integer, 10), true
}

func keywordLoweringSQL(t *testing.T, cases []keywordCase) string {
	t.Helper()
	rows := make([][]string, len(cases))
	for i, c := range cases {
		rows[i] = []string{literal(c.ID), literal(c.Text)}
	}
	return loweringTemplate(t, "keywords.sql", map[string]string{
		"{{texts}}": rowSet([]string{"id", "text"}, []string{"VARCHAR", "VARCHAR"}, rows),
	})
}

func sameOutcome(actual, expected loweringOutcome) bool {
	if actual.Status != expected.Status || (actual.Value == nil) != (expected.Value == nil) {
		return false
	}
	return actual.Value == nil || math.Float64bits(*actual.Value) == math.Float64bits(*expected.Value)
}

func formatOutcome(outcome loweringOutcome) string {
	if outcome.Value == nil {
		return outcome.Status
	}
	return fmt.Sprintf("%s %s", outcome.Status, strconv.FormatFloat(*outcome.Value, 'g', -1, 64))
}
