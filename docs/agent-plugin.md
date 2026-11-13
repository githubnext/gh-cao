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

Use the portable skill for the task:

| Task | Skill |
| --- | --- |
| Bootstrap a control plane | `setup-cao` |
| Diagnose a deployment failure and preserve evidence | `debug-cao` |
| Discover or install an existing campaign | `add-cao-campaign` |
| Describe an outcome, scope, and execution requirements for new work | `create-cao-campaign` |
| Download and query activity evidence | `analyze-cao` |
| Select or extend CLI commands | `cao-cli` |

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

Run `gh cao dashboard` in an interactive terminal and open its private loopback
URL in a browser. The native CLI owns bounded acquisition, managed PostgreSQL,
cached canonical evidence, and scoped maintenance with trusted terminal approval.
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
