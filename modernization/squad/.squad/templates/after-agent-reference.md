# After Agent Reference

Pinned native Squad completion reference adapted to native gh-aw local state.

1. Collect each real delegated result with the actual host's supported result
   tool and timeout bounds. Do not relaunch a task just to obtain its status.
2. Verify delivered paths and concrete command/evidence results. An empty
   response, changed file or newly existing output alone is not proof of success.
   Missing/uncertain results remain incomplete; do not retry an uncertain
   mutation automatically or mark it DONE.
3. Give the independent qualified Reviewer the exact current artifact/diff,
   acceptance requirements and original evidence. Enforce rejection lockout
   from `.squad/skills/reviewer-protocol/SKILL.md`. Review cannot grant scope.
4. Delegate independent Validator work only when admitted criteria and commands
   permit it. Preserve `blocked`, `conditional`, `fail` and `not-attempted`.
5. Report compact outcomes, actual revisions and remaining gates. Follow the
   assembled-output templates only where their raw evidence is safe to retain.
6. Scribe is optional and synchronous, only for actually accepted decisions and
   permitted local paths. Make it the sole shared-ledger writer, read/re-read
   local files, append accepted entries with stable supplied decision IDs and
   verify them. Keep source inbox entries; do not delete, archive or summarize
   previous evidence. No atomic create/CAS guarantee is claimed. Unexpected
   changes or uncertain writes stop the persistence action.
7. An unpersisted optional decision is explicitly `not persisted`. Required
   evidence must be returned/retained through the declared workflow artifact or
   safe-output mechanism or its gate remains blocked. Never require an absent
   state MCP service or create a history/log as a substitute.
8. Return the admitted deliverable, not new work. No automatic Scribe histories,
   session/orchestration logs, Ralph scan/rescan, ceremonies, follow-up dispatch,
   commits, issue/PR publication or merge. CAO owns the next lifecycle transition.
