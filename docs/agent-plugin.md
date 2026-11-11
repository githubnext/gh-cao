---
title: Agent plugin and Copilot canvas
description: Install CAO's portable agent skills and use the local Copilot canvas for query authoring and approved commands.
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

In Copilot CLI, open the **Central Agentic Ops** canvas for the current
repository or an explicit `OWNER/REPOSITORY`. You need:

- GitHub CLI authentication with Actions read access to that repository.
- A current `cao-activity-index` artifact from a successful default-branch
  `cao-activity.yml` run, including `payload-hashes.json` and `gh-aw-logs.sqlite`.
- Go compatible with the bundled server module, or `CAO_DASHBOARD_EXECUTABLE`
  set to an absolute path to a compatible `cao-dashboard` binary.

The canvas downloads and checksum-verifies the snapshot, then starts the shared
Go server on loopback. Go serves assets, declarative queries, pagination,
diagnostics, repository memory, and revision subscriptions behind one capability
URL. Missing, corrupt, or unsupported snapshots stop startup. There is no canvas
backend selector, Node query engine, or browser IndexedDB fallback.
Synthetic manifests and manifests without a SQLite checksum are rejected.
Pages is not an acquisition source. Dashboard backlinks require an explicit
`caoUrl` or `CAO_DASHBOARD_URL`; the canvas does not discover a repository's
Pages URL.

Keep the capability URL private; do not expose or forward it to other users.
For ordinary local serving or the synthetic-only Pages/IndexedDB demo instead,
see [Choose a local serving mode](dashboard-data-ingestion.md#choose-a-local-serving-mode).

### Code and process boundary

The canvas uses the installed plugin's server, query engine, specification, and
site. It never imports or compiles executable code from the active checkout.
Workspace campaign documents remain JSON data. If necessary, the bundled site
is staged in a temporary workspace directory without relaxing server path
restrictions. The private Node bridge handles Copilot integration, JSON
composition/live reload, local-user GitHub reads, and approved commands only.

The preview stays available between completed, interrupted, or failed agent
turns. Closing the canvas, exiting the session, or stopping/reloading the
extension stops the Go child and cleans up temporary assets. Owner-process
exit also terminates the child through its stdin lifetime.

The extension exposes `cao_dashboard_read_data_specification` for bounded
specification reads and `cao_dashboard_execute_query` for evaluating Dashboard
Language. Use these before making data-model assumptions or introducing custom
query semantics. They use bundled resources; when developing a newer checkout,
also check that revision's [data specification](https://github.com/githubnext/gh-cao/blob/main/specs/dashboard-data.md)
and implementation rather than assuming the installed plugin is current.

## Design queries in a Copilot canvas

The **Query editor** under **Experimental** is canvas-only, not part of the
static website, hosted server, or ordinary local preview. Enter intent, subject,
optional objective, and acceptance criteria, then select **Generate query and
view**. The Copilot SDK uses the bundled `generate-dashboard-ir` skill and
Dashboard Language specification to produce a complete query and declarative view.

The sparkle button, **Improve all fields with Copilot**, uses the bundled
`author-dashboard-intent` skill to refine all four fields together. Review its
suggestions before generation. Edits made while enhancement is pending are not
overwritten. Subject, objective, and acceptance have a **512-character combined
limit**, counted as Unicode characters; keep extra detail in intent. The
sparkle action can condense an overlong draft, but its result must meet the limit.

The browser data worker validates the document; Go executes the query against
canonical SQLite data without opening browser IndexedDB. Accepted previews
remain subscribed to data changes. Invalid documents show diagnostics without
replacing the previous preview. Generation can feed validation errors and the
rejected draft back to Copilot for at most ten attempts per click. Each attempt
uses AI credits. **Cancel** stops pending generation or validation.

**Save as custom view** stores an accepted document in
`.cao/dashboard/custom-views/<id>.json` and opens it under **Custom views**.
Identical saves reuse the file; independent queries are namespaced. Saved views
survive canvas/server restarts, but stay workspace-local: they are not published
or installed elsewhere, and `.cao/` is ignored by Git. Up to 50 views are
supported. Remove a saved JSON file to remove a view; use the editor to save
revisions rather than editing or renaming content-addressed files. Unsaved
drafts belong only to the currently loaded canvas.

Generation requires Copilot authentication. Only authoring text and trusted
language context go to the SDK, not dashboard evidence rows. Generation sessions
have no shell, filesystem, MCP, write tools, or automatically discovered skills.
Preview documents cannot add CLI actions or named UI elements. Each view is
capped at 200 rows, and each source must be a declared query with `limit` between
1 and the view's `data.limit`. Layered charts omit `data.limit` and bound their
source query to at most 200 rows. Forms, lazy pagination, and drill navigation
are not supported. Query authoring does not change policy or workflow authority.

## Run approved commands

Only a trusted canvas bridge with paired host execution and approval callbacks
can run dashboard-declared CLI actions. Ordinary Go local serving and Pages
previews cannot run local commands; a web page cannot grant execution.

Each action opens a command-review dialog and requires confirmation for that
run, with no second Copilot-host confirmation. The server validates the declared
action and resolved command, then executes in the canvas working directory with
local GitHub CLI credentials and the supported-command allowlist, without a
shell. Actions using `gh aw` may install the pinned extension first. Commands
are admitted within queue concurrency and quota limits and must not require
interactive keyboard input.

After durable queueing, the full-page console shows the command and live stdout
and stderr, with ANSI colors, selection, and scrollback. Terminal input is
disabled. **Back to Commands** or a disconnected browser detaches the display;
it does not cancel execution. Reopening a command replays retained output rather
than rerunning it. The console remains available after success, failure,
timeout, or cancellation.

**Cancel** in the approval dialog starts nothing. The console offers **Copy**
and, for failed or timed-out work, **Retry command**; a retry requires fresh
approval. The last HTTPS pull-request URL in retained output becomes the durable
result for **Open pull request** and **Copy**. Without one, Copy copies the
transcript. Enterprise hosts, custom ports, and path prefixes are supported;
embedded credentials are rejected and query strings/fragments are excluded.
Output links open without opener access. Copying never cancels or reruns work.

Keep local command authority separate from remote execution: canvas approval
does not widen CAO rollout policy or grant workflow authority.

## Compiler-version evidence

When downloading data, the canvas refreshes the latest stable gh-aw release
from GitHub; failure is reported rather than presenting an old snapshot's release
as current. Installed versions and observation times remain snapshot evidence.

The read-only GitHub API can also read compiler metadata from default-branch
lock files. Reads are cached per credential, limited to 128 distinct files and
eight concurrent requests, and return only file identity and parsed compiler
version, never workflow contents or credentials. Live reads do not rewrite
immutable snapshot evidence. Missing or stale evidence is not proof of a current
installation; upgrade remains available when the installed or release version
is unknown.

## Maintain the plugin

Run `npm run typecheck:copilot`, `npm run check:agent-plugin`, and the focused
`tests/unit/dashboard-extension.test.mjs` and
`tests/unit/dashboard-local-preview.test.mjs` tests after extension changes.
They cover SDK declarations, registered tools, package containment, workspace
layouts, and cleanup. See [Contributing](https://github.com/githubnext/gh-cao/blob/main/CONTRIBUTING.md)
for the remaining repository checks.
