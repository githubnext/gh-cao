package operational_test

import (
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/operational"
)

func claimedLifecycleCommand(t *testing.T, f *auditFixture, lease operational.CLICommandLease) operational.CLICommand {
	t.Helper()
	command, _, err := f.queue.EnqueueAudited(t.Context(), auditInvocation, "lifecycle", 0, admissionEvent(0))
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := f.executor.Claim(t.Context(), lease)
	if err != nil || claimed == nil || claimed.ID != command.ID {
		t.Fatal("could not claim lifecycle command", err)
	}
	return *claimed
}

func lifecycleEvents(t *testing.T, f *auditFixture, name string, count int) []operational.AuditEvent {
	t.Helper()
	events, err := f.store.ListAudit(t.Context(), operational.AuditFilter{Event: name})
	if err != nil || len(events) != count {
		t.Fatalf("%s: got %d events, want %d: %v", name, len(events), count, err)
	}
	if _, err := f.store.VerifyAudit(t.Context()); err != nil {
		t.Fatal(err)
	}
	return events
}

func TestLifecycleResultAndSettlementAreAtomicAndIdempotent(t *testing.T) {
	for _, dialect := range []string{"postgres"} {
		for _, phase := range []string{"result", "finished"} {
			t.Run(dialect+"/"+phase, func(t *testing.T) {
				f := newAuditFixture(t, dialect)
				lease := acquireAuditLease(t, f)
				command := claimedLifecycleCommand(t, f, lease)
				result := operational.CLICommandResult{Status: operational.CLICommandCompleted, ExitCode: new(0)}
				if phase == "finished" {
					if err := f.executor.Finish(t.Context(), lease, command.ID, result); err != nil {
						t.Fatal(err)
					}
				}
				transition := func() error {
					if phase == "result" {
						return f.executor.Finish(t.Context(), lease, command.ID, result)
					}
					return f.executor.Settle(t.Context(), lease, command.ID, result.ExitCode)
				}
				wait, release := f.gate(t, true)
				done := make(chan error, 1)
				go func() { done <- transition() }()
				wait()
				events, err := f.store.ListAudit(t.Context(), operational.AuditFilter{Event: "cli_command." + phase})
				if err != nil || len(events) != 0 {
					t.Fatal("uncommitted audit event became visible", err)
				}
				release()
				if err := <-done; err == nil {
					t.Fatal("audit fault did not roll back lifecycle transition")
				}
				current, err := f.queue.Detail(t.Context(), command.ID)
				if err != nil || !current.ExecutionClaimed || (phase == "result" && current.Status != operational.CLICommandRunning) {
					t.Fatal("failed append released claim or committed unaudited result", err)
				}
				f.clearGate(t)
				for range 3 {
					if err := transition(); err != nil {
						t.Fatal(err)
					}
				}
				event := lifecycleEvents(t, f, "cli_command."+phase, 1)[0]
				if event.Actor != command.Actor || event.Attributes["repository"] != command.Repository ||
					event.Attributes["command_id"] != strconv.FormatInt(command.ID, 10) || event.Attributes["status"] != "completed" {
					t.Fatal("lifecycle event did not bind authoritative command identity")
				}
				next, err := f.executor.Claim(t.Context(), lease)
				if err != nil || next != nil {
					t.Fatal("replaying completion dispatched work", err)
				}
			})
		}
	}
}

func expireLifecycleLease(t *testing.T, f *auditFixture) {
	t.Helper()
	f.exec(t, `UPDATE cao_operational_cli_queues SET lease_until=clock_timestamp()-interval '1 second' WHERE namespace=$1`, f.namespace)
}

func TestLifecycleRecoveryBeforeRegistrationRetainsClaimsOnAuditFailure(t *testing.T) {
	for _, dialect := range []string{"postgres"} {
		for _, phase := range []string{"interruption", "acknowledgement"} {
			t.Run(dialect+"/"+phase, func(t *testing.T) {
				f := newAuditFixture(t, dialect)
				oldLease := acquireAuditLease(t, f)
				command := claimedLifecycleCommand(t, f, oldLease)
				expireLifecycleLease(t, f)
				var replacement operational.CLICommandLease
				acquire := func() error {
					var err error
					replacement, err = f.executor.Acquire(t.Context(), "replacement", time.Minute)
					return err
				}
				transition := acquire
				if phase == "acknowledgement" {
					if err := acquire(); err != nil {
						t.Fatal(err)
					}
					transition = func() error { return f.executor.AcknowledgeRecovery(t.Context(), replacement) }
				}
				wait, release := f.gate(t, true)
				done := make(chan error, 1)
				go func() { done <- transition() }()
				wait()
				release()
				if err := <-done; err == nil {
					t.Fatal("recovery ignored audit failure")
				}
				current, err := f.queue.Detail(t.Context(), command.ID)
				if err != nil || !current.ExecutionClaimed {
					t.Fatal("recovery failure erased an uncertain pre-registration claim")
				}
				lifecycleEvents(t, f, "cli_command.finished", 0)
				f.clearGate(t)
				if phase == "interruption" {
					if err := acquire(); err != nil {
						t.Fatal(err)
					}
				}
				if _, err := f.executor.Claim(t.Context(), replacement); !errors.Is(err, operational.ErrCLIQueueRecovery) {
					t.Fatal("interrupted command was dispatched before reconciliation")
				}
				if err := f.executor.Settle(t.Context(), oldLease, command.ID, nil); !errors.Is(err, operational.ErrConflict) {
					t.Fatal("stale lease settled the replacement's claim")
				}
				result := lifecycleEvents(t, f, "cli_command.result", 1)[0]
				if result.Attributes["recovered"] != "true" || result.Attributes["execution_claimed"] != "true" || result.Attributes["status"] != "failed" {
					t.Fatal("interruption event manufactured a known result or termination")
				}
				if err := f.executor.AcknowledgeRecovery(t.Context(), replacement); err != nil {
					t.Fatal(err)
				}
				if err := f.executor.AcknowledgeRecovery(t.Context(), replacement); !errors.Is(err, operational.ErrConflict) {
					t.Fatal("duplicate recovery acknowledgement was accepted")
				}
				lifecycleEvents(t, f, "cli_command.finished", 1)
			})
		}
	}
}

func TestLifecycleCancellationWinsLateResult(t *testing.T) {
	for _, dialect := range []string{"postgres"} {
		t.Run(dialect, func(t *testing.T) {
			f := newAuditFixture(t, dialect)
			lease := acquireAuditLease(t, f)
			command := claimedLifecycleCommand(t, f, lease)
			if _, _, err := f.queue.CancelAudited(t.Context(), command.ID, operational.AuditEvent{
				Event: "cli_command.cancel_requested", Outcome: "success", Actor: "admin",
			}); err != nil {
				t.Fatal(err)
			}
			if err := f.executor.Finish(t.Context(), lease, command.ID, operational.CLICommandResult{Status: operational.CLICommandCompleted}); err != nil {
				t.Fatal(err)
			}
			for range 2 {
				if err := f.executor.Settle(t.Context(), lease, command.ID, new(0)); err != nil {
					t.Fatal(err)
				}
			}
			event := lifecycleEvents(t, f, "cli_command.finished", 1)[0]
			if event.Attributes["status"] != "canceled" || event.Outcome != "failure" || event.Actor != command.Actor {
				t.Fatal("late success replaced authoritative cancellation")
			}
		})
	}
}

func TestLifecycleReceiptAppendAndDeletionAreAtomic(t *testing.T) {
	for _, dialect := range []string{"postgres"} {
		t.Run(dialect, func(t *testing.T) {
			f := newAuditFixture(t, dialect)
			journal := f.store.(operational.RevocationTransitions)
			const id, namespace = "hashed-identity", "tokens"
			if err := journal.QueueRevocation(t.Context(), id, "encrypted-credential", namespace); err != nil {
				t.Fatal(err)
			}
			// A successful external revoke has already happened. The receipt
			// replacement must succeed even while audit INSERTs fail.
			wait, release := f.gate(t, true)
			if changed, err := journal.TransitionRevocation(t.Context(), id, "encrypted-credential", "credential-free-receipt", namespace, nil); err != nil || !changed {
				t.Fatal("audit availability blocked credential removal", err)
			}
			events := []operational.AuditEvent{{Event: "token.revoked", Outcome: "success", Actor: "octocat", Attributes: map[string]string{"hashed_token": id}}}
			done := make(chan error, 1)
			go func() {
				_, err := journal.TransitionRevocation(t.Context(), id, "credential-free-receipt", "", namespace, events)
				done <- err
			}()
			wait()
			release()
			if err := <-done; err == nil {
				t.Fatal("receipt acknowledgement ignored audit failure")
			}
			record, err := journal.PendingRevocation(t.Context(), namespace)
			if err != nil || record.Value != "credential-free-receipt" {
				t.Fatal("audit failure lost receipt or restored credential")
			}
			lifecycleEvents(t, f, "token.revoked", 0)
			f.clearGate(t)
			for replay := range 3 {
				changed, err := journal.TransitionRevocation(t.Context(), id, record.Value, "", namespace, events)
				if err != nil || changed != (replay == 0) {
					t.Fatal("receipt replay changed committed semantics", err)
				}
			}
			lifecycleEvents(t, f, "token.revoked", 1)
			if record, err := journal.PendingRevocation(t.Context(), namespace); err != nil || record.ID != "" {
				t.Fatal("committed receipt remains pending", err)
			}
		})
	}
}
