---
title: Status header
description: Understand the operating status and work-in-motion label at the top of Overview.
---

The status header gives you the overall shape of the factory right now.

## What it shows

- **Work in motion** appears when runs are queued or in progress.
- The heading classifies the available run evidence as idle, humming, needing
  attention, or under strain. When grader observations are present, it names
  those observations without claiming verified repository Value.

Treat the heading as a signal, not a health score. Follow an unexpected state
into the relevant run, output, or value view before deciding what happened.

## Data it uses

| Query | Source | Purpose |
| --- | --- | --- |
| `overview-factory-status` | Run and grader summaries | Selects the status heading. |
| `overview-header-presentation` | Factory status and all-workflow health | Applies the health attention heading across CAO and targeted repositories. |
| `overview-run-summary` | `runs` and workflow inventory | Counts active work, including review and live rollout modes. |

If status evidence is unavailable, the header says so instead of inferring a
healthy state.

## When to investigate

Investigate when the heading reports strain, needs attention, or is unavailable.
Start with failed or active Runs, then check retained outputs and Operational
value when run evidence does not explain the status.