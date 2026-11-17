# Use gh aw

gh-aw governs **how** an authorized workflow executes: engines, models, tools,
network, permissions, job topology, and safe-output primitives. CAO policy
governs **whether and where** it may run. Neither grants the other's authority.

## Procedure

1. Load the installed `agentic-workflows` skill
   (`skills/agentic-workflows/SKILL.md` in the catalog,
   `.github/skills/agentic-workflows/SKILL.md` in a control repository). It
   routes create, update, debug, upgrade, and report tasks to current upstream
   gh-aw guidance.
2. Apply the repository overlay in `.github/aw/instructions.md` when present.
   For CAO campaigns, also follow [create a campaign](operations/create-campaign.md).
3. Edit only `.github/workflows/*.md` sources. `.github/workflows/*.lock.yml`
   files are generated: run `gh aw compile` (in the catalog, `npm run compile`
   or `npm run compile:locks`) and review the lock diff. Pass
   `--schedule-seed githubnext/gh-cao` whenever compiling catalog workflows
   manually.
4. Resolve merge conflicts in Markdown sources, then regenerate locks instead of
   editing conflict markers.
5. Inspect runs with `gh aw logs`, `gh aw audit RUN_ID`, and `gh aw doctor --dir .`.

## Boundaries

- GitHub tools exposed to agents stay read-only; repository writes use only
  the workflow's declared safe outputs.
- Do not add engines, limits, tools, permissions, credentials, jobs, or
  safe-output primitives to `.github/workflows/cao.json`.
- Compiled capability never grants CAO rollout, live mode, or repository reach.
