package adxtrace

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestSampleCountMustBeExplicitAndEntryBudgetIsConfigurable(t *testing.T) {
	c := configFor(t, "sample")
	policy, err := json.Marshal(c.Policy)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "policy.json")
	if err := os.WriteFile(path, policy, 0o600); err != nil {
		t.Fatal(err)
	}
	values := map[string]string{
		"CAO_ADX_TRACES_ENABLED": "true", "CAO_ADX_TRACE_DIRECTORY": c.Directory,
		"CAO_ADX_TRACE_DURABLE_STORAGE": "true", "CAO_ADX_TRACE_POLICY": path,
		"CAO_ADX_TRACE_MODE": "sample", "CAO_ADX_TRACE_SEED": c.Seed,
		"CAO_ADX_TRACE_REPOSITORIES": "octo/api", "CAO_ADX_TRACE_MAX_ENTRIES": "25000",
	}
	get := func(key string) string { return values[key] }
	if _, err := FromEnv(get); err == nil {
		t.Fatal("missing sample count silently selected no traces")
	}
	values["CAO_ADX_TRACE_SAMPLE_COUNT"] = "0"
	got, err := FromEnv(get)
	if err != nil || got.SampleCount != 0 || got.MaxEntries != 25000 {
		t.Fatalf("explicit zero selection or capacity rejected: %v", err)
	}
	values["CAO_ADX_TRACE_MAX_ENTRIES"] = "100001"
	if _, err := FromEnv(get); err == nil {
		t.Fatal("unbounded ledger admitted")
	}
}

func TestFullCaptureRetainsLateArrivalsOutsideFrozenFrame(t *testing.T) {
	w, s := workerFor(t, configFor(t, "full"))
	admit(t, w, "1", "initial")
	drain(t, w)
	e := admit(t, w, "2", "late but retained")
	drain(t, w)
	state := stateFor(t, w)
	if state.Records[e.ID].State != "eligible" || state.Records[e.ID].File == "" {
		t.Fatal("full mode discarded approved late evidence")
	}
	if state.Frames[w.spool.frameID("octo/api", "2026-01-01")].State != "invalidated" {
		t.Fatal("late arrival did not invalidate the frozen frame")
	}
	found := false
	for _, row := range s.rows["TraceManifest"] {
		found = found || row["RunId"] == e.ID && row["State"] == "ready"
	}
	if !found {
		t.Fatal("full mode failed to deliver a late trace")
	}
}

func TestMalformedUTF8CannotSilentlyChangeApprovedContent(t *testing.T) {
	c := configFor(t, "full")
	raw := bytes.Replace(source(t, "question"), []byte("question"), []byte{0xff}, 1)
	if _, err := Project(c, "octo/api", "1", "2026-01-01", 1, raw, clock); err == nil {
		t.Fatal("invalid UTF-8 source silently repaired")
	}
}
