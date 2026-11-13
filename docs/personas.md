---
title: Who is CAO for?
description: The target user personas and two main usage patterns for Central Agentic Ops.
---

Central Agentic Ops (CAO) is built for people who **maintain many, sometimes
massively many, repositories with
[GitHub Agentic Workflows](https://github.github.com/gh-aw/introduction/overview/)**.
At that scale, running a workflow in one repository is easy; knowing what every
workflow did, what it cost, and whether it may run somewhere new is not.

All CAO personas share a technical baseline. They:

- work comfortably in a terminal, with `gh`, Git, and GitHub Actions;
- understand GitHub permissions, GitHub Apps, tokens, and secrets;
- reason about security, least privilege, and change control as part of the job;
- prefer reviewed, reproducible configuration over one-off clicks.

CAO therefore favors a CLI, policy files in a reviewed control repository, and
explicit, fail-closed authority over guided wizards that hide what is granted.

> [!NOTE]
> These personas guide product and documentation decisions. They are not
> validated user research, and CAO never infers a person's role from their
> repository activity. See the
> [dashboard specification](https://github.com/githubnext/gh-cao/blob/main/specs/dashboard.md#15-domain-adaptation-and-personalization).

## Multi-repository maintainer

A developer, often an open-source maintainer or a small team lead, who owns a
large number of repositories under one or a few accounts or organizations.

- **Context:** tens to hundreds of repositories, frequently similar in shape
  (libraries, actions, templates, samples). Little or no dedicated operations
  staff.
- **Goals:** automate repetitive maintenance (triage, dependency updates, docs,
  health checks) everywhere at once; spot which repositories need attention;
  keep agent cost and noise under control.
- **Pain points:** copying and updating workflows repository by repository;
  no single view of runs, outcomes, and spend; fear that a bad workflow change
  floods every repository with issues or pull requests.
- **How CAO helps:** one control repository and campaign installs a workflow
  across many repositories; [review mode](rollout-and-routing.md) previews
  output before going live; the [dashboard](dashboard.md) shows activity,
  cost, and outcomes across the fleet.
- **Typical setup:** local CLI with their own `gh` credentials or a single
  GitHub App; one organization; see
  [Set Up One Organization](setup-one-organization.md).

## Organization DevOps administrator

A platform, DevOps, or IT administrator responsible for agentic automation
across one or more enterprise organizations.

- **Context:** hundreds to many thousands of repositories, multiple teams and
  owners, compliance and audit obligations, central identity and secret
  management.
- **Goals:** roll agentic workflows out safely and incrementally; enforce who
  may run what, where, and with which write capabilities; report adoption,
  cost, and operational value to leadership; stop everything quickly when
  needed.
- **Pain points:** credential sprawl and over-broad tokens; workflows reaching
  repositories they were not approved for; no audit trail tying an agent's
  output to a reviewed policy; capacity and API-quota limits at scale.
- **How CAO helps:** reviewed [policy](configuration.md) controls scope and
  mode at an exact workflow commit; GitHub App
  [authentication](authentication.md) and
  [admission gates](admission.md) keep authority least-privilege and
  fail-closed; [safe outputs](execution-and-safety.md) bound every write;
  [operational value](operational-value.md) and an
  [emergency stop](operations.md#emergency-stop) support governance.
- **Typical setup:** dedicated control repository, GitHub Apps per
  organization, optional [hosted deployment](deployment.md); see
  [Set Up Multiple Organizations](setup-multiple-organizations.md).

## Two main usage patterns

Personas describe **who** uses CAO; usage patterns describe **how** they access
it. Both personas can use either pattern: a maintainer may need a shared
service, and an organization administrator may investigate locally.

### Local server with a dev token (`gh cao dashboard`)

- **Scenario:** an individual wants to inspect agentic workflow activity, cost,
  and outcomes for repositories they can already read, without deploying a
  shared service.
- **Entry point:** run `gh cao dashboard` in a repository checkout, or select
  repositories with `--repo OWNER/REPOSITORY`, then open the printed dashboard
  URL. A locally authenticated GitHub CLI and Docker or Podman are the normal
  prerequisites; the command manages a private PostgreSQL cache.
- **Access and credentials:** the loopback Go server uses a local bearer
  capability (the dev token), not hosted GitHub sign-in. This token protects
  local dashboard access; the operator's authenticated `gh` identity separately
  supplies GitHub evidence access. Keep the capability URL and cached private
  evidence private; this is not a remote or multi-user deployment.
- **Lifecycle and boundaries:** keep the command running for bounded evidence
  collection and refresh, then stop it with Ctrl-C. The cache persists for
  later sessions. No cloud deployment, control-repository setup, or campaign
  installation is required, and starting the dashboard does not change remote
  workflows or rollout policy. Acquisition still needs network access and
  GitHub permissions; partial evidence is not complete history.
- **Documentation needs:** lead with the
  [local quickstart](setup-quickstarts.md#open-a-local-dashboard-no-deployment),
  opening and stopping the server, credential separation, cache privacy,
  refresh limits, and troubleshooting. Link to the
  [command reference](cao-cli.md#on-demand-local-dashboard) for flags rather
  than making hosted setup a prerequisite.

### Hosted service

- **Scenario:** a team needs a shared dashboard with centrally managed access,
  evidence collection, and service operations, rather than a server tied to
  one person's terminal.
- **Entry point:** an operator deploys and configures the service; users open
  its shared URL and sign in with GitHub. Start with
  [deployment options](deployment.md) and the selected hosting guide.
- **Access and credentials:** GitHub OAuth and explicit user, organization, or
  team authorization govern dashboard access. Collection credentials are
  configured separately; signing in does not grant GitHub collection or
  workflow execution authority. Do not share a local dev token as a hosted
  authentication mechanism.
- **Lifecycle and boundaries:** the Go host serves the dashboard, assets,
  sign-in, query API, and revision events from the same origin, with queries
  evaluated against PostgreSQL. Operators own deployment, secrets, monitoring,
  recovery, and evidence freshness. Hosting does not widen reviewed control
  policy or promote workflows to live mode, and a hosted failure does not
  fall back to browser storage or a static site. Deployment options remain
  experimental; see their security and operational review requirements.
- **Documentation needs:** separate the service operator's deployment,
  collection, authorization, and recovery tasks from the dashboard user's
  sign-in and investigation journey. Explain stale or unavailable evidence
  and access failures, and link to [authentication](authentication.md) and
  [operations](operations.md) instead of requiring every viewer to run a
  local server.

Use these two patterns to organize documentation entry points and prerequisites,
not as persona-specific restrictions. Both use the same dashboard and evidence
model. The synthetic Pages demo is an illustration, not a third operational
modality or a host for private evidence.

## Who CAO is not for

- Users running one agentic workflow in one repository. Use
  [GitHub Agentic Workflows](https://github.github.com/gh-aw/introduction/overview/)
  directly.
- Users who need a no-code experience without terminal, GitHub permission, or
  security knowledge.
