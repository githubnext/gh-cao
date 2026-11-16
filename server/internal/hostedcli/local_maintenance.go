package hostedcli

import (
	"errors"
	"path"
	"regexp"
	"strings"
)

var maintenanceWorkflow = regexp.MustCompile(`^(?:\.github/workflows/)?[A-Za-z0-9][A-Za-z0-9_.-]*\.md$`)
var enrollmentPattern = regexp.MustCompile(`^[A-Za-z0-9_.*-]+/[A-Za-z0-9_.*-]+$`)

// PlanLocalMaintenance is for the private, queue-authorized native adapter.
// It deliberately does not extend PlanCommand or hosted credential grants.
func PlanLocalMaintenance(command string) (Plan, error) {
	tokens, err := Tokenize(command)
	if err != nil {
		return Plan{}, err
	}
	if hasPrefix(tokens, "gh", "cao", "repos", "add") {
		return planRepositoryEnrollment(tokens[4:])
	}
	if hasPrefix(tokens, "gh", "aw", "upgrade") {
		return planUpgrade(tokens[3:])
	}
	if !hasPrefix(tokens, "gh", "aw", "update") && !hasPrefix(tokens, "gh", "aw", "disable") {
		return Plan{}, ErrNotHosted
	}
	operation, args := tokens[2], tokens[3:]
	target := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "--") {
		target, args = args[0], args[1:]
		if strings.Contains(target, "..") || !maintenanceWorkflow.MatchString(target) && (operation != "update" || !validGitHubSourceURL(target)) {
			return Plan{}, errors.New("maintenance requires an exact Markdown workflow or update package source")
		}
	}
	if operation == "update" {
		values, err := publishingOptions(args,
			map[string]bool{"--create-pull-request": true, "--auto-merge": true},
			map[string]bool{"--repo": true, "--pr-message": true})
		if err != nil {
			return Plan{}, err
		}
		if !ValidRepository(values["--repo"]) || strings.Contains(values["--repo"], "..") {
			return Plan{}, errors.New("maintenance requires an exact repository")
		}
		normalized := []string{"update"}
		if target != "" {
			normalized = append(normalized, target)
		}
		return Plan{Kind: KindGhAwUpdate, Program: "gh-aw", Args: append(normalized, args...), Repository: values["--repo"]}, nil
	}
	if len(args) != 2 || args[0] != "--repo" || !ValidRepository(args[1]) || strings.Contains(args[1], "..") {
		return Plan{}, errors.New("disable requires only one exact repository")
	}
	normalized := []string{"disable"}
	if target != "" {
		normalized = append(normalized, strings.TrimSuffix(path.Base(target), ".md"))
	}
	return Plan{Program: "gh-aw", Args: append(normalized, args...), Repository: args[1]}, nil
}

// planRepositoryEnrollment stages review-mode enrollment in the native CLI's
// local policy checkout. The new target is outside the selected maintenance
// scope by definition, so Repository is set only for exact names.
func planRepositoryEnrollment(args []string) (Plan, error) {
	if len(args) != 1 || len(args[0]) > 200 || strings.Contains(args[0], "..") || !enrollmentPattern.MatchString(args[0]) {
		return Plan{}, errors.New("repository enrollment requires one OWNER/REPO with optional * wildcards")
	}
	plan := Plan{Kind: KindCaoReposAdd, Program: "gh-cao", Args: []string{"repos", "add", args[0]}}
	if ValidRepository(args[0]) {
		plan.Repository = strings.ToLower(args[0])
	}
	return plan, nil
}
