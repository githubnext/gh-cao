# Spawn Reference

Pinned native Squad reference adapted to CAO native gh-aw local-state mode.

## Before spawning

Select one actual registered specialist through `.squad/routing.md`. Read and
inline its complete charter, including its scope and domain evidence stops.
Never load all charters. Verify qualification and independence before choosing
the identity. Review and validation are real separate delegated work, not
coordinator impersonation. Read `.squad/native-gh-aw.md` for capabilities.

Use the native host's `task` tool and its advertised schema. This is a Copilot
tool call, not a `squad` shell command, SDK call, MCP bridge or new session.
Use `agent_type: "general-purpose"` where the host supports it, the actual
cast ID for `name`, and a human-readable description containing the cast name.
Inherit model/reasoning/context unless the current task explicitly overrides
them through supported host fields. Do not copy model defaults from examples.

## Required prompt contents

```text
You are {actual display name}, the {actual role} on this team.
{complete actual charter}

TEAM_ROOT: {absolute path to the materialized .squad directory}
STATE_BACKEND: local
RUNTIME_MODE: native-gh-aw
REQUESTER / CURRENT_DATETIME: {actual supplied values, or explicitly absent}

TASK: {single concrete deliverable}
PHASE / TARGET / MODE: {admitted values}
AUTHORITY: {actual current issue, plan and source revisions and CAO admission}
DEPENDENCIES: {supplied proof and supported outstanding/blocked dependencies}
ALLOWED PATHS / ACTIONS / COMMANDS: {explicit current grant}
INPUTS: {specific allowed files and evidence, not all repository/team history}
EXCLUSIONS: {forbidden operations and adjacent work}
ACCEPTANCE: {actual approved measurable criteria}
OWNERSHIP: {exclusive paths, accountable identity, reviewer/validator identities}
{applicable mission/governance/process and native local-state constraints}

Read only your charter, shared decisions and specifically allowed task inputs.
No subdelegation, repository discovery, dispatch or governance/cast changes.
Do the domain work only within the stated phase and permissions.
No Git publication, issue/PR creation, merge, production or cloud operation.
Return proposed decisions and evidence in your result. Do not edit the shared
ledger, create histories or use squad_state_*/memory.* tools. Permitted local
audit writes require a serialized writer and exact allowed path.
Finish with actual changed paths/revisions, checks/methods/results, evidence,
unknowns/blockers, and the proposed decision or safe-output payload if requested.
Do not claim an unexecuted check passed.
```

Dependencies and assurance gates run serially. Parallelism requires distinct
owned paths and no shared-state writers. Choose supported synchronous/background
execution only after checking the actual tool schema; no hypothetical fields,
sub-session worktrees or CLI model resolver.

For reviewer handoffs include the exact complete diff, task and acceptance inputs,
not only the author's summary. Missing native delegation blocks the handoff.
Do not silently perform the specialist's work inline.
