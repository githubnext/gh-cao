package server

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/audit"
	"github.com/githubnext/gh-cao/server/internal/hostedcli"
	"github.com/githubnext/gh-cao/server/internal/operational"
	"github.com/githubnext/gh-cao/server/internal/operational/postgres"
	"github.com/githubnext/gh-cao/server/internal/testutil"
)

func hostedPostgresFixture(t *testing.T) (*hostedCLIFixture, *postgres.Store, *sql.DB) {
	t.Helper()
	f := newHostedCLIFixture(t, []string{"octocat"})
	store, dsn, _ := testutil.OperationalPostgres(t)
	queue, err := store.OpenCLICommandQueue(t.Context(), hostedcli.QueueName, operational.CLICommandQueueOptions{})
	if err != nil {
		t.Fatal(err)
	}
	f.app.hostedCLI.queue = queue.(operational.AuditedCLICommandQueue)
	f.app.config.Audit = f.app.config.Audit.WithStore(store)
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return f, store, db
}

func TestHostedPostgresAuditFailureCannotAdmitOrCancel(t *testing.T) {
	f, store, db := hostedPostgresFixture(t)
	queue := f.app.hostedCLI.queue
	lease, err := queue.Acquire(t.Context(), "executor", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if response := f.do(t, http.MethodPost, "/__cli_commands", hostedUpgradeRequest, true); response.Code != http.StatusAccepted {
		t.Fatalf("original enqueue = %d: %s", response.Code, response.Body)
	}
	if _, err := db.ExecContext(t.Context(), `CREATE FUNCTION fail_audit() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN RAISE EXCEPTION 'injected audit failure'; END $$;
		CREATE TRIGGER fail_audit BEFORE INSERT ON cao_operational_audit_events FOR EACH ROW
		WHEN (NEW.event IN ('cli_command.admitted','cli_command.retried','cli_command.cancel_requested'))
		EXECUTE FUNCTION fail_audit()`); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/__cli_commands", "/__cli_commands/1/cancel"} {
		body := hostedUpgradeRequest
		if path != "/__cli_commands" {
			body = "{}"
		}
		if response := f.do(t, http.MethodPost, path, body, true); response.Code != http.StatusServiceUnavailable {
			t.Fatalf("audit failure on %s = %d: %s", path, response.Code, response.Body)
		}
	}
	command, err := queue.Claim(t.Context(), lease)
	if err != nil || command == nil || command.ID != 1 {
		t.Fatalf("failed cancel changed original: %+v, %v", command, err)
	}
	if err := queue.Finish(t.Context(), lease, command.ID, operational.CLICommandResult{Status: operational.CLICommandFailed}); err != nil {
		t.Fatal(err)
	}
	if err := queue.Settle(t.Context(), lease, command.ID, nil); err != nil {
		t.Fatal(err)
	}
	if response := f.do(t, http.MethodPost, "/__cli_commands/1/retry", `{}`, true); response.Code != http.StatusServiceUnavailable {
		t.Fatalf("retry audit failure = %d: %s", response.Code, response.Body)
	}
	command, err = queue.Claim(t.Context(), lease)
	if err != nil || command != nil {
		t.Fatalf("unaudited enqueue/retry became runnable: %+v, %v", command, err)
	}
	page, err := queue.List(t.Context(), operational.CLICommandFilter{Limit: 10})
	if err != nil || page.Total != 1 {
		t.Fatalf("audit failures persisted commands: %+v, %v", page, err)
	}
	events, err := store.ListAudit(t.Context(), operational.AuditFilter{})
	if err != nil || len(events) != 3 || events[0].Event != "cli_command.admitted" ||
		events[1].Event != "cli_command.result" || events[2].Event != "cli_command.finished" {
		t.Fatalf("audit failure corrupted the chain: %+v, %v", events, err)
	}
	if _, err := store.VerifyAudit(t.Context()); err != nil {
		t.Fatal(err)
	}
}

// lostAdmissionReply preserves the real durable transaction, then simulates
// losing its commit acknowledgement. The handler cannot infer rollback.
type lostAdmissionReply struct {
	operational.AuditedCLICommandQueue
	lose bool
}

func (q *lostAdmissionReply) EnqueueAudited(ctx context.Context, invocation operational.CLICommandInvocation, key string, retryOf int64, event operational.AuditEvent) (operational.CLICommand, operational.AuditEvent, error) {
	command, stored, err := q.AuditedCLICommandQueue.EnqueueAudited(ctx, invocation, key, retryOf, event)
	if err == nil && q.lose {
		q.lose = false
		return operational.CLICommand{}, operational.AuditEvent{}, context.DeadlineExceeded
	}
	return command, stored, err
}

func TestHostedPostgresUnknownCommitDoesNotCompensate(t *testing.T) {
	f, store, _ := hostedPostgresFixture(t)
	f.app.hostedCLI.queue = &lostAdmissionReply{AuditedCLICommandQueue: f.app.hostedCLI.queue, lose: true}
	body := `{"id":"upgrade-target-repository","values":{"repository":"octo/app"},"idempotencyKey":"lost-reply"}`
	if response := f.do(t, http.MethodPost, "/__cli_commands", body, true); response.Code != http.StatusServiceUnavailable {
		t.Fatalf("unknown commit = %d: %s", response.Code, response.Body)
	}
	queue, err := store.OpenCLICommandQueue(t.Context(), hostedcli.QueueName, operational.CLICommandQueueOptions{})
	if err != nil {
		t.Fatal(err)
	}
	original, err := queue.Detail(t.Context(), 1)
	if err != nil || original.Status != operational.CLICommandQueued {
		t.Fatalf("unknown commit triggered compensation: %+v, %v", original, err)
	}
	if response := f.do(t, http.MethodPost, "/__cli_commands", body, true); response.Code != http.StatusAccepted {
		t.Fatalf("idempotent recovery = %d: %s", response.Code, response.Body)
	}
	if _, err := queue.Detail(t.Context(), 2); !errors.Is(err, operational.ErrCLICommandNotFound) {
		t.Fatalf("lost reply recovery duplicated command: %v", err)
	}
	events, err := store.ListAudit(t.Context(), operational.AuditFilter{})
	if err != nil || len(events) != 2 || events[0].Attributes["command_id"] != "1" || events[1].Attributes["command_id"] != "1" {
		t.Fatalf("lost reply recovery audit = %+v, %v", events, err)
	}
}

func TestHostedCLIRejectsNonAtomicQueue(t *testing.T) {
	f := newHostedCLIFixture(t, []string{"octocat"})
	plain := struct{ operational.CLICommandQueue }{f.queue}
	err := f.app.EnableHostedCLIActions(t.Context(), HostedCLIConfig{
		Catalog: f.app.hostedCLI.catalog, ControlRepository: "octo/cao", Queue: plain,
	})
	if err == nil {
		t.Fatal("hosted admission accepted a queue without atomic audit persistence")
	}
}

type failingHostedMirror struct{}

func (failingHostedMirror) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestHostedPostgresMirrorFailureReportsCommittedCommand(t *testing.T) {
	f, store, _ := hostedPostgresFixture(t)
	f.app.config.Audit = audit.New(failingHostedMirror{}).WithStore(store)
	body := `{"id":"upgrade-target-repository","values":{"repository":"octo/app"},"idempotencyKey":"mirror-failure"}`
	for range 2 {
		response := f.do(t, http.MethodPost, "/__cli_commands", body, true)
		if response.Code != http.StatusServiceUnavailable ||
			!strings.Contains(response.Body.String(), "CLI command 1 is queued") ||
			!strings.Contains(response.Body.String(), "mutation and audit event were stored") {
			t.Fatalf("mirror failure hid committed identity/state: %d %s", response.Code, response.Body)
		}
		command, err := f.app.hostedCLI.queue.Detail(t.Context(), 1)
		if err != nil || command.Status != operational.CLICommandQueued {
			t.Fatalf("mirror failure canceled committed work: %+v, %v", command, err)
		}
		if _, err := f.app.hostedCLI.queue.Detail(t.Context(), 2); !errors.Is(err, operational.ErrCLICommandNotFound) {
			t.Fatalf("mirror failure retry duplicated admission: %v", err)
		}
	}
	f.app.config.Audit = audit.New(io.Discard).WithStore(store)
	if response := f.do(t, http.MethodPost, "/__cli_commands", body, true); response.Code != http.StatusAccepted {
		t.Fatalf("mirror recovery = %d: %s", response.Code, response.Body)
	}
	events, err := store.ListAudit(t.Context(), operational.AuditFilter{})
	if err != nil || len(events) != 3 {
		t.Fatalf("mirror recovery lost durable events: %+v, %v", events, err)
	}
	for _, event := range events {
		if event.Attributes["command_id"] != "1" {
			t.Fatalf("mirror recovery changed command identity: %+v", event)
		}
	}
	if _, err := store.VerifyAudit(t.Context()); err != nil {
		t.Fatal(err)
	}
}
