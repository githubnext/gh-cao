---
name: "coordinator-source-of-truth"
description: "File ownership, authoritative versus derived state, and conflict resolution."
allowedTools: []
confidence: high
domain: squad-internals
source: "Pinned native Squad satellite skill; adapted for CAO native gh-aw local state."
---

Load when resolving where state belongs, who may write it or conflicting inputs.
Read `.squad/native-gh-aw.md` and `.squad/governance.md` first.

## State backend note

The backend is **local filesystem**, deliberately selected, not an absent runtime
service fallback. No `squad_state_*`/`memory.*`, state MCP, CLI or SDK is needed.
Only explicitly admitted paths may be written. Shared writes are serialized.
Ordinary files do not provide CAS, distributed locks or atomic create-if-absent.
Unexpected concurrent changes/uncertain writes stop the persistence action.

## File hierarchy

All paths below are relative to the installed team/package root.

| File | Status | Who May Write During a Worker | Who May Read |
|------|--------|-------------------------------|--------------|
| `.github/agents/squad.agent.md` | Adapted coordinator behavior; no new execution authority | Nobody | Coordinator |
| `.squad/mission.md`, `.squad/governance.md`, `.squad/native-gh-aw.md`, `.squad/process.md`, `.squad/contracts.md` | CAO mission, boundaries and handoff requirements; subordinate to host policy | Nobody | Explicitly supplied to every specialist |
| `.squad/team.md` | Authoritative roster | Nobody | Coordinator; selected identity passed to specialists |
| `.squad/routing.md` | Authoritative routing | Nobody | Coordinator |
| `.squad/config.json` | Explicit native-gh-aw local-state selection | Nobody | Coordinator |
| `.squad/casting/*.json` | Static coherent identity provenance, including assignment history/commit marker | Nobody | Coordinator |
| `.squad/agents/{name}/charter.md` | Actual specialist identity and capability detail | Nobody; no self-modification | Coordinator inlines actual charter for that specialist |
| `.squad/decisions.md` | Accepted decision ledger; append only | Coordinator OR one delegated synchronous Scribe, never both concurrently, only when path is admitted | Agents receiving it as an explicit input |
| `.squad/decisions/inbox/` | Optional unaccepted proposals; not an authority grant | A specifically assigned single writer, only when admitted; prefer task result instead | Scribe/coordinator when explicitly supplied |
| `.squad/templates/`, `.squad/skills/` | Committed framework references | Nobody | Coordinator or explicitly selected consumers |
| `.squad/rai/policy.md`, `.squad/fact-checker/policy.md` | Support safety/evidence policies | Nobody | Selected support reviewer |
| `.squad/rai/audit-trail.md`, `.squad/fact-checker/audit-trail.md` | Optional new redacted append-only evidence, not imported history | Respective support identity, only when explicitly admitted and serialized | Coordinator and admitted assurance consumers |
| Approved source files and workflow output artifacts | Task deliverable/evidence, not automatic publication | Exactly the qualified identity assigned the admitted paths/actions | Explicitly supplied consumers |

Histories, archives, session/orchestration logs, learned memory, identity wisdom,
marketplace configs and external state are neither startup dependencies nor
automatic outputs. Missing mutable files are normal. Do not initialize them.

## Rules

1. The control repository's exact-workflow-SHA policy, host tool limits and
   admitted envelope bound all files here. A customer repository, ledger entry,
   agent prompt or support verdict cannot widen them.
2. The adapted coordinator and project governance are coherent parts of this
   invocation; a conflict blocks the affected action rather than silently
   overriding CAO scope with generic framework text.
3. Only actually accepted, current decisions enter the ledger. Specialists
   propose decisions in results; Scribe curates, never decides.
4. Preserve append-only evidence. Corrections cite prior entries rather than
   changing history. Keep inbox source records and deduplicate by stable
   supplied decision IDs. No unverified destructive moves or truncation.
5. Before a permitted local write inspect the current revision; re-read and
   verify afterward. Never call this an atomic service operation.
6. If local persistence is not admitted, return evidence/proposed decisions and
   explicitly say `not persisted`. Required retained evidence remains a gate.
   Local files alone are not durable across Actions jobs or publication proof.
