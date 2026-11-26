package cli

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// Inventory observation uses the session's explicit acquisition scope, not
// policy targets. Configured-only identities use ordinary snapshot publication.
func refreshDashboardInventory(ctx context.Context, selected []string, cache string, options dashboardOptions, gh ghExecutor) error {
	repositories := slices.Clone(selected)
	configuration, _ := ctx.Value(dashboardConfigurationKey{}).(*dashboardConfiguration)
	if configuration != nil {
		for _, repository := range configuration.builtins {
			if !slices.Contains(repositories, repository) {
				repositories = append(repositories, repository)
			}
		}
	}
	lock := filepath.Join(cache, "collection.lock")
	if err := os.Mkdir(lock, 0o700); err != nil {
		return errors.New("dashboard collection is already locked")
	}
	defer func() { _ = os.Remove(lock) }()
	metadata, checkpoint, state, err := dashboardInventoryRefreshCheckpoint(ctx, cache, selected)
	if err != nil {
		return err
	}
	budget, err := newDashboardAcquisition(ctx, gh, &state)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	evidence := make([]dashboardRepositoryEvidence, 0, len(repositories))
	for _, repository := range repositories {
		identity, _, err := budget.exec(ctx, "repo", "view", "github.com/"+repository, "--json", "nameWithOwner,url,id")
		if err != nil {
			return err
		}
		var resolved struct {
			Name string `json:"nameWithOwner"`
			URL  string `json:"url"`
			ID   string `json:"id"`
		}
		if json.Unmarshal([]byte(identity), &resolved) != nil || !strings.EqualFold(resolved.Name, repository) ||
			!strings.EqualFold(resolved.URL, "https://github.com/"+repository) || resolved.ID == "" {
			return errors.New("inventory refresh did not resolve the exact repository")
		}
		inventory, _, err := budget.exec(ctx, "workflow", "list", "--repo", "github.com/"+repository,
			"--all", "--limit", "1000", "--json", "id,name,path,state")
		if err != nil {
			return err
		}
		var workflows []dashboardWorkflow
		if json.Unmarshal([]byte(inventory), &workflows) != nil || workflows == nil || len(workflows) >= 1000 {
			return errors.New("inventory refresh workflow listing is invalid or truncated")
		}
		agentic, err := collectDashboardAgenticWorkflows(ctx, repository, budget.exec)
		if err != nil {
			return err
		}
		source := dashboardRepositoryEvidence{Repository: repository, GithubID: resolved.ID,
			Workflows: workflows, AgenticWorkflows: agentic}
		if options.localRepository == repository {
			source.Campaigns, err = detectDashboardCampaigns(".")
			if err != nil {
				return err
			}
		}
		evidence = append(evidence, source)
	}
	batch, err := projectDashboardEvidence(evidence, now.Add(-options.window), now)
	if err != nil {
		return err
	}
	delete(batch, "runs")
	batch["transactions"] = []map[string]any{metadata, checkpoint}
	return writeDashboardSnapshot(ctx, cache, batch, true)
}

func dashboardInventoryRefreshCheckpoint(ctx context.Context, cache string, selected []string) (map[string]any, map[string]any, dashboardIngestion, error) {
	unlock := dashboardSnapshotGuard(ctx)
	defer unlock()
	metadata, err := readDashboardStatus(ctx, cache, selected)
	if err != nil {
		return nil, nil, dashboardIngestion{}, err
	}
	state, err := readDashboardIngestion(ctx, cache)
	if err != nil {
		return nil, nil, state, err
	}
	checkpoint, err := dashboardIngestionRecord(state)
	return metadata, checkpoint, state, err
}
