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

	"github.com/githubnext/gh-cao/server/internal/model"
	"github.com/githubnext/gh-cao/server/internal/redisx"
)

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

func TestQueryTransparentlyInjectsMarketplacePackagesWithoutSecretLeakage(t *testing.T) {
	database := integrationDatabase(t)
	seedDatabase(t, database, nil)
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
