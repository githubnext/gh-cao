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

## Open a local dashboard

Use your locally authenticated [GitHub CLI](https://cli.github.com) to install
the release extension and inspect a repository:

```bash
gh auth status
gh extension install githubnext/gh-cao
gh cao dashboard --repo OWNER/REPOSITORY
```

No CAO cloud deployment, Azure setup, hosted dashboard, Redis, or separate
Node.js or gh-aw installation is required. An installed local Podman engine (on
macOS, after a one-time `podman machine init`; a stopped machine is started automatically) provides PostgreSQL automatically unless a connection is specified. From an
existing repository checkout, use `gh cao dashboard` without `--repo`.
No `gh cao setup` or control-repository configuration is needed.

Open the **Dashboard:** URL printed in the terminal. The bundled Go server
serves on loopback and queries local PostgreSQL. Managed data is bind-mounted
under the ignored `.github/cao/dashboard/<scope>/postgres/` cache.
Podman is the only managed container engine; `--postgres-url` or
`CAO_POSTGRES_URL` bypasses container management and uses provisioned storage.
Collection uses your GitHub CLI identity to read GitHub evidence and refreshes
automatically every five minutes: **no cloud deployment does not mean offline**.
The recent evidence window and session lifetime default to, and cannot exceed,
48 hours; collection is bounded, not a complete historical backfill.
Press **Ctrl-C** to stop serving and collection while retaining the cache.
See the [local dashboard quickstart](docs/setup-quickstarts.md#open-a-local-dashboard-no-deployment)
for scope, cache reuse, and evidence limits.

## Set up shared monitoring when ready

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

Use a private control repository for non-public evidence. Production dashboards
use same-origin Go/PostgreSQL hosting; local development uses the same Go server
over a verified canonical JSONL artifact ingested into PostgreSQL. GitHub Pages hosts only the **Synthetic enterprise
demo**, with the same canonical pipeline, queries, and views but no live services.
Setup does not configure Pages.

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
  [Actions artifacts and the Pages demo](docs/deployment-actions.md)
- [Dashboard evidence](docs/dashboard.md) and [agent analysis](docs/agent-analysis.md)
- [Operations, updates, and recovery](docs/operations.md)
- [Documentation site](https://githubnext.github.io/gh-cao/) and
  [compact agent context](https://githubnext.github.io/gh-cao/llms-small.txt)

<span id="contributing"></span>
<span id="maintainers"></span>
<span id="support"></span>

Want to contribute? Read [Contributing](CONTRIBUTING.md) and follow the
[Code of Conduct](CODE_OF_CONDUCT.md). For help, check [Support](SUPPORT.md).
Report vulnerabilities privately through [Security](SECURITY.md).

Maintained by [GitHub Next](https://github.com/githubnext).

**Acknowledgements:** CAO's service design principles are inspired by SAP's
[Calesi pattern for CAP-Level Service Integration](https://cap.cloud.sap/docs/guides/integration/calesi):
domain-level interfaces, protocol-independent consumption, fast local development,
interchangeable implementations, and deliberate extensibility.

Licensed under the [MIT License](LICENSE).
