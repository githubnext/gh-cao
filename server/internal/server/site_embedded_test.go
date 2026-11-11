//go:build dashboard_embed

package server

import (
	"bytes"
	"context"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"path"
	"strings"
	"testing"

	"github.com/githubnext/gh-cao/server/internal/dashboardassets"
	"github.com/githubnext/gh-cao/server/internal/model"
	"github.com/githubnext/gh-cao/server/internal/testutil"
)

func TestEmbeddedDashboardResources(t *testing.T) {
	t.Chdir(t.TempDir())
	snapshot := testutil.SQLiteSnapshot(t, map[string][]model.Row{
		"repositories": {{"id": "owner/repo", "fullName": "owner/repo"}},
	})
	app, err := NewLocalSQLiteApp(t.Context(), snapshot, Config{
		Listen: "127.0.0.1:8080", AccessToken: testAccessToken, MCPEnabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := app.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := app.Stop(context.WithoutCancel(t.Context())); err != nil {
			t.Error(err)
		}
	})
	site, err := dashboardassets.Site()
	if err != nil {
		t.Fatal(err)
	}
	files, javascriptFiles := 0, 0
	if err := fs.WalkDir(site, ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		files++
		if path.Ext(name) == ".js" {
			javascriptFiles++
		}
		request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/"+name, nil)
		response := httptest.NewRecorder()
		app.static(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("embedded asset %s: %d", name, response.Code)
		}
		if path.Base(name) == "index.html" {
			if !strings.Contains(response.Body.String(), `content="server-http"`) ||
				!strings.Contains(response.Body.String(), `content="sqlite"`) ||
				response.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("entry %s lost the server bootstrap", name)
			}
			return nil
		}
		expected, err := fs.ReadFile(site, name)
		if err != nil {
			return err
		}
		if !bytes.Equal(response.Body.Bytes(), expected) {
			t.Fatalf("embedded asset %s differs from the built resource", name)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if files < 2 || javascriptFiles == 0 {
		t.Fatal("embedded dashboard must contain its HTML and JavaScript assets")
	}
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		request := httptest.NewRequestWithContext(t.Context(), method, "/src/agent/mcp-contract.json", nil)
		request.Header.Set("Range", "bytes=0-5")
		response := httptest.NewRecorder()
		app.static(response, request)
		if response.Code != http.StatusPartialContent || method == http.MethodHead && response.Body.Len() != 0 ||
			method == http.MethodGet && response.Body.Len() != 6 {
			t.Fatalf("%s embedded byte range: %d, %d bytes", method, response.Code, response.Body.Len())
		}
	}
	for _, endpoint := range []string{"/", "/repositories", "/repositories/", "/src/agent/mcp-contract.json"} {
		request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "http://localhost"+endpoint, nil)
		response := httptest.NewRecorder()
		app.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusOK || strings.Contains(response.Body.String(), testAccessToken) {
			t.Fatalf("local embedded %s changed public asset access or exposed credentials: %d", endpoint, response.Code)
		}
	}
	unauthorized := httptest.NewRecorder()
	app.Handler().ServeHTTP(unauthorized, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "http://localhost/api/repositories", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatal("embedded serving bypassed API authentication")
	}
	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "http://localhost/repositories", nil)
	authorize(request)
	response := httptest.NewRecorder()
	app.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `content="server-http"`) {
		t.Fatal("embedded SPA navigation did not use the authenticated server handler")
	}

	if err := ValidateSite(t.TempDir()); err == nil {
		t.Fatal("an explicit empty site fell back to the embedded dashboard")
	}
}

func TestEmbeddedDashboardHosted(t *testing.T) {
	testHostedHandlerServesDashboardAndAssets(t, true)
}
