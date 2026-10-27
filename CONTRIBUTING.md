# Contributing to Central Agentic Ops

Thank you for your interest in contributing to Central Agentic Ops! We welcome
contributions from the community and are excited to work with you.

**IMPORTANT: This project uses agentic development by a core team, primarily
using Copilot coding agent or local coding agents.**

**Traditional pull requests are not enabled for contributors without write
access.** If you do not have write access, please do not create a pull request
directly. Instead, create a detailed agentic plan in an issue, discuss it with
the team, and a core team member will create and implement the pull request
using agents.

Contributors who have write access but are not yet core team members may create
pull requests by following the review workflow below.

## How development works

Central Agentic Ops is developed by a core team using agentic development. This
means:

- **Core team members use agents to create and manage pull requests**, through
  Copilot coding agent or local coding agents.
- **Continuous integration provides automated quality assurance** for every
  pull request.
- **Community members contribute through agentic plans** that the core team can
  execute.
- **Contributors with write access work in draft pull requests** until their
  changes are ready for core team review.

### Why this approach?

Central Agentic Ops practices the development model it supports: agents help
build and operate agentic workflows. This approach provides:

- **Consistency:** Changes go through the same automated quality gates.
- **Dogfooding:** We use agentic development to build agentic operations.
- **Established practices:** Agents follow the repository's documented patterns
  and safety boundaries.
- **Actionable plans:** Contributors think through implementation and validation
  before work begins.

## Quick start for community contributors

**If you do not have write access, do not create a pull request directly.**
Instead, craft a detailed agentic plan in an issue so a core team member can
implement it using agents.

### Step 1: Analyze with an agent

Before filing a contribution request, use an agent to:

- Scan the relevant source code and identify root causes for bugs.
- Analyze execution patterns and trace the issue.
- Research related issues and existing implementation patterns.
- Propose specific changes, including code examples when useful.
- Create a complete plan for implementation and validation.

For CAO deployment failures, use the
[`debug-cao` skill](skills/debug-cao/SKILL.md) to preserve the failing revision,
narrow the fault to one system boundary, and collect reproducible evidence.

Remove credentials, private repository data, and other sensitive information
from prompts, plans, examples, and logs.

### Step 2: Open an issue with your agentic plan

Search existing issues first, then open an issue that:

- Describes what you want to contribute.
- Includes the agent's analysis and findings for bugs.
- Explains the use case and expected behavior.
- Provides a complete, step-by-step implementation plan.
- Identifies the relevant files, tests, and documentation.
- Includes specific implementation details and examples.
- Uses the appropriate issue labels when available.

## Pull requests for contributors with write access

If you have write access but are not yet a core team member:

1. Open your pull request in draft mode and continue working there.
2. When the change is complete and validated, mark the pull request ready for
   review and request a review from the maintainers.
3. The maintainers will either approve and merge it immediately or return it
   with comments for follow-up.

## Core team implementation reference

Core team members implementing an accepted plan should use Node.js 24 and npm,
then install dependencies and the pinned `gh-aw` compiler:

```bash
npm ci
npm run install:gh-aw
```

If npmjs access is blocked on the Microsoft corporate network, follow the
[1ES installation instructions](AGENTS.md#installing-npm-dependencies) instead of
the default `npm ci` command.

Keep changes focused and include tests for new behavior. Edit agentic workflow
sources in `.github/workflows/*.md`; do not edit generated
`.github/workflows/*.lock.yml` files directly. Preserve the control-plane safety
boundary: workers operate on one dispatched repository, and rollout policy
remains in `.github/workflows/cao.json`.

See [AGENTS.md](AGENTS.md) for architecture, repository conventions, and focused
test commands. Use the narrowest applicable checks while iterating. To run the
complete root validation locally:

```bash
npm run check
```

Workflow source changes must pass `npm run compile`. Run
`npm run compile:locks` only when generated lock files should be updated.

`npm run test:integration:installer` streams the root installer into Bash,
downloads real public gh-aw and gh-cao releases with unusable tokens, and installs
the local catalog's runtime in a temporary control checkout. Only control-repository
sign-in and identity are simulated; public downloads, upstream installation,
checksums, native extension dispatch, runtime materialization, and policy initialization
are real. The test isolates HOME, GitHub CLI configuration, and extension storage,
verifies credentials remain unchanged, and removes its installation afterward.
It requires network access, Bash, curl, Git, GitHub CLI 2.68+, Node.js 24+, and the
repository's npm dependencies; CI runs it as a separate public-installer job.

### CI feedback tiers

PRs favor quick feedback over exhaustive coverage: affected root unit areas,
lint/type checks, compilation, dashboard unit/build checks, and short-mode Go
tests remain enabled. Browser matrices, Windows server checks, public download
and authenticated compatibility tests, bootstrap, and service-backed integration
run after merge rather than on the PR critical path. A green PR is intentionally
not a claim that every integration or performance scenario passed.

Main pushes select full suites through each workflow's path filters. Root
contract areas are further selected by `scripts/ci-test-plan.mjs`; documentation-only
changes omit root integration/load/API checks. Unknown paths or an unavailable
comparison base widen selection. Fast contract checks run concurrently with units;
the existing `test` aggregate check requires both to succeed.

`Daily full validation` (`.github/workflows/ci-daily.yml`) runs at 06:37 UTC
and can be dispatched manually on main. It reuses the complete contract,
dashboard, Go, bootstrap, simulator, Azure, query/MCP parity, mobile, view,
deployed-data, worker-memory, and stress suites without change filtering.
This replaces the separate daily stress schedules and hourly massive-scale run.
The 15-minute deployed health check remains, but its expensive query performance
and cost benchmarks run only on relevant main pushes, manual runs, and the daily
full suite.

The daily run publishes an Actions summary and a 30-day `ci-daily-report`
artifact (Markdown and JSON), including every suite's outcome, total completed
runner-minutes, and the 20 slowest jobs. Per-suite traces, profiles, and detailed
reports remain attached to the same run. Failed, cancelled, or skipped suites
make the aggregate report incomplete. Campaign lifecycle API-quota exhaustion
also fails daily/manual full runs; main pushes retain infrastructure-incomplete
warnings instead of blocking on shared quota.
These measurements exclude queue time and billing multipliers. Use them to
compare test cost and find regressions rather than assuming savings from job
count alone. Required branch checks should use the fast PR jobs, not daily or
main-only integrations.

### Documentation sources and validation

Edit the authored source, not another copy of its rendered output:

| Surface | Source of truth |
| --- | --- |
| Repository entry point | Root `README.md`; keep it short and route readers to task guides |
| Documentation pages and landing page | `docs/*.md` and `docs/README.md`; `docs/content.config.ts` loads these, while `astro.config.mjs` owns navigation and redirects |
| Catalog pages | `docs/pages/catalog/` renders campaign manifests and READMEs through `docs/lib/catalog.ts`; the repository README and site rendering of a campaign are intentionally the same source |
| Contributor architecture and contracts | `ARCHITECTURE.md`, its compiled `CODEBASE.yml`, and `specs/`; the reader-facing `docs/architecture.md` explains those boundaries without replacing them |
| Agent discovery | Generated `llms*.txt`, `agent/` indexes, and per-page `index.json`; edit the source Markdown/metadata, never generated output |

Some authored pages, such as `docs/control-policy-specification.md`, are
intentional pointers to normative contracts. Others, such as
`docs/dashboard-language-specification.md`, contain the contract itself.
Do not treat these as generated files or merge them merely because their titles
overlap. See [Agent-readable documentation](docs/agent-resources.md) for metadata
and discovery generation.

Keep one procedural home per task: [setup](docs/setup-quickstarts.md),
[credential provisioning](docs/control-plane-authentication.md),
[runtime authentication](docs/authentication.md),
[Pages deployment](docs/deployment-actions.md), and
[plugin/canvas use](docs/agent-plugin.md). Scope-specific guides should retain
their distinct decisions and caveats, linking to shared procedures instead of
copying them.

Follow [gh-aw's navigation](https://github.com/github/gh-aw/blob/main/docs/astro.config.mjs)
for shared section labels and order. Keep CAO-specific groups only where useful;
do not add pages merely to fill an upstream category.

Keep a visual only when it explains a boundary, relationship, or task better
than the surrounding text. Prefer one canonical diagram to repeated storyboards;
label screenshots as snapshots and examples as illustrative. Check labels
against the current implementation, both themes, and narrow screens. Remove
retired assets with their references. Branding variants are intentional:
`docs/assets/social-card.svg` is the editable source for `public/social-card.png`.

Use relative `.md` links between authored pages; `docs/rewrite-docs-links.mjs`
converts them to site routes. Preserve moved section anchors or update all
inbound references, and do not redirect a still-authored page to a different
guide with incompatible anchors. Validate documentation changes with
`npm run docs:build` (including generated agent-doc validation and discovery
tests) and `npm run compile` after Markdown edits. Never hand-edit or commit
generated `llms*.txt` output.

### Go extension development

The root Go module provides the operator-facing `gh cao` GitHub CLI extension.
It is independent of the existing `server/` module. `make check` scopes Go checks to `cmd/` and `internal/`,
excluding dashboard server tests and Go files inside npm dependencies.
Extension CI also runs golangci-lint with the root `.golangci.yml`, including
gofmt/goimports checks and the standard linters. To run those checks locally:

```bash
go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0
golangci-lint fmt --diff ./cmd/... ./internal/...
golangci-lint run ./cmd/... ./internal/...
```

Formatting diffs must be empty; CI fails when the formatter reports changes.

`make test-doctor` runs the dependency-free doctor unit suite with injected
GitHub CLI responses, an empty executable search path, isolated GitHub CLI
configuration, and no authentication tokens. These tests also run in `make check`;
they require neither `gh`, gh-aw, Node.js, nor network access.

`make authtests` separately runs the `authtests`-tagged doctor integration suite
against the real GitHub CLI, the policy-pinned gh-aw installation, and GitHub
authentication. It checks both text and JSON diagnostics without changing policy.
Missing prerequisites fail the integration suite rather than skipping checks.
CI provisions gh-aw and a read-only token only in the separate authenticated
integration job; unit tests and ordinary smoke tests do not need them.

Use the Go version declared in `go.mod` or newer, GitHub CLI, and Make. For local
extension installation, the checkout directory must be named `gh-cao`:

```bash
make check
make build VERSION=dev
gh extension install .
gh cao --help
gh cao version
gh cao doctor
```

Build before installing locally: GitHub CLI links to the root `gh-cao`
executable, and rebuilding updates that local installation. The extension is
named `gh cao`; the Go module path is `github.com/githubnext/gh-cao`.
In a differently named worktree, run `./gh-cao` directly instead.
The root installer downloads verified release assets directly into GitHub CLI's
extension data directory under `gh-cao`, so released installs use `gh cao`
without requiring a local checkout. It refuses to overwrite local linked/source
installations.
On Windows, build `gh-cao.exe` with
`go build -o gh-cao.exe ./cmd/gh-cao`.

| Path | Responsibility |
| --- | --- |
| `cmd/gh-cao/` | Process entry point, interrupt cancellation, exit status, and link-time version (`-X main.version=...`). |
| `internal/cli/` | Native Cobra operator commands, onboarding and authentication, policy and campaign management, read-only SQLite and Dashboard Language queries, computations, MCP, help, and diagnostics. |
| `internal/ghcli/` | Context-aware GitHub CLI calls through `github.com/cli/go-gh/v2`, the same helper used by gh-aw. |
| `dashboard/catalog.go` | Embeds the authoritative dashboard JSON, fragments, canonical query projections, and MCP contract in the standalone binary. |
| `go.mod`, `go.sum` | Pinned extension dependencies, separate from `server/go.mod`. |
| `Makefile` | `build`, `release`, `fmt`, `lint` (gofmt and go vet), `test` (race detector), dependency-free `test-doctor`, opt-in `authtests`, and `check`. |
| `scripts/build-release.sh` | Cross-compile six extension binaries with the exact release tag embedded in `main.version`, then write SHA-256 checksums. |
| `.golangci.yml` | Root-module standard linters and gofmt/goimports formatting, separate from the server configuration. |
| `.github/workflows/go-extension.yml` | Path-scoped extension formatting, golangci-lint (including tagged auth tests), vet, tests, build, smoke checks, and separate authenticated integration tests. |

Reuse `github.com/github/gh-aw/pkg/logger` rather than implementing another
logger. Enable its namespaces with `DEBUG=cao:*` and disable debug colors with
`DEBUG_COLORS=0`. Log only non-sensitive metadata, never credentials, raw
arguments, or subprocess payloads. Import small shared gh-aw utility packages
as needed rather than its full CLI command implementation.

Help for every operator command and version work offline without GitHub authentication.
Guided onboarding runs as `gh cao setup`; independent authentication profiles
are grouped under `gh cao setup auth github-app|enterprise-app|hosted-cli|token`, not a
root-level `setup-auth` command. These native commands preserve the setup and
setup-auth JSON result contracts and secure credential prompts.
Actions and Node-only tools invoke `node activity/cao.mjs <command>` directly,
including SQLite `repair-database`. Hosted-executor App creation belongs to native
`gh cao setup auth hosted-cli`; its retired Node profile fails before side effects.
There is no shell launcher;
retained internal Node implementations serve Actions and parity tests, not operators.
`doctor` checks GitHub CLI, gh-aw, authentication, and basic local policy structure
and compiler pin without loading Node.js. Authentication may contact GitHub.
Every operator command runs in Go without Node.js or an installed JavaScript
runtime. `CAO_RUNTIME_ROOT` has no effect. Run policy mutations from the control
checkout root. Keep the root Go module independent of the server module.

Preserve production onboarding, authentication, policy handling, computation,
and Dashboard Language semantics with focused native tests and the JavaScript
implementation as a parity oracle in tests only. Never invoke a JavaScript
runtime from a production extension command. Node.js 24+ and root `npm ci`
enable parity-oracle coverage; tests skip those comparisons when Node is absent.
Catalog definitions and canonical
source mappings are embedded directly from their authoritative JSON; do not
maintain a separate catalog. SQLite evidence access is read-only and does not
migrate or repair downloaded projections. `TestNativeStandaloneBinary` runs
the built binary with an empty `PATH` outside the checkout to enforce this boundary.
Campaign installation through `add` and package updates through `update` pass
evolving gh-aw options unchanged. Standalone workflow installation retains its
bounded `--force` and `--engine` options.
Activity/Dashboard pipeline commands are deliberately not registered in Go;
their Actions, Node command modules and runtime ownership remain unchanged.
CI invokes the shipped JavaScript validator directly with
`node activity/cao.mjs validate --json`; it does not build or download the Go
extension or require consumer npm metadata. User-facing commands belong in
`gh cao`; Actions execution support belongs in JavaScript. Control-plane
validation has one JavaScript implementation and no Go or shell-wrapper command.
Preserve its report and exit-code contracts in `tests/unit/cao-validation.test.mjs`.
Retain native policy guards used by operator construction and mutation.
The CI JSON artifact stays outside the
checkout in `RUNNER_TEMP`.
See [the operator migration table](docs/cao-cli.md#github-cli-extension).

#### Release binaries

Following the gh-aw release pattern, `make release VERSION=v1.2.3` writes
standalone `gh-cao-OS-ARCH` assets for Darwin, Linux and Windows on amd64/arm64
(`.exe` on Windows), plus `checksums.txt` under
`dist/gh-cao/`. The version must be a stable `vMAJOR.MINOR.PATCH` tag, and the
output directory must be empty to prevent stale assets from entering a release.
For a different output directory, run:

```bash
bash scripts/build-release.sh v1.2.3 .tmp/extension-release
```

Builds disable CGO, trim source paths, and use
`-ldflags="-s -w -X main.version=v1.2.3"`. Both `version` and `--version`
report that exact tag. Verify downloaded assets with
`sha256sum --check checksums.txt` on Linux or
`shasum -a 256 --check checksums.txt` on macOS.

The Release workflow runs extension checks and builds at the exact release SHA
in a read-only job with Go caching disabled, then checks the embedded version
before uploading its artifact. It also runs the real GitHub CLI against an
isolated local API fixture to install and dispatch that built binary. The
fixture reproduces runtime-only latest-release failures, including `--pin`,
and verifies unpinned and pinned binary installation without network access,
real credentials, or changes to the developer's installed extensions.
A separate source-free attachment job verifies
the binary checksums and the existing attested runtime. Publication requires
the exact eleven-asset set (runtime, Azure assets, six binaries, and checksums)
before the release becomes an immutable prerelease;
the highlights agent remains restricted to updating release notes.
`node --test tests/unit/build-release.test.mjs tests/unit/release-workflow.test.mjs`
checks the build contract and release gates.

Run `make build && node --test tests/integration/gh-extension-installation.test.mjs`
for the GitHub CLI installation contract (macOS or Linux; requires `gh`).
To check a release binary instead, set `CAO_EXTENSION_BINARY` to its absolute
path and `CAO_EXTENSION_VERSION` to the embedded release tag.

GitHub CLI uses the latest stable release to discover binary extensions, even
when `--pin` names a complete prerelease. A runtime-only latest release therefore
blocks both forms; adding binaries only to a prerelease is insufficient.
Publishing a complete prerelease does not authorize promotion. After independent
approval, **Promote prerelease to latest** revalidates the candidate, promotes it,
and verifies the actual unpinned public install and embedded version in isolated
extension storage. Only then is the public install path verified. No root
launcher or source-build fallback is needed.

### CI feedback and post-merge coverage

Pull requests run the fast checks relevant to their changed paths: repository
unit contracts, lint, type checks, workflow compilation, dashboard unit tests and
production build, Go quality and API contract checks, and focused Actions,
shell, installer, and SVG checks. Frontend-only changes do not start Go CI;
control workflow Markdown and generated locks do not start Actions lint, which
checks handwritten workflow YAML.

Pushes to `main` retain the integration, load, campaign lifecycle, browser,
mobile, Azure, query-parity, deployed-data benchmark, and performance suites.
Their workflow path filters limit execution to relevant inputs. Existing
scheduled and manual runs remain available; Dashboard CI can also be dispatched
manually. Superseded PR runs are cancelled without cancelling main-branch
validation.

Moving these suites after merge shortens PR feedback but does not establish
their results before merge. Run the applicable integration or browser tests
locally when changing those contracts, and monitor the main-branch checks after
merging.

Setup-path changes must also pass the clean consumer integration suite:

```bash
npm run test:integration:setup-pathways
```

Maintainers with access to the dedicated Contoso fixtures can dispatch the
complete authentication, Activity, private Pages dashboard, campaign review,
and bounded live-mutation acceptance journey:

```bash
npm run test:e2e:setup-pathways
```

To verify existing current-revision evidence without dispatching new runs:

```bash
npm run test:e2e:setup-pathways:contoso
```

By participating in this project, you agree to follow our
[Code of Conduct](CODE_OF_CONDUCT.md).
