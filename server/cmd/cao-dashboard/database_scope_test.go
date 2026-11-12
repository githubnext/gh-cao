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
