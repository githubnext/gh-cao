package main

import (
	"context"
	"net/url"
	"strings"
	"testing"
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
