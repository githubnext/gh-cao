# Setup implementation details

Load this reference only to inspect installer behavior, runtime layout, source
versus generated files, or steering files.

The primary operator path is `gh extension install githubnext/gh-cao`, then
`gh cao setup` from an existing control checkout.

> [!TIP]
> If GitHub CLI extension installation fails because of SAML token authorization,
> use `curl -fsSL https://raw.githubusercontent.com/githubnext/gh-cao/main/install-gh-cao.sh | bash`.
> The fallback requires Bash, curl, and Node.js 24+ and invokes the upstream
> `github/gh-aw` installer for gh-aw. Public tool downloads are anonymous without
> changing stored GitHub credentials.

Native setup prepares missing
runtime through gh-aw in an isolated checkout and verifies the selected stable
release's exact-SHA attested archive and file inventory before preflighting
local destinations. It adds missing files without replacing existing content,
preserves unrelated files and policy, and refuses malformed policy or conflicts.
Complete installations are reused, not upgraded. Missing gh-aw is installed at
the catalog minimum; older or broken existing installations require explicit
repair. Setup constructs missing baseline policy through the same helper as
optional policy-only `gh cao init`, saving it only after external setup succeeds.
Native setup itself needs no local Node.js or separate init.
Repository creation belongs to `gh repo create` or the installer's explicit
`--create-repo`; cloning can use `gh repo clone`. Neither path authorizes
agent execution.

After configuration, interactive setup can separately offer reviewed publication
with a default-No prompt. A temporary index supplies the file summary, policy
diff, and full review diff without touching real staging. Only exact files from
this setup's runtime/package plan and policy are eligible. An empty real index,
matching single origin, synchronized default branch, and unchanged reviewed
Git/file state are required; existing local commits, feature branches, or
uncommitted runtime from earlier setup require manual publication. Normal Git
commit/push respects identity, hooks and protections, never forces, and includes
no extra tags/submodule pushes. A failed push leaves the commit local.
Success returns deployment-pending dashboard/Actions links without dispatching,
polling or waiting. Declining/cancelling, CI and unattended setup never publish.

The root `install-gh-cao.sh` downloads and verifies the platform-specific Go extension
from a published release, installs or upgrades the required gh-aw version,
installs the root CAO package when runtime files are absent, materializes the runtime,
and runs `gh cao init` only when policy is absent. Actions invoke Node entry points directly.
It preserves an existing `.github/workflows/cao.json`.
Tools, Node.js 24+, sign-in, and checkout-root readiness are checked before
installation or upgrade. `--create-repo OWNER/NAME` explicitly creates and
clones a private repository, refusing an existing clone directory. Bare
installation stops and prints `gh cao setup`; `--setup` opens guided setup
directly on the controlling terminal. Without a terminal or when `CI` is set,
setup applies only the exact public-control-repository profile with
repository-workflow-token authentication, public Actions-backed Pages, no
campaigns, and exact self-only Activity scope. Private/internal repositories or
incompatible policy fail closed. Repository creation requires an explicit exact
coordinate, and an outdated installed gh-aw fails with manual-upgrade guidance. Other streamed
children retain null stdin.
The repository-root `aw.yml` explicitly composes Activity, dashboard,
validation, shared runtime resources, instructions, and project settings. Empty
`skills` and `agents` declarations keep Agent Plugin and repository-only
authoring resources out of control-repository installation.

The producer uses the dedicated `runtime/package.json` `files` field and
`npm pack --ignore-scripts`, bundles the YAML parser with esbuild and its license
notice, preserves the dependency-free prompt adapter as portable source, and
seals a revision-bound file inventory. Explicit source packing preserves the
same prompt source. The materializer downloads
the prebuilt archive matching gh-aw's full `resolvedCommit`: stable `vX.Y.Z`
references use release assets, while main/SHA references use successful,
unexpired `cao-runtime.yml` Actions artifacts retained for 90 days.
Before extraction it verifies signed provenance for the catalog repository,
signer workflow, source and signer SHA, and GitHub-hosted runner. It validates
the file inventory, checksums and owned destinations before replacement.
Missing or unverifiable archives stop installation without another-revision
or source/npm fallback. Operators need Node.js and authenticated GitHub CLI
artifact/attestation access on github.com, not npm; explicit developer source
materialization and dashboard builds retain their existing npm requirements.
Consumer-owned root package metadata is preserved. Tests, fixtures,
screenshots, development configuration, and catalog component manifests are not
installed. The Agent Plugin, portable skills, Canvas extension, and bundled
specifications are separate client installations, not control-plane resources.
Updates prune development files inside the owned `activity/` and `dashboard/`
directories. Previously copied plugin files outside those directories remain
untouched; review them separately rather than deleting client-owned resources
as part of a runtime update.

Guided setup separates Repositories, Connections, and Review/apply. Arrow-key
prompts validate input inline; terminals without styling support, `NO_COLOR`,
and `ACCESSIBLE` select plain numbered prompts without animation.
Confirmation defaults to No. After confirmation, the Pages source is verified
without changing visibility. Public dashboards require separate default-No
consent before credentials are configured. Declining or cancelling that consent
removes only a newly created site where possible; existing sites remain with
restriction or unpublication guidance. Policy is saved atomically afterward.
Failures report partial external effects honestly; rerun setup after fixing
the reported problem. The default CLI output gives next actions; `setup --json`
keeps structured stdout. Setup never installs a campaign or dispatches a workflow;
commit/push requires the separate interactive approval above.

Canonical runtime files live together under `.github/workflows/shared/`,
including `control.md`, `control.mjs`, `policy.mjs`, the policy schema, the
materializer, and authentication helpers. Operator commands use `gh cao`;
Actions invoke `gh cao COMMAND` directly, without a shell launcher.
Do not fetch a second runtime copy or construct one ad hoc.

Editable workflow sources are `.github/workflows/*.md`; `.lock.yml` workflows
are generated and must be changed only by compiling their sources. Campaign
records and installed package sources are campaign-owned and remain untouched
during setup.

Preserve consumer-owned root `AGENTS.md`. CAO-specific consumer guidance belongs
in `.github/cao/instructions.md`; the gh-aw overlay under
`.github/aw/instructions.md` points to it. Campaign-specific steering belongs in
`.github/cao/<campaign-slug>.md` only after a campaign exists and the user asks
for custom prompting, so setup must not create it.

Use [CAO Commands](https://github.com/githubnext/gh-cao/blob/main/docs/cao-cli.md) for current command behavior and
the installed `gh cao --help` output for operator syntax. `gh cao --help`
lists the retained pipeline tools.
