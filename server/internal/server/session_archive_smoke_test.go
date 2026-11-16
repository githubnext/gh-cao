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
	original, snapshot := newLocalPostgresTestApp(t, false)
	config := original.config
	config.ADX, config.ADXTraces = &adxingest.Config{}, &adxtrace.Config{}
	config.SessionArchive = &sessionarchive.Config{
		Enabled: true, DurableStorage: true, Directory: t.TempDir(), Namespace: "http-smoke",
		Repositories: []string{"octo/one"}, DuckDBPath: duckdb,
		Policy: adxtrace.Policy{Version: "synthetic-v1", ContentApproved: true, RightsApproved: true,
			Events: map[string][]string{"session.format": {"version"}, "session.collection": {"complete"},
				"user.message": {"content"}, "tool.execution_complete": {"output"}}},
	}
	config.SessionArchiveRead = &SessionArchiveReadAccess{Approved: true}
	// #nosec G302 -- the private archive directory requires owner traversal.
	if err := os.Chmod(config.SessionArchive.Directory, 0o700); err != nil {
		t.Fatal(err)
	}
	app := snapshot.app(t, config)
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
{"id":"collect","type":"session.collection","data":{"complete":true}}
{"id":"x' OR true --","type":"user.message","data":{"content":"synthetic smoke-test message"}}
{"id":"Z","type":"tool.execution_complete","data":{"output":"synthetic private tool result"}}
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
	largeUsage := filepath.Join(logs, "run-9007199254740993", "usage")
	if err := os.MkdirAll(largeUsage, 0o700); err != nil {
		t.Fatal(err)
	}
	largePrefix := filepath.Join(t.TempDir(), "octo-one-large")
	for path, content := range map[string]string{
		filepath.Join(largeUsage, "aw_session.jsonl"): `{"type":"session.format","data":{"version":1}}
{"id":"collect","type":"session.collection","data":{"complete":true}}
{"id":"z","timestamp":"2026-11-14T12:00:00Z","type":"user.message","data":{"content":"synthetic large-attempt message"}}
`,
		filepath.Join(largeUsage, "aw_info.json"): `{"repository":"octo/one","run_id":9007199254740993,"run_attempt":9007199254740993}`,
		largePrefix + ".jsonl": `{"kind":"run","run":{"repository":"octo/one","run_id":9007199254740993,"run_attempt":9007199254740993,"status":"completed","completed_at":"2026-11-14T13:00:00Z"}}
`,
	} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := app.sessionArchive.Capture(t.Context(), "octo/one", logs, largePrefix); err != nil {
		t.Fatal(err)
	}
	if err := app.sessionArchive.backend.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	if status := app.sessionArchive.backend.Status(); status.ActiveTraces != 2 || status.StagedTraces != 0 {
		t.Fatalf("synthetic capture did not publish both complete traces: %+v", status)
	}

	app.config.AgentCatalogPath, app.config.MCPContractPath = testAgentCatalog, testMCPContract
	app.config.DashboardQueries, err = ParseDashboardQueries("../../../dashboard/site/src/agent/queries.generated.json")
	if err != nil {
		t.Fatal(err)
	}
	app.mcp, err = app.newMCPHandler()
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(app.Handler())
	t.Cleanup(server.Close)
	client := server.Client()
	client.Timeout = 10 * time.Second
	for _, test := range []struct {
		name, body, rows string
		columns          []sessionarchive.Column
		limited          bool
	}{
		{
			name: "captured-event-order",
			body: `{"kind":"session-query","repository":"octo/one","language-version":"0.2.0","query":{
"name":"synthetic-session-smoke","subject":"Verify captured synthetic events are queryable.","from":"archive-events",
"filter":{"predicates":[{"field":"RunId","equals":"123"},{"field":"RunAttempt","equals":1}]},
"select":[{"field":"Sequence"},{"field":"Type"}],"order-by":[{"field":"Sequence","direction":"asc"}],"limit":10}}`,
			columns: []sessionarchive.Column{{Name: "Sequence", Type: "integer"}, {Name: "Type", Type: "string"}},
			rows: `[{"Sequence":0,"Type":"session.format"},{"Sequence":1,"Type":"session.collection"},
{"Sequence":2,"Type":"user.message"},{"Sequence":3,"Type":"tool.execution_complete"}]`,
		},
		{
			name: "large-integer-identity",
			body: `{"kind":"session-query","repository":"octo/one","language-version":"0.2.0","query":{
"name":"large-integer-identity","subject":"Preserve acquired run and attempt precision.","from":"archive-events",
"filter":{"predicates":[{"field":"RunAttempt","equals":9007199254740993}]},
"select":[{"field":"RunId"},{"field":"RunAttempt"},{"field":"Sequence"}]}}`,
			columns: []sessionarchive.Column{{Name: "RunId", Type: "string"}, {Name: "RunAttempt", Type: "integer"}, {Name: "Sequence", Type: "integer"}},
			rows: `[{"RunId":"9007199254740993","RunAttempt":9007199254740993,"Sequence":0},
{"RunId":"9007199254740993","RunAttempt":9007199254740993,"Sequence":1},
{"RunId":"9007199254740993","RunAttempt":9007199254740993,"Sequence":2}]`,
		},
		{
			name: "observed-clock-range",
			body: `{"kind":"session-query","repository":"octo/one","language-version":"0.2.0","query":{
"name":"observed-clock-range","subject":"Filter supplied event clocks without inventing missing timestamps.","from":"archive-events",
"filter":{"predicates":[{"field":"Type","in":["user.message","USER.MESSAGE"]},
{"field":"Timestamp","gte":"2026-11-14T00:00:00Z","lt":"2026-11-15T00:00:00Z"}]},
"select":[{"field":"EventId"},{"field":"Timestamp"}]}}`,
			columns: []sessionarchive.Column{{Name: "EventId", Type: "string"}, {Name: "Timestamp", Type: "string"}},
			rows:    `[{"EventId":"z","Timestamp":"2026-11-14T12:00:00Z"}]`,
		},
		{
			name: "exact-counts-and-missing-values",
			body: `{"kind":"session-query","repository":"octo/one","language-version":"0.2.0","query":{
"name":"exact-counts","subject":"Count acquired event identity and clocks at their own grain.","from":"archive-events",
"aggregate":{"values":[{"field":"Sequence","as":"events","reducer":"count"},
{"field":"EventId","as":"event-ids","reducer":"count"},{"field":"EventId","as":"unique-ids","reducer":"distinct-count"},
{"field":"Timestamp","as":"timed-events","reducer":"count"},{"field":"Timestamp","as":"unique-clocks","reducer":"distinct-count"},
{"field":"TraceId","as":"sessions","reducer":"distinct-count"}]}}}`,
			columns: []sessionarchive.Column{{Name: "events", Type: "integer"}, {Name: "event-ids", Type: "integer"},
				{Name: "unique-ids", Type: "integer"}, {Name: "timed-events", Type: "integer"},
				{Name: "unique-clocks", Type: "integer"}, {Name: "sessions", Type: "integer"}},
			rows: `[{"events":7,"event-ids":5,"unique-ids":4,"timed-events":1,"unique-clocks":1,"sessions":2}]`,
		},
		{
			name: "quoted-literal-and-reserved-alias",
			body: `{"kind":"session-query","repository":"octo/one","language-version":"0.2.0","query":{
"name":"quoted-literal","subject":"Treat quoted acquired identifiers as data and quote output aliases.","from":"archive-events",
"filter":{"predicates":[{"field":"EventId","equals":"x' OR true --"}]},
"select":[{"field":"EventId","as":"where"},{"field":"Sequence","as":"event-count"}]}}`,
			columns: []sessionarchive.Column{{Name: "where", Type: "string"}, {Name: "event-count", Type: "integer"}},
			rows:    `[{"where":"x' OR true --","event-count":2}]`,
		},
		{
			name: "lexical-order-empty-last-at-limit",
			body: `{"kind":"session-query","repository":"octo/one","language-version":"0.2.0","query":{
"name":"lexical-event-ids","subject":"Order captured event identifiers with missing values last.","from":"archive-events",
"select":[{"field":"EventId"}],"order-by":[{"field":"EventId","direction":"asc"}],"limit":7}}`,
			columns: []sessionarchive.Column{{Name: "EventId", Type: "string"}},
			rows:    `[{"EventId":"Z"},{"EventId":"collect"},{"EventId":"collect"},{"EventId":"x' OR true --"},{"EventId":"z"},{"EventId":""},{"EventId":""}]`,
		},
		{
			name: "disclosed-top-n",
			body: `{"kind":"session-query","repository":"octo/one","language-version":"0.2.0","query":{
"name":"top-event-ids","subject":"Select the two highest event identifiers and disclose omitted rows.","from":"archive-events",
"select":[{"field":"EventId"}],"order-by":[{"field":"EventId","direction":"desc"}],"limit":2}}`,
			columns: []sessionarchive.Column{{Name: "EventId", Type: "string"}},
			rows:    `[{"EventId":"z"},{"EventId":"x' OR true --"}]`,
			limited: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, transport := range []string{"HTTP", "MCP"} {
				t.Run(transport, func(t *testing.T) {
					request, err := http.NewRequestWithContext(t.Context(), http.MethodPost,
						server.URL+"/api/v1/session-archive/query", strings.NewReader(test.body))
					if err != nil {
						t.Fatal(err)
					}
					request.Header.Set("Content-Type", "application/json")
					if transport == "MCP" {
						var arguments map[string]json.RawMessage
						if err := json.Unmarshal([]byte(test.body), &arguments); err != nil {
							t.Fatal(err)
						}
						delete(arguments, "kind")
						rpc := mcpLogsRequest(t, "tools/call", map[string]any{"name": "cao_session_query", "arguments": arguments})
						request, err = http.NewRequestWithContext(t.Context(), http.MethodPost, server.URL+"/mcp", rpc.Body)
						if err != nil {
							t.Fatal(err)
						}
						request.Header = rpc.Header.Clone()
					}
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
					decoder := json.NewDecoder(response.Body)
					decoder.UseNumber()
					if transport == "MCP" {
						var message struct {
							Result struct {
								IsError           bool                    `json:"isError"`
								StructuredContent sessionArchiveResult    `json:"structuredContent"`
								Content           []struct{ Text string } `json:"content"`
							}
						}
						if err := decoder.Decode(&message); err != nil {
							t.Fatal(err)
						}
						if message.Result.IsError || len(message.Result.Content) != 1 {
							t.Fatalf("MCP session data lake query failed: %+v", message.Result)
						}
						result = message.Result.StructuredContent
						var text sessionArchiveResult
						textDecoder := json.NewDecoder(strings.NewReader(message.Result.Content[0].Text))
						textDecoder.UseNumber()
						if err := textDecoder.Decode(&text); err != nil || !reflect.DeepEqual(result, text) {
							t.Fatalf("MCP text and structured results differ: %v", err)
						}
					} else if err := decoder.Decode(&result); err != nil {
						t.Fatal(err)
					}
					var want []map[string]any
					decoder = json.NewDecoder(strings.NewReader(test.rows))
					decoder.UseNumber()
					if err := decoder.Decode(&want); err != nil {
						t.Fatal(err)
					}
					var input sessionArchiveQuery
					if err := json.Unmarshal([]byte(test.body), &input); err != nil {
						t.Fatal(err)
					}
					if result.Kind != "session-query" || result.Generation != 1 || result.Rows == nil ||
						!reflect.DeepEqual(*result.Rows, want) || !reflect.DeepEqual(result.Columns, test.columns) ||
						result.Metadata == nil || *result.Metadata != input.Query.Metadata(test.limited, 0, 0) ||
						len(result.Events) != 0 || len(result.Summary) != 0 {
						t.Fatalf("synthetic DuckDB query result mismatch: %+v; expected %+v", result, want)
					}
					encoded, err := json.Marshal(result)
					if err != nil {
						t.Fatal(err)
					}
					for _, content := range []string{"synthetic smoke-test message", "synthetic large-attempt message", "synthetic private tool result", "PayloadJSON"} {
						if strings.Contains(string(encoded), content) {
							t.Fatalf("analytical query leaked full approved content: %s", content)
						}
					}
				})
			}
		})
	}
}
