# ADR: Use catalog source paths as the installed CAO runtime layout

## Status

Accepted

## Context

CAO previously had two filesystem layouts for the same executable resources:

- catalog sources such as `activity/`, `dashboard/`, `<campaign>/cao.json`, and
  `<campaign>/dashboard.json`; and
- installed copies under `.github/aw/`, including `.github/aw/activity/`,
  `.github/aw/dashboard/`, and campaign-specific runtime directories.

Activity, Dashboard, the local server, the CLI, campaign workers, tests, and
documentation compensated by probing both layouts or rewriting workflow
checkouts. This made it possible to combine a workflow from the control
repository revision with runtime files from a different catalog revision. It
also made local catalog behavior differ from installed control-repository
behavior.

The straightforward alternative, mapping every runtime resource directly to
its catalog path in `aw.yml`, is not supported by gh-aw. Package resources are
restricted to gh-aw-owned paths, selected shared workflow modules, and a small
set of repository metadata files. gh-aw packages also cannot execute an
arbitrary post-install hook.

CAO still needs these properties:

- policy, workflow code, and runtime code must remain bound to reviewed
  revisions;
- missing or incomplete runtime material must fail closed;
- installed and source-managed control repositories must use the same paths;
- `.github/aw/` must remain owned by gh-aw rather than becoming a second CAO
  runtime namespace; and
- updates must remove files deleted by the selected catalog revision rather
  than retaining stale executable resources.

## Decision

CAO uses the catalog source layout as its only executable runtime layout.

gh-aw installs workflow sources and a bounded trusted bootstrap under supported
destinations. The native `gh cao` extension materializes the runtime after
gh-aw resolves the package:

- `gh cao setup` stages the root runtime when the local runtime is incomplete;
- `cao add` materializes the selected campaign before reading its declaration;
  and
- `cao update` materializes each updated package before merging declarations.

The materializer reads gh-aw package or campaign records, accepts only the CAO
repository and a full 40-character `resolvedCommit`, downloads that immutable
archive, and copies a bounded resource set to identical repository-relative
paths. Tag-only and legacy records fail closed rather than resolving mutable
provenance at installation time. Root materialization owns `activity/`,
`dashboard/`, the local gh-aw setup action, and `.github/cao/instructions.md`. If exact focused Activity or Dashboard package
records coexist with the root record, their revisions retain ownership of
their respective directories and are preflighted before any destination is
replaced. Operational campaign materialization owns the complete top-level
`<campaign>/` directory. Each owned destination is replaced before copying so
a new revision cannot leave deleted runtime files behind.

Activity and Dashboard workflows check out the control repository at
`github.workflow_sha` and fail before execution when the required canonical
runtime files are incomplete. Workflows do not fetch another CAO checkout or probe alternate
runtime paths.

`.github/aw/` is reserved for gh-aw instruction overlays, package records,
campaign ownership records, and gh-aw runtime data such as logs. The
`.github/aw/instructions.md` overlay may point to
`.github/cao/instructions.md`, but CAO executable resources and detailed CAO
guidance are not installed under `.github/aw/`.

Campaign dashboards are discovered only as `<campaign>/dashboard.json`.
Activity declarations, grader resources, scripts, and implementation ledgers
likewise use their catalog paths in control repositories.

## Alternatives considered

### Keep source and installed layouts with shared path resolvers

Rejected. Resolvers preserve ambiguity and allow files from different
revisions to be combined. Every new consumer must also remember the fallback
order and security checks.

### Map all resources directly through `aw.yml`

Rejected because gh-aw intentionally rejects arbitrary package resource
destinations. Depending on unsupported destinations would make compilation and
installation fail before CAO could enforce its own invariants.

### Rewrite installed workflows to checkout the CAO catalog revision

Rejected. A second checkout separates runtime provenance from the control
repository workflow and policy revision. It also obscures which repository
owns rollout authority.

### Commit duplicated runtime trees in the catalog

Rejected. Checked-in duplication would still require synchronization and would
make one tree stale by construction.

### Download runtime files during every Activity or Dashboard run

Rejected. Runtime network availability would become part of every execution,
and a run could execute material that is not present in the reviewed control
repository revision.

## Consequences

### Positive

- Catalog, source-managed, and installed control repositories use one path for
  each runtime file.
- Workflows execute code from the same control-repository revision as their
  policy and generated workflow.
- Missing runtime files fail closed through an explicit local action.
- Updates remove stale campaign files by replacing owned destinations.
- `.github/aw/` remains an unambiguous gh-aw namespace.
- Local-server and dashboard build behavior match installed control
  repositories without a flat installed-dashboard compatibility directory.

### Negative

- gh-aw alone cannot complete canonical CAO materialization. Operators must use
  the CAO installer or CLI entry points that invoke the trusted materializer.
- The Activity and Dashboard manifests are root-package components, not
  complete standalone installation entry points. Direct component `gh aw add`
  installs workflows but cannot invoke the required materialization hook.
- Canonical root and campaign directories are campaign-owned. Local changes
  inside them are replaced on materialization.
- Installation requires downloading the resolved CAO archive and a usable
  `tar` executable.
- Package removal still requires CAO lifecycle tooling to remove
  materializer-owned canonical directories; gh-aw ownership metadata alone
  does not describe those files.
- Workflow compilation remains dependent on gh-aw platform behavior. In
  particular, gh-aw v0.89.17 on Windows currently rejects some Windows-form
  artifact paths during secret-redaction coverage validation, so affected lock
  files must be regenerated on Linux or after that compiler bug is fixed.

## Validation

The decision is enforced by:

- runtime artifact tests in `tests/unit/cao-runtime-artifact.test.mjs`;
- Activity and Dashboard workflow contract tests;
- campaign, CLI, installer, dashboard build, and local-server tests;
- searches that reject CAO runtime paths under `.github/aw/`; and
- gh-aw compilation of workflow sources where the compiler can complete on the
  current platform.
