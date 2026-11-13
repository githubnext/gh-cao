package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDashboardCollectRejectsInvalidOptionsBeforeWork(t *testing.T) {
	for _, args := range [][]string{
		{"--repo", "octo/*"}, {"--timeout", "0s"}, {"--timeout", "49h"},
		{"--repo", "octo/api", "--count", "0"},
		{"--repo", "octo/api", "--window", "169h"},
		{"--repo", "octo/api", "--postgres-url", ""},
		{"--repo", "octo/api", "--container-engine", "remote"},
	} {
		t.Run(args[len(args)-1], func(t *testing.T) {
			gh := func(context.Context, ...string) (string, string, error) {
				t.Fatal("invalid options contacted GitHub")
				return "", "", nil
			}
			cmd := dashboardCommand("test", gh, nil)
			cmd.SetArgs(append([]string{"collect"}, args...))
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetErr(&bytes.Buffer{})
			if err := cmd.ExecuteContext(t.Context()); err == nil {
				t.Fatal("invalid collection options were accepted")
			}
		})
	}
}

func TestDashboardCollectMeasuresProductionPipeline(t *testing.T) {
	cache := t.TempDir()
	ingestions := 0
	writeLogs := dashboardTestLogs(t)
	logs := func(ctx context.Context, request dashboardLogRequest) error {
		if err := writeLogs(ctx, request); err != nil {
			return err
		}
		cached, err := readDashboardLogCache(request.Cache, request.Repository)
		if err != nil {
			return err
		}
		run := cached.Enriched["123"]
		run["workflow_path"] = ".github/workflows/agent.lock.yml"
		for range 2 {
			if err := request.OnRuns(ctx, []map[string]any{run}); err != nil {
				return err
			}
		}
		return nil
	}
	report, err := benchmarkDashboardCollection(t.Context(), []string{"octo/api"}, cache,
		dashboardOptions{window: dashboardMaximumWindow, count: 2},
		dashboardTestGH(t, time.Now().UTC().Add(-time.Second)), logs, func() error {
			ingestions++
			_, err := readDashboardArtifactBatch(t.Context(), cache)
			return err
		})
	if err != nil {
		t.Fatal(err)
	}
	if report["status"] != "completed" || report["completeness"] != "partial" ||
		report["retained_counts"].(map[string]int)["runs"] != 1 || ingestions != 4 ||
		report["streamed_run_attempts"] != 1 || report["evidence_publications"] != 3 {
		t.Fatalf("one-shot collection bypassed publication or status ingestion: %+v", report)
	}
	phases := report["phase_ms"].(map[string]float64)
	for _, phase := range []string{"github", "logs", "normalization", "snapshot", "postgres"} {
		if phases[phase] <= 0 {
			t.Fatalf("production phase %s was not measured: %+v", phase, phases)
		}
	}
}

func TestDashboardCollectDoesNotReportFailedIngestionAsSuccess(t *testing.T) {
	cause := errors.New("database unavailable")
	report, err := benchmarkDashboardCollection(t.Context(), []string{"octo/api"}, t.TempDir(),
		dashboardOptions{window: dashboardMaximumWindow, count: 2},
		dashboardTestGH(t, time.Now().UTC().Add(-time.Second)), dashboardTestLogs(t),
		func() error { return cause })
	if !errors.Is(err, cause) || report["status"] != "failed" {
		t.Fatalf("database failure was hidden: %+v %v", report, err)
	}
}

func TestDashboardCollectDoesNotBypassActiveCollection(t *testing.T) {
	cache := t.TempDir()
	if err := os.Mkdir(filepath.Join(cache, "collection.lock"), 0o700); err != nil {
		t.Fatal(err)
	}
	report, err := benchmarkDashboardCollection(t.Context(), []string{"octo/api"}, cache,
		dashboardOptions{window: dashboardMaximumWindow, count: 2},
		func(context.Context, ...string) (string, string, error) {
			t.Fatal("locked collection contacted GitHub")
			return "", "", nil
		}, nil, func() error {
			t.Fatal("locked collection ingested another process's in-flight snapshot")
			return nil
		})
	if err == nil || report["status"] != "failed" {
		t.Fatalf("active collection was bypassed: %+v %v", report, err)
	}
}
