package collect

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/mining"
)

func miningOutcome(t *testing.T) mining.Outcome {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Second)
	return mining.Outcome{Repository: "octo/api", Number: 12, URL: "https://github.com/octo/api/issues/12",
		State: "closed", StateReason: "completed", UpdatedAt: now, ObservedAt: now, Source: "github-webhook"}
}

func TestMinedEvidenceIsDurableOrderedAndErased(t *testing.T) {
	ctx := t.Context()
	lake := Lake{Directory: t.TempDir()}
	if err := lake.Prepare(); err != nil {
		t.Fatal(err)
	}
	newer := miningOutcome(t)
	older := newer
	older.State, older.StateReason = "open", ""
	older.UpdatedAt = newer.UpdatedAt.Add(-time.Second)
	for _, observation := range []mining.Outcome{newer, older, newer} {
		if err := lake.recordMining(ctx, "octo/api", nil, &observation); err != nil {
			t.Fatal(err)
		}
	}
	packet, before, err := readMiningPacket(lake.miningPath("octo/api"))
	if err != nil || len(packet.Outcomes) != 1 || packet.Outcomes[0].State != "closed" {
		t.Fatalf("out-of-order observation replaced truth: %+v, %v", packet, err)
	}
	if err := lake.recordMining(ctx, "octo/api", nil, &newer); err != nil {
		t.Fatal(err)
	}
	_, after, err := readMiningPacket(lake.miningPath("octo/api"))
	if err != nil || string(before) != string(after) {
		t.Fatal("replay changed durable evidence")
	}
	conflict := newer
	conflict.StateReason = "not_planned"
	if err := lake.recordMining(ctx, "octo/api", nil, &conflict); err == nil {
		t.Fatal("conflicting same-time disposition was silently resolved")
	}
	if err := lake.ForgetContext(ctx, "octo/api"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(lake.miningPath("octo/api")); !os.IsNotExist(err) {
		t.Fatalf("withdrawal retained mined evidence: %v", err)
	}
}

func TestEpisodeArtifactsAndCachedBackfillNeedRecordedIdentity(t *testing.T) {
	lake := Lake{Directory: t.TempDir()}
	if err := lake.Prepare(); err != nil {
		t.Fatal(err)
	}
	logs := t.TempDir()
	path := filepath.Join(lake.ShardDirectory(), lake.ShardPrefix("octo/api")+"1.jsonl")
	if err := os.WriteFile(path, []byte(`{"kind":"run","run":{"repository":"octo/api","run_id":42,"run_attempt":2}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	artifact := `{"repository":"octo/api","run_id":42,"run_attempt":"2","context":{
		"episode_id":"root:1","hop_id":"hop:2","parent_hop_id":"hop:1","run_id":"41","repo":"octo/root"}}`
	if err := os.WriteFile(filepath.Join(logs, "aw_info.json"), []byte(artifact), 0o600); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := lake.mineDownloadedInfo(t.Context(), "octo/api", logs); err != nil {
			t.Fatal(err)
		}
	}
	cached := `{"kind":"run","run":{"repository":"octo/api","run_id":42,"run_attempt":2,"context":{"episode_id":"root:1","hop_id":"hop:2","parent_hop_id":"hop:1"}}}` + "\n" +
		`{"kind":"run","run":{"repository":"octo/api","run_id":43,"run_attempt":"1","aw_info":{"context":{"episode_id":"root:1","hop_id":"hop:3","parent_hop_id":"hop:2"}},` +
		`"episode_hops":[{"run_id":43,"repository":"octo/api","hop_id":"hop:4","parent_hop_id":"hop:3","caller_run_id":"43","caller_repository":"octo/api","evidence":["context.episode_id","context.hop_id"]}]}}`
	if err := os.WriteFile(path, []byte(cached), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := lake.mineCachedInfo(t.Context(), "octo/api"); err != nil {
		t.Fatal(err)
	}
	packet, _, err := readMiningPacket(lake.miningPath("octo/api"))
	if err != nil || len(packet.Episodes) != 3 || packet.Episodes[0].Context.EpisodeID != "root:1" ||
		packet.Episodes[0].Context.RunID != "41" || packet.Episodes[2].Context.HopID != "hop:4" {
		t.Fatalf("episode backfill = %+v, %v", packet, err)
	}
	conflict := strings.Replace(artifact, `"root:1"`, `"different-root"`, 1)
	if err := os.WriteFile(filepath.Join(logs, "aw_info.json"), []byte(conflict), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := lake.mineDownloadedInfo(t.Context(), "octo/api", logs); err == nil {
		t.Fatal("conflicting recorded lineage was accepted")
	}
}

func TestMiningWebhooksPreserveDistinctObjectsAndDeliveryDedupe(t *testing.T) {
	w := refreshWorker(t)
	if err := w.Runner.Lake.Prepare(); err != nil {
		t.Fatal(err)
	}
	if err := w.Enrollment.AddRepositories(t.Context(), 7, []string{"octo/api"}); err != nil {
		t.Fatal(err)
	}
	admitter := Admitter{Enrollment: w.Enrollment, Queue: w.Queue, Mining: true}
	for n := 1; n <= 2; n++ {
		payload := []byte(fmt.Sprintf(`{"action":"closed","installation":{"id":7},"repository":{"full_name":"octo/api"},
			"pull_request":{"number":%d,"html_url":"https://github.com/octo/api/pull/%d",
			"state":"closed","merged":true,"updated_at":"2026-10-20T00:00:00Z"}}`, n, n))
		for replay := range 2 {
			result, err := admitter.AdmitDelivery(t.Context(), "pull_request", payload, fmt.Sprintf("pr-%d", n), time.Hour)
			if err != nil || result.Enqueued != (replay == 0) || result.Duplicate != (replay == 1) {
				t.Fatalf("webhook admission = %+v, %v", result, err)
			}
		}
	}
	for range 2 {
		if !w.process(t.Context(), refreshLease(t, w.Queue)) {
			t.Fatal("outcome task did not persist evidence")
		}
	}
	packet, _, err := readMiningPacket(w.Runner.Lake.miningPath("octo/api"))
	if err != nil || len(packet.Outcomes) != 2 {
		t.Fatalf("repository debounce lost an object: %+v, %v", packet, err)
	}
	pending, err := w.Projector.PendingProjection(t.Context())
	if err != nil || !pending {
		t.Fatal("evidence ACKed without durable projection intent")
	}
	if content, err := json.Marshal(packet); err != nil || strings.Contains(string(content), "pull_request") {
		t.Fatal("raw webhook payload was retained")
	}
}

func TestMiningTasksNeverChangeAdmittedInstallation(t *testing.T) {
	w := refreshWorker(t)
	if err := w.Enrollment.AddRepositories(t.Context(), 7, []string{"octo/api"}); err != nil {
		t.Fatal(err)
	}
	outcome := miningOutcome(t)
	if _, err := w.Queue.Enqueue(t.Context(), Task{Repository: "octo/api", InstallationID: 8, Outcome: &outcome}); err != nil {
		t.Fatal(err)
	}
	if w.process(t.Context(), refreshLease(t, w.Queue)) {
		t.Fatal("stale installation observation changed evidence")
	}
	if _, err := os.Stat(w.Runner.Lake.miningPath("octo/api")); !os.IsNotExist(err) {
		t.Fatalf("stale observation retained evidence: %v", err)
	}
	leases, err := w.Queue.Lease(t.Context(), "stale-check", 10, time.Millisecond)
	if err != nil || len(leases) != 0 {
		t.Fatalf("stale observation was retried with a replacement installation: %v, %v", leases, err)
	}
}
