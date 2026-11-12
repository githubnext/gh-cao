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

- `ApiServer`: `GET /api/health` and `GET /api/readiness` per `server/spec/generated/openapi.json`.
  With no operational store or canonical data adapter yet, both honestly report 503/unavailable.
  Other routes return 404. No database exists yet.
- `Dockerfile`: non-root, provider-neutral; no data or credentials baked in.

Run (after `verify`): `CAO_PORT=8080 java -cp "experiments/java-backend/target/cao-java-backend-0.1.0-SNAPSHOT.jar:experiments/java-backend/target/lib/*" org.cao.backend.http.Main`.
Env: `CAO_BIND` (default `127.0.0.1`, numeric IP; non-loopback requires `CAO_ALLOW_NON_LOOPBACK=true`),
`CAO_PORT` (default 8080), `CAO_SHUTDOWN_GRACE_SECONDS` (default 10).
Container: `podman build -t cao-java-backend experiments/java-backend` (not executed here).
Never expose the listener publicly: there is no authentication yet.

## Build and test

Requires JDK 21 and Maven 3.9+. All plugin and dependency versions are pinned.

```sh
mvn --batch-mode --no-transfer-progress -f experiments/java-backend/pom.xml verify
```

`verify` runs JUnit 5 tests, compiles with `-Xlint:all -Werror`, and enforces Checkstyle.
