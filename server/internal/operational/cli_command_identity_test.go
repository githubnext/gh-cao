package operational_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/githubnext/gh-cao/server/internal/operational"
)

func TestCLICommandIdentityRoundTripAndLegacyRetry(t *testing.T) {
	for _, dialect := range []string{"postgres"} {
		t.Run(dialect, func(t *testing.T) {
			f := newAuditFixture(t, dialect)
			lease := acquireAuditLease(t, f)
			command, _, err := f.queue.EnqueueAudited(t.Context(), auditInvocation, "identity", 0, admissionEvent(0))
			if err != nil || command.ActorID != auditInvocation.ActorID {
				t.Fatalf("enqueue lost identity: %+v, %v", command, err)
			}
			changed := auditInvocation
			changed.ActorID++
			if _, _, err := f.queue.EnqueueAudited(t.Context(), changed, "identity", 0, admissionEvent(0)); !errors.Is(err, operational.ErrConflict) {
				t.Fatalf("same login/different ID reused idempotency key: %v", err)
			}
			page, err := f.executor.List(t.Context(), operational.CLICommandFilter{Limit: 10})
			if err != nil || len(page.Commands) != 1 || page.Commands[0].ActorID != auditInvocation.ActorID {
				t.Fatalf("list lost identity: %+v, %v", page, err)
			}
			history, err := f.executor.ListBefore(t.Context(), operational.CLICommandCursorFilter{Limit: 10})
			if err != nil || len(history.Commands) != 1 || history.Commands[0].ActorID != auditInvocation.ActorID {
				t.Fatalf("history lost identity: %+v, %v", history, err)
			}

			// Restore the exact pre-upgrade JSON shape, without inferring an ID
			// from a mutable login or changing schema/namespace versions.
			legacy := auditInvocation
			legacy.ActorID = 0
			data, err := json.Marshal(legacy)
			if err != nil {
				t.Fatal(err)
			}
			f.exec(t, `UPDATE cao_operational_cli_commands SET invocation=$1 WHERE namespace=$2 AND id=$3`, data, f.namespace, command.ID)
			detail, err := f.executor.Detail(t.Context(), command.ID)
			if err != nil || detail.ActorID != 0 || detail.Actor != legacy.Actor || detail.Status != operational.CLICommandQueued {
				t.Fatalf("legacy record must remain readable: %+v, %v", detail, err)
			}
			claimed, err := f.executor.Claim(t.Context(), lease)
			if err != nil || claimed == nil || claimed.ActorID != 0 || claimed.ID != command.ID {
				t.Fatalf("legacy record must remain claimable for explicit executor failure: %+v, %v", claimed, err)
			}
			if err := f.executor.Finish(t.Context(), lease, command.ID, operational.CLICommandResult{
				Status: operational.CLICommandFailed, Error: "The queued command has no verified requester account ID.",
			}); err != nil {
				t.Fatal(err)
			}
			if err := f.executor.Settle(t.Context(), lease, command.ID, nil); err != nil {
				t.Fatal(err)
			}
			retry, _, err := f.queue.EnqueueAudited(t.Context(), changed, "new-admission", command.ID, admissionEvent(command.ID))
			if err != nil || retry.ActorID != changed.ActorID || retry.RetryOf != command.ID {
				t.Fatalf("legacy retry must bind current admitted ID: %+v, %v", retry, err)
			}
			original, err := f.queue.Detail(t.Context(), command.ID)
			if err != nil || original.ActorID != 0 || original.Status != operational.CLICommandFailed || original.Error == "" || original.ExecutionClaimed {
				t.Fatalf("legacy original must retain explicit failure and missing identity: %+v, %v", original, err)
			}
			claimed, err = f.executor.Claim(t.Context(), lease)
			if err != nil || claimed == nil || claimed.ID != retry.ID || claimed.ActorID != changed.ActorID {
				t.Fatalf("new admission lost identity at claim: %+v, %v", claimed, err)
			}
		})
	}
}
