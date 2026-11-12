package collect

import (
	"fmt"
	"testing"
)

func TestPushWorkflowPathsAndDefaultBranch(t *testing.T) {
	for _, tc := range []struct {
		name, ref, branch, changes string
		deleted                    bool
		want                       IntentKind
	}{
		{"added", "refs/heads/trunk", "trunk", `"commits":[{"added":[".github/workflows/build.lock.yml"]}]`, false, IntentWorkflowRefresh},
		{"modified", "refs/heads/trunk", "trunk", `"commits":[{"modified":[".github/workflows/build.lock.yml"]}]`, false, IntentWorkflowRefresh},
		{"removed", "refs/heads/trunk", "trunk", `"commits":[{"removed":[".github/workflows/build.lock.yml"]}]`, false, IntentWorkflowRefresh},
		{"other branch", "refs/heads/main", "trunk", `"commits":[{"added":[".github/workflows/build.lock.yml"]}]`, false, IntentIgnore},
		{"tag", "refs/tags/trunk", "trunk", `"commits":[{"added":[".github/workflows/build.lock.yml"]}]`, false, IntentIgnore},
		{"no default", "refs/heads/main", "", `"commits":[{"added":[".github/workflows/build.lock.yml"]}]`, false, IntentIgnore},
		{"deleted branch", "refs/heads/trunk", "trunk", `"commits":[{"removed":[".github/workflows/build.lock.yml"]}]`, true, IntentIgnore},
		{"other files", "refs/heads/trunk", "trunk", `"commits":[{"modified":["README.md",".github/workflows-other/build.md"]}]`, false, IntentValueRefresh},
		{"markdown source", "refs/heads/trunk", "trunk", `"commits":[{"modified":[".github/workflows/build.md"]}]`, false, IntentValueRefresh},
		{"yaml extension", "refs/heads/trunk", "trunk", `"commits":[{"modified":[".github/workflows/build.yaml"]}]`, false, IntentValueRefresh},
		{"ordinary workflow", "refs/heads/trunk", "trunk", `"commits":[{"modified":[".github/workflows/build.yml"]}]`, false, IntentValueRefresh},
		{"nested workflow", "refs/heads/trunk", "trunk", `"commits":[{"modified":[".github/workflows/shared/build.lock.yml"]}]`, false, IntentValueRefresh},
		{"truncated", "refs/heads/trunk", "trunk", `"size":3,"commits":[{"modified":["README.md"]}]`, false, IntentWorkflowRefresh},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payload := fmt.Sprintf(`{"ref":%q,"deleted":%t,"installation":{"id":7},"repository":{"full_name":"Octo/API","default_branch":%q},%s}`, tc.ref, tc.deleted, tc.branch, tc.changes)
			intent, err := ParseEvent("push", []byte(payload))
			if err != nil || intent.Kind != tc.want {
				t.Fatalf("classification = %+v, %v; want %s", intent, err, tc.want)
			}
			if intent.Kind != IntentIgnore && (intent.Repository != "octo/api" || !intent.ValueRefresh) {
				t.Fatalf("repository was not normalized: %+v", intent)
			}
		})
	}
}

func refreshLease(t *testing.T, queue Queue) Lease {
	t.Helper()
	leases, err := queue.Lease(t.Context(), "refresh-test", 1, 0)
	if err != nil || len(leases) != 1 {
		t.Fatalf("lease = %+v, %v", leases, err)
	}
	return leases[0]
}
