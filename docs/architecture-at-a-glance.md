---
title: What is Central Agentic Ops?
description: Monitor and govern GitHub Agentic Workflows across repositories, with shared evidence and policy-controlled execution.
next:
  link: /gh-cao/personas/
  label: Who is CAO for?
---

**Central Agentic Ops (CAO)** is a system for monitoring and governing
[GitHub Agentic Workflows (AWs)](https://github.github.com/gh-aw/introduction/overview/)
across repositories. It brings collected activity, cost, and
outcome evidence into a shared dashboard and supports policy-governed execution
and rollout of participating shared workflows.

<span id="what-is-central-agentic-ops"></span>
<span id="what-is-cao"></span>

Agentic work might begin with a single task, then grow across repositories.
AWs run those tasks in GitHub Actions, using Markdown instructions and configured
tools and permissions. These AWs form the building blocks of CAO as we scale.

<span id="what-you-can-do-today"></span>
<span id="agentic-workflows-operated-across-repositories"></span>

## How it works

1. **Observe activity.** [CAO Activity](activity.md) collects a bounded,
   read-only snapshot from explicitly configured repositories for dashboards,
   reports, and agents. Existing workflows keep running where they are.
2. **Evaluate results.** Follow runs and outputs back to GitHub; check
   [freshness and completeness](dashboard.md#read-a-result) and
   [accepted outcomes](operational-value-how-to.md), not just run success.
   Missing evidence is not a healthy zero.
3. **Govern shared work when needed.** Explicitly integrated workflows run
   under reviewed control-repository policy. Start with one target in `review`,
   inspect the proposal, and authorize live output separately.

Monitoring existing workflows does not move them or change their permissions.
When you choose to govern shared automation, describe the outcome, target scope, and execution
requirements; guidance and authoring agents derive the simplest structure.
Campaigns are one way to package and scale work, not a prerequisite. See
[From Outcome to Execution](operation-patterns.md).

## A simple example

An issue-triage workflow runs in one repository while another investigates
failures elsewhere. CAO shows their collected runs and available outputs in one
place, with links back to GitHub. A successful run is not automatically a
successful outcome. The evidence helps you investigate what the work produced.

<span id="see-the-product"></span>

<figure>
<img src="https://github.com/user-attachments/assets/1c408552-1a2d-45cc-967e-b46d8811798d" alt="CAO Repositories chart view showing retained runs and issue outputs across repositories." width="1200" height="766" loading="lazy" decoding="async">
<figcaption>Public sample, 19 October 2026. Collected evidence, not a live feed.</figcaption>
</figure>

[Explore the public Repositories view](https://githubnext.github.io/gh-cao/cao/#page-repositories).

<span id="essential-safety-boundaries"></span>
<span id="how-evidence-reaches-the-dashboard"></span>
<span id="operating-workflows-across-repositories"></span>

CAO governs explicitly integrated workflows, not every observed AW or ordinary
Actions job. Review is real execution and can create proposals in the review
destination; live output requires explicit authorization. Missing required
authority, access, or evidence fails closed.

<span id="get-started"></span>

- [Set up CAO and monitor your workflows](setup-quickstarts.md)
- [Architecture and safety boundaries](architecture.md)
- [Browse campaigns](catalog.md)
