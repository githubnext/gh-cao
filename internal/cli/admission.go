package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"unicode"

	"github.com/spf13/cobra"
)

const (
	admissionURLVariable      = "GH_AW_DEFAULT_ADMISSION_URL"
	admissionAudienceVariable = "GH_AW_DEFAULT_ADMISSION_AUDIENCE"
	admissionOutageVariable   = "GH_AW_DEFAULT_ADMISSION_ON_OUTAGE"
)

var admissionVariables = []string{admissionURLVariable, admissionAudienceVariable, admissionOutageVariable}

type admissionSettings struct {
	URL      string `json:"url"`
	Audience string `json:"audience"`
	OnOutage string `json:"onOutage"`
}

type admissionCLIReport struct {
	Command    string            `json:"command"`
	Repository string            `json:"repository"`
	Scope      string            `json:"scope"`
	Applied    bool              `json:"applied"`
	Enabled    bool              `json:"enabled"`
	Settings   admissionSettings `json:"settings"`
	Sources    map[string]string `json:"sources,omitempty"`
	Warnings   []string          `json:"warnings"`
}

func admissionCommands(gh ghExecutor) *cobra.Command {
	group := &cobra.Command{
		Use: "admission", Short: "Inspect or disable the cooperative gh-aw admission gate",
		Long: `Inspect or disable gh-aw admission variables for an explicit scope.

Select --repo OWNER/REPO, --all-repos from local CAO policy, repeatable --org,
or --enterprise to provision organization defaults across its organizations.
Use gh cao setup admission to configure the gate.
Disable applies changes unless --dry-run is supplied.
Organization provisioning uses visibility all (including future repositories);
repository variables take precedence and are not removed by organization setup.

The HTTPS authority must already be deployed with matching audience, reviewed
enrollment, and trusted fresh accounting. These commands do not deploy a server,
enroll a repository, mint credentials, change CAO rollout policy, or compile
workflows. Existing workflows need a compiler with admission support.
Only GitHub.com is supported by the current server identity profile.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	for _, operation := range []string{"status", "disable"} {
		group.AddCommand(admissionOperationCommand(gh, operation))
	}
	legacySetup := setupAdmissionCommand(gh)
	legacySetup.Use = "setup"
	legacySetup.Hidden = true
	legacySetup.Long = "Deprecated: use gh cao setup admission instead.\n\n" + legacySetup.Long
	legacySetup.PreRunE = func(cmd *cobra.Command, _ []string) error {
		_, err := fmt.Fprintln(cmd.ErrOrStderr(), "gh cao admission setup is deprecated; use gh cao setup admission instead.")
		return err
	}
	group.AddCommand(legacySetup)
	return group
}

func admissionOperationCommand(gh ghExecutor, operation string) *cobra.Command {
	commandName := "admission " + operation
	if operation == "setup" {
		commandName = "setup admission"
	}
	var repo, endpoint, audience, outage string
	var orgs []string
	var enterprise string
	var allRepos bool
	var dryRun, jsonOutput bool
	cmd := &cobra.Command{
		Use: operation, Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			execute := func(args ...string) (string, error) {
				ctx, cancel := context.WithTimeout(cmd.Context(), doctorTimeout)
				defer cancel()
				out, _, err := gh(ctx, append([]string{"api", "--hostname", "github.com"}, args...)...)
				if err != nil {
					if cmd.Context().Err() != nil {
						return "", cmd.Context().Err()
					}
					return "", errors.New("GitHub admission variable operation failed; verify repository access and Actions variable permissions")
				}
				return out, nil
			}
			if operation == "setup" {
				if !cmd.Flags().Changed("url") {
					defaults, err := configuredAdmissionSettings(execute, repo)
					if err != nil {
						return err
					}
					endpoint = defaults.URL
					if !cmd.Flags().Changed("audience") {
						audience = defaults.Audience
					}
				}
				if audience == "" {
					audience = endpoint
				}
				if err := validateAdmissionSettings(admissionSettings{endpoint, audience, outage}); err != nil {
					return err
				}
			}
			targets, err := resolveAdmissionTargets(cmd, execute, repo, orgs, enterprise, allRepos)
			if err != nil {
				return err
			}
			reports := make([]admissionCLIReport, 0, len(targets))
			// Finish all reads before the first mutation, including fleet preflight.
			currentByTarget := make([]map[string]string, len(targets))
			for index, target := range targets {
				currentByTarget[index], err = readAdmissionVariables(execute, target.base())
				if err != nil {
					return fmt.Errorf("admission preflight failed for %s; no changes applied: %w", target.name, err)
				}
			}
			for index, target := range targets {
				repo := target.name
				base := target.base()
				current := currentByTarget[index]
				targetExecute := execute
				if target.kind == "organization" {
					targetExecute = func(args ...string) (string, error) {
						return execute(append(args, "--raw-field", "visibility=all")...)
					}
				}
				report := admissionCLIReport{Command: commandName, Repository: repo,
					Scope:    target.kind,
					Warnings: []string{"This cooperative gate does not grant CAO rollout authority or enforce participation."}}
				if target.kind == "organization" {
					warning := "Organization provisioning uses visibility all, including future repositories. Repository overrides still take precedence; this is not mandatory enforcement."
					if operation == "status" {
						warning = "This reports organization defaults, not repository-effective state or visibility coverage. Selected/private visibility and repository overrides may change which repositories use these defaults."
					}
					report.Warnings = append(report.Warnings, warning)
				}
				switch operation {
				case "status":
					inherited := map[string]string{}
					var err error
					if target.kind == "repository" {
						inherited, err = readAdmissionVariables(execute, "repos/"+repo+"/actions/organization-variables")
					}
					if err != nil {
						return errors.New("cannot resolve inherited admission variables; no effective configuration reported")
					}
					report.Settings, report.Sources = effectiveAdmissionSettings(current, inherited)
					if target.kind == "organization" {
						report.Settings, report.Sources = effectiveAdmissionSettings(nil, current)
					}
					report.Enabled = report.Settings.URL != ""
					if report.Enabled {
						if err := validateAdmissionSettings(report.Settings); err != nil {
							report.Warnings = append(report.Warnings, "Effective admission settings are invalid; participating workflows will fail activation.")
							if err := printAdmissionReports(cmd, append(reports, report), jsonOutput); err != nil {
								return err
							}
							return errors.New("effective admission configuration is invalid")
						}
					}
				case "setup":
					report.Settings = admissionSettings{endpoint, audience, outage}
					report.Enabled = true
					report.Warnings = append(report.Warnings, "Confirm authority enrollment, fresh accounting, matching audience and a feature-capable compiled workflow before rollout.")
					if !dryRun {
						// Publish URL last so an unconfigured gate cannot activate halfway
						// through setup. Never delete unrelated managed defaults.
						for _, setting := range []struct{ name, value string }{
							{admissionOutageVariable, outage}, {admissionAudienceVariable, audience}, {admissionURLVariable, endpoint},
						} {
							if err := setAdmissionVariable(targetExecute, base, current, setting.name, setting.value); err != nil {
								if outputErr := printAdmissionReports(cmd, reports, jsonOutput); outputErr != nil {
									return outputErr
								}
								return fmt.Errorf("setup admission stopped for %s while setting %s; earlier settings may have changed, inspect admission status before retrying: %w", repo, setting.name, err)
							}
						}
						report.Applied = true
					}
				case "disable":
					report.Warnings = append(report.Warnings, "Sets an empty URL instead of deleting the override; local/default credit checks remain in force. More specific overrides can still enable admission.")
					if !dryRun {
						// Deletion could reactivate an inherited organization endpoint.
						if err := setAdmissionVariable(targetExecute, base, current, admissionURLVariable, ""); err != nil {
							if outputErr := printAdmissionReports(cmd, reports, jsonOutput); outputErr != nil {
								return outputErr
							}
							return fmt.Errorf("admission disable stopped for %s; earlier targets may have changed: %w", repo, err)
						}
						report.Applied = true
					}
				}
				if report.Enabled && report.Settings.OnOutage == "ignore" {
					report.Warnings = append(report.Warnings, "Outage policy ignore permits execution without admission when the authority is unavailable.")
				}
				reports = append(reports, report)
			}
			return printAdmissionReports(cmd, reports, jsonOutput)
		},
	}
	switch operation {
	case "setup":
		cmd.Short = "Configure admission variables; --dry-run previews changes"
		cmd.Flags().StringVar(&endpoint, "url", "", "HTTPS /api/admission URL (default: configured target or current control repository endpoint)")
		cmd.Flags().StringVar(&audience, "audience", "", "Authority audience (default: endpoint URL)")
		cmd.Flags().StringVar(&outage, "on-outage", "block", "Outage policy: block or ignore (default block)")
	case "status":
		cmd.Short = "Read effective repository and inherited organization admission variables"
	case "disable":
		cmd.Short = "Disable admission defaults with an empty URL; --dry-run previews changes"
	}
	cmd.Flags().StringVarP(&repo, "repo", "R", "", "Exact GitHub.com repository OWNER/REPO")
	cmd.Flags().StringArrayVar(&orgs, "org", nil, "Configure all repositories through organization variables (repeatable)")
	cmd.Flags().StringVar(&enterprise, "enterprise", "", "Configure organization variables for every organization in this enterprise")
	cmd.Flags().BoolVar(&allRepos, "all-repos", false, "Configure every repository selected by local CAO policy scope")
	cmd.MarkFlagsMutuallyExclusive("repo", "org", "enterprise", "all-repos")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Write machine-readable results")
	if operation != "status" {
		cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Describe configuration without applying it")
	}
	return cmd
}

func validateAdmissionSettings(s admissionSettings) error {
	endpoint, err := url.Parse(s.URL)
	if err != nil || endpoint.Scheme != "https" || endpoint.Hostname() == "" ||
		endpoint.User != nil || endpoint.Path != "/api/admission" || endpoint.RawPath != "" ||
		endpoint.RawQuery != "" || endpoint.ForceQuery || endpoint.Fragment != "" ||
		strings.ContainsAny(s.URL, "# \t\r\n") || strings.Contains(s.URL, "${{") ||
		len(s.URL) > 2048 {
		return errors.New("--url must be an absolute HTTPS /api/admission URL without credentials, query, fragment, escapes, or expressions")
	}
	if s.Audience == "" || len(s.Audience) > 2048 || strings.ContainsFunc(s.Audience, unicode.IsControl) ||
		strings.Contains(s.Audience, "${{") {
		return errors.New("--audience must be a nonempty literal without controls or expressions")
	}
	if s.OnOutage != "block" && s.OnOutage != "ignore" {
		return errors.New("--on-outage must be block or ignore")
	}
	return nil
}

type admissionAPI func(...string) (string, error)

func readAdmissionVariables(execute admissionAPI, endpoint string) (map[string]string, error) {
	// Filter in gh so unrelated repository variables never enter output or errors.
	out, err := execute(endpoint+"?per_page=100", "--paginate", "--jq",
		`[.variables[] | select(.name == "GH_AW_DEFAULT_ADMISSION_URL" or .name == "GH_AW_DEFAULT_ADMISSION_AUDIENCE" or .name == "GH_AW_DEFAULT_ADMISSION_ON_OUTAGE") | {name, value}]`)
	if err != nil {
		return nil, err
	}
	result := map[string]string{}
	decoder := json.NewDecoder(strings.NewReader(out))
	pages := 0
	for {
		var values []struct{ Name, Value string }
		err := decoder.Decode(&values)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil || values == nil {
			return nil, errors.New("GitHub returned invalid admission variable metadata")
		}
		pages++
		for _, value := range values {
			if value.Name != admissionURLVariable && value.Name != admissionAudienceVariable && value.Name != admissionOutageVariable {
				return nil, errors.New("GitHub returned unexpected admission variable metadata")
			}
			if _, exists := result[value.Name]; exists {
				return nil, errors.New("GitHub returned duplicate admission variable metadata")
			}
			result[value.Name] = value.Value
		}
	}
	if pages == 0 {
		return nil, errors.New("GitHub returned no admission variable metadata")
	}
	return result, nil
}

func setAdmissionVariable(execute admissionAPI, base string, current map[string]string, name, value string) error {
	args := []string{base, "--method", "POST", "--raw-field", "name=" + name, "--raw-field", "value=" + value}
	if _, exists := current[name]; exists {
		args = []string{base + "/" + name, "--method", "PATCH", "--raw-field", "value=" + value}
	}
	_, err := execute(args...)
	return err
}

func effectiveAdmissionSettings(repo, org map[string]string) (admissionSettings, map[string]string) {
	values, sources := map[string]string{}, map[string]string{}
	for _, name := range admissionVariables {
		if value, ok := repo[name]; ok {
			values[name], sources[name] = value, "repository"
		} else if value, ok := org[name]; ok {
			values[name], sources[name] = value, "organization"
		} else {
			sources[name] = "default"
		}
	}
	settings := admissionSettings{values[admissionURLVariable], values[admissionAudienceVariable], values[admissionOutageVariable]}
	if settings.Audience == "" {
		settings.Audience = settings.URL
	}
	if settings.OnOutage == "" {
		settings.OnOutage = "ignore"
	}
	return settings, sources
}

func printAdmissionReport(cmd *cobra.Command, report admissionCLIReport, jsonOutput bool) error {
	if jsonOutput {
		return printJSON(cmd, report)
	}
	action := "Preview (no changes)"
	if report.Command == "admission status" {
		action = "Current effective configuration"
	} else if report.Applied {
		action = "Applied repository configuration"
	}
	_, err := fmt.Fprintf(cmd.OutOrStdout(), "%s for %s %s\nEnabled: %t\nURL: %q\nAudience: %q\nOn outage: %q\n",
		action, report.Scope, report.Repository, report.Enabled, report.Settings.URL, report.Settings.Audience, report.Settings.OnOutage)
	if err != nil {
		return err
	}
	for _, warning := range report.Warnings {
		if _, err := fmt.Fprintln(cmd.OutOrStdout(), warning); err != nil {
			return err
		}
	}
	return nil
}
