package main

import "testing"

func TestValidateRoleOptions(t *testing.T) {
	production := []string{"role", "policy", "listen", "tls-cert", "tls-key"}
	localOnly := []string{"repository", "namespace", "events", "poll-interval", "once", "api-url"}
	tests := []struct {
		name  string
		role  string
		flags []string
		ok    bool
	}{
		{"no flags", "receive", nil, true},
		{"receive production options", "receive", production, true},
		{"dispatch production options", "dispatch", production, true},
		{"receive rejects workflow", "receive", []string{"role", "workflow"}, false},
		{"dispatch rejects workflow", "dispatch", []string{"role", "workflow"}, false},
		{"discover role and workflows", "discover", []string{"role", "workflow"}, true},
		{"discover rejects policy", "discover", []string{"role", "workflow", "policy"}, false},
		{"discover rejects once", "discover", []string{"role", "workflow", "once"}, false},
		{"local options", "local", append([]string{"role"}, localOnly...), true},
		{"local rejects workflow", "local", []string{"role", "workflow"}, false},
	}
	for _, name := range []string{"policy", "listen", "tls-cert", "tls-key"} {
		tests = append(tests, struct {
			name  string
			role  string
			flags []string
			ok    bool
		}{"local rejects " + name, "local", []string{"role", name}, false})
	}
	for _, name := range localOnly {
		for _, role := range []string{"receive", "dispatch"} {
			tests = append(tests, struct {
				name  string
				role  string
				flags []string
				ok    bool
			}{role + " rejects " + name, role, []string{"role", name}, false})
		}
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateRoleOptions(test.role, test.flags)
			if (err == nil) != test.ok {
				t.Fatalf("validateRoleOptions(%q, %v) = %v, want ok=%t", test.role, test.flags, err, test.ok)
			}
			if err != nil && err.Error() != "gateway discovery, local simulation, and production options cannot be combined" {
				t.Fatalf("unexpected rejection message %q", err)
			}
		})
	}
}

func TestRunRejectsIncompatibleOptionsBeforeInfrastructure(t *testing.T) {
	for _, name := range []string{"CAO_GATEWAY_RECEIVE_KEY_FILE", "CAO_GATEWAY_DISPATCH_KEY_FILE",
		"CAO_GATEWAY_WEBHOOK_SECRET", "CAO_GATEWAY_PREVIOUS_WEBHOOK_SECRET"} {
		t.Setenv(name, "")
	}
	for _, args := range [][]string{
		{"--role=receive", "--once"},
		{"--role=dispatch", "--namespace=ns"},
		{"--role=local", "--listen=127.0.0.1:1"},
		{"--role=receive", "--workflow=/tmp/a.lock.yml"},
	} {
		err := run(t.Context(), args)
		if err == nil || err.Error() != "gateway discovery, local simulation, and production options cannot be combined" {
			t.Fatalf("run(%v) = %v, want option-combination rejection", args, err)
		}
	}
}
