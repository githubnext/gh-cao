# Native gh-aw local-state capabilities

Execution host: native Copilot `--agent squad --disable-builtin-mcps`.
The committed agent prompt, team, casting files, charters, references and skills
are sufficient team assets. **No Squad CLI, SDK package, model resolver, MCP
state bridge, marketplace, update check or upstream synchronization is required.**
Read the bundled skill documents directly with file tools.

## Supported capabilities

- Actual host-provided native specialist delegation, bounded file access and
  explicitly admitted commands. If native delegation or required command
  capability is absent, return a precise blocker; do not introduce a mock runner.
- The fixed actual 85-specialist/four-support cast, read from local committed
  team/routing/registry files. No runtime casting mutation.
- Plain local filesystem reads and explicitly allowed writes. Missing mutable
  state means no previous record, not a missing external service.
- One writer at a time for shared decisions/audits; coordinator or one synchronous
  Scribe may append accepted sanitized decisions. Support reviewers may append
  their own redacted audit evidence only when explicitly permitted and serialized.
- Preserve existing bytes and revision context, re-read after a write and report
  its actual result. Local files are job-local and are not durable across Actions
  jobs unless the declared safe-output/artifact mechanism explicitly retains them.

## Deliberate limitations

Ordinary file editing does **not** supply atomic CAS, atomic create-if-absent,
distributed locks, transactional multi-file state or concurrent-writer safety.
Do not emulate those guarantees with read-then-write. If they are needed, or
another writer/uncertain result is observed, stop the affected persistence action.
Avoid destructive inbox processing; retain source entries and use accepted
decision IDs to deduplicate. Do not delete or archive earlier evidence.

Only a current admitted write grant permits team-local runtime state edits.
Otherwise return the decision/evidence in the task result and state it was not
persisted. An optional unpersisted note need not block an otherwise evidenced
deliverable; required acceptance evidence must still be returned/retained through
the workflow's declared mechanism or the affected gate is blocked.

Remote/external/orphan/two-layer/git-notes backends, team-root redirection and
upstream inheritance are unsupported and rejected, not silently mapped to local.
No histories, memory/wisdom, monitoring, auto-init or CLI bootstrap is required.
Host tool restrictions and CAO exact-workflow-SHA policy remain authoritative.
This prompt is not a sandbox and grants no publication, dispatch or merge power.
