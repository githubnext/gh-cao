package adxtrace

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/model"
)

type simulator struct {
	rows     map[string][]model.Row
	fail     string
	early    bool
	requests int
	maxBytes int
}

func (s *simulator) Capacity(context.Context) error { return nil }
func (s *simulator) Write(_ context.Context, table string, rows []model.Row) error {
	content, _ := json.Marshal(rows)
	s.requests++
	s.maxBytes = max(s.maxBytes, len(content))
	if len(content) > 1<<20 {
		return errors.New("oversized request")
	}
	for _, row := range rows {
		content, _ := json.Marshal(row)
		var copy model.Row
		if err := decode(content, &copy); err != nil {
			return err
		}
		s.rows[table] = append(s.rows[table], copy)
		if table == "TraceManifest" && row["State"] == "ready" {
			if len(s.rows["TraceEvents"]) == 0 || len(s.rows["TraceContentParts"]) == 0 {
				s.early = true
			}
		}
	}
	if s.fail == table {
		s.fail = ""
		return errors.New("ambiguous simulated response")
	}
	return nil
}
func (s *simulator) VerifyEvidence(_ context.Context, namespace, id, version string, tables map[string][]model.Row) error {
	for table, expected := range tables {
		var actual []model.Row
		for _, row := range s.rows[table] {
			if row["Namespace"] == namespace && row["RunId"] == id && row["ProjectionId"] == version {
				if (table == "TraceDatasets" || table == "TraceMiningManifest") && scalarNumber(row["SourceRevision"]) != scalarNumber(expected[0]["SourceRevision"]) {
					continue
				}
				actual = append(actual, row)
			}
		}
		if rowSetDigest(actual) != rowSetDigest(expected) {
			return errors.New("not visible")
		}
	}
	return nil
}

func configFor(t *testing.T, mode string) Config {
	t.Helper()
	c := Config{Enabled: true, Directory: t.TempDir(), DurableStorage: true,
		Repositories: []string{"octo/api"}, Mode: mode, SampleCount: 2, Seed: "fixed-predeclared-seed",
		Policy: Policy{Version: "approved-v1", ContentApproved: true, RightsApproved: true, Events: map[string][]string{
			"session.format": {"version"}, "workflow.info": {"engineId", "engineHarness", "agentVersion", "episode", "workflowRef", "callerWorkflowRef", "workflowCallId"},
			"user.message": {"content", "sessionId"}, "assistant.message": {"content", "channel", "sessionId"},
			"assistant.reasoning": {"content"}, "tool.execution_start": {"input", "toolCallId", "toolName"},
			"tool.execution_complete": {"output", "error", "success", "toolCallId", "toolName"},
		}}}
	// #nosec G302 -- directories need owner traversal; no group/other access.
	if err := os.Chmod(c.Directory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	return c
}

var clock = time.Date(2026, 1, 5, 12, 0, 0, 0, time.UTC)

func workerFor(t *testing.T, c Config) (*Worker, *simulator) {
	t.Helper()
	s := &simulator{rows: map[string][]model.Row{}}
	w, err := New(t.Context(), c, "destination", s)
	if err != nil {
		t.Fatal(err)
	}
	w.now = func() time.Time { return clock }
	return w, s
}

func source(t *testing.T, content string) []byte {
	t.Helper()
	events := []model.Row{
		{"type": "session.format", "data": model.Row{"version": 1}},
		{"id": "workflow", "type": "workflow.info", "data": model.Row{
			"engineId": "copilot", "engineHarness": "copilot-sdk", "agentVersion": "1.2",
			"workflowRef": "octo/api/.github/workflows/work.lock.yml@sha", "callerWorkflowRef": "caller@other",
			"workflowCallId": "invocation", "episode": model.Row{"episodeId": "episode", "hopId": "hop", "parentHopId": "parent"},
		}},
		{"id": "user", "type": "user.message", "data": model.Row{"content": content, "sessionId": "native-session"}},
		{"id": "tool", "parentId": "user", "type": "tool.execution_start", "data": model.Row{
			"toolCallId": "call", "toolName": "read", "input": model.Row{"path": "file", "password": "PRIVATE"},
		}},
		{"id": "result", "parentId": "tool", "type": "tool.execution_complete", "data": model.Row{
			"toolCallId": "call", "toolName": "read", "success": false, "output": "output", "error": nil,
		}},
		{"type": "assistant.reasoning", "data": model.Row{"content": "NEVER_TRAIN_THIS"}},
		{"type": "assistant.message", "data": model.Row{"content": "answer", "channel": "final"}},
	}
	var b bytes.Buffer
	for _, event := range events {
		if err := json.NewEncoder(&b).Encode(event); err != nil {
			t.Fatal(err)
		}
	}
	return b.Bytes()
}

func admit(t *testing.T, w *Worker, run, content string) Evidence {
	t.Helper()
	e, err := Project(w.spool.config, "octo/api", run, "2026-01-01", 1, source(t, content), clock)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.spool.admit(t.Context(), e, "", clock); err != nil {
		t.Fatal(err)
	}
	return e
}

func drain(t *testing.T, w *Worker) {
	t.Helper()
	for i := 0; i < 100; i++ {
		more, err := w.Step(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if !more {
			return
		}
	}
	t.Fatal("worker did not drain")
}

func stateFor(t *testing.T, w *Worker) ledger {
	t.Helper()
	var result ledger
	if err := w.spool.locked(t.Context(), func(_ Objects, state *ledger) error {
		content, err := json.Marshal(state)
		if err != nil {
			return err
		}
		return decode(content, &result)
	}); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestProjectionFidelityRedactionAndUnknownExtensions(t *testing.T) {
	c := configFor(t, "full")
	e, err := Project(c, "octo/api", "42", "2026-01-01", 2, source(t, strings.Repeat("é", 40000)+" ghp_abcdefghijklmnopqrstuvwxyz123456"), clock)
	if err != nil {
		t.Fatal(err)
	}
	content, _ := json.Marshal(e)
	if bytes.Contains(content, []byte("PRIVATE")) || bytes.Contains(content, []byte("ghp_abcdefghijklmnopqrstuvwxyz")) {
		t.Fatal("secret escaped redaction")
	}
	events, err := reconstruct(e)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 7 || events[4]["data"].(map[string]any)["error"] != nil {
		t.Fatal("lost order, explicit null or event")
	}
	if got := scalar(events[2]["data"].(map[string]any)["content"]); !strings.HasPrefix(got, strings.Repeat("é", 40000)) {
		t.Fatal("UTF-8 content split corrupted evidence")
	}
	if e.Tables["TraceEvents"][4]["EpisodeId"] != "episode" || e.Tables["TraceEvents"][4]["ParentHopId"] != "parent" {
		t.Fatal("lost supplied execution lineage")
	}
	bad := append(source(t, "x"), []byte("{\"type\":\"extension.new\",\"data\":{\"unapproved\":\"PRIVATE\"}}\n")...)
	e, err = Project(c, "octo/api", "42", "2026-01-01", 2, bad, clock)
	if err != nil || !slices.Contains(e.Gaps, "unapproved-event") {
		t.Fatal("unknown extension not explicit")
	}
	content, _ = json.Marshal(e)
	if bytes.Contains(content, []byte("unapproved\\\"")) {
		t.Fatal("unknown content persisted")
	}
	if _, err := Project(c, "octo/api", "42", "2026-01-01", 2, []byte("{\"type\":\"session.format\",\"data\":{\"version\":99}}"), clock); err == nil {
		t.Fatal("unsupported format accepted")
	}
}

func TestRetryRestartCorrectionAndWithdrawal(t *testing.T) {
	c := configFor(t, "full")
	w, s := workerFor(t, c)
	e := admit(t, w, "42", strings.Repeat("large body ", 200000))
	s.fail = "TraceContentParts"
	if _, err := w.Step(t.Context()); err == nil {
		t.Fatal("expected ambiguous write")
	}
	if len(s.rows["TraceManifest"]) != 0 {
		t.Fatal("published partial trace")
	}
	restarted, err := New(t.Context(), c, "destination", s)
	if err != nil {
		t.Fatal(err)
	}
	restarted.now = w.now
	drain(t, restarted)
	if s.early || s.maxBytes > 1<<20 || s.requests < 4 {
		t.Fatal("batch or ready ordering violation")
	}
	if stateFor(t, restarted).Records[e.ID].Delivered == "" {
		t.Fatal("restart lost pending trace")
	}
	correction := admit(t, restarted, "42", "corrected")
	if correction.Version == e.Version {
		t.Fatal("correction did not create immutable version")
	}
	drain(t, restarted)
	if stateFor(t, restarted).Frames[restarted.spool.frameID("octo/api", "2026-01-01")].State != "invalidated" {
		t.Fatal("correction retained frozen frame readiness")
	}
	if err := restarted.WithdrawRepository(t.Context(), "octo/api"); err != nil {
		t.Fatal(err)
	}
	drain(t, restarted)
	st := stateFor(t, restarted)
	if st.Records[e.ID].File != "" || !st.Withdrawn["octo/api"] {
		t.Fatal("withdrawal lost no-replay intent")
	}
	if err := restarted.spool.admit(t.Context(), correction, "", clock); err == nil {
		t.Fatal("withdrawal allowed replay")
	}
	if last := s.rows["TraceManifest"][len(s.rows["TraceManifest"])-1]; last["State"] != "withdrawn" {
		t.Fatal("missing destination tombstone")
	}
}

func TestEvidenceIdentityCorrectionReplacesCompatibleSpool(t *testing.T) {
	for _, mode := range []string{"full", "sample"} {
		t.Run(mode, func(t *testing.T) {
			c := configFor(t, mode)
			w, s := workerFor(t, c)
			artifact := source(t, "unchanged approved trajectory")
			current, err := Project(c, "octo/api", "42", "2026-01-01", 1, artifact, clock)
			if err != nil {
				t.Fatal(err)
			}
			previous := current
			// Exercise the previous evidence hash inside a compatible spool,
			// not migration of a previous policy fingerprint.
			tables := map[string][]model.Row{}
			for table, rows := range current.Tables {
				if table != "TraceManifest" {
					tables[table] = rows
				}
			}
			projected, err := json.Marshal(tables)
			if err != nil {
				t.Fatal(err)
			}
			previous.Version = digest([]byte(current.ArtifactDigest + current.PolicyDigest + digest(projected)))
			if err := w.spool.admit(t.Context(), previous, "", clock); err != nil {
				t.Fatal(err)
			}
			drain(t, w)
			before := stateFor(t, w).Records[current.ID]
			spec := DatasetSpec{ID: "identity-correction", Transform: "reviewed-transform-v1", Evaluator: "human-review-v1",
				TrainingApproved: true, ToolSchemaEvidence: "reviewed-config:sha",
				ToolSchemas: []map[string]any{{"name": "read", "parameters": map[string]any{"type": "object"}}},
				Entries: []DatasetEntry{{TraceID: previous.ID, TraceVersion: previous.Version, TaskGroup: "episode",
					Split: "train", QualityLabel: "accepted", LabelEvidence: "review:123"}}}
			content, err := json.Marshal(spec)
			if err != nil {
				t.Fatal(err)
			}
			datasetPath, err := exportDatasetAt(t.Context(), c, "destination", content, clock)
			if err != nil {
				t.Fatal(err)
			}
			drain(t, w)
			restarted, err := New(t.Context(), c, "destination", s)
			if err != nil {
				t.Fatal(err)
			}
			restarted.now = func() time.Time { return clock.Add(time.Minute) }
			if err := restarted.CaptureSession(t.Context(), "octo/api", "42", 1, clock.AddDate(0, 0, -4), artifact, ""); err != nil {
				t.Fatal(err)
			}
			state := stateFor(t, restarted)
			after := state.Records[current.ID]
			if after.Version != current.Version || after.ArtifactDigest != before.ArtifactDigest || after.PolicyDigest != before.PolicyDigest ||
				after.Revision <= before.Revision || after.Delivered != "" || after.File == before.File {
				t.Fatal("changed evidence identity was skipped instead of replacing the same artifact and policy")
			}
			for _, path := range []string{filepath.Join(c.Directory, before.File), datasetPath} {
				if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("stale approved or derived payload retained: %v", err)
				}
			}
			for _, d := range state.Datasets {
				if d.State != "invalidated" || d.File != "" {
					t.Fatal("identity correction did not invalidate the frozen dataset")
				}
			}
			f := state.Frames[restarted.spool.frameID("octo/api", "2026-01-01")]
			if f.State != "invalidated" || f.Members[current.ID] != previous.Version {
				t.Fatal("identity correction retained readiness or rewrote frozen membership")
			}
			drain(t, restarted)
			delivered := stateFor(t, restarted).Records[current.ID]
			last := s.rows["TraceManifest"][len(s.rows["TraceManifest"])-1]
			if delivered.Delivered == "" || last["ProjectionId"] != current.Version || last["State"] != "ready" {
				t.Fatal("replacement was not delivered under its new immutable identity")
			}
			eventCount := len(s.rows["TraceEvents"])
			restarted.now = func() time.Time { return clock.Add(2 * time.Minute) }
			if err := restarted.CaptureSession(t.Context(), "octo/api", "42", 1, clock.AddDate(0, 0, -4), artifact, ""); err != nil {
				t.Fatal(err)
			}
			drain(t, restarted)
			repeated := stateFor(t, restarted).Records[current.ID]
			if repeated.Revision != delivered.Revision || repeated.Delivered != delivered.Delivered || len(s.rows["TraceEvents"]) != eventCount {
				t.Fatal("repeat acquisition changed or redelivered an unchanged evidence identity")
			}
		})
	}
}

func TestPreviousPreservationSpoolFailsClosedWithoutMutation(t *testing.T) {
	c := configFor(t, "full")
	c.Repositories = []string{"octo/api", "octo/withdrawn"}
	w, s := workerFor(t, c)
	e := admit(t, w, "42", "retained approved evidence")
	if err := w.CaptureSession(t.Context(), "octo/withdrawn", "43", 1, clock.AddDate(0, 0, -4), source(t, "withdrawn evidence"), ""); err != nil {
		t.Fatal(err)
	}
	if err := w.WithdrawRepository(t.Context(), "octo/withdrawn"); err != nil {
		t.Fatal(err)
	}
	priorPolicy, err := json.Marshal(struct {
		Transform string
		Policy    Policy
	}{"cao-approved-events-v1", c.Policy})
	if err != nil {
		t.Fatal(err)
	}
	priorIdentity, err := json.Marshal(struct {
		Namespace, Policy, Mode, Seed string
		Count                         int
		Repositories                  []string
		MaxTrace                      int64
		Delay                         time.Duration
		Mining                        bool `json:",omitempty"`
	}{"destination", digest(priorPolicy), c.Mode, c.Seed, c.SampleCount,
		c.Repositories, c.MaxTraceBytes, c.FrameDelay, c.MiningEnabled})
	if err != nil {
		t.Fatal(err)
	}
	var files = map[string][]byte{}
	if err := w.spool.locked(t.Context(), func(root Objects, state *ledger) error {
		state.Fingerprint = digest(priorIdentity)
		if err := w.spool.save(root, state); err != nil {
			return err
		}
		for _, file := range []string{"ledger.json", state.Records[e.ID].File} {
			content, err := root.Read(file, c.MaxSpoolBytes)
			if err != nil {
				return err
			}
			files[file] = content
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := New(t.Context(), c, "destination", s); err == nil || err.Error() != "trace ledger is corrupt, incompatible or outside budget" {
		t.Fatalf("previous transform spool was not rejected at the compatibility boundary: %v", err)
	}
	for file, before := range files {
		after, err := os.ReadFile(filepath.Join(c.Directory, file)) // #nosec G304 -- names were read from this test's private spool.
		if err != nil || !bytes.Equal(before, after) {
			t.Fatalf("startup rejection changed approved payload or withdrawal ledger: %v", err)
		}
	}
}

func TestFrozenWholeTraceSamplingDeterminismAndNoRefill(t *testing.T) {
	c := configFor(t, "sample")
	w, s := workerFor(t, c)
	for i := 0; i < 5; i++ {
		admit(t, w, strconv.Itoa(i+1), "same approved trajectory")
	}
	drain(t, w)
	st := stateFor(t, w)
	f := st.Frames[w.spool.frameID("octo/api", "2026-01-01")]
	if f.Population != 5 || len(f.Members) != 2 || f.State != "ready" {
		t.Fatalf("invalid sample frame: %+v", f)
	}
	if len(s.rows["TraceManifest"]) != 2 {
		t.Fatal("did not retain exactly whole selected traces")
	}
	for _, row := range s.rows["TraceSampleMembership"] {
		if scalar(row["Probability"]) != "0.4" || scalar(row["Weight"]) != "2.5" {
			t.Fatal("invalid inclusion probability or weight")
		}
	}
	other := configFor(t, "sample")
	w2, _ := workerFor(t, other)
	for i := 4; i >= 0; i-- {
		admit(t, w2, strconv.Itoa(i+1), "same approved trajectory")
	}
	drain(t, w2)
	f2 := stateFor(t, w2).Frames[w2.spool.frameID("octo/api", "2026-01-01")]
	a, _ := json.Marshal(f.Members)
	b, _ := json.Marshal(f2.Members)
	if !bytes.Equal(a, b) {
		t.Fatal("selection depends on acquisition order")
	}
	admit(t, w, "99", "late")
	drain(t, w)
	f2 = stateFor(t, w).Frames[f.ID]
	b, _ = json.Marshal(f2.Members)
	if !bytes.Equal(a, b) || f2.State != "invalidated" {
		t.Fatal("late arrival refilled sample")
	}
}

func TestEmptySampleSourceGapAndResourceBudgets(t *testing.T) {
	c := configFor(t, "sample")
	c.SampleCount = 0
	w, s := workerFor(t, c)
	admit(t, w, "1", "available")
	drain(t, w)
	if len(s.rows["TraceSampleMembership"]) != 0 {
		t.Fatal("invented empty-sample weights")
	}
	if row := s.rows["TraceSampleFrames"][0]; scalar(row["Population"]) != "1" || scalar(row["Selected"]) != "0" {
		t.Fatal("missing zero selection frame")
	}
	gap := Evidence{ID: "missing", Repository: "octo/api", RunID: "2", Attempt: 1, Day: "2026-01-02"}
	if err := w.spool.admit(t.Context(), gap, "artifact-unavailable", clock); err != nil {
		t.Fatal(err)
	}
	drain(t, w)
	if stateFor(t, w).Frames[w.spool.frameID("octo/api", "2026-01-02")].State != "incomplete" {
		t.Fatal("source gap claimed complete")
	}
	tiny := configFor(t, "full")
	tiny.MaxSpoolBytes = 2 * tiny.MaxTraceBytes
	tiny.MaxEntries = 1
	bounded, _ := workerFor(t, tiny)
	admit(t, bounded, "1", "first")
	if _, err := bounded.Step(t.Context()); !errors.Is(err, ErrCapacity) {
		t.Fatal("entry budget not enforced")
	}
}

func TestDatasetLineageReasoningExclusionAndInvalidation(t *testing.T) {
	c := configFor(t, "full")
	w, _ := workerFor(t, c)
	e := admit(t, w, "42", "question")
	drain(t, w)
	spec := DatasetSpec{ID: "evaluation-v1", Transform: "reviewed-transform-v1", Evaluator: "human-review-v1", TrainingApproved: true, ToolSchemaEvidence: "reviewed-config:sha",
		ToolSchemas: []map[string]any{{"name": "read", "parameters": map[string]any{"type": "object"}}},
		Entries:     []DatasetEntry{{TraceID: e.ID, TraceVersion: e.Version, TaskGroup: "episode", Split: "train", QualityLabel: "accepted", LabelEvidence: "review:123"}}}
	content, _ := json.Marshal(spec)
	path, err := exportDatasetAt(t.Context(), c, "destination", content, clock)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path) // #nosec G304 -- ExportDataset returns a path within this test's private spool.
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte("NEVER_TRAIN_THIS")) || !bytes.Contains(data, []byte(e.ArtifactDigest)) {
		t.Fatal("reasoning leak or missing source lineage")
	}
	drain(t, w)
	admit(t, w, "42", "corrected")
	drain(t, w)
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("stale derived dataset retained")
	}
	if _, err := exportDatasetAt(t.Context(), c, "destination", content, clock); err == nil {
		t.Fatal("stale dataset replay allowed")
	}
}

func TestCaptureBeforeProjectionAndIdentityGaps(t *testing.T) {
	c := configFor(t, "full")
	w, _ := workerFor(t, c)
	root := t.TempDir()
	logs := filepath.Join(root, "logs")
	usage := filepath.Join(logs, "run-42", "usage")
	shards := filepath.Join(root, "shards")
	for _, dir := range []string{usage, shards} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	info := []byte(`{"run_id":42,"run_attempt":"1","repository":"octo/api"}`)
	for path, content := range map[string][]byte{
		filepath.Join(logs, "run-42", "aw_info.json"): info,
		filepath.Join(usage, "aw_session.jsonl"):      source(t, "captured before compaction"),
		filepath.Join(shards, "repo-1.jsonl"):         []byte("{\"kind\":\"run\",\"schema_version\":5,\"run\":{\"run_id\":42,\"run_attempt\":1,\"repository\":\"octo/api\",\"status\":\"completed\",\"updated_at\":\"2026-01-01T10:00:00Z\"}}\n"),
	} {
		if err := os.WriteFile(path, content, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Capture(t.Context(), "octo/api", logs, filepath.Join(shards, "repo-")); err != nil {
		t.Fatal(err)
	}
	st := stateFor(t, w)
	if len(st.Records) != 1 {
		t.Fatal("capture not wired to downloaded evidence")
	}
	for _, r := range st.Records {
		if r.State != "eligible" {
			t.Fatalf("unexpected identity gap: %+v", r)
		}
	}
	c2 := configFor(t, "full")
	w2, _ := workerFor(t, c2)
	if err := os.WriteFile(filepath.Join(logs, "run-42", "aw_info.json"), []byte(`{"run_id":42,"run_attempt":2,"repository":"octo/api"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := w2.Capture(t.Context(), "octo/api", logs, filepath.Join(shards, "repo-")); err != nil {
		t.Fatal(err)
	}
	for _, r := range stateFor(t, w2).Records {
		if r.State != "source-gap" {
			t.Fatal("attempt mismatch associated wrong artifact")
		}
	}
}

func TestDefaultOffAndApprovalAndSpoolCompatibility(t *testing.T) {
	if _, err := FromEnv(func(key string) string {
		if key == "CAO_ADX_TRACE_POLICY" {
			return "/not/a/file"
		}
		return ""
	}); err != nil {
		t.Fatal("disabled path touched policy")
	}
	c := configFor(t, "full")
	for _, change := range []func(*Config){func(c *Config) { c.Policy.ContentApproved = false }, func(c *Config) { c.DurableStorage = false },
		func(c *Config) { c.Mode = "tier" }, func(c *Config) { c.Policy.RedactPatterns = []string{"["} }} {
		next := c
		next.Policy = c.Policy
		change(&next)
		if next.Validate() == nil {
			t.Fatal("unsafe config accepted")
		}
	}
	w, _ := workerFor(t, c)
	admit(t, w, "42", "x")
	c.Policy.Version = "changed-policy"
	if _, err := New(t.Context(), c, "destination", &simulator{}); err == nil {
		t.Fatal("silently changed frozen evidence policy")
	}
}
