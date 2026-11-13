package cli

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/spf13/cobra"
)

func dashboardCollectCommand(gh ghExecutor, logs dashboardLogsCollector) *cobra.Command {
	options := &dashboardOptions{refresh: 5 * time.Minute}
	var jsonOutput bool
	var timeout time.Duration
	cmd := &cobra.Command{
		Use: "collect", Short: "Collect and ingest one bounded dashboard pass now",
		Args: cobra.NoArgs,
		Long: `Collect one bounded pass through the same streaming and PostgreSQL pipeline
used by gh cao dashboard, then exit without starting an HTTP listener.

Existing cache, repository scope, history checkpoints and API limits are honored.
An active collection lock is never bypassed. Use a separate --cache-dir for
benchmarks; its managed PostgreSQL instance is independent of the live dashboard.
--json reports exclusive wall-time attribution in milliseconds. Setup is separate
from collection; logs time excludes downstream normalization, snapshot and database
work. This measures wall time, not CPU time or exhaustive upstream coverage.`,
	}
	dashboardCollectionFlags(cmd, options)
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Report phase timings, counts and partial coverage as JSON")
	cmd.Flags().DurationVar(&timeout, "timeout", 5*time.Minute, "Overall collection timeout (positive, at most 48h)")
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		if timeout <= 0 || timeout > dashboardMaximumDuration {
			return errors.New("--timeout must be positive and at most 48h")
		}
		if err := resolveDashboardStorageOptions(cmd, options); err != nil {
			return err
		}
		repositories, cache, err := resolveDashboardOptions(cmd, options, gh)
		if err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
		defer cancel()
		start := time.Now()
		server, site, cleanup, err := dashboardServerAssets(*options)
		if err != nil {
			return err
		}
		defer cleanup()
		if err := ensureDashboardCacheIgnored(cache); err != nil {
			return err
		}
		if err := initializeDashboardSnapshot(ctx, cache, repositories, *options); err != nil {
			return err
		}
		namespace := dashboardPostgresNamespace(repositories)
		options.postgresURL, err = configureDashboardPostgres(ctx, server, cache, namespace, options.postgresURL, options.containerEngine)
		if err != nil {
			return err
		}
		ingestor, err := startDashboardIngestor(ctx, server, site, cache, namespace, options.postgresURL, cmd.ErrOrStderr())
		if err != nil {
			return err
		}
		defer func() { _ = ingestor.Close() }()
		setup := time.Since(start)
		options.onRunBundles = ingestor.IngestRunBundles
		report, collectErr := benchmarkDashboardCollection(ctx, repositories, cache, *options, gh, logs, ingestor.Ingest)
		collectErr = errors.Join(collectErr, ingestor.Close())
		if collectErr != nil {
			report["status"] = "failed"
		}
		report["setup_ms"] = float64(setup) / float64(time.Millisecond)
		if jsonOutput {
			return errors.Join(collectErr, printJSON(cmd, report))
		}
		_, outputErr := fmt.Fprintf(cmd.OutOrStdout(), "Collection %s in %.0f ms (partial coverage).\n", report["status"], report["total_ms"])
		return errors.Join(collectErr, outputErr)
	}
	return cmd
}

func benchmarkDashboardCollection(ctx context.Context, repositories []string, cache string, options dashboardOptions, gh ghExecutor, logs dashboardLogsCollector, ingest func() error) (map[string]any, error) {
	timings := &dashboardCollectionTimings{phases: map[string]time.Duration{
		"github": 0, "logs": 0, "normalization": 0, "snapshot": 0, "postgres": 0,
	}}
	options.timings = timings
	var ingestionErr error
	publish := func() error {
		start := time.Now()
		err := ingest()
		timings.record("postgres", start)
		timings.ingestions++
		return err
	}
	options.onCollecting = func() { ingestionErr = errors.Join(ingestionErr, publish()) }
	options.onPublished = publish
	if runBundles := options.onRunBundles; runBundles != nil {
		options.onRunBundles = func() error {
			start := time.Now()
			err := runBundles()
			timings.record("postgres", start)
			timings.ingestions++
			return err
		}
	}
	timedGH := func(ctx context.Context, args ...string) (string, string, error) {
		start := time.Now()
		output, diagnostics, err := gh(ctx, args...)
		timings.record("github", start)
		timings.githubCommands++
		return output, diagnostics, err
	}
	streamed := map[string]bool{}
	timedLogs := func(ctx context.Context, request dashboardLogRequest) error {
		publishRuns := request.OnRuns
		request.OnRuns = func(ctx context.Context, runs []map[string]any) error {
			for _, run := range runs {
				key := nativeText(run["repository"]) + ":" + nativeScalar(run["run_id"]) + ":" + nativeScalar(run["run_attempt"])
				streamed[key] = true
			}
			if publishRuns != nil {
				return publishRuns(ctx, runs)
			}
			return nil
		}
		start, before := time.Now(), timings.total()
		err := logs(ctx, request)
		timings.phases["logs"] += max(0, time.Since(start)-(timings.total()-before))
		return err
	}
	start := time.Now()
	_, collectErr := collectDashboard(ctx, repositories, cache, options, timedGH, timedLogs)
	if ctx.Err() == nil && (collectErr == nil || timings.githubCommands > 0) {
		ingestionErr = errors.Join(ingestionErr, publish())
	}
	elapsed := time.Since(start)
	err := errors.Join(collectErr, ingestionErr)
	report := map[string]any{
		"status": "completed", "completeness": "partial", "repositories": repositories,
		"total_ms": float64(elapsed) / float64(time.Millisecond), "phase_ms": timings.milliseconds(),
		"evidence_publications": timings.publications, "postgres_ingestions": timings.ingestions,
		"github_commands":       timings.githubCommands,
		"streamed_run_attempts": len(streamed),
	}
	if err != nil {
		report["status"] = "failed"
		return report, err
	}
	batch, err := readDashboardArtifactBatch(ctx, cache)
	if err != nil {
		report["status"] = "failed"
		return report, err
	}
	counts := map[string]int{}
	for collection, rows := range batch {
		counts[collection] = len(rows)
	}
	report["retained_counts"] = counts
	state, err := readDashboardIngestion(ctx, cache)
	if err != nil {
		report["status"] = "failed"
		return report, err
	}
	report["ingestion_status"], report["ingestion_message"] = state.Status, state.Message
	report["api_remaining"], report["api_reserve"] = state.Remaining, state.Reserve
	return report, nil
}
