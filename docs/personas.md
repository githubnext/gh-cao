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

CAO supports two main ways of working: individual investigation on a local
server and shared access through a hosted service. Either persona can use
both. A maintainer may need a shared dashboard; an organization administrator
may prefer a local session to investigate a specific repository.

### Local server with a dev token (`gh cao dashboard`)

Use a local server to inspect workflow activity, cost, and outcomes without
deploying a shared service. Run `gh cao dashboard` in a repository checkout,
or select repositories with `--repo OWNER/REPOSITORY`, then open the printed
dashboard URL. The usual prerequisites are an authenticated GitHub CLI and
Docker or Podman. No control-repository setup or campaign installation is
required.

The command serves the dashboard on your machine and manages a private
PostgreSQL cache. A local bearer token—the dev token—controls dashboard
access; your `gh` credentials separately authorize evidence collection from
GitHub. Keep the dashboard URL and cache private. This mode is for local use,
not shared hosting.

Leave the command running to refresh evidence, and press Ctrl-C to stop.
The cache remains for later sessions. Collection requires network access and
repository permissions, and its limits can leave gaps in the available
history. Opening the dashboard does not change remote workflows or rollout
policy.

**Documentation priorities**

- Start with the [local quickstart](setup-quickstarts.md#open-a-local-dashboard-no-deployment),
  not hosted deployment instructions.
- Explain how to open and stop a session, protect the token and cache, and
  recognize incomplete or stale evidence.
- Keep detailed flags and limits in the
  [command reference](cao-cli.md#on-demand-local-dashboard).

### Hosted service

Use a hosted service when a team needs a shared dashboard with centrally
managed access and evidence collection. An operator deploys and maintains the
service; users open its URL and sign in with GitHub. They do not need to run
a local server.

GitHub OAuth and explicit user, organization, or team authorization control
dashboard access. Operators configure collection credentials separately.
Signing in grants neither collection nor workflow execution authority, and
a local dev token is not a substitute for hosted authentication.

The Go service serves the dashboard and evaluates queries against PostgreSQL.
Operators are responsible for secrets, monitoring, recovery, and evidence
freshness. Hosting does not widen reviewed policy or enable live workflow
execution. Deployment options are experimental and require the security and
operational reviews described in the [deployment guide](deployment.md).

**Documentation priorities**

- Separate the operator's deployment and maintenance tasks from the user's
  sign-in and investigation tasks.
- Guide operators through hosting, collection, access control, and recovery.
  Link to [deployment options](deployment.md),
  [authentication](authentication.md), and [operations](operations.md).
- Help users resolve access failures and recognize stale or unavailable
  evidence without requiring them to understand the deployment.

Organize documentation around these two patterns, with prerequisites and next
steps for each. Both use the same dashboard and evidence model. The synthetic
Pages demo illustrates CAO; it is not an operational hosting option.

## Who CAO is not for

- Users running one agentic workflow in one repository. Use
  [GitHub Agentic Workflows](https://github.github.com/gh-aw/introduction/overview/)
  directly.
- Users who need a no-code experience without terminal, GitHub permission, or
  security knowledge.
