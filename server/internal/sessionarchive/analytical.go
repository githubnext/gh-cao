package sessionarchive

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"regexp"
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
	_, err := d.compile()
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

func identifier(name string) string { return `"` + strings.ReplaceAll(name, `"`, `""`) + `"` }

type analyticalPlan struct {
	columns    []Column
	inputs     []string
	projection string
	filter     string
	group      string
	order      string
	limit      int
}

func (d Definition) compile() (analyticalPlan, error) {
	p := analyticalPlan{limit: 10000}
	if d.From != "archive-events" || len(d.Name) > 128 || !queryName.MatchString(d.Name) ||
		strings.TrimSpace(d.Subject) == "" || utf8.RuneCountInString(d.Subject+d.Objective+d.Acceptance) > 512 ||
		len(d.Select) > 32 || (d.Select != nil && len(d.Select) == 0) || len(d.OrderBy) > 16 ||
		(d.Limit != nil && (*d.Limit < 1 || *d.Limit > 10000)) {
		return p, ErrQuery
	}
	if d.Limit != nil {
		p.limit = *d.Limit
	}
	fields, needed := analyticalColumns(), map[string]bool{}
	read := func(name string) (string, error) {
		if fields[name] == "" {
			return "", ErrQuery
		}
		needed[name] = true
		return identifier(name), nil
	}
	var predicates []string
	if d.Filter != nil {
		if len(d.Filter.Predicates) < 1 || len(d.Filter.Predicates) > 64 {
			return p, ErrQuery
		}
		for _, predicate := range d.Filter.Predicates {
			field, err := read(predicate.Field)
			if err != nil {
				return p, err
			}
			sql, err := predicate.compile(field, fields[predicate.Field])
			if err != nil {
				return p, err
			}
			predicates = append(predicates, sql)
		}
	}
	p.filter = "true"
	if len(predicates) > 0 {
		p.filter = strings.Join(predicates, " AND ")
	}
	available, expressions := map[string]string{}, map[string]string{}
	var ties []string
	if d.Aggregate == nil {
		if len(d.Select) == 0 {
			return p, ErrQuery
		}
		for name, kind := range fields {
			available[name], expressions[name] = kind, identifier(name)
		}
		ties = []string{"Repository", "RunId", "RunAttempt", "TraceId", "ProjectionId", "Sequence"}
	} else {
		a := d.Aggregate
		if len(a.By) > 16 || len(a.Values) < 1 || len(a.Values) > 16 {
			return p, ErrQuery
		}
		var groups []string
		for _, name := range a.By {
			expression, err := read(name)
			if err != nil || available[name] != "" {
				return p, ErrQuery
			}
			available[name], expressions[name] = fields[name], expression
			groups = append(groups, expression)
		}
		ties = a.By
		if len(groups) > 0 {
			p.group = " GROUP BY " + strings.Join(groups, ",")
		}
		for _, value := range a.Values {
			expression, err := read(value.Field)
			if err != nil || !outputName.MatchString(value.As) || available[value.As] != "" || fields[value.As] != "" {
				return p, ErrQuery
			}
			if fields[value.Field] == "string" {
				expression = "nullif(" + expression + ",'')"
			}
			switch value.Reducer {
			case "count":
				expression = "count(" + expression + ")"
			case "distinct-count":
				expression = "count(DISTINCT " + expression + ")"
			default:
				return p, ErrQuery
			}
			available[value.As], expressions[value.As] = "integer", expression
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
	var projections, orders []string
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
		projections = append(projections, expressions[selection.Field]+" AS "+identifier(name))
		p.columns = append(p.columns, Column{Name: name, Type: kind})
	}
	for _, order := range d.OrderBy {
		if outputs[order.Field] == "" || (order.Direction != "" && order.Direction != "asc" && order.Direction != "desc") {
			return p, ErrQuery
		}
		orders = append(orders, analyticalOrder(identifier(order.Field), outputs[order.Field], order.Direction))
	}
	for i, name := range ties {
		expression, err := read(name)
		if err != nil {
			return p, err
		}
		hidden := identifier("__archive_tie_" + strconv.Itoa(i))
		projections = append(projections, expression+" AS "+hidden)
		orders = append(orders, analyticalOrder(hidden, fields[name], "asc"))
	}
	p.projection, p.order = strings.Join(projections, ","), strings.Join(orders, ",")
	for _, name := range eventColumns {
		if needed[name] {
			p.inputs = append(p.inputs, name)
		}
	}
	return p, nil
}

func analyticalOrder(expression, kind, direction string) string {
	if direction == "" {
		direction = "asc"
	}
	if kind == "string" {
		expression = "nullif(" + expression + ",'')"
	}
	return expression + " " + direction + " NULLS LAST"
}
