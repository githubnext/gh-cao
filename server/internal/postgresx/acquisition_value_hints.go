package postgresx

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/githubnext/gh-cao/server/internal/collectnative"
)

// InvalidateValue is a scope-fenced invalidation, not a Value acquisition or an
// accepted outcome. It shares the ownership transaction, not a separate KV
// read/modify/write protected by a process or expiring repository mutex.
func (a Acquisition) InvalidateValue(ctx context.Context, scope collectnative.EnrollmentScope) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	tx, err := a.store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := a.fenceScope(ctx, tx, scope); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO cao_acquisition_value_hints(namespace,repository,epoch,dirty,processed)
		VALUES($1,$2,$3,1,0) ON CONFLICT(namespace,repository) DO UPDATE SET
		epoch=excluded.epoch,
		dirty=CASE WHEN cao_acquisition_value_hints.epoch=excluded.epoch THEN cao_acquisition_value_hints.dirty+1 ELSE 1 END,
		processed=CASE WHEN cao_acquisition_value_hints.epoch=excluded.epoch THEN cao_acquisition_value_hints.processed ELSE 0 END,
		attempt_day=CASE WHEN cao_acquisition_value_hints.epoch=excluded.epoch THEN cao_acquisition_value_hints.attempt_day ELSE NULL END,
		retry_at=CASE WHEN cao_acquisition_value_hints.epoch=excluded.epoch THEN cao_acquisition_value_hints.retry_at ELSE NULL END
		WHERE cao_acquisition_value_hints.epoch<>excluded.epoch OR cao_acquisition_value_hints.dirty<9223372036854775807`,
		a.store.namespace, scope.Repository, scope.Epoch)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return collectnative.ErrCapacity
	}
	return tx.Commit()
}

func (a Acquisition) ValueHint(ctx context.Context, scope collectnative.EnrollmentScope) (collectnative.ValueHint, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	tx, err := a.store.db.BeginTx(ctx, nil)
	if err != nil {
		return collectnative.ValueHint{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := a.fenceScope(ctx, tx, scope); err != nil {
		return collectnative.ValueHint{}, err
	}
	var hint collectnative.ValueHint
	var day sql.NullString
	var retry sql.NullTime
	err = tx.QueryRowContext(ctx, `SELECT dirty,processed,attempt_day,retry_at FROM cao_acquisition_value_hints
		WHERE namespace=$1 AND repository=$2 AND epoch=$3`,
		a.store.namespace, scope.Repository, scope.Epoch).Scan(&hint.Dirty, &hint.Processed, &day, &retry)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	if err != nil {
		return collectnative.ValueHint{}, err
	}
	hint.AttemptDay, hint.RetryAt = day.String, retry.Time
	return hint, tx.Commit()
}

// CompleteValueHint acknowledges only the generation observed before an
// independently authorized acquisition. Concurrent invalidations remain dirty;
// an obsolete consent epoch cannot acknowledge a new owner's generation.
func (a Acquisition) CompleteValueHint(ctx context.Context, scope collectnative.EnrollmentScope,
	generation int64, completed time.Time, retry time.Time,
) error {
	if generation <= 0 || completed.IsZero() || !retry.IsZero() && retry.Before(completed) {
		return collectnative.ErrIdentity
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	tx, err := a.store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := a.fenceScope(ctx, tx, scope); err != nil {
		return err
	}
	var retryAt any
	if !retry.IsZero() {
		retryAt = retry.UTC()
	}
	result, err := tx.ExecContext(ctx, `UPDATE cao_acquisition_value_hints SET
		processed=CASE WHEN $6::timestamptz IS NULL THEN greatest(processed,$4) ELSE processed END,
		attempt_day=CASE WHEN $6::timestamptz IS NULL THEN $5 ELSE attempt_day END,retry_at=$6
		WHERE namespace=$1 AND repository=$2 AND epoch=$3 AND dirty>=$4 AND processed<=$4
		AND (attempt_day IS NULL OR attempt_day<=$5)`,
		a.store.namespace, scope.Repository, scope.Epoch, generation, completed.UTC().Format("2006-01-02"), retryAt)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return collectnative.ErrClaimLost
	}
	return tx.Commit()
}
