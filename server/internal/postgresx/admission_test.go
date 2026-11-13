package postgresx

import (
	"context"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/githubnext/gh-cao/server/internal/admission"
	"github.com/githubnext/gh-cao/server/internal/model"
	"github.com/githubnext/gh-cao/server/internal/query"
)

func admissionIdentity() admission.Identity {
	return admission.Identity{Issuer: admission.Issuer, Repository: "acme/project", RepositoryID: "1001", OwnerID: "42",
		RunID: "9007199254740993", RunAttempt: "2", WorkflowRef: "acme/project/.github/workflows/triage.lock.yml@refs/heads/main",
		WorkflowSHA: strings.Repeat("a", 40), Event: "workflow_dispatch", Actor: "octocat", ActorID: "101"}
}

func admissionRunRows(t *testing.T, store *Store) []model.Row {
	t.Helper()
	result, _, err := store.ExecuteSQLPlan(t.Context(), []query.Definition{{
		Name: "admission-runs", From: "$runs",
		Select: []query.SelectedField{{Field: "id"}, {Field: "status"}, {Field: "admissionStatus"}, {Field: "githubRunId"},
			{Field: "attempt"}, {Field: "repositoryId"}, {Field: "workflowId"}, {Field: "updatedAt"}, {Field: "startedAt"},
			{Field: "completedAt"}, {Field: "conclusion"}, {Field: "aicTotal"}},
	}}, []string{"admission-runs"})
	if err != nil {
		t.Fatal(err)
	}
	return result["admission-runs"].Rows
}

func TestReplacementWithoutInvocationEvidenceSkipsPreservationDependencies(t *testing.T) {
	store, config := nativeTestStore(t)
	nativeSeed(t.Context(), t, store, nil)
	other, err := NewConfig(t.Context(), config, "other")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = other.Close() }()
	if err := other.RecordAdmittedRun(t.Context(), admissionIdentity()); err != nil {
		t.Fatal(err)
	}
	before := admissionRunRows(t, other)
	writer, err := store.BeginIngestion(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Abort(t.Context())
	var dependencies int
	if err := writer.tx.QueryRow(t.Context(), `SELECT count(*) FROM pg_class
		WHERE relnamespace=pg_my_temp_schema() AND relname IN
		('admitted_workflows','admitted_repositories','admitted_audits',
		 'admitted_grader_observations','admitted_graders')`).Scan(&dependencies); err != nil {
		t.Fatal(err)
	}
	if dependencies != 0 {
		t.Fatal("snapshot without retained invocations staged their preservation dependencies")
	}
	nativeParents(t.Context(), t, writer)
	state, err := writer.Publish(t.Context(), "replacement-without-invocations")
	if err != nil || state.Counts["$runs"] != 1 {
		t.Fatalf("ordinary replacement failed: %+v %v", state, err)
	}
	if !reflect.DeepEqual(before, admissionRunRows(t, other)) {
		t.Fatal("replacement changed another namespace's invocation evidence")
	}
}

func TestAdmittedRunProjectionIsCanonicalAndIdempotent(t *testing.T) {
	store, _ := nativeTestStore(t)
	identity := admissionIdentity()
	if err := store.RecordAdmittedRun(t.Context(), identity); err != nil {
		t.Fatal(err)
	}
	before, err := store.State(t.Context())
	if err != nil || !before.Ready || before.Counts["$runs"] != 1 {
		t.Fatalf("state not published: %+v %v", before, err)
	}
	rows := admissionRunRows(t, store)
	if len(rows) != 1 || rows[0]["status"] != "admitted" || rows[0]["admissionStatus"] != "admitted" ||
		rows[0]["id"] != "github:run:acme/project:9007199254740993" || rows[0]["githubRunId"] != identity.RunID {
		t.Fatalf("wrong run: %+v", rows)
	}
	for _, key := range []string{"startedAt", "completedAt", "conclusion", "aicTotal"} {
		if _, exists := rows[0][key]; exists {
			t.Fatalf("fabricated %s: %+v", key, rows)
		}
	}
	var wg sync.WaitGroup
	for n := 0; n < 8; n++ {
		wg.Go(func() {
			if err := store.RecordAdmittedRun(t.Context(), identity); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	after, err := store.State(t.Context())
	if err != nil || after.Revision != before.Revision || !reflect.DeepEqual(rows, admissionRunRows(t, store)) {
		t.Fatal("retry changed run or revision")
	}
	// No additional tables or invented start/completion facts are necessary.
	var parentID, path string
	if err := store.db.QueryRowContext(t.Context(), `SELECT r.repository_id,w.path FROM runs r
		JOIN workflows w ON w.namespace=r.namespace AND w.id=r.workflow_id WHERE r.namespace=$1`, store.namespace).Scan(&parentID, &path); err != nil {
		t.Fatal(err)
	}
	if parentID != "repository:acme%2Fproject" || path != ".github/workflows/triage.md" {
		t.Fatalf("noncanonical parents: %s %s", parentID, path)
	}
	identity.RunID = strings.Repeat("9", 100)
	identity.RunAttempt = strings.Repeat("9", 100)
	if err := store.RecordAdmittedRun(t.Context(), identity); err != nil {
		t.Fatal(err)
	}
	var exactID string
	var attempt *int64
	if err := store.db.QueryRowContext(t.Context(), `SELECT github_run_id,attempt FROM runs WHERE namespace=$1 AND github_run_id=$2`,
		store.namespace, identity.RunID).Scan(&exactID, &attempt); err != nil || exactID != identity.RunID || attempt != nil {
		t.Fatalf("large signed identifier was normalized or truncated: %q %v %v", exactID, attempt, err)
	}
}

func TestAdmittedRunSurvivesIngestionAndYieldsToCollectedEvidence(t *testing.T) {
	store, _ := nativeTestStore(t)
	identity := admissionIdentity()
	if err := store.RecordAdmittedRun(t.Context(), identity); err != nil {
		t.Fatal(err)
	}
	before := admissionRunRows(t, store)
	for _, revision := range []string{"unrelated-snapshot", "unrelated-repeat"} {
		writer, err := store.BeginIngestion(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Publish(t.Context(), revision); err != nil {
			writer.Abort(t.Context())
			t.Fatal(err)
		}
		writer.Abort(t.Context())
		if !reflect.DeepEqual(before, admissionRunRows(t, store)) {
			t.Fatal("replacement erased admitted run")
		}
		var completeness string
		if err := store.db.QueryRowContext(t.Context(), `SELECT completeness FROM cao_quality WHERE namespace=$1 AND collection='$runs'`, store.namespace).Scan(&completeness); err != nil || completeness != "partial" {
			t.Fatalf("sparse admission was marked complete: %q %v", completeness, err)
		}
	}
	writer, err := store.BeginIngestion(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Abort(t.Context())
	for _, record := range []struct {
		source string
		row    model.Row
	}{
		{"$repositories", model.Row{"id": "github:repository:1001", "owner": "acme", "name": "project"}},
		{"$workflows", model.Row{"id": "github:workflow:2002", "repositoryId": "github:repository:1001", "path": ".github/workflows/triage.md"}},
		{"$runs", model.Row{"id": before[0]["id"], "repositoryId": "github:repository:1001", "workflowId": "github:workflow:2002",
			"owner": "acme", "repository": "project", "githubRunId": identity.RunID, "attempt": 2, "status": "completed", "conclusion": "success"}},
	} {
		if err := writer.Append(t.Context(), record.source, record.row); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := writer.Publish(t.Context(), "collected-snapshot"); err != nil {
		t.Fatal(err)
	}
	rows := admissionRunRows(t, store)
	if len(rows) != 1 || rows[0]["status"] != "completed" || rows[0]["conclusion"] != "success" {
		t.Fatalf("collection did not replace sparse run: %+v", rows)
	}
	if rows[0]["workflowId"] != "github:workflow:2002" || rows[0]["repositoryId"] != "github:repository:1001" {
		t.Fatal("collected parent identities did not replace provisional relationships")
	}
	state, err := store.State(t.Context())
	if err != nil || state.Counts["$workflows"] != 1 || state.Counts["$repositories"] != 1 {
		t.Fatalf("provisional parents were resurrected: %+v %v", state, err)
	}
	if err := store.RecordAdmittedRun(t.Context(), identity); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(rows, admissionRunRows(t, store)) {
		t.Fatal("late retry downgraded collected evidence")
	}
}

func TestAdmittedRunUsesExistingParentsAndRollsBackFailures(t *testing.T) {
	store, _ := nativeTestStore(t)
	nativeSeed(t.Context(), t, store, map[string][]model.Row{
		"$campaigns":    {{"id": "campaign:existing", "slug": "existing"}},
		"$repositories": {{"id": "github:repository:1001", "owner": "acme", "name": "project"}},
		"$workflows":    {{"id": "github:workflow:2002", "repositoryId": "github:repository:1001", "path": ".github/workflows/triage.md", "campaignId": "campaign:existing"}},
	})
	identity := admissionIdentity()
	if err := store.RecordAdmittedRun(t.Context(), identity); err != nil {
		t.Fatal(err)
	}
	var repositoryID, workflowID string
	if err := store.db.QueryRowContext(t.Context(), `SELECT repository_id,workflow_id FROM runs WHERE namespace=$1 AND id=$2`,
		store.namespace, "github:run:acme/project:"+identity.RunID).Scan(&repositoryID, &workflowID); err != nil {
		t.Fatal(err)
	}
	if repositoryID != "github:repository:1001" || workflowID != "github:workflow:2002" {
		t.Fatalf("existing inventory duplicated: repository=%q workflow=%q", repositoryID, workflowID)
	}
	writer, err := store.BeginIngestion(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Publish(t.Context(), "pending-with-campaign"); err != nil {
		writer.Abort(t.Context())
		t.Fatal(err)
	}
	writer.Abort(t.Context())
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	identity.RunID = "44"
	if err := store.RecordAdmittedRun(ctx, identity); err == nil {
		t.Fatal("cancelled publication succeeded")
	}
	var count int
	if err := store.db.QueryRowContext(t.Context(), `SELECT count(*) FROM runs WHERE namespace=$1 AND github_run_id='44'`, store.namespace).Scan(&count); err != nil || count != 0 {
		t.Fatalf("failed publication left run: %d %v", count, err)
	}
	if _, err := store.db.ExecContext(t.Context(), `ALTER TABLE runs ADD CONSTRAINT reject_admission_fixture CHECK (github_run_id <> '44') NOT VALID`); err != nil {
		t.Fatal(err)
	}
	identity.Repository = "new/project"
	identity.WorkflowRef = "new/project/.github/workflows/triage.lock.yml@refs/heads/main"
	if err := store.RecordAdmittedRun(t.Context(), identity); err == nil {
		t.Fatal("failed run insert returned success")
	}
	if err := store.db.QueryRowContext(t.Context(), `SELECT count(*) FROM repositories WHERE namespace=$1 AND owner='new'`, store.namespace).Scan(&count); err != nil || count != 0 {
		t.Fatalf("failed run insert leaked a parent: %d %v", count, err)
	}
}
