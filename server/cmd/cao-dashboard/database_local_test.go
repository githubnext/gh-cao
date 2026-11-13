package main

import (
	"context"
	"net/url"
	"strings"
	"testing"
)

const (
	validLocalEndpoint = "postgres://cao_bootstrap:secret@127.0.0.1:5432/cao_dashboard?sslmode=disable"
	validLocalPassword = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
)

func TestParseLocalProvisionTargetAcceptsManagedLoopbackEndpoint(t *testing.T) {
	for _, endpoint := range []string{
		validLocalEndpoint,
		"postgres://cao_bootstrap@[::1]:5432/cao_dashboard",
	} {
		address, err := parseLocalProvisionTarget(endpoint, validLocalPassword, "ns")
		if err != nil || address == nil {
			t.Fatalf("%s: got %v, %v", endpoint, address, err)
		}
	}
}

func TestParseLocalProvisionTargetRejectsUnmanagedInputs(t *testing.T) {
	cases := map[string][3]string{
		"unparseable":     {"://bad", validLocalPassword, "ns"},
		"empty endpoint":  {"", validLocalPassword, "ns"},
		"no user":         {"postgres://127.0.0.1/cao_dashboard", validLocalPassword, "ns"},
		"other user":      {"postgres://postgres@127.0.0.1/cao_dashboard", validLocalPassword, "ns"},
		"other database":  {"postgres://cao_bootstrap@127.0.0.1/other", validLocalPassword, "ns"},
		"other scheme":    {"postgresql://cao_bootstrap@127.0.0.1/cao_dashboard", validLocalPassword, "ns"},
		"hostname":        {"postgres://cao_bootstrap@localhost/cao_dashboard", validLocalPassword, "ns"},
		"remote address":  {"postgres://cao_bootstrap@10.0.0.5/cao_dashboard", validLocalPassword, "ns"},
		"short password":  {validLocalEndpoint, "abc", "ns"},
		"upper password":  {validLocalEndpoint, strings.ToUpper(validLocalPassword), "ns"},
		"empty password":  {validLocalEndpoint, "", "ns"},
		"empty namespace": {validLocalEndpoint, validLocalPassword, ""},
		"blank namespace": {validLocalEndpoint, validLocalPassword, " \t"},
	}
	for name, c := range cases {
		address, err := parseLocalProvisionTarget(c[0], c[1], c[2])
		if err == nil || address != nil {
			t.Errorf("%s: got %v, %v; want rejection", name, address, err)
			continue
		}
		if !strings.Contains(err.Error(), "managed PostgreSQL provisioning requires") {
			t.Errorf("%s: unexpected error %q", name, err)
		}
		if strings.Contains(err.Error(), c[1]) && c[1] != "" {
			t.Errorf("%s: error leaks input: %q", name, err)
		}
	}
}

func TestLocalOwnerEndpointScopesRoleAndSchema(t *testing.T) {
	address, err := parseLocalProvisionTarget(validLocalEndpoint+"&role=postgres&search_path=public", validLocalPassword, "ns")
	if err != nil {
		t.Fatal(err)
	}
	original := address.String()
	owner := localOwnerEndpoint(address)
	parsed, err := url.Parse(owner)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	if query.Get("role") != "cao_owner" || query.Get("search_path") != "cao" || query.Get("sslmode") != "disable" {
		t.Fatalf("unexpected owner endpoint query: %v", query)
	}
	if !strings.HasPrefix(owner, "postgres://cao_bootstrap:secret@127.0.0.1:5432/cao_dashboard?") {
		t.Fatalf("owner endpoint lost identity: %q", owner)
	}
	if address.String() != original {
		t.Fatalf("localOwnerEndpoint mutated its input: %q != %q", address.String(), original)
	}
}

func TestProvisionLocalDatabaseRejectsBeforeConnecting(t *testing.T) {
	err := provisionLocalDatabase(context.Background(), "postgres://cao_bootstrap@db.example/cao_dashboard", validLocalPassword, "ns")
	if err == nil || !strings.Contains(err.Error(), "managed PostgreSQL provisioning requires") {
		t.Fatalf("got %v", err)
	}
}
