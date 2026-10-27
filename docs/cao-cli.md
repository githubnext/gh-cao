---
title: CAO Commands
description: Configure and inspect CAO with the Go extension, and use the Node Actions runtime for workflow-agent evidence APIs.
agent:
  type: cli-reference
  prominent: true
---

Use this reference when operating CAO or adding a CLI command. GitHub CLI
installs the compiled Go extension as `gh cao`; guided setup prepares the
repository runtime for Activity and Dashboard Actions. Invoke that runtime as
`node activity/cao.mjs <command>`; there is no shell launcher. Agents
using the CLI should follow the
[`cao-cli` skill](https://github.com/githubnext/gh-cao/blob/main/skills/cao-cli/SKILL.md);
agents querying activity data should use
[`analyze-cao`](https://github.com/githubnext/gh-cao/blob/main/skills/analyze-cao/SKILL.md).
Onboarding and lifecycle administration run only in Go, not in Node wrappers.
The Node `cao` entry point is the supported Actions runtime, including evidence
APIs for agents inside agentic workflows, not an alternative operator surface.
Its `queries`, `query-info`, `prompt`, `query`, `gh runs|issues|prs`,
`computation`, `mcp`, and `download` commands and runtime dependencies remain
installed and supported. Similar read capabilities in `gh cao` are intentional:
operators use `gh cao`, workflow agents use `node activity/cao.mjs`, and server
hosting/administration belongs to `cao-dashboard`. Workflow agents need not
build, install, or invoke the Go extension. See [workflow-agent evidence](agent-analysis.md#inside-an-agentic-workflow).
Run policy commands from the control repository root:

```bash
gh cao --help
node activity/cao.mjs --help
```

Command failures exit nonzero and report the error without printing the command
reference. Request command-specific help with `gh cao COMMAND --help`.

## Operator evidence

`gh cao evidence` groups read-only operator evidence collection. The initial
`release` subcommand reuses existing package/promotion workflow runs and optional
Azure deployment receipts; it does not build, publish, promote, deploy, change
authority, or configure evidence retention. It runs entirely in Go with no Node.js
or repository checkout required:

```bash
gh cao evidence release \
  --repo githubnext/gh-cao \
  --revision FULL_40_CHARACTER_SOURCE_SHA \
  --image ghcr.io/githubnext/gh-cao/cao-server@sha256:EXACT_64_CHARACTER_DIGEST \
  --package-run PACKAGE_RUN_ID \
  --output /secure/evidence/release-evidence.json
```

Optionally add `--promotion-run RUN_ID`, `--azure-receipt PATH`, or
`--verify-provenance`. The last option invokes `gh attestation verify` with exact
source/image/reusable-publisher constraints and requires the package run attempt
to match. Existing GitHub/registry read authentication is required; do not put
credentials in arguments. The new output uses mode `0600` where supported and
is never overwritten; operators must restrict destination-directory ACLs,
especially on Windows where POSIX mode bits do not enforce private access.
GitHub.com/GHCR are currently supported. Errors exit nonzero; optional unavailable
evidence stays unknown in the JSON.

The report always remains `incomplete`: approval references do not establish
independent approval, configured Azure image read-back is not measured running
image identity, and local source-built Azure images remain development-only.
Owners must retain original artifacts, signed bundles and verification receipts
through their approved evidence channel and supply independent retention,
governance and examination-period evidence. See the
[release evidence boundaries](https://github.com/githubnext/gh-cao/blob/main/server/README.md#owner-selected-release-evidence-collection).
This command requires an extension release containing it; merging source alone
does not update an installed binary.

## GitHub CLI extension

Install the extension with GitHub CLI, then run guided setup from an existing
control-repository checkout's root:

```bash
gh extension install githubnext/gh-cao
gh cao --version
gh cao setup
```

:::tip[SAML-token installation failure]
If `gh extension install` fails because your token lacks cross-organization
SAML authorization, use the [curl installer](#control-plane-installer):

```bash
curl -fsSL https://raw.githubusercontent.com/githubnext/gh-cao/main/install-gh-cao.sh | bash -s -- --setup
```

This fallback requires Bash, curl, Git, Node.js 24+, and GitHub CLI 2.68+ signed
in for the control repository. Public tool downloads are anonymous; stored
credentials remain available for control-repository and runtime-provenance access.
:::

### GitHub CLI release discovery

CAO releases include native binaries for GitHub CLI discovery.
There is intentionally no root executable script:
CAO ships a standalone Go binary, not a source-building or Node.js launcher.
GitHub CLI classifies the extension using `releases/latest` before selecting a
`--pin` tag, so maintainers must keep complete native assets in the latest stable
release. Do not modify immutable historical releases to repair discovery.

Refresh a GitHub-CLI-managed extension with:

```bash
gh extension upgrade gh-cao
gh cao --version
```

Rerun `install-gh-cao.sh` only for an installer-managed extension.

Extension installation alone does not install the control-repository runtime
or `gh-aw`. `gh cao setup` prepares them as needed. Upgrade the extension to
receive new commands and fixes; merging source does not update an installed binary.

### Guided runtime preparation

`gh cao setup` runs from an existing GitHub checkout's root. It prepares missing
monitoring runtime files and missing baseline policy, then configures exact
repository scope, authentication, and Actions-backed Pages. It stages the root
package with gh-aw in an isolated checkout, verifies the selected stable
release's exact runtime provenance and file checksums, and checks all
destinations before adding files. Existing policy is read, not replaced with a
baseline. Invalid policy and conflicting runtime files fail closed. Complete
runtime installs are reused; use `gh cao update --campaign root` for upgrades.

Control repositories receive the curated production runtime under `activity/`,
`dashboard/`, and `.github/`, not tests, screenshots, contributor tooling, or
plugin files. Normal materialization downloads a prebuilt exact-SHA archive,
without install-time npm. Stable versions use release assets; main/SHA installs
use successful Actions artifacts retained for 90 days, waiting at most five
minutes for an already active exact-SHA runtime build. Artifact and attestation
access needs authenticated GitHub CLI even when public tool downloads are
anonymous. See the [runtime provenance contract](https://github.com/githubnext/gh-cao/blob/main/specs/repository-layout-and-installation.md#41-provenance).
Dashboard builds still install their production npm dependencies.

Missing `gh-aw` is installed at the selected catalog's minimum version. Setup
never silently upgrades an existing older or broken extension. It does not need
Node.js locally. `gh cao init` remains an optional, policy-only local preparation
command for scripting; it is not a prerequisite and does not configure remote
settings. Both paths construct the same exact, campaign-free baseline policy.

Create or clone repositories explicitly with `gh repo create` or `gh repo clone`
before setup. Setup never creates repositories, enrolls campaigns, or dispatches
workflows. A failed or cancelled setup can leave prepared runtime files and
already applied Pages or credential settings; inspect them before retrying.
After approval, brief progress lines identify each setup step before it starts,
including release download, workflow preparation, Pages, authentication, and
policy saving. Progress goes to stderr, keeping `--json` stdout machine-readable.

Interactive setup offers a separate **“Commit and push these CAO monitoring
changes? [y/N]”** after configuration. It shows a one-line change summary,
with additions in green and deletions in red when terminal color is enabled.
The complete diff is in a temporary file available while the prompt is open;
file-by-file statistics and the policy patch are not printed inline.
Review uses an isolated Git index and does not stage your files. Only the exact
runtime/package files prepared by this setup and policy are eligible, never
unrelated files, including files alongside the runtime.

Approval requires an empty real index, a matching single origin push destination,
and a default-branch checkout exactly synchronized with the remote (or both
unborn for an empty repository). Setup rechecks Git/file state after review,
commits the reviewed changes, and pushes normally without force, extra tags,
submodule pushes, or protection bypasses. Existing local commits, staged changes,
feature branches, or uncommitted runtime from an earlier setup require manual
publication. Commit failures leave staging for inspection; push failures retain
the local commit with recovery guidance.

Success reports Actions and known dashboard URLs as **deployment pending** and
returns immediately: no workflow dispatch, polling, or deployment wait.
Interactive color output separates the success message from the prompt, highlights
the link labels, and underlines the URLs. Redirected, no-color, and accessible
output stays plain.
`--json` includes `publication.status` and, after a push, its commit, links, and
`deploymentStatus: "pending"`. Declining or cancelling publication leaves
configuration local. Unattended/CI setup never commits or pushes.

### Linear, Jira, and Azure DevOps webhook setup

`gh cao setup linear`, `gh cao setup jira`, and `gh cao setup ado`
(`azure-devops` is an alias) prepare an existing webhook gateway to receive
issue-service events. These native commands work offline and print service
configuration instructions and a secret-free gateway enrollment fragment.
They do not provision a gateway, register a webhook with the provider, modify
policy, upload credentials, or activate workflows.

Supply your existing public HTTPS gateway URL, a unique source route, the
provider's canonical resource prefix and native scope, exact destination
**GitHub numeric repository IDs**, and an explicit future expiry:

```bash
gh cao setup linear \
  --gateway-url https://hooks.example.com \
  --source-id linear-acme \
  --resource-url-prefix https://linear.app/acme/issue/ \
  --scope LINEAR_ORGANIZATION_ID \
  --destination-repository-id 123456789 \
  --expires-at 2030-01-01T00:00:00Z

gh cao setup jira \
  --gateway-url https://hooks.example.com \
  --source-id jira-app \
  --resource-url-prefix https://example.atlassian.net/rest/api/3/issue/ \
  --scope APP \
  --destination-repository-id 123456789 \
  --expires-at 2030-01-01T00:00:00Z

gh cao setup ado \
  --gateway-url https://hooks.example.com \
  --source-id ado-app \
  --resource-url-prefix https://dev.azure.com/acme/_apis/wit/workItems/ \
  --scope AZURE_PROJECT_ID \
  --collection-id AZURE_COLLECTION_ID \
  --destination-repository-id 123456789 \
  --expires-at 2030-01-01T00:00:00Z \
  --json --output /secure/deployment/ado-webhook-setup.json
```

Replace all example identities and choose a suitably bounded expiry. Repeat
`--destination-repository-id` for multiple explicitly approved destinations.
`--json` emits the preparation report for automation; `--output` saves it to
a new mode-`0600` file without overwriting files or symlinks. Its parent directory
must exist; restrict directory ACLs too, especially on Windows.
`--dry-run` never creates that file. No provider API token or webhook secret is
accepted as a flag or included in the report.

Complete the printed instructions in the provider's administration interface:

| Service | Webhook configuration | gh-aw trigger | Matching safe outputs |
| --- | --- | --- | --- |
| Linear | Workspace settings → API → Webhooks; select Issue events and the intended workspace/team scope. Use the generated receiver URL. Preserve `Linear-Signature`, `Linear-Delivery` and the signed `webhookTimestamp`; the receiver requires timestamp freshness within one minute. | `linear_issue`, scoped by `organization-id` | `linear-create-issue`, `linear-update-issue`, `linear-add-comment` |
| Jira | Jira administration → System → WebHooks; select issue created/updated/deleted events and restrict the project. Configure a webhook secret and ensure signed deliveries include `X-Hub-Signature: sha256=…` and `X-Atlassian-Webhook-Identifier`. Unsigned webhook configurations are unsupported. | `jira_issue`, scoped by `project` | `jira-create-issue`, `jira-update-issue`, `jira-add-comment`, `jira-add-label` |
| Azure DevOps | Project settings → Service hooks → Web Hooks; create subscriptions for `workitem.created`, `workitem.updated`, and `workitem.deleted` in the intended project. Configure HTTPS Basic authentication with username `cao` and a dedicated receive-only password. | `azure_devops_issue`, scoped by `project_id` | `ado-create-work-item`, `ado-update-work-item`, `ado-comment-on-work-item`, `ado-assign-work-item`, `ado-link-work-items`, `ado-upload-workitem-attachment` |

Store the provider signing secret (or Azure hook password), 32–512 bytes, only
in the **receive deployment's secret manager** under the generated
`CAO_GATEWAY_SOURCE_…` name. `--secret-env` selects another valid receive-only
name. Optional rotation uses the corresponding `_PREVIOUS` environment name.
Never reuse outbound API credentials or place secret values in policy, generated
reports, command arguments, workflow inputs, or committed files.

Review and merge the generated source entry into the gateway policy's
`externalSources`, preserving existing entries, choosing unique source IDs and
secret names, and advancing the reviewed policy revision. This is gateway
enrollment, **not** a replacement for `.github/workflows/cao.json`. Enroll each
destination independently in managed discovery; the gateway requires fresh,
approved compiled workflow inventory and private destinations. Restart the
receiver after adding a source. Deploy the receiver and dispatch roles with
their separately scoped credentials and durable operational storage before
testing delivery. GitHub Pages is not a webhook receiver.

In the destination workflow, declare the matching scoped gh-aw trigger and
only the safe outputs required by its mission. Configure outbound credentials
separately in GitHub Actions secrets:

- **Linear:** `LINEAR_API_KEY`; issue creation also needs a trusted `team-id`
  or `LINEAR_TEAM_ID` secret. An optional project can be fixed by `project-id`
  or the `LINEAR_PROJECT_ID` variable.
- **Jira:** `JIRA_USER_EMAIL` and `JIRA_API_TOKEN`. Explicitly set your own
  trusted `JIRA_BASE_URL` through `safe-outputs.env`; never rely on an upstream
  example tenant.
- **Azure DevOps:** `AZURE_DEVOPS_EXT_PAT` by default, or an explicitly configured
  supported `SYSTEM_ACCESSTOKEN`. Fix the intended organization/project and
  output targets in reviewed workflow configuration.

These names map to gh-aw v0.92.0; consult your installed compiler's
[safe-output reference](https://github.github.com/gh-aw/reference/safe-outputs/)
for destination options and limits. Compile changed workflows, review the
generated locks, and keep rollout in review mode unless separately authorized.
Neither receiving a webhook nor possessing a provider token grants CAO authority.
Test a real scoped event, check provider delivery status and gateway admission,
then verify the intended workflow and declared output. Setup reports preparation
only, never successful delivery or live provider access. See the
[external-source gateway contract](https://github.com/githubnext/gh-cao/blob/main/specs/rfc-033-webhook-gateway.md#622-independently-enrolled-external-sources)
for authentication, normalization, expiry and dispatch requirements.

### Optional ADX destination setup

`gh cao setup adx` prepares the existing hosted server's **snapshot-only** ADX
integration. It runs offline without Node.js, Azure CLI, a checkout, or GitHub
authentication. Supply an existing public-cloud Kusto query endpoint/database,
Entra application identity and exact canonical repository IDs already collected
in your CAO namespace, not repository names to resolve or wildcard scope:

```bash
gh cao setup adx \
  --endpoint https://CLUSTER.REGION.kusto.windows.net \
  --database CaoAnalytics \
  --tenant-id 11111111-1111-1111-1111-111111111111 \
  --client-id 22222222-2222-2222-2222-222222222222 \
  --repository-id EXACT_CANONICAL_REPOSITORY_ID \
  --data-approved --single-replica \
  --output /secure/deployment/adx-environment.json
```

Replace the example identity and scope with reviewed values. Repeat
`--repository-id` for up to 1,000 unique IDs. Explicit `--data-approved`
acknowledges that the corpus/use and actual destination meet applicable service
terms, privacy and governance; `--non-personal-data` is the alternative approval
for non-personal, non-regulated data. `--single-replica` confirms exactly one
exporter, durable operational state and a persistent worker lifecycle.
These acknowledgments do not verify suitability, authority, topology or access.

Stdout is always a JSON environment object with string values, including
`CAO_ADX_ENABLED=true` and explicit trace/mining disables. `--output` additionally
creates that object as a new mode-`0600` file, never overwriting existing files
or symlinks; its parent directory must exist. Restrict directory ACLs as well,
especially on Windows where POSIX modes do not enforce private access.
`--dry-run` prints the same settings but never creates the output file.
No ambient ADX settings or credentials are read. Optional `--interval` defaults
to `30s` (range `1s`–`1h`) and `--max-storage-bytes` defaults to 50,000,000,000
(range 1 MiB–1 TiB).

Review and apply `cao-dashboard adx schema` to the destination separately before
deployment. Map the JSON keys to environment settings in your existing
single-replica hosted PostgreSQL deployment; this is not a shell script or
`gh cao deploy azure` configuration file, and setup does not apply it.
Inject the omitted `CAO_ADX_CLIENT_SECRET` directly from the deployment secret
manager, never a flag, reviewed policy, or committed document. The generated
profile intentionally disables any existing traces/mining when applied; separate
trace enablement requires its own reviewed scope, content/rights approvals and
durable spool configuration. No resources, schema, credentials, policy,
collection, deployment, live traces, mining or model training are changed by
this command. See the [ADX operator contract](https://github.com/githubnext/gh-cao/blob/main/server/ADX-INGEST.md)
and [trace contract](https://github.com/githubnext/gh-cao/blob/main/server/ADX-TRACES.md).

### Control-plane installer

Use the root installer as a fallback when SAML-restricted tokens prevent
GitHub CLI extension installation:

```bash
curl -fsSL https://raw.githubusercontent.com/githubnext/gh-cao/main/install-gh-cao.sh | bash
```

The installer registers `gh cao` directly in GitHub CLI's data directory,
following the gh-aw installer pattern, without requiring Go or a repository
rename. It honors `XDG_DATA_HOME` and Windows `LOCALAPPDATA`, defaulting to
`~/.local/share/gh/extensions/gh-cao`. Rerun the installer to update this
manually installed extension; `gh extension upgrade` does not manage it.
Linked/source installations are not overwritten. Release binaries use the
`gh-cao-OS-ARCH` naming convention, with `.exe` for Windows, and cover Linux,
macOS, and Windows on amd64 and arm64. `checksums.txt` contains their SHA-256
digests. Downloads fail closed unless the asset's exact checksum and embedded
version match. Verification finishes before replacing an existing binary.
It installs `gh aw` using the upstream
[`install-gh-aw.sh`](https://github.com/github/gh-aw/blob/main/install-gh-aw.sh),
passing the required version and keeping upstream checksum verification enabled.
The upstream installer runs without token environment variables or the Actions
`INPUT_VERSION` override that would select authenticated extension installation.
Public CAO release metadata, binaries, checksums, and the upstream installer
are fetched anonymously, ignoring curl configuration that could inject
authentication. No credentials are cleared from the parent environment,
GitHub CLI configuration, or keychain. Authenticated control-repository
creation and setup retain the operator's sign-in.
Anonymous API rate limits or network errors fail closed; they do not trigger
an authenticated download fallback.
An explicit catalog source `githubnext/gh-cao@vX.Y.Z` selects the same
extension tag, including a published prerelease with that tag; otherwise the
installer selects the latest published CAO `vX.Y.Z` release, including prereleases
but excluding drafts. Main/SHA and local catalog
sources still select their own runtime revision independently.
Node.js 24+ is required by this installer, but not by native setup
or the other Go operator commands.

### Command reference

For local development, build with `make build` and use `./gh-cao` directly, or follow the
[local extension installation guide](https://github.com/githubnext/gh-cao/blob/main/CONTRIBUTING.md#go-extension-development)
to invoke it as `gh cao`.

| Surface | Commands | Execution |
| --- | --- | --- |
| Policy and workflow controls | `init`, `campaigns mode`, `campaigns enable`, `campaigns disable` | Native Go; `init` is optional local policy-only preparation, not runtime, Pages, or credential setup. |
| Standalone workflow installation | `add WORKFLOW [--force] [--engine ENGINE]` | gh-aw installs and compiles; CAO registers the standalone workflow in policy. Interactive publication and first run require separate default-No approvals. |
| Campaign installation | `add CAMPAIGN [GH_AW_ADD_OPTIONS...]` | Native Go; gh-aw owns installation records and workflow generation, Go materializes campaign resources and merges policy. No Node runtime required. |
| Onboarding and authentication | `setup`, `setup auth github-app\|enterprise-app\|hosted-cli\|token` | Native Go; setup prepares missing monitoring runtime and policy, Pages, scope, and authentication, preserving separate read/write and hosted-executor credentials. |
| Issue-service webhook setup | `setup linear`, `setup jira`, `setup ado` (alias `setup azure-devops`) | Native Go; offline enrollment preparation and provider instructions, with separate receive secrets and outbound safe-output credentials. |
| Cooperative admission | `setup admission`, `admission status`, `admission disable` | Native Go; configure admission with `setup admission`. Configures or inspects non-secret Actions variables, not server deployment or enrollment authority. |
| Cloud deployment | `deploy azure` | Native Go; read-only preflight and planning, or explicit application of a reviewed development deployment plan. |
| Lifecycle | `repos add OWNER/REPO`, `update`, `upgrade-gh-aw` | Native Go; gh-aw still owns workflow generation and campaign installation records. |
| Read-only analysis | `download`, `pages`, `queries`, `query-info`, `prompt`, `query`, `gh runs\|issues\|prs`, `computation intelligence\|runtime-health`, `mcp` | Native Go with pure-Go SQLite access and declarative Dashboard Language evaluation. |
| Workflow value | `value publish`, `value enable`, `value disable` | Publish prepared evidence to a hosted dashboard, or edit reviewed local policy for recurring collection of one workflow. |
| Operator evidence | `evidence release` | Native Go; bounded read-only GitHub references, optional provenance verification and development Azure receipt summaries. No Node runtime or checkout required. |
| Installation diagnostics | `doctor`, `version` | Native Go. `doctor` checks GitHub CLI, gh-aw, authentication, and basic local policy structure and compiler pin. |
| Workflow-agent evidence APIs | `download`, `pages`, `queries`, `query-info`, `prompt`, `query`, `gh runs\|issues\|prs`, `computation intelligence\|runtime-health`, `mcp` | Supported `node activity/cao.mjs <command>` APIs inside agentic workflows, including their installed runtime dependencies; intentional overlap with operator reads, not deprecated duplicates. |
| Active ingestion and packaging | `ingest-jsonl`, `compact-jsonl`, `hash-payloads` | Node Actions pipeline: import raw or normalized JSONL into SQLite, compact raw shards per repository, and generate normalized run/record shards plus payload checksums. |
| Manual artifact import | `ingest` | Supported `node activity/cao.mjs ingest --context FILE --logs DIRECTORY` importer for extracted gh-aw artifacts; not used by the Activity or Dashboard pipelines and not deprecated. |
| Actions pipeline and maintainer tools | `discover-workflows`, `audit-jsonl`, `recycle-cache`, `issue-status`, `activity-stats`, `validate-activity-data`, `operational-value`, `cluster-problems`, `dashboard-complexity`, `prune-dashboard` | Invoke with `node activity/cao.mjs <command>`; not migrated to the extension. |
| Control-plane validation | `node activity/cao.mjs validate --json` | Actions-only JavaScript runtime invoked by `cao-validate.yml`; no Go or shell-wrapper command. |
| Node-only administration | `node activity/cao.mjs repair-database` | Mutating SQLite repair/retention; no equivalent Go command. |

This table groups commands by responsibility, not by interchangeable executables.
Inspect the executable trees with `gh cao --help`,
`node activity/cao.mjs --help`, and `cao-dashboard --help`; use native
subcommand help to inspect each registered child and its flags. `help` is built-in
discovery, not another operation. `pages` remains dashboard-page discovery in both
the operator and Node runtimes.

`cao-dashboard` owns server hosting and administration, including
`serve`, `serve-hosted`, `serve-pages`, `azure-functions`, `ingest`, `collect`,
`backfill`, `execute-cli`, `hosted-cli queue`, `database`, `admission validate`,
`audit`, `doctor`, `app-key-check`, `compile-queries`, `benchmark-queries`,
`otel-smoke`, `simulate-api`, and `simulate-webhooks`. Its read-only `doctor`
checks server state, data, queries, and collection; it is distinct from the
operator installation `doctor` and the mutating Node `repair-database`.
See the [server reference](https://github.com/githubnext/gh-cao/blob/main/server/README.md)
for deployment and administration contracts.

Other server binaries are separate process boundaries, not `gh cao` subcommands:
`cao-webhook-gateway` selects isolated receive, dispatch, local, or discover roles;
`cao-cli-quota` consumes an internal stdin/stdout scheduling protocol; and
`cao-cli-relay` wraps a hosted workload inside its network-isolated container.
The npm `cao` bin and `npm run dashboard:data -- <command>` invoke the same
Node entry point, not additional command registrations. npm dashboard serving
scripts wrap `cao-dashboard`; they do not move server operations into Node.

The extension is **entirely Go** and does not execute Node.js, npm, or installed
JavaScript modules. Its default reviewed dashboard catalog, canonical source
projections, and MCP contract are embedded from the same JSON documents used by
the dashboard. `pages`, `queries`, `query-info`, and definition-only `prompt`
work outside an installed control repository. Use `--dashboard FILE` to read
an explicit dashboard document and its directory-confined fragments.

Evidence queries read the canonical `__idb_records` projection through a
read-only SQLite connection; they do not run migrations or repair the snapshot.
Download evidence first with `gh cao download`, or pass `--database FILE` for
an explicitly prepared projection. Policy mutations still operate on the current
control checkout; the executable does not grant rollout authority.

```bash
gh cao setup
gh cao setup --repository 'my-monalisa-org/*'
gh cao setup auth github-app --help
gh cao setup auth enterprise-app --help
gh cao setup auth hosted-cli --help
gh cao setup auth token --help
gh cao add daily-repo-status
gh cao add githubnext/gh-cao/dependabot
gh cao repos add acme/example-service
gh cao campaigns mode preview dependabot
gh cao campaigns enable dependabot
gh cao campaigns disable dependabot
gh cao doctor --json
gh cao pages --json
gh cao query campaign-runs --param campaign=dependabot
```

All help works offline, including `gh cao setup --help` and
`gh cao campaigns --help`. Campaign mode and enablement controls are available
under `campaigns`. Workflow and campaign installation use `gh cao add`.
Repository enrollment uses `gh cao repos add OWNER/REPO`; root-level `add`
installs standalone workflows or campaign packages, not repositories.
Authentication is grouped under `gh cao setup auth`; there is no root-level
`gh cao setup-auth` command. Run `gh cao setup` without a subcommand for guided
control-plane setup, or configure credentials independently with
`gh cao setup auth github-app`, `gh cao setup auth enterprise-app`, or
`gh cao setup auth token`. Each authentication profile retains its existing
options, dry-run behavior, credential prompts, and separate read/write scope.
New `github-app` installations on github.com preselect the read App's control
repository plus exact allowlist, and the write App's control repository or
explicit `--write-repository` replacement scope. Review and approve the
preselected repositories on GitHub. Existing installations retain **Configure**;
GHES/data-residency hosts retain manual selection. Missing/invalid IDs, failed
lookups, or more than 100 repositories stop preselection without widening or
truncating access; see [limits and recovery](control-plane-authentication.md#configure-private-github-apps).
Installation coverage is still verified, `--dry-run --repo OWNER/REPO` stays
offline, and `--no-open` prints browser URLs.
After wildcard expansion, guided setup skips authentication prompts when every
remote target is confirmed public, using Actions' built-in token for public-only
monitoring. The control repository itself may be private or internal: its own
job token can read it without an App or PAT. Fresh setup leaves the authentication
mode unset and stores no credentials. An existing conflicting repository or
inherited organization mode receives an explicit `workflow-token` override.
Review and public Pages
consent still require confirmation. Nonpublic remote targets or unknown visibility
never qualify; lookup failures stop setup. For authenticated operations, use
`gh cao setup auth`. Its unattended monitoring shortcut is to repeat
`--repository OWNER/REPO` or pass a quoted `OWNER/*` to snapshot
currently public repositories. This public monitoring quickstart accepts a public Pages
dashboard; exact private/internal inputs fail. It enables no agent execution or
campaigns. Review and publish the resulting exact policy. Public evidence is
marked partial when logs/artifacts or authenticated enrichments cannot be
guaranteed; it is not reported as complete zero activity.
The dedicated hosted-executor App profile also runs natively, without a Node
runtime or installed control checkout:

```bash
gh cao setup auth hosted-cli --repo OWNER/CONTROL \
  --target-repository OWNER/TARGET --private-key-out /secure/bootstrap.pem
```

It creates a separate private App with exactly actions, contents, pull requests,
and workflows write plus metadata read. Targets are explicit same-owner
repositories, never inferred from policy. GitHub's browser flow requires creation
and installation approval with **Only select repositories**. New github.com
installations preselect only the control repository for review; repository-admin
additions to an approved installation retain their browser fallback when denied.
`--app-name`, repeatable `--target-repository`, `--no-open`, and `--dry-run` are
supported. Dry-run prints the manifest, targets, and executor environment names
without contacting GitHub or requiring a key destination.

The private key is written to a new mode-`0600` temporary bootstrap file, never
overwritten or printed. Import it into the `execute-cli` deployment's secret
manager, then securely remove the file. A failed post-creation verification keeps
the downloaded key for recovery but does not print success configuration.
Keep it separate from dashboard login and Actions read/write credentials.
Setup does not change policy, Actions secrets, authentication mode, or enable
hosted execution. The retired Node profile fails before any side effects and
directs operators to `gh cao setup auth hosted-cli`; it does not wrap Go.
The native JSON result for authentication retains `command: "setup-auth"`.
`gh cao setup auth token --help` also works offline. Campaign installation through
`add` and package updates through `update` retain gh-aw option passthrough.
JSON output, repeatable query parameters, stdin queries, interactive
secret prompts, and command exit codes retain their existing contracts.
There is no `gh cao run`: workflow execution still belongs to gh-aw.

### Retired Node operator commands

The following Node commands are removed, including their administration modules
and JavaScript exports. They exit nonzero with native replacement guidance
before reading stdin, parsing options, opening a database, or changing files or
GitHub settings. Even two-argument calls cannot enter legacy ingestion.

| Retired `node activity/cao.mjs` command | Native replacement |
| --- | --- |
| `init` | `gh cao init` |
| `setup` | `gh cao setup` |
| `setup-auth PROFILE` | `gh cao setup auth PROFILE` (`github-app`, `enterprise-app`, `token`, `hosted-cli`) |
| `add CAMPAIGN` | `gh cao add CAMPAIGN` |
| `mode MODE CAMPAIGN...` | `gh cao campaigns mode MODE CAMPAIGN...` |
| `enable CAMPAIGN...` | `gh cao campaigns enable CAMPAIGN...` |
| `disable CAMPAIGN...` | `gh cao campaigns disable CAMPAIGN...` |
| `update [REF] [OPTIONS...]` | `gh cao update [REF] [OPTIONS...]` |
| `upgrade-gh-aw VERSION` | `gh cao upgrade-gh-aw VERSION` |

Repository enrollment remains `gh cao repos add OWNER/REPO`, not campaign or
standalone workflow installation. No native operator command invokes Node as a
bridge. This consolidation does not activate workflows, widen scope, or change
review-mode defaults; review and publish policy changes separately.

### `gh cao add WORKFLOW_OR_CAMPAIGN`

`gh cao add` is the single native command for installing a standalone CAO workflow
or a campaign package. There is no campaign-installation subcommand.
Both installation kinds use gh-aw;
CAO verifies and registers standalone workflows or materializes and merges
declared campaign workers without widening policy.

| Operation | Command |
| --- | --- |
| Install one standalone workflow | `gh cao add WORKFLOW [--force] [--engine ENGINE]` |
| Install a campaign package | `gh cao add CAMPAIGN [GH_AW_ADD_OPTIONS...]` |
| Enroll one exact repository | `gh cao repos add OWNER/REPO` |

```bash
gh cao add daily-repo-status
gh cao add daily-repo-status@FULL_COMMIT_SHA
gh cao add githubnext/gh-cao/.github/workflows/daily-repo-status.md@main
gh cao add https://github.com/githubnext/gh-cao/blob/main/.github/workflows/daily-repo-status.md
gh cao add ./my-report.md --force
gh cao add githubnext/gh-cao/dependabot@FULL_COMMIT_SHA
gh cao add ./dependabot --force
```

Bare catalog workflow names resolve to CAO's
`.github/workflows/` directory, not gh-aw's implicit `workflows/` directory.
The published `githubnext/gh-cao/daily-repo-status` and `repo-status` shorthands
remain workflow sources. Explicit `.md` sources and GitHub blob URLs also select
workflow installation. Campaigns use `OWNER/REPO[/PATH][@REF]` coordinates or
existing local directories; trailing options are passed unchanged to gh-aw.
Missing explicit local directories fail rather than resolving remotely.
Standalone workflows accept only `--force` and `--engine`; custom install
directories, renames and gh-aw publication flags are not accepted because the
source must retain its exact standalone CAO identity.

After gh-aw succeeds, CAO verifies the installed standalone import and compiled
lock, then atomically registers the workflow in `control-plane.workflows`.
New entries set `mode: review`, `max-mode: review`, and `max-repositories: 1`.
Existing entries (including disables and live settings), defaults, campaign
policy and scope are preserved. Missing policy gets the same exact-current-
repository baseline as `init`; prepare the runtime separately with `setup`.
Failed installs do not mutate policy; files left by a partial installation are
reported for inspection. Repeating with `--force` never resets policy.

Interactive standalone workflow additions offer commit/push of only the installation changes using
the same review safeguards as setup. Pre-existing edits to an installation file,
a staged index, a non-default branch or unsynchronized remote block automated
publication. Unrelated work is never staged. After successful publication, a
separate default-No confirmation can request a first review run through `gh aw
run` on the published default branch, with target/required inputs and explicit-scope
checks. CAO verifies the branch still points at the published commit before
dispatch, then checks the returned run's workflow and SHA against that commit.
This can spend AI credits and create outputs; CAO admission still applies.
Unavailable remote workflows and unknown dispatch outcomes are reported without
automatic retries. A branch race or unverifiable run is reported for inspection,
not treated as success. Declining, CI/noninteractive use and unchanged repeated
additions never cause implicit publication or execution.
Run verification needs a GitHub CLI version that returns the native dispatch
URL (2.87+). CAO never substitutes gh-aw's latest-run tracking fallback for the
activated run's identity; missing identity requires manual inspection, not a retry.

This command requires an extension release containing it; merging source does
not update an already installed binary. Review and publish source, lock and
policy together. Daily Repo Status explicitly selects `gpt-5.3-codex`; override
it by changing `model:` in the installed Markdown source and recompiling the
lockfile. See the [Daily Repo Status quickstart](setup/quickstart.md).

Campaign installation fetches only the installed package's full `resolvedCommit`
SHA from the official catalog, or copies only Git-tracked campaign files from a
local catalog checkout. It replaces the selected campaign's resources, refreshes
its intelligence metadata, and merges declared workers while preserving existing
enablement, mode ceilings, campaign settings, and repository scope. It does not
reinstall the control-plane runtime or plugin. Missing policy is initialized for
the exact current control repository. Campaign installation never commits, pushes
or dispatches work. Prepare the root runtime with `gh cao setup`, not `gh cao add`;
review and commit generated workflows and policy together.

`gh cao doctor` is deliberately **not** `node activity/cao.mjs repair-database`: use the latter for
the existing mutating SQLite snapshot repair/retention operation. Neither Activity nor
Dashboard Actions is switched to the Go executable.

### Publish and refresh workflow value

Run the [operational-value skill](operational-value-how-to.md) first. It defines
the measurement contract and produces validated `*-evidence.jsonl`; publishing
does not collect evidence, run an evaluator or enable recurring work.
Campaign membership is not required: campaign workers and standalone workflows
use the same canonical Value display path. Their source stays in the owning
repository, while the evaluator, collector and publication machinery may live
in the CAO control repository. Daily File Diet, for example, is sourced from
`github/gh-aw` and measured by tooling in `githubnext/gh-cao`.

Native schema-v2 `operational_value` envelopes carry compact measurements;
richer schema-v2 `operational_value_evidence` envelopes carry report facts and
drilldowns. After either form enters canonical storage through its supported
publication path, Dashboard Language selects the latest primary result without
workflow-specific dashboard JSON or code.

```bash
gh cao value publish github/gh-aw daily-file-diet \
  --server https://dashboard.example.com \
  --input daily-file-diet-pulse-evidence.jsonl
```

The server must support value publication and already contain the observed
repository and workflow. Hosted publication requires an authorized
administrator's current, stepped-up OAuth session: supply its cookie value in
`CAO_SERVER_SESSION` and its session-bound CSRF value in `CAO_SERVER_CSRF` through
your private credential environment. These are credentials; never put their
values in policy, report files, URLs, commits or shared command history.
`CAO_SERVER_TOKEN` is accepted only for a loopback server with PostgreSQL, not
as a remote PAT or hosted authentication fallback.

One original schema-v2 envelope is accepted, up to 8 MiB. The response confirms
its identity, original capture time and database revision. Exact retries leave
the revision unchanged. Stale captures, changed frozen contracts and rewritten
immutable measurements fail rather than replace accepted evidence.
Evidence timestamps use UTC and at most microsecond precision; finer nonzero
fractions are rejected rather than silently rounded.
Activity replacement retains directly published evidence. Local SQLite and
static Pages have no direct-publication endpoint; Pages uses Activity transport.

**Collection is wired automatically by the skill** once its frozen function and
repository-local recurring collector pass verification. It stages the exact
pair with `value enable ... --if-absent`, preserving existing registrations and
explicit disables. Review, commit and deploy those files to publish the reviewed
registration; registration does not bypass collection admission.
For manual registration or an intentional re-enable, replace `COLLECTOR_PATH.mjs`
with that collector's actual path; an evidence file or renderer is not a
collector:

```bash
gh cao value enable github/gh-aw daily-file-diet --collector COLLECTOR_PATH.mjs
gh cao value disable github/gh-aw daily-file-diet
```

These commands edit local `.github/workflows/cao.json`; review, commit and deploy
that policy and collector together. They do not enable the measured workflow,
enroll repositories, upload executable code or immediately start a collection.
In particular, a standalone workflow's existence does not cause CAO to discover
or schedule its evaluator; recurring collection requires this exact opt-in.
Recurring remote acquisition remains deferred pending enforceable shared API
budgets. Activity publishes the enabled registration and actual deferral reason,
while bounded local-only computations run outside its publication path.
A failed refresh retains the last accepted capture without changing
its timestamp or inventing a zero. Disable stops future collection, not history.
An explicit disable persists even if the skill is used again.
See the [collector contract](operational-value.md#recurring-collection-after-opt-in).

### Diagnose the installation

Run `gh cao doctor` from the control repository root, or use
`gh cao doctor --json` for machine-readable results. No Node.js runtime is
required. Checks cover:

- The GitHub CLI executable and installed gh-aw version.
- Authentication reported by `gh auth status`, which may contact GitHub.
- The local `.github/workflows/cao.json` file's basic structure and exact
  `gh-aw-version` pin, including agreement with the installed compiler.

Text output labels each check `PASS`, `FAIL`, or `SKIP` and provides remediation
for failures. JSON output contains `command`, `ok`, and `checks`; each check has
`name`, `status`, `message`, and optional `remediation`. Results are still emitted
when checks fail; the command exits nonzero on any failed or skipped check.
External checks have a 15-second timeout (the local GitHub CLI version probe
has a shorter five-second limit). Cancellation stops the diagnostic.

The command is read-only: it does not install extensions, log in, modify policy,
repair databases, or change remote settings. Raw authentication output and
subprocess errors are not included in reports. A passing basic policy check
does not establish rollout authority or full schema validity; use the
[CAO validation workflow](#validate-the-control-plane) for full policy and workflow validation.

CAO and gh-aw have separate jobs:

- **`gh cao`** configures CAO policy, authentication, installed campaigns, workflow enablement, and queries evidence.
- **`node activity/cao.mjs`** prepares pipeline data and runs the retained Node-only tools.
- **`gh aw run`** starts a compiled agentic workflow.
- **`gh run`** lists, watches, and inspects the resulting GitHub Actions runs.

## Common Operator Loop

```bash
# Add a campaign and merge its workers into CAO policy.
gh cao add githubnext/gh-cao/dependabot

# Keep the campaign review-only and enable its workflows.
gh cao campaigns mode preview dependabot
gh cao campaigns enable dependabot

# Review and commit the workflow and policy changes together.
git diff -- .github
git add .github
git commit -m "Configure Dependabot campaign"
git push

# Run one bounded review.
gh aw run dependabot --ref main \
  --raw-field target_repo="acme/example-service" \
  --raw-field max_repos="1" \
  --raw-field rollout_percent="100" \
  --raw-field safe_output_mode="review"
```

`gh cao campaigns mode preview` writes `mode: review` to `.github/workflows/cao.json`. The word *preview* distinguishes this operator command from a live promotion; campaign execution still reports `review` mode.

## Configure the Control Plane

| Command | Use it to |
| --- | --- |
| `gh cao setup` | Prepare missing bare runtime and baseline policy, then guide scope, authentication, and Pages. No prior `init` is required. |
| `gh cao setup linear\|jira\|ado ... [--json] [--dry-run]` | Prepare secret-free external-source enrollment and service-specific webhook instructions; does not deploy or activate the receiver. |
| `gh cao init` | Optionally create only a local minimal review-safe policy for scripting. No runtime installation or remote configuration; refuses existing policy. |
| `gh cao setup admission --repo OWNER/REPO --url HTTPS_URL [--dry-run]` | Configure non-secret admission variables (OIDC requires no workflow secret); `--dry-run` previews changes. |
| `gh cao admission status --repo OWNER/REPO [--json]` | Read effective admission settings, including repository overrides of inherited organization variables. |
| `gh cao admission disable --repo OWNER/REPO [--dry-run]` | Disable the cooperative gate by setting an empty repository URL, without deleting the override and reactivating organization defaults; `--dry-run` previews changes. |
| `gh cao setup admission --all-repos --url HTTPS_URL [--dry-run]` | Configure all repositories selected by the current control repository's validated global CAO scope. |
| `gh cao setup admission --org ORG [--org ORG...] --url HTTPS_URL [--dry-run]` | Configure organization defaults with visibility all, including future repositories; preserve repository overrides. |
| `gh cao setup admission --enterprise SLUG --url HTTPS_URL [--dry-run]` | Enumerate enterprise organizations and configure their organization variables, with enterprise read and per-organization Variables write access. |
| `gh cao setup auth github-app ...` | Configure organization-owned read and write Apps. |
| `gh cao setup auth enterprise-app ...` | Configure existing enterprise-owned Apps with policy-derived read scope and explicit `--write-repository` output scope. |
| `gh cao setup auth hosted-cli ...` | Create the dedicated least-privilege App that the dashboard server's optional `execute-cli` worker uses for single-use, repository-scoped command tokens. |
| `gh cao setup auth token ...` | Configure explicitly consented owner-scoped fine-grained PAT pairs when an App is unavailable. |
| `gh cao add OWNER/REPO/CAMPAIGN` | Install one campaign and merge its declared workers into policy without broadening rollout or enabling live mode. |
| `gh cao repos add OWNER/REPO` | Verify GitHub CLI read access and stage one exact repository in local control-plane policy, with review-mode overrides for existing campaigns. |
| `gh cao deploy azure --plan --config FILE --template FILE --out FILE` | Review a fresh Azure Functions deployment without writing to Azure; `--apply --plan-file FILE` then repeats preflight and refuses change-set drift. See [Azure deployment](deployment-azure.md#native-fresh-deployment-command). |
| `gh cao update` | Upgrade gh-aw when required, update installed campaigns, and refresh worker declarations while preserving operator-owned settings. |
| `gh cao update --campaign SLUG` | Update exactly one installed campaign, retaining runtime materialization and worker-declaration refresh. Use `root` for the CAO root package. |
| `gh cao upgrade-gh-aw VERSION` | Install an exact gh-aw release, upgrade local Agentic Workflow files, and update the pinned policy version after the upgrade succeeds. |

### Update and enrollment details

Omit `--campaign` to update all installed CAO packages. Selection also works with
refs and update options, for example `gh cao update main --campaign dependabot`.
The slug is the last component of the installed package name (`root` for
`githubnext/gh-cao`). Unknown, ambiguous, empty, unsafe, or repeated selections
fail before updates or gh-aw upgrades. Campaign selection does not narrow a
required gh-aw compiler upgrade, which may refresh shared workflow files.

The canvas Updates page exposes **Add repository** beside Repositories. Its
debounced GitHub search only selects a target; execution still requires fresh
approval of the exact `gh cao repos add OWNER/REPO` command. Enrollment
requires existing exact owner and repository allowlists and does not rewrite a
broad discovery policy. An already enrolled repository is unchanged. New targets
stay in review mode even when a campaign defaults to live. The command neither
commits nor pushes, enables campaigns, changes rollout limits, configures
credentials, nor expands an explicitly narrowed Activity scope. Review and commit
the policy before activation at a reviewed workflow SHA.

Repository enrollment runs entirely in the Go extension and does not require
Node.js or installed Activity runtime files.

### Setup and manual campaign execution

Use `gh cao setup` for review-before-apply configuration and optional separately
approved publication. Setup installs no campaign or agentic execution; a push
starts normal monitoring Actions asynchronously. To add automation
afterward, follow the manual [Repo Assist walkthrough](repo-assist-quickstart.md)
using `gh cao add` and `gh aw run`.

Without an interactive terminal or when `CI` is set, `gh cao setup` applies
only the agent-safe public profile: exact current-repository read and Activity
scope, no campaigns, public Actions-backed Pages, and repository
`GITHUB_TOKEN` (`workflow-token`) authentication. It refuses private/internal
control repositories, broader existing policy, or installed campaigns.
`gh cao setup --json` returns the applied profile with
`"nonInteractive": true`. Bare installation remains usable before setup with
exact control-repository scope, no operational campaigns, and
repository-local workflow-token Activity access.

## Control Campaigns

```bash
# Map the campaign to review mode in policy.
gh cao campaigns mode preview dependabot

# Promote only after review and explicit approval.
gh cao campaigns mode live dependabot

# Enable or disable every installed workflow declared by the campaign.
gh cao campaigns enable dependabot
gh cao campaigns disable dependabot
```

These commands accept multiple campaign slugs:

```bash
gh cao campaigns disable dependabot repo-assist
```

`mode live` does not widen repository scope, grant target consent, or create credential access. Complete the [live rollout gates](rollout-and-routing.md) separately.
It also checks whether the control repository has a selected GitHub App or PAT with write credentials configured. If CI can confirm they are missing, it suggests `gh cao setup`; lack of permission to inspect Actions settings does not block the mode change. This check does not verify installation access or token permissions.

`disable` prevents new starts for the campaign's installed workflows. It does not cancel an active run or replace the [control-plane emergency stop](operations.md#emergency-stop).

## Validate the Control Plane

Control-plane validation is Actions-only. Publish reviewed configuration changes
to the control repository's default branch and inspect **CAO validation** in
Actions. To request another validation run:

```bash
gh workflow run cao-validate.yml
gh run list --workflow cao-validate.yml --limit 5
```

Validation checks the policy with the production resolver, the installed gh-aw compiler version, strict compilation and generated workflow drift, campaign workflow identity and enablement, `gh aw doctor`, and bounded trust-boundary security rules. GitHub workflow state is reported as unknown when API access is unavailable. Warnings do not fail the workflow by default.

The workflow invokes `node activity/cao.mjs validate --json` from the shipped
JavaScript runtime at the installed revision. This is the sole implementation;
`gh cao` does not expose `validate`. CI does not build or download
the Go extension or require consumer npm metadata. The internal runtime command
retains `--strict-warnings` for Actions callers that require warnings to fail.

Strict compilation runs against a committed temporary snapshot of the current
workflow files in the Actions checkout, without rewriting generated artifacts.
A monitoring-only installation with no top-level `.github/workflows/*.md`
sources skips compilation, but still checks for orphaned `.lock.yml` artifacts
and runs the other validation checks above. Existing Markdown sources always
undergo strict compilation, even when their frontmatter is missing or malformed.

CI writes its JSON report under `RUNNER_TEMP`, outside the checkout, before
uploading the `cao-validation` artifact. This keeps report creation from
triggering a false `gh aw doctor` warning and validation issue on a fresh
installation. Download the structured report with
`gh run download RUN_ID --name cao-validation`.

Exit code `0` means no validation errors, `1` means validation findings failed the requested threshold, and `2` means the validator itself could not complete. Validation never rewrites workflow artifacts; run `npm run compile:locks` to regenerate stale locks.

## Run and Watch a Campaign

There is intentionally no `cao run` command. gh-aw owns workflow execution:

```bash
gh aw run CAMPAIGN --ref BRANCH \
  --raw-field target_repo="OWNER/REPOSITORY" \
  --raw-field max_repos="1" \
  --raw-field rollout_percent="100" \
  --raw-field safe_output_mode="review"
```

CAO Activity and CAO Dashboard are conventional GitHub Actions workflows, not
Agentic Workflows, so operate them with GitHub CLI instead of `gh aw`:

```bash
gh workflow run cao-activity.yml
gh run list --workflow cao-activity.yml
gh run list --workflow cao-dashboard.yml
```

Then use GitHub CLI to find and watch the orchestrator:

```bash
gh run list --workflow CAMPAIGN.lock.yml --event workflow_dispatch --limit 5
gh run watch RUN_ID --exit-status
```

Manual inputs may narrow checked-in policy for one run; they never widen it.

## Operate the Hosted CLI Backend

The server binary includes a privileged operator interface for the hosted CLI
queue. Run it in the trusted executor environment with the same operational
store configuration as `execute-cli`:

```bash
cao-dashboard hosted-cli queue list --status queued --limit 50
cao-dashboard hosted-cli queue list --status queued --limit 50 \
  --continuation-token TOKEN_FROM_PREVIOUS_PAGE
cao-dashboard hosted-cli queue show COMMAND_ID
cao-dashboard hosted-cli queue cancel COMMAND_ID --actor OPERATOR_LOGIN
```

Queue output contains command summaries, not invocation values or process
output. List responses include `limit`, `hasMore`, and, when another page is
available, `nextContinuationToken`. Pass that opaque token to
`--continuation-token` with the same status filters to retrieve the next page.
The cursor follows command IDs and does not count or skip preceding rows.
Cancellation is an operator override that bypasses the dashboard
requester's live repository-permission check; it requires a durable audit store
and records the supplied operator login as a self-reported identity, not proof
of the caller's identity. Restrict database and executor environment access
accordingly. See
[Hosted CLI actions](../server/README.md#hosted-cli-actions) for the operator
workflow and SQLite debugging limitation.

For a single-worker development loop without a dispatcher, use
[Local Worker Debugging](local-debugging.md). Its request-only `debug` mode
is not a `gh cao campaigns mode` command or a campaign configuration.

CLI failures print one actionable message by default. Set `CAO_DEBUG=1` only
when diagnosing the CLI itself and an internal stack trace is needed.

## Inspect Activity

First download the JSONL shards and SQLite snapshot published by your deployed CAO dashboard:

```bash
gh cao download --url https://OWNER.github.io/CONTROL_REPO/cao/payload-hashes.json
```

The download remains local under `.cao/`. It is derived evidence, not rollout authority.
Use a trusted HTTPS source; redirects cannot downgrade HTTPS to HTTP.
For independent manifest integrity, pass `--manifest-sha256 SHA256` (or set
`DASHBOARD_MANIFEST_SHA256`) with a digest obtained through a separate trusted channel.
Checksums fetched alongside the snapshot detect corruption, not a compromised publisher
or TLS trust authority.

Use the gh-like query surface for common questions:

```bash
gh cao gh runs --repo OWNER/REPOSITORY --status failure --limit 20
gh cao gh issues --repo OWNER/REPOSITORY --since 2026-09-01
gh cao gh prs --repo OWNER/REPOSITORY --workflow WORKFLOW --limit 10
```

Check runtime health or query canonical records:

```bash
gh cao computation intelligence
gh cao computation intelligence --campaign dependabot --previous prior-intelligence.json --feedback decision-feedback.json
gh cao computation runtime-health --campaign dependabot
gh cao computation runtime-health --campaign dependabot --diagnose

gh cao query \
  --collection runs \
  --where conclusion=failure \
  --limit 20
```

For the computation model and result semantics, see
[Intelligence](intelligence.md),
[Runtime Health Computation](computation-runtime-health.md), and
[Portfolio Intelligence Computation](computation-intelligence.md).

The `intelligence` computation fingerprints canonical runtime-health evidence,
correlates matching failure signals, suppresses ineligible or recovering
candidates, and emits advisory Decisions. It never dispatches workers or grants
execution authority. Passing a prior result with `--previous` reuses unchanged
nonterminal Decisions. A versioned `--feedback` file binds dispositions to a
Decision ID and input fingerprint; unchanged terminal Decisions are suppressed,
while stale feedback remains inspectable and cannot affect changed evidence.

The result also includes partial Campaign intelligence contracts compiled from
declared canonical inventory. Undeclared problem, outcome, schedule, overlap,
attention, and operational-value fields remain `null`; descriptions and READMEs
are not reinterpreted as those contracts.

Feedback uses this shape:

```json
{
  "contractVersion": "1.0.0",
  "records": [{
    "decisionId": "runtime-health-decision:...",
    "inputFingerprint": "sha256:...",
    "disposition": "deferred",
    "observedAt": "2026-10-01T11:00:00Z",
    "actor": "operator:octocat",
    "authority": "control-repository-review"
  }]
}
```

Allowed dispositions are `accepted`, `rejected`, `deferred`, `superseded`,
`recovered`, `expired`, and `unresolved`. Feedback never grants execution
authority.

Render a named dashboard query as an editable agent prompt with the same template as the UI:

```bash
gh cao prompt cost-by-campaign
gh cao prompt campaign-runs --param campaign=dependabot --database .cao/gh-aw-logs.sqlite
```

Without `--database`, the command needs no downloaded snapshot and includes no evidence rows. With `--database FILE`, it executes the named query against that existing snapshot and includes a bounded preview; this does not grant rollout authority. Use `--dashboard FILE` to experiment with a different dashboard definition.

Run `node activity/cao.mjs repair-database --database snapshot.sqlite` (or the npm
`cao repair-database` bin) to repair a SQLite snapshot and apply retention. This
is **not a read-only check**: it creates a backup before repair and may prune
expired or orphaned records even when SQLite integrity is healthy. Existing
backup filenames retain the `.doctor-backup-` prefix. The default database is
`.cao/gh-aw-logs.sqlite`; `--ttl-days` defaults to 7 and `--run-ttl-days` defaults
to 30 when `--ttl-days` is omitted, otherwise it follows that explicit TTL.
Use `--ttl-days all --run-ttl-days all` to retain historical records without
age-based pruning; structural repairs still apply.

The JSON report identifies `command: "repair-database"` and retains the health,
before/after diagnostics, repair actions, errors, and backup path. Exit status
is 0 when healthy, 2 when the report remains unhealthy, and 1 for command errors.
The old Node `doctor` command is rejected, not aliased. This is different from
`gh aw doctor`, which validates the workflow installation and repository setup;
native `gh cao doctor` and `cao-dashboard doctor` are unchanged.

## Evaluate and Evolve

The CLI also exposes evidence used to improve campaigns:

| Command | Purpose |
| --- | --- |
| `node activity/cao.mjs operational-value` | Compute campaign-specific value evidence from the canonical snapshot. |
| `node activity/cao.mjs cluster-problems` | Cluster bounded problem evidence emitted by installed campaigns. |
| `node activity/cao.mjs dashboard-complexity --input FILE [--database FILE]` | Rank Dashboard Language queries by estimated computation pressure. |
| `node activity/cao.mjs prune-dashboard --input FILE` | Report reusable, redundant, and unreferenced dashboard queries and views. |

Without `--database`, `dashboard-complexity` normalizes every canonical database table to weight one. With `--database FILE`, it weights tables by their deployed row counts (normalized to the largest table) read from that downloaded snapshot, giving a more realistic computation-pressure ranking.

Data-pipeline and dashboard-maintainer commands are listed by `node activity/cao.mjs --help`. For the full data workflow, see [Dashboard data ingestion](dashboard-data-ingestion.md#use-local-sqlite).

## Review What Each Command Changed

`init`, `add`, `update`, `upgrade-gh-aw`, and `campaigns mode` can change checked-in control-plane files. Review those changes and commit workflow sources, generated locks, and `.github/workflows/cao.json` together:

```bash
git status --short
git diff -- .github
```

Never edit generated `.github/workflows/*.lock.yml` files by hand. Never treat a successful command as permission to widen scope or enable live output.
