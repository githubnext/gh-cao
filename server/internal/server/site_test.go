package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/githubnext/gh-cao/server/internal/dashboardassets"
	"github.com/githubnext/gh-cao/server/internal/operational/memory"
	"github.com/githubnext/gh-cao/server/internal/redisx"
)

func hostedTestSite(t *testing.T) string {
	t.Helper()
	site := t.TempDir()
	if err := os.WriteFile(filepath.Join(site, "index.html"), []byte("<html><head></head><body>Hosted dashboard</body></html>"), 0o600); err != nil {
		t.Fatal(err)
	}
	return site
}

func TestDashboardServesHTMLJavaScriptAndCSS(t *testing.T) {
	app, _ := newLocalPostgresTestApp(t, false)
	for name, content := range map[string]string{
		"index.html": `<html><head><link rel="stylesheet" href="/app.css"></head><body><main>Dashboard</main><script type="module" src="/app.js"></script></body></html>`,
		"app.js":     `document.querySelector("main").textContent = "Dashboard loaded";`,
		"app.css":    `main { color: green; }`,
	} {
		if err := os.WriteFile(filepath.Join(app.config.SiteDirectory, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	handler := app.Handler()
	for _, test := range []struct {
		path        string
		contentType string
		content     string
	}{
		{"/", "text/html", "<main>Dashboard</main>"},
		{"/repositories", "text/html", "<main>Dashboard</main>"},
		{"/repositories/", "text/html", "<main>Dashboard</main>"},
		{"/app.js", "javascript", `document.querySelector("main").textContent = "Dashboard loaded";`},
		{"/app.css", "text/css", `main { color: green; }`},
	} {
		t.Run(test.path, func(t *testing.T) {
			request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "http://localhost"+test.path, nil)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusOK ||
				!strings.Contains(response.Header().Get("Content-Type"), test.contentType) ||
				!strings.Contains(response.Body.String(), test.content) {
				t.Fatalf("dashboard response: status=%d content-type=%q body=%s",
					response.Code, response.Header().Get("Content-Type"), response.Body.String())
			}
			if test.contentType == "text/html" &&
				(!strings.Contains(response.Body.String(), `content="server-http"`) ||
					!strings.Contains(response.Body.String(), `content="postgres"`) ||
					response.Header().Get("Cache-Control") != "no-store") {
				t.Fatal("dashboard entry did not select the uncached Go/PostgreSQL bootstrap")
			}
		})
	}
}

func TestHostedStartupRequiresBuiltDashboard(t *testing.T) {
	for _, name := range []string{"missing-index", "empty-index", "directory-index", "escaping-index"} {
		t.Run(name, func(t *testing.T) {
			config := localOAuthConfig(t, "https://github.test")
			index := filepath.Join(config.SiteDirectory, "index.html")
			if err := os.Remove(index); err != nil {
				t.Fatal(err)
			}
			switch name {
			case "empty-index":
				if err := os.WriteFile(index, nil, 0o600); err != nil {
					t.Fatal(err)
				}
			case "directory-index":
				if err := os.Mkdir(index, 0o700); err != nil {
					t.Fatal(err)
				}
			case "escaping-index":
				if err := os.Symlink(filepath.Join(hostedTestSite(t), "index.html"), index); err != nil {
					t.Fatal(err)
				}
			}
			store, err := memory.New(memory.Config{})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			if _, err := New(t.Context(), store, config); err == nil || !strings.Contains(err.Error(), "built dashboard index.html") {
				t.Fatalf("hosted startup accepted an unusable dashboard: %v", err)
			}
		})
	}
}

func TestHostedHandlerServesDashboardAndAssets(t *testing.T) {
	testHostedHandlerServesDashboardAndAssets(t, false)
}

func testHostedHandlerServesDashboardAndAssets(t *testing.T, embedded bool) {
	github := fakeGitHub(t, fakeGitHubOptions{membershipState: "active", accessExpiresIn: 3600})
	for _, name := range []string{"container", "platform", "external"} {
		t.Run(name, func(t *testing.T) {
			profile := hostedHostProfile()
			switch name {
			case "platform":
				profile = azureFunctionsHostProfile()
			case "external":
				profile.Listener = HostListenerExternal
			}
			address, closeRedis := fakeRedis(t)
			t.Cleanup(closeRedis)
			client, err := redisx.New("redis://" + address)
			if err != nil {
				t.Fatal(err)
			}
			site := hostedTestSite(t)
			if embedded {
				site = ""
			}
			config := Config{
				Database: constructorDatabase(), SiteDirectory: site,
				HostProfile: profile,
				Proxy:       ProxyPolicy{AllowedHosts: []string{"dashboard.example.com"}, RequireHTTPS: true},
				GitHubOAuth: validOAuthConfig(github.URL),
			}
			if profile.Listener == HostListenerProcess {
				config.Listen = "127.0.0.1:8443"
				config.CertFile, config.KeyFile = "certificate.pem", "key.pem"
			}
			app, err := New(t.Context(), redisx.NewStore(client, "hosted-site"), config)
			if err != nil {
				t.Fatal(err)
			}
			if profile.Listener == HostListenerExternal {
				app.startContext = t.Context()
			}
			assetPath, assetContent := "/app.js", []byte("export default 'hosted';")
			if embedded {
				assetPath = "/" + dashboardassets.MCPContract
				assetContent, err = dashboardassets.ReadFile(dashboardassets.Path("", dashboardassets.MCPContract))
				if err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.WriteFile(filepath.Join(app.config.SiteDirectory, "app.js"), assetContent, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			handler := app.Handler()
			for _, path := range []string{"/", "/index.html", "/repositories", "/repositories/", assetPath} {
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, azureRequest(t, http.MethodGet, path))
				if response.Code != http.StatusFound || response.Header().Get("Location") != "/auth/login" {
					t.Fatalf("unauthenticated %s bypassed hosted login: %d", path, response.Code)
				}
			}
			session, _ := callbackSession(t, app)
			for _, path := range []string{"/", "/index.html", "/repositories", "/repositories/", assetPath, "/api/auth/session"} {
				request := azureRequest(t, http.MethodGet, path)
				request.AddCookie(session)
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, request)
				if response.Code != http.StatusOK || response.Header().Get("Location") != "" {
					t.Fatalf("hosted %s was not served by Go: %d", path, response.Code)
				}
				body := response.Body.String()
				switch path {
				case assetPath:
					if body != string(assetContent) {
						t.Fatal("hosted asset was not served from the configured site")
					}
				case "/api/auth/session":
					if !strings.Contains(response.Header().Get("Content-Type"), "application/json") {
						t.Fatal("hosted API returned the dashboard instead of JSON")
					}
				default:
					if !embedded && !strings.Contains(body, "Hosted dashboard") {
						t.Fatal("hosted HTML was not served from the configured site")
					}
					for _, marker := range []string{
						`<meta name="dashboard-data-backend" content="server-http">`,
						`<meta name="dashboard-storage-backend" content="postgres">`,
						`<meta name="cao-auth-mode" content="github">`,
					} {
						if !strings.Contains(body, marker) {
							t.Fatalf("hosted %s did not select the Go backend: missing %s", path, marker)
						}
					}
					if response.Header().Get("Cache-Control") != "no-store" {
						t.Fatal("hosted dashboard bootstrap must not be statically cached")
					}
				}
			}
		})
	}
}
