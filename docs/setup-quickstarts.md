---
title: Set Up CAO
description: Open a local dashboard with authenticated GitHub CLI and no cloud deployment, or set up shared monitoring.
---

Start with existing GitHub Agentic Workflow evidence on your own machine.
Shared monitoring and automation are separate, optional steps.

## Open a local dashboard (no deployment)

Use an installed CAO release, your **locally authenticated GitHub CLI**, and a
running local Podman engine (on macOS, `podman machine start`). The command automatically pulls PostgreSQL,
starts or reuses its owned loopback-only container, provisions canonical and
operational storage, and configures a separate non-owner runtime login.
Podman is the only managed engine; the command never falls back to another
engine and fails closed when local Podman is unavailable.

Persistent data and private local credentials live under
`.github/cao/dashboard/<repository-scope>/postgres/`; the PostgreSQL data directory
is bind-mounted into the container. The cache is added to `.gitignore`.
Image layers remain in the container engine. `--cache-dir` overrides the cache.
The container and data are retained for subsequent dashboard sessions.

To use your own database instead, pass `--postgres-url` or set `CAO_POSTGRES_URL`.
That connection bypasses container management and automatic provisioning;
provision its exact printed namespace using the
[PostgreSQL setup contract](https://github.com/githubnext/gh-cao/blob/main/server/POSTGRES.md).
The query server itself never gains bootstrap credentials. No cloud deployment,
Azure setup, Redis, or separate Node.js, npm or gh-aw installation is required.
The release includes the Go server, built dashboard and pinned gh-aw log collector.

Verify GitHub CLI authentication, install the extension, and select the
GitHub.com repository you can read:

```bash
gh auth status
# If needed:
gh auth login
gh extension install githubnext/gh-cao
gh cao dashboard --repo OWNER/REPOSITORY
```

In an existing repository checkout, `gh cao dashboard` resolves the current
repository instead. Repeat `--repo OWNER/REPOSITORY` to select up to **20 exact
GitHub.com repositories**. A checkout is unnecessary when `--repo` is explicit.
No `gh cao setup`, policy file, repository enrollment, campaign, or CAO Actions
installation is required.

**Open and stop:** Keep the command running in the terminal. After initial
collection, open the full **Dashboard:** URL it prints in your browser; the
command also prints the **PostgreSQL namespace**. The Go server binds to loopback
on an available local port and evaluates the normal dashboard queries against
canonical PostgreSQL on your machine, not a hosted service or browser IndexedDB.
Press **Ctrl-C** in that terminal to stop the server and collection. The namespace
remains for later sessions with the same repository scope.

**Online evidence, local serving:** GitHub remains the authenticated upstream
source for repository, workflow, run, and available artifact evidence.
Collection uses your local `gh` identity and requires network access and
repository permissions. No cloud deployment does **not** mean offline
acquisition or bypassing GitHub access controls.

**Automatic, bounded refresh:** The server starts immediately; collection runs
in background passes separated by **five minutes** while the command runs.
The recent evidence window defaults to **seven days** (maximum `168h`), while
server lifetime defaults to, and cannot exceed, **48 hours** per invocation.
History checkpoints survive restarts, and admitted pages remain in PostgreSQL
inside the selected window. The header's data-status tooltip shows ingestion progress, quota headroom,
pauses and errors. A shared 100-point core API pass ceiling reserves at least
1000 points or 20% of core quota for other work.
For example, `gh cao dashboard --repo OWNER/REPOSITORY --window 12h --duration 2h`
narrows both. The default count is **200** per repository for each of the
recent-run lookup and cached usage sample, not a promise of complete history.
Only agentic `.lock.yml` workflows are included. Acquisition budgets, API
access, and artifact availability can leave evidence partial or unavailable;
ordinary Actions jobs and missing evidence must not look like healthy zeros.
A failed refresh reports the error and retains the last accepted PostgreSQL revision.

**Optional reuse:** Existing local setup can stay in place, but the command
does not require or adopt its CAO policy as collection scope; use the current
repository or explicit `--repo` selections. For a single repository, an existing
`.github/aw/logs` artifact cache is reused automatically, or select one with
`--logs-dir PATH`. Otherwise caches live in your user cache directory, keyed by
repository scope. `--cache-dir PATH` selects a separate private dashboard cache;
do not point it at an existing Activity database. Cache files can contain
private evidence: protect their permissions and do not publish them.

See the [command reference](cao-cli.md#on-demand-local-dashboard) for all flags,
acquisition limits, and separately approved maintenance actions. Starting the
dashboard does not change policy, workflows, or remote repository state.

## Set up shared monitoring (optional)

The remaining walkthrough prepares a control repository for monitoring in
GitHub Actions. It is **not a prerequisite** for `gh cao dashboard`.

**Estimated time: 15 minutes**; approval and deployment vary.

**Monitoring only:** no campaign installation or new agentic work.

This is one monitoring path. For private targets, PATs, enterprise hosts, and
other topologies, see [setup combinations](setup-compatibility.md).

<details>
<summary>Prepare a control repository for shared monitoring.</summary>

## Prerequisites

- **Tools:** Git and [GitHub CLI](https://cli.github.com) 2.68+.
  Installer alternative: Bash, curl, Node.js 24+ (Git Bash on Windows).
- **Logged in to GitHub CLI:** verify with `gh auth status` and run
  `gh auth login` if needed.
- **Repository:** exact `OWNER/REPOSITORY`; Agentic Workflow history is optional.

Use a **private control repository** for private or internal evidence. Production
dashboard access belongs to the authenticated [hosted server](deployment.md).
Pages is synthetic-only; setup does not configure Pages or request public
publication consent.

## Step 1 - Install CAO

### Extension-first quickstart

On GitHub.com:

```bash
gh extension install githubnext/gh-cao
gh cao --version
```

Create or clone a clean default-branch control checkout.
Native setup prepares missing runtime, policy, and gh-aw without a separate
installer or local Node.js.

:::tip[Installation and authentication]
If authentication fails:

```bash
gh auth login
gh auth status
```

Sign-in is required for extension installation and control-repository access.
:::

<details>
<summary>Create or clone a control checkout.</summary>

```bash
gh repo create OWNER/CONTROL_REPOSITORY --private --clone
cd CONTROL_REPOSITORY
```

Use `--public` only for public evidence.

To use an existing remote repository instead:

```bash
gh repo clone OWNER/CONTROL_REPOSITORY
cd CONTROL_REPOSITORY
```

</details>

<details>
<summary>Public-only setup example.</summary>

Create a **public** control with
`gh repo create OWNER/CONTROL_REPOSITORY --public --clone`, then enter `OWNER/*`
at **Repositories**, review the exact names, and approve
commit-and-push. All evidence must be public. Interactive wildcards containing
private/internal targets are rejected for public controls, not silently trimmed.

</details>

<details>
<summary>Installer alternative (Node.js 24+).</summary>

The root installer installs **gh-aw**, the **`gh cao` GitHub CLI extension**,
and baseline monitoring runtime and policy.

:::tip[SAML-token installation failure]
If SAML authorization blocks extension installation, use the curl fallback
at the control checkout's root:

```bash
curl -fsSL https://raw.githubusercontent.com/githubnext/gh-cao/main/install-gh-cao.sh | bash
```

Public tool downloads are anonymous;
stored credentials and runtime-provenance access are unchanged.
[Installer details](cao-cli.md#control-plane-installer).
:::

Existing policy is preserved. No campaign is added or run.

</details>

<span id="guided-setup-instead"></span>

## Step 2 - Connect one repository

In an **interactive terminal** at the **control repository's root**:

```bash
gh cao setup
```

Setup prepares missing runtime and policy, reuses existing installations, and configures scope,
and authentication. No separate `init` is needed.
See [details](cao-cli.md#guided-runtime-preparation).

1. At **Repositories**, enter your exact `OWNER/REPOSITORY`.
   The control stays included. Comma-separated entries and `OWNER/*` also work:
   review the exact expanded names. Future repositories are not enrolled.
2. Choose **Authentication** only if prompted. Confirmed public targets
   automatically use `public-only` with Actions' `GITHUB_TOKEN`, no App/PAT,
   even when the control repository is private. Private/internal remote targets require credentials;
   see [Authentication](authentication.md#choose-cross-repository-authentication).
3. Review the exact read scope, output scope, and authentication, then confirm
   **Apply this setup?** This grants no dashboard publication authority.
4. Store secrets in Actions, never in policy or committed files.
   Credentials do not expand scope.
5. Review the one-line change summary; open the full-diff path for details.
   **“Commit and push these CAO monitoring changes? [y/N]”** optionally publishes
   reviewed CAO files and returns **deployment pending** links immediately.

No campaigns or agentic work are installed. Publication requires a synchronized
default branch, matching origin, and empty index; no protection bypass.
Declining, blocked, or unattended setup stays local.

If setup stops, inspect its error, local files, and credential changes
before retrying; do not publish incomplete setup. For unattended public-only
setup, see [Authentication](authentication.md#public-only-monitoring-quickstart).

<span id="step-3---review-and-publish-your-configuration"></span>

## Step 3 - Publish and open your dashboard

Check installation:

```bash
gh cao doctor
```

If setup published, follow its Actions link. Otherwise review and publish:

```bash
git status --short
git diff -- .github
```

Check that `.github/workflows/cao.json` names only the intended repositories
and that `control-plane.campaigns` is `{}`. Keep `control-plane.workflows` and
`control-plane.operations` absent or `{}` too: monitoring does not enroll
execution. Do not publish private evidence to a public dashboard.

Include only setup-created files, never unrelated files or secrets:

```bash
git add .github activity dashboard
git diff --cached --stat
git commit -m "Set up CAO monitoring"
git push
```

Check **CAO validation** in Actions for full policy and workflow validation.
The installation prepares shared runtime tools; it does not install scheduled
Activity or dashboard artifact publication workflows.

<span id="4-open-your-dashboard"></span>
<span id="step-4---wait-for-collection-and-inspect-your-dashboard"></span>

Open the authenticated hosted dashboard, or prepare an explicit operational
canonical JSONL for local Go/PostgreSQL ingestion. See [Agent analysis](agent-analysis.md).
Missing evidence remains unavailable, not proof of zero cost or success.
CAO monitors Agentic Workflows, not every ordinary Actions job.
The [sample dashboard](https://githubnext.github.io/gh-cao/cao/) is synthetic
and cannot verify operational results.

## Optional: add automation later

Monitoring is enough. For automation, [derive the simplest suitable structure](operation-patterns.md)
from your outcome and scope.

For optional automation, try the **[Daily Repo Status quickstart](setup/quickstart.md)**:
one bounded control-repository report.
Other choices: [Direct operation](authority-envelopes.md) for supplied targets,
[Standalone fleet workflow](configuration.md#standalone-agentic-workflows) for fleets,
[campaign](https://githubnext.github.io/gh-cao/catalog/) for independent workers,
or ordinary GitHub Actions for deterministic work.

None is enrolled, enabled or run by setup. Installation is not execution:
review and commit workflows and policy together before enabling them. Start in
**review** mode with **one repository**; model access and AI credits are separate
prerequisites. See [Roll out a campaign](rollout-and-routing.md).

</details>

The [Repo Assist walkthrough](repo-assist-quickstart.md) demonstrates a review-only campaign.

## Continue learning

- [Explore the dashboard](dashboard.md).
- [Configuration](configuration.md): understand your policy.
