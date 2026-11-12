package collect

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/githubnext/gh-cao/server/internal/ingest"
)

func TestRunnerCompactionAndReplayKeepCollidingRepositoriesIsolated(t *testing.T) {
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
	runner := Runner{Lake: lake, CatalogRoot: catalog, NodeBinary: node}
	for _, repository := range repositories {
		if err := runner.compact(t.Context(), repository); err != nil {
			t.Fatal(err)
		}
		paths, err := lake.repositoryShards(t.Context(), lake.ShardDirectory(), repository)
		if err != nil || len(paths) != 1 {
			t.Fatalf("repository compaction: %v, %v", paths, err)
		}
	}
	compactActivitySnapshot(t, lake)
	if _, _, _, err := ingest.ValidateManifest(lake.Directory); err != nil {
		t.Fatalf("compacted lake is not replayable: %v", err)
	}
	if err := lake.ForgetContext(t.Context(), repositories[0]); err != nil {
		t.Fatal(err)
	}
	compactActivitySnapshot(t, lake)
	retained, err := lake.RetainedRepositories(t.Context(), 10)
	if err != nil || len(retained) != 1 || retained[0] != repositories[1] {
		t.Fatalf("withdrawal affected colliding repository: %v, %v", retained, err)
	}
	if _, _, _, err := ingest.ValidateManifest(lake.Directory); err != nil {
		t.Fatalf("post-withdrawal lake is not replayable: %v", err)
	}
}

// compactActivitySnapshot uses the independent Actions producer as a test
// oracle. Projector only replays its output; it cannot regenerate it.
func compactActivitySnapshot(t *testing.T, lake Lake) {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required for the independent Activity snapshot oracle")
	}
	catalog, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	// #nosec G204 -- independent checked-in oracle with test-owned snapshot paths.
	command := exec.CommandContext(t.Context(), node,
		filepath.Join(catalog, "activity", "cao.mjs"), "hash-payloads",
		"--shard-dir", lake.ShardDirectory(),
		"--runs-dir", lake.RunsDirectory(),
		"--records-dir", lake.RecordsDirectory(),
		"--inventory", lake.InventoryPath(),
		"--output", lake.ManifestPath())
	command.Dir = catalog
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("Activity snapshot oracle failed: %v: %s", err, output)
	}
}

func TestValidateActivityCatalogRequiresANonBlankRoot(t *testing.T) {
	err := validateActivityCatalog("  ", os.Stat)
	if err == nil {
		t.Fatal("expected an error for a blank catalog root")
	}
	if !strings.Contains(err.Error(), "catalog root") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateActivityCatalogRequiresTheCompactorScript(t *testing.T) {
	directory := t.TempDir()
	if err := validateActivityCatalog(directory, os.Stat); err == nil {
		t.Fatal("expected an error when activity/cao.mjs is missing")
	}
	if err := os.MkdirAll(filepath.Join(directory, "activity"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "activity", "cao.mjs"), []byte("//"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateActivityCatalog(directory, os.Stat); err != nil {
		t.Fatalf("expected a populated catalog root to validate, got: %v", err)
	}
}

func TestValidateWorkflowCatalogRequiresACompiledWorkflow(t *testing.T) {
	directory := t.TempDir()
	if err := validateWorkflowCatalog(directory); err == nil {
		t.Fatal("expected an error when .github/workflows is missing")
	}
	workflows := filepath.Join(directory, ".github", "workflows")
	if err := os.MkdirAll(workflows, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workflows, "example.md"), []byte("# Example\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateWorkflowCatalog(directory); err == nil {
		t.Fatal("expected an error when no compiled workflow is present")
	}
	if err := os.WriteFile(filepath.Join(workflows, "example.lock.yml"), []byte("name: Example\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateWorkflowCatalog(directory); err != nil {
		t.Fatalf("expected a compiled workflow to validate, got: %v", err)
	}
}

func writeValidCollectionCatalog(t *testing.T, directory string) {
	t.Helper()
	for path, content := range map[string]string{
		filepath.Join("activity", "cao.mjs"):                      "//",
		filepath.Join(".github", "workflows", "example.lock.yml"): "name: Example\n",
	} {
		target := filepath.Join(directory, path)
		if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRunnerValidateFailsOnAMissingTokenProvider(t *testing.T) {
	directory := t.TempDir()
	writeValidCollectionCatalog(t, directory)
	runner := Runner{
		Lake:        Lake{Directory: filepath.Join(directory, "lake")},
		CatalogRoot: directory,
	}
	err := runner.Validate()
	if err == nil {
		t.Fatal("expected an error when no token provider is configured")
	}
	if !strings.Contains(err.Error(), "installation token provider") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// fakeTokens satisfies TokenProvider without contacting GitHub.
type fakeTokens struct{ token string }

func (f fakeTokens) InstallationToken(context.Context, int64) (string, error) {
	return f.token, nil
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

func TestRunnerReproducesTheActionsCollectionCommand(t *testing.T) {
	workspace := t.TempDir()
	nodeRecordPath := filepath.Join(workspace, "node-args")
	catalogRoot := filepath.Join(workspace, "catalog")
	writeValidCollectionCatalog(t, catalogRoot)
	gh := writeFakeBinary(t, workspace, "gh", `exit 99`)
	node := writeFakeBinary(t, workspace, "node",
		`printf '%s\n' "$@" >> `+nodeRecordPath+`; printf 'token=%s\n' "$GH_TOKEN" >> `+nodeRecordPath)
	lake := Lake{Directory: filepath.Join(workspace, "lake")}
	runner := Runner{
		Lake: lake, CatalogRoot: catalogRoot,
		GitHubBinary: gh, NodeBinary: node,
		Tokens: fakeTokens{token: "installation-token"},
	}
	if err := runner.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := lake.Prepare(); err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(lake.ShardDirectory(), legacyShardPrefix("octo/api")+"1.jsonl")
	writeIdentityShard(t, legacy, "octo/api")
	if err := runner.Collect(context.Background(), Task{
		Repository: "Octo/API", InstallationID: 5,
	}); err != nil {
		t.Fatal(err)
	}
	recorded, err := os.ReadFile(nodeRecordPath) // #nosec G304 -- the path is built from the test's own temporary directory.
	if err != nil {
		t.Fatal(err)
	}
	arguments := string(recorded)
	for _, expected := range []string{
		"activity/collect-workflow-logs.mjs\n" + gh + "\n--audit", "--repo\nocto/api", "--artifacts\nusage,experiment\n",
		"--start-date\n-30d", "--cache-before\n-30d", "--count\n10000",
		"--max-storage\n1200", "--prune-older-runs", "token=installation-token",
	} {
		if !strings.Contains(arguments, expected) {
			t.Fatalf("collection command is missing %q:\n%s", expected, arguments)
		}
	}
	if !strings.Contains(arguments, "--max-github-api-rate-limit\n-2000") {
		t.Fatalf("expected a negative reserve to be passed:\n%s", arguments)
	}
	if !strings.Contains(arguments, filepath.Join(lake.ShardDirectory(), lake.ShardPrefix("octo/api"))+"*") {
		t.Fatalf("expected a repository-scoped shard prefix:\n%s", arguments)
	}
	if _, err := os.Stat(filepath.Join(lake.ShardDirectory(), lake.ShardPrefix("octo/api")+"legacy-1.jsonl")); err != nil {
		t.Fatalf("legacy evidence was not migrated for cache reuse: %v", err)
	}
	if strings.Contains(arguments, legacyShardPrefix("octo/api")+"*") {
		t.Fatal("acquisition still uses a lossy legacy cache selector")
	}
	if !strings.Contains(arguments, "--group\nocto/api="+lake.ShardPrefix("octo/api")) {
		t.Fatal("compaction does not use acquisition's versioned identity")
	}
	collection := strings.Index(arguments, "activity/collect-workflow-logs.mjs")
	evidence := strings.Index(arguments, "activity/session-evidence.mjs")
	compaction := strings.Index(arguments, "compact-jsonl")
	if collection < 0 || evidence <= collection || compaction <= evidence {
		t.Fatal("expected explicit workflow collection, then session evidence, then compaction")
	}
}

func TestRunnerDoesNotLeakAmbientCredentials(t *testing.T) {
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
