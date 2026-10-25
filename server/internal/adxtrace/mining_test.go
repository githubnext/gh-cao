package adxtrace

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/mining"
	"github.com/githubnext/gh-cao/server/internal/model"
	"github.com/githubnext/gh-cao/server/internal/sessionexport"
)

type miningSource struct {
	revision int64
	calls    int
	change   func(*sessionexport.MiningSnapshot)
}

func (s *miningSource) ReadMining(_ context.Context, repository, run string, attempt int64) (sessionexport.MiningSnapshot, error) {
	s.calls++
	canonical := "github:run:" + repository + ":" + run
	member := mining.Membership{Repository: repository, RunID: run, Attempt: int(attempt), ObservedAt: clock.Add(-time.Hour),
		Context: mining.Context{EpisodeID: "episode", HopID: "hop", ParentHopID: "parent", RootRunID: "1", RootRepo: "octo/root"}}
	outcome := mining.Outcome{Repository: "octo/target", Number: 12, PullRequest: true, URL: "https://github.com/octo/target/pull/12",
		State: "closed", Merged: true, UpdatedAt: clock.Add(-2 * time.Hour), ObservedAt: clock.Add(-time.Hour), Source: "github-webhook"}
	result := sessionexport.MiningSnapshot{Namespace: "canonical-source", Revision: s.revision,
		Run:     model.Row{"id": canonical, "repositoryId": "repository:" + model.EncodeCoordinate(repository), "attempt": attempt},
		Audits:  append(member.Audits(), outcome.Audit("output", canonical)),
		Outputs: []model.Row{{"id": "output", "runId": canonical, "safeOutputType": "create_pull_request", "isPullRequest": true, "safeOutputUrl": outcome.URL}}}
	if s.change != nil {
		s.change(&result)
	}
	return result, nil
}

func miningWorkerFor(t *testing.T, mode string) (*Worker, *simulator, *miningSource) {
	t.Helper()
	c := configFor(t, mode)
	if mode == "sample" {
		c.SampleCount = 1
	}
	return miningWorkerConfig(t, c)
}

func miningWorkerConfig(t *testing.T, c Config) (*Worker, *simulator, *miningSource) {
	t.Helper()
	c.MiningEnabled, c.Policy.MiningApproved = true, true
	s := &simulator{rows: map[string][]model.Row{}}
	reader := &miningSource{revision: 1}
	w, err := New(t.Context(), c, "destination", s, reader)
	if err != nil {
		t.Fatal(err)
	}
	w.now = func() time.Time { return clock }
	return w, s, reader
}

func admitCreated(t *testing.T, w *Worker, run string) Evidence {
	t.Helper()
	raw := bytes.ReplaceAll(source(t, "reviewed task"), []byte(`"toolName":"read"`), []byte(`"toolName":"create_pull_request"`))
	raw = bytes.ReplaceAll(raw, []byte(`"success":false`), []byte(`"success":true`))
	raw = bytes.ReplaceAll(raw, []byte(`"output":"output"`), []byte(`"output":{"url":"https://github.com/octo/target/pull/12"}`))
	e, err := Project(w.spool.config, "octo/api", run, "2026-01-01", 1, raw, clock)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.spool.admit(t.Context(), e, "", clock); err != nil {
		t.Fatal(err)
	}
	return e
}

func outcomeSpec(e Evidence, version string) DatasetSpec {
	return DatasetSpec{ID: "reviewed-outcome-corpus", Transform: "text-v1", Evaluator: "independent-human-v1", ToolSchemaEvidence: "historical-config:sha", TrainingApproved: true,
		ToolSchemas: []map[string]any{{"name": "create_pull_request", "parameters": map[string]any{"type": "object"}}},
		Entries: []DatasetEntry{{TraceID: e.ID, TraceVersion: e.Version, MiningVersion: version, TaskGroup: "episode", Split: "train",
			QualityLabel: "reviewed-correct", LabelEvidence: "independent-review:123",
			Outcome: &DatasetOutcome{OutputID: "output", AuditID: "audit:server-mining:outcome:" + model.EncodeCoordinate("output"),
				Disposition: "accepted", RuleVersion: outcomeLabelVersion, Cutoff: clock.Format(time.RFC3339Nano)}}}}
}

func TestMiningIndependentUpdateRetryDatasetAndWithdrawal(t *testing.T) {
	w, s, reader := miningWorkerFor(t, "full")
	e := admitCreated(t, w, "42")
	drain(t, w)
	first := stateFor(t, w).Records[e.ID].Mining
	if first == nil || first.Delivered == "" || len(s.rows["TraceEpisodes"]) != 2 || len(s.rows["TraceOutcomes"]) != 1 {
		t.Fatal("missing verified episode/outcome projection")
	}
	if integer(s.rows["TraceOutcomes"][0]["ProducingAttempt"]) != 1 {
		t.Fatal("exact creation result did not attest its attempt")
	}
	spec := outcomeSpec(e, first.Version)
	encoded, _ := json.Marshal(spec)
	path, err := exportDatasetAt(t.Context(), w.spool.config, "destination", encoded, clock)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path) // #nosec G304 -- private dataset path returned by the exporter.
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte(`"canonicalNamespace":"canonical-source"`)) ||
		!bytes.Contains(data, []byte(`"ProducingAttempt":1`)) || bytes.Contains(data, []byte("NEVER_TRAIN_THIS")) {
		t.Fatal("dataset lost independent outcome/source lineage or leaked reasoning")
	}
	drain(t, w)
	count := len(s.rows["TraceEvents"])
	now := clock.Add(w.spool.config.MiningInterval)
	w.now = func() time.Time { return now }
	reader.revision++
	drain(t, w)
	st := stateFor(t, w)
	if st.Records[e.ID].Mining.Version != first.Version || len(s.rows["TraceEvents"]) != count {
		t.Fatal("unrelated revision or heartbeat rewrote immutable evidence")
	}
	for _, d := range st.Datasets {
		if d.State != "ready" {
			t.Fatal("no-op polling invalidated a frozen dataset")
		}
	}
	reader.revision++
	reader.change = func(snapshot *sessionexport.MiningSnapshot) {
		audit := snapshot.Audits[len(snapshot.Audits)-1]
		audit["status"], audit["code"], audit["decision"] = "pending", "open", "open"
		audit["timestamp"], audit["observedAt"] = now, now
	}
	now = now.Add(w.spool.config.MiningInterval)
	s.fail = "TraceOutcomes"
	for i := 0; i < 10; i++ {
		_, err = w.Step(t.Context())
		if err != nil {
			break
		}
	}
	if err == nil {
		t.Fatal("ambiguous outcome delivery was not exercised")
	}
	restarted, err := New(t.Context(), w.spool.config, "destination", s, reader)
	if err != nil {
		t.Fatal(err)
	}
	restarted.now = w.now
	drain(t, restarted)
	st = stateFor(t, restarted)
	if st.Records[e.ID].Mining.Version == first.Version || len(s.rows["TraceEvents"]) != count {
		t.Fatal("outcome change was not independent of trace acquisition")
	}
	for _, d := range st.Datasets {
		if d.State != "invalidated" || d.File != "" {
			t.Fatal("reopen retained a training label")
		}
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("invalidated local dataset was not removed")
	}
	if err := restarted.WithdrawRepository(t.Context(), "octo/api"); err != nil {
		t.Fatal(err)
	}
	calls := reader.calls
	drain(t, restarted)
	st = stateFor(t, restarted)
	if reader.calls != calls || st.Records[e.ID].Mining.File != "" || st.Records[e.ID].Mining.State != "invalidated" {
		t.Fatal("withdrawal retained mining replay or acquisition")
	}
	latest := s.rows["TraceMiningManifest"][len(s.rows["TraceMiningManifest"])-1]
	if latest["State"] != "invalidated" || integer(latest["OutcomeCount"]) != 0 {
		t.Fatal("mining tombstone missing")
	}
}

func TestMiningBindingsFailClosedAndLegacyOutcomesStayUnknown(t *testing.T) {
	w, _, reader := miningWorkerFor(t, "full")
	trace := admit(t, w, "42", "question")
	r := stateFor(t, w).Records[trace.ID]
	for _, test := range []struct {
		name   string
		change func(*sessionexport.MiningSnapshot)
	}{
		{"namespace", func(s *sessionexport.MiningSnapshot) { s.Namespace = "" }},
		{"repository", func(s *sessionexport.MiningSnapshot) { s.Run["repositoryId"] = "another" }},
		{"run", func(s *sessionexport.MiningSnapshot) { s.Run["id"] = "another" }},
		{"attempt", func(s *sessionexport.MiningSnapshot) { s.Run["attempt"] = 2 }},
		{"unknown-attempt", func(s *sessionexport.MiningSnapshot) { delete(s.Audits[0], "claimRunAttempt") }},
		{"episode", func(s *sessionexport.MiningSnapshot) { s.Audits[0]["correlationId"] = "contradiction" }},
		{"parent", func(s *sessionexport.MiningSnapshot) { s.Audits[0]["code"] = "contradiction" }},
		{"root", func(s *sessionexport.MiningSnapshot) { s.Audits = s.Audits[1:] }},
		{"host", func(s *sessionexport.MiningSnapshot) {
			s.Outputs[0]["safeOutputUrl"] = "https://example.org/octo/target/pull/12"
		}},
		{"category", func(s *sessionexport.MiningSnapshot) { s.Outputs[0]["isPullRequest"] = false }},
		{"url-conflict", func(s *sessionexport.MiningSnapshot) {
			s.Outputs[0]["correlationId"] = "https://github.com/octo/target/pull/13"
		}},
		{"output-scope", func(s *sessionexport.MiningSnapshot) { s.Outputs = nil }},
		{"future-rule", func(s *sessionexport.MiningSnapshot) { s.Audits[2]["code"] = "all-green" }},
		{"duplicate-audit", func(s *sessionexport.MiningSnapshot) { s.Audits = append(s.Audits, s.Audits[0]) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			snapshot, _ := reader.ReadMining(t.Context(), "octo/api", "42", 1)
			test.change(&snapshot)
			if _, _, err := projectMining(snapshot, r, trace, clock, time.Minute); err == nil {
				t.Fatal("invalid source binding accepted")
			}
		})
	}
	snapshot, _ := reader.ReadMining(t.Context(), "octo/api", "42", 1)
	delete(snapshot.Audits[2], "payloadRef")
	result, _, err := projectMining(snapshot, r, trace, clock, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if result.Tables["TraceOutcomes"][0]["Status"] != "unknown" || integer(result.Tables["TraceOutcomes"][0]["ProducingAttempt"]) != 0 {
		t.Fatal("legacy URL or current selected attempt was inferred")
	}
	snapshot.Run, snapshot.Audits, snapshot.Outputs = nil, nil, nil
	result, _, err = projectMining(snapshot, r, trace, clock, time.Minute)
	if err != nil || result.State != "unavailable" || len(result.Tables) != 0 {
		t.Fatal("missing/superseded run did not become unavailable")
	}
}

func TestMiningSourceLossAndNamespaceDriftInvalidate(t *testing.T) {
	for _, kind := range []string{"loss", "namespace", "revision", "context"} {
		t.Run(kind, func(t *testing.T) {
			w, s, reader := miningWorkerFor(t, "full")
			e := admitCreated(t, w, "42")
			drain(t, w)
			spec, _ := json.Marshal(outcomeSpec(e, stateFor(t, w).Records[e.ID].Mining.Version))
			if _, err := exportDatasetAt(t.Context(), w.spool.config, "destination", spec, clock); err != nil {
				t.Fatal(err)
			}
			w.now = func() time.Time { return clock.Add(w.spool.config.MiningInterval) }
			reader.revision = 2
			reader.change = func(snapshot *sessionexport.MiningSnapshot) {
				switch kind {
				case "loss":
					snapshot.Run, snapshot.Audits, snapshot.Outputs = nil, nil, nil
				case "namespace":
					snapshot.Namespace = "different-source"
				case "revision":
					snapshot.Revision = 0
				case "context":
					snapshot.Audits[0]["correlationId"] = "conflicting"
				}
			}
			calls := reader.calls
			var err error
			for i := 0; i < 10 && reader.calls == calls; i++ {
				_, err = w.Step(t.Context())
				if err != nil {
					break
				}
			}
			if reader.calls == calls {
				t.Fatal("independent refresh did not execute")
			}
			if (err != nil) != (kind != "loss") {
				t.Fatalf("unexpected refresh error: %v", err)
			}
			drain(t, w)
			st := stateFor(t, w)
			for _, d := range st.Datasets {
				if d.State != "invalidated" || d.File != "" {
					t.Fatal("lost source retained dataset")
				}
			}
			latest := s.rows["TraceMiningManifest"][len(s.rows["TraceMiningManifest"])-1]
			if latest["State"] == "ready" {
				t.Fatal("source loss or contradiction remained ready")
			}
			if st.MiningNamespace != "canonical-source" {
				t.Fatal("source namespace drift was adopted")
			}
		})
	}
}

func TestOutcomeDatasetRequiresIndependentQualityExactAttemptAndCurrentCutoff(t *testing.T) {
	w, _, _ := miningWorkerFor(t, "full")
	e := admitCreated(t, w, "42")
	drain(t, w)
	for _, test := range []struct {
		name   string
		change func(*DatasetSpec)
		now    time.Time
	}{
		{"quality", func(s *DatasetSpec) { s.Entries[0].LabelEvidence = "" }, clock},
		{"version", func(s *DatasetSpec) { s.Entries[0].MiningVersion = "unknown" }, clock},
		{"audit", func(s *DatasetSpec) { s.Entries[0].Outcome.AuditID = "unknown" }, clock},
		{"output", func(s *DatasetSpec) { s.Entries[0].Outcome.OutputID = "unknown" }, clock},
		{"rule", func(s *DatasetSpec) { s.Entries[0].Outcome.RuleVersion = "unknown" }, clock},
		{"pending-negative", func(s *DatasetSpec) { s.Entries[0].Outcome.Disposition = "pending" }, clock},
		{"old-cutoff", func(s *DatasetSpec) { s.Entries[0].Outcome.Cutoff = clock.Add(-time.Hour).Format(time.RFC3339Nano) }, clock},
		{"future-cutoff", func(s *DatasetSpec) { s.Entries[0].Outcome.Cutoff = clock.Add(time.Hour).Format(time.RFC3339Nano) }, clock},
		{"expired", func(*DatasetSpec) {}, clock.Add(time.Hour)},
	} {
		t.Run(test.name, func(t *testing.T) {
			spec := outcomeSpec(e, stateFor(t, w).Records[e.ID].Mining.Version)
			test.change(&spec)
			content, _ := json.Marshal(spec)
			if _, err := exportDatasetAt(t.Context(), w.spool.config, "destination", content, test.now); err == nil {
				t.Fatal("unsupported outcome training label admitted")
			}
		})
	}
	unknown, _, _ := miningWorkerFor(t, "full")
	e = admit(t, unknown, "42", "question")
	drain(t, unknown)
	spec := outcomeSpec(e, stateFor(t, unknown).Records[e.ID].Mining.Version)
	spec.ToolSchemas = []map[string]any{{"name": "read", "parameters": map[string]any{"type": "object"}}}
	content, _ := json.Marshal(spec)
	if _, err := exportDatasetAt(t.Context(), unknown.spool.config, "destination", content, clock); err == nil || !strings.Contains(err.Error(), "exact attempt") {
		t.Fatal("unknown output attempt borrowed the current attempt")
	}
}

func TestMiningSamplingReadsOnlyWholeSelectedTracesAndBoundsWork(t *testing.T) {
	w, s, reader := miningWorkerFor(t, "sample")
	admit(t, w, "42", "one")
	admit(t, w, "43", "two")
	drain(t, w)
	if reader.calls != 1 || len(s.rows["TraceOutcomes"]) != 1 {
		t.Fatal("mining widened frozen sample membership")
	}
	for i := 0; i < 10; i++ {
		if more, err := w.Step(t.Context()); more || err != nil {
			t.Fatal("unchanged polling did not wait for its interval")
		}
	}
	if reader.calls != 1 {
		t.Fatal("polling ignored cadence")
	}
	w.now = func() time.Time { return clock.Add(w.spool.config.MiningInterval) }
	reader.revision++
	drain(t, w)
	if reader.calls != 2 {
		t.Fatal("independent refresh did not resume")
	}
}

func TestOutcomeProgressRejectsOlderAndContradictoryObservations(t *testing.T) {
	old := miningEvidence{Tables: map[string][]model.Row{"TraceOutcomes": {{"OutputId": "x", "ObjectUpdatedAt": clock.Format(time.RFC3339Nano), "Status": "accepted", "Signal": "merged", "ObjectState": "closed"}}}}
	for _, delta := range []int{-1, 0, 1} {
		next := miningEvidence{Tables: map[string][]model.Row{"TraceOutcomes": {{"OutputId": "x", "ObjectUpdatedAt": clock.Add(time.Duration(delta) * time.Second).Format(time.RFC3339Nano), "Status": "pending", "Signal": "open", "ObjectState": "open"}}}}
		if err := checkOutcomeProgress(old, next); (err != nil) != (delta <= 0) {
			t.Fatalf("delta %s: %v", strconv.Itoa(delta), err)
		}
	}
}

func TestMiningRequiresSeparateApprovalAndCanonicalReader(t *testing.T) {
	c := configFor(t, "full")
	c.MiningEnabled = true
	if err := c.Validate(); err == nil {
		t.Fatal("mining enabled without evidence approval")
	}
	c.Policy.MiningApproved = true
	if _, err := New(t.Context(), c, "destination", &simulator{rows: map[string][]model.Row{}}); err == nil {
		t.Fatal("mining started without canonical source")
	}
	files, err := os.ReadDir(c.Directory)
	if err != nil || len(files) != 0 {
		t.Fatal("invalid activation touched private spool")
	}
	c.MiningEnabled = false
	if _, err := New(t.Context(), c, "destination", &simulator{rows: map[string][]model.Row{}}, &miningSource{}); err == nil {
		t.Fatal("disabled mining admitted a reader")
	}
}

func TestMiningRetainedPayloadExpiresWithoutSourceReacquisition(t *testing.T) {
	w, s, reader := miningWorkerFor(t, "full")
	e := admitCreated(t, w, "42")
	drain(t, w)
	calls := reader.calls
	w.now = func() time.Time { return clock.Add(w.spool.config.Retention + time.Hour) }
	drain(t, w)
	r := stateFor(t, w).Records[e.ID]
	if reader.calls != calls || r.Mining.State != "invalidated" || r.Mining.File != "" || r.File != "" {
		t.Fatal("retention retained mining replay or reacquired sources")
	}
	latest := s.rows["TraceMiningManifest"][len(s.rows["TraceMiningManifest"])-1]
	if latest["State"] != "invalidated" {
		t.Fatal("expiry did not retire destination mining")
	}
}

func TestMiningMinedEpisodesPreventCrossSplitLeakage(t *testing.T) {
	c := configFor(t, "full")
	c.Policy.Events["workflow.info"] = []string{"engineId"}
	w, _, _ := miningWorkerConfig(t, c)
	e := admitCreated(t, w, "42")
	drain(t, w)
	spec := outcomeSpec(e, stateFor(t, w).Records[e.ID].Mining.Version)
	content, _ := json.Marshal(spec)
	if _, err := exportDatasetAt(t.Context(), w.spool.config, "destination", content, clock); err != nil {
		t.Fatal(err)
	}

	raw := source(t, "related hop")
	events, err := Project(w.spool.config, "octo/api", "43", "2026-01-01", 1, raw, clock)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range events.Tables["TraceEvents"] {
		if row["EpisodeId"] != "" {
			t.Fatal("fixture unexpectedly admitted native episode context")
		}
	}
	if err := w.spool.admit(t.Context(), events, "", clock); err != nil {
		t.Fatal(err)
	}
	drain(t, w)
	spec.Entries = []DatasetEntry{{TraceID: events.ID, TraceVersion: events.Version, MiningVersion: stateFor(t, w).Records[events.ID].Mining.Version,
		TaskGroup: "different-task", Split: "test", QualityLabel: "reviewed", LabelEvidence: "independent-review"}}
	spec.ToolSchemas = []map[string]any{{"name": "read", "parameters": map[string]any{"type": "object"}}}
	content, _ = json.Marshal(spec)
	if _, err := exportDatasetAt(t.Context(), w.spool.config, "destination", content, clock); err == nil || !strings.Contains(err.Error(), "leakage") {
		t.Fatalf("mined episode split binding was not enforced: %v", err)
	}
}

func TestMiningCreationProofKeepsNativeRunCoordinates(t *testing.T) {
	w, _, reader := miningWorkerFor(t, "full")
	e := admitCreated(t, w, "43")
	drain(t, w)
	r := stateFor(t, w).Records[e.ID]
	snapshot, _ := reader.ReadMining(t.Context(), "octo/api", "43", 1)
	projected, _, err := projectMining(snapshot, r, e, clock, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	row := projected.Tables["TraceOutcomes"][0]
	if row["CanonicalRunId"] != "github:run:octo/api:43" || row["ProducingAttempt"] != int64(1) {
		t.Fatal("native creation result lost exact run/attempt coordinates")
	}
}

func TestMiningCreationProofRejectsAmbiguousCompletions(t *testing.T) {
	start := model.Row{"type": "tool.execution_start", "data": model.Row{"toolCallId": "call", "toolName": "create_pull_request", "input": model.Row{}}}
	success := model.Row{"type": "tool.execution_complete", "data": model.Row{"toolCallId": "call", "success": true, "output": model.Row{"url": "https://github.com/octo/target/pull/12"}}}
	failure := model.Row{"type": "tool.execution_complete", "data": model.Row{"toolCallId": "call", "success": false, "error": "failed"}}
	conflict := model.Row{"type": "tool.execution_complete", "data": model.Row{"toolCallId": "call", "success": true, "output": model.Row{"url": "https://github.com/octo/target/pull/13"}}}
	for name, events := range map[string][]model.Row{
		"duplicate": {start, success, success}, "conflicting": {start, success, conflict},
		"failed-then-successful": {start, failure, success}, "completion-before-start": {success, start, success},
	} {
		t.Run(name, func(t *testing.T) {
			var raw bytes.Buffer
			for _, event := range events {
				if err := json.NewEncoder(&raw).Encode(event); err != nil {
					t.Fatal(err)
				}
			}
			trace, err := Project(configFor(t, "full"), "octo/api", "42", "2026-01-01", 1, raw.Bytes(), clock)
			if err != nil {
				t.Fatal(err)
			}
			if len(traceOutputProofs(trace)) != 0 {
				t.Fatal("ambiguous creation completion attested a producing attempt")
			}
		})
	}
}
