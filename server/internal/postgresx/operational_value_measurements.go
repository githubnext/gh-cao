package postgresx

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/githubnext/gh-cao/server/internal/model"
)

var ErrOperationalValueRevisionStale = errors.New("canonical PostgreSQL revision changed during operational Value evaluation")

func (s *Store) ReplaceOperationalValues(
	ctx context.Context,
	expectedRevision int64,
	rows []model.Row,
) (State, error) {
	connection, err := pgx.ConnectConfig(ctx, s.config.Copy())
	if err != nil {
		return State{}, err
	}
	defer func() { _ = connection.Close(context.WithoutCancel(ctx)) }()
	tx, err := connection.Begin(ctx)
	if err != nil {
		return State{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock_shared(712083241, 17484)`); err != nil {
		return State{}, err
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended(current_schema() || ':' || $1, 0))`, s.namespace); err != nil {
		return State{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO cao_state (namespace, revision, data_revision, evaluated_at, schema_version)
		VALUES ($1,0,'','epoch',$2) ON CONFLICT (namespace) DO NOTHING`, s.namespace, model.SchemaVersion); err != nil {
		return State{}, err
	}
	var state State
	if err := tx.QueryRow(ctx, `SELECT revision,data_revision,evaluated_at FROM cao_state
		WHERE namespace=$1 FOR UPDATE`, s.namespace).Scan(&state.Revision, &state.DataRevision, &state.EvaluatedAt); err != nil {
		return State{}, err
	}
	if state.Revision != expectedRevision {
		return State{}, ErrOperationalValueRevisionStale
	}
	if _, err := tx.Exec(ctx, "SET CONSTRAINTS ALL DEFERRED"); err != nil {
		return State{}, err
	}
	if _, err := tx.Exec(ctx, "DELETE FROM operational_values WHERE namespace=$1", s.namespace); err != nil {
		return State{}, err
	}
	writer := &Writer{
		store: s, connection: connection, tx: tx,
		ordinals: map[string]int64{}, inventoryOrdinals: map[string]int64{},
		inventoryTables: map[string]bool{}, batches: map[string][][]any{},
		evaluatedAt: state.EvaluatedAt, ingestedAt: time.Now().UTC(), staged: map[string]bool{},
	}
	var evaluatedAt time.Time
	for _, row := range rows {
		if err := writer.Append(ctx, "$operationalValues", row); err != nil {
			return State{}, err
		}
		if value, ok := row["timestamp"].(string); ok {
			if instant, err := time.Parse(time.RFC3339Nano, value); err == nil && instant.After(evaluatedAt) {
				evaluatedAt = instant
			}
		}
	}
	if err := writer.Flush(ctx); err != nil {
		return State{}, err
	}
	now := time.Now().UTC()
	if evaluatedAt.IsZero() {
		evaluatedAt = now
	}
	availability := "available"
	if len(rows) == 0 {
		availability = "empty"
	}
	if err := writer.Quality(ctx, "$operationalValues", model.Metadata{
		"availability": availability, "completeness": "complete", "freshness": "current",
		"as-of": evaluatedAt.Format(time.RFC3339Nano), "retrieved-at": now.Format(time.RFC3339Nano),
	}); err != nil {
		return State{}, err
	}
	if _, err := tx.Exec(ctx, "SET CONSTRAINTS ALL IMMEDIATE"); err != nil {
		return State{}, err
	}
	if err := tx.QueryRow(ctx, `UPDATE cao_state SET revision=revision+1,
		evaluated_at=greatest(evaluated_at,$1),schema_version=$2
		WHERE namespace=$3 RETURNING revision,evaluated_at`,
		evaluatedAt, model.SchemaVersion, s.namespace).Scan(&state.Revision, &state.EvaluatedAt); err != nil {
		return State{}, err
	}
	state.Ready = true
	state.Counts = map[string]int{"$operationalValues": len(rows)}
	if err := tx.Commit(ctx); err != nil {
		return State{}, err
	}
	return state, nil
}
