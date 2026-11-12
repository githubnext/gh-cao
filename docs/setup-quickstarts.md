---
title: Set Up CAO
description: Set up CAO, monitor one repository, and explore your dashboard before adding automation.
---

Monitor existing GitHub Agentic Workflow evidence in one repository.

**Estimated time: 15 minutes**; approval and deployment vary.

**Monitoring only:** no campaign installation or new agentic work.

This is one monitoring path. For private targets, PATs, enterprise hosts, and
other topologies, see [setup combinations](setup-compatibility.md).

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
snapshot for local Go/SQLite serving. See [Agent analysis](agent-analysis.md).
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

The [Repo Assist walkthrough](repo-assist-quickstart.md) demonstrates a review-only campaign.

## Continue learning

- [Explore the dashboard](dashboard.md).
- [Configuration](configuration.md): understand your policy.
