package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

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
