package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

func repositoryCommands(gh ghExecutor) *cobra.Command {
	repos := &cobra.Command{
		Use: "repos", Short: "Manage control-plane repository enrollment", Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return fmt.Errorf("repos requires add")
		},
	}
	repos.AddCommand(&cobra.Command{
		Use:   "add OWNER/REPO",
		Short: "Enroll repositories by exact name or wildcard",
		Long:  "Enroll one repository or current accessible wildcard matches in review mode. Quote patterns such as 'owner/service-*'. Wildcards resolve to exact names (at most 100); future matches are not enrolled. Review and commit policy before activation.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return nativeRepositoryAdd(cmd, args[0], gh)
		},
	})
	return repos
}

func addSourceIsCampaign(input string) (bool, error) {
	if info, err := os.Stat(input); err == nil {
		return info.IsDir(), nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	source := input
	if index := strings.LastIndex(source, "@"); index >= 0 {
		source = source[:index]
		if index == len(input)-1 {
			return false, fmt.Errorf("add source must include a nonempty revision after @")
		}
	}
	if slugPattern.MatchString(source) || strings.HasSuffix(source, ".md") || strings.HasPrefix(input, "https://") {
		return false, nil
	}
	// Keep the published qualified shorthand for the catalog's standalone report.
	if source == catalogRepository+"/daily-repo-status" || source == catalogRepository+"/repo-status" {
		return false, nil
	}
	if filepath.IsAbs(source) || strings.HasPrefix(source, ".") {
		return false, fmt.Errorf("add campaign directory does not exist: %s", input)
	}
	return true, nil
}

func campaignSlug(spec string) (string, error) {
	withoutRef := spec
	if index := strings.LastIndex(spec, "@"); index > strings.Index(spec, "/") {
		withoutRef = spec[:index]
	}
	parts := strings.Split(strings.TrimRight(withoutRef, "/"), "/")
	slug := parts[len(parts)-1]
	if !slugPattern.MatchString(slug) {
		return "", fmt.Errorf("unable to determine campaign name from %s", spec)
	}
	return slug, nil
}

func installCampaign(ctx context.Context, gh ghExecutor, spec string, options []string) (map[string]any, error) {
	campaign, err := campaignSlug(spec)
	if err != nil {
		return nil, err
	}
	if campaign == "gh-cao" {
		return nil, fmt.Errorf("add cannot install the CAO root package; use gh cao setup")
	}
	var sourceRoot string
	if info, err := os.Stat(spec); err == nil && info.IsDir() {
		source, err := filepath.Abs(spec)
		if err != nil {
			return nil, err
		}
		source, err = filepath.EvalSymlinks(source)
		if err != nil {
			return nil, err
		}
		sourceRoot = filepath.Dir(source)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	args := append([]string{"aw", "add", spec}, options...)
	if _, stderr, err := gh(ctx, args...); err != nil {
		return nil, commandError("gh aw add failed", stderr, err)
	}
	if err := pinAutoUpgradeActions(); err != nil {
		return nil, err
	}
	declaration, err := materializeCampaign(ctx, gh, campaign, sourceRoot)
	if err != nil {
		return nil, fmt.Errorf("unable to materialize CAO %s: %w", campaign, err)
	}
	policy, err := readPolicy()
	create := errors.Is(err, os.ErrNotExist)
	if create {
		policy, _, err = newPolicy(ctx, gh)
	}
	if err != nil {
		return nil, err
	}
	mergeCampaignDeclaration(policy, declaration)
	if err := writePolicy(policy, create); err != nil {
		return nil, err
	}
	return map[string]any{
		"command": "add", "campaign": campaign, "orchestrator": declaration.Orchestrator,
		"workers": declaration.workerNames(), "policy": policyPath,
	}, nil
}

func mergeCampaignDeclaration(policy map[string]any, declaration *campaignDeclaration) {
	control, _ := policy["control-plane"].(map[string]any)
	if control == nil {
		control = map[string]any{}
		policy["control-plane"] = control
	}
	campaigns, _ := control["campaigns"].(map[string]any)
	if campaigns == nil {
		campaigns = map[string]any{}
		control["campaigns"] = campaigns
	}
	campaign, _ := campaigns[declaration.Campaign].(map[string]any)
	if campaign == nil {
		campaign = map[string]any{}
		campaigns[declaration.Campaign] = campaign
	}
	existing, _ := campaign["workers"].(map[string]any)
	workers := map[string]any{}
	for _, worker := range declaration.Workers {
		value := map[string]any{"workflow": worker.Workflow}
		old, _ := existing[worker.Name].(map[string]any)
		if enabled, ok := old["enabled"].(bool); ok {
			value["enabled"] = enabled
		}
		if mode := old["max-mode"]; mode == "review" || mode == "live" {
			value["max-mode"] = mode
		}
		workers[worker.Name] = value
	}
	campaign["workers"] = workers
}
