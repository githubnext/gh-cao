package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDashboardStreamEmitsExplicitVerifiedRunBundles(t *testing.T) {
	cache := t.TempDir()
	now := time.Now().UTC().Add(-time.Second)
	deltas, snapshots := 0, 0
	options := dashboardOptions{window: dashboardMaximumDuration, count: 2,
		onRunBundles: func() error {
			deltas++
			content, err := os.ReadFile(filepath.Join(cache, "run-bundles.json"))
			if err != nil {
				t.Fatal(err)
			}
			manifestContent, err := os.ReadFile(filepath.Join(cache, "payload-hashes.json"))
			if err != nil {
				t.Fatal(err)
			}
			var manifest map[string]string
			if err := json.Unmarshal(manifestContent, &manifest); err != nil {
				t.Fatal(err)
			}
			hash := sha256.Sum256(content)
			if manifest["run-bundles.json"] != hex.EncodeToString(hash[:]) {
				t.Fatal("streamed bundle is not bound to the published manifest")
			}
			var publication struct {
				Version int `json:"version"`
				Runs    []struct {
					Run         map[string]any `json:"run"`
					Collections map[string]struct {
						Coverage string `json:"coverage"`
					} `json:"collections"`
				} `json:"runs"`
			}
			if err := json.Unmarshal(content, &publication); err != nil || publication.Version != 1 ||
				len(publication.Runs) != 1 || publication.Runs[0].Run["attempt"] != float64(1) {
				t.Fatalf("unexpected normalized bundle: %+v %v", publication, err)
			}
			for _, collection := range publication.Runs[0].Collections {
				if collection.Coverage != "partial" {
					t.Fatal("streamed artifact claimed complete removal authority")
				}
			}
			return nil
		},
		onPublished: func() error { snapshots++; return nil },
	}
	logs := func(ctx context.Context, request dashboardLogRequest) error {
		return request.OnRuns(ctx, []map[string]any{{
			"run_id": 123, "run_attempt": "1", "repository": "octo/api",
			"workflow_path":       ".github/workflows/agent.lock.yml",
			"token_usage_summary": map[string]any{"total_aic": 2},
		}})
	}
	if _, err := collectDashboard(t.Context(), []string{"octo/api"}, cache, options, dashboardTestGH(t, now), logs); err != nil || deltas != 1 || snapshots != 0 {
		t.Fatalf("streaming did not separate deltas from final reconciliation: deltas=%d snapshots=%d err=%v", deltas, snapshots, err)
	}
	manifestContent, err := os.ReadFile(filepath.Join(cache, "payload-hashes.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest map[string]string
	if err := json.Unmarshal(manifestContent, &manifest); err != nil {
		t.Fatal(err)
	}
	if _, exists := manifest["run-bundles.json"]; exists {
		t.Fatal("final snapshot advertised a stale streamed bundle")
	}
}

func TestDashboardStreamPublishesAndReconcilesWhileCollecting(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "complete", true: "late-failure"}[fail], func(t *testing.T) {
			now := time.Now().UTC().Add(-time.Second)
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			cache := t.TempDir()
			published := make(chan struct{}, 2)
			publications := 0
			options := dashboardOptions{window: dashboardMaximumDuration, count: 2, onPublished: func() error {
				publications++
				rows := readDashboardPublishedCollection(t, cache, "runs")
				if len(rows) != 1 || rows[0]["aicTotal"] != float64(publications) {
					t.Fatalf("incremental publication was missing, duplicated or stale: %v", rows)
				}
				state, err := readDashboardIngestion(ctx, cache)
				if err != nil || state.Status != "collecting" || len(state.Cursors) != 0 {
					t.Fatalf("provisional evidence advanced history or completed acquisition: %+v %v", state, err)
				}
				published <- struct{}{}
				return nil
			}}
			logs := func(ctx context.Context, request dashboardLogRequest) error {
				return downloadDashboardLogs(ctx, request, func(ctx context.Context, _ []string, output io.Writer) error {
					record := map[string]any{
						"run_id": 123, "run_attempt": "1", "repository": "octo/api",
						"workflow_path":       ".github/workflows/agent.lock.yml",
						"token_usage_summary": map[string]any{"total_aic": 1},
					}
					encoder := json.NewEncoder(output)
					if err := encoder.Encode(map[string]any{"type": "run", "run": record}); err != nil {
						return err
					}
					select {
					case <-published:
					case <-ctx.Done():
						return ctx.Err()
					}
					if fail {
						return errors.New("late acquisition failure")
					}
					record["token_usage_summary"] = map[string]any{"total_aic": 2}
					if err := encoder.Encode(map[string]any{"type": "summary", "data": map[string]any{"runs": []any{record}}}); err != nil {
						return err
					}
					return encoder.Encode(map[string]any{"type": "complete"})
				})
			}
			_, err := collectDashboard(ctx, []string{"octo/api"}, cache, options, dashboardTestGH(t, now), logs)
			if (err != nil) != fail || publications != map[bool]int{false: 2, true: 1}[fail] {
				t.Fatalf("unexpected streaming collection outcome: publications=%d err=%v", publications, err)
			}
			rows := readDashboardPublishedCollection(t, cache, "runs")
			if len(rows) != 1 || rows[0]["aicTotal"] != float64(publications) {
				t.Fatalf("final publication lost streamed data or double counted: %v", rows)
			}
			if fail {
				state, err := readDashboardIngestion(ctx, cache)
				if err != nil || state.Status != "failed" || len(state.Cursors) != 0 {
					t.Fatalf("failed stream lost its checkpoint or failure status: %+v %v", state, err)
				}
			}
		})
	}
}

func TestDashboardStreamRejectsUnselectedEvidence(t *testing.T) {
	for _, field := range []string{"repository", "workflow_path", "run_id", "run_attempt"} {
		t.Run(field, func(t *testing.T) {
			cache := t.TempDir()
			now := time.Now().UTC().Add(-time.Second)
			options := dashboardOptions{window: dashboardMaximumDuration, count: 2, onPublished: func() error {
				t.Fatal("unselected or mismatched-attempt stream evidence was published")
				return nil
			}}
			logs := func(ctx context.Context, request dashboardLogRequest) error {
				record := map[string]any{"run_id": 123, "run_attempt": "1", "repository": "octo/api", "workflow_path": ".github/workflows/agent.lock.yml"}
				record[field] = "unselected"
				return request.OnRuns(ctx, []map[string]any{record})
			}
			_, err := collectDashboard(t.Context(), []string{"octo/api"}, cache, options, dashboardTestGH(t, now), logs)
			if field == "run_attempt" {
				if err != nil {
					t.Fatalf("mismatched attempt should not block fresh Actions evidence: %v", err)
				}
			} else if err == nil {
				t.Fatal("out-of-scope streamed evidence was accepted")
			}
		})
	}
}

func TestDashboardHydratesRichCachedAndUnifiedEvidence(t *testing.T) {
	now := time.Now().UTC().Add(-time.Second)
	cache := t.TempDir()
	session, err := os.ReadFile("../../dashboard/site/test/fixtures/unified-telemetry-session.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	logs := func(_ context.Context, request dashboardLogRequest) error {
		content, err := json.Marshal(map[string]any{
			"schema_version": 5, "kind": "run", "run": map[string]any{
				"run_id": 123, "run_attempt": "1", "repository": request.Repository,
				"updated_at": now.Format(time.RFC3339Nano),
				"mcp_tool_usage": map[string]any{"tool_calls": []map[string]any{{
					"tool_call_id": "cached-call", "timestamp": now.Format(time.RFC3339Nano),
					"server_name": "cached-only-server", "tool_name": "must-not-double-count",
					"status": "success", "input_size": 10, "output_size": 20,
				}}},
			},
		})
		if err != nil {
			return err
		}
		if err := os.WriteFile(request.Cache, append(content, '\n'), 0o600); err != nil {
			return err
		}
		root := filepath.Join(request.Directory, "run-123", "usage")
		if err := os.MkdirAll(root, 0o700); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(root, "aw_session.jsonl"),
			[]byte(strings.ReplaceAll(string(session), `"303"`, `"123"`)), 0o600)
	}
	if _, err := collectDashboard(t.Context(), []string{"octo/api"}, cache,
		dashboardOptions{window: dashboardMaximumDuration, count: 2}, dashboardTestGH(t, now), logs); err != nil {
		t.Fatal(err)
	}
	for _, store := range []string{"runs", "tools", "domains", "audits"} {
		rows := readDashboardPublishedCollection(t, cache, store)
		if len(rows) == 0 {
			t.Fatalf("rich %s evidence was discarded", store)
		}
		content, err := json.Marshal(rows)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(content), "PRIVATE_CONTEXT") || store == "tools" && strings.Contains(string(content), "cached-only-server") {
			t.Fatalf("raw context or duplicate cached timeline entered %s", store)
		}
	}
}

func TestDashboardArtifactReadRejectsSymlinksAndOversize(t *testing.T) {
	for _, kind := range []string{"symlink", "oversize"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			run := filepath.Join(root, "run-123")
			if err := os.Mkdir(run, 0o700); err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(run, "aw_session.jsonl")
			if kind == "symlink" {
				outside := filepath.Join(t.TempDir(), "outside.jsonl")
				writeTestFile(t, outside, "{}")
				if err := os.Symlink(outside, target); err != nil {
					t.Fatal(err)
				}
			} else {
				file, err := os.Create(target)
				if err != nil {
					t.Fatal(err)
				}
				if err := file.Truncate((16 << 20) + 1); err != nil {
					t.Fatal(err)
				}
				if err := file.Close(); err != nil {
					t.Fatal(err)
				}
			}
			total := 0
			if _, err := readDashboardArtifacts(t.Context(), root, "123", &total); err == nil {
				t.Fatal("unsafe artifact was admitted")
			}
		})
	}
}

func TestDashboardSnapshotDoesNotRetainFalseEmptySources(t *testing.T) {
	cache := t.TempDir()
	batch := map[string][]map[string]any{"runs": {}, "tools": {{"id": "tool"}}}
	if err := publishDashboardSnapshot(t.Context(), cache, batch); err != nil {
		t.Fatal(err)
	}
	delete(batch, "tools")
	if err := publishDashboardSnapshot(t.Context(), cache, batch); err != nil {
		t.Fatal(err)
	}
	if rows := readDashboardPublishedCollection(t, cache, "tools"); len(rows) != 0 {
		t.Fatal("uncollected tools survived canonical artifact replacement")
	}
}

func readDashboardPublishedCollection(t *testing.T, directory, collection string) []map[string]any {
	t.Helper()
	rows := []map[string]any{}
	for _, phase := range []string{"runs", "records"} {
		content, err := os.ReadFile(filepath.Join(directory, "gh-aw-logs-"+phase, "local.jsonl"))
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(strings.TrimSpace(string(content)), "\n") {
			var envelope struct {
				Collection string         `json:"collection"`
				Record     map[string]any `json:"record"`
			}
			if err := json.Unmarshal([]byte(line), &envelope); err != nil {
				t.Fatal(err)
			}
			if envelope.Collection == collection {
				rows = append(rows, envelope.Record)
			}
		}
	}
	return rows
}
