package admission

import (
	"strings"
	"testing"
)

func TestValidTokenWindow(t *testing.T) {
	const now = int64(1_000_000)
	tests := []struct {
		name          string
		iat, nbf, exp int64
		want          bool
	}{
		{"typical", now - 30, now - 30, now + 300, true},
		{"maximum lifetime", now - 300, now - 300, now + 300, true},
		{"lifetime too long", now - 301, now - 301, now + 300, false},
		{"skew boundary accepted", now + 30, now + 30, now + 60, true},
		{"issued in future", now + 31, now, now + 100, false},
		{"not before in future", now - 30, now + 31, now + 100, false},
		{"issued too long ago", now - 601, now - 601, now + 1, false},
		{"expired", now - 30, now - 30, now, false},
		{"expiry not after issue", now - 10, now - 10, now - 10, false},
		{"not before at expiry", now - 30, now + 20, now + 20, false},
		{"zero issued-at", 0, now - 30, now + 300, false},
		{"zero not-before", now - 30, 0, now + 300, false},
		{"zero expiry", now - 30, now - 30, 0, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := validTokenWindow(test.iat, test.nbf, test.exp, now); got != test.want {
				t.Fatalf("validTokenWindow = %t, want %t", got, test.want)
			}
		})
	}
}

func TestIdentityViolation(t *testing.T) {
	valid := Identity{
		Issuer: Issuer, Subject: "repo:acme/project:ref:refs/heads/main", Repository: "acme/project",
		RepositoryID: "1001", OwnerID: "42", RunID: "1234", RunAttempt: "2", Ref: "refs/heads/main",
		Event: "workflow_dispatch", Actor: "octocat", ActorID: "101",
		WorkflowRef: "acme/project/.github/workflows/triage.lock.yml@refs/heads/main",
		WorkflowSHA: strings.Repeat("a", 40),
	}
	tests := []struct {
		name   string
		mutate func(*Identity)
		want   string
	}{
		{"valid", func(*Identity) {}, ""},
		{"bad repository", func(i *Identity) { i.Repository = "nope" }, "identity_fields"},
		{"empty subject", func(i *Identity) { i.Subject = "" }, "identity_fields"},
		{"empty ref", func(i *Identity) { i.Ref = "" }, "identity_fields"},
		{"empty event", func(i *Identity) { i.Event = "" }, "identity_fields"},
		{"leading zero owner", func(i *Identity) { i.OwnerID = "042" }, "identity_fields"},
		{"non decimal actor id", func(i *Identity) { i.ActorID = "abc" }, "identity_fields"},
		{"oversized run id", func(i *Identity) { i.RunID = strings.Repeat("1", 129) }, "identity_fields"},
		{"short sha", func(i *Identity) { i.WorkflowSHA = "abc" }, "identity_fields"},
		{"workflow from another repository", func(i *Identity) {
			i.WorkflowRef = "other/project/.github/workflows/triage.lock.yml@refs/heads/main"
		}, "workflow_ref"},
		{"workflow outside workflows directory", func(i *Identity) {
			i.WorkflowRef = "acme/project/scripts/triage.yml@refs/heads/main"
		}, "workflow_ref"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			identity := valid
			test.mutate(&identity)
			if got := identityViolation(identity); got != test.want {
				t.Fatalf("identityViolation = %q, want %q", got, test.want)
			}
		})
	}
}
