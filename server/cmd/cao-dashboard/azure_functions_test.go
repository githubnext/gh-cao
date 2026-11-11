package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/dashboardassets"
)

func TestAzureFunctionsCommand(t *testing.T) {
	t.Setenv("FUNCTIONS_CUSTOMHANDLER_PORT", "")
	if err := run([]string{"azure-functions"}); err == nil ||
		!strings.Contains(err.Error(), "FUNCTIONS_CUSTOMHANDLER_PORT is required") {
		t.Fatalf("Azure Functions must require the platform port: %v", err)
	}
	if err := run([]string{"azure-functions", "unexpected"}); err == nil ||
		!strings.Contains(err.Error(), "unknown command") {
		t.Fatalf("Azure Functions must reject positional arguments: %v", err)
	}
	if err := run([]string{"azure-functions", "--listen", "0.0.0.0:8080"}); err == nil ||
		!strings.Contains(err.Error(), "unknown flag") {
		t.Fatalf("Azure Functions must not accept ordinary serving flags: %v", err)
	}
	if err := run([]string{"functions"}); err == nil ||
		!strings.Contains(err.Error(), "unknown command") {
		t.Fatalf("the subcommand must be named azure-functions, not functions: %v", err)
	}
}

func TestResolveFunctionsConfig_RequiresPort(t *testing.T) {
	env := map[string]string{}
	getenv := func(name string) string { return env[name] }

	_, err := resolveFunctionsConfig(getenv)
	if err == nil {
		t.Fatal("expected an error when FUNCTIONS_CUSTOMHANDLER_PORT is unset")
	}
}

func TestResolveFunctionsConfig_TrimsAndRequiresNonBlankPort(t *testing.T) {
	env := map[string]string{"FUNCTIONS_CUSTOMHANDLER_PORT": "   "}
	getenv := func(name string) string { return env[name] }

	_, err := resolveFunctionsConfig(getenv)
	if err == nil {
		t.Fatal("expected an error when FUNCTIONS_CUSTOMHANDLER_PORT is blank")
	}
}

func TestResolveFunctionsConfig_Defaults(t *testing.T) {
	env := map[string]string{"FUNCTIONS_CUSTOMHANDLER_PORT": "8080"}
	getenv := func(name string) string { return env[name] }

	config, err := resolveFunctionsConfig(getenv)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if config.port != "8080" {
		t.Errorf("port = %q, want %q", config.port, "8080")
	}
	if config.host != "127.0.0.1" {
		t.Errorf("host = %q, want %q", config.host, "127.0.0.1")
	}
	if config.siteDirectory != "" {
		t.Errorf("siteDirectory = %q, want embedded resources", config.siteDirectory)
	}
	wantQueries := dashboardassets.Path("", dashboardassets.DashboardQueries)
	if config.queriesPath != wantQueries {
		t.Errorf("queriesPath = %q, want %q", config.queriesPath, wantQueries)
	}
}

func TestResolveFunctionsConfig_OverridesAndDerivedQueriesPath(t *testing.T) {
	env := map[string]string{
		"FUNCTIONS_CUSTOMHANDLER_PORT": " 9090 ",
		"FUNCTIONS_CUSTOMHANDLER_HOST": " 0.0.0.0 ",
		"CAO_AZURE_SITE_DIRECTORY":     "/srv/dashboard",
	}
	getenv := func(name string) string { return env[name] }

	config, err := resolveFunctionsConfig(getenv)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if config.port != "9090" {
		t.Errorf("port = %q, want %q", config.port, "9090")
	}
	if config.host != "0.0.0.0" {
		t.Errorf("host = %q, want %q", config.host, "0.0.0.0")
	}
	if config.siteDirectory != "/srv/dashboard" {
		t.Errorf("siteDirectory = %q, want %q", config.siteDirectory, "/srv/dashboard")
	}
	wantQueries := filepath.Join("/srv/dashboard", filepath.FromSlash(dashboardassets.DashboardQueries))
	if config.queriesPath != wantQueries {
		t.Errorf("queriesPath = %q, want %q", config.queriesPath, wantQueries)
	}
}

func TestResolveFunctionsConfig_ExplicitQueriesPathOverridesDerivedDefault(t *testing.T) {
	env := map[string]string{
		"FUNCTIONS_CUSTOMHANDLER_PORT": "8080",
		"CAO_AZURE_SITE_DIRECTORY":     "/srv/dashboard",
		"CAO_AZURE_DASHBOARD_QUERIES":  "/etc/cao/dashboard.json",
	}
	getenv := func(name string) string { return env[name] }

	config, err := resolveFunctionsConfig(getenv)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if config.queriesPath != "/etc/cao/dashboard.json" {
		t.Errorf("queriesPath = %q, want %q", config.queriesPath, "/etc/cao/dashboard.json")
	}
}

func TestEnvOrDefault(t *testing.T) {
	env := map[string]string{"SET_VALUE": "  configured  ", "BLANK_VALUE": "   "}
	getenv := func(name string) string { return env[name] }

	if got := envOrDefault(getenv, "SET_VALUE", "fallback"); got != "configured" {
		t.Errorf("envOrDefault(SET_VALUE) = %q, want %q", got, "configured")
	}
	if got := envOrDefault(getenv, "BLANK_VALUE", "fallback"); got != "fallback" {
		t.Errorf("envOrDefault(BLANK_VALUE) = %q, want %q", got, "fallback")
	}
	if got := envOrDefault(getenv, "MISSING_VALUE", "fallback"); got != "fallback" {
		t.Errorf("envOrDefault(MISSING_VALUE) = %q, want %q", got, "fallback")
	}
}

// TestShutdownOnDone_GracefulShutdownOnCancel verifies that cancelling ctx
// triggers a real *http.Server shutdown that stops it from serving further
// requests, using an actual listener rather than a mock.
func TestShutdownOnDone_GracefulShutdownOnCancel(t *testing.T) {
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	server.Start()
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		shutdownOnDone(ctx, server.Config, time.Second)
		close(done)
	}()

	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("shutdownOnDone did not return after context cancellation")
	}

	if err := getWithContext(t, server.URL); err == nil {
		t.Fatal("expected requests to fail after shutdown, but request succeeded")
	}
}

// getWithContext issues a context-scoped GET request and closes the response
// body, keeping tests free of the context-less http.Get helper.
func getWithContext(t *testing.T, url string) error {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("unexpected error building request: %v", err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()

	if _, err := io.Copy(io.Discard, response.Body); err != nil {
		t.Fatalf("unexpected error draining response body: %v", err)
	}
	return nil
}

// TestShutdownOnDone_WaitsForContext confirms shutdownOnDone blocks until
// ctx is done, rather than shutting down immediately.
func TestShutdownOnDone_WaitsForContext(t *testing.T) {
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	server.Start()
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		shutdownOnDone(ctx, server.Config, time.Second)
		close(done)
	}()

	select {
	case <-done:
		t.Fatal("shutdownOnDone returned before context was cancelled")
	case <-time.After(100 * time.Millisecond):
	}

	if err := getWithContext(t, server.URL); err != nil {
		t.Fatalf("expected server to still accept requests, got error: %v", err)
	}
}

func TestShutdownOnDoneWaitsForActiveRequestBeforeTelemetryFlush(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}

	requestDone := make(chan struct{})
	go func() {
		defer close(requestDone)
		response, err := http.DefaultClient.Do(request)
		if err == nil {
			_ = response.Body.Close()
		}
	}()
	<-started
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		shutdownOnDone(ctx, server.Config, time.Second)
	}()
	cancel()
	select {
	case <-done:
		t.Fatal("HTTP drain finished before active request")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	<-requestDone
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("HTTP drain did not finish after active request")
	}
}

type drainingTestHandler struct {
	http.Handler
	drain func()
}

func (h drainingTestHandler) Drain() { h.drain() }

func TestShutdownOnDoneDrainsStreams(t *testing.T) {
	started := make(chan struct{})
	drained := make(chan struct{})
	server := httptest.NewServer(drainingTestHandler{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			close(started)
			<-drained
			w.WriteHeader(http.StatusOK)
		}),
		drain: func() { close(drained) },
	})
	defer server.Close()

	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	requestDone := make(chan struct{})
	go func() {
		defer close(requestDone)
		response, err := http.DefaultClient.Do(request)
		if err == nil {
			_ = response.Body.Close()
		}
	}()
	<-started
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		shutdownOnDone(ctx, server.Config, time.Second)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("stream was not drained before HTTP shutdown")
	}
	<-requestDone
}
