package adxtrace

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCacheOnlyReusesDurableAdmissions(t *testing.T) {
	c := configFor(t, "full")
	w, _ := workerFor(t, c)
	known := admit(t, w, "42", "retained")
	root, target := t.TempDir(), t.TempDir()
	cache := []byte("{\"kind\":\"run\",\"run\":{\"run_id\":42,\"run_attempt\":1}}\n" +
		"{\"kind\":\"run\",\"run\":{\"run_id\":43,\"run_attempt\":1}}\n" +
		"{\"kind\":\"record\",\"record\":{\"id\":\"structural\"}}\n")
	if err := os.WriteFile(filepath.Join(root, "repo-1.jsonl"), cache, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := w.PrepareCache(t.Context(), "octo/api", filepath.Join(root, "repo-"), filepath.Join(target, "repo-")); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(target, "repo-1.jsonl")) // #nosec G304 -- fixed file within t.TempDir.
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(content, []byte(`"run_id":43`)) || !bytes.Contains(content, []byte(`"run_id":42`)) || !bytes.Contains(content, []byte("structural")) {
		t.Fatal("cached lossy metadata suppressed uncaptured source acquisition")
	}
	original, err := os.ReadFile(filepath.Join(root, "repo-1.jsonl")) // #nosec G304 -- fixed file within t.TempDir.
	if err != nil || !bytes.Equal(original, cache) {
		t.Fatal("staging changed canonical cache")
	}
	st := stateFor(t, w)
	if st.Records[known.ID].State != "eligible" {
		t.Fatal("staging changed evidence lifecycle")
	}
}

func TestDatasetGroupFreezeSurvivesLaterExports(t *testing.T) {
	c := configFor(t, "full")
	w, _ := workerFor(t, c)
	one := admit(t, w, "42", "first")
	two := admit(t, w, "43", "same episode")
	drain(t, w)
	spec := DatasetSpec{ID: "frozen-evaluation", Transform: "v1", Evaluator: "review", TrainingApproved: true, ToolSchemaEvidence: "reviewed-config:sha",
		ToolSchemas: []map[string]any{{"name": "read", "parameters": map[string]any{"type": "object"}}},
		Entries:     []DatasetEntry{{TraceID: one.ID, TraceVersion: one.Version, TaskGroup: "episode", Split: "test", QualityLabel: "accepted", LabelEvidence: "review:1"}}}
	content, _ := json.Marshal(spec)
	if _, err := exportDatasetAt(t.Context(), c, "destination", content, clock); err != nil {
		t.Fatal(err)
	}
	spec.Entries = []DatasetEntry{{TraceID: two.ID, TraceVersion: two.Version, TaskGroup: "different-group", Split: "train", QualityLabel: "accepted", LabelEvidence: "review:2"}}
	content, _ = json.Marshal(spec)
	if _, err := exportDatasetAt(t.Context(), c, "destination", content, clock); err == nil {
		t.Fatal("same episode escaped held-out grouping across exports")
	}
	spec.Entries[0].TaskGroup = "episode"
	content, _ = json.Marshal(spec)
	if _, err := exportDatasetAt(t.Context(), c, "destination", content, clock); err == nil {
		t.Fatal("held-out group moved into training in a later export")
	}
}

func TestExpiryInvalidatesDatasetAndPreservesNoReplayTombstones(t *testing.T) {
	c := configFor(t, "full")
	w, _ := workerFor(t, c)
	e := admit(t, w, "42", "retained")
	drain(t, w)
	w.now = func() time.Time { return clock.Add(c.Retention + time.Hour) }
	drain(t, w)
	st := stateFor(t, w)
	if st.Records[e.ID].State != "expired" || st.Records[e.ID].File != "" {
		t.Fatal("retention did not remove content")
	}

	if st.Frames[w.spool.frameID("octo/api", "2026-01-01")].State != "invalidated" {
		t.Fatal("expired sample retained readiness")
	}
	if err := w.spool.admit(t.Context(), e, "", w.now()); err != nil {
		t.Fatal(err)
	}
	if stateFor(t, w).Records[e.ID].State != "expired" {
		t.Fatal("source replay revived expired content")
	}
}

func TestTrainingContentExcludesReasoningAndUnknownFields(t *testing.T) {
	event := map[string]any{"type": "assistant.message", "data": map[string]any{
		"channel": "final", "reasoningText": "NEVER_TRAIN_EXTENSION",
		"content": []any{
			map[string]any{"type": "thinking", "thinking": "NEVER_TRAIN_BLOCK"},
			map[string]any{"type": "text", "text": "answer", "analysis": "NEVER_TRAIN_BLOCK_EXTENSION"},
		},
	}}
	projected, err := trainingEvent(event, "assistant")
	if err != nil {
		t.Fatal(err)
	}
	content, err := json.Marshal(projected)
	if err != nil || bytes.Contains(content, []byte("NEVER_TRAIN")) || !bytes.Contains(content, []byte("answer")) {
		t.Fatal("text dataset leaked reasoning or omitted supported content")
	}
	event["data"].(map[string]any)["content"] = []any{map[string]any{"type": "unknown", "text": "unsupported"}}
	if _, err := trainingEvent(event, "assistant"); err == nil {
		t.Fatal("unsupported block silently admitted")
	}
}

func TestDatasetRejectsIncompleteAndAmbiguousTrajectories(t *testing.T) {
	for _, scenario := range []string{"unmatched-tool", "reused-call", "unfinished-turn", "schema-evidence", "invalid-schema"} {
		t.Run(scenario, func(t *testing.T) {
			c := configFor(t, "full")
			w, _ := workerFor(t, c)
			raw := source(t, "question")
			switch scenario {
			case "unmatched-tool":
				raw = bytes.ReplaceAll(raw, []byte(`"type":"tool.execution_complete"`), []byte(`"type":"assistant.reasoning"`))
			case "reused-call":
				lines := bytes.Split(bytes.TrimSpace(raw), []byte("\n"))
				extra := bytes.Join([][]byte{lines[3], lines[4], lines[6]}, []byte("\n"))
				raw = append(raw, extra...)
			case "unfinished-turn":
				raw = append(raw, []byte("{\"type\":\"user.message\",\"data\":{\"content\":\"unfinished\"}}\n")...)
			}
			e, err := Project(c, "octo/api", "42", "2026-01-01", 1, raw, clock)
			if err != nil {
				t.Fatal(err)
			}
			if err := w.spool.admit(t.Context(), e, "", clock); err != nil {
				t.Fatal(err)
			}
			drain(t, w)
			spec := DatasetSpec{ID: "reviewed", Transform: "v1", Evaluator: "human", TrainingApproved: true, ToolSchemaEvidence: "historical-config:sha",
				ToolSchemas: []map[string]any{{"name": "read", "parameters": map[string]any{"type": "object"}}},
				Entries:     []DatasetEntry{{TraceID: e.ID, TraceVersion: e.Version, TaskGroup: "episode", Split: "test", QualityLabel: "accepted", LabelEvidence: "review:1"}}}
			if scenario == "schema-evidence" {
				spec.ToolSchemaEvidence = ""
			}
			if scenario == "invalid-schema" {
				spec.ToolSchemas[0]["parameters"] = "not-a-schema"
			}
			content, _ := json.Marshal(spec)
			if _, err := exportDatasetAt(t.Context(), c, "destination", content, clock); err == nil {
				t.Fatal("invalid dataset accepted")
			}
		})
	}
}
