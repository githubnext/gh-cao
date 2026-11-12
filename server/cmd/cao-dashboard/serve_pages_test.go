package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func pagesFixture(t *testing.T) (string, map[string]string) {
	t.Helper()
	directory := t.TempDir()
	files := map[string]string{
		"index.html":                     "<!doctype html><script src=\"./assets/app.js\"></script><main>Pages dashboard</main>",
		"assets/app.js":                  "window.pages = true;",
		"assets/app.css":                 "body { color: black; }",
		"assets/engine.wasm":             "\x00asm\x01\x00\x00\x00",
		"src/main.js":                    "export const original = true;",
		"data/payload-hashes.json":       `{"inventory.json":"hash"}`,
		"data/inventory.json":            `{"repositories":["owner/repo"]}`,
		"data/shards/2026-10-21.json":    `{"runs":[{"id":1}]}`,
		"data/shards/2026-10-21.json.gz": "\x1f\x8b\x08\x00compressed",
		"nested/index.html":              "<!doctype html><main>Nested</main>",
	}
	for name, body := range files {
		filename := filepath.Join(directory, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(filename), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filename, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(directory, ".cao-pages-demo.json"),
		[]byte(`{"tool":"cao-pages-demo","synthetic":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	return directory, files
}

func TestServePagesRejectsOperationalArtifacts(t *testing.T) {
	for _, name := range []string{".cao-pages-demo.json", "gh-aw-logs.sqlite", "agent-summary.json", "payload-hashes.json"} {
		t.Run(name, func(t *testing.T) {
			directory, _ := pagesFixture(t)
			if err := os.WriteFile(filepath.Join(directory, name), []byte(`{}`), 0o600); err != nil {
				t.Fatal(err)
			}
			if root, _, err := newPagesHandler(directory, "/"); err == nil {
				_ = root.Close()
				t.Fatal("accepted operational or unlabelled data as a Pages demo")
			}
		})
	}
}

func testPagesHandler(t *testing.T, directory, basePath string) *pagesHandler {
	t.Helper()
	root, handler, err := newPagesHandler(directory, basePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })
	return handler
}

func TestServePagesFilesUnchanged(t *testing.T) {
	directory, files := pagesFixture(t)
	for _, basePath := range []string{"/", "/repository/"} {
		t.Run(basePath, func(t *testing.T) {
			handler := testPagesHandler(t, directory, basePath)
			for name, body := range files {
				for _, method := range []string{http.MethodGet, http.MethodHead} {
					request := httptest.NewRequestWithContext(t.Context(), method, basePath+name, nil)
					response := httptest.NewRecorder()
					handler.ServeHTTP(response, request)
					if response.Code != http.StatusOK {
						t.Fatalf("%s %s: status %d", method, request.URL, response.Code)
					}
					if response.Header().Get("Content-Length") != strconv.Itoa(len(body)) {
						t.Errorf("%s: Content-Length = %q", name, response.Header().Get("Content-Length"))
					}
					want := body
					if method == http.MethodHead {
						want = ""
					}
					if response.Body.String() != want {
						t.Errorf("%s %s changed contents", method, name)
					}
					if response.Header().Get("Set-Cookie") != "" {
						t.Errorf("%s sets hosted cookie", name)
					}
					mimeTypes := map[string]string{
						".html": "text/html", ".js": "text/javascript", ".css": "text/css",
						".json": "application/json", ".sqlite": "application/vnd.sqlite3", ".wasm": "application/wasm",
					}
					if wantMIME := mimeTypes[filepath.Ext(name)]; wantMIME != "" {
						if got := response.Header().Get("Content-Type"); !strings.HasPrefix(got, wantMIME) {
							t.Errorf("%s: MIME %q, want %q", name, got, wantMIME)
						}
					}
				}
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), http.MethodGet, basePath, nil))
			if response.Body.String() != files["index.html"] {
				t.Fatal("index was rewritten or not served")
			}
		})
	}
}

func TestServePagesBoundaries(t *testing.T) {
	directory, _ := pagesFixture(t)
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret"), []byte("outside secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, target := range map[string]string{
		"escape": outside, "secret": filepath.Join(outside, "secret"), "index-link": filepath.Join(outside, "secret"),
	} {
		if name == "index-link" {
			if err := os.Mkdir(filepath.Join(directory, name), 0o750); err != nil {
				t.Fatal(err)
			}
			name += "/index.html"
		}
		if err := os.Symlink(target, filepath.Join(directory, filepath.FromSlash(name))); err != nil {
			t.Fatal(err)
		}
	}
	for _, basePath := range []string{"/", "/repository/"} {
		handler := testPagesHandler(t, directory, basePath)
		for _, suffix := range []string{
			"missing", "data/", "assets/", "escape/secret", "secret", "index-link/",
			"../index.html", "%2e%2e/index.html", "%2e%2e%2findex.html",
			"data/../../index.html", "data//inventory.json", `data%5cinventory.json`,
			"%00index.html", "api/auth/session", "api/query", "assets/app.js/",
		} {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), http.MethodGet, basePath+suffix, nil))
			if response.Code != http.StatusNotFound || strings.Contains(response.Body.String(), "outside secret") {
				t.Errorf("%s%s: status %d, body %q", basePath, suffix, response.Code, response.Body.String())
			}
		}
		for _, target := range []string{basePath + "nested?query=1", strings.TrimSuffix(basePath, "/") + "?query=1"} {
			if target == "?query=1" {
				continue
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, nil))
			want := strings.Split(target, "?")[0] + "/?query=1"
			if response.Code != http.StatusMovedPermanently || response.Header().Get("Location") != want {
				t.Errorf("%s: status %d, redirect %q, want %q", target, response.Code, response.Header().Get("Location"), want)
			}
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), http.MethodPost, basePath, nil))
		if response.Code != http.StatusMethodNotAllowed || response.Header().Get("Allow") != "GET, HEAD" {
			t.Fatal("POST not rejected")
		}
		if basePath != "/" {
			response = httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/index.html", nil))
			if response.Code != http.StatusNotFound {
				t.Fatal("served outside base path")
			}
		}
	}
}

func TestServePagesRedirectsStayOnOrigin(t *testing.T) {
	directory, _ := pagesFixture(t)
	query := "next=https%3A%2F%2Felsewhere.invalid%2F&escaped=%252f"
	for _, basePath := range []string{"/", "/repository/"} {
		handler := testPagesHandler(t, directory, basePath)
		for _, requestPath := range []string{basePath + "nested", strings.TrimSuffix(basePath, "/")} {
			if requestPath == "" {
				continue
			}
			for _, method := range []string{http.MethodGet, http.MethodHead} {
				request := httptest.NewRequestWithContext(t.Context(), method, "https://elsewhere.invalid"+requestPath+"?"+query, nil)
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, request)
				location := response.Header().Get("Location")
				target, err := url.Parse(location)
				if err != nil || target.IsAbs() || target.Host != "" || strings.HasPrefix(location, "//") {
					t.Fatalf("%s %s: unsafe redirect %q, error %v", method, requestPath, location, err)
				}
				want := requestPath + "/?" + query
				if response.Code != http.StatusMovedPermanently || location != want {
					t.Errorf("%s %s: status %d, redirect %q, want %q", method, requestPath, response.Code, location, want)
				}
			}
		}
	}
}

func TestServePagesRangeAndConditional(t *testing.T) {
	directory, files := pagesFixture(t)
	handler := testPagesHandler(t, directory, "/")
	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/assets/engine.wasm", nil)
	request.Header.Set("Range", "bytes=0-5")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusPartialContent || response.Body.String() != files["assets/engine.wasm"][:6] {
		t.Fatal("binary asset partial download failed")
	}
	request = httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/assets/engine.wasm", nil)
	request.Header.Set("If-Modified-Since", response.Header().Get("Last-Modified"))
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNotModified || response.Body.Len() != 0 {
		t.Fatal("conditional request failed")
	}
}

func TestServePagesValidation(t *testing.T) {
	directory, _ := pagesFixture(t)
	command := newServePagesCommand()
	if got := command.Flags().Lookup("site").DefValue; got != "../.tmp/dashboard-pages" {
		t.Fatalf("--site default = %q", got)
	}
	if command.Flags().Lookup("directory") != nil {
		t.Fatal("obsolete --directory flag remains")
	}
	for _, address := range []string{"127.0.0.1:0", "[::1]:8080", "127.10.0.1:8080"} {
		if err := validatePagesListen(address); err != nil {
			t.Errorf("%s: %v", address, err)
		}
	}
	for _, address := range []string{":8080", "0.0.0.0:8080", "[::]:8080", "192.0.2.1:8080", "localhost:8080", "127.0.0.1"} {
		if err := validatePagesListen(address); err == nil {
			t.Errorf("accepted %q", address)
		}
	}
	for _, basePath := range []string{"", "repo", "//repo/", "/repo/../other", "/repo//", "/repo?x", "/repo#x", "/%2e/", `/repo\child`, "/repo\n", "/repo other"} {
		if root, _, err := newPagesHandler(directory, basePath); err == nil {
			_ = root.Close()
			t.Errorf("accepted base path %q", basePath)
		}
	}
	handler := testPagesHandler(t, directory, "/repo")
	if handler.basePath != "/repo/" {
		t.Fatal("base path not normalized")
	}
	for _, source := range []string{"", filepath.Join(directory, "missing"), filepath.Join(directory, "index.html"), t.TempDir()} {
		if root, _, err := newPagesHandler(source, "/"); err == nil {
			_ = root.Close()
			t.Errorf("accepted invalid Pages source %q", source)
		}
	}
}

type pagesReadyWriter struct {
	once  sync.Once
	ready chan string
}

func (w *pagesReadyWriter) Write(data []byte) (int, error) {
	w.once.Do(func() { w.ready <- string(data) })
	return len(data), nil
}

func TestServePagesCommandAndShutdown(t *testing.T) {
	directory, files := pagesFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := newRootCommand()
	ready := &pagesReadyWriter{ready: make(chan string, 1)}
	cmd.SetOut(ready)
	cmd.SetArgs([]string{"serve-pages", "--site", directory, "--listen", "127.0.0.1:0", "--base-path", "/repo"})
	done := make(chan error, 1)
	go func() { done <- cmd.ExecuteContext(ctx) }()
	var address string
	select {
	case line := <-ready.ready:
		if !strings.HasPrefix(line, "Pages preview: http://127.0.0.1:") || !strings.HasSuffix(line, "/repo/\n") {
			t.Fatalf("unexpected readiness line %q", line)
		}
		address = strings.TrimSpace(strings.TrimPrefix(line, "Pages preview: "))
	case err := <-done:
		t.Fatalf("command exited before ready: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("command did not become ready")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Timeout: time.Second}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil || response.StatusCode != http.StatusOK || !bytes.Equal(body, []byte(files["index.html"])) {
		t.Fatalf("live Pages response: %d, %q, %v", response.StatusCode, body, err)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("command did not stop")
	}
	request, err = http.NewRequestWithContext(context.Background(), http.MethodGet, address, nil)
	if err != nil {
		t.Fatal(err)
	}
	if response, err := client.Do(request); err == nil {
		_ = response.Body.Close()
		t.Fatal("listener still accepts requests after shutdown")
	}
}

func TestServePagesShutdownFailures(t *testing.T) {
	listener := newTestListener(t)
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	if err := runPagesServer(context.Background(), &http.Server{ReadHeaderTimeout: time.Second}, listener, time.Second); err == nil {
		t.Fatal("listener failure not propagated")
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	listener = newTestListener(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	httpServer := &http.Server{
		ReadHeaderTimeout: time.Second,
		Handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			close(entered)
			<-release
		}),
	}
	done := make(chan error, 1)
	go func() { done <- runPagesServer(ctx, httpServer, listener, 10*time.Millisecond) }()
	requestDone := make(chan struct{})
	go func() {
		defer close(requestDone)
		client := &http.Client{Timeout: 5 * time.Second}
		request, err := http.NewRequestWithContext(context.WithoutCancel(ctx), http.MethodGet, "http://"+listener.Addr().String(), nil)
		if err != nil {
			return
		}
		response, err := client.Do(request)
		if err == nil {
			_ = response.Body.Close()
		}
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("request did not start")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("shutdown error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown timeout not bounded")
	}
	select {
	case <-requestDone:
	case <-time.After(5 * time.Second):
		t.Fatal("active connection not forcibly closed")
	}
}
