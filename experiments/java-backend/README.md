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

- `OrchestratorPlanner` / `InventoryPartition` / `EffectiveRecord.buildOrchestrator`: orchestrator planning
  from caller-supplied inventory and workflow evidence (no GitHub access): scope-checked inventory,
  deterministic cell/batch partitioning with JavaScript-compatible inventory digests, per-target modes,
  eligible worker workflows and the effective repository cap, emitted as the §6.3 orchestrator record.

- `operational` package: PostgreSQL adapter for the operational `AuditLog`
  (`specs/server-operational-storage.md`): tamper-evident SHA-256 hash chain compatible with the Go
  canonical record bytes, anchors, namespace isolation, runtime-versus-owner separation
  (`OperationalBootstrap` creates tables; the runtime opens pre-provisioned state only), and
  verified-TLS-or-loopback URL validation. Only audit is implemented; no other operational service exists.
- `ApiServer`: `GET /api/health` and `GET /api/readiness` per `server/spec/generated/openapi.json`.
  With `CAO_POSTGRES_URL` set, operational connectivity is probed live; canonical data does not
  exist yet, so both endpoints still report 503/unavailable. Without it both report everything unavailable.
  Other routes return 404.
- `Dockerfile`: non-root, provider-neutral; no data or credentials baked in.

Provision (schema owner only, once) then run (after `verify`):

```sh
CP="experiments/java-backend/target/cao-java-backend-0.1.0-SNAPSHOT.jar:experiments/java-backend/target/lib/*"
CAO_POSTGRES_URL=postgres://owner@127.0.0.1:5432/cao java -cp "$CP" org.cao.backend.operational.BootstrapMain
CAO_POSTGRES_URL=postgres://runtime@127.0.0.1:5432/cao CAO_PORT=8080 java -cp "$CP" org.cao.backend.http.Main
```

Runtime needs only SELECT on `cao_operational_namespaces`, `UPDATE(protected_bytes, protected_entries)` and
SELECT/INSERT on `cao_operational_audit_events` for audit (see `server/postgres/operational-grants.sql`).
`CAO_POSTGRES_URL` secrets belong in an Actions/secret store, never an image; a non-loopback host needs
`sslmode=verify-full`. `CAO_OPERATIONAL_NAMESPACE` defaults to `CAO_DATABASE_NAMESPACE`, then `hosted-dashboard`.
Startup fails (non-zero) for an invalid, insecure or unprovisioned store.

Run without storage (after `verify`): `CAO_PORT=8080 java -cp "experiments/java-backend/target/cao-java-backend-0.1.0-SNAPSHOT.jar:experiments/java-backend/target/lib/*" org.cao.backend.http.Main`.
Env: `CAO_BIND` (default `127.0.0.1`, numeric IP; non-loopback requires `CAO_ALLOW_NON_LOOPBACK=true`),
`CAO_PORT` (default 8080), `CAO_SHUTDOWN_GRACE_SECONDS` (default 10).
Container: `docker build -t cao-java-backend experiments/java-backend` (not executed here).
Never expose the listener publicly: there is no authentication yet.

## Build and test

Requires JDK 21 and Maven 3.9+. All plugin and dependency versions are pinned.

```sh
mvn --batch-mode --no-transfer-progress -f experiments/java-backend/pom.xml verify
```

`verify` runs JUnit 5 tests (including real-PostgreSQL tests that start a disposable loopback instance from local
PostgreSQL server binaries in `/usr/lib/postgresql/<n>/bin` or `CAO_TEST_POSTGRES_BIN`; they fail, not skip, without them), compiles with `-Xlint:all -Werror`, and enforces Checkstyle.
