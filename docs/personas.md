---
title: Who is CAO for?
description: The target user personas for Central Agentic Ops.
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

## Who CAO is not for

- Users running one agentic workflow in one repository. Use
  [GitHub Agentic Workflows](https://github.github.com/gh-aw/introduction/overview/)
  directly.
- Users who need a no-code experience without terminal, GitHub permission, or
  security knowledge.
