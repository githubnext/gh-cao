package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/operational"
	"github.com/githubnext/gh-cao/server/internal/operational/postgres"
	"github.com/githubnext/gh-cao/server/internal/testutil"
)

func TestDatabaseCommandsRequireExplicitAuthority(t *testing.T) {
	t.Setenv("CAO_DATABASE_ADMIN_URL", "")
	t.Setenv("CAO_POSTGRES_URL", "postgres://runtime@127.0.0.1/runtime")
	for _, test := range []struct {
		args []string
		want string
	}{
		{[]string{"bootstrap"}, "--store"},
		{[]string{"bootstrap", "--store", "operational"}, "--namespace"},
		{[]string{"bootstrap", "--store", "operational", "--namespace", "deployment"}, "CAO_DATABASE_ADMIN_URL"},
		{[]string{"maintain", "--store", "operational", "--namespace", "deployment"}, "maintain requires"},
		{[]string{"retain-audit", "--store", "canonical", "--namespace", "deployment"}, "retain-audit requires"},
	} {
		cmd := newDatabaseCommand()
		cmd.SetOut(io.Discard)
		cmd.SetErr(io.Discard)
		cmd.SetArgs(test.args)
		if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), test.want) {
			t.Fatalf("%v: got %v, want %s", test.args, err, test.want)
		}
	}
}

func runDatabase(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := newRootCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(io.Discard)
	cmd.SetArgs(append([]string{"database"}, args...))
	err := cmd.Execute()
	return out.String(), err
}

func operationalCommandFixture(t *testing.T) (*postgres.Store, string, string) {
	t.Helper()
	store, dsn, namespace := testutil.OperationalPostgres(t)
	policy := filepath.Join(t.TempDir(), "cao.json")
	if err := os.WriteFile(policy, []byte(`{"control-plane":{"web":{"host":{"target":{"module":"container"},"operational-store":{"backend":"postgres"}}}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CAO_POLICY_PATH", policy)
	t.Setenv("CAO_POSTGRES_URL", dsn)
	t.Setenv("CAO_OPERATIONAL_NAMESPACE", namespace)
	return store, dsn, namespace
}

func TestDatabasePostgresLifecycle(t *testing.T) {
	store, dsn, namespace := operationalCommandFixture(t)
	t.Setenv("CAO_DATABASE_ADMIN_URL", dsn)
	seedPostgresAuditHistory(t, dsn, namespace, time.Now().Add(-2*operational.AuditRetention), 3)
	for _, test := range []struct {
		action  string
		removed int64
	}{{"bootstrap", 0}, {"retain-audit", 2}} {
		out, err := runDatabase(t, test.action, "--store", "operational", "--namespace", namespace)
		if err != nil {
			t.Fatal(err)
		}
		var result struct {
			Backend string `json:"backend"`
			Removed int64  `json:"audit_events_removed"`
		}
		if err := json.Unmarshal([]byte(out), &result); err != nil || result.Backend != "postgres" || result.Removed != test.removed {
			t.Fatalf("database result=%q err=%v", out, err)
		}
	}
	if head, err := store.VerifyAudit(t.Context()); err != nil || head.Events != 1 || head.LastSequence != 3 {
		t.Fatalf("explicit retention: %+v, %v", head, err)
	}
	if event, err := store.AppendAudit(t.Context(), operational.AuditEvent{Event: "test.current", Outcome: "success"}); err != nil || event.Sequence != 4 {
		t.Fatalf("append after retention: %+v, %v", event, err)
	}
}

func seedPostgresAuditHistory(t *testing.T, dsn, namespace string, at time.Time, count int) {
	t.Helper()
	at = at.UTC().Truncate(time.Microsecond)
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	prev := operational.AuditGenesis()
	for sequence := 1; sequence <= count; sequence++ {
		event := operational.AuditEvent{Sequence: int64(sequence), OccurredAt: at, Event: "test.accepted", Outcome: "success"}
		record, err := operational.CanonicalAuditRecord(event)
		if err != nil {
			t.Fatal(err)
		}
		hash := operational.AuditChainHash(prev, record)
		if _, err := db.ExecContext(t.Context(), `INSERT INTO cao_operational_audit_events
			(namespace,sequence,occurred_at,event,outcome,actor,record,prev_hash,hash) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
			namespace, event.Sequence, at, event.Event, event.Outcome, event.Actor, string(record), prev, hash); err != nil {
			t.Fatal(err)
		}
		prev = hash
	}
}
