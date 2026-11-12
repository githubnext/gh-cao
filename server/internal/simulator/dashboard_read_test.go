package simulator

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/model"
	"github.com/githubnext/gh-cao/server/internal/operational"
	"github.com/githubnext/gh-cao/server/internal/operational/postgres"
	"github.com/githubnext/gh-cao/server/internal/postgresx"
	"github.com/githubnext/gh-cao/server/internal/server"
	"github.com/githubnext/gh-cao/server/internal/testutil"
)

const dashboardScenarioToken = "synthetic-dashboard-contract-token-only"

type dashboardReadFixture struct {
	database *postgresx.Store
	writer   *sql.DB
	endpoint string
	services operational.Store
	config   server.Config
}

func newDashboardReadFixture(t *testing.T) dashboardReadFixture {
	t.Helper()
	t.Log("SYNTHETIC canonical read scenario: no production credentials or remote durability evidence")
	database, connection := testutil.PostgresWithConfig(t, t.Context(), "POSTGRES_URL")
	endpoint := testutil.ConnectionString(t, connection)
	services, err := postgres.New(t.Context(), endpoint, "synthetic-contract", postgres.Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = services.Close() })
	writer, err := sql.Open("pgx", endpoint)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = writer.Close() })
	ingestion, err := database.BeginIngestion(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer ingestion.Abort(t.Context())
	for source, rows := range map[string][]model.Row{
		"$repositories": {
			{"id": "repository:synthetic/alpha", "owner": "synthetic", "name": "alpha"},
			{"id": "repository:synthetic/beta", "owner": "synthetic", "name": "beta"},
		},
		"$workflows": {{
			"id": "workflow:synthetic/pilot", "repositoryId": "repository:synthetic/alpha",
			"path": ".github/workflows/pilot.md", "name": "Synthetic contract pilot",
		}},
		"$runs": {{
			"id": "run:synthetic/sparse", "repositoryId": "repository:synthetic/alpha",
			"workflowId": "workflow:synthetic/pilot", "owner": "synthetic", "repository": "alpha",
			"githubRunId": "1", "attempt": 1, "status": "admitted", "admissionStatus": "admitted",
			"observedAt": "2026-10-01T12:00:00Z",
		}},
	} {
		for _, row := range rows {
			if err := ingestion.Append(t.Context(), source, row); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := ingestion.Publish(t.Context(), "synthetic"); err != nil {
		t.Fatal(err)
	}
	site := t.TempDir()
	if err := os.WriteFile(filepath.Join(site, "index.html"),
		[]byte("<html><head></head><body>SYNTHETIC canonical contract scenario (not the dashboard UI)</body></html>"), 0o600); err != nil {
		t.Fatal(err)
	}
	source, err := filepath.Abs("../../../dashboard/site/src")
	if err != nil {
		t.Fatal(err)
	}
	definitions, err := server.ParseDashboardQueries(filepath.Join(source, "agent/queries.generated.json"))
	if err != nil {
		t.Fatal(err)
	}
	return dashboardReadFixture{
		database: database, writer: writer, endpoint: endpoint, services: services,
		config: server.Config{
			Database: database,
			Listen:   "127.0.0.1:0", SiteDirectory: site, AccessToken: dashboardScenarioToken,
			DatabaseQueriesPath:  filepath.Join(source, "data/queries/database.json"),
			DashboardQueries:     definitions,
			DashboardQueriesPath: filepath.Join(source, "agent/queries.generated.json"),
			AgentCatalogPath:     filepath.Join(source, "agent/catalog.generated.json"),
			MCPContractPath:      filepath.Join(source, "agent/mcp-contract.json"),
			Logger:               log.New(io.Discard, "", 0),
		},
	}
}

func TestDashboardReadSyntheticScenario(t *testing.T) {
	fixture := newDashboardReadFixture(t)
	app, err := server.New(t.Context(), fixture.services, fixture.config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 5*time.Second)
		defer cancel()
		if err := app.Stop(ctx); err != nil {
			t.Error(err)
		}
	})
	if err := app.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	host := httptest.NewServer(app.Handler())
	t.Cleanup(host.Close)
	runDashboardReadScenario(t, fixture, host.URL)
}

type dashboardScenarioResult struct {
	Revision int64                   `json:"revision"`
	Sources  map[string]model.Source `json:"sources"`
}

type dashboardScenarioClient struct {
	client   *http.Client
	endpoint string
}

func newDashboardScenarioClient(t *testing.T, endpoint string) dashboardScenarioClient {
	t.Helper()
	transport, err := NewLocalTransport(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(transport.CloseIdleConnections)
	return dashboardScenarioClient{client: &http.Client{Transport: transport, Timeout: 5 * time.Second}, endpoint: endpoint}
}

func (c dashboardScenarioClient) request(t *testing.T, method, path, body string, status int) []byte {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), method, c.endpoint+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+dashboardScenarioToken)
	request.Header.Set("Content-Type", "application/json")
	response, err := c.client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	content, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil || response.StatusCode != status {
		t.Fatalf("%s %s: status=%d want=%d body=%s error=%v", method, path, response.StatusCode, status, content, err)
	}
	return content
}

func (c dashboardScenarioClient) query(t *testing.T, body string) dashboardScenarioResult {
	t.Helper()
	var result dashboardScenarioResult
	if err := json.Unmarshal(c.request(t, http.MethodPost, "/api/v1/query", body, http.StatusOK), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func runDashboardReadScenario(t *testing.T, fixture dashboardReadFixture, endpoint string) {
	t.Helper()
	client := newDashboardScenarioClient(t, endpoint)
	t.Run("synthetic sparse evidence stays unknown", func(t *testing.T) {
		result := client.query(t, `{"sourceNames":["pilot"],"queries":[{
			"name":"pilot","from":"$runs","filter":{"predicates":[{"field":"id","equals":"run:synthetic/sparse"}]},
			"select":[{"field":"id"},{"field":"status"},{"field":"conclusion"},{"field":"cost"}]}]}`)
		source := result.Sources["pilot"]
		if len(source.Rows) != 1 || source.Rows[0]["status"] != "admitted" ||
			source.Rows[0]["conclusion"] != nil || source.Rows[0]["cost"] != nil ||
			source.Metadata["completeness"] != "partial" {
			t.Fatalf("partial evidence became a known outcome, zero cost or complete source: %+v", source)
		}
		repositories := client.request(t, http.MethodGet, "/api/repositories", "", http.StatusOK)
		if !strings.Contains(string(repositories), "repository:synthetic/alpha") {
			t.Fatalf("canonical API omitted the synthetic fixture: %s", repositories)
		}
	})
	t.Run("synthetic unknown source fails closed", func(t *testing.T) {
		client.request(t, http.MethodPost, "/api/v1/query", `{"sourceNames":["$unknown"]}`, http.StatusBadRequest)
	})
	t.Run("synthetic stale revision is rejected", func(t *testing.T) {
		first := client.query(t, `{"sourceNames":["repositories"],"pagination":{"repositories":{"limit":1}}}`)
		page := first.Sources["repositories"]
		if len(page.Rows) != 1 || page.ContinuationToken == "" {
			t.Fatalf("expected a bounded first page: %+v", page)
		}
		next, err := json.Marshal(map[string]any{
			"sourceNames": []string{"repositories"},
			"pagination":  map[string]any{"repositories": map[string]any{"limit": 1, "continuationToken": page.ContinuationToken}},
		})
		if err != nil {
			t.Fatal(err)
		}
		client.query(t, string(next))
		dashboardScenarioWrite(t, fixture, `UPDATE repositories SET name='updated' WHERE id='repository:synthetic/alpha';
			UPDATE cao_state SET revision=revision+1`)
		updated := client.query(t, `{"sourceNames":["repositories"]}`)
		if updated.Revision == first.Revision || len(updated.Sources["repositories"].Rows) != 2 {
			t.Fatalf("external commit did not produce fresh canonical state: %+v", updated)
		}
		changed := client.query(t, `{"sourceNames":["changed"],"queries":[{
			"name":"changed","from":"$repositories","filter":{"predicates":[{"field":"id","equals":"repository:synthetic/alpha"}]},
			"select":[{"field":"name"}]}]}`)
		if rows := changed.Sources["changed"].Rows; len(rows) != 1 || rows[0]["name"] != "updated" {
			t.Fatalf("query payload did not reflect the committed revision: %+v", changed)
		}
		content := client.request(t, http.MethodPost, "/api/v1/query", string(next), http.StatusBadRequest)
		if !strings.Contains(string(content), "stale continuation token") {
			t.Fatalf("expected stale-revision rejection: %s", content)
		}
	})
	t.Run("synthetic dependency failure stays unavailable", func(t *testing.T) {
		dashboardScenarioWrite(t, fixture, `ALTER TABLE repositories RENAME TO unavailable_repositories`)
		client.request(t, http.MethodPost, "/api/v1/query", `{"sourceNames":["repositories"]}`, http.StatusServiceUnavailable)
		client.request(t, http.MethodGet, "/api/repositories", "", http.StatusServiceUnavailable)
		dashboardScenarioWrite(t, fixture, `ALTER TABLE unavailable_repositories RENAME TO repositories`)
		client.query(t, `{"sourceNames":["repositories"]}`)
	})
	t.Run("synthetic active transport cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"/api/v1/events", nil)
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Authorization", "Bearer "+dashboardScenarioToken)
		response, err := client.client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = response.Body.Close() }()
		if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != "text/event-stream" {
			t.Fatalf("revision stream did not open: %d", response.StatusCode)
		}
		stream := bufio.NewReader(response.Body)
		line, err := stream.ReadString('\n')
		if err != nil || !strings.HasPrefix(line, "data: ") {
			t.Fatalf("no initial canonical revision event: %q %v", line, err)
		}
		if _, err := stream.ReadString('\n'); err != nil {
			t.Fatal(err)
		}
		cancel()
		if _, err := io.ReadAll(stream); !errors.Is(err, context.Canceled) {
			t.Fatalf("active HTTP request did not report cancellation: %v", err)
		}
		client.query(t, `{"sourceNames":["repositories"]}`)
	})
}

func dashboardScenarioWrite(t *testing.T, fixture dashboardReadFixture, statement string) {
	t.Helper()
	if _, err := fixture.writer.ExecContext(t.Context(), statement); err != nil {
		t.Fatal(err)
	}
}
