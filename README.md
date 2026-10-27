# Central Agentic Ops

<span id="project-status-and-scope"></span>

> [!NOTE]
> **Research prototype:** Features and interfaces may change.

> [!TIP]
> **Agents:** Use the [setup skill](skills/setup-cao/SKILL.md) to create a control
> plane and the [debug skill](skills/debug-cao/SKILL.md) when CAO fails.

Central Agentic Ops (CAO) brings GitHub Agentic Workflow runs, costs, and
outcomes into one dashboard, then adds policy-governed execution when you are
ready. It operates from a control repository across explicitly enrolled
repositories. CAO does not replace repository permissions, branch protection,
or human review.

## Start safely

Install the extension:

```bash
gh extension install githubnext/gh-cao
```

Git and authenticated GitHub CLI 2.68+ are required. Need a repository?
Create one with `gh repo create OWNER/REPOSITORY --private --clone`, then
`cd REPOSITORY`.

From the repository root:

```bash
gh cao setup
```

Setup makes this the control repository by preparing the monitoring runtime,
policy, and gh-aw. It does **not** install or run a campaign, and it keeps
changes local unless you separately approve commit and push. A published setup
reports **deployment pending**; an empty dashboard is normal until Agentic
Workflow evidence exists.

Use a private control repository for non-public evidence, but verify dashboard
visibility before publishing: a private repository does not guarantee private
GitHub Pages.

> [!TIP]
> **SAML-token installation failure:** Use the
> [curl installer](docs/cao-cli.md#control-plane-installer):
> `curl -fsSL https://raw.githubusercontent.com/githubnext/gh-cao/main/install-gh-cao.sh | bash -s -- --setup`.

Follow [Set Up CAO](docs/setup-quickstarts.md) for authentication, scope,
publication, and your first dashboard visit.

## Add work when ready

Start with one repository in `review`; live outputs require explicit reviewed
policy and declared safe-output capabilities. Describe the outcome and scope
first—campaigns are one way to scale work, not a prerequisite. See
[From Outcome to Execution](docs/operation-patterns.md) and the
[campaign catalog](https://githubnext.github.io/gh-cao/catalog/).
You can also inspect [verified Repo Assist examples](repo-assist/README.md#see-repo-assist-in-action)
without running more work.

For agents, install the [CAO plugin](docs/agent-plugin.md) and use the task skill:
`setup-cao`, `debug-cao`, `add-cao-campaign`, `create-cao-campaign`,
`analyze-cao`, or `cao-cli`.

## Read the docs

<span id="github-cli-extension"></span>
<span id="github-pages-setup"></span>
<span id="agent-analysis"></span>
<span id="agent-plugin-and-requirements"></span>

- [What CAO is and its safety boundaries](docs/architecture-at-a-glance.md)
- [CLI commands](docs/cao-cli.md), [authentication](docs/authentication.md), and
  [deployment](docs/deployment.md), including
  [GitHub Actions and Pages](docs/deployment-actions.md)
- [Dashboard evidence](docs/dashboard.md) and [agent analysis](docs/agent-analysis.md)
- [Operations, updates, and recovery](docs/operations.md)
- [Documentation site](https://githubnext.github.io/gh-cao/) and
  [compact agent context](https://githubnext.github.io/gh-cao/llms-small.txt)

<span id="contributing"></span>
<span id="maintainers"></span>
<span id="support"></span>

## Project

- Want to contribute? Read [Contributing](CONTRIBUTING.md).
- Help keep the community welcoming: follow the
  [Code of Conduct](CODE_OF_CONDUCT.md).
- Need support? Check out [Support](SUPPORT.md).
- Report vulnerabilities privately through [Security](SECURITY.md).

Maintained by [GitHub Next](https://github.com/githubnext).

Licensed under the [MIT License](LICENSE).
