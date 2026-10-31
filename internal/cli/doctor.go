package cli

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

const doctorTimeout = 15 * time.Second

var githubCLIVersionPattern = regexp.MustCompile(`(?m)^gh version ([0-9]+\.[0-9]+\.[0-9]+(?:[-+][0-9A-Za-z.-]+)?)\b`)

type doctorCheck struct {
	Name        string `json:"name"`
	Status      string `json:"status"`
	Message     string `json:"message"`
	Remediation string `json:"remediation,omitempty"`
}

type doctorReport struct {
	Command string        `json:"command"`
	OK      bool          `json:"ok"`
	Checks  []doctorCheck `json:"checks"`
}

func doctorCommand(version func(context.Context) (string, error), gh ghExecutor) *cobra.Command {
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Diagnose GitHub CLI, authentication, gh-aw, and local policy",
		Long: `Run read-only installation checks from the control repository root.

Checks GitHub CLI, gh-aw, GitHub authentication, and basic local cao.json
structure and compiler pin. Authentication checks only the operator's gh login,
not workflow App installation access or target write permissions.
Authentication may contact GitHub. External checks
have at most 15 seconds each; the GitHub CLI version probe has five seconds.
No files, secrets, or repository settings are changed.
Use doctor token-broker --settings PATH --policy PATH --json for separate
offline broker deployment-settings and mounted-policy checks.

This does not repair SQLite snapshots or replace the full policy and workflow
validation performed by the CAO validation Actions workflow. No Node.js runtime
is required for these installation checks. Explicit runtime credential verification:
gh workflow run cao-validate.yml -f verify-credentials=true`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			report, err := diagnose(cmd.Context(), version, gh)
			if err != nil {
				return err
			}
			if jsonOutput {
				err = printJSON(cmd, report)
			} else {
				for _, check := range report.Checks {
					if _, err = fmt.Fprintf(cmd.OutOrStdout(), "[%s] %s: %s\n", strings.ToUpper(check.Status), check.Name, check.Message); err != nil {
						return err
					}
					if check.Remediation != "" {
						if _, err = fmt.Fprintf(cmd.OutOrStdout(), "  %s\n", check.Remediation); err != nil {
							return err
						}
					}
				}
			}
			if err != nil {
				return err
			}
			if !report.OK {
				return fmt.Errorf("doctor found installation problems; follow the reported remediation")
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Write machine-readable diagnostic results")
	cmd.AddCommand(doctorTokenBrokerCommand())
	return cmd
}

func diagnose(ctx context.Context, version func(context.Context) (string, error), gh ghExecutor) (doctorReport, error) {
	report := doctorReport{Command: "doctor", OK: true, Checks: []doctorCheck{}}
	add := func(name, status, message, remediation string) {
		report.Checks = append(report.Checks, doctorCheck{name, status, message, remediation})
		if status == "fail" || status == "skip" {
			report.OK = false
		}
	}
	if err := ctx.Err(); err != nil {
		return report, err
	}
	versionCtx, cancel := context.WithTimeout(ctx, doctorTimeout)
	output, err := version(versionCtx)
	cancel()
	ghAvailable := err == nil
	if !ghAvailable {
		add("github-cli", "fail", "GitHub CLI is unavailable.", "Install gh and ensure it is on PATH.")
	} else if match := githubCLIVersionPattern.FindStringSubmatch(output); len(match) != 0 {
		add("github-cli", "pass", "gh version "+match[1], "")
	} else {
		ghAvailable = false
		add("github-cli", "fail", "GitHub CLI returned an unrecognized version.", "Check that gh on PATH is the GitHub CLI.")
	}

	execute := func(args ...string) (string, string, error) {
		checkCtx, cancel := context.WithTimeout(ctx, doctorTimeout)
		defer cancel()
		return gh(checkCtx, args...)
	}
	var compilerVersion string
	if err := ctx.Err(); err != nil {
		return report, err
	}
	if ghAvailable {
		stdout, stderr, err := execute("aw", "version")
		if err == nil {
			compilerVersion = versionPattern.FindString(stdout + "\n" + stderr)
		}
		if compilerVersion == "" {
			add("gh-aw", "fail", "gh-aw is unavailable or its version could not be determined.", "Install gh-aw with gh extension install github/gh-aw, or repair the existing installation.")
		} else {
			add("gh-aw", "pass", "gh-aw version "+compilerVersion, "")
		}
		if err := ctx.Err(); err != nil {
			return report, err
		}
		// Never render authentication output: gh may include credential details.
		_, _, err = execute("auth", "status")
		if err != nil {
			add("authentication", "fail", "GitHub authentication could not be verified.", "Run gh auth status to inspect the selected host and accounts; use gh auth login to authenticate.")
		} else {
			add("authentication", "pass", "Operator GitHub CLI authentication verified; workflow credentials and target write access are not checked.", "")
		}
	} else {
		add("gh-aw", "skip", "Requires a working GitHub CLI.", "")
		add("authentication", "skip", "Requires a working GitHub CLI.", "")
	}
	if err := ctx.Err(); err != nil {
		return report, err
	}
	policy, err := readDoctorPolicy()
	if err != nil {
		add("policy", "fail", "Local CAO policy is missing, unreadable, or has invalid basic structure.", "Run gh cao setup from the control repository root if no policy exists; otherwise review .github/workflows/cao.json. Use gh cao init only for local policy preparation.")
		add("gh-aw-version", "skip", "Requires a readable CAO policy.", "")
		return report, nil
	}
	add("policy", "pass", "Local CAO policy has valid basic structure (not full schema or authority validation).", "")
	tokenBrokerPolicyCheck(&report, policy, false, "")
	pin, ok := policy["gh-aw-version"].(string)
	if !ok || pin == "" || versionPattern.FindString(pin) != pin {
		add("gh-aw-version", "fail", "CAO policy must pin an exact gh-aw version.", "Review gh-aw-version in .github/workflows/cao.json.")
	} else if compilerVersion == "" {
		add("gh-aw-version", "skip", "Requires a working gh-aw installation.", "")
	} else if pin != compilerVersion {
		add("gh-aw-version", "fail", "Installed gh-aw does not match the CAO policy pin.", "Align the installed gh-aw version and reviewed gh-aw-version policy pin before compiling workflows.")
	} else {
		add("gh-aw-version", "pass", "Installed gh-aw matches the CAO policy pin.", "")
	}
	return report, nil
}
