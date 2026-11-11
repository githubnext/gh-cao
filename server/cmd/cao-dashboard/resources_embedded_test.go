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
	snapshot := filepath.Join(directory, "snapshot.sqlite")
	if err := os.WriteFile(snapshot, sqliteDownloadFixture(t), 0o600); err != nil {
		t.Fatal(err)
	}
	listener := newTestListener(t)
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, binary, "serve", "--sqlite="+snapshot, "--listen="+address, // #nosec G204 -- Execute only the binary built above with test-owned configuration.
		"--access-token="+localSQLiteTestToken, "--mcp-enabled", "--exit-on-stdin-close")
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
		request.Header.Set("Authorization", "Bearer "+localSQLiteTestToken)
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
