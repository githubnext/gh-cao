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

	"github.com/githubnext/gh-cao/server/internal/hostedcli"
	"github.com/githubnext/gh-cao/server/internal/operational"
)

func TestOperatorProductionMaintenanceCatalogAdmission(t *testing.T) {
	catalog, err := hostedcli.LoadCatalog(filepath.Join("..", "..", "..", "dashboard", "site", "dashboard.json"))
	if err != nil {
		t.Fatal(err)
	}
	app := &App{hostedCLI: &hostedCLI{catalog: catalog, local: &LocalOperatorActionsConfig{Repositories: []string{"octo/service", "octo/other"}}}}
	for _, id := range []string{"update-repository-sources", "disable-repository-workflows", "update-workflow-source", "update-workflows", "disable-workflow"} {
		values := map[string]string{"repository": "octo/service"}
		if id == "update-workflow-source" || id == "update-workflows" || id == "disable-workflow" {
			values = map[string]string{"workflow": ".github/workflows/review.md", "repository-coordinate": "octo/service"}
		}
		invocation, err := app.admitOperatorAction(t.Context(), hostedcli.Request{ID: id, Values: values})
		if err != nil || invocation.Repository != "octo/service" {
			t.Fatalf("production maintenance %s failed admission: %v", id, err)
		}
		for key := range values {
			if strings.Contains(key, "repository") {
				values[key] = "octo/outside"
			}
		}
		if _, err := app.admitOperatorAction(t.Context(), hostedcli.Request{ID: id, Values: values}); err == nil {
			t.Fatalf("production maintenance %s widened scope", id)
		}
	}
	if _, err := app.admitOperatorAction(t.Context(), hostedcli.Request{ID: "disable-workflow", Values: map[string]string{"workflow": "review*", "repository-coordinate": "octo/service"}}); err == nil {
		t.Fatal("wildcard workflow admitted")
	}
	invocation, err := app.admitOperatorAction(t.Context(), hostedcli.Request{ID: "add-repository", Values: map[string]string{"repository-pattern": "Octo/New-Service"}})
	if err != nil || invocation.Command != "gh cao repos add Octo/New-Service" || invocation.Repository != "octo/new-service" {
		t.Fatalf("exact enrollment failed admission: %+v %v", invocation, err)
	}
	invocation, err = app.admitOperatorAction(t.Context(), hostedcli.Request{ID: "add-repository", Values: map[string]string{"repository-pattern": "octo/service-*"}})
	if err != nil || invocation.Command != "gh cao repos add 'octo/service-*'" || invocation.Repository != "" {
		t.Fatalf("wildcard enrollment failed admission: %+v %v", invocation, err)
	}
	for _, pattern := range []string{"octo/../other", "octo", "octo/a b", "octo/api --repo x"} {
		if _, err := app.admitOperatorAction(t.Context(), hostedcli.Request{ID: "add-repository", Values: map[string]string{"repository-pattern": pattern}}); err == nil {
			t.Fatalf("invalid enrollment pattern %q admitted", pattern)
		}
	}
	invocation, err = app.admitOperatorAction(t.Context(), hostedcli.Request{ID: "setup-cao", Values: map[string]string{"repository-pattern": "octo/service"}})
	if err != nil || invocation.Command != "gh cao setup --repository octo/service" || invocation.Repository != "octo/service" {
		t.Fatalf("exact setup failed admission: %+v %v", invocation, err)
	}
	invocation, err = app.admitOperatorAction(t.Context(), hostedcli.Request{ID: "setup-cao", Values: map[string]string{"repository-pattern": "octo/service-*"}})
	if err != nil || invocation.Command != "gh cao setup --repository 'octo/service-*'" || invocation.Repository != "" {
		t.Fatalf("wildcard setup failed admission: %+v %v", invocation, err)
	}
}

func TestOperatorEnrollmentReachesNativeAdapterOutsideMaintenanceScope(t *testing.T) {
	calls := make(chan []string, 1)
	adapter := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var args []string
		if err := json.NewDecoder(r.Body).Decode(&args); err != nil {
			t.Error(err)
		}
		calls <- args
		_, _ = w.Write([]byte("{\"complete\":true,\"exitCode\":0}\n"))
	}))
	defer adapter.Close()
	app := &App{config: Config{OperatorActions: &LocalOperatorConfig{
		AdapterURL: adapter.URL, OperatorActions: &LocalOperatorActionsConfig{Repositories: []string{"octo/other"}},
	}}}
	code, err := app.callOperatorAdapter(t.Context(), operational.CLICommand{
		ID: 1, CLICommandInvocation: operational.CLICommandInvocation{
			Command: "gh cao repos add octo/service-*",
		},
	}, func(string, []byte) {})
	if err != nil || code == nil || *code != 0 {
		t.Fatalf("enrollment did not execute: %v %v", code, err)
	}
	if args := <-calls; strings.Join(args, " ") != "repos add octo/service-*" {
		t.Fatalf("unexpected native argv: %v", args)
	}
	if _, err := app.callOperatorAdapter(t.Context(), operational.CLICommand{
		ID: 2, CLICommandInvocation: operational.CLICommandInvocation{
			Command: "gh cao repos add octo/service", Repository: "octo/forged",
		},
	}, func(string, []byte) {}); err == nil {
		t.Fatal("enrollment with a forged queue repository executed")
	}
}

func TestOperatorExecutionRechecksCurrentScopeWithoutQueueState(t *testing.T) {
	adapter := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("unapproved maintenance reached the native adapter")
	}))
	defer adapter.Close()
	for _, scope := range []*LocalOperatorActionsConfig{
		nil,
		{Repositories: []string{"octo/other"}},
	} {
		app := &App{config: Config{OperatorActions: &LocalOperatorConfig{
			AdapterURL: adapter.URL, OperatorActions: scope,
		}}}
		var stderr strings.Builder
		code, err := app.callOperatorAdapter(t.Context(), operational.CLICommand{
			ID: 1, CLICommandInvocation: operational.CLICommandInvocation{
				Command: "gh aw disable review.md --repo octo/service", Repository: "octo/service",
			},
		}, func(stream string, data []byte) {
			if stream != "stderr" {
				t.Errorf("rejection was written to %s", stream)
			}
			_, _ = stderr.Write(data)
		})
		if code == nil || *code == 0 || err == nil {
			t.Fatal("missing or changed scope did not fail closed")
		}
		if !strings.Contains(stderr.String(), "no command was started.") {
			t.Fatalf("pre-dispatch rejection was not streamed: %q", stderr.String())
		}
	}
}

func TestOperatorActionsUseScopedDurableQueueAndNativeAdapter(t *testing.T) {
	app, _ := newLocalPostgresTestApp(t, false)
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
	app.config.OperatorActions = &LocalOperatorConfig{
		AdapterURL:      adapter.URL + adapterPrefix,
		OperatorActions: &LocalOperatorActionsConfig{Repositories: []string{"octo/control"}},
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
		req.Header.Set("Authorization", "Bearer "+testAccessToken)
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}
	valid := `{"id":"upgrade-repository","values":{"repository":"octo/control"},"idempotencyKey":"same-request"}`
	for _, test := range []struct {
		path, body, origin string
		status             int
	}{
		{"/__cli_commands", valid, "https://evil.example", 403},
		{"/__cli_commands", valid, "", 403},
		{"/__cli_commands", `{"id":"dispatch","values":{"repository":"octo/control"}}`, "http://127.0.0.1:8080", 403},
		{"/__cli_commands", `{"id":"upgrade-repository","values":{"repository":"octo/other"}}`, "http://127.0.0.1:8080", 403},
		{"/__cli_commands", `{"id":"upgrade-repository","values":{"repository":"octo/control"},"arguments":{"create-pull-request":false}}`, "http://127.0.0.1:8080", 422},
		{"/__cli_commands", valid, "http://127.0.0.1:8080", 202},
		{"/__cli_commands", valid, "http://127.0.0.1:8080", 202},
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
		if strings.Join(args, " ") != "upgrade --repo octo/control --create-pull-request" {
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
	output := request(http.MethodGet, "/__cli_commands/1/output", "", "")
	if output.Code != 200 || !strings.Contains(output.Body.String(), "maintenance ready") || !strings.Contains(output.Body.String(), `"type":"complete"`) {
		t.Fatalf("output could not be reattached: %d %s", output.Code, output.Body.String())
	}
	commands, err := app.hostedCLI.queue.List(t.Context(), operational.CLICommandFilter{Limit: 10})
	if err != nil || commands.Total != 1 {
		t.Fatalf("idempotent admission duplicated maintenance: %+v %v", commands, err)
	}
	index := request(http.MethodGet, "/", "", "")
	if !strings.Contains(index.Body.String(), "__CLI_QUEUE__ = true") || !strings.Contains(index.Body.String(), "__LOCAL_CLI_QUEUE__ = true") {
		t.Fatal("native dashboard did not activate local maintenance controls")
	}
	capability := httptest.NewRecorder()
	handler.ServeHTTP(capability, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "http://127.0.0.1:8080/?access_token="+testAccessToken, nil))
	var session *http.Cookie
	for _, cookie := range capability.Result().Cookies() {
		if cookie.Name == localCLISessionCookie {
			session = cookie
		}
	}
	if session == nil || session.Path != "/__cli_commands" || !session.HttpOnly || !session.Secure || session.SameSite != http.SameSiteStrictMode || strings.Contains(session.Value, testAccessToken) {
		t.Fatalf("worker command session cookie is missing or unscoped: %+v", session)
	}
	// The data worker carries only the scoped cookie, never the bearer token.
	workerRequest := func(method, body, origin string, cookie *http.Cookie) int {
		req := httptest.NewRequestWithContext(t.Context(), method, "http://127.0.0.1:8080/__cli_commands", bytes.NewBufferString(body))
		req.URL.Scheme, req.URL.Host = "", ""
		req.Header.Set("Content-Type", "application/json")
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		req.AddCookie(cookie)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response.Code
	}
	forged := &http.Cookie{
		Name: localCLISessionCookie, Value: strings.Repeat("0", 64), Path: "/__cli_commands",
		Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode,
	}
	for _, test := range []struct {
		method, origin string
		cookie         *http.Cookie
		status         int
	}{
		{http.MethodGet, "", session, 200},
		{http.MethodPost, "http://127.0.0.1:8080", session, 202},
		{http.MethodPost, "https://evil.example", session, 403},
		{http.MethodGet, "", forged, 403},
		{http.MethodPost, "http://127.0.0.1:8080", forged, 403},
	} {
		if code := workerRequest(test.method, valid, test.origin, test.cookie); code != test.status {
			t.Fatalf("%s %s cookie=%s: got %d expected %d", test.method, test.origin, test.cookie.Value[:4], code, test.status)
		}
	}
	for _, test := range []struct {
		method, path, origin, token string
		status                      int
	}{
		{http.MethodGet, "/__cli_commands", "", app.localCLISession(), 200},
		{http.MethodPost, "/__cli_commands", "http://127.0.0.1:8080", app.localCLISession(), 202},
		{http.MethodPost, "/__cli_commands", "https://evil.example", app.localCLISession(), 403},
		{http.MethodPost, "/__cli_commands", "", app.localCLISession(), 403},
		{http.MethodGet, "/__cli_commands", "", testAccessToken, 403},
		{http.MethodGet, "/__cli_commands", "", "forged", 403},
		{http.MethodGet, "/__cli_commands", "", "", 403},
		{http.MethodGet, "/api/v1/diagnostics", "", app.localCLISession(), 401},
	} {
		req := httptest.NewRequestWithContext(t.Context(), test.method, "http://127.0.0.1:8080"+test.path, bytes.NewBufferString(valid))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", test.origin)
		req.Header.Set("X-CAO-CLI-Session", test.token)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		if response.Code != test.status {
			t.Fatalf("%s %s with scoped header returned %d expected %d", test.method, test.path, response.Code, test.status)
		}
	}
}

func TestOperatorCancellationWaitsForNativeExitReceipt(t *testing.T) {
	app, _ := newLocalPostgresTestApp(t, false)
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
	app.config.OperatorActions = &LocalOperatorConfig{
		AdapterURL: adapter.URL, OperatorActions: &LocalOperatorActionsConfig{Repositories: []string{"octo/control"}},
	}
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
				Command: "gh aw upgrade --repo octo/control --create-pull-request", Repository: "octo/control",
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
	app, _ := newLocalPostgresTestApp(t, false)
	adapter := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("{\"stream\":\"stderr\",\"data\":\"interrupted\\n\"}\n"))
	}))
	defer adapter.Close()
	app.config.OperatorActions = &LocalOperatorConfig{
		AdapterURL: adapter.URL, OperatorActions: &LocalOperatorActionsConfig{Repositories: []string{"octo/control"}},
	}
	code, err := app.callOperatorAdapter(t.Context(), operational.CLICommand{
		ID: 1, CLICommandInvocation: operational.CLICommandInvocation{
			Command: "gh aw upgrade --repo octo/control --create-pull-request", Repository: "octo/control",
		},
	}, func(string, []byte) {})
	if code != nil || err == nil {
		t.Fatalf("disconnected native subprocess was treated as confirmed: %v %v", code, err)
	}
}
