package sessionarchive

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

func identifier(name string) string { return `"` + strings.ReplaceAll(name, `"`, `""`) + `"` }

func analyticalLiteral(value Scalar) string {
	switch value.Type {
	case "string":
		return literal(value.String)
	case "integer":
		return strconv.FormatInt(value.Integer, 10)
	case "number":
		return strconv.FormatFloat(value.Number, 'g', -1, 64)
	case "boolean":
		return strconv.FormatBool(value.Boolean)
	default:
		panic("unvalidated analytical scalar")
	}
}

func (p PlannedPredicate) sql() string {
	field := identifier(p.Field.Name)
	if p.State != "" {
		present := "list_contains(PresentFields," + literal(p.Field.Name) + ")"
		null := "list_contains(NullFields," + literal(p.Field.Name) + ")"
		invalid := "list_contains(InvalidFields," + literal(p.Field.Name) + ")"
		excluded, redacted := p.omissionSQL("excluded"), p.omissionSQL("redacted")
		switch p.State {
		case "present":
			return present
		case "absent":
			return "(NOT " + present + " AND NOT " + excluded + " AND NOT " + redacted + ")"
		case "null":
			return null
		case "invalid":
			return invalid
		case "excluded":
			return excluded
		case "redacted":
			return redacted
		case "valid":
			return "(" + present + " AND NOT " + null + " AND NOT " + invalid + " AND NOT " + excluded + " AND NOT " + redacted + ")"
		}
	}
	if p.Equals != nil {
		return field + "=" + analyticalLiteral(*p.Equals)
	}
	if p.In != nil {
		var values []string
		for _, value := range p.In {
			values = append(values, analyticalLiteral(value))
		}
		return field + " IN (" + strings.Join(values, ",") + ")"
	}
	parts := []string{field + " IS NOT NULL"}
	if p.Field.Type == "string" {
		parts = append(parts, field+"<>''")
	}
	for _, bound := range []struct {
		value    *Scalar
		operator string
	}{{p.GTE, ">="}, {p.LT, "<"}} {
		if bound.value == nil {
			continue
		}
		parts = append(parts, field+bound.operator+analyticalLiteral(*bound.value))
	}
	return "(" + strings.Join(parts, " AND ") + ")"
}

// Omission ancestors are generated paths, never user-selected JSON traversal.
func (p PlannedPredicate) OmissionPaths(kind string) []string {
	paths := []string{p.Path + ":" + kind}
	if kind == "excluded" {
		for path, _, ok := strings.Cut(p.Path, "."); ok; {
			paths = append(paths, path+":"+kind)
			remaining := strings.TrimPrefix(p.Path, path+".")
			child, _, more := strings.Cut(remaining, ".")
			if !more {
				break
			}
			path += "." + child
		}
	}
	return paths
}

func (p PlannedPredicate) omissionSQL(kind string) string {
	paths := p.OmissionPaths(kind)
	terms := make([]string, 0, len(paths))
	for _, path := range paths {
		terms = append(terms, "list_contains(OmissionReasons,"+literal(path)+")")
	}
	return "(" + strings.Join(terms, " OR ") + ")"
}

func predicatesSQL(predicates []PlannedPredicate) string {
	parts := make([]string, 0, len(predicates))
	for _, predicate := range predicates {
		parts = append(parts, predicate.sql())
	}
	if len(parts) == 0 {
		return "true"
	}
	return strings.Join(parts, " AND ")
}

func (e PlannedExpression) sql() string {
	if e.Field != "" {
		return identifier(e.Field)
	}
	if e.Function == "" {
		if e.Literal == nil {
			kind := map[string]string{"number": "DOUBLE", "integer": "BIGINT", "boolean": "BOOLEAN", "string": "VARCHAR"}[e.Type]
			return "CAST(NULL AS " + kind + ")"
		}
		return analyticalLiteral(*e.Literal)
	}
	var args []string
	for _, arg := range e.Args {
		args = append(args, arg.sql())
	}
	switch e.Function {
	case "and":
		return "(coalesce(" + args[0] + ",false) AND coalesce(" + args[1] + ",false))"
	case "if":
		return "(CASE WHEN " + args[0] + " THEN " + args[1] + " ELSE " + args[2] + " END)"
	case "is-null":
		return "(" + args[0] + " IS NULL)"
	case "equals", "gt", "lte":
		op := map[string]string{"equals": "=", "gt": ">", "lte": "<="}[e.Function]
		return "coalesce((" + args[0] + op + args[1] + "),false)"
	case "coalesce":
		return "coalesce(" + strings.Join(args, ",") + ")"
	case "sum":
		return "(CAST(" + args[0] + " AS DOUBLE)+CAST(" + args[1] + " AS DOUBLE))"
	case "difference":
		return "(CAST(" + args[0] + " AS DOUBLE)-CAST(" + args[1] + " AS DOUBLE))"
	case "product":
		return "(CAST(" + args[0] + " AS DOUBLE)*CAST(" + args[1] + " AS DOUBLE))"
	case "quotient":
		return "(CAST(" + args[0] + " AS DOUBLE)/nullif(CAST(" + args[1] + " AS DOUBLE),0))"
	}
	panic("unvalidated analytical expression")
}

func sqlOrder(field Column, direction string) string {
	expression := identifier(field.Name)
	if field.Type == "string" {
		expression = "nullif(" + expression + ",'')"
	}
	return expression + " " + direction + " NULLS LAST"
}

// stageSQL lowers join, filter, reduce, and post-aggregate phases to CTEs
// named with prefix that read from, joining rights in join order. It returns
// the CTE text and the last CTE name.
func (p Plan) stageSQL(prefix, from string, rights []string) (string, string) {
	filter := "true"
	predicates := make([]string, 0, len(p.Predicates))
	groups := make([]string, 0, len(p.By))
	for _, predicate := range p.Predicates {
		predicates = append(predicates, predicate.sql())
	}
	if len(predicates) > 0 {
		filter = strings.Join(predicates, " AND ")
	}
	expressions := map[string]string{}
	for _, value := range p.Values {
		expression := identifier(value.Field.Name)
		if value.Field.Type == "string" {
			expression = "nullif(" + expression + ",'')"
		}
		filter := " FILTER (WHERE " + predicatesSQL(value.Predicates) + ")"
		switch value.Reducer {
		case "count":
			expression = "count(" + expression + ")" + filter
		case "distinct-count":
			expression = "count(DISTINCT " + expression + ")" + filter
		case "max":
			expression = "max(" + expression + ")" + filter
		case "first":
			expression = "list_extract(list(" + expression + " ORDER BY Sequence)" + filter + ",1)"
		case "sum":
			if value.Field.Type == "integer" {
				// sum(BIGINT) is wide integer arithmetic; convert once like upstream BigInt -> Number.
				expression = "CAST(sum(" + expression + ")" + filter + " AS DOUBLE)"
			} else {
				order := "Sequence"
				if p.Input != nil {
					keys := make([]string, 0, len(value.Order))
					for _, key := range value.Order {
						keys = append(keys, sqlOrder(key, "asc"))
					}
					order = strings.Join(keys, ",")
				}
				valid := predicatesSQL(value.Predicates) + " AND " + expression + " IS NOT NULL"
				expression = "(CASE WHEN count(" + expression + ")" + filter + ">0 THEN list_reduce(list(" +
					expression + " ORDER BY " + order + ") FILTER (WHERE " + valid + "),lambda a,b: a+b,0::DOUBLE) ELSE NULL END)"
			}
		}
		expressions[value.As] = expression
	}
	base := make([]string, 0, len(p.By)+len(p.Values))
	for _, field := range p.By {
		base = append(base, identifier(field.Name))
		groups = append(groups, identifier(field.Name))
	}
	for _, value := range p.Values {
		base = append(base, expressions[value.As]+" AS "+identifier(value.As))
	}
	group := ""
	if len(groups) > 0 {
		group = " GROUP BY " + strings.Join(groups, ",")
	}
	ctes := ""
	for i, join := range p.Joins {
		next := prefix + "joined_" + strconv.Itoa(i)
		ctes += join.sql(next, from, rights[i]) + ",\n"
		from = next
	}
	ctes += prefix + "filtered AS (SELECT * FROM " + from + " WHERE " + filter + ")"
	rows := prefix + "filtered"
	for i, computed := range p.RowComputations {
		next := prefix + "rowcomputed_" + strconv.Itoa(i)
		ctes += ",\n" + next + " AS (SELECT *," + computed.Expression.sql() + " AS " + identifier(computed.As) + " FROM " + rows + ")"
		rows = next
	}
	if len(p.Windows) > 0 {
		windows := make([]string, 0, len(p.Windows))
		for _, window := range p.Windows {
			windows = append(windows, window.sql()+" AS "+identifier(window.As))
		}
		ctes += ",\n" + prefix + "windowed AS (SELECT *," + strings.Join(windows, ",") + " FROM " + rows + ")"
		rows = prefix + "windowed"
	}
	reduced := "SELECT * FROM " + rows
	if len(p.Values) > 0 {
		reduced = "SELECT " + strings.Join(base, ",") + " FROM " + rows + group
	}
	ctes += ",\n" + prefix + "reduced AS (" + reduced + ")"
	last := prefix + "reduced"
	for i, computed := range p.Computations {
		next := prefix + "computed_" + strconv.Itoa(i)
		ctes += ",\n" + next + " AS (SELECT *," + computed.Expression.sql() + " AS " + identifier(computed.As) + " FROM " + last + ")"
		last = next
	}
	if p.Aggregate() || prefix == "" {
		return ctes, last
	}
	// A row-level stage exposes only its selected row, including its key.
	selected := make([]string, 0, len(p.Selections))
	for _, selection := range p.Selections {
		selected = append(selected, identifier(selection.Field.Name)+" AS "+identifier(selection.As))
	}
	ctes += ",\n" + prefix + "selected AS (SELECT " + strings.Join(selected, ",") + " FROM " + last + ")"
	return ctes, prefix + "selected"
}

// sql lowers one join to a CTE named name. Binding keys use null-safe
// equality; right rows with a missing explicit key (null, or "" for a
// string) are removed first because Kusto matches "" with "". Imported
// strings coalesce to "" like every other string value.
func (j PlannedJoin) sql(name, left, right string) string {
	projected := make([]string, 0, len(j.On)+len(j.Fields))
	conditions := make([]string, 0, len(j.On))
	present := []string{"true"}
	for i, key := range j.On {
		alias := identifier("__archive_key_" + strconv.Itoa(i))
		projected = append(projected, identifier(key.Right.Name)+" AS "+alias)
		l := "l." + identifier(key.Left.Name)
		switch {
		case key.Binding && key.Left.Type == "string":
			conditions = append(conditions, "coalesce("+l+",'')=coalesce(r."+alias+",'')")
		case key.Binding:
			conditions = append(conditions, l+" IS NOT DISTINCT FROM r."+alias)
		default:
			conditions = append(conditions, l+"=r."+alias)
			present = append(present, identifier(key.Right.Name)+" IS NOT NULL")
			if key.Right.Type == "string" {
				present = append(present, identifier(key.Right.Name)+"<>''")
			}
		}
	}
	imported := make([]string, 0, len(j.Fields))
	for i, field := range j.Fields {
		alias := identifier("__archive_field_" + strconv.Itoa(i))
		projected = append(projected, identifier(field.Field.Name)+" AS "+alias)
		value := "r." + alias
		if field.Field.Type == "string" {
			value = "coalesce(" + value + ",'')"
		}
		imported = append(imported, value+" AS "+identifier(field.As))
	}
	kind := "INNER"
	if j.Type == "left" {
		kind = "LEFT"
	}
	return name + " AS (SELECT l.*," + strings.Join(imported, ",") + " FROM " + left + " l " + kind + " JOIN (SELECT " +
		strings.Join(projected, ",") + " FROM " + right + " WHERE " + strings.Join(present, " AND ") + ") r ON " +
		strings.Join(conditions, " AND ") + ")"
}

// sql lowers one window over a total order: groupby plus order-by covers the
// row key, so DuckDB's sort stability never decides a value.
func (w PlannedWindow) sql() string {
	partition := make([]string, 0, len(w.Partition))
	for _, column := range w.Partition {
		partition = append(partition, identifier(column.Name))
	}
	order := make([]string, 0, len(w.Order))
	for _, item := range w.Order {
		order = append(order, sqlOrder(item.Field, item.Direction))
	}
	over := " OVER (PARTITION BY " + strings.Join(partition, ",") + " ORDER BY " + strings.Join(order, ",") + ")"
	if w.Operation == "row-number" {
		return "row_number()" + over
	}
	if w.Type == "string" {
		return "coalesce(lag(" + identifier(w.Field.Name) + ")" + over + ",'')"
	}
	return "lag(" + identifier(w.Field.Name) + ")" + over
}

// sql lowers the plan over the archive source queries named in source
// order (the plan's own base first). Each source is materialized once and
// earlier stages lower before the plans that read them.
func (p Plan) sql(names, sources []string) string {
	projections := make([]string, 0, len(p.Selections)+len(p.Ties))
	orders := make([]string, 0, len(p.Orders)+len(p.Ties))
	columns := make([]string, 0, len(p.Columns))
	packed := make([]string, 0, len(p.Columns))
	ctes := ""
	read := map[string]string{}
	for i, name := range names {
		read[name] = "source_" + strconv.Itoa(i)
		ctes += read[name] + " AS MATERIALIZED (" + sources[i] + "),\n"
	}
	outputs := map[*Plan]string{}
	cte := func(plan *Plan, source string) string {
		if plan != nil {
			return outputs[plan]
		}
		return read[source]
	}
	lower := func(plan *Plan, prefix string) string {
		rights := make([]string, 0, len(plan.Joins))
		for _, join := range plan.Joins {
			rights = append(rights, cte(join.Input, join.Source))
		}
		text, last := plan.stageSQL(prefix, cte(plan.Input, plan.Source), rights)
		ctes += text
		return last
	}
	dependencies := p.Dependencies()
	for i, stage := range dependencies[:len(dependencies)-1] {
		outputs[stage] = lower(stage, "stage_"+strconv.Itoa(i)+"_")
		ctes += ",\n"
	}
	last := lower(&p, "")
	expressions := map[string]string{}
	for _, name := range p.Inputs {
		expressions[name] = identifier(name)
	}
	for _, join := range p.Joins {
		for _, field := range join.Fields {
			expressions[field.As] = identifier(field.As)
		}
	}
	// Select after the explicit post-aggregate phase, including computed fields.
	if len(p.Values) > 0 {
		for _, value := range p.Values {
			expressions[value.As] = identifier(value.As)
		}
	}
	for _, computed := range append(slices.Clone(p.RowComputations), p.Computations...) {
		expressions[computed.As] = identifier(computed.As)
	}
	for _, window := range p.Windows {
		expressions[window.As] = identifier(window.As)
	}
	for _, selection := range p.Selections {
		projections = append(projections, expressions[selection.Field.Name]+" AS "+identifier(selection.As))
	}
	for _, order := range p.Orders {
		orders = append(orders, sqlOrder(order.Field, order.Direction))
	}
	for i, tie := range p.Ties {
		hidden := Column{Name: "__archive_tie_" + strconv.Itoa(i), Type: tie.Type}
		projections = append(projections, identifier(tie.Name)+" AS "+identifier(hidden.Name))
		orders = append(orders, sqlOrder(hidden, "asc"))
	}
	order := ""
	if len(orders) > 0 {
		order = " ORDER BY " + strings.Join(orders, ",")
	}
	for _, column := range p.Columns {
		columns = append(columns, identifier(column.Name))
		packed = append(packed, identifier(column.Name)+":=r."+identifier(column.Name))
	}
	return `WITH ` + ctes + `,
projected AS (SELECT ` + strings.Join(projections, ",") + ` FROM ` + last + `),
page AS (SELECT ` + strings.Join(columns, ",") + `,row_number() OVER (` + order + `) AS __archive_order FROM projected` + order +
		` LIMIT ` + strconv.Itoa(p.Limit+1) + `)
SELECT coalesce((SELECT list(struct_pack(` + strings.Join(packed, ",") + `) ORDER BY r.__archive_order) FROM page r), []) AS rows,
(SELECT count(DISTINCT TraceId) FROM source_0 WHERE len(Gaps)>0) AS tracesWithGaps`
}

// activeSQL resolves only server-owned files and exact pinned membership.
// An empty membership returns no values and no paths.
func (s *Snapshot) activeSQL(ctx context.Context) ([]string, []string, error) {
	members := s.selected()
	if len(members) > 10000 {
		return nil, nil, ErrLimit
	}
	if len(members) == 0 {
		return nil, nil, nil
	}
	var values, paths []string
	selected := make(map[Member]bool, len(members))
	for _, m := range members {
		selected[m] = true
		values = append(values, fmt.Sprintf("(%s,%s,%s,%d,%s,%s)", literal(m.Namespace), literal(m.Repository),
			literal(m.RunID), m.Attempt, literal(m.ID), literal(m.Version)))
	}
	found := make(map[Member]bool, len(members))
	for _, file := range s.manifest.Files {
		var relevant bool
		for _, member := range file.Members {
			if selected[member] {
				if found[member] {
					return nil, nil, ErrIntegrity
				}
				relevant, found[member] = true, true
			}
		}
		if !relevant {
			continue
		}
		path := filepath.Join(s.archive.config.Directory, file.Path)
		if err := verifyPublishedFile(ctx, path, file); err != nil {
			return nil, nil, err
		}
		paths = append(paths, literal(path))
	}
	if len(found) != len(selected) {
		return nil, nil, ErrIntegrity
	}
	return values, paths, nil
}

const activeColumns = "Namespace,Repository,RunId,RunAttempt,TraceId,ProjectionId"

// sourceSQL projects exact pinned event rows. Its projection never includes
// payload JSON, including for empty archives.
func (s *Snapshot) sourceSQL(ctx context.Context, fields []string) (string, error) {
	values, paths, err := s.activeSQL(ctx)
	if err != nil {
		return "", err
	}
	fields = slices.Clone(fields)
	for _, name := range []string{"TraceId", "Gaps"} {
		if !slices.Contains(fields, name) {
			fields = append(fields, name)
		}
	}
	var columns []string
	for _, name := range fields {
		columns = append(columns, "e."+identifier(name))
	}
	if len(values) == 0 {
		var empty []string
		for _, name := range fields {
			i := slices.Index(eventColumns, name)
			if i < 0 {
				return "", ErrIntegrity
			}
			empty = append(empty, "CAST(NULL AS "+eventSQLTypes[i]+") AS "+identifier(name))
		}
		return "SELECT " + strings.Join(empty, ",") + " WHERE false", nil
	}
	return `WITH active(` + activeColumns + `) AS (VALUES ` +
		strings.Join(values, ",") + `) SELECT ` + strings.Join(columns, ",") +
		` FROM read_parquet([` + strings.Join(paths, ",") + `],hive_partitioning=false) e
INNER JOIN active a USING(` + activeColumns + `)`, nil
}

// itemFieldSQL derives one element field's typed value and Present/Null/
// Invalid membership from approved JSON. Values are set only when upstream
// graders would accept them, so a numeric or boolean string never becomes valid.
func itemFieldSQL(element ItemElement) (value, present, null, invalid string) {
	raw, text := "Element", "json_extract_string(Element,'$')"
	present = "Element IS NOT NULL"
	if element.Key != "" {
		raw, text = "json_extract(Element,'$."+element.Key+"')", "json_extract_string(Element,'$."+element.Key+"')"
		present = "json_type(Element)='OBJECT' AND " + raw + " IS NOT NULL"
	}
	kind := "json_type(" + raw + ")"
	null = kind + "='NULL'"
	// A non-null element field that upstream would reject.
	failed := func(condition string) string {
		return present + " AND " + kind + "<>'NULL' AND " + condition
	}
	switch element.Typing {
	case "unsigned", "signed":
		low := "0"
		if element.Typing == "signed" {
			low = "-9007199254740991"
		}
		number := "TRY_CAST(" + raw + " AS DOUBLE)"
		valid := kind + " IN ('BIGINT','UBIGINT','DOUBLE') AND " + number + "=trunc(" + number + ") AND " +
			number + " BETWEEN " + low + " AND 9007199254740991"
		return "CASE WHEN " + valid + " THEN CAST(" + number + " AS BIGINT) END", present, null, failed("NOT coalesce(" + valid + ",false)")
	case "boolean":
		return "CASE WHEN " + kind + "='BOOLEAN' THEN CAST(" + raw + " AS BOOLEAN) END", present, null, failed(kind + "<>'BOOLEAN'")
	case "length":
		return "CASE WHEN " + kind + "='ARRAY' THEN CAST(json_array_length(" + raw + ") AS BIGINT) END", present, null, failed(kind + "<>'ARRAY'")
	case "visit":
		// States are visits: a string element is its own id; a record contributes its string id.
		return "coalesce(CASE WHEN json_type(Element)='VARCHAR' THEN json_extract_string(Element,'$') " +
				"WHEN json_type(Element)='OBJECT' AND " + kind + "='VARCHAR' THEN " + text + " END,'')",
			"(json_type(Element)='VARCHAR' OR " + present + ")", null, failed(kind + "<>'VARCHAR'")
	}
	return "coalesce(CASE WHEN " + kind + "='VARCHAR' THEN " + text + " END,'')", present, null, failed(kind + "<>'VARCHAR'")
}

// largestCollectionSQL mirrors upstream findOversizedTrajectoryCollection
// over trajectoryIR: the longest guarded IR array or reference array, with
// absent events counted as synthesized agent events and absent or null
// toolCalls bounded by agent tool events. Excluded inputs make it null.
func largestCollectionSQL() string {
	length := func(path string) string {
		return "coalesce(CASE WHEN json_type(t.Payload,'$." + path + "')='ARRAY' THEN CAST(json_array_length(t.Payload,'$." + path + "') AS BIGINT) END,0)"
	}
	terms := []string{
		"CASE WHEN json_extract(t.Payload,'$.data.events') IS NULL THEN coalesce(t.Synthesized,0) ELSE " + length("data.events") + " END",
		"CASE WHEN coalesce(json_type(t.Payload,'$.data.toolCalls'),'NULL')='NULL' THEN coalesce(t.Tools,0) ELSE " + length("data.toolCalls") + " END",
		length("data.reference.patch.files"),
		"CASE WHEN json_type(t.Payload,'$.data.reference')='OBJECT' THEN coalesce(list_max(list_transform(map_values(json_transform(" +
			"json_extract(t.Payload,'$.data.reference'),'\"MAP(VARCHAR,JSON)\"')),lambda v: CASE WHEN json_type(v)='ARRAY' THEN " +
			"CAST(json_array_length(v) AS BIGINT) ELSE 0 END)),0) ELSE 0 END",
	}
	for _, key := range GuardedCollections {
		if key != "events" && key != "toolCalls" && key != "reference" {
			terms = append(terms, length("data."+key))
		}
	}
	reasons := GuardedOmissions()
	omitted := make([]string, 0, len(reasons))
	for _, reason := range reasons {
		omitted = append(omitted, literal(reason))
	}
	return "CASE WHEN list_has_any(coalesce(t.OmissionReasons,[]),[" + strings.Join(omitted, ",") + "]) THEN NULL ELSE greatest(" + strings.Join(terms, ",") + ") END"
}

// itemSQL flattens one approved collection of each active trace's last
// agent-phase trajectory.ir event at query time. Payload JSON stays internal.
func (s *Snapshot) itemSQL(ctx context.Context, item ItemSource) (string, error) {
	values, paths, err := s.activeSQL(ctx)
	if err != nil {
		return "", err
	}
	if len(values) == 0 {
		types := map[string]string{"string": "VARCHAR", "integer": "BIGINT", "boolean": "BOOLEAN"}
		var empty []string
		for _, column := range item.Columns() {
			empty = append(empty, "CAST(NULL AS "+types[column.Type]+") AS "+identifier(column.Name))
		}
		for _, name := range append([]string{"Gaps"}, fieldStateInputs...) {
			empty = append(empty, "CAST(NULL AS VARCHAR[]) AS "+identifier(name))
		}
		return "SELECT " + strings.Join(empty, ",") + " WHERE false", nil
	}
	ir := " FILTER (WHERE Type='trajectory.ir' AND Phase='agent')"
	var tools []string
	for _, kind := range ReconciledToolTypes {
		tools = append(tools, literal(kind))
	}
	toolTypes := strings.Join(tools, ",")
	position, nested := "", "SELECT * FROM elements"
	if item.Nested != "" {
		// Nested rows keep the parent Ordinal; a trace without any nested
		// element contributes one anchor with null Ordinal and Position.
		children := "json_extract(Element,'$." + item.Nested + "')"
		position = "Position,"
		nested = `SELECT * EXCLUDE (Element,Children),json_extract(Element,'$.` + item.Nested + `['||Position||']') AS Element
FROM (SELECT *,unnest(range(Children)) AS Position FROM (SELECT *,CASE WHEN json_type(` + children + `)='ARRAY'
THEN CAST(json_array_length(` + children + `) AS BIGINT) ELSE 0 END AS Children FROM elements) WHERE Children>0)
UNION ALL BY NAME SELECT s.*,NULL::BIGINT AS Ordinal,NULL::BIGINT AS Position,NULL::JSON AS Element FROM sized s
WHERE NOT EXISTS (SELECT 1 FROM elements e WHERE e.Namespace=s.Namespace AND e.Repository=s.Repository AND e.RunId=s.RunId
AND e.RunAttempt=s.RunAttempt AND e.TraceId=s.TraceId AND e.ProjectionId=s.ProjectionId AND json_type(` + children + `)='ARRAY'
AND json_array_length(` + children + `)>0)`
	}
	fields := make([]string, 0, len(item.Elements))
	presents := []string{"CASE WHEN Collection IS NOT NULL THEN ['Items'] ELSE [] END"}
	nulls := []string{"CASE WHEN json_type(Collection)='NULL' THEN ['Items'] ELSE [] END"}
	invalids := []string{"CASE WHEN Collection IS NOT NULL AND json_type(Collection) NOT IN ('NULL','ARRAY') THEN ['Items'] ELSE [] END"}
	for _, element := range item.Elements {
		value, present, null, invalid := itemFieldSQL(element)
		name := literal(element.Column.Name)
		fields = append(fields, value+" AS "+identifier(element.Column.Name))
		presents = append(presents, "CASE WHEN "+present+" THEN ["+name+"] ELSE [] END")
		nulls = append(nulls, "CASE WHEN "+null+" THEN ["+name+"] ELSE [] END")
		invalids = append(invalids, "CASE WHEN "+invalid+" THEN ["+name+"] ELSE [] END")
	}
	return `WITH active(` + activeColumns + `) AS (VALUES ` + strings.Join(values, ",") + `),
events AS (SELECT e.Namespace,e.Repository,e.RunId,e.RunAttempt,e.TraceId,e.ProjectionId,e.Sequence,e.Type,e.Phase,
e.Component,e.PolicyDigest,e.Gaps,e.OmissionReasons,e.PayloadJSON FROM read_parquet([` + strings.Join(paths, ",") + `],hive_partitioning=false) e
INNER JOIN active a USING(` + activeColumns + `)),
traces AS (SELECT ` + activeColumns + `,max(PolicyDigest) AS PolicyDigest,list_distinct(flatten(list(Gaps))) AS Gaps,
max(Sequence)` + ir + ` AS Sequence,max_by(PayloadJSON,Sequence)` + ir + ` AS Payload,
max_by(OmissionReasons,Sequence)` + ir + ` AS OmissionReasons,
count(*) FILTER (WHERE Phase='agent' AND Component='agent') AS Synthesized,
count(*) FILTER (WHERE Phase='agent' AND Type IN (` + toolTypes + `)) AS Tools FROM events GROUP BY ` + activeColumns + `),
bound AS (SELECT a.Namespace,a.Repository,a.RunId,a.RunAttempt,a.TraceId,a.ProjectionId,t.PolicyDigest,coalesce(t.Gaps,[]) AS Gaps,
t.Sequence,coalesce(t.OmissionReasons,[]) AS OmissionReasons,json_extract(t.Payload,'$.` + item.Path + `') AS Collection,
` + largestCollectionSQL() + ` AS LargestCollection
FROM active a LEFT JOIN traces t USING(` + activeColumns + `)),
sized AS (SELECT *,CASE WHEN json_type(Collection)='ARRAY' THEN CAST(json_array_length(Collection) AS BIGINT) END AS Items FROM bound),
items AS (SELECT *,unnest(CASE WHEN Items>0 THEN range(Items) ELSE [NULL::BIGINT] END) AS Ordinal FROM sized),
elements AS (SELECT *,CASE WHEN Ordinal IS NOT NULL THEN json_extract(Collection,'$['||Ordinal||']') END AS Element FROM items),
nested AS (` + nested + `)
SELECT Namespace,Repository,RunId,RunAttempt,TraceId,ProjectionId,PolicyDigest,Sequence,Ordinal,Items,
coalesce(CASE json_type(Element) WHEN 'OBJECT' THEN 'object' WHEN 'ARRAY' THEN 'array' WHEN 'VARCHAR' THEN 'string'
WHEN 'BOOLEAN' THEN 'boolean' WHEN 'NULL' THEN 'null' WHEN 'BIGINT' THEN 'number' WHEN 'UBIGINT' THEN 'number'
WHEN 'DOUBLE' THEN 'number' END,'') AS Kind,LargestCollection,` + position + `
` + strings.Join(fields, ",") + `,Gaps,OmissionReasons,
list_concat(` + strings.Join(presents, ",") + `) AS PresentFields,
list_concat(` + strings.Join(nulls, ",") + `) AS NullFields,
list_concat(` + strings.Join(invalids, ",") + `) AS InvalidFields
FROM nested`, nil
}

func (s *Snapshot) Analytical(ctx context.Context, definition Definition) (AnalyticalResult, error) {
	plan, err := definition.Plan()
	if err != nil {
		return AnalyticalResult{}, err
	}
	if err := plan.Require(AnalyticalCapabilities(10000)); err != nil {
		return AnalyticalResult{}, err
	}
	if err := s.check(); err != nil {
		return AnalyticalResult{}, err
	}
	combined, cancel := context.WithCancel(ctx)
	//nolint:contextcheck // Independent snapshot revocation must cancel verification and the caller-scoped subprocess.
	stop := context.AfterFunc(s.ctx, cancel)
	defer func() { stop(); cancel() }()
	names, inputs := plan.SourceInputs()
	sources := make([]string, 0, len(names))
	for _, name := range names {
		var source string
		if item, ok := lookupItemSource(name); ok {
			source, err = s.itemSQL(combined, item)
		} else {
			source, err = s.sourceSQL(combined, inputs[name])
		}
		if err != nil {
			if accessErr := s.check(); accessErr != nil {
				return AnalyticalResult{}, accessErr
			}
			return AnalyticalResult{}, err
		}
		sources = append(sources, source)
	}
	var result []struct {
		Rows           []map[string]any `json:"rows"`
		TracesWithGaps int              `json:"tracesWithGaps"`
	}
	if err := s.archive.converter.query(combined, plan.sql(names, sources), &result); err != nil {
		if accessErr := s.check(); accessErr != nil {
			return AnalyticalResult{}, accessErr
		}
		return AnalyticalResult{}, err
	}
	if err := s.check(); err != nil {
		return AnalyticalResult{}, err
	}
	if len(result) != 1 {
		return AnalyticalResult{}, ErrIntegrity
	}
	rows := result[0].Rows
	limited := len(rows) > plan.Limit
	if limited && !plan.ExplicitLimit {
		return AnalyticalResult{}, ErrLimit
	}
	if limited {
		rows = rows[:plan.Limit]
	}
	return AnalyticalResult{
		Columns: plan.Columns, Rows: rows,
		Metadata: definition.Metadata(limited, s.admissionGaps, result[0].TracesWithGaps),
	}, nil
}
