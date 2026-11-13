---
title: Agent plugin and Copilot canvas
description: Install CAO's portable agent skills and open the local gh cao dashboard in Copilot.
---

The CAO plugin supplies agent guidance and a local dashboard canvas. It is
separate from both the `gh cao` extension and the monitoring runtime installed
in a control repository. Installing the plugin does not enroll repositories,
configure credentials, or authorize workflow execution.

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
| Describe an outcome, scope, and execution requirements for new work | `create-cao-campaign` (checks whether a campaign is needed first) |
| Download and query activity evidence | `analyze-cao` |
| Select or extend CLI commands | `cao-cli` |

For human setup instructions, start with [Set Up CAO](setup-quickstarts.md).
For shell or MCP analysis without a canvas, see [Agent analysis](agent-analysis.md).

## Open a Copilot canvas

Open the **Central Agentic Ops** canvas for the current repository or an explicit
`OWNER/REPOSITORY`. You need:

- GitHub CLI authentication and an installed `gh-cao` release that supports
  `gh cao dashboard --json --exit-on-stdin-close`.
- A running Docker or Podman engine, unless `CAO_POSTGRES_URL` selects an
  already provisioned PostgreSQL runtime connection.

The canvas runs the installed `gh cao dashboard` in the session's working
directory, passing `--repo` only when explicitly selected. The CLI acquires
bounded recent activity, manages local PostgreSQL when needed, and serves the
shared Go dashboard on loopback. No prepared `.cao/` snapshot, Go compiler,
plugin-built server, or staged site is required. Startup failures are reported;
there is no legacy canvas-host or browser IndexedDB fallback.

The CLI retains its private canonical evidence and PostgreSQL cache under
`.github/cao/dashboard/<scope>/` between sessions. Collection is read-only
against the selected repositories and grants no rollout or remote execution
authority. Coverage remains partial until the bounded background passes acquire
more evidence. See [the dashboard guide](dashboard.md) for collection limits.

Keep the capability URL private; do not expose or forward it to other users.
The canvas validates the CLI's JSON readiness URL and checks that the loopback
page responds before opening it. Diagnostics and capability URLs are not
forwarded to the extension's JSON-RPC output.

### Code and process boundary

The canvas executes the installed `gh` command, never server code or site assets
from the active checkout. It does not build the plugin's bundled server sources.
Keep the installed CLI current independently of plugin updates.

The dashboard stays available between completed, interrupted, or failed agent
turns. Closing the canvas, exiting the session, or stopping/reloading the
extension closes the CLI's stdin and waits for shutdown. Owner-process exit
also closes that pipe. The CLI stops its Go server and collection work while
retaining managed PostgreSQL and cached evidence for reuse.

The extension exposes `cao_dashboard_read_data_specification` for bounded
specification reads and `cao_dashboard_execute_query` for evaluating Dashboard
Language. These tools still use bundled resources; when developing a newer
checkout, check that revision's specification and implementation rather than
assuming the installed plugin is current.

## Design queries in a Copilot canvas

The native dashboard does not expose the former canvas-only **Query editor**,
custom-view persistence, or Copilot session handoff. Use the plugin's dashboard
authoring skills and query/specification tools from the conversation instead.
This keeps dashboard serving and acquisition owned by the installed CLI rather
than a separate Node integration host.

## Run approved commands

The embedded dashboard uses the native CLI's maintenance actions and approval
checks. Commands still require the CLI's trusted
interactive-terminal approval. If the extension has no controlling terminal,
approval fails explicitly: start `gh cao dashboard` in an interactive terminal
or review and run the copied command separately.

Canvas access alone does not approve commands or widen CAO rollout policy.

## Compiler-version evidence

Compiler and release observations come from the native CLI's bounded acquisition
profile. Missing or stale evidence is not proof of a current installation.
The canvas does not run separate GitHub discovery or rewrite snapshot evidence.

## Maintain the plugin

Run `npm run typecheck:copilot`, `npm run check:agent-plugin`, and the focused
`tests/unit/dashboard-extension.test.mjs` and
`tests/unit/dashboard-local-preview.test.mjs` tests after extension changes.
They cover SDK declarations, registered tools, CLI arguments, readiness failures,
workspace isolation, and process cleanup. See
[Contributing](https://github.com/githubnext/gh-cao/blob/main/CONTRIBUTING.md)
for the remaining repository checks.
