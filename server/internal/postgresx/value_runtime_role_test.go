package postgresx

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func valueRuntimeRoleStore(t *testing.T, owner *Store, config *pgx.ConnConfig) *Store {
	t.Helper()
	if config.Host != "127.0.0.1" && config.Host != "::1" {
		t.Skip("runtime-role tests require an isolated numeric-loopback PostgreSQL administrator")
	}
	role := fmt.Sprintf("cao_value_runtime_%d", time.Now().UnixNano())
	secret := make([]byte, 24)
	if _, err := rand.Read(secret); err != nil {
		t.Fatal(err)
	}
	password := hex.EncodeToString(secret)
	identifier := pgx.Identifier{role}.Sanitize()
	if _, err := owner.db.ExecContext(t.Context(), "CREATE ROLE "+identifier+
		" LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS PASSWORD '"+password+"'"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if _, err := owner.db.ExecContext(ctx, "DROP OWNED BY "+identifier+"; DROP ROLE "+identifier); err != nil {
			t.Error("remove isolated value runtime role")
		}
	})
	var schema, database string
	if err := owner.db.QueryRowContext(t.Context(), "SELECT current_schema(),current_database()").Scan(&schema, &database); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile("../../postgres/canonical-grants.sql")
	if err != nil {
		t.Fatal(err)
	}
	grants := strings.ReplaceAll(string(content), `:"schema"`, pgx.Identifier{schema}.Sanitize())
	grants = strings.ReplaceAll(grants, `:"runtime"`, identifier)
	if _, err := owner.db.ExecContext(t.Context(), grants); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.db.ExecContext(t.Context(), "GRANT TEMPORARY ON DATABASE "+pgx.Identifier{database}.Sanitize()+" TO "+identifier); err != nil {
		t.Fatal(err)
	}
	runtimeConfig := config.Copy()
	runtimeConfig.User, runtimeConfig.Password = role, password
	runtime, err := openConfig(t.Context(), runtimeConfig, false, "value-runtime")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	var current string
	var create, updateContract, deleteState, ownsPacket bool
	if err := runtime.db.QueryRowContext(t.Context(), `SELECT current_user,
		has_schema_privilege(current_user,current_schema(),'CREATE'),
		has_table_privilege(current_user,'cao_contract','UPDATE'),
		has_table_privilege(current_user,'cao_state','DELETE'),
		(SELECT pg_get_userbyid(relowner)=current_user FROM pg_class WHERE oid='operational_value_evidence'::regclass)`).
		Scan(&current, &create, &updateContract, &deleteState, &ownsPacket); err != nil {
		t.Fatal(err)
	}
	if current != role || create || updateContract || deleteState || ownsPacket {
		t.Fatal("runtime publication test requires a real non-owner role without schema or bootstrap authority")
	}
	requiredPrivileges := map[string][]string{
		"cao_value_publications":           {"SELECT", "INSERT", "UPDATE"},
		"operational_value_evidence":       {"SELECT", "INSERT", "DELETE"},
		"operational_value_evidence_facts": {"SELECT", "INSERT", "DELETE"},
		"cao_sample_cao_quality":           {"SELECT", "INSERT", "DELETE"},
	}
	for _, table := range statisticalTables {
		requiredPrivileges[table.name] = []string{"SELECT", "INSERT", "DELETE"}
	}
	for table, required := range requiredPrivileges {
		for _, privilege := range required {
			var allowed bool
			if err := runtime.db.QueryRowContext(t.Context(), `SELECT has_table_privilege(current_user,$1,$2)`,
				table, privilege).Scan(&allowed); err != nil || !allowed {
				t.Fatalf("production runtime grant missing %s on %s: %v", privilege, table, err)
			}
		}
		forbidden := []string{"TRUNCATE", "TRIGGER", "REFERENCES"}
		if strings.HasPrefix(table, "cao_sample_") {
			forbidden = append(forbidden, "UPDATE")
		}
		for _, privilege := range forbidden {
			var allowed bool
			if err := runtime.db.QueryRowContext(t.Context(), `SELECT has_table_privilege(current_user,$1,$2)`,
				table, privilege).Scan(&allowed); err != nil || allowed {
				t.Fatalf("runtime must not receive %s on %s: %v", privilege, table, err)
			}
		}
	}
	return runtime
}

func TestValuePublicationRuntimeRole(t *testing.T) {
	owner, config := nativeTestStore(t)
	store := valueRuntimeRoleStore(t, owner, config)
	packet := evidencePacket(t)
	seedPublicationParents(t, store, packet)
	published, err := store.PublishOperationalValue(t.Context(), packet)
	if err != nil || !published.Updated {
		t.Fatalf("production runtime grants cannot publish typed value evidence: %+v %v", published, err)
	}
	read, err := store.ReadOperationalValue(t.Context(), "octo/example", "verify-bugs")
	if err != nil || read["id"] != packet["id"] {
		t.Fatalf("production runtime grants cannot read typed value evidence: %v", err)
	}
	next := recurringPublication(t)
	updated, err := store.PublishOperationalValue(t.Context(), next)
	if err != nil || !updated.Updated || updated.Revision != published.Revision+1 {
		t.Fatalf("runtime cannot atomically replace a measured packet and facts: %+v %v", updated, err)
	}
	writer, err := store.BeginIngestion(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Abort(t.Context())
	if _, err := writer.Publish(t.Context(), "runtime-replacement"); err != nil {
		t.Fatalf("production runtime grants cannot preserve publications across snapshot replacement: %v", err)
	}
	read, err = store.ReadOperationalValue(t.Context(), "octo/example", "verify-bugs")
	if err != nil || read["observedAt"] != next["observedAt"] ||
		len(read["facts"].([]any)) != len(next["facts"].([]any)) {
		t.Fatalf("runtime snapshot replacement lost publication history: %v", err)
	}
	retry, err := store.PublishOperationalValue(t.Context(), next)
	if err != nil || retry.Updated {
		t.Fatalf("runtime canonical retry failed: %+v %v", retry, err)
	}
}
