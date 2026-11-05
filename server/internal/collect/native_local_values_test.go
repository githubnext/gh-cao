package collect

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/collectnative"
	"github.com/githubnext/gh-cao/server/internal/model"
	"github.com/githubnext/gh-cao/server/internal/operational"
	"github.com/githubnext/gh-cao/server/internal/postgresx"
	"github.com/githubnext/gh-cao/server/internal/query"
	"github.com/githubnext/gh-cao/server/internal/testutil"
)

func nativeLocalValueFixture(t *testing.T) (Projector, string) {
	t.Helper()
	data := testutil.Postgres(t, t.Context(), "CAO_TEST_POSTGRES_URL", "POSTGRES_URL")
	queue, _, _ := nativeRunQueue(t)
	enrollment := nativeEnrollmentFixture(t, data, queue)
	if err := enrollment.AddRepositories(t.Context(), 7, []string{"octo/example", "octo/secret"}); err != nil {
		t.Fatal(err)
	}
	services := queue.Tasks.(operational.Store).OperationalServices()
	root := t.TempDir()
	repository, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	for _, directory := range []string{"activity", "optimization/operational-value", ".github/value-evidence"} {
		if err := os.MkdirAll(filepath.Join(root, directory), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	write := func(name string, content []byte) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), content, 0o600); err != nil { // #nosec G703 -- fixed fixture filenames beneath the test-owned catalog.
			t.Fatal(err)
		}
	}
	for _, name := range []string{"dashboard", ".github/workflows"} {
		if err := os.Symlink(filepath.Join(repository, name), filepath.Join(root, name)); err != nil {
			t.Fatal(err)
		}
	}
	files, err := os.ReadDir(filepath.Join(repository, "activity"))
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		source := filepath.Join(repository, "activity", file.Name())
		if file.Name() == "local-values.mjs" || file.Name() == "workflow-values.mjs" {
			content, err := os.ReadFile(source) // #nosec G304 -- entries enumerated from the checked-out Activity runtime.
			if err != nil {
				t.Fatal(err)
			}
			write("activity/"+file.Name(), content)
		} else if err := os.Symlink(source, filepath.Join(root, "activity", file.Name())); err != nil {
			t.Fatal(err)
		}
	}
	adapter, err := os.ReadFile(filepath.Join(repository, "optimization", "operational-value.mjs")) // #nosec G304 -- fixed checked-out pinned adapter.
	if err != nil {
		t.Fatal(err)
	}
	write("optimization/operational-value.mjs", adapter)
	// Uses the production pinned adapter and canonical socket client. An
	// intentionally scope-free count must still see only the reviewed target.
	write("optimization/operational-value/fixture.mjs", []byte(`
import { executeCanonicalQuery } from "../../activity/canonical-query.mjs";
export const localCollection = true;
export const definition = {
  slug:"fixture", workflowName:"Fixture", adoption:{adoptedAt:"2026-10-01T00:00:00.000Z"},
  evaluation:{mode:"attainment-only"},
  evidence:{repositories:["octo/example"],measurementKind:"interval",
    window:{durationDays:1,maturationDays:0,cadenceDays:365}},
  metrics:[{id:"count",role:"primary",name:"Count",unit:"runs",direction:"increase"}]
};
export async function collectBatch(windows, options) {
  if (!options.localOnly || options.database) throw new Error("local canonical execution required");
  const result = await executeCanonicalQuery({name:"count",from:"runs",
    aggregate:{values:[{field:"id",as:"count",reducer:"count"}]}});
  const count = result.rows[0].count;
  if (count !== 1) throw new Error("reviewed canonical scope leaked");
  return windows.map(() => ({evidence:{count,coverageStatus:"partial",
    missingReason:"fixture-partial-coverage"},provenance:{}}));
}
export const scoreMetric = (_metric, evidence) => evidence.count;
`))
	evidence, err := os.ReadFile(filepath.Join(repository, "dashboard/site/test/fixtures/operational-value-evidence.jsonl")) // #nosec G304 -- fixed checked-out contract fixture.
	if err != nil {
		t.Fatal(err)
	}
	write(".github/value-evidence/fixture.jsonl", evidence)
	policy := fmt.Sprintf(`{"version":1,"gh-aw-version":"v0.93.1","control-plane":{
		"scope":{"allowed-repositories":["octo/example"]},"activity":{"repositories":["octo/example"]},
		"campaigns":{"activity":{"workflow-values":[{"repository":"octo/example","workflow":"verify-bugs",
		"collector":"not-present.mjs","evidence":{"path":".github/value-evidence/fixture.jsonl","sha256":"%x"}}]}}}}`,
		sha256.Sum256(evidence))
	write("cao.json", []byte(policy))
	writer, err := data.BeginIngestion(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Abort(t.Context())
	for _, repo := range []string{"example", "secret"} {
		coordinate := "octo/" + repo
		repositoryID := "repository:" + model.EncodeCoordinate(coordinate)
		workflowID := "workflow:" + model.EncodeCoordinate(coordinate+":.github/workflows/verify-bugs.md")
		for source, row := range map[string]model.Row{
			"$repositories": {"id": repositoryID, "owner": "octo", "name": repo},
			"$workflows": {"id": workflowID, "repositoryId": repositoryID,
				"path": ".github/workflows/verify-bugs.md"},
			"$runs": {"id": "run:" + repo, "repositoryId": repositoryID, "workflowId": workflowID,
				"owner": "octo", "repository": repo, "status": "completed", "conclusion": "success"},
		} {
			if err := writer.Append(t.Context(), source, row); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := writer.Append(t.Context(), "$campaigns", model.Row{
		"id": "campaign:optimization", "slug": "optimization", "name": "Optimization",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Publish(t.Context(), "native-value-fixture"); err != nil {
		t.Fatal(err)
	}
	return Projector{Data: data, State: services.State, Leases: services.Leases, Enrollment: enrollment,
		CatalogRoot: root, PolicyPath: filepath.Join(root, "cao.json")}, root
}

func TestNativeLocalValuesProductionCanonicalQueriesHealthAndReviewedImports(t *testing.T) {
	p, _ := nativeLocalValueFixture(t)
	lease := &workflowValueLease{acquire: true}
	p.Leases = lease
	if err := p.CollectNativeLocalValues(t.Context()); err != nil {
		t.Fatal(err)
	}
	if lease.locks != 1 {
		t.Fatal("local Value must use only its own lease, not projection or enrollment locks")
	}
	results, _, err := p.Data.ExecuteSQLPlan(t.Context(), []query.Definition{
		{Name: "values", From: "$operationalValues"},
		{Name: "health", From: "$workflowValueCollections"},
	}, []string{"values", "health"})
	if err != nil {
		t.Fatal(err)
	}
	values, health := results["values"].Rows, results["health"].Rows
	if len(values) == 0 || fmt.Sprint(values[0]["value"]) != "1" ||
		values[0]["coverage-status"] != "partial" || values[0]["repository"] != "octo/example" {
		t.Fatalf("canonical local computation lost scope/partial outputs: %+v", values)
	}
	if len(health) != 1 || health[0]["registration"] != "enabled" ||
		health[0]["reasonCode"] != "shared-budget-unavailable" || health[0]["lastAttemptAt"] != nil {
		t.Fatalf("native collection health disappeared or invented an attempt: %+v", health)
	}
	if results["health"].Metadata["availability"] != "available" ||
		results["health"].Metadata["freshness"] != "unknown" {
		t.Fatalf("health metadata concealed publication or promoted freshness: %+v", results["health"].Metadata)
	}
	packet, err := p.Data.ReadOperationalValue(t.Context(), "octo/example", "verify-bugs")
	if err != nil || packet["observedAt"] != "2026-10-20T12:00:00Z" {
		t.Fatalf("reviewed import missing: %+v %v", packet, err)
	}
	if p.Lake.Directory != "" {
		t.Fatal("native fixture unexpectedly has an Activity lake")
	}
	before, err := p.Data.State(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := p.CollectNativeLocalValues(t.Context()); err != nil {
		t.Fatal(err)
	}
	after, err := p.Data.State(t.Context())
	if err != nil || after.Revision != before.Revision {
		t.Fatalf("durable success checkpoint failed: %+v %+v %v", before, after, err)
	}
	p.MinInterval = time.Nanosecond
	if err := p.CollectNativeLocalValues(t.Context()); err != nil {
		t.Fatalf("identical reviewed import was not replay-safe: %v", err)
	}
}

func TestNativeLocalValuesFailureDoesNotConsumeInputs(t *testing.T) {
	p, _ := nativeLocalValueFixture(t)
	p.NodeBinary = "/nonexistent-node"
	if err := p.CollectNativeLocalValues(t.Context()); err == nil {
		t.Fatal("missing selected Node runtime was silently disabled")
	}
	completed, err := p.State.OperationalState(t.Context(), nativeLocalValueCompletedKey)
	if err != nil || len(completed) != 0 {
		t.Fatalf("failed execution consumed input: %q %v", completed, err)
	}
	p.NodeBinary = "node"
	if err := p.CollectNativeLocalValues(t.Context()); err != nil {
		t.Fatalf("retry required an Activity projection or file recovery: %v", err)
	}
}

func TestNativeLocalValuesUnselectedAndMisconfigured(t *testing.T) {
	if err := (Projector{NodeBinary: "/nonexistent-node"}).CollectNativeLocalValues(t.Context()); err != nil {
		t.Fatal("unselected Value must need no runtime", err)
	}
	if err := (Projector{CatalogRoot: "selected", PolicyPath: "selected"}).CollectNativeLocalValues(t.Context()); !errors.Is(err, collectnative.ErrUnsupported) {
		t.Fatalf("selected incomplete native Value configuration did not fail closed: %v", err)
	}
}

func TestNativeLocalValuesWithdrawalDuringEvaluationCannotResurrectMeasurements(t *testing.T) {
	p, root := nativeLocalValueFixture(t)
	module := filepath.Join(root, "optimization/operational-value/fixture.mjs")
	source, err := os.ReadFile(module) // #nosec G304 -- fixed module in the test-owned catalog.
	if err != nil {
		t.Fatal(err)
	}
	started, release := filepath.Join(root, "started"), filepath.Join(root, "release")
	startedJSON, _ := json.Marshal(started)
	releaseJSON, _ := json.Marshal(release)
	wait := `const fs = await import("node:fs/promises");
	await fs.writeFile(` + string(startedJSON) + `,"ready");
	for (;;) {
	  try { await fs.stat(` + string(releaseJSON) + `); break; } catch {}
	  await new Promise(resolve => setTimeout(resolve,10));
	}
	const count = result.rows[0].count;`
	if err := os.WriteFile(module, []byte(strings.Replace(string(source), "const count = result.rows[0].count;", wait, 1)), 0o600); err != nil { // #nosec G703 -- fixed module in the test-owned catalog.
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- p.CollectNativeLocalValues(ctx) }()
	deadline := time.Now().Add(15 * time.Second)
	for {
		if _, err := os.Stat(started); err == nil {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("local evaluation did not reach the canonical query: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("local evaluation did not start before its deadline")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := p.Enrollment.RemoveRepositories(ctx, 7, []string{"octo/example"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(release, []byte("ready"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, postgresx.ErrOperationalValueRevisionStale) {
		t.Fatalf("withdrawn source published evaluated measurements: %v", err)
	}
	nativeCanonicalCount(t, p.Data, "$operationalValues", 0)
	nativeCanonicalCount(t, p.Data, "$workflowValueCollections", 0)
	if _, err := p.Data.ReadOperationalValue(ctx, "octo/example", "verify-bugs"); !errors.Is(err, postgresx.ErrValueParentMissing) {
		t.Fatalf("withdrawn source retained imported evidence: %v", err)
	}
	completed, err := p.State.OperationalState(ctx, nativeLocalValueCompletedKey)
	if err != nil || len(completed) != 0 {
		t.Fatalf("stale computation consumed its durable input: %q %v", completed, err)
	}
}

func TestNativeLocalValuesRetainHistoryOutsideReviewedComputeScope(t *testing.T) {
	p, _ := nativeLocalValueFixture(t)
	state, err := p.Data.State(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	previous := model.Row{"id": "retained-secret", "repositoryId": "repository:octo%2Fsecret",
		"repository": "octo/secret", "campaignId": "campaign:optimization", "campaign": "optimization",
		"valueId": "old.metric", "value": json.Number("7"), "timestamp": time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)}
	if _, err := p.Data.ReplaceOperationalValues(t.Context(), state.Revision, []model.Row{previous}); err != nil {
		t.Fatal(err)
	}
	if err := p.CollectNativeLocalValues(t.Context()); err != nil {
		t.Fatal(err)
	}
	result, _, err := p.Data.ExecuteSQLPlan(t.Context(), []query.Definition{{
		Name: "history", From: "$operationalValues",
		Filter: &query.Filter{Predicates: []query.Predicate{{Field: "repository", Equals: "octo/secret"}}},
	}}, []string{"history"})
	if err != nil || len(result["history"].Rows) != 1 || fmt.Sprint(result["history"].Rows[0]["value"]) != "7" {
		t.Fatalf("local refresh dropped disabled/out-of-policy retained history: %+v %v", result, err)
	}
}

func TestNativeLocalValueDefinitionsOnlyTranslateCanonicalRoots(t *testing.T) {
	definitions := []query.Definition{
		{Name: "repositories", From: "$repositories"},
		{Name: "values", From: "operationalValues", Union: []string{"runs", "repositories"},
			Joins: []query.Join{{Source: "workflows"}, {Source: "unknown"}}},
	}
	result := nativeLocalValueDefinitions(definitions)
	if result[1].From != "$operationalValues" || result[1].Union[0] != "$runs" ||
		result[1].Union[1] != "repositories" || result[1].Joins[0].Source != "$workflows" ||
		result[1].Joins[1].Source != "unknown" {
		t.Fatalf("canonical root translation widened the query contract: %+v", result)
	}
	if definitions[1].Union[0] != "runs" || definitions[1].Joins[0].Source != "workflows" {
		t.Fatal("canonical translation mutated caller-owned definitions")
	}
}

func TestNativeLocalValueTransportRejectsMalformedEnvelope(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "evidence.jsonl")
	content, err := json.Marshal(map[string]any{"schema_version": 2, "kind": "workflow_value_collection",
		"workflow_value_collection": map[string]any{}, "extra": true})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filename, content, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readNativeValueEnvelopes(context.Background(), filename, "workflow_value_collection"); err == nil {
		t.Fatal("extra envelope members accepted")
	}
}

func TestNativeLocalValuesActivityDisableRetainsEvidenceAndMeasurements(t *testing.T) {
	p, _ := nativeLocalValueFixture(t)
	if err := p.CollectNativeLocalValues(t.Context()); err != nil {
		t.Fatal(err)
	}
	before, _, err := p.Data.ExecuteSQLPlan(t.Context(),
		[]query.Definition{{Name: "values", From: "$operationalValues"}}, []string{"values"})
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(p.PolicyPath) // #nosec G304 -- reviewed policy in the test-owned fixture.
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(content, &document); err != nil {
		t.Fatal(err)
	}
	control := document["control-plane"].(map[string]any)
	activity := control["campaigns"].(map[string]any)["activity"].(map[string]any)
	activity["enabled"] = false
	content, err = json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.PolicyPath, content, 0o600); err != nil { // #nosec G703 -- reviewed policy in the test-owned fixture.
		t.Fatal(err)
	}
	if err := p.CollectNativeLocalValues(t.Context()); err != nil {
		t.Fatal(err)
	}
	after, _, err := p.Data.ExecuteSQLPlan(t.Context(), []query.Definition{
		{Name: "values", From: "$operationalValues"},
		{Name: "health", From: "$workflowValueCollections"},
	}, []string{"values", "health"})
	if err != nil || len(before["values"].Rows) == 0 || len(after["values"].Rows) != len(before["values"].Rows) {
		t.Fatalf("disabled Activity dropped retained measurements: %+v %v", after, err)
	}
	health := after["health"].Rows
	if len(health) != 1 || health[0]["state"] != "disabled" || health[0]["reasonCode"] != "activity-disabled" {
		t.Fatalf("disabled collection decision was lost: %+v", health)
	}
	if _, err := p.Data.ReadOperationalValue(t.Context(), "octo/example", "verify-bugs"); err != nil {
		t.Fatalf("disabled registration dropped accepted evidence: %v", err)
	}
}
