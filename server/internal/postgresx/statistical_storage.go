package postgresx

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/githubnext/gh-cao/internal/dashboardquery"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/githubnext/gh-cao/server/internal/inputlimits"
	"github.com/githubnext/gh-cao/server/internal/model"
	"github.com/githubnext/gh-cao/server/internal/query"
)

const statisticalSnapshotPrefix = "$frozen:"

func statisticalRelation(table entityTable, namespace string) query.SQLRelation {
	return query.SQLRelation{
		SQL:    "(SELECT * FROM " + query.SQLIdentifier(table.name) + " WHERE namespace = {}) AS t",
		Params: []any{namespace}, Order: "t.ordinal",
		Columns: relationColumns(table, func(index int) string {
			return "get_bit(t.present_fields, " + strconv.Itoa(index) + ") = 1"
		}),
	}
}

// Only dependencies of statistical views use frozen evidence. Exact consumers
// in the same plan retain their original roots and metadata.
func frozenStatisticalDefinitions(definitions []query.Definition) []query.Definition {
	result := slices.Clone(definitions)
	byName := make(map[string]query.Definition, len(definitions))
	for _, definition := range definitions {
		byName[definition.Name] = definition
	}
	cloned := map[string]bool{}
	var freeze func(string) string
	freeze = func(name string) string {
		if name == "" || name == "$statisticalMembership" {
			return name
		}
		if strings.HasPrefix(name, "$") {
			return statisticalSnapshotPrefix + name
		}
		alias := "frozen:" + name
		if cloned[name] {
			return alias
		}
		cloned[name] = true
		definition, exists := byName[name]
		if !exists {
			return alias // The production compiler fails closed on an absent dependency.
		}
		definition.Name = alias
		definition.From = freeze(definition.From)
		definition.Union = slices.Clone(definition.Union)
		for index, source := range definition.Union {
			definition.Union[index] = freeze(source)
		}
		definition.Joins = slices.Clone(definition.Joins)
		for index, join := range definition.Joins {
			definition.Joins[index].Source = freeze(join.Source)
		}
		result = append(result, definition)
		return alias
	}
	for index, definition := range definitions {
		if !slices.Contains([]string{"statistical-runs", "statistical-tools", "statistical-domains", "statistical-skills", "statistical-audits"}, definition.Name) {
			continue
		}
		from := freeze(definition.From)
		result[index].From = from
		result[index].Joins = slices.Clone(definition.Joins)
		for joinIndex, join := range definition.Joins {
			source := freeze(join.Source)
			result[index].Joins[joinIndex].Source = source
		}
	}
	return result
}

func (w *Writer) statisticalCandidates(ctx context.Context) ([]map[string]any, error) {
	fields := []string{"id", "owner", "repository", "githubRunId", "startedAt", "createdAt", "completedAt", "updatedAt", "status", "conclusion"}
	relation := statisticalRelation(entityTables["$runs"], w.store.namespace)
	projections := make([]string, len(fields))
	for index, field := range fields {
		column := relation.Columns[field]
		projections[index] = "CASE WHEN " + column.Presence + " THEN " + column.Expression + " END"
	}
	//nolint:gosec // Identifiers and expressions are generated from TypeSpec.
	rows, err := w.tx.Query(ctx, "SELECT "+strings.Join(projections, ",")+" FROM "+
		strings.ReplaceAll(relation.SQL, "{}", "$1")+fmt.Sprintf(" LIMIT %d", query.MaxInputRows+1), w.store.namespace)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	candidates := []map[string]any{}
	var bytes int64
	for rows.Next() {
		if len(candidates) == query.MaxInputRows {
			return nil, dashboardquery.ErrStatisticalBudget
		}
		values, err := rows.Values()
		if err != nil {
			return nil, err
		}
		row := map[string]any{}
		for index, field := range fields {
			value := values[index]
			if instant, ok := value.(time.Time); ok {
				value = instant.UTC().Format("2006-01-02T15:04:05.000Z")
			}
			row[field] = value
		}
		bytes += query.EstimateRowsBytes([]model.Row{model.Row(row)})
		if bytes > query.MaxWorkingBytes {
			return nil, dashboardquery.ErrStatisticalBudget
		}
		candidates = append(candidates, row)
	}
	return candidates, rows.Err()
}

func (w *Writer) writeStatisticalRecords(ctx context.Context, source string, records []map[string]any) error {
	table := statisticalTables[source]
	columns := make([]string, 0, 3+len(table.columns))
	columns = append(columns, "namespace", "ordinal", "present_fields")
	for _, column := range table.columns {
		columns = append(columns, column.name)
	}
	_, err := w.tx.CopyFrom(ctx, pgx.Identifier{table.name}, columns, pgx.CopyFromSlice(len(records), func(index int) ([]any, error) {
		presence := make([]byte, len(table.columns))
		values := make([]any, len(columns))
		values[0], values[1] = w.store.namespace, int64(index)
		for fieldIndex, column := range table.columns {
			value, exists := records[index][column.field]
			presence[fieldIndex] = '0'
			if exists {
				presence[fieldIndex] = '1'
			}
			bound, err := column.bind(value)
			if err != nil {
				return nil, fmt.Errorf("bind statistical field %s: %w", column.field, err)
			}
			values[fieldIndex+3] = bound
		}
		// #nosec G115 -- the column count is bounded by the generated TypeSpec registry.
		values[2] = pgtype.Bits{Bytes: presenceBytes(presence), Len: int32(len(presence)), Valid: true}
		return values, nil
	}))
	return err
}

// The namespace writer lock and transaction encompass canonical rows, frozen
// bundles, quality, frame, and the revision. No reader can observe half a frame.
func (w *Writer) persistStatisticalSnapshot(ctx context.Context, dataRevision string, maxBytes int64) error {
	ctx, cancel := context.WithTimeout(ctx, inputlimits.PostgresReadTransactionTimeout)
	defer cancel()
	started := time.Now()
	if _, err := w.tx.Exec(ctx, "SET CONSTRAINTS ALL DEFERRED"); err != nil {
		return err
	}
	for _, source := range statisticalSources() {
		if _, err := w.tx.Exec(ctx, "DELETE FROM "+query.SQLIdentifier(statisticalTables[source].name)+" WHERE namespace=$1", w.store.namespace); err != nil {
			return err
		}
	}
	if _, err := w.tx.Exec(ctx, `DELETE FROM cao_sample_cao_quality WHERE namespace=$1`, w.store.namespace); err != nil {
		return err
	}
	if _, err := w.tx.Exec(ctx, `INSERT INTO cao_sample_cao_quality SELECT * FROM cao_quality WHERE namespace=$1`, w.store.namespace); err != nil {
		return err
	}
	quality := model.Metadata{}
	var availability, completeness, freshness string
	var recorded sql.NullTime
	if err := w.tx.QueryRow(ctx, `SELECT availability,completeness,freshness,as_of FROM cao_quality
		WHERE namespace=$1 AND collection='$runs'`, w.store.namespace).Scan(&availability, &completeness, &freshness, &recorded); err != nil {
		return err
	}
	quality["availability"], quality["completeness"], quality["freshness"] = availability, completeness, freshness
	candidates, inputErr := w.statisticalCandidates(ctx)
	if inputErr != nil && !errors.Is(inputErr, dashboardquery.ErrStatisticalBudget) {
		return inputErr
	}
	selected, frame, err := selectStatisticalSnapshot(ctx, quality, recorded, candidates, inputErr)
	if err != nil {
		return err
	}
	if err := w.writeStatisticalRecords(ctx, "$statisticalMembership", dashboardquery.StatisticalMembership(selected)); err != nil {
		return err
	}
	members := dashboardquery.StatisticalMembership(selected)
	memberRows := make([]model.Row, len(members))
	for index, member := range members {
		memberRows[index] = model.Row(member)
	}
	if err := w.copyStatisticalBundles(ctx, maxBytes-query.EstimateRowsBytes(memberRows)-(1<<20)); err != nil {
		if !errors.Is(err, dashboardquery.ErrStatisticalBudget) {
			return err
		}
		// Failed admission exposes no truncated bundle or falsely valid weights.
		for _, source := range statisticalSources() {
			if _, err := w.tx.Exec(ctx, "DELETE FROM "+query.SQLIdentifier(statisticalTables[source].name)+" WHERE namespace=$1", w.store.namespace); err != nil {
				return err
			}
		}
		frame = map[string]any{"weight-valid": false}
		quality["availability"], quality["completeness"] = "unavailable", "unknown"
		quality["error"] = "statistical bundles exceed snapshot storage budget"
		statisticalLog.Print("event=statistical-publication status=unavailable reason=bundle-budget")
	}
	if strata, ok := frame["strata"].([]map[string]any); ok {
		for _, stratum := range strata {
			stratum["id"] = stratum["stratum"]
		}
		if err := w.writeStatisticalRecords(ctx, "$statisticalStrata", strata); err != nil {
			return err
		}
	}
	frame["id"], frame["source-data-revision"], frame["storage-mode"] = "snapshot", dataRevision, "persisted-bundles"
	var revision int64
	if err := w.tx.QueryRow(ctx, `SELECT revision+1 FROM cao_state WHERE namespace=$1`, w.store.namespace).Scan(&revision); err != nil {
		return err
	}
	frame["source-revision"] = revision
	for _, field := range []string{"availability", "completeness", "freshness", "error"} {
		if value, present := quality[field]; present {
			frame[field] = value
		}
	}
	if err := w.writeStatisticalRecords(ctx, "$statisticalFrame", []map[string]any{frame}); err != nil {
		return err
	}
	statisticalLog.Printf("event=statistical-publication status=completed availability=%s elapsed-ms=%d", quality["availability"], time.Since(started).Milliseconds())
	return nil
}

func statisticalSources() []string {
	sources := make([]string, 0, len(statisticalTables))
	for source := range statisticalTables {
		sources = append(sources, source)
	}
	slices.Sort(sources)
	return sources
}

func (w *Writer) copyStatisticalBundles(ctx context.Context, maxBytes int64) error {
	var bytes, count int64
	// Copy children before their structural parents, following the full reference
	// closure (including optional Experiment links on grader/eval observations).
	for _, source := range []string{
		"$runs", "$domains", "$tools", "$skills", "$friction", "$audits", "$issues",
		"$experimentAssignments", "$graderObservations", "$evalObservations",
		"$experiments", "$graders", "$evals", "$workflows", "$repositories", "$campaigns",
	} {
		table := entityTables[source]
		predicate := ""
		switch source {
		case "$runs":
			predicate = `EXISTS(SELECT 1 FROM cao_sample_statistical_membership m WHERE m.namespace=t.namespace AND m.id=t.id)`
		case "$workflows":
			predicate = `EXISTS(SELECT 1 FROM cao_sample_runs r WHERE r.namespace=t.namespace AND r.workflow_id=t.id)`
			for _, definition := range []string{"experiments", "graders", "evals"} {
				predicate += ` OR EXISTS(SELECT 1 FROM cao_sample_` + definition + ` d WHERE d.namespace=t.namespace AND d.workflow_id=t.id)`
			}
		case "$repositories":
			predicate = `EXISTS(SELECT 1 FROM cao_sample_workflows w WHERE w.namespace=t.namespace AND w.repository_id=t.id)
				OR EXISTS(SELECT 1 FROM cao_sample_runs r WHERE r.namespace=t.namespace AND r.repository_id=t.id)`
		case "$campaigns":
			predicate = `EXISTS(SELECT 1 FROM cao_sample_workflows w WHERE w.namespace=t.namespace AND w.campaign_id=t.id)`
		case "$experiments":
			for _, child := range []string{"experiment_assignments", "grader_observations", "eval_observations"} {
				if predicate != "" {
					predicate += " OR "
				}
				predicate += `EXISTS(SELECT 1 FROM cao_sample_` + child + ` c WHERE c.namespace=t.namespace AND c.experiment_id=t.id)`
			}
		case "$graders":
			predicate = `EXISTS(SELECT 1 FROM cao_sample_grader_observations c WHERE c.namespace=t.namespace AND c.grader_id=t.id)`
		case "$evals":
			predicate = `EXISTS(SELECT 1 FROM cao_sample_eval_observations c WHERE c.namespace=t.namespace AND c.eval_id=t.id)`
		default:
			if !table.partitioned {
				return fmt.Errorf("statistical bundle selection is not defined for %q", source)
			}
			predicate = `EXISTS(SELECT 1 FROM cao_sample_statistical_membership m WHERE m.namespace=t.namespace AND m.id=t.run_id)`
		}
		relation := query.SQLIdentifier(table.name) + " t WHERE t.namespace=$1 AND (" + predicate + ")"
		var rows, size int64
		// Measure expanded typed values; compressed TOAST pointers cannot hide an oversized bundle.
		bounded := "(SELECT t.* FROM " + relation + fmt.Sprintf(" LIMIT %d) t", query.MaxInputRows+1)
		if err := w.tx.QueryRow(ctx, "SELECT count(*),coalesce(sum("+entityBytesSQL(table, "t")+"),0)::bigint FROM "+bounded, w.store.namespace).Scan(&rows, &size); err != nil {
			return err
		}
		count, bytes = count+rows, bytes+size
		if rows > query.MaxInputRows || count > query.MaxWorkingRows || bytes > maxBytes {
			return dashboardquery.ErrStatisticalBudget
		}
		if _, err := w.tx.Exec(ctx, "INSERT INTO "+query.SQLIdentifier(statisticalTables[source].name)+" SELECT t.* FROM "+relation, w.store.namespace); err != nil {
			return err
		}
	}
	return nil
}

func (r *readTransaction) statisticalRecords(ctx context.Context, source string) ([]map[string]any, error) {
	table := statisticalTables[source]
	columns := make([]string, 0, 1+len(table.columns))
	columns = append(columns, "present_fields::text")
	for _, column := range table.columns {
		columns = append(columns, query.SQLIdentifier(column.name))
	}
	//nolint:gosec // Identifiers come only from the private generated TypeSpec registry.
	rows, err := r.tx.QueryContext(ctx, "SELECT "+strings.Join(columns, ",")+" FROM "+
		query.SQLIdentifier(table.name)+" WHERE namespace=$1 ORDER BY ordinal", r.store.namespace)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	result := []map[string]any{}
	for rows.Next() {
		var presence string
		values := make([]any, len(table.columns))
		dest := make([]any, len(values)+1)
		dest[0] = &presence
		for index := range values {
			dest[index+1] = &values[index]
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, err
		}
		record := map[string]any{}
		for index, column := range table.columns {
			if presence[index] != '1' {
				continue
			}
			value, err := decodeSQLValue(values[index], sqlKind(column))
			if err != nil {
				return nil, err
			}
			if column.sql == "BIGINT" && value != nil {
				integer, err := strconv.Atoi(fmt.Sprint(value))
				if err != nil {
					return nil, err
				}
				value = integer
			}
			record[column.field] = value
		}
		result = append(result, record)
	}
	return result, rows.Err()
}

func (r *readTransaction) statisticalSQLSource(ctx context.Context) (query.SQLRelation, model.Metadata, error) {
	frames, err := r.statisticalRecords(ctx, "$statisticalFrame")
	if err != nil {
		return query.SQLRelation{}, nil, err
	}
	quality := model.Metadata{"availability": "unavailable", "completeness": "unknown", "freshness": "unknown",
		"source-id": "$statisticalMembership", "statistical-frame": map[string]any{"weight-valid": false}}
	if len(frames) == 1 {
		frame := frames[0]
		delete(frame, "id")
		for _, field := range []string{"availability", "completeness", "freshness", "error"} {
			if value, exists := frame[field]; exists {
				quality[field] = value
				delete(frame, field)
			}
		}
		strata, err := r.statisticalRecords(ctx, "$statisticalStrata")
		if err != nil {
			return query.SQLRelation{}, nil, err
		}
		for _, stratum := range strata {
			delete(stratum, "id")
		}
		if _, exists := frame["frame-id"]; exists {
			frame["strata"] = strata
		}
		quality["as-of"], quality["statistical-frame"] = frame["watermark"], frame
		var revision int64
		if err := r.tx.QueryRowContext(ctx, `SELECT revision FROM cao_state WHERE namespace=$1`, r.store.namespace).Scan(&revision); err != nil {
			return query.SQLRelation{}, nil, err
		}
		if frame["source-revision"] != int(revision) {
			quality["freshness"] = "stale"
		}
	}
	return statisticalRelation(statisticalTables["$statisticalMembership"], r.store.namespace), quality, nil
}

func (r *readTransaction) frozenStatisticalSource(ctx context.Context, name string) (query.SQLRelation, model.Metadata, error) {
	source := strings.TrimPrefix(name, statisticalSnapshotPrefix)
	table, exists := statisticalTables[source]
	if !exists || !entityTables[source].canonical {
		return query.SQLRelation{}, nil, fmt.Errorf("statistical evidence source %q is not registered", source)
	}
	quality := model.Metadata{"source-id": name, "availability": "unavailable", "completeness": "unknown", "freshness": "unknown"}
	var availability, completeness, freshness string
	var asOf sql.NullTime
	err := r.tx.QueryRowContext(ctx, `SELECT availability,completeness,freshness,as_of FROM cao_sample_cao_quality
		WHERE namespace=$1 AND collection=$2`, r.store.namespace, source).Scan(&availability, &completeness, &freshness, &asOf)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return query.SQLRelation{}, nil, err
	}
	if err == nil {
		quality["availability"], quality["completeness"], quality["freshness"] = availability, completeness, freshness
		if asOf.Valid {
			quality["as-of"] = asOf.Time.UTC().Format(time.RFC3339Nano)
		}
	}
	relation := statisticalRelation(table, r.store.namespace)
	if quality["availability"] == "unavailable" {
		relation.SQL = "(SELECT * FROM " + relation.SQL + " WHERE FALSE) AS t"
	}
	return relation, quality, nil
}
