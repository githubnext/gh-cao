package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/githubnext/gh-cao/dashboard"
)

func validateDashboardListen(address string) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
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
		directory, err := os.MkdirTemp("", "gh-cao-dashboard-")
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
	for _, filename := range []string{server, filepath.Join(site, "index.html"),
		filepath.Join(site, "src/agent/queries.generated.json"),
		filepath.Join(site, "src/data/queries/database.json")} {
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
	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if _, err := collectDashboard(ctx, repositories, cache, options, gh, logs); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, options.duration)
	defer cancel()
	child := exec.CommandContext(ctx, server, "serve", "--sqlite", filepath.Join(cache, "gh-aw-logs.sqlite"),
		"--site", site, "--listen", options.listen, "--exit-on-stdin-close",
		"--database-queries", filepath.Join(site, "src/data/queries/database.json"),
		"--dashboard-queries", filepath.Join(site, "src/agent/queries.generated.json"),
		"--agent-catalog", filepath.Join(site, "src/agent/catalog.generated.json"),
		"--mcp-contract", filepath.Join(site, "src/agent/mcp-contract.json"))
	child.Env = dashboardServerEnvironment()
	stdin, err := child.StdinPipe()
	if err != nil {
		return err
	}
	child.Cancel = stdin.Close
	child.WaitDelay = 10 * time.Second
	child.Stdout = cmd.OutOrStdout()
	stderr, stderrWriter := io.Pipe()
	child.Stderr = stderrWriter
	defer func() { _ = stderr.Close(); _ = stderrWriter.Close() }()
	if err := child.Start(); err != nil {
		_ = stdin.Close()
		return fmt.Errorf("cannot start the local Go dashboard server: %w", err)
	}
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
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Dashboard: %s#page-immediate-operations\nSnapshot: %s\nCoverage: bounded recent activity; uncollected evidence is unavailable.\n", address, filepath.Join(cache, "gh-aw-logs.sqlite"))
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
		_ = stderrWriter.Close()
		serverDone <- serverResult{err, expected}
	}()
	finish := func(result serverResult) error {
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
	ticker := time.NewTicker(options.refresh)
	defer ticker.Stop()
	for {
		select {
		case result := <-serverDone:
			return finish(result)
		case <-ctx.Done():
			_ = stdin.Close()
			return finish(<-serverDone)
		case <-ticker.C:
			if _, err := collectDashboard(ctx, repositories, cache, options, gh, logs); err != nil && ctx.Err() == nil {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Dashboard refresh failed; previous snapshot retained: %v\n", err)
			}
		}
	}
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
