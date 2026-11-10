---
private: true
emoji: "🧪"
name: Dashboard Authoring Corpus
description: Synthesizes one agentic-workflow task, infers its operational value and dashboard, validates the pair, and grows a training corpus.
intent: Improve model reliability when authoring workflow-specific operational dashboards by accumulating novel, validated task, value, and dashboard examples.
on:
  schedule: weekly
  workflow_dispatch:
    inputs:
      focus:
        description: "Optional workflow persona, domain, or task family"
        required: false
        type: string
  skip-if-match: "is:pr is:open label:dashboard-authoring-corpus"
permissions:
  contents: read
  copilot-requests: write
  issues: read
  pull-requests: read
tracker-id: dashboard-authoring-corpus
max-turns: 300
max-ai-credits: 600
max-daily-ai-credits: -1
engine:
  id: pi
  model-routing:
    goal: cost
    mode: auto
strict: true
timeout-minutes: 45
concurrency:
  group: "${{ github.workflow }}"
  cancel-in-progress: false
  job-discriminator: "${{ github.run_id }}"
runtimes:
  node:
    version: "24"
network:
  allowed:
    - defaults
    - node
tools:
  bash:
    - "*"
skills:
  - .github/skills/dashboard-authoring
  - .github/skills/generate-dashboard-ir
safe-outputs:
  create-pull-request:
    title-prefix: "[dashboard-corpus] "
    labels: [dashboard-authoring-corpus, ai-generated]
    draft: true
    if-no-changes: warn
    allowed-files:
      - ".github/skills/generate-dashboard-ir/corpus/index.json"
      - ".github/skills/generate-dashboard-ir/corpus/examples/*.json"
      - ".github/skills/generate-dashboard-ir/corpus/examples/*.dashboard.yml"
  noop:
    report-as-issue: false
features:
  gh-aw-detection: true
pre-agent-steps:
  - name: Install dashboard validator dependencies
    run: npm ci --prefix dashboard/site --ignore-scripts
---

# Dashboard Authoring Corpus

Grow the generate-dashboard-ir skill's corpus by one validated example. Use the installed `dashboard-authoring` skill to define the workflow intent and operational-value contract, then use the installed `generate-dashboard-ir` skill for corpus creation and Dashboard Language generation. Treat the Dashboard Language specification and validator as authoritative.

## Context

- Repository: `${{ github.repository }}`
- Optional focus: `${{ inputs.focus }}`
- Intent skill: `.github/skills/dashboard-authoring/SKILL.md`
- Dashboard IR skill: `.github/skills/generate-dashboard-ir/SKILL.md`
- Corpus index: `.github/skills/generate-dashboard-ir/corpus/index.json`
- Specification: `docs/dashboard-language-specification.md`
- Validator: `dashboard/site/src/validator.js`

## Synthesize one task

Read the corpus index and metadata before choosing a candidate. Synthesize exactly one realistic, bounded agentic-workflow task for a repository operator. Use the optional focus when provided. The task must have a clear actor, subject, activation evidence, required effect, no-op conditions, and success conditions.

Reject a candidate whose intent, opportunity, attainment metric, or dashboard composition materially duplicates an indexed example. Call `noop` with the duplicate IDs when no novel candidate remains.

Do not create an executable workflow, grader, fabricated run, or fabricated evidence. This feature produces design examples only.

## Infer operational value

Infer a design-time contract from the task's intent. Bind each hypothetical run to a stable opportunity, enumerate accepted repository evidence and repositories, select exactly one direct metric in `[0,1]`, and define maturation, zero, and missing-evidence rules.

Use an attainment-only baseline with null value and cutoff. Synthetic tasks have no immutable pre-adoption evidence for a comparable baseline.

## Infer the dashboard

Use the installed `generate-dashboard-ir` skill with the synthetic task and operational-value contract as the user intent, plus the specification and validator from Context. Scope every view to the synthetic workflow with a `workflow` filter. Include only views that help an operator understand activation, execution, required effects, actionable exceptions, or operational-value attainment.

## Validate and publish

Add one metadata/dashboard pair and update the sorted corpus index exactly as the skill requires. Run:

```text
npm --prefix dashboard/site run validate:corpus
```

If validation fails, repair the candidate and rerun it. Do not create a pull request unless validation passes. Use `create-pull-request` for exactly the new pair and index update. In the pull request body, summarize the synthetic task, value contract, selected views, duplicate check, and successful validation command.

Call `noop` when there is no novel candidate, required vocabulary cannot express the dashboard without invention, or validation cannot pass within the run.
