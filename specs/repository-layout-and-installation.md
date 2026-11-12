---
title: Central Agentic Ops Repository Layout and Installation Specification
description: Normative layout, ownership, and installation requirements for CAO catalog and control repositories.
version: 1.0.0
status: Working Draft
editors:
  - GitHub Next
---

# Central Agentic Ops Repository Layout and Installation Specification

**Version:** 1.0.0  
**Status:** Working Draft  
**Latest Version:** https://github.com/githubnext/gh-cao/blob/main/specs/repository-layout-and-installation.md
**Editors:** GitHub Next

## Abstract

This specification defines the repository layout and installation lifecycle for
Central Agentic Ops (CAO). It establishes one canonical executable runtime
layout for catalog, source-managed, and installed control repositories, assigns
ownership to catalog resources and gh-aw metadata, and defines the
fail-closed materialization strategy required when gh-aw campaign installation
cannot place arbitrary executable resources at their canonical paths.

## 1. Status and conformance

This document is a Working Draft. Sections 2 through 7 are normative. The
abstract, examples, and rationale are informative unless they contain an
explicit normative requirement.

The key words "MUST", "MUST NOT", "REQUIRED", "SHALL", "SHALL NOT", "SHOULD",
"SHOULD NOT", "RECOMMENDED", "NOT RECOMMENDED", "MAY", and "OPTIONAL" are to
be interpreted as described in [RFC 2119](https://www.rfc-editor.org/rfc/rfc2119).

A conforming CAO catalog and a conforming CAO control repository satisfy all
applicable requirements in this document. A component manifest alone does not
claim conformance as a complete installation.

## 2. Repository roles and canonical paths

A repository MAY be a catalog, a source-managed control repository, an installed
control repository, or more than one of those roles.

| Path | Owner | Required role |
| --- | --- | --- |
| `aw.yml` | CAO catalog | Root installation bundle |
| `<campaign>/` | CAO catalog or materializer | Canonical executable campaign runtime |
| `activity/` | CAO catalog or materializer | Canonical Activity runtime |
| `dashboard/` | CAO catalog or materializer | Canonical Dashboard runtime |
| `plugin.json` | CAO catalog or Agent Plugin installer | Separately installed portable Agent Plugin manifest |
| `com.github.copilot/extensions/cao-dashboard/` | CAO catalog or Agent Plugin installer | Separately installed Copilot CLI extension and Dashboard canvas runtime |
| `specs/dashboard-data.md` | CAO catalog or Agent Plugin installer | Canonical data specification bundled with the separate Dashboard extension |
| `.github/workflows/` | Control repository and gh-aw | Workflow sources, generated locks, and CAO policy |
| `.github/workflows/cao.json` | Control repository | Persistent non-secret rollout policy |
| `.github/workflows/shared/materialize-cao.mjs` | CAO bootstrap | Trusted canonical-runtime materializer |
| `.github/actions/setup-cao-runtime/` | CAO bootstrap | Local runtime verification action |
| `.github/actions/setup-gh-aw/` | CAO bootstrap | Local policy-pinned gh-aw setup action |
| `.github/cao/instructions.md` | Control repository | Optional CAO operator guidance |
| `.github/aw/` | gh-aw | Instructions, campaign records, ownership metadata, and gh-aw runtime data |

An executable CAO resource MUST use the same repository-relative path in a
catalog and in a conforming control repository. A conforming implementation
MUST NOT create an executable CAO mirror below `.github/aw/`, nor resolve a
runtime file by probing both a canonical path and an alternate installed path.

`.github/aw/` records do not grant, narrow, or revoke CAO rollout authority.
Only the reviewed control-repository policy and compiled workflow can do so, as
defined by the control architecture specification.

## 3. Catalog manifest and ownership rules

The root `aw.yml` MUST include Activity and Dashboard as the deterministic
components of a complete CAO installation. Operational campaign manifests
MUST identify only the workflows and catalog resources that they own and MUST
NOT import the root catalog package. Operational campaigns MUST be installed
through `cao add` after control-plane installation so the lifecycle command can
materialize their canonical runtime without reinstalling control-plane or Agent
Plugin resources.

Activity and Dashboard component manifests are root-campaign components. Direct
`gh aw add` of either component MAY install its supported manifest resources,
but it MUST be treated as incomplete until canonical runtime materialization
has succeeded. Documentation and tooling MUST direct operators to the root CAO
installer or CAO lifecycle commands for a complete Activity and Dashboard
installation.

A campaign-owned canonical directory is replaceable by the materializer.
Operators MUST NOT rely on uncommitted local changes inside `activity/`,
`dashboard/`, or a materialized `<campaign>/` directory surviving an install or
update. Consumer-owned policy and steering files outside the selected
campaign-owned destinations MUST NOT be replaced by materialization.

Installed campaign records under `.github/aw/` MUST be modified
only through gh-aw campaign commands. CAO lifecycle tooling MUST consume those
records as provenance; it MUST NOT create substitute ownership records.

## 4. Canonical materialization

### 4.1 Provenance

Before replacing any runtime destination, the materializer MUST read installed
CAO campaign records and select only records belonging to the CAO
catalog repository. Each selected record MUST contain a full, 40-character
`resolvedCommit` SHA. Missing, shortened, tag-only, mutable, or otherwise
invalid provenance MUST fail closed.

For Activity, Dashboard, and root installation, the materializer MUST acquire
the prebuilt `cao-runtime-<resolvedCommit>.tgz` archive. Exact stable `vX.Y.Z`
source references use that release's assets; other references use an unexpired
artifact from a successful `cao-runtime.yml` push run at that SHA.
The materializer MUST verify GitHub's signed build provenance with `gh
attestation verify`, requiring the catalog repository, the
`.github/workflows/cao-runtime.yml` signer, matching source and signer commit
digests, and a GitHub-hosted runner before extracting the archive. It MUST then
validate the revision-bound file inventory, checksums, bounded ownership, and
required runtime resources in every selected bundle before replacing any
destination. Missing, expired, mismatched, or unverifiable artifacts MUST fail
closed, without latest-release or install-time source/npm fallback.

Operational campaigns retain source-archive acquisition by their immutable
revision. Workflow execution MUST NOT fetch a branch, tag, or alternate runtime
layout. Local developer source materialization is a separate, explicit path
that may use npm packing.

When the root record and exact focused Activity or Dashboard records coexist,
the focused record MUST own its corresponding canonical directory. The root
record MUST NOT overwrite that directory. Each selected revision MUST be
preflighted before the first replacement.

### 4.2 Replacement

Root materialization owns `activity/`, `dashboard/`,
`.github/actions/setup-cao-runtime/`, `.github/actions/setup-gh-aw/`, and
`.github/cao/instructions.md`. Every repository-local action referenced by a
workflow that the root campaign installs MUST be provided at its canonical path
by the root installation; installed workflow files MUST NOT be rewritten to
reference the catalog, because gh-aw records their content hashes.
Focused Activity or Dashboard materialization owns the corresponding canonical
directory. Operational campaign materialization owns its complete
`<campaign>/` directory.

Root materialization MUST remove a retired shell launcher only when its contents
match a known shipped SHA-256 digest. It MUST preserve unrelated files, symlinks,
and directories. Focused campaign updates MUST NOT retire root files. A verified
older runtime archive MAY contain the retired launcher; materialization MUST
verify that entry but MUST NOT install it. Failed preflight or verification MUST
NOT retire an existing launcher.

Activity and Dashboard materialization MUST copy only the production resources
selected at build time by `npm pack` using the dedicated
`runtime/package.json` manifest's `files` field. The package MUST include the operator CLI, collection and query
modules, local preview runtime,
dashboard build inputs, production browser sources, and required dependency
manifests at their canonical source paths. Compatible libraries may be bundled
with esbuild, retaining their license notices; filesystem-relative CLI modules,
workers, and build inputs MUST retain their canonical paths. Packing MUST disable
lifecycle scripts and MUST NOT install dependencies or publish to a registry. Package
metadata MUST remain in temporary staging rather than replacing a consumer's
root `package.json`. It MUST NOT copy dashboard tests, fixtures, screenshots,
development plans, lint/test configuration, or catalog-only component
manifests. Production runtime directories selected by the package's `files`
field MUST NOT contain development-only resources.

The producer workflow MUST build and test on each main push with read-only
repository permission, then attest in a separate job that does not check out
or execute source code. The release workflow MUST create a draft `vX.Y.Z`
release whose tag and target match an exact main SHA, wait for the successful
`cao-runtime.yml` push run at that SHA, download its prebuilt archive, verify
the checksum and signed provenance, and attach exactly the archive and checksum
to the draft. Missing, failed, mismatched, self-hosted, incomplete, or
unverifiable runtime evidence MUST leave the release unpublished. A maintainer
MUST publish the draft only after the release workflow succeeds and the notes
and assets are reviewed. Release assets MUST NOT be added or overwritten after
publication; publication makes the tag and assets immutable. Main artifacts
are retained for 90 days; release assets are the durable distribution path.
Normal installation requires Node.js 24 or newer and GitHub CLI v2.68.0 or
newer, authenticated before repository access. The installer MUST reject older
or unparseable versions before repository creation or file writes. Installation
MUST NOT invoke npm. Existing dashboard build dependency installation is
unchanged apart from omitting development dependencies.

The control-plane installer MUST NOT install the Agent Plugin manifest,
Copilot extension, bundled specification documents, or repository-only
dashboard authoring skills. Those resources belong to the separate Agent
Plugin or catalog authoring installation. Root materialization MUST preserve
pre-existing resources outside its owned destination set, including separately
installed plugin resources and skills.
The installer MUST add the repository-root `aw.yml` package. That manifest MUST
explicitly compose only the Activity, dashboard, and validation workflows, the
five shared control-plane resources, seven portable control-repository skills
(including the Agentic Workflows router), one Agentic Workflows agent, and the
gh-aw project settings. The router skill MUST load current routing guidance
through the canonical upstream Agentic Workflows skill on `main` instead of
copying its routing table. The custom agent MUST delegate to the installed
router skill instead of maintaining a second routing table.
If canonical guidance cannot be loaded, the router MUST fail explicitly rather
than use stale remembered guidance. Version-sensitive workflow behavior MUST
remain constrained by the control repository's policy-pinned `gh aw` CLI, and
shipped CAO skills MUST delegate parsing, mutation, compilation, querying, and
verification to existing deterministic `gh aw`, `cao`, and repository
validation commands. It MUST declare only
`.github/agents/agentic-workflows.md` in its nonempty `agents` list so agent
auto-discovery cannot install repository-only agents. Because gh-aw v0.91.5
auto-scans root `skills/` even when `skills` entries are explicit, that
directory MUST contain only the seven portable skills declared by the root
manifest. Additional package manifests MUST live only in campaign or component
directories.

For each selected owned destination, materialization MUST remove the existing
destination and copy the complete selected production bundle. Operational
campaigns retain complete-directory replacement. This replacement
rule ensures that a file removed by a newer selected revision cannot remain
executable. The materializer MUST NOT remove paths outside the bounded owned
destination set.

### 4.3 Lifecycle entry points

The root installer MUST download the compiled `gh-cao-OS-ARCH` release asset
(or the legacy `OS-ARCH` asset from an older published release)
and `checksums.txt` for the current supported platform (Linux, Darwin or Windows;
amd64 or arm64). It MUST verify exactly one matching SHA-256 entry and the
embedded version before atomically replacing the binary in GitHub CLI's
`gh-cao` extension data directory. It MUST honor `XDG_DATA_HOME` and Windows
`LOCALAPPDATA`, refuse linked/source installations, and preserve an existing
binary on verification failure. An explicit `githubnext/gh-cao@vX.Y.Z`
source MUST select that extension release; other sources MUST use the latest
published `vX.Y.Z` extension release, including prereleases but excluding drafts.
Runtime revision selection remains independent.
Before installing or upgrading tools or writing CAO files, the installer MUST
verify that the selected published release contains exactly one uploaded,
nonempty asset for each of the six supported platform binaries and
`checksums.txt`. The installer MUST select one complete naming set, preferring
`gh-cao-OS-ARCH` when both sets are complete, and MUST verify the checksum entry
for the exact downloaded name. It MUST NOT mix naming sets or retry another
asset after verification fails. Runtime-only or incomplete releases MUST fail
with an actionable release-contract error, without substituting another release.
An immutable published release cannot be repaired in place.
When an older verified extension initializes a new policy using the former
`githubnext/gh-aw-cao` schema URI, the installer MUST align only that metadata URI
with the installed catalog's canonical schema URI. It MUST reject unknown
mismatches and MUST NOT rewrite an existing policy or any authority fields.
The installer MUST invoke `gh cao init` and `gh cao setup`.
No shell launcher is installed. Actions and Node-only
tools MUST invoke `gh cao COMMAND` or another explicit Node
entry point directly, including SQLite `repair-database`.
User-facing commands MUST live in the Go extension except the Node-only
administration tools above. Actions execution
support MUST live in the JavaScript runtime under `activity/` and `dashboard/`.
Agentic workflow execution and agent evidence preparation MUST NOT build,
install, or invoke the native extension as a runtime dependency. Copilot setup
MUST stage dashboard evidence with `gh cao download`. Dedicated
extension build, release, and operator-installer compatibility tests are separate
from this runtime boundary.
Control-plane validation MUST have
one JavaScript implementation, without a Go or shell-wrapper command. Its workflow
MUST execute the shipped JavaScript runtime directly with
`node activity/cao.mjs validate --json` at its installed revision, using the
production policy resolver. It MUST NOT build or download the Go extension or
require consumer npm metadata. Runtime tests MUST preserve the structured report
and exit-code contract. CI MUST keep its report under `RUNNER_TEMP`, not
in the repository working tree.

The supported root CAO installer MUST materialize the root runtime after gh-aw
has resolved the root package record. For a main or SHA source, the
materializer MUST wait at most five minutes when the exact runtime build is
queued or running, then fail closed if it does not complete successfully; it
MUST NOT substitute another revision or build from source. `cao add` MUST materialize a selected
non-root campaign before reading its declaration. `cao add` MUST reject the
root campaign before invoking gh-aw and direct operators to the root installer.
`cao update` MUST materialize each updated selected campaign before merging
declarations.

Documentation MUST recommend `gh extension install githubnext/gh-cao` first,
followed by native `gh cao setup`. It MUST present the root `install-gh-cao.sh`
as a curl fallback in a callout for SAML-token installation failures, not as
the default extension installation path. The fallback MUST invoke the upstream
`github/gh-aw` `install-gh-aw.sh` with the
required version and checksum verification enabled. Public tool release discovery
and downloads MUST be anonymous, independent of the operator's GitHub token and
curl authentication configuration. The upstream installer MUST NOT inherit token
variables or the Actions `INPUT_VERSION` override that selects authenticated
extension installation. Anonymous rate limits and download/verification failures
MUST fail closed, not retry with operator credentials. This isolation MUST NOT
clear stored credentials or remove authentication from control-repository
creation, setup, or the separate runtime artifact/provenance acquisition path.

The installer MUST check tools, Node.js 24+, GitHub sign-in, and checkout-root
readiness before installation or upgrade. Creating a control repository MUST
require either explicit `--create-repo` or default-No confirmation on a
controlling terminal when no GitHub checkout or remote is available. An exact
`OWNER/NAME` argument MUST support non-interactive use. Interactive creation
MUST require an explicitly entered eligible owner and valid repository name,
without suggesting or preselecting either. Blank input MUST select nothing.
Availability lookup failures MUST be surfaced rather than treated as
availability. A Git checkout without remotes MUST be connected in place;
a directory without a Git checkout MUST receive a new clone. The terminal prompt
MUST support keyboard-driven entry and MUST fall back to plain text
when `ACCESSIBLE` or `NO_COLOR` is set or the terminal reports no styling
support. It MUST create a private repository and refuse an existing clone
directory. After a successful
installation without `--setup`, it MUST finish without
another prompt and print the setup command for later. `--setup` MUST open
guided setup directly on the controlling terminal without allowing streamed
children to consume installer source from stdin.
Repository creation MUST require explicit `--create-repo OWNER/NAME`
or interactive entry of both owner and name. No signed-in owner,
control name, target, or generated fallback coordinate may be inferred or
suggested. Occupied remote names or local paths, including dangling symlinks,
MUST be rejected without choosing a replacement. In-place connection of a
checkout without remotes MUST check remote-name availability without rejecting
its existing working directory. Lookup failures MUST stop without mutation
rather than implying availability. Existing repositories and paths MUST NOT
be reused or overwritten, and explicit names MUST NOT be silently changed.
Informational status lines SHOULD remain
aligned with completed steps while using muted styling to distinguish existing
state from completed work. Animated presentation MUST be disabled when
`ACCESSIBLE`, `NO_COLOR`, or `CI` is set or the terminal reports no styling
support.

Guided setup MUST separate prompt presentation from policy authority, show the
exact plan with default-No confirmation before applying settings, and atomically
save policy after scope and authentication succeed. Setup MUST NOT configure
Pages, change its visibility, or request public-dashboard consent.
The repository-scope prompt MUST state that comma-separated names and
`*` wildcard patterns are accepted; wildcard patterns MUST be expanded before
review so only exact repository names are saved. Cancellation before
application MUST leave policy and credentials unchanged. Failures after application starts MUST disclose possible
partial external changes rather than imply rollback. Setup MUST NOT install
campaigns, dispatch workflows, commit, or push. The UI and its license notices
MUST be bundled so installed setup does not need npm or consumer node_modules.

Without explicit repository arguments, setup without an interactive terminal
or with `CI` set MUST apply only a
deterministic public-control-repository profile. It MUST require the control
repository to be public, require an empty campaign map, and require existing
scope to be absent or already exactly the control repository. It MUST select
repository `GITHUB_TOKEN` (`workflow-token`) authentication, leave Pages untouched, set exact
self-only Activity scope, preserve unrelated policy fields, and write policy
last. It MUST fail before external mutation for private/internal repositories,
broader existing scope, or non-empty campaigns. It MUST use plain unstyled
output and MUST NOT prompt, open a browser, install a campaign, dispatch,
commit, or push. The explicit public-monitoring shortcut below and a previously
saved public-only scope MAY select a wider exact public collection scope.

Operational campaigns MUST remain separate from installation and setup.
Operators install campaigns with `gh cao add`, review and commit
workflows and policy together, then explicitly dispatch authorized work through
`gh aw run`. Installation and setup MUST NOT commit, push, or dispatch campaigns.
Validation MUST report a manually paused orchestrator with active workers as a
pause note rather than recommending scheduled re-enablement.

`control-plane.activity.repositories`, when present, MUST contain exact
repository coordinates, contain at least one entry, remain within
`control-plane.scope.allowed-owners`, and, when an exact repository allowlist is
present, be its subset. Activity authentication MUST use that declared
collection subset instead of silently narrowing broader rollout scope. Without
the field, existing fail-closed authentication behavior remains unchanged.

`control-plane.activity.public-only: true` MUST require an explicit exact
Activity scope and workflow-token authentication. It MUST NOT change campaign,
worker, operation, safe-output or live authority. Collection MUST verify the
control repository's identity and known visibility and verify all selected remote
repositories are public with unchanged identity before discovery and recheck each
target before collecting logs. Only the actual control repository identified by
the job's `GITHUB_REPOSITORY` MAY be private or internal, using its repository-local
token. Missing visibility, changed identity, or non-public remote targets MUST
block publication, including after a visibility change.
Restricted public evidence MUST remain explicitly partial, not complete
or zero; public-only collection MUST NOT invoke new operational-value collectors
or authenticated issue-status enrichment.

Guided setup SHOULD select public-only monitoring for the control repository and
public remote targets, regardless of the control repository's visibility.
When workflow-token authentication is already selected or is the default, setup
MUST NOT write an authentication variable or credentials. It MUST check repository
variables before inherited organization variables shared with that repository.
An existing conflicting effective mode MUST receive a repository-level
`workflow-token` override; unreadable or malformed settings MUST fail closed.
`gh cao setup --repository OWNER/REPO|OWNER/*` MAY configure
that profile without prompts in a public control repository; it SHALL NOT
publish operational evidence to Pages. Wildcards MUST resolve only currently public repositories to saved
exact names, and exact non-public selections MUST fail before mutation. Setup
MUST remain bare, with no campaign or agent execution, and MUST write policy
last. No user-supplied App/PAT is required.

`cao update` without a ref MUST retain release-based selection. An explicit
`main`, `master`, `latest`, or commit SHA MUST resolve once to a full catalog
commit SHA before any package is reapplied. Every selected installed CAO package MUST be reapplied
at that same SHA through gh-aw, and its recorded `resolvedCommit` MUST match
before materialization. Explicit-ref updates replace campaign-owned files;
they MUST NOT widen operator-owned rollout settings.

`main` and `master` MUST both resolve the catalog repository's default branch
reported by GitHub, not assume a literal branch name or use the control
repository's default branch.

`latest` MUST select the highest published stable `vMAJOR.MINOR.PATCH` version,
excluding drafts, prereleases, and non-version tags across all release pages.
It MUST provide an explicit path from an untagged install back to release-based updates. It MUST
NOT depend on a release tag existing at the previously installed commit.
Options MUST be accepted before or after the ref without interpreting option
values as refs, and multiple refs MUST be rejected before any mutation.

gh-aw campaign installation by itself cannot run this post-install lifecycle.
Therefore an implementation MUST NOT represent direct Activity or Dashboard
component installation as a complete CAO installation.

## 5. Workflow execution and verification

Activity and Dashboard workflows MUST check out the control repository at
`github.workflow_sha`. They MUST use the repository-local
`$/.github/actions/setup-cao-runtime` action to verify their required canonical
runtime bundle before executing it.

Runtime verification MUST be local and fail closed when a required canonical
file is absent. A workflow MUST NOT download another CAO checkout, use a
catalog ref to replace local runtime provenance, or probe alternate runtime
layouts. Workflow and policy changes MUST remain in the same reviewed commit,
because execution resolves authority at the exact workflow revision.

## 6. Removal and recovery

The current CAO lifecycle does not support campaign removal. Operators MUST NOT
use `gh aw remove` as a CAO campaign-removal procedure: it removes workflow files
but does not remove materializer-owned canonical directories or campaign records,
and would leave orphaned executable material. A future CAO removal lifecycle
MUST remove the selected campaign record and its bounded canonical destination
transactionally; until then, removal is unsupported.

If campaign provenance, archive acquisition, archive extraction, bundle
validation, or local runtime verification fails, lifecycle tooling and
workflows MUST stop before executing the affected CAO runtime. An operator MAY
recover only by installing or updating through a supported lifecycle entry
point with valid immutable provenance.

## 7. Compliance testing

A conforming implementation MUST test:

1. materialization with immutable full-SHA provenance and rejection of invalid
   provenance;
2. complete-bundle preflight before destination replacement;
3. replacement that removes stale campaign-owned files;
4. preservation of exact focused Activity and Dashboard ownership during root
   materialization;
5. absence of executable CAO runtime mirrors under `.github/aw/`;
6. local fail-closed Activity and Dashboard runtime verification; and
7. installation and update behavior for canonical destinations; and
8. rejection or documentation of unsupported campaign removal until a
   transactional removal lifecycle exists; and
9. exclusion of development-only and Agent Plugin resources from new control
   installations, preservation of separately installed plugin resources, and
   completeness of the documented installation commit in a fresh checkout.

Generated workflow lock files are implementation artifacts. Conformance tests
MUST validate the editable workflow sources and MUST NOT require hand edits to
lock files.
