package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

type campaignWorker struct {
	Name, Workflow string
}

type campaignDeclaration struct {
	Campaign, Orchestrator string
	Workers                []campaignWorker
	StandaloneWorkflows    []string
}

func (d *campaignDeclaration) workerNames() []string {
	names := make([]string, 0, len(d.Workers))
	for _, worker := range d.Workers {
		names = append(names, worker.Name)
	}
	return names
}

func campaignWorkflows(campaign string) ([]string, error) {
	declaration, err := readCampaignDeclaration(campaign, campaign)
	if err != nil {
		return nil, err
	}
	result := []string{declaration.Orchestrator}
	for _, worker := range declaration.Workers {
		result = append(result, worker.Workflow)
	}
	result = append(result, declaration.StandaloneWorkflows...)
	return result, nil
}

func readCampaignDeclaration(directory, campaign string) (result *campaignDeclaration, err error) {
	path := filepath.Join(directory, "cao.json")
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("campaign %s is not installed or does not declare CAO workflows", campaign)
	}
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	var declaration struct {
		Campaign            string          `json:"campaign"`
		Orchestrator        string          `json:"orchestrator"`
		Workers             json.RawMessage `json:"workers"`
		StandaloneWorkflows []string        `json:"standalone-workflows"`
	}
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&declaration); err != nil {
		return nil, fmt.Errorf("%s contains an invalid CAO declaration: %w", path, err)
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%s must contain one JSON object", path)
	}
	if declaration.Campaign != campaign {
		return nil, fmt.Errorf("installed CAO declaration names campaign %s, expected %s", declaration.Campaign, campaign)
	}
	if !slugPattern.MatchString(declaration.Orchestrator) {
		return nil, fmt.Errorf("%s orchestrator must be a kebab-case workflow identifier", path)
	}
	workers := json.NewDecoder(strings.NewReader(string(declaration.Workers)))
	token, err := workers.Token()
	if err != nil || token != json.Delim('{') {
		return nil, fmt.Errorf("%s workers must be a non-empty object", path)
	}
	result = &campaignDeclaration{Campaign: campaign, Orchestrator: declaration.Orchestrator}
	seen := map[string]bool{}
	workerNames := map[string]bool{}
	for workers.More() {
		key, err := workers.Token()
		if err != nil {
			return nil, err
		}
		worker, ok := key.(string)
		if !ok || !slugPattern.MatchString(worker) {
			return nil, fmt.Errorf("%s worker must be a kebab-case identifier", path)
		}
		if workerNames[worker] {
			return nil, fmt.Errorf("%s workers must use unique identifiers", path)
		}
		workerNames[worker] = true
		var workflow string
		if err := workers.Decode(&workflow); err != nil || !slugPattern.MatchString(workflow) {
			return nil, fmt.Errorf("%s worker %s must name a kebab-case workflow", path, worker)
		}
		if seen[workflow] {
			return nil, fmt.Errorf("%s workers must name unique workflows", path)
		}
		seen[workflow] = true
		result.Workers = append(result.Workers, campaignWorker{Name: worker, Workflow: workflow})
	}
	if len(result.Workers) == 0 {
		return nil, fmt.Errorf("%s workers must be a non-empty object", path)
	}
	seen[declaration.Orchestrator] = true
	for _, workflow := range declaration.StandaloneWorkflows {
		if !slugPattern.MatchString(workflow) || seen[workflow] {
			return nil, fmt.Errorf("%s standalone-workflows must name unique kebab-case workflows distinct from the campaign workflows", path)
		}
		seen[workflow] = true
		result.StandaloneWorkflows = append(result.StandaloneWorkflows, workflow)
	}
	return result, nil
}

func checkLiveAuthentication(cmd *cobra.Command, gh ghExecutor) {
	run := func(args ...string) (string, error) {
		stdout, _, err := gh(cmd.Context(), args...)
		return strings.TrimSpace(stdout), err
	}
	repository, err := run("repo", "view", "--json", "nameWithOwner", "--jq", ".nameWithOwner")
	if err != nil || !repositoryPattern.MatchString(repository) {
		log.Print("Live authentication inspection unavailable")
		return
	}
	variables, err := run("api", "/repos/"+repository+"/actions/variables?per_page=100", "--paginate", "--jq", ".variables[].name")
	if err != nil {
		log.Print("Live authentication variable inspection unavailable")
		return
	}
	secrets, err := run("api", "/repos/"+repository+"/actions/secrets?per_page=100", "--paginate", "--jq", ".secrets[].name")
	if err != nil {
		log.Print("Live authentication secret-name inspection unavailable")
		return
	}
	has := func(list, value string) bool {
		for _, line := range strings.Split(list, "\n") {
			if line == value {
				return true
			}
		}
		return false
	}
	variable := func(name string) (string, error) {
		return run("api", "/repos/"+repository+"/actions/variables/"+name, "--jq", ".value")
	}
	mode := ""
	if has(variables, "GH_AW_GITHUB_AUTH_MODE") {
		mode, err = variable("GH_AW_GITHUB_AUTH_MODE")
		if err != nil {
			log.Print("Live authentication mode inspection unavailable")
			return
		}
	}
	configured := false
	switch mode {
	case "app":
		if has(variables, "GH_AW_GITHUB_WRITE_APP_ID") && has(secrets, "GH_AW_GITHUB_WRITE_APP_PRIVATE_KEY") {
			value, err := variable("GH_AW_GITHUB_WRITE_APP_ID")
			if err != nil {
				log.Print("Live authentication App inspection unavailable")
				return
			}
			configured = value != ""
		}
	case "pat":
		if has(variables, "GH_AW_GITHUB_WRITE_PAT_REPOSITORIES") {
			value, err := variable("GH_AW_GITHUB_WRITE_PAT_REPOSITORIES")
			if err != nil {
				log.Print("Live authentication PAT-map inspection unavailable")
				return
			}
			var repositories map[string]string
			if err := json.Unmarshal([]byte(value), &repositories); err != nil {
				log.Print("Live authentication PAT-map metadata is invalid")
				return
			}
			for _, secret := range repositories {
				configured = configured || (strings.HasPrefix(secret, "GH_AW_GITHUB_WRITE_PAT_") && has(secrets, secret))
			}
		}
	}
	if !configured && os.Getenv("CI") == "true" {
		if _, err := fmt.Fprintf(cmd.ErrOrStderr(), "Live write authentication is not configured for %s; run gh cao setup to configure a GitHub App or PAT before posting live items.\n", repository); err != nil {
			log.Print("Unable to write live authentication warning")
		}
	}
}
