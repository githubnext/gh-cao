// Package postgresx owns the fresh, native dashboard projection.
package postgresx

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"

	"github.com/githubnext/gh-cao/server/internal/inputlimits"
	"github.com/githubnext/gh-cao/server/internal/logger"
	"github.com/githubnext/gh-cao/server/internal/model"
	"github.com/githubnext/gh-cao/server/internal/postgresconn"
	"github.com/githubnext/gh-cao/server/internal/query"
)

var storeLog = logger.New("cao:postgresx:store")

type State struct {
	Ready        bool
	Revision     int64
	DataRevision string
	EvaluatedAt  time.Time
	Counts       map[string]int
}

type Store struct {
	db                  *sql.DB
	config              *pgx.ConnConfig
	namespace           string
	linkedRetentionDays int
	dailyCounts         bool
	runCapacity         runCapacity
	stopMaintenance     context.CancelFunc
	maintenanceDone     sync.WaitGroup
}

// Open reports whether the store has a database handle. A zero-valued Store
// is useful only as a test seam, not as a source of revision state.
func (s *Store) Open() bool { return s != nil && s.db != nil }

type NativeReader interface {
	State(context.Context) (State, error)
	ExecuteSQLPlan(context.Context, []query.Definition, []string) (map[string]model.Source, model.Metrics, error)
	ExecuteSQLPlanWithOptions(context.Context, []query.Definition, []string, SQLExecutionOptions) (map[string]model.Source, model.Metrics, error)
	Diagnostics(context.Context) (model.Diagnostics, error)
}

type readTransaction struct {
	store *Store
	tx    *sql.Tx
}

func New(ctx context.Context, dsn string, namespaces ...string) (*Store, error) {
	if len(namespaces) > 1 {
		return nil, errors.New("postgres store accepts at most one namespace")
	}
	namespace := "default"
	if len(namespaces) == 1 {
		namespace = namespaces[0]
	}
	return NewWithNamespace(ctx, dsn, namespace)
}

func NewWithNamespace(ctx context.Context, dsn, namespace string) (*Store, error) {
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, errors.New("invalid postgres connection configuration")
	}
	return NewConfig(ctx, config, namespace)
}

func NewConfig(ctx context.Context, config *pgx.ConnConfig, namespaces ...string) (*Store, error) {
	return openConfig(ctx, config, true, namespaces...)
}

// OpenWithNamespace opens pre-provisioned canonical storage without DDL or a
// partition-maintenance goroutine. Production roles must use this path.
func OpenWithNamespace(ctx context.Context, dsn, namespace string) (*Store, error) {
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, errors.New("invalid postgres connection configuration")
	}
	return openConfig(ctx, config, false, namespace)
}

func openConfig(ctx context.Context, config *pgx.ConnConfig, provision bool, namespaces ...string) (*Store, error) {
	if config == nil {
		return nil, errors.New("postgres connection configuration is required")
	}
	if len(namespaces) > 1 {
		return nil, errors.New("postgres store accepts at most one namespace")
	}
	namespace := "default"
	if len(namespaces) == 1 {
		namespace = namespaces[0]
	}
	if namespace == "" {
		return nil, errors.New("postgres namespace is required")
	}
	if err := validateTransport(config); err != nil {
		return nil, err
	}
	config, err := instrumentConfig(config)
	if err != nil {
		return nil, fmt.Errorf("configure postgres telemetry: %w", err)
	}
	db := stdlib.OpenDB(*config)
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(30 * time.Minute)
	check := checkProvisionedContract
	if provision {
		check = initialize
	}
	if err := check(ctx, db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("postgres storage contract check failed: %w", err)
	}
	if provision {
		if err := initializeDailyCounts(ctx, db); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("provision daily outcome capability: %w", err)
		}
		if err := initializeStatistics(ctx, db); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("provision native statistics capability: %w", err)
		}
	}
	retention, err := configuredRetentionDays()
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	linkedRetention, err := configuredLinkedRetentionDays()
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	store := &Store{db: db, config: config.Copy(), namespace: namespace, linkedRetentionDays: linkedRetention}
	store.dailyCounts, err = dailyCapability(ctx, db)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	if store.dailyCounts {
		if err := store.backfillDailyOnce(ctx); err != nil {
			_ = db.Close()
			return nil, err
		}
	}
	if !provision {
		if err := store.checkProvisionedPartitions(ctx, time.Now(), retention, linkedRetention); err != nil {
			_ = db.Close()
			return nil, err
		}
		return store, nil
	}
	if err := store.RunPartitionMaintenance(ctx, time.Now(), retention, linkedRetention); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("initialize run partitions: %w", err)
	}
	if _, err := store.CurateAudits(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("maintain existing native audits: %w", err)
	}
	storeLog.Printf("store opened retention_days=%d linked_retention_days=%d", retention, linkedRetention)
	maintenanceCtx, stop := context.WithCancel(context.WithoutCancel(ctx))
	store.stopMaintenance = stop
	store.maintenanceDone.Add(1)
	go func() {
		defer store.maintenanceDone.Done()
		ticker := time.NewTicker(partitionMaintenanceInterval)
		defer ticker.Stop()
		runPartitionMaintenanceLoop(maintenanceCtx, ticker.C, func(ctx context.Context, now time.Time) error {
			return store.RunPartitionMaintenance(ctx, now, retention, linkedRetention)
		})
	}()
	return store, nil
}

// maintenanceTickOutcome classifies how one partition-maintenance tick
// ended, stable across log wording changes so it is useful to log without
// exposing the underlying Postgres error text.
type maintenanceTickOutcome string

const (
	maintenanceTickOutcomeSucceeded    maintenanceTickOutcome = "succeeded"
	maintenanceTickOutcomeFailed       maintenanceTickOutcome = "failed"
	maintenanceTickOutcomeShuttingDown maintenanceTickOutcome = "shutting-down"
)

// classifyMaintenanceTick decides whether a completed run's error (if any)
// is worth logging as a genuine failure, applying the same "a later tick
// retries, so a shutdown-time error is not a failure" rule
// runPartitionMaintenanceLoop previously applied inline. It is a pure
// function so each outcome is independently testable without a real ticker
// channel or Postgres connection.
func classifyMaintenanceTick(runErr, ctxErr error) maintenanceTickOutcome {
	switch {
	case runErr == nil:
		return maintenanceTickOutcomeSucceeded
	case ctxErr != nil:
		return maintenanceTickOutcomeShuttingDown
	default:
		return maintenanceTickOutcomeFailed
	}
}

func runPartitionMaintenanceLoop(ctx context.Context, ticks <-chan time.Time, run func(context.Context, time.Time) error) {
	for {
		select {
		case <-ctx.Done():
			return
		case now, ok := <-ticks:
			if !ok {
				return
			}
			outcome := classifyMaintenanceTick(run(ctx, now), ctx.Err())
			if outcome == maintenanceTickOutcomeFailed {
				// A later maintenance tick retries; ingestion never creates partitions.
				storeLog.Printf("partition maintenance tick outcome=%s", outcome)
			}
		}
	}
}

func validateTransport(config *pgx.ConnConfig) error {
	return postgresconn.ValidateTransport(config)
}

func (s *Store) Close() error {
	if s.stopMaintenance != nil {
		s.stopMaintenance()
		s.maintenanceDone.Wait()
	}
	return s.db.Close()
}

func (s *Store) DeleteNamespace(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, "SET CONSTRAINTS ALL DEFERRED"); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock_shared(712083241, 17484)`); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended(current_schema() || ':' || $1, 0))`, s.namespace); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM cao_state WHERE namespace = $1`, s.namespace); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM cao_github_resources WHERE namespace = $1`, s.namespace); err != nil {
		return err
	}
	return tx.Commit()
}

func tableNames() []string {
	names := make([]string, 0, len(entityTables))
	for source, table := range entityTables {
		if !table.runtime {
			names = append(names, source)
		}
	}
	sort.Strings(names)
	return names
}

func (s *Store) State(ctx context.Context) (state State, err error) {
	err = s.WithReadTransaction(ctx, func(ctx context.Context, reader NativeReader) error {
		state, err = reader.State(ctx)
		return err
	})
	return state, err
}

func (r *readTransaction) State(ctx context.Context) (State, error) {
	state := State{Counts: map[string]int{}}
	err := r.tx.QueryRowContext(ctx, `SELECT revision, data_revision, evaluated_at FROM cao_state WHERE namespace = $1`,
		r.store.namespace).Scan(&state.Revision, &state.DataRevision, &state.EvaluatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return state, nil
	}
	if err != nil {
		return State{}, fmt.Errorf("read postgres state: %w", err)
	}
	if state.Revision == 0 {
		return state, nil
	}
	rows, err := r.tx.QueryContext(ctx, "SELECT collection FROM cao_quality WHERE namespace=$1 ORDER BY collection", r.store.namespace)
	if err != nil {
		return State{}, fmt.Errorf("read native counts: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var collections []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return State{}, err
		}
		collections = append(collections, name)
	}
	if err := rows.Err(); err != nil {
		return State{}, err
	}
	if err := rows.Close(); err != nil {
		return State{}, err
	}
	// Separate plans avoid a CASE of partitioned subqueries whose bind-time
	// planning cost grows far faster than the number of retained rows.
	for _, name := range collections {
		table, exists := entityTables[name]
		if !exists {
			return State{}, fmt.Errorf("unknown native quality collection %q", name)
		}
		var count int
		if err := r.tx.QueryRowContext(ctx, "SELECT count(*) FROM "+query.SQLIdentifier(table.name)+" WHERE namespace=$1",
			r.store.namespace).Scan(&count); err != nil {
			return State{}, fmt.Errorf("read native count for %s: %w", name, err)
		}
		state.Counts[name] = count
	}
	if err := rows.Err(); err != nil {
		return State{}, err
	}
	state.Ready = true
	return state, nil
}

func (s *Store) WithReadTransaction(ctx context.Context, fn func(context.Context, NativeReader) error) error {
	ctx, cancel := context.WithTimeout(ctx, inputlimits.PostgresReadTransactionTimeout)
	defer cancel()
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return fmt.Errorf("begin postgres read: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	deadline, _ := ctx.Deadline()
	// pg_settings reports these timeouts in milliseconds. Zero means unlimited;
	// retain any stricter role/session ceiling, including while waiting on locks.
	if _, err := tx.ExecContext(ctx, `SELECT set_config(name,
		LEAST(NULLIF(setting::bigint,0),$1::bigint)::text,true)
		FROM pg_settings WHERE name IN ('statement_timeout','lock_timeout','idle_in_transaction_session_timeout')`,
		max(1, time.Until(deadline).Milliseconds())); err != nil {
		return fmt.Errorf("configure postgres read limits: %w", err)
	}
	// JIT compilation of wide dashboard plans can consume the entire read budget.
	if _, err := tx.ExecContext(ctx, "SET LOCAL jit = off"); err != nil {
		return err
	}
	// PostgreSQL 16 can estimate window-filtered CTEs as one row and choose
	// quadratic rescans. Compiled joins are equijoins; prefer hash/merge plans.
	if _, err := tx.ExecContext(ctx, "SET LOCAL enable_nestloop = off"); err != nil {
		return err
	}
	if err := fn(ctx, &readTransaction{store: s, tx: tx}); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ExecuteSQLPlan(ctx context.Context, definitions []query.Definition, requested []string) (sources map[string]model.Source, metrics model.Metrics, err error) {
	err = s.WithReadTransaction(ctx, func(ctx context.Context, reader NativeReader) error {
		sources, metrics, err = reader.ExecuteSQLPlan(ctx, definitions, requested)
		return err
	})
	return sources, metrics, err
}

func (s *Store) Diagnostics(ctx context.Context) (diagnostics model.Diagnostics, err error) {
	err = s.WithReadTransaction(ctx, func(ctx context.Context, reader NativeReader) error {
		diagnostics, err = reader.Diagnostics(ctx)
		return err
	})
	return diagnostics, err
}

func (r *readTransaction) Diagnostics(ctx context.Context) (model.Diagnostics, error) {
	state, err := r.State(ctx)
	if err != nil {
		return model.Diagnostics{}, err
	}
	result := model.Diagnostics{Counts: map[string]int{}, RelationshipErrors: []string{}, DuplicateRecordIDs: map[string][]string{}}
	if !state.Ready {
		return result, nil
	}
	if err := r.tx.QueryRowContext(ctx, "SELECT schema_version FROM cao_state WHERE namespace = $1", r.store.namespace).Scan(&result.SchemaVersion); err != nil {
		return result, err
	}
	for source, count := range state.Counts {
		name := strings.TrimPrefix(source, "$")
		result.Counts[name] = count
		result.DuplicateRecordIDs[name] = []string{}
	}
	return result, nil
}

func decodeJSON(content []byte, value any) error {
	decoder := json.NewDecoder(strings.NewReader(string(content)))
	decoder.UseNumber()
	return decoder.Decode(value)
}
