package server

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/githubnext/gh-cao/server/internal/marketplace"
	"github.com/githubnext/gh-cao/server/internal/model"
	"github.com/githubnext/gh-cao/server/internal/operational/postgres"
	"github.com/githubnext/gh-cao/server/internal/query"
	"github.com/githubnext/gh-cao/server/internal/redisx"
	"github.com/githubnext/gh-cao/server/internal/testutil"
)

func TestLocalMarketplaceUsesNativeResolverWithoutServerCredentials(t *testing.T) {
	t.Setenv("GH_TOKEN", "")
	prefix := "/" + strings.Repeat("a", 48)
	for _, status := range []int{http.StatusOK, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			adapter := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != prefix+"/marketplace" ||
					r.Header.Get("Authorization") != "" {
					t.Errorf("unexpected marketplace request: %s %s", r.Method, r.URL.Path)
				}
				w.WriteHeader(status)
				if status == http.StatusOK {
					_ = json.NewEncoder(w).Encode(marketplace.Result{
						Packages:    []marketplace.Package{{ID: "example/catalog/demo", Name: "Demo"}},
						Diagnostics: []marketplace.RegistryDiagnostic{{RegistryID: "official", Status: "available", Packages: 1}},
					})
				}
			}))
			defer adapter.Close()
			app := &App{config: Config{OperatorActions: &LocalOperatorConfig{
				AdapterURL: adapter.URL + prefix, Marketplace: true,
			}}}
			source := app.localMarketplaceSource(t.Context())
			if status == http.StatusOK {
				if len(source.Rows) != 1 || source.Rows[0]["package-name"] != "Demo" ||
					source.Metadata["availability"] != "available" || source.Metadata["completeness"] != "complete" {
					t.Fatalf("native catalog unavailable: %+v", source)
				}
			} else if len(source.Rows) != 0 || source.Metadata["availability"] != "unavailable" {
				t.Fatalf("failed resolver did not fail closed: %+v", source)
			}
		})
	}
}

func TestLocalMarketplaceRejectsAdapterRedirects(t *testing.T) {
	prefix := "/" + strings.Repeat("b", 48)
	var followed bool
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		followed = true
		_ = json.NewEncoder(w).Encode(marketplace.Result{
			Packages:    []marketplace.Package{{ID: "example/catalog/redirected", Name: "Redirected"}},
			Diagnostics: []marketplace.RegistryDiagnostic{{RegistryID: "official", Status: "available", Packages: 1}},
		})
	}))
	defer other.Close()
	adapter := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+"/marketplace", http.StatusTemporaryRedirect)
	}))
	defer adapter.Close()
	app := &App{config: Config{OperatorActions: &LocalOperatorConfig{
		AdapterURL: adapter.URL + prefix, Marketplace: true,
	}}}
	source := app.localMarketplaceSource(t.Context())
	if followed || len(source.Rows) != 0 || source.Metadata["availability"] != "unavailable" {
		t.Fatalf("adapter redirect was followed or accepted: followed=%t source=%+v", followed, source)
	}
}

func TestMarketplaceResultSourceReportsRegistryCompleteness(t *testing.T) {
	for _, test := range []struct {
		name         string
		packages     []marketplace.Package
		diagnostics  []marketplace.RegistryDiagnostic
		availability string
		completeness string
		freshness    string
	}{
		{
			name: "complete", packages: []marketplace.Package{{ID: "demo"}},
			diagnostics:  []marketplace.RegistryDiagnostic{{RegistryID: "official", Status: "available"}},
			availability: "available", completeness: "complete", freshness: "fresh",
		},
		{
			name: "partial", packages: []marketplace.Package{{ID: "demo"}},
			diagnostics: []marketplace.RegistryDiagnostic{
				{RegistryID: "official", Status: "available"}, {RegistryID: "private", Status: "unavailable"},
			},
			availability: "available", completeness: "partial", freshness: "fresh",
		},
		{
			name:         "unavailable",
			diagnostics:  []marketplace.RegistryDiagnostic{{RegistryID: "private", Status: "unavailable"}},
			availability: "unavailable", completeness: "unknown", freshness: "unknown",
		},
		{
			name:         "empty",
			diagnostics:  []marketplace.RegistryDiagnostic{{RegistryID: "official", Status: "available"}},
			availability: "empty", completeness: "complete", freshness: "fresh",
		},
		{
			name: "no registries", availability: "empty", completeness: "complete", freshness: "fresh",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := marketplaceResultSource(t.Context(), &marketplace.Result{
				Packages: test.packages, Diagnostics: test.diagnostics,
			})
			if source.Metadata["availability"] != test.availability ||
				source.Metadata["completeness"] != test.completeness ||
				source.Metadata["freshness"] != test.freshness {
				t.Fatalf("incorrect registry evidence state: %+v", source.Metadata)
			}
		})
	}
}

// newFakeMarketplaceGitHubServer serves the minimal GitHub REST surface
// ResolveRegistry needs (commit/tree/blob lookups) over HTTPS with a
// self-signed certificate, mirroring the fake used in the marketplace
// package's own tests.
func newFakeMarketplaceGitHubServer(t *testing.T, manifest string) *httptest.Server {
	t.Helper()
	const commitSHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "/commits/"):
			_ = json.NewEncoder(w).Encode(map[string]any{"sha": commitSHA})
		case strings.Contains(r.URL.Path, "/git/trees/"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"tree": []map[string]any{{"type": "blob", "path": "demo/aw.yml", "sha": "blob-sha"}},
			})
		case strings.Contains(r.URL.Path, "/git/blobs/"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"encoding": "base64",
				"content":  base64.StdEncoding.EncodeToString([]byte(manifest)),
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

// withInsecureDefaultTransport temporarily trusts self-signed certificates on
// the process-wide default HTTP transport, which is what marketplace.Options
// falls back to when the server wires up a zero-value Options. It is
// restored automatically at the end of the test.
func withInsecureDefaultTransport(t *testing.T) {
	t.Helper()
	previous := http.DefaultTransport
	http.DefaultTransport = &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}} // #nosec G402 -- test-only, restored via t.Cleanup
	t.Cleanup(func() { http.DefaultTransport = previous })
}

func TestQueryTransparentlyInjectsMarketplacePackagesWithoutSnapshotOrSecretLeakage(t *testing.T) {
	database, connection := testutil.PostgresWithConfig(t, t.Context(), "POSTGRES_URL", "CAO_TEST_POSTGRES_URL")
	services, err := postgres.New(t.Context(), testutil.ConnectionString(t, connection), "marketplace-test", postgres.Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = services.Close() })
	withInsecureDefaultTransport(t)
	githubServer := newFakeMarketplaceGitHubServer(t, "name: Demo\ndescription: Example\nincludes:\n  - demo.md\n")

	policyPath := filepath.Join(t.TempDir(), "cao.json")
	policy := `{
		"version": 1,
		"control-plane": {
			"marketplace": {
				"cache-ttl-seconds": 60,
				"registries": [
					{"id": "official", "repository": "example/packages", "ref": "main", "api-url": "` + githubServer.URL + `/api/v3"}
				]
			}
		}
	}`
	if err := os.WriteFile(policyPath, []byte(policy), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(marketplacePolicyPathEnv, policyPath)

	site, err := os.MkdirTemp(".", ".test-site-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(site); err != nil {
			t.Errorf("remove test site: %v", err)
		}
	})
	if err := os.WriteFile(site+"/index.html", []byte("<html><head></head></html>"), 0o600); err != nil {
		t.Fatal(err)
	}
	app, err := New(context.Background(), services, Config{
		Database: database, DatabaseQueriesPath: "../../../dashboard/site/src/data/queries/database.json",
		Listen: "127.0.0.1:8443", SiteDirectory: site, AccessToken: testAccessToken,
	})
	if err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequestWithContext(
		t.Context(), http.MethodPost, "https://localhost/api/v1/query",
		strings.NewReader(`{"sourceNames":["marketplace-packages"]}`),
	)
	request.Header.Set("Content-Type", "application/json")
	authorize(request)
	response := httptest.NewRecorder()
	app.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("query returned %d: %s", response.Code, response.Body.String())
	}

	var result struct {
		Sources map[string]model.Source `json:"sources"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	source, ok := result.Sources["marketplace-packages"]
	if !ok {
		t.Fatalf("expected the marketplace-packages source to be injected, got: %#v", result.Sources)
	}
	if len(source.Rows) != 1 ||
		source.Rows[0]["package-name"] != "Demo" ||
		source.Rows[0]["package-source"] != "example/packages/demo@aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" {
		t.Fatalf("expected the resolved demo package row, got: %#v", source.Rows)
	}
	if source.Metadata["availability"] != "available" {
		t.Fatalf("unexpected availability: %v", source.Metadata["availability"])
	}
	if want := (map[string]any{
		"relation": "repository",
		"href":     githubServer.URL + "/example/packages",
		"label":    "Open example/packages on GitHub",
	}); !reflect.DeepEqual(source.Rows[0]["repository-link"], want) {
		t.Fatalf("native marketplace query lost repository link: %#v", source.Rows[0]["repository-link"])
	}
	cached, err := app.services.Cache.CachedMarketplaceRegistry(t.Context(), "official", "marketplace-before-first-snapshot")
	if err != nil || len(cached) == 0 {
		t.Fatalf("marketplace without a snapshot was not cached: %v", err)
	}
	githubServer.Close()
	page, status, err := app.executeQuery(t.Context(), queryRequest{
		SourceNames: []string{"catalog"},
		Queries: []query.Definition{{
			Name: "catalog", From: "marketplace-packages",
			OrderBy: []query.OrderField{{Field: "package-name", Direction: "asc"}},
		}},
		Pagination: map[string]paginationRequest{"catalog": {Limit: 1}},
	}, false)
	if err != nil || status != http.StatusOK || len(page.Sources["catalog"].Rows) != 1 {
		t.Fatalf("derived marketplace pagination without a snapshot: status=%d err=%v", status, err)
	}
	for _, body := range []string{
		`{"sourceNames":["runs"]}`,
		`{"sourceNames":["marketplace-packages","runs"]}`,
		`{"sourceNames":["marketplace-packages"],"pagination":{"workflow-subject-page":{"limit":1}}}`,
		`{"sourceNames":["joined"],"queries":[{"name":"joined","from":"marketplace-packages","joins":[{"source":"runs","on":[{"left":"repository","right":"repository"}]}]}]}`,
		`{"sourceNames":["combined"],"queries":[{"name":"combined","union":["marketplace-packages","runs"]}]}`,
	} {
		request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "https://localhost/api/v1/query", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		authorize(request)
		response := httptest.NewRecorder()
		app.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), "dashboard data is unavailable") {
			t.Fatalf("canonical query without a snapshot returned %d: %s", response.Code, response.Body.String())
		}
	}
	readiness := httptest.NewRecorder()
	app.Handler().ServeHTTP(readiness, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "https://localhost/api/readiness", nil))
	if readiness.Code != http.StatusServiceUnavailable {
		t.Fatalf("marketplace access changed canonical readiness: %d", readiness.Code)
	}
	t.Setenv(marketplacePolicyPathEnv, filepath.Join(t.TempDir(), "missing-policy.json"))
	prefix := "/" + strings.Repeat("b", 48)
	adapter := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != prefix+"/marketplace" {
			t.Errorf("unexpected adapter path: %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(marketplace.Result{
			Packages: []marketplace.Package{
				{ID: "local/catalog/demo", Name: "Local catalog"},
				{ID: "local/catalog/second", Name: "Second catalog"},
			},
			Diagnostics: []marketplace.RegistryDiagnostic{{RegistryID: "local", Status: "available", Packages: 2}},
		})
	}))
	defer adapter.Close()
	app.config.OperatorActions = &LocalOperatorConfig{AdapterURL: adapter.URL + prefix, Marketplace: true}
	localQuery := queryRequest{
		SourceNames: []string{"catalog"},
		Queries: []query.Definition{{
			Name: "catalog", From: "marketplace-packages",
			OrderBy: []query.OrderField{{Field: "package-name", Direction: "asc"}},
		}},
		Pagination: map[string]paginationRequest{"catalog": {Limit: 1}},
	}
	page, status, err = app.executeQuery(t.Context(), localQuery, false)
	if err != nil || status != http.StatusOK || len(page.Sources["catalog"].Rows) != 1 ||
		page.Sources["catalog"].Rows[0]["package-name"] != "Local catalog" ||
		page.Sources["catalog"].Metadata["completeness"] != "complete" ||
		page.Sources["catalog"].ContinuationToken == "" {
		t.Fatalf("local catalog did not cross the production query boundary: status=%d err=%v sources=%+v", status, err, page.Sources)
	}
	localQuery.Pagination["catalog"] = paginationRequest{Limit: 1, ContinuationToken: page.Sources["catalog"].ContinuationToken}
	page, status, err = app.executeQuery(t.Context(), localQuery, false)
	if err != nil || status != http.StatusOK || len(page.Sources["catalog"].Rows) != 1 ||
		page.Sources["catalog"].Rows[0]["package-name"] != "Second catalog" ||
		page.Sources["catalog"].Metadata["completeness"] != "complete" {
		t.Fatalf("local catalog continuation lost complete evidence: status=%d err=%v sources=%+v", status, err, page.Sources)
	}
}

func TestQueryDegradesMarketplacePackagesToUnavailableWhenThePolicyFileIsMissing(t *testing.T) {
	database := integrationDatabase(t)
	seedDatabase(t, database, nil)
	t.Setenv(marketplacePolicyPathEnv, filepath.Join(t.TempDir(), "does-not-exist.json"))

	address, closeServer := fakeRedis(t)
	defer closeServer()
	client, err := redisx.New("redis://" + address)
	if err != nil {
		t.Fatal(err)
	}
	site, err := os.MkdirTemp(".", ".test-site-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(site); err != nil {
			t.Errorf("remove test site: %v", err)
		}
	})
	if err := os.WriteFile(site+"/index.html", []byte("<html><head></head></html>"), 0o600); err != nil {
		t.Fatal(err)
	}
	app, err := New(context.Background(), redisx.NewStore(client, "test"), Config{
		Database: database, DatabaseQueriesPath: "../../../dashboard/site/src/data/queries/database.json",
		Listen: "127.0.0.1:8443", SiteDirectory: site, AccessToken: testAccessToken,
	})
	if err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequestWithContext(
		t.Context(), http.MethodPost, "https://localhost/api/v1/query",
		strings.NewReader(`{"sourceNames":["marketplace-packages"]}`),
	)
	request.Header.Set("Content-Type", "application/json")
	authorize(request)
	response := httptest.NewRecorder()
	app.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("query returned %d: %s", response.Code, response.Body.String())
	}

	var result struct {
		Sources map[string]model.Source `json:"sources"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	source, ok := result.Sources["marketplace-packages"]
	if !ok {
		t.Fatalf("expected the marketplace-packages source to still be present, got: %#v", result.Sources)
	}
	if len(source.Rows) != 0 || source.Metadata["availability"] != "unavailable" {
		t.Fatalf("expected a graceful unavailable source, got: %#v", source)
	}
}

func TestMarketplacePolicyPathDefaultsAndHonorsEnvOverride(t *testing.T) {
	t.Setenv(marketplacePolicyPathEnv, "")
	if got := marketplacePolicyPath(); got != defaultMarketplacePolicyPath {
		t.Fatalf("expected the default policy path, got: %q", got)
	}
	t.Setenv(marketplacePolicyPathEnv, "custom/cao.json")
	if got := marketplacePolicyPath(); got != "custom/cao.json" {
		t.Fatalf("expected the overridden policy path, got: %q", got)
	}
}

func TestLocalSQLiteMarketplaceResolvesRegistryWithoutSnapshotPackages(t *testing.T) {
	withInsecureDefaultTransport(t)
	githubServer := newFakeMarketplaceGitHubServer(t, "name: Demo\ndescription: Example\nincludes:\n  - demo.md\n")
	policyPath := filepath.Join(t.TempDir(), "cao.json")
	policy := `{"version":1,"control-plane":{"marketplace":{"registries":[
		{"id":"official","repository":"example/packages","ref":"main","api-url":"` + githubServer.URL + `/api/v3"}
	]}}}`
	if err := os.WriteFile(policyPath, []byte(policy), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(marketplacePolicyPathEnv, policyPath)
	app, _ := newLocalPostgresTestApp(t, false)
	request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "http://localhost/api/v1/query",
		strings.NewReader(`{"sourceNames":["marketplace-directory"],"pagination":{"marketplace-directory":{"limit":25}}}`))
	request.Header.Set("Content-Type", "application/json")
	authorize(request)
	response := httptest.NewRecorder()
	app.Handler().ServeHTTP(response, request)
	var result queryResponse
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || response.Code != http.StatusOK {
		t.Fatalf("local marketplace query: status=%d body=%s err=%v", response.Code, response.Body.String(), err)
	}
	source := result.Sources["marketplace-directory"]
	if len(source.Rows) != 1 || source.Rows[0]["package-name"] != "Demo" ||
		source.Rows[0]["package-source"] != "example/packages/demo@aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" ||
		source.Metadata["availability"] != "available" {
		t.Fatalf("local marketplace did not resolve configured registry: %+v", source)
	}
}
