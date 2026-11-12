package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
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
	EndDate                                 string
	Count                                   int
	ManagedCache                            bool
	WorkflowIDs                             []string
	WorkflowCounts                          map[string]int
	BeforeRunID, AfterRunID                 int64
	MaxAPIUsed                              int
	CheckQuota                              func(context.Context, int) error
	Limited                                 *bool
}

type dashboardLogsCollector func(context.Context, dashboardLogRequest) error

func collectDashboard(ctx context.Context, repositories []string, cache string, options dashboardOptions, gh ghExecutor, logs dashboardLogsCollector) (metadata map[string]any, err error) {
	if err := prepareDashboardCache(cache); err != nil {
		return nil, err
	}
	lock := filepath.Join(cache, "collection.lock")
	if err := os.Mkdir(lock, 0o700); err != nil {
		return nil, errors.New("dashboard collection is already locked; if its owner crashed, remove the empty collection.lock directory before retrying")
	}
	defer func() { _ = os.Remove(lock) }()
	if _, err := os.Stat(filepath.Join(cache, "inventory-sources.json")); err == nil {
		if _, err := readDashboardStatus(ctx, cache, repositories); err != nil {
			return nil, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	now := time.Now().UTC()
	state, err := readDashboardIngestion(ctx, cache)
	if err != nil {
		return nil, err
	}
	if state.TargetHours != 0 && state.TargetHours != options.window.Hours()*float64(len(repositories)) {
		state.Cursors = map[string]dashboardHistoryCursor{}
		state.NextRepo = 0
	}
	previous := maps.Clone(state.Cursors)
	previousRepo := state.NextRepo
	state.Status, state.Message = "collecting", "Discovering retained history and collecting available artifacts; coverage remains partial."
	state.NextAt = ""
	state.summarize(repositories, now.Add(-options.window), now, options.window)
	if _, statErr := os.Stat(filepath.Join(cache, "inventory-sources.json")); errors.Is(statErr, os.ErrNotExist) {
		metadata = dashboardObservationMetadata(repositories, options, now, nil)
		metadata["coverage"] = "Initial collection pending; upstream evidence is not yet available."
		if err := publishDashboardSnapshot(ctx, cache, map[string][]map[string]any{"transactions": {metadata}}); err != nil {
			return nil, err
		}
	}
	defer func() {
		if err != nil {
			state.Cursors, state.NextRepo = maps.Clone(previous), previousRepo
			state.Status, state.Message = "failed", dashboardPauseMessage(err)
			if errors.Is(err, errDashboardQuota) {
				state.Status = "paused"
				state.NextAt = time.Now().UTC().Add(options.refresh).Format(time.RFC3339)
				if state.Remaining != nil && *state.Remaining <= state.Reserve {
					state.NextAt = state.ResetAt
				}
			}
		}
		state.summarize(repositories, time.Now().UTC().Add(-options.window), time.Now().UTC(), options.window)
		if err != nil {
			state.Cursors = previous
		}
		writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		writeErr := writeDashboardIngestion(writeCtx, cache, state)
		if errors.Is(err, errDashboardQuota) && writeErr == nil {
			metadata, err = readDashboardStatus(writeCtx, cache, repositories)
		} else {
			err = errors.Join(err, writeErr)
		}
	}()
	budget, err := newDashboardAcquisition(ctx, gh, &state)
	if err != nil {
		return nil, err
	}
	metadata, err = collectDashboardPass(ctx, repositories, cache, options, budget, logs, &state, now)
	return metadata, err
}

func collectDashboardPass(ctx context.Context, repositories []string, cache string, options dashboardOptions, budget *dashboardAcquisition, logs dashboardLogsCollector, state *dashboardIngestion, now time.Time) (map[string]any, error) {
	gh := budget.exec
	cutoff := now.Add(-options.window)
	evidence := make([]dashboardRepositoryEvidence, 0, 1)
	repositoryIndex := state.NextRepo % len(repositories)
	for _, repository := range repositories[repositoryIndex : repositoryIndex+1] {
		state.Repository = repository
		if err := writeDashboardIngestion(ctx, cache, *state); err != nil {
			return nil, err
		}
		target := "github.com/" + repository
		identity, _, err := gh(ctx, "repo", "view", target, "--json", "nameWithOwner,url,id")
		if err != nil {
			if errors.Is(err, errDashboardQuota) {
				return nil, err
			}
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
			if errors.Is(err, errDashboardQuota) {
				return nil, err
			}
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
			if errors.Is(err, errDashboardQuota) {
				return nil, err
			}
			return nil, fmt.Errorf("cannot read recent runs for %s; previous snapshot preserved", repository)
		}
		runs := []map[string]any{}
		if err := json.Unmarshal([]byte(raw), &runs); err != nil || runs == nil {
			return nil, errors.New("GitHub runs response is not a valid run list")
		}
		var history []map[string]any
		nextCursor := state.Cursors[repository]
		if !options.seed {
			cursor := dashboardHistoryRange(state.Cursors[repository], cutoff, now)
			history, nextCursor, err = collectDashboardHistory(ctx, repository, cursor, options.count, gh)
			if err != nil {
				return nil, err
			}
		}
		seen := map[string]bool{}
		for _, run := range runs {
			seen[nativeScalar(run["databaseId"])] = true
		}
		for _, run := range history {
			if !seen[nativeScalar(run["databaseId"])] {
				runs = append(runs, run)
			}
		}
		richRuns := history
		if len(richRuns) == 0 {
			richRuns = runs
		}
		workflowIDs := dashboardObservedWorkflowIDs(workflows, richRuns)
		eligible := make([]map[string]any, 0, len(richRuns))
		for _, run := range richRuns {
			if !slices.Contains(workflowIDs, nativeScalar(run["workflowDatabaseId"])) {
				continue
			}
			created, createdErr := time.Parse(time.RFC3339, nativeText(run["createdAt"]))
			id, idErr := strconv.ParseInt(nativeScalar(run["databaseId"]), 10, 64)
			if createdErr != nil || idErr != nil || id < 1 || id == 1<<63-1 ||
				!dashboardRunID.MatchString(nativeScalar(run["databaseId"])) {
				return nil, errors.New("agentic run has an invalid creation time or identity")
			}
			if !created.Before(cutoff) && !created.After(now) {
				eligible = append(eligible, run)
			}
		}
		richRuns = eligible
		workflowIDs = dashboardObservedWorkflowIDs(workflows, richRuns)
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
		if len(workflowIDs) > 0 && !options.seed {
			limited := false
			start, end := now, cutoff
			before, after := int64(0), int64(1<<63-1)
			workflowCounts := map[string]int{}
			for _, run := range richRuns {
				created, _ := time.Parse(time.RFC3339, nativeText(run["createdAt"]))
				if created.Before(start) {
					start = created
				}
				if created.After(end) {
					end = created
				}
				id, _ := strconv.ParseInt(nativeScalar(run["databaseId"]), 10, 64)
				before, after = max(before, id), min(after, id)
				workflowCounts[nativeScalar(run["workflowDatabaseId"])]++
			}
			err = logs(ctx, dashboardLogRequest{
				Repository: repository, Directory: directory, Cache: shard,
				StartDate: start.Format(time.RFC3339), EndDate: end.Format(time.RFC3339),
				Count:        options.count,
				ManagedCache: managedCache, WorkflowIDs: workflowIDs,
				WorkflowCounts: workflowCounts, BeforeRunID: before + 1, AfterRunID: max(0, after-1),
				MaxAPIUsed: budget.ceiling, CheckQuota: budget.check, Limited: &limited,
			})
			if err != nil {
				if errors.Is(err, errDashboardQuota) {
					return nil, err
				}
				return nil, fmt.Errorf("gh-aw log collection failed for %s; verify authentication and API quota; previous snapshot preserved", repository)
			}
			if limited {
				state.Message = "Artifact acquisition reached its time or quota ceiling; history discovery continues with partial artifact evidence."
			}
		}
		cached, err := readDashboardLogCache(shard, repository)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		runs = mergeDashboardCachedRuns(runs, workflows, cached, options.count)
		evidence = append(evidence, dashboardRepositoryEvidence{
			Repository: repository, Workflows: workflows, Runs: runs, Enriched: cached.Enriched,
			Directory: directory, SafeOutputs: cached.SafeOutputs,
			GithubID: resolved.ID,
		})
		if !options.seed {
			state.Cursors[repository] = nextCursor
			state.NextRepo = (repositoryIndex + 1) % len(repositories)
		}
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
	metadata := dashboardObservationMetadata(repositories, options, now, counts)
	batch["transactions"] = []map[string]any{metadata}
	quota, err := budget.quota(ctx)
	if err != nil {
		return nil, err
	}
	state.Status = "waiting"
	state.NextAt = time.Now().UTC().Add(options.refresh).Format(time.RFC3339)
	if quota.Remaining <= state.Reserve {
		state.Status, state.Message = "paused", dashboardPauseMessage(errDashboardQuota)
		state.NextAt = state.ResetAt
	}
	state.summarize(repositories, cutoff, time.Now().UTC(), options.window)
	checkpoint, err := dashboardIngestionRecord(*state)
	if err != nil {
		return nil, err
	}
	batch["transactions"] = append(batch["transactions"], checkpoint)
	if err := writeDashboardSnapshot(ctx, cache, batch, true); err != nil {
		return nil, err
	}
	return metadata, nil
}

func dashboardObservationMetadata(repositories []string, options dashboardOptions, now time.Time, counts map[string]int) map[string]any {
	return map[string]any{
		"id": "local-dashboard", "kind": "local-dashboard", "createdAt": now.Format(time.RFC3339Nano),
		"repositories": repositories, "windowStart": now.Add(-options.window).Format(time.RFC3339Nano),
		"windowEnd": now.Format(time.RFC3339Nano), "windowHours": options.window.Hours(),
		"runLimitPerAcquisition": options.count, "historyPageSize": min(options.count, 100),
		"workflowLimitPerRepository": 1000,
		"availability":               "available", "completeness": "partial",
		"coverage": "Resumable bounded history discovery and available gh-aw artifacts; partial upstream and daily observations. A scanned interval does not establish complete artifacts, accepted outcomes, campaign policy or Operational Value.",
		"counts":   counts,
	}
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
				"githubId": strconv.FormatInt(workflow.ID, 10), "name": workflow.Name, "path": path, "state": workflow.State, "observedAt": observed,
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
