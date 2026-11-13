package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

func dashboardLogFlag(args []string, name string) string {
	index := slices.Index(args, name)
	if index < 0 || index+1 == len(args) {
		return ""
	}
	return args[index+1]
}

func finishDashboardLogStream(output io.Writer) error {
	_, err := io.WriteString(output, "{\"type\":\"summary\",\"data\":{\"runs\":[]}}\n{\"type\":\"complete\"}\n")
	return err
}

func TestDashboardLogsBatchRunsPeriodicallyBeforeCollectionCompletes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var batches []int
		published := make(chan int, 10)
		err := streamDashboardLogs(t.Context(), dashboardLogRequest{
			Count: 9,
			OnRuns: func(_ context.Context, runs []map[string]any) error {
				batches = append(batches, len(runs))
				published <- len(runs)
				return nil
			},
		}, nil, func(_ context.Context, _ []string, output io.Writer) error {
			encoder := json.NewEncoder(output)
			for id := range 9 {
				if err := encoder.Encode(map[string]any{"type": "run", "run": map[string]any{"run_id": id + 1}}); err != nil {
					return err
				}
			}
			time.Sleep(1100 * time.Millisecond)
			for _, expected := range []int{1, 8} {
				select {
				case count := <-published:
					if count != expected {
						return fmt.Errorf("unexpected periodic batch: got %d, want %d", count, expected)
					}
				default:
					return errors.New("run updates were delayed until EOF")
				}
			}
			return finishDashboardLogStream(output)
		})
		if err != nil || !slices.Equal(batches, []int{1, 8, 0}) {
			t.Fatalf("final summary was not reconciled after periodic batches: %v %v", batches, err)
		}
	})
}

type dashboardDelayedLogWriter struct {
	strings.Builder
}

func (w *dashboardDelayedLogWriter) Write(content []byte) (int, error) {
	time.Sleep(6 * time.Second)
	return w.Builder.Write(content)
}

func TestDashboardLogsOutputHelper(t *testing.T) {
	if os.Getenv("CAO_TEST_LOGS_OUTPUT") != "1" {
		return
	}
	if err := finishDashboardLogStream(os.Stdout); err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

func TestDashboardLogsSlowConsumerDrainsAfterProcessExit(t *testing.T) {
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("GH_PATH", binary)
	t.Setenv("CAO_TEST_LOGS_OUTPUT", "1")
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	var output dashboardDelayedLogWriter
	if err := executeDashboardLogs(ctx, []string{"-test.run=^TestDashboardLogsOutputHelper$"}, &output); err != nil {
		t.Fatalf("slow ingestion lost stdout after the collector exited successfully: %v", err)
	}
	if !strings.HasSuffix(output.String(), "{\"type\":\"complete\"}\n") {
		t.Fatal("collector completion was lost under consumer backpressure")
	}
}

func TestDashboardRemoteCollectionDoesNotDiscoverLocalWorkflows(t *testing.T) {
	t.Chdir(t.TempDir())
	ids := dashboardObservedWorkflowIDs([]dashboardWorkflow{
		{ID: 1, Path: ".github/workflows/first.lock.yml"},
		{ID: 2, Path: ".github/workflows/second.lock.yml"},
		{ID: 3, Path: ".github/workflows/ordinary.yml"},
	}, []map[string]any{
		{"workflowDatabaseId": 2}, {"workflowDatabaseId": 1},
		{"workflowDatabaseId": 3}, {"workflowDatabaseId": 2},
	})
	if !slices.Equal(ids, []string{"2", "1"}) {
		t.Fatalf("unexpected remote workflow scope: %v", ids)
	}
	cache := filepath.Join(t.TempDir(), "cached.jsonl")
	analysis := `{"schema_version":5,"kind":"run","run":{"run_id":123}}`
	writeTestFile(t, cache, analysis+"\n"+`{"kind":"workflow_runs","payload":[]}`+"\n")
	calls, count := 0, 0
	err := downloadDashboardLogs(t.Context(), dashboardLogRequest{
		Repository: "octo/api", Cache: cache, Directory: filepath.Dir(cache),
		WorkflowIDs: ids, Count: 5, ManagedCache: true,
		WorkflowPaths: map[string]string{"1": ".github/workflows/first.lock.yml", "2": ".github/workflows/second.lock.yml"},
	}, func(ctx context.Context, args []string, output io.Writer) error {
		if !slices.Equal(args[:4], []string{"aw", "logs", ".github/workflows/second.lock.yml", ".github/workflows/first.lock.yml"}) || dashboardLogFlag(args, "--repo") != "github.com/octo/api" ||
			!slices.Contains(args, "--audit") || slices.Contains(args, "--parse") ||
			dashboardLogFlag(args, "--format") != "jsonl" || !slices.Contains(args, "--verbose") ||
			dashboardLogFlag(args, "--max-storage") != "256" || !slices.Contains(args, "--prune-older-runs") ||
			dashboardLogFlag(args, "--artifacts") != "all" {
			t.Fatalf("remote rich collection contract was lost: %v", args)
		}
		if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > 2*time.Minute {
			t.Fatal("workflow targets do not share a bounded collection lifetime")
		}
		content, err := os.ReadFile(cache)
		if err != nil || strings.TrimSpace(string(content)) != analysis {
			t.Fatalf("discovery must be fresh while cached analysis survives: %s, %v", content, err)
		}
		calls++
		part, err := strconv.Atoi(dashboardLogFlag(args, "--count"))
		if err != nil {
			return err
		}
		count += part
		return finishDashboardLogStream(output)
	})
	if err != nil || calls != 1 || count != 5 {
		t.Fatalf("shared rich acquisition budget changed: calls=%d count=%d err=%v", calls, count, err)
	}
	entries, err := os.ReadDir(".")
	if err != nil || len(entries) != 0 {
		t.Fatalf("remote collection populated the empty working folder: %v, %v", entries, err)
	}
}

func TestDashboardListingRefreshPreservesInvalidCache(t *testing.T) {
	cache := filepath.Join(t.TempDir(), "cached.jsonl")
	content := "{\"kind\":\"workflow_runs\"}\ninvalid\n"
	writeTestFile(t, cache, content)
	if err := refreshDashboardLogListings(cache); err == nil {
		t.Fatal("invalid cached evidence was silently discarded")
	}

	actual, err := os.ReadFile(cache)
	if err != nil || string(actual) != content {
		t.Fatalf("failed refresh replaced the original cache: %v", err)
	}
}

func TestDashboardCollectionBudgetDoesNotMaskErrors(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(strconv.FormatBool(failed), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				cause := errors.New("invalid acquired evidence")
				err := downloadDashboardLogs(t.Context(), dashboardLogRequest{
					Cache: filepath.Join(t.TempDir(), "cached.jsonl"), WorkflowIDs: []string{"1"}, Count: 1,
					WorkflowPaths: map[string]string{"1": ".github/workflows/agent.lock.yml"},
				}, func(ctx context.Context, _ []string, _ io.Writer) error {
					time.Sleep(3 * time.Minute)
					if failed {
						return cause
					}
					return ctx.Err()
				})
				if failed && !errors.Is(err, cause) || !failed && err != nil {
					t.Fatalf("acquisition limit masked an error or rejected bounded partial evidence: %v", err)
				}
			})
		})
	}
}

func TestDashboardLogsShareQuotaCeilingAndHistoryBounds(t *testing.T) {
	calls := 0
	limited := false
	request := dashboardLogRequest{
		Repository: "octo/api", Cache: filepath.Join(t.TempDir(), "cache.jsonl"),
		WorkflowIDs: []string{"1", "2"}, WorkflowCounts: map[string]int{"1": 3, "2": 1}, Count: 4,
		WorkflowPaths: map[string]string{"1": ".github/workflows/first.lock.yml", "2": ".github/workflows/second.lock.yml"},
		StartDate:     "2026-11-05T00:00:00Z", EndDate: "2026-11-06T00:00:00Z",
		BeforeRunID: 101, AfterRunID: 90, MaxAPIUsed: 350, Limited: &limited,
		CheckQuota: func(context.Context, int) error {
			if calls > 0 {
				return errDashboardQuota
			}
			return nil
		},
	}
	err := downloadDashboardLogs(t.Context(), request, func(_ context.Context, args []string, output io.Writer) error {
		calls++
		if dashboardLogFlag(args, "--max-github-api-rate-limit") != "350" || dashboardLogFlag(args, "--count") != "4" ||
			dashboardLogFlag(args, "--before-run-id") != "101" || dashboardLogFlag(args, "--after-run-id") != "90" ||
			dashboardLogFlag(args, "--start-date") != request.StartDate || dashboardLogFlag(args, "--end-date") != request.EndDate ||
			dashboardLogFlag(args, "--max-storage") != "0" || slices.Contains(args, "--prune-older-runs") {
			t.Fatalf("collector widened shared quota, cache ownership or historical scope: %v", args)
		}
		return finishDashboardLogStream(output)
	})
	if err != nil || calls != 1 || limited {
		t.Fatalf("quota limit did not preserve partial bounded acquisition: calls=%d limited=%v err=%v", calls, limited, err)
	}
	request.CheckQuota = func(context.Context, int) error { return errDashboardQuota }
	err = downloadDashboardLogs(t.Context(), request, func(context.Context, []string, io.Writer) error {
		t.Fatal("collector ran after the shared quota was exhausted")
		return nil
	})
	if err != nil || !limited {
		t.Fatalf("quota pause was not preserved: limited=%v err=%v", limited, err)
	}
}

func TestDashboardLogsStreamPublishesBeforeCommandFinishes(t *testing.T) {
	published := make(chan struct{})
	var observations []string
	err := streamDashboardLogs(t.Context(), dashboardLogRequest{
		Count: 1,
		OnRuns: func(_ context.Context, runs []map[string]any) error {
			if nativeScalar(runs[0]["run_id"]) != "9007199254740993" {
				t.Fatal("stream parsing rounded a GitHub run identity")
			}
			observations = append(observations, nativeText(runs[0]["classification"]))
			if len(observations) == 1 {
				close(published)
			}
			return nil
		},
	}, nil, func(ctx context.Context, _ []string, output io.Writer) error {
		if _, err := io.WriteString(output, `{"type":"progress","stage":"started"}
	{"type":"future-extension","extra":true}
	{"type":"run","run":{"run_id":9007199254740993,"classification":"provisional"}}
	`); err != nil {
			return err
		}
		select {
		case <-published:
		case <-ctx.Done():
			return ctx.Err()
		}
		_, err := io.WriteString(output, `{"type":"summary","data":{"runs":[{"run_id":9007199254740993,"classification":"final"}]}}
	{"type":"complete"}
	`)
		return err
	})
	if err != nil || !slices.Equal(observations, []string{"provisional", "final"}) {
		t.Fatalf("stream did not reconcile after incremental publication: %v, %v", observations, err)
	}
}

func TestDashboardLogsStreamRejectsIncompleteAndFailedCommands(t *testing.T) {
	for _, test := range []struct {
		name, stream string
		exit         error
	}{
		{"empty", "", nil},
		{"missing-terminal", `{"type":"summary","data":{"runs":[]}}` + "\n", nil},
		{"missing-summary", `{"type":"complete"}` + "\n", nil},
		{"malformed", "not-json\n", nil},
		{"multiple-events", `{"type":"complete"} {"type":"complete"}` + "\n", nil},
		{"invalid-run", `{"type":"run"}` + "\n", nil},
		{"invalid-summary", `{"type":"summary","data":{}}` + "\n", nil},
		{"over-budget", `{"type":"summary","data":{"runs":[{},{}]}}` + "\n", nil},
		{"error-event", `{"type":"error","message":"PRIVATE_DIAGNOSTIC"}` + "\n", nil},
		{"late-exit-error", "{\"type\":\"summary\",\"data\":{\"runs\":[]}}\n{\"type\":\"complete\"}\n", errors.New("exit failed")},
	} {
		t.Run(test.name, func(t *testing.T) {
			summaries := 0
			err := streamDashboardLogs(t.Context(), dashboardLogRequest{
				Count: 1, OnRuns: func(context.Context, []map[string]any) error { summaries++; return nil },
			}, nil, func(_ context.Context, _ []string, output io.Writer) error {
				if _, err := io.WriteString(output, test.stream); err != nil {
					return err
				}
				return test.exit
			})
			if err == nil || summaries != 0 || strings.Contains(err.Error(), "PRIVATE_DIAGNOSTIC") {
				t.Fatalf("invalid stream reported success, published final data or leaked diagnostics: %v %d", err, summaries)
			}
		})
	}
}

func TestDashboardLogsStreamPublicationFailureCancelsProducer(t *testing.T) {
	cause := errors.New("publication failed")
	err := streamDashboardLogs(t.Context(), dashboardLogRequest{
		Count: 1, OnRuns: func(context.Context, []map[string]any) error { return cause },
	}, nil, func(ctx context.Context, _ []string, output io.Writer) error {
		if _, err := fmt.Fprintln(output, `{"type":"run","run":{"run_id":123}}`); err != nil {
			return err
		}
		<-ctx.Done()
		return ctx.Err()
	})
	if !errors.Is(err, cause) {
		t.Fatalf("publication error was masked: %v", err)
	}
}

func TestDashboardLogsPrerequisite(t *testing.T) {
	for _, mode := range []string{"installed", "stderr-help", "missing", "outdated", "install-fails", "still-unsupported", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			err := ensureDashboardLogs(ctx, func(_ context.Context, args ...string) (string, string, error) {
				calls++
				if calls == 1 || calls == 3 {
					if !slices.Equal(args, []string{"aw", "logs", "--help"}) {
						t.Fatalf("unexpected prerequisite check: %v", args)
					}
					if mode == "installed" || calls == 3 && mode != "still-unsupported" {
						return "--format jsonl", "", nil
					}
					if mode == "stderr-help" {
						return "", "--format jsonl", nil
					}
					if mode == "canceled" {
						cancel()
					}
					if mode == "outdated" || mode == "still-unsupported" {
						return "--format tsv", "", nil
					}
					return "", "", errors.New("unavailable")
				}
				if !slices.Equal(args, []string{"extension", "install", "github/gh-aw", "--pin", dashboardLogsVersion, "--force"}) {
					t.Fatalf("prerequisite installation changed workflows or policy: %v", args)
				}
				if mode == "install-fails" {
					return "", "PRIVATE_DIAGNOSTIC", errors.New("install failed")
				}
				return "", "", nil
			})
			wantError := mode == "install-fails" || mode == "still-unsupported" || mode == "canceled"
			if (err != nil) != wantError || err != nil && strings.Contains(err.Error(), "PRIVATE_DIAGNOSTIC") {
				t.Fatalf("unexpected prerequisite result: %v", err)
			}
			if (mode == "installed" || mode == "stderr-help" || mode == "canceled") && calls != 1 {
				t.Fatalf("unnecessary installation: %d calls", calls)
			}
		})
	}
}
