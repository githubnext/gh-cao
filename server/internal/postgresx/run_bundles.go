package postgresx

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/githubnext/gh-cao/server/internal/model"
	"github.com/githubnext/gh-cao/server/internal/query"
)

const MaxRunBundles = 16

// RunBundlePublication is a verified local source observation, not an authority
// envelope. Namespace, credentials and source directory remain session-owned.
type RunBundlePublication struct {
	Version    int                    `json:"version"`
	ObservedAt time.Time              `json:"observedAt"`
	Runs       []RunBundle            `json:"runs"`
	Parents    map[string][]model.Row `json:"parents,omitempty"`
	Ingestion  model.Row              `json:"ingestion,omitempty"`
}

type RunBundle struct {
	Run         model.Row                      `json:"run"`
	Collections map[string]RunBundleCollection `json:"collections,omitempty"`
}

type RunBundleCollection struct {
	Coverage string      `json:"coverage"`
	Rows     []model.Row `json:"rows"`
}

func validateRunBundles(input RunBundlePublication) error {
	if input.Version != 1 || input.ObservedAt.IsZero() ||
		!input.ObservedAt.Equal(input.ObservedAt.Truncate(time.Microsecond)) ||
		len(input.Runs) == 0 || len(input.Runs) > MaxRunBundles {
		return errors.New("run bundles require version 1, a microsecond source observation time and 1-16 Runs")
	}
	ids := map[string]bool{}
	total := 0
	for _, bundle := range input.Runs {
		id, _ := bundle.Run["id"].(string)
		attempt, err := (entityColumn{kind: "numeric", sql: "BIGINT"}).bind(bundle.Run["attempt"])
		if id == "" || ids[id] || err != nil || attempt == nil || attempt.(int64) < 1 {
			return errors.New("run bundle requires a unique Run identity and positive exact attempt")
		}
		ids[id] = true
		for _, field := range []string{"repositoryId", "workflowId"} {
			if value, ok := bundle.Run[field].(string); !ok || value == "" {
				return fmt.Errorf("run bundle requires %s", field)
			}
		}
		for name, collection := range bundle.Collections {
			table, exists := entityTables["$"+name]
			if !exists || !table.partitioned || name == "runs" {
				return fmt.Errorf("run bundle collection %q is not Run-owned", name)
			}
			if !slices.Contains([]string{"complete", "partial", "unavailable"}, collection.Coverage) ||
				collection.Coverage == "unavailable" && len(collection.Rows) != 0 {
				return errors.New("invalid run bundle collection coverage")
			}
			if len(collection.Rows) > query.MaxInputRows {
				return errors.New("run bundle exceeds the per-source row limit")
			}
			for _, row := range collection.Rows {
				rowID, _ := row["id"].(string)
				provenance, _ := entityFieldValue(row, "provenance.source")
				if row["runId"] != id || row["source"] == conclusionSource || provenance == conclusionSource ||
					strings.HasPrefix(rowID, "audit:"+conclusionSource+":") ||
					strings.HasPrefix(rowID, "grader-observation:"+conclusionSource+":") {
					return errors.New("run bundle row has another owner or impersonates invocation evidence")
				}
			}
			total += len(collection.Rows)
		}
	}
	for source, rows := range input.Parents {
		if !slices.Contains([]string{"campaigns", "repositories", "workflows", "experiments", "graders", "evals"}, source) {
			return fmt.Errorf("unsupported run bundle parent %q", source)
		}
		total += len(rows)
	}
	if total > query.MaxWorkingRows {
		return errors.New("run bundles exceed the aggregate row budget")
	}
	if input.Ingestion != nil && (input.Ingestion["id"] != "local-dashboard-ingestion" ||
		input.Ingestion["kind"] != "local-dashboard-ingestion") {
		return errors.New("run bundles may update only the local ingestion receipt")
	}
	return nil
}

// PublishRunBundles mutates only explicitly supplied subtrees and their parents.
// Full reconciliation remains a separate operation; small snapshots are never
// interpreted as deltas.
func (s *Store) PublishRunBundles(ctx context.Context, input RunBundlePublication) (State, error) {
	return s.publishRunBundles(ctx, input, "")
}

var ErrSnapshotIdentityChanged = errors.New("verified snapshot identity changed; retry ingestion")

// HasVerifiedSnapshot checks durable content identity, never local file metadata.
func (s *Store) HasVerifiedSnapshot(ctx context.Context, identity string) (bool, error) {
	var verified bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM cao_state
		WHERE namespace=$1 AND revision>0 AND verified_snapshot=$2)`, s.namespace, identity).Scan(&verified)
	return verified, err
}

// PublishVerifiedRunBundles rechecks the verified base under the writer lock.
func (s *Store) PublishVerifiedRunBundles(ctx context.Context, input RunBundlePublication, identity string) (State, error) {
	if identity == "" {
		return State{}, errors.New("verified snapshot identity is required")
	}
	return s.publishRunBundles(ctx, input, identity)
}

func (s *Store) publishRunBundles(ctx context.Context, input RunBundlePublication, identity string) (State, error) {
	if err := validateRunBundles(input); err != nil {
		return State{}, err
	}
	w, err := s.beginCanonicalWrite(ctx)
	if err != nil {
		return State{}, err
	}
	defer w.Abort(ctx)
	if identity != "" && (w.verifiedSnapshot == nil || *w.verifiedSnapshot != identity) {
		return State{}, ErrSnapshotIdentityChanged
	}
	w.delta = true
	if w.revision == 0 {
		return State{}, errors.New("run-bundle ingestion requires an initial authoritative snapshot")
	}
	if err := w.loadAccounting(ctx); err != nil {
		return State{}, err
	}
	if w.bundleEpoch == nil {
		return State{}, errors.New("run bundles require an authoritative snapshot publication")
	}
	if !input.ObservedAt.After(*w.bundleEpoch) {
		return w.unchangedBundleState(ctx)
	}
	if err := w.stageRunBundles(ctx, input); err != nil {
		return State{}, err
	}
	accepted, err := w.selectRunBundles(ctx)
	if err != nil {
		return State{}, err
	}
	if accepted == 0 {
		return w.unchangedBundleState(ctx)
	}
	changed, err := w.applyRunBundleRows(ctx)
	if err != nil {
		return State{}, err
	}
	return w.publishRunBundles(ctx, input, changed)
}

func (w *Writer) stageRunBundles(ctx context.Context, input RunBundlePublication) error {
	for _, statement := range []string{
		`CREATE TEMP TABLE run_bundle_input(id text PRIMARY KEY,observed_at timestamptz NOT NULL,digest text NOT NULL) ON COMMIT DROP`,
		`CREATE TEMP TABLE run_bundle_coverage(id text,collection text,coverage text,PRIMARY KEY(id,collection)) ON COMMIT DROP`,
	} {
		if _, err := w.tx.Exec(ctx, statement); err != nil {
			return err
		}
	}
	var identities, coverage [][]any
	for _, bundle := range input.Runs {
		content, err := json.Marshal(struct {
			Run     RunBundle              `json:"run"`
			Parents map[string][]model.Row `json:"parents"`
		}{bundle, input.Parents})
		if err != nil {
			return err
		}
		digest := sha256.Sum256(content)
		id := bundle.Run["id"]
		identities = append(identities, []any{id, input.ObservedAt, hex.EncodeToString(digest[:])})
		if err := w.Append(ctx, "$runs", bundle.Run); err != nil {
			return err
		}
		for name, collection := range bundle.Collections {
			coverage = append(coverage, []any{id, "$" + name, collection.Coverage})
			for _, row := range collection.Rows {
				if err := w.Append(ctx, "$"+name, row); err != nil {
					return err
				}
			}
		}
	}
	for name, rows := range input.Parents {
		for _, row := range rows {
			if err := w.Append(ctx, "$"+name, row); err != nil {
				return err
			}
		}
	}
	if input.Ingestion != nil {
		if err := w.Append(ctx, "$transactions", input.Ingestion); err != nil {
			return err
		}
	}
	if err := w.Flush(ctx); err != nil {
		return err
	}
	if err := w.stageBundleParentVersions(ctx, input); err != nil {
		return err
	}
	if _, err := w.tx.CopyFrom(ctx, pgx.Identifier{"run_bundle_input"},
		[]string{"id", "observed_at", "digest"}, pgx.CopyFromRows(identities)); err != nil {
		return err
	}
	if len(coverage) > 0 {
		_, err := w.tx.CopyFrom(ctx, pgx.Identifier{"run_bundle_coverage"},
			[]string{"id", "collection", "coverage"}, pgx.CopyFromRows(coverage))
		return err
	}
	return nil
}

func (w *Writer) selectRunBundles(ctx context.Context) (int64, error) {
	// The receipt is consulted only after matching the canonical publication
	// revision. Its Runs rows give one namespace-local partition locator.
	_, err := w.tx.Exec(ctx, `CREATE TEMP TABLE run_bundle_candidates ON COMMIT DROP AS
		SELECT n.id,n.attempt,i.observed_at,i.digest,r.run_at AS old_run_at,n.run_at,
			r.ordinal AS old_ordinal,r.id IS NULL OR n.attempt>coalesce(r.attempt,1) AS new_attempt,
			v.attempt AS previous_attempt,
			v.observed_at AS previous_observation,v.digest AS previous_digest,
			coalesce(n.attempt,1)>coalesce(r.attempt,1) OR (
				coalesce(n.attempt,1)=coalesce(r.attempt,1)
				AND (greatest(n.updated_at,n.completed_at) IS NULL
					OR greatest(r.updated_at,r.completed_at) IS NULL
					OR greatest(n.updated_at,n.completed_at)>=greatest(r.updated_at,r.completed_at))
				AND (v.attempt IS DISTINCT FROM n.attempt OR i.observed_at>=v.observed_at)) AS current
		FROM runs_stage n JOIN run_bundle_input i ON i.id=n.id
		LEFT JOIN cao_run_accounting k ON k.namespace=n.namespace AND k.collection='$runs' AND k.run_id=n.id
		LEFT JOIN runs r ON r.namespace=k.namespace AND r.id=k.run_id AND r.run_at=k.run_at
		LEFT JOIN cao_bundle_versions v ON v.namespace=n.namespace AND v.collection='$runs' AND v.id=n.id
		WHERE n.namespace=$1 AND NOT EXISTS(SELECT 1 FROM audits a
			WHERE a.namespace=n.namespace AND a.run_id=n.id AND a.source='conclusion-admission'
			AND a.source_sequence=0 AND CASE WHEN a.name ~ '^[1-9][0-9]*$'
				THEN a.name::numeric>n.attempt ELSE false END)`, w.store.namespace)
	if err != nil {
		return 0, err
	}
	var conflict bool
	if err := w.tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM run_bundle_candidates
		WHERE current AND attempt=previous_attempt AND observed_at=previous_observation AND digest<>previous_digest)`).Scan(&conflict); err != nil {
		return 0, err
	}
	if conflict {
		return 0, errors.New("conflicting Run-bundle content at the same accepted source version")
	}
	tag, err := w.tx.Exec(ctx, `CREATE TEMP TABLE affected_runs ON COMMIT DROP AS
		SELECT * FROM run_bundle_candidates WHERE current
		AND (previous_observation IS NULL OR attempt IS DISTINCT FROM previous_attempt
			OR observed_at<>previous_observation OR digest<>previous_digest)`)
	if err != nil {
		return 0, err
	}
	if _, err := w.tx.Exec(ctx, "CREATE UNIQUE INDEX ON affected_runs(id)"); err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

func (w *Writer) unchangedBundleState(ctx context.Context) (State, error) {
	state := State{Ready: true, Revision: w.revision, DataRevision: w.PreviousDataRevision,
		WorkflowSubjectRevision: w.workflowSubjectRevision, WorkflowSubjectDigest: w.workflowSubjectDigest,
		Counts: map[string]int{}}
	for source, value := range w.accounting {
		state.Counts[source] = int(value.records)
	}
	if err := w.tx.QueryRow(ctx, "SELECT evaluated_at FROM cao_state WHERE namespace=$1", w.store.namespace).Scan(&state.EvaluatedAt); err != nil {
		return State{}, err
	}
	state.EvaluatedAt = state.EvaluatedAt.UTC()
	if err := w.tx.Commit(ctx); err != nil {
		return State{}, err
	}
	w.tx = nil
	return state, nil
}

func inputIdentity(input RunBundlePublication) (string, error) {
	content, err := json.Marshal(input)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(content)
	return hex.EncodeToString(digest[:]), nil
}
