package cli

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/githubnext/gh-cao/dashboard"
)

func validateDashboardListen(address string) error {
	host, port, err := net.SplitHostPort(address)
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		return errors.New("--listen must be a numeric loopback address and port")
	}
	if _, err := strconv.ParseUint(port, 10, 16); err != nil {
		return errors.New("--listen must be a numeric loopback address and port")
	}
	return nil
}

func dashboardServerAssets(options dashboardOptions) (server, site string, cleanup func(), err error) {
	if options.server != "" && options.site != "" {
		server, err = filepath.Abs(options.server)
		if err != nil {
			return "", "", nil, err
		}
		site, err = filepath.Abs(options.site)
		if err != nil {
			return "", "", nil, err
		}
		cleanup = func() {}
	} else {
		bundle, subErr := fs.Sub(dashboard.LocalBundle, "local-assets/bundle")
		if subErr != nil {
			return "", "", nil, subErr
		}
		if _, err := fs.Stat(bundle, "site/index.html"); err != nil {
			return "", "", nil, errors.New("source build has no local server bundle; use a release binary or pass --server-executable and --site")
		}
		directory, err := os.MkdirTemp(".", ".gh-cao-dashboard-")
		if err != nil {
			return "", "", nil, err
		}
		cleanup = func() { _ = os.RemoveAll(directory) }
		err = fs.WalkDir(bundle, ".", func(name string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			destination := filepath.Join(directory, filepath.FromSlash(name))
			if entry.IsDir() {
				return os.MkdirAll(destination, 0o700)
			}
			content, err := fs.ReadFile(bundle, name)
			if err != nil {
				return err
			}
			mode := os.FileMode(0o600)
			if name == "cao-dashboard" || name == "cao-dashboard.exe" {
				server = destination
				mode = 0o700
			}
			return os.WriteFile(destination, content, mode)
		})
		if err != nil {
			cleanup()
			return "", "", nil, err
		}
		site = filepath.Join(directory, "site")
	}
	for _, filename := range []string{server, filepath.Join(site, "index.html"), filepath.Join(site, "dashboard.json"),
		filepath.Join(site, "src/agent/queries.generated.json"),
		filepath.Join(site, "src/data/queries/database.json"),
		filepath.Join(site, "src/agent/catalog.generated.json"),
		filepath.Join(site, "src/agent/mcp-contract.json")} {
		if filename == "" {
			cleanup()
			return "", "", nil, errors.New("local server bundle contains no executable")
		}
		info, err := os.Stat(filename)
		if err != nil || !info.Mode().IsRegular() {
			cleanup()
			return "", "", nil, fmt.Errorf("local dashboard asset is missing: %s", filename)
		}
	}
	return server, site, cleanup, nil
}

func serveDashboard(cmd *cobra.Command, _ string, repositories []string, cache string, options dashboardOptions, gh ghExecutor, logs dashboardLogsCollector) error {
	server, site, cleanup, err := dashboardServerAssets(options)
	if err != nil {
		return err
	}
	defer cleanup()
	namespace := dashboardPostgresNamespace(repositories)
	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
		"Starting local CAO dashboard for %s...\n"+
			"Collecting up to %s of recent activity; cached artifacts are reused.\n"+
			"History fills in bounded background passes. The dashboard shows progress and quota pauses.\n"+
			"PostgreSQL namespace: %s.\n",
		strings.Join(repositories, ", "), options.window, namespace)
	if err := ensureDashboardCacheIgnored(cache); err != nil {
		return err
	}
	if err := initializeDashboardSnapshot(ctx, cache, repositories, options); err != nil {
		return err
	}
	options.postgresURL, err = configureDashboardPostgres(ctx, server, cache, namespace, options.postgresURL)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, options.duration)
	defer cancel()
	ingestRequests := make(chan struct{}, 1)
	actionsConfig, stopActions, err := startDashboardActionAdapter(ctx, repositories, cache, ingestRequests)
	if err != nil {
		return err
	}
	defer stopActions()
	child := exec.CommandContext(ctx, server, "serve", "--source", cache,
		"--redis-namespace", namespace,
		"--site", site, "--listen", options.listen, "--exit-on-stdin-close",
		"--operator-config", actionsConfig,
		"--database-queries", filepath.Join(site, "src/data/queries/database.json"),
		"--dashboard-queries", filepath.Join(site, "src/agent/queries.generated.json"),
		"--agent-catalog", filepath.Join(site, "src/agent/catalog.generated.json"),
		"--mcp-contract", filepath.Join(site, "src/agent/mcp-contract.json"))
	child.Env = append(dashboardServerEnvironment(), "CAO_POSTGRES_URL="+options.postgresURL, "CAO_OPERATIONAL_NAMESPACE="+namespace)
	stdin, err := child.StdinPipe()
	if err != nil {
		return err
	}
	child.Cancel = stdin.Close
	child.WaitDelay = 10 * time.Second
	serverOutput, serverOutputWriter := io.Pipe()
	child.Stdout = serverOutputWriter
	defer func() { _ = serverOutput.Close(); _ = serverOutputWriter.Close() }()
	addressDone := make(chan error, 1)
	readAddress := func() {
		scanner := bufio.NewScanner(serverOutput)
		for scanner.Scan() {
			var ready struct {
				URL string `json:"url"`
			}
			if json.Unmarshal(scanner.Bytes(), &ready) == nil && ready.URL != "" {
				printDashboardReady(cmd.ErrOrStderr(), ready.URL+"#page-overview", repositories, cache, options)
			} else {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), scanner.Text())
			}
		}
		addressDone <- scanner.Err()
	}
	stderr, stderrWriter := io.Pipe()
	child.Stderr = stderrWriter
	defer func() { _ = stderr.Close(); _ = stderrWriter.Close() }()
	if err := child.Start(); err != nil {
		_ = stdin.Close()
		return fmt.Errorf("cannot start the local Go dashboard server: %w", err)
	}
	go readAddress()
	defer func() { _ = stdin.Close() }()
	type serverResult struct {
		err      error
		expected bool
	}
	serverDone := make(chan serverResult, 1)
	outputDone := make(chan error, 1)
	go func() {
		defer func() { _ = stderr.Close() }()
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			line := scanner.Text()
			const marker = "serving local dashboard at "
			if _, address, found := strings.Cut(line, marker); found {
				printDashboardReady(cmd.ErrOrStderr(), address+"#page-overview", repositories, cache, options)
			} else {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), line)
			}
		}
		outputDone <- scanner.Err()
	}()
	go func() {
		err := child.Wait()
		expected := ctx.Err() != nil
		cancel()
		_ = serverOutputWriter.Close()
		_ = stderrWriter.Close()
		serverDone <- serverResult{err, expected}
	}()
	finish := func(result serverResult) error {
		if err := <-addressDone; err != nil {
			return fmt.Errorf("read local dashboard address: %w", err)
		}
		if err := <-outputDone; err != nil {
			return fmt.Errorf("read local dashboard server output: %w", err)
		}
		if !result.expected {
			if result.err != nil {
				return fmt.Errorf("local dashboard server stopped unexpectedly: %w", result.err)
			}
			return errors.New("local dashboard server stopped unexpectedly")
		}
		return nil
	}
	ingest := func() {
		refresh := exec.CommandContext(ctx, server, "ingest", "--source", cache,
			"--redis-namespace", namespace,
			"--database-queries", filepath.Join(site, "src/data/queries/database.json"))
		refresh.Env = append(dashboardServerEnvironment(), "CAO_POSTGRES_URL="+options.postgresURL)
		if err := refresh.Run(); err != nil && ctx.Err() == nil {
			_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "Dashboard PostgreSQL ingestion failed; previous canonical revision retained.")
		}
	}
	// Publish the "collecting" status before the pass ends so the dashboard
	// shows ingestion as running while GitHub evidence is acquired.
	options.onCollecting = ingest
	// PostgreSQL and the initial snapshot are ready before the server starts,
	// so the first pass begins immediately instead of waiting for the URL.
	ticker := time.NewTimer(0)
	defer ticker.Stop()
	pass := func() {
		if _, err := collectDashboard(ctx, repositories, cache, options, gh, logs); err != nil && ctx.Err() == nil {
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Dashboard refresh failed; previous snapshot retained: %v\n", err)
		}
		if ctx.Err() == nil {
			ingest()
		}
		// Requests made during this pass are satisfied by it.
		select {
		case <-ingestRequests:
		default:
		}
		ticker.Reset(dashboardRefreshInterval(cache, options.refresh))
	}
	for {
		select {
		case result := <-serverDone:
			return finish(result)
		case <-ctx.Done():
			_ = stdin.Close()
			return finish(<-serverDone)
		case <-ingestRequests:
			if !ticker.Stop() {
				select {
				case <-ticker.C:
				default:
				}
			}
			pass()
		case <-ticker.C:
			pass()
		}
	}
}

// dashboardEmptyRefresh is the retry interval while the snapshot has no runs,
// so a new dashboard populates quickly. Quota pauses still bound each pass.
const dashboardEmptyRefresh = 30 * time.Second

func dashboardRefreshInterval(cache string, refresh time.Duration) time.Duration {
	if dashboardSnapshotHasRuns(cache) {
		return refresh
	}
	return min(refresh, dashboardEmptyRefresh)
}

func dashboardSnapshotHasRuns(cache string) bool {
	file, err := os.Open(filepath.Join(cache, "gh-aw-logs-runs", "local.jsonl"))
	if err != nil {
		return false
	}
	defer func() { _ = file.Close() }()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64<<10), 4<<20)
	for scanner.Scan() {
		var row struct {
			Collection string `json:"collection"`
		}
		if json.Unmarshal(scanner.Bytes(), &row) == nil && row.Collection == "runs" {
			return true
		}
	}
	return false
}

func dashboardPostgresNamespace(repositories []string) string {
	digest := sha256.Sum256([]byte(strings.Join(repositories, "\n")))
	return fmt.Sprintf("dashboard-%x", digest[:12])
}

func printDashboardReady(output io.Writer, address string, repositories []string, cache string, options dashboardOptions) {
	maintenance := "every command requires approval in this terminal."
	if runtime.GOOS == "windows" {
		maintenance = "copy-only; trusted native terminal approval is unavailable on Windows."
	}
	_, _ = fmt.Fprintf(output,
		"\nCAO dashboard is ready. Open this URL in your browser:\n\n"+
			"  Dashboard: %s\n\n"+
			"  Repositories: %s\n"+
			"  Canonical artifact cache: %s\n"+
			"  Refresh: every %s\n"+
			"  Session: %s; press Ctrl+C to stop. Cached data is kept.\n"+
			"  Maintenance: %s\n"+
			"  Ingestion: resumable history discovery; status and quota are shown in the data tooltip.\n"+
			"  Coverage: partial recent activity; uncollected evidence is unavailable.\n\n",
		address, strings.Join(repositories, ", "), cache,
		options.refresh, options.duration, maintenance)
}

func dashboardServerEnvironment() []string {
	result := []string{}
	for _, value := range os.Environ() {
		name, _, _ := strings.Cut(value, "=")
		if strings.HasPrefix(name, "CAO_") || strings.HasPrefix(name, "DASHBOARD_") ||
			strings.HasPrefix(name, "OTEL_") || strings.HasPrefix(name, "GITHUB_") ||
			strings.HasPrefix(name, "GH_") {
			continue
		}
		result = append(result, value)
	}
	return append(result, "OTEL_SDK_DISABLED=true")
}
