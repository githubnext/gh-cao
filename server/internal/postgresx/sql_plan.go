package postgresx

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/githubnext/gh-cao/server/internal/inputlimits"
	"github.com/githubnext/gh-cao/server/internal/model"
	"github.com/githubnext/gh-cao/server/internal/query"
	"github.com/githubnext/gh-cao/server/internal/sqlbuilder"
)

type SQLPage struct{ Offset, Limit int }
type SQLResourceLimits struct {
	MaxInputRows    int
	MaxOperations   int
	MaxWorkingBytes int64
}

type SQLExecutionOptions struct {
	Runtime        func(context.Context, string) (model.Source, error)
	Pages          map[string]SQLPage
	ResourceLimits *SQLResourceLimits
}

func sqlResourceLimits(override *SQLResourceLimits) (SQLResourceLimits, error) {
	limits := SQLResourceLimits{
		MaxInputRows:  query.MaxInputRows,
		MaxOperations: query.MaxOperations, MaxWorkingBytes: query.MaxWorkingBytes,
	}
	if override == nil {
		return limits, nil
	}
	if override.MaxInputRows <= 0 || override.MaxInputRows > query.MaxWorkingRows ||
		override.MaxOperations <= 0 || override.MaxOperations > 10*query.MaxOperations ||
		override.MaxWorkingBytes <= 0 || override.MaxWorkingBytes > 4*query.MaxWorkingBytes {
		return SQLResourceLimits{}, errors.New("invalid SQL resource limits")
	}
	return *override, nil
}

func (r *readTransaction) ExecuteSQLPlan(ctx context.Context, definitions []query.Definition, requested []string) (map[string]model.Source, model.Metrics, error) {
	return r.ExecuteSQLPlanWithOptions(ctx, definitions, requested, SQLExecutionOptions{})
}

func (r *readTransaction) ExecuteSQLPlanWithOptions(ctx context.Context, definitions []query.Definition, requested []string, options SQLExecutionOptions) (map[string]model.Source, model.Metrics, error) {
	limits, err := sqlResourceLimits(options.ResourceLimits)
	if err != nil {
		return nil, model.Metrics{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, inputlimits.PostgresReadTransactionTimeout)
	defer cancel()
	metadata := map[string]model.Metadata{}
	retrievedAt := time.Now().UTC().Format(time.RFC3339Nano)
	definitions = frozenStatisticalDefinitions(definitions)
	valueSelections := query.OperationalValueEvidenceSelections(definitions, requested)
	measurementSelections := query.SourceEqualities(definitions, requested, "workflow-value-measurements",
		"$operationalValues", "id", "workflow-route")
	resolve := func(name string) (query.SQLRelation, error) {
		if strings.HasPrefix(name, statisticalSnapshotPrefix) {
			relation, quality, err := r.frozenStatisticalSource(ctx, name)
			metadata[name] = quality
			return relation, err
		}
		if name == "$statisticalMembership" {
			relation, quality, err := r.statisticalSQLSource(ctx)
			metadata[name] = quality
			return relation, err
		}
		if table, exists := entityTables[name]; exists && table.runtime {
			if options.Runtime == nil {
				return query.SQLRelation{}, fmt.Errorf("runtime source %q requires its authorized provider", name)
			}
			source, err := options.Runtime(ctx, name)
			if err != nil {
				return query.SQLRelation{}, err
			}
			if source.Metadata == nil {
				source.Metadata = model.Metadata{}
			}
			for _, field := range []string{"as-of", "retrieved-at"} {
				if _, present := source.Metadata[field]; !present {
					source.Metadata[field] = retrievedAt
				}
			}
			for _, field := range []string{"completeness", "freshness"} {
				if _, present := source.Metadata[field]; !present {
					source.Metadata[field] = "unknown"
				}
			}
			metadata[name] = source.Metadata
			return runtimeSQLSource(name, source)
		}
		relation, err := r.entitySQLSource(name)
		if err != nil {
			return query.SQLRelation{}, err
		}
		if name == "$operationalValueEvidence" {
			relation = r.operationalValueEvidenceSQLSource(valueSelections...)
		}
		if name == "$operationalValues" && len(measurementSelections) > 0 {
			var predicates []string
			for _, selection := range measurementSelections {
				if selection.Field == "id" {
					predicates = append(predicates, "t.id={}")
					relation.Params = append(relation.Params, selection.Value)
				} else {
					predicates = append(predicates, "("+relation.Columns["workflow-route"].Expression+")={}")
					relation.Params = append(relation.Params, selection.Value)
				}
			}
			relation.SQL = "(SELECT * FROM " + relation.SQL + " WHERE " + strings.Join(predicates, " OR ") + ") AS t"
		}
		value, err := r.rootMetadata(ctx, name, valueSelections...)
		if err != nil {
			return query.SQLRelation{}, err
		}
		metadata[name] = value
		if value["availability"] == "unavailable" {
			relation.SQL = "(SELECT * FROM " + relation.SQL + " WHERE FALSE) AS t"
		}
		return relation, nil
	}
	plan, err := query.CompileSQL(definitions, requested, resolve)
	if err != nil {
		return nil, model.Metrics{}, err
	}
	statsSQL, statsArgs, err := plan.StatisticsStatement()
	if err != nil {
		return nil, model.Metrics{}, err
	}
	rows, err := r.tx.QueryContext(ctx, statsSQL, statsArgs...)
	if err != nil {
		return nil, model.Metrics{}, fmt.Errorf("execute SQL resource checks: %w", err)
	}
	type relationSize struct {
		rows  int
		bytes int64
	}
	sizes, err := func() (map[string]relationSize, error) {
		defer func() { _ = rows.Close() }()
		result := map[string]relationSize{}
		for rows.Next() {
			var name string
			var size relationSize
			var duplicateJoin bool
			if err := rows.Scan(&name, &size.rows, &size.bytes, &duplicateJoin); err != nil {
				return nil, err
			}
			if duplicateJoin {
				return nil, errors.New("joined source has more than one row per join key")
			}
			result[name] = size
		}
		return result, rows.Err()
	}()
	if err != nil {
		return nil, model.Metrics{}, err
	}
	metrics := model.Metrics{PushedDown: []string{"sql-plan"}, FallbackOperations: []string{}}
	for _, step := range plan.Steps {
		size, exists := sizes[step.Relation]
		if !exists {
			return nil, metrics, errors.New("SQL resource statistics are incomplete")
		}
		if size.rows > limits.MaxInputRows {
			return nil, metrics, fmt.Errorf("query %q exceeds max input rows", step.Query)
		}
		if step.Operation == "facet-output" && size.rows > query.MaxChartFacets {
			return nil, metrics, fmt.Errorf("query %q exceeds max chart facets", step.Query)
		}
		metrics.PeakWorkingRows = max(metrics.PeakWorkingRows, size.rows)
		metrics.PeakWorkingBytes = max(metrics.PeakWorkingBytes, size.bytes)
		if size.bytes > limits.MaxWorkingBytes {
			return nil, metrics, fmt.Errorf("query %q exceeds max working bytes", step.Query)
		}
		metrics.Operations += size.rows * step.Weight
		if metrics.Operations > limits.MaxOperations {
			return nil, metrics, fmt.Errorf("query %q exceeds max operations", step.Query)
		}
		switch step.Operation {
		case "from":
			metrics.QueryCount++
		case "filter":
			metrics.FilterCount++
		case "compute":
			metrics.ComputeCount++
		case "join":
			metrics.JoinCount++
		case "aggregate":
			metrics.AggregateCount++
		case "select":
			metrics.SelectCount++
		case "order-by":
			metrics.OrderByCount++
		case "limit":
			metrics.LimitCount++
		}
		metrics.PushedDown = append(metrics.PushedDown, step.Operation)
	}
	output := make(map[string]model.Source, len(requested))
	for _, name := range requested {
		relation := plan.Outputs[name]
		size := sizes[unquoteSQLName(relation.SQL)]
		page, paginated := options.Pages[name]
		if size.rows > query.MaxOutputRows && !paginated {
			return nil, metrics, fmt.Errorf("query %q exceeds max output rows", name)
		}
		if !paginated {
			page = SQLPage{Limit: query.MaxOutputRows}
		}
		if page.Offset > size.rows {
			return nil, metrics, fmt.Errorf("invalid or stale continuation token for %q", name)
		}
		statement, args, fields, err := plan.OutputStatement(name, page.Offset, page.Limit)
		if err != nil {
			return nil, metrics, err
		}
		composed := composeMetadata(definitions, metadata, name, map[string]bool{})
		ownedMetadata := model.Metadata{}
		for field, value := range composed {
			ownedMetadata[field] = value
		}
		composed = ownedMetadata
		if composed["availability"] == "unavailable" {
			statement = "SELECT * FROM (" + statement + ") AS unavailable WHERE FALSE"
		}
		rows, err := r.tx.QueryContext(ctx, statement, args...)
		if err != nil {
			return nil, metrics, fmt.Errorf("read SQL result: %w", err)
		}
		result, err := func() ([]model.Row, error) {
			defer func() { _ = rows.Close() }()
			result := []model.Row{}
			for rows.Next() {
				values := make([]any, len(fields))
				present := make([]bool, len(fields))
				dest := make([]any, len(fields)*2)
				for index := range fields {
					dest[index*2], dest[index*2+1] = &values[index], &present[index]
				}
				if len(fields) == 0 {
					var empty bool
					dest = []any{&empty}
				}
				if err := rows.Scan(dest...); err != nil {
					return nil, err
				}
				row := model.Row{}
				for index, field := range fields {
					if !present[index] {
						continue
					}
					value, err := decodeSQLValue(values[index], relation.Columns[field].Kind)
					if err != nil {
						return nil, err
					}
					row[field] = value
				}
				metrics.RetainedRows++
				metrics.RetainedBytes += query.EstimateRowsBytes([]model.Row{row})
				if metrics.RetainedRows > query.MaxRetainedRows || metrics.RetainedBytes > query.MaxRetainedBytes {
					return nil, &query.PlanLimitError{QueryID: name, Boundary: query.BoundaryRetainedBytes}
				}
				result = append(result, row)
			}
			return result, rows.Err()
		}()
		if err != nil {
			return nil, metrics, err
		}
		composed["source-id"], composed["source-kind"], composed["row-count"] = name, "derived", len(result)
		if composed["availability"] != "unavailable" {
			composed["availability"] = "available"
			if size.rows == 0 {
				composed["availability"] = "empty"
			}
		}
		if paginated {
			composed["total-row-count"] = size.rows
			if composed["availability"] == "unavailable" {
				composed["total-row-count"] = 0
			}
		}
		output[name] = model.Source{Source: name, Rows: result, Metadata: composed}
		metrics.OutputRows += len(result)
	}
	return output, metrics, nil
}

func (r *readTransaction) rootMetadata(ctx context.Context, name string, valueSelections ...query.SourceEquality) (model.Metadata, error) {
	if name == dailySource || name == "$dailyRunCounts" {
		return r.dailyMetadata(ctx)
	}
	var availability, completeness, freshness string
	var asOf, retrieved sql.NullTime
	err := r.tx.QueryRowContext(ctx, `SELECT availability,completeness,freshness,as_of,retrieved_at FROM cao_quality
			WHERE namespace=$1 AND collection=$2`, r.store.namespace, name).Scan(&availability, &completeness, &freshness, &asOf, &retrieved)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Metadata{"source-id": name, "source-kind": "canonical", "availability": "unavailable", "completeness": "unknown", "freshness": "unknown"}, nil
	}
	if err != nil {
		return nil, err
	}
	if name == "$operationalValueEvidence" {
		predicate, params := r.operationalValueEvidencePredicate(valueSelections)
		statement, args, err := sqlbuilder.Build(`SELECT min(p.window_end),min(p.observed_at)
			FROM operational_value_evidence p WHERE `+predicate, params...)
		if err != nil {
			return nil, err
		}
		if err := r.tx.QueryRowContext(ctx, statement, args...).Scan(&asOf, &retrieved); err != nil {
			return nil, err
		}
		completeness, freshness = "unknown", "unknown"
	}
	if name == "$workflowValueCollections" {
		if err := r.tx.QueryRowContext(ctx, `SELECT min(observed_at),min(observed_at)
			FROM workflow_value_collections WHERE namespace=$1`, r.store.namespace).Scan(&asOf, &retrieved); err != nil {
			return nil, err
		}
		completeness, freshness = "unknown", "unknown"
	}
	value := model.Metadata{"source-id": name, "source-kind": "canonical", "availability": availability, "completeness": completeness, "freshness": freshness}
	var evaluated time.Time
	if err := r.tx.QueryRowContext(ctx, "SELECT evaluated_at FROM cao_state WHERE namespace=$1", r.store.namespace).Scan(&evaluated); err != nil {
		return nil, err
	}
	for field, instant := range map[string]sql.NullTime{"as-of": asOf, "retrieved-at": retrieved} {
		value[field] = evaluated.UTC().Format(time.RFC3339Nano)
		if instant.Valid {
			value[field] = instant.Time.UTC().Format(time.RFC3339Nano)
		}
	}
	return value, nil
}

func mergeMetadata(left, right model.Metadata, optional bool) model.Metadata {
	result := model.Metadata{}
	for key, value := range left {
		result[key] = value
	}
	if frame, exists := right["statistical-frame"]; exists {
		result["statistical-frame"] = frame
		result["sample-frame"] = frame
	}
	for _, field := range []string{"as-of", "retrieved-at"} {
		a, _ := left[field].(string)
		b, _ := right[field].(string)
		if a == "" || b != "" && b < a {
			result[field] = b
		}
	}
	for _, field := range []string{"completeness", "freshness"} {
		a, _ := left[field].(string)
		b, _ := right[field].(string)
		if a == "fresh" {
			a = "current"
		}
		if b == "fresh" {
			b = "current"
		}
		weak := "partial"
		if field == "freshness" {
			weak = "stale"
		}
		switch {
		case a == "" || b == "" || a == "unknown" || b == "unknown":
			result[field] = "unknown"
		case a == weak || b == weak:
			result[field] = weak
		default:
			result[field] = a
		}
	}
	if right["availability"] == "unavailable" {
		if optional {
			if result["completeness"] != "unknown" {
				result["completeness"] = "partial"
			}
		} else {
			result["availability"] = "unavailable"
		}
	} else if result["availability"] == "empty" && right["availability"] == "available" {
		result["availability"] = "available"
	}
	return result
}

func composeMetadata(definitions []query.Definition, roots map[string]model.Metadata, name string, visiting map[string]bool) model.Metadata {
	if root, exists := roots[name]; exists {
		return root
	}
	if visiting[name] {
		return model.Metadata{"availability": "unavailable", "completeness": "unknown", "freshness": "unknown"}
	}
	visiting[name] = true
	defer delete(visiting, name)
	for _, definition := range definitions {
		if definition.Name != name {
			continue
		}
		result := model.Metadata{"availability": "empty", "completeness": "complete", "freshness": "current"}
		if definition.From != "" {
			result = composeMetadata(definitions, roots, definition.From, visiting)
		}
		for _, source := range definition.Union {
			result = mergeMetadata(result, composeMetadata(definitions, roots, source, visiting), false)
		}
		inputs := []model.Metadata{result}
		for _, join := range definition.Joins {
			right := composeMetadata(definitions, roots, join.Source, visiting)
			inputs = append(inputs, right)
			result = mergeMetadata(result, right, join.Type == "left" && !dailyAggregateMetadata(right))
		}
		if len(definition.Joins) > 0 && len(definition.Union) == 0 {
			var provenance model.Metadata
			consistent := true
			for _, input := range inputs {
				if input["aggregate-version"] == nil {
					continue
				}
				fields := aggregateMetadataFields(input)
				if provenance == nil {
					provenance = fields
				} else if !reflect.DeepEqual(provenance, fields) {
					consistent = false
				}
			}
			for field := range aggregateMetadataFields(result) {
				delete(result, field)
			}
			if consistent && dailyAggregateMetadata(provenance) {
				for field, value := range provenance {
					result[field] = value
				}
				// Inventory quality cannot erase the known partial coverage of counts.
				for _, input := range inputs {
					if input["completeness"] == "partial" {
						result["completeness"] = "partial"
					}
				}
			}
		}
		return result
	}

	// No arbitrary source-name or legacy logical-source fallback is admitted.
	return model.Metadata{"source-id": strings.TrimSpace(name), "availability": "unavailable", "completeness": "unknown", "freshness": "unknown"}
}

func dailyAggregateMetadata(metadata model.Metadata) bool {
	return fmt.Sprint(metadata["aggregate-version"]) == "1"
}

func aggregateMetadataFields(metadata model.Metadata) model.Metadata {
	fields := model.Metadata{}
	for field, value := range metadata {
		if strings.HasPrefix(field, "aggregate-") || field == "excluded-runs" || field == "conflicted-runs" || field == "query-diagnostic" {
			fields[field] = value
		}
	}
	return fields
}

func unquoteSQLName(name string) string {
	if len(name) >= 2 && name[0] == '"' && name[len(name)-1] == '"' {
		return name[1 : len(name)-1]
	}
	return name
}

func decodeSQLValue(value any, kind query.SQLKind) (any, error) {
	if value == nil {
		return nil, nil
	}
	if bytes, binary := value.([]byte); binary {
		value = string(bytes)
	}
	switch kind {
	case query.SQLText:
		return value, nil
	case query.SQLNumber:
		return json.Number(fmt.Sprint(value)), nil
	case query.SQLTimestamp:
		instant, valid := value.(time.Time)
		if !valid {
			return nil, errors.New("invalid typed SQL timestamp")
		}
		return instant.UTC().Format(time.RFC3339Nano), nil
	case query.SQLStructured:
		text, valid := value.(string)
		if !valid {
			return nil, errors.New("invalid SQL wire projection")
		}
		var decoded any
		if err := decodeJSON([]byte(text), &decoded); err != nil {
			return nil, err
		}
		return decoded, nil
	case query.SQLBoolean:
		if text, stringValue := value.(string); stringValue {
			return strconv.ParseBool(text)
		}
	}
	return value, nil
}
