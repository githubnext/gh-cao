# Add a CAO Campaign

For outcome requests, first follow
[From Outcome to Execution](https://github.com/githubnext/gh-cao/blob/main/docs/operation-patterns.md).
Derive structure from outcome, scope, and execution requirements; do not ask
users to choose a pattern. Explain the fit; ask only for genuinely missing
requirements. If a campaign is unnecessary, use the matching guidance instead.
Catalog requests may proceed. Infer structure, never authority.
Keep exact package selection and installation approval explicit.

## Procedure

1. Verify the control plane and immutable catalog commit.
2. Discover eligible manifests.
3. Recommend matches with safety tradeoffs and `None of these`.
4. Require exact selection and installation approval.
5. Install and validate policy preservation.

Never hard-code candidates or silently install.

## Deterministic boundary

`gh cao add` owns resolution, installation, policy merging, and
installed-declaration validation. Use
`gh cao doctor` and `gh aw doctor --dir .` for local checks; inspect CAO validation
in Actions after approved changes are published. Do not reimplement package or
policy mutation.

## Boundaries

- Use [create a campaign](create-campaign.md) to author a new campaign when the execution
  requirements justify one and no existing package fits.
- Require `.github/workflows/cao.json` and installed `gh cao`; hand off to
  [setup](../setup.md) when the control repository is not initialized.
- Discovery is read-only until recommendation, safety review, exact selection,
  and installation approval.
- Install through `gh cao add`, which validates the declaration and merges
  policy without enabling live mode or broadening scope.
- Never edit generated `.lock.yml` files or `.github/aw/campaigns/*.json` ownership records directly.
- Treat all installed campaign package sources as immutable. Add requested prompt guidance only through the consumer-owned `.github/cao/<campaign-slug>.md` file loaded by the installed workflows' optional `runtime-import`; do not patch installed workflow Markdown or other package-owned files.

## Discover the Catalog

1. Verify GitHub authentication with `gh auth status`.
2. Resolve the `githubnext/gh-cao` default-branch commit once and use it for
   every inspected file to prevent mixed-revision recommendations.
3. Enumerate top-level directories at that commit. A candidate operational campaign must contain both `aw.yml` and `cao.json` in the same directory.
4. Parse each candidate's `aw.yml` as YAML and `cao.json` as JSON. Do not infer metadata with regular expressions. Exclude campaigns with `private: true`; label campaigns with `experimental: true` clearly.
5. Read each remaining manifest, README, CAO declaration, orchestrator, and
   worker from the same commit. Derive outcomes from files, not names.

Failed authentication, access, parsing, or revision consistency stops discovery
as incomplete. Never guess from partial data.

## Recommend

Use supplied requirements. Recommend no more than three installable campaigns,
ordered by fit. For each, summarize:

- the outcome it targets;
- its orchestrator and worker responsibilities;
- whether it is experimental;
- notable permissions, network access, credentials, and safe outputs;
- the main tradeoff or reason it may not fit.

Include `None of these`. Require one exact selection; a vague outcome, first
recommendation, or no response is not consent. If none fits, offer the
[create a campaign](create-campaign.md) handoff without installing.

Verify named campaigns through the same process and safety review before
installation approval.

## Review Before Installation

Before approval, inspect the selected campaign at the resolved commit and report:

- campaign name, slug, catalog commit, and maturity;
- installed orchestrator and workers from `cao.json`;
- workflow permissions, tools, network hosts, credentials, schedules, and AI credit limits;
- safe-output types and whether they remain review-routed by current policy;
- the exact files and campaign record that campaign installation will own;
- the current campaign policy, exact target overrides, and global scope from `.github/workflows/cao.json`.

Treat missing declarations, invalid metadata, inaccessible sources, unresolved placeholders, or conflicts with an existing installed campaign as blockers. Never suggest that credential reach expands CAO policy.

## Install

After explicit approval, run from the control repository:

```bash
gh cao add githubnext/gh-cao/<campaign-slug>@<catalog-commit>
```

Forward additional campaign-installer options only when the user requested them and they do not weaken the reviewed boundary. Do not pass secrets in command arguments.

After installation:

1. Parse `.github/workflows/cao.json` and reject unresolved placeholders.
2. Confirm the new campaign identity, orchestrator, and workers match the installed `<campaign-slug>/cao.json` declaration.
3. Confirm global scope, campaign mode, exact target overrides, worker settings, and unrelated campaign settings did not broaden or change. A newly added campaign must remain in review unless the user separately requests and approves a policy change.
4. Run `gh aw doctor --dir .` and report any incomplete prerequisites without bypassing them.
5. Review `git diff` for campaign-owned files and policy changes. Reject any customization of installed package sources; when the user requested additional prompting, confirm the only prompt customization is `.github/cao/<campaign-slug>.md`. Do not commit, push, enable workers, dispatch workflows, or promote live mode unless the user explicitly asks.

Report campaign, inspected commit, orchestrator and workers, capabilities,
policy preservation, validation, and required approvals.