---
title: Local Worker Debugging
description: Use a local debugger to launch one CAO worker, inspect staged proposals, and iterate without a campaign dispatcher.
agent:
  type: operations
  prominent: true
---

Use `debug` to test one worker from your development branch against one enrolled
target. Your debugger runs locally, but the worker still executes in GitHub
Actions in the control repository. This is not a local Actions emulator.

**Debug belongs to a single workflow run, never a campaign.** Do not put `debug`
in `.github/workflows/cao.json` or use it with a campaign orchestrator. Persistent
campaign, target, default, and worker-ceiling modes remain `review` or `live`.
Debug does not enable a campaign, widen scope, or grant live authority.

## Before You Start

Work from a development branch of the control repository with GitHub CLI and
the policy-pinned gh-aw compiler installed. The branch must contain the worker,
its compiled lock, shared control with debug support, and the corresponding
policy. Local, unpushed edits are not used by an Actions run.

The campaign and worker must already be declared and enabled. Choose a target
inside the policy's owner scope and any exact repository allowlist. Existing
credential requirements, request limits, and GitHub API-capacity checks still
apply; a review-only worker may be debugged without promoting its policy.

Use the human operator's existing GitHub authentication with **write, maintain,
or admin** access to the control repository. Do not use a bot or pass credentials
in workflow inputs. Admission verifies the human sender and authoritative
event inputs; precompute checks the actor's current permission again. GitHub
cannot prove that a CLI/API request originated in a local debugger, so human
manual dispatch is the enforceable boundary.

:::caution[Staging is not a sandbox]
Shared control forces global gh-aw safe-output staging and suppresses automatic
activation and failure issues. Proposed outputs are previews, not executed
target changes. The run still reads target data and consumes Actions/model
resources. Repository-memory staging is not enforced by the currently pinned
gh-aw compiler; its separate persistence job remains unchanged. Do not treat
debug as a guarantee of zero writes or isolation from shared repository memory.
:::

## Prepare the Revision

Set these non-secret values for your control repository, enrolled target, and
worker workflow ID. The worker ID is the Markdown filename without `.md`, not
the campaign orchestrator:

```bash
CONTROL_REPO=acme/central-agentic-ops
TARGET_REPO=acme/example-service
WORKER=dependabot-update-planner
DEBUG_REF=$(git branch --show-current)

gh auth status
```

Edit `.github/workflows/$WORKER.md`, not its generated `.lock.yml`. Compile
after every change, including prompt-only edits:

```bash
gh aw compile "$WORKER" --strict --schedule-seed githubnext/gh-cao
git diff -- .github/workflows
```

If you change shared imports, compile all importing workflows with
`gh aw compile --strict --schedule-seed githubnext/gh-cao` and review every
affected lock. Commit and push the reviewed source and generated files before
launching. Include any changed shared imports and corresponding policy in the
same reviewed commit; do not broaden policy merely to get a debug run admitted.

For a worker-only change:

```bash
git add ".github/workflows/$WORKER.md" ".github/workflows/$WORKER.lock.yml"
git commit -m "Prepare worker debug revision"
git push origin "$DEBUG_REF"
```

The workflow must support `workflow_dispatch` and be registered on the control
repository's default branch before GitHub can dispatch it on your development
ref. A new workflow existing only on a feature branch is not sufficient.

## Launch One Worker

```bash
gh aw run "$WORKER" --repo "$CONTROL_REPO" --ref "$DEBUG_REF" \
  --raw-field target_repo="$TARGET_REPO" \
  --raw-field safe_output_repo="$TARGET_REPO" \
  --raw-field safe_output_mode=debug
```

`gh aw run` launches the worker; `gh run` observes the resulting Actions run.
`gh run` alone is not a workflow launcher.

Supply both repositories explicitly, with the same value. Debug previews the
target's normal output route rather than routing proposals to a review
repository. Supply any additional mission-specific required inputs within the
worker's existing bounds.

**Omit `correlation_id`, `central_repo`, and `control_plane_run_url`.** There is
no dispatcher, and nonempty dispatcher inputs reject debug admission. Never
invent an orchestrator URL or use debug to dispatch downstream work.

## Watch and Inspect

Find the manual run matching your branch and revision; do not assume the newest
run belongs to you when several developers are testing:

```bash
gh run list --repo "$CONTROL_REPO" --workflow "$WORKER.lock.yml" \
  --branch "$DEBUG_REF" --event workflow_dispatch --limit 5

RUN_ID=123456789
gh run watch "$RUN_ID" --repo "$CONTROL_REPO" --exit-status
gh run view "$RUN_ID" --repo "$CONTROL_REPO"
```

Start with the **Central Agentic Ops admission** summary and the first failing
step. If admission or precompute fails, the agent must not execute. Download
the bounded CAO evidence outside your checkout:

```bash
DEBUG_ARTIFACTS=$(mktemp -d)
gh run download "$RUN_ID" --repo "$CONTROL_REPO" \
  --name cao-admission --dir "$DEBUG_ARTIFACTS/admission"
```

For a run that reached successful precompute:

```bash
gh run download "$RUN_ID" --repo "$CONTROL_REPO" \
  --name cao-control-precompute --dir "$DEBUG_ARTIFACTS/precompute"
```

The precompute artifact has a one-day retention window, so collect it promptly.
It is not expected for a request denied before precompute.

Inspect `admission.json`, `control-precompute.json`, and the run's staged
safe-output previews. The handoff should record `safe_output_mode: debug`,
`launch_kind: manual-debug`, your `debug_actor`, `safe_outputs_staged: true`,
and the admitted target/output repository. Dispatcher correlation fields and
candidate/downstream-worker lists should be empty. Staged proposals do not
prove that a pull request, issue, comment, or other target mutation occurred.

Keep artifacts private and out of Git. Before sharing a diagnostic excerpt,
remove secrets, authorization headers, signed URLs, prompts, and private
target data.

## Iterate Without Changing Rollout

Preserve the run ID, attempt, workflow SHA, target, inputs, and first causal
error. Change one boundary at a time, recompile, commit, push, and manually
launch a fresh debug run. Confirm the new run uses the intended revision before
comparing proposals and behavior. A different actor cannot rerun your debug
attempt; they must launch their own manual run.

When using a local debugger agent, give it the control repository, worker,
development ref, target, and observed failure. The
[`central-agentic-ops` diagnostics guide](https://github.com/githubnext/gh-cao/blob/main/.github/cao/tasks/diagnostics.md)
provides a bounded diagnostic procedure. Keep it on the human operator's
authentication and require the single-worker command above; do not let it
enable workflows, promote a campaign, or widen scope as a workaround.

| Symptom | Next check |
| --- | --- |
| Debug rejected as a policy mode | Remove `debug` from persistent policy; use it only as the worker's dispatch input. |
| Manual-worker or original-human requirement failed | Select the worker, dispatch as the human operator, and avoid bots or another actor's rerun. |
| Actor write permission cannot be verified | Restore permission evidence/access; do not substitute dispatcher fields or skip the check. |
| Dispatcher-envelope error | Omit all three correlation inputs instead of copying an orchestrator envelope. |
| Target or output-route error | Use an already enrolled target and set `safe_output_repo` to that exact target. |
| Disabled identity, raised limit, missing credential, or insufficient API capacity | Follow the specific admission/precompute reason; debug does not bypass these gates. |
| Local changes absent from the run | Compile and push the intended ref, then verify the workflow SHA in the run evidence. |

After the debug loop, follow the normal reviewed workflow/policy change
process. A staged proposal cannot promote or authorize a later live run.
See [Admission Gates](admission.md) for gate diagnostics and
[Execution and Safety](execution-and-safety.md) for the execution boundary.

## Inspect server telemetry locally

For development diagnostics, the repository provides loopback-only Docker Compose
backends for the Go server's OTLP/HTTP exports. They are not part of any deployment.

| Command | Backend |
| --- | --- |
| `npm run dashboard:server:aspire-up` / `aspire-down` | Standalone Aspire dashboard on `http://127.0.0.1:18888`, OTLP/HTTP on port `4318` |
| `npm run dashboard:server:lgtm-up` / `lgtm-down` | Grafana LGTM (Grafana, Loki, Tempo, Prometheus) on `http://127.0.0.1:3000`, OTLP/HTTP on port `4318` |

Both publish port `4318`, so stop one before starting the other. Their receivers
are unauthenticated and keep data only until the container is removed. Before
`npm run dashboard:local`, clear inherited signal-specific `OTEL_EXPORTER_OTLP_*`
endpoints and headers, then set `OTEL_SDK_DISABLED=false` and
`OTEL_EXPORTER_OTLP_ENDPOINT=http://127.0.0.1:4318`. Add `CAO_OTEL_LOGS_ENABLED=true`
and a `DEBUG` namespace such as `cao:server,cao:telemetry` to export logs. See the
[local telemetry guide](https://github.com/githubnext/gh-cao/blob/main/server/LOCAL-TELEMETRY.md)
for the full procedure and opt-in export tests.
