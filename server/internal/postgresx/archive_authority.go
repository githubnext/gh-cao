package postgresx

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/githubnext/gh-cao/server/internal/collectnative"
	"github.com/githubnext/gh-cao/server/internal/sessionarchive"
)

type archiveAuthority struct{ store *Store }

// ArchiveAuthority keeps Blob visibility and retirement tied to committed source
// enrollment. Content remains in the separately owned archive destination.
func (s *Store) ArchiveAuthority() sessionarchive.NativeAuthority {
	return archiveAuthority{store: s}
}

var _ sessionarchive.NativeAuthority = archiveAuthority{}

func validArchiveDestination(destination string) bool {
	return len(destination) > 0 && len(destination) <= 512 && utf8.ValidString(destination) &&
		!strings.ContainsRune(destination, 0) && strings.TrimSpace(destination) == destination
}

func archiveSource(ctx context.Context, tx *sql.Tx, namespace string, source sessionarchive.Source) error {
	var installation, epoch int64
	err := tx.QueryRowContext(ctx, `SELECT installation_id,epoch FROM cao_acquisition_scopes
		WHERE namespace=$1 AND repository=$2 FOR SHARE`, namespace, source.Repository).Scan(&installation, &epoch)
	if errors.Is(err, sql.ErrNoRows) || err == nil && (installation != source.InstallationID || epoch != source.Epoch) {
		return sessionarchive.ErrWithdrawn
	}
	return err
}

func (a archiveAuthority) Register(ctx context.Context, destination string, source sessionarchive.Source) error {
	if !validArchiveDestination(destination) || source.Validate() != nil {
		return sessionarchive.ErrIntegrity
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	tx, err := a.store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := archiveSource(ctx, tx, a.store.namespace, source); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(
		hashtextextended(current_schema() || ':' || $1 || ':archive-registrations',0))`, a.store.namespace); err != nil {
		return err
	}
	var withdrawn bool
	err = tx.QueryRowContext(ctx, `SELECT withdrawn FROM cao_archive_sources
		WHERE namespace=$1 AND destination=$2 AND repository=$3 AND installation_id=$4 AND epoch=$5`,
		a.store.namespace, destination, source.Repository, source.InstallationID, source.Epoch).Scan(&withdrawn)
	if err == nil {
		if withdrawn {
			return sessionarchive.ErrWithdrawn
		}
		return tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	var total, repository int
	if err := tx.QueryRowContext(ctx, `SELECT count(*),count(*) FILTER(WHERE repository=$2)
		FROM cao_archive_sources WHERE namespace=$1`, a.store.namespace, source.Repository).Scan(&total, &repository); err != nil {
		return err
	}
	if total >= 1_000_000 || repository >= 4096 {
		return collectnative.ErrCapacity
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO cao_archive_sources
		(namespace,destination,repository,installation_id,epoch,withdrawn,cleanup_pending)
		VALUES($1,$2,$3,$4,$5,false,false)`, a.store.namespace, destination,
		source.Repository, source.InstallationID, source.Epoch); err != nil {
		return err
	}
	return tx.Commit()
}

func (a archiveAuthority) Current(ctx context.Context, destination, repository string) (sessionarchive.Source, error) {
	source := sessionarchive.Source{Repository: repository}
	if !validArchiveDestination(destination) || !validEnrollmentRepository(repository) {
		return source, sessionarchive.ErrIntegrity
	}
	err := a.store.db.QueryRowContext(ctx, `SELECT r.installation_id,r.epoch
		FROM cao_archive_sources r JOIN cao_acquisition_scopes s
		ON s.namespace=r.namespace AND s.repository=r.repository
		AND s.installation_id=r.installation_id AND s.epoch=r.epoch
		WHERE r.namespace=$1 AND r.destination=$2 AND r.repository=$3 AND NOT r.withdrawn`,
		a.store.namespace, destination, repository).Scan(&source.InstallationID, &source.Epoch)
	if errors.Is(err, sql.ErrNoRows) {
		return source, sessionarchive.ErrWithdrawn
	}
	return source, err
}

func (a archiveAuthority) WithRead(ctx context.Context, destination string, source sessionarchive.Source,
	action func(context.Context) error,
) error {
	if !validArchiveDestination(destination) || source.Validate() != nil || action == nil {
		return sessionarchive.ErrIntegrity
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	tx, err := a.store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := archiveSource(ctx, tx, a.store.namespace, source); err != nil {
		return err
	}
	var withdrawn bool
	err = tx.QueryRowContext(ctx, `SELECT withdrawn FROM cao_archive_sources
		WHERE namespace=$1 AND destination=$2 AND repository=$3 AND installation_id=$4 AND epoch=$5 FOR SHARE`,
		a.store.namespace, destination, source.Repository, source.InstallationID, source.Epoch).Scan(&withdrawn)
	if errors.Is(err, sql.ErrNoRows) || err == nil && withdrawn {
		return sessionarchive.ErrWithdrawn
	}
	if err != nil {
		return err
	}
	if err := archiveTransactionAction(ctx, tx, action); err != nil {
		return err
	}
	return tx.Commit()
}

// Callback delivery must stop if its lock-holding connection is lost, not just
// when the caller's HTTP deadline expires.
func archiveTransactionAction(ctx context.Context, tx *sql.Tx, action func(context.Context) error) error {
	operation, cancel := context.WithCancel(ctx)
	defer cancel()
	stop, done := make(chan struct{}), make(chan error, 1)
	go func() {
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				done <- nil
				return
			case <-operation.Done():
				done <- operation.Err()
				return
			case <-ticker.C:
				if _, err := tx.ExecContext(operation, `SELECT 1`); err != nil {
					cancel()
					done <- err
					return
				}
			}
		}
	}()
	err := action(operation)
	close(stop)
	watchErr := <-done
	return errors.Join(err, watchErr, ctx.Err())
}

func (a archiveAuthority) DrainWithdrawals(ctx context.Context, destination string, limit int,
	action func(context.Context, sessionarchive.Source) error,
) (int, error) {
	if !validArchiveDestination(destination) || limit < 1 || limit > 256 || action == nil {
		return 0, sessionarchive.ErrIntegrity
	}
	for count := 0; count < limit; count++ {
		more, err := a.drainWithdrawal(ctx, destination, action)
		if err != nil || !more {
			return count, err
		}
	}
	return limit, nil
}

func (a archiveAuthority) drainWithdrawal(ctx context.Context, destination string,
	action func(context.Context, sessionarchive.Source) error,
) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	tx, err := a.store.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	var source sessionarchive.Source
	err = tx.QueryRowContext(ctx, `SELECT repository,installation_id,epoch FROM cao_archive_sources
		WHERE namespace=$1 AND destination=$2 AND cleanup_pending
		ORDER BY repository,installation_id,epoch LIMIT 1 FOR UPDATE SKIP LOCKED`,
		a.store.namespace, destination).Scan(&source.Repository, &source.InstallationID, &source.Epoch)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := archiveTransactionAction(ctx, tx, func(ctx context.Context) error { return action(ctx, source) }); err != nil {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE cao_archive_sources SET cleanup_pending=false
		WHERE namespace=$1 AND destination=$2 AND repository=$3 AND installation_id=$4 AND epoch=$5 AND withdrawn`,
		a.store.namespace, destination, source.Repository, source.InstallationID, source.Epoch); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

func (s *Store) withdrawArchiveSources(ctx context.Context, tx *sql.Tx, repository string) error {
	_, err := tx.ExecContext(ctx, `UPDATE cao_archive_sources SET withdrawn=true,cleanup_pending=true
		WHERE namespace=$1 AND repository=$2 AND NOT withdrawn`, s.namespace, repository)
	return err
}
