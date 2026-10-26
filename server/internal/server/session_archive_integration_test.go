package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/adxingest"
	"github.com/githubnext/gh-cao/server/internal/adxtrace"
	"github.com/githubnext/gh-cao/server/internal/sessionarchive"
)

func archiveRuntime(t *testing.T) string {
	t.Helper()
	path := os.Getenv("DUCKDB_PATH")
	if path == "" {
		path = "duckdb"
	}
	if _, err := exec.LookPath(path); err != nil {
		if os.Getenv("CAO_SESSION_ARCHIVE_REQUIRE_DUCKDB") == "1" {
			t.Fatal("real pinned DuckDB runtime is required")
		}
		t.Skip("real pinned DuckDB unavailable")
	}
	return path
}

func TestSessionArchiveLocalBackfillIntegration(t *testing.T) {
	duckdb := archiveRuntime(t)
	if runtime.GOOS == "windows" {
		t.Fatal("approved private archive requires a POSIX host")
	}
	if _, err := exec.LookPath("node"); err != nil {
		t.Fatal("production local acquisition integration requires node")
	}
	original, _ := newSQLiteTestApp(t, false)
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	snapshot := filepath.Join(t.TempDir(), "snapshot.sqlite")
	config := original.config
	config.Database = nil
	config.DatabaseQueriesPath, err = filepath.Abs(config.DatabaseQueriesPath)
	if err != nil {
		t.Fatal(err)
	}
	config.ADX, config.ADXTraces = &adxingest.Config{}, &adxtrace.Config{}
	config.SessionArchive = &sessionarchive.Config{
		Enabled: true, DurableStorage: true, Directory: t.TempDir(), Namespace: "integration",
		Repositories: []string{"octo/one"}, DuckDBPath: duckdb,
		Policy: adxtrace.Policy{Version: "test-approved", ContentApproved: true, RightsApproved: true,
			Events: map[string][]string{"session.format": {"version"}, "session.collection": {"complete"},
				"user.message": {"content"}, "tool.execution_complete": {"output", "error", "success", "toolCallId"}}},
	}
	config.SessionArchiveRead = &SessionArchiveReadAccess{Approved: true}
	// #nosec G302 -- the private archive directory requires owner traversal.
	if err := os.Chmod(config.SessionArchive.Directory, 0o700); err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	now := time.Now().UTC().Truncate(time.Second)
	raw := `{"type":"session.format","data":{"version":1}}
{"type":"session.collection","data":{"complete":true}}
{"type":"user.message","data":{"content":"full approved acquired message"}}
{"id":"tool","type":"tool.execution_complete","data":{"toolCallId":"call","output":{"array":[null,true,false,0,9007199254740993,1.25],"text":"approved content"},"error":null,"success":false}}
`
	source := filepath.Join(directory, "source.jsonl")
	if err := os.WriteFile(source, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	fixture := filepath.Join(directory, "run.jsonl")
	record, err := json.Marshal(map[string]any{"schema_version": 2, "kind": "run", "run": map[string]any{
		"organization": "octo", "repository": "one", "run_id": 124, "run_attempt": 1,
		"workflow_name": "test", "workflow_path": ".github/workflows/test.lock.yml",
		"created_at": now.Add(-time.Minute).Format(time.RFC3339), "updated_at": now.Format(time.RFC3339),
		"completed_at": now.Format(time.RFC3339), "status": "completed", "conclusion": "success",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fixture, append(record, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	// #nosec G204 -- fixed production ingestion entry point and test-owned files.
	initialize := exec.CommandContext(t.Context(), "node", "activity/cao.mjs", "ingest-jsonl",
		"--database", snapshot, "--input", fixture)
	initialize.Dir = root
	if output, err := initialize.CombinedOutput(); err != nil {
		t.Fatalf("initialize production SQLite snapshot: %v\n%s", err, output)
	}
	// #nosec G304 -- this immutable snapshot is created in the test's private directory.
	before, err := os.ReadFile(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	gh := `#!/bin/sh
set -eu
if [ "$1" = api ]; then
  printf '[{"workflows":[{"path":".github/workflows/test.lock.yml"}]}]\n'
  exit 0
fi
test "$1" = aw
test "$GH_TOKEN" = archive-integration-token
cache=''
output=''
while [ "$#" -gt 0 ]; do
  case "$1" in
    --cached-jsonl) cache="${2%\*}"; shift ;;
    --output) output="$2"; shift ;;
  esac
  shift
done
mkdir -p "$output/run-124/usage"
cp "` + source + `" "$output/run-124/usage/aw_session.jsonl"
printf '{"repository":"octo/one","run_id":124,"run_attempt":"1"}\n' > "$output/run-124/usage/aw_info.json"
cp "` + fixture + `" "${cache}1.jsonl"
`
	if err := os.WriteFile(filepath.Join(directory, "gh"), []byte(gh), 0o700); err != nil { // #nosec G306 -- executable test-only acquisition stub.
		t.Fatal(err)
	}
	t.Setenv("PATH", directory+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("GH_TOKEN", "archive-integration-token")
	overlay := filepath.Join(directory, "overlay.sqlite")
	app, err := NewLocalSQLiteOverlayApp(t.Context(), snapshot, overlay, time.Hour, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.Stop(context.WithoutCancel(t.Context())) })
	if app.localBackfill == nil || app.localBackfill.traces == nil || app.adx != nil || app.adxTraces != nil {
		t.Fatal("archive did not wire local acquisition independently of ADX")
	}
	if err := app.localBackfill.refresh(t.Context(), log.New(io.Discard, "", 0), func(int64) {}); err != nil {
		t.Fatal(err)
	}
	if app.sessionArchive.backend.Status().StagedTraces != 1 {
		t.Fatal("production acquisition did not durably admit full evidence before cleanup")
	}
	if err := app.sessionArchive.backend.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	body := `{"kind":"timeline","repository":"octo/one","runId":"124","attempt":1}`
	response := archiveRequest(t, app, body, true)
	var result sessionArchiveResult
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || response.Code != 200 || result.Generation != 1 || len(result.Events) != 4 {
		t.Fatalf("real archive HTTP roundtrip: %d %s %v", response.Code, response.Body.String(), err)
	}
	payload := result.Events[3].PayloadJSON
	if !strings.Contains(payload, `"error":null`) || !strings.Contains(payload, `9007199254740993`) ||
		!strings.Contains(payload, `"success":false`) || result.Events[3].RunID != "124" || result.Events[3].RunAttempt != 1 {
		t.Fatalf("approved JSON types, content, or identity lost: %s", payload)
	}
	ordinary := httptest.NewRequestWithContext(t.Context(), "POST", "http://localhost/api/v1/query",
		strings.NewReader(`{"sourceNames":["runs"]}`))
	authorize(ordinary)
	operational := httptest.NewRecorder()
	app.Handler().ServeHTTP(operational, ordinary)
	if operational.Code != 200 || strings.Contains(operational.Body.String(), "full approved acquired message") {
		t.Fatalf("archive changed ordinary dashboard query behavior: %d", operational.Code)
	}
	// #nosec G304 -- compare the same test-owned snapshot to detect accidental mutation.
	after, err := os.ReadFile(snapshot)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("archive acquisition modified the immutable deployed snapshot")
	}
	if err := app.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
	restarted, err := NewLocalSQLiteOverlayApp(t.Context(), snapshot, overlay, 0, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restarted.Stop(context.WithoutCancel(t.Context())) })
	response = archiveRequest(t, restarted, body, true)
	var recovered sessionArchiveResult
	if err := json.Unmarshal(response.Body.Bytes(), &recovered); err != nil || response.Code != 200 || !reflect.DeepEqual(recovered, result) {
		t.Fatalf("offline restart lost complete approved publication: %d %v", response.Code, err)
	}
	local := restarted.sessionArchive.backend.(*localSessionArchive)
	if err := local.Withdraw(t.Context(), "octo/one", "124", 1); err != nil {
		t.Fatal(err)
	}
	if response := archiveRequest(t, restarted, body, true); response.Code != 404 && response.Code != 403 {
		t.Fatalf("withdrawn trace remained accessible: %d", response.Code)
	}
}

func TestSessionArchiveScopeRejectsBeforeRuntimeOrAcquisition(t *testing.T) {
	original, snapshot := newSQLiteTestApp(t, false)
	config := original.config
	config.Database = nil
	config.SessionArchive = &sessionarchive.Config{
		Enabled: true, DurableStorage: true, Directory: t.TempDir(), Namespace: "integration",
		Repositories: []string{"octo/not-enrolled"}, DuckDBPath: "/must-not-be-executed",
		Policy: adxtrace.Policy{Version: "test-approved", ContentApproved: true, RightsApproved: true,
			Events: map[string][]string{"user.message": {"content"}}},
	}
	if _, err := NewLocalSQLiteOverlayApp(t.Context(), snapshot, filepath.Join(t.TempDir(), "delta.sqlite"), time.Minute, config); err == nil ||
		!strings.Contains(err.Error(), "contained in the local snapshot collection scope") {
		t.Fatalf("archive scope bypassed immutable local collection authority: %v", err)
	}
	files, err := os.ReadDir(config.SessionArchive.Directory)
	if err != nil || len(files) != 0 {
		t.Fatal("rejected scope touched archive files")
	}
}

type sessionArchiveResponseHook struct {
	sessionArchiveBackend
	afterQuery func()
}

func (backend *sessionArchiveResponseHook) Query(ctx context.Context, query sessionArchiveQuery) (sessionArchiveResult, error) {
	result, err := backend.sessionArchiveBackend.Query(ctx, query)
	if err == nil {
		backend.afterQuery()
	}
	return result, err
}

func TestSessionArchiveWithdrawalBeforeResponseIntegration(t *testing.T) {
	duckdb := archiveRuntime(t)
	for _, kind := range []string{"timeline", "summary"} {
		for _, withdrawal := range []string{"trace", "repository"} {
			t.Run(kind+"/"+withdrawal, func(t *testing.T) {
				app, _ := newSQLiteTestApp(t, false)
				config := sessionarchive.Config{
					Enabled: true, DurableStorage: true, Directory: t.TempDir(), Namespace: "response",
					Repositories: []string{"octo/one"}, DuckDBPath: duckdb,
					Policy: adxtrace.Policy{Version: "approved-v1", ContentApproved: true, RightsApproved: true,
						Events: map[string][]string{"session.format": {"version"}, "session.collection": {"complete"},
							"user.message": {"content"}}},
				}
				// #nosec G302 -- the private archive directory requires owner traversal.
				if err := os.Chmod(config.Directory, 0o700); err != nil {
					t.Fatal(err)
				}
				archive, err := sessionarchive.Open(config)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = archive.Close() })
				raw := []byte("{\"type\":\"session.format\",\"data\":{\"version\":1}}\n" +
					"{\"type\":\"session.collection\",\"data\":{\"complete\":true}}\n" +
					"{\"type\":\"user.message\",\"data\":{\"content\":\"withdrawn synthetic content\"}}\n")
				projection, err := sessionarchive.Project(adxtrace.Config{
					Enabled: true, DurableStorage: true, Directory: config.Directory,
					Repositories: config.Repositories, Policy: config.Policy, Mode: "full",
				}, config.Namespace, "octo/one", "123", time.Now().UTC().Format(time.DateOnly), 1, raw, time.Now())
				if err != nil {
					t.Fatal(err)
				}
				if err := archive.Admit(t.Context(), projection); err != nil {
					t.Fatal(err)
				}
				if err := archive.Flush(t.Context()); err != nil {
					t.Fatal(err)
				}
				backend := &sessionArchiveResponseHook{
					sessionArchiveBackend: &localSessionArchive{Archive: archive, namespace: config.Namespace},
					afterQuery: func() {
						var err error
						if withdrawal == "trace" {
							err = archive.Withdraw(t.Context(), "octo/one", "123", 1)
						} else {
							err = archive.WithdrawRepository(t.Context(), "octo/one")
						}
						if err != nil {
							t.Fatal(err)
						}
					},
				}
				app.sessionArchive = &sessionArchiveService{
					backend: backend, repositories: config.Repositories, read: SessionArchiveReadAccess{Approved: true},
				}
				body := `{"kind":"summary","repository":"octo/one"}`
				if kind == "timeline" {
					body = `{"kind":"timeline","repository":"octo/one","runId":"123","attempt":1}`
				}
				response := archiveRequest(t, app, body, true)
				if response.Code != http.StatusForbidden || strings.Contains(response.Body.String(), "withdrawn synthetic content") ||
					strings.Contains(response.Body.String(), `"events"`) || strings.Contains(response.Body.String(), `"summary"`) {
					t.Fatalf("completed query released withdrawn evidence: status %d", response.Code)
				}
			})
		}
	}
}
