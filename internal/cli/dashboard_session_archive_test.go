package cli

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func fakeDashboardDuckDB(t *testing.T, version string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake DuckDB CLI is a shell script")
	}
	directory := t.TempDir()
	path := filepath.Join(directory, "duckdb")
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho '"+version+" (Test) abc123'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return directory
}

func TestDashboardSessionArchiveAutoDetection(t *testing.T) {
	work := t.TempDir()
	t.Chdir(work)
	approved := `{"version":"v1","contentApproved":true,"rightsApproved":true,"events":{"user.message":["content"]}}`
	resolve := func(options dashboardOptions) (dashboardOptions, string, error) {
		var diagnostics strings.Builder
		err := resolveDashboardSessionArchiveOptions(&options, &diagnostics)
		return options, diagnostics.String(), err
	}

	t.Setenv("PATH", t.TempDir())
	if options, diagnostics, err := resolve(dashboardOptions{}); err != nil || options.sessionArchive || diagnostics != "" {
		t.Fatalf("missing DuckDB was not a quiet skip: %v %q", err, diagnostics)
	}
	if _, _, err := resolve(dashboardOptions{sessionArchive: true}); err == nil {
		t.Fatal("--session-archive accepted a missing DuckDB")
	}

	t.Setenv("PATH", fakeDashboardDuckDB(t, "v1.4.3"))
	if options, diagnostics, err := resolve(dashboardOptions{}); err != nil || options.sessionArchive ||
		!strings.Contains(diagnostics, "requires "+dashboardDuckDBVersion+" or newer") {
		t.Fatalf("old DuckDB was not reported: %v %q", err, diagnostics)
	}

	t.Setenv("PATH", fakeDashboardDuckDB(t, "v2.10.1"))
	if options, diagnostics, err := resolve(dashboardOptions{}); err != nil || options.sessionArchive ||
		!strings.Contains(diagnostics, "no approved content policy") {
		t.Fatalf("missing policy was not reported: %v %q", err, diagnostics)
	}
	if err := os.MkdirAll(filepath.Dir(dashboardSessionArchiveDefaultPolicy), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dashboardSessionArchiveDefaultPolicy, []byte(approved), 0o600); err != nil {
		t.Fatal(err)
	}
	options, diagnostics, err := resolve(dashboardOptions{})
	if err != nil || !options.sessionArchive || diagnostics != "" || !filepath.IsAbs(options.duckDB) ||
		options.sessionArchivePolicy != filepath.Join(work, dashboardSessionArchiveDefaultPolicy) {
		t.Fatalf("archive was not enabled automatically: %+v %v %q", options, err, diagnostics)
	}
	if options, _, err := resolve(dashboardOptions{noSessionArchive: true}); err != nil || options.sessionArchive {
		t.Fatalf("--no-session-archive did not disable the archive: %v", err)
	}
	if _, _, err := resolve(dashboardOptions{noSessionArchive: true, duckDB: "duckdb"}); err == nil {
		t.Fatal("--no-session-archive accepted a conflicting --duckdb")
	}
}

func TestDashboardSessionArchivePolicyRequiresExplicitApproval(t *testing.T) {
	directory := t.TempDir()
	for name, content := range map[string]string{
		"unapproved": `{"version":"v1","contentApproved":false,"rightsApproved":true,"events":{"user.message":["content"]}}`,
		"no-events":  `{"version":"v1","contentApproved":true,"rightsApproved":true,"events":{}}`,
		"malformed":  `{`,
	} {
		path := filepath.Join(directory, name+".json")
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := prepareDashboardSessionArchive(directory, dashboardOptions{sessionArchivePolicy: path}, io.Discard); err == nil {
			t.Fatalf("%s policy accepted", name)
		}
	}
}

func TestDashboardSessionArchiveFramesCompletedRunsWithArtifacts(t *testing.T) {
	cache := t.TempDir()
	policy := filepath.Join(cache, "approved.json")
	if err := os.WriteFile(policy, []byte(`{"version":"v1","contentApproved":true,"rightsApproved":true,"events":{"user.message":["content"]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	archive, err := prepareDashboardSessionArchive(cache, dashboardOptions{sessionArchivePolicy: policy, duckDB: "/opt/duckdb", window: 24 * time.Hour}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(archive.policy); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("policy copy is not private: %v", err)
	}
	if _, ok := archive.captureRequest(); ok {
		t.Fatal("capture requested before any frames were recorded")
	}
	logs := filepath.Join(cache, "logs")
	for _, id := range []string{"1", "2"} {
		if err := os.MkdirAll(filepath.Join(logs, "run-"+id), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	source := dashboardRepositoryEvidence{Repository: "Octo/Repo", Directory: logs, Runs: []map[string]any{
		{"databaseId": float64(1), "status": "completed", "updatedAt": "2026-01-02T03:04:05Z"},
		{"databaseId": float64(2), "status": "in_progress", "updatedAt": "2026-01-02T03:04:05Z"},
		{"databaseId": float64(3), "status": "completed", "updatedAt": "2026-01-02T03:04:05Z"},
	}}
	runs := []map[string]any{
		{"repositoryFullName": "Octo/Repo", "githubRunId": "1", "attempt": float64(2)},
		{"repositoryFullName": "Octo/Repo", "githubRunId": "2", "attempt": float64(1)},
		{"repositoryFullName": "Octo/Repo", "githubRunId": "3", "attempt": float64(1)},
		{"repositoryFullName": "other/repo", "githubRunId": "1", "attempt": float64(1)},
	}
	if err := archive.writeFrames(source, runs, time.Now()); err != nil {
		t.Fatal(err)
	}
	frames := filepath.Join(archive.frames, dashboardCoordinate("Octo/Repo"))
	entries, err := os.ReadDir(frames)
	if err != nil || len(entries) != 1 || entries[0].Name() != "run-1-2.jsonl" {
		t.Fatalf("frames = %v, %v", entries, err)
	}
	content, err := os.ReadFile(filepath.Join(frames, "run-1-2.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var frame struct {
		Kind string            `json:"kind"`
		Run  map[string]string `json:"run"`
	}
	if err := json.Unmarshal(content, &frame); err != nil || frame.Kind != "run" || frame.Run["run_id"] != "1" ||
		frame.Run["run_attempt"] != "2" || frame.Run["repository"] != "Octo/Repo" || frame.Run["organization"] != "Octo" ||
		frame.Run["status"] != "completed" || frame.Run["updated_at"] != "2026-01-02T03:04:05Z" {
		t.Fatalf("frame = %s", content)
	}
	request, ok := archive.captureRequest()
	if !ok || !strings.HasSuffix(string(request), "\n") {
		t.Fatalf("request = %q", request)
	}
	var decoded struct {
		Type    string `json:"type"`
		Sources []struct{ Repository, Logs, Frames string }
	}
	if err := json.Unmarshal(request, &decoded); err != nil || decoded.Type != "session-archive-capture" ||
		len(decoded.Sources) != 1 || decoded.Sources[0].Logs != logs || decoded.Sources[0].Frames != frames {
		t.Fatalf("request = %s", request)
	}
	environment := strings.Join(archive.environment("local", []string{"Octo/Repo"}), "\n")
	for _, expected := range []string{"CAO_SESSION_ARCHIVE_DIRECTORY=" + archive.store, "CAO_SESSION_ARCHIVE_POLICY=" + archive.policy,
		"CAO_SESSION_ARCHIVE_DUCKDB=/opt/duckdb", "CAO_SESSION_ARCHIVE_REPOSITORIES=Octo/Repo"} {
		if !strings.Contains(environment, expected) {
			t.Fatalf("environment lacks %s", expected)
		}
	}
}
