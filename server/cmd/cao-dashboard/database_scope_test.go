package main

import (
	"strings"
	"testing"
)

func TestDatabaseScopeValidate(t *testing.T) {
	tests := []struct {
		name    string
		scope   databaseScope
		backend string
		wantErr string
	}{
		{"invalid store", databaseScope{action: "bootstrap", store: "other"}, "", "--store must be"},
		{"empty store", databaseScope{action: "bootstrap"}, "", "--store must be"},
		{"postgres canonical maintain", databaseScope{action: "maintain", store: "canonical", namespace: "ns"}, "postgres", ""},
		{"postgres operational retain", databaseScope{action: "retain-audit", store: "operational", namespace: "ns"}, "postgres", ""},
		{"postgres missing namespace", databaseScope{action: "bootstrap", store: "canonical", namespace: "  "}, "", "--namespace is required"},
		{"maintain operational", databaseScope{action: "maintain", store: "operational", namespace: "ns"}, "", "maintain requires canonical"},
		{"retain canonical", databaseScope{action: "retain-audit", store: "canonical", namespace: "ns"}, "", "retain-audit requires operational"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			backend, err := tt.scope.validate()
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("validate() error = %v, want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil || backend != tt.backend {
				t.Fatalf("validate() = %q, %v; want %q", backend, err, tt.backend)
			}
		})
	}
}

func TestAdminDatabaseURL(t *testing.T) {
	env := func(value string) func(string) string {
		return func(key string) string {
			if key != "CAO_DATABASE_ADMIN_URL" {
				t.Fatalf("read unexpected variable %q", key)
			}
			return value
		}
	}
	got, err := adminDatabaseURL(env("  postgres://admin@127.0.0.1/cao  \n"))
	if err != nil || got != "postgres://admin@127.0.0.1/cao" {
		t.Fatalf("padded endpoint = %q, %v", got, err)
	}
	for _, value := range []string{"", " \t\n"} {
		got, err := adminDatabaseURL(env(value))
		if err == nil || !strings.Contains(err.Error(), "CAO_DATABASE_ADMIN_URL is required") || got != "" {
			t.Fatalf("endpoint %q = %q, %v; want required error", value, got, err)
		}
	}
}
