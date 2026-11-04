package dashboardquery

import (
	"fmt"
	"math"
	"reflect"
	"strings"
)

var engineComputeArity = map[string][2]int{
	"coalesce": {1, 8}, "concat": {1, 8}, "literal": {1, 1}, "lower": {1, 1},
	"upper": {1, 1}, "title-case": {1, 1}, "trim": {1, 1}, "replace-suffix": {3, 3},
	"url-encode": {1, 1}, "date-day": {1, 1}, "activity-shard-day": {1, 1},
	"timestamp": {1, 1}, "iso-timestamp": {1, 1}, "sqrt": {1, 1}, "date-bucket": {2, 2},
	"calendar-week-point": {3, 3}, "dashboard-link": {3, 4}, "link": {2, 2},
	"link-href": {1, 1}, "equals-any": {2, 8}, "greater-than": {2, 2}, "if": {3, 3},
	"format-count": {1, 1}, "format-percent": {1, 1}, "array-length": {1, 1},
	"failure-streak-point": {3, 3}, "number": {1, 1}, "positive-integer": {2, 2},
	"sum": {2, 8}, "difference": {2, 2}, "product": {2, 8}, "quotient": {2, 2},
}

func engineKeys(mapping map[string]any, allowed string) error {
	if mapping == nil {
		return engineUnavailable("expected an object")
	}
	set := map[string]bool{}
	for _, key := range strings.Fields(allowed) {
		set[key] = true
	}
	for key := range mapping {
		if !set[key] {
			return engineUnavailable("unsupported vocabulary %q", key)
		}
	}
	return nil
}

func engineArray(value any) bool {
	if value == nil {
		return false
	}
	kind := reflect.TypeOf(value).Kind()
	return kind == reflect.Slice || kind == reflect.Array
}

func engineStrings(value any, min, max int) bool {
	if !engineArray(value) {
		return false
	}
	values := engineList(value)
	if len(values) < min || len(values) > max {
		return false
	}
	for _, item := range values {
		if _, ok := item.(string); !ok {
			return false
		}
	}
	return true
}

func engineInteger(value any, min, max int) bool {
	if _, ok := value.(string); ok {
		return false
	}
	n, ok := engineNumber(value)
	return ok && math.Trunc(n) == n && n >= float64(min) && n <= float64(max)
}

func engineHas(mapping nativeRow, key string) bool { _, ok := mapping[key]; return ok }

func engineValidate(def nativeRow) error {
	if err := engineKeys(def, "name description subject objective acceptance parameters stores from union joins filter compute temporal-series aggregate predict window select order-by page limit facet time"); err != nil {
		return err
	}
	if engineString(def["from"]) == "" {
		return engineUnavailable("from must identify an input source")
	}
	if value, ok := def["union"]; ok && !engineStrings(value, 1, 64) {
		return engineUnavailable("union must contain between 1 and 64 source names")
	}
	if value, ok := def["joins"]; ok {
		if !engineArray(value) || len(engineList(value)) > 4 {
			return engineUnavailable("joins exceed max-joins (4)")
		}
		for _, item := range engineList(value) {
			join := engineMap(item)
			if err := engineKeys(join, "source type on fields"); err != nil {
				return err
			}
			if engineString(join["source"]) == "" || !engineArray(join["on"]) || len(engineList(join["on"])) == 0 {
				return engineUnavailable("joins require a source and equality keys")
			}
			if kind := engineString(join["type"]); engineHas(join, "type") && kind != "inner" && kind != "left" && kind != "inner-many" {
				return engineUnavailable("unsupported join type %q", kind)
			}
			for _, pair := range engineList(join["on"]) {
				mapping := engineMap(pair)
				if err := engineKeys(mapping, "left right"); err != nil {
					return err
				}
				if engineString(mapping["left"]) == "" || engineString(mapping["right"]) == "" {
					return engineUnavailable("join equality keys require left and right fields")
				}
			}
			if err := engineValidateProjection(join["fields"], true); err != nil {
				return err
			}
		}
	}
	if value, ok := def["filter"]; ok {
		if err := engineValidateFilter(engineMap(value), false); err != nil {
			return err
		}
	}
	if value, ok := def["compute"]; ok {
		if !engineArray(value) || len(engineList(value)) > 128 {
			return engineUnavailable("compute requires at most 128 definitions")
		}
		for _, item := range engineList(value) {
			compute := engineMap(item)
			if err := engineKeys(compute, "as function args"); err != nil {
				return err
			}
			fn := engineString(compute["function"])
			arity, ok := engineComputeArity[fn]
			if !ok {
				return engineUnavailable("unsupported computed-field function %q", fn)
			}
			args := engineList(compute["args"])
			if engineString(compute["as"]) == "" || !engineArray(compute["args"]) || len(args) < arity[0] || len(args) > arity[1] {
				return engineUnavailable("computed-field function %q has invalid output or arity", fn)
			}
			for _, arg := range args {
				mapping := engineMap(arg)
				if err := engineKeys(mapping, "field value parameter context"); err != nil {
					return err
				}
				if len(mapping) != 1 {
					return engineUnavailable("compute arguments require exactly one scalar operand")
				}
				if engineHas(mapping, "field") && engineString(mapping["field"]) == "" {
					return engineUnavailable("compute field must be a field name")
				}
				if engineHas(mapping, "value") && !engineIsScalar(mapping["value"]) {
					return engineUnavailable("compute literal values must be scalar")
				}
				if engineHas(mapping, "parameter") && engineString(mapping["parameter"]) == "" {
					return engineUnavailable("compute parameter requires a parameter name")
				}
				if engineHas(mapping, "context") && mapping["context"] != "time-start" && mapping["context"] != "time-end" {
					return engineUnavailable("unsupported computation context %q", mapping["context"])
				}
			}
		}
	}
	if value, ok := def["select"]; ok {
		if err := engineValidateProjection(value, false); err != nil {
			return err
		}
	}
	if value, ok := def["order-by"]; ok {
		if err := engineValidateOrder(value, 0, 16); err != nil {
			return err
		}
	}
	if value, ok := def["page"]; ok {
		page := engineMap(value)
		if err := engineKeys(page, "keys revision comparator"); err != nil {
			return err
		}
		keys := engineList(page["keys"])
		order := engineList(def["order-by"])
		if !engineStrings(page["keys"], 1, 4) || page["revision"] != "workflow-subjects" ||
			page["comparator"] != "utf8-bytewise-v1" || len(order) != len(keys) ||
			engineHas(def, "limit") || engineHas(def, "facet") {
			return engineUnavailable("page requires workflow-subject keys and matching ascending order-by")
		}
		seen := map[string]bool{}
		for index, key := range keys {
			field := engineString(key)
			clause := engineMap(order[index])
			if field == "" || seen[field] || engineString(clause["field"]) != field ||
				(engineHas(clause, "direction") && clause["direction"] != "asc") {
				return engineUnavailable("page keys must exactly match ascending order-by")
			}
			seen[field] = true
		}
	}
	if value, ok := def["limit"]; ok && !engineInteger(value, 1, engineMaxOutputRows) {
		return engineUnavailable("limit must be a positive integer no greater than %d", engineMaxOutputRows)
	}
	if value, ok := def["aggregate"]; ok {
		aggregate := engineMap(value)
		if err := engineKeys(aggregate, "by values"); err != nil {
			return err
		}
		if engineHas(aggregate, "by") && !engineStrings(aggregate["by"], 0, 64) {
			return engineUnavailable("aggregate by must contain field names")
		}
		values := engineList(aggregate["values"])
		if !engineArray(aggregate["values"]) || len(values) == 0 || len(values) > 64 {
			return engineUnavailable("aggregate values must contain between 1 and 64 definitions")
		}
		for _, item := range values {
			value := engineMap(item)
			if err := engineKeys(value, "field as reducer filter"); err != nil {
				return err
			}
			if engineString(value["field"]) == "" || engineString(value["as"]) == "" {
				return engineUnavailable("aggregate requires field and as")
			}
			switch value["reducer"] {
			case "count", "distinct-count", "distinct-list", "distinct-values", "unique", "sum", "mean", "min", "max", "median", "p75", "p90", "p95", "calendar-week-rhythm", "latest-failure-streak":
			default:
				return engineUnavailable("unsupported aggregate reducer %q", value["reducer"])
			}
			if filter, ok := value["filter"]; ok {
				if err := engineValidateFilter(engineMap(filter), true); err != nil {
					return err
				}
			}
		}
	}
	if value, ok := def["window"]; ok {
		if err := engineValidateWindows(value); err != nil {
			return err
		}
	}
	if value, ok := def["predict"]; ok {
		if !engineArray(value) || len(engineList(value)) < 1 || len(engineList(value)) > 8 {
			return engineUnavailable("predict must contain between 1 and 8 definitions")
		}
		for _, item := range engineList(value) {
			p := engineMap(item)
			if err := engineKeys(p, "field on as method order groupby"); err != nil {
				return err
			}
			predictors := engineFields(p["on"])
			if engineString(p["field"]) == "" || engineString(p["as"]) == "" || len(predictors) < 1 || len(predictors) > 8 {
				return engineUnavailable("predict requires field, as and 1 to 8 predictors")
			}
			if _, ok := p["on"].(string); !ok && !engineStrings(p["on"], 1, 8) {
				return engineUnavailable("prediction on must contain field names")
			}
			method := engineString(p["method"])
			switch method {
			case "", "linear", "log", "exp", "pow", "quad", "poly":
			default:
				return engineUnavailable("unsupported prediction method %q", method)
			}
			if method != "" && method != "linear" && len(predictors) != 1 {
				return engineUnavailable("prediction method %s requires exactly one predictor", method)
			}
			if engineHas(p, "order") && (method != "poly" || !engineInteger(p["order"], 1, 10)) {
				return engineUnavailable("prediction order requires poly and an integer from 1 to 10")
			}
			if engineHas(p, "groupby") && !engineStrings(p["groupby"], 0, 64) {
				return engineUnavailable("prediction groupby requires field names")
			}
		}
	}
	if value, ok := def["facet"]; ok {
		facet := engineMap(value)
		if err := engineKeys(facet, "field row column as"); err != nil {
			return err
		}
		as := engineString(facet["as"])
		if strings.TrimSpace(as) == "" || strings.Contains(" facet-field facet-row facet-column facet-row-index facet-column-index ", " "+as+" ") {
			return engineUnavailable("facet requires an unreserved output name")
		}
		if engineHas(facet, "field") {
			if engineString(facet["field"]) == "" || engineHas(facet, "row") || engineHas(facet, "column") {
				return engineUnavailable("facet field cannot be combined with row or column")
			}
		} else if engineString(facet["row"]) == "" && engineString(facet["column"]) == "" {
			return engineUnavailable("facet requires field or row/column")
		}
	}
	if value, ok := def["temporal-series"]; ok {
		if err := engineValidateTemporal(engineMap(value)); err != nil {
			return err
		}
	}
	return nil
}

func engineValidateProjection(value any, renameRequired bool) error {
	if !engineArray(value) || len(engineList(value)) > 256 {
		return engineUnavailable("field projection must contain at most 256 fields")
	}
	for _, item := range engineList(value) {
		field := engineMap(item)
		if err := engineKeys(field, "field as"); err != nil {
			return err
		}
		if engineString(field["field"]) == "" || (renameRequired && engineString(field["as"]) == "") {
			return engineUnavailable("field projection requires named fields and aliases")
		}
	}
	return nil
}

func engineValidateOrder(value any, min, max int) error {
	if !engineArray(value) || len(engineList(value)) < min || len(engineList(value)) > max {
		return engineUnavailable("order-by requires %d to %d ordering clauses", min, max)
	}
	for _, item := range engineList(value) {
		clause := engineMap(item)
		if err := engineKeys(clause, "field direction"); err != nil {
			return err
		}
		if engineString(clause["field"]) == "" || (engineHas(clause, "direction") && clause["direction"] != "asc" && clause["direction"] != "desc") {
			return engineUnavailable("order-by requires field and optional asc or desc direction")
		}
	}
	return nil
}

func engineValidateFilter(filter nativeRow, aggregate bool) error {
	allowed := "predicates search"
	max, min := 64, 0
	if aggregate {
		allowed, max, min = "predicates", 8, 1
	}
	if err := engineKeys(filter, allowed); err != nil {
		return err
	}
	predicates := engineList(filter["predicates"])
	if (engineHas(filter, "predicates") && !engineArray(filter["predicates"])) || len(predicates) < min || len(predicates) > max {
		return engineUnavailable("filter requires %d to %d predicates", min, max)
	}
	for _, item := range predicates {
		predicate := engineMap(item)
		allowed := "field equals in includes gte lt optional"
		if aggregate {
			allowed = "field equals in"
		}
		if err := engineKeys(predicate, allowed); err != nil {
			return err
		}
		if engineString(predicate["field"]) == "" {
			return engineUnavailable("filter predicates require a field")
		}
		if alternatives, ok := predicate["in"]; ok {
			if !engineArray(alternatives) || len(engineList(alternatives)) > 32 || (aggregate && len(engineList(alternatives)) == 0) {
				return engineUnavailable("predicate alternatives exceed max-predicate-alternatives (32)")
			}
			for _, alternative := range engineList(alternatives) {
				if aggregate && (!engineIsScalar(alternative) || alternative == nil) {
					return engineUnavailable("aggregate comparisons require string, number or boolean literals")
				}
			}
		}
		if aggregate {
			if engineHas(predicate, "equals") == engineHas(predicate, "in") || (engineHas(predicate, "equals") && (!engineIsScalar(predicate["equals"]) || predicate["equals"] == nil)) {
				return engineUnavailable("aggregate predicates require exactly one equals or in comparison")
			}
		}
		if optional, ok := predicate["optional"]; ok {
			if _, ok := optional.(bool); !ok {
				return engineUnavailable("predicate optional must be boolean")
			}
		}
	}
	if value, ok := filter["search"]; ok {
		search := engineMap(value)
		if err := engineKeys(search, "fields query"); err != nil {
			return err
		}
		if !engineStrings(search["fields"], 0, 64) {
			return engineUnavailable("search requires at most 64 field names")
		}
		if _, ok := search["query"].(string); !ok {
			return engineUnavailable("search query must be text")
		}
	}
	return nil
}

func engineValidateWindows(value any) error {
	if !engineArray(value) || len(engineList(value)) < 1 || len(engineList(value)) > 8 {
		return engineUnavailable("window must contain between 1 and 8 definitions")
	}
	outputs := map[string]bool{}
	for _, item := range engineList(value) {
		window := engineMap(item)
		if err := engineKeys(window, "field as operation order-by groupby frame reducer alignment mode time-field unit"); err != nil {
			return err
		}
		if engineString(window["field"]) == "" || engineString(window["as"]) == "" {
			return engineUnavailable("window requires field and as")
		}
		as := engineString(window["as"])
		if outputs[as] {
			return engineUnavailable("window output names must be unique")
		}
		outputs[as] = true
		if err := engineValidateOrder(window["order-by"], 1, 8); err != nil {
			return err
		}
		if engineHas(window, "groupby") && !engineStrings(window["groupby"], 1, 8) {
			return engineUnavailable("window groupby requires 1 to 8 fields")
		}
		forbid := func(keys ...string) error {
			for _, key := range keys {
				if engineHas(window, key) {
					return engineUnavailable("window %s does not allow %s", window["operation"], key)
				}
			}
			return nil
		}
		switch window["operation"] {
		case "rolling":
			if !engineInteger(window["frame"], 1, 1000) ||
				(engineHas(window, "reducer") && !strings.Contains(" sum mean min max ", " "+engineString(window["reducer"])+" ")) ||
				(engineHas(window, "alignment") && window["alignment"] != "trailing" && window["alignment"] != "centered") ||
				(window["alignment"] == "centered" && engineInt(window["frame"])%2 != 1) {
				return engineUnavailable("rolling window requires a valid frame, reducer and alignment")
			}
			if err := forbid("mode", "time-field", "unit"); err != nil {
				return err
			}
		case "previous", "row-number":
			if err := forbid("frame", "reducer", "alignment", "mode", "time-field", "unit"); err != nil {
				return err
			}
		case "change":
			if err := forbid("frame", "reducer", "alignment"); err != nil {
				return err
			}
			if engineHas(window, "mode") && window["mode"] != "absolute" && window["mode"] != "percentage" && window["mode"] != "rate" {
				return engineUnavailable("unsupported window change mode %q", window["mode"])
			}
			if window["mode"] == "rate" {
				if engineString(window["time-field"]) == "" || !strings.Contains(" second minute hour day ", " "+engineString(window["unit"])+" ") {
					return engineUnavailable("rate window requires a time-field and unit")
				}
			} else if err := forbid("time-field", "unit"); err != nil {
				return err
			}
		default:
			return engineUnavailable("unsupported window operation %q", window["operation"])
		}
	}
	return nil
}

func engineValidateTemporal(series nativeRow) error {
	if err := engineKeys(series, "time series shape link carry point-carry measures maps trend"); err != nil {
		return err
	}
	if engineString(series["time"]) == "" || engineString(series["series"]) == "" {
		return engineUnavailable("temporal-series requires time and series fields")
	}
	shape := engineString(series["shape"])
	if engineHas(series, "shape") && shape != "tidy" && shape != "groups" && shape != "panels" {
		return engineUnavailable("unsupported temporal-series shape %q", shape)
	}
	if engineHas(series, "link") && (shape != "panels" || engineString(series["link"]) == "") {
		return engineUnavailable("temporal-series link requires panels")
	}
	for _, key := range []string{"carry", "point-carry"} {
		if value, ok := series[key]; ok {
			min := 0
			if key == "point-carry" {
				min = 1
				if shape != "panels" {
					return engineUnavailable("point-carry requires temporal panels")
				}
			}
			if !engineStrings(value, min, 16) {
				return engineUnavailable("temporal-series %s must contain %d to 16 fields", key, min)
			}
		}
	}
	count := 0
	for _, key := range []string{"measures", "maps"} {
		if value, ok := series[key]; ok {
			if !engineArray(value) || len(engineList(value)) > 64 {
				return engineUnavailable("temporal-series %s must contain at most 64 definitions", key)
			}
			count += len(engineList(value))
			for _, item := range engineList(value) {
				mapping := engineMap(item)
				fields := "field key kind"
				if key == "maps" {
					fields = "field definitions group kind"
				}
				if err := engineKeys(mapping, fields); err != nil {
					return err
				}
				if engineString(mapping["field"]) == "" || engineString(mapping["kind"]) == "" {
					return engineUnavailable("temporal-series %s require field and kind", key)
				}
			}
		}
	}
	if count == 0 {
		return engineUnavailable("temporal-series requires measure or map definitions")
	}
	if value, ok := series["trend"]; ok {
		trend := engineMap(value)
		if err := engineKeys(trend, "direction"); err != nil {
			return err
		}
		if shape != "groups" || engineString(trend["direction"]) == "" {
			return engineUnavailable("temporal trend requires groups and a direction field")
		}
	}
	return nil
}

func engineDescribeDefinition(def nativeRow) string {
	return fmt.Sprintf("%s from %s", def["name"], def["from"])
}
