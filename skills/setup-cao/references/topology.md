# CAO control-repository topologies

Load this reference only when the repository is source-managed, is also a
catalog, or its role is ambiguous.

## Separate control repository

The normal setup creates or reuses a repository whose installed CAO runtime,
policy, credentials, and campaign records operate on explicitly enrolled remote
repositories. Prefer a private repository. Use the installer and interactive
`gh cao setup` flow.

## Source-managed control repository

A repository may run workflows it maintains directly in-tree only when its
maintainers explicitly select that topology. It must contain or commit
`.github/workflows/cao.json` and the CAO runtime sources. Do not infer this role
from repository names, workflows, catalog files, or credentials, and do not run
the installer over maintained in-tree workflows.

Campaign records are not required for workflows maintained directly in-tree.
That does not make those workflows active: `.github/workflows/cao.json` remains
the rollout and live-activation authority.

## Catalog dogfooding

A catalog may also be an explicitly selected source-managed control repository.
Apply both sets of safety rules:

- campaign manifests and workflow sources define catalog content;
- `.github/workflows/cao.json` defines rollout policy;
- Actions variables and secrets provide credentials;
- target authority remains the exact reviewed scope at the workflow SHA.

Keep those records separate. Catalog contents do not activate the control plane,
credentials do not widen policy, and target repository files do not grant
control-plane authority.

For the normative role definitions and safety boundaries, consult root
`AGENTS.md`, `ARCHITECTURE.md`, and the control architecture and policy
specifications rather than duplicating them here.
