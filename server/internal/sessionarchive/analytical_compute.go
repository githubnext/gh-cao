package sessionarchive

import (
	"encoding/json"
	"slices"
)

var scalarFunctions = []string{"if", "equals", "gt", "lte", "sum", "difference", "product", "quotient", "coalesce", "is-null", "and"}

// Expressions use Dashboard Language's function/args and field/value vocabulary,
// with strict decoding rather than the dashboard's contextual argument resolver.
type Expression struct {
	Field    string          `json:"field,omitempty"`
	Value    json.RawMessage `json:"value,omitempty"`
	Type     string          `json:"type,omitempty"`
	Function string          `json:"function,omitempty"`
	Args     []Expression    `json:"args,omitempty"`
}

type ComputedValue struct {
	As       string       `json:"as"`
	Function string       `json:"function"`
	Args     []Expression `json:"args"`
}

func (e *Expression) UnmarshalJSON(content []byte) error {
	type expression Expression
	var decoded expression
	if err := decode(content, &decoded); err != nil {
		return err
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(content, &keys); err != nil {
		return err
	}
	var allowed []string
	switch {
	case keys["field"] != nil:
		if decoded.Field == "" {
			return ErrQuery
		}
		allowed = []string{"field"}
	case keys["value"] != nil:
		allowed = []string{"value", "type"}
	case keys["function"] != nil:
		if decoded.Function == "" || decoded.Args == nil {
			return ErrQuery
		}
		allowed = []string{"function", "args"}
	default:
		return ErrQuery
	}
	for key := range keys {
		if !slices.Contains(allowed, key) {
			return ErrQuery
		}
	}
	*e = Expression(decoded)
	return nil
}

type PlannedExpression struct {
	Type     string
	Field    string
	Literal  *Scalar
	Function string
	Args     []PlannedExpression
}

type PlannedComputation struct {
	As         string
	Expression PlannedExpression
}

func computationNulls(value any, expression bool) bool {
	switch v := value.(type) {
	case nil:
		return false
	case map[string]any:
		for key, child := range v {
			if child == nil && key == "value" && expression {
				if kind, ok := v["type"].(string); ok && numeric(kind) {
					continue
				}
			}
			if !computationNulls(child, expression || key == "post-aggregate" || key == "compute") {
				return false
			}
		}
	case []any:
		for _, child := range v {
			if !computationNulls(child, expression) {
				return false
			}
		}
	}
	return true
}

func numeric(kind string) bool { return kind == "integer" || kind == "number" }

func (e Expression) plan(fields map[string]string, depth int, nodes *int) (PlannedExpression, error) {
	*nodes++
	p := PlannedExpression{}
	if depth > 8 || *nodes > 256 {
		return p, ErrQuery
	}
	switch {
	case e.Field != "":
		p.Type, p.Field = fields[e.Field], e.Field
		if p.Type == "" || e.Value != nil || e.Function != "" || e.Type != "" || e.Args != nil {
			return p, ErrQuery
		}
		return p, nil
	case e.Value != nil:
		if e.Function != "" || e.Args != nil {
			return p, ErrQuery
		}
		kind := e.Type
		if string(e.Value) == "null" {
			if !numeric(kind) {
				return p, ErrQuery
			}
			p.Type = kind
			return p, nil
		}
		if kind == "" {
			switch e.Value[0] {
			case '"':
				kind = "string"
			case 't', 'f':
				kind = "boolean"
			default:
				kind = "number"
			}
		}
		literal, err := parseScalar(e.Value, kind)
		if err != nil {
			return p, err
		}
		p.Type, p.Literal = kind, literal
		return p, nil
	case e.Function != "":
		if e.Type != "" || !slices.Contains(scalarFunctions, e.Function) {
			return p, ErrQuery
		}
	default:
		return p, ErrQuery
	}
	p.Function = e.Function
	for _, arg := range e.Args {
		planned, err := arg.plan(fields, depth+1, nodes)
		if err != nil {
			return p, err
		}
		p.Args = append(p.Args, planned)
	}
	n := len(p.Args)
	switch p.Function {
	case "and":
		if n != 2 || p.Args[0].Type != "boolean" || p.Args[1].Type != "boolean" {
			return p, ErrQuery
		}
		p.Type = "boolean"
	case "is-null":
		if n != 1 {
			return p, ErrQuery
		}
		p.Type = "boolean"
	case "if":
		if n != 3 || p.Args[0].Type != "boolean" {
			return p, ErrQuery
		}
		p.Type = p.Args[1].Type
		if p.Type != p.Args[2].Type {
			if !numeric(p.Type) || !numeric(p.Args[2].Type) {
				return p, ErrQuery
			}
			p.Type = "number"
		}
	case "equals", "gt", "lte":
		if n != 2 || p.Args[0].Type != p.Args[1].Type && (!numeric(p.Args[0].Type) || !numeric(p.Args[1].Type)) {
			return p, ErrQuery
		}
		if p.Function != "equals" && !numeric(p.Args[0].Type) {
			return p, ErrQuery
		}
		p.Type = "boolean"
	case "sum", "difference", "product", "quotient":
		if n != 2 || !numeric(p.Args[0].Type) || !numeric(p.Args[1].Type) {
			return p, ErrQuery
		}
		p.Type = "number"
	case "coalesce":
		if n != 2 || p.Args[0].Type != p.Args[1].Type && (!numeric(p.Args[0].Type) || !numeric(p.Args[1].Type)) {
			return p, ErrQuery
		}
		p.Type = p.Args[0].Type
		if p.Type != p.Args[1].Type {
			p.Type = "number"
		}
	}
	return p, nil
}

// rowLevel rejects null tests over strings: archive strings use "" for
// absence, and KQL coalesce treats "" as missing while DuckDB does not.
func (e PlannedExpression) rowLevel() bool {
	for _, arg := range e.Args {
		if (e.Function == "is-null" || e.Function == "coalesce") && arg.Type == "string" || !arg.rowLevel() {
			return false
		}
	}
	return true
}

func (e PlannedExpression) visitFields(visit func(string)) {
	if e.Field != "" {
		visit(e.Field)
	}
	for _, arg := range e.Args {
		arg.visitFields(visit)
	}
}

func (e PlannedExpression) require(functions []string) bool {
	if e.Function != "" && !slices.Contains(functions, e.Function) {
		return false
	}
	for _, arg := range e.Args {
		if !arg.require(functions) {
			return false
		}
	}
	return true
}
