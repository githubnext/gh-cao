package operational_test

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/operational"
)

var auditInvocation = operational.CLICommandInvocation{
	ActionID: "compile", Command: "gh aw compile --strict", Actor: "octocat", ActorID: 101, Repository: "octo/repo",
}

func admissionEvent(retryOf int64) operational.AuditEvent {
	name := "cli_command.admitted"
	if retryOf != 0 {
		name = "cli_command.retried"
	}
	return operational.AuditEvent{Event: name, Outcome: "success", Actor: "octocat",
		Attributes: map[string]string{"assurance": `{"twoFactor":true}`, "authenticated_at": "2026-10-17T00:00:00Z"}}
}

type auditedResult struct {
	command operational.CLICommand
	event   operational.AuditEvent
	err     error
}

func acquireAuditLease(t *testing.T, f *auditFixture) operational.CLICommandLease {
	t.Helper()
	lease, err := f.executor.Acquire(t.Context(), "executor", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	return lease
}

func failedAuditCommand(t *testing.T, f *auditFixture, lease operational.CLICommandLease) operational.CLICommand {
	t.Helper()
	command, _, err := f.queue.EnqueueAudited(t.Context(), auditInvocation, "original", 0, admissionEvent(0))
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := f.executor.Claim(t.Context(), lease)
	if err != nil || claimed == nil || claimed.ID != command.ID {
		t.Fatalf("original claim = %+v, %v", claimed, err)
	}
	if err := f.executor.Finish(t.Context(), lease, command.ID, operational.CLICommandResult{Status: operational.CLICommandFailed}); err != nil {
		t.Fatal(err)
	}
	if err := f.executor.Settle(t.Context(), lease, command.ID, nil); err != nil {
		t.Fatal(err)
	}
	return command
}

func TestAuditedRetryRecordsNewRequesterWithoutChangingAction(t *testing.T) {
	for _, dialect := range []string{"postgres"} {
		t.Run(dialect, func(t *testing.T) {
			f := newAuditFixture(t, dialect)
			original := failedAuditCommand(t, f, acquireAuditLease(t, f))
			invocation := original.CLICommandInvocation
			invocation.Actor = "mona"
			invocation.ActorID = 202
			event := admissionEvent(original.ID)
			event.Actor = invocation.Actor
			retried, recorded, err := f.queue.EnqueueAudited(t.Context(), invocation, "new-requester", original.ID, event)
			if err != nil || retried.Actor != invocation.Actor || retried.ActorID != invocation.ActorID || recorded.Actor != invocation.Actor || retried.RetryOf != original.ID {
				t.Fatalf("new requester retry = %+v, %+v, %v", retried, recorded, err)
			}
			retained, err := f.queue.Detail(t.Context(), original.ID)
			if err != nil || retained.Actor != original.Actor || retained.ActorID != original.ActorID {
				t.Fatal("retry changed the original requester")
			}
			replayed, _, err := f.queue.EnqueueAudited(t.Context(), invocation, "new-requester", original.ID, event)
			if err != nil || replayed.ID != retried.ID {
				t.Fatalf("retry replay = %+v, %v", replayed, err)
			}
			otherActor := invocation
			otherActor.ActorID++
			if _, _, err := f.queue.EnqueueAudited(t.Context(), otherActor, "new-requester", original.ID, event); !errors.Is(err, operational.ErrConflict) {
				t.Fatalf("same login with different account ID reused an idempotency key: %v", err)
			}
			otherActor.Actor = "hubot"
			otherEvent := event
			otherEvent.Actor = otherActor.Actor
			if _, _, err := f.queue.EnqueueAudited(t.Context(), otherActor, "new-requester", original.ID, otherEvent); !errors.Is(err, operational.ErrConflict) {
				t.Fatalf("different requester reused an idempotency key: %v", err)
			}
			for _, change := range []func(*operational.CLICommandInvocation){
				func(value *operational.CLICommandInvocation) { value.Command = "gh aw logs" },
				func(value *operational.CLICommandInvocation) { value.Repository = "octo/other" },
				func(value *operational.CLICommandInvocation) { value.ActionID = "other" },
				func(value *operational.CLICommandInvocation) { value.Arguments = map[string]bool{"argument": true} },
				func(value *operational.CLICommandInvocation) { value.Values = map[string]string{"value": "changed"} },
				func(value *operational.CLICommandInvocation) { input := "changed"; value.Input = &input },
			} {
				changed := invocation
				change(&changed)
				if _, _, err := f.queue.EnqueueAudited(t.Context(), changed, "", original.ID, event); !errors.Is(err, operational.ErrConflict) {
					t.Fatalf("changed action was retried: %v", err)
				}
			}
			if _, err := f.store.VerifyAudit(t.Context()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestAuditedAdmissionCannotBeClaimedBeforeAuditCommit(t *testing.T) {
	for _, dialect := range []string{"postgres"} {
		for _, retry := range []bool{false, true} {
			for _, fail := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/retry=%t/fail=%t", dialect, retry, fail), func(t *testing.T) {
					f := newAuditFixture(t, dialect)
					lease := acquireAuditLease(t, f)
					var retryOf int64
					if retry {
						retryOf = failedAuditCommand(t, f, lease).ID
					}
					initialCommands, initialEvents := f.counts(t)
					wait, release := f.gate(t, fail)
					result := make(chan auditedResult, 1)
					go func() {
						command, event, err := f.queue.EnqueueAudited(t.Context(), auditInvocation, "request", retryOf, admissionEvent(retryOf))
						result <- auditedResult{command, event, err}
					}()
					wait()
					if commands, events := f.counts(t); commands != initialCommands || events != initialEvents {
						t.Fatalf("uncommitted mutation visible: commands=%d events=%d", commands, events)
					}
					claimed := make(chan *operational.CLICommand, 1)
					claimErr := make(chan error, 1)
					go func() {
						command, err := f.executor.Claim(t.Context(), lease)
						claimErr <- err
						claimed <- command
					}()
					select {
					case command := <-claimed:
						t.Fatalf("claim crossed the uncommitted admission: %+v", command)
					case <-time.After(30 * time.Millisecond):
					}
					release()
					admitted := <-result
					command, err := <-claimed, <-claimErr
					if err != nil {
						t.Fatal(err)
					}
					if fail {
						if admitted.err == nil || admitted.command.ID != 0 || admitted.event.Sequence != 0 || command != nil {
							t.Fatalf("failed audit produced runnable work: admission=%+v claim=%+v", admitted, command)
						}
						if commands, events := f.counts(t); commands != initialCommands || events != initialEvents {
							t.Fatalf("rollback split queue/audit: commands=%d events=%d", commands, events)
						}
					} else if admitted.err != nil || command == nil || command.ID != admitted.command.ID {
						t.Fatalf("committed admission = %+v, claim = %+v", admitted, command)
					}
					f.clearGate(t)
					// Retry the same request after rollback or a lost success response.
					replayed, event, err := f.queue.EnqueueAudited(t.Context(), auditInvocation, "request", retryOf, admissionEvent(retryOf))
					if err != nil || (!fail && replayed.ID != admitted.command.ID) || replayed.RetryOf != retryOf {
						t.Fatalf("idempotent replay = %+v, %v", replayed, err)
					}
					if event.Attributes["command_id"] != strconv.FormatInt(replayed.ID, 10) || event.Attributes["assurance"] == "" {
						t.Fatalf("unbound admission event = %+v", event)
					}
					if commands, _ := f.counts(t); commands != initialCommands+1 {
						t.Fatalf("replay duplicated command: %d", commands)
					}
					if _, err := f.store.VerifyAudit(t.Context()); err != nil {
						t.Fatal(err)
					}
				})
			}
		}
	}
}

func TestAuditedAdmissionConcurrentIdempotency(t *testing.T) {
	for _, dialect := range []string{"postgres"} {
		t.Run(dialect, func(t *testing.T) {
			f := newAuditFixture(t, dialect)
			var wg sync.WaitGroup
			results := make(chan auditedResult, 8)
			for i := range 8 {
				wg.Go(func() {
					queue := f.queue
					if i%2 != 0 {
						queue = f.executor
					}
					command, event, err := queue.EnqueueAudited(t.Context(), auditInvocation, "request", 0, admissionEvent(0))
					results <- auditedResult{command, event, err}
				})
			}
			wg.Wait()
			close(results)
			var id int64
			for result := range results {
				if result.err != nil || (id != 0 && id != result.command.ID) {
					t.Fatalf("idempotency result = %+v (expected %d)", result, id)
				}
				id = result.command.ID
			}
			if commands, events := f.counts(t); commands != 1 || events != 8 {
				t.Fatalf("commands=%d events=%d", commands, events)
			}
			other := auditInvocation
			other.Command = "gh aw logs"
			if _, _, err := f.queue.EnqueueAudited(t.Context(), other, "request", 0, admissionEvent(0)); !errors.Is(err, operational.ErrConflict) {
				t.Fatalf("different invocation reused key: %v", err)
			}
			if _, _, err := f.queue.EnqueueAudited(t.Context(), auditInvocation, "request", id, admissionEvent(id)); !errors.Is(err, operational.ErrConflict) {
				t.Fatalf("different retry ancestry reused key: %v", err)
			}
			if _, err := f.store.VerifyAudit(t.Context()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestAuditedCancelRollsBackAndRetainsExecutionClaim(t *testing.T) {
	for _, dialect := range []string{"postgres"} {
		t.Run(dialect, func(t *testing.T) {
			f := newAuditFixture(t, dialect)
			lease := acquireAuditLease(t, f)
			command, _, err := f.queue.EnqueueAudited(t.Context(), auditInvocation, "request", 0, admissionEvent(0))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.executor.Claim(t.Context(), lease); err != nil {
				t.Fatal(err)
			}
			event := operational.AuditEvent{Event: "cli_command.cancel_requested", Outcome: "success", Actor: "octocat"}
			wait, release := f.gate(t, true)
			done := make(chan error, 1)
			go func() {
				_, _, err := f.queue.CancelAudited(t.Context(), command.ID, event)
				done <- err
			}()
			wait()
			release()
			if err := <-done; err == nil {
				t.Fatal("cancel ignored audit failure")
			}
			current, err := f.queue.Detail(t.Context(), command.ID)
			if err != nil || current.Status != operational.CLICommandRunning || !current.ExecutionClaimed {
				t.Fatalf("cancel escaped rollback: %+v, %v", current, err)
			}
			f.clearGate(t)
			for range 2 {
				canceled, entry, err := f.queue.CancelAudited(t.Context(), command.ID, event)
				if err != nil || canceled.Status != operational.CLICommandCanceled || !canceled.ExecutionClaimed || entry.Attributes["status"] != "canceled" {
					t.Fatalf("audited cancel = %+v, %+v, %v", canceled, entry, err)
				}
			}
			if commands, events := f.counts(t); commands != 1 || events != 3 {
				t.Fatalf("commands=%d events=%d", commands, events)
			}
			if _, err := f.store.VerifyAudit(t.Context()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestAuditedReplayFailureDoesNotCancelCommittedAdmission(t *testing.T) {
	for _, dialect := range []string{"postgres"} {
		t.Run(dialect, func(t *testing.T) {
			f := newAuditFixture(t, dialect)
			lease := acquireAuditLease(t, f)
			original, _, err := f.queue.EnqueueAudited(t.Context(), auditInvocation, "request", 0, admissionEvent(0))
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			if _, _, err := f.queue.EnqueueAudited(ctx, auditInvocation, "request", 0, admissionEvent(0)); err == nil {
				t.Fatal("canceled caller confirmed admission")
			}
			command, err := f.executor.Claim(t.Context(), lease)
			if err != nil || command == nil || command.ID != original.ID {
				t.Fatalf("replay failure canceled committed work: %+v, %v", command, err)
			}
			if commands, events := f.counts(t); commands != 1 || events != 1 {
				t.Fatalf("commands=%d events=%d", commands, events)
			}
		})
	}
}
