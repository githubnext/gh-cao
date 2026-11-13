package postgresx

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/githubnext/gh-cao/internal/dashboardquery"

	"github.com/githubnext/gh-cao/server/internal/model"
	"github.com/githubnext/gh-cao/server/internal/query"
)

const defaultStatisticalRefreshInterval = 5 * time.Minute

func configuredStatisticalRefreshInterval() (time.Duration, error) {
	value, present := os.LookupEnv("CAO_STATISTICAL_REFRESH_INTERVAL")
	if !present {
		return defaultStatisticalRefreshInterval, nil
	}
	interval, err := time.ParseDuration(value)
	if err != nil || interval < time.Millisecond {
		return 0, errors.New("CAO_STATISTICAL_REFRESH_INTERVAL must be a positive duration of at least 1ms")
	}
	return interval, nil
}

type statisticalSnapshot struct {
	revision     int64
	scope        string
	builtAt      time.Time
	frame        map[string]any
	sources      map[string]model.Source
	membership   model.Source
	refreshError bool
}

// One bounded cache belongs to one Store/namespace. Its throttle is deliberately
// process-local; ingestion revisions and result invalidation do not reset it.
type statisticalCache struct {
	mu          sync.Mutex
	interval    time.Duration
	now         func() time.Time
	lastAttempt time.Time
	inFlight    chan struct{}
	snapshot    *statisticalSnapshot
	scope       string
	failed      bool
}

func (c *statisticalCache) get(ctx context.Context, revision int64, scope string, build func() (*statisticalSnapshot, error)) (*statisticalSnapshot, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		c.mu.Lock()
		if c.scope != scope {
			c.scope, c.snapshot = scope, nil
		}
		if c.inFlight != nil {
			done := c.inFlight
			c.mu.Unlock()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-done:
				continue
			}
		}
		now := time.Now()
		if c.now != nil {
			now = c.now()
		}
		interval := c.interval
		if interval == 0 {
			interval = defaultStatisticalRefreshInterval
		}
		value := c.snapshot
		if value != nil && value.revision == revision && !c.failed || !c.lastAttempt.IsZero() && now.Sub(c.lastAttempt) < interval {
			if value != nil && value.revision > revision {
				value = nil // Never attach a newer sample to an older read snapshot.
			}
			if value != nil && c.failed {
				copy := *value
				copy.refreshError = true
				value = &copy
			}
			c.mu.Unlock()
			return value, nil
		}
		c.lastAttempt, c.inFlight = now, make(chan struct{})
		done := c.inFlight
		c.mu.Unlock()

		value, err := build()
		if err == nil {
			err = ctx.Err()
		}
		c.mu.Lock()
		c.failed = err != nil
		if err == nil && c.scope == scope {
			value.scope, value.builtAt = scope, now
			c.snapshot = value
		}
		if c.scope != scope {
			value = nil
		}
		c.inFlight = nil
		close(done)
		c.mu.Unlock()
		return value, err
	}
}

func (r *readTransaction) lazyStatisticalSnapshot(ctx context.Context) (*statisticalSnapshot, error) {
	if r.statistical != nil || r.statisticalError != nil {
		return r.statistical, r.statisticalError
	}
	var revision int64
	var dataRevision, scope string
	if err := r.tx.QueryRowContext(ctx, `SELECT revision,data_revision FROM cao_state WHERE namespace=$1`,
		r.store.namespace).Scan(&revision, &dataRevision); err != nil {
		return nil, err
	}
	// Enrollment epochs survive withdrawal/reenrollment. Checking the snapshot's
	// authority on every read also fences caches held by other server processes.
	if err := r.tx.QueryRowContext(ctx, `SELECT encode(sha256(convert_to(coalesce(string_agg(
		repository || ':' || epoch::text || ':' || installation_id::text, ',' ORDER BY repository),''),'UTF8')),'hex')
		FROM cao_acquisition_scopes WHERE namespace=$1`, r.store.namespace).Scan(&scope); err != nil {
		return nil, err
	}
	r.statistical, r.statisticalError = r.store.statistical.get(ctx, revision, scope, func() (*statisticalSnapshot, error) {
		return r.buildStatisticalSnapshot(ctx, revision, dataRevision)
	})
	return r.statistical, r.statisticalError
}

func (r *readTransaction) buildStatisticalSnapshot(ctx context.Context, revision int64, dataRevision string) (*statisticalSnapshot, error) {
	quality, err := r.rootMetadata(ctx, "$runs")
	if err != nil {
		return nil, err
	}
	var recorded sql.NullTime
	if err := r.tx.QueryRowContext(ctx, `SELECT as_of FROM cao_quality WHERE namespace=$1 AND collection='$runs'`,
		r.store.namespace).Scan(&recorded); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	candidates, watermark, policy, inputErr := r.lazyStatisticalCandidates(ctx, recorded)
	if inputErr != nil && !errors.Is(inputErr, dashboardquery.ErrStatisticalBudget) {
		return nil, inputErr
	}
	selected, frame, err := selectStatisticalSnapshot(ctx, quality, watermark, policy, candidates, inputErr)
	if err != nil {
		return nil, err
	}
	frame["source-data-revision"], frame["source-revision"], frame["storage-mode"] = dataRevision, revision, "lazy-summaries"
	quality["statistical-frame"], quality["sample-frame"] = frame, frame
	snapshot := &statisticalSnapshot{
		revision: revision, frame: frame, sources: map[string]model.Source{},
		membership: model.Source{Rows: []model.Row{}, Metadata: quality},
	}
	for _, row := range dashboardquery.StatisticalMembership(selected) {
		snapshot.membership.Rows = append(snapshot.membership.Rows, model.Row(row))
	}
	ids := make([]string, len(selected))
	for index, row := range selected {
		ids[index] = row["id"].(string)
	}
	var bytes int64
	var count int
	bytes += query.EstimateRowsBytes(snapshot.membership.Rows) + 1<<20
	for _, source := range []string{"$runs", "$workflows", "$repositories", "$campaigns"} {
		if source != "$runs" {
			parent, field := "$runs", "workflowId"
			switch source {
			case "$repositories":
				parent, field = "$workflows", "repositoryId"
			case "$campaigns":
				parent, field = "$workflows", "campaignId"
			}
			idSet := map[string]bool{}
			for _, row := range snapshot.sources[parent].Rows {
				if id, ok := row[field].(string); ok {
					idSet[id] = true
				}
			}
			ids = slices.Collect(maps.Keys(idSet))
			slices.Sort(ids)
		}
		records, err := r.statisticalEntityRecords(ctx, source, ids, &bytes, &count)
		if errors.Is(err, dashboardquery.ErrStatisticalBudget) {
			snapshot.membership.Rows = []model.Row{}
			snapshot.sources = map[string]model.Source{}
			snapshot.frame["weight-valid"], snapshot.frame["full-population-estimable"] = false, false
			quality["availability"], quality["completeness"] = "unavailable", "unknown"
			quality["error"] = "statistical summaries exceed storage budget"
			return snapshot, nil
		}
		if err != nil {
			return nil, err
		}
		metadata, err := r.rootMetadata(ctx, source)
		if err != nil {
			return nil, err
		}
		snapshot.sources[source] = model.Source{Rows: records, Metadata: metadata}
	}
	statisticalLog.Printf("event=statistical-refresh status=completed selected_count=%d retained_bytes=%d", len(selected), bytes)
	return snapshot, nil
}

func (r *readTransaction) lazyStatisticalCandidates(ctx context.Context, recorded sql.NullTime) ([]map[string]any, string, string, error) {
	watermark, policy := recorded.Time, "recorded-run-as-of"
	if !recorded.Valid {
		var latest *time.Time
		if err := r.tx.QueryRowContext(ctx, `SELECT max(greatest(started_at,created_at,completed_at,updated_at))
			FROM runs WHERE namespace=$1`, r.store.namespace).Scan(&latest); err != nil {
			return nil, "", "", err
		}
		if latest == nil {
			return nil, "", "canonical-run-max", nil
		}
		watermark, policy = *latest, "canonical-run-max"
	}
	clock := watermark.UTC().Format("2006-01-02T15:04:05.000Z")
	rows, err := r.tx.QueryContext(ctx, `SELECT id,owner,repository,github_run_id,started_at,created_at,completed_at,updated_at,status,conclusion
		FROM runs WHERE namespace=$1 AND status='completed' AND (
		(coalesce(started_at,created_at)>=$2 AND coalesce(started_at,created_at)<$3)
		OR (started_at IS NULL AND created_at IS NULL))
		ORDER BY id COLLATE "C" LIMIT $4`, r.store.namespace, watermark.Add(-7*24*time.Hour), watermark, query.MaxInputRows+1)
	if err != nil {
		return nil, "", "", err
	}
	defer func() { _ = rows.Close() }()
	fields := []string{"id", "owner", "repository", "githubRunId", "startedAt", "createdAt", "completedAt", "updatedAt", "status", "conclusion"}
	var candidates []map[string]any
	var bytes int64
	for rows.Next() {
		values := make([]any, len(fields))
		targets := make([]any, len(fields))
		for index := range values {
			targets[index] = &values[index]
		}
		if err := rows.Scan(targets...); err != nil {
			return nil, "", "", err
		}
		row := map[string]any{}
		for index, value := range values {
			if instant, ok := value.(time.Time); ok {
				value = instant.UTC().Format("2006-01-02T15:04:05.000Z")
			}
			row[fields[index]] = value
		}
		bytes += query.EstimateRowsBytes([]model.Row{model.Row(row)})
		if len(candidates) >= query.MaxInputRows || bytes > query.MaxWorkingBytes {
			return nil, clock, policy, dashboardquery.ErrStatisticalBudget
		}
		candidates = append(candidates, row)
	}
	return candidates, clock, policy, rows.Err()
}

func (r *readTransaction) statisticalEntityRecords(ctx context.Context, source string, ids []string, bytes *int64, count *int) ([]model.Row, error) {
	result := []model.Row{}
	if len(ids) == 0 {
		return result, nil
	}
	table := entityTables[source]
	relation := relationColumns(table, func(index int) string {
		return fmt.Sprintf("get_bit(t.present_fields,%d)=1", index)
	})
	columns := []string{"present_fields::text"}
	for _, column := range table.columns {
		if column.kind == "object" {
			columns = append(columns, relation[column.field].Expression)
		} else {
			columns = append(columns, "t."+query.SQLIdentifier(column.name))
		}
	}
	//nolint:gosec // Identifiers and expressions come only from the generated TypeSpec registry.
	rows, err := r.tx.QueryContext(ctx, "SELECT "+strings.Join(columns, ",")+" FROM "+query.SQLIdentifier(table.name)+
		" t WHERE namespace=$1 AND id=ANY($2::text[]) ORDER BY ordinal LIMIT $3", r.store.namespace, ids, query.MaxInputRows+1)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var presence string
		values := make([]any, len(table.columns))
		targets := make([]any, len(values)+1)
		targets[0] = &presence
		for index := range values {
			targets[index+1] = &values[index]
		}
		if err := rows.Scan(targets...); err != nil {
			return nil, err
		}
		record := model.Row{}
		for index, column := range table.columns {
			if presence[index] == '1' && !strings.Contains(column.field, ".") {
				value, err := decodeSQLValue(values[index], sqlKind(column))
				if err != nil {
					return nil, err
				}
				record[column.field] = value
			}
		}
		*bytes += query.EstimateRowsBytes([]model.Row{record})
		*count += 1
		if len(result) >= query.MaxInputRows || *count > query.MaxWorkingRows || *bytes > query.MaxWorkingBytes {
			return nil, dashboardquery.ErrStatisticalBudget
		}
		result = append(result, record)
	}
	return result, rows.Err()
}

func (r *readTransaction) lazyStatisticalMetadata(ctx context.Context, snapshot *statisticalSnapshot, quality model.Metadata) (model.Metadata, error) {
	result := maps.Clone(quality)
	if result == nil {
		result = model.Metadata{}
	}
	interval := r.store.statistical.interval
	if interval == 0 {
		interval = defaultStatisticalRefreshInterval
	}
	result["refresh-interval-ms"] = interval.Milliseconds()
	if snapshot == nil {
		result["availability"], result["completeness"], result["freshness"] = "unavailable", "unknown", "unknown"
		result["error"] = "statistical refresh is throttled; no authorized sample is available"
		result["statistical-frame"] = map[string]any{"weight-valid": false, "storage-mode": "lazy-summaries"}
		r.store.statistical.mu.Lock()
		if r.store.statistical.failed {
			result["error"] = "statistical refresh failed; retry is throttled"
		}
		r.store.statistical.mu.Unlock()
		return result, nil
	}
	var revision int64
	if err := r.tx.QueryRowContext(ctx, `SELECT revision FROM cao_state WHERE namespace=$1`,
		r.store.namespace).Scan(&revision); err != nil {
		return nil, err
	}
	if snapshot.revision != revision || snapshot.refreshError {
		result["freshness"] = "stale"
	}
	result["sample-built-at"] = snapshot.builtAt.UTC().Format(time.RFC3339Nano)
	result["statistical-frame"], result["sample-frame"] = snapshot.frame, snapshot.frame
	if snapshot.refreshError {
		result["error"] = "statistical refresh failed; serving the previous authorized sample"
	}
	return result, nil
}

func (r *readTransaction) lazyStatisticalSQLSource(ctx context.Context, source string) (query.SQLRelation, model.Metadata, error) {
	snapshot, err := r.lazyStatisticalSnapshot(ctx)
	if err != nil {
		return query.SQLRelation{}, nil, fmt.Errorf("refresh statistical sample: %w", err)
	}
	value := model.Source{Rows: []model.Row{}}
	table := statisticalTables["$statisticalMembership"]
	if source == "$statisticalMembership" {
		if snapshot != nil {
			value = snapshot.membership
		}
	} else {
		source = strings.TrimPrefix(source, statisticalSnapshotPrefix)
		var exists bool
		table, exists = entityTables[source]
		if !exists || !table.canonical {
			return query.SQLRelation{}, nil, fmt.Errorf("statistical evidence source %q is not registered", source)
		}
		if snapshot != nil {
			if cached, exists := snapshot.sources[source]; exists {
				value = cached
			} else {
				var revision int64
				if err := r.tx.QueryRowContext(ctx, `SELECT revision FROM cao_state WHERE namespace=$1`,
					r.store.namespace).Scan(&revision); err != nil {
					return query.SQLRelation{}, nil, err
				}
				quality, err := r.rootMetadata(ctx, source)
				if err != nil {
					return query.SQLRelation{}, nil, err
				}
				if revision == snapshot.revision && snapshot.membership.Metadata["availability"] != "unavailable" {
					relation, err := r.entitySQLSource(source)
					metadata, metadataErr := r.lazyStatisticalMetadata(ctx, snapshot, quality)
					if err != nil {
						return query.SQLRelation{}, nil, err
					}
					return relation, metadata, metadataErr
				}
				value.Metadata = quality
				value.Metadata["availability"] = "unavailable"
				value.Metadata["error"] = "statistical detail is not retained at the sampled revision"
			}
		}
	}
	quality, err := r.lazyStatisticalMetadata(ctx, snapshot, value.Metadata)
	if err != nil {
		return query.SQLRelation{}, nil, err
	}
	if quality["availability"] == "unavailable" {
		value.Rows = []model.Row{}
	}
	relation, err := typedRowsSQLSource(source, table, value)
	return relation, quality, err
}
