package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	ghaw "github.com/github/gh-aw/pkg/cli"
)

func dashboardObservedWorkflowIDs(workflows []dashboardWorkflow, runs []map[string]any) []string {
	agentic := map[string]bool{}
	for _, workflow := range workflows {
		if workflow.ID > 0 && strings.HasPrefix(workflow.Path, ".github/workflows/") && strings.HasSuffix(workflow.Path, ".lock.yml") {
			agentic[strconv.FormatInt(workflow.ID, 10)] = true
		}
	}
	ids := []string{}
	for _, run := range runs {
		id := nativeScalar(run["workflowDatabaseId"])
		if agentic[id] {
			ids = append(ids, id)
			delete(agentic, id)
		}
	}
	return ids
}

func embeddedDashboardLogs(ctx context.Context, request dashboardLogRequest) error {
	return downloadDashboardLogs(ctx, request, ghaw.DownloadWorkflowLogs)
}

func downloadDashboardLogs(ctx context.Context, request dashboardLogRequest, download func(context.Context, ghaw.LogsDownloadOptions) error) error {
	if len(request.WorkflowIDs) == 0 || len(request.WorkflowIDs) > request.Count {
		return errors.New("dashboard log collection requires bounded observed remote workflow IDs")
	}
	if err := refreshDashboardLogListings(request.Cache); err != nil {
		return err
	}
	caller := ctx
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	budget := 0
	if request.ManagedCache {
		budget = 256
	}
	for index, id := range request.WorkflowIDs {
		if request.CheckQuota != nil {
			if err := request.CheckQuota(ctx, 1); err != nil {
				if !errors.Is(err, errDashboardQuota) {
					return err
				}
				if request.Limited != nil {
					*request.Limited = true
				}
				return nil
			}
		}
		count := request.Count / len(request.WorkflowIDs)
		if index < request.Count%len(request.WorkflowIDs) {
			count++
		}
		if request.WorkflowCounts != nil {
			count = request.WorkflowCounts[id]
		}
		ceiling := request.MaxAPIUsed
		if ceiling == 0 {
			ceiling = -1000
		}
		// An omitted workflow makes the pinned collector discover local lock files.
		// Explicit remote IDs work from an empty folder and cannot select a namesake.
		err := download(ctx, ghaw.LogsDownloadOptions{
			WorkflowName: id, RepoOverride: "github.com/" + request.Repository, Count: count,
			StartDate: request.StartDate, EndDate: request.EndDate,
			BeforeRunID: request.BeforeRunID, AfterRunID: request.AfterRunID,
			OutputDir: request.Directory, CachedJSONL: request.Cache,
			TimeoutMinutes: 2, MaxGitHubAPIRateLimit: ceiling, MaxStorageMB: budget, MaxStorageMBSet: true,
			ArtifactSets: []string{"all"}, Audit: true, SuppressRender: true,
			PruneOlderRuns: request.ManagedCache,
		})
		if ctx.Err() == context.DeadlineExceeded && caller.Err() == nil &&
			(err == nil || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled)) {
			_, _ = fmt.Fprintln(os.Stderr, "Dashboard collection reached its two-minute budget; available cached evidence will be published with partial coverage.")
			if request.Limited != nil {
				*request.Limited = true
			}
			return nil
		}
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	return nil
}

func refreshDashboardLogListings(filename string) (err error) {
	input, err := os.Open(filename)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, input.Close()) }()
	output, err := os.CreateTemp(filepath.Dir(filename), ".dashboard-logs-*.jsonl")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(output.Name()) }()
	scanner := bufio.NewScanner(io.LimitReader(input, (256<<20)+1))
	scanner.Buffer(make([]byte, 64<<10), 4<<20)
	total := 0
	for scanner.Scan() {
		total += len(scanner.Bytes()) + 1
		if total > 256<<20 {
			err = errors.New("gh-aw JSONL cache exceeds the 256 MiB collection budget")
			break
		}
		if strings.TrimSpace(scanner.Text()) == "" {
			continue
		}
		var envelope struct {
			Kind string `json:"kind"`
		}
		if err = json.Unmarshal(scanner.Bytes(), &envelope); err != nil {
			err = errors.New("gh-aw cache contains malformed JSONL")
			break
		}
		// Analyses and artifacts remain reusable; open-ended discovery lists do not.
		if envelope.Kind != "workflow_runs" {
			if _, err = fmt.Fprintln(output, scanner.Text()); err != nil {
				break
			}
		}
	}
	err = errors.Join(err, scanner.Err(), output.Close())
	if err != nil {
		return err
	}
	return os.Rename(output.Name(), filename)
}
