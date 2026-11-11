package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStaticFilesStayInsideSiteRoot(t *testing.T) {
	site := t.TempDir()
	outside := t.TempDir()
	for name, content := range map[string]string{
		filepath.Join(site, "index.html"):   "<head></head><body>dashboard</body>",
		filepath.Join(site, "app.js"):       "export default 1;",
		filepath.Join(site, "app.css"):      "body { color: red; }",
		filepath.Join(outside, "secret.js"): "private-file-content",
	} {
		if err := os.WriteFile(name, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for name, target := range map[string]string{
		"escaped.js": filepath.Join(outside, "secret.js"),
		"outside":    outside,
		"alias.js":   "app.js",
	} {
		if err := os.Symlink(target, filepath.Join(site, name)); err != nil {
			t.Fatal(err)
		}
	}
	app := &App{config: Config{SiteDirectory: site}}
	for _, test := range []struct {
		path   string
		status int
		body   string
	}{
		{"/", http.StatusOK, "dashboard"},
		{"/index.html", http.StatusOK, "dashboard"},
		{"/missing-route", http.StatusOK, "dashboard"},
		{"/app.js", http.StatusOK, "export default 1;"},
		{"/app.css", http.StatusOK, "body { color: red; }"},
		{"/alias.js", http.StatusOK, "export default 1;"},
		{"/../secret.js", http.StatusNotFound, ""},
		{"/%2e%2e/secret.js", http.StatusNotFound, ""},
		{"//" + strings.TrimPrefix(filepath.Join(outside, "secret.js"), "/"), http.StatusNotFound, ""},
		{"/escaped.js", http.StatusNotFound, ""},
		{"/outside/secret.js", http.StatusNotFound, ""},
		{"/api/missing", http.StatusNotFound, ""},
		{"/mcp", http.StatusNotFound, ""},
	} {
		t.Run(test.path, func(t *testing.T) {
			response := httptest.NewRecorder()
			app.static(response, httptest.NewRequestWithContext(t.Context(), http.MethodGet, test.path, nil))
			if response.Code != test.status {
				t.Fatalf("status = %d, want %d", response.Code, test.status)
			}
			if strings.Contains(response.Body.String(), "private-file-content") {
				t.Fatal("static handler exposed a file outside the site root")
			}
			if test.body != "" && !strings.Contains(response.Body.String(), test.body) {
				t.Fatal("static handler did not serve the expected content")
			}
		})
	}

	response := httptest.NewRecorder()
	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/app.js", nil)
	request.Header.Set("Range", "bytes=0-5")
	app.static(response, request)
	if response.Code != http.StatusPartialContent || response.Body.String() != "export" {
		t.Fatal("static handler did not preserve byte-range serving")
	}
	response = httptest.NewRecorder()
	app.static(response, httptest.NewRequestWithContext(t.Context(), http.MethodHead, "/app.js", nil))
	if response.Code != http.StatusOK || response.Body.Len() != 0 {
		t.Fatal("static handler did not preserve HEAD serving")
	}
	if err := os.WriteFile(filepath.Join(site, "app.js"), []byte("export default 2;"), 0o600); err != nil {
		t.Fatal(err)
	}
	response = httptest.NewRecorder()
	app.static(response, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/app.js", nil))
	if response.Body.String() != "export default 2;" {
		t.Fatal("disk-backed serving did not observe an edited asset")
	}
}

func TestServeIndexRejectsEscapingSymlink(t *testing.T) {
	site := t.TempDir()
	outside := filepath.Join(t.TempDir(), "private.html")
	if err := os.WriteFile(outside, []byte("private-file-content"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(site, "index.html")); err != nil {
		t.Fatal(err)
	}
	app := &App{config: Config{SiteDirectory: site}}
	response := httptest.NewRecorder()
	app.serveIndex(response, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil), "")
	if response.Code != http.StatusNotFound || strings.Contains(response.Body.String(), "private-file-content") {
		t.Fatal("index handler followed a symlink outside the site root")
	}
}

func TestStaticMarkdownHasStableMediaType(t *testing.T) {
	site := t.TempDir()
	content := "# Dashboard guide\n"
	if err := os.WriteFile(filepath.Join(site, "guide.md"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	app := &App{config: Config{SiteDirectory: site}}
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		response := httptest.NewRecorder()
		app.static(response, httptest.NewRequestWithContext(t.Context(), method, "/guide.md", nil))
		if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "text/markdown; charset=utf-8" {
			t.Fatalf("%s markdown: status %d, content type %q", method, response.Code, response.Header().Get("Content-Type"))
		}
		if method == http.MethodGet && response.Body.String() != content {
			t.Fatal("markdown content changed")
		}
		if method == http.MethodHead && response.Body.Len() != 0 {
			t.Fatal("HEAD markdown returned a body")
		}
	}
}

func TestServeIndexEscapesAccessTokenInJavaScriptContext(t *testing.T) {
	site := t.TempDir()
	if err := os.WriteFile(filepath.Join(site, "index.html"), []byte("<head></head>"), 0o600); err != nil {
		t.Fatal(err)
	}
	app := &App{config: Config{SiteDirectory: site}, accessToken: testAccessToken}
	for _, token := range []string{
		testAccessToken,
		`");alert(1);//`,
		`</script><script>alert(1)</script>`,
		`<!--<img src=x onerror=alert(1)>-->`,
		"quote\"backslash\\newline\nseparator\u2028\u2029",
	} {
		response := httptest.NewRecorder()
		app.serveIndex(response, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil), token)
		if response.Code != http.StatusOK {
			t.Fatal("index bootstrap failed")
		}
		body := response.Body.String()
		if strings.Count(body, "<script>") != 1 || strings.Count(body, "</script>") != 1 ||
			strings.Contains(body, "<img") {
			t.Fatal("access token escaped its script context")
		}
		_, argument, found := strings.Cut(body, `localStorage.setItem("cao-dashboard-access-token",`)
		if !found {
			t.Fatal("access token bootstrap is missing")
		}
		var decoded string
		if err := json.NewDecoder(strings.NewReader(argument)).Decode(&decoded); err != nil {
			t.Fatal("access token is not encoded as a JavaScript-compatible JSON string")
		}
		if decoded != token {
			t.Fatal("access token did not round-trip through the bootstrap")
		}
	}
}

func TestStaticAdminCapabilityRequiresAuthorizedHostedSession(t *testing.T) {
	site := t.TempDir()
	if err := os.WriteFile(filepath.Join(site, "index.html"), []byte("<head></head>"), 0o600); err != nil {
		t.Fatal(err)
	}
	stale := assuredSession("operator")
	stale.AuthenticatedAt = time.Now().UTC().Add(-24 * time.Hour)
	for _, test := range []struct {
		name    string
		hosted  bool
		session oauthSession
		admin   bool
	}{
		{name: "local bearer is not admin", session: assuredSession("operator")},
		{name: "hosted without session", hosted: true},
		{name: "hosted reader", hosted: true, session: assuredSession("reader")},
		{name: "hosted stale admin", hosted: true, session: stale},
		{name: "hosted unassured admin", hosted: true, session: oauthSession{Login: "operator"}},
		{name: "hosted admin", hosted: true, session: assuredSession("operator"), admin: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			app := &App{config: Config{SiteDirectory: site, AdminUsers: []string{"operator"}}}
			if test.hosted {
				app.oauth = &githubOAuth{}
			}
			request := httptest.NewRequestWithContext(
				context.WithValue(t.Context(), oauthSessionContextKey{}, test.session),
				http.MethodGet, "/?local-preview=admin", nil,
			)
			for _, path := range []string{"/", "/index.html", "/missing-route"} {
				request.URL.Path = path
				response := httptest.NewRecorder()
				app.static(response, request)
				if response.Code != http.StatusOK {
					t.Fatalf("index returned %d", response.Code)
				}
				if present := strings.Contains(response.Body.String(), "globalThis.__DASHBOARD_ADMIN__ = true"); present != test.admin {
					t.Fatalf("admin capability = %t, want %t", present, test.admin)
				}
				if response.Header().Get("Cache-Control") != "no-store" {
					t.Fatal("session-specific index must not be cached")
				}
			}
		})
	}
}
