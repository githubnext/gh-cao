package adxingest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/adxtrace"
	"github.com/githubnext/gh-cao/server/internal/model"
)

func kustoTraceConfig(t *testing.T, mode string, count int) adxtrace.Config {
	t.Helper()
	config := adxtrace.Config{Enabled: true, Directory: t.TempDir(), DurableStorage: true,
		Repositories: []string{"octo/api"}, Mode: mode, SampleCount: count, Seed: "predeclared-synthetic-seed",
		FrameDelay: time.Hour, Policy: adxtrace.Policy{Version: "synthetic-v1", ContentApproved: true, RightsApproved: true,
			Events: map[string][]string{
				"session.format": {"version"}, "session.collection": {},
				"workflow.info": {"engineId", "engineHarness", "agentVersion", "episode"},
				"user.message":  {"content"}, "assistant.message": {"content", "channel"},
				"assistant.reasoning":     {"content"},
				"tool.execution_start":    {"toolName", "toolCallId", "input"},
				"tool.execution_complete": {"toolName", "toolCallId", "output", "success", "error"},
			}}}
	// #nosec G302 -- owner-only spool needs directory traversal.
	if err := os.Chmod(config.Directory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := config.Validate(); err != nil {
		t.Fatal(err)
	}
	return config
}

func kustoTraceSource(t *testing.T, user string) []byte {
	t.Helper()
	events := []model.Row{
		{"type": "session.format", "data": model.Row{"version": 1}},
		{"type": "session.collection", "data": model.Row{}},
		{"type": "workflow.info", "data": model.Row{"engineId": "copilot", "engineHarness": "synthetic-harness",
			"agentVersion": "synthetic-1", "episode": model.Row{"episodeId": "episode", "hopId": "hop", "parentHopId": "parent"}}},
		{"id": "user", "type": "user.message", "data": model.Row{"content": user}},
		{"id": "start", "parentId": "user", "type": "tool.execution_start",
			"data": model.Row{"toolName": "read", "toolCallId": "call-1", "input": model.Row{"n": json.Number("9007199254740993")}}},
		{"id": "result", "parentId": "start", "type": "tool.execution_complete",
			"data": model.Row{"toolName": "read", "toolCallId": "call-1", "success": false, "error": nil,
				"output": strings.Repeat("synthetic-output", 5000)}},
		{"type": "tool.execution_start", "data": model.Row{"toolName": "read", "toolCallId": "call-2", "input": model.Row{"path": "fixture"}}},
		{"type": "tool.execution_complete", "data": model.Row{"toolName": "read", "toolCallId": "call-2", "output": "ok", "success": true}},
		{"type": "assistant.reasoning", "data": model.Row{"content": "DO_NOT_TRAIN_SYNTHETIC_REASONING"}},
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

func captureKustoTrace(t *testing.T, w *adxtrace.Worker, config adxtrace.Config, run int, user string) adxtrace.Evidence {
	t.Helper()
	return captureKustoArtifact(t, w, config, run, kustoTraceSource(t, user))
}

func captureKustoArtifact(t *testing.T, w *adxtrace.Worker, config adxtrace.Config, run int, source []byte) adxtrace.Evidence {
	t.Helper()
	day := time.Now().UTC().Add(-72 * time.Hour).Format(time.DateOnly)
	evidence, err := adxtrace.Project(config, "octo/api", fmt.Sprint(run), day, 1, source, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	logs, shards := t.TempDir(), t.TempDir()
	directory := filepath.Join(logs, fmt.Sprintf("run-%d", run))
	if err := os.MkdirAll(filepath.Join(directory, "usage"), 0o700); err != nil {
		t.Fatal(err)
	}
	for path, content := range map[string][]byte{
		filepath.Join(directory, "usage", "aw_session.jsonl"): source,
		filepath.Join(directory, "aw_info.json"):              fmt.Appendf(nil, `{"run_id":%d,"run_attempt":1,"repository":"octo/api"}`, run),
		filepath.Join(shards, "repo-1.jsonl"): fmt.Appendf(nil,
			`{"kind":"run","run":{"run_id":%d,"run_attempt":1,"repository":"octo/api","status":"completed","completed_at":"%sT00:00:00Z"}}`, run, day),
	} {
		if err := os.WriteFile(path, content, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Capture(t.Context(), "octo/api", logs, filepath.Join(shards, "repo-")); err != nil {
		t.Fatal(err)
	}
	return evidence
}

func drainKustoTraces(t *testing.T, w *adxtrace.Worker) {
	t.Helper()
	for range 30 {
		more, err := w.Step(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if !more {
			return
		}
	}
	t.Fatal("trace delivery did not drain")
}

func assertKustoDocuments(t *testing.T, k *kustoAcceptance, namespace string, evidence adxtrace.Evidence) {
	t.Helper()
	expected := make([][]any, 0, len(evidence.Tables["TraceEvents"]))
	for sequence, row := range evidence.Tables["TraceEvents"] {
		expected = append(expected, []any{sequence, row["ContentDigest"]})
	}
	k.assert(t, "cao_TraceDocuments("+literal(namespace)+","+literal(evidence.ID)+",32768)"+
		" | project Sequence, Digest=hash_sha256(Content) | order by Sequence asc", expected)
}

func TestKustoEmulatorWholeTraceLifecycle(t *testing.T) {
	k := newKustoAcceptance(t)
	k.apply(t, adxtrace.Schema())
	config := kustoTraceConfig(t, "full", 0)
	const namespace = "whole-trace"
	worker, err := adxtrace.New(t.Context(), config, namespace, k.client)
	if err != nil {
		t.Fatal(err)
	}
	// More than one production streaming batch, with UTF-8 part boundaries.
	evidence := captureKustoTrace(t, worker, config, 42, strings.Repeat("\u00e9", 600000))
	k.loseAcknowledgement("TraceContentParts")
	if _, err := worker.Step(t.Context()); err == nil {
		t.Fatal("lost acknowledgement was not retried")
	}
	k.assert(t, "cao_ReadyTraces() | count", [][]any{{0}})
	worker, err = adxtrace.New(t.Context(), config, namespace, k.client)
	if err != nil {
		t.Fatal(err)
	}
	k.holdIngestion("TraceContentParts")
	if _, err := worker.Step(t.Context()); err == nil {
		t.Fatal("actual engine verification accepted invisible content")
	}
	k.assert(t, "cao_ReadyTraces() | count", [][]any{{0}})
	k.holdIngestion("")
	drainKustoTraces(t, worker)
	assertKustoDocuments(t, k, namespace, evidence)
	k.assert(t, "TraceContentParts | summarize count() > dcount(RowDigest)", [][]any{{true}})
	args := literal(namespace) + "," + literal(evidence.ID)
	k.assert(t, "cao_TraceTimeline("+args+",4) | project Sequence, Type",
		[][]any{{0, "session.format"}, {1, "session.collection"}, {2, "workflow.info"}, {3, "user.message"}})
	k.assert(t, "cao_TraceContext("+args+") | project EngineId, EngineHarness, AgentVersion, EpisodeId, HopId, ParentHopId",
		[][]any{{"copilot", "synthetic-harness", "synthetic-1", "episode", "hop", "parent"}})
	k.assert(t, "cao_TraceConfigurationCandidates("+args+") | project Sequence, Candidate",
		[][]any{{5, "observed-error-candidate"}})
	k.assert(t, "cao_TraceToolResponseCandidates("+args+",65536) | project Sequence, ToolName, ApprovedOutputTextBytes",
		[][]any{{5, "read", 80000}})
	k.assert(t, "cao_TraceRepeatedToolCandidates("+args+") | project ToolName, ObservedStarts, Sequences",
		[][]any{{"read", 2, []int{4, 6}}})
	k.assert(t, "cao_TraceDocuments("+args+") | where Sequence == 4 | project tostring(Event.data.input.n)",
		[][]any{{"9007199254740993"}})
	k.assert(t, "cao_TraceDocuments("+args+") | where Sequence == 5 | project isnull(Event.data.error), Event.data.success",
		[][]any{{true, false}})
	k.assert(t, "cao_TraceSampleFrames() | project Mode, Population, Selected", [][]any{{"full", 1, 1}})
	k.assert(t, "cao_TraceSampleMembership() | project Probability == 1.0, Weight == 1.0", [][]any{{true, true}})

	datasetSpec := adxtrace.DatasetSpec{ID: "synthetic", Transform: "native-v1", Evaluator: "human-fixture",
		TrainingApproved: true, ToolSchemaEvidence: "synthetic-schema-v1",
		ToolSchemas: []map[string]any{{"name": "read", "parameters": map[string]any{"type": "object"}}},
		Entries: []adxtrace.DatasetEntry{{TraceID: evidence.ID, TraceVersion: evidence.Version,
			TaskGroup: "synthetic-task", Split: "test", QualityLabel: "synthetic-only", LabelEvidence: "fixture-review"}}}
	spec, err := json.Marshal(datasetSpec)
	if err != nil {
		t.Fatal(err)
	}

	path, err := adxtrace.ExportDataset(t.Context(), config, namespace, spec)
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(path) // #nosec G304 -- ExportDataset returns a private synthetic file inside the test spool.
	if err != nil || bytes.Contains(content, []byte("DO_NOT_TRAIN_SYNTHETIC_REASONING")) {
		t.Fatal("synthetic dataset missing or contains reasoning")
	}
	drainKustoTraces(t, worker)
	k.assert(t, "cao_TraceDatasets() | project MemberCount, Split, State", [][]any{{1, "test", "ready"}})
	corrected := captureKustoTrace(t, worker, config, 42, "corrected synthetic input")
	if corrected.Version == evidence.Version {
		t.Fatal("correction did not change immutable identity")
	}
	drainKustoTraces(t, worker)
	assertKustoDocuments(t, k, namespace, corrected)
	k.assert(t, "cao_ReadyTraces() | project ProjectionId", [][]any{{corrected.Version}})
	k.assert(t, "TraceManifest | summarize dcount(ProjectionId)", [][]any{{2}})
	k.assert(t, "cao_TraceSampleFrames() | count", [][]any{{0}})
	k.assert(t, "cao_TraceDatasets() | count", [][]any{{0}})
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("correction did not remove invalidated local dataset")
	}
	datasetSpec.ID, datasetSpec.Entries[0].TraceVersion = "synthetic-corrected", corrected.Version
	spec, err = json.Marshal(datasetSpec)
	if err != nil {
		t.Fatal(err)
	}
	path, err = adxtrace.ExportDataset(t.Context(), config, namespace, spec)
	if err != nil {
		t.Fatal(err)
	}
	drainKustoTraces(t, worker)
	k.assert(t, "cao_TraceDatasets() | count", [][]any{{1}})
	if err := worker.WithdrawRepository(t.Context(), "octo/api"); err != nil {
		t.Fatal(err)
	}
	drainKustoTraces(t, worker)
	for _, query := range []string{"cao_ReadyTraces()", "cao_TraceEvents()", "cao_TraceContentParts()", "cao_TraceDatasets()",
		"cao_TraceSampleMembership()", "cao_TraceDocuments(" + args + ")"} {
		k.assert(t, query+" | count", [][]any{{0}})
	}
	k.assert(t, "TraceManifest | summarize arg_max(SourceRevision, *) by Namespace, RunId | project State", [][]any{{"withdrawn"}})
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("withdrawal did not remove ready local dataset")
	}
}

func TestKustoEmulatorRejectsCorruptAndMissingContent(t *testing.T) {
	k := newKustoAcceptance(t)
	k.apply(t, adxtrace.Schema())
	config := kustoTraceConfig(t, "full", 0)
	const namespace = "corrupt-content"
	worker, err := adxtrace.New(t.Context(), config, namespace, k.client)
	if err != nil {
		t.Fatal(err)
	}
	evidence := captureKustoTrace(t, worker, config, 42, "synthetic input")
	drainKustoTraces(t, worker)
	assertKustoDocuments(t, k, namespace, evidence)
	// Preserve the declared digest/count while corrupting an actual engine row.
	k.execute(t, "/v1/rest/mgmt", `.set-or-replace TraceContentParts <| TraceContentParts
        | extend Content=iff(Sequence == 3, strcat(Content, "corruption"), Content)`)
	args := literal(namespace) + "," + literal(evidence.ID)
	k.assert(t, "cao_TraceDocuments("+args+") | where Sequence == 3 | count", [][]any{{0}})
	k.execute(t, "/v1/rest/mgmt", ".clear table TraceContentParts data")
	k.assert(t, "cao_ReadyTraces() | count", [][]any{{0}})
	k.assert(t, "cao_TraceSampleFrames() | count", [][]any{{0}})
}

func TestKustoEmulatorSampleReadiness(t *testing.T) {
	for _, count := range []int{0, 2} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			k := newKustoAcceptance(t)
			k.apply(t, adxtrace.Schema())
			config := kustoTraceConfig(t, "sample", count)
			const namespace = "sample-traces"
			worker, err := adxtrace.New(t.Context(), config, namespace, k.client)
			if err != nil {
				t.Fatal(err)
			}

			evidence := map[string]adxtrace.Evidence{}
			for run := 1; run <= 3; run++ {
				e := captureKustoTrace(t, worker, config, run, fmt.Sprintf("synthetic run %d", run))
				evidence[e.ID] = e
			}
			k.assert(t, "cao_TraceSampleFrames() | count", [][]any{{0}})
			drainKustoTraces(t, worker)
			k.assert(t, "cao_ReadyTraces() | count", [][]any{{count}})
			k.assert(t, "cao_TraceSampleFrames() | project Mode, Population, Selected", [][]any{{"sample", 3, count}})
			k.assert(t, "cao_TraceSampleMembership() | count", [][]any{{count}})
			if count == 0 {
				return
			}
			k.assert(t, "cao_TraceSampleMembership() | project Probability, Weight",
				[][]any{{float64(2) / 3, 1.5}, {float64(2) / 3, 1.5}})
			for _, row := range k.query(t, "cao_TraceSampleMembership() | project TraceId") {
				id, ok := row[0].(string)
				if !ok {
					t.Fatal("missing selected trace ID")
				}
				assertKustoDocuments(t, k, namespace, evidence[id])
			}
			if err := worker.WithdrawRepository(t.Context(), "octo/api"); err != nil {
				t.Fatal(err)
			}
			drainKustoTraces(t, worker)
			k.assert(t, "cao_TraceSampleFrames() | count", [][]any{{0}})
			k.assert(t, "cao_TraceSampleMembership() | count", [][]any{{0}})
		})
	}
}

func TestKustoEmulatorSampleSourceGap(t *testing.T) {
	k := newKustoAcceptance(t)
	k.apply(t, adxtrace.Schema())
	config := kustoTraceConfig(t, "sample", 2)
	worker, err := adxtrace.New(t.Context(), config, "incomplete-sample", k.client)
	if err != nil {
		t.Fatal(err)
	}
	captureKustoTrace(t, worker, config, 1, "synthetic eligible input")
	shards := t.TempDir()
	day := time.Now().UTC().Add(-72 * time.Hour).Format(time.DateOnly)
	shard := fmt.Appendf(nil,
		`{"kind":"run","run":{"run_id":99,"run_attempt":1,"repository":"octo/api","status":"completed","completed_at":"%sT00:00:00Z"}}`, day)
	if err := os.WriteFile(filepath.Join(shards, "repo-1.jsonl"), shard, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := worker.Capture(t.Context(), "octo/api", t.TempDir(), filepath.Join(shards, "repo-")); err != nil {
		t.Fatal(err)
	}
	drainKustoTraces(t, worker)
	k.assert(t, "TraceSampleFrames | project Population, Selected, SourceGaps, State", [][]any{{1, 1, 1, "incomplete"}})
	k.assert(t, "cao_ReadyTraces() | count", [][]any{{1}})
	k.assert(t, "cao_TraceSampleFrames() | count", [][]any{{0}})
	k.assert(t, "cao_TraceSampleMembership() | count", [][]any{{0}})
}
