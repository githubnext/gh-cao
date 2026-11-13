package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	gh "github.com/cli/go-gh/v2"

	"github.com/githubnext/gh-cao/internal/ghcli"
)

type dashboardLogCommand func(context.Context, []string, io.Writer) error

const dashboardLogsVersion = "v1.2.3"

func ensureDashboardLogs(ctx context.Context, gh ghExecutor) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	supported := func() bool {
		output, diagnostics, err := gh(ctx, "aw", "logs", "--help")
		return err == nil && strings.Contains(output+"\n"+diagnostics, "--format jsonl")
	}
	if supported() {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(os.Stderr, "Installing gh-aw %s for streaming dashboard collection...\n", dashboardLogsVersion)
	_, _, err := gh(ctx, "extension", "install", "github/gh-aw", "--pin", dashboardLogsVersion, "--force")
	if err != nil {
		return errors.New("dashboard collection requires gh-aw with JSONL streaming; automatic installation failed; install github/gh-aw " + dashboardLogsVersion + " and retry")
	}
	if !supported() {
		return errors.New("installed gh-aw does not support 'gh aw logs --format jsonl'")
	}
	return nil
}

func executeDashboardLogs(ctx context.Context, args []string, output io.Writer) error {
	binary, err := gh.Path()
	if err != nil {
		return errors.New("dashboard collection requires GitHub CLI and the gh-aw extension")
	}
	directory, err := os.MkdirTemp("", "cao-logs-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(directory) }()
	child := exec.CommandContext(ctx, binary, args...)
	child.Dir = directory
	child.Env = append(os.Environ(), "GH_PROMPT_DISABLED=1", "GH_PAGER=cat")
	child.Stderr = os.Stderr
	child.WaitDelay = 5 * time.Second
	configureDashboardProcess(child)
	stdout, err := child.StdoutPipe()
	if err != nil {
		return err
	}
	defer func() { _ = stdout.Close() }()
	if err := child.Start(); err != nil {
		return errors.New("installed gh-aw log command could not start")
	}
	// Drain before Wait: database backpressure must not spend WaitDelay after
	// the collector exits and truncate the remaining JSONL events.
	_, readErr := io.Copy(output, stdout)
	if readErr != nil {
		_ = child.Cancel()
	}
	err = child.Wait()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if readErr != nil {
		return errors.New("installed gh-aw JSONL output could not be read completely")
	}
	if err != nil {
		return errors.New("installed gh-aw log command failed; check gh-aw installation, authentication and API quota")
	}
	return nil
}

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

func installedDashboardLogs(ctx context.Context, request dashboardLogRequest) error {
	if err := ensureDashboardLogs(ctx, ghcli.Exec); err != nil {
		return err
	}
	return downloadDashboardLogs(ctx, request, executeDashboardLogs)
}

func downloadDashboardLogs(ctx context.Context, request dashboardLogRequest, download dashboardLogCommand) error {
	if len(request.WorkflowIDs) == 0 || len(request.WorkflowIDs) > request.Count {
		return errors.New("dashboard log collection requires bounded observed remote workflow IDs")
	}
	for _, id := range request.WorkflowIDs {
		path := request.WorkflowPaths[id]
		if !strings.HasPrefix(path, ".github/workflows/") || !strings.HasSuffix(path, ".lock.yml") || strings.Contains(path, "..") {
			return errors.New("dashboard log collection requires observed remote agentic workflow paths")
		}
	}
	count := request.Count
	if request.WorkflowCounts != nil {
		count = 0
		for _, id := range request.WorkflowIDs {
			observed := request.WorkflowCounts[id]
			if observed < 1 || observed > request.Count-count {
				return errors.New("dashboard log collection requires a valid shared observed-run budget")
			}
			count += observed
		}
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
	ceiling := request.MaxAPIUsed
	if ceiling == 0 {
		ceiling = -1000
	}
	args := []string{"aw", "logs"}
	for _, id := range request.WorkflowIDs {
		args = append(args, request.WorkflowPaths[id])
	}
	args = append(args, "--repo", "github.com/"+request.Repository,
		"--count", strconv.Itoa(count), "--start-date", request.StartDate, "--end-date", request.EndDate,
		"--before-run-id", strconv.FormatInt(request.BeforeRunID, 10),
		"--after-run-id", strconv.FormatInt(request.AfterRunID, 10),
		"--output", request.Directory, "--cached-jsonl", request.Cache,
		"--timeout", "2", "--max-github-api-rate-limit", strconv.Itoa(ceiling),
		"--max-storage", strconv.Itoa(budget), "--artifacts", "all", "--audit",
		"--format", "jsonl", "--verbose")
	if request.ManagedCache {
		args = append(args, "--prune-older-runs")
	}
	scoped := request
	scoped.Count = count
	err := streamDashboardLogs(ctx, scoped, args, download)
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
	return ctx.Err()
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
