package sessionarchive

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/githubnext/gh-cao/server/internal/query"
)

var ErrQuery = errors.New("invalid archive analytical query")

type Definition struct {
	Name       string                `json:"name"`
	Subject    string                `json:"subject"`
	Objective  string                `json:"objective,omitempty"`
	Acceptance string                `json:"acceptance,omitempty"`
	From       string                `json:"from"`
	Filter     *AnalyticalFilter     `json:"filter,omitempty"`
	Aggregate  *AnalyticalAggregate  `json:"aggregate,omitempty"`
	Select     []query.SelectedField `json:"select,omitempty"`
	OrderBy    []query.OrderField    `json:"order-by,omitempty"`
	Limit      *int                  `json:"limit,omitempty"`
}

type AnalyticalFilter struct {
	Predicates []AnalyticalPredicate `json:"predicates"`
}

type AnalyticalPredicate struct {
	Field  string          `json:"field"`
	Equals json.RawMessage `json:"equals,omitempty"`
	In     json.RawMessage `json:"in,omitempty"`
	GTE    json.RawMessage `json:"gte,omitempty"`
	LT     json.RawMessage `json:"lt,omitempty"`
}

type AnalyticalAggregate struct {
	By     []string          `json:"by,omitempty"`
	Values []AnalyticalValue `json:"values"`
}

type AnalyticalValue struct {
	Field   string `json:"field"`
	As      string `json:"as"`
	Reducer string `json:"reducer"`
}

type Column struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

type QueryMetadata struct {
	Limited          bool   `json:"limited"`
	UpstreamCoverage string `json:"upstreamCoverage"`
	AdmissionGaps    int    `json:"admissionGaps"`
	TracesWithGaps   int    `json:"tracesWithGaps"`
}

type AnalyticalResult struct {
	Columns  []Column         `json:"columns"`
	Rows     []map[string]any `json:"rows"`
	Metadata QueryMetadata    `json:"metadata"`
}

func (d *Definition) UnmarshalJSON(content []byte) error {
	// Null is not an omitted filter, selector or operand.
	tokens := json.NewDecoder(bytes.NewReader(content))
	for {
		token, err := tokens.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if token == nil {
			return ErrQuery
		}
	}
	type definition Definition
	var decoded definition
	if err := decode(content, &decoded); err != nil {
		return err
	}
	*d = Definition(decoded)
	return nil
}

func (d Definition) Validate() error {
	_, err := d.Plan()
	return err
}

var queryName = regexp.MustCompile(`^[a-z][a-z0-9]*(?:-[a-z0-9]+)*$`)
var outputName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9-]{0,127}$`)

// Column names and types come from the generated evidence contract, not a
// second archive schema. Content and structured fields are not analytical inputs.
func analyticalColumns() map[string]string {
	fields := map[string]string{}
	for i, name := range eventColumns {
		switch {
		case name == "PayloadJSON":
			continue
		case eventSQLTypes[i] == "VARCHAR":
			fields[name] = "string"
		case eventSQLTypes[i] == "BIGINT":
			fields[name] = "integer"
		}
	}
	return fields
}

type Scalar struct {
	Type    string
	String  string
	Integer int64
}

type PlannedPredicate struct {
	Field  Column
	Equals *Scalar
	In     []Scalar
	GTE    *Scalar
	LT     *Scalar
}

type PlannedValue struct {
	Field   Column
	As      string
	Reducer string
}

type PlannedSelection struct {
	Field Column
	As    string
}

type PlannedOrder struct {
	Field     Column
	Direction string
}

// Plan contains typed operations only. Storage membership and backend syntax
// belong to the archive adapters, never the caller's query definition.
type Plan struct {
	Columns       []Column
	Inputs        []string
	Predicates    []PlannedPredicate
	By            []Column
	Values        []PlannedValue
	Selections    []PlannedSelection
	Orders        []PlannedOrder
	Ties          []Column
	Limit         int
	ExplicitLimit bool
}

type Capabilities struct {
	LanguageVersion string   `json:"language-version"`
	Source          string   `json:"source"`
	Fields          []Column `json:"fields"`
	Predicates      []string `json:"predicates"`
	Reducers        []string `json:"reducers"`
	MaxRows         int      `json:"maxRows"`
}

func AnalyticalCapabilities(maxRows int) Capabilities {
	c := Capabilities{LanguageVersion: "0.1.0", Source: "archive-events",
		Predicates: []string{"equals", "in", "gte", "lt"}, Reducers: []string{"count", "distinct-count"}, MaxRows: maxRows}
	fields := analyticalColumns()
	for _, name := range eventColumns {
		if kind := fields[name]; kind != "" {
			c.Fields = append(c.Fields, Column{Name: name, Type: kind})
		}
	}
	return c
}

func (p *Plan) Require(c Capabilities) error {
	if c.LanguageVersion != "0.1.0" || c.Source != "archive-events" || c.MaxRows < 1 {
		return ErrQuery
	}
	fields := analyticalColumns()
	for _, name := range p.Inputs {
		if !slices.ContainsFunc(c.Fields, func(field Column) bool { return field.Name == name && field.Type == fields[name] }) {
			return ErrQuery
		}
	}
	for _, predicate := range p.Predicates {
		for _, mode := range []struct {
			name    string
			present bool
		}{
			{"equals", predicate.Equals != nil}, {"in", predicate.In != nil},
			{"gte", predicate.GTE != nil}, {"lt", predicate.LT != nil},
		} {
			if mode.present && !slices.Contains(c.Predicates, mode.name) {
				return ErrQuery
			}
		}
	}
	for _, value := range p.Values {
		if !slices.Contains(c.Reducers, value.Reducer) {
			return ErrQuery
		}
	}
	if p.ExplicitLimit && p.Limit > c.MaxRows {
		return ErrLimit
	}
	p.Limit = min(p.Limit, c.MaxRows)
	return nil
}

func (d Definition) Plan() (Plan, error) {
	p := Plan{Limit: 10000, ExplicitLimit: d.Limit != nil}
	if d.From != "archive-events" || len(d.Name) > 128 || !queryName.MatchString(d.Name) ||
		strings.TrimSpace(d.Subject) == "" || utf8.RuneCountInString(d.Subject+d.Objective+d.Acceptance) > 512 ||
		len(d.Select) > 32 || (d.Select != nil && len(d.Select) == 0) || len(d.OrderBy) > 16 ||
		(d.Limit != nil && (*d.Limit < 1 || *d.Limit > 10000)) {
		return p, ErrQuery
	}
	if d.Limit != nil {
		p.Limit = *d.Limit
	}
	fields, needed := analyticalColumns(), map[string]bool{}
	read := func(name string) (Column, error) {
		if fields[name] == "" {
			return Column{}, ErrQuery
		}
		needed[name] = true
		return Column{Name: name, Type: fields[name]}, nil
	}
	if d.Filter != nil {
		if len(d.Filter.Predicates) < 1 || len(d.Filter.Predicates) > 64 {
			return p, ErrQuery
		}
		for _, predicate := range d.Filter.Predicates {
			field, err := read(predicate.Field)
			if err != nil {
				return p, err
			}
			planned, err := predicate.plan(field)
			if err != nil {
				return p, err
			}
			p.Predicates = append(p.Predicates, planned)
		}
	}
	available := map[string]string{}
	var ties []string
	if d.Aggregate == nil {
		if len(d.Select) == 0 {
			return p, ErrQuery
		}
		for name, kind := range fields {
			available[name] = kind
		}
		ties = []string{"Repository", "RunId", "RunAttempt", "TraceId", "ProjectionId", "Sequence"}
	} else {
		a := d.Aggregate
		if len(a.By) > 16 || len(a.Values) < 1 || len(a.Values) > 16 {
			return p, ErrQuery
		}
		for _, name := range a.By {
			field, err := read(name)
			if err != nil || available[name] != "" {
				return p, ErrQuery
			}
			available[name] = fields[name]
			p.By = append(p.By, field)
		}
		ties = a.By
		for _, value := range a.Values {
			field, err := read(value.Field)
			if err != nil || !outputName.MatchString(value.As) || available[value.As] != "" || fields[value.As] != "" {
				return p, ErrQuery
			}
			switch value.Reducer {
			case "count", "distinct-count":
			default:
				return p, ErrQuery
			}
			available[value.As] = "integer"
			p.Values = append(p.Values, PlannedValue{Field: field, As: value.As, Reducer: value.Reducer})
		}
	}
	selected := d.Select
	if len(selected) == 0 {
		for _, name := range d.Aggregate.By {
			selected = append(selected, query.SelectedField{Field: name})
		}
		for _, value := range d.Aggregate.Values {
			selected = append(selected, query.SelectedField{Field: value.As})
		}
	}
	outputs, used := map[string]string{}, map[string]bool{}
	for _, selection := range selected {
		kind := available[selection.Field]
		name := selection.As
		if name == "" {
			name = selection.Field
		}
		if kind == "" || !outputName.MatchString(name) || used[strings.ToLower(name)] {
			return p, ErrQuery
		}
		if fields[selection.Field] != "" {
			needed[selection.Field] = true
		}
		used[strings.ToLower(name)], outputs[name] = true, kind
		p.Selections = append(p.Selections, PlannedSelection{Field: Column{Name: selection.Field, Type: kind}, As: name})
		p.Columns = append(p.Columns, Column{Name: name, Type: kind})
	}
	for _, order := range d.OrderBy {
		if outputs[order.Field] == "" || (order.Direction != "" && order.Direction != "asc" && order.Direction != "desc") {
			return p, ErrQuery
		}
		direction := order.Direction
		if direction == "" {
			direction = "asc"
		}
		p.Orders = append(p.Orders, PlannedOrder{Field: Column{Name: order.Field, Type: outputs[order.Field]}, Direction: direction})
	}
	for _, name := range ties {
		field, err := read(name)
		if err != nil {
			return p, err
		}
		p.Ties = append(p.Ties, field)
	}
	for _, name := range eventColumns {
		if needed[name] {
			p.Inputs = append(p.Inputs, name)
		}
	}
	return p, nil
}

func parseScalar(raw json.RawMessage, kind string) (*Scalar, error) {
	value := &Scalar{Type: kind}
	switch kind {
	case "string":
		if len(raw) == 0 || raw[0] != '"' || json.Unmarshal(raw, &value.String) != nil || strings.ContainsRune(value.String, 0) {
			return nil, ErrQuery
		}
	case "integer":
		n, err := strconv.ParseInt(string(raw), 10, 64)
		if err != nil || strconv.FormatInt(n, 10) != string(raw) {
			return nil, ErrQuery
		}
		value.Integer = n
	default:
		return nil, ErrQuery
	}
	return value, nil
}

func (predicate AnalyticalPredicate) plan(field Column) (PlannedPredicate, error) {
	p := PlannedPredicate{Field: field}
	modes := 0
	for _, present := range []bool{predicate.Equals != nil, predicate.In != nil, predicate.GTE != nil || predicate.LT != nil} {
		if present {
			modes++
		}
	}
	if modes != 1 {
		return p, ErrQuery
	}
	for _, operand := range []struct {
		raw    json.RawMessage
		target **Scalar
	}{
		{predicate.Equals, &p.Equals}, {predicate.GTE, &p.GTE}, {predicate.LT, &p.LT},
	} {
		if operand.raw != nil {
			value, err := parseScalar(operand.raw, field.Type)
			if err != nil {
				return p, err
			}
			*operand.target = value
		}
	}
	if predicate.In != nil {
		var alternatives []json.RawMessage
		if json.Unmarshal(predicate.In, &alternatives) != nil || len(alternatives) < 1 || len(alternatives) > 32 {
			return p, ErrQuery
		}
		for _, raw := range alternatives {
			value, err := parseScalar(raw, field.Type)
			if err != nil {
				return p, err
			}
			p.In = append(p.In, *value)
		}
	}
	return p, nil
}
