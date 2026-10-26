package sessionarchive

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

func analyticalLiteral(raw json.RawMessage, kind string) (string, error) {
	switch kind {
	case "string":
		var value string
		if len(raw) == 0 || raw[0] != '"' || json.Unmarshal(raw, &value) != nil || strings.ContainsRune(value, 0) {
			return "", ErrQuery
		}
		return literal(value), nil
	case "integer":
		value, err := strconv.ParseInt(string(raw), 10, 64)
		if err != nil || strconv.FormatInt(value, 10) != string(raw) {
			return "", ErrQuery
		}
		return strconv.FormatInt(value, 10), nil
	default:
		return "", ErrQuery
	}
}

func (p AnalyticalPredicate) compile(field, kind string) (string, error) {
	modes := 0
	for _, present := range []bool{p.Equals != nil, p.In != nil, p.GTE != nil || p.LT != nil} {
		if present {
			modes++
		}
	}
	if modes != 1 {
		return "", ErrQuery
	}
	if p.Equals != nil {
		value, err := analyticalLiteral(p.Equals, kind)
		if err != nil {
			return "", err
		}
		return field + "=" + value, nil
	}
	if p.In != nil {
		var alternatives []json.RawMessage
		if err := json.Unmarshal(p.In, &alternatives); err != nil || len(alternatives) < 1 || len(alternatives) > 32 {
			return "", ErrQuery
		}
		values := make([]string, 0, len(alternatives))
		for _, raw := range alternatives {
			value, err := analyticalLiteral(raw, kind)
			if err != nil {
				return "", err
			}
			values = append(values, value)
		}
		return field + " IN (" + strings.Join(values, ",") + ")", nil
	}
	parts := []string{field + " IS NOT NULL"}
	if kind == "string" {
		parts = append(parts, field+"<>''")
	}
	for _, bound := range []struct {
		raw      json.RawMessage
		operator string
	}{{p.GTE, ">="}, {p.LT, "<"}} {
		if bound.raw == nil {
			continue
		}
		value, err := analyticalLiteral(bound.raw, kind)
		if err != nil {
			return "", err
		}
		parts = append(parts, field+bound.operator+value)
	}
	return "(" + strings.Join(parts, " AND ") + ")", nil
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
	plan, err := definition.compile()
	if err != nil {
		return AnalyticalResult{}, err
	}
	if err := s.check(); err != nil {
		return AnalyticalResult{}, err
	}
	combined, cancel := context.WithCancel(ctx)
	//nolint:contextcheck // Independent snapshot revocation must cancel verification and the caller-scoped subprocess.
	stop := context.AfterFunc(s.ctx, cancel)
	defer func() { stop(); cancel() }()
	source, err := s.sourceSQL(combined, plan.inputs)
	if err != nil {
		if accessErr := s.check(); accessErr != nil {
			return AnalyticalResult{}, accessErr
		}
		return AnalyticalResult{}, err
	}
	var columns, packed []string
	for _, column := range plan.columns {
		columns = append(columns, identifier(column.Name))
		packed = append(packed, identifier(column.Name)+":=r."+identifier(column.Name))
	}
	order := ""
	if plan.order != "" {
		order = " ORDER BY " + plan.order
	}
	sql := `WITH source AS MATERIALIZED (` + source + `),
filtered AS (SELECT * FROM source WHERE ` + plan.filter + `),
reduced AS (SELECT ` + plan.projection + ` FROM filtered` + plan.group + `),
page AS (SELECT ` + strings.Join(columns, ",") + `,row_number() OVER (` + order + `) AS __archive_order FROM reduced` + order +
		` LIMIT ` + strconv.Itoa(plan.limit+1) + `)
SELECT coalesce((SELECT list(struct_pack(` + strings.Join(packed, ",") + `) ORDER BY r.__archive_order) FROM page r), []) AS rows,
(SELECT count(DISTINCT TraceId) FROM source WHERE len(Gaps)>0) AS tracesWithGaps`
	var result []struct {
		Rows           []map[string]any `json:"rows"`
		TracesWithGaps int              `json:"tracesWithGaps"`
	}
	if err := s.archive.converter.query(combined, sql, &result); err != nil {
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
	limited := len(rows) > plan.limit
	if limited && definition.Limit == nil {
		return AnalyticalResult{}, ErrLimit
	}
	if limited {
		rows = rows[:plan.limit]
	}
	return AnalyticalResult{
		Columns: plan.columns, Rows: rows,
		Metadata: QueryMetadata{Limited: limited, UpstreamCoverage: "unknown",
			AdmissionGaps: s.admissionGaps, TracesWithGaps: result[0].TracesWithGaps},
	}, nil
}
