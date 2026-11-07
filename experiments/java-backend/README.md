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

- `HealthServer`: public `/api/health`, `/api/v1/health`, `/api/readiness` on a JDK `HttpServer`,
  backed by `OperationalProbe`/`DataProbe` contracts. The shipped `Main` has no storage adapter, so it
  honestly reports unhealthy/not ready. No database, authentication or query API exists yet.

## Run

Configuration (environment, no secrets): `CAO_HOST` (numeric, default `127.0.0.1`), `CAO_PORT` (default
8080), `CAO_PROBE_TIMEOUT_MS` (3000), `CAO_SHUTDOWN_GRACE_SECONDS` (10). SIGTERM drains and stops.

```sh
mvn --batch-mode --no-transfer-progress -f experiments/java-backend/pom.xml package
CAO_PORT=8080 java -jar experiments/java-backend/target/cao-java-backend-0.1.0-SNAPSHOT.jar
# container (unexecuted proposal; run from experiments/java-backend after package)
docker build -t cao-java-backend:dev . && docker run --rm -p 127.0.0.1:8080:8080 cao-java-backend:dev
```

Never expose the listener publicly: it has no authentication. Future PostgreSQL URLs and credentials
must come from environment-injected secret references; none are baked into the image.

## Build and test

Requires JDK 21 and Maven 3.9+. All plugin and dependency versions are pinned.

```sh
mvn --batch-mode --no-transfer-progress -f experiments/java-backend/pom.xml verify
```

`verify` runs JUnit 5 tests, compiles with `-Xlint:all -Werror`, and enforces Checkstyle.
