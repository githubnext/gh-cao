---
title: At a glance
description: See what is moving, what needs attention, and whether the evidence is ready for follow-up.
agent:
  type: dashboard
  prominent: true
  binding:
    kind: dashboard-page
    id: overview
---

The dashboard is the operational view of GitHub Agentic Workflow evidence,
whether collected locally or by a Central Agentic Ops control plane.
It turns retained activity evidence into focused views, each designed to
answer a specific question:

- What work is moving now?
- What has reached the repositories in scope?
- What needs attention or a closer look?
- Is the evidence complete and current enough to act on?

## Open it locally without a cloud deployment

With an installed release and locally authenticated GitHub CLI, run:

```bash
gh cao dashboard --repo OWNER/REPOSITORY
```

Or run `gh cao dashboard` in an existing repository checkout. Open the full
**Dashboard:** URL printed in the terminal. The bundled Go server serves on
loopback and queries a private PostgreSQL namespace on your machine.
No CAO cloud deployment, Azure setup, hosted dashboard, PostgreSQL, Redis,
containers, or separate Node.js or gh-aw installation is needed.

GitHub is still the authenticated upstream evidence source; collection needs
network access and your repository permissions. The command collects bounded
recent evidence automatically and refreshes every five minutes. Its evidence
window defaults to seven days, with resumable historical discovery; the server
lifetime remains capped at 48 hours per invocation. The data-status tooltip shows ingestion
progress and quota pauses. Acquisition preserves API headroom and does not
promise complete upstream or artifact coverage. Press **Ctrl-C** to stop while retaining
the cache. See the [local quickstart](setup-quickstarts.md#open-a-local-dashboard-no-deployment)
for installation, optional cache reuse, and evidence limits.

## How it works

Every view follows the same data flow:

1. **A collector acquires evidence.** `gh cao dashboard` reads bounded recent
	evidence using local GitHub CLI authentication; the separate Activity workflow
	publishes bounded operational snapshots for shared monitoring.
2. **The selected backend prepares and queries the data.** Production Go
	serving uses PostgreSQL; local development uses the same Go server over a real
	PostgreSQL namespace. Each evaluates Dashboard Language.
3. **The active view presents the result.** When the underlying data changes, the
	query runs again and the view updates.

The dashboard is not a live feed. It reads the latest successfully collected
evidence from the selected local or shared collection path.
[Data ingestion](dashboard-data-ingestion.md) follows that
evidence into each query backend, while the
[Data model](dashboard-data-model.md) explains the records and relationships
available to every view.

The public Pages site is instead a **Synthetic enterprise demo** with no live
services. Generated raw samples pass through production normalization, canonical
ingestion, worker queries, and these same views. It publishes no operational
SQLite snapshot or agent summary. Its daily rebuild is independent of real
collection freshness.

On the first visit to the synthetic demo, a dismissible import screen explains
how the browser downloads and caches its first snapshot. Dismissing it keeps
the import running; Overview continues to show preparation progress rather
than calling campaigns idle. The import screen is automatically removed when a
complete snapshot is ready, and its animation timers and progress subscription
are released. If preparation fails or is cancelled while the screen is still
open, it shows a **Retry import** action. **Explore data** or Escape dismisses
the screen; Overview cannot reopen it after dismissal. Later visits use the
cached snapshot during refresh. Go-backed dashboards do not use this
browser-local first-import experience and do not fall back to it on failure.

## Read a result

Treat each result as a starting point for investigation, not as a scorecard. A
high count, a quiet period, or a warning tells you where to look next. Before
following up, check the availability, completeness, and freshness shown with it:

- **Empty** means the dashboard has evidence and found no matching activity.
- **Unavailable** means the dashboard could not obtain the evidence it needs.
- **Partial** means the result may describe only part of the campaign.
- **Stale** means newer activity may not be represented yet.

These distinctions keep missing information from looking like a healthy zero.

See the [Glossary](glossary.md) for definitions of Dashboard terms such as
rollout mode, safe output, outcome, and operational value.

## Know the boundary

Reading dashboard evidence does not start work, approve an output, grant
workflow authority, change rollout policy, or write to a repository. Make those
decisions through the control repository and its reviewed workflows and policy.
The synthetic Pages demo is read-only; optional canvas commands and
[hosted CLI actions](https://github.com/githubnext/gh-cao/blob/main/server/README.md#hosted-cli-actions)
have separate authorization and approval requirements.

<span id="design-queries-in-a-copilot-canvas"></span>

The [canvas Query editor](agent-plugin.md#design-queries-in-a-copilot-canvas)
supports local AI-assisted authoring and saved views. It is not enabled on the
static website, hosted server, or ordinary local preview.

### Configure compiler defaults

**Maintenance → Governance** is visible only to an authorized hosted
administrator with a current, assured session. It is hidden in published
dashboards, local Go previews and Copilot canvases, including through direct
links. The standalone Node `--admin` mode has been retired.

Hosted repository installation tokens cannot administer organization or
enterprise variables, so this editor remains disabled there. Use `gh aw env`
outside the dashboard with administrative credentials for the exact chosen
scope. Review sets and deletions before applying, verify saved variables and
recompile workflows to incorporate changed compiler defaults.

Organization and enterprise variables are inherited by repositories with access
to them, including CAO repositories, but may also affect repositories outside
CAO. Existing narrower overrides and variable access lists remain in effect.
`null` or omitted keys remove an override at the selected scope, not an inherited
default. Visibility controls access for newly created variables only.

This page manages **non-secret GitHub Actions variables**, not secrets, rollout
policy, or live authority. Never enter credentials in the YAML. The chosen scope
requires administrative access; hosted repository installation tokens cannot
perform these operations, so editing remains disabled for hosted administrators.

## Continue reading

- Start with [Overview](dashboard-overview.md) to understand the default
	operational view.
- Choose where to host the dashboard in [Deployment options](deployment.md).
- Read [Data ingestion](dashboard-data-ingestion.md) to follow evidence from
	GitHub Actions into operational backends, separately from demo ingestion.
- Use the [Data model](dashboard-data-model.md) to understand entities,
	relationships, identities, and retention.
- Build views with the [Dashboard Language guide](dashboard-language.md), then
	consult the [language specification](dashboard-language-specification.md) and
	[view catalog](dashboard-view-catalog.md) for complete reference material.
- Let browser agents read the same pages through [WebMCP](dashboard-webmcp.md),
	and agents without a browser or shell through
	[Agent analysis](agent-analysis.md).
- Use the [Copilot plugin and canvas](agent-plugin.md) for local query authoring
	and explicitly approved commands.