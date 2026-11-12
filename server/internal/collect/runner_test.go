package collect

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/collectnative"
	"github.com/githubnext/gh-cao/server/internal/ingest"
)

func TestActivityCompactionAndReplayKeepCollidingRepositoriesIsolated(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required for the production Activity compactor")
	}
	catalog, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	lake := Lake{Directory: t.TempDir()}
	if err := lake.Prepare(); err != nil {
		t.Fatal(err)
	}
	repositories := []string{"octo-a/b", "octo/a-b"}
	for i, repository := range repositories {
		owner, _, _ := strings.Cut(repository, "/")
		for part := range 2 {
			content := fmt.Sprintf(`{"schema_version":2,"kind":"run","run":{"organization":"%s","repository":"%s","run_id":%d,"run_attempt":1,"workflow_name":"test","workflow_path":".github/workflows/test.lock.yml","created_at":"2026-10-20T00:00:00Z"}}`+"\n", owner, repository, 10*i+part+1)
			path := filepath.Join(lake.ShardDirectory(), fmt.Sprintf("%s%d-%d.jsonl", legacyShardPrefix(repository), i, part))
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		if err := lake.MigrateRepositoryShards(t.Context(), repository); err != nil {
			t.Fatal(err)
		}
	}
	for _, repository := range repositories {
		// #nosec G204 -- invokes the checked-in Activity CLI with test-owned paths.
		command := exec.CommandContext(t.Context(), node, filepath.Join(catalog, "activity", "cao.mjs"),
			"compact-jsonl", "--input-dir", lake.ShardDirectory(),
			"--group", repository+"="+lake.ShardPrefix(repository))
		command.Dir = catalog
		command.Env = collectionEnvironment("")
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("Activity compaction: %v: %s", err, output)
		}
		paths, err := lake.repositoryShards(t.Context(), lake.ShardDirectory(), repository)
		if err != nil || len(paths) != 1 {
			t.Fatalf("repository compaction: %v, %v", paths, err)
		}
	}
	projector := Projector{Lake: lake, CatalogRoot: catalog, NodeBinary: node}
	if err := projector.refreshManifest(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := ingest.ValidateManifest(lake.Directory); err != nil {
		t.Fatalf("compacted lake is not replayable: %v", err)
	}
	if err := lake.ForgetContext(t.Context(), repositories[0]); err != nil {
		t.Fatal(err)
	}
	if err := projector.refreshManifest(t.Context()); err != nil {
		t.Fatal(err)
	}
	retained, err := lake.RetainedRepositories(t.Context(), 10)
	if err != nil || len(retained) != 1 || retained[0] != repositories[1] {
		t.Fatalf("withdrawal affected colliding repository: %v, %v", retained, err)
	}
	if _, _, _, err := ingest.ValidateManifest(lake.Directory); err != nil {
		t.Fatalf("post-withdrawal lake is not replayable: %v", err)
	}
}

func TestRunnerRejectsRetiredCollectionWithoutSideEffects(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	for _, task := range []Task{
		{Repository: "octo/api", InstallationID: 7},
		{Repository: "../invalid"},
	} {
		lake := Lake{Directory: filepath.Join(t.TempDir(), "must-not-create")}
		runner := Runner{Lake: lake}
		if err := runner.Validate(); !errors.Is(err, collectnative.ErrUnsupported) {
			t.Fatalf("retired validation did not fail closed: %v", err)
		}
		if err := runner.Collect(t.Context(), task); !errors.Is(err, collectnative.ErrUnsupported) {
			t.Fatalf("retired collection did not fail closed: %v", err)
		}
		if _, err := os.Stat(lake.Directory); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("retired collection touched the lake: %v", err)
		}
	}
}

func TestRunnerRecoveryTimeout(t *testing.T) {
	for _, timeout := range []time.Duration{0, -time.Second, time.Minute} {
		want := timeout
		if want <= 0 {
			want = 20 * time.Minute
		}
		if got := (Runner{Timeout: timeout}).timeout(); got != want {
			t.Fatalf("recovery timeout = %v, want %v", got, want)
		}
	}
}

// writeFakeBinary installs an executable that records its arguments and
// environment so the test can assert the collection command line without
// running the real GitHub CLI.
func writeFakeBinary(t *testing.T, directory, name, body string) string {
	t.Helper()
	path := filepath.Join(directory, name)
	script := "#!/bin/sh\n" + body + "\n"
	// #nosec G306 -- the fake binary must be executable by the test process.
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCollectionEnvironmentDoesNotLeakAmbientCredentials(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "ambient-secret")
	t.Setenv("AZURE_CLIENT_SECRET", "ambient-secret")
	environment := collectionEnvironment("")
	for _, entry := range environment {
		if strings.Contains(entry, "ambient-secret") {
			t.Fatalf("collection environment leaked an ambient credential: %q", entry)
		}
	}
}

func TestCollectionEnvironmentUsesConfiguredGitHubAPIURL(t *testing.T) {
	t.Setenv("GITHUB_API_URL", "https://api.github.com")
	environment := collectionEnvironment("http://127.0.0.1:8081")
	for _, entry := range environment {
		if strings.HasPrefix(entry, "GITHUB_API_URL=") {
			if got := strings.TrimPrefix(entry, "GITHUB_API_URL="); got != "http://127.0.0.1:8081" {
				t.Fatalf("collection API URL = %q, want simulator URL", got)
			}
			return
		}
	}
	t.Fatal("collection environment omitted configured GitHub API URL")
}

func TestLakeUsesThePublishedSnapshotLayout(t *testing.T) {
	lake := Lake{Directory: t.TempDir()}
	if err := lake.Prepare(); err != nil {
		t.Fatal(err)
	}
	populated, err := lake.Populated()
	if err != nil {
		t.Fatal(err)
	}
	if populated {
		t.Fatal("an empty lake must not report as populated")
	}
	layout := map[string]string{
		"shards":   "gh-aw-logs-shards",
		"runs":     "gh-aw-logs-runs",
		"records":  "gh-aw-logs-records",
		"manifest": "payload-hashes.json",
		"sources":  "inventory-sources.json",
	}
	actual := map[string]string{
		"shards":   filepath.Base(lake.ShardDirectory()),
		"runs":     filepath.Base(lake.RunsDirectory()),
		"records":  filepath.Base(lake.RecordsDirectory()),
		"manifest": filepath.Base(lake.ManifestPath()),
		"sources":  filepath.Base(lake.InventoryPath()),
	}
	for key, want := range layout {
		if actual[key] != want {
			t.Fatalf("%s directory = %q, want %q", key, actual[key], want)
		}
	}
	// Populated means projectable: compacted run shards plus a manifest. Raw
	// collected shards alone are not enough, because projection validates the
	// manifest before reading anything.
	raw := filepath.Join(lake.ShardDirectory(), lake.ShardPrefix("octo/api")+"1.jsonl")
	if err := WriteFileAtomic(raw, []byte("{}\n")); err != nil {
		t.Fatal(err)
	}
	populated, err = lake.Populated()
	if err != nil {
		t.Fatal(err)
	}
	if populated {
		t.Fatal("raw shards without a manifest must not report as populated")
	}
	if err := WriteFileAtomic(filepath.Join(lake.RunsDirectory(), "runs.jsonl"), []byte("{}\n")); err != nil {
		t.Fatal(err)
	}
	if err := WriteFileAtomic(lake.ManifestPath(), []byte("{}")); err != nil {
		t.Fatal(err)
	}
	populated, err = lake.Populated()
	if err != nil {
		t.Fatal(err)
	}
	if !populated {
		t.Fatal("a lake holding shards must report as populated")
	}
}

func TestWriteFileAtomicLeavesNoPartialFile(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "nested", "manifest.json")
	payload, err := json.Marshal(map[string]string{"a": "b"})
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteFileAtomic(path, payload); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "manifest.json" {
		t.Fatalf("unexpected directory contents: %v", entries)
	}
}
