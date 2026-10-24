package cli

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"slices"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/spf13/cobra"

	"github.com/githubnext/gh-cao/internal/dashboardquery"
)

func nativeQuery(cmd *cobra.Command, args []string) (err error) {
	id := ""
	if len(args) > 0 {
		id = args[0]
	} else if nativeString(cmd, "collection") == "" && !nativeBool(cmd, "stdin") {
		id = nativeString(cmd, "id")
	}
	if err := requireNativeSnapshot(cmd, id != ""); err != nil {
		return err
	}
	db, err := openNativeSnapshot(nativeSnapshotPath(cmd))
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, db.Close()) }()
	if id != "" {
		for _, flag := range []string{"collection", "stdin", "where"} {
			if cmd.Flags().Changed(flag) {
				return fmt.Errorf("--%s cannot be combined with a named query", flag)
			}
		}
		catalog, err := loadNativeCatalog(nativeString(cmd, "dashboard"))
		if err != nil {
			return err
		}
		parameters, err := nativeParseParameters(nativeStrings(cmd, "param"))
		if err != nil {
			return err
		}
		limit, err := nativeLimit(cmd, 500)
		if err != nil {
			return err
		}
		result, err := catalog.runQuery(cmd.Context(), db, id, parameters, limit)
		if err != nil {
			if len(args) == 0 && strings.HasPrefix(err.Error(), "Unknown dashboard query") {
				return fmt.Errorf("%w; pass --collection COLLECTION to read one canonical record by id", err)
			}
			return err
		}
		return printJSON(cmd, result)
	}
	if cmd.Flags().Changed("param") || cmd.Flags().Changed("dashboard") {
		return fmt.Errorf("--param and --dashboard require a named dashboard query")
	}
	if nativeBool(cmd, "stdin") {
		content, err := io.ReadAll(io.LimitReader(cmd.InOrStdin(), 1024*1024+1))
		if err != nil {
			return err
		}
		if len(content) > 1024*1024 {
			return fmt.Errorf("raw query exceeds 1048576 bytes")
		}
		var definition map[string]any
		if err := json.Unmarshal(content, &definition); err != nil || definition == nil {
			return fmt.Errorf("--stdin requires one JSON query object")
		}
		rows, err := dashboardquery.Execute(cmd.Context(), definition, func(source string) ([]map[string]any, error) {
			if !slices.Contains(nativeCollections, source) {
				return nil, nativeErrorf("Unknown collection: %s", source)
			}
			return readNativeCollection(cmd.Context(), db, source)
		})
		if err != nil {
			return err
		}
		return printJSON(cmd, rows)
	}
	collection := nativeString(cmd, "collection")
	if !slices.Contains(nativeCollections, collection) {
		return nativeErrorf("Unknown collection: %s", collection)
	}
	rows, err := readNativeCollection(cmd.Context(), db, collection)
	if err != nil {
		return err
	}
	if id := nativeString(cmd, "id"); id != "" {
		rows = slices.DeleteFunc(rows, func(row map[string]any) bool { return row["id"] != id })
	}
	for _, filter := range nativeStrings(cmd, "where") {
		field, value, ok := strings.Cut(filter, "=")
		if !ok || field == "" {
			return nativeErrorf("Invalid --where value: %s", filter)
		}
		rows = slices.DeleteFunc(rows, func(row map[string]any) bool {
			observed, exists := nativeFieldLookup(row, field)
			if !exists {
				return value != "undefined"
			}
			return nativeScalar(observed) != value
		})
	}
	limit, err := nativeLimit(cmd, len(rows))
	if err != nil {
		return err
	}
	if limit < len(rows) {
		rows = rows[:limit]
	}
	return printJSON(cmd, rows)
}

func nativeParseParameters(values []string) (map[string]any, error) {
	parameters := map[string]any{}
	for _, entry := range values {
		name, value, ok := strings.Cut(entry, "=")
		if !ok || name == "" {
			return nil, nativeErrorf("Invalid --param value: %s", entry)
		}
		if _, exists := parameters[name]; exists {
			return nil, nativeErrorf("Parameter --param %s may only be specified once", name)
		}
		parameters[name] = value
	}
	return parameters, nil
}

func (c *nativeCatalog) resolveParameters(id string, supplied map[string]any) ([]map[string]any, error) {
	if len(supplied) > 8 {
		return nil, nativeErrorf("At most 8 query parameters are accepted")
	}
	declared := c.queryParameters(id)
	for _, parameter := range declared {
		if _, exists := supplied[nativeText(parameter["name"])]; parameter["required"] == true && !exists {
			return nil, nativeErrorf("Query %s requires parameter %q", id, parameter["name"])
		}
	}
	names := []string{}
	for name := range supplied {
		names = append(names, name)
	}
	slices.Sort(names)
	result := []map[string]any{}
	for _, name := range names {
		value := supplied[name]
		var parameter map[string]any
		known := []string{}
		for _, entry := range declared {
			known = append(known, nativeText(entry["name"]))
			if entry["name"] == name {
				parameter = entry
			}
		}
		if parameter == nil {
			label := strings.Join(known, ", ")
			if label == "" {
				label = "none"
			}
			return nil, nativeErrorf("Unknown parameter %q for query %s; declared parameters: %s", name, id, label)
		}
		switch value.(type) {
		case string, bool, float64:
		default:
			return nil, nativeErrorf("Parameter %q must be a string, number, or boolean", name)
		}
		if len(utf16.Encode([]rune(nativeScalar(value)))) > 256 {
			return nil, nativeErrorf("Parameter %q exceeds 256 characters", name)
		}
		kind := nativeText(parameter["type"])
		if kind == "" {
			result = append(result, map[string]any{"name": name, "field": parameter["field"], "value": nativeScalar(value)})
			continue
		}
		schema := nativeObject(parameter["schema"])
		valid := false
		switch kind {
		case "string":
			_, valid = value.(string)
		case "boolean":
			_, valid = value.(bool)
		case "number":
			if number, ok := value.(float64); ok && !math.IsNaN(number) && !math.IsInf(number, 0) {
				valid = true
				if min, ok := schema["minimum"].(float64); ok && number < min {
					valid = false
				}
				if max, ok := schema["maximum"].(float64); ok && number > max {
					valid = false
				}
				if step, ok := schema["multipleOf"].(float64); ok && math.Abs(number/step-math.Round(number/step)) > 1e-9 {
					valid = false
				}
			}
		}
		if values := nativeArray(schema["enum"]); values != nil && !slices.Contains(values, value) {
			valid = false
		}
		if !valid {
			return nil, nativeErrorf("Invalid value for query parameter %q", name)
		}
		result = append(result, map[string]any{"name": name, "type": kind, "value": value})
	}
	return result, nil
}

func nativeResolveOperands(value any, supplied map[string]any, container string) any {
	if array, ok := value.([]any); ok {
		result := make([]any, len(array))
		for i, entry := range array {
			result[i] = nativeResolveOperands(entry, supplied, container)
		}
		return result
	}
	if object := nativeObject(value); object != nil {
		if len(object) == 1 && nativeText(object["parameter"]) != "" {
			value := supplied[nativeText(object["parameter"])]
			if container == "args" {
				return map[string]any{"value": value}
			}
			return value
		}
		result := map[string]any{}
		for key, entry := range object {
			result[key] = nativeResolveOperands(entry, supplied, key)
		}
		return result
	}
	return value
}

func (c *nativeCatalog) runQuery(ctx context.Context, db *sql.DB, id string, parameters map[string]any, limit int) (map[string]any, error) {
	id = strings.TrimSpace(id)
	if c.index[id] == nil {
		return nil, nativeErrorf("Unknown dashboard query: %s", id)
	}
	resolved, err := c.resolveParameters(id, parameters)
	if err != nil {
		return nil, err
	}
	if limit < 1 {
		return nil, nativeErrorf("Query limit must be a positive integer")
	}
	limit = min(limit, 5000)
	execution := c.execution(id)
	unavailable := func(reason string) map[string]any {
		return map[string]any{"query": id, "rows": []map[string]any{}, "metadata": map[string]any{
			"availability": "unavailable", "completeness": "unknown", "freshness": "unknown",
			"query-diagnostic": reason, "requirements": execution["requirements"]}}
	}
	if execution["local"] != true {
		return unavailable(nativeText(execution["reason"])), nil
	}
	cache := map[string][]map[string]any{}
	visiting := map[string]bool{}
	canonicalRead := false
	otherCanonicalRead := false
	evidenceClocks := map[string]any{}
	var statisticalFrame map[string]any
	aggregateMetadata := map[string]map[string]any{}
	dailyRead := false
	var dailyFailure error
	var load func(string) ([]map[string]any, error)
	load = func(source string) ([]map[string]any, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if rows, ok := cache[source]; ok {
			return rows, nil
		}
		if visiting[source] {
			return nil, nativeErrorf("Cyclic dashboard query: %s", source)
		}
		if len(visiting) >= 128 {
			return nil, nativeErrorf("Dashboard query dependency depth exceeds 128")
		}
		visiting[source] = true
		defer delete(visiting, source)
		var rows []map[string]any
		var err error
		switch {
		case source == "$statisticalMembership":
			canonicalRead, otherCanonicalRead = true, true
			candidates, readErr := load("$runs")
			if readErr != nil {
				return nil, readErr
			}
			selected, frame, sampleErr := dashboardquery.SampleStatisticalRuns(candidates, dashboardquery.StatisticalOptions{
				Context: ctx, Logf: log.Printf,
				Watermark: dashboardquery.CanonicalStatisticalWatermark(candidates), Capacity: dashboardquery.StatisticalSQLiteCapacity,
			})
			if sampleErr != nil {
				return nil, sampleErr
			}
			frame["watermark-policy"] = "canonical-run-max"
			statisticalFrame = frame
			rows = dashboardquery.StatisticalMembership(selected)
		case source == "daily-run-counts":
			canonicalRead, otherCanonicalRead = true, true
			dailyRead = true
			var available bool
			var undated int64
			rows, available, undated, err = dashboardquery.ReadDailyRunCounts(ctx, db, 500_000, 1<<30)
			if err == nil && !available {
				err = nativeErrorf("Daily run counts are not available in this snapshot; download or rebuild an aggregate-capable snapshot")
			}
			if err == nil {
				aggregateMetadata[source], err = nativeDailyCountMetadata(ctx, db, undated)
			}
			if err != nil && dailyFailure == nil {
				dailyFailure = err
			}
		case strings.HasPrefix(source, "$"):
			canonicalRead = true
			otherCanonicalRead = otherCanonicalRead || source != "$operationalValueEvidence"
			rows, err = readNativeCollection(ctx, db, strings.TrimPrefix(source, "$"))
			if err == nil && source == "$operationalValueEvidence" {
				evidenceClocks, err = dashboardquery.OperationalValueEvidenceMetadata(rows)
				if err == nil {
					rows, err = dashboardquery.OperationalValueEvidenceRows(rows)
				}
			}
		case source == "simulation-days":
			rows = []map[string]any{}
			for day := 1; day <= 30; day++ {
				rows = append(rows, map[string]any{"day": float64(day), "date": fmt.Sprintf("2025-01-%02dT00:00:00.000Z", day)})
			}
		default:
			definition := c.index[source]
			isDashboard := definition != nil
			if definition == nil {
				definition = c.database[source]
			}
			if definition == nil {
				return nil, nativeErrorf("No canonical database query or runtime source is registered for %q.", source)
			}
			definition = nativeObject(nativeResolveOperands(definition, parameters, ""))
			inputs := []string{}
			rows, err = dashboardquery.Execute(ctx, definition, func(input string) ([]map[string]any, error) {
				inputs = append(inputs, input)
				if isDashboard && input == source && c.database[source] != nil {
					return dashboardquery.Execute(ctx, c.database[source], load)
				}
				return load(input)
			})
			var provenance map[string]any
			preserveJoined := len(nativeArray(definition["joins"])) > 0 && len(nativeArray(definition["union"])) == 0
			for _, input := range inputs {
				metadata := aggregateMetadata[input]
				if metadata == nil && preserveJoined {
					continue
				}
				if metadata == nil {
					provenance = nil
					break
				}
				provenance = metadata
			}
			aggregateMetadata[source] = provenance
		}
		if err != nil {
			return nil, err
		}
		if rows == nil {
			rows = []map[string]any{}
		}
		cache[source] = rows
		return rows, nil
	}
	rows, err := load(id)
	// Daily evidence is required even when reached through an optional left join.
	if dailyFailure != nil {
		err = dailyFailure
	}
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return unavailable(err.Error()), nil
	}
	for _, parameter := range resolved {
		if field := nativeText(parameter["field"]); field != "" {
			rows = slices.DeleteFunc(slices.Clone(rows), func(row map[string]any) bool {
				return nativeField(row, field) != parameter["value"]
			})
		}
	}
	completeness, freshness := "complete", "fresh"
	if canonicalRead {
		completeness, freshness = "unknown", "unknown"
	}
	if dailyRead {
		// A snapshot proves available evidence, not exhaustive upstream history.
		completeness = "partial"
	}
	if len(rows) > limit {
		rows, completeness = rows[:limit], "partial"
	}
	availability := "available"
	if len(rows) == 0 {
		availability = "empty"
	}
	metadata := map[string]any{"availability": availability, "completeness": completeness, "freshness": freshness,
		"as-of": "1970-01-01T00:00:00.000Z", "returned-rows": len(rows), "limit": limit}
	if statisticalFrame != nil {
		metadata["statistical-frame"], metadata["as-of"] = statisticalFrame, statisticalFrame["watermark"]
		metadata["sample-frame"] = statisticalFrame
	}
	for _, field := range []string{"as-of", "retrieved-at"} {
		if instant, present := evidenceClocks[field]; present && !otherCanonicalRead {
			metadata[field] = instant
		}
	}
	for field, value := range aggregateMetadata[id] {
		metadata[field] = value
	}
	if len(resolved) > 0 {
		values := map[string]any{}
		for _, parameter := range resolved {
			values[nativeText(parameter["name"])] = parameter["value"]
		}
		metadata["parameters"] = values
	}
	return map[string]any{"query": id, "rows": rows, "metadata": metadata}, nil
}

func nativeDailyCountMetadata(ctx context.Context, db *sql.DB, undated int64) (map[string]any, error) {
	states, err := dashboardquery.ReadCollectionFields(ctx, db, "dailyRunCountState",
		[]string{"id", "coverage", "excludedRuns", "conflictedRuns", "retentionCutoff", "retentionWatermark"}, 1, 64<<10)
	if err != nil {
		return nil, err
	}
	metadata := map[string]any{"aggregate-version": 1, "aggregate-time-grain": "utc-run-start-day",
		"aggregate-coverage": "absent", "excluded-runs": float64(undated), "conflicted-runs": 0}
	for _, state := range states {
		if state["id"] != "daily-run-counts" {
			continue
		}
		if state["coverage"] != nil {
			metadata["aggregate-coverage"] = state["coverage"]
		}
		for field, key := range map[string]string{"excluded-runs": "excludedRuns", "conflicted-runs": "conflictedRuns"} {
			if state[key] != nil {
				metadata[field] = state[key]
			}
		}
		if cutoff := nativeText(state["retentionCutoff"]); cutoff != "" {
			if _, err := time.Parse("2006-01-02", cutoff); err != nil {
				return nil, errors.New("daily run count retention cutoff must be a UTC date")
			}
			watermark, ok := state["retentionWatermark"].(float64)
			if !ok || math.IsNaN(watermark) || math.IsInf(watermark, 0) || watermark < 0 || watermark > 253402300799999 {
				return nil, errors.New("daily run count retention watermark must be Unix milliseconds")
			}
			metadata["aggregate-retention-start"] = cutoff
			metadata["aggregate-retention-watermark"] = time.UnixMilli(int64(watermark)).UTC().Format("2006-01-02T15:04:05.000Z")
		}
		if state["coverage"] == "retained-v44" {
			metadata["query-diagnostic"] = "Backfill covers retained v44 runs only; previously evicted history is unavailable."
		} else if nativeNumber(metadata["excluded-runs"]) != 0 || nativeNumber(metadata["conflicted-runs"]) != 0 {
			metadata["query-diagnostic"] = "Some runs have missing start/identity facts or unresolved equal-revision conflicts."
		}
	}
	return metadata, nil
}

func nativeExistingPromptSnapshot(cmd *cobra.Command) (string, error) {
	file := nativeString(cmd, "database")
	if !cmd.Flags().Changed("database") {
		return "", nil
	}
	if _, err := os.Stat(file); err != nil {
		return "", nativeErrorf("No CAO snapshot at %s; pass --database FILE for an existing snapshot or omit it for a definition-only prompt", file)
	}
	return file, nil
}
