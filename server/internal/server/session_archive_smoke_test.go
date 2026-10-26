package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/adxingest"
	"github.com/githubnext/gh-cao/server/internal/adxtrace"
	"github.com/githubnext/gh-cao/server/internal/sessionarchive"
)

func TestSessionArchiveLocalHTTPIntegration(t *testing.T) {
	duckdb := archiveRuntime(t)
	original, snapshot := newSQLiteTestApp(t, false)
	config := original.config
	config.Database = nil
	config.ADX, config.ADXTraces = &adxingest.Config{}, &adxtrace.Config{}
	config.SessionArchive = &sessionarchive.Config{
		Enabled: true, DurableStorage: true, Directory: t.TempDir(), Namespace: "http-smoke",
		Repositories: []string{"octo/one"}, DuckDBPath: duckdb,
		Policy: adxtrace.Policy{Version: "synthetic-v1", ContentApproved: true, RightsApproved: true,
			Events: map[string][]string{"session.format": {"version"}, "session.collection": {"complete"},
				"user.message": {"content"}}},
	}
	config.SessionArchiveRead = &SessionArchiveReadAccess{Approved: true}
	// #nosec G302 -- the private archive directory requires owner traversal.
	if err := os.Chmod(config.SessionArchive.Directory, 0o700); err != nil {
		t.Fatal(err)
	}
	app, err := NewLocalSQLiteOverlayApp(t.Context(), snapshot, filepath.Join(t.TempDir(), "overlay.sqlite"), 0, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := app.Stop(context.WithoutCancel(t.Context())); err != nil {
			t.Error(err)
		}
	})
	if err := app.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	if app.sessionArchive == nil {
		t.Fatal("local server did not configure the session archive")
	}

	logs := t.TempDir()
	usage := filepath.Join(logs, "run-123", "usage")
	if err := os.MkdirAll(usage, 0o700); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		filepath.Join(usage, "aw_session.jsonl"): `{"type":"session.format","data":{"version":1}}
{"type":"session.collection","data":{"complete":true}}
{"type":"user.message","data":{"content":"synthetic smoke-test message"}}
`,
		filepath.Join(usage, "aw_info.json"): `{"repository":"octo/one","run_id":123,"run_attempt":1}`,
	}
	prefix := filepath.Join(t.TempDir(), "octo-one")
	shard, err := json.Marshal(map[string]any{"kind": "run", "run": map[string]any{
		"repository": "octo/one", "run_id": 123, "run_attempt": 1, "status": "completed",
		"completed_at": time.Now().UTC().Format(time.RFC3339),
	}})
	if err != nil {
		t.Fatal(err)
	}
	files[prefix+".jsonl"] = string(shard) + "\n"
	for path, content := range files {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := app.sessionArchive.Capture(t.Context(), "octo/one", logs, prefix); err != nil {
		t.Fatal(err)
	}
	if err := app.sessionArchive.backend.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(app.Handler())
	t.Cleanup(server.Close)
	client := server.Client()
	client.Timeout = 10 * time.Second
	body := `{"kind":"session-query","repository":"octo/one","language-version":"0.1.0","query":{
"name":"synthetic-session-smoke","subject":"Verify captured synthetic events are queryable.","from":"archive-events",
"filter":{"predicates":[{"field":"RunId","equals":"123"},{"field":"RunAttempt","equals":1}]},
"select":[{"field":"Sequence"},{"field":"Type"}],"order-by":[{"field":"Sequence","direction":"asc"}],"limit":10}}`
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost,
		server.URL+"/api/v1/session-archive/query", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	authorize(request)
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("session query returned %s", response.Status)
	}
	var result sessionArchiveResult
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	want := []map[string]any{
		{"Sequence": float64(0), "Type": "session.format"},
		{"Sequence": float64(1), "Type": "session.collection"},
		{"Sequence": float64(2), "Type": "user.message"},
	}
	if result.Kind != "session-query" || result.Generation != 1 || result.Rows == nil ||
		!reflect.DeepEqual(*result.Rows, want) || result.Metadata == nil || result.Metadata.Limited {
		t.Fatalf("captured session was not fully queryable: %+v", result)
	}
}
