package postgresx

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/githubnext/gh-cao/server/internal/logger"
	"github.com/githubnext/gh-cao/server/internal/model"
)

var postgresxLog = logger.New("cao:postgresx:repository_lifecycle")

// repositoryLifecycleRejectionStage identifies which precondition of
// UpdateRepositoryLifecycle failed, so a rejected update is diagnosable
// without logging the repository coordinate or GitHub identifier.
type repositoryLifecycleRejectionStage string

const (
	repositoryLifecycleRejectionStageID         repositoryLifecycleRejectionStage = "github-id"
	repositoryLifecycleRejectionStageLifecycle  repositoryLifecycleRejectionStage = "lifecycle"
	repositoryLifecycleRejectionStageTimestamp  repositoryLifecycleRejectionStage = "timestamp"
	repositoryLifecycleRejectionStageCoordinate repositoryLifecycleRejectionStage = "coordinate"
)

// validRepositoryLifecycle reports whether lifecycle is one of the three
// values the storage schema accepts.
func validRepositoryLifecycle(lifecycle string) bool {
	return lifecycle == "active" || lifecycle == "archived" || lifecycle == "deleted"
}

// parseRepositoryCoordinate splits repository into its owner and name,
// rejecting coordinates that are empty, missing the separator, or carry a
// second slash in the name segment. It is a pure function extracted from
// UpdateRepositoryLifecycle and RepositoryActive so the coordinate format is
// independently testable without a database connection.
func parseRepositoryCoordinate(repository string) (owner, name string, ok bool) {
	owner, name, ok = strings.Cut(repository, "/")
	if !ok || owner == "" || name == "" || strings.Contains(name, "/") {
		return "", "", false
	}
	return owner, name, true
}

// classifyRepositoryLifecycleUpdate validates UpdateRepositoryLifecycle's
// preconditions in a fixed order and reports which one failed. It is a pure
// function extracted from UpdateRepositoryLifecycle so each precondition is
// independently testable, and so the diagnostic log can report a rejection
// stage without logging the repository coordinate or GitHub identifier.
func classifyRepositoryLifecycleUpdate(
	githubID int64, repository, lifecycle string, admittedAt time.Time,
) (owner, name string, stage repositoryLifecycleRejectionStage, err error) {
	if githubID <= 0 {
		return "", "", repositoryLifecycleRejectionStageID, errors.New("invalid repository lifecycle update")
	}
	if !validRepositoryLifecycle(lifecycle) {
		return "", "", repositoryLifecycleRejectionStageLifecycle, errors.New("invalid repository lifecycle update")
	}
	if admittedAt.IsZero() {
		return "", "", repositoryLifecycleRejectionStageTimestamp, errors.New("repository lifecycle admission timestamp is required")
	}
	owner, name, ok := parseRepositoryCoordinate(repository)
	if !ok {
		return "", "", repositoryLifecycleRejectionStageCoordinate, errors.New("invalid repository coordinate")
	}
	return owner, name, "", nil
}

// UpdateRepositoryLifecycle records a verified, enrolled repository delivery.
// The collector state is independent of the replaceable canonical projection.
func (s *Store) UpdateRepositoryLifecycle(ctx context.Context, githubID int64, repository, lifecycle string, admittedAt time.Time) error {
	owner, name, stage, err := classifyRepositoryLifecycleUpdate(githubID, repository, lifecycle, admittedAt)
	if err != nil {
		postgresxLog.Printf("repository lifecycle update rejected stage=%s", stage)
		return err
	}
	id := "github:repository:" + strconv.FormatInt(githubID, 10)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended(current_schema() || ':' || $1, 0))`, s.namespace); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO cao_state(namespace,revision,data_revision,evaluated_at,schema_version)
		VALUES($1,0,'','epoch',$2) ON CONFLICT(namespace) DO NOTHING`, s.namespace, model.SchemaVersion); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO cao_repository_lifecycle(namespace,id,owner,name,lifecycle,changed_at)
		VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(namespace,id) DO UPDATE SET
		owner=excluded.owner,name=excluded.name,lifecycle=excluded.lifecycle,changed_at=excluded.changed_at
		WHERE cao_repository_lifecycle.changed_at<=excluded.changed_at`,
		s.namespace, id, owner, name, lifecycle, admittedAt.UTC()); err != nil {
		return err
	}

	if err = overlayRepositoryLifecycle(ctx, s.namespace, func(ctx context.Context, statement string, args ...any) error {
		_, err := tx.ExecContext(ctx, statement, args...)
		return err
	}); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE cao_state SET revision=revision+1 WHERE namespace=$1`, s.namespace); err != nil {
		return err
	}
	return tx.Commit()
}

// RepositoryActive reports whether a repository's latest admitted lifecycle
// still allows fresh collection; unknown repositories remain eligible.
func (s *Store) RepositoryActive(ctx context.Context, repository string) (bool, error) {
	owner, name, ok := strings.Cut(repository, "/")
	if !ok || owner == "" || name == "" {
		return false, errors.New("invalid repository coordinate")
	}
	var inactive bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM cao_repository_lifecycle
		WHERE namespace=$1 AND owner=$2 AND name=$3 AND lifecycle IN ('archived','deleted'))`,
		s.namespace, owner, name).Scan(&inactive)
	return !inactive, err
}

func repositoryLifecyclePresence() string {
	bits := make([]byte, len(entityTables["$repositories"].columns))
	for i, column := range entityTables["$repositories"].columns {
		bits[i] = '0'
		if column.field == "id" || column.field == "owner" || column.field == "name" || column.field == "lifecycle" {
			bits[i] = '1'
		}
	}
	return string(bits)
}

func overlayRepositoryLifecycle(ctx context.Context, namespace string, exec func(context.Context, string, ...any) error) error {
	return exec(ctx, `UPDATE repositories r SET lifecycle=l.lifecycle,
		present_fields=set_bit(r.present_fields, $2, 1)
		FROM cao_repository_lifecycle l WHERE r.namespace=$1 AND l.namespace=r.namespace AND l.id=r.id`,
		namespace, repositoryLifecycleIndex())
}

// ClearRepositoryLifecycles removes legacy lifecycle overrides only after a
// complete GitHub enumeration verifies that these immutable IDs are active.
func (s *Store) ClearRepositoryLifecycles(ctx context.Context, identifiers []int64) error {
	ids := make([]string, 0, len(identifiers))
	for _, id := range identifiers {
		if id > 0 {
			ids = append(ids, "github:repository:"+strconv.FormatInt(id, 10))
		}
	}
	if len(ids) == 0 {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM cao_repository_lifecycle WHERE namespace=$1 AND id=ANY($2)`, s.namespace, ids)
	return err
}

func repositoryLifecycleIndex() int {
	for i, column := range entityTables["$repositories"].columns {
		if column.field == "lifecycle" {
			return i
		}
	}
	panic("repository lifecycle is missing from the storage contract")
}
