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
	Repository string
	Workflows  []dashboardWorkflow
	// AgenticWorkflows holds lowercase workflow IDs reported by `gh aw list`.
	AgenticWorkflows map[string]bool
	// Campaigns are declared by the local checkout of this repository only.
	Campaigns   []dashboardCampaign
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
	WorkflowPaths                           map[string]string
	WorkflowCounts                          map[string]int
	BeforeRunID, AfterRunID                 int64
	MaxAPIUsed                              int
	CheckQuota                              func(context.Context, int) error
	Limited                                 *bool
	OnRuns                                  func(context.Context, []map[string]any) error
	OnRunReceived                           func(context.Context, map[string]any) error
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
	progress := newDashboardProgress(cache, now)
	if err := progress.publish("discovering", ""); err != nil {
		return nil, err
	}
	defer func() {
		phase := "waiting"
		if state.Status == "paused" {
			phase = "paused"
		}
		if err != nil {
			phase = "failed"
			if errors.Is(err, errDashboardQuota) {
				phase = "paused"
			}
		}
		err = errors.Join(err, progress.publish(phase, progress.value.Repository))
	}()
	options.progress = progress
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
				state.NextAt = time.Now().UTC().Add(dashboardNextRefresh(state, options.refresh, time.Now())).Format(time.RFC3339)
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
		if err := options.progress.publish("discovering", repository); err != nil {
			return nil, err
		}
		if err := writeDashboardIngestion(ctx, cache, *state); err != nil {
			return nil, err
		}
		if options.onCollecting != nil {
			options.onCollecting()
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
		// gh aw rejects host-qualified targets; the helper refuses non-github.com GH_HOST.
		agentic, err := collectDashboardAgenticWorkflows(ctx, repository, gh)
		if err != nil {
			if errors.Is(err, errDashboardQuota) {
				return nil, err
			}
			return nil, fmt.Errorf("cannot read agentic workflow list for %s; previous snapshot preserved: %w", repository, err)
		}
		var campaigns []dashboardCampaign
		if options.localRepository == repository {
			// Remote repositories expose no checkout, so their membership stays unknown.
			campaigns, err = detectDashboardCampaigns(".")
			if err != nil {
				return nil, fmt.Errorf("cannot read campaign declarations for %s; previous snapshot preserved: %w", repository, err)
			}
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
		runs = slices.DeleteFunc(runs, dashboardRunNotExecuted)
		cursor := dashboardHistoryRange(state.Cursors[repository], cutoff, now)
		history, nextCursor, err := collectDashboardHistory(ctx, repository, cursor, options.count, gh)
		if err != nil {
			return nil, err
		}
		history = slices.DeleteFunc(history, dashboardRunNotExecuted)
		seen := map[string]bool{}
		for _, run := range runs {
			seen[nativeScalar(run["databaseId"])] = true
		}
		for _, run := range history {
			if !seen[nativeScalar(run["databaseId"])] {
				runs = append(runs, run)
			}
		}
		eligible := make([]map[string]any, 0, len(runs))
		for _, run := range runs {
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
		eligible = slices.DeleteFunc(eligible, func(run map[string]any) bool {
			return !slices.Contains(dashboardObservedWorkflowIDs(workflows, []map[string]any{run}), nativeScalar(run["workflowDatabaseId"]))
		})
		shard := filepath.Join(cache, dashboardCoordinate(repository)+".jsonl")
		if info, err := os.Lstat(shard); err == nil && !info.Mode().IsRegular() {
			return nil, errors.New("gh-aw cache must be a regular JSONL file")
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		prior, err := readDashboardLogCache(shard, repository)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		backlog, err := readDashboardArtifactBacklog(cache, repository)
		if err != nil {
			return nil, err
		}
		// Artifacts, and the issue safe outputs inside them, follow discovery
		// through a persisted backlog so runs left by a time or quota ceiling
		// are retried instead of being skipped once the history cursor moves.
		queue := dashboardArtifactQueue(backlog, eligible, prior.Enriched, cutoff)
		richRuns := queue[:min(len(queue), options.count)]
		workflowIDs := dashboardObservedWorkflowIDs(workflows, richRuns)
		directory := filepath.Join(cache, "logs", dashboardCoordinate(repository))
		managedCache := true
		if options.logs != "" {
			managedCache = false
			directory, err = filepath.Abs(options.logs)
			if err != nil {
				return nil, err
			}
		} else if len(repositories) == 1 && options.localRepository == repository {
			// Reuse the operator's ordinary artifact cache only for its own repository.
			if dashboardLocalLogsPresent() {
				managedCache = false
				directory, err = filepath.Abs(dashboardLocalLogs)
				if err != nil {
					return nil, err
				}
			}
		}
		if err := os.MkdirAll(directory, 0o700); err != nil {
			return nil, err
		}
		streamed := map[string]map[string]any{}
		attempted := map[string]bool{}
		limited := false
		if len(workflowIDs) > 0 {
			start, end := now, cutoff
			before, after := int64(0), int64(1<<63-1)
			workflowCounts := map[string]int{}
			workflowPaths := map[string]string{}
			for _, workflow := range workflows {
				workflowPaths[strconv.FormatInt(workflow.ID, 10)] = workflow.Path
			}
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
			selectedRun := func(record map[string]any) (map[string]any, error) {
				id := nativeScalar(record["run_id"])
				rawIndex := slices.IndexFunc(richRuns, func(raw map[string]any) bool {
					return nativeScalar(raw["databaseId"]) == id
				})
				if !strings.EqualFold(nativeText(record["repository"]), repository) {
					return nil, errors.New("gh-aw streamed a run outside the selected observations")
				}
				// Runs in the requested ID span outside this batch already have
				// artifacts or are not admitted; they are not new evidence.
				if rawIndex < 0 {
					runID, err := strconv.ParseInt(id, 10, 64)
					known := slices.ContainsFunc(workflows, func(workflow dashboardWorkflow) bool {
						return nativeText(record["workflow_path"]) == workflow.Path
					})
					if err != nil || runID < after || runID > before || !known {
						return nil, errors.New("gh-aw streamed a run outside the selected observations")
					}
					return nil, nil
				}
				raw := richRuns[rawIndex]
				workflowIndex := slices.IndexFunc(workflows, func(workflow dashboardWorkflow) bool {
					return strconv.FormatInt(workflow.ID, 10) == nativeScalar(raw["workflowDatabaseId"])
				})
				if workflowIndex < 0 || nativeText(record["workflow_path"]) != workflows[workflowIndex].Path {
					return nil, errors.New("gh-aw streamed a mismatched workflow identity")
				}
				if nativeScalar(record["run_attempt"]) != nativeScalar(raw["attempt"]) {
					return nil, nil
				}
				return raw, nil
			}
			onRuns := func(streamCtx context.Context, records []map[string]any) error {
				source := dashboardRepositoryEvidence{
					Repository: repository, Workflows: workflows, AgenticWorkflows: agentic, Campaigns: campaigns, Directory: directory,
					GithubID: resolved.ID, Enriched: map[string]map[string]any{},
				}
				for _, record := range records {
					id := nativeScalar(record["run_id"])
					attempted[id] = true
					raw, err := selectedRun(record)
					if err != nil {
						return err
					}
					// Fresh Actions attempts remain authoritative, including during reruns.
					if raw == nil {
						continue
					}
					streamed[id], source.Enriched[id] = record, record
					source.Runs = append(source.Runs, raw)
				}
				if len(source.Runs) == 0 {
					return nil
				}
				if err := options.progress.publish("ingesting", repository); err != nil {
					return err
				}
				if err := publishDashboardEvidence(streamCtx, repositories, cache, options, []dashboardRepositoryEvidence{source}, *state, now); err != nil {
					return err
				}
				return options.progress.publish("receiving", repository)
			}
			if err := options.progress.publish("receiving", repository); err != nil {
				return nil, err
			}
			// gh-aw takes an absolute used-quota ceiling; refresh it from the remaining pass budget.
			if err := budget.check(ctx, 1); err != nil && !errors.Is(err, errDashboardQuota) {
				return nil, err
			}
			err = logs(ctx, dashboardLogRequest{
				Repository: repository, Directory: directory, Cache: shard,
				StartDate: start.Format(time.RFC3339), EndDate: end.Format(time.RFC3339),
				Count:        options.count,
				ManagedCache: managedCache, WorkflowIDs: workflowIDs,
				WorkflowCounts: workflowCounts, WorkflowPaths: workflowPaths, BeforeRunID: before + 1, AfterRunID: max(0, after-1),
				MaxAPIUsed: budget.ceiling, CheckQuota: budget.check, Limited: &limited,
				OnRuns: onRuns,
				OnRunReceived: func(_ context.Context, record map[string]any) error {
					raw, err := selectedRun(record)
					if err != nil || raw == nil {
						return err
					}
					return options.progress.receive(repository, record)
				},
			})
			if err != nil {
				if errors.Is(err, errDashboardQuota) {
					return nil, err
				}
				return nil, fmt.Errorf("gh-aw log collection failed for %s; last published evidence and history checkpoint retained: %w", repository, err)
			}
			if limited {
				state.Message = "Artifact acquisition reached its time or quota ceiling; history discovery continues with partial artifact evidence."
			}
		}
		// A completed, unlimited request has yielded every artifact this batch
		// can provide; only a time or quota ceiling leaves runs for retry.
		remaining := slices.DeleteFunc(slices.Clone(queue), func(run map[string]any) bool {
			id := nativeScalar(run["databaseId"])
			return attempted[id] || !limited && slices.ContainsFunc(richRuns, func(selected map[string]any) bool {
				return nativeScalar(selected["databaseId"]) == id
			})
		})
		if err := writeDashboardArtifactBacklog(cache, repository, remaining); err != nil {
			return nil, err
		}
		cached, err := readDashboardLogCache(shard, repository)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		if cached.Enriched == nil {
			cached.Enriched = map[string]map[string]any{}
		}
		for id, record := range streamed {
			previous := cached.Enriched[id]
			if nativeScalar(previous["run_attempt"]) != nativeScalar(record["run_attempt"]) {
				previous = nil
			}
			merged := maps.Clone(previous)
			if merged == nil {
				merged = map[string]any{}
			}
			maps.Copy(merged, record)
			cached.Enriched[id] = merged
		}
		runs = slices.DeleteFunc(mergeDashboardCachedRuns(runs, workflows, cached, options.count), dashboardRunNotExecuted)
		evidence = append(evidence, dashboardRepositoryEvidence{
			Repository: repository, Workflows: workflows, AgenticWorkflows: agentic, Campaigns: campaigns, Runs: runs, Enriched: cached.Enriched,
			Directory: directory, SafeOutputs: cached.SafeOutputs,
			GithubID: resolved.ID,
		})
		state.Cursors[repository] = nextCursor
		state.NextRepo = (repositoryIndex + 1) % len(repositories)
	}
	normalizationStart := time.Now()
	if err := options.progress.publish("ingesting", state.Repository); err != nil {
		return nil, err
	}
	batch, err := projectDashboardEvidence(evidence, cutoff, now)
	if err != nil {
		return nil, err
	}
	// Cached and non-streamed runs reach the archive only through the merged pass.
	if options.onSessionArchiveRuns != nil {
		for _, source := range evidence {
			options.onSessionArchiveRuns(source, batch["runs"])
		}
	}
	batch, err = hydrateDashboardEvidence(ctx, evidence, batch, now)
	options.timings.record("normalization", normalizationStart)
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
	state.Remaining = &quota.Remaining
	if quota.Remaining <= state.Reserve {
		state.Status, state.Message = "paused", dashboardPauseMessage(errDashboardQuota)
	}
	state.summarize(repositories, cutoff, time.Now().UTC(), options.window)
	state.NextAt = time.Now().UTC().Add(dashboardNextRefresh(*state, options.refresh, time.Now())).Format(time.RFC3339)
	if state.Status == "paused" {
		state.NextAt = state.ResetAt
	}
	checkpoint, err := dashboardIngestionRecord(*state)
	if err != nil {
		return nil, err
	}
	batch["transactions"] = append(batch["transactions"], checkpoint)
	if err := options.publishSnapshot(ctx, cache, batch); err != nil {
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

// dashboardRunNotExecuted reports runs whose jobs never ran: skipped
// activations and runs held for approval. They carry no agent evidence and
// would otherwise crowd executed runs out of each bounded page.
func dashboardRunNotExecuted(run map[string]any) bool {
	switch strings.ToLower(nativeText(run["conclusion"])) {
	case "skipped", "action_required":
		return true
	}
	switch strings.ToLower(nativeText(run["status"])) {
	case "waiting", "action_required":
		return true
	}
	return false
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

// collectDashboardAgenticWorkflows asks gh-aw which workflows in the repository
// are agentic workflows; the result carries IDs only, never source provenance.
func collectDashboardAgenticWorkflows(ctx context.Context, repository string, gh ghExecutor) (map[string]bool, error) {
	// gh aw list accepts only owner/repo, so an ambient GH_HOST would silently
	// change the queried host; fail closed instead of widening scope.
	if host := strings.TrimSpace(os.Getenv("GH_HOST")); host != "" && !strings.EqualFold(host, "github.com") {
		return nil, errors.New("gh aw list cannot be pinned to github.com while GH_HOST names another host")
	}
	raw, _, err := gh(ctx, "aw", "list", "--repo", repository, "--json")
	if err != nil {
		return nil, err
	}
	var listed []struct {
		Workflow string `json:"workflow"`
	}
	if err := json.Unmarshal([]byte(raw), &listed); err != nil || listed == nil {
		return nil, errors.New("gh aw list did not return a valid workflow list")
	}
	result := make(map[string]bool, len(listed))
	for _, entry := range listed {
		id := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(entry.Workflow), ".md"))
		if id == "" || strings.ContainsAny(id, `/\`) || strings.Contains(id, "..") {
			return nil, errors.New("gh aw list returned an invalid workflow identity")
		}
		result[id] = true
	}
	return result, nil
}

func projectDashboardEvidence(evidence []dashboardRepositoryEvidence, cutoff, now time.Time) (map[string][]map[string]any, error) {
	batch := map[string][]map[string]any{"campaigns": {}, "repositories": {}, "workflows": {}, "runs": {}}
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
		membership := map[string]dashboardCampaign{}
		for _, campaign := range source.Campaigns {
			record := map[string]any{
				"id": dashboardCampaignID(campaign.Slug), "slug": campaign.Slug, "name": campaign.Name,
				"workerCount": len(campaign.Roles) - 1, "observedAt": observed,
				"provenance": map[string]any{"source": "local-dashboard", "sourceId": campaign.Slug + "/cao.json", "observedAt": observed},
			}
			if campaign.Description != "" {
				record["description"] = campaign.Description
			}
			if campaign.Icon != "" {
				record["icon"] = campaign.Icon
			}
			batch["campaigns"] = append(batch["campaigns"], record)
			for workflow := range campaign.Roles {
				membership[workflow] = campaign
			}
		}
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
			record := map[string]any{
				"id": "workflow:" + dashboardCoordinate(source.Repository+":"+path), "repositoryId": repositoryID,
				"githubId": strconv.FormatInt(workflow.ID, 10), "name": workflow.Name, "path": path, "state": workflow.State, "observedAt": observed,
				"organizationLink": "https://github.com/" + parts[0],
				"repositoryLink":   "https://github.com/" + source.Repository,
				"workflowLink":     "https://github.com/" + source.Repository + "/actions/workflows/" + filepath.Base(workflow.Path),
				"provenance":       map[string]any{"source": "local-dashboard", "sourceId": workflow.Path, "observedAt": observed},
			}
			// gh aw list exposes no source provenance, so sourceKind stays unset (unknown).
			id := strings.TrimSuffix(filepath.Base(path), ".md")
			if campaign, member := membership[id]; member {
				record["role"], record["campaignId"] = campaign.Roles[id], dashboardCampaignID(campaign.Slug)
			} else if source.AgenticWorkflows[id] {
				record["role"] = "standalone"
			}
			batch["workflows"] = append(batch["workflows"], record)
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
