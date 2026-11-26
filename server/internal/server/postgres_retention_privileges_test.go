package server

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/githubnext/gh-cao/server/internal/operational"
	"github.com/githubnext/gh-cao/server/internal/operational/postgres"
	"github.com/githubnext/gh-cao/server/internal/postgresx"
)

func TestPostgresRetentionPrivilegeBoundary(t *testing.T) {
	f := newPostgresRoles(t)
	ctx := t.Context()
	table := pgx.Identifier{f.schema, "cao_operational_audit_events"}.Sanitize()
	prev := operational.AuditGenesis()
	tx, err := f.admin.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	// Seed valid historical records without manipulating a live deployment or
	// weakening triggers. These INSERTs are the exact canonical append format.
	for i := 1; i <= 515; i++ {
		event := operational.AuditEvent{Sequence: int64(i), OccurredAt: time.Now().UTC().Add(-operational.AuditRetention - time.Hour).Truncate(time.Millisecond),
			Event: "test.retention", Outcome: "success"}
		record, err := operational.CanonicalAuditRecord(event)
		if err != nil {
			t.Fatal(err)
		}
		hash := operational.AuditChainHash(prev, record)
		if _, err := tx.ExecContext(ctx, "INSERT INTO "+table+ // #nosec G202 -- The schema/table identifier is quoted by pgx; all values are bound.
			"(namespace,sequence,occurred_at,event,outcome,record,prev_hash,hash) VALUES($1,$2,$3,$4,$5,$6,$7,$8)",
			f.namespace, event.Sequence, event.OccurredAt, event.Event, event.Outcome, string(record), prev, hash); err != nil {
			t.Fatal(err)
		}
		prev = hash
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	runtime, err := postgres.Open(ctx, f.runtimeDSN, f.namespace, postgres.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = runtime.Close() }()
	if err := runtime.Maintain(ctx); err != nil {
		t.Fatal(err)
	}
	if v, err := runtime.VerifyAudit(ctx); err != nil || v.Events != 515 {
		t.Fatalf("ordinary Maintain changed audit retention: %+v %v", v, err)
	}
	retention, err := postgres.Open(ctx, f.retentionDSN, f.namespace, postgres.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = retention.Close() }()
	for _, want := range []int64{512, 2, 0} {
		if removed, err := retention.RetainAudit(ctx); err != nil || removed != want {
			t.Fatalf("bounded retention: removed=%d want=%d err=%v", removed, want, err)
		}
	}
	if v, err := runtime.VerifyAudit(ctx); err != nil || v.Events != 1 || v.FirstSequence != 515 {
		t.Fatalf("retained suffix: %+v %v", v, err)
	}
	event, err := runtime.AppendAudit(ctx, operational.AuditEvent{Event: "test.after_retention", Outcome: "success"})
	if err != nil || event.Sequence != 516 {
		t.Fatalf("sequence restarted after retention: %+v %v", event, err)
	}
	if removed, err := retention.RetainAudit(ctx); err != nil || removed != 1 {
		t.Fatalf("aged predecessor retention: %d %v", removed, err)
	}
	if removed, err := retention.RetainAudit(ctx); err != nil || removed != 0 {
		t.Fatalf("retention removed unexpired event: %d %v", removed, err)
	}
	db := roleDB(t, f.retentionDSN)
	for _, statement := range []string{
		`UPDATE cao_operational_audit_events SET actor='changed'`,
		`DELETE FROM cao_operational_audit_events`,
		`TRUNCATE cao_operational_audit_events`,
		`DELETE FROM cao_operational_namespaces`,
		`SELECT * FROM cao_operational_records`,
		`ALTER TABLE cao_operational_audit_events DISABLE TRIGGER ALL`,
		`DROP FUNCTION cao_operational_retain_audit(TEXT)`,
	} {
		assertSQLDenied(t, db, statement)
	}
	// A caller-owned temporary relation must not shadow the qualified tables
	// in the SECURITY DEFINER operation.
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.ExecContext(ctx, `CREATE TEMP TABLE cao_operational_audit_events (payload text)`); err != nil {
		t.Fatal(err)
	}
	var removed int64
	if err := conn.QueryRowContext(ctx, `SELECT cao_operational_retain_audit($1)`, f.namespace).Scan(&removed); err != nil || removed != 0 {
		t.Fatalf("temporary object affected retention: %d %v", removed, err)
	}
	if _, err := conn.ExecContext(ctx, `SELECT cao_operational_retain_audit('missing')`); err == nil {
		t.Fatal("missing namespace accepted")
	}
	if _, err := conn.ExecContext(ctx, `SELECT cao_operational_retain_audit('deployment', now())`); err == nil {
		t.Fatal("caller supplied retention cutoff")
	}
	owner, err := postgres.Open(ctx, f.ownerDSN, f.namespace, postgres.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = owner.Close() }()
	if err := owner.DeleteNamespace(ctx); err != nil {
		t.Fatalf("explicit owner destruction failed: %v", err)
	}
}

func TestPostgresProvisioningRequiredBeforeRuntime(t *testing.T) {
	f := newPostgresRoles(t)
	ctx := t.Context()
	// Simulate the prior version marker on an isolated fixture, leaving the
	// stored capacity bytes and audit history intact for the upgrade.
	namespaceTable := pgx.Identifier{f.schema, "cao_operational_namespaces"}.Sanitize()
	if _, err := f.admin.ExecContext(ctx, "ALTER TABLE "+namespaceTable+
		" DROP CONSTRAINT cao_operational_namespaces_version_check; UPDATE "+namespaceTable+" SET version=1"); err != nil {
		t.Fatal(err)
	}
	if s, err := postgres.Open(ctx, f.runtimeDSN, f.namespace, postgres.Config{}); err == nil {
		_ = s.Close()
		t.Fatal("runtime accepted an unupgraded operational contract")
	}
	owner, err := postgres.New(ctx, f.ownerDSN, f.namespace, postgres.Config{})
	if err != nil {
		t.Fatal(err)
	}
	_ = owner.Close()
	runtime, err := postgres.Open(ctx, f.runtimeDSN, f.namespace, postgres.Config{})
	if err != nil {
		t.Fatal(err)
	}
	_ = runtime.Close()
	if s, err := postgres.Open(ctx, f.runtimeDSN, "not-provisioned", postgres.Config{}); err == nil {
		_ = s.Close()
		t.Fatal("runtime created a namespace")
	}
	// Missing an upcoming partition is detected before serving, not when
	// ingestion first crosses that day. Owner maintenance repairs the window.
	future := time.Now().UTC().AddDate(0, 0, 1)
	partition := pgx.Identifier{f.schema, "runs_d" + future.Format("20060102")}.Sanitize()
	if _, err := f.admin.ExecContext(ctx, "DROP TABLE "+partition+" CASCADE"); err != nil {
		t.Fatal(err)
	}
	if s, err := postgresx.OpenWithNamespace(ctx, f.runtimeDSN, f.namespace); err == nil {
		_ = s.Close()
		t.Fatal("runtime accepted an incomplete partition window")
	}
	canonical, err := postgresx.NewWithNamespace(ctx, f.ownerDSN, f.namespace)
	if err != nil {
		t.Fatal(err)
	}
	_ = canonical.Close()
	canonical, err = postgresx.OpenWithNamespace(ctx, f.runtimeDSN, f.namespace)
	if err != nil {
		t.Fatal(err)
	}
	_ = canonical.Close()
}
