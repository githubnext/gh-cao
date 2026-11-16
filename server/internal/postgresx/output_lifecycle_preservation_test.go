package postgresx

import (
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/model"
	"github.com/githubnext/gh-cao/server/internal/query"
)

func TestOutputLifecycleSurvivesSnapshotReplacementAndWeakerReplay(t *testing.T) {
	store, _ := nativeTestStore(t)
	at := time.Now().UTC().Truncate(time.Second)
	repository := model.Row{"id": "repository:octo%2Fapi", "owner": "octo", "name": "api", "observedAt": at}
	workflow := model.Row{
		"id":           "workflow:octo%2Fapi%3A.github%2Fworkflows%2Freview.md",
		"repositoryId": repository["id"], "path": ".github/workflows/review.md", "observedAt": at,
	}
	run := model.Row{
		"id": "github:run:octo/api:42", "repositoryId": repository["id"], "workflowId": workflow["id"],
		"owner": "octo", "repository": "api", "githubRunId": "42", "attempt": 1,
		"startedAt": at, "observedAt": at,
	}
	base := model.Row{
		"id": "output:1", "repositoryId": repository["id"], "workflowId": workflow["id"], "runId": run["id"],
		"safeOutputType": "create_issue", "targetRepository": "octo/api",
		"targetUrl": "https://github.com/octo/api/issues/7", "title": "Review finding",
		"deliveryState": "delivered", "deliverySignal": "safe_output.result",
		"deliveredAt": at, "deliveryObservedAt": at, "deliverySource": "safe-output",
		"deliveryEvidenceId": "event:1", "ruleVersion": "gh-aw-v1.2.3-adoption-v1",
		"survivalState": "unknown", "observedAt": at,
	}
	write := func(revision string, lifecycle model.Row) {
		t.Helper()
		writer, err := store.BeginIngestion(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		defer writer.Abort(t.Context())
		for source, row := range map[string]model.Row{
			"$repositories": repository, "$workflows": workflow, "$runs": run,
		} {
			if err := writer.Append(t.Context(), source, row); err != nil {
				t.Fatal(err)
			}
		}
		if lifecycle != nil {
			if err := writer.Append(t.Context(), "$outputLifecycles", lifecycle); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := writer.Publish(t.Context(), revision); err != nil {
			t.Fatal(err)
		}
	}
	accepted := model.Row{}
	for key, value := range base {
		accepted[key] = value
	}
	accepted["adoptionState"], accepted["adoptionSignal"] = "accepted", "completed"
	accepted["adoptionAt"], accepted["adoptionObservedAt"] = at.Add(time.Hour), at.Add(time.Hour)
	accepted["adoptionSource"] = "github-api"
	accepted["survivalState"], accepted["survivalSignal"] = "observed", "resource_still_accepted"
	accepted["survivalObservedAt"] = at.Add(time.Hour)
	write("accepted", accepted)

	replayed := model.Row{}
	for key, value := range base {
		replayed[key] = value
	}
	replayed["adoptionState"], replayed["adoptionSignal"] = "pending", "human_decision_pending"
	replayed["adoptionObservedAt"], replayed["adoptionSource"] = at.Add(2*time.Hour), "adoption-registry"
	replayed["observedAt"] = at.Add(2 * time.Hour)
	write("weaker-replay", replayed)

	results, _, err := store.ExecuteSQLPlan(t.Context(), []query.Definition{
		{Name: "lifecycles", From: "$outputLifecycles"},
	}, []string{"lifecycles"})
	if err != nil {
		t.Fatal(err)
	}
	rows := results["lifecycles"].Rows
	if len(rows) != 1 || rows[0]["adoptionState"] != "accepted" ||
		rows[0]["adoptionSource"] != "github-api" || rows[0]["survivalState"] != "observed" {
		t.Fatalf("authoritative lifecycle was not preserved: %+v", rows)
	}

	write("detail-expired", nil)
	results, _, err = store.ExecuteSQLPlan(t.Context(), []query.Definition{
		{Name: "lifecycles", From: "$outputLifecycles"},
	}, []string{"lifecycles"})
	if err != nil || len(results["lifecycles"].Rows) != 1 {
		t.Fatalf("lifecycle did not survive omitted run detail: %+v %v", results, err)
	}
}
