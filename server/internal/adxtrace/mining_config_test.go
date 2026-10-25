package adxtrace

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/sessionexport"
)

func TestMiningEnvironmentRequiresBothActivationAndReviewedApproval(t *testing.T) {
	c := configFor(t, "full")
	path := filepath.Join(t.TempDir(), "policy.json")
	write := func() {
		t.Helper()
		data, err := json.Marshal(c.Policy)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write()
	values := map[string]string{
		"CAO_ADX_TRACES_ENABLED": "true", "CAO_ADX_TRACE_DIRECTORY": c.Directory,
		"CAO_ADX_TRACE_DURABLE_STORAGE": "true", "CAO_ADX_TRACE_REPOSITORIES": "octo/api",
		"CAO_ADX_TRACE_POLICY": path, "CAO_ADX_TRACE_MODE": "full",
	}
	get := func(name string) string { return values[name] }
	if got, err := FromEnv(get); err != nil || got.MiningEnabled {
		t.Fatal("ordinary trace capture enabled mining")
	}
	values["CAO_ADX_TRACE_MINING_ENABLED"] = "true"
	if _, err := FromEnv(get); err == nil {
		t.Fatal("mining activation bypassed reviewed approval")
	}
	c.Policy.MiningApproved = true
	write()
	if got, err := FromEnv(get); err != nil || !got.MiningEnabled {
		t.Fatal("explicit approved mining was rejected")
	}
	for _, invalid := range []string{"0", "sometimes"} {
		values["CAO_ADX_TRACE_MINING_ENABLED"] = invalid
		if _, err := FromEnv(get); err == nil {
			t.Fatal("ambiguous activation enabled mining")
		}
	}
	values["CAO_ADX_TRACE_MINING_ENABLED"] = "true"
	values["CAO_ADX_TRACE_MINING_INTERVAL"] = "1ns"
	if _, err := FromEnv(get); err == nil {
		t.Fatal("unbounded polling cadence admitted")
	}
	values["CAO_ADX_TRACES_ENABLED"] = "false"
	values["CAO_ADX_TRACE_POLICY"] = "/not/read/when/disabled"
	if got, err := FromEnv(get); err != nil || got.Enabled || got.MiningEnabled {
		t.Fatal("disabled trace capture read policy or activated mining")
	}
}

func TestDatasetMetadataUpgradeKeepsNonMiningCorpusIdentity(t *testing.T) {
	w, sink := workerFor(t, configFor(t, "full"))
	e := admit(t, w, "42", "reviewed")
	drain(t, w)
	spec := outcomeSpec(e, "")
	spec.Entries[0].Outcome = nil
	spec.ToolSchemas[0]["name"] = "read"
	content, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(content, []byte(`"MiningVersion"`)) || bytes.Contains(content, []byte(`"Outcome"`)) {
		t.Fatal("additive mining fields changed non-mining dataset specification identity")
	}
	if _, err := exportDatasetAt(t.Context(), w.spool.config, "destination", content, clock); err != nil {
		t.Fatal(err)
	}
	drain(t, w)
	if err := w.spool.locked(t.Context(), func(root *os.Root, state *ledger) error {
		for _, d := range state.Datasets {
			for _, row := range d.Rows {
				for _, field := range []string{"MiningVersion", "OutcomeId", "Disposition", "LabelRuleVersion", "LabelCutoff"} {
					delete(row, field)
				}
			}
		}
		return w.spool.save(root, state)
	}); err != nil {
		t.Fatal(err)
	}
	drain(t, w)
	last := sink.rows["TraceDatasets"][len(sink.rows["TraceDatasets"])-1]
	if last["State"] != "ready" || last["MiningVersion"] != "" {
		t.Fatal("legacy non-mining metadata did not receive compatible additive defaults")
	}
	for _, d := range stateFor(t, w).Datasets {
		if d.ID != digest(content) || d.State != "ready" || d.Delivered != "ready" {
			t.Fatal("metadata upgrade rewrote or invalidated the approved corpus")
		}
	}
}

func TestMiningRedactionChecksUnescapedIdentifiersBeforeDurableWrite(t *testing.T) {
	c := configFor(t, "full")
	c.Policy.RedactPatterns = []string{"sensitive\nidentifier"}
	w, sink, reader := miningWorkerConfig(t, c)
	admitCreated(t, w, "42")
	reader.change = func(snapshot *sessionexport.MiningSnapshot) {
		snapshot.Audits[1]["targetWorkflowPath"] = "sensitive\nidentifier"
	}
	var err error
	for i := 0; i < 10; i++ {
		_, err = w.Step(t.Context())
		if err != nil {
			break
		}
	}
	if err == nil {
		t.Fatal("JSON escaping bypassed approved metadata redaction")
	}
	drain(t, w)
	if len(sink.rows["TraceMiningManifest"]) != 0 || len(sink.rows["TraceEpisodes"]) != 0 {
		t.Fatal("unapproved metadata reached destination")
	}
	if w.Status().Gaps == 0 {
		t.Fatal("failed first projection lost degraded status")
	}
}

func TestOfflineDatasetRejectsExpiredSourcesWithoutWorkerStep(t *testing.T) {
	for _, cached := range []bool{false, true} {
		t.Run(map[bool]string{false: "new", true: "cached"}[cached], func(t *testing.T) {
			w, _ := workerFor(t, configFor(t, "full"))
			e := admit(t, w, "42", "reviewed")
			drain(t, w)
			spec := outcomeSpec(e, "")
			spec.Entries[0].Outcome = nil
			spec.ToolSchemas[0]["name"] = "read"
			content, err := json.Marshal(spec)
			if err != nil {
				t.Fatal(err)
			}
			if cached {
				if _, err := exportDatasetAt(t.Context(), w.spool.config, "destination", content, clock); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := exportDatasetAt(t.Context(), w.spool.config, "destination", content, clock.Add(w.spool.config.Retention)); err == nil {
				t.Fatal("offline export bypassed retention without a running worker")
			}
		})
	}
}

func TestDatasetUsesValidatedDefaultBudgets(t *testing.T) {
	w, _ := workerFor(t, configFor(t, "full"))
	e := admit(t, w, "42", "reviewed")
	drain(t, w)
	spec := outcomeSpec(e, "")
	spec.Entries[0].Outcome = nil
	spec.ToolSchemas[0]["name"] = "read"
	content, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	config := w.spool.config
	config.MaxSpoolBytes = 0
	if _, err := exportDatasetAt(t.Context(), config, "destination", content, clock); err != nil {
		t.Fatalf("validated default spool budget was ignored: %v", err)
	}
}

func TestMiningWithdrawalRetryKeepsImmutableTombstone(t *testing.T) {
	w, sink, _ := miningWorkerFor(t, "full")
	admitCreated(t, w, "42")
	drain(t, w)
	if err := w.WithdrawRepository(t.Context(), "octo/api"); err != nil {
		t.Fatal(err)
	}
	sink.fail = "TraceMiningManifest"
	var err error
	for i := 0; i < 10; i++ {
		_, err = w.Step(t.Context())
		if err != nil {
			break
		}
	}
	if err == nil {
		t.Fatal("ambiguous invalidation was not exercised")
	}
	w.now = func() time.Time { return clock.Add(time.Minute) }
	drain(t, w)
}
