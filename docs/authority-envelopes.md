---
title: Authority Envelopes
description: Topology-independent authority contracts and the explicit-target gh-aw operation integration.
---

For terminology, examples and topology tradeoffs, start with
[From Outcome to Execution](operation-patterns.md). This page is the
implementation contract for explicit-target execution.

CAO separates governance from orchestration. The authority kernel resolves
whether requested work fits reviewed policy. Campaigns supply fleet discovery,
rollout and worker dispatch; standalone workflows supply deterministic fleet
execution; direct operations execute an explicitly requested target set.
None is a substitute for gh-aw execution enforcement.

## Contracts

The kernel is `.github/workflows/shared/authority.mjs`. Its resolution and
validation functions have no GitHub, filesystem, credential or dispatch access.
The adapters in `policy.mjs` and `operations.mjs` translate reviewed policy and
integration evidence into its contracts.

| Contract | Authority and contents |
| --- | --- |
| Request | Untrusted operation identity, explicit targets, requested mode/destination and narrowing target cap. Unknown authority-bearing keys and unsupported versions reject. |
| Verified context | Integration-supplied control repository, exact policy SHA, workflow reference, run ID and attempt. Never accepted from request JSON. |
| Envelope | Version, operation identity, execution binding, maximum target count, scoped target/mode/destination grants, required enforcement controls and `delegation: false`. |
| Decision | Explicit allow/deny and reason. Invalid, unsupported or unavailable evidence blocks preparation; it is never an empty success. |

The existing campaign and standalone adapters use the same mode/target authority
resolver and retain their serialized handoffs. Their debug and worker checks are
pattern-specific enforcement, not kernel workflow roles. Versioned run-bound
envelopes are used by the new direct path; they do not retroactively turn legacy
dispatch correlation into cryptographic provenance.

An envelope is an upper bound, not a credential or independently trusted token.
`validateEnvelope` checks its structure, binding and supported controls.
`validateNarrowing` compares already verified grants; it does not establish their
provenance or permit cross-run delegation. The direct adapter re-reads policy at
the exact workflow SHA and re-resolves the request before accepting its local
admission handoff. Changed requests, attempts, policy bindings or grants reject.

Version 1 supports `policy-revision`, `target-scope` and `output-routing`
requirements. Unsupported or missing requirements reject. New integrations must
implement verified context and enforcement before advertising support; there is
no runtime-loaded plugin registry, arbitrary callback language, pooled budget,
generic scheduler, new credential broker or alternative execution backend.

## Direct operation policy

Declare the exact workflow slug under `control-plane.operations`:

```json
{
  "version": 1,
  "gh-aw-version": "v1.0.1",
  "control-plane": {
    "scope": {
      "allowed-owners": ["acme"],
      "allowed-repositories": ["acme/service-a", "acme/service-b"]
    },
    "operations": {
      "direct-audit": {
        "enabled": false,
        "mode": "review",
        "max-mode": "review",
        "max-repositories": 2
      }
    }
  }
}
```

This is a disabled example, not a rollout instruction. Review and explicitly
enable policy before intended execution. Global mode/target-cap defaults apply
when absent; review and one target remain the base defaults. Optional `targets`
entries narrow per-target modes. Do not add `workers`, inventory, rollout,
engine, tools, permissions, credentials or safe-output primitives to an operation.
The policy parser rejects those keys. A workflow cannot simultaneously belong
to this registry and an existing standalone/campaign/worker declaration.

Bare setup refuses declared operations, including disabled ones, before replacing
scope or configuring authentication. CLI policy mutation preserves operation
declarations; campaign enable/disable commands do not activate them.

## Workflow integration

The complete compile-tested example is
[`tests/fixtures/workflows/direct-audit.md`](https://github.com/githubnext/gh-cao/blob/main/tests/fixtures/workflows/direct-audit.md).
It is a fixture, not an installed or enabled workflow. Its shared import is:

```yaml
imports:
  - uses: shared/operation.md
    with:
      workflow: direct-audit
      read_repository: ${{ inputs.target_repo || github.repository }}
      read_contents: read
      read_issues: read
```

As with existing CAO workflows, the importing workflow must declare:

```yaml
jobs:
  pre-activation:
    outputs:
      cao_authorized: ${{ steps.cao_admission.outputs.authorized == 'true' && steps.cao_precompute.outputs.authorized != 'false' }}
if: needs.pre_activation.outputs.cao_authorized == 'true'
```

Keep this output and condition explicit under the pinned compiler. Shared import
output merging is not supported reliably enough to remove them. The compiler
also retains its own native `activated` condition.

Request either `target_repo` or `target_repos` (a JSON array), never both.
The declared cap bounds the whole set; no discovery, ranking, cell selection,
rollout percentage or downstream dispatch occurs. Every target must be accessible,
unarchived, enabled, and resolve to the requested name with complete identity
evidence. Duplicate names or resolved IDs reject.

Multiple targets are supported only in review mode with one common review
destination. That destination need not appear in the target allowlist, but must
pass the existing owner/access/privacy checks. Live mode requires one explicit
`target_repo`, an authorized live policy ceiling, and compiled safe outputs
bound to that target. The workflow's safe-output expressions must match its
declared inputs, as in the complete fixture. A target policy that would change
the requested execution mode rejects rather than silently misrouting outputs.

The emitted precompute record contains `authority_envelope` and the admitted
`candidate_repositories`. Missing tools, credential reach or compiled safe
outputs are not solved by the envelope: report incomplete and do not select
another recipient or output mechanism.

## Compatibility and validation

Existing policies, workflow imports and campaign/standalone/debug contracts need
no migration. Root installation ships the shared import and both new runtime
modules; hosted collector packaging retains their transitive imports. Operations
remain ordinary workflow evidence, not synthetic campaigns. No dashboard entity
or query schema is introduced.

Regression coverage exercises existing policy behavior, malformed/versioned
contracts, binding changes, widening, destination restrictions, unsupported
controls, direct admission/preparation, and pinned compilation of both existing
standalone and direct workflows. No example dispatch or live policy change is
required to validate these local contracts.
