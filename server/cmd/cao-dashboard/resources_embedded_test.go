//go:build dashboard_embed

package main

import (
	"context"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/model"
	"github.com/githubnext/gh-cao/server/internal/operational/postgres"
	"github.com/githubnext/gh-cao/server/internal/testutil"
)

func TestEmbeddedDashboardExecutable(t *testing.T) {
	t.Setenv("CAO_POLICY_PATH", "")
	t.Setenv("OTEL_SDK_DISABLED", "true")
	directory := t.TempDir()
	binary := filepath.Join(directory, "cao-dashboard")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	build := exec.CommandContext(t.Context(), "go", "build", "-o", binary, ".") // #nosec G204 -- Fixed source package and test-owned executable.
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build standalone executable: %v\n%s", err, output)
	}
	database, connection := testutil.PostgresWithConfig(t, t.Context(), "POSTGRES_URL")
	endpoint := testutil.ConnectionString(t, connection)
	services, err := postgres.New(t.Context(), endpoint, "embedded-test", postgres.Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = services.Close() })
	writer, err := database.BeginIngestion(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Abort(t.Context())
	if err := writer.Append(t.Context(), "$repositories", model.Row{"id": "octo/one", "owner": "octo", "name": "one"}); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Publish(t.Context(), "embedded-test"); err != nil {
		t.Fatal(err)
	}
	listener := newTestListener(t)
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	const token = "0123456789abcdef0123456789abcdef"
	command := exec.CommandContext(ctx, binary, "serve", "--listen="+address, // #nosec G204 -- Execute only the binary built above with test-owned configuration.
		"--access-token="+token, "--mcp-enabled", "--exit-on-stdin-close", "--redis-namespace=default")
	command.Env = append(os.Environ(), "CAO_POSTGRES_URL="+endpoint, "CAO_OPERATIONAL_NAMESPACE=embedded-test")
	command.Dir = directory
	input, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	logFile, err := os.Create(filepath.Join(directory, "server.log")) // #nosec G304 -- Private test-owned log.
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = logFile.Close() }()
	command.Stdout, command.Stderr = logFile, logFile
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait(); close(done) }()
	defer func() {
		_ = input.Close()
		if err := <-done; err != nil {
			output, _ := os.ReadFile(logFile.Name())
			t.Errorf("standalone server: %v\n%s", err, output)
		}
	}()
	client := &http.Client{Timeout: time.Second}
	defer client.CloseIdleConnections()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+address+"/api/readiness", nil)
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Authorization", "Bearer "+token)
		response, err := client.Do(request)
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				break
			}
		}
		select {
		case <-ctx.Done():
			t.Fatal("standalone server did not become ready")
		case err := <-done:
			output, _ := os.ReadFile(logFile.Name())
			t.Fatalf("standalone server exited before readiness: %v\n%s", err, output)
		case <-ticker.C:
		}
	}
	for _, path := range []string{"/", "/repositories", "/dashboard.json", "/src/agent/mcp-contract.json", "/service-worker.js"} {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+address+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Authorization", "Bearer "+token)
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		content, err := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if err != nil || response.StatusCode != http.StatusOK || len(content) == 0 {
			t.Fatalf("standalone %s: status=%d error=%v", path, response.StatusCode, err)
		}
		if path == "/" && !strings.Contains(string(content), `content="server-http"`) {
			t.Fatal("standalone executable lost the Go query backend")
		}
	}
}
