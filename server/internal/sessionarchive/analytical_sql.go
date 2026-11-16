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

// stageSQL lowers filter, reduce, and post-aggregate phases to CTEs named
// with prefix that read from. It returns the CTE text and the last CTE name.
func (p Plan) stageSQL(prefix, from string) (string, string) {
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
	ctes := prefix + "filtered AS (SELECT * FROM " + from + " WHERE " + filter + ")"
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

func (p Plan) sql(source string) string {
	projections := make([]string, 0, len(p.Selections)+len(p.Ties))
	orders := make([]string, 0, len(p.Orders)+len(p.Ties))
	columns := make([]string, 0, len(p.Columns))
	packed := make([]string, 0, len(p.Columns))
	// Earlier stages lower first; each reads the CTE its predecessor produced.
	stages := []Plan{}
	for stage := p.Input; stage != nil; stage = stage.Input {
		stages = append([]Plan{*stage}, stages...)
	}
	ctes, from := "", "source"
	for i, stage := range stages {
		text, last := stage.stageSQL("stage_"+strconv.Itoa(i)+"_", from)
		ctes += text + ",\n"
		from = last
	}
	text, last := p.stageSQL("", from)
	ctes += text
	expressions := map[string]string{}
	for _, name := range p.Inputs {
		expressions[name] = identifier(name)
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
	return `WITH source AS MATERIALIZED (` + source + `),
` + ctes + `,
projected AS (SELECT ` + strings.Join(projections, ",") + ` FROM ` + last + `),
page AS (SELECT ` + strings.Join(columns, ",") + `,row_number() OVER (` + order + `) AS __archive_order FROM projected` + order +
		` LIMIT ` + strconv.Itoa(p.Limit+1) + `)
SELECT coalesce((SELECT list(struct_pack(` + strings.Join(packed, ",") + `) ORDER BY r.__archive_order) FROM page r), []) AS rows,
(SELECT count(DISTINCT TraceId) FROM source WHERE len(Gaps)>0) AS tracesWithGaps`
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

// itemFieldSQL derives one item field's typed value and Present/Null/Invalid
// membership from approved JSON. Values are set only when upstream graders
// would accept them, so a numeric or boolean string never becomes valid.
func itemFieldSQL(item itemSource) (value, invalid string) {
	raw := "json_extract(Element,'$." + item.Key + "')"
	kind := "json_type(" + raw + ")"
	switch item.Element.Type {
	case "integer":
		number := "TRY_CAST(" + raw + " AS DOUBLE)"
		valid := kind + " IN ('BIGINT','UBIGINT','DOUBLE') AND " + number + "=trunc(" + number + ") AND " +
			number + " BETWEEN 0 AND 9007199254740991"
		return "CASE WHEN " + valid + " THEN CAST(" + number + " AS BIGINT) END", "NOT coalesce(" + valid + ",false)"
	case "boolean":
		return "CASE WHEN " + kind + "='BOOLEAN' THEN CAST(" + raw + " AS BOOLEAN) END", kind + "<>'BOOLEAN'"
	}
	// States are visits: a string element is its own id; a record contributes its string id.
	return "coalesce(CASE WHEN json_type(Element)='VARCHAR' THEN json_extract_string(Element,'$') " +
			"WHEN json_type(Element)='OBJECT' AND " + kind + "='VARCHAR' THEN json_extract_string(Element,'$." + item.Key + "') END,'')",
		kind + "<>'VARCHAR'"
}

// itemSQL flattens one approved collection of each active trace's last
// agent-phase trajectory.ir event at query time. Payload JSON stays internal.
func (s *Snapshot) itemSQL(ctx context.Context, item itemSource) (string, error) {
	values, paths, err := s.activeSQL(ctx)
	if err != nil {
		return "", err
	}
	columns := append(slices.Clone(itemColumns), item.Element)
	if len(values) == 0 {
		types := map[string]string{"string": "VARCHAR", "integer": "BIGINT", "boolean": "BOOLEAN"}
		var empty []string
		for _, column := range columns {
			empty = append(empty, "CAST(NULL AS "+types[column.Type]+") AS "+identifier(column.Name))
		}
		for _, name := range append([]string{"Gaps"}, fieldStateInputs...) {
			empty = append(empty, "CAST(NULL AS VARCHAR[]) AS "+identifier(name))
		}
		return "SELECT " + strings.Join(empty, ",") + " WHERE false", nil
	}
	ir := " FILTER (WHERE Type='trajectory.ir' AND Phase='agent')"
	field, invalid := itemFieldSQL(item)
	elementKey := "json_extract(Element,'$." + item.Key + "')"
	present := "json_type(Element)='OBJECT' AND " + elementKey + " IS NOT NULL"
	if item.Element.Type == "string" {
		present = "(json_type(Element)='VARCHAR' OR " + present + ")"
	}
	return `WITH active(` + activeColumns + `) AS (VALUES ` + strings.Join(values, ",") + `),
events AS (SELECT e.Namespace,e.Repository,e.RunId,e.RunAttempt,e.TraceId,e.ProjectionId,e.Sequence,e.Type,e.Phase,
e.PolicyDigest,e.Gaps,e.OmissionReasons,e.PayloadJSON FROM read_parquet([` + strings.Join(paths, ",") + `],hive_partitioning=false) e
INNER JOIN active a USING(` + activeColumns + `)),
traces AS (SELECT ` + activeColumns + `,max(PolicyDigest) AS PolicyDigest,list_distinct(flatten(list(Gaps))) AS Gaps,
max(Sequence)` + ir + ` AS Sequence,max_by(PayloadJSON,Sequence)` + ir + ` AS Payload,
max_by(OmissionReasons,Sequence)` + ir + ` AS OmissionReasons FROM events GROUP BY ` + activeColumns + `),
bound AS (SELECT a.Namespace,a.Repository,a.RunId,a.RunAttempt,a.TraceId,a.ProjectionId,t.PolicyDigest,coalesce(t.Gaps,[]) AS Gaps,
t.Sequence,coalesce(t.OmissionReasons,[]) AS OmissionReasons,json_extract(t.Payload,'$.` + item.Path + `') AS Collection
FROM active a LEFT JOIN traces t USING(` + activeColumns + `)),
sized AS (SELECT *,CASE WHEN json_type(Collection)='ARRAY' THEN CAST(json_array_length(Collection) AS BIGINT) END AS Items FROM bound),
items AS (SELECT *,unnest(CASE WHEN Items>0 THEN range(Items) ELSE [NULL::BIGINT] END) AS Ordinal FROM sized),
elements AS (SELECT *,CASE WHEN Ordinal IS NOT NULL THEN json_extract(Collection,'$['||Ordinal||']') END AS Element FROM items)
SELECT Namespace,Repository,RunId,RunAttempt,TraceId,ProjectionId,PolicyDigest,Sequence,Ordinal,Items,
coalesce(CASE json_type(Element) WHEN 'OBJECT' THEN 'object' WHEN 'ARRAY' THEN 'array' WHEN 'VARCHAR' THEN 'string'
WHEN 'BOOLEAN' THEN 'boolean' WHEN 'NULL' THEN 'null' WHEN 'BIGINT' THEN 'number' WHEN 'UBIGINT' THEN 'number'
WHEN 'DOUBLE' THEN 'number' END,'') AS Kind,
` + field + ` AS ` + identifier(item.Element.Name) + `,Gaps,OmissionReasons,
list_concat(CASE WHEN Collection IS NOT NULL THEN ['Items'] ELSE [] END,
CASE WHEN ` + present + ` THEN [` + literal(item.Element.Name) + `] ELSE [] END) AS PresentFields,
list_concat(CASE WHEN json_type(Collection)='NULL' THEN ['Items'] ELSE [] END,
CASE WHEN json_type(` + elementKey + `)='NULL' THEN [` + literal(item.Element.Name) + `] ELSE [] END) AS NullFields,
list_concat(CASE WHEN Collection IS NOT NULL AND json_type(Collection) NOT IN ('NULL','ARRAY') THEN ['Items'] ELSE [] END,
CASE WHEN json_type(Element)='OBJECT' AND ` + elementKey + ` IS NOT NULL AND json_type(` + elementKey + `)<>'NULL' AND ` + invalid + `
THEN [` + literal(item.Element.Name) + `] ELSE [] END) AS InvalidFields
FROM elements`, nil
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
	var source string
	base := plan.Base()
	if item, ok := lookupItemSource(base.Source); ok {
		source, err = s.itemSQL(combined, item)
	} else {
		source, err = s.sourceSQL(combined, base.Inputs)
	}
	if err != nil {
		if accessErr := s.check(); accessErr != nil {
			return AnalyticalResult{}, accessErr
		}
		return AnalyticalResult{}, err
	}
	var result []struct {
		Rows           []map[string]any `json:"rows"`
		TracesWithGaps int              `json:"tracesWithGaps"`
	}
	if err := s.archive.converter.query(combined, plan.sql(source), &result); err != nil {
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
