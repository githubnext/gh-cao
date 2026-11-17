# CAO diagnostic report

Use one issue for one root cause.

## Required sections

1. **Summary** — observed failure and boundary.
2. **Impact and safety** — control repository, campaign, targets, effective
   mode, destination, observed writes, first occurrence, and frequency.
3. **Immutable versions** — campaign, workflow, gh-aw, action, runner, runtime,
   and target with full SHA or digest and evidence link.
4. **Reproduction** — credential type and scope without values, exact event and
   inputs, policy revision, command or run URL, first failing job and step.
5. **Expected and actual behavior**.
6. **Evidence** — exact attempt, first causal annotation or redacted excerpt,
   source permalinks, artifact/cache state, and comparable successful run.
7. **Boundary analysis** — primary category, last good boundary, first broken
   boundary, and adjacent causes excluded.
8. **Hypothesis** — one falsifiable sentence.
9. **Next diagnostic** — one bounded review-safe observation.
10. **Redaction statement** and the acceptance condition proving the root cause
    is fixed without weakening policy or safe-output boundaries.

Do not paste whole logs, omit versions, use mutable branch links when a SHA is
known, combine unrelated failures, or propose a broad speculative fix.
