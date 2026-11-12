package operational_test

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/operational"
	"github.com/githubnext/gh-cao/server/internal/operational/postgres"
	"github.com/githubnext/gh-cao/server/internal/testutil"
)

type auditFixture struct {
	queue, executor operational.AuditedCLICommandQueue
	store           operational.AuditLog
	db              *sql.DB
	namespace       string
	gateKey         int32
	releaseOnce     sync.Once
}

func newAuditFixture(t *testing.T, _ string) *auditFixture {
	t.Helper()
	first, dsn, namespace := testutil.OperationalPostgres(t)
	second, err := postgres.Open(t.Context(), dsn, namespace, postgres.Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Close() })
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	f := &auditFixture{store: first, db: db, namespace: namespace}
	options := operational.CLICommandQueueOptions{MaxConcurrency: 1}
	for source, target := range map[operational.CLICommandQueues]*operational.AuditedCLICommandQueue{first: &f.queue, second: &f.executor} {
		queue, err := source.OpenCLICommandQueue(t.Context(), "commands", options)
		if err != nil {
			t.Fatal(err)
		}
		*target = queue.(operational.AuditedCLICommandQueue)
	}
	return f
}

func (f *auditFixture) exec(t *testing.T, statement string, args ...any) {
	t.Helper()
	if _, err := f.db.ExecContext(t.Context(), statement, args...); err != nil {
		t.Fatal(err)
	}
}

// gate blocks inside the audit INSERT after mutation but before commit.
func (f *auditFixture) gate(t *testing.T, fail bool) (wait, release func()) {
	t.Helper()
	lock, err := f.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	key := int32(time.Now().UnixNano() % 2147483647)
	f.gateKey = key
	if _, err := lock.ExecContext(t.Context(), `SELECT pg_advisory_xact_lock(9132,$1)`, key); err != nil {
		_ = lock.Rollback()
		t.Fatal(err)
	}
	release = func() { f.releaseOnce.Do(func() { _ = lock.Rollback() }) }
	raise := ""
	if fail {
		raise = `RAISE EXCEPTION 'injected audit failure';`
	}
	f.exec(t, fmt.Sprintf(`CREATE FUNCTION audit_test_gate() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN PERFORM pg_advisory_xact_lock(9132,%d); %s RETURN NEW; END $$;
		CREATE TRIGGER audit_test_gate BEFORE INSERT ON cao_operational_audit_events FOR EACH ROW EXECUTE FUNCTION audit_test_gate()`,
		key, raise))
	t.Cleanup(func() { release(); f.clearGate(t) })
	wait = func() {
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			var waiting bool
			if err := f.db.QueryRowContext(t.Context(), `SELECT EXISTS(SELECT 1 FROM pg_locks WHERE locktype='advisory' AND classid=9132 AND objid=$1 AND NOT granted)`, key).Scan(&waiting); err != nil {
				t.Fatal(err)
			}
			if waiting {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatal("admission did not reach the audit INSERT")
	}
	return wait, release
}

func (f *auditFixture) clearGate(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := f.db.ExecContext(ctx, `DROP TRIGGER IF EXISTS audit_test_gate ON cao_operational_audit_events; DROP FUNCTION IF EXISTS audit_test_gate()`); err != nil {
		t.Error(err)
	}
}

func (f *auditFixture) counts(t *testing.T) (commands, events int) {
	t.Helper()
	for table, target := range map[string]*int{"cao_operational_cli_commands": &commands, "cao_operational_audit_events": &events} {
		if err := f.db.QueryRowContext(t.Context(), "SELECT count(*) FROM "+table+" WHERE namespace=$1", f.namespace).Scan(target); err != nil {
			t.Fatal(err)
		}
	}
	return commands, events
}
