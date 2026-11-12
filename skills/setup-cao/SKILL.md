---
name: setup-cao
description: "Create, bootstrap, initialize, or install a bare CAO control plane: one control repository, runtime, exact repository scope, and authentication. Stop before campaign installation or execution."
metadata:
  argument-hint: "Optionally provide the control repository and repositories CAO should read"
---

# Set Up Central Agentic Ops

Set up monitoring; leave `control-plane.campaigns` as an empty object.
No operation enrollment, live rollout or dispatch.

For later execution, follow
[outcome-to-execution guidance](https://github.com/githubnext/gh-cao/blob/main/docs/operation-patterns.md)
to infer structure, never authority; do not ask users to choose a pattern.
Explain the fit; ask only for genuinely missing requirements.

## Deterministic boundary

Use `gh cao setup` from the control checkout for
policy construction, authentication setup, external checks, mutation, and
postconditions. Use `gh cao doctor` for local installation checks.
The agent owns topology, exact scope, tradeoffs, approval, and diff review.
Do not recreate setup logic or write policy JSON directly.

## Invariants

- Establish exact repository scope before selecting authentication. Capability
  is not consent, and credential reach never widens policy.
- Ask before repository creation or reuse, runtime installation, credential
  configuration, commit, or push unless explicitly authorized. Without a user,
  use the safe defaults in the non-interactive reference; absence of a user
  grants no additional effects.
- Keep rollout authority only in `.github/workflows/cao.json`. Keep credentials
  out of files, chat, command arguments, workflow inputs, and commits.
- Fail closed when scope, authority, access, credentials, installation, or
  validation is incomplete.
- Never edit generated `.lock.yml` files directly. Preserve root `AGENTS.md`
  (consumer-owned); do not modify campaign-owned files during setup.
- Install, enroll, enable, and execute no agentic operation during setup.

## Choose the repository role

- Prefer a separate control repository.
- Use a source-managed control repository only when the user explicitly chooses
  to run workflows maintained in-tree and the CAO runtime and policy are present
  or will be committed.
- A repository that is both a catalog and an explicitly selected source-managed
  control plane is a supported dogfood topology; apply both roles without
  combining campaign source, rollout policy, credentials, or target authority.

Read [topology](references/topology.md) when the role is ambiguous or the
repository is source-managed or also a catalog. Read [public control
repositories](references/public-control-repositories.md) before using a public
control repository or selecting a non-public target.

## Preferred human flow

Prefer interactive setup for a separate control repository; see the
[setup guide](https://github.com/githubnext/gh-cao/blob/main/docs/setup-quickstarts.md#guided-setup-instead).

```bash
# From an existing control checkout's root:
gh extension install githubnext/gh-cao
gh cao setup
```

Create/clone explicitly with `gh repo create` or `gh repo clone`; choose visibility
with the user. Native setup prepares missing runtime/policy without `init`
or local Node.js.

> [!TIP]
> For SAML-token installation failures:
> `curl -fsSL https://raw.githubusercontent.com/githubnext/gh-cao/main/install-gh-cao.sh | bash`.
> Requires Bash, curl, and Node.js 24+; public downloads are anonymous.
> Stored credentials are unchanged.

Setup offers authentication choices based on that scope.
A separate default-No prompt can publish reviewed CAO files; declining/CI stays
local. Push links to Actions artifacts. Setup never configures synthetic-only Pages.

## Agent procedure

1. Verify `gh --version` and `gh auth status`; identify the control repository
   and topology and obtain approval to create or reuse it.
2. Obtain authorized `OWNER/REPOSITORY` entries; verify names, owners, and visibility.
3. With approval, install the extension as above. Review source-managed
   workflows before installation; do not overwrite them without approval.
4. Prefer `gh cao setup`. Read
   [authentication](references/authentication.md) only when selecting or
   troubleshooting an authentication profile.
5. If interaction is unavailable, read
   [non-interactive setup](references/non-interactive.md) and take its safe
   defaults. Do not infer broader scope or configure credentials without
   explicit authorization.
6. Check local installation and repository state:

   ```bash
   gh cao doctor
   gh aw doctor --repo OWNER/CONTROL_REPOSITORY --dir .
   git diff --check
   ```

   Confirm `.github/workflows/cao.json` parses, has no unresolved placeholders,
   and contains an empty `control-plane.campaigns` object, with
   `control-plane.workflows` and `control-plane.operations` absent or empty. Full validation runs
   in Actions after publication; do not replace it with inline validation.
7. Show the exact diff. Reject campaign, live-mode, credential, generated-lock,
   campaign-owned, or unrelated changes. Ask before commit or push.
8. Report repository, visibility, read scope, authentication, validation and
   commit status. Confirm no operation was enrolled, enabled or run.

Read [implementation details](references/implementation-details.md) for
installer, runtime, generated/source and steering boundaries.

## Stop conditions

Stop without weakening boundaries for:

- missing or malformed scope, or a repository that cannot be inspected;
- a non-empty pre-existing campaign map;
- a declared standalone workflow or direct operation, even when disabled;
- a public control repository that could expose non-public evidence;
- no compatible authentication, or declined/incomplete credentials;
- setup fails after approved mutations, until you inspect policy, runtime, and
  credential configuration metadata; never expose secret values;
- installation or validation failure;
- conflicting pre-existing files without approval; or
- unexpected campaign, live, credential, generated-lock, campaign-owned, or
  unrelated changes.
