# ADR 21442: Advisory Dashboard Dependency Diagnostics Without Installation

## Status

Draft

## Context

`gh cao dashboard` manages a local PostgreSQL container through Docker or Podman unless an explicit `--postgres-url` or `CAO_POSTGRES_URL` is supplied. Before this pull request, a missing or unusable engine produced terse errors such as `docker is not installed`, and the failure text did not preserve the underlying check error in a form operators could act on.

Pull request #21442 changes the diagnostics only:

- A missing executable is reported with its `exec.ErrNotFound` cause preserved. An installed but failing engine keeps its check failure.
- Each failed engine receives OS-specific guidance for Windows 11+, macOS, and Linux. The guidance names package-manager commands and startup or context checks.
- Windows 11+ has two documented routes, because it does not include Docker or a Linux container engine by default: run CAO inside WSL 2 with Linux `gh`, CAO, and Podman, or run natively with a configured Docker Desktop or Podman engine.
- The guidance states that a separate PostgreSQL installation is not needed with Docker or Podman. Native PostgreSQL remains an explicit alternative that requires database provisioning before `--postgres-url` or `CAO_POSTGRES_URL` is supplied.
- The dependency checks are advisory. The pull request states that they perform no installation. The implementation calls `exec.LookPath` and runs engine status commands; it executes no package-manager command.

`specs/dashboard-data.md` already specifies managed local Docker or Podman provisioning and the bypass behavior for explicit connections. It does not state whether the CLI may install host software or how it treats missing dependencies. This pull request's authority boundary is therefore not yet recorded as a decision.

The pull request's own assessment states that installation and startup instructions were not exercised on Windows or Linux machines.

## Decision

1. `gh cao dashboard` diagnoses missing or unusable container engines and reports guidance only. It MUST NOT download or install a container engine, PostgreSQL, WSL, or any other host dependency.
2. Missing engines are reported separately from installed engines that are stopped, inaccessible, misconfigured, or pointed at an unsuitable endpoint. Missing engines receive installation guidance. Installed engines receive startup or configuration guidance and do not receive reinstallation advice.
3. Platform guidance is selected from the runtime operating system and the requested engine. Windows-specific WSL and Virtual Machine Platform guidance appears only for Windows.
4. The managed container path remains the default. Native PostgreSQL is documented as an explicit alternative that requires provisioning and an explicit connection.

## Alternatives Considered

- **Install a missing container engine or PostgreSQL automatically.** Not chosen. The pull request requires advisory checks that "perform no installation." The rationale for rejecting automatic installation, including its privilege, consent, or supply-chain implications, is Not inferable from current pull request evidence.

No second alternative is supported by the pull request evidence. Whether other diagnostic designs, such as a single Windows route, were considered is Not inferable from current pull request evidence.

## Consequences

**Positive:**

- Operators without an engine receive OS-specific next steps, including the Windows prerequisites that a default Windows 11 installation lacks.
- The CLI does not change host packages, services, WSL features, or PostgreSQL state during diagnosis, so the trust and privilege boundary of `gh cao dashboard` does not widen.
- Preserved check causes and state-specific guidance separate a missing dependency from a configuration failure.
- Explicit connection bypass and engine-selection behavior remain unchanged.

**Negative:**

- The package-manager identifiers, package names, and commands are literal strings in the CLI and documentation. They can go stale when upstream package IDs or distribution commands change, and this decision does not add a mechanism to detect that.
- The Windows WSL route requires users to install and authenticate Linux `gh` and the CAO extension inside WSL. Windows-only installations are not sufficient, and the guidance cannot perform that work.
- Because the instructions were not exercised on Windows or Linux machines in the pull request, their accuracy on real installations is unverified.
- Users must perform installation steps manually, so the CLI reports failure and guidance rather than completing setup.
