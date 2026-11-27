package main

import (
	"context"
	"database/sql"
	"net/url"
	"strings"
	"testing"

	grants "github.com/githubnext/gh-cao/server/postgres"
)

const localTestPassword = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestParseLocalProvisionTarget(t *testing.T) {
	const valid = "postgres://cao_bootstrap@127.0.0.1:5432/cao_dashboard?sslmode=disable"
	for _, test := range []struct {
		name, endpoint, password, namespace string
		ok                                  bool
	}{
		{"valid", valid, localTestPassword, "local", true},
		{"valid ipv6 loopback", "postgres://cao_bootstrap@[::1]:5432/cao_dashboard", localTestPassword, "local", true},
		{"no user", "postgres://127.0.0.1/cao_dashboard", localTestPassword, "local", false},
		{"bad scheme", "postgresql://cao_bootstrap@127.0.0.1/cao_dashboard", localTestPassword, "local", false},
		{"bad user", "postgres://admin@127.0.0.1/cao_dashboard", localTestPassword, "local", false},
		{"bad database", "postgres://cao_bootstrap@127.0.0.1/other", localTestPassword, "local", false},
		{"hostname", "postgres://cao_bootstrap@localhost/cao_dashboard", localTestPassword, "local", false},
		{"remote host", "postgres://cao_bootstrap@10.0.0.5/cao_dashboard", localTestPassword, "local", false},
		{"short password", valid, "abc", "local", false},
		{"uppercase password", valid, strings.ToUpper(localTestPassword), "local", false},
		{"blank namespace", valid, localTestPassword, "  ", false},
		{"unparsable", "postgres://cao_bootstrap@127.0.0.1:bad/cao_dashboard", localTestPassword, "local", false},
		{"empty", "", localTestPassword, "local", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			address, err := parseLocalProvisionTarget(test.endpoint, test.password, test.namespace)
			if test.ok {
				if err != nil || address == nil || address.Hostname() == "" {
					t.Fatalf("target = %v, %v; want accepted", address, err)
				}
				return
			}
			if err == nil || address != nil {
				t.Fatalf("target = %v, %v; want rejection", address, err)
			}
			for _, leak := range []string{test.endpoint, test.password} {
				if leak != "" && strings.Contains(err.Error(), leak) {
					t.Fatalf("error %q leaks input", err)
				}
			}
		})
	}
}

func TestLocalOwnerAddress(t *testing.T) {
	admin, err := url.Parse("postgres://cao_bootstrap@127.0.0.1:5432/cao_dashboard?sslmode=disable&role=other")
	if err != nil {
		t.Fatal(err)
	}
	before := admin.String()
	owner, err := url.Parse(localOwnerAddress(admin))
	if err != nil {
		t.Fatal(err)
	}
	query := owner.Query()
	if query.Get("role") != "cao_owner" || query.Get("search_path") != "cao" || query.Get("sslmode") != "disable" {
		t.Fatalf("owner query = %v", query)
	}
	if owner.Host != admin.Host || owner.Path != admin.Path || owner.User.Username() != "cao_bootstrap" {
		t.Fatalf("owner address = %s", owner)
	}
	if admin.String() != before {
		t.Fatalf("administrator address mutated: %s", admin)
	}
}

func TestProvisionLocalDatabaseRejectsInvalidTargetBeforeConnecting(t *testing.T) {
	err := provisionLocalDatabase(context.Background(), "postgres://cao_bootstrap@10.0.0.5/cao_dashboard", localTestPassword, "local")
	if err == nil || !strings.Contains(err.Error(), "requires its local administrator") {
		t.Fatalf("error = %v", err)
	}
}

func TestRenderLocalGrantsBindsEveryRoleVariable(t *testing.T) {
	for name, contract := range map[string]string{"canonical": grants.CanonicalGrants, "operational": grants.OperationalGrants} {
		t.Run(name, func(t *testing.T) {
			if !strings.Contains(contract, `:"schema"`) {
				t.Fatalf("packaged %s contract no longer uses the schema variable", name)
			}
			rendered := renderLocalGrants(contract)
			for _, variable := range []string{`:"schema"`, `:"runtime"`, `:"retention"`, `:"reader"`} {
				if strings.Contains(rendered, variable) {
					t.Fatalf("rendered %s contract still contains %s", name, variable)
				}
			}
			if !strings.Contains(rendered, `"cao"`) || !strings.Contains(rendered, `"cao_runtime"`) {
				t.Fatalf("rendered %s contract does not bind the cao schema and runtime role", name)
			}
		})
	}
	if got := renderLocalGrants("GRANT USAGE ON SCHEMA :\"schema\" TO :\"reader\""); got != `GRANT USAGE ON SCHEMA "cao" TO "cao_evidence"` {
		t.Fatalf("rendered = %q", got)
	}
}

func TestApplyLocalGrantsReportsGenericFailure(t *testing.T) {
	owner, err := sql.Open("pgx", "postgres://cao_owner@127.0.0.1:1/cao_dashboard")
	if err != nil {
		t.Fatal(err)
	}
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
	err = applyLocalGrants(context.Background(), owner)
	if err == nil || err.Error() != "cannot apply managed PostgreSQL runtime privilege contracts" {
		t.Fatalf("error = %v", err)
	}
	if strings.Contains(err.Error(), "127.0.0.1") {
		t.Fatalf("error %q leaks the endpoint", err)
	}
}

func TestApplyLocalGrantsHonorsCanceledContext(t *testing.T) {
	owner, err := sql.Open("pgx", "postgres://cao_owner@127.0.0.1:1/cao_dashboard")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = owner.Close() }()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := applyLocalGrants(ctx, owner); err == nil || strings.Contains(err.Error(), "127.0.0.1") {
		t.Fatalf("error = %v; want a generic failure", err)
	}
}
