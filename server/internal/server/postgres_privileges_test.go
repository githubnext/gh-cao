package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/githubnext/gh-cao/server/internal/model"
	"github.com/githubnext/gh-cao/server/internal/operational"
	"github.com/githubnext/gh-cao/server/internal/operational/postgres"
	"github.com/githubnext/gh-cao/server/internal/postgresx"
	"github.com/githubnext/gh-cao/server/internal/query"
	gateway "github.com/githubnext/gh-cao/server/internal/webhookgateway"
	"github.com/githubnext/gh-cao/server/internal/webhookgatewaylocal"
)

func TestPostgresWebhookRuntimePrivilegeBoundary(t *testing.T) {
	f := newPostgresRoles(t)
	store, err := postgres.Open(t.Context(), f.runtimeDSN, f.namespace, postgres.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	rate := gateway.RateBudget{Capacity: 100, Period: time.Hour}
	queue, err := store.OpenWebhookQueue(t.Context(), "webhooks", gateway.QueueOptions{
		MaxRows: 128, MaxBytes: 2 << 20, MaxTenantRows: 128, MaxTenantBytes: 2 << 20,
		MaxSourceRows: 128, MaxSourceBytes: 2 << 20, MaxDestinationRows: 128, MaxDestinationBytes: 2 << 20,
		MaxPayloadBytes: 65536, MaxBatch: 16, MaxConcurrency: 8,
		MaxAge: time.Hour, DedupWindow: time.Hour, TerminalRetention: 2 * time.Hour, LeaseDuration: time.Minute,
		Rates: gateway.RateBudgets{Global: rate, App: rate, Tenant: rate, Destination: rate, Workflow: rate},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := queue.ApplyRevision(t.Context(), gateway.PolicyRevision{
		Revision: 1, Digest: strings.Repeat("a", 64), Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	if job, err := queue.Claim(t.Context(), gateway.ClaimRequest{Owner: "worker", LeaseDuration: time.Minute}); err != nil || job != nil {
		t.Fatalf("empty gateway queue claim: %v %v", job, err)
	}
	runtimeDB, readerDB := roleDB(t, f.runtimeDSN), roleDB(t, f.readerDSN)
	for _, table := range []string{"cao_operational_webhook_queues", "cao_operational_webhook_deliveries", "cao_operational_webhook_jobs"} {
		for _, privilege := range []string{"SELECT", "INSERT", "UPDATE", "DELETE"} {
			var allowed bool
			if err := runtimeDB.QueryRowContext(t.Context(), `SELECT has_table_privilege(current_user,$1,$2)`, table, privilege).Scan(&allowed); err != nil || !allowed {
				t.Fatalf("gateway runtime lacks %s on %s: %v", privilege, table, err)
			}
		}
		qualified := pgx.Identifier{f.schema, table}.Sanitize()
		assertSQLDenied(t, runtimeDB, "ALTER TABLE "+qualified+" ADD COLUMN forbidden int")
		assertSQLDenied(t, readerDB, "SELECT * FROM "+qualified)
	}
}

func roleDB(t *testing.T, dsn string) *sql.DB {
	t.Helper()
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func assertSQLDenied(t *testing.T, db *sql.DB, statement string) {
	t.Helper()
	_, err := db.ExecContext(t.Context(), statement)
	var failure *pgconn.PgError
	if !errors.As(err, &failure) || failure.Code != "42501" {
		t.Fatalf("expected insufficient_privilege for %s, got %v", statement, err)
	}
}

func TestPostgresRuntimePrivilegeBoundary(t *testing.T) {
	f := newPostgresRoles(t)
	ctx := t.Context()
	host := &resolvedHostPolicy{OperationalBackend: "postgres", OperationalPostgresURL: f.runtimeDSN, OperationalNamespace: f.namespace}
	store, _, err := newOperationalStore(ctx, host)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	s, ok := store.(*postgres.Store)
	if !ok {
		t.Fatal("factory did not use PostgreSQL")
	}
	if err := s.PutSession(ctx, "session", "encrypted", time.Hour, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.PutSession(ctx, "session", "refreshed", time.Hour, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendAudit(ctx, operational.AuditEvent{Event: "test.accepted", Outcome: "success"}); err != nil {
		t.Fatal(err)
	}
	queue, err := s.OperationalServices().CLICommands.OpenCLICommandQueue(ctx, "commands", operational.CLICommandQueueOptions{})
	if err != nil {
		t.Fatal(err)
	}
	audited, ok := queue.(operational.AuditedCLICommandQueue)
	if !ok {
		t.Fatal("queue lacks atomic audit mutations")
	}
	command, event, err := audited.EnqueueAudited(ctx, operational.CLICommandInvocation{ActionID: "compile", Command: "gh aw compile"}, "one", 0,
		operational.AuditEvent{Event: "cli_command.admitted", Outcome: "success", Actor: "operator"})
	if err != nil || command.ID == 0 || event.Sequence != 2 {
		t.Fatalf("atomic admission failed: %+v %+v %v", command, event, err)
	}
	if _, _, err := audited.CancelAudited(ctx, command.ID, operational.AuditEvent{Event: "cli_command.cancel_requested", Outcome: "success", Actor: "operator"}); err != nil {
		t.Fatal(err)
	}
	if err := s.EnsureQueue(ctx, "collect", "workers"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.EnqueueTask(ctx, operational.EnqueueRequest{Queue: "collect", Fields: operational.TaskFields{Task: "one"}}); err != nil {
		t.Fatal(err)
	}
	messages, err := s.ReadTasks(ctx, operational.QueueRead{Queue: "collect", Group: "workers", Consumer: "one", Count: 1})
	if err != nil || len(messages) != 1 {
		t.Fatalf("queue read: %v %v", messages, err)
	}
	if err := s.CompleteTask(ctx, "collect", "workers", messages[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Maintain(ctx); err != nil {
		t.Fatalf("runtime maintenance requires elevated privileges: %v", err)
	}
	testWebhookRuntime(t, f)
	health, err := s.Health(ctx)
	if err != nil || !health.Ready || health.Entries < 1 {
		t.Fatalf("protected state accounting: %+v %v", health, err)
	}
	if v, err := s.VerifyAudit(ctx); err != nil || v.Events != 3 {
		t.Fatalf("audit chain: %+v %v", v, err)
	}
	auditTable := pgx.Identifier{f.schema, "cao_operational_audit_events"}.Sanitize()
	// #nosec G202 -- Both identifiers are generated fixture names quoted by pgx.
	if _, err := f.admin.ExecContext(ctx, "REVOKE INSERT ON "+auditTable+" FROM "+pgx.Identifier{f.runtime}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	if _, _, err := audited.EnqueueAudited(ctx, operational.CLICommandInvocation{ActionID: "compile", Command: "gh aw compile"}, "must-rollback", 0,
		operational.AuditEvent{Event: "cli_command.admitted", Outcome: "success", Actor: "operator"}); err == nil {
		t.Fatal("missing audit INSERT privilege did not fail admission")
	}
	// #nosec G202 -- Both identifiers are generated fixture names quoted by pgx.
	if _, err := f.admin.ExecContext(ctx, "GRANT INSERT ON "+auditTable+" TO "+pgx.Identifier{f.runtime}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	if after, err := s.Health(ctx); err != nil || after.Entries != health.Entries {
		t.Fatalf("audit failure leaked protected capacity: before=%+v after=%+v err=%v", health, after, err)
	}
	page, err := queue.List(ctx, operational.CLICommandFilter{Limit: 10})
	if err != nil || page.Total != 1 {
		t.Fatalf("audit failure left admitted work: %+v %v", page, err)
	}
	db := roleDB(t, f.runtimeDSN)
	for _, statement := range []string{
		`UPDATE cao_operational_audit_events SET actor='changed'`,
		`DELETE FROM cao_operational_audit_events`,
		`TRUNCATE cao_operational_audit_events`,
		`DELETE FROM cao_operational_namespaces`,
		`UPDATE cao_operational_namespaces SET namespace='other'`,
		`UPDATE cao_operational_namespaces SET version=1`,
		`ALTER TABLE cao_operational_audit_events DISABLE TRIGGER ALL`,
		`DROP TRIGGER cao_operational_audit_append_only ON cao_operational_audit_events`,
		`DROP TABLE cao_operational_audit_events CASCADE`,
		`CREATE TABLE runtime_ddl (id int)`,
		`SELECT cao_operational_retain_audit('deployment')`,
		`SET session_replication_role = replica`,
		"SET ROLE " + pgx.Identifier{f.owner}.Sanitize(),
	} {
		assertSQLDenied(t, db, statement)
	}
	if _, err := s.RetainAudit(ctx); err == nil {
		t.Fatal("ordinary runtime reached retention authority")
	}
	if err := s.DeleteNamespace(ctx); err == nil {
		t.Fatal("ordinary runtime destroyed its namespace")
	}
	// Even an accidental DELETE grant cannot turn a caller-controlled GUC or
	// the RI trigger's owner identity into authority to erase audit history.
	for _, table := range []string{"cao_operational_audit_events", "cao_operational_namespaces"} {
		qualified := pgx.Identifier{f.schema, table}.Sanitize()
		if _, err := f.admin.ExecContext(ctx, "GRANT DELETE ON "+qualified+" TO "+pgx.Identifier{f.runtime}.Sanitize()); err != nil { // #nosec G202 -- Identifiers are quoted by pgx; only random fixture roles are used.
			t.Fatal(err)
		}
		assertSQLDenied(t, db, "BEGIN; SET LOCAL cao.audit_retention='on'; DELETE FROM "+table)
		if _, err := db.ExecContext(ctx, "ROLLBACK"); err != nil {
			t.Fatal(err)
		}
		if _, err := f.admin.ExecContext(ctx, "REVOKE DELETE ON "+qualified+" FROM "+pgx.Identifier{f.runtime}.Sanitize()); err != nil { // #nosec G202 -- Identifiers are quoted by pgx; only random fixture roles are used.
			t.Fatal(err)
		}
	}
	if changed, err := postgres.New(ctx, f.runtimeDSN, f.namespace, postgres.Config{}); err == nil {
		_ = changed.Close()
		t.Fatal("runtime credentials performed bootstrap DDL")
	}
	testCanonicalRuntime(t, f)
	testAuditReader(t, f)
}

func testWebhookRuntime(t *testing.T, f postgresRoles) {
	t.Helper()
	ctx := t.Context()
	store, err := postgres.Open(ctx, f.runtimeDSN, f.namespace, postgres.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	repository := webhookgatewaylocal.Repository{ID: 42, FullName: "octo/repo"}
	repository.Owner.ID, repository.Owner.Type = 7, "Organization"
	simulation, err := webhookgatewaylocal.NewSimulation(repository, []string{"issues"})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := simulation.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	queue, err := store.OpenWebhookQueue(ctx, "webhooks", policy.Policy.Queue)
	if err != nil {
		t.Fatal(err)
	}
	if err := queue.ApplyRevision(ctx, policy.Revision); err != nil {
		t.Fatal(err)
	}
	payload := []byte(`{"action":"opened","installation":{"id":1},"repository":{"id":42},"organization":{"id":7}}`)
	event, err := gateway.ParseEvent("delivery", "issues", payload, gateway.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	event.SourceID = "local"
	subscription := policy.Subscriptions[0]
	job, err := gateway.NewJob(time.Now().Add(-time.Second), "input", event, subscription, policy.Revisions[subscription.ID].Digest)
	if err != nil {
		t.Fatal(err)
	}
	var request map[string]any
	if err := json.Unmarshal(job.Payload, &request); err != nil {
		t.Fatal(err)
	}
	request["client_payload"].(map[string]any)["_gateway"] = map[string]any{
		"version": "cao.webhook/v1", "sourceId": event.SourceID, "deliveryId": event.DeliveryID,
		"subscriptionId": subscription.ID, "revision": policy.Revisions[subscription.ID],
	}
	job.Payload, err = json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	job.QueueRevision = policy.Revision
	if _, err := queue.AdmitBatch(ctx, gateway.Admission{
		Revision: policy.Revision, DeliveryID: event.DeliveryID, DeliveryDigest: gateway.MetadataDigest(payload),
		Jobs: []gateway.Job{job},
	}); err != nil {
		t.Fatal(err)
	}
	worker := gateway.Worker{Policies: simulation, Queue: queue, Authorizer: simulation, Dispatcher: simulation,
		Owner: "runtime-role", HTTPTimeout: time.Second, LeaseDuration: time.Minute}
	if worked, err := worker.RunOnce(ctx); err != nil || !worked {
		t.Fatal("restricted runtime could not process gateway work", worked, err)
	}
	db := roleDB(t, f.runtimeDSN)
	var state string
	if err := db.QueryRowContext(ctx, `SELECT state FROM cao_operational_webhook_jobs WHERE namespace=$1 AND queue='webhooks'`, f.namespace).Scan(&state); err != nil || state != "succeeded" {
		t.Fatal("restricted runtime did not persist gateway acceptance", state, err)
	}
	next := policy.Revision
	next.Revision++
	next.Digest = strings.Repeat("a", 64)
	if err := queue.ApplyRevision(ctx, next); err != nil {
		t.Fatal(err)
	}
	if _, err := queue.Maintain(ctx, 100); err != nil {
		t.Fatal(err)
	}
	registry := queue.(gateway.RegistryStore)
	bound, err := registry.BindRegistry(ctx, next)
	if err != nil {
		t.Fatal("restricted runtime could not bind subscription registry", err)
	}
	published, err := registry.ReplaceRegistry(ctx, next, bound.Version,
		[]gateway.RepositoryInventory{{RepositoryID: 42, Dirty: true}})
	if err != nil || published.Version <= bound.Version {
		t.Fatal("restricted runtime could not atomically invalidate subscription inventory", err)
	}
}

func testCanonicalRuntime(t *testing.T, f postgresRoles) {
	t.Helper()
	ctx := t.Context()
	store, err := postgresx.OpenWithNamespace(ctx, f.runtimeDSN, f.namespace)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	writer, err := store.BeginIngestion(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Abort(ctx)
	for _, row := range []struct {
		source string
		value  model.Row
	}{
		{"$repositories", model.Row{"id": "repo", "owner": "octo", "name": "repo"}},
		{"$workflows", model.Row{"id": "workflow", "repositoryId": "repo", "path": ".github/workflows/test.md"}},
		{"$runs", model.Row{"id": "run", "repositoryId": "repo", "workflowId": "workflow", "createdAt": time.Now().UTC().Format(time.RFC3339)}},
		{"$audits", model.Row{"id": "audit", "runId": "run", "source": "session", "type": "firewall.event"}},
	} {
		if err := writer.Append(ctx, row.source, row.value); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := writer.Publish(ctx, "restricted-role"); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"repositories", "workflows", "runs", "audits"} {
		var estimate float64
		if err := f.admin.QueryRowContext(ctx, `SELECT reltuples FROM pg_class WHERE oid=to_regclass($1)`,
			pgx.Identifier{f.schema, table}.Sanitize()).Scan(&estimate); err != nil || estimate != 1 {
			t.Fatalf("restricted-role %s statistics were skipped: %v %v", table, estimate, err)
		}
	}
	result, _, err := store.ExecuteSQLPlan(ctx, []query.Definition{{Name: "runs", From: "$runs", Select: []query.SelectedField{{Field: "id"}}}}, []string{"runs"})
	if err != nil || len(result["runs"].Rows) != 1 {
		t.Fatalf("restricted runtime canonical query: %+v %v", result, err)
	}
	if err := store.DeleteNamespace(ctx); err == nil {
		t.Fatal("runtime destroyed canonical namespace")
	}
	if err := store.RunPartitionMaintenance(ctx, time.Now().AddDate(0, 0, 35), 30, 7); err == nil {
		t.Fatal("runtime created future partitions")
	}
}

func testAuditReader(t *testing.T, f postgresRoles) {
	t.Helper()
	policy := filepath.Join(t.TempDir(), "cao.json")
	if err := os.WriteFile(policy, []byte(`{"control-plane":{"web":{"host":{"target":{"module":"container"},"operational-store":{"backend":"postgres"}}}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CAO_POLICY_PATH", policy)
	t.Setenv("CAO_POSTGRES_URL", f.readerDSN)
	t.Setenv("CAO_OPERATIONAL_NAMESPACE", f.namespace)
	reader, closeReader, err := OpenAuditReader(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = closeReader() }()
	events, err := reader.ListAudit(t.Context(), operational.AuditFilter{})
	if err != nil || len(events) != 3 {
		t.Fatalf("SELECT-only export: %v %v", events, err)
	}
	if v, err := reader.VerifyAudit(t.Context()); err != nil || v.Events != 3 {
		t.Fatalf("SELECT-only verification: %+v %v", v, err)
	}
	if _, err := reader.AppendAudit(t.Context(), operational.AuditEvent{Event: "reader.append", Outcome: "success"}); err == nil {
		t.Fatal("read-only role appended audit")
	}
	db := roleDB(t, f.readerDSN)
	assertSQLDenied(t, db, `SELECT cao_operational_retain_audit('deployment')`)
	assertSQLDenied(t, db, `SELECT * FROM cao_operational_records`)
}
