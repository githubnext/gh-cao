package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func setupCommand(execute runtimeExecutor, gh ghExecutor) *cobra.Command {
	setup := operatorCommand("setup", "Prepare monitoring runtime, policy, Pages, scope, and authentication", cobra.NoArgs, []operatorFlag{
		jsonFlag,
		{name: "repository", description: "Public monitoring quickstart scope OWNER/REPO or OWNER/* (repeatable; accepts public Pages)", repeat: true},
	}, execute)
	setup.Long = `Prepare monitoring in an existing control-repository checkout.

Install missing bare runtime files and initialize missing policy, then configure
repository scope, dashboard Pages, and authentication. No prior init or installer
is required. Existing policy and unrelated files are preserved; conflicting
runtime files stop preparation instead of being overwritten. Missing gh-aw is
installed at the catalog's required version; existing older versions require
an explicit upgrade. Interactive setup then shows a one-line change summary
and full-diff path before a separate default-No commit-and-push prompt.
Publication requires the synchronized default branch, matching origin and an
empty Git index. It commits only reviewed CAO files, respects branch protections,
and returns immediately with deployment-pending links, without waiting for Actions.
Declining or unattended setup leaves files local. Setup never creates a
repository, enrolls campaigns, or runs agentic work. Use gh repo create or
gh repo clone explicitly before setup.

Guided monitoring of the control repository and public remote targets uses the
built-in Actions token without an App or PAT, even for a private control repository.
Private/internal remote targets still require separately configured credentials.

Run without a subcommand for guided setup, use auth to configure credentials,
admission to configure the cooperative OIDC gate's non-secret variables,
or adx to prepare optional snapshot export settings for an existing destination.
Admission does not require a workflow secret. Non-interactive setup applies only the bounded public profile.
Setup and authentication run natively in Go; help works offline.

Quickstart: gh cao setup --repository 'OWNER/*'
This explicitly accepts a public dashboard, snapshots only currently public
repositories, and uses Actions GITHUB_TOKEN. No campaign or agent execution is
enabled. Logs/artifacts may be unavailable; evidence is marked partial.`
	auth := &cobra.Command{
		Use: "auth", Short: "Configure Actions and hosted-executor authentication", Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return fmt.Errorf("setup auth requires github-app, enterprise-app, hosted-cli, or token")
		},
	}
	authenticate := func(cmd *cobra.Command, args []string) error {
		// Preserve the setup-auth result contract behind the Go command hierarchy.
		return execute(cmd, append([]string{"setup-auth"}, args[2:]...))
	}
	common := []operatorFlag{
		textFlag("repo", "Control repository OWNER/REPO"), textFlag("policy", "CAO policy path"),
		{name: "write-repository", description: "Explicit safe-output repository OWNER/REPO (repeatable)", repeat: true},
		boolFlag("dry-run", "Describe configuration without applying it"),
	}
	app := operatorCommand("github-app", "Configure organization-owned read and write GitHub Apps", cobra.NoArgs, appendFlags(common,
		textFlag("read-client-id", "Existing read App client ID"), textFlag("write-client-id", "Existing write App client ID"),
		textFlag("read-app-name", "Name for a new read App"), textFlag("write-app-name", "Name for a new write App"),
		boolFlag("force", "Replace the selected App configuration"), boolFlag("no-open", "Print browser URLs instead of opening them"),
	), authenticate)
	app.Long = `Configure separate private read-only and write-capable GitHub Apps.

The read App covers the control repository plus the exact policy allowlist.
The write App covers only the control repository by default; --write-repository
replaces that default with the exact approved output repositories.

New installations on github.com preselect these repositories for your review.
Keep Only select repositories and approve on GitHub. Preselection requires
valid repository/account IDs and at most 100 repositories per installation;
lookup failures stop setup rather than omitting repositories or selecting all.
Existing installations open Configure when coverage is missing. GHES and
data-residency hosts retain manual selection. Setup verifies selected-repository
coverage after installation; browser approval does not change CAO policy.

--dry-run is offline when --repo is supplied. --no-open prints browser URLs.`
	enterprise := operatorCommand("enterprise-app", "Configure existing enterprise-owned GitHub Apps", cobra.NoArgs, appendFlags(common,
		textFlag("read-client-id", "Read App client ID"), textFlag("write-client-id", "Write App client ID"),
	), authenticate)
	token := operatorCommand("token", "Configure explicitly scoped fine-grained PAT pairs", cobra.NoArgs, appendFlags(common,
		textFlag("expires-in", "Token expiration in days, 1 through 366 (default 30)"),
		boolFlag("no-open", "Print token-creation URLs instead of opening them"),
		boolFlag("keep-existing", "Preserve existing token secrets"),
		boolFlag("replace-existing", "Replace existing token secrets"),
	), authenticate)
	token.MarkFlagsMutuallyExclusive("keep-existing", "replace-existing")
	hosted := operatorCommand("hosted-cli", "Create the dedicated hosted-executor GitHub App", cobra.NoArgs, []operatorFlag{
		textFlag("repo", "Control repository OWNER/REPO"),
		{name: "target-repository", description: "Additional exact OWNER/REPO under the control owner (repeatable)", repeat: true},
		textFlag("app-name", "Globally unique App name"),
		textFlag("private-key-out", "Temporary bootstrap file (mode 0600, never overwritten)"),
		boolFlag("dry-run", "Print the manifest and targets without changing GitHub"),
		boolFlag("no-open", "Print browser URLs instead of opening them"),
	}, authenticate)
	hosted.Long = `Create a dedicated private GitHub App for cao-dashboard execute-cli, natively in Go.

Requests only actions, contents, pull_requests and workflows write plus metadata
read. Never requests administration, repository_creation, installation_repositories
or organization permissions. GitHub's browser flow provides creation and installation
approval; choose Only select repositories. New github.com installations preselect
only the control repository for review. Additional targets must be explicit
OWNER/REPO values under the control repository owner, not policy-derived scope.
Repository administrators can add repositories to an approved installation unless
organization policy requires owner approval; denied additions continue in the browser.

--private-key-out is required except for --dry-run. The key is never printed or
passed in command arguments. Import this temporary bootstrap file into the
execute-cli deployment's secret manager, then securely remove it. Keep this App
separate from dashboard login and Actions read/write Apps. This command does not
change CAO policy, Actions secrets, authentication mode, or hosted-feature gates.`
	auth.AddCommand(app, enterprise, hosted, token)
	setup.AddCommand(auth, setupADXCommand())
	admission := admissionCommands(gh)
	for _, command := range admission.Commands() {
		if command.Name() != "setup" {
			continue
		}
		admission.RemoveCommand(command)
		command.Use = "admission"
		command.Long = `Configure the cooperative OIDC admission gate for an explicit scope.

Equivalent to gh cao admission setup; --dry-run previews without changing settings.
Writes only GH_AW_DEFAULT_ADMISSION_URL, GH_AW_DEFAULT_ADMISSION_AUDIENCE and
GH_AW_DEFAULT_ADMISSION_ON_OUTAGE as non-secret Actions variables.
Select --repo OWNER/REPO, --all-repos from local CAO policy, repeatable --org,
or --enterprise for each enterprise organization's defaults. Organization
variables use visibility all, including future repositories; repository
overrides are preserved and still take precedence.

Admission authenticates with a job-issued GitHub Actions OIDC token. No PAT,
GitHub App private key, installation token or shared admission secret is needed
or uploaded. Never put credentials in the endpoint URL or audience. Any optional
authority backend credentials belong in the server's protected secret storage,
not workflow secrets.

Deploy the authority and configure reviewed enrollment and trusted fresh
accounting separately before applying these settings. Omit --url after setup
to reuse the target's effective endpoint or the current control repository's
configured server for bulk rollout/recovery. Setup defaults outages to block
and the audience to the endpoint URL. It does not deploy the server,
grant enrollment, modify CAO policy, or compile workflows. Use gh cao admission
status --repo OWNER/REPO to inspect effective settings after configuration.`
		setup.AddCommand(command)
		break
	}
	return setup
}
