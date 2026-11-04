package query

import (
	"encoding/json"
	"fmt"
	"strings"
)

const (
	MaxInputRows             = 200_000
	MaxJoinRows              = 200_000
	MaxOutputRows            = 100_000
	MaxJoins                 = 4
	MaxDependencyDepth       = 32
	MaxPlanQueries           = 256
	MaxDependencyJoins       = 16
	MaxWorkingRows           = 500_000
	MaxRetainedRows          = 500_000
	MaxWorkingBytes          = 256 << 20
	MaxRetainedBytes         = 512 << 20
	MaxAggregateValues       = 64
	MaxPredicateAlternatives = 32
	MaxOperations            = 5_000_000
)

type Definition struct {
	Name           string            `json:"name"`
	From           string            `json:"from"`
	Union          []string          `json:"union,omitempty"`
	Joins          []Join            `json:"joins,omitempty"`
	Filter         *Filter           `json:"filter,omitempty"`
	Compute        []ComputedField   `json:"compute,omitempty"`
	Aggregate      *Aggregate        `json:"aggregate,omitempty"`
	TemporalSeries *TemporalSeries   `json:"temporal-series,omitempty"`
	Predict        []json.RawMessage `json:"predict,omitempty"`
	Window         []WindowField     `json:"window,omitempty"`
	Select         []SelectedField   `json:"select,omitempty"`
	OrderBy        []OrderField      `json:"order-by,omitempty"`
	Page           *PageBoundary     `json:"page,omitempty"`
	Limit          *int              `json:"limit,omitempty"`
	Facet          *Facet            `json:"facet,omitempty"`
	Stores         []string          `json:"stores,omitempty"`
}

type PageBoundary struct {
	Keys       []string `json:"keys"`
	Revision   string   `json:"revision"`
	Comparator string   `json:"comparator"`
}

const MaxChartFacets = 64

type Facet struct {
	Field  string `json:"field,omitempty"`
	Row    string `json:"row,omitempty"`
	Column string `json:"column,omitempty"`
	As     string `json:"as"`
}

type Join struct {
	Source string          `json:"source"`
	Type   string          `json:"type,omitempty"`
	On     []JoinKey       `json:"on"`
	Fields []SelectedField `json:"fields"`
}

type JoinKey struct {
	Left  string `json:"left"`
	Right string `json:"right"`
}

type Filter struct {
	Predicates []Predicate `json:"predicates,omitempty"`
	Search     *Search     `json:"search,omitempty"`
}

type Search struct {
	Fields []string `json:"fields"`
	Query  string   `json:"query"`
}

type Predicate struct {
	Field    string `json:"field"`
	Equals   any    `json:"equals,omitempty"`
	In       []any  `json:"in,omitempty"`
	Includes string `json:"includes,omitempty"`
	GTE      any    `json:"gte,omitempty"`
	LT       any    `json:"lt,omitempty"`
	Optional bool   `json:"optional,omitempty"`
}

type Argument struct {
	Field     *string `json:"field,omitempty"`
	Value     any     `json:"value,omitempty"`
	Context   string  `json:"context,omitempty"`
	Parameter string  `json:"parameter,omitempty"`
}

func (a Argument) MarshalJSON() ([]byte, error) {
	switch {
	case a.Field != nil:
		return json.Marshal(struct {
			Field string `json:"field"`
		}{*a.Field})
	case a.Context != "":
		return json.Marshal(struct {
			Context string `json:"context"`
		}{a.Context})
	case a.Parameter != "":
		return json.Marshal(struct {
			Parameter string `json:"parameter"`
		}{a.Parameter})
	default:
		return json.Marshal(struct {
			Value any `json:"value"`
		}{a.Value})
	}
}

// argumentKind identifies which of the mutually exclusive shapes a compute
// argument's raw JSON object took. It is useful for diagnosing a malformed
// dashboard query document without logging its field names or values.
type argumentKind string

const (
	argumentKindField     argumentKind = "field"
	argumentKindValue     argumentKind = "value"
	argumentKindContext   argumentKind = "context"
	argumentKindParameter argumentKind = "parameter"
	argumentKindInvalid   argumentKind = "invalid"
)

// classifyArgumentKind inspects a decoded compute-argument object and reports
// which shape it took, checking "field", "value", then "context" in priority
// order. It is a pure function extracted from UnmarshalJSON so the priority
// and rejection logic is testable directly against a raw key set, without
// constructing JSON bytes for every case.
func classifyArgumentKind(raw map[string]json.RawMessage) argumentKind {
	switch {
	case has(raw, "field"):
		return argumentKindField
	case has(raw, "value"):
		return argumentKindValue
	case has(raw, "context"):
		return argumentKindContext
	case has(raw, "parameter"):
		return argumentKindParameter
	default:
		return argumentKindInvalid
	}
}

func has(raw map[string]json.RawMessage, key string) bool {
	_, ok := raw[key]
	return ok
}

func (a *Argument) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	switch classifyArgumentKind(raw) {
	case argumentKindField:
		var value string
		if err := json.Unmarshal(raw["field"], &value); err != nil {
			return err
		}
		a.Field = &value
		return nil
	case argumentKindValue:
		return json.Unmarshal(raw["value"], &a.Value)
	case argumentKindContext:
		return json.Unmarshal(raw["context"], &a.Context)
	case argumentKindParameter:
		if len(raw) != 1 {
			return fmt.Errorf("compute parameter argument must contain only parameter")
		}
		a.Field, a.Value, a.Context = nil, nil, ""
		return json.Unmarshal(raw["parameter"], &a.Parameter)
	default:
		queryLog.Printf("compute argument decode rejected reason=missing-field-value-context")
		return fmt.Errorf("compute argument must contain field, value, or context")
	}
}

type ComputedField struct {
	As       string     `json:"as"`
	Function string     `json:"function"`
	Args     []Argument `json:"args"`
}

type Aggregate struct {
	By     []string         `json:"by,omitempty"`
	Values []AggregateValue `json:"values"`
}

type AggregateValue struct {
	Field   string  `json:"field"`
	As      string  `json:"as"`
	Reducer string  `json:"reducer"`
	Filter  *Filter `json:"filter,omitempty"`
}

type SelectedField struct {
	Field string `json:"field"`
	As    string `json:"as,omitempty"`
}

type OrderField struct {
	Field     string `json:"field"`
	Direction string `json:"direction,omitempty"`
}

type WindowField struct {
	Operation string       `json:"operation"`
	Field     string       `json:"field"`
	As        string       `json:"as"`
	OrderBy   []OrderField `json:"order-by"`
	GroupBy   []string     `json:"groupby,omitempty"`
	Frame     *int         `json:"frame,omitempty"`
	Reducer   string       `json:"reducer,omitempty"`
	Alignment string       `json:"alignment,omitempty"`
	Mode      string       `json:"mode,omitempty"`
	TimeField string       `json:"time-field,omitempty"`
	Unit      string       `json:"unit,omitempty"`
}

func (d *Definition) UnmarshalJSON(data []byte) error {
	type definition Definition
	var decoded definition
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if window, exists := raw["window"]; exists && strings.TrimSpace(string(window)) == "null" {
		return fmt.Errorf("window must contain between 1 and 8 entries")
	}
	*d = Definition(decoded)
	return nil
}

type TemporalSeries struct {
	Time       string            `json:"time"`
	Series     string            `json:"series"`
	Shape      string            `json:"shape,omitempty"`
	Link       string            `json:"link,omitempty"`
	Carry      []string          `json:"carry,omitempty"`
	PointCarry []string          `json:"point-carry,omitempty"`
	Measures   []TemporalMeasure `json:"measures,omitempty"`
	Maps       []TemporalMap     `json:"maps,omitempty"`
	Trend      *TemporalTrend    `json:"trend,omitempty"`
}

type TemporalTrend struct {
	Direction string `json:"direction"`
}

type TemporalMeasure struct {
	Field string `json:"field"`
	Key   string `json:"key,omitempty"`
	Kind  string `json:"kind"`
}

type TemporalMap struct {
	Field       string `json:"field"`
	Definitions string `json:"definitions,omitempty"`
	Group       string `json:"group,omitempty"`
	Kind        string `json:"kind"`
}
