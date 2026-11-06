package postgresx

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/githubnext/gh-cao/server/internal/collectnative"
	"github.com/githubnext/gh-cao/server/internal/mining"
)

const (
	nativeOutcomeRows  = 20000
	nativeOutcomeBytes = 16 << 20
)

// RecordOutcome retains independently observed dispositions before or after
// their safe-output reference arrives. Both retention and attribution are fenced
// by the committed enrollment epoch, not by an operational ownership hint.
func (a Acquisition) RecordOutcome(ctx context.Context, scope collectnative.EnrollmentScope, next mining.Outcome) error {
	if err := next.Validate(); err != nil {
		return err
	}
	if next.Repository != scope.Repository || len(next.URL) > 2048 {
		return collectnative.ErrIdentity
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	tx, err := a.store.beginInvocation(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := a.fenceScope(ctx, tx, scope); err != nil {
		return err
	}
	now := time.Now().UTC()
	cutoff := now.AddDate(0, 0, -30)
	if _, err := tx.ExecContext(ctx, `DELETE FROM cao_acquisition_outcomes
		WHERE namespace=$1 AND repository=$2 AND (epoch<>$3 OR observed_at<$4)`,
		a.store.namespace, scope.Repository, scope.Epoch, cutoff); err != nil {
		return err
	}
	if next.ObservedAt.Before(cutoff) {
		return tx.Commit()
	}
	var previousContent []byte
	err = tx.QueryRowContext(ctx, `SELECT content FROM cao_acquisition_outcomes
		WHERE namespace=$1 AND repository=$2 AND url=$3`,
		a.store.namespace, scope.Repository, next.URL).Scan(&previousContent)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if len(previousContent) > 0 {
		previous, err := decodeNativeOutcome(previousContent, scope.Repository)
		if err != nil {
			return err
		}
		next, err = previous.Merge(next)
		if err != nil {
			return err
		}
	}
	content, err := json.Marshal(next)
	if err != nil {
		return err
	}
	if len(content) > 32768 {
		return collectnative.ErrCapacity
	}
	if bytes.Equal(content, previousContent) {
		return tx.Commit()
	}
	var rows, size int64
	if err := tx.QueryRowContext(ctx, `SELECT count(*),coalesce(sum(octet_length(content)),0)
		FROM cao_acquisition_outcomes WHERE namespace=$1 AND repository=$2`,
		a.store.namespace, scope.Repository).Scan(&rows, &size); err != nil {
		return err
	}
	if len(previousContent) == 0 {
		rows++
	}
	if rows > nativeOutcomeRows || size-int64(len(previousContent))+int64(len(content)) > nativeOutcomeBytes {
		return collectnative.ErrCapacity
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO cao_acquisition_outcomes(namespace,repository,url,epoch,observed_at,content)
		VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(namespace,repository,url)
		DO UPDATE SET epoch=excluded.epoch,observed_at=excluded.observed_at,content=excluded.content`,
		a.store.namespace, scope.Repository, next.URL, scope.Epoch, next.ObservedAt, content); err != nil {
		return err
	}
	changed, err := a.store.attributeNativeOutcome(ctx, tx, next, "")
	if err != nil {
		return err
	}
	if changed > 0 {
		if _, err := tx.ExecContext(ctx, `UPDATE cao_state SET revision=revision+1,evaluated_at=greatest(evaluated_at,$2)
			WHERE namespace=$1`, a.store.namespace, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (a Acquisition) fenceScope(ctx context.Context, tx *sql.Tx, scope collectnative.EnrollmentScope) error {
	if scope.Epoch <= 0 || scope.InstallationID <= 0 || !validEnrollmentRepository(scope.Repository) {
		return collectnative.ErrIdentity
	}
	var installation, epoch int64
	err := tx.QueryRowContext(ctx, `SELECT installation_id,epoch FROM cao_acquisition_scopes
		WHERE namespace=$1 AND repository=$2 FOR UPDATE`,
		a.store.namespace, scope.Repository).Scan(&installation, &epoch)
	if errors.Is(err, sql.ErrNoRows) || err == nil && (installation != scope.InstallationID || epoch != scope.Epoch) {
		return collectnative.ErrClaimLost
	}
	return err
}

func decodeNativeOutcome(content []byte, repository string) (mining.Outcome, error) {
	var outcome mining.Outcome
	if len(content) > 32768 || decodeJSON(content, &outcome) != nil ||
		outcome.Validate() != nil || outcome.Repository != repository {
		return mining.Outcome{}, errors.New("invalid durable native outcome")
	}
	return outcome, nil
}

func (s *Store) attributeNativeOutcome(ctx context.Context, tx *sql.Tx, outcome mining.Outcome, runID string) (int, error) {
	rows, err := tx.QueryContext(ctx, `SELECT i.id,i.run_id,r.run_at FROM issues i
		JOIN runs r ON r.namespace=i.namespace AND r.id=i.run_id AND r.run_at=i.run_at
		JOIN repositories repo ON repo.namespace=r.namespace AND repo.id=r.repository_id
		WHERE i.namespace=$1 AND (i.correlation_id=$2 OR i.safe_output_url=$2)
		AND coalesce(i.is_pull_request,false)=$3
		AND ((i.safe_output_type='create_issue' AND NOT $3) OR (i.safe_output_type='create_pull_request' AND $3))
		AND lower(repo.owner || '/' || repo.name)=$4 AND ($5='' OR r.id=$5)
		ORDER BY i.id LIMIT 20001`, s.namespace, outcome.URL, outcome.PullRequest, outcome.Repository, runID)
	if err != nil {
		return 0, err
	}
	defer func() { _ = rows.Close() }()
	type reference struct {
		issue, run string
		runAt      time.Time
	}
	var references []reference
	for rows.Next() {
		var ref reference
		if err := rows.Scan(&ref.issue, &ref.run, &ref.runAt); err != nil {
			_ = rows.Close()
			return 0, err
		}
		references = append(references, ref)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return 0, err
	}
	if len(references) > nativeOutcomeRows {
		return 0, collectnative.ErrCapacity
	}
	for _, ref := range references {
		audit := outcome.Audit(ref.issue, ref.run)
		if err := validateAuditProjection(audit); err != nil {
			return 0, err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM audits WHERE namespace=$1 AND id=$2 AND run_id=$3 AND run_at=$4
			AND source=$5 AND type='outcome.observed'`, s.namespace, audit["id"], ref.run, ref.runAt, mining.Source); err != nil {
			return 0, err
		}
		if err := insertInvocationEntity(ctx, tx, s.namespace, "$audits", audit, ref.runAt); err != nil {
			return 0, err
		}
	}
	if len(references) > 0 {
		if err := partialInvocationQuality(ctx, tx, s.namespace, "$audits"); err != nil {
			return 0, err
		}
	}
	return len(references), nil
}

// Replay only dispositions selected by exact safe-output URLs in this run.
// Run replacement must never erase independently retained observations.
func (a Acquisition) replayOutcomes(ctx context.Context, tx *sql.Tx, publication AcquisitionPublication, runID string, now time.Time) error {
	rows, err := tx.QueryContext(ctx, `SELECT o.content FROM cao_acquisition_outcomes o
		WHERE o.namespace=$1 AND o.repository=$2 AND o.epoch=$3 AND o.observed_at >= $5
		AND EXISTS (SELECT 1 FROM issues i WHERE i.namespace=o.namespace AND i.run_id=$4
			AND (i.safe_output_url=o.url OR i.correlation_id=o.url))
		ORDER BY o.url LIMIT 20001`, a.store.namespace, publication.Identity.Repository, publication.Epoch,
		runID, now.AddDate(0, 0, -30))
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	var outcomes []mining.Outcome
	for rows.Next() {
		var content []byte
		if err := rows.Scan(&content); err != nil {
			_ = rows.Close()
			return err
		}
		outcome, err := decodeNativeOutcome(content, publication.Identity.Repository)
		if err != nil {
			_ = rows.Close()
			return err
		}
		outcomes = append(outcomes, outcome)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return err
	}
	if len(outcomes) > nativeOutcomeRows {
		return collectnative.ErrCapacity
	}
	for _, outcome := range outcomes {
		if _, err := a.store.attributeNativeOutcome(ctx, tx, outcome, runID); err != nil {
			return err
		}
	}
	return nil
}
