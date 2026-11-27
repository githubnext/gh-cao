# Repository Ownership

**Status: mapping contract only; no operational campaign exists.** There is no
campaign manifest, policy registration, orchestrator, worker, or live evidence
producer. Do not register or enable this campaign until the missing
deterministic controls below are implemented and independently reviewed.

The intended outcome is for maintainers to maintain reviewed ownership for
active repositories already enrolled within the existing CAO policy. CAO
admission, credential reach, and compiled workflow capabilities remain
independent boundaries; this package grants no new scope or live authority.
The eventual campaign must never treat absence of evidence as an unowned repo.

## Mapping contract

The proposed control-repository path is
`.github/cao/repository-ownership/owners.yml`. It is not created by this
package. Only human-reviewed changes to the control repository should establish
or change it; actual review enforcement depends on repository protections.
Target files and inferred signals cannot override it. The YAML has this shape:

```yaml
version: 1
repositories:
  - repository: <owner/name>
    owners:
      - kind: <team|person|service>
        id: <provider-qualified-identity>
    reviewed_at: "<UTC timestamp>"
    review_due_at: "<UTC timestamp>"
    exemption:
      reason: <text>
      expires_at: "<UTC timestamp>"
```

The root and entry fields shown are required except `exemption`, whose fields
are required when present. Owner lists have 1–100 entries. Identities are
`github:team:<org>/<slug>`, `github:user:<login>`, or
`service:<namespace>/<id>`. GitHub names and slugs can change; they are not
immutable provider IDs. A service ID is a human-managed identifier, not
proof that a GitHub account or service exists. Names and identities are
deduplicated case-insensitively. Timestamps are quoted `YYYY-MM-DDTHH:mm:ssZ`;
the due date and exemption expiry must be after the review timestamp. A
mapping is stale at its due timestamp and an exemption expires at its expiry
timestamp. An exemption is a declaration, not independently verified
authorization to suppress work. Missing mapping means unknown, not unowned.

`mapping.mjs` provides an offline parser and time classifier using the
repository's existing `yaml` dependency. It checks document structure,
duplicate keys, aliases, limits, identities, and timestamps. It does **not**
verify identities, protected-branch review history, repository enrollment,
credentials, evidence freshness, or exemption authority. No workflow loads
it. An installed control repository must provision a verified,
compatible parser before runtime integration; do not install dependencies
on the fly as a missing-evidence fallback.

No inferred observation schema, output, or dashboard source is implemented.
Any future observation must carry actual provenance, retrieval time,
availability and completeness, and remain separate from confirmed mappings.
No mapping, test fixture, or workflow run is operational ownership evidence.

## Required before activation

- Implement deterministic policy-bounded inventory, active-status checks,
  fair batching and dispatch authorization, with exact target/mode binding.
- Resolve how to package the parser: current operational-campaign contract
  tests prohibit manifest `resources`, and the repository's `yaml` dependency
  is not guaranteed in a control-repository workflow.
- Design bounded advisory campaign memory only after dispatch state has a
  verified owner and lifecycle. No memory-writing job should run when
  activation is blocked.
- Validate the mapping at the exact workflow revision with trusted review
  provenance and handle renamed, inaccessible, deleted, or ambiguous identities.
- Define and verify real evidence producers, freshness windows, partial
  results, breakers, request and AI budgets, and grace and exemption semantics.
- Add independently dispatchable enforcement logic with handler-enforced
  issue deduplication and safe resolution. Never close an issue solely because
  evidence is missing or stale. Property updates and archiving are
  proposal-only unless supported by a reviewed, bounded safe output; automatic
  archiving is disabled.
- Prove the control-repository packaging, tests, and an authorized review-mode
  run. Keep `review` as the default. Do not assume live execution or promote
  from review based on compilation.

An operational-value evaluator is not supplied: there is no independently
observable worker outcome or verified evidence producer to measure yet.
