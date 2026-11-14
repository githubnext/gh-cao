package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// dashboardCampaign is one campaign declared by a checked-in <campaign>/cao.json.
type dashboardCampaign struct {
	Slug, Name, Description, Icon string
	// Roles maps lowercase workflow IDs to "orchestrator" or "worker".
	Roles map[string]string
}

var dashboardCampaignIcon = regexp.MustCompile(`^:([a-z0-9]+(?:-[a-z0-9]+)*):$`)

// detectDashboardCampaigns reads campaign declarations from the top-level
// directories of a checkout. Only declared orchestrators and workers receive
// campaign membership; declared standalone workflows stay standalone.
func detectDashboardCampaigns(root string) ([]dashboardCampaign, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	claimed := map[string]string{}
	var campaigns []dashboardCampaign
	for _, entry := range entries {
		if !entry.IsDir() || !slugPattern.MatchString(entry.Name()) {
			continue
		}
		directory := filepath.Join(root, entry.Name())
		if _, err := os.Stat(filepath.Join(directory, "cao.json")); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return nil, err
		}
		declaration, err := readCampaignDeclaration(directory, entry.Name())
		if err != nil {
			return nil, err
		}
		campaign := dashboardCampaign{Slug: declaration.Campaign, Name: declaration.Campaign,
			Roles: map[string]string{declaration.Orchestrator: "orchestrator"}}
		for _, worker := range declaration.Workers {
			campaign.Roles[worker.Workflow] = "worker"
		}
		for workflow := range campaign.Roles {
			if owner, exists := claimed[workflow]; exists {
				return nil, fmt.Errorf("workflow %s is declared by campaigns %s and %s", workflow, owner, campaign.Slug)
			}
			claimed[workflow] = campaign.Slug
		}
		if err := readDashboardCampaignManifest(filepath.Join(directory, "aw.yml"), &campaign); err != nil {
			return nil, err
		}
		campaigns = append(campaigns, campaign)
	}
	slices.SortFunc(campaigns, func(a, b dashboardCampaign) int { return strings.Compare(a.Slug, b.Slug) })
	return campaigns, nil
}

// readDashboardCampaignManifest copies optional display metadata from aw.yml.
func readDashboardCampaignManifest(path string, campaign *dashboardCampaign) error {
	content, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var manifest struct {
		Name        string `yaml:"name"`
		Description string `yaml:"description"`
		Icon        string `yaml:"icon"`
	}
	if err := yaml.Unmarshal(content, &manifest); err != nil {
		return fmt.Errorf("%s is not a valid campaign manifest: %w", path, err)
	}
	if name := strings.TrimSpace(manifest.Name); name != "" {
		campaign.Name = name
	}
	campaign.Description = strings.TrimSpace(manifest.Description)
	if icon := dashboardCampaignIcon.FindStringSubmatch(strings.TrimSpace(manifest.Icon)); icon != nil {
		campaign.Icon = icon[1]
	}
	return nil
}

func dashboardCampaignID(slug string) string {
	return "campaign:dashboard-sources:" + dashboardCoordinate(slug)
}
