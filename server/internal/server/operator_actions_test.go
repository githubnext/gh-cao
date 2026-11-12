package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/operational"
)

func TestOperatorActionsUseScopedDurableQueueAndNativeAdapter(t *testing.T) {
	app, _ := newSQLiteTestApp(t, false)
	prefix := "/" + strings.Repeat("a", 48)
	adapterPrefix := "/" + strings.Repeat("b", 48)
	calls := make(chan []string, 1)
	adapter := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != adapterPrefix+"/execute" || r.Header.Get("X-Command-ID") != "1" {
			t.Errorf("unexpected execution RPC: %s", r.URL.Path)
		}

		var args []string
		if err := json.NewDecoder(r.Body).Decode(&args); err != nil {
			t.Error(err)
		}
		calls <- args
		_, _ = w.Write([]byte("{\"stream\":\"stdout\",\"data\":\"maintenance ready\\n\"}\n{\"complete\":true,\"exitCode\":0}\n"))
	}))
	defer adapter.Close()
	app.config.Canvas = &LocalCanvasConfig{
		Prefix: prefix, AdapterURL: adapter.URL + adapterPrefix,
		OperatorActions: &LocalOperatorActionsConfig{QueuePath: filepath.Join(t.TempDir(), "commands.sqlite"), Repositories: []string{"github/gh-aw"}},
	}
	document := `{"dashboard":{"cli-actions":[
		{"id":"upgrade-repository","command":"gh aw upgrade --repo {{repository}}","arguments":[{"id":"create-pull-request","flag":"--create-pull-request","default":true}]},
		{"id":"dispatch","command":"gh workflow run agent.yml --repo {{repository}}"}
	]}}`
	if err := os.WriteFile(filepath.Join(app.config.SiteDirectory, "dashboard.json"), []byte(document), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := app.enableOperatorActions(t.Context()); err != nil {
		t.Fatal(err)
	}
	handler := app.Handler()
	request := func(method, path, body, origin string) *httptest.ResponseRecorder {
		req := httptest.NewRequestWithContext(t.Context(), method, "http://127.0.0.1:8080"+path, bytes.NewBufferString(body))
		req.URL.Scheme, req.URL.Host = "", ""
		req.Header.Set("Content-Type", "application/json")
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}
	valid := `{"id":"upgrade-repository","values":{"repository":"github/gh-aw"},"idempotencyKey":"same-request"}`
	for _, test := range []struct {
		path, body, origin string
		status             int
	}{
		{"/__cli_commands", valid, "http://127.0.0.1:8080", 404},
		{prefix + "/__cli_commands", valid, "https://evil.example", 403},
		{prefix + "/__cli_commands", valid, "", 403},
		{prefix + "/__cli_commands", `{"id":"dispatch","values":{"repository":"github/gh-aw"}}`, "http://127.0.0.1:8080", 403},
		{prefix + "/__cli_commands", `{"id":"upgrade-repository","values":{"repository":"octo/other"}}`, "http://127.0.0.1:8080", 403},
		{prefix + "/__cli_commands", `{"id":"upgrade-repository","values":{"repository":"github/gh-aw"},"arguments":{"create-pull-request":false}}`, "http://127.0.0.1:8080", 422},
		{prefix + "/__cli_commands", valid, "http://127.0.0.1:8080", 202},
		{prefix + "/__cli_commands", valid, "http://127.0.0.1:8080", 202},
	} {
		response := request(http.MethodPost, test.path, test.body, test.origin)
		if response.Code != test.status {
			t.Fatalf("%s: got %d expected %d: %s", test.path, response.Code, test.status, response.Body.String())
		}
	}
	worker, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { defer close(done); app.runOperatorCommands(worker) }()
	defer func() { cancel(); <-done }()
	select {
	case args := <-calls:
		if strings.Join(args, " ") != "upgrade --repo github/gh-aw --create-pull-request" {
			t.Fatalf("unexpected native argv: %v", args)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("declared maintenance did not reach the native adapter")
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		command, err := app.hostedCLI.queue.Detail(t.Context(), 1)
		if err != nil {
			t.Fatal(err)
		}
		if command.Status == operational.CLICommandCompleted && !command.ExecutionClaimed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("native execution did not settle: %+v", command)
		}
		time.Sleep(10 * time.Millisecond)
	}
	output := request(http.MethodGet, prefix+"/__cli_commands/1/output", "", "")
	if output.Code != 200 || !strings.Contains(output.Body.String(), "maintenance ready") || !strings.Contains(output.Body.String(), `"type":"complete"`) {
		t.Fatalf("output could not be reattached: %d %s", output.Code, output.Body.String())
	}
	commands, err := app.hostedCLI.queue.List(t.Context(), operational.CLICommandFilter{Limit: 10})
	if err != nil || commands.Total != 1 {
		t.Fatalf("idempotent admission duplicated maintenance: %+v %v", commands, err)
	}
	index := request(http.MethodGet, prefix+"/?local-preview=canvas", "", "")
	if !strings.Contains(index.Body.String(), "__CLI_QUEUE__ = true") || !strings.Contains(index.Body.String(), "__LOCAL_CLI_QUEUE__ = true") {
		t.Fatal("native dashboard did not activate local maintenance controls")
	}
}

func TestOperatorCancellationWaitsForNativeExitReceipt(t *testing.T) {
	app, _ := newSQLiteTestApp(t, false)
	started, canceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	adapter := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/execute":
			w.Header().Set("Content-Type", "application/x-ndjson")
			w.WriteHeader(http.StatusOK)
			w.(http.Flusher).Flush()
			close(started)
			<-release
			_, _ = w.Write([]byte("{\"complete\":true,\"exitCode\":130}\n"))
		case "/cancel":
			close(canceled)
			w.WriteHeader(http.StatusAccepted)
		default:
			http.NotFound(w, r)
		}
	}))
	defer adapter.Close()
	app.config.Canvas = &LocalCanvasConfig{AdapterURL: adapter.URL}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	type result struct {
		code *int
		err  error
	}
	completed := make(chan result, 1)
	go func() {
		code, err := app.callOperatorAdapter(ctx, operational.CLICommand{
			ID: 1, CLICommandInvocation: operational.CLICommandInvocation{
				Command: "gh aw upgrade --repo github/gh-aw --create-pull-request", Repository: "github/gh-aw",
			},
		}, func(string, []byte) {})
		completed <- result{code, err}
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("native execution did not start")
	}
	cancel()
	select {
	case <-canceled:
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("native cancellation was not requested")
	}
	select {
	case got := <-completed:
		close(release)
		t.Fatalf("cancellation was mistaken for a subprocess exit: %+v", got)
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	select {
	case got := <-completed:
		if got.err != nil || got.code == nil || *got.code != 130 {
			t.Fatalf("native exit receipt was not retained: %+v", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("native subprocess exit was not confirmed")
	}
}

func TestOperatorDisconnectDoesNotConfirmNativeExit(t *testing.T) {
	app, _ := newSQLiteTestApp(t, false)
	adapter := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("{\"stream\":\"stderr\",\"data\":\"interrupted\\n\"}\n"))
	}))
	defer adapter.Close()
	app.config.Canvas = &LocalCanvasConfig{AdapterURL: adapter.URL}
	code, err := app.callOperatorAdapter(t.Context(), operational.CLICommand{
		ID: 1, CLICommandInvocation: operational.CLICommandInvocation{
			Command: "gh aw upgrade --repo github/gh-aw --create-pull-request", Repository: "github/gh-aw",
		},
	}, func(string, []byte) {})
	if code != nil || err == nil {
		t.Fatalf("disconnected native subprocess was treated as confirmed: %v %v", code, err)
	}
}
