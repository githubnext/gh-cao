# CAO Java backend (experimental)

An independent Java 21 reimplementation experiment for Central Agentic Ops. It is
**not** a conforming CAO implementation, is not deployed, and grants no authority.
Only reviewed, merged increments count as progress; see `implementation-status.md`.

## What exists

The fail-closed policy/authority kernel for the control-plane subset of
`specs/control-architecture.md` (§5 configuration, §5.4 narrowing, §6.2 resolution,
§7 failure, Appendix B outcomes):

- `PolicyParser`: strict `cao.json` parsing (UTF-8, duplicate keys, unknown
  properties, `${{ }}` expressions, wildcards, `debug` modes, execution fields all rejected;
  unimplemented sections are rejected rather than ignored).
- `PolicyResolver`: campaign/worker resolution with owner/repository scope, review
  default, exact-target and worker-ceiling narrowing, and narrowing-only dispatch
  requests (widening throws `PolicyException`; expected denials return stable reasons).

No HTTP service, database or container exists yet, so no cloud packaging is included.

## Build and test

Requires JDK 21 and Maven 3.9+. All plugin and dependency versions are pinned.

```sh
mvn --batch-mode --no-transfer-progress -f experiments/java-backend/pom.xml verify
```

`verify` runs JUnit 5 tests, compiles with `-Xlint:all -Werror`, and enforces Checkstyle.
