package postgresx

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/mining"
	"github.com/githubnext/gh-cao/server/internal/model"
	"github.com/githubnext/gh-cao/server/internal/sessionexport"
)

func TestMiningExportExactRunAttemptAndBoundedSnapshot(t *testing.T) {
	store, _ := nativeTestStore(t)
	ctx := t.Context()
	repository := "repository:" + model.EncodeCoordinate("octo/api")
	runID := "github:run:octo/api:42"
	now := time.Now().UTC().Truncate(time.Microsecond)
	member := mining.Membership{Repository: "octo/api", RunID: "42", Attempt: 2, ObservedAt: now, Context: mining.Context{EpisodeID: "episode", HopID: "hop"}}
	outcome := mining.Outcome{Repository: "octo/target", Number: 12, PullRequest: true, URL: "https://github.com/octo/target/pull/12",
		State: "closed", Merged: true, UpdatedAt: now, ObservedAt: now, Source: "github-api"}
	seed := func(attempt int, audits []model.Row) {
		t.Helper()
		nativeSeed(ctx, t, store, map[string][]model.Row{
			"$repositories": {{"id": repository, "owner": "octo", "name": "api"}},
			"$workflows":    {{"id": "exact-workflow", "repositoryId": repository, "path": ".github/workflows/work.md"}},
			"$runs":         {{"id": runID, "repositoryId": repository, "workflowId": "exact-workflow", "attempt": attempt, "owner": "octo", "repository": "api", "githubRunId": "42"}},
			"$issues": {{"id": "output", "runId": runID, "safeOutputType": "create_pull_request", "correlationId": outcome.URL, "isPullRequest": true},
				{"id": "edit", "runId": runID, "safeOutputType": "update_pull_request", "correlationId": outcome.URL}},
			"$audits": audits,
		})
	}
	audits := append(member.Audits(), outcome.Audit("output", runID))
	seed(2, audits)
	snapshot, err := store.ReadMining(ctx, "octo/api", "42", 2)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Namespace != store.namespace || snapshot.Revision < 1 || len(snapshot.Audits) != 2 || len(snapshot.Outputs) != 1 ||
		snapshot.Run["attempt"] != json.Number("2") || snapshot.Audits[1]["payloadRef"] != outcome.URL {
		t.Fatalf("exact single-revision mining snapshot: %#v", snapshot)
	}
	for _, request := range []struct {
		repo, run string
		attempt   int64
	}{{"octo/api", "42", 1}, {"octo/api", "43", 2}, {"octo/other", "42", 2}} {
		result, err := store.ReadMining(ctx, request.repo, request.run, request.attempt)
		if err != nil || result.Run != nil || len(result.Audits) != 0 || len(result.Outputs) != 0 {
			t.Fatalf("missing/superseded source leaked: %#v %v", result, err)
		}
	}
	for _, run := range []string{"0", "042", "42 OR true", "-1"} {
		if _, err := store.ReadMining(ctx, "octo/api", run, 2); err == nil {
			t.Fatal("non-exact run ID accepted")
		}
	}
	seed(3, audits)
	result, err := store.ReadMining(ctx, "octo/api", "42", 2)
	if err != nil || result.Run != nil {
		t.Fatal("superseded attempt exported")
	}
	large := outcome.Audit("output", runID)
	large["payloadRef"] = strings.Repeat("x", sessionexport.MaxBytes+1)
	seed(2, []model.Row{large})
	if _, err := store.ReadMining(ctx, "octo/api", "42", 2); !errors.Is(err, sessionexport.ErrTooLarge) {
		t.Fatalf("source read was not bounded: %v", err)
	}
}
