package postgresx

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/githubnext/gh-cao/server/internal/mining"
)

// AppendEpisode retains only membership for the currently selected run attempt.
// Caller/root coordinates are evidence, not foreign keys or discovery authority.
func (w *Writer) AppendEpisode(ctx context.Context, membership mining.Membership) error {
	if err := membership.Validate(); err != nil {
		return err
	}
	runID := "github:run:" + membership.Repository + ":" + membership.RunID
	var attempt int
	err := w.tx.QueryRow(ctx, `SELECT coalesce(attempt,1) FROM runs WHERE namespace=$1 AND id=$2`,
		w.store.namespace, runID).Scan(&attempt)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && attempt != membership.Attempt) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, row := range membership.Audits() {
		if err := w.Append(ctx, "$audits", row); err != nil {
			return err
		}
	}
	return nil
}

// AppendOutcome joins an authoritative object disposition only to exact,
// previously collected safe-output references. Unattributed webhook objects
// never become agent outputs.
func (w *Writer) AppendOutcome(ctx context.Context, outcome mining.Outcome) error {
	if err := outcome.Validate(); err != nil {
		return err
	}
	if err := w.Flush(ctx); err != nil {
		return err
	}
	if !w.staged["$issues"] {
		return nil
	}
	rows, err := w.tx.Query(ctx, `SELECT i.id,i.run_id FROM issues_stage i
		JOIN runs r ON r.namespace=i.namespace AND r.id=i.run_id
		WHERE i.namespace=$1 AND (i.correlation_id=$2 OR i.safe_output_url=$2)
			AND coalesce(i.is_pull_request,false)=$3
			AND ((i.safe_output_type='create_issue' AND NOT $3)
				OR (i.safe_output_type='create_pull_request' AND $3))
		ORDER BY i.id LIMIT 20001`,
		w.store.namespace, outcome.URL, outcome.PullRequest)
	if err != nil {
		return err
	}
	type reference struct{ issue, run string }
	var references []reference
	for rows.Next() {
		var ref reference
		if err := rows.Scan(&ref.issue, &ref.run); err != nil {
			rows.Close()
			return err
		}
		references = append(references, ref)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if len(references) > 20000 {
		return errors.New("outcome attribution exceeds bounded reference limit")
	}
	for _, ref := range references {
		if err := w.Append(ctx, "$audits", outcome.Audit(ref.issue, ref.run)); err != nil {
			return err
		}
	}
	return nil
}

// MiningTargets pages distinct exact output URLs, not repositories or GitHub
// search results. The server separately verifies current installation enrollment.
func (s *Store) MiningTargets(ctx context.Context, after string, limit int) ([]string, error) {
	if limit < 1 || limit > 100 {
		return nil, errors.New("mining target page must contain 1 to 100 entries")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT coalesce(nullif(i.safe_output_url,''),i.correlation_id) AS url
		FROM issues i JOIN runs r ON r.namespace=i.namespace AND r.id=i.run_id AND r.run_at=i.run_at
		WHERE i.namespace=$1 AND coalesce(nullif(i.safe_output_url,''),i.correlation_id)>$2
			AND ((i.github_entity_type='issue' AND i.safe_output_type='create_issue')
				OR (i.github_entity_type='pull_request' AND i.safe_output_type='create_pull_request'))
		ORDER BY url LIMIT $3`, s.namespace, after, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var targets []string
	for rows.Next() {
		var target string
		if err := rows.Scan(&target); err != nil {
			return nil, err
		}
		targets = append(targets, target)
	}
	return targets, rows.Err()
}
