# Model Selection Reference

Pinned Squad model-selection reference adapted for native gh-aw. This mode does
not install or call `@bradygaster/squad-sdk`, a Squad CLI or a live model catalog.
The host resolves its own actual available model and reasoning/context controls.

## Per-Agent Model Selection

1. Honor an explicit current task model choice only when the host supports it
   and it is consistent with the admitted execution configuration.
2. Otherwise omit model overrides and inherit host configuration. The bundle's
   generic config contains no agent-specific model defaults or frozen catalog.
3. Record an explicit choice/source when actually known. Do not invent a
   resolver result, availability, cost, rationale, benchmark or budget.
4. If an explicitly required model/capability is unavailable, report the
   limitation; do not silently select a substitute or install a provider.

## Task fit and independence

Use the qualified specialist charter, task complexity and actual evidence
requirements to determine the needed work and response mode. Implementation,
architecture, security, verification and factual research still require their
real domain evidence; a model label does not prove qualification or independence.
Never lower required review/validation gates to meet a cost or response-time goal.

Reasoning effort and context tier likewise inherit host settings unless the
current task explicitly requires a supported override. Never mutate team config,
authenticate, upgrade tooling, call a provider or widen permissions to resolve
model choice. No automatic multi-model fan-out or economy/profile migration.
