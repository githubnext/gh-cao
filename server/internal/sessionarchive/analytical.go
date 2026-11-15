package sessionarchive

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/githubnext/gh-cao/server/internal/query"
)

var ErrQuery = errors.New("invalid archive analytical query")

type Definition struct {
	Name          string                `json:"name"`
	Subject       string                `json:"subject"`
	Objective     string                `json:"objective,omitempty"`
	Acceptance    string                `json:"acceptance,omitempty"`
	From          string                `json:"from"`
	Filter        *AnalyticalFilter     `json:"filter,omitempty"`
	Aggregate     *AnalyticalAggregate  `json:"aggregate,omitempty"`
	PostAggregate []ComputedValue       `json:"post-aggregate,omitempty"`
	Select        []query.SelectedField `json:"select,omitempty"`
	OrderBy       []query.OrderField    `json:"order-by,omitempty"`
	Limit         *int                  `json:"limit,omitempty"`
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
	State  string          `json:"state,omitempty"`
}

type AnalyticalAggregate struct {
	By     []string          `json:"by,omitempty"`
	Values []AnalyticalValue `json:"values"`
}

type AnalyticalValue struct {
	Field   string             `json:"field"`
	As      string             `json:"as"`
	Reducer string             `json:"reducer"`
	Filter  *AnalyticalFilter  `json:"filter,omitempty"`
	OrderBy []query.OrderField `json:"order-by,omitempty"`
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
	QueryDigest      string `json:"queryDigest"`
	Implementation   string `json:"implementation"`
	LanguageVersion  string `json:"language-version"`
}

type AnalyticalResult struct {
	Columns  []Column         `json:"columns"`
	Rows     []map[string]any `json:"rows"`
	Metadata QueryMetadata    `json:"metadata"`
}

func (d *Definition) UnmarshalJSON(content []byte) error {
	var raw any
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.UseNumber()
	if err := decoder.Decode(&raw); err != nil {
		return err
	}
	if !computationNulls(raw, false) {
		return ErrQuery
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

// Existing count-only requests remain usable without granting new operators.
func (d Definition) BaseProfile() bool {
	if len(d.PostAggregate) > 0 {
		return false
	}
	if d.Filter != nil {
		for _, predicate := range d.Filter.Predicates {
			if predicate.State != "" {
				return false
			}
		}
	}
	if d.Aggregate != nil {
		for _, value := range d.Aggregate.Values {
			if value.Filter != nil || len(value.OrderBy) > 0 || value.Reducer != "count" && value.Reducer != "distinct-count" {
				return false
			}
		}
	}
	return true
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
		case eventSQLTypes[i] == "DOUBLE":
			fields[name] = "number"
		case eventSQLTypes[i] == "BOOLEAN":
			fields[name] = "boolean"
		}
	}
	return fields
}

type Scalar struct {
	Type    string
	String  string
	Integer int64
	Number  float64
	Boolean bool
}

type PlannedPredicate struct {
	Field  Column
	Equals *Scalar
	In     []Scalar
	GTE    *Scalar
	LT     *Scalar
	State  string
	Path   string
}

type PlannedValue struct {
	Field      Column
	As         string
	Reducer    string
	Predicates []PlannedPredicate
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
	Computations  []PlannedComputation
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
	Functions       []string `json:"functions"`
	FieldStates     []string `json:"fieldStates"`
	MaxRows         int      `json:"maxRows"`
}

func AnalyticalCapabilities(maxRows int) Capabilities {
	c := Capabilities{LanguageVersion: "0.2.0", Source: "archive-events",
		Predicates: []string{"equals", "in", "gte", "lt", "state"},
		Reducers:   []string{"count", "distinct-count", "sum", "max", "first"},
		Functions:  scalarFunctions, FieldStates: fieldStates, MaxRows: maxRows}
	fields := analyticalColumns()
	for _, name := range eventColumns {
		if kind := fields[name]; kind != "" {
			c.Fields = append(c.Fields, Column{Name: name, Type: kind})
		}
	}
	return c
}

func (p *Plan) Require(c Capabilities) error {
	if c.LanguageVersion != "0.2.0" || c.Source != "archive-events" || c.MaxRows < 1 {
		return ErrQuery
	}
	fields := analyticalColumns()
	for _, name := range p.Inputs {
		if slices.Contains([]string{"PresentFields", "NullFields", "InvalidFields", "OmissionReasons"}, name) {
			continue
		}
		if !slices.ContainsFunc(c.Fields, func(field Column) bool { return field.Name == name && field.Type == fields[name] }) {
			return ErrQuery
		}
	}
	predicates := slices.Clone(p.Predicates)
	for _, value := range p.Values {
		predicates = append(predicates, value.Predicates...)
	}
	for _, predicate := range predicates {
		for _, mode := range []struct {
			name    string
			present bool
		}{
			{"equals", predicate.Equals != nil}, {"in", predicate.In != nil},
			{"gte", predicate.GTE != nil}, {"lt", predicate.LT != nil},
			{"state", predicate.State != ""},
		} {
			if mode.present && !slices.Contains(c.Predicates, mode.name) {
				return ErrQuery
			}
			if predicate.State != "" && !slices.Contains(c.FieldStates, predicate.State) {
				return ErrQuery
			}
		}
	}
	for _, computed := range p.Computations {
		if !computed.Expression.require(c.Functions) {
			return ErrQuery
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
	reserved := map[string]bool{}
	for name := range fields {
		reserved[strings.ToLower(name)] = true
	}
	read := func(name string) (Column, error) {
		if fields[name] == "" {
			return Column{}, ErrQuery
		}
		needed[name] = true
		return Column{Name: name, Type: fields[name]}, nil
	}
	planFilter := func(filter *AnalyticalFilter) ([]PlannedPredicate, error) {
		return planPredicates(filter, read, needed)
	}
	if d.Filter != nil {
		var err error
		p.Predicates, err = planFilter(d.Filter)
		if err != nil {
			return p, err
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
			if err != nil || !outputName.MatchString(value.As) || reserved[strings.ToLower(value.As)] {
				return p, ErrQuery
			}
			kind := field.Type
			switch value.Reducer {
			case "count", "distinct-count":
				kind = "integer"
			case "sum", "max":
				if kind != "integer" && kind != "number" {
					return p, ErrQuery
				}
				if value.Reducer == "sum" {
					kind = "number"
				}
			case "first":
				if !numeric(kind) {
					return p, ErrQuery
				}
			default:
				return p, ErrQuery
			}
			ordered := value.Reducer == "first" || value.Reducer == "sum" && field.Type == "number"
			if ordered {
				if len(value.OrderBy) != 1 || value.OrderBy[0].Field != "Sequence" ||
					(value.OrderBy[0].Direction != "" && value.OrderBy[0].Direction != "asc") {
					return p, ErrQuery
				}
				for _, binding := range traceBinding {
					if !slices.Contains(a.By, binding) {
						return p, ErrQuery
					}
				}
				needed["Sequence"] = true
			} else if len(value.OrderBy) != 0 {
				return p, ErrQuery
			}
			predicates, err := planFilter(value.Filter)
			if err != nil {
				return p, err
			}
			available[value.As] = kind
			reserved[strings.ToLower(value.As)] = true
			p.Values = append(p.Values, PlannedValue{Field: field, As: value.As, Reducer: value.Reducer, Predicates: predicates})
		}
	}
	if len(d.PostAggregate) > 32 || len(d.PostAggregate) > 0 && d.Aggregate == nil {
		return p, ErrQuery
	}
	nodes := 0
	for _, computed := range d.PostAggregate {
		if !outputName.MatchString(computed.As) || reserved[strings.ToLower(computed.As)] {
			return p, ErrQuery
		}
		expression, err := (Expression{Function: computed.Function, Args: computed.Args}).plan(available, 0, &nodes)
		if err != nil {
			return p, err
		}
		available[computed.As] = expression.Type
		reserved[strings.ToLower(computed.As)] = true
		p.Computations = append(p.Computations, PlannedComputation{As: computed.As, Expression: expression})
	}
	selected := d.Select
	if len(selected) == 0 {
		for _, name := range d.Aggregate.By {
			selected = append(selected, query.SelectedField{Field: name})
		}
		for _, value := range d.Aggregate.Values {
			selected = append(selected, query.SelectedField{Field: value.As})
		}
		for _, computed := range d.PostAggregate {
			selected = append(selected, query.SelectedField{Field: computed.As})
		}
	}
	if len(selected) > 32 {
		return p, ErrQuery
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
	predicates := len(p.Predicates)
	for _, value := range p.Values {
		predicates += len(value.Predicates)
	}
	if predicates > 64 {
		return p, ErrQuery
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
	case "number":
		var number json.Number
		if json.Unmarshal(raw, &number) != nil || len(raw) == 0 || raw[0] == '"' {
			return nil, ErrQuery
		}
		n, err := number.Float64()
		if err != nil || math.IsInf(n, 0) || math.IsNaN(n) {
			return nil, ErrQuery
		}
		value.Number = n
	case "boolean":
		if string(raw) != "true" && string(raw) != "false" {
			return nil, ErrQuery
		}
		value.Boolean = string(raw) == "true"
	default:
		return nil, ErrQuery
	}
	return value, nil
}

func (predicate AnalyticalPredicate) plan(field Column) (PlannedPredicate, error) {
	p := PlannedPredicate{Field: field}
	modes := 0
	for _, present := range []bool{predicate.Equals != nil, predicate.In != nil, predicate.GTE != nil || predicate.LT != nil, predicate.State != ""} {
		if present {
			modes++
		}
	}
	if modes != 1 {
		return p, ErrQuery
	}
	if predicate.State != "" {
		if !slices.Contains(fieldStates, predicate.State) {
			return p, ErrQuery
		}
		for _, promoted := range evidenceFields {
			if promoted.Name == field.Name && promoted.Index >= 0 {
				p.State, p.Path = predicate.State, promoted.Path
				return p, nil
			}
		}
		return p, ErrQuery
	}
	if field.Type == "boolean" && (predicate.GTE != nil || predicate.LT != nil) {
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

var traceBinding = []string{"Namespace", "Repository", "RunId", "RunAttempt", "TraceId", "ProjectionId"}
var fieldStates = []string{"present", "absent", "null", "invalid", "excluded", "redacted", "valid"}

func planPredicates(filter *AnalyticalFilter, read func(string) (Column, error), needed map[string]bool) ([]PlannedPredicate, error) {
	if filter == nil {
		return nil, nil
	}
	if len(filter.Predicates) < 1 || len(filter.Predicates) > 64 {
		return nil, ErrQuery
	}
	var out []PlannedPredicate
	for _, predicate := range filter.Predicates {
		field, err := read(predicate.Field)
		if err != nil {
			return nil, err
		}
		planned, err := predicate.plan(field)
		if err != nil {
			return nil, err
		}
		if planned.State != "" {
			for _, name := range []string{"PresentFields", "NullFields", "InvalidFields", "OmissionReasons"} {
				needed[name] = true
			}
		}
		out = append(out, planned)
	}
	return out, nil
}

func (d Definition) Metadata(limited bool, admissions, gaps int) QueryMetadata {
	content, _ := json.Marshal(d)
	digest := sha256.Sum256(content)
	return QueryMetadata{Limited: limited, UpstreamCoverage: "unknown", AdmissionGaps: admissions, TracesWithGaps: gaps,
		QueryDigest: hex.EncodeToString(digest[:]), Implementation: "cao/archive-query/0.2.0", LanguageVersion: "0.2.0"}
}
