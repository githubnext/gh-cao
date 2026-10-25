package adxingest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/adxtrace"
	"github.com/githubnext/gh-cao/server/internal/mining"
	"github.com/githubnext/gh-cao/server/internal/model"
	"github.com/githubnext/gh-cao/server/internal/sessionexport"
)

type kustoMiningSource struct {
	revision int64
	updated  time.Time
	observed time.Time
	change   func(*sessionexport.MiningSnapshot)
}

func (s *kustoMiningSource) ReadMining(_ context.Context, repository, run string, attempt int64) (sessionexport.MiningSnapshot, error) {
	canonical := "github:run:" + repository + ":" + run
	member := mining.Membership{Repository: repository, RunID: run, Attempt: int(attempt), ObservedAt: s.observed,
		Context: mining.Context{EpisodeID: "episode", HopID: "hop", ParentHopID: "parent", RootRunID: "1", RootRepo: "octo/root"}}
	number, err := strconv.ParseInt(run, 10, 64)
	if err != nil {
		return sessionexport.MiningSnapshot{}, err
	}
	outcome := mining.Outcome{Repository: "octo/target", Number: number, PullRequest: true,
		URL: "https://github.com/octo/target/pull/" + run, State: "closed", Merged: true,
		UpdatedAt: s.updated, ObservedAt: s.observed, Source: "github-webhook"}
	output := "output-" + run
	snapshot := sessionexport.MiningSnapshot{Namespace: "synthetic-canonical", Revision: s.revision,
		Run:    model.Row{"id": canonical, "repositoryId": "repository:" + model.EncodeCoordinate(repository), "attempt": attempt},
		Audits: append(member.Audits(), outcome.Audit(output, canonical)),
		Outputs: []model.Row{{"id": output, "runId": canonical, "safeOutputType": "create_pull_request",
			"isPullRequest": true, "safeOutputUrl": outcome.URL}}}
	// Two recorded hops share one producing attempt: usage must not double.
	member.Context = mining.Context{EpisodeID: "episode", HopID: "child", ParentHopID: "hop"}
	snapshot.Audits = append(snapshot.Audits, member.Audits()...)
	if s.change != nil {
		s.change(&snapshot)
	}
	return snapshot, nil
}

func kustoMiningConfig(t *testing.T) (adxtrace.Config, *kustoMiningSource) {
	t.Helper()
	config := kustoTraceConfig(t, "full", 0)
	config.MiningEnabled, config.Policy.MiningApproved, config.MiningInterval = true, true, time.Minute
	return config, &kustoMiningSource{revision: 1, updated: time.Now().UTC().Add(-2 * time.Hour),
		observed: time.Now().UTC().Add(-time.Hour)}
}

func kustoCreationTrace(t *testing.T, run int) []byte {
	t.Helper()
	events := []model.Row{
		{"type": "session.format", "data": model.Row{"version": 1}},
		{"type": "workflow.info", "data": model.Row{"episode": model.Row{"episodeId": "episode", "hopId": "hop", "parentHopId": "parent"}}},
		{"type": "user.message", "data": model.Row{"content": "synthetic task"}},
		{"type": "tool.execution_start", "data": model.Row{"toolName": "create_pull_request",
			"toolCallId": "create-1", "input": model.Row{"title": "synthetic output"}}},
		{"type": "tool.execution_complete", "data": model.Row{"toolName": "create_pull_request", "toolCallId": "create-1",
			"success": true, "error": nil, "output": model.Row{"url": fmt.Sprintf("https://github.com/octo/target/pull/%d", run)}}},
		{"type": "assistant.message", "data": model.Row{"content": "synthetic final", "channel": "final"}},
	}
	var body bytes.Buffer
	for _, event := range events {
		if err := json.NewEncoder(&body).Encode(event); err != nil {
			t.Fatal(err)
		}
	}
	return body.Bytes()
}

func waitKustoMiningPoll(t *testing.T, interval time.Duration) {
	t.Helper()
	// Respect the production minimum poll interval; do not alter a private
	// ledger or production validation just to accelerate the functional test.
	timer := time.NewTimer(interval + time.Second)
	defer timer.Stop()
	select {
	case <-t.Context().Done():
		t.Fatal(t.Context().Err())
	case <-timer.C:
	}
}

func TestKustoEmulatorMiningLifecycle(t *testing.T) {
	k := newKustoAcceptance(t)
	k.apply(t, adxtrace.Schema())
	config, source := kustoMiningConfig(t)
	const namespace = "mining-lifecycle"
	worker, err := adxtrace.New(t.Context(), config, namespace, k.client, source)
	if err != nil {
		t.Fatal(err)
	}
	trace := captureKustoArtifact(t, worker, config, 42, kustoCreationTrace(t, 42))
	unattested := captureKustoTrace(t, worker, config, 43, "synthetic task without creation proof")
	k.loseAcknowledgement("TraceOutcomes")
	for range 10 {
		_, err = worker.Step(t.Context())
		if err != nil {
			break
		}
	}
	if err == nil {
		t.Fatal("ambiguous outcome ingestion was not exercised")
	}
	k.assert(t, "cao_TraceMining() | count", [][]any{{0}})
	worker, err = adxtrace.New(t.Context(), config, namespace, k.client, source)
	if err != nil {
		t.Fatal(err)
	}
	drainKustoTraces(t, worker)
	cutoff := time.Now().UTC().Format(time.RFC3339Nano)
	miningVersion := k.query(t, "cao_TraceMining() | where RunId=="+literal(trace.ID)+" | project ProjectionId")[0][0].(string)
	unattestedVersion := k.query(t, "cao_TraceMining() | where RunId=="+literal(unattested.ID)+" | project ProjectionId")[0][0].(string)
	k.assert(t, "TraceOutcomes | count", [][]any{{3}})
	k.assert(t, `cao_TraceOutcomes() | where OutputId=="output-42" | project Status, ProducingAttempt, CanonicalNamespace, CanonicalRunId, TraceVersion`,
		[][]any{{"accepted", 1, "synthetic-canonical", "github:run:octo/api:42", trace.Version}})
	k.assert(t, `cao_TraceOutcomes() | where OutputId=="output-43" | project Status, ProducingAttempt`, [][]any{{"accepted", 0}})
	k.assert(t, `cao_TraceEpisodeRuns("mining-lifecycle","episode") | summarize Attempts=count(), Hops=sum(ObservedHops), Missing=sum(MissingParents)`,
		[][]any{{2, 4, 2}})
	k.assert(t, `cao_TraceOutcomeSummary("mining-lifecycle",now()) | project EligibleOutputs, Accepted, Pending, UnknownAttempts, Scope, EvidenceState`,
		[][]any{{2, 2, 0, 1, "selected_outputs", "partial"}})
	k.assert(t, `cao_TraceMiningAt("mining-lifecycle", now()+1d) | count`, [][]any{{0}})

	spec, err := json.Marshal(adxtrace.DatasetSpec{ID: "synthetic-outcome-corpus", Transform: "native-v1", Evaluator: "independent-fixture",
		TrainingApproved: true, ToolSchemaEvidence: "synthetic-schema-v1",
		ToolSchemas: []map[string]any{{"name": "create_pull_request", "parameters": map[string]any{"type": "object"}}},
		Entries: []adxtrace.DatasetEntry{{TraceID: trace.ID, TraceVersion: trace.Version, MiningVersion: miningVersion,
			TaskGroup: "episode", Split: "test", QualityLabel: "synthetic-only", LabelEvidence: "independent-review",
			Outcome: &adxtrace.DatasetOutcome{OutputID: "output-42",
				AuditID:     "audit:server-mining:outcome:" + model.EncodeCoordinate("output-42"),
				Disposition: "accepted", RuleVersion: mining.OutcomeRuleVersion, Cutoff: cutoff}}}})
	if err != nil {
		t.Fatal(err)
	}
	path, err := adxtrace.ExportDataset(t.Context(), config, namespace, spec)
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(path) // #nosec G304 -- exporter-owned synthetic file inside t.TempDir.
	if err != nil || !bytes.Contains(content, []byte(`"canonicalNamespace":"synthetic-canonical"`)) {
		t.Fatal("outcome dataset lost canonical source lineage")
	}
	drainKustoTraces(t, worker)
	k.assert(t, "cao_TraceDatasets() | project MiningVersion, OutcomeId, Disposition, LabelRuleVersion",
		[][]any{{miningVersion, "output-42", "accepted", mining.OutcomeRuleVersion}})

	source.revision++
	source.change = func(snapshot *sessionexport.MiningSnapshot) {
		if snapshot.Run["id"] != "github:run:octo/api:42" {
			return
		}
		audit := snapshot.Audits[2]
		audit["status"], audit["code"], audit["decision"] = "pending", "open", "open"
		audit["timestamp"], audit["observedAt"] = time.Now().UTC(), time.Now().UTC()
	}
	waitKustoMiningPoll(t, config.MiningInterval)
	drainKustoTraces(t, worker)
	k.assert(t, `cao_TraceOutcomes() | where OutputId=="output-42" | project Status, ProducingAttempt`, [][]any{{"pending", 1}})
	k.assert(t, "TraceEvents | count", [][]any{{16}})
	k.assert(t, "cao_ReadyTraces() | where RunId=="+literal(trace.ID)+" | project ProjectionId", [][]any{{trace.Version}})
	k.assert(t, "cao_TraceMining() | where RunId=="+literal(unattested.ID)+" | project ProjectionId", [][]any{{unattestedVersion}})
	k.assert(t, "cao_TraceDatasets() | count", [][]any{{0}})
	k.assert(t, `cao_TraceOutcomesAt("mining-lifecycle",datetime(`+cutoff+`)) | where OutputId=="output-42" | project Status, AvailableAsOf`,
		[][]any{{"accepted", true}})
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("reopen did not invalidate and remove local outcome dataset")
	}
	source.revision++
	source.change = func(snapshot *sessionexport.MiningSnapshot) {
		if snapshot.Run["id"] == "github:run:octo/api:42" {
			snapshot.Audits[2]["timestamp"] = source.updated
		}
	}
	waitKustoMiningPoll(t, config.MiningInterval)
	for range 10 {
		_, err = worker.Step(t.Context())
		if err != nil {
			break
		}
	}
	if err == nil {
		t.Fatal("out-of-order independent outcome did not fail closed")
	}
	drainKustoTraces(t, worker)
	k.assert(t, "cao_TraceMining() | where RunId=="+literal(trace.ID)+" | count", [][]any{{0}})
	k.assert(t, `cao_TraceOutcomesAt("mining-lifecycle",datetime(`+cutoff+`)) | where OutputId=="output-42" | count`, [][]any{{0}})
	k.assert(t, "TraceEvents | count", [][]any{{16}})
	if err := worker.WithdrawRepository(t.Context(), "octo/api"); err != nil {
		t.Fatal(err)
	}
	k.loseAcknowledgement("TraceMiningManifest")
	err = nil
	for range 10 {
		_, err = worker.Step(t.Context())
		if err != nil {
			break
		}
	}
	if err == nil {
		t.Fatal("ambiguous mining withdrawal was not exercised")
	}
	worker, err = adxtrace.New(t.Context(), config, namespace, k.client, source)
	if err != nil {
		t.Fatal(err)
	}
	drainKustoTraces(t, worker)
	for _, query := range []string{"cao_TraceMining()", "cao_TraceEpisodes()", "cao_TraceOutcomes()", "cao_TraceDatasets()",
		`cao_TraceOutcomesAt("mining-lifecycle",datetime(` + cutoff + `))`} {
		k.assert(t, query+" | count", [][]any{{0}})
	}
}

func TestKustoEmulatorMiningBindingFailures(t *testing.T) {
	for _, kind := range []string{"host", "category", "attempt", "unknown-attempt", "context", "missing-selected-run"} {
		t.Run(kind, func(t *testing.T) {
			k := newKustoAcceptance(t)
			k.apply(t, adxtrace.Schema())
			config, source := kustoMiningConfig(t)
			source.change = func(snapshot *sessionexport.MiningSnapshot) {
				switch kind {
				case "host":
					snapshot.Outputs[0]["safeOutputUrl"] = "https://example.invalid/octo/target/pull/42"
				case "category":
					snapshot.Outputs[0]["isPullRequest"] = false
				case "attempt":
					snapshot.Run["attempt"] = 2
				case "unknown-attempt":
					delete(snapshot.Audits[0], "claimRunAttempt")
				case "context":
					snapshot.Audits[0]["correlationId"] = "contradictory-episode"
				case "missing-selected-run":
					snapshot.Run, snapshot.Audits, snapshot.Outputs = nil, nil, nil
				}
			}
			worker, err := adxtrace.New(t.Context(), config, "invalid-binding", k.client, source)
			if err != nil {
				t.Fatal(err)
			}
			captureKustoArtifact(t, worker, config, 42, kustoCreationTrace(t, 42))
			for range 10 {
				var more bool
				more, err = worker.Step(t.Context())
				if err != nil || !more {
					break
				}
			}
			if kind != "missing-selected-run" && err == nil {
				t.Fatal("invalid source binding did not fail closed")
			}
			if kind == "missing-selected-run" && err != nil {
				t.Fatal(err)
			}
			k.assert(t, "cao_ReadyTraces() | count", [][]any{{1}})
			k.assert(t, "cao_TraceMining() | count", [][]any{{0}})
			k.assert(t, "cao_TraceOutcomes() | count", [][]any{{0}})
		})
	}
}
