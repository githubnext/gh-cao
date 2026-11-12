package server

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/githubnext/gh-cao/server/internal/model"
	"github.com/githubnext/gh-cao/server/internal/operational"
	"github.com/githubnext/gh-cao/server/internal/operational/postgres"
	"github.com/githubnext/gh-cao/server/internal/postgresx"
	"github.com/githubnext/gh-cao/server/internal/query"
	"github.com/githubnext/gh-cao/server/internal/testutil"
)

type localPostgresFixture struct {
	database *postgresx.Store
	services operational.Store
}

func (f *localPostgresFixture) app(t *testing.T, config Config) *App {
	t.Helper()
	config.Database = f.database
	app, err := New(t.Context(), f.services, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := app.Stop(context.WithoutCancel(t.Context())); err != nil {
			t.Error(err)
		}
	})
	return app
}

func newLocalPostgresTestApp(t *testing.T, mcpEnabled bool) (*App, *localPostgresFixture) {
	t.Helper()
	database, connection := testutil.PostgresWithConfig(t, t.Context(), "POSTGRES_URL", "CAO_TEST_POSTGRES_URL")
	services, err := postgres.New(t.Context(), testutil.ConnectionString(t, connection), "local-test", postgres.Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = services.Close() })
	fixture := &localPostgresFixture{database: database, services: services}
	fixture.publish(t, "initial", "one")
	site := t.TempDir()
	if err := os.WriteFile(filepath.Join(site, "index.html"), []byte("<html><head></head><body>Dashboard</body></html>"), 0o600); err != nil {
		t.Fatal(err)
	}
	definitions, err := ParseDashboardQueries("../../../dashboard/site/src/agent/queries.generated.json")
	if err != nil {
		t.Fatal(err)
	}
	app := fixture.app(t, Config{
		Listen: "127.0.0.1:8080", SiteDirectory: site, AccessToken: testAccessToken,
		DatabaseQueriesPath: "../../../dashboard/site/src/data/queries/database.json",
		DashboardQueries:    definitions, AgentCatalogPath: testAgentCatalog, MCPContractPath: testMCPContract,
		MCPEnabled: mcpEnabled, Logger: log.New(io.Discard, "", 0),
	})
	if err := app.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	return app, fixture
}

func (f *localPostgresFixture) publish(t *testing.T, revision, name string) {
	t.Helper()
	writer, err := f.database.BeginIngestion(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Abort(t.Context())
	now := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)
	for source, rows := range map[string][]model.Row{
		"$repositories": {
			{"id": "repository:octo/one", "owner": "octo", "name": name},
			{"id": "repository:octo/two", "owner": "octo", "name": "two"},
		},
		"$workflows": {{"id": "workflow:one", "repositoryId": "repository:octo/one", "path": "one.md", "name": "One"}},
		"$runs": {{
			"id": "run:one", "repositoryId": "repository:octo/one", "workflowId": "workflow:one",
			"owner": "octo", "repository": "one", "startedAt": now, "completedAt": now, "duration": "10s",
			"conclusion": "success", "status": "completed", "githubRunId": "1", "attempt": 1,
		}},
	} {
		for _, row := range rows {
			if err := writer.Append(t.Context(), source, row); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := writer.Publish(t.Context(), revision); err != nil {
		t.Fatal(err)
	}
}

func TestLocalPostgresExternalPublication(t *testing.T) {
	app, fixture := newLocalPostgresTestApp(t, false)
	queryPage := func(body string) (int, queryResponse) {
		t.Helper()
		request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "http://localhost/api/v1/query", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		authorize(request)
		response := httptest.NewRecorder()
		app.Handler().ServeHTTP(response, request)
		var result queryResponse
		if response.Code == http.StatusOK {
			if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
		}
		return response.Code, result
	}
	body := `{"sourceNames":["repositories"],"pagination":{"repositories":{"limit":1}}}`
	status, first := queryPage(body)
	source := first.Sources["repositories"]
	if status != http.StatusOK || len(source.Rows) != 1 || source.ContinuationToken == "" {
		t.Fatalf("local PostgreSQL query failed: %d %+v", status, first)
	}
	events := app.hub.Subscribe(t.Context(), app, false)
	defer app.hub.Unsubscribe(events)
	fixture.publish(t, "updated", "updated")
	status, updated := queryPage(body)
	if status != http.StatusOK || updated.Revision == first.Revision {
		t.Fatalf("external publication was not visible: %d %+v", status, updated)
	}
	stale, err := json.Marshal(queryRequest{SourceNames: []string{"repositories"}, Pagination: map[string]paginationRequest{
		"repositories": {Limit: 1, ContinuationToken: source.ContinuationToken},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if status, _ := queryPage(string(stale)); status == http.StatusOK {
		t.Fatal("stale continuation token was accepted")
	}
	deadline := time.After(5 * time.Second)
	for {
		select {
		case event := <-events:
			if event.revision == updated.Revision {
				return
			}
		case <-deadline:
			t.Fatal("external PostgreSQL publication did not notify subscribers")
		}
	}
}

func TestLocalPostgresMCP(t *testing.T) {
	app, _ := newLocalPostgresTestApp(t, true)
	httpServer := httptest.NewServer(app.Handler())
	defer httpServer.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "local-postgres-test", Version: "1"}, nil)
	session, err := client.Connect(t.Context(), &mcp.StreamableClientTransport{
		Endpoint:   httpServer.URL + "/mcp",
		HTTPClient: &http.Client{Transport: bearerTransport{token: testAccessToken, base: http.DefaultTransport}},
	}, &mcp.ClientSessionOptions{ProtocolVersion: "2026-07-28"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()
	result, err := session.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "cao_query", Arguments: map[string]any{"id": "campaign-runs", "limit": 10},
	})
	if err != nil || result.IsError {
		t.Fatalf("PostgreSQL MCP query: %+v %v", result, err)
	}
}

func TestLocalPostgresRejectsInvalidQuery(t *testing.T) {
	app, _ := newLocalPostgresTestApp(t, false)
	_, _, err := app.executeQuery(t.Context(), queryRequest{
		SourceNames: []string{"invalid"},
		Queries:     []query.Definition{{Name: "invalid", From: "$missing"}},
	}, false)
	if err == nil {
		t.Fatal("unknown source was silently replaced with empty rows")
	}
}
