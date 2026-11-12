package server

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/githubnext/gh-cao/server/internal/operational"
	"github.com/githubnext/gh-cao/server/internal/operational/postgres"
	"github.com/githubnext/gh-cao/server/internal/testutil"
)

func hostedCLIPostgresTestStore(t *testing.T) *postgres.Store {
	t.Helper()
	store, _, _ := testutil.OperationalPostgres(t)
	return store
}

func TestAuditReaderRequiresProvisionedState(t *testing.T) {
	writer, dsn, namespace := testutil.OperationalPostgres(t)
	path := filepath.Join(t.TempDir(), "cao.json")
	if err := os.WriteFile(path, []byte(`{"control-plane":{"web":{"host":{"target":{"module":"container"},"operational-store":{"backend":"postgres"}}}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CAO_POLICY_PATH", path)
	t.Setenv("CAO_POSTGRES_URL", dsn)
	t.Setenv("CAO_OPERATIONAL_NAMESPACE", "missing")
	for _, open := range []func() (operational.AuditLog, func() error, error){
		func() (operational.AuditLog, func() error, error) { return OpenAuditReader(t.Context()) },
		func() (operational.AuditLog, func() error, error) { return OpenAuditStore(t.Context()) },
	} {
		if _, closeStore, err := open(); err == nil {
			_ = closeStore()
			t.Fatal("audit open provisioned missing namespace")
		}
	}
	t.Setenv("CAO_OPERATIONAL_NAMESPACE", namespace)
	event := operational.AuditEvent{Event: "test.accepted", Outcome: "success"}
	if _, err := writer.AppendAudit(t.Context(), event); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CAO_POSTGRES_URL", dsn+"&default_transaction_read_only=on")
	reader, closeReader, err := OpenAuditReader(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = closeReader() }()
	if result, err := reader.VerifyAudit(t.Context()); err != nil || result.Events != 1 {
		t.Fatalf("read-only verify: %+v, %v", result, err)
	}
	if _, err := reader.AppendAudit(t.Context(), event); err == nil {
		t.Fatal("read-only evidence connection has write authority")
	}
	t.Setenv("CAO_POSTGRES_URL", dsn)
	runtime, closeRuntime, err := OpenAuditStore(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = closeRuntime() }()
	if _, err := runtime.AppendAudit(t.Context(), event); err != nil {
		t.Fatalf("writable verifier lost append authority: %v", err)
	}
}
