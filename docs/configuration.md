---
title: Configuration Reference
description: Checked-in policy, credential secrets, and manual inputs for Central Agentic Ops.
---

Persistent non-secret policy lives only in `.github/workflows/cao.json` in the control repository. Workflows read that file at the exact `github.workflow_sha`, so workflow code and policy are one reviewed revision. Repository variables named `CENTRAL_AGENTIC_OPS_*` are not read as defaults, overrides, or compatibility fallbacks.

For explicitly requested targets without campaign or fleet topology, use
[`control-plane.operations` and the shared operation import](authority-envelopes.md).
Campaigns and standalone fleet workflows remain supported operating patterns
over the same authority kernel.
Use [From Outcome to Execution](operation-patterns.md) to derive the simplest
structure from the outcome, scope, and execution requirements before configuring
policy. This design decision grants no authority: explicit-target work, one fleet
mission and orchestrator/worker campaigns retain the same reviewed boundaries.

Keep credentials in Actions secrets. Manual inputs may select a target or narrow a checked-in limit for one run, but they never change policy or widen it.

The policy is plain JSON so Node.js can parse it with the built-in `JSON.parse` API and no runtime dependencies. Its Draft 2020-12 schema is published at `.github/workflows/shared/cao.schema.json`; the checked-in policy's `$schema` property enables editor completion and diagnostics. The dependency-free resolver remains the runtime validator for constraints JSON Schema cannot express, including duplicate keys, case-insensitive uniqueness, and `cell-index < cell-count`.

## Control Policy

This minimal policy enables the installed Dependabot campaign and its workers in `review` mode for repositories owned by `acme`:

```json
{
  "$schema": "https://raw.githubusercontent.com/githubnext/gh-cao/main/.github/workflows/shared/cao.schema.json",
  "version": 1,
  "gh-aw-version": "v0.93.5",
  "control-plane": {
    "scope": {
      "allowed-owners": ["acme"]
    },
    "campaigns": {
      "dependabot": {
        "workers": {
          "update-planner": {
            "workflow": "dependabot-update-planner"
          }
        }
      }
    }
  }
}
```

Commit the file before running an installed campaign. A missing or invalid document fails closed. An undeclared campaign skips activation before repository discovery or agent execution. See [Admission Gates](admission.md) for the exact pre-activation checks and the checks deferred to authorized-run precompute.

Control repositories must declare `gh-aw-version` at the document root. Campaign infrastructure reads this exact release when installing the CLI, and fleet maintenance can compare it with the expected release to identify repositories that need an upgrade. The field remains optional for target-authority-only documents.

`cao.json` is the canonical gh-aw version pin for a control repository. Do not duplicate the pin in `.github/workflows/aw.json`; that file configures repository-wide compiler behavior such as strict mode, maintenance, and automatic upgrades. `npm run check:gh-aw-versions` verifies that campaign `min-version` fields, generated workflow compiler versions, and documentation examples match the control policy.

The schema defaults are:

| JSON path | Default | Range or values |
| --- | --- | --- |
| `control-plane.scope.allowed-owners` | Control repository owner | Owner names |
| `control-plane.scope.allowed-repositories` | All repositories under an allowed owner | Exact `owner/repository` names |
| `control-plane.activity.repositories` | Global scope | Nonempty exact collection subset admitted by owner and repository scope |
| `control-plane.activity.public-only` | `false` | Explicit observation-only collection of verified public repositories with `GITHUB_TOKEN`; evidence stays partial |
| `control-plane.inventory.max-scan-repositories` | `1000` | `1` through `100000` |
| `control-plane.inventory.cell-count` | `1` | `1` through `1000` |
| `control-plane.inventory.cell-index` | `0` | Less than `cell-count` |
| `control-plane.inventory.batch-size` | `100000` | `1` through `100000` |
| `control-plane.inventory.batch-index` | `0` | Non-negative integer |
| `control-plane.web.experimental` | `false` | `true` or `false` |
| `control-plane.web.favicon` | `./favicon.svg` | Absolute HTTPS URL or `./` relative path |
| `control-plane.defaults.mode` | `review` | `review` or `live` |
| `control-plane.defaults.max-repositories` | `1` | `1` through `1000` |
| `control-plane.defaults.rollout-percent` | `100` | `1` through `100` |
| `control-plane.defaults.monthly-ai-credit-budget` | `0` | Deprecated compatibility field; no runtime admission effect |
| `control-plane.campaigns.<campaign>.targets.<owner/repository>.mode` | Campaign mode | `review` or `live` |

Each entry under `control-plane.campaigns` may override the defaults with `enabled`, `mode`, `max-repositories`, and `rollout-percent`; `monthly-ai-credit-budget` remains accepted as a deprecated compatibility field but has no runtime admission effect. The internal `dashboard` campaign also accepts `deploy: false` to keep building its reusable artifact while another workflow owns Pages deployment; `deploy` defaults to `true`. Its optional `targets` map assigns a different mode to an exact repository while unmatched repositories retain the campaign mode. Every campaign target must remain inside the global allowed owners and, when present, the global repository allowlist. The `workers` map is the campaign's workflow catalog: every worker entry requires its exact `workflow` slug, may set `enabled: false` to disable that worker, and may set `max-mode` to narrow its mode. Campaign and worker names are lowercase kebab-case identifiers loaded directly from this policy.

The optional `control-plane.activity.repositories` field declares an explicit
evidence-collection subset when campaign rollout scope is broader than the
repositories whose workflow runs Activity should ingest. Every entry must be
an exact repository admitted by `allowed-owners` and, when an exact
`allowed-repositories` list is present, must appear there. The field does not
grant campaign authority or credential reach. When omitted, Activity uses the
global scope and preserves its fail-closed authentication checks.

For public-only monitoring without App/PAT configuration, `gh cao setup`
recommends the built-in-token profile when every selected repository is public.
For scripts, use `gh cao setup --repository 'OWNER/*'`; it accepts
public Pages publication and saves only currently public exact repository names.
The reviewed `activity.public-only: true` setting requires an explicit Activity
repository list and workflow-token authentication. Runtime visibility changes
stop publication. This is observation only, not permission to activate campaigns,
execute agents, or write to targets. See the
[public-only quickstart](authentication.md#public-only-monitoring-quickstart).

The optional `control-plane.web` section configures deterministic web surfaces without changing rollout authority. Dashboard views declared in experimental navigation sections are omitted by default; set `experimental` to `true` to include them. Set `favicon` to an absolute HTTPS URL without credentials, query, or fragment, or to a non-traversing `./` relative path available in the generated site. The dashboard campaign ships `./favicon.svg` as its default.

### Deployment-specific host extensions

When a hosted dashboard needs different server and Redis capabilities from the
default deployment, keep `.github/workflows/cao.json` as the sole rollout
policy and create an optional, reviewed host extension in the same directory,
such as `.github/workflows/cao.host.json`:

```json
{
  "extends": "cao.json",
  "control-plane": {
    "web": {
      "host": {
        "target": { "module": "container", "name": "hosted-dashboard" },
        "redis": { "module": "generic", "tls": { "mode": "required" } }
      }
    }
  }
}
```

This is an overlay, **not** a standalone control policy: only
`control-plane.web.host` may be supplied. Do not copy `scope`, `campaigns`,
`defaults`, `target-authority`, or any other rollout or credential fields into
it. Keep Redis URLs, passwords, and TLS certificates in the deployment's
secret environment, not in either JSON document.

The host extension may also declare `cors` next to `target` and `redis`. Omit
it to keep the dashboard same-origin. When another reviewed origin must call
the server, list exact origins, for example
`"cors": { "allowed-origins": ["https://tools.example.com"] }`.
Wildcards and plaintext non-loopback origins are rejected. CORS is
credential-less: listed origins can read only anonymous responses and can never
use or read the signed-in session.

To use the extension, explicitly point the hosted server's `CAO_POLICY_PATH`
at the reviewed extension and pass that file as the dashboard build's control
settings argument (for example, from `dashboard/site/`,
`npm run build -- dist ../../.github/workflows/cao.host.json`). The dashboard
build loads the composed control settings; the Go server resolves the same
host declaration. Operational workflows continue to read `cao.json` at their
exact workflow SHA and never obtain rollout authority from the extension.

Imports resolve relative to the importing file and must remain within the
extension's directory after symlink resolution. Cycles, more than eight
imports, duplicate keys, expressions, non-host overrides, and invalid
composed policy fail closed. See the
[control architecture specification](https://github.com/githubnext/gh-cao/blob/main/specs/control-architecture.md#531-deployment-specific-host-extensions)
and [managed Redis guide](deployment-managed-redis.md) for host-module options.

For example, this policy keeps Dependabot in review across its scope while promoting one exact target to live:

```json
{
  "version": 1,
  "control-plane": {
    "scope": {
      "allowed-owners": ["acme"],
      "allowed-repositories": ["acme/example-service"]
    },
    "campaigns": {
      "dependabot": {
        "mode": "review",
        "max-repositories": 1,
        "rollout-percent": 100,
        "targets": {
          "acme/example-service": {
            "mode": "live"
          }
        },
        "workers": {
          "update-planner": {
            "workflow": "dependabot-update-planner"
          }
        }
      }
    }
  }
}
```

Shared control applies schema defaults, then `control-plane.defaults`, campaign values, the exact target mode, and any explicit worker ceiling, in that order. A dispatch request may narrow the result. Workers independently resolve their exact target from the policy revision at `github.workflow_sha`; an envelope that requests a wider mode fails before agent execution. Campaign repository and percentage caps still apply across all candidates regardless of target mode.

### Standalone Agentic Workflows

Start with a regular workflow, such as
[Daily Repo Status from Agentics](https://github.com/githubnext/agentics/blob/main/docs/repo-status.md),
and adapt its repository assumptions before reusing it across repositories.
`gh aw add` installs the upstream workflow; it does not automatically add CAO
admission or redirect its writes.

Keep upstream installation ownership with `gh aw add`/`update`. Prepare a
distinctly named, locally maintained variant, recording its source revision and
manual update responsibility. Import `shared/control.md` as `role: standalone`,
bind reads to the admitted target, and use each candidate's resolved mode and
output destination. Route every effect, including closing older reports, to
review; keep report matching target-specific.

Declare the variant under `control-plane.workflows` with `enabled: false`,
`mode: review`, `max-mode: review`, and `max-repositories: 1`. Start manual-only
against one explicitly enrolled repository, without activating a duplicate
upstream schedule. Keep separate read/write credentials in Actions secrets.
Checked-in rollout policy decides where it may run; optional control-owned
steering may refine the task but cannot widen authority.

Compile with the policy-pinned `gh aw`, inspect admission and output routing,
and review source, lock, and policy together before enabling a review run.
Review mode executes the agent and may create review outputs; it is not a
no-write simulation. Once the integration is proven, reuse the definition
across additional explicitly enrolled targets through reviewed policy changes.
Live delivery additionally requires a supported output path and explicit live
authorization. No campaign or new installation command is required.

Any agentic workflow can opt into CAO without becoming a campaign or introducing
an orchestrator/worker pair. Install or maintain its Markdown source in the
control repository, retain its own trigger and mission, and declare its exact
workflow slug under `control-plane.workflows`:

```json
{
  "version": 1,
  "gh-aw-version": "v0.93.5",
  "control-plane": {
    "scope": {
      "allowed-owners": ["acme"],
      "allowed-repositories": ["acme/service-a", "acme/service-b"]
    },
    "workflows": {
      "fleet-audit": {
        "enabled": true,
        "mode": "review",
        "max-mode": "review",
        "max-repositories": 2,
        "rollout-percent": 50
      }
    }
  }
}
```

In `.github/workflows/fleet-audit.md`, add the shared-control import and
pre-activation gate:

```yaml
jobs:
  pre-activation:
    outputs:
      cao_authorized: ${{ steps.cao_admission.outputs.authorized == 'true' && steps.cao_precompute.outputs.authorized != 'false' }}
if: needs.pre_activation.outputs.cao_authorized == 'true'
imports:
  - uses: shared/control.md
    with:
      role: standalone
      workflow: fleet-audit
      read_repository: ${{ github.repository }}
      read_contents: read
```

Keep the workflow's existing gh-aw engine, read permissions, tools, credentials,
and safe outputs. Its pre-activation permissions need `contents: read` and
`actions: read`, as in the campaign integration. Expose optional `target_repo`,
`safe_output_repo`, `safe_output_mode`, `max_repos`, and `rollout_percent` manual
inputs only when needed; omit defaults that would unintentionally narrow a
scheduled or manual run's checked-in policy.
The [compilation fixture](https://github.com/githubnext/gh-cao/blob/main/tests/fixtures/workflows/fleet-audit.md)
shows a complete review-report workflow.

The identity is the filename slug, not the display name. Admission verifies it
against the actual compiled workflow reference and policy at `github.workflow_sha`.
No campaign descriptor, worker mapping, dispatch envelope, or campaign
installation record is required. An undeclared or disabled workflow cannot
activate. Compile with the policy-pinned `gh aw`, then commit source, generated
lock, and policy together; use `gh aw run fleet-audit` for a manual run.

Standalone workflows inherit `control-plane.defaults` and global scope and
inventory settings. Each declaration supports `enabled`, `mode`,
`max-repositories`, `rollout-percent`, optional `targets.<owner/repository>.mode`,
and optional `max-mode`. As with worker ceilings, `max-mode` can only narrow
default and exact-target modes; `live` does not promote a review target.
`monthly-ai-credit-budget` remains compatibility-only. Persistent `debug`,
campaign workers, and gh-aw execution capabilities are not accepted in this map.

Precompute uses the same bounded discovery, deterministic cells and batches,
and request narrowing as campaign orchestration. It excludes archived/disabled
repositories and admits the first deterministic subset bounded by the
repository limit and percentage of the eligible batch. The workflow performs
its mission directly against that admitted set; it does not dispatch workers
or discover additional targets. An empty set is a no-op; unavailable required
inventory evidence fails closed.

Read `/tmp/gh-aw/agent/control-precompute.json` and use each admitted candidate's
`safe_output_mode` and `safe_output_repo`. Mixed review/live target policies
remain distinct: review proposals go to the validated review destination
(the control repository by default), and live target writes go only to that
target. Fleet admission does not mint broader credentials or add safe-output
capabilities. Declare the required read scope, output repository allowlists,
and authentication in the workflow using gh-aw; if they cannot reach an
admitted target or implement its output route, report incomplete rather than
falling back to a different destination. Merely installing or observing a
workflow does not opt it into this contract.

### Monthly Campaign Budgets

`monthly-ai-credit-budget` is deprecated. Existing policy files may keep the field during migration, but shared control no longer reads month-to-date usage or gates repository admission with monthly AI Credit totals. Existing repository, rollout, dispatch, and native gh-aw workflow credit limits remain cumulative.

## Live Authority

`live` workers use only the control repository's `.github/workflows/cao.json` as their policy authority. Declare live campaign and worker ceilings, allowed owners or repositories, and target-specific modes there. Workers resolve that policy from the exact workflow SHA before agent execution; no `cao.json` or authority declaration is required in a target repository.

## Campaign Package Marketplace

`control-plane.marketplace.registries` is an ordered registry list. Earlier
registries take precedence when multiple registries expose the same package
coordinate. New policies include the official catalog explicitly; remove that
entry to disable it or add public, private, or GitHub Enterprise registries.

```json
{
  "control-plane": {
    "marketplace": {
      "cache-ttl-seconds": 900,
      "registries": [
        {
          "id": "official",
          "name": "Official CAO catalog",
          "repository": "githubnext/gh-cao",
          "ref": "main",
          "auth": { "type": "none" }
        },
        {
          "id": "internal",
          "repository": "acme/cao-packages",
          "path": "packages",
          "ref": "stable",
          "api-url": "https://github.acme.example/api/v3",
          "auth": { "type": "pat", "secret": "CAO_INTERNAL_REGISTRY_PAT" }
        }
      ]
    }
  }
}
```

Authentication types are `none`, `pat`, and `github-app`. PAT entries reference
one environment secret name. GitHub App entries reference `app-id-secret`,
`private-key-secret`, and `installation-id-secret`. Values stay in the trusted
Activity or hosted resolver and are never published to the dashboard. The
marketplace is read-only: its action copies
`gh cao add OWNER/REPOSITORY[/PATH]@COMMIT`; it does not execute installation.
See [Browse campaign packages](marketplace.md) for registry setup, backend
behavior, troubleshooting, and the read-only dashboard flow.
`specs/marketplace.md` defines the normalized package and failure contracts.

## Credentials

Configure a GitHub App or fine-grained PAT profile for cross-repository access
that requires authenticated evidence or writes. For public-only monitoring,
guided setup can use the built-in Actions token with the explicit reviewed
collection policy described above; unavailable evidence remains partial.

| Name | Required | Purpose |
| --- | --- | --- |
| `GH_AW_GITHUB_READ_APP_ID` | With App authentication | Repository variable containing the read-only GitHub App client ID. |
| `GH_AW_GITHUB_READ_APP_PRIVATE_KEY` | With App authentication | Repository secret containing the read-only App private key. |
| `GH_AW_GITHUB_WRITE_APP_ID` | With write-capable App authentication | Repository variable containing the safe-output and API-gate GitHub App client ID. |
| `GH_AW_GITHUB_WRITE_APP_PRIVATE_KEY` | With write-capable App authentication | Repository secret containing the safe-output and API-gate App private key. |
| `GH_AW_GITHUB_AUTH_MODE` | Optional for repository-local Activity; required for broader Activity scopes | Repository variable selecting `app`, `pat`, or `workflow-token`. Unset Activity mode uses `GITHUB_TOKEN` only for an exact control-repository scope. App setup writes `app` after the read App is verified, while PAT setup writes `pat` after the selected profile is complete. |
| `GH_AW_GITHUB_READ_PAT_REPOSITORIES` | With owner-scoped PAT authentication | Non-secret JSON repository variable mapping each exact readable repository to its owner-scoped secret name. |
| `GH_AW_GITHUB_WRITE_PAT_REPOSITORIES` | With owner-scoped PAT authentication | Non-secret JSON repository variable mapping each approved output repository to its owner-scoped secret name. |
| `GH_AW_GITHUB_READ_PAT_<OWNER>` | With owner-scoped PAT authentication | Read-only fine-grained token for one resource owner; hyphens in the owner are encoded as underscores. |
| `GH_AW_GITHUB_WRITE_PAT_<OWNER>` | With owner-scoped PAT authentication | Write-capable fine-grained token for one resource owner, used only by safe-output processing. |
| `GH_AW_GITHUB_READ_PAT` | PAT fallback | Read-only fine-grained token for control and target repository access. |
| `GH_AW_GITHUB_WRITE_PAT` | PAT fallback | Write-capable fine-grained token used only by safe-output processing. |
| `GH_AW_GITHUB_TOKEN` | Deprecated PAT fallback | Legacy combined token retained for backward compatibility. |
| `GH_AW_CI_TOKEN` | Optional Dependabot path | Additional token used only when an empty CI commit is required. |

The installer-managed root package remains free of interactive setup
so gh-aw installation works non-interactively. Follow [Automated App setup](authentication.md#automated-app-setup) to create both Apps and install them for the accounts represented in the exact repository allowlist, or configure the four values manually. Shared control uses the read-only App for GitHub tools and admission. It exposes the write-capable App to safe outputs and, with only `Actions: write`, to best-effort API-gate persistence after a fresh capacity denial. Orchestrator safe outputs to the control repository use the permission-scoped workflow token when no App token is active; cross-repository outputs retain their configured credential. Each path uses only its documented credential fallback when that credential's reach is sufficient.

## Manual Inputs

Campaign orchestrators expose these `workflow_dispatch` inputs:

| Input | Effect |
| --- | --- |
| `target_repo` | Selects one exact target within checked-in owner and repository scope. |
| `safe_output_repo` | Selects an allowed private review destination for this run. |
| `max_repos` | Narrows the checked-in campaign repository ceiling. |
| `rollout_percent` | Narrows the checked-in campaign rollout ceiling. |
| `safe_output_mode` | Narrows `live` policy to `review`, or requests the already-authorized mode. |

Manual inputs affect only one run. They do not update `.github/workflows/cao.json`. Use `gh aw run <workflow-name>` to trigger an installed workflow so gh-aw validates and records its inputs correctly.

## Ops Publish Add-on

Ops Publish reads `control-plane.publishing` and `control-plane.scope` from the same JSON policy. `publishing.enabled` defaults to `false`; when enabled, `publishing.reviewers` must be non-empty. `publishing.control-repositories` defaults to the repository containing the add-on.

PAT fallback uses the separate `CENTRAL_AGENTIC_OPS_PUBLISH_CONTROL_TOKEN` and `CENTRAL_AGENTIC_OPS_PUBLISH_TARGET_TOKEN` secrets. These credentials are not policy and never override owner, repository, reviewer, or target-authority checks. See [Ops Publish](operations.md#publishing-reviewed-campaign-issues).

## Markdown Steering

Each workflow may load optional repository-specific instructions from `.github/cao/<operation>.md` in the control repository. Supported operation names are `uk-ai-advisory`, `cao-evolution`, `dependabot`, `eslint-rules`, `eu-cra-compliance`, `optimization`, and `software-development-practices`.

Steering can refine evidence, priorities, and selection within resolved policy. It cannot grant tools, credentials, permissions, repository reach, or safe-output capabilities. Campaign updates do not overwrite these files.

## Optional Observability

The dispatcher span is built into `shared/control.md`; exporter configuration determines where gh-aw sends it. Set the `GH_AW_DEFAULT_OTLP_ENDPOINT` Actions variable and `GH_AW_DEFAULT_OTLP_HEADERS` Actions secret at repository, organization, or enterprise scope. Export is disabled when the endpoint or matching headers are absent. Hosted dashboard servers configure their own OpenTelemetry export; see [Azure](deployment-azure.md#monitoring-the-deployment) and [Upstash Redis](deployment-upstash.md#monitoring-the-deployment).

```bash
CONTROL_REPO="acme/central-agentic-ops"
gh variable set GH_AW_DEFAULT_OTLP_ENDPOINT \
  --repo "$CONTROL_REPO" \
  --body "https://collector.example.com/v1/traces"
gh secret set GH_AW_DEFAULT_OTLP_HEADERS --repo "$CONTROL_REPO"
```

At the secret prompt, enter the complete exporter header string, such as
`Authorization=Bearer <token>` or
`Authorization=Basic <credentials>,X-Scope-OrgID=<tenant>`. Use an ingestion
credential dedicated to Actions; do not reuse an observability read credential.

The optional `shared/sentry.md`, `shared/grafana.md`, and `shared/datadog.md` imports configure exporters only; they do not create the dispatcher span. Their headers are:

| Provider | Header |
| --- | --- |
| Sentry | `Authorization: <GH_AW_OTEL_SENTRY_AUTHORIZATION>` |
| Grafana Cloud | `Authorization: <GH_AW_OTEL_GRAFANA_AUTHORIZATION>` |
| Datadog | `DD-API-KEY: <GH_AW_OTEL_DATADOG_API_KEY or DD_API_KEY>` |

Installed Central Agentic Ops campaigns do not include these optional provider files by default. Use the default OTLP variable and secret unless an installed campaign deliberately carries a provider import, then recompile all affected workflows.

## Sources of Truth

- Machine-readable policy schema: `.github/workflows/shared/cao.schema.json`
- Runtime policy resolution: `.github/workflows/shared/policy.mjs` and [Control Policy Specification](control-policy-specification.md)
- Checked-in control policy: `.github/workflows/cao.json`
- Deterministic control commands: `.github/workflows/shared/control.mjs`
- Shared runtime enforcement: `.github/workflows/shared/control.md`
- Campaign inventory: the root and campaign `aw.yml` manifests
- Credentials and permissions: [Configure Authentication](authentication.md)
