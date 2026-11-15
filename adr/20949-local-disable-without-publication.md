# ADR 20949: Permit Explicit Local Disable Without Pull-Request Publication

## Status

Draft

## Context

Pull request #20949 splits dashboard maintenance into Repository, Workflow, and Campaign pages under the Updates section, with scoped Update and Disable actions for repositories and individual workflows.

Before this change, `specs/dashboard-data.md` required every declared repository maintenance command to use an exact selected repository, pull-request publication, and fresh trusted terminal approval. The pull request keeps publication and approval for updates and upgrades. For explicit local disable commands it changes the rule in three ways:

- Disable does not publish a pull request. The CLI rejects `disable` when publication is requested with `disable does not publish a pull request`.
- Disable cancels in-progress gh-aw runs before it prevents future runs, for either a repository or one individual workflow.
- Disable reads the Markdown inventory and cancellation scope from a private, shallow checkout of the approved repository (`gh repo clone`, created in a temporary directory). The ambient checkout is not used. If that checkout cannot be prepared, the command stops and no workflows are disabled.

The pull request also records these related facts:

- Hosted disable remains unsupported. Hosted updates keep the control-repository scope.
- Campaign disable remains an existing policy deactivation command and does not cancel active runs.
- Individual workflow targets must be exact. Wildcards, substrings, `..`, and package URLs are rejected for disable.
- The private adapter filters inherited `GH_REPO`, `GH_AW_WORKFLOWS_DIR`, and `GIT_*` variables from the disable subprocess.
- Update approval must disclose that gh-aw may reconcile package-manifest sibling workflows, which can replace their sources and overwrite local sibling edits in the proposed pull request, and may refresh shared references, container pins, and generated files across the exact repository.

The pull request has no evidence of an operator incident, a requirement for disable to be fast, or a review of earlier alternatives. Those are Not inferable from current pull request evidence.

## Decision

Local, terminal-approved `gh aw disable` for an exact repository or an exact Markdown workflow is an allowed maintenance action that does not publish a pull request. Before it prevents future runs, it cancels in-progress runs in the approved repository. It runs against that repository's own checkout, so both the workflow inventory and the GitHub cancellation scope belong to the approved target.

This exception is limited to local disable. It does not extend hosted execution authority, does not apply to updates or upgrades, and does not change campaign disable, which remains policy deactivation without run cancellation.

## Alternatives Considered

- **Keep pull-request publication for every maintenance command, including disable.** This matches the rule that existed before this pull request. The pull request does not record why it was rejected. Rationale for the rejection: Not inferable from current pull request evidence.
- **Expose disable through the hosted executor.** The pull request keeps hosted disable unsupported and keeps hosted execution limited to control-repository updates. Rationale for this choice: Not inferable from current pull request evidence beyond the stated scope limit.

## Consequences

**Positive:**

- Operators can stop future runs of one workflow or a whole repository without waiting for a pull request to merge. The cancellation step precedes the disable step.
- Using the approved repository's own checkout keeps inventory reads and run cancellation tied to the exact target. The command fails closed when that checkout cannot be prepared.
- Hosted authority is unchanged. The exception lives in the private native adapter, and the hosted allowlist does not include disable.

**Negative:**

- Disable changes repository state without a reviewable pull request. Its audit trail relies on the operational command queue and trusted terminal approval.
- Cancelling in-progress runs stops the targeted workflow's or repository's active work, which may interrupt runs the operator wanted to finish. Whether a cancelled run can be resumed is Not inferable from current pull request evidence.
- The rule now differs by command: updates and upgrades publish, disable does not, and campaign disable does not cancel runs. Operators must learn these differences.
- Approval for update sweeps must disclose sibling reconciliation, which can overwrite local sibling edits in the proposed pull request. An exact workflow operand does not limit that effect.
- Each disable call clones the target repository. A network or access failure blocks the disable, which is intended but adds latency.
- Filtering inherited `GIT_*` and `GH_REPO` variables protects the checkout, but it depends on the list staying complete as the gh-aw CLI and Git change.
