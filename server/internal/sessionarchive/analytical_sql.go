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

func (p Plan) sql(source string) string {
	filter := "true"
	predicates := make([]string, 0, len(p.Predicates))
	groups := make([]string, 0, len(p.By))
	projections := make([]string, 0, len(p.Selections)+len(p.Ties))
	orders := make([]string, 0, len(p.Orders)+len(p.Ties))
	columns := make([]string, 0, len(p.Columns))
	packed := make([]string, 0, len(p.Columns))
	for _, predicate := range p.Predicates {
		predicates = append(predicates, predicate.sql())
	}
	if len(predicates) > 0 {
		filter = strings.Join(predicates, " AND ")
	}
	expressions := map[string]string{}
	for _, name := range p.Inputs {
		expressions[name] = identifier(name)
	}
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
				valid := predicatesSQL(value.Predicates) + " AND " + expression + " IS NOT NULL"
				expression = "(CASE WHEN count(" + expression + ")" + filter + ">0 THEN list_reduce(list(" +
					expression + " ORDER BY Sequence) FILTER (WHERE " + valid + "),lambda a,b: a+b,0::DOUBLE) ELSE NULL END)"
			}
		}
		expressions[value.As] = expression
	}
	base := make([]string, 0, len(p.By)+len(p.Values))
	for _, field := range p.By {
		base = append(base, identifier(field.Name))
	}
	for _, value := range p.Values {
		base = append(base, expressions[value.As]+" AS "+identifier(value.As))
	}
	// Select after the explicit post-aggregate phase, including computed fields.
	if len(p.Values) > 0 {
		for _, value := range p.Values {
			expressions[value.As] = identifier(value.As)
		}
	}
	for _, computed := range p.Computations {
		expressions[computed.As] = identifier(computed.As)
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
	for _, field := range p.By {
		groups = append(groups, identifier(field.Name))
	}
	group, order := "", ""
	if len(groups) > 0 {
		group = " GROUP BY " + strings.Join(groups, ",")
	}
	if len(orders) > 0 {
		order = " ORDER BY " + strings.Join(orders, ",")
	}
	for _, column := range p.Columns {
		columns = append(columns, identifier(column.Name))
		packed = append(packed, identifier(column.Name)+":=r."+identifier(column.Name))
	}
	reduced := "SELECT * FROM filtered"
	if len(p.Values) > 0 {
		reduced = "SELECT " + strings.Join(base, ",") + " FROM filtered" + group
	}
	computations := ""
	last := "reduced"
	for i, computed := range p.Computations {
		next := "computed_" + strconv.Itoa(i)
		computations += ",\n" + next + " AS (SELECT *," + computed.Expression.sql() + " AS " + identifier(computed.As) + " FROM " + last + ")"
		last = next
	}
	return `WITH source AS MATERIALIZED (` + source + `),
filtered AS (SELECT * FROM source WHERE ` + filter + `),
reduced AS (` + reduced + `)` + computations + `,
projected AS (SELECT ` + strings.Join(projections, ",") + ` FROM ` + last + `),
page AS (SELECT ` + strings.Join(columns, ",") + `,row_number() OVER (` + order + `) AS __archive_order FROM projected` + order +
		` LIMIT ` + strconv.Itoa(p.Limit+1) + `)
SELECT coalesce((SELECT list(struct_pack(` + strings.Join(packed, ",") + `) ORDER BY r.__archive_order) FROM page r), []) AS rows,
(SELECT count(DISTINCT TraceId) FROM source WHERE len(Gaps)>0) AS tracesWithGaps`
}

// sourceSQL resolves only server-owned files and exact pinned membership.
// Its projection never includes payload JSON, including for empty archives.
func (s *Snapshot) sourceSQL(ctx context.Context, fields []string) (string, error) {
	members := s.selected()
	if len(members) > 10000 {
		return "", ErrLimit
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
	if len(members) == 0 {
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
					return "", ErrIntegrity
				}
				relevant, found[member] = true, true
			}
		}
		if !relevant {
			continue
		}
		path := filepath.Join(s.archive.config.Directory, file.Path)
		if err := verifyPublishedFile(ctx, path, file); err != nil {
			return "", err
		}
		paths = append(paths, literal(path))
	}
	if len(found) != len(selected) {
		return "", ErrIntegrity
	}
	return `WITH active(Namespace,Repository,RunId,RunAttempt,TraceId,ProjectionId) AS (VALUES ` +
		strings.Join(values, ",") + `) SELECT ` + strings.Join(columns, ",") +
		` FROM read_parquet([` + strings.Join(paths, ",") + `],hive_partitioning=false) e
INNER JOIN active a USING(Namespace,Repository,RunId,RunAttempt,TraceId,ProjectionId)`, nil
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
	source, err := s.sourceSQL(combined, plan.Inputs)
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
