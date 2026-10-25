---
title: Build Your First Campaign
description: Define one repository outcome, generate the workflows, and prove the campaign in review mode.
agent:
  type: campaign-guide
  prominent: true
---

Start with the desired outcome, target scope, and execution requirements.
[From Outcome to Execution](operation-patterns.md) lets guidance and authoring
agents derive the simplest suitable structure and explain it; you do not need
to choose a pattern name. Use this campaign-specific guide when independent
per-target execution, history, retries, or specialized workers are needed and
the [catalog](catalog.md) does not already produce the outcome, or when you
explicitly want to author a campaign. Agents should follow the
[`create-cao-campaign` skill](https://github.com/githubnext/gh-cao/blob/main/skills/create-cao-campaign/SKILL.md);
to install an existing campaign instead, use the
[`add-cao-campaign` skill](https://github.com/githubnext/gh-cao/blob/main/skills/add-cao-campaign/SKILL.md).
Before implementation, read [Orchestrators and Workers](orchestrators-and-workers.md)
and the [control architecture specification](https://github.com/githubnext/gh-cao/blob/main/specs/control-architecture.md).

A campaign has:

- one **orchestrator** that selects eligible repositories;
- one or more **workers** that each handle one selected repository;
- one measurable **outcome** that defines success.

This guide creates a review-ready campaign. Production rollout comes later.

## Before You Start

Complete [Set Up CAO](setup-quickstarts.md) so you have a validated control repository. Use a private control repository when the target or required evidence is non-public.

You also need GitHub Agentic Workflows `v0.93.5` or newer:

```bash
gh aw version
```

## 1. Write the Campaign Contract

Describe these requirements before writing a workflow:

| Question | Example |
| --- | --- |
| What should improve? | Repositories should stop using an unsupported Node.js version. |
| What is the allowed scope? | Only `acme/api` and `acme/web`; eligibility may narrow this set. |
| Which repositories qualify? | A checked-in engine range includes the unsupported version. |
| Why independent target runs? | Each repository needs its own execution history and retries. |
| What output is needed? | A pull request that updates the range and CI matrix. |
| When should nothing happen? | The repository is already supported, a matching PR exists, or evidence is incomplete. |

Keep the outcome to one sentence. If it needs unrelated outputs or success measures, split it.

## 2. Generate the Campaign

Open the CAO source or control repository in a coding agent and use:

```text
Read and follow skills/create-cao-campaign/SKILL.md.

Outcome:
[What should measurably improve?]

Target scope:
[Which exact repositories may participate?]

Execution requirements:
[Why does each target need independent execution, history, retries, or specialized workers?]

Eligibility:
[What evidence makes a repository a candidate?]

Safe output:
[What is the smallest useful output?]

No-op cases:
[When should the campaign create nothing?]

Explain the simplest structure that fits these requirements before generating
files. If a campaign is unnecessary, use the matching integration instead.

Use [OWNER/REPOSITORY] for the first proof. Keep it in review mode, limit it to
one repository, and do not change the target. Compile the workflows and report
the changed files, validation results, and first-run command.
```

Do not copy or edit a generated `.lock.yml` file. For a campaign, the skill
creates editable workflow Markdown and compiles it. The remaining steps apply
to that campaign structure, not to observation or other integrations.

## 3. Review What Was Generated

Expect:

| File | Purpose |
| --- | --- |
| `<campaign>/aw.yml` | Installable campaign manifest |
| `<campaign>/README.md` | Campaign purpose, setup, and behavior |
| `.github/workflows/<campaign>.md` | Orchestrator |
| `.github/workflows/<campaign>-<worker>.md` | One focused worker per task |
| `.github/workflows/cao.json` | Campaign and worker registration |

Before running, confirm:

- agent tools are read-only;
- safe outputs are minimal and explicit;
- every worker receives exactly one repository;
- duplicate, healthy, and insufficient-evidence cases return `noop`;
- mode is `review` and the repository limit is one.

## 4. Compile and Test

In this catalog repository:

```bash
npm run compile:locks
npm run compile
npm test
git diff --check
```

In another repository:

```bash
gh aw compile --strict --schedule-seed OWNER/REPOSITORY
```

Review generated lock-file changes for unexpected permissions, secrets, network hosts, write destinations, or worker dispatches.

## 5. Prove One Review Run

Commit the source, generated locks, manifest, runtime resources, and policy together. Then run only the orchestrator:

```bash
gh aw run <campaign> --ref <default-branch> \
  --raw-field target_repo="OWNER/REPOSITORY" \
  --raw-field max_repos="1" \
  --raw-field rollout_percent="100" \
  --raw-field safe_output_mode="review"
```

The proof passes when exactly one authorized repository was selected, only expected workers ran, output stayed in the private review destination, and the target did not change. A useful review item or an explainable `noop` are both valid.

Do not promote the campaign to `live` during this first authoring session.

## Keep It Private or Propose It

A campaign can remain in its control repository. To propose it for the official catalog, open a pull request with its manifest, README, workflow sources, generated locks, policy registration, and focused contract tests.

The catalog is curated. Publication does not grant rollout authority in another control repository.
