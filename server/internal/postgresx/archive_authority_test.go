package postgresx

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/sessionarchive"
)

func nativeArchiveEnrollment(ctx context.Context, t *testing.T, store *Store, repository string) sessionarchive.Source {
	t.Helper()
	enrollment := store.NativeEnrollment()
	if err := enrollment.WithMutation(ctx, func(ctx context.Context) error {
		return enrollment.Enroll(ctx, 7, []string{repository})
	}); err != nil {
		t.Fatal(err)
	}
	scope, err := enrollment.CurrentScope(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	return sessionarchive.Source{Repository: scope.Repository, InstallationID: scope.InstallationID, Epoch: scope.Epoch}
}

func TestNativeArchiveAuthorityAtomicWithdrawalAndDurableCleanup(t *testing.T) {
	first, config := nativeTestStore(t)
	second, err := NewConfig(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = second.Close() }()
	a, b := first.ArchiveAuthority(), second.ArchiveAuthority()
	source := nativeArchiveEnrollment(t.Context(), t, first, "octo/api")
	other := nativeArchiveEnrollment(t.Context(), t, first, "octo/other")
	const destination = "synthetic-native-blob-v1"
	for _, registered := range []sessionarchive.Source{source, other} {
		if err := a.Register(t.Context(), destination, registered); err != nil {
			t.Fatal(err)
		}
	}
	if err := b.Register(t.Context(), destination, source); err != nil {
		t.Fatal("duplicate replica registration:", err)
	}
	var revision int64
	if err := first.db.QueryRowContext(t.Context(), `SELECT revision FROM cao_state WHERE namespace=$1`, first.namespace).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	// Force failure after source retirement has been staged in the transaction.
	if _, err := first.db.ExecContext(t.Context(), fmt.Sprintf(
		`ALTER TABLE cao_state ADD CONSTRAINT archive_rollback CHECK (revision <= %d)`, revision)); err != nil {
		t.Fatal(err)
	}
	enrollment := second.NativeEnrollment()
	withdraw := func(ctx context.Context) error {
		return enrollment.WithMutation(ctx, func(ctx context.Context) error {
			_, err := enrollment.Withdraw(ctx, source.InstallationID, []string{source.Repository})
			return err
		})
	}
	if err := withdraw(t.Context()); err == nil {
		t.Fatal("source failure was not propagated")
	}
	if current, err := b.Current(t.Context(), destination, source.Repository); err != nil || current != source {
		t.Fatalf("rolled-back withdrawal retired registered evidence: %+v %v", current, err)
	}
	if count, err := b.DrainWithdrawals(t.Context(), destination, 10, func(context.Context, sessionarchive.Source) error {
		t.Error("rolled-back source mutation leaked a cleanup task")
		return nil
	}); err != nil || count != 0 {
		t.Fatalf("rollback outbox: %d %v", count, err)
	}
	if _, err := first.db.ExecContext(t.Context(), `ALTER TABLE cao_state DROP CONSTRAINT archive_rollback`); err != nil {
		t.Fatal(err)
	}
	if err := withdraw(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Current(t.Context(), destination, source.Repository); !errors.Is(err, sessionarchive.ErrWithdrawn) {
		t.Fatalf("physical Blob cleanup was required to hide withdrawn content: %v", err)
	}
	failed := errors.New("synthetic Blob cleanup unavailable")
	if count, err := a.DrainWithdrawals(t.Context(), destination, 1, func(_ context.Context, pending sessionarchive.Source) error {
		if pending != source {
			t.Errorf("cleanup lost original source: %+v", pending)
		}
		return failed
	}); count != 0 || !errors.Is(err, failed) {
		t.Fatalf("failed cleanup was acknowledged: %d %v", count, err)
	}
	// The second instance resumes retirement without holding current enrollment.
	var next sessionarchive.Source
	if count, err := b.DrainWithdrawals(t.Context(), destination, 1, func(ctx context.Context, pending sessionarchive.Source) error {
		if pending != source {
			return errors.New("cleanup retargeted the enrolled source")
		}
		next = nativeArchiveEnrollment(ctx, t, first, source.Repository)
		return a.Register(ctx, destination, next)
	}); err != nil || count != 1 {
		t.Fatalf("cross-instance cleanup/re-enrollment: %d %v", count, err)
	}
	if next.Epoch <= source.Epoch {
		t.Fatal("re-enrollment reused old archive consent")
	}
	if current, err := b.Current(t.Context(), destination, source.Repository); err != nil || current != next {
		t.Fatalf("old cleanup retired new source: %+v %v", current, err)
	}
	if err := a.Register(t.Context(), destination, source); !errors.Is(err, sessionarchive.ErrWithdrawn) {
		t.Fatalf("old writer revived withdrawn archive: %v", err)
	}
	if current, err := a.Current(t.Context(), destination, other.Repository); err != nil || current != other {
		t.Fatalf("unrelated archive scope changed: %+v %v", current, err)
	}
	var rows, retired int
	if err := first.db.QueryRowContext(t.Context(), `SELECT count(*),count(*) FILTER(WHERE withdrawn AND NOT cleanup_pending)
		FROM cao_archive_sources WHERE namespace=$1 AND repository=$2`, first.namespace, source.Repository).Scan(&rows, &retired); err != nil ||
		rows != 2 || retired != 1 {
		t.Fatalf("retirement discarded its permanent privacy fence: rows=%d retired=%d err=%v", rows, retired, err)
	}
}

func TestNativeArchiveAuthorityReadLocksAndConnectionLoss(t *testing.T) {
	first, config := nativeTestStore(t)
	config = config.Copy()
	config.RuntimeParams["application_name"] = fmt.Sprintf("native-archive-reader-%d", time.Now().UnixNano())
	second, err := NewConfig(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = second.Close() }()
	source := nativeArchiveEnrollment(t.Context(), t, first, "octo/api")
	const destination = "synthetic-native-blob-v1"
	if err := first.ArchiveAuthority().Register(t.Context(), destination, source); err != nil {
		t.Fatal(err)
	}
	enrollment := first.NativeEnrollment()
	if err := second.ArchiveAuthority().WithRead(t.Context(), destination, source, func(ctx context.Context) error {
		expiry, cancel := context.WithTimeout(ctx, 150*time.Millisecond)
		defer cancel()
		if err := enrollment.WithMutation(expiry, func(ctx context.Context) error {
			_, err := enrollment.Withdraw(ctx, source.InstallationID, []string{source.Repository})
			return err
		}); err == nil {
			t.Error("enrollment committed while archive response delivery was pinned")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	err = second.ArchiveAuthority().WithRead(ctx, destination, source, func(ctx context.Context) error {
		var killed bool
		if err := first.db.QueryRowContext(ctx, `SELECT pg_terminate_backend(pid) FROM pg_stat_activity
			WHERE application_name=$1 AND pid<>pg_backend_pid()`, config.RuntimeParams["application_name"]).Scan(&killed); err != nil {
			return err
		}
		if !killed {
			return errors.New("lock-holding test backend did not terminate")
		}
		<-ctx.Done()
		return nil
	})
	if err == nil || ctx.Err() != nil {
		t.Fatalf("connection loss did not promptly cancel pinned response: %v parent=%v", err, ctx.Err())
	}
	if err := enrollment.WithMutation(t.Context(), func(ctx context.Context) error {
		_, err := enrollment.Withdraw(ctx, source.InstallationID, []string{source.Repository})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := second.ArchiveAuthority().WithRead(t.Context(), destination, source, func(context.Context) error {
		t.Error("withdrawn read callback executed")
		return nil
	}); !errors.Is(err, sessionarchive.ErrWithdrawn) {
		t.Fatalf("withdrawn read was not rejected: %v", err)
	}
}
