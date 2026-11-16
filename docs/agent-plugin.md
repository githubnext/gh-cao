---
title: Agent plugin
description: Install CAO's portable agent skills and dashboard query tools.
---

The CAO plugin supplies agent guidance and read-only Dashboard Language tools.
It is separate from both the `gh cao` extension and the monitoring runtime
installed in a control repository. Installing the plugin does not enroll
repositories, configure credentials, or authorize workflow execution.

## Install the plugin

Install `githubnext/gh-cao` through an
[Agent Plugins-compatible client](https://agent-plugins.org/compatible-clients)
and enable it in that client. Install the complete plugin bundle:
`com.github.copilot/extensions/` is not automatically discovered as a
repository-local extension, and its extension folder is not a standalone package.

Use the `central-agentic-ops` skill for every CAO task. It is a lightweight
router: it loads the shared `.github/cao/instructions.md`, then only the task
guide that matches the request.

| Task | Guide under `.github/cao/instructions/` |
| --- | --- |
| Bootstrap a control plane | `setup.md` |
| Discover or install an existing campaign | `operations.md` → `operations/add-campaign.md` |
| Describe an outcome, scope, and execution requirements for new work | `operations.md` → `operations/create-campaign.md` |
| Select or extend `gh cao` commands | `gh-cao.md` |
| Author, compile, or inspect gh-aw workflows | `gh-aw.md` |
| Inspect GitHub state with `gh` | `gh.md` |
| Answer questions from dashboard queries and activity evidence | `dashboard-analysis.md` |
| Diagnose a deployment failure and preserve evidence | `diagnostics.md` |

The skill source is `skills/central-agentic-ops/`; control repositories receive
it at `.github/skills/central-agentic-ops/` and the guides through the CAO
runtime. The separate `agentic-workflows` skill remains the gh-aw router.

The skill also installs standalone with `gh skill install githubnext/gh-cao
central-agentic-ops`. That copies only `SKILL.md`; when a guide is absent
locally, the skill fetches it from `raw.githubusercontent.com` at the installed
`metadata.github-ref`, or `main` when none is recorded.

For human setup instructions, start with [Set Up CAO](setup-quickstarts.md).
For shell or MCP analysis, see [Agent analysis](agent-analysis.md).

## Dashboard tools

The extension exposes `cao_dashboard_read_data_specification` for bounded
specification reads and `cao_dashboard_execute_query` for evaluating Dashboard
Language over explicitly supplied source rows. These tools use bundled resources;
when developing a newer checkout, check that revision's specification and
implementation rather than assuming the installed plugin is current.

The plugin registers no canvas and starts no dashboard, HTTP server, acquisition,
or command executor. It requests no GitHub credentials. Use dashboard authoring
skills and query/specification tools from the conversation.

## Open the dashboard

Run `gh cao dashboard` and open its private loopback
URL in a browser. The native CLI owns bounded acquisition, managed PostgreSQL,
cached canonical evidence, and scoped maintenance after dashboard confirmation.
See [the dashboard guide](dashboard.md) for prerequisites and collection limits.
The plugin does not manage that process or approve its commands.

## Maintain the plugin

Run `npm run typecheck:copilot`, `npm run check:agent-plugin`, and the focused
`tests/unit/dashboard-extension.test.mjs` and
`tests/unit/dashboard-bundled-resources.test.mjs` tests after extension changes.
They cover registered tools, absence of canvas/credential capabilities, and
bundled-resource isolation. See
[Contributing](https://github.com/githubnext/gh-cao/blob/main/CONTRIBUTING.md)
for the remaining repository checks.
