package collect

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/githubquota"
	"github.com/githubnext/gh-cao/server/internal/mining"
	"github.com/githubnext/gh-cao/server/internal/model"
	"github.com/githubnext/gh-cao/server/internal/postgresx"
)

type miningClient struct {
	requests int
	fail     bool
}

func (c *miningClient) QuotaRateLimit(context.Context, int64) (githubquota.ResponseQuota, error) {
	c.requests++
	return miningQuota(), nil
}

func miningQuota() githubquota.ResponseQuota {
	return githubquota.ResponseQuota{HasResponse: true, HasObservation: true, Resource: githubquota.ResourceCore,
		Observation: githubquota.Observation{Remaining: 4999, ResetAt: time.Now().Add(time.Hour)}}
}

func (c *miningClient) ReadOutcome(_ context.Context, _ int64, target mining.Target) (mining.Outcome, githubquota.ResponseQuota, error) {
	c.requests++
	if c.fail {
		return mining.Outcome{}, miningQuota(), fmt.Errorf("object evidence unavailable")
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	return mining.Outcome{Repository: target.Repository, Number: target.Number,
		URL: target.URL, PullRequest: target.PullRequest, State: "closed", StateReason: "completed",
		UpdatedAt: now, ObservedAt: now, Source: "github-api"}, miningQuota(), nil
}

func seedMiningTargets(t *testing.T, store *postgresx.Store) {
	t.Helper()
	writer, err := store.BeginIngestion(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Abort(t.Context())
	for _, record := range []struct {
		source string
		row    model.Row
	}{
		{"$repositories", model.Row{"id": "repo", "owner": "octo", "name": "api"}},
		{"$workflows", model.Row{"id": "workflow", "repositoryId": "repo", "path": ".github/workflows/test.md"}},
		{"$runs", model.Row{"id": "run", "repositoryId": "repo", "workflowId": "workflow"}},
	} {
		if err := writer.Append(t.Context(), record.source, record.row); err != nil {
			t.Fatal(err)
		}
	}
	for n := 100; n < 111; n++ {
		if err := writer.Append(t.Context(), "$issues", model.Row{
			"id": fmt.Sprint(n), "runId": "run", "isPullRequest": false, "githubEntityType": "issue",
			"safeOutputType": "create_issue",
			"correlationId":  fmt.Sprintf("https://github.com/octo/api/issues/%d", n),
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := writer.Publish(t.Context(), "mining-input"); err != nil {
		t.Fatal(err)
	}
}

func TestOutcomeBackfillHasHardSharedBudgetAndResumes(t *testing.T) {
	data := integrationPostgres(t, t.Context())
	seedMiningTargets(t, data)
	w := refreshWorker(t)
	if err := w.Enrollment.AddRepositories(t.Context(), 7, []string{"octo/api"}); err != nil {
		t.Fatal(err)
	}

	client := &miningClient{}
	quota := &fakeRunQuota{}
	p := w.Projector
	p.Data, p.Enrollment, p.OutcomeClient = data, w.Enrollment, client
	p.Quota, p.QuotaApp = quota, "app-test"
	if err := p.CollectOutcomeEvidence(t.Context()); err != nil {
		t.Fatal(err)
	}
	if client.requests != 10 || len(quota.commits) != 9 || len(quota.observations) != 1 {
		t.Fatalf("budget excludes initialization or exceeded limit: requests=%d commits=%d", client.requests, len(quota.commits))
	}
	packet, _, err := readMiningPacket(p.Lake.miningPath("octo/api"))
	if err != nil || len(packet.Outcomes) != 9 {
		t.Fatalf("bounded evidence = %d, %v", len(packet.Outcomes), err)
	}
	if err := p.CollectOutcomeEvidence(t.Context()); err != nil || client.requests != 10 {
		t.Fatal("replica cadence did not prevent a second budget")
	}
	payload, err := p.State.OperationalState(t.Context(), miningBackfillKey)
	if err != nil {
		t.Fatal(err)
	}
	var state miningBackfillState
	if err := json.Unmarshal(payload, &state); err != nil {
		t.Fatal(err)
	}
	if state.Requests != 10 || state.Observed != 9 || state.After != "https://github.com/octo/api/issues/108" {
		t.Fatalf("inaccurate repair checkpoint: %+v", state)
	}
	state.AttemptedAt = time.Now().Add(-time.Hour)
	payload, err = json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.State.SetOperationalState(t.Context(), miningBackfillKey, payload); err != nil {
		t.Fatal(err)
	}
	if err := p.CollectOutcomeEvidence(t.Context()); err != nil {
		t.Fatal(err)
	}
	packet, _, err = readMiningPacket(p.Lake.miningPath("octo/api"))
	if err != nil || len(packet.Outcomes) != 11 || client.requests != 12 {
		t.Fatalf("resumed evidence=%d requests=%d err=%v", len(packet.Outcomes), client.requests, err)
	}
}

func TestOutcomeBackfillFailuresNeverFabricateEvidence(t *testing.T) {
	data := integrationPostgres(t, t.Context())
	seedMiningTargets(t, data)
	w := refreshWorker(t)
	if err := w.Enrollment.AddRepositories(t.Context(), 7, []string{"octo/api"}); err != nil {
		t.Fatal(err)
	}
	client := &miningClient{fail: true}
	p := w.Projector
	p.Data, p.Enrollment, p.OutcomeClient = data, w.Enrollment, client
	p.Quota, p.QuotaApp = &fakeRunQuota{}, "app-test"
	if err := p.CollectOutcomeEvidence(t.Context()); err == nil {
		t.Fatal("unavailable object evidence was reported as successful")
	}
	if client.requests != 10 {
		t.Fatalf("failed reads violated request cap: %d", client.requests)
	}
	if _, err := os.Stat(p.Lake.miningPath("octo/api")); !os.IsNotExist(err) {
		t.Fatalf("failed reads fabricated retained evidence: %v", err)
	}
	payload, err := p.State.OperationalState(t.Context(), miningBackfillKey)
	if err != nil {
		t.Fatal(err)
	}
	var state miningBackfillState
	if err := json.Unmarshal(payload, &state); err != nil {
		t.Fatal(err)
	}
	if state.Failures != 9 || state.Observed != 0 || state.Status != "partial" {
		t.Fatalf("failed reads did not retain incomplete coverage: %+v", state)
	}
}
