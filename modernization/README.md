# Modernization

An optional, experimental CAO package that runs the **actual Squad coordinator
and modernization team** inside GitHub Agentic Workflows. It assesses a system
across repositories, proposes a coordinated modernization, files approved work,
implements tested draft PRs, and verifies the system together. It is not a
planning-only agent, a slash-command integration, or part of the default install.

## Install and authority

```sh
gh cao add githubnext/gh-cao/modernization
```

The package installs `modernization-system`, the `modernization` coordinator,
`modernization-task`, and the pinned team under `modernization/squad/`.
It requires the CAO control runtime and gh-aw v1.2.3. The native engine invokes
`--agent squad`; trusted setup overlays the vendored team at the exact workflow
SHA. Target-supplied Squad configuration cannot replace that cast, and the
overlay is excluded from target patches.

Installation grants no target access or live authority. Configure exact target
scope and credentials in the control repository. Enable the standalone system
workflow separately from the campaign and task worker. Newly installed
standalone policy is disabled, review-only. This catalog's dogfood declarations
also leave the campaign and worker disabled and review-only.

System assessment currently supports up to ten repositories under **one owner**,
using an exact-repository read App token, or one explicitly mapped fleet read
PAT. Mixed PAT secrets and cross-owner system runs fail rather than borrowing
credentials. The coordinator uses CAO's exact-scoped read adapter and can inspect
the reviewed engagement's owners; workers read only their own target and the
control-repository handoff. Read Apps need Contents, Actions, Issues, Pull
requests, Checks and Commit statuses read permissions. Live safe-output write
credentials remain separate and must already be configured through CAO.

## Lifecycle and measurable gates

| Phase | Result and completion criterion |
|---|---|
| Assess | Run `modernization-system` with explicit `target_repos`. Squad files one central proposal with exact baselines, an acyclic task graph, named specialists and measurable task/system acceptance. No target issue or PR is created. |
| Approve | A human reviews the proposal and commits its concrete JSON as `.github/cao/modernization.json`. Every repository must be admitted by CAO at that workflow SHA. This record approves intent, not rollout or credentials. |
| File issues | The coordinator dispatches at most three ready tasks per run, additionally bounded by CAO's effective repository count. One worker creates one linked issue per task, including dependency IDs and the plan digest. Dependent issues can be filed before their prerequisites finish. |
| Implement | After the issue exists, a single-target worker can implement only when all predecessors are complete. Different Squad specialists author and review the patch. Live outputs are tested **draft** PRs; no merge or deployment capability is granted. |
| Unblock | A predecessor must have one matching merged PR into the approved default branch, an independent approving GitHub review on its exact head, and at least one successful check/status with no failing or pending evidence. Closing an issue does not unblock anything. |
| Verify | Run `modernization-system` again with the same explicit system. With an approved plan present, it reports pass/fail/incomplete against every system criterion and exact merged revisions, including actual integration evidence and remaining deployment acceptance. |

Review mode routes issues to the admitted review repository, and implementation
patches to existing CAO review-bundle artifacts plus a central review issue.
It never creates a target PR. Review proposals cannot satisfy dependency merge
gates. Live target issues and PRs require an explicit reviewed policy change
enabling the campaign, worker, targets and live ceiling together. Merely
approving the plan is insufficient.

The coordinator freezes ready tasks in a pre-agent artifact. Dispatch and
publication guards bind the task, phase, digest, destination, mode, originating
workflow/run, and workflow SHA; handoffs expire after one hour. The publication
guard runs in a read-only prerequisite job, rechecks the target head and rejects
existing task outputs. Native publication retains CAO's shared dispatcher
telemetry. Review bundles wait for that guard through the native publication job,
rather than bypassing it through an independent upload job.
If the base moves or a handoff expires, run the coordinator again; do not retry
with forged or manually altered inputs.

## Reviewed plan contract

The production validator is [`engagement.mjs`](engagement.mjs). No additional
fields are accepted. Use actual roster directory IDs from
`squad/.squad/agents/` in `specialists`, not invented job titles.

```json
{
  "schema": "cao-modernization/v1",
  "id": "zava-modernization",
  "objective": "Modernize the shared API without breaking its consumers",
  "repositories": [
    {
      "repository": "octodemo/example-api",
      "base_sha": "0000000000000000000000000000000000000000",
      "base_branch": "main"
    }
  ],
  "tasks": [
    {
      "id": "versioned-api",
      "repository": "octodemo/example-api",
      "title": "Introduce the compatible API",
      "objective": "Keep existing consumers working during migration",
      "acceptance": ["Existing consumer contract suite passes unchanged"],
      "depends_on": [],
      "specialists": ["REPLACE_WITH_ACTUAL_ROSTER_DIRECTORY_ID"]
    }
  ],
  "integration_acceptance": ["All enrolled consumer end-to-end suites pass against the merged API"]
}
```

The example is illustrative, not executable approval: replace the repository,
baseline SHA and specialist. Real multi-repository plans list every participant
and link consumer task IDs to prerequisite task IDs.

Task bodies carry both `<!-- cao-modernization:DIGEST:TASK-ID -->` and
`<!-- cao-modernization-phase:issue -->` or `implement` markers. Progress comes
from bounded GitHub issue/PR/review/check evidence, never repository memory or
Squad chat. Plan edits change the digest and require a new reviewed engagement;
they intentionally do not silently adopt work from an old plan.

## Limits and failure handling

- One active plan per control revision; at most 50 tasks and ten repositories.
  The entire engagement must fit the current admitted inventory cell/batch.
- A default-branch change, non-ancestor baseline, duplicate task identities,
  closed uncompleted issue, closed unmerged PR, missing review/check evidence,
  inaccessible repository, or incomplete pagination blocks progression. Diagnose
  and revise the engagement instead of manufacturing success.
- Evidence lists are capped at ten 100-row pages. Large histories can require a
  narrower engagement or a future indexed-evidence adapter; truncation is an
  error, not proof that no output exists.
- With gh-aw v1.2.3, the task checkout's job-conditional read/write credential
  expression produces four informational unknown-step warnings: each job names
  the other job's credential step in an inactive branch. Compilation succeeds;
  authenticated Actions execution remains a separate acceptance gate.
- Squad's independent internal patch review does not replace required GitHub
  review or system integration acceptance. Completion is not an attestation of
  correctness or production readiness.
- No automatic PR merge, review-thread repair loop, cloud deployment, background
  heartbeat, continuous Squad state service, or arbitrary cross-owner system
  token aggregation is included. Team state is recreated per run; durable state
  is the reviewed plan and linked GitHub outputs.

Source provenance, copying permission, pinned revisions and adaptations are in
[`squad-source.json`](squad-source.json). Source engagement histories and customer
memory are deliberately excluded. No license is invented for the source team.
The framework coordinator's upstream license and notices remain with its assets.

Offline compilation, contract tests and a healthy Kubernetes dashboard do not
prove authenticated Actions/Squad execution or live repository acceptance. Those
require an explicitly enrolled test system and separately reviewed activation.
