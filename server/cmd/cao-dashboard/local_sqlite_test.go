package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const localSQLiteTestToken = "0123456789abcdef0123456789abcdef"

func TestServeSQLiteFlagValidation(t *testing.T) {
	t.Setenv("CAO_POLICY_PATH", "")
	t.Setenv("CAO_POSTGRES_URL", "")
	for _, test := range []struct {
		name string
		args []string
		want string
	}{
		{"empty", []string{"--sqlite="}, "--sqlite requires"},
		{"removed-overlay", []string{"--sqlite-overlay=local.sqlite"}, "unknown flag: --sqlite-overlay"},
		{"removed-backfill", []string{"--backfill"}, "unknown flag: --backfill"},
		{"removed-backfill-interval", []string{"--backfill-interval=1m"}, "unknown flag: --backfill-interval"},
		{"postgres", []string{"--sqlite=local.sqlite", "--postgres-url=postgres://localhost/test"}, "none of the others"},
		{"redis", []string{"--sqlite=local.sqlite", "--redis-url=redis://localhost"}, "none of the others"},
		{"source", []string{"--sqlite=local.sqlite", "--source=deployed"}, "none of the others"},
		{"stdin-postgres", []string{"--exit-on-stdin-close", "--postgres-url=postgres://localhost/test"}, "requires local SQLite"},
		{"public", []string{"--sqlite=local.sqlite", "--listen=0.0.0.0:8080"}, "non-loopback"},
		{"default-public", []string{"--listen=0.0.0.0:8080"}, "non-loopback"},
		{"download-empty-url", []string{"--download-sqlite="}, "must use HTTP or HTTPS"},
		{"download-postgres", []string{"--download-sqlite", "--postgres-url=postgres://localhost/test"}, "none of the others"},
		{"download-redis", []string{"--download-sqlite", "--redis-url=redis://localhost"}, "none of the others"},
		{"download-source", []string{"--download-sqlite", "--source=deployed"}, "none of the others"},
		{"download-public", []string{"--download-sqlite", "--listen=0.0.0.0:8080"}, "non-loopback"},
		{"download-empty-path", []string{"--download-sqlite", "--sqlite="}, "--sqlite requires"},
		{"missing-cache-unbuilt-site", []string{"--sqlite=missing.sqlite", "--site=missing-site",
			"--dashboard-queries=../../../dashboard/site/src/agent/queries.generated.json"}, "build the dashboard"},
	} {
		t.Run(test.name, func(t *testing.T) {
			cmd := newServeCommand()
			cmd.SilenceUsage, cmd.SilenceErrors = true, true
			cmd.SetArgs(test.args)
			err := cmd.ExecuteContext(t.Context())
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("expected %q, got %v", test.want, err)
			}
		})
	}
}

func TestServeSQLiteOwnerStdinLifetime(t *testing.T) {
	t.Setenv("CAO_POLICY_PATH", "")
	t.Setenv("OTEL_SDK_DISABLED", "true")
	directory := t.TempDir()
	path := filepath.Join(directory, "snapshot.sqlite")
	if err := os.WriteFile(path, sqliteDownloadFixture(t), 0o600); err != nil {
		t.Fatal(err)
	}
	reader, writer := io.Pipe()
	defer func() { _ = reader.Close(); _ = writer.Close() }()
	cmd := newServeCommand()
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	cmd.SetIn(reader)
	listener := newTestListener(t)
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	cmd.SetArgs(append(sqliteServeTestArgs(t), "--sqlite="+path, "--listen="+address, "--exit-on-stdin-close"))
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- cmd.ExecuteContext(ctx) }()
	client := &http.Client{Timeout: time.Second}
	defer client.CloseIdleConnections()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+address+"/api/readiness", nil)
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Authorization", "Bearer "+localSQLiteTestToken)
		response, err := client.Do(request)
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				break
			}
		}
		select {
		case <-ctx.Done():
			t.Fatal("server did not become ready")
		case err := <-done:
			t.Fatalf("server stopped before readiness: %v", err)
		case <-ticker.C:
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("stdin-owned server shutdown: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("server outlived its owner")
	}
}

func TestServeSQLiteDownloadURLArguments(t *testing.T) {
	t.Setenv("CAO_POLICY_PATH", "")
	t.Setenv("CAO_POSTGRES_URL", "invalid-unused-postgres")
	t.Setenv("CAO_REDIS_URL", "invalid-unused-redis")
	t.Setenv("OTEL_SDK_DISABLED", "true")
	for _, args := range [][]string{
		{"--download-sqlite"},
		{"--download-sqlite=https://owner.github.io/project/"},
		{"--download-sqlite", "https://owner.github.io/project/"},
		{"--sqlite=custom.sqlite", "--download-sqlite=https://custom.example.org/"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			cmd := newServeCommand()
			cmd.SilenceUsage, cmd.SilenceErrors = true, true
			cmd.SetArgs(append(args, "--dashboard-queries=missing-queries.json"))
			err := cmd.ExecuteContext(t.Context())
			if err == nil || !strings.Contains(err.Error(), "missing-queries.json") {
				t.Fatalf("download flags must select SQLite without database services: %v", err)
			}
		})
	}
}

func TestServeSQLiteDownloadEnvironmentURL(t *testing.T) {
	t.Setenv("DASHBOARD_DATA_URL", "https://custom.example.org/site")
	cmd := newServeCommand()
	if got := cmd.Flags().Lookup("download-sqlite").NoOptDefVal; got != "https://custom.example.org/site" {
		t.Fatalf("download URL default = %q", got)
	}
	if got := cmd.Flags().Lookup("sqlite").DefValue; got != defaultSQLitePath {
		t.Fatalf("SQLite cache default = %q", got)
	}
}

func sqliteServeTestArgs(t *testing.T) []string {
	t.Helper()
	site := t.TempDir()
	if err := os.WriteFile(filepath.Join(site, "index.html"), []byte("<html><head></head><body>Dashboard</body></html>"), 0o600); err != nil {
		t.Fatal(err)
	}
	root, err := filepath.Abs("../../../dashboard/site/src")
	if err != nil {
		t.Fatal(err)
	}
	return []string{
		"--site=" + site,
		"--access-token=" + localSQLiteTestToken,
		"--database-queries=" + filepath.Join(root, "data/queries/database.json"),
		"--dashboard-queries=" + filepath.Join(root, "agent/queries.generated.json"),
		"--agent-catalog=" + filepath.Join(root, "agent/catalog.generated.json"),
		"--mcp-contract=" + filepath.Join(root, "agent/mcp-contract.json"),
	}
}

func TestServeSQLiteDefaultStartup(t *testing.T) {
	t.Setenv("CAO_POLICY_PATH", "")
	t.Setenv("CAO_POSTGRES_URL", "invalid-unused-postgres")
	t.Setenv("CAO_REDIS_URL", "invalid-unused-redis")
	t.Setenv("OTEL_SDK_DISABLED", "true")
	t.Setenv("PATH", t.TempDir())
	t.Setenv("GH_TOKEN", "unused-local-credential")
	content := sqliteDownloadFixture(t)
	baseArgs := sqliteServeTestArgs(t)
	for _, test := range []struct {
		name    string
		cached  bool
		custom  bool
		refresh bool
		mcp     bool
	}{
		{name: "default-missing"},
		{name: "default-offline", cached: true},
		{name: "custom-missing", custom: true},
		{name: "custom-offline", cached: true, custom: true},
		{name: "explicit-refresh", cached: true, refresh: true},
		{name: "local-token-mcp", cached: true, mcp: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			working := filepath.Join(t.TempDir(), "server")
			if err := os.Mkdir(working, 0o700); err != nil {
				t.Fatal(err)
			}
			t.Chdir(working)
			cache := defaultSQLitePath
			args := append([]string{}, baseArgs...)
			if test.mcp {
				t.Setenv("GITHUB_TOKEN", "local-user-token-not-an-actions-identity")
				t.Setenv("GITHUB_ACTOR", "")
				args = append(args, "--mcp-enabled")
			}
			if test.custom {
				cache = "custom.sqlite"
				args = append(args, "--sqlite="+cache)
			}
			if test.cached {
				if err := os.MkdirAll(filepath.Dir(cache), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(cache, content, 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(cache+".local.sqlite", []byte("legacy delta must remain untouched"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			var requests atomic.Int32
			remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				switch r.URL.Path {
				case "/payload-hashes.json":
					_, _ = fmt.Fprint(w, sqliteDownloadManifest(content))
				case "/gh-aw-logs.sqlite":
					_, _ = w.Write(content)
				default:
					http.NotFound(w, r)
				}
			}))
			defer remote.Close()
			t.Setenv("DASHBOARD_DATA_URL", remote.URL)
			if test.cached && !test.refresh {
				t.Setenv("DASHBOARD_DATA_URL", "invalid-unused-data-url")
			}
			if test.refresh {
				args = append(args, "--download-sqlite")
			}
			listener := newTestListener(t)
			address := listener.Addr().String()
			if err := listener.Close(); err != nil {
				t.Fatal(err)
			}
			args = append(args, "--listen="+address)
			cmd := newServeCommand()
			cmd.SilenceUsage, cmd.SilenceErrors = true, true
			cmd.SetArgs(args)
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			done := make(chan error, 1)
			go func() { done <- cmd.ExecuteContext(ctx) }()
			t.Cleanup(func() {
				cancel()
				select {
				case err := <-done:
					if err != nil {
						t.Errorf("serve shutdown: %v", err)
					}
				case <-time.After(10 * time.Second):
					t.Error("serve did not stop")
				}
			})
			client := &http.Client{Timeout: time.Second}
			defer client.CloseIdleConnections()
			ticker := time.NewTicker(10 * time.Millisecond)
			defer ticker.Stop()
			for {
				req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+address+"/api/readiness", nil)
				if err != nil {
					t.Fatal(err)
				}
				req.Header.Set("Authorization", "Bearer "+localSQLiteTestToken)
				response, requestErr := client.Do(req)
				if requestErr == nil {
					_ = response.Body.Close()
					if response.StatusCode == http.StatusOK {
						break
					}
				}
				select {
				case err := <-done:
					done <- err
					t.Fatalf("serve exited before readiness: %v", err)
				case <-ctx.Done():
					t.Fatal("serve did not become ready")
				case <-ticker.C:
				}
			}
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+address+"/api/v1/query",
				strings.NewReader(`{"sourceNames":["repositories"]}`))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Authorization", "Bearer "+localSQLiteTestToken)
			req.Header.Set("Content-Type", "application/json")
			response, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = response.Body.Close() }()
			var result struct {
				Sources map[string]struct{ Rows []json.RawMessage }
			}
			if err := json.NewDecoder(response.Body).Decode(&result); err != nil || response.StatusCode != http.StatusOK ||
				len(result.Sources["repositories"].Rows) != 1 {
				t.Fatalf("dashboard did not find the cached repository: status=%d, decode=%v", response.StatusCode, err)
			}
			wantRequests := int32(2)
			if test.cached && !test.refresh {
				wantRequests = 0
			}
			if got := requests.Load(); got != wantRequests {
				t.Fatalf("snapshot requests = %d, want %d", got, wantRequests)
			}
			if _, err := os.Stat(cache); err != nil {
				t.Fatalf("snapshot was not persisted: %v", err)
			}
			stored, err := os.ReadFile(cache) // #nosec G304 -- test-owned snapshot cache.
			if err != nil || string(stored) != string(content) {
				t.Fatal("serving modified the published snapshot")
			}
			if test.cached {
				legacy, err := os.ReadFile(cache + ".local.sqlite") // #nosec G304 -- test-owned legacy overlay.
				if err != nil || string(legacy) != "legacy delta must remain untouched" {
					t.Fatal("serving changed the legacy overlay")
				}
			} else if _, err := os.Stat(cache + ".local.sqlite"); !os.IsNotExist(err) {
				t.Fatalf("read-only serving created a writable overlay: %v", err)
			}
		})
	}
}

func TestServeSQLiteStartupFailuresDoNotRepairCache(t *testing.T) {
	t.Setenv("CAO_POLICY_PATH", "")
	t.Setenv("OTEL_SDK_DISABLED", "true")
	baseArgs := sqliteServeTestArgs(t)
	for _, test := range []struct {
		name         string
		want         string
		wantRequests int32
	}{
		{"invalid-cache", "read canonical SQLite snapshot", 0},
		{"directory-cache", "must be a regular file", 0},
		{"dangling-symlink", "open canonical SQLite snapshot", 0},
		{"download-failure", "HTTP 403", 1},
		{"unbuilt-site", "build the dashboard", 0},
		{"reviewed-policy", "reviewed operational-store selection", 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			cache := "cache.sqlite"
			switch test.name {
			case "invalid-cache":
				if err := os.WriteFile(cache, []byte("not a SQLite database"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "directory-cache":
				if err := os.Mkdir(cache, 0o700); err != nil {
					t.Fatal(err)
				}
			case "dangling-symlink":
				if err := os.Symlink("missing-target.sqlite", cache); err != nil {
					t.Fatal(err)
				}
			case "reviewed-policy":
				policy := `{"version":1,"control-plane":{"web":{"host":{"target":{"module":"container","replicas":1},"operational-store":{"backend":"memory","single-process":true,"allow-volatile":true}}}}}`
				if err := os.WriteFile("cao.json", []byte(policy), 0o600); err != nil {
					t.Fatal(err)
				}
				t.Setenv("CAO_POLICY_PATH", "cao.json")
			}
			before, beforeErr := os.Lstat(cache)
			var requests atomic.Int32
			remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				w.WriteHeader(http.StatusForbidden)
			}))
			defer remote.Close()
			t.Setenv("DASHBOARD_DATA_URL", remote.URL)
			cmd := newServeCommand()
			cmd.SilenceUsage, cmd.SilenceErrors = true, true
			args := append(append([]string{}, baseArgs...), "--sqlite="+cache)
			if test.name == "unbuilt-site" {
				args = append(args, "--site=missing-site")
			}
			cmd.SetArgs(args)
			err := cmd.ExecuteContext(t.Context())
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("expected %q, got %v", test.want, err)
			}
			if got := requests.Load(); got != test.wantRequests {
				t.Fatalf("snapshot requests = %d, want %d", got, test.wantRequests)
			}
			after, afterErr := os.Lstat(cache)
			if os.IsNotExist(beforeErr) {
				if !os.IsNotExist(afterErr) {
					t.Fatal("failed startup created a cache entry")
				}
			} else if beforeErr != nil || afterErr != nil || !os.SameFile(before, after) {
				t.Fatal("failed startup replaced an existing cache entry")
			}
		})
	}
}

func TestServePostgresRequiresExplicitSelection(t *testing.T) {
	t.Setenv("CAO_POLICY_PATH", "")
	t.Setenv("CAO_POSTGRES_URL", "")
	for _, flag := range []string{"--postgres-url=", "--redis-url=redis://localhost", "--source=deployed"} {
		t.Run(flag, func(t *testing.T) {
			cmd := newServeCommand()
			cmd.SilenceUsage, cmd.SilenceErrors = true, true
			cmd.SetArgs([]string{flag})
			err := cmd.ExecuteContext(t.Context())
			if err == nil || !strings.Contains(err.Error(), "--postgres-url or CAO_POSTGRES_URL is required") {
				t.Fatalf("explicit Postgres feature must require its database: %v", err)
			}
		})
	}
}
