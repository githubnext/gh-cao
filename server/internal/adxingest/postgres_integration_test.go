package adxingest

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/model"
	operationalpostgres "github.com/githubnext/gh-cao/server/internal/operational/postgres"
	"github.com/githubnext/gh-cao/server/internal/postgresx"
	"github.com/githubnext/gh-cao/server/internal/testutil"
)

func publishSession(t *testing.T, data *postgresx.Store, tokens int) {
	t.Helper()
	writer, err := data.BeginIngestion(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Abort(t.Context())
	at := time.Now().UTC().Truncate(time.Microsecond).Format(time.RFC3339Nano)
	for _, record := range []struct {
		source string
		row    model.Row
	}{
		{"$repositories", model.Row{"id": "repo"}},
		{"$repositories", model.Row{"id": "excluded"}},
		{"$workflows", model.Row{"id": "workflow", "repositoryId": "repo"}},
		{"$workflows", model.Row{"id": "excluded-workflow", "repositoryId": "excluded"}},
		{"$runs", model.Row{"id": "run", "repositoryId": "repo", "workflowId": "workflow",
			"status": "completed", "completedAt": at, "attempt": 1, "inputTokens": tokens, "outputTokens": nil,
			"title": "DO_NOT_EXPORT", "failureLog": "DO_NOT_EXPORT"}},
		{"$runs", model.Row{"id": "excluded-run", "repositoryId": "excluded", "workflowId": "excluded-workflow",
			"status": "completed", "completedAt": at}},
		{"$tools", model.Row{"id": "start", "runId": "run", "name": "read", "type": "tool_call",
			"timestamp": at, "summary": "DO_NOT_EXPORT"}},
		{"$domains", model.Row{"id": "request", "runId": "run", "domain": "example.invalid",
			"decision": "allowed", "timestamp": at, "summary": "DO_NOT_EXPORT"}},
		{"$graders", model.Row{"id": "grader", "workflowId": "workflow", "unit": "milliseconds"}},
		{"$graderObservations", model.Row{"id": "grade", "graderId": "grader", "runId": "run", "value": 10, "timestamp": at}},
		{"$evals", model.Row{"id": "eval", "workflowId": "workflow"}},
		{"$evalObservations", model.Row{"id": "answer", "evalId": "eval", "runId": "run", "evalResult": "UNKNOWN", "timestamp": at}},
	} {
		if err := writer.Append(t.Context(), record.source, record.row); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := writer.Publish(t.Context(), fmt.Sprintf("adx-fixture-%d", tokens)); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresSimulatorDurableRestartAndCanonicalReplacement(t *testing.T) {
	data := testutil.Postgres(t, t.Context(), "POSTGRES_URL")
	publishSession(t, data, 0)
	endpoint := os.Getenv("POSTGRES_URL")
	namespace := fmt.Sprintf("adx-test-%d", time.Now().UnixNano())
	state, err := operationalpostgres.New(t.Context(), endpoint, namespace, operationalpostgres.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = state.Close() }()
	sim, client := newSimulator(t)
	sim.failTable = "ToolEvents"
	worker, err := NewWorker(testConfig(), data, client, state.OperationalServices())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := worker.Step(t.Context()); err == nil {
		t.Fatal("expected ambiguous destination failure")
	}
	pending := readState(t, worker).Pending
	if pending == nil || pending.Tables["SessionFacts"][0]["InputTokens"].(json.Number) != "0" {
		t.Fatal("pending canonical snapshot was not persisted with exact numeric evidence")
	}
	publishSession(t, data, 7)
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := operationalpostgres.Open(t.Context(), endpoint, namespace, operationalpostgres.Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	restarted, err := NewWorker(testConfig(), data, client, reopened.OperationalServices())
	if err != nil {
		t.Fatal(err)
	}
	step(t, restarted)
	if sim.latestRevision() != 1 {
		t.Fatal("restart lost source revision binding")
	}
	step(t, restarted)
	step(t, restarted)
	if sim.latestRevision() != 2 {
		t.Fatal("canonical correction did not become ready")
	}
	for _, rows := range sim.tables {
		for _, row := range rows {
			if row["RunId"] != "run" {
				t.Fatal("out-of-scope canonical evidence was exported")
			}
		}
	}
	for _, table := range []string{"SessionFacts", "ToolEvents", "FirewallEvents", "GraderObservations", "EvalObservations", "SessionVersions"} {
		if len(sim.tables[table]) == 0 {
			t.Fatalf("canonical family %s did not cross the production boundary", table)
		}
	}
	counters, _, err := reopened.OperationalServices().IngestionMetrics.IngestionHealth(t.Context())
	if err != nil || counters["adxExported"] != 2 {
		t.Fatalf("durable ADX export metrics: %v, %v", counters, err)
	}
}
