package postgresx

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
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

func lifecycleAuthorityPresence(issueAccepted, pullRequestAccepted bool) string {
	expression := "present_fields"
	for index, column := range entityTables["$outputLifecycles"].columns {
		bit := ""
		switch column.field {
		case "adoptionAt":
			bit = fmt.Sprintf(
				"CASE WHEN safe_output_type='create_issue' THEN %d ELSE %d END",
				map[bool]int{false: 0, true: 1}[issueAccepted],
				map[bool]int{false: 0, true: 1}[pullRequestAccepted],
			)
		case "reversalSignal", "reversedAt", "survivalSignal", "survivalObservedAt":
			bit = "0"
		case "adoptionState", "adoptionSignal", "adoptionObservedAt", "adoptionSource",
			"ruleVersion", "survivalState", "observedAt":
			bit = "1"
		}
		if bit != "" {
			expression = fmt.Sprintf("set_bit(%s,%d,%s)", expression, index, bit)
		}
	}
	return expression
}

func lifecycleFallback(outputType string) (mining.AdoptionEvaluation, string) {
	evaluation := mining.EvaluateSafeOutputDelivery(outputType, time.Unix(0, 0).UTC())
	source := "adoption-registry"
	if evaluation.AcceptanceKind == "fallback_delivery" {
		source = "delivery-rule"
	}
	return evaluation, source
}

// invalidateNativeOutcomeAuthority removes provider evidence acquired under a
// target repository's previous enrollment epoch without deleting producing
// lifecycle rows owned by other repositories.
func (s *Store) invalidateNativeOutcomeAuthority(
	ctx context.Context,
	tx *sql.Tx,
	repository string,
) (bool, error) {
	if !validEnrollmentRepository(repository) {
		return false, collectnative.ErrIdentity
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM cao_acquisition_outcomes
		WHERE namespace=$1 AND repository=$2`, s.namespace, repository); err != nil {
		return false, err
	}
	audits, err := tx.ExecContext(ctx, `DELETE FROM audits a USING output_lifecycles l
		WHERE a.namespace=$1 AND l.namespace=a.namespace AND l.target_repository=$2
		AND l.safe_output_type IN ('create_issue','create_pull_request')
		AND a.run_id=l.run_id AND a.payload_ref=l.target_url
		AND a.source=$3 AND a.type='outcome.observed'`,
		s.namespace, repository, mining.Source)
	if err != nil {
		return false, err
	}
	issue, issueSource := lifecycleFallback("create_issue")
	pullRequest, pullRequestSource := lifecycleFallback("create_pull_request")
	//nolint:gosec // The generated lifecycle contract supplies the only dynamic bit indexes.
	statement := `UPDATE output_lifecycles SET
		adoption_state=CASE WHEN safe_output_type='create_issue' THEN $3 ELSE $4 END,
		adoption_signal=CASE WHEN safe_output_type='create_issue' THEN $5 ELSE $6 END,
		adoption_at=CASE
			WHEN safe_output_type='create_issue' AND $9 THEN delivered_at
			WHEN safe_output_type='create_pull_request' AND $10 THEN delivered_at
			ELSE NULL END,
		adoption_observed_at=delivery_observed_at,
		adoption_source=CASE WHEN safe_output_type='create_issue' THEN $7 ELSE $8 END,
		rule_version=$11,
		reversal_signal=NULL,reversed_at=NULL,
		survival_state='unknown',survival_signal=NULL,survival_observed_at=NULL,
		observed_at=delivery_observed_at,
		present_fields=` + lifecycleAuthorityPresence(issue.AcceptedAt != nil, pullRequest.AcceptedAt != nil) + `
		WHERE namespace=$1 AND target_repository=$2
		AND safe_output_type IN ('create_issue','create_pull_request')`
	lifecycles, err := tx.ExecContext(ctx, statement,
		s.namespace, repository,
		issue.Status, pullRequest.Status,
		issue.Signal, pullRequest.Signal,
		issueSource, pullRequestSource,
		issue.AcceptedAt != nil, pullRequest.AcceptedAt != nil,
		mining.AdoptionRegistryVersion,
	)
	if err != nil {
		return false, err
	}
	auditRows, err := audits.RowsAffected()
	if err != nil {
		return false, err
	}
	lifecycleRows, err := lifecycles.RowsAffected()
	return auditRows > 0 || lifecycleRows > 0, err
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
	rows, err := tx.QueryContext(ctx, `SELECT l.id,l.run_id,r.run_at,l.safe_output_type,coalesce(i.id,'')
		FROM output_lifecycles l
		JOIN runs r ON r.namespace=l.namespace AND r.id=l.run_id
		LEFT JOIN issues i ON i.namespace=l.namespace AND i.run_id=l.run_id
			AND (i.safe_output_url=l.target_url OR i.correlation_id=l.target_url)
		WHERE l.namespace=$1 AND l.target_url=$2 AND l.target_repository=$3
		AND ((l.safe_output_type='create_issue' AND NOT $4)
			OR (l.safe_output_type='create_pull_request' AND $4))
		AND ($5='' OR l.run_id=$5)
		ORDER BY l.id LIMIT 20001`, s.namespace, outcome.URL, outcome.Repository, outcome.PullRequest, runID)
	if err != nil {
		return 0, err
	}
	defer func() { _ = rows.Close() }()
	type reference struct {
		lifecycle, run, outputType, issue string
		runAt                             time.Time
	}
	var references []reference
	for rows.Next() {
		var ref reference
		if err := rows.Scan(&ref.lifecycle, &ref.run, &ref.runAt, &ref.outputType, &ref.issue); err != nil {
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
	changed := 0
	for _, ref := range references {
		previous, err := s.readOutputLifecycle(ctx, tx, ref.lifecycle)
		if err != nil {
			return changed, err
		}
		next, err := mining.ApplyOutcomeToLifecycle(previous, outcome)
		if err != nil {
			return changed, err
		}
		updated, err := s.replaceOutputLifecycle(ctx, tx, previous, next, outcome.ObservedAt)
		if err != nil {
			return changed, err
		}
		if updated {
			changed++
		}
		if ref.issue == "" ||
			ref.runAt.Before(dayStart(outcome.ObservedAt.AddDate(0, 0, -s.linkedRetentionDays))) {
			continue
		}
		audit := outcome.AuditForSafeOutput(ref.issue, ref.run, ref.outputType)
		if err := validateAuditProjection(audit); err != nil {
			return changed, err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM audits WHERE namespace=$1 AND id=$2 AND run_id=$3 AND run_at=$4
			AND source=$5 AND type='outcome.observed'`, s.namespace, audit["id"], ref.run, ref.runAt, mining.Source); err != nil {
			return changed, err
		}
		if err := insertInvocationEntity(ctx, tx, s.namespace, "$audits", audit, ref.runAt); err != nil {
			return changed, err
		}
	}
	if len(references) > 0 {
		if err := partialInvocationQuality(ctx, tx, s.namespace, "$audits"); err != nil {
			return changed, err
		}
	}
	return changed, nil
}

// Replay only dispositions selected by exact safe-output URLs in this run.
// Run replacement must never erase independently retained observations.
func (a Acquisition) replayOutcomes(ctx context.Context, tx *sql.Tx, _ AcquisitionPublication, runID string, now time.Time) error {
	rows, err := tx.QueryContext(ctx, `SELECT o.repository,o.content FROM cao_acquisition_outcomes o
		JOIN cao_acquisition_scopes s ON s.namespace=o.namespace AND s.repository=o.repository
			AND s.epoch=o.epoch AND s.installation_id>0
		WHERE o.namespace=$1 AND o.observed_at >= $3
		AND EXISTS (SELECT 1 FROM output_lifecycles l WHERE l.namespace=o.namespace
			AND l.run_id=$2 AND l.target_url=o.url AND l.target_repository=o.repository)
		ORDER BY o.repository,o.url LIMIT 20001`, a.store.namespace, runID, now.AddDate(0, 0, -30))
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	var outcomes []mining.Outcome
	for rows.Next() {
		var repository string
		var content []byte
		if err := rows.Scan(&repository, &content); err != nil {
			_ = rows.Close()
			return err
		}
		outcome, err := decodeNativeOutcome(content, repository)
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

func (a Acquisition) LifecycleRefreshDue(
	ctx context.Context,
	scope collectnative.EnrollmentScope,
	before time.Time,
) (bool, error) {
	current, err := a.CurrentScope(ctx, scope.Repository)
	if err != nil {
		return false, err
	}
	if current.InstallationID != scope.InstallationID || current.Epoch != scope.Epoch {
		return false, collectnative.ErrClaimLost
	}
	var due bool
	err = a.store.db.QueryRowContext(ctx, `SELECT EXISTS(
		SELECT 1 FROM output_lifecycles l
		WHERE l.namespace=$1 AND l.target_repository=$2
			AND l.safe_output_type IN ('create_issue','create_pull_request')
			AND l.target_url IS NOT NULL AND l.adoption_observed_at<$3
	)`, a.store.namespace, scope.Repository, before).Scan(&due)
	return due, err
}

func (a Acquisition) LifecycleRefreshTargets(
	ctx context.Context,
	scope collectnative.EnrollmentScope,
	before time.Time,
	limit int,
) ([]string, bool, error) {
	if limit < 1 || limit > 100 {
		return nil, false, collectnative.ErrCapacity
	}
	current, err := a.CurrentScope(ctx, scope.Repository)
	if err != nil {
		return nil, false, err
	}
	if current.InstallationID != scope.InstallationID || current.Epoch != scope.Epoch {
		return nil, false, collectnative.ErrClaimLost
	}
	rows, err := a.store.db.QueryContext(ctx, `SELECT l.target_url,min(l.adoption_observed_at) AS oldest
		FROM output_lifecycles l
		WHERE l.namespace=$1 AND l.target_repository=$2
			AND l.safe_output_type IN ('create_issue','create_pull_request')
			AND l.target_url IS NOT NULL AND ($3 OR l.adoption_observed_at<$4)
		GROUP BY l.target_url ORDER BY oldest,l.target_url LIMIT $5`,
		a.store.namespace, scope.Repository, before.IsZero(), before, limit+1)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = rows.Close() }()
	targets := make([]string, 0, limit+1)
	for rows.Next() {
		var target string
		var ignored time.Time
		if err := rows.Scan(&target, &ignored); err != nil {
			return nil, false, err
		}
		targets = append(targets, target)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	more := len(targets) > limit
	if more {
		targets = targets[:limit]
	}
	return targets, more, nil
}
