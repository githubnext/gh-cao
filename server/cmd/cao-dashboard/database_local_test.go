package main

import (
	"context"
	"net/url"
	"strings"
	"testing"
)

const testLocalPassword = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestParseLocalAdminEndpoint(t *testing.T) {
	const valid = "postgres://cao_bootstrap:secret@127.0.0.1:5432/cao_dashboard?sslmode=disable"
	cases := []struct {
		name      string
		endpoint  string
		password  string
		namespace string
		want      localProvisionRejection
	}{
		{"valid ipv4 loopback", valid, testLocalPassword, "local", ""},
		{"valid ipv6 loopback", "postgres://cao_bootstrap@[::1]:5432/cao_dashboard", testLocalPassword, "local", ""},
		{"empty endpoint", "", testLocalPassword, "local", rejectEndpoint},
		{"malformed endpoint", "postgres://%zz", testLocalPassword, "local", rejectEndpoint},
		{"missing user", "postgres://127.0.0.1/cao_dashboard", testLocalPassword, "local", rejectEndpoint},
		{"wrong user", "postgres://postgres@127.0.0.1/cao_dashboard", testLocalPassword, "local", rejectEndpoint},
		{"wrong database", "postgres://cao_bootstrap@127.0.0.1/other", testLocalPassword, "local", rejectEndpoint},
		{"hostname instead of ip", "postgres://cao_bootstrap@localhost/cao_dashboard", testLocalPassword, "local", rejectEndpoint},
		{"non loopback", "postgres://cao_bootstrap@10.0.0.5/cao_dashboard", testLocalPassword, "local", rejectEndpoint},
		{"wrong scheme", "postgresql://cao_bootstrap@127.0.0.1/cao_dashboard", testLocalPassword, "local", rejectEndpoint},
		{"short password", valid, "abc", "local", rejectPassword},
		{"uppercase password", valid, strings.ToUpper(testLocalPassword), "local", rejectPassword},
		{"empty namespace", valid, testLocalPassword, "", rejectNamespace},
		{"blank namespace", valid, testLocalPassword, " \t", rejectNamespace},
		{"endpoint checked before password", "", "abc", "", rejectEndpoint},
		{"password checked before namespace", valid, "abc", "", rejectPassword},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			address, got := parseLocalAdminEndpoint(tc.endpoint, tc.password, tc.namespace)
			if got != tc.want {
				t.Fatalf("rejection = %q, want %q", got, tc.want)
			}
			if (address == nil) != (tc.want != "") {
				t.Fatalf("address = %v with rejection %q", address, got)
			}
		})
	}
}

func TestLocalOwnerURLAssumesOwnerWithoutMutatingAdminAddress(t *testing.T) {
	address, err := url.Parse("postgres://cao_bootstrap:secret@127.0.0.1:5432/cao_dashboard?sslmode=disable&role=other")
	if err != nil {
		t.Fatal(err)
	}
	before := address.String()
	owner, err := url.Parse(localOwnerURL(address))
	if err != nil {
		t.Fatal(err)
	}
	query := owner.Query()
	if query.Get("role") != "cao_owner" || query.Get("search_path") != "cao" || query.Get("sslmode") != "disable" {
		t.Fatalf("owner query = %v", query)
	}
	if owner.Host != address.Host || owner.Path != address.Path || owner.User.String() != address.User.String() {
		t.Fatalf("owner url changed connection identity: %s", owner.Redacted())
	}
	if address.String() != before {
		t.Fatalf("admin address mutated: %s", address.Redacted())
	}
}

func TestProvisionLocalDatabaseRejectsInvalidInputsBeforeConnecting(t *testing.T) {
	const want = "managed PostgreSQL provisioning requires its local administrator, database, password and namespace"
	err := provisionLocalDatabase(context.Background(), "postgres://cao_bootstrap@10.0.0.5/cao_dashboard", testLocalPassword, "local")
	if err == nil || err.Error() != want {
		t.Fatalf("error = %v, want %q", err, want)
	}
	err = provisionLocalDatabase(context.Background(), "postgres://cao_bootstrap@127.0.0.1/cao_dashboard", "short", "local")
	if err == nil || err.Error() != want {
		t.Fatalf("error = %v, want %q", err, want)
	}
}
