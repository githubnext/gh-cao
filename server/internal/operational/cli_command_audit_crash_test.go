package operational_test

import (
	"testing"

	"github.com/githubnext/gh-cao/server/internal/operational"
)

func TestAuditedPostgresConnectionLossDuringAppend(t *testing.T) {
	f := newAuditFixture(t, "postgres")
	lease := acquireAuditLease(t, f)
	wait, release := f.gate(t, false)
	done := make(chan error, 1)
	go func() {
		_, _, err := f.queue.EnqueueAudited(t.Context(), auditInvocation, "request", 0, admissionEvent(0))
		done <- err
	}()
	wait()
	var pid int
	if err := f.db.QueryRowContext(t.Context(), `SELECT pid FROM pg_locks WHERE locktype='advisory' AND classid=9132 AND objid=$1 AND NOT granted`, f.gateKey).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	f.exec(t, `SELECT pg_terminate_backend($1)`, pid)
	if err := <-done; err == nil {
		t.Fatal("lost transaction connection reported success")
	}
	release()
	f.clearGate(t)
	verifyAuditCrashRecovery(t, f, lease)
}

func verifyAuditCrashRecovery(t *testing.T, f *auditFixture, lease operational.CLICommandLease) {
	t.Helper()
	if commands, events := f.counts(t); commands != 0 || events != 0 {
		t.Fatalf("crash leaked partial admission: commands=%d events=%d", commands, events)
	}
	command, err := f.executor.Claim(t.Context(), lease)
	if err != nil || command != nil {
		t.Fatalf("crash left runnable work: %+v, %v", command, err)
	}
	admitted, event, err := f.queue.EnqueueAudited(t.Context(), auditInvocation, "request", 0, admissionEvent(0))
	if err != nil || event.Sequence != 1 {
		t.Fatalf("recovery did not roll back idempotency and audit sequence: %+v, %v", event, err)
	}
	replayed, _, err := f.executor.EnqueueAudited(t.Context(), auditInvocation, "request", 0, admissionEvent(0))
	if err != nil || replayed.ID != admitted.ID {
		t.Fatalf("recovery duplicated work: %+v, %v", replayed, err)
	}
	if _, err := f.store.VerifyAudit(t.Context()); err != nil {
		t.Fatal(err)
	}
}
