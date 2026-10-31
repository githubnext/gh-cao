package tokenbroker

import (
	"os"
	"strings"
	"testing"
)

func TestPreviouslyAuthorizedCleanupSurvivesPolicyRevocation(t *testing.T) {
	for _, change := range []string{"remove-policy", "empty-enrollment", "rotate-sha"} {
		t.Run(change, func(t *testing.T) {
			f := newFixture(t)
			if w := f.call(exchangeBody); w.Code != 200 {
				t.Fatal("initial authorized exchange failed")
			}
			switch change {
			case "remove-policy":
				if err := os.Remove(f.policy); err != nil {
					t.Fatal(err)
				}
			case "empty-enrollment":
				if err := os.WriteFile(f.policy, []byte(`{"control-plane":{"token-broker":{"copilot-requests":{"workflows":[]}}}}`), 0600); err != nil {
					t.Fatal(err)
				}
			case "rotate-sha":
				current := f.identity
				current.WorkflowSHA = strings.Repeat("b", 40)
				f.writePolicy(t, current)
			}
			if w := f.call(exchangeBody); w.Code != 403 || f.mints != 1 {
				t.Fatal("revoked enrollment still permitted issuance")
			}
			job := f.identity.CheckRunID
			f.identity.CheckRunID = "8002"
			if w := f.call(invalidateBody); w.Code != 403 || len(f.revoked) != 0 {
				t.Fatal("another untracked job bypassed current policy")
			}
			f.identity.CheckRunID = job
			f.identity.EnvironmentPresent = true
			if w := f.call(invalidateBody); w.Code != 403 || len(f.revoked) != 0 {
				t.Fatal("unsupported signed profile bypassed cleanup authorization")
			}
			f.identity.EnvironmentPresent = false
			subject := f.identity.Subject
			f.identity.Subject = "repo:acme@99/project@1001:ref:refs/heads/main"
			if w := f.call(invalidateBody); w.Code != 403 || len(f.revoked) != 0 {
				t.Fatal("mismatched signed subject bypassed cleanup authorization")
			}
			f.identity.Subject = subject
			if w := f.call(invalidateBody); w.Code != 204 || len(f.revoked) != 1 {
				t.Fatal("policy change prevented exact previously authorized cleanup")
			}
			if w := f.call(invalidateBody); w.Code != 204 || len(f.revoked) != 1 {
				t.Fatal("previously authorized cleanup was not idempotent")
			}
			if w := f.call(exchangeBody); w.Code != 403 || f.mints != 1 {
				t.Fatal("cleanup restored revoked issuance authority")
			}
		})
	}
}

func TestCleanupRetrySurvivesPolicyRemoval(t *testing.T) {
	f := newFixture(t)
	if w := f.call(exchangeBody); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if err := os.Remove(f.policy); err != nil {
		t.Fatal(err)
	}
	f.revokeFails = true
	if w := f.call(invalidateBody); w.Code != 503 || len(f.service.contexts[executionKey(f.identity)].tokens) != 1 {
		t.Fatal("failed cleanup lost a revoked-policy credential")
	}
	f.revokeFails = false
	if w := f.call(invalidateBody); w.Code != 204 || len(f.revoked) != 1 {
		t.Fatal("revoked-policy cleanup could not be retried")
	}
}
