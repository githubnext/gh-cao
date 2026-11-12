---
title: Setup Combinations and Verification
description: Choose setup by repository visibility, organization scope, effective capabilities, and GitHub host.
---

The [low-friction quickstart](setup-quickstarts.md) observes existing Agentic
Workflow evidence. It is not a requirement to use a public control repository,
install a campaign, or adopt one enterprise-wide topology. You can keep
monitoring only, use a separate control repository, explicitly maintain workflows
in-tree, or choose direct operations, standalone workflows, campaigns, or ordinary
Actions as appropriate. See [operating patterns](operation-patterns.md) and
[deployment and governance](deployment-and-governance.md).

## Repository visibility and organization scope

The control repository is always included in setup scope. Every remote target
must resolve to its canonical name and visibility before setup proceeds.

| Control repository | Remote targets | Same organization | Cross-organization or mixed owners |
| --- | --- | --- | --- |
| Public | All public | Interactive public-only monitoring, no automation App/PAT | Same public-only profile; exact names for every owner |
| Public | Any private/internal, including mixed public/private scope | Rejected before policy or credential changes | Rejected; use a private control |
| Private | All public | Interactive public-only monitoring; the control's own job token reads it | Same profile; restricted remote evidence remains partial |
| Private | Private/internal, or a mix with public | Approved private organization Apps, or permitted owner-scoped PATs | Approved enterprise Apps within one enterprise, or separate permitted PATs per owner |
| Internal (where available) | Private/internal | Interactive setup with Apps or PATs | Same credential boundaries; internal visibility is not public visibility |

Setup does not configure Pages or request public-publication consent.
Repository contents, logs, review outputs, and operational artifacts each need
suitable access controls. Production dashboards use same-origin Go/PostgreSQL;
local analysis uses real SQLite snapshots. Pages is a synthetic-only demo.

Interactive `OWNER/*` expands accessible repositories and saves exact names.
For a public control, any non-public match fails setup. The unattended
`gh cao setup --repository 'OWNER/*'` shortcut instead selects **currently public**
matches only, leaves Pages untouched, never commits/pushes, and requires a public
control. It cannot configure a private/internal control or credentials. A failed
lookup is never evidence that a repository is public.

Cross-organization does not automatically mean enterprise-owned Apps are
available. Verify that all organizations belong to the same enterprise before
choosing that profile. A newly created private organization App cannot span
owners. Approved App reuse still requires installations on every selected owner
and repository. Unrelated owners may use separate approved owner-scoped PATs
where policy and APIs allow; otherwise keep independent control planes. No
credential or role broadens the reviewed policy.

## Who can set it up?

CAO does not grant authority based on an operator's title. "Enterprise admin" and
"organization admin" can refer to different custom roles; check effective
permissions, credential restrictions, and organization/enterprise policy.

| Operator or credential | What must actually be available |
| --- | --- |
| Enterprise owner or delegated enterprise administrator | Access to the designated organization-owned control repository, plus the required repository settings and publication permissions. Enterprise administration alone is not a repository grant, App installation, or private key. |
| Organization owner or delegated organization administrator | Rights in each selected organization; authority in one organization does not carry into another. App creation, installation approval, repository selection, and key access are separate capabilities. |
| Repository administrator | Required Actions variables/secrets, approved target access, and permitted Git publication. May extend an already-approved App installation only where GitHub and organization policy allow. |
| Regular user with repository write access | Can contribute through the normal protected-branch/PR process, but write access alone does not guarantee Actions-secret management, App installation, or access to private targets. Ask an authorized administrator for those steps. |
| Operator with only PATs | CLI sign-in needs the control settings permissions; automation needs separate approved fine-grained read/write tokens per resource owner. Tokens cannot exceed the user's repository access, bypass approval/SSO/IP policy, or access unsupported APIs. |

For public-only monitoring, no **additional automation** PAT or App is configured.
The CLI still needs authenticated control-repository settings access. If an
inherited App/PAT mode must be replaced, setup also needs repository Variables
write access. Optional publication requires a matching origin, synchronized
default branch, empty index, and permission to push workflow files under current
rulesets. A blocked publication leaves configuration local; it never bypasses
protection.

[Fine-grained PAT setup](setup-fine-grained-pat.md) stores only secret **names**
in repository maps. One owner-scoped read credential covers that owner's exact
repositories; write credentials cover only approved output repositories, normally
the control. Keeping an existing secret verifies its presence, **not** its value,
approval, expiry, API access, or effective permissions. Verify those independently
before collection or execution. Classic PATs are not a supported automation
profile. Target authentication and model-provider authentication are separate.

## Enterprise Cloud data residency (`*.ghe.com`)

This is GitHub Enterprise **Cloud**, not GitHub Enterprise Server or a custom
Pages hostname. Set the host before authentication, cloning, and setup:

```bash
export GH_HOST=octocorp.ghe.com
gh auth login --hostname "$GH_HOST"
gh auth status --hostname "$GH_HOST"
gh repo clone platform/central-agentic-ops
cd central-agentic-ops
gh cao setup
```

The web host is `octocorp.ghe.com`; REST requests use
`https://api.octocorp.ghe.com`, not a GHES `/api/v3` URL. App manifests, PAT
creation forms, installation/settings links, repository origin, and publication
must use the selected enterprise host. Public CAO/gh-aw release downloads still
come from GitHub.com anonymously; do not send enterprise credentials to that
catalog host. Ensure the network allows those releases and the chosen runtime
deployment.

[Data-residency enterprises use Enterprise Managed Users](https://docs.github.com/en/enterprise-cloud@latest/admin/data-residency/about-github-enterprise-cloud-with-data-residency#repository-visibility):
**public repositories are unavailable**. Use private/internal controls and
targets with organization/enterprise App installations or approved PATs.
The public-control/public-target video path therefore cannot be reproduced
unchanged on GHE.com. Names are host-local; a `github.com` target and a GHE.com
control are not a supported single-host setup combination.

## What the automated tests prove

Run from the source checkout, with Go, Git, GitHub CLI, Node.js, the policy-pinned
gh-aw binary, and a terminal launcher (`script` on Linux, `python3` on macOS):

```bash
npm run test:integration:bootstrap
npm run test:integration:setup
```

These are **local simulator-backed command/protocol end-to-end tests**, not live
GitHub certification. They build the current extension, run real `gh cao setup`
and `gh` subprocesses (including an interactive pseudo-terminal), and confine
GitHub HTTPS to a loopback simulator with isolated home/configuration and
synthetic authentication. Unknown routes and destinations fail. No live
repositories, credentials, campaigns, or workflow dispatches are created.

| Evidence | Automated coverage and boundary |
| --- | --- |
| Bootstrap | Real installer, local release/checksum verification, simulated repository creation, runtime preparation, Git push and fresh clone; no live repository provisioning |
| Installer compatibility | Stable/prerelease minimums with missing, current, outdated and newer gh-aw; exact fresh-install version forwarding, byte-preserved existing extensions, noninteractive upgrade refusal, unchanged policy/index/HEAD, and rejection outside a checkout before distribution |
| Recorded low-friction path | Public control, organization wildcard saved as exact names, no App/PAT prompt or Pages configuration, separately approved Git publication and monitoring-pending result |
| Visibility and owner combinations | Public/private controls on GitHub.com; private/internal controls on GHE.com; same, cross, and mixed owners; public/private/internal/mixed targets as allowed by the host; public controls reject non-public evidence |
| PAT path | Interactive retention of simulated existing secret names, exact owner read maps, control-only write map, and PAT-mode selection; no token issuance, decryption, or live approval |
| App path | Native CLI previews on both hosts, separate read/write permissions and installation targets, reuse plans, rejection of new cross-owner organization Apps and invalid App-ID combinations; browser consent and real key/installation verification are not exercised by this suite |
| Capability boundaries | No Pages calls, denied secret lookup, denied authentication-mode override, protected consumer/index state; not a simulation of every GitHub role or custom-role policy |
| Enterprise host | Real CLI/API and HTTPS Git routing to the simulated GHE.com host, private/internal scope and publication; not a public-repository scenario or cross-host federation |

The installer matrix follows the
[playground canary scenarios](https://github.com/githubnext/cao-playground/blob/cb66e602b239816f871b674e033128d0f8c2e889/tests/install_release_test.sh)
with fixed versions, an inert upstream installer/version-only gh-aw fixture,
and the production local-source materializer. It verifies installer decisions
and real CLI extension discovery, not upstream release availability or gh-aw
execution compatibility. Environment unit tests reject inherited credentials,
extension/configuration overrides and proxy bypasses; a missing-extension probe
must not discover an ambient developer or runner installation.

The simulator verifies setup without Pages configuration. It does not execute
ordinary Activity/Dashboard Actions workflows or establish real hosted
authentication, access controls, or populated-dashboard readiness.

## Playground retirement gate

`gh-cao` now owns the replacement checks in
`.github/workflows/installer-canary.yml`. Its daily schedule and manual trigger
run the real public installer integration on Linux and macOS for stable and
prerelease gh-aw, each with missing, current, outdated and newer installations.
Pull requests run deterministic canary contracts only. The integration uses
isolated homes/configuration, a real local control checkout and source catalog,
anonymous public release downloads, checksum verification, real extension
discovery and source materialization. Control-repository sign-in/identity are
synthetic; no remote repository is created or configured.

The same canary reads existing default-branch Activity, Dashboard build and
deployed-dashboard integration results. It requires recent successful completed
runs (less than 24 hours old), successful collection/indexing and build jobs,
and a successful populated-dashboard browser check. Skipped jobs, missing
evidence and a latest completed failure do not count as success. This reuses
the existing deployment rather than creating or dispatching a second one;
The hosted operational dashboard and synthetic Pages demo are separate;
demo readiness does not establish operational evidence freshness.
Run these read-only checks locally with:

```bash
node scripts/installer-canary.mjs matrix
node scripts/installer-canary.mjs smoke
```

Archive `githubnext/cao-playground` only after the replacement workflow is
merged and all 16 platform/channel/scenario cells plus the deployed smoke job
pass in Actions. Preserve the repository and its run/issue history; do not
delete it. Local simulator or single-platform results are not that retirement
gate. Archival is a separate operator action, not something the canary performs.

## Live verification still required

Only in an approved disposable environment, have the actual operator and
organization/enterprise administrators verify creation/reuse rights, selected
App installations and key access, PAT resource owners and approval/expiry,
SSO/IP restrictions, Actions availability, workflow-file publication rules,
runner/network access, and hosted dashboard authorization. Include the
requested enterprise-owner/admin, organization-owner/admin, repository-admin,
write-only-user, and PAT-only accounts with their **actual grants**, not assumed
role equivalence. Check both allowed operations and expected denials.

With explicit approval, verify current-revision CAO validation, Activity and
Dashboard artifact production and hosted ingestion, inspect real operational data and partial-evidence
diagnostics, and prove credential read/write boundaries using the
[activation checks](control-plane-authentication.md#validate-before-activation).
Monitoring success does not prove any campaign's model access or live authority.
Campaign installation, review probes, target writes, or paid model calls require
separate authorization. None is part of the local simulator commands above.
