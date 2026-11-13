package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

func dashboardShardTestBatch(count int, now time.Time) map[string][]map[string]any {
	batch := map[string][]map[string]any{
		"transactions": {dashboardObservationMetadata([]string{"octo/api"},
			dashboardOptions{window: dashboardMaximumWindow}, now, nil)},
		"repositories": {{"id": "repo:octo/api", "fullName": "octo/api"}},
	}
	for i := 0; i < count; i++ {
		id := fmt.Sprintf("run:%06d", i)
		batch["runs"] = append(batch["runs"], map[string]any{
			"id": id, "attempt": 1, "createdAt": now.Add(-time.Hour).Format(time.RFC3339Nano),
			"updatedAt": now.Format(time.RFC3339Nano), "resolvedModel": "observed",
		})
		batch["tools"] = append(batch["tools"], map[string]any{
			"id": "tool:" + id, "runId": id, "name": strings.Repeat("tool", 64),
		})
	}
	return batch
}

func TestDashboardIncrementalSnapshotPublishesOnlyIngestionReceipt(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(fmt.Sprintf("legacy=%t", legacy), func(t *testing.T) {
			cache, now := t.TempDir(), time.Now().UTC()
			batch := dashboardShardTestBatch(1, now)
			receipt, err := dashboardIngestionRecord(dashboardIngestion{
				ID: "local-dashboard-ingestion", Kind: "local-dashboard-ingestion", Status: "collecting",
			})
			if err != nil {
				t.Fatal(err)
			}
			batch["transactions"] = append(batch["transactions"], receipt)
			if legacy {
				if err := publishDashboardSnapshot(t.Context(), cache, batch); err != nil {
					t.Fatal(err)
				}
			}
			for publication := 0; publication < 2; publication++ {
				if err := writeDashboardSnapshot(t.Context(), cache, batch, true); err != nil {
					t.Fatal(err)
				}
				recovered, err := readDashboardArtifactBatch(t.Context(), cache)
				if err != nil {
					t.Fatal(err)
				}
				if rows := recovered["transactions"]; len(rows) != 1 || !reflect.DeepEqual(rows[0], receipt) {
					t.Fatalf("canonical shards must contain only the ingestion receipt: %v", rows)
				}
				metadata, err := readDashboardStatus(t.Context(), cache, []string{"octo/api"})
				if err != nil || metadata["id"] != "local-dashboard" || metadata["windowEnd"] != batch["transactions"][0]["windowEnd"] {
					t.Fatalf("observation metadata was not preserved in inventory: %v %v", metadata, err)
				}
			}
		})
	}
}

func TestDashboardIncrementalSnapshotReusesUntouchedShards(t *testing.T) {
	cache, now := t.TempDir(), time.Now().UTC()
	batch := dashboardShardTestBatch(160, now)
	if err := writeDashboardSnapshot(t.Context(), cache, batch, true); err != nil {
		t.Fatal(err)
	}
	before, err := readDashboardSnapshotManifest(t.Context(), cache)
	if err != nil {
		t.Fatal(err)
	}
	index, _, err := readDashboardShardIndex(t.Context(), cache, before)
	if err != nil || len(index.Groups) != 10 {
		t.Fatalf("unexpected bounded layout: %+v %v", index, err)
	}
	untouched := index.Groups[len(index.Groups)-1].Shards
	infos := map[string]os.FileInfo{}
	for _, name := range untouched {
		info, err := os.Stat(filepath.Join(cache, name))
		if err != nil {
			t.Fatal(err)
		}
		infos[name] = info
	}
	incoming := dashboardShardTestBatch(16, now)
	for _, run := range incoming["runs"] {
		run["status"] = "completed"
		delete(run, "resolvedModel")
	}
	bundles, err := dashboardRunBundles(incoming, now)
	if err != nil {
		t.Fatal(err)
	}
	// Withhold one unrelated payload to prove publication never opens it;
	// restore it before verifying the complete cold-recovery artifact.
	withheld := filepath.Join(cache, untouched[0])
	held := filepath.Join(cache, "held-payload.jsonl")
	if err := os.Rename(withheld, held); err != nil {
		t.Fatal(err)
	}
	writeErr := writeDashboardSnapshotWithBundles(t.Context(), cache, incoming, true, bundles)
	restoreErr := os.Rename(held, withheld)
	if writeErr != nil || restoreErr != nil {
		t.Fatalf("small publication read an unrelated payload: write=%v restore=%v", writeErr, restoreErr)
	}
	after, err := readDashboardSnapshotManifest(t.Context(), cache)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range untouched {
		info, err := os.Stat(filepath.Join(cache, name))
		if err != nil || !os.SameFile(infos[name], info) || after[name] != before[name] {
			t.Fatalf("unrelated immutable shard was rewritten: %s %v", name, err)
		}
	}
	changed := 0
	for name := range before {
		if dashboardCanonicalShard(name) && after[name] != before[name] {
			changed++
		}
	}
	if changed != 1 {
		t.Fatalf("16-Run update replaced %d old payloads; expected only its Run shard", changed)
	}
	recovered, err := readDashboardArtifactBatch(t.Context(), cache)
	if err != nil || len(recovered["runs"]) != 160 || len(recovered["tools"]) != 160 {
		t.Fatalf("cold recovery lost retained evidence: %v", err)
	}
	if recovered["runs"][0]["resolvedModel"] != "observed" || recovered["runs"][0]["status"] != "completed" {
		t.Fatalf("sparse update did not survive recovery: %v", recovered["runs"][0])
	}
	if !dashboardSnapshotHasRuns(cache) {
		t.Fatal("indexed snapshot incorrectly selected empty-dashboard refresh")
	}
}

func TestDashboardIncrementalSnapshotRetentionAcrossGroups(t *testing.T) {
	cache, now := t.TempDir(), time.Now().UTC()
	batch := dashboardShardTestBatch(48, now)
	for _, run := range batch["runs"][:17] {
		run["createdAt"] = now.Add(-6 * 24 * time.Hour).Format(time.RFC3339Nano)
	}
	if err := writeDashboardSnapshot(t.Context(), cache, batch, true); err != nil {
		t.Fatal(err)
	}
	incoming := dashboardShardTestBatch(0, now.Add(2*24*time.Hour))
	if err := writeDashboardSnapshot(t.Context(), cache, incoming, true); err != nil {
		t.Fatal(err)
	}
	recovered, err := readDashboardArtifactBatch(t.Context(), cache)
	if err != nil || len(recovered["runs"]) != 31 || len(recovered["tools"]) != 31 {
		t.Fatalf("whole and partially expired groups were not retained correctly: %v %v", recovered, err)
	}
	for _, run := range recovered["runs"] {
		if nativeText(run["id"]) < "run:000017" {
			t.Fatal("expired Run survived its indexed cutoff")
		}
	}
}

func TestDashboardIncrementalSnapshotPreservesGlobalIssueIdentity(t *testing.T) {
	cache, now := t.TempDir(), time.Now().UTC()
	batch := dashboardShardTestBatch(32, now)
	batch["issues"] = []map[string]any{{"id": "github:issue:octo/api:1", "runId": "run:000000"}}
	if err := writeDashboardSnapshot(t.Context(), cache, batch, true); err != nil {
		t.Fatal(err)
	}
	incoming := dashboardShardTestBatch(0, now)
	incoming["runs"] = []map[string]any{{"id": "run:000031", "attempt": 1, "updatedAt": now.Format(time.RFC3339Nano)}}
	incoming["issues"] = []map[string]any{{"id": "github:issue:octo/api:1", "runId": "run:000031"}}
	if err := writeDashboardSnapshot(t.Context(), cache, incoming, true); err != nil {
		t.Fatal(err)
	}
	recovered, err := readDashboardArtifactBatch(t.Context(), cache)
	if err != nil || len(recovered["issues"]) != 1 || recovered["issues"][0]["runId"] != "run:000031" {
		t.Fatalf("same GitHub object was duplicated across Run groups: %v %v", recovered["issues"], err)
	}
}

func TestDashboardIncrementalSnapshotSplitsLargeRunGroups(t *testing.T) {
	cache, now := t.TempDir(), time.Now().UTC()
	batch := dashboardShardTestBatch(2, now)
	batch["tools"] = nil
	for _, run := range batch["runs"] {
		for i := 0; i < 6; i++ {
			batch["tools"] = append(batch["tools"], map[string]any{
				"id": fmt.Sprintf("tool:%s:%d", run["id"], i), "runId": run["id"], "name": strings.Repeat("x", 256<<10),
			})
		}
	}
	if err := writeDashboardSnapshot(t.Context(), cache, batch, true); err != nil {
		t.Fatal(err)
	}
	manifest, err := readDashboardSnapshotManifest(t.Context(), cache)
	if err != nil {
		t.Fatal(err)
	}
	for name := range manifest {
		if !dashboardCanonicalShard(name) {
			continue
		}
		info, err := os.Stat(filepath.Join(cache, name))
		if err != nil || info.Size() > dashboardShardBytes {
			t.Fatalf("canonical payload exceeded its shard target: %s %v", name, err)
		}
	}
	recovered, err := readDashboardArtifactBatch(t.Context(), cache)
	if err != nil || len(recovered["tools"]) != 12 {
		t.Fatalf("bounded shard splitting dropped records: %v", err)
	}
}

func TestDashboardIncrementalSnapshotFailurePreservesCheckpoint(t *testing.T) {
	for _, failure := range []string{"encoding", "index", "shard", "cancelled"} {
		t.Run(failure, func(t *testing.T) {
			cache, now := t.TempDir(), time.Now().UTC()
			batch := dashboardShardTestBatch(32, now)
			if err := writeDashboardSnapshot(t.Context(), cache, batch, true); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(filepath.Join(cache, "payload-hashes.json"))
			if err != nil {
				t.Fatal(err)
			}
			inventory, err := os.ReadFile(filepath.Join(cache, "inventory-sources.json"))
			if err != nil {
				t.Fatal(err)
			}
			manifest, err := readDashboardSnapshotManifest(t.Context(), cache)
			if err != nil {
				t.Fatal(err)
			}
			index, name, err := readDashboardShardIndex(t.Context(), cache, manifest)
			if err != nil {
				t.Fatal(err)
			}
			incoming := dashboardShardTestBatch(1, now)
			ctx := t.Context()
			switch failure {
			case "encoding":
				incoming["runs"][0]["invalid"] = make(chan int)
			case "index":
				if err := os.WriteFile(filepath.Join(cache, name), []byte("{}"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "shard":
				if err := os.WriteFile(filepath.Join(cache, index.Groups[0].Shards[0]), []byte("{}"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "cancelled":
				cancelled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = cancelled
			}
			if err := writeDashboardSnapshot(ctx, cache, incoming, true); err == nil {
				t.Fatal("invalid publication advanced its checkpoint")
			}
			after, err := os.ReadFile(filepath.Join(cache, "payload-hashes.json"))
			if err != nil || string(before) != string(after) {
				t.Fatalf("failed publication changed its manifest: %v", err)
			}
			after, err = os.ReadFile(filepath.Join(cache, "inventory-sources.json"))
			if err != nil || string(inventory) != string(after) {
				t.Fatalf("failed publication changed acquisition metadata: %v", err)
			}
		})
	}
}

func TestDashboardStatusCheckpointDoesNotRewriteEvidence(t *testing.T) {
	cache, now := t.TempDir(), time.Now().UTC()
	if err := writeDashboardSnapshot(t.Context(), cache, dashboardShardTestBatch(32, now), true); err != nil {
		t.Fatal(err)
	}
	before, err := readDashboardSnapshotManifest(t.Context(), cache)
	if err != nil {
		t.Fatal(err)
	}
	state := dashboardIngestion{ID: "local-dashboard-ingestion", Kind: "local-dashboard-ingestion",
		Status: "failed", Cursors: map[string]dashboardHistoryCursor{}}
	if err := writeDashboardIngestion(t.Context(), cache, state); err != nil {
		t.Fatal(err)
	}
	after, err := readDashboardSnapshotManifest(t.Context(), cache)
	if err != nil {
		t.Fatal(err)
	}
	delete(before, "inventory-sources.json")
	delete(after, "inventory-sources.json")
	if !reflect.DeepEqual(before, after) {
		t.Fatal("status-only checkpoint changed canonical shards or their index")
	}
	recovered, err := readDashboardIngestion(t.Context(), cache)
	if err != nil || recovered.Status != "failed" {
		t.Fatalf("status-only checkpoint did not survive recovery: %+v %v", recovered, err)
	}
}

func TestDashboardFullSnapshotStillReplacesIncrementalHistory(t *testing.T) {
	cache, now := t.TempDir(), time.Now().UTC()
	batch := dashboardShardTestBatch(32, now)
	if err := writeDashboardSnapshot(t.Context(), cache, batch, true); err != nil {
		t.Fatal(err)
	}
	incoming := dashboardShardTestBatch(16, now)
	bundles, err := dashboardRunBundles(incoming, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeDashboardSnapshotWithBundles(t.Context(), cache, incoming, true, bundles); err != nil {
		t.Fatal(err)
	}
	replacement := dashboardShardTestBatch(1, now)
	if err := publishDashboardSnapshot(t.Context(), cache, replacement); err != nil {
		t.Fatal(err)
	}
	manifest, err := readDashboardSnapshotManifest(t.Context(), cache)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest) != 3 || manifest["run-bundles.json"] != "" {
		t.Fatalf("full replacement retained incremental entries: %v", manifest)
	}
	recovered, err := readDashboardArtifactBatch(t.Context(), cache)
	if err != nil || len(recovered["runs"]) != 1 || len(recovered["tools"]) != 1 {
		t.Fatalf("full replacement merged old history: %v %v", recovered, err)
	}
	// A later incremental publication upgrades this explicit full snapshot once.
	if err := writeDashboardSnapshot(t.Context(), cache, dashboardShardTestBatch(2, now), true); err != nil {
		t.Fatal(err)
	}
	recovered, err = readDashboardArtifactBatch(t.Context(), cache)
	if err != nil || len(recovered["runs"]) != 2 {
		t.Fatalf("full snapshot could not resume incremental publication: %v", err)
	}
}

func TestDashboardSnapshotRecoversInterruptedCheckpointAliases(t *testing.T) {
	cache, now := t.TempDir(), time.Now().UTC()
	batch := dashboardShardTestBatch(16, now)
	state := dashboardIngestion{ID: "local-dashboard-ingestion", Kind: "local-dashboard-ingestion",
		Status: "collecting", Cursors: map[string]dashboardHistoryCursor{
			"octo/api": {Page: 1, End: now},
		}}
	record, err := dashboardIngestionRecord(state)
	if err != nil {
		t.Fatal(err)
	}
	batch["transactions"] = append(batch["transactions"], record)
	bundles, err := dashboardRunBundles(batch, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeDashboardSnapshotWithBundles(t.Context(), cache, batch, true, bundles); err != nil {
		t.Fatal(err)
	}
	manifest, err := readDashboardSnapshotManifest(t.Context(), cache)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"inventory-sources.json", "run-bundles.json"} {
		before, err := os.ReadFile(filepath.Join(cache, name))
		if err != nil {
			t.Fatal(err)
		}
		// The next publisher replaced this alias, then stopped before replacing
		// payload-hashes.json. The committed manifest still selects old bytes.
		if err := os.WriteFile(filepath.Join(cache, name), []byte(`{"uncommitted":true}`), 0o600); err != nil {
			t.Fatal(err)
		}
		if name == "inventory-sources.json" {
			recovered, err := readDashboardIngestion(t.Context(), cache)
			if err != nil || recovered.Cursors["octo/api"].Page != 1 {
				t.Fatalf("interrupted publication advanced its cursor: %+v %v", recovered, err)
			}
		} else if _, err := readDashboardCheckpointPayload(t.Context(), cache, name, manifest[name]); err != nil {
			t.Fatal(err)
		}
		after, err := os.ReadFile(filepath.Join(cache, name))
		if err != nil || string(before) != string(after) {
			t.Fatalf("committed %s was not restored: %v", name, err)
		}
	}
	recovered, err := readDashboardArtifactBatch(t.Context(), cache)
	if err != nil || len(recovered["runs"]) != 16 {
		t.Fatalf("interrupted checkpoint damaged recovery evidence: %v", err)
	}
}

func TestDashboardReconciliationCollectsOnlyUnreferencedOwnedPayloads(t *testing.T) {
	cache, now := t.TempDir(), time.Now().UTC()
	batch := dashboardShardTestBatch(1, now)
	if err := writeDashboardSnapshot(t.Context(), cache, batch, true); err != nil {
		t.Fatal(err)
	}
	orphanManifest := map[string]string{}
	orphan, err := writeDashboardImmutablePayload(t.Context(), cache, "gh-aw-logs-runs/", ".jsonl", []byte("uncommitted"), orphanManifest)
	if err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(cache, "gh-aw-logs-runs", "operator-note.txt")
	if err := os.WriteFile(foreign, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeDashboardSnapshot(t.Context(), cache, batch, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(cache, orphan)); !os.IsNotExist(err) {
		t.Fatalf("uncommitted immutable preparation survived reconciliation: %v", err)
	}
	if _, err := os.Stat(foreign); err != nil {
		t.Fatalf("reconciliation removed an unowned file: %v", err)
	}
}

func BenchmarkDashboardSmallPublication(b *testing.B) {
	for _, count := range []int{100, 10000} {
		for _, incremental := range []bool{false, true} {
			name := fmt.Sprintf("retained_%d/%s", count, map[bool]string{false: "rewrite", true: "incremental"}[incremental])
			b.Run(name, func(b *testing.B) {
				cache, now := b.TempDir(), time.Now().UTC()
				seed := dashboardShardTestBatch(count, now)
				if err := writeDashboardSnapshot(b.Context(), cache, seed, incremental); err != nil {
					b.Fatal(err)
				}
				incoming := dashboardShardTestBatch(16, now)
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					for _, run := range incoming["runs"] {
						run["aicTotal"] = i
					}
					bundles, err := dashboardRunBundles(incoming, now.Add(time.Duration(i)*time.Microsecond))
					if err != nil {
						b.Fatal(err)
					}
					batch := incoming
					if !incremental {
						previous, err := readDashboardArtifactBatch(b.Context(), cache)
						if err != nil {
							b.Fatal(err)
						}
						batch, err = mergeDashboardHistory(previous, incoming)
						if err != nil {
							b.Fatal(err)
						}
					}
					if err := writeDashboardSnapshotWithBundles(b.Context(), cache, batch, incremental, bundles); err != nil {
						b.Fatal(err)
					}
				}
				b.StopTimer()
				manifest, err := readDashboardSnapshotManifest(b.Context(), cache)
				if err != nil {
					b.Fatal(err)
				}
				if incremental {
					index, _, err := readDashboardShardIndex(b.Context(), cache, manifest)
					if err != nil || slices.ContainsFunc(index.Groups, func(group dashboardShardGroup) bool {
						return len(group.Runs) > dashboardShardRuns
					}) {
						b.Fatalf("unbounded incremental layout: %v", err)
					}
				}
				for name, expected := range manifest {
					if _, err := readDashboardVerifiedPayload(b.Context(), cache, name, expected); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}
