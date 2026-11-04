package query

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/githubnext/gh-cao/server/internal/logger"
	"github.com/githubnext/gh-cao/server/internal/model"
)

var queryLog = logger.New("cao:query")

func ParseDefinitions(data []byte) ([]Definition, error) {
	var definitions []Definition
	if err := json.Unmarshal(data, &definitions); err != nil {
		return nil, fmt.Errorf("parse dashboard queries: %w", err)
	}
	return definitions, Validate(definitions)
}

func Validate(definitions []Definition) error {
	if len(definitions) > 2048 {
		return errors.New("dashboard query batch exceeds 2048 definitions")
	}
	seen := map[string]bool{}
	available := map[string]bool{}
	for _, definition := range definitions {
		if definition.Name == "" || definition.From == "" {
			return errors.New("query name and from are required")
		}
		if seen[definition.Name] {
			return fmt.Errorf("query name %q is declared more than once", definition.Name)
		}
		seen[definition.Name] = true
		if len(definition.Joins) > MaxJoins {
			return fmt.Errorf("query %q exceeds max joins", definition.Name)
		}
		if len(definition.Union) > 64 || len(definition.Compute) > 128 || len(definition.Select) > 256 || len(definition.OrderBy) > 16 {
			return fmt.Errorf("query %q exceeds structural resource limits", definition.Name)
		}
		if err := validateFilter(definition.Filter); err != nil {
			return fmt.Errorf("query %q: %w", definition.Name, err)
		}
		for _, join := range definition.Joins {
			if join.Source == "" || len(join.On) == 0 {
				return fmt.Errorf("query %q contains an unbounded join", definition.Name)
			}
			if join.Type != "" && join.Type != "inner" && join.Type != "left" && join.Type != "inner-many" {
				return fmt.Errorf("query %q has unsupported join type %q", definition.Name, join.Type)
			}
			if seen[join.Source] && !available[join.Source] {
				return fmt.Errorf("query %q has an invalid dependency", definition.Name)
			}
		}
		if definition.Aggregate != nil && (len(definition.Aggregate.Values) == 0 || len(definition.Aggregate.Values) > MaxAggregateValues) {
			return fmt.Errorf("query %q has invalid aggregate values", definition.Name)
		}
		if definition.Aggregate != nil {
			for _, value := range definition.Aggregate.Values {
				if !supportedReducer(value.Reducer) {
					return fmt.Errorf("query %q has unsupported reducer %q", definition.Name, value.Reducer)
				}
				if value.Filter != nil && len(value.Filter.Predicates) > 8 {
					return fmt.Errorf("query %q has too many aggregate filter predicates", definition.Name)
				}
				if err := validateFilter(value.Filter); err != nil {
					return fmt.Errorf("query %q aggregate: %w", definition.Name, err)
				}
			}
		}
		if definition.TemporalSeries != nil &&
			(len(definition.TemporalSeries.Measures) > 64 || len(definition.TemporalSeries.Maps) > 64 || len(definition.TemporalSeries.Carry) > 16 || len(definition.TemporalSeries.PointCarry) > 16) {
			return fmt.Errorf("query %q exceeds temporal-series resource limits", definition.Name)
		}
		if definition.TemporalSeries != nil && definition.TemporalSeries.Shape != "" &&
			definition.TemporalSeries.Shape != "tidy" && definition.TemporalSeries.Shape != "groups" && definition.TemporalSeries.Shape != "panels" {
			return fmt.Errorf("query %q has unsupported temporal-series shape %q", definition.Name, definition.TemporalSeries.Shape)
		}
		for _, computed := range definition.Compute {
			minimum, maximum, ok := computeArity(computed.Function)
			if !ok {
				return fmt.Errorf("query %q has unsupported computed-field function %q", definition.Name, computed.Function)
			}
			if len(computed.Args) < minimum || len(computed.Args) > maximum {
				return fmt.Errorf("query %q computed field %q has invalid arity", definition.Name, computed.As)
			}
		}
		if len(definition.Predict) > 0 {
			return fmt.Errorf("query %q uses prediction, which is not supported by the dashboard server", definition.Name)
		}
		if definition.Window != nil {
			if err := validateWindows(definition.Window); err != nil {
				return fmt.Errorf("query %q: %w", definition.Name, err)
			}
		}
		if definition.Limit != nil && (*definition.Limit <= 0 || *definition.Limit > MaxOutputRows) {
			return fmt.Errorf("query %q has invalid limit", definition.Name)
		}
		if definition.Page != nil {
			if len(definition.Page.Keys) == 0 || len(definition.Page.Keys) > 4 ||
				definition.Page.Revision != "workflow-subjects" ||
				definition.Page.Comparator != "utf8-bytewise-v1" ||
				len(definition.OrderBy) != len(definition.Page.Keys) ||
				definition.Limit != nil || definition.Facet != nil {
				return fmt.Errorf("query %q has invalid page boundary", definition.Name)
			}
			seen := map[string]bool{}
			for index, key := range definition.Page.Keys {
				order := definition.OrderBy[index]
				if strings.TrimSpace(key) == "" || seen[key] || order.Field != key ||
					(order.Direction != "" && order.Direction != "asc") {
					return fmt.Errorf("query %q has invalid page boundary", definition.Name)
				}
				seen[key] = true
			}
		}
		if definition.Facet != nil {
			facet := definition.Facet
			if strings.TrimSpace(facet.As) == "" || (facet.Field == "" && facet.Row == "" && facet.Column == "") ||
				(facet.Field != "" && (facet.Row != "" || facet.Column != "")) {
				return fmt.Errorf("query %q has invalid facet", definition.Name)
			}
			switch facet.As {
			case "facet-field", "facet-row", "facet-column", "facet-row-index", "facet-column-index":
				return fmt.Errorf("query %q has a reserved facet output name", definition.Name)
			}
		}
		available[definition.Name] = true
	}
	names := make([]string, 0, len(definitions))
	for _, definition := range definitions {
		names = append(names, definition.Name)
	}
	if _, err := Dependencies(definitions, names); err != nil {
		return err
	}
	return nil
}

func computeArity(function string) (int, int, bool) {
	arities := map[string][2]int{
		"literal": {1, 1}, "coalesce": {1, 8}, "concat": {1, 8}, "lower": {1, 1}, "upper": {1, 1},
		"title-case": {1, 1}, "trim": {1, 1}, "replace-suffix": {3, 3}, "url-encode": {1, 1},
		"date-day": {1, 1}, "activity-shard-day": {1, 1}, "timestamp": {1, 1}, "date-bucket": {2, 2}, "calendar-week-point": {3, 3}, "dashboard-link": {3, 4}, "link-href": {1, 1}, "link": {2, 2},
		"equals-any": {2, 8}, "greater-than": {2, 2}, "if": {3, 3}, "format-count": {1, 1},
		"format-percent": {1, 1}, "array-length": {1, 1}, "failure-streak-point": {3, 3},
		"number": {1, 1}, "positive-integer": {2, 2}, "sum": {2, 8}, "difference": {2, 2},
		"product": {2, 8}, "quotient": {2, 2}, "iso-timestamp": {1, 1}, "sqrt": {1, 1},
	}
	arity, ok := arities[function]
	return arity[0], arity[1], ok
}

func supportedReducer(reducer string) bool {
	switch reducer {
	case "count", "distinct-count", "distinct-list", "distinct-values", "calendar-week-rhythm",
		"latest-failure-streak", "sum", "mean", "min", "max", "unique", "median", "p75", "p90", "p95":
		return true
	default:
		return false
	}
}

func validateFilter(filter *Filter) error {
	if filter == nil {
		return nil
	}
	if len(filter.Predicates) > 64 {
		return errors.New("filter exceeds 64 predicates")
	}
	if filter.Search != nil && len(filter.Search.Fields) > 64 {
		return errors.New("search exceeds 64 fields")
	}
	for _, predicate := range filter.Predicates {
		if len(predicate.In) > MaxPredicateAlternatives {
			return fmt.Errorf("predicate %q exceeds %d alternatives", predicate.Field, MaxPredicateAlternatives)
		}
	}
	return nil
}

func Dependencies(definitions []Definition, requested []string) ([]string, error) {
	order, _, _, err := dependencyPlan(definitions, requested)
	return order, err
}

// WalkPlan resolves every root before visiting its dependent definition.
// The loader owns source authorization, including optional join/union inputs.
func WalkPlan(definitions []Definition, requested []string, load func(string) error, visit func(Definition) error) error {
	if err := Validate(definitions); err != nil {
		return err
	}
	order, err := Dependencies(definitions, requested)
	if err != nil {
		return err
	}
	index := make(map[string]Definition, len(definitions))
	for _, definition := range definitions {
		index[definition.Name] = definition
	}
	for _, name := range order {
		definition, derived := index[name]
		if !derived {
			if err := load(name); err != nil {
				return err
			}
			continue
		}
		inputs := append([]string{definition.From}, definition.Union...)
		for _, join := range definition.Joins {
			inputs = append(inputs, join.Source)
		}
		for _, input := range inputs {
			if _, derived := index[input]; !derived {
				if err := load(input); err != nil {
					return err
				}
			}
		}
		if err := visit(definition); err != nil {
			return err
		}
	}
	return nil
}

func alias(field SelectedField) string {
	if field.As != "" {
		return field.As
	}
	return field.Field
}

func dependencyPlan(definitions []Definition, requested []string) ([]string, int, int, error) {
	index := make(map[string]Definition, len(definitions))
	for _, definition := range definitions {
		index[definition.Name] = definition
	}
	visiting, visited := map[string]bool{}, map[string]bool{}
	var result []string
	maxDepth := 0
	var visit func(string, int) error
	visit = func(name string, depth int) error {
		if depth > MaxDependencyDepth {
			return fmt.Errorf("query dependency graph exceeds max depth of %d", MaxDependencyDepth)
		}
		maxDepth = max(maxDepth, depth)
		if visited[name] {
			return nil
		}
		if visiting[name] {
			return fmt.Errorf("query dependency cycle at %q", name)
		}
		visiting[name] = true
		if definition, ok := index[name]; ok {
			inputs := append([]string{definition.From}, definition.Union...)
			for _, join := range definition.Joins {
				inputs = append(inputs, join.Source)
			}
			for _, input := range inputs {
				if _, isQuery := index[input]; isQuery {
					if err := visit(input, depth+1); err != nil {
						return err
					}
				}
			}
		}
		visiting[name] = false
		visited[name] = true
		result = append(result, name)
		return nil
	}
	for _, name := range requested {
		if err := visit(name, 1); err != nil {
			return nil, 0, 0, err
		}
	}
	maxDepth, maxJoinDepth := computeDependencyDepths(result, index)
	queryLog.Printf("dependency plan resolved requested=%d resolved=%d max_depth=%d max_join_depth=%d",
		len(requested), len(result), maxDepth, maxJoinDepth)
	return result, maxDepth, maxJoinDepth, nil
}

// computeDependencyDepths walks the already-resolved dependency order and
// returns the deepest chained-query depth and the deepest accumulated join
// count along any chain, used to size downstream SQL compilation limits.
func computeDependencyDepths(result []string, index map[string]Definition) (maxDepth, maxJoinDepth int) {
	depths := map[string]int{}
	joinDepths := map[string]int{}
	for _, name := range result {
		definition, isQuery := index[name]
		if !isQuery {
			continue
		}
		depth := 1
		joinDepth := len(definition.Joins)
		inputs := append([]string{definition.From}, definition.Union...)
		for _, join := range definition.Joins {
			inputs = append(inputs, join.Source)
		}
		for _, input := range inputs {
			depth = max(depth, depths[input]+1)
			joinDepth = max(joinDepth, joinDepths[input]+len(definition.Joins))
		}
		depths[name] = depth
		joinDepths[name] = joinDepth
		maxDepth = max(maxDepth, depth)
		maxJoinDepth = max(maxJoinDepth, joinDepth)
	}
	return maxDepth, maxJoinDepth
}

func estimateRowsBytes(rows []model.Row, remaining int64) int64 {
	var total int64
	for _, row := range rows {
		total += 64
		for key, value := range row {
			total += int64(len(key)) + 48 + estimateValueBytes(value, 0, remaining)
			if total > remaining {
				return total
			}
		}
	}
	return total
}

func EstimateRowsBytes(rows []model.Row) int64 {
	return estimateRowsBytes(rows, MaxRetainedBytes)
}

func EstimateRowsBytesWithLimit(rows []model.Row, limit int64) int64 {
	return estimateRowsBytes(rows, limit)
}

func estimateValueBytes(value any, depth int, limit int64) int64 {
	if depth > 32 {
		return limit + 1
	}
	switch typed := value.(type) {
	case nil:
		return 0
	case bool, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64:
		return 8
	case string:
		return int64(len(typed))
	case []string:
		total := int64(24)
		for _, item := range typed {
			total += 16 + int64(len(item))
		}
		return total
	case []any:
		total := int64(24)
		for _, item := range typed {
			total += 16 + estimateValueBytes(item, depth+1, limit)
			if total > limit {
				return total
			}
		}
		return total
	case map[string]any:
		total := int64(48)
		for key, item := range typed {
			total += int64(len(key)) + 32 + estimateValueBytes(item, depth+1, limit)
			if total > limit {
				return total
			}
		}
		return total
	default:
		return 32
	}
}
