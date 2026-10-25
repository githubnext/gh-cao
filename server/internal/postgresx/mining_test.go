package postgresx

import (
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/mining"
	"github.com/githubnext/gh-cao/server/internal/model"
)

func TestMiningProjectsOnlyExactOutputsAndSelectedAttempts(t *testing.T) {
	store, _ := nativeTestStore(t)
	ctx := t.Context()
	w, err := store.BeginIngestion(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Abort(ctx)
	nativeParents(ctx, t, w)
	runID := "github:run:octo/api:42"
	if err := w.Append(ctx, "$runs", model.Row{"id": runID, "workflowId": "workflow", "repositoryId": "repository",
		"attempt": 2, "owner": "octo", "repository": "api", "githubRunId": "42"}); err != nil {
		t.Fatal(err)
	}
	if err := w.Append(ctx, "$issues", model.Row{
		"id": "output", "runId": runID, "githubEntityType": "pull_request", "isPullRequest": true,
		"safeOutputType": "create_pull_request",
		"correlationId":  "https://github.com/octo/target/pull/12",
	}); err != nil {
		t.Fatal(err)
	}
	if err := w.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if err := w.Append(ctx, "$issues", model.Row{
		"id": "not-created", "runId": runID, "githubEntityType": "pull_request", "isPullRequest": true,
		"safeOutputType": "update_pull_request", "correlationId": "https://github.com/octo/target/pull/12",
	}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	member := mining.Membership{Repository: "octo/api", RunID: "42", Attempt: 2,
		Context: mining.Context{EpisodeID: "recorded", HopID: "hop", ParentHopID: "parent"}, ObservedAt: now}
	if err := w.AppendEpisode(ctx, member); err != nil {
		t.Fatal(err)
	}
	member.Attempt, member.Context.EpisodeID = 1, "old-attempt"
	if err := w.AppendEpisode(ctx, member); err != nil {
		t.Fatal(err)
	}
	member.RunID, member.Context.EpisodeID = "404", "missing-run"
	if err := w.AppendEpisode(ctx, member); err != nil {
		t.Fatal(err)
	}
	outcome := mining.Outcome{Repository: "octo/target", Number: 12, PullRequest: true,
		URL: "https://github.com/octo/target/pull/12", State: "closed", Merged: true,
		UpdatedAt: now, ObservedAt: now, Source: "github-webhook"}
	if err := w.AppendOutcome(ctx, outcome); err != nil {
		t.Fatal(err)
	}
	outcome.Number, outcome.URL = 13, "https://github.com/octo/target/pull/13"
	if err := w.AppendOutcome(ctx, outcome); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Publish(ctx, "mined"); err != nil {
		t.Fatal(err)
	}
	var memberships, accepted, wrong int
	if err := store.db.QueryRowContext(ctx, `SELECT count(*) FILTER(WHERE type='episode.membership'),
		count(*) FILTER(WHERE type='outcome.observed' AND status='accepted'),
		count(*) FILTER(WHERE correlation_id IN ('old-attempt','missing-run'))
		FROM audits WHERE namespace=$1`, store.namespace).Scan(&memberships, &accepted, &wrong); err != nil {
		t.Fatal(err)
	}
	if memberships != 1 || accepted != 1 || wrong != 0 {
		t.Fatalf("mined counts membership=%d accepted=%d invalid=%d", memberships, accepted, wrong)
	}
	targets, err := store.MiningTargets(ctx, "", 10)
	if err != nil || len(targets) != 1 || targets[0] != "https://github.com/octo/target/pull/12" {
		t.Fatalf("exact repair targets = %v, %v", targets, err)
	}
}
