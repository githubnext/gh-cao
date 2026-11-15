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
		if value.Reducer == "distinct-count" {
			expression = "DISTINCT " + expression
		}
		expressions[value.As] = "count(" + expression + ")"
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
	return `WITH source AS MATERIALIZED (` + source + `),
filtered AS (SELECT * FROM source WHERE ` + filter + `),
reduced AS (SELECT ` + strings.Join(projections, ",") + ` FROM filtered` + group + `),
page AS (SELECT ` + strings.Join(columns, ",") + `,row_number() OVER (` + order + `) AS __archive_order FROM reduced` + order +
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
		Metadata: QueryMetadata{Limited: limited, UpstreamCoverage: "unknown",
			AdmissionGaps: s.admissionGaps, TracesWithGaps: result[0].TracesWithGaps},
	}, nil
}
