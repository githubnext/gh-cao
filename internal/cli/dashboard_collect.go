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
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

var dashboardRunID = regexp.MustCompile(`^[1-9][0-9]{0,18}$`)

type dashboardWorkflow struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Path  string `json:"path"`
	State string `json:"state"`
}

type dashboardRepositoryEvidence struct {
	Repository  string
	Workflows   []dashboardWorkflow
	Runs        []map[string]any
	Enriched    map[string]map[string]any
	Directory   string
	SafeOutputs []map[string]any
	GithubID    string
}

type dashboardLogRequest struct {
	Repository, Directory, Cache, StartDate string
	Count                                   int
	ManagedCache                            bool
	WorkflowIDs                             []string
}

type dashboardLogsCollector func(context.Context, dashboardLogRequest) error

func collectDashboard(ctx context.Context, repositories []string, cache string, options dashboardOptions, gh ghExecutor, logs dashboardLogsCollector) (map[string]any, error) {
	if err := prepareDashboardCache(cache); err != nil {
		return nil, err
	}
	lock := filepath.Join(cache, "collection.lock")
	if err := os.Mkdir(lock, 0o700); err != nil {
		return nil, errors.New("dashboard collection is already locked; if its owner crashed, remove the empty collection.lock directory before retrying")
	}
	defer func() { _ = os.Remove(lock) }()
	if _, err := os.Stat(filepath.Join(cache, "gh-aw-logs.sqlite")); err == nil {
		if _, err := readDashboardStatus(ctx, cache, repositories); err != nil {
			return nil, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	now := time.Now().UTC()
	cutoff := now.Add(-options.window)
	evidence := make([]dashboardRepositoryEvidence, 0, len(repositories))
	for _, repository := range repositories {
		target := "github.com/" + repository
		identity, _, err := gh(ctx, "repo", "view", target, "--json", "nameWithOwner,url,id")
		if err != nil {
			return nil, fmt.Errorf("cannot access %s; check 'gh auth status --hostname github.com', repository access, and API quota; previous snapshot preserved", repository)
		}
		var resolved struct {
			Name string `json:"nameWithOwner"`
			URL  string `json:"url"`
			ID   string `json:"id"`
		}
		if json.Unmarshal([]byte(identity), &resolved) != nil || !strings.EqualFold(resolved.Name, repository) ||
			!strings.EqualFold(resolved.URL, "https://github.com/"+repository) || resolved.ID == "" {
			return nil, errors.New("repository lookup did not resolve the exact requested github.com repository")
		}
		inventory, _, err := gh(ctx, "workflow", "list", "--repo", target, "--all", "--limit", "1000", "--json", "id,name,path,state")
		if err != nil {
			return nil, fmt.Errorf("cannot read workflow inventory for %s; previous snapshot preserved", repository)
		}
		workflows := []dashboardWorkflow{}
		if err := json.Unmarshal([]byte(inventory), &workflows); err != nil || workflows == nil {
			return nil, errors.New("GitHub workflow inventory is not a valid workflow list")
		}
		raw, _, err := gh(ctx, "run", "list", "--repo", target, "--created", ">="+cutoff.Format(time.RFC3339),
			"--limit", strconv.Itoa(options.count), "--json",
			"databaseId,workflowDatabaseId,workflowName,displayTitle,status,conclusion,event,headBranch,headSha,createdAt,startedAt,updatedAt,url,attempt")
		if err != nil {
			return nil, fmt.Errorf("cannot read recent runs for %s; previous snapshot preserved", repository)
		}
		runs := []map[string]any{}
		if err := json.Unmarshal([]byte(raw), &runs); err != nil || runs == nil {
			return nil, errors.New("GitHub runs response is not a valid run list")
		}
		workflowIDs := dashboardObservedWorkflowIDs(workflows, runs)
		directory := filepath.Join(cache, "logs", dashboardCoordinate(repository))
		managedCache := true
		if options.logs != "" {
			managedCache = false
			directory, err = filepath.Abs(options.logs)
			if err != nil {
				return nil, err
			}
		} else if len(repositories) == 1 {
			// Reuse the operator's ordinary artifact cache when it exists.
			existing, statErr := os.Stat(".github/aw/logs")
			if statErr == nil && existing.IsDir() {
				managedCache = false
				directory, err = filepath.Abs(".github/aw/logs")
				if err != nil {
					return nil, err
				}
			}
		}
		if err := os.MkdirAll(directory, 0o700); err != nil {
			return nil, err
		}
		shard := filepath.Join(cache, dashboardCoordinate(repository)+".jsonl")
		if info, err := os.Lstat(shard); err == nil && !info.Mode().IsRegular() {
			return nil, errors.New("gh-aw cache must be a regular JSONL file")
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		if len(workflowIDs) > 0 {
			err = logs(ctx, dashboardLogRequest{
				Repository: repository, Directory: directory, Cache: shard,
				StartDate: cutoff.Format("2006-01-02"), Count: options.count,
				ManagedCache: managedCache, WorkflowIDs: workflowIDs,
			})
			if err != nil {
				return nil, fmt.Errorf("gh-aw log collection failed for %s; verify authentication and API quota; previous snapshot preserved", repository)
			}
		}
		cached, err := readDashboardLogCache(shard, repository)
		if err != nil && (len(workflowIDs) != 0 || !errors.Is(err, os.ErrNotExist)) {
			return nil, err
		}
		runs = mergeDashboardCachedRuns(runs, workflows, cached, options.count)
		evidence = append(evidence, dashboardRepositoryEvidence{
			Repository: repository, Workflows: workflows, Runs: runs, Enriched: cached.Enriched,
			Directory: directory, SafeOutputs: cached.SafeOutputs,
			GithubID: resolved.ID,
		})
	}
	batch, err := projectDashboardEvidence(evidence, cutoff, now)
	if err != nil {
		return nil, err
	}
	batch, err = hydrateDashboardEvidence(ctx, evidence, batch, now)
	if err != nil {
		return nil, fmt.Errorf("hydrate canonical gh-aw evidence; previous snapshot preserved: %w", err)
	}
	counts := map[string]int{}
	for store, rows := range batch {
		counts[store] = len(rows)
	}
	metadata := map[string]any{
		"id": "local-dashboard", "kind": "local-dashboard", "createdAt": now.Format(time.RFC3339Nano),
		"repositories": repositories, "windowStart": cutoff.Format(time.RFC3339Nano),
		"windowEnd": now.Format(time.RFC3339Nano), "windowHours": options.window.Hours(),
		"runLimitPerAcquisition": options.count, "maximumDisplayedRunsPerRepository": options.count * 2,
		"workflowLimitPerRepository": 1000,
		"availability":               "available", "completeness": "partial",
		"coverage": "Bounded recent gh-aw logs and available session, tool, firewall, skill, friction, safe-output, experiment, eval and grader evidence; partial daily observations. Missing artifacts, campaign policy, queue backlog, accepted outcomes and Operational Value remain unavailable.",
		"counts":   counts,
	}
	batch["transactions"] = []map[string]any{metadata}
	if err := publishDashboardSnapshot(ctx, cache, batch); err != nil {
		return nil, err
	}
	return metadata, nil
}

type dashboardCachedLogs struct {
	Enriched    map[string]map[string]any
	Runs        map[string]map[string]any
	SafeOutputs []map[string]any
}

func readDashboardLogCache(filename, repository string) (result dashboardCachedLogs, err error) {
	file, err := os.Open(filename)
	if err != nil {
		return result, fmt.Errorf("read gh-aw cached JSONL: %w", err)
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	result = dashboardCachedLogs{Enriched: map[string]map[string]any{}, Runs: map[string]map[string]any{}}
	scanner := bufio.NewScanner(io.LimitReader(file, (256<<20)+1))
	scanner.Buffer(make([]byte, 64<<10), 4<<20)
	total := 0
	for scanner.Scan() {
		total += len(scanner.Bytes()) + 1
		if total > 256<<20 {
			return result, errors.New("gh-aw JSONL cache exceeds the 256 MiB collection budget")
		}
		if strings.TrimSpace(scanner.Text()) == "" {
			continue
		}
		var envelope struct {
			Version    int              `json:"schema_version"`
			Kind       string           `json:"kind"`
			Run        map[string]any   `json:"run"`
			Request    map[string]any   `json:"request"`
			Payload    []map[string]any `json:"payload"`
			SafeOutput map[string]any   `json:"safe_output"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &envelope); err != nil {
			return result, errors.New("gh-aw cache contains malformed JSONL")
		}
		if envelope.Kind != "run" && envelope.Kind != "workflow_runs" && envelope.Kind != "safe_output_item" {
			continue
		}
		if envelope.Version != 2 && envelope.Version != 4 && envelope.Version != 5 {
			return result, errors.New("gh-aw cache uses an unsupported run schema version")
		}
		if envelope.Kind == "safe_output_item" {
			if !dashboardRunID.MatchString(nativeScalar(envelope.SafeOutput["run_id"])) {
				return result, errors.New("gh-aw safe output contains an invalid GitHub run ID")
			}
			result.SafeOutputs = append(result.SafeOutputs, envelope.SafeOutput)
			if len(result.SafeOutputs) > 10000 {
				return result, errors.New("gh-aw cache exceeds the retained safe-output budget")
			}
			continue
		}
		if envelope.Kind == "workflow_runs" {
			if !strings.EqualFold(nativeText(envelope.Request["repository"]), repository) ||
				envelope.Request["host"] != nil && envelope.Request["host"] != "github.com" {
				return result, errors.New("gh-aw cached listing is outside the requested repository")
			}
			for _, run := range envelope.Payload {
				id := nativeScalar(run["databaseId"])
				if !dashboardRunID.MatchString(id) {
					return result, errors.New("gh-aw cache contains an invalid GitHub run ID")
				}
				previous := result.Runs[id]
				if previous == nil || nativeTimestamp(run["updatedAt"]) >= nativeTimestamp(previous["updatedAt"]) {
					result.Runs[id] = run
				}
			}
			if len(result.Runs) > 10000 {
				return result, errors.New("gh-aw cache exceeds the retained run budget")
			}
			continue
		}
		if !strings.EqualFold(nativeText(envelope.Run["repository"]), repository) {
			return result, errors.New("gh-aw cache contains a run outside the requested repository")
		}
		id := nativeScalar(envelope.Run["run_id"])
		if !dashboardRunID.MatchString(id) {
			return result, errors.New("gh-aw cache contains an invalid GitHub run ID")
		}
		old := result.Enriched[id]
		if old == nil || nativeTimestamp(envelope.Run["updated_at"]) >= nativeTimestamp(old["updated_at"]) {
			result.Enriched[id] = envelope.Run
		}
		if len(result.Enriched) > 10000 {
			return result, errors.New("gh-aw cache exceeds the retained run budget")
		}
	}
	return result, scanner.Err()
}

func mergeDashboardCachedRuns(runs []map[string]any, workflows []dashboardWorkflow, cached dashboardCachedLogs, limit int) []map[string]any {
	paths := map[string]int64{}
	for _, workflow := range workflows {
		paths[strings.ToLower(workflow.Path)] = workflow.ID
	}
	seen := map[string]bool{}
	for index, run := range runs {
		id := nativeScalar(run["databaseId"])
		seen[id] = true
		if fresh := cached.Runs[id]; fresh != nil && nativeTimestamp(fresh["updatedAt"]) >= nativeTimestamp(run["updatedAt"]) {
			runs[index] = mergeDashboardRunListing(run, fresh)
		}
	}
	ids := make([]string, 0, len(cached.Enriched))
	for id := range cached.Enriched {
		ids = append(ids, id)
	}
	slices.SortFunc(ids, func(left, right string) int {
		order := strings.Compare(nativeText(cached.Enriched[right]["created_at"]), nativeText(cached.Enriched[left]["created_at"]))
		if order == 0 {
			order = strings.Compare(left, right)
		}
		return order
	})
	for _, id := range ids[:min(limit, len(ids))] {
		if seen[id] {
			continue
		}
		enriched := cached.Enriched[id]
		workflowID := paths[strings.ToLower(nativeText(enriched["workflow_path"]))]
		if workflowID <= 0 {
			continue
		}
		run := map[string]any{"databaseId": id, "workflowDatabaseId": workflowID}
		for from, to := range map[string]string{
			"created_at": "createdAt", "updated_at": "updatedAt", "started_at": "startedAt",
			"run_attempt": "attempt", "display_title": "displayTitle", "branch": "headBranch",
			"head_sha": "headSha", "status": "status", "conclusion": "conclusion", "event": "event", "url": "url",
		} {
			if value, ok := enriched[from]; ok {
				run[to] = value
			}
		}
		if fresh := cached.Runs[id]; fresh != nil {
			run = mergeDashboardRunListing(run, fresh)
		}
		runs = append(runs, run)
	}
	return runs
}

func mergeDashboardRunListing(run, fresh map[string]any) map[string]any {
	merged := make(map[string]any, len(run)+len(fresh))
	for key, value := range run {
		merged[key] = value
	}
	if fresh["attempt"] != nil && nativeScalar(fresh["attempt"]) != nativeScalar(run["attempt"]) {
		for _, key := range []string{"startedAt", "status", "conclusion", "updatedAt"} {
			delete(merged, key)
		}
	}
	for key, value := range fresh {
		merged[key] = value
	}
	merged["workflowDatabaseId"] = run["workflowDatabaseId"]
	return merged
}

func dashboardCoordinate(value string) string {
	// Canonical coordinate IDs use JavaScript encodeURIComponent, not form encoding.
	var result strings.Builder
	for _, char := range []byte(strings.ToLower(value)) {
		if char >= 'a' && char <= 'z' || char >= '0' && char <= '9' || strings.ContainsRune("-_.!~*'()", rune(char)) {
			result.WriteByte(char)
		} else {
			_, _ = fmt.Fprintf(&result, "%%%02X", char)
		}
	}
	return result.String()
}

func projectDashboardEvidence(evidence []dashboardRepositoryEvidence, cutoff, now time.Time) (map[string][]map[string]any, error) {
	batch := map[string][]map[string]any{"repositories": {}, "workflows": {}, "runs": {}}
	observed := now.Format(time.RFC3339Nano)
	for _, source := range evidence {
		parts := strings.Split(source.Repository, "/")
		if len(parts) != 2 {
			return nil, errors.New("invalid repository evidence identity")
		}
		repositoryID := "repository:" + dashboardCoordinate(source.Repository)
		batch["repositories"] = append(batch["repositories"], map[string]any{
			"id": repositoryID, "githubId": source.GithubID, "owner": parts[0], "name": parts[1], "observedAt": observed,
			"provenance": map[string]any{"source": "local-dashboard", "sourceId": source.Repository, "observedAt": observed},
		})
		workflows := map[string]dashboardWorkflow{}
		for _, workflow := range source.Workflows {
			if !strings.HasPrefix(workflow.Path, ".github/workflows/") || !strings.HasSuffix(workflow.Path, ".lock.yml") {
				continue
			}
			if workflow.ID <= 0 || workflow.Name == "" || strings.Contains(workflow.Path, "..") {
				return nil, errors.New("workflow inventory contains an invalid agentic workflow identity")
			}
			workflows[strconv.FormatInt(workflow.ID, 10)] = workflow
			path := strings.TrimSuffix(strings.ToLower(workflow.Path), ".lock.yml") + ".md"
			batch["workflows"] = append(batch["workflows"], map[string]any{
				"id": "workflow:" + dashboardCoordinate(source.Repository+":"+path), "repositoryId": repositoryID,
				"githubId": workflow.ID, "name": workflow.Name, "path": path, "state": workflow.State, "observedAt": observed,
				"organizationLink": "https://github.com/" + parts[0],
				"repositoryLink":   "https://github.com/" + source.Repository,
				"workflowLink":     "https://github.com/" + source.Repository + "/actions/workflows/" + filepath.Base(workflow.Path),
				"provenance":       map[string]any{"source": "local-dashboard", "sourceId": workflow.Path, "observedAt": observed},
			})
		}
		for _, raw := range source.Runs {
			workflow, exists := workflows[nativeScalar(raw["workflowDatabaseId"])]
			if !exists {
				continue
			}
			id := nativeScalar(raw["databaseId"])
			created, err := time.Parse(time.RFC3339Nano, nativeText(raw["createdAt"]))
			if err != nil || !dashboardRunID.MatchString(id) {
				return nil, errors.New("GitHub run has an invalid identity or creation time")
			}
			if created.Before(cutoff) || created.After(now) {
				continue
			}
			path := strings.TrimSuffix(strings.ToLower(workflow.Path), ".lock.yml") + ".md"
			attempt, attemptErr := strconv.Atoi(nativeScalar(raw["attempt"]))
			if attemptErr != nil || attempt < 1 {
				return nil, errors.New("GitHub run has an invalid attempt")
			}
			run := map[string]any{
				"id": "github:run:" + source.Repository + ":" + id, "githubRunId": id, "attempt": attempt,
				"repositoryId": repositoryID, "workflowId": "workflow:" + dashboardCoordinate(source.Repository+":"+path),
				"owner": parts[0], "repository": parts[1], "repositoryFullName": source.Repository,
				"workflowPath": path, "observedAt": observed,
				"organizationLink": "https://github.com/" + parts[0],
				"repositoryLink":   "https://github.com/" + source.Repository,
				"workflowLink":     "https://github.com/" + source.Repository + "/actions/workflows/" + filepath.Base(workflow.Path),
				"provenance":       map[string]any{"source": "local-dashboard", "sourceId": "run:" + id, "observedAt": observed},
			}
			for from, to := range map[string]string{
				"displayTitle": "title", "headBranch": "branch", "headSha": "headSha", "createdAt": "createdAt",
				"startedAt": "startedAt", "updatedAt": "updatedAt", "url": "runLink",
				"status": "status", "conclusion": "conclusion", "event": "event",
			} {
				if value, ok := raw[from]; ok {
					run[to] = value
				}
			}
			if raw["status"] == "completed" {
				run["completedAt"] = raw["updatedAt"]
			}
			batch["runs"] = append(batch["runs"], run)
		}
	}
	return batch, nil
}
