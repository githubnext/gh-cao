package sqlitex

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/githubnext/gh-cao/internal/dashboardquery"

	"github.com/githubnext/gh-cao/server/internal/dashboardstore"
	"github.com/githubnext/gh-cao/server/internal/logger"
	"github.com/githubnext/gh-cao/server/internal/model"
	"github.com/githubnext/gh-cao/server/internal/postgresx"
	"github.com/githubnext/gh-cao/server/internal/query"
)

var statisticalLog = logger.New("cao:sqlitex:sampling")

const (
	maxSnapshotInputRows     = 500_000
	maxSnapshotWorkingBytes  = 1 << 30
	maxSnapshotRetainedRows  = 2_000_000
	maxSnapshotRetainedBytes = 2 << 30
	maxSnapshotOperations    = 50_000_000
)

func (r *reader) ExecuteSQLPlan(ctx context.Context, definitions []query.Definition, requested []string) (map[string]model.Source, model.Metrics, error) {
	return r.ExecuteSQLPlanWithOptions(ctx, definitions, requested, dashboardstore.ExecutionOptions{})
}

func (r *reader) ExecuteSQLPlanWithOptions(ctx context.Context, definitions []query.Definition, requested []string, options dashboardstore.ExecutionOptions) (map[string]model.Source, model.Metrics, error) {
	metrics := model.Metrics{PushedDown: []string{}, FallbackOperations: []string{"local-go-plan"}}
	if options.ResourceLimits != nil {
		return nil, metrics, errors.New("local SQLite queries do not support overriding resource limits")
	}
	if len(options.Repositories) > 0 {
		return nil, metrics, errors.New("local SQLite queries do not support repository scoping")
	}
	if err := query.Validate(definitions); err != nil {
		return nil, metrics, err
	}
	order, err := query.Dependencies(definitions, requested)
	if err != nil {
		return nil, metrics, err
	}
	if len(order) > query.MaxPlanQueries {
		return nil, metrics, errors.New("local query exceeds max plan queries")
	}
	var keysetPage *dashboardstore.Page
	for output, page := range options.Pages {
		if page.Keyset == nil {
			continue
		}
		if keysetPage != nil {
			return nil, metrics, errors.New("one query request may contain only one keyset page")
		}
		plan, err := query.ResolvePagePlan(definitions, output)
		if err != nil {
			return nil, metrics, err
		}
		if plan == nil || plan.Boundary != page.Keyset.Boundary || plan.Comparator != page.Keyset.Comparator {
			return nil, metrics, fmt.Errorf("invalid or stale continuation token for %q", output)
		}
		keysetPage = &page
	}
	executionDefinitions, fields := projectSnapshotPlan(definitions, requested, order)
	var valueSelections []dashboardquery.EvidenceSelection
	for _, selection := range query.OperationalValueEvidenceSelections(definitions, requested) {
		valueSelections = append(valueSelections, dashboardquery.EvidenceSelection{Field: selection.Field, Value: selection.Value})
	}
	var measurementSelections []dashboardquery.EvidenceSelection
	for _, selection := range query.SourceEqualities(definitions, requested, "workflow-value-measurements",
		"$operationalValues", "id", "workflow-route") {
		measurementSelections = append(measurementSelections, dashboardquery.EvidenceSelection{Field: selection.Field, Value: selection.Value})
	}
	usesMeasurements := slices.Contains(order, "workflow-value-measurements")
	state, err := r.State(ctx)
	if err != nil {
		return nil, metrics, err
	}
	var workflowScope *workflowSubjectScope
	if keysetPage != nil {
		scope, err := readWorkflowSubjectScope(ctx, r.tx, *keysetPage)
		if err != nil {
			return nil, metrics, err
		}
		workflowScope = &scope
		valueSelections = nil
		measurementSelections = nil
		if len(scope.routes) == 0 {
			scope.routes = []string{"invalid"}
		}
		for _, route := range scope.routes {
			valueSelections = append(valueSelections, dashboardquery.EvidenceSelection{Field: "workflow-route", Value: route})
			measurementSelections = append(measurementSelections, dashboardquery.EvidenceSelection{Field: "workflow-route", Value: route})
		}
	}
	roots := map[string]model.Metadata{}
	cache := map[string][]map[string]any{}
	uses := map[string]int{}
	reachable := map[string]bool{}
	for _, name := range order {
		reachable[name] = true
	}
	for _, definition := range definitions {
		if reachable[definition.Name] {
			for _, input := range snapshotInputs(definition) {
				uses[input]++
			}
		}
	}
	for _, name := range requested {
		uses[name]++
	}
	sizes := map[string]int64{}
	var retainedRows int
	var retainedBytes int64
	evaluator, err := dashboardquery.NewEvaluatorWithLimits(ctx, maxSnapshotInputRows, maxSnapshotOperations)
	if err != nil {
		return nil, metrics, err
	}
	retain := func(name string, rows []map[string]any) error {
		owned := make([]model.Row, len(rows))
		for index, row := range rows {
			owned[index] = model.Row(row)
		}
		bytes := query.EstimateRowsBytesWithLimit(owned, maxSnapshotRetainedBytes)
		metrics.PeakWorkingRows = max(metrics.PeakWorkingRows, len(rows))
		metrics.PeakWorkingBytes = max(metrics.PeakWorkingBytes, bytes)
		retainedRows += len(rows)
		retainedBytes += bytes
		metrics.RetainedRows = max(metrics.RetainedRows, retainedRows)
		metrics.RetainedBytes = max(metrics.RetainedBytes, retainedBytes)
		metrics.Operations = int(evaluator.Operations())
		if len(rows) > maxSnapshotInputRows || bytes > maxSnapshotWorkingBytes {
			return fmt.Errorf("local query %q exceeds working input budget", name)
		}
		if retainedRows > maxSnapshotRetainedRows || retainedBytes > maxSnapshotRetainedBytes {
			return &query.PlanLimitError{QueryID: name, Boundary: query.BoundaryRetainedBytes}
		}
		cache[name] = rows
		sizes[name] = bytes
		return nil
	}
	loadRoot := func(name string) error {
		if _, exists := cache[name]; exists {
			return nil
		}
		source, err := r.source(ctx, name, state, options, fields[name], valueSelections, measurementSelections, workflowScope, usesMeasurements)
		if err != nil {
			return err
		}
		roots[name] = source.Metadata
		rows := make([]map[string]any, len(source.Rows))
		for index, row := range source.Rows {
			rows[index] = map[string]any(row)
		}
		return retain(name, rows)
	}
	err = query.WalkPlan(executionDefinitions, requested, loadRoot, func(definition query.Definition) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		content, err := json.Marshal(definition)
		if err != nil {
			return err
		}
		var plan map[string]any
		if err := json.Unmarshal(content, &plan); err != nil {
			return err
		}
		rows, err := evaluator.Execute(plan, func(input string) ([]map[string]any, error) {
			rows, exists := cache[input]
			if !exists {
				return nil, fmt.Errorf("local query input %q is unavailable", input)
			}
			return rows, nil
		})
		if err != nil {
			return err
		}
		metrics.QueryCount++
		if postgresx.ComposeMetadata(definitions, roots, definition.Name)["availability"] == "unavailable" {
			rows = nil
		}
		if err := retain(definition.Name, rows); err != nil {
			return err
		}
		// Results are owned clones, so completed inputs can leave the live DAG.
		for _, input := range snapshotInputs(definition) {
			uses[input]--
			if uses[input] == 0 {
				retainedRows -= len(cache[input])
				retainedBytes -= sizes[input]
				delete(cache, input)
				delete(sizes, input)
			}
		}
		return nil
	})
	if err != nil {
		return nil, metrics, err
	}
	output := make(map[string]model.Source, len(requested))
	for _, name := range requested {
		rows := cache[name]
		total := len(rows)
		metadata := postgresx.ComposeMetadata(definitions, roots, name)
		owned := model.Metadata{}
		for key, value := range metadata {
			owned[key] = value
		}
		metadata = owned
		if metadata["availability"] == "unavailable" {
			rows, total = nil, 0
		}
		page, paginated := options.Pages[name]
		if paginated {
			if page.Offset < 0 || page.Offset > total || page.Limit <= 0 || page.Limit > query.MaxOutputRows {
				return nil, metrics, fmt.Errorf("invalid or stale page for %q", name)
			}
			limit := page.Limit
			if page.Keyset != nil {
				limit++
			}
			rows = rows[page.Offset:min(page.Offset+limit, total)]
			if page.Keyset == nil {
				metadata["total-row-count"] = total
			} else {
				metadata["workflow-subject-revision"] = page.Keyset.Revision
				metadata["continuation-comparator"] = page.Keyset.Comparator
			}
		} else if total > query.MaxOutputRows {
			return nil, metrics, fmt.Errorf("query %q exceeds max output rows", name)
		}
		result := make([]model.Row, len(rows))
		for index, row := range rows {
			result[index] = model.Row(row)
		}
		metadata["source-id"], metadata["source-kind"], metadata["row-count"] = name, "derived", len(result)
		if metadata["availability"] != "unavailable" {
			metadata["availability"] = "available"
			if total == 0 {
				metadata["availability"] = "empty"
			}
		}
		output[name] = model.Source{Source: name, Rows: result, Metadata: metadata}
		metrics.OutputRows += len(result)
	}
	return output, metrics, nil
}

func snapshotInputs(definition query.Definition) []string {
	inputs := append([]string{definition.From}, definition.Union...)
	for _, join := range definition.Joins {
		inputs = append(inputs, join.Source)
	}
	return inputs
}

func keysetPageFromOptions(options dashboardstore.ExecutionOptions) *dashboardstore.Page {
	for _, page := range options.Pages {
		if page.Keyset != nil {
			copy := page
			return &copy
		}
	}
	return nil
}

func readWorkflowSubjectPage(
	ctx context.Context,
	tx *sql.Tx,
	projected []string,
	page dashboardstore.Page,
) ([]map[string]any, error) {
	if page.Keyset == nil || page.Limit <= 0 || page.Limit > 32 ||
		len(page.Keyset.Keys) != 3 || (len(page.Keyset.After) != 0 && len(page.Keyset.After) != 3) {
		return nil, errors.New("invalid workflow subject page")
	}
	statement := `SELECT value FROM __idb_records
		WHERE database_name=? AND store_name='workflows'`
	args := []any{dashboardquery.DatabaseName}
	if len(page.Keyset.After) != 0 {
		statement += ` AND (
			json_extract(value,'$.repositoryId') COLLATE BINARY,
			coalesce(json_extract(value,'$.path'),'') COLLATE BINARY,
			json_extract(value,'$.id') COLLATE BINARY
		) > (? COLLATE BINARY,? COLLATE BINARY,? COLLATE BINARY)`
		args = append(args, page.Keyset.After[0], page.Keyset.After[1], page.Keyset.After[2])
	}
	statement += ` ORDER BY
		json_extract(value,'$.repositoryId') COLLATE BINARY,
		coalesce(json_extract(value,'$.path'),'') COLLATE BINARY,
		json_extract(value,'$.id') COLLATE BINARY LIMIT ?`
	args = append(args, page.Limit+1)
	rows, err := tx.QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	result := []map[string]any{}
	for rows.Next() {
		var content string
		if err := rows.Scan(&content); err != nil {
			return nil, err
		}
		var record map[string]any
		if err := json.Unmarshal([]byte(content), &record); err != nil {
			return nil, err
		}
		if len(projected) != 0 {
			selected := make(map[string]any, len(projected))
			for _, field := range projected {
				if value, present := record[field]; present {
					selected[field] = value
				}
			}
			record = selected
		}
		result = append(result, record)
	}
	return result, rows.Err()
}

type workflowSubjectScope struct {
	routes              []string
	collectionRecordIDs []string
	repositoryIDs       []string
	campaignIDs         []string
}

func readWorkflowSubjectScope(
	ctx context.Context,
	tx *sql.Tx,
	page dashboardstore.Page,
) (workflowSubjectScope, error) {
	workflows, err := readWorkflowSubjectPage(ctx, tx, []string{"id", "repositoryId", "campaignId", "path"}, page)
	if err != nil {
		return workflowSubjectScope{}, err
	}
	if len(workflows) > page.Limit {
		workflows = workflows[:page.Limit]
	}
	scope := workflowSubjectScope{repositoryIDs: make([]string, 0, len(workflows))}
	seen := map[string]bool{}
	for _, workflow := range workflows {
		repositoryID, _ := workflow["repositoryId"].(string)
		if repositoryID != "" && !seen[repositoryID] {
			seen[repositoryID] = true
			scope.repositoryIDs = append(scope.repositoryIDs, repositoryID)
		}
		campaignID, _ := workflow["campaignId"].(string)
		if campaignID != "" && !seen["campaign:"+campaignID] {
			seen["campaign:"+campaignID] = true
			scope.campaignIDs = append(scope.campaignIDs, campaignID)
		}
	}
	repositories, err := dashboardquery.ReadCollectionRecords(
		ctx, tx, "repositories", scope.repositoryIDs, []string{"id", "owner", "name"}, maxSnapshotWorkingBytes,
	)
	if err != nil {
		return workflowSubjectScope{}, err
	}
	coordinates := make(map[string]string, len(repositories))
	for _, repository := range repositories {
		id, _ := repository["id"].(string)
		owner, _ := repository["owner"].(string)
		name, _ := repository["name"].(string)
		if id != "" && owner != "" && name != "" {
			coordinates[id] = owner + "/" + name
		}
	}
	scope.routes = make([]string, 0, len(workflows))
	scope.collectionRecordIDs = make([]string, 0, len(workflows))
	for _, workflow := range workflows {
		repositoryID, _ := workflow["repositoryId"].(string)
		path, _ := workflow["path"].(string)
		coordinate := coordinates[repositoryID]
		if coordinate == "" || path == "" {
			continue
		}
		scope.routes = append(scope.routes, coordinate+":"+path)
		slug := strings.TrimSuffix(strings.TrimPrefix(path, ".github/workflows/"), ".md")
		scope.collectionRecordIDs = append(scope.collectionRecordIDs,
			"workflow-value-collection:"+model.EncodeCoordinate(strings.ToLower(coordinate)+":"+slug))
	}
	return scope, nil
}

func (r *reader) source(
	ctx context.Context,
	name string,
	state dashboardstore.State,
	options dashboardstore.ExecutionOptions,
	fields snapshotFields,
	valueSelections, measurementSelections []dashboardquery.EvidenceSelection,
	workflowScope *workflowSubjectScope,
	usesMeasurements bool,
) (model.Source, error) {
	if name == "$statisticalMembership" {
		quality := r.store.metadata["$runs"]
		watermark, _ := quality["as-of"].(string)
		policy := "recorded-run-as-of"
		rows, watermark, err := dashboardquery.ReadStatisticalRunCandidates(ctx, r.tx, watermark,
			[]string{"id", "owner", "repository", "githubRunId", "startedAt", "createdAt", "completedAt", "updatedAt", "status", "conclusion"},
			dashboardquery.StatisticalMaxInputRows, dashboardquery.StatisticalMaxInputBytes)
		if err != nil {
			if errors.Is(err, dashboardquery.ErrSnapshotInputBudget) {
				statisticalLog.Print("event=statistical-source status=unavailable reason=input-budget")
				return model.Source{Source: name, Rows: []model.Row{}, Metadata: model.Metadata{
					"source-id": name, "availability": "unavailable", "completeness": "unknown",
					"error": "statistical candidate frame exceeds input budget",
				}}, nil
			}
			return model.Source{}, err
		}
		// Unlike exact-source presentation clocks, sampling never falls back to
		// the server's evaluation time when the recorded clock is absent.
		if _, recorded := quality["as-of"].(string); !recorded || quality["as-of"] == "" {
			policy = "canonical-run-max"
		}
		if watermark == "" {
			statisticalLog.Print("event=statistical-source status=unavailable reason=missing-clock")
			return model.Source{Source: name, Rows: []model.Row{}, Metadata: model.Metadata{
				"source-id": name, "availability": "unavailable", "completeness": "unknown",
				"error": "statistical sampling requires a recorded canonical watermark",
			}}, nil
		}
		selected, frame, err := dashboardquery.SampleStatisticalRuns(rows, dashboardquery.StatisticalOptions{
			Context: ctx, Logf: statisticalLog.Printf,
			Watermark: watermark, Capacity: dashboardquery.StatisticalSQLiteCapacity,
			Complete: quality["completeness"] == "complete" && quality["availability"] != "unavailable",
		})
		if err != nil {
			if errors.Is(err, dashboardquery.ErrStatisticalBudget) {
				return model.Source{Source: name, Rows: []model.Row{}, Metadata: model.Metadata{
					"source-id": name, "availability": "unavailable", "completeness": "unknown",
					"error": "statistical candidate frame exceeds input budget",
				}}, nil
			}
			return model.Source{}, err
		}
		frame["watermark-policy"] = policy
		members := dashboardquery.StatisticalMembership(selected)
		result := make([]model.Row, len(members))
		for index, member := range members {
			result[index] = model.Row(member)
		}
		metadata := model.Metadata{"source-id": name, "as-of": watermark, "completeness": quality["completeness"],
			"freshness": quality["freshness"], "availability": "available", "statistical-frame": frame}
		if quality["availability"] == "unavailable" {
			metadata["availability"] = "unavailable"
		}
		return model.Source{Source: name, Rows: result, Metadata: metadata}, nil
	}
	if name == "daily-run-counts" || name == "$dailyRunCounts" {
		read := dashboardquery.ReadDailyRunCounts
		if name == "$dailyRunCounts" {
			read = dashboardquery.ReadDailyRunCountRecords
		}
		rows, available, undated, err := read(ctx, r.tx, maxSnapshotInputRows, maxSnapshotWorkingBytes)
		if err != nil {
			return model.Source{}, err
		}
		metadata := model.Metadata{"source-id": name, "source-kind": "canonical", "availability": "unavailable", "completeness": "unknown", "freshness": "unknown",
			"aggregate-version": 1, "aggregate-time-grain": "utc-run-start-day", "aggregate-coverage": "absent"}
		if available {
			metadata["availability"], metadata["completeness"] = "available", "partial"
			if len(rows) == 0 {
				metadata["availability"] = "empty"
			}
			metadata["undated-run-count"] = undated
			metadata["excluded-runs"] = undated
			metadata["retention"] = "all-admitted-dated-observations"
			states, err := dashboardquery.ReadCollectionFields(ctx, r.tx, "dailyRunCountState",
				[]string{"id", "coverage", "excludedRuns", "conflictedRuns", "retentionCutoff", "retentionWatermark"}, 1, 64<<10)
			if err != nil {
				return model.Source{}, err
			}
			for _, dailyState := range states {
				if dailyState["id"] != "daily-run-counts" {
					continue
				}
				if dailyState["coverage"] != nil {
					metadata["aggregate-coverage"] = dailyState["coverage"]
				}
				for field, key := range map[string]string{"excluded-runs": "excludedRuns", "conflicted-runs": "conflictedRuns"} {
					if dailyState[key] != nil {
						metadata[field] = dailyState[key]
					}
				}
				if dailyState["retentionCutoff"] == nil {
					continue
				}
				cutoff, ok := dailyState["retentionCutoff"].(string)
				if !ok {
					return model.Source{}, errors.New("daily run count retention cutoff must be a UTC date")
				}
				if cutoff == "" {
					continue
				}
				if _, err := time.Parse("2006-01-02", cutoff); err != nil {
					return model.Source{}, errors.New("daily run count retention cutoff must be a UTC date")
				}
				metadata["retention"] = "three-utc-calendar-weeks"
				metadata["aggregate-retention-start"] = cutoff
				if dailyState["retentionWatermark"] != nil {
					watermark, ok := dailyState["retentionWatermark"].(float64)
					if !ok || math.IsNaN(watermark) || math.IsInf(watermark, 0) || watermark < 0 || watermark > 253402300799999 {
						return model.Source{}, errors.New("daily run count retention watermark must be Unix milliseconds")
					}
					metadata["aggregate-retention-watermark"] = time.UnixMilli(int64(watermark)).UTC().Format(time.RFC3339Nano)
				}
			}
		}
		localMetadata, err := dashboardquery.LocalDashboardMetadata(ctx, r.tx)
		if err != nil {
			return model.Source{}, err
		}
		for key, value := range localMetadata {
			metadata[key] = value
		}
		result := make([]model.Row, len(rows))
		for index, row := range rows {
			result[index] = model.Row(row)
		}
		return model.Source{Source: name, Rows: result, Metadata: metadata}, nil
	}
	if postgresx.IsRuntimeSource(name) && options.Runtime != nil {
		return options.Runtime(ctx, name)
	}
	collection := strings.TrimPrefix(name, "$")
	if strings.HasPrefix(name, "$") && canonicalCollection(collection) {
		if _, declared := state.Counts[name]; !declared {
			return model.Source{}, fmt.Errorf("canonical SQLite source %q is not present in the snapshot", name)
		}
		var projected []string
		if !fields.all && collection != "operationalValueEvidence" {
			projected = make([]string, 0, len(fields.fields))
			for field := range fields.fields {
				projected = append(projected, field)
			}
			sort.Strings(projected)
		}
		var rows []map[string]any
		var err error
		switch {
		case collection == "repositories" && workflowScope != nil:
			rows, err = dashboardquery.ReadCollectionRecords(
				ctx, r.tx, collection, workflowScope.repositoryIDs, projected, maxSnapshotWorkingBytes,
			)
		case collection == "campaigns" && workflowScope != nil:
			rows, err = dashboardquery.ReadCollectionRecords(
				ctx, r.tx, collection, workflowScope.campaignIDs, projected, maxSnapshotWorkingBytes,
			)
		case collection == "operationalValueEvidence":
			rows, err = dashboardquery.ReadEvidencePackets(ctx, r.tx, valueSelections)
		case collection == "workflowValueCollections" && workflowScope != nil:
			rows, err = dashboardquery.ReadCollectionRecords(
				ctx, r.tx, collection, workflowScope.collectionRecordIDs, projected, maxSnapshotWorkingBytes,
			)
		case collection == "operationalValues" && usesMeasurements:
			rows, err = dashboardquery.ReadMeasurements(ctx, r.tx, measurementSelections)
		case collection == "workflows" && keysetPageFromOptions(options) != nil:
			rows, err = readWorkflowSubjectPage(ctx, r.tx, projected, *keysetPageFromOptions(options))
		default:
			rows, err = dashboardquery.ReadCollectionFields(ctx, r.tx, collection, projected, maxSnapshotInputRows, maxSnapshotWorkingBytes)
		}
		if err != nil {
			return model.Source{}, err
		}
		if collection == "operationalValues" {
			dashboardquery.MeasurementRows(rows)
		}
		metadata := model.Metadata{
			"source-id": name, "completeness": "unknown", "freshness": "unknown",
			"as-of": state.EvaluatedAt.Format("2006-01-02T15:04:05.999999999Z07:00"),
		}
		for key, value := range r.store.metadata[name] {
			metadata[key] = value
		}
		localMetadata, err := dashboardquery.LocalDashboardMetadata(ctx, r.tx)
		if err != nil {
			return model.Source{}, err
		}
		for key, value := range localMetadata {
			metadata[key] = value
		}
		if name == "$workflows" {
			metadata["workflow-subject-revision"] = state.WorkflowSubjectRevision
		}
		if collection == "operationalValueEvidence" {
			metadata["evidence-packets-read"] = len(rows)
			metadata["evidence-scoped-read"] = len(valueSelections) > 0
			clocks, err := dashboardquery.OperationalValueEvidenceMetadata(rows)
			if err != nil {
				return model.Source{}, err
			}
			for field, value := range clocks {
				metadata[field] = value
			}
			rows, err = dashboardquery.OperationalValueEvidenceRows(rows)
			if err != nil {
				return model.Source{}, err
			}
			metadata["evidence-facts-materialized"] = len(rows)
		}
		result := make([]model.Row, len(rows))
		for index, row := range rows {
			result[index] = model.Row(row)
		}
		availability := "available"
		if len(result) == 0 {
			availability = "empty"
		}
		if metadata["availability"] != "unavailable" {
			metadata["availability"] = availability
		}
		return model.Source{Source: name, Rows: result, Metadata: metadata}, nil
	}
	return model.Source{}, fmt.Errorf("local query source %q is not registered", name)
}
