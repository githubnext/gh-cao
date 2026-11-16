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
	Name       string `json:"name"`
	Subject    string `json:"subject"`
	Objective  string `json:"objective,omitempty"`
	Acceptance string `json:"acceptance,omitempty"`
	// Queries are earlier definitions in the same request. Each reads its
	// immediate predecessor (the first reads an archive source) and the
	// outer definition reads the last one, so a chain has no unused stages.
	Queries []Definition      `json:"queries,omitempty"`
	From    string            `json:"from"`
	Filter  *AnalyticalFilter `json:"filter,omitempty"`
	// Compute adds row-level values after filter and before window/aggregate,
	// following Dashboard Language order; filter cannot read them.
	Compute []ComputedValue `json:"compute,omitempty"`
	// Window adds partitioned row-level values to non-aggregate queries only.
	Window        []AnalyticalWindow    `json:"window,omitempty"`
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

// AnalyticalWindow is the Dashboard Language previous/row-number window
// (DLS §5.5.4). groupby and order-by must identify each row uniquely, so the
// order is total and backend sort stability never decides a result.
type AnalyticalWindow struct {
	Field     string             `json:"field,omitempty"`
	As        string             `json:"as"`
	Operation string             `json:"operation"`
	OrderBy   []query.OrderField `json:"order-by"`
	GroupBy   []string           `json:"groupby"`
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

var queryName = regexp.MustCompile(`^[a-z][a-z0-9]*(?:-[a-z0-9]+)*$`)
var outputName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9-]{0,127}$`)

const eventSource = "archive-events"

// itemSource flattens one approved structured collection from each active
// trace's last agent-phase trajectory.ir event. Every active trace yields an
// anchor row (null Ordinal) when the collection is absent, empty or invalid,
// so complete trace bindings remain visible without fabricating elements.
type itemSource struct {
	Name, Path, Key string
	Element         Column
}

var itemSources = []itemSource{
	{"archive-objectives", "data.objectives", "satisfiedAtEventIndex", Column{"SatisfiedAtEventIndex", "integer"}},
	{"archive-actions", "data.actions", "validAtIssueTime", Column{"ValidAtIssueTime", "boolean"}},
	{"archive-states", "data.states", "id", Column{"Id", "string"}},
}

var itemColumns = []Column{
	{"Namespace", "string"}, {"Repository", "string"}, {"RunId", "string"}, {"RunAttempt", "integer"},
	{"TraceId", "string"}, {"ProjectionId", "string"}, {"PolicyDigest", "string"}, {"Sequence", "integer"},
	{"Ordinal", "integer"}, {"Items", "integer"}, {"Kind", "string"},
}

var fieldStateInputs = []string{"PresentFields", "NullFields", "InvalidFields", "OmissionReasons"}

func lookupItemSource(name string) (itemSource, bool) {
	for _, item := range itemSources {
		if item.Name == name {
			return item, true
		}
	}
	return itemSource{}, false
}

// ItemSourceSpec exposes one approved item source's collection path, element
// key and typed element column, so every backend lowers the same contract.
func ItemSourceSpec(name string) (path, key string, element Column, ok bool) {
	item, ok := lookupItemSource(name)
	return item.Path, item.Key, item.Element, ok
}

// sourceColumns returns the ordered public vocabulary of one analytical source.
func sourceColumns(source string) ([]Column, bool) {
	if source == eventSource {
		fields := analyticalColumns()
		var columns []Column
		for _, name := range eventColumns {
			if kind := fields[name]; kind != "" {
				columns = append(columns, Column{Name: name, Type: kind})
			}
		}
		return columns, true
	}
	item, ok := lookupItemSource(source)
	if !ok {
		return nil, false
	}
	return append(slices.Clone(itemColumns), item.Element), true
}

// statePath returns the generated evidence path whose field states qualify a
// column. Item fields are qualified by their whole approved collection path.
func statePath(source, name string) string {
	if source == eventSource {
		for _, promoted := range evidenceFields {
			if promoted.Name == name && promoted.Index >= 0 {
				return promoted.Path
			}
		}
		return ""
	}
	if item, ok := lookupItemSource(source); ok && (name == "Items" || name == item.Element.Name) {
		return item.Path
	}
	return ""
}

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
	// Order is the explicit input-key order of a real sum over a stage.
	Order []Column
}

// PlannedWindow is one typed previous or row-number window. Field is empty
// for row-number. previous over a string yields "" on a partition's first
// row, because archive strings use "" rather than null for absence.
type PlannedWindow struct {
	Operation string
	Field     Column
	As        string
	Type      string
	Partition []Column
	Order     []PlannedOrder
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
	Source string
	// Input is the earlier stage this plan reads, or nil for an archive source.
	Input      *Plan
	Columns    []Column
	Inputs     []string
	Predicates []PlannedPredicate
	// Key identifies each output row: the aggregate groups, or for a row-level
	// plan the source trace binding plus Sequence/Ordinal, or the input key.
	Key             []Column
	RowComputations []PlannedComputation
	Windows         []PlannedWindow
	By              []Column
	Values          []PlannedValue
	Computations    []PlannedComputation
	Selections      []PlannedSelection
	Orders          []PlannedOrder
	Ties            []Column
	Limit           int
	ExplicitLimit   bool
}

type SourceCapability struct {
	Name   string   `json:"name"`
	Fields []Column `json:"fields"`
}

type Capabilities struct {
	LanguageVersion string   `json:"language-version"`
	Source          string   `json:"source"`
	Fields          []Column `json:"fields"`
	// Sources lists additional item sources. Backends without a reviewed
	// lowering omit them, so Require rejects those plans before execution.
	Sources     []SourceCapability `json:"sources,omitempty"`
	Predicates  []string           `json:"predicates"`
	Reducers    []string           `json:"reducers"`
	Functions   []string           `json:"functions"`
	FieldStates []string           `json:"fieldStates"`
	MaxRows     int                `json:"maxRows"`
	// Chaining is the maximum number of earlier definitions a query may read
	// through queries. Zero means the backend rejects chained queries.
	Chaining int `json:"chaining,omitempty"`
	// Compute, Windows and StateFamilies advertise row-level compute,
	// previous/row-number windows and structured-family state predicates.
	// Backends without an accepted lowering omit them and reject such plans.
	Compute       bool     `json:"compute,omitempty"`
	Windows       []string `json:"windows,omitempty"`
	StateFamilies []string `json:"stateFamilies,omitempty"`
}

// maxStages bounds the earlier definitions of one chained query.
const maxStages = 2

var windowOperations = []string{"previous", "row-number"}

// stateFamily returns the generated path of an approved structured family.
// Families have field states only; their values are never query inputs.
func stateFamily(name string) string {
	for _, family := range evidenceFields {
		if family.Index < 0 && family.Name == name {
			return family.Path
		}
	}
	return ""
}

func stateFamilies() []string {
	var names []string
	for _, family := range evidenceFields {
		if family.Index < 0 {
			names = append(names, family.Name)
		}
	}
	return names
}

// AnalyticalCapabilities advertises the item sources, query chaining,
// row-level compute, windows and structured-family states that both the
// DuckDB and the hosted KQL lowerings implement. The KQL lowering is accepted
// only through the real Kusto emulator replay.
func AnalyticalCapabilities(maxRows int) Capabilities {
	c := Capabilities{LanguageVersion: "0.2.0", Source: eventSource,
		Predicates: []string{"equals", "in", "gte", "lt", "state"},
		Reducers:   []string{"count", "distinct-count", "sum", "max", "first"},
		Functions:  scalarFunctions, FieldStates: fieldStates, MaxRows: maxRows, Chaining: maxStages}
	c.Fields, _ = sourceColumns(eventSource)
	c.Compute, c.Windows, c.StateFamilies = true, windowOperations, stateFamilies()
	for _, item := range itemSources {
		fields, _ := sourceColumns(item.Name)
		c.Sources = append(c.Sources, SourceCapability{Name: item.Name, Fields: fields})
	}
	return c
}

func (p *Plan) Require(c Capabilities) error {
	if c.LanguageVersion != "0.2.0" || c.Source != eventSource || c.MaxRows < 1 {
		return ErrQuery
	}
	if p.Stages() > c.Chaining {
		return ErrQuery
	}
	for stage := p; stage != nil; stage = stage.Input {
		if err := stage.require(c); err != nil {
			return err
		}
	}
	if p.ExplicitLimit && p.Limit > c.MaxRows {
		return ErrLimit
	}
	p.Limit = min(p.Limit, c.MaxRows)
	return nil
}

// Aggregate reports whether the plan reduces rows rather than keeping them.
func (p *Plan) Aggregate() bool {
	return len(p.Values) > 0
}

// Stages counts the earlier definitions this plan reads.
func (p *Plan) Stages() int {
	n := 0
	for stage := p.Input; stage != nil; stage = stage.Input {
		n++
	}
	return n
}

// Base returns the archive source plan at the start of a chain.
func (p *Plan) Base() *Plan {
	for p.Input != nil {
		p = p.Input
	}
	return p
}

func (p *Plan) require(c Capabilities) error {
	if p.Input != nil {
		// Stage fields were checked against the stage that produced them.
		return p.requireOperators(c)
	}
	advertised, found := c.Fields, p.Source == c.Source
	for _, source := range c.Sources {
		if !found && source.Name == p.Source {
			advertised, found = source.Fields, true
		}
	}
	columns, known := sourceColumns(p.Source)
	if !found || !known {
		return ErrQuery
	}
	fields := map[string]string{}
	for _, column := range columns {
		fields[column.Name] = column.Type
	}
	for _, name := range p.Inputs {
		if slices.Contains(fieldStateInputs, name) {
			continue
		}
		if !slices.ContainsFunc(advertised, func(field Column) bool { return field.Name == name && field.Type == fields[name] }) {
			return ErrQuery
		}
	}
	return p.requireOperators(c)
}

func (p *Plan) requireOperators(c Capabilities) error {
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
		if predicate.Field.Type == "structured" && !slices.Contains(c.StateFamilies, predicate.Field.Name) {
			return ErrQuery
		}
	}
	if len(p.RowComputations) > 0 && !c.Compute {
		return ErrQuery
	}
	for _, window := range p.Windows {
		if !slices.Contains(c.Windows, window.Operation) {
			return ErrQuery
		}
	}
	for _, computed := range append(slices.Clone(p.RowComputations), p.Computations...) {
		if !computed.Expression.require(c.Functions) {
			return ErrQuery
		}
	}
	for _, value := range p.Values {
		if !slices.Contains(c.Reducers, value.Reducer) {
			return ErrQuery
		}
	}
	return nil
}

// Plan validates the whole chain before any rows are read (DLS-QUERY-017):
// stage names are unique and never shadow a source, every stage reads only its
// immediate predecessor, and every stage output stays keyed by exact trace
// and projection identity.
func (d Definition) Plan() (Plan, error) {
	if len(d.Queries) > maxStages {
		return Plan{}, ErrQuery
	}
	names := map[string]bool{d.Name: true}
	var input *Plan
	for i, stage := range d.Queries {
		_, shadows := sourceColumns(stage.Name)
		// A stage either aggregates by complete trace binding with its default
		// output, or is row-level and selects its whole row key unrenamed.
		rowLevel := stage.Aggregate == nil
		if names[stage.Name] || shadows || len(stage.Queries) != 0 || stage.OrderBy != nil || stage.Limit != nil ||
			(i > 0 && stage.From != d.Queries[i-1].Name) ||
			rowLevel && stage.Select == nil || !rowLevel && (stage.Select != nil || !bindsTrace(stage.Aggregate.By)) {
			return Plan{}, ErrQuery
		}
		names[stage.Name] = true
		planned, err := stage.plan(input)
		if err != nil {
			return Plan{}, err
		}
		for _, key := range planned.Key {
			if !slices.ContainsFunc(planned.Selections, func(s PlannedSelection) bool { return s.Field.Name == key.Name && s.As == key.Name }) {
				return Plan{}, ErrQuery
			}
		}
		input = &planned
	}
	if input != nil && d.From != d.Queries[len(d.Queries)-1].Name {
		return Plan{}, ErrQuery
	}
	return d.plan(input)
}

func bindsTrace(by []string) bool {
	for _, binding := range traceBinding {
		if !slices.Contains(by, binding) {
			return false
		}
	}
	return true
}

func (d Definition) plan(input *Plan) (Plan, error) {
	p := Plan{Source: d.From, Input: input, Limit: 10000, ExplicitLimit: d.Limit != nil}
	columns, known := sourceColumns(d.From)
	if input != nil {
		columns, known = input.Columns, true
	}
	if !known || len(d.Name) > 128 || !queryName.MatchString(d.Name) ||
		strings.TrimSpace(d.Subject) == "" || utf8.RuneCountInString(d.Subject+d.Objective+d.Acceptance) > 512 ||
		len(d.Select) > 32 || (d.Select != nil && len(d.Select) == 0) || len(d.OrderBy) > 16 ||
		(d.Limit != nil && (*d.Limit < 1 || *d.Limit > 10000)) {
		return p, ErrQuery
	}
	if d.Limit != nil {
		p.Limit = *d.Limit
	}
	fields, needed := map[string]string{}, map[string]bool{}
	for _, column := range columns {
		fields[column.Name] = column.Type
	}
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
	// Structured families of the event source are state-only predicate
	// targets; comparisons fail because they have no scalar type.
	family := func(name string) bool {
		return input == nil && d.From == eventSource && stateFamily(name) != ""
	}
	readPredicate := func(name string) (Column, error) {
		if fields[name] == "" && family(name) {
			return Column{Name: name, Type: "structured"}, nil
		}
		return read(name)
	}
	planFilter := func(filter *AnalyticalFilter) ([]PlannedPredicate, error) {
		// Stage outputs have no field-state metadata, so state predicates are rejected there.
		return planPredicates(filter, readPredicate, func(name string) string {
			if input != nil {
				return ""
			}
			if fields[name] == "" && family(name) {
				return stateFamily(name)
			}
			return statePath(d.From, name)
		}, needed)
	}
	output := func(name string) bool {
		return outputName.MatchString(name) && !reserved[strings.ToLower(name)] && !family(name)
	}
	if d.Filter != nil {
		var err error
		p.Predicates, err = planFilter(d.Filter)
		if err != nil {
			return p, err
		}
	}
	if len(d.Compute) > 32 {
		return p, ErrQuery
	}
	rowNodes := 0
	for _, computed := range d.Compute {
		if !output(computed.As) {
			return p, ErrQuery
		}
		expression, err := (Expression{Function: computed.Function, Args: computed.Args}).plan(fields, 0, &rowNodes)
		if err != nil || !expression.rowLevel() {
			return p, ErrQuery
		}
		expression.visitFields(func(name string) { needed[name] = true })
		fields[computed.As] = expression.Type
		reserved[strings.ToLower(computed.As)] = true
		p.RowComputations = append(p.RowComputations, PlannedComputation{As: computed.As, Expression: expression})
	}
	available := map[string]string{}
	var ties []string
	switch {
	case d.Aggregate == nil:
		if len(d.Select) == 0 {
			return p, ErrQuery
		}
		for name, kind := range fields {
			available[name] = kind
		}
		ties = []string{"Repository", "RunId", "RunAttempt", "TraceId", "ProjectionId", "Sequence"}
		key := append(slices.Clone(traceBinding), "Sequence")
		if input != nil {
			ties = nil
			for _, column := range input.Key {
				ties = append(ties, column.Name)
			}
			p.Key = input.Key
		} else {
			if d.From != eventSource {
				ties[len(ties)-1], key[len(key)-1] = "Ordinal", "Ordinal"
			}
			for _, name := range key {
				p.Key = append(p.Key, Column{Name: name, Type: fields[name]})
			}
		}
		if len(d.Window) > 8 {
			return p, ErrQuery
		}
		for _, window := range d.Window {
			planned, err := window.plan(read, fields, p.Key)
			if err != nil || !output(window.As) {
				return p, ErrQuery
			}
			available[window.As] = planned.Type
			reserved[strings.ToLower(window.As)] = true
			p.Windows = append(p.Windows, planned)
		}
	case d.Window != nil:
		// Aggregated windows read a row-level or aggregate earlier stage instead.
		return p, ErrQuery
	default:
		a := d.Aggregate
		if len(a.By) > 16 || len(a.Values) < 1 || len(a.Values) > 16 || input != nil && !bindsTrace(a.By) {
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
		ties, p.Key = a.By, p.By
		for _, value := range a.Values {
			field, err := read(value.Field)
			if err != nil || !output(value.As) {
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
			var order []Column
			switch {
			case ordered && input != nil:
				// A stage row is unique by its input key; a real sum orders by the
				// key fields not grouped here, preserving sequential IEEE arithmetic.
				if value.Reducer == "first" {
					return p, ErrQuery
				}
				var remaining []string
				for _, key := range input.Key {
					if !slices.Contains(a.By, key.Name) {
						remaining = append(remaining, key.Name)
					}
				}
				if len(value.OrderBy) != len(remaining) {
					return p, ErrQuery
				}
				for i, item := range value.OrderBy {
					if item.Field != remaining[i] || item.Direction != "" && item.Direction != "asc" {
						return p, ErrQuery
					}
					column, err := read(item.Field)
					if err != nil {
						return p, err
					}
					order = append(order, column)
				}
			case ordered:
				// Item rows have no reviewed element order; ordered reducers stay event-only.
				if d.From != eventSource {
					return p, ErrQuery
				}
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
			case len(value.OrderBy) != 0:
				return p, ErrQuery
			}
			predicates, err := planFilter(value.Filter)
			if err != nil {
				return p, err
			}
			available[value.As] = kind
			reserved[strings.ToLower(value.As)] = true
			p.Values = append(p.Values, PlannedValue{Field: field, As: value.As, Reducer: value.Reducer, Predicates: predicates, Order: order})
		}
	}
	if len(d.PostAggregate) > 32 || len(d.PostAggregate) > 0 && d.Aggregate == nil {
		return p, ErrQuery
	}
	nodes := 0
	for _, computed := range d.PostAggregate {
		if !output(computed.As) {
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
	inputs := eventColumns
	if input != nil {
		inputs = nil
		for _, column := range columns {
			inputs = append(inputs, column.Name)
		}
	} else if d.From != eventSource {
		inputs = nil
		for _, column := range columns {
			inputs = append(inputs, column.Name)
		}
		inputs = append(inputs, fieldStateInputs...)
	}
	for _, name := range inputs {
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

// plan types one window. Partitions must keep complete trace binding, and
// groupby plus order-by must cover the row key so the order is total.
func (w AnalyticalWindow) plan(read func(string) (Column, error), fields map[string]string, key []Column) (PlannedWindow, error) {
	p := PlannedWindow{Operation: w.Operation, As: w.As}
	if len(w.GroupBy) < 1 || len(w.GroupBy) > 8 || len(w.OrderBy) < 1 || len(w.OrderBy) > 8 {
		return p, ErrQuery
	}
	covered := map[string]bool{}
	for _, name := range w.GroupBy {
		column, err := read(name)
		if err != nil || covered[name] {
			return p, ErrQuery
		}
		covered[name] = true
		p.Partition = append(p.Partition, column)
	}
	if !bindsTrace(w.GroupBy) {
		return p, ErrQuery
	}
	for _, order := range w.OrderBy {
		column, err := read(order.Field)
		if err != nil || covered[order.Field] || order.Direction != "" && order.Direction != "asc" && order.Direction != "desc" {
			return p, ErrQuery
		}
		covered[order.Field] = true
		direction := order.Direction
		if direction == "" {
			direction = "asc"
		}
		p.Order = append(p.Order, PlannedOrder{Field: column, Direction: direction})
	}
	for _, column := range key {
		if !covered[column.Name] {
			return p, ErrQuery
		}
	}
	switch w.Operation {
	case "row-number":
		// Dashboard Language accepts an unused field on row-number.
		if w.Field != "" && fields[w.Field] == "" {
			return p, ErrQuery
		}
		p.Type = "integer"
	case "previous":
		column, err := read(w.Field)
		if err != nil {
			return p, ErrQuery
		}
		p.Field, p.Type = column, column.Type
	default:
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

func (predicate AnalyticalPredicate) plan(field Column, path string) (PlannedPredicate, error) {
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
		if path == "" {
			return p, ErrQuery
		}
		p.State, p.Path = predicate.State, path
		return p, nil
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

func planPredicates(filter *AnalyticalFilter, read func(string) (Column, error), path func(string) string, needed map[string]bool) ([]PlannedPredicate, error) {
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
		planned, err := predicate.plan(field, path(field.Name))
		if err != nil {
			return nil, err
		}
		if planned.State != "" {
			for _, name := range fieldStateInputs {
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
