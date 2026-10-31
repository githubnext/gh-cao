---
title: Authentication
description: Choose and configure the least-privilege GitHub credential for your CAO scope.
---

Authentication controls what CAO *can reach*. The checked-in `.github/workflows/cao.json` policy controls what CAO *may operate on*. A credential never expands policy.

This page owns runtime credential selection, permission ceilings, verification,
and rotation. Use [Control Plane Authentication Profiles](control-plane-authentication.md)
to create or reuse credentials, and [Set Up CAO](setup-quickstarts.md) for the
complete monitoring installation.

## Control Repository Visibility

Public and private control repositories are supported. Their contents inherit that visibility, including policy, workflow runs, operational metadata, dashboard data, and review outputs.

Use a private control repository whenever the target or required evidence is private or internal. Use a public control repository only when all review material may be public.

See [setup combinations and verification](setup-compatibility.md) for the
visibility/owner matrix, effective operator capabilities, PAT limitations,
GHE.com data residency, and the distinction between simulator and live evidence.

## Optional Copilot requests token broker

The hosted server can separately broker **billable Copilot inference** credentials
for explicitly enrolled GitHub Actions jobs. This is not a target-access
credential, admission gate, hosted CLI executor, or replacement for the bundled
workflows' built-in inference profile. Broker support is opt-in and experimental;
the default `control-plane.token-broker.copilot-requests.workflows` list is empty.

### Prepare the deployment

1. Create a dedicated private GitHub App with the organization permission
   **Copilot Requests: Read-only** (`copilot_requests: read`), installed in the
   intended organization. Do not reuse dashboard login, collector, safe-output,
   or hosted-executor Apps. Obtain its exact numeric App, installation, and
   organization IDs. Review [GitHub App Copilot access](https://docs.github.com/en/enterprise-cloud@latest/copilot/how-tos/administer-copilot/manage-for-organization/manage-app-copilot-access)
   and configure organization spending controls: a read-named permission can
   authorize paid requests, and CAO admission budgets are not a broker spending cap.
2. Store its PEM private key as an Azure Key Vault **secret**, not in Actions,
   policy, command arguments, or a generated environment document. Grant the
   server's managed identity secret-read access at the narrowest supported scope.
   An unversioned secret URL reads the current version at each mint; a versioned
   URL requires an explicit deployment update during rotation.
3. Prepare settings with [native token-broker setup](cao-cli.md#optional-copilot-token-broker-setup).
   Deploy them only to an existing OAuth-protected HTTPS `serve-hosted` server
   with an exact reviewed allowed host and a process-owned listener. The public
   URL must be exactly `https://HOST/api/token-broker`, without credentials,
   port, query, fragment, or a trailing slash; the same URL is the OIDC audience.
   Complete the ordinary hosted storage, proxy, OAuth, and secret-manager
   configuration separately. See the [server security contract](https://github.com/githubnext/gh-cao/blob/main/server/SECURITY.md#broker-enrollment-and-setup)
   for every environment variable and managed-identity constraint.

Run exactly **one process replica**, with no autoscaling or overlapping
deployments. Azure Functions and externally owned listeners are unsupported.
Job invalidation and graceful shutdown attempt revocation, but a crash loses the
process-local registry; GitHub-issued tokens can remain usable until their actual
expiry, capped at one hour. No durable cleanup or multi-replica guarantee exists.

### Enroll a reviewed workflow revision

In the control repository's protected `cao.json`, add only explicitly approved
entries under `control-plane.token-broker.copilot-requests.workflows`. Each entry
requires four strings:

| Field | Required identity |
| --- | --- |
| `owner-id` | Immutable numeric organization ID matching the broker installation owner |
| `repository-id` | Immutable numeric repository ID |
| `workflow-ref` | Exact `OWNER/REPO/.github/workflows/NAME.lock.yml@refs/heads/BRANCH` or tag reference |
| `workflow-sha` | Full 40-character lowercase commit SHA of the reviewed workflow revision |

No wildcards or unpinned revisions are accepted. Protect the mounted policy from
untrusted writes, retain the reviewed workflow/policy evidence, and update pins
only after reviewing new workflow revisions. Removing enrollment stops new
issuance; a previously tracked job can still authenticate cleanup.
Only push, schedule, and manual-dispatch jobs with signed `check_run_id` evidence
are supported. Pull requests, reusable workflows, environment-based identities,
and `copilot-task` requests are unsupported.

For a compatible gh-aw release, opt the intended consumer into
`token-broker.copilot-request: true`, configure `GH_AW_TOKEN_BROKER_URL` to the
exact endpoint, and do **not** also grant `permissions.copilot-requests: write`.
Review and compile the Markdown workflow sources and generated locks. Do not
change bundled workflows or introduce `COPILOT_GITHUB_TOKEN` fallback merely to
enable a broker. Consult the selected compiler's
[experimental broker contract](https://github.com/github/gh-aw/blob/main/docs/src/content/docs/specs/token-broker-specification.md):
bounded threat-detection/evals consumers are wired, but main-agent inference
requires renewable AWF credential support. An installation token alone does not
prove that your consumer can use or renew it.

### Diagnose and accept

Use `gh cao doctor token-broker --settings PATH --policy PATH --json` to inspect
prepared non-secret settings and the intended mounted policy without contacting
GitHub or Azure. Ordinary `gh cao doctor` remains an installation diagnostic,
not proof of broker deployment, App permission, or model entitlement.

Before live acceptance, independently verify real GitHub Actions OIDC exchange,
the exact enrolled workflow revision and organization installation, Key Vault
access and key rotation, authorized Copilot inference and renewal, fresh-OIDC
job invalidation, and graceful-shutdown cleanup. Confirm another job, an
unenrolled revision, and an unsupported execution profile cannot mint credentials.
Retain sanitized outcomes, never request/response bodies or credentials.
Setup output, offline tests, or a successful token-shaped response do not
establish this acceptance.

Use the server's existing [OpenTelemetry configuration](https://github.com/githubnext/gh-cao/blob/main/server/README.md#telemetry)
for broker metrics and spans. Keep exporter authentication in the deployment
secret manager. Never enable proxy/APM body capture for `/api/token-broker`;
OIDC credentials and issued tokens must not enter traces, logs, or artifacts.
Alert on issuance/revocation failures and absent telemetry, and preserve the
one-hour crash limitation even when instrumentation is healthy.

## Choose Cross-Repository Authentication

| Your run | Use |
| --- | --- |
| Monitor the control repository or confirmed public remote targets | Built-in Actions `GITHUB_TOKEN`, with explicit public-only policy for remote targets |
| Private targets in one organization | Organization-owned private GitHub Apps |
| Targets across organizations in one enterprise | Enterprise-owned private GitHub Apps |
| Apps are unavailable and the required APIs are PAT-compatible | One fine-grained PAT pair per resource owner |

For monitoring the control repository and public remote targets, interactive
setup automatically selects **Public-only monitoring (no App or PAT)** without
asking for authentication configuration, even if the control repository is private
or internal. Private/internal remote targets or authenticated operations
still require an App or PAT profile. Monitoring uses the built-in Actions
`GITHUB_TOKEN`; it does not grant cross-repository execution or writes.

:::tip[Prefer GitHub Apps]
Apps use short-lived, installation-scoped tokens and do not depend on one person's continued access. CAO separates a read-only App from a write-capable App.
:::

## Configure Your Profile

<span id="reuse-approved-apps"></span>
<span id="automated-app-setup"></span>

Run credential setup from the control repository:
[reuse approved Apps](control-plane-authentication.md#reuse-approved-apps),
[create private Apps](control-plane-authentication.md#configure-private-github-apps),
or [configure owner-scoped PATs](control-plane-authentication.md#configure-a-fine-grained-token).
The sections below explain runtime behavior, not provisioning.

### Policy

Target-repository authentication is defined once in `.github/workflows/shared/control.md` and inherited by Orchestrator and worker workflows. Before GitHub MCP or CLI proxy startup, shared control resolves the exact read scope, mints a repository-scoped App token with only the importing workflow's declared read permissions or selects the exact owner-scoped PAT, and binds the GitHub tools to that credential. Checkout authentication alone is not evidence that the agent's GitHub tools use the same token. A separate write-capable App serves safe outputs. Safe-output tokens are narrowed to the selected handler's permissions. Copilot inference permission remains explicit in every Copilot-backed workflow. Workflow-local GitHub App blocks should not be added unless a future Agentic Workflow has a documented isolation requirement that shared control cannot satisfy.

The shared control import resolves each `read_*` permission directly into the
App-token action's `permission-*` inputs at compile time. Undeclared permissions
remain empty strings. Do not route these values through runtime step outputs:
gh-aw strict-mode validation must be able to inspect the declared scopes.

### Pre-activation inventory credentials

Admission reads policy at the exact `github.workflow_sha` with the control
repository's job token. After admission, shared control resolves the selected
read profile and creates **separate control and inventory Octokit clients**.
Orchestrator precompute reads the control repository's current workflow states
with the job-scoped token, whose `Actions: read` permission is declared on
`pre_activation`, instead of spending a shared App or PAT rate-limit budget.
Changing `GH_TOKEN` does not change a github-script client.

The pre-activation App action remains restricted to the control repository with
`Actions: read` and `Contents: read`. Inventory uses independently minted,
read-only tokens for only the explicit admitted target, or the exact
`allowed-repositories` enrollment. Tokens are grouped by owner and split into
batches of at most 500 repository names; no installation-wide token is minted.
They are revoked after precompute, including on failure. Credentials stay in
Actions execution wiring, never in `cao.json`, dispatch inputs or artifacts.
The later worker/agent read tokens and target-bound safe outputs are unchanged.

Owner-wide discovery is not supported by this pre-activation credential path.
Provide an explicit in-scope `target_repo` or commit exact enrollment. The
optional shared-control import `inventory_exclude_control: true` narrows fleet
inventory before token creation and rejects an explicit control target. It does
not replace a worker's own target restrictions or grant new policy authority.
Existing campaign target-mode overrides still apply; a `targets` mode override
map is not a repository allowlist.

In explicit `app` mode, missing credentials or denied installation access never
fall through to a PAT or workflow token. In `pat` mode, the control repository
and each target need exact read-secret mappings. Shared control narrows scheduled
orchestrators in explicit `pat` mode to the control repository, so every
campaign can continue bounded local work without treating another repository's
PAT as a fallback. Manual runs without a target, and any custom workflow that
overrides this default to request fleet inventory, must map every enrolled
repository to the selected read PAT secret; multi-secret fleets are explicitly
unsupported in this path. Use explicit targets or the App profile for remote
repositories. Unset mode retains legacy precedence, selecting one profile before
making requests; a partially configured App fails closed. Public metadata reads
with the workflow token do not establish private target reach.

Required inventory is **all-or-nothing**. An incomplete fleet retains the
admission artifact and an `authorized: true` policy decision, but records
`activation_ready: false`, an incomplete inventory status and sanitized
diagnostics in `cao-control-precompute`. The precompute step fails and emits
`authorized=false` to the existing activation gate: no agent or dispatch runs.
A complete, empty partition remains a legitimate no-op. Renamed or redirected
repositories require reviewed enrollment updates rather than following aliases.
Diagnostics distinguish
missing credentials, malformed/out-of-scope enrollment, denied permission,
capacity and service failures where the API permits. A 404 can mean repository
identity, token scope, installation access or organization restrictions; it
does not alone prove absent installation membership.

### Recover from `pat-fleet-unsupported`

This is a credential-configuration failure, not a policy denial. Shared-control
scheduled orchestrators avoid it by defaulting to bounded local work, but
retrying the same fleet run, reducing `max_repos`, or changing only
`GH_AW_GITHUB_AUTH_MODE` will not restore remote inventory.

**Unblock one run without changing authentication:** dispatch the failed
orchestrator with one explicitly enrolled target. For the ESLint Factory failure
in `githubnext/gh-cao`, run:

```bash
gh workflow run eslint-rules.lock.yml --repo githubnext/gh-cao \
  -f target_repo=github/gh-aw -f safe_output_mode=review -f max_repos=1
gh run list --repo githubnext/gh-cao --workflow eslint-rules.lock.yml --limit 5
```

Inspect the new run's precompute summary before relying on its results. This uses
the target's existing PAT mapping and the separate control PAT; both secrets must
still be valid and have the required read access. Repeat the dispatch with
`target_repo=githubnext/gh-cao` to handle that repository separately. These
commands do not repair subsequent scheduled fleet runs or widen policy.

**Restore scheduled fleet inventory:** configure approved Apps covering the
whole enrolled scope:

```bash
gh cao setup --repo githubnext/gh-cao
```

Choose the approved private GitHub App authentication path. Because this scope
spans `github` and `githubnext`, a private organization-owned App for just one
owner is insufficient. Use approved enterprise-owned Apps installed in both
organizations, if available, following
[enterprise-owned App setup](control-plane-authentication.md#enterprise-owned-apps).
An authorized App administrator must supply the credentials and approve the
installations; do not simply change the mode variable before provisioning them.
If no approved cross-owner App is available, keep using explicit targeted runs;
the current scheduled mixed-PAT fleet cannot run as configured.

After setup, verify runtime access without starting campaign work:

```bash
gh workflow run cao-validate.yml --repo githubnext/gh-cao -f verify-credentials=true
gh run list --repo githubnext/gh-cao --workflow cao-validate.yml --limit 5
```

Open the resulting validation run and inspect the credential-verification job,
summary, and `cao-credential-verification` artifact; a successful dispatch of the
validation workflow alone is not proof of credential access. Only after all
required checks pass, start a fresh fleet run in review mode:

```bash
gh workflow run eslint-rules.lock.yml --repo githubnext/gh-cao -f safe_output_mode=review
```

For another control repository or campaign, replace the repository and workflow
names above with its exact coordinates. Do not remap different owners to one
fine-grained PAT secret or remove enrolled repositories just to bypass the gate.

### Explicit runtime credential verification

`gh cao doctor` checks local tools, policy/compiler configuration and the
**operator's** GitHub CLI login. It does not prove workflow App access.
After publishing reviewed product/runtime changes, explicitly request a
read-only check in the existing CAO validation workflow:

```bash
gh workflow run cao-validate.yml -f verify-credentials=true
```

This resolves policy at the workflow SHA and reuses runtime credential
resolution to check control-source/workflow access and every exactly enrolled
target's metadata, Actions and Contents read reach. Disabled campaigns need not
be activated for verification. To inspect the write App's `Issues: write`
installation prerequisite on the enrolled target destinations rather than the
control review destination:

```bash
gh workflow run cao-validate.yml -f verify-credentials=true \
  -f output-mode=live -f output-permission=issues
```

These options select diagnostic checks, not live authority. The write App is
used only for read-only installation/permission inspection; no write token,
issue, dispatch or activation is produced. The ordinary validation issue
reporter is disabled for this invocation. Inspect the sanitized
`cao-credential-verification` artifact and job summary. PAT/workflow-token output
write permissions cannot be established by these read checks and are reported
as unverified. Even a passing App prerequisite check leaves **issue publication
unverified**: an actual bounded safe-output canary requires separate authority.

### Runtime credential selection

The [credential reference](#credential-reference) lists the variable and secret
names. Select a profile explicitly rather than relying on legacy precedence.

`GH_AW_GITHUB_AUTH_MODE` explicitly selects `app`, `pat`, or the repository-local
`workflow-token` mode. With no mode configured, Activity uses `GITHUB_TOKEN`
when the exact collection scope contains only the control repository, or when
reviewed policy explicitly enables public-only monitoring. An
explicit `control-plane.activity.repositories` list narrows evidence collection
within the global allowed-repository scope without narrowing campaign rollout
authority. GitHub App setup
writes `app` as soon as the read App credentials and selected-repository
installations are verified, so Activity can run while write-App setup is still
in progress. PAT setup writes `pat` only after the complete selected profile is
stored. In `pat` mode, read operations and
cross-repository safe outputs select the owner-scoped secret mapped to their
exact repository and do not fall through to App credentials or legacy PAT
secrets. Orchestrator safe outputs targeting the control repository use its
permission-scoped `GITHUB_TOKEN` to avoid consuming a user's PAT rate limit.
Missing cross-repository map entries and mapped secrets fail closed. In
`app` mode, missing App IDs, private keys, installations, or repository grants
likewise fail closed instead of borrowing PAT credentials.

App tokens are narrowed to the declared job or safe-output handler permissions.
A PAT cannot be narrowed per job: each use has the token's full permissions,
so its stored read/write ceilings must already be least-privilege.

The committed root `aw.yml` intentionally has no `config` block so normal
installation remains compatible with non-interactive `gh aw add`.

### Repository-provided token fallback

The repository-provided `GITHUB_TOKEN` supports bounded control-repository
operations and the public-only monitoring profile below. Public visibility alone
does not guarantee access to another repository's logs, artifacts, security
data, authenticated enrichments, or write APIs.

### Public-only monitoring quickstart

Complete [Set Up CAO](setup-quickstarts.md) for interactive installation and
publication. The unattended shortcut below has a narrower visibility contract.

Confirmed public remote targets skip authentication selection, not review or
public Pages consent. The control repository may be private or internal: its
own job token can read it.
Unknown visibility or failed lookups never qualify. For unattended setup:

```bash
gh cao setup --repository 'my-monalisa-org/*'
```

The `--repository` quickstart selects public-only monitoring, accepts a public
Pages dashboard and skips credential and confirmation prompts. No separate
authentication-mode flag is needed. The existing GitHub CLI sign-in still needs permission
to configure **the control repository's** Pages and, only when switching an
existing authentication mode, its Actions variable. No
user-supplied automation PAT or App is created or stored. Wildcards select only
currently public repositories and save their exact names, not a pattern; future
repositories are not enrolled. Exact private/internal inputs fail. Setup changes
only the control repository's configuration and local policy, never targets,
campaign activation, agent execution, safe outputs, or live authority.
Review, commit and push the installed files and policy to publish the setup.
Interactive setup can offer publication after separate default-No review;
this unattended public-only shortcut never commits or pushes.
A private control repository cannot use this shortcut, even with public-only
targets: the control repository is always included. Use interactive setup instead;
public remote targets do not require extra credentials there.

The policy records the independently bounded Activity scope:

```json
{
  "control-plane": {
    "scope": {
      "allowed-owners": ["my-monalisa-org"],
      "allowed-repositories": ["my-monalisa-org/cao", "my-monalisa-org/example"]
    },
    "activity": {
      "repositories": ["my-monalisa-org/cao", "my-monalisa-org/example"],
      "public-only": true
    },
    "campaigns": {}
  }
}
```

Activity uses workflow-token authentication, including the default when no mode
is configured. Setup does not create authentication variables or store credentials
when the built-in token is already selected or is the default. It checks the
repository setting first, then organization variables shared with that repository.
Only an existing conflicting mode requires a repository-level `workflow-token`
override; existing credentials are not deleted.

Activity verifies repository
identity and visibility before discovery and again before log collection, and
requires every remote target to remain public. Only the actual control repository
may be private/internal, using its own job token. Publication is blocked if identity
or visibility cannot be verified or a remote target changes to private/internal.
There is no App/PAT fallback or owner-wide discovery
at collection time. GitHub's REST API exposes
[public workflow metadata](https://docs.github.com/en/rest/actions/workflow-runs#list-workflow-runs-for-a-repository);
the existing gh-aw collector attempts available logs and artifacts with the
job token. Restricted or missing evidence remains **partial**, with a reason in
the inventory metadata and Actions summary, even when the collector exits zero.
Public-only collection skips issue-status GraphQL enrichment and new
operational-value collectors. It observes existing gh-aw workflows; it does not
execute new agentic work. No runs observed is not proof of zero activity.

For a fresh repository-local installation, keep
`control-plane.scope.allowed-repositories` set to the control repository.
Activity needs no App, PAT, secret, or authentication variable: an unset
`GH_AW_GITHUB_AUTH_MODE` uses the job's `GITHUB_TOKEN`. You may explicitly set
`workflow-token` for the same behavior. This supports an initial collection
with no agentic runs and publishes an explicit empty snapshot for the dashboard.
Collection and operational-value reads reserve 100 core API requests rather
than the larger App/PAT reserves, so they fit the standard 1,000-request
workflow-token quota. Other owner-wide or cross-repository Activity scopes require
`app` or `pat`; without the explicit public-only policy, Activity rejects those
scopes rather than silently collecting only the control repository.

A Repo Assist campaign can have broader rollout scope while its agentic
workflows run in the control repository. To collect only that repository's
workflow evidence, declare the narrower Activity scope explicitly:

```json
{
  "control-plane": {
    "scope": {
      "allowed-repositories": [
        "acme/cao",
        "github/gh-aw"
      ]
    },
    "activity": {
      "repositories": ["acme/cao"]
    }
  }
}
```

Every Activity repository must remain within `allowed-owners` and, when
`control-plane.scope.allowed-repositories` is present, must also appear in that
exact allowlist. The field accepts exact `OWNER/REPOSITORY` coordinates only.
Omitting it preserves the fail-closed rule: workflow-token mode rejects broader
collection scope rather than inferring a control-repository-only subset.

<span id="multiple-organizations-in-one-enterprise"></span>

### Multi-owner runtime credentials

Installation IDs and tokens are never stored in policy or dispatch inputs.
gh-aw selects the installation from the target owner and repository at runtime.

When read-only workflow steps need `GH_TOKEN`, the selected authentication mode
determines the profile. App mode uses the imported read App token. PAT mode maps
the exact target repository to its owner-scoped read secret. Safe-output
processing independently maps the exact destination repository to its
owner-scoped write secret. The legacy split or combined PAT names are consulted
only when no explicit authentication mode is configured. Missing, incomplete,
or invalid credentials must not be copied into dispatch inputs or persisted in
artifacts.

CAO Activity applies the same separation independently from agentic workflow
authentication. It creates one collection job per resource owner. App mode
mints a fresh installation token for that owner and its exact repository
selection, which supports both organization-owned Apps and enterprise-owned
Apps installed in each enrolled organization. PAT mode requires a non-empty
exact `allowed-repositories` scope, validates every
`GH_AW_GITHUB_READ_PAT_REPOSITORIES` entry against the expected
`GH_AW_GITHUB_READ_PAT_<OWNER>` name, and exposes only that owner's token to its
collection job. Logs, inventory, issue status, and operational-value evidence
are collected in owner-scoped fragments and merged before the unchanged
snapshot and cache publication stages. Repository-local workflow-token mode
uses `GITHUB_TOKEN` for collection as well as trusted control-repository
checkout and notification operations. Explicit App/PAT modes never fall back
to it when the selected collection credential is missing.

Owner-scoped PATs isolate repository permissions, not rate-limit capacity:
tokens created by the same user share the user's core API quota. Activity
serializes PAT collection jobs and checks remaining core capacity before
inventory discovery and each repository log invocation. It stops at the
configured reserve without waiting for reset or switching credentials. App
installation collection jobs retain independent, parallel execution.

<span id="api-capacity"></span>

## API Capacity Admission

Before activation, shared control checks the primary REST API capacity of the exact credential selected for control precompute. The check uses GitHub's `GET /rate_limit` endpoint, which [does not consume primary rate-limit capacity](https://docs.github.com/en/rest/using-the-rest-api/rate-limits-for-the-rest-api#checking-the-status-of-your-rate-limit). Admission reserves at least 100 core requests and raises that requirement for broader configured inventory scans.

When capacity is insufficient, the run stops before repository discovery. The admission summary reports remaining and required requests, the UTC reset timestamp, and the approximate minutes and hours until reset. The dashboard exposes the latest failure as a GitHub API capacity admission gate rather than an undifferentiated workflow failure.

### Fetch GitHub data efficiently

Integrations that repeatedly read GitHub data should minimize both request volume and response size:

- Use [conditional requests](https://docs.github.com/en/rest/using-the-rest-api/best-practices-for-using-the-rest-api#use-conditional-requests) for data that may be unchanged. Persist the last response's `ETag` and send it as `If-None-Match` on the next request; GitHub returns `304 Not Modified` without consuming the primary rate-limit quota when the representation is unchanged.
- Use [GraphQL](https://docs.github.com/en/graphql/guides/using-graphql-with-github-actions) when a workflow needs related data from many repositories or resources. A single query can select only the fields needed and batch relationships that would otherwise require many REST requests.
- Keep discovery bounded and reuse data already fetched in the current run. Do not poll while waiting for rate-limit replenishment; stop and report incomplete work instead.

For direct HTTP clients, send the conditional-request headers explicitly. The CAO control precompute helper uses `gh api --cache 60s` for its bounded read requests; this lets the GitHub CLI reuse cached responses and negotiate conditional requests. For GitHub MCP calls, prefer one bounded query over repeated lookups. Conditional requests and GraphQL reduce avoidable traffic but do not replace the admission capacity check or the fail-closed limits described below.

Follow this order:

1. Do not rerun before the reported reset time. GitHub directs integrations with zero remaining capacity to wait until `x-ratelimit-reset`; repeated requests while limited can result in integration blocking. See [rate limits for the REST API](https://docs.github.com/en/rest/using-the-rest-api/rate-limits-for-the-rest-api#exceeding-the-rate-limit) and [REST API best practices](https://docs.github.com/en/rest/using-the-rest-api/best-practices-for-using-the-rest-api#handle-rate-limit-errors-appropriately).
2. For long-lived cross-repository automation, configure the least-privilege GitHub App profile. Follow [GitHub's guide to authenticated App requests in Actions](https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/making-authenticated-api-requests-with-a-github-app-in-a-github-actions-workflow). Shared control requests only `Actions: read` and `Contents: read` for pre-activation and still applies the checked-in CAO scope.
3. If an App cannot be installed and the exact scope is PAT-compatible, use separate owner-scoped read-only and write-capable fine-grained PATs only after informed consent. Follow [GitHub's fine-grained PAT guidance](https://docs.github.com/en/authentication/keeping-your-account-and-data-secure/managing-your-personal-access-tokens#creating-a-fine-grained-personal-access-token), restrict each token to its required repositories and permissions, set expirations, and let `gh cao setup auth token` store the protected Actions secrets and non-secret repository maps.

<span id="fine-grained-pat-fallback-1"></span>

## Fine-Grained PAT Fallback

A PAT is not a substitute for repository or organization access. It can only exercise access already held by the user who created it, and it becomes unusable when that user loses the underlying access. Lack of organization-owner permission to install an App does not by itself make a PAT viable.

Before offering a PAT fallback, verify all of these conditions:

1. The user can select the target organization as the PAT resource owner and already has the required access to every enrolled repository.
2. Organization and enterprise policy permits fine-grained PATs, and any required organization approval can be obtained before the first run.
3. Each token covers repositories from exactly one resource owner. A multi-owner control plane requires a separate read token and, when writes are approved, a separate write token for every represented owner.
4. Every endpoint required by the installed campaign supports the chosen token. Check endpoint-specific requirements: for example, [creating a check run](https://docs.github.com/en/rest/checks/runs#create-a-check-run) requires a GitHub App, while reading checks is a different operation. Do not substitute a classic PAT to work around an endpoint gap.
5. The PAT can be limited to the exact enrolled repositories, campaign-required permissions, and an explicit expiration and rotation owner.

If any condition fails, stop and recommend obtaining a GitHub App installation, narrowing or splitting the scope, or involving an organization owner. Do not present a PAT as an access bypass.

Before selecting, configuring, validating, or using a PAT, explain that it is user-bound, longer-lived than an App installation token, limited to one resource owner, subject to organization policy and endpoint gaps, and dependent on manual rotation and revocation. Obtain explicit confirmation to proceed. Inability to use an App, or the presence of an existing PAT secret, is not consent.

## Public Read-Only Profile

An App or PAT is not required for a bounded `review` run when every target repository is public and outputs remain in the current control repository. GitHub Actions automatically provides `GITHUB_TOKEN`; the workflows use it for control-repository workflow discovery, public checkout, and review outputs authorized in the control repository. This is built-in-token operation, not anonymous or credential-free operation.

:::caution[Public does not mean fully readable]
The built-in token may check out public code, but it does not automatically gain access to another repository's Actions logs, security data, issues, pull requests, or write APIs.
:::

Keep this profile within these boundaries:

- use `review` mode and keep safe outputs in the current control repository;
- keep target owners allowlisted and all repository and dispatch caps in force;
- treat unavailable cross-repository API data, including Actions logs or security data, as incomplete rather than weakening the requested analysis;
- configure an App or PAT for private or internal targets, an alternate review repository, or any `live` cross-repository write.

The workflow token is scoped to the repository containing the workflow. Public checkout does not grant target-repository write access, and a public repository's visibility does not expand the token's Actions, security, issue, or pull-request permissions. If a worker cannot read required target evidence with the available token, it must report incomplete and produce no speculative result.

:::caution[No additional GitHub credentials is not the same as free]
The built-in token removes a *GitHub credential* requirement. It does not remove *model* cost or entitlement. Every agentic run still consumes the configured engine's model entitlement and the campaign's AI credit budget, which are provisioned, billed, and rate-limited separately from GitHub permissions. A control repository can therefore satisfy this profile completely and still be unable to run: `copilot-requests: write` must be granted, the account or organization must be entitled to the configured model, and `max-ai-credits` plus any `monthly-ai-credit-budget` must have headroom. Treat missing model access as a blocked run with its own reason, never as an absence of findings.
:::

## Credential Boundary

- Each App client ID lives in its control-repository Actions variable, and each private key or PAT lives in its corresponding Actions secret.
- worker workflows receive repository names and routing policy, never credentials.
- Each Orchestrator and worker workflow run resolves its own token through imported shared control.
- Tokens must not appear in prompts, logs, safe outputs, Repo Memory, review bundles, or correlation metadata.
- For campaigns outside the public read-only profile, the App installation or PAT repository selection must cover every repository the enabled campaigns may read or update.

## Permissions

Grant only permissions required by installed campaigns. The current full catalog separates these App-level ceilings; each minted token is narrower when its job or safe-output handler needs fewer permissions:

| Permission | Read App | Write App | Reason |
| --- | --- | --- | --- |
| Actions | Read | Write | Inspect runs and dispatch approved workers |
| Administration | None | Read | Validate repository settings needed by approved maintenance outputs |
| Checks | Read | None | Inspect checks |
| Contents | Read | Write | Read repositories and create approved changes |
| Issues | Read | Write | Inspect issues and emit issue or comment safe outputs |
| Packages | Read | None | Inspect published package evidence |
| Pull requests | Read | Write | Inspect pull requests and emit approved pull-request outputs |
| Secret scanning alerts | Read | None | Inspect code-security evidence |
| Security events | Read | None | Inspect code-security evidence |
| Commit statuses | Read | None | Inspect status evidence |
| Vulnerability alerts | Read | None | Prioritize dependency security work |
| Metadata | Read | Read | Required automatically for GitHub Apps |

A campaign-only installation should narrow these permissions to that campaign's workflows. Fine-grained PATs should be limited to the same repositories and permissions.

For owner-scoped token creation, secure prompts, interrupted setup, and
repository maps, follow [Configure a fine-grained token](control-plane-authentication.md#configure-a-fine-grained-token).

### Migrate from the legacy PAT

Do not copy one broad legacy token into both new secrets. Create independent tokens with separate permission and repository ceilings:

1. Run the setup preview and verify one read token per resource owner covers only the exact repositories CAO must inspect.
2. Verify each write token covers only approved safe-output repositories. When review outputs stay in the control repository, the control repository normally is the only write destination.
3. Run a bounded review that proves the read PAT can read all intended repositories and cannot perform a reversible write probe.
4. Prove separately that the write PAT can perform and clean up the same probe only in an approved output repository.
5. Confirm `GH_AW_GITHUB_AUTH_MODE=pat`, delete `GH_AW_GITHUB_TOKEN` and any legacy split-PAT secrets, then repeat the bounded review to prove neither path depended on a compatibility fallback.

Use a disposable tag, branch, or equivalent repository-standard probe and always clean it up. Do not perform a write probe against an unapproved production repository.

<span id="validation"></span>

## Validate Before Activation

Use the [activation checklist](control-plane-authentication.md#validate-before-activation)
to prove read access, approved output routing, and a bounded `review` run before
any explicit live promotion. Local `gh cao doctor` is not runtime credential
proof; start with [read-only runtime verification](#explicit-runtime-credential-verification).
Any write-and-cleanup probe needs separate authority for its exact destination.
Recheck when scope, campaign APIs, mode, or review destination changes.

## Model Inference Is Separate

CAO installation does not require Copilot organization billing. Bundled workflows do: they use `copilot-requests: write` and the built-in workflow token for inference.

For this reason, verifying it up front is completely optional: most user tokens cannot read organization billing, and a granted `copilot-requests: write` permission alone does not ensure model access. Without an entitlement, a bundled workflow fails with HTTP 403 before the agent starts. Customers may author workflows with another gh-aw-supported engine/provider and configure its Actions secrets; that requires an explicit workflow change and compilation. CAO does not support `COPILOT_GITHUB_TOKEN` inference fallback, runtime token precedence, or mixed authentication profiles, and target-access credentials cannot authenticate model inference.

## Credential Reference

These are configuration references, not an unconditional fallback chain.
The explicit [runtime mode](#runtime-credential-selection) selects the profile.

| Name | Location | Purpose |
| --- | --- | --- |
| `GH_AW_GITHUB_AUTH_MODE` | Actions variable | `app`, `pat`, or `workflow-token` |
| `GH_AW_GITHUB_READ_APP_ID`, `GH_AW_GITHUB_WRITE_APP_ID` | Actions variables | Separate App client IDs |
| `GH_AW_GITHUB_READ_APP_PRIVATE_KEY`, `GH_AW_GITHUB_WRITE_APP_PRIVATE_KEY` | Actions secrets | Corresponding PEM private keys |
| `GH_AW_GITHUB_READ_PAT_<OWNER>`, `GH_AW_GITHUB_WRITE_PAT_<OWNER>` | Actions secrets | Separate owner-scoped fine-grained tokens |
| `GH_AW_GITHUB_READ_PAT_REPOSITORIES`, `GH_AW_GITHUB_WRITE_PAT_REPOSITORIES` | Actions variables | Exact repository-to-secret-name JSON maps |
| `GITHUB_TOKEN` | Provided by Actions | Bounded repository-local access and approved public-only monitoring |
| `GH_AW_GITHUB_READ_PAT`, `GH_AW_GITHUB_WRITE_PAT`, `GH_AW_GITHUB_TOKEN` | Legacy Actions secrets | Compatibility only for shared-control workflows with no explicit mode; not used by Activity |

Tokens are resolved inside each run. They never belong in policy, dispatch inputs, prompts, logs, safe outputs, or review bundles.

<span id="rotation-and-incidents"></span>

### Rotation and revocation

For Apps, add replacement keys, validate bounded review runs, revoke old keys,
and recheck installations; App private keys do not expire automatically.
For PATs, create replacements with the same or
narrower access, replace the affected owner's read/write secrets, validate read
and approved safe-output access separately, then revoke the previous tokens.
Track PAT approval and expiration yourself; CAO does not rotate credentials or
warn before expiry. Audit-log activity is attributed to the App identity or
the PAT owner's user account, respectively.

:::danger[Suspected exposure]
Cancel active runs and revoke the credential first. Disabling a campaign does not revoke its App installation or PAT. Inspect logs and outputs, rotate credentials, and resume only in `review` mode.
:::

## Offline dashboard access

`offline_access` is a GitHub App **user-authorization scope**, not a repository
permission or offline data grant. Hosted sign-in requests it for GitHub App
client IDs (`Iv...`) to receive an expiring user token and refresh token even
when expiration is disabled globally on that App. The App's configured
permissions still govern access; organization/team checks require
organization **Members: read** and an approved installation in each allowed
organization. OAuth App clients retain `read:org`.
This GitHub App scope is available on GitHub.com and GHES 3.23 or later.

Tokens and refresh tokens remain server-side, never in browser caches. The
static dashboard can display previously downloaded data offline, but sign-in,
refresh, and server-backed operations require a connection.
