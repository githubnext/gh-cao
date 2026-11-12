package operational_test

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/audit"
	"github.com/githubnext/gh-cao/server/internal/operational"
)

func (f *auditFixture) holdWriter(t *testing.T) *sql.Tx {
	t.Helper()
	tx, err := f.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(t.Context(), `SELECT namespace FROM cao_operational_namespaces WHERE namespace=$1 FOR UPDATE`, f.namespace); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback() })
	return tx
}

func TestAuditedStoreLockWaitsAreBounded(t *testing.T) {
	for _, dialect := range []string{"postgres"} {
		t.Run(dialect, func(t *testing.T) {
			f := newAuditFixture(t, dialect)
			lease := acquireAuditLease(t, f)
			lock := f.holdWriter(t)
			ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
			defer cancel()
			started := time.Now()
			_, _, err := f.queue.EnqueueAudited(ctx, auditInvocation, "request", 0, admissionEvent(0))
			if err == nil || time.Since(started) > 2*time.Second {
				t.Fatalf("admission lock wait did not fail within budget: %v, %v", time.Since(started), err)
			}
			// A canceled shutdown/request context still permits cleanup auditing,
			// but the store must stop even while the namespace remains locked.
			canceled, stop := context.WithCancel(t.Context())
			stop()
			started = time.Now()
			err = audit.New(io.Discard).WithStore(f.store).Record(canceled, "token.revoke_failed", audit.Failure)
			if !errors.Is(err, audit.ErrUnavailable) || time.Since(started) > operational.AuditWriteTimeout+time.Second {
				t.Fatalf("detached audit lock wait did not fail within budget: %v, %v", time.Since(started), err)
			}
			if err := lock.Rollback(); err != nil {
				t.Fatal(err)
			}
			if commands, events := f.counts(t); commands != 0 || events != 0 {
				t.Fatalf("timeout left a partial commit: commands=%d events=%d", commands, events)
			}
			command, event, err := f.queue.EnqueueAudited(t.Context(), auditInvocation, "request", 0, admissionEvent(0))
			if err != nil || event.Sequence != 1 {
				t.Fatalf("retry after timeout = %+v, %v", event, err)
			}
			claimed, err := f.executor.Claim(t.Context(), lease)
			if err != nil || claimed == nil || claimed.ID != command.ID {
				t.Fatalf("retry is not claimable: %+v, %v", claimed, err)
			}
			if _, err := f.store.VerifyAudit(t.Context()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestAuditedAdmissionTimeoutDuringAppendRollsBack(t *testing.T) {
	f := newAuditFixture(t, "postgres")
	lease := acquireAuditLease(t, f)
	wait, release := f.gate(t, false)
	done := make(chan error, 1)
	ctx, cancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
	defer cancel()
	go func() {
		_, _, err := f.queue.EnqueueAudited(ctx, auditInvocation, "request", 0, admissionEvent(0))
		done <- err
	}()
	wait()
	if err := <-done; err == nil {
		t.Fatal("audit statement lock timeout reported success")
	}
	release()
	f.clearGate(t)
	command, err := f.executor.Claim(t.Context(), lease)
	if err != nil || command != nil {
		t.Fatalf("timed-out append leaked runnable command: %+v, %v", command, err)
	}
	if commands, events := f.counts(t); commands != 0 || events != 0 {
		t.Fatalf("statement timeout split mutation: commands=%d events=%d", commands, events)
	}
}
