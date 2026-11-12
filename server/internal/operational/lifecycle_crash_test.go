package operational_test

import (
	"errors"
	"testing"

	"github.com/githubnext/gh-cao/server/internal/operational"
)

func TestLifecyclePostgresConnectionLoss(t *testing.T) {
	for _, phase := range []string{"result", "finished"} {
		t.Run(phase, func(t *testing.T) {
			f := newAuditFixture(t, "postgres")
			lease := acquireAuditLease(t, f)
			command := claimedLifecycleCommand(t, f, lease)
			if phase == "finished" {
				if err := f.executor.Finish(t.Context(), lease, command.ID, operational.CLICommandResult{Status: operational.CLICommandCompleted}); err != nil {
					t.Fatal(err)
				}
			}
			wait, release := f.gate(t, false)
			done := make(chan error, 1)
			go func() {
				if phase == "result" {
					done <- f.executor.Finish(t.Context(), lease, command.ID, operational.CLICommandResult{Status: operational.CLICommandCompleted})
				} else {
					done <- f.executor.Settle(t.Context(), lease, command.ID, nil)
				}
			}()
			wait()
			var pid int
			if err := f.db.QueryRowContext(t.Context(), `SELECT pid FROM pg_locks WHERE locktype='advisory' AND classid=9132 AND objid=$1 AND NOT granted`, f.gateKey).Scan(&pid); err != nil {
				t.Fatal(err)
			}
			f.exec(t, `SELECT pg_terminate_backend($1)`, pid)
			if err := <-done; err == nil {
				t.Fatal("lost connection was reported as successful completion")
			}
			release()
			f.clearGate(t)
			verifyLifecycleCrash(t, f, phase, command.ID)
		})
	}
}

func verifyLifecycleCrash(t *testing.T, f *auditFixture, phase string, id int64) {
	t.Helper()
	current, err := f.queue.Detail(t.Context(), id)
	if err != nil || !current.ExecutionClaimed || (phase == "result" && current.Status != operational.CLICommandRunning) {
		t.Fatal("crash committed unaudited lifecycle state", err)
	}
	lifecycleEvents(t, f, "cli_command."+phase, 0)
	expireLifecycleLease(t, f)
	lease := acquireAuditLease(t, f)
	// An interrupted known result remains known. A lost result is explicitly
	// failed/unknown, and neither permits dispatch before operator recovery.
	if _, err := f.executor.Claim(t.Context(), lease); !errors.Is(err, operational.ErrCLIQueueRecovery) {
		t.Fatal("crash recovery silently dispatched work")
	}
	if err := f.executor.AcknowledgeRecovery(t.Context(), lease); err != nil {
		t.Fatal(err)
	}
	event := lifecycleEvents(t, f, "cli_command.finished", 1)[0]
	wantStatus := "failed"
	if phase == "finished" {
		wantStatus = "completed"
	}
	if event.Attributes["status"] != wantStatus || event.Attributes["recovered"] != "true" {
		t.Fatal("recovery misrepresented the retained execution result")
	}
}
