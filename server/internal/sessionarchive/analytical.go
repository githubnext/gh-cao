package sessionarchive

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"maps"
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
	// Queries are earlier definitions in the same request. Each reads an
	// archive source or any earlier stage through from or joins, and every
	// stage is read by a later stage or the outer definition.
	Queries []Definition `json:"queries,omitempty"`
	From    string       `json:"from"`
	// Joins import fields from an archive source or earlier stage before
	// filter, on complete trace binding plus optional typed equality keys.
	Joins  []AnalyticalJoin  `json:"joins,omitempty"`
	Filter *AnalyticalFilter `json:"filter,omitempty"`
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

// AnalyticalJoin is a trace-scoped Dashboard Language equality join
// (DLS-QUERY-005..008). Both sides always match on JoinBinding; on adds
// typed integer or string keys. The right side must be unique on the
// binding plus its on keys, so a join never multiplies left rows.
type AnalyticalJoin struct {
	Source string        `json:"source"`
	Type   string        `json:"type,omitempty"`
	On     []JoinKey     `json:"on,omitempty"`
	Fields []JoinedField `json:"fields"`
}

type JoinKey struct {
	Left  string `json:"left"`
	Right string `json:"right"`
}

type JoinedField struct {
	Field string `json:"field"`
	As    string `json:"as,omitempty"`
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

// ItemSource flattens one approved structured collection from each active
// trace's last agent-phase trajectory.ir event. Every active trace yields an
// anchor row (null Ordinal) when the collection is absent, empty or invalid,
// so complete trace bindings remain visible without fabricating elements.
// A nested source flattens one array field of each collection element into
// rows keyed by the parent Ordinal plus Position; its trace anchor (null
// Ordinal and Position) appears when the trace has no nested elements.
type ItemSource struct {
	Name, Path string
	Nested     string
	Elements   []ItemElement
}

// ItemElement types one element field. Key names the JSON key of a record
// element; an empty Key reads a nested element itself. Typing is one of
// unsigned or signed (safe integers), boolean, string (record string, else
// ""), visit (a string element or a record's string id, else "") or length
// (an array's length; invalid when present, non-null and not an array).
type ItemElement struct {
	Key    string
	Column Column
	Typing string
}

var itemSources = []ItemSource{
	{Name: "archive-objectives", Path: "data.objectives", Elements: []ItemElement{
		{"satisfiedAtEventIndex", Column{"SatisfiedAtEventIndex", "integer"}, "unsigned"},
		{"id", Column{"Id", "string"}, "string"},
		{"dependsOn", Column{"DependsOn", "integer"}, "length"}}},
	{Name: "archive-objective-dependencies", Path: "data.objectives", Nested: "dependsOn", Elements: []ItemElement{
		{"", Column{"Value", "string"}, "string"}}},
	{Name: "archive-actions", Path: "data.actions", Elements: []ItemElement{
		{"validAtIssueTime", Column{"ValidAtIssueTime", "boolean"}, "boolean"}}},
	{Name: "archive-states", Path: "data.states", Elements: []ItemElement{
		{"id", Column{"Id", "string"}, "visit"},
		{"firstEventIndex", Column{"FirstEventIndex", "integer"}, "signed"}}},
	{Name: "archive-ir-events", Path: "data.events", Elements: []ItemElement{
		{"index", Column{"Index", "integer"}, "signed"},
		{"kind", Column{"EventKind", "string"}, "string"},
		{"ref", Column{"Ref", "string"}, "string"}}},
	{Name: "archive-observations", Path: "data.observations", Elements: []ItemElement{
		{"sourceToolCallId", Column{"SourceToolCallId", "string"}, "string"},
		{"consumedByActionIds", Column{"ConsumedByActionIds", "integer"}, "length"}}},
	{Name: "archive-observation-consumers", Path: "data.observations", Nested: "consumedByActionIds", Elements: []ItemElement{
		{"", Column{"Value", "string"}, "string"}}},
	{Name: "archive-tool-calls", Path: "data.toolCalls", Elements: []ItemElement{
		{"id", Column{"Id", "string"}, "string"}}},
}

var itemColumns = []Column{
	{"Namespace", "string"}, {"Repository", "string"}, {"RunId", "string"}, {"RunAttempt", "integer"},
	{"TraceId", "string"}, {"ProjectionId", "string"}, {"PolicyDigest", "string"}, {"Sequence", "integer"},
	{"Ordinal", "integer"}, {"Items", "integer"}, {"Kind", "string"}, {"LargestCollection", "integer"},
}

// GuardedCollections are the trajectory collections whose length upstream's
// MAX_TRAJECTORY_ITEMS guard inspects and that the archive can approve.
// LargestCollection is null when any of them, or the IR data itself, is
// excluded, or when reference is redacted.
var GuardedCollections = []string{"events", "states", "actions", "toolCalls", "observations", "resources", "provenanceEdges", "objectives", "reference"}

// GuardedOmissions lists the omission reasons that make LargestCollection unverifiable.
func GuardedOmissions() []string {
	reasons := make([]string, 0, 2+len(GuardedCollections))
	reasons = append(reasons, "data:excluded", "data.reference:redacted")
	for _, key := range GuardedCollections {
		reasons = append(reasons, "data."+key+":excluded")
	}
	return reasons
}

// ReconciledToolTypes bound upstream's toolCalls fallback: when the IR
// toolCalls is absent or null, trajectoryIR.toolCalls holds reconciled calls,
// at most one per agent-phase event of these types. (An absent IR events key
// falls back to one synthesized event per agent-phase agent-component event.)
var ReconciledToolTypes = []string{"tool.execution_start", "tool.execution_complete", "mcp.tool_call", "mcp.rpc.request", "mcp.rpc.response"}

var fieldStateInputs = []string{"PresentFields", "NullFields", "InvalidFields", "OmissionReasons"}

func lookupItemSource(name string) (ItemSource, bool) {
	for _, item := range itemSources {
		if item.Name == name {
			return item, true
		}
	}
	return ItemSource{}, false
}

// ItemSourceSpec exposes one approved item source's collection path, nested
// field and typed elements, so every backend lowers the same contract.
func ItemSourceSpec(name string) (ItemSource, bool) {
	return lookupItemSource(name)
}

// Columns returns the ordered public vocabulary of the item source.
func (item ItemSource) Columns() []Column {
	columns := slices.Clone(itemColumns)
	if item.Nested != "" {
		columns = append(columns, Column{"Position", "integer"})
	}
	for _, element := range item.Elements {
		columns = append(columns, element.Column)
	}
	return columns
}

// sourceKey names the columns that identify one row of an archive source.
func sourceKey(source string) []string {
	key := slices.Clone(traceBinding)
	if source == eventSource {
		return append(key, "Sequence")
	}
	key = append(key, "Ordinal")
	if item, _ := lookupItemSource(source); item.Nested != "" {
		key = append(key, "Position")
	}
	return key
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
	return item.Columns(), true
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
	if item, ok := lookupItemSource(source); ok {
		if name == "Items" || slices.ContainsFunc(item.Elements, func(e ItemElement) bool { return e.Column.Name == name }) {
			return item.Path
		}
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

// PlannedJoin imports Fields from an archive source (Input nil, reading
// Inputs) or an earlier stage. Binding keys use null-safe equality; other
// keys never match a missing right value (null, or "" for strings).
type PlannedJoin struct {
	Type   string
	Source string
	Input  *Plan
	Inputs []string
	On     []PlannedJoinKey
	Fields []PlannedSelection
}

type PlannedJoinKey struct {
	Left, Right Column
	Binding     bool
}

// JoinBinding is the complete trace binding every join matches implicitly.
var JoinBinding = append(slices.Clone(traceBinding), "PolicyDigest")

// Plan contains typed operations only. Storage membership and backend syntax
// belong to the archive adapters, never the caller's query definition.
type Plan struct {
	// Name is the earlier definition's name; empty for the outer definition.
	Name   string
	Source string
	// Input is the earlier stage this plan reads, or nil for an archive source.
	Input      *Plan
	Joins      []PlannedJoin
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
	// Joins is the maximum number of joins one definition may declare. Zero
	// means the backend rejects joins.
	Joins int `json:"joins,omitempty"`
	// Compute, Windows and StateFamilies advertise row-level compute,
	// previous/row-number windows and structured-family state predicates.
	// Backends without an accepted lowering omit them and reject such plans.
	Compute       bool     `json:"compute,omitempty"`
	Windows       []string `json:"windows,omitempty"`
	StateFamilies []string `json:"stateFamilies,omitempty"`
}

// maxStages bounds the earlier definitions of one chained query; maxJoins
// bounds the joins of one definition.
const (
	maxStages = 8
	maxJoins  = 4
)

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
		Functions:  scalarFunctions, FieldStates: fieldStates, MaxRows: maxRows, Chaining: maxStages, Joins: maxJoins}
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
	for _, stage := range p.Dependencies() {
		if len(stage.Joins) > c.Joins {
			return ErrQuery
		}
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
	return len(p.Dependencies()) - 1
}

// Dependencies returns every distinct plan this plan reads through from or
// joins, then the plan itself, so each plan follows the plans it reads.
func (p *Plan) Dependencies() []*Plan {
	var out []*Plan
	seen := map[*Plan]bool{}
	var visit func(*Plan)
	visit = func(q *Plan) {
		if q == nil || seen[q] {
			return
		}
		seen[q] = true
		visit(q.Input)
		for _, join := range q.Joins {
			visit(join.Input)
		}
		out = append(out, q)
	}
	visit(p)
	return out
}

// SourceInputs returns every archive source the plan reads, starting with
// its own base source, and the union of each source's inputs in source
// column order. TraceId and Gaps are always read for coverage metadata.
func (p *Plan) SourceInputs() ([]string, map[string][]string) {
	sources := []string{p.Base().Source}
	needed := map[string]map[string]bool{}
	add := func(source string, inputs []string) {
		if !slices.Contains(sources, source) {
			sources = append(sources, source)
		}
		if needed[source] == nil {
			needed[source] = map[string]bool{"TraceId": true, "Gaps": true}
		}
		for _, name := range inputs {
			needed[source][name] = true
		}
	}
	add(sources[0], nil)
	for _, stage := range p.Dependencies() {
		if stage.Input == nil {
			add(stage.Source, stage.Inputs)
		}
		for _, join := range stage.Joins {
			if join.Input == nil {
				add(join.Source, join.Inputs)
			}
		}
	}
	inputs := map[string][]string{}
	for _, source := range sources {
		order := eventColumns
		if source != eventSource {
			order = nil
			columns, _ := sourceColumns(source)
			for _, column := range columns {
				order = append(order, column.Name)
			}
			order = append(order, "Gaps")
			order = append(order, fieldStateInputs...)
		}
		for _, name := range order {
			if needed[source][name] {
				inputs[source] = append(inputs[source], name)
			}
		}
	}
	return sources, inputs
}

// Base returns the archive source plan at the start of a chain.
func (p *Plan) Base() *Plan {
	for p.Input != nil {
		p = p.Input
	}
	return p
}

func (p *Plan) require(c Capabilities) error {
	// Stage fields were checked against the stage that produced them.
	if p.Input == nil {
		if err := requireSource(c, p.Source, p.Inputs); err != nil {
			return err
		}
	}
	for _, join := range p.Joins {
		if join.Input == nil {
			if err := requireSource(c, join.Source, join.Inputs); err != nil {
				return err
			}
		}
	}
	return p.requireOperators(c)
}

func requireSource(c Capabilities, source string, inputs []string) error {
	advertised, found := c.Fields, source == c.Source
	for _, candidate := range c.Sources {
		if !found && candidate.Name == source {
			advertised, found = candidate.Fields, true
		}
	}
	columns, known := sourceColumns(source)
	if !found || !known {
		return ErrQuery
	}
	fields := map[string]string{}
	for _, column := range columns {
		fields[column.Name] = column.Type
	}
	for _, name := range inputs {
		if slices.Contains(fieldStateInputs, name) || name == "Gaps" {
			continue
		}
		if !slices.ContainsFunc(advertised, func(field Column) bool { return field.Name == name && field.Type == fields[name] }) {
			return ErrQuery
		}
	}
	return nil
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

// Plan validates every stage before any rows are read (DLS-QUERY-017):
// stage names are unique and never shadow a source, each stage reads only an
// archive source or earlier stages, every stage is read by a later stage or
// the outer definition, and every stage output stays keyed by exact trace
// and projection identity.
func (d Definition) Plan() (Plan, error) {
	if len(d.Queries) > maxStages {
		return Plan{}, ErrQuery
	}
	names := map[string]bool{d.Name: true}
	stages := map[string]*Plan{}
	var ordered []*Plan
	for _, stage := range d.Queries {
		_, shadows := sourceColumns(stage.Name)
		// A stage either aggregates by complete trace binding with its default
		// output, or is row-level and outputs its whole row key unrenamed.
		rowLevel := stage.Aggregate == nil
		if names[stage.Name] || shadows || len(stage.Queries) != 0 || stage.OrderBy != nil || stage.Limit != nil ||
			!rowLevel && (stage.Select != nil || !bindsTrace(stage.Aggregate.By)) {
			return Plan{}, ErrQuery
		}
		names[stage.Name] = true
		planned, err := stage.plan(stages, true)
		if err != nil {
			return Plan{}, err
		}
		for _, key := range planned.Key {
			if !slices.ContainsFunc(planned.Selections, func(s PlannedSelection) bool { return s.Field.Name == key.Name && s.As == key.Name }) {
				return Plan{}, ErrQuery
			}
		}
		planned.Name = stage.Name
		stages[stage.Name] = &planned
		ordered = append(ordered, &planned)
	}
	p, err := d.plan(stages, false)
	if err != nil {
		return p, err
	}
	used := map[*Plan]bool{}
	for _, plan := range append(slices.Clone(ordered), &p) {
		used[plan.Input] = true
		for _, join := range plan.Joins {
			used[join.Input] = true
		}
	}
	for _, stage := range ordered {
		if !used[stage] {
			return Plan{}, ErrQuery
		}
	}
	return p, nil
}

func bindsTrace(by []string) bool {
	for _, binding := range traceBinding {
		if !slices.Contains(by, binding) {
			return false
		}
	}
	return true
}

// plan types one definition. stages holds the earlier definitions it may
// read by name; stage marks an earlier definition rather than the outer one.
func (d Definition) plan(stages map[string]*Plan, stage bool) (Plan, error) {
	input := stages[d.From]
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
	// order lists fields in stable output order for a stage's default select.
	var order []string
	for _, column := range columns {
		fields[column.Name] = column.Type
		order = append(order, column.Name)
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
	imported := map[string]bool{}
	planFilter := func(filter *AnalyticalFilter) ([]PlannedPredicate, error) {
		// Stage outputs and joined fields have no field-state metadata, so state predicates are rejected there.
		return planPredicates(filter, readPredicate, func(name string) string {
			if input != nil || imported[name] {
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
	if len(d.Joins) > maxJoins {
		return p, ErrQuery
	}
	for _, join := range d.Joins {
		planned, err := join.plan(stages, read)
		if err != nil {
			return p, err
		}
		for _, field := range planned.Fields {
			if !output(field.As) || fields[field.As] != "" {
				return p, ErrQuery
			}
			fields[field.As] = field.Field.Type
			reserved[strings.ToLower(field.As)], imported[field.As] = true, true
			order = append(order, field.As)
		}
		p.Joins = append(p.Joins, planned)
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
		order = append(order, computed.As)
		p.RowComputations = append(p.RowComputations, PlannedComputation{As: computed.As, Expression: expression})
	}
	available := map[string]string{}
	var ties []string
	switch {
	case d.Aggregate == nil:
		if len(d.Select) == 0 && !stage {
			return p, ErrQuery
		}
		for name, kind := range fields {
			available[name] = kind
		}
		if input != nil {
			for _, column := range input.Key {
				ties = append(ties, column.Name)
			}
			p.Key = input.Key
		} else {
			key := sourceKey(d.From)
			// Namespace is fixed by the request scope.
			ties = key[1:]
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
			order = append(order, window.As)
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
	if len(selected) == 0 && d.Aggregate == nil {
		// A row-level stage outputs every available field by default.
		for _, name := range order {
			selected = append(selected, query.SelectedField{Field: name})
		}
	} else if len(selected) == 0 {
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
	if len(selected) > 32 && (!stage || d.Select != nil) {
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

// plan types one join. The left side reads the definition's fields,
// including earlier imports; the right side is an archive source or earlier
// stage that must be unique on JoinBinding plus the explicit right keys.
func (j AnalyticalJoin) plan(stages map[string]*Plan, read func(string) (Column, error)) (PlannedJoin, error) {
	p := PlannedJoin{Type: j.Type, Source: j.Source, Input: stages[j.Source]}
	if p.Type == "" {
		p.Type = "inner"
	}
	var columns []Column
	var key []string
	if p.Input != nil {
		columns = p.Input.Columns
		for _, column := range p.Input.Key {
			key = append(key, column.Name)
		}
	} else {
		var known bool
		if columns, known = sourceColumns(j.Source); !known {
			return p, ErrQuery
		}
		key = sourceKey(j.Source)
	}
	right := map[string]string{}
	for _, column := range columns {
		right[column.Name] = column.Type
	}
	if p.Type != "inner" && p.Type != "left" || len(j.On) > 4 || len(j.Fields) < 1 || len(j.Fields) > 16 {
		return p, ErrQuery
	}
	matched := map[string]bool{}
	pair := func(left, name string, binding bool) error {
		column, err := read(left)
		if err != nil || right[name] != column.Type || matched[name] {
			return ErrQuery
		}
		if !binding && column.Type != "integer" && column.Type != "string" {
			return ErrQuery
		}
		matched[name] = true
		p.On = append(p.On, PlannedJoinKey{Left: column, Right: Column{Name: name, Type: column.Type}, Binding: binding})
		return nil
	}
	for _, name := range JoinBinding {
		if err := pair(name, name, true); err != nil {
			return p, err
		}
	}
	for _, on := range j.On {
		if slices.Contains(JoinBinding, on.Left) {
			return p, ErrQuery
		}
		if err := pair(on.Left, on.Right, false); err != nil {
			return p, err
		}
	}
	// Uniqueness: every right row key column is matched, so each left row
	// meets at most one right row.
	for _, name := range key {
		if !matched[name] {
			return p, ErrQuery
		}
	}
	needed := maps.Clone(matched)
	for _, field := range j.Fields {
		as := field.As
		if as == "" {
			as = field.Field
		}
		if right[field.Field] == "" || needed[field.Field] && !matched[field.Field] {
			return p, ErrQuery
		}
		needed[field.Field] = true
		p.Fields = append(p.Fields, PlannedSelection{Field: Column{Name: field.Field, Type: right[field.Field]}, As: as})
	}
	if p.Input == nil {
		for _, column := range columns {
			if needed[column.Name] {
				p.Inputs = append(p.Inputs, column.Name)
			}
		}
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
