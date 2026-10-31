---
title: Try a Repo Assist review
description: Manually install, review, and run one bounded Repo Assist campaign.
---

To monitor existing Agentic Workflows without adding a campaign, start with
[Set Up CAO](setup-quickstarts.md). This optional walkthrough uses ordinary
campaign commands after setup; nothing commits, pushes, or dispatches on your behalf.
It demonstrates the orchestrator/worker pattern, not the default next step.
For known targets or a single fleet mission, first compare
[direct and standalone operations](operation-patterns.md).

## 1. Install the campaign

From your configured control repository, install Repo Assist from a reviewed
catalog revision:

```bash
gh cao add githubnext/gh-cao/repo-assist
gh cao campaigns mode preview repo-assist
```

Installation is not execution. Review `.github/workflows/cao.json` before
publishing. Keep exact owner and repository allowlists for the control repository
and the one existing target you explicitly select. Configure
`control-plane.campaigns.repo-assist.targets` for that target, keep campaign mode
and the maintenance worker ceiling at `review`, set `max-repositories` to `1`,
enable only maintenance, and disable issue triage, issue fix, and PR upkeep.
See the [campaign policy example](https://github.com/githubnext/gh-cao/blob/main/repo-assist/README.md#install)
and [rollout guide](rollout-and-routing.md).

Use a private control repository for non-public targets or evidence. A public
control repository exposes workflow evidence, review outputs, and dashboard
data. Repository-local workflow-token Activity access covers only the control
repository: declare `control-plane.activity.repositories` accordingly, or
configure appropriate cross-repository credentials. Target access and model
authentication remain separate prerequisites; see [authentication](authentication.md).

Inspect the diff and run `gh cao doctor`. Commit the reviewed workflows and
policy together, then publish and immediately pause the orchestrator:

```bash
git push
gh workflow disable repo-assist.lock.yml
```

Keep it paused until Step 2 so its six-hour schedule does not request recurring
work while you inspect the installation. Check **CAO validation** in Actions
before execution.

## 2. Request one maintenance review

The normal budgets are 250 AI credits for orchestration, 600 for maintenance,
and a configurable output check (400 by default): up to **1,250 AI credits**
under default limits, not expected consumption or measured usage.

Replace the coordinates below and `main` with your control repository's actual
default branch. Enabling the orchestrator also enables its six-hour schedule.

```bash
gh workflow enable repo-assist.lock.yml
gh aw run repo-assist --ref main \
  --raw-field target_repo="TARGET_OWNER/TARGET_REPOSITORY" \
  --raw-field safe_output_repo="CONTROL_OWNER/CONTROL_REPOSITORY" \
  --raw-field max_repos="1" \
  --raw-field safe_output_mode="review"
gh workflow disable repo-assist.lock.yml
```

Pause the orchestrator even if dispatch fails if you do not want scheduled work.
Disabling does not cancel an already requested run. Inspect Actions to follow
the orchestrator and maintenance worker; repeating `gh aw run` requests new work,
not a status check.

Maintenance proposes one small validated patch in a control-repository review
issue and downloadable review bundle, or records a no-op. It never applies
changes to the target repository. A no-op is a valid result, and unavailable
required evidence remains incomplete. Proposals are optional, not readiness
grades or requirements for using CAO.

## 3. Collect and inspect evidence

After inspecting the outcome, request collection:

```bash
gh workflow run cao-activity.yml
```

This dispatch returns without waiting. Normal Activity push and schedule
triggers may also collect evidence independently. Inspect the Activity run,
then the dashboard workflow and its successful Pages deployment job; workflow
success with a skipped deployment is not publication. Follow
[Set Up CAO](setup-quickstarts.md#4-open-your-dashboard) to open the dashboard.
Runtime success alone does not prove a proposal was applied or accepted.

## See more without running more

Read [verified historical Repo Assist examples](https://github.com/githubnext/gh-cao/blob/main/repo-assist/README.md#see-repo-assist-in-action)
without dispatching work or spending AI credits. No demo repository is created,
and useful findings are not guaranteed. Other workers, recurring operation,
additional targets, and live authority require separate reviewed choices.
