package postgresx

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/githubnext/gh-cao/server/internal/adxtrace"
	"github.com/githubnext/gh-cao/server/internal/collectnative"
	"github.com/githubnext/gh-cao/server/internal/sessionexport"
)

type enrollmentMutationKey struct{}

type enrollmentChange struct {
	previous collectnative.EnrollmentScope
	next     int64
}

type enrollmentMutation struct {
	store   *Store
	tx      *sql.Tx
	changes map[string]enrollmentChange
}

type nativeEnrollment struct {
	store *Store
	trace *adxtrace.Worker
}

type enrollmentTraceTransactionKey struct{}

// NativeEnrollment makes the acquisition scope rows authoritative for native
// ingestion membership. Operational metadata remains an index, not authority.
func (s *Store) NativeEnrollment() collectnative.EnrollmentAuthority {
	return nativeEnrollment{store: s}
}

// NativeEnrollmentWithTrace composes scope and approved trace withdrawal on the
// same database transaction. An arbitrary filesystem or separately committed
// destination cannot participate in this atomic privacy boundary.
func (s *Store) NativeEnrollmentWithTrace(ctx context.Context, config adxtrace.Config, destination string,
	sink adxtrace.Sink, readers ...sessionexport.MiningReader,
) (collectnative.EnrollmentAuthority, *adxtrace.Worker, error) {
	if !config.SharedStorage {
		return nil, nil, collectnative.ErrUnsupported
	}
	if err := config.Validate(); err != nil {
		return nil, nil, err
	}
	storage, err := s.TraceStorage(destination, config.MaxSpoolBytes, config.MaxEntries)
	if err != nil {
		return nil, nil, err
	}
	worker, err := adxtrace.NewShared(ctx, config, destination, sink, storage, readers...)
	if err != nil {
		return nil, nil, err
	}
	return nativeEnrollment{store: s, trace: worker}, worker, nil
}

func (a nativeEnrollment) mutation(ctx context.Context) (*enrollmentMutation, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	mutation, ok := ctx.Value(enrollmentMutationKey{}).(*enrollmentMutation)
	if !ok || mutation.store != a.store {
		return nil, collectnative.ErrUnsupported
	}
	return mutation, nil
}

func (a nativeEnrollment) WithMutation(ctx context.Context, action func(context.Context) error) error {
	if action == nil || ctx.Value(enrollmentMutationKey{}) != nil {
		return collectnative.ErrUnsupported
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	tx, err := a.store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var locked bool
	if err := tx.QueryRowContext(ctx, `SELECT pg_try_advisory_xact_lock(
		hashtextextended(current_schema() || ':' || $1 || ':native-enrollment',0))`,
		a.store.namespace).Scan(&locked); err != nil {
		return err
	}
	if !locked {
		return collectnative.ErrBusy
	}
	mutation := &enrollmentMutation{store: a.store, tx: tx, changes: map[string]enrollmentChange{}}
	operation := context.WithValue(ctx, enrollmentMutationKey{}, mutation)
	if err := action(operation); err != nil {
		return err
	}
	if err := a.flush(operation, mutation); err != nil {
		return err
	}
	return tx.Commit()
}

func (a nativeEnrollment) originalScope(ctx context.Context, tx *sql.Tx, repository string) (collectnative.EnrollmentScope, error) {
	scope := collectnative.EnrollmentScope{Repository: repository}
	err := tx.QueryRowContext(ctx, `SELECT installation_id,epoch FROM cao_acquisition_scopes
		WHERE namespace=$1 AND repository=$2`, a.store.namespace, repository).Scan(&scope.InstallationID, &scope.Epoch)
	if errors.Is(err, sql.ErrNoRows) {
		return scope, nil
	}
	return scope, err
}

func validEnrollmentRepository(repository string) bool {
	return (collectnative.Identity{Repository: repository, InstallationID: 1, RunID: 1, Attempt: 1}).Validate() == nil
}

func (a nativeEnrollment) CurrentScope(ctx context.Context, repository string) (collectnative.EnrollmentScope, error) {
	if !validEnrollmentRepository(repository) {
		return collectnative.EnrollmentScope{}, collectnative.ErrIdentity
	}
	if ctx.Value(enrollmentMutationKey{}) == nil {
		return a.store.Acquisition().CurrentScope(ctx, repository)
	}
	mutation, err := a.mutation(ctx)
	if err != nil {
		return collectnative.EnrollmentScope{}, err
	}
	if change, ok := mutation.changes[repository]; ok {
		if change.next != change.previous.InstallationID {
			// A predicted epoch must not escape into durable task admission:
			// rollback could otherwise let later consent reuse that identity.
			return collectnative.EnrollmentScope{}, collectnative.ErrBusy
		}
		if change.next == 0 {
			return collectnative.EnrollmentScope{}, collectnative.ErrIdentity
		}
		scope := change.previous
		scope.InstallationID = change.next
		return scope, nil
	}
	scope, err := a.originalScope(ctx, mutation.tx, repository)
	if err == nil && scope.InstallationID == 0 {
		err = collectnative.ErrIdentity
	}
	return scope, err
}

func (a nativeEnrollment) stage(ctx context.Context, repository string, installation int64) error {
	mutation, err := a.mutation(ctx)
	if err != nil {
		return err
	}
	if installation < 0 || !validEnrollmentRepository(repository) {
		return collectnative.ErrIdentity
	}
	change, exists := mutation.changes[repository]
	if !exists {
		if len(mutation.changes) >= 100000 {
			return collectnative.ErrCapacity
		}
		change.previous, err = a.originalScope(ctx, mutation.tx, repository)
		if err != nil {
			return err
		}
	}
	change.next = installation
	mutation.changes[repository] = change
	return nil
}

func (a nativeEnrollment) Enroll(ctx context.Context, installation int64, repositories []string) error {
	if installation <= 0 {
		return collectnative.ErrIdentity
	}
	for _, repository := range repositories {
		if err := a.stage(ctx, repository, installation); err != nil {
			return err
		}
	}
	return nil
}

func (a nativeEnrollment) Withdraw(ctx context.Context, installation int64, repositories []string) ([]string, error) {
	if installation < 0 {
		return nil, collectnative.ErrIdentity
	}
	removed := make([]string, 0, len(repositories))
	for _, repository := range repositories {
		if !validEnrollmentRepository(repository) {
			return nil, collectnative.ErrIdentity
		}
		mutation, err := a.mutation(ctx)
		if err != nil {
			return nil, err
		}
		scope, err := a.originalScope(ctx, mutation.tx, repository)
		if change, staged := mutation.changes[repository]; staged {
			scope = change.previous
			scope.InstallationID = change.next
		}
		if err != nil {
			return nil, err
		}
		if scope.InstallationID == 0 {
			continue
		}
		if installation > 0 && scope.InstallationID != installation {
			continue
		}
		if err := a.stage(ctx, repository, 0); err != nil {
			return nil, err
		}
		removed = append(removed, repository)
	}
	return removed, nil
}

func (a nativeEnrollment) scopeRows(ctx context.Context, statement string, args ...any) (*sql.Rows, error) {
	if ctx.Value(enrollmentMutationKey{}) == nil {
		return a.store.db.QueryContext(ctx, statement, args...)
	}
	mutation, err := a.mutation(ctx)
	if err != nil {
		return nil, err
	}
	return mutation.tx.QueryContext(ctx, statement, args...)
}

func (a nativeEnrollment) delta(ctx context.Context) ([]byte, error) {
	values := map[string]int64{}
	if ctx.Value(enrollmentMutationKey{}) != nil {
		mutation, err := a.mutation(ctx)
		if err != nil {
			return nil, err
		}
		for repository, change := range mutation.changes {
			values[repository] = change.next
		}
	}
	return json.Marshal(values)
}

const enrollmentView = `WITH delta AS (
	SELECT key AS repository,value::bigint AS installation_id FROM jsonb_each_text($2::jsonb)
), membership AS (
	SELECT s.repository,s.installation_id FROM cao_acquisition_scopes s
	WHERE namespace=$1 AND NOT EXISTS(SELECT 1 FROM delta d WHERE d.repository=s.repository)
	UNION ALL SELECT repository,installation_id FROM delta
) `

func (a nativeEnrollment) ScanRepositories(ctx context.Context, installation int64, cursor string, count int) ([]string, string, error) {
	if installation < 0 || count < 1 || count > 1000 || cursor != "" && !validEnrollmentRepository(cursor) {
		return nil, "", collectnative.ErrIdentity
	}
	delta, err := a.delta(ctx)
	if err != nil {
		return nil, "", err
	}
	rows, err := a.scopeRows(ctx, enrollmentView+`SELECT repository FROM membership
		WHERE installation_id>0 AND ($3::bigint=0 OR installation_id=$3)
		AND repository COLLATE "C">$4 COLLATE "C" ORDER BY repository COLLATE "C" LIMIT $5`,
		a.store.namespace, string(delta), installation, cursor, count+1)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = rows.Close() }()
	result := make([]string, 0, count+1)
	for rows.Next() {
		var repository string
		if err := rows.Scan(&repository); err != nil {
			return nil, "", err
		}
		result = append(result, repository)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(result) > count {
		result = result[:count]
		next = result[len(result)-1]
	}
	return result, next, nil
}

func (a nativeEnrollment) RepositoryCount(ctx context.Context) (int64, error) {
	delta, err := a.delta(ctx)
	if err != nil {
		return 0, err
	}
	rows, err := a.scopeRows(ctx, enrollmentView+`SELECT count(*) FROM membership WHERE installation_id>0`, a.store.namespace, string(delta))
	if err != nil {
		return 0, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return 0, err
		}
		return 0, sql.ErrNoRows
	}
	var count int64
	err = rows.Scan(&count)
	return count, err
}

func (a nativeEnrollment) Installations(ctx context.Context) ([]int64, error) {
	delta, err := a.delta(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := a.scopeRows(ctx, enrollmentView+`SELECT DISTINCT installation_id FROM membership
		WHERE installation_id>0 ORDER BY installation_id LIMIT 100001`, a.store.namespace, string(delta))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var identifiers []int64
	for rows.Next() {
		if len(identifiers) == 100000 {
			return nil, collectnative.ErrCapacity
		}
		var installation int64
		if err := rows.Scan(&installation); err != nil {
			return nil, err
		}
		identifiers = append(identifiers, installation)
	}
	return identifiers, rows.Err()
}

func (a nativeEnrollment) HasInstallation(ctx context.Context, installation int64) (bool, error) {
	if installation <= 0 {
		return false, collectnative.ErrIdentity
	}
	delta, err := a.delta(ctx)
	if err != nil {
		return false, err
	}
	rows, err := a.scopeRows(ctx, enrollmentView+`SELECT EXISTS(SELECT 1 FROM membership WHERE installation_id=$3)`,
		a.store.namespace, string(delta), installation)
	if err != nil {
		return false, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return false, err
		}
		return false, sql.ErrNoRows
	}
	var known bool
	err = rows.Scan(&known)
	return known, err
}

func (a nativeEnrollment) flush(ctx context.Context, mutation *enrollmentMutation) error {
	if len(mutation.changes) == 0 {
		return ctx.Err()
	}
	if err := a.store.lockInvocation(ctx, mutation.tx); err != nil {
		return err
	}
	ctx = context.WithValue(ctx, enrollmentTraceTransactionKey{}, mutation)
	repositories := make([]string, 0, len(mutation.changes))
	for repository := range mutation.changes {
		repositories = append(repositories, repository)
	}
	slices.Sort(repositories)
	for _, repository := range repositories {
		change := mutation.changes[repository]
		current, err := a.originalScope(ctx, mutation.tx, repository)
		if err != nil {
			return err
		}
		if current.Epoch > 0 {
			if err := mutation.tx.QueryRowContext(ctx, `SELECT installation_id,epoch FROM cao_acquisition_scopes
				WHERE namespace=$1 AND repository=$2 FOR UPDATE`, a.store.namespace, repository).
				Scan(&current.InstallationID, &current.Epoch); err != nil {
				return err
			}
		}
		if current != change.previous {
			return collectnative.ErrClaimLost
		}
		if current.InstallationID == change.next {
			continue
		}
		if current.InstallationID > 0 {
			if err := a.store.withdrawArchiveSources(ctx, mutation.tx, repository); err != nil {
				return err
			}
			if a.trace != nil {
				if err := a.trace.WithdrawRepository(ctx, repository); err != nil {
					if errors.Is(err, adxtrace.ErrBusy) {
						return errors.Join(collectnative.ErrBusy, err)
					}
					return err
				}
			}
			if err := a.store.eraseNativeRepository(ctx, mutation.tx, repository); err != nil {
				return err
			}
		}
		var epoch int64
		err = mutation.tx.QueryRowContext(ctx, `INSERT INTO cao_acquisition_scopes(namespace,repository,installation_id,epoch)
			VALUES($1,$2,$3,1) ON CONFLICT(namespace,repository)
			DO UPDATE SET installation_id=excluded.installation_id,epoch=cao_acquisition_scopes.epoch+1
			WHERE cao_acquisition_scopes.epoch=$4 AND cao_acquisition_scopes.installation_id=$5 RETURNING epoch`,
			a.store.namespace, repository, change.next, change.previous.Epoch, change.previous.InstallationID).Scan(&epoch)
		if errors.Is(err, sql.ErrNoRows) {
			return collectnative.ErrClaimLost
		}
		if err != nil {
			return err
		}
		if _, err := mutation.tx.ExecContext(ctx, `DELETE FROM cao_acquisition_attempts WHERE namespace=$1 AND repository=$2`,
			a.store.namespace, repository); err != nil {
			return err
		}
	}
	_, err := mutation.tx.ExecContext(ctx, `UPDATE cao_state SET revision=revision+1 WHERE namespace=$1`, a.store.namespace)
	return err
}
