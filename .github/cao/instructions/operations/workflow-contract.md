# CAO campaign workflow contract

Use this checklist after the campaign outcome and worker split are stable.

## Orchestrator

- Name it with the exact campaign display name.
- Keep the complete workflow `name` at 32 characters or fewer, omitting
  redundant role words when needed.
- Add a fuzzy schedule when periodic and `workflow_dispatch` with
  `target_repo`, `safe_output_repo`, `max_repos`, `rollout_percent`, and
  `safe_output_mode` defaulting to `review`.
- Import `shared/control.md` with static campaign identity and
  `role: orchestrator`; use only request-narrowing inputs.
- Use singleton concurrency, bounded credits, least privilege, explicit tools
  and network, `strict: true`, and `threat-detection: false`.
- default new dispatchers to `hourly`. Configure
  `safe-outputs.threat-detection: false`.
- Configure `repo-memory.branch-name: memory/<campaign-slug>` and store only
  bounded advisory dispatch state.
- List every worker in `safe-outputs.dispatch-workflow.workflows`, bound the
  maximum, and deduplicate `(worker, target_repo, mode)` tuples.
- Select and rank only. Do not perform target work.
- Finish with the standard Orchestrator Report inherited from shared control;
  do not copy or rename that contract.
- Every orchestrator inherits the dedicated
  `central-agentic-ops.dispatcher.run` OTEL span from `shared/control.md`;
  configure OTLP exporters only and do not emit a duplicate span.

## Worker

- Name it `<Campaign Name> / <Worker Name>` and give it a stable tracker ID.
- Accept the complete control envelope: `target_repo`, `safe_output_repo`,
  `safe_output_mode`, `correlation_id`, `central_repo`,
  `control_plane_run_url`, and `batch_label`.
- Declare `safe_output_mode` as a required `workflow_dispatch` string input.
  Never give workers a default mode: the orchestrator must pass the
  policy-resolved effective mode explicitly, and omission must fail dispatch
  validation rather than silently downgrade a live target to review.
- Preserve shared control's manual-only `debug` path and global output staging.
  Debug is never an orchestrator selection or persistent policy mode. Keep
  dispatcher correlation inputs optional so local debugger runs can omit them;
  shared control still requires them for ordinary review/live worker dispatches.
- Import shared control with static campaign, worker, and `role: worker`.
- Pass each declared GitHub `read` permission to the matching
  `shared/control.md` `read_<permission>` input. The shared control import
  resolves the dispatched repository's credential from the control
  repository's selected authentication profile and binds GitHub MCP or CLI
  tools to that credential. Preserve supported App, owner-scoped PAT, and
  workflow-token profiles; do not add a workflow-local alternative, mix
  profiles, or fall through from an explicitly selected profile. A target
  checkout using the correct credential does not prove that the agent's GitHub
  tools use it.
- Use repository-scoped concurrency, least privilege, explicit tools and
  network, strict mode, bounded credits and timeout, and mission-specific safe
  outputs.
- Analyze only the dispatched target. Do not discover repositories, dispatch
  work, or widen mode.
- Use the complete Worker Report format required by shared control for issues
  and pull requests. Include campaign and worker labels.
- Workers have no `evals` configuration; use deterministic graders for worker
  measurement.
- Confirm the orchestrator disables threat detection and every worker omits
  `evals`.

## Shared requirements

- A package descriptor MAY include `standalone-workflows`, a list of unique
  kebab-case workflow slugs distinct from its orchestrator and workers. Each
  must import shared control with `role: standalone` and its own exact workflow
  identity, never a campaign/worker identity. Installation registers each
  auxiliary workflow disabled with `mode: review` and `max-mode: review`,
  preserving existing operator declarations. Campaign workflow enable/disable
  inventory includes these files, but enabling a campaign does not enable their
  standalone policy. See the optional `modernization` package for an example.
- Declare the campaign and exact worker workflow slugs in
  `.github/workflows/cao.json`.
- Put the optional
  `{{#runtime-import? .github/cao/<campaign-slug>.md}}` at the bottom of every
  prompt. Every orchestrator and worker prompt must include it at the bottom of
  the Markdown body. Never place the runtime import at the top of the Markdown
  body. Do not create the consumer-owned steering file.
- Use stable identity and deterministic safe-output deduplication. A healthy,
  duplicate, or already-tracked outcome is a silent `noop`.
- Keep campaign memory advisory, bounded, validated, and free of secrets, raw
  source, prompts, logs, and unbounded API responses.
- Configure:

```yaml
repo-memory:
  branch-name: "memory/<campaign-slug>"
```

  The orchestrator prompt must read and use `$GH_AW_MEMORY_DIR` before selecting
  dispatches. Encourage workers to use the same campaign memory branch when
  shared history prevents repeated analysis. Treat memory as advisory, never as
  policy, target authority, credential storage, or current evidence.
- Follow the control repository's selected engine and inference/billing
  profile. The bundled Copilot profile declares `copilot-requests: write` and
  uses the built-in workflow token. Other gh-aw-supported engine/provider
  profiles are valid when explicitly configured for the control repository;
  apply the same profile consistently to the campaign workflows that need
  inference. Do not infer a provider from available secrets, mix billing
  profiles, or use a target-access credential for model inference.
- Do not add workflow-local read App/PAT selection. Keep target read credential
  resolution in `shared/control.md`, preserve exact repository scope, and fail
  closed when the explicitly selected profile cannot produce a credential.
- Configure provider credentials as Actions secrets. Do not infer a provider
  from available secrets or silently change an installed workflow's engine.
