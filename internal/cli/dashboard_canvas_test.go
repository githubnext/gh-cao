package cli

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestDashboardCanvasReadinessAndParentLifetime(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test server uses a POSIX shell")
	}
	directory := t.TempDir()
	server := filepath.Join(directory, "test-server")
	if err := os.WriteFile(server, []byte(`#!/bin/sh
if [ "$1" = "ingest" ]; then
  printf '%s\n' '{"type":"ready"}'
  while IFS= read -r request; do
    printf '%s\n' '{"type":"complete","result":{"revision":1}}'
  done
  exit 0
fi
if [ "$1" != "serve" ]; then exit 0; fi
printf '%s\n' 'serving local dashboard at http://127.0.0.1:12345/?access_token=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa' >&2
printf 'server diagnostics\n'
cat >/dev/null
printf stopped >"$0.stopped"
`), 0o700); err != nil {
		t.Fatal(err)
	}
	site := filepath.Join(directory, "site")
	for _, path := range []string{"index.html", "dashboard.json", "src/agent/queries.generated.json",
		"src/data/queries/database.json", "src/agent/catalog.generated.json", "src/agent/mcp-contract.json"} {
		writeTestFile(t, filepath.Join(site, path), "{}")
	}
	input, owner := io.Pipe()
	defer func() { _ = input.Close(); _ = owner.Close() }()
	output, writer := io.Pipe()
	defer func() { _ = output.Close(); _ = writer.Close() }()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	cmd := dashboardCommand("test", dashboardTestGH(t, time.Now()), func(context.Context, dashboardLogRequest) error {
		return nil
	})
	cmd.SetContext(ctx)
	cmd.SetIn(input)
	cmd.SetOut(writer)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{
		"--repo", "octo/api", "--cache-dir", filepath.Join(directory, "cache"),
		"--server-executable", server, "--site", site,
		"--postgres-url", "postgres://unused-runtime@127.0.0.1/test",
		"--json", "--exit-on-stdin-close",
	})
	done := make(chan error, 1)
	go func() {
		done <- cmd.Execute()
		_ = writer.Close()
	}()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("dashboard shutdown: %v", err)
		}
	}()
	var ready struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(output).Decode(&ready); err != nil {
		t.Fatal(err)
	}
	if ready.URL != "http://127.0.0.1:12345/?access_token=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa#page-overview" {
		t.Fatalf("unexpected readiness URL: %q", ready.URL)
	}
	// Closing the owner pipe, without a signal, must stop the server too.
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
	rest, err := io.ReadAll(output)
	if err != nil || len(rest) != 0 {
		t.Fatalf("stdout included non-readiness output: %q (%v)", rest, err)
	}
	if data, err := os.ReadFile(server + ".stopped"); err != nil || string(data) != "stopped" {
		t.Fatalf("native server did not stop with its owner: %q (%v)", data, err)
	}
}
