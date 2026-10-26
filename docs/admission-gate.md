---
title: Set up the admission gate
description: Deploy and operate the optional OIDC-authenticated workflow cost gate, from a canary repository to organization and enterprise rollout.
---

The admission gate decides whether a participating gh-aw execution may start.
It authenticates the isolated admission job with GitHub Actions OIDC, checks reviewed
workflow enrollment and fresh accounting, and returns policy-defined AI Credit
allowances. It is an optional **cooperative start-time check**, not a billing
API, a reservation system, or an unbypassable organization policy.

This gate is separate from [CAO control-policy admission](admission.md).
Approval cannot enable a campaign, widen repository scope, promote review to
live, or grant writes. Those decisions remain in the control repository's
reviewed policy.

## Before you begin

:::caution[Use a matching client revision]
The server supports the v1.2 wire contract merged in
[github/gh-aw#70429](https://github.com/github/gh-aw/pull/70429), checked at
`be70cb4ec4317135a1ab21d5b830b6fae7d5be4a`. It does **not** implement the older
nine-field/correlation-ID protocol merged in
[github/gh-aw#70286](https://github.com/github/gh-aw/pull/70286) at
`d9abaf92a50fd8778f12fa122138b30c75daf8f2`. Confirm the client's actual protocol,
not merely that the initial proposal or a compiler release exists.
:::

You need:

- An HTTPS hosted CAO server using the PostgreSQL operational backend. Local
  SQLite, Redis, and volatile memory cannot enable this service.
- Explicit enrollment for each immutable repository ID, owner ID, workflow
  reference and revision, subject, event, and original actor ID.
- A trusted publisher that atomically supplies complete, fresh AI Credits
  accounting with reviewed policy. The gate does not create that publisher.
- Compiled workflows from a gh-aw version that implements admission. Existing
  workflows must be recompiled with that version before variables activate it.
- GitHub permissions to read and manage Actions variables at the selected scope.

The initial server identity profile supports GitHub.com, a single string
audience, and non-reusable workflows. Reusable calls and unsupported identity
profiles are denied rather than assigned weaker policy.

:::caution[No admission workflow secret]
Admission uses a job-issued OIDC JWT. Do not create a PAT, App private key,
installation token, or shared secret for this exchange. The setup commands
configure **non-secret Actions variables** only. Existing server OAuth/database
credentials, and any separately authorized backend App credentials, stay in the
server's protected secret storage.
:::

## 1. Prepare and validate the authority

Provision the canonical and operational PostgreSQL namespaces using the existing
owner-run [database setup](https://github.com/githubnext/gh-cao/blob/main/server/POSTGRES.md).
Keep the owner credential out of runtime processes. Configure the hosted
deployment, OAuth, HTTPS/proxy boundaries, dashboard assets, and collection
through the [server deployment guide](deployment.md).

Mount the protected policy/accounting snapshot and set:

```sh
export CAO_ADMISSION_POLICY_FILE=/run/cao/admission-snapshot.json
export CAO_ADMISSION_AUDIENCE=https://cao.example.org/api/admission
```

The file must be server-owned and not writable by workflows, targets, or
dashboard users. Publish it atomically so policy and accounting form a single
consistent snapshot. Use the complete format and trust requirements in the
[server admission reference](https://github.com/githubnext/gh-cao/blob/main/server/ADMISSION.md#protected-policy-and-accounting-input).
Do not copy synthetic example identities or timestamps into a live deployment.

Validate the current snapshot locally with the server binary:

```sh
cao-dashboard admission validate
# Or supply the same inputs explicitly:
cao-dashboard admission validate \
  --policy /run/cao/admission-snapshot.json \
  --audience https://cao.example.org/api/admission
```

The command checks policy structure, enabled enrollment and current accounting
freshness/budget without contacting GitHub, modifying the file, or inventing
usage. A failure exits nonzero. A successful check is **not** proof that issuer
keys, runtime storage, deployment routing, or a particular workflow are ready.
Keep the trusted publisher running; observations older than two minutes become
unavailable rather than zero.

Start or roll out the hosted service with its normal deployment command, such
as `cao-dashboard serve-hosted` in the configured process deployment. The public
service route is exactly `POST /api/admission`. Authentication and trace
handling do not depend on dashboard cookies.

With server OTLP export configured, the client's valid `traceparent` parents
the server HTTP span and its request-scoped dependencies. The merged client
relays its caller-owned trace context through activation, allowing the
workflow and server sessions to be correlated by trace ID. The response never
returns a trace/correlation ID. Missing, malformed or duplicate context starts
a fresh trace without changing the decision; baggage and tracestate are
discarded, and sampling is controlled locally.

The service is instrumented through the hosted server's standard OTLP/HTTP
configuration, not a separate admission exporter. Set `OTEL_EXPORTER_OTLP_ENDPOINT`
(or signal-specific trace/metric endpoints) and the server's service/resource
attributes. HTTP duration/status metrics use the fixed `/api/admission` route;
child spans identify identity verification, trusted-key refresh, policy
evaluation and durable decisions. No raw credentials, request identity,
allowances or denial reasons are captured. Export failure does not change an
admission result, and shutdown flushes exporters with the server's existing
bounded lifecycle. See the
[OTel operation reference](https://github.com/githubnext/gh-cao/blob/main/server/ADMISSION.md#opentelemetry-operations)
for names and configuration.

## 2. Configure one canary repository

Preview the exact endpoint and audience:

```sh
gh cao setup admission --repo acme/control \
  --url https://cao.example.org/api/admission --dry-run
```

Apply by omitting `--dry-run`:

```sh
gh cao setup admission --repo acme/control \
  --url https://cao.example.org/api/admission
gh cao admission status --repo acme/control --json
```

Setup defaults the
audience to the endpoint and outages to `block`. Use `--audience VALUE` if the
authority has a different registered audience. `--on-outage ignore` deliberately
permits execution after transient failures are exhausted.

The three managed variables are:

| Variable | Meaning |
| --- | --- |
| `GH_AW_DEFAULT_ADMISSION_URL` | Full HTTPS endpoint ending in `/api/admission`; empty disables the call. |
| `GH_AW_DEFAULT_ADMISSION_AUDIENCE` | Expected OIDC audience; empty falls back to the endpoint. |
| `GH_AW_DEFAULT_ADMISSION_ON_OUTAGE` | `block` or `ignore`; gh-aw's built-in fallback is `ignore`, while CAO setup defaults to `block`. |

Setup preserves unrelated variables and publishes the URL last. Once the URL
is configured, later setup commands can omit `--url`: they reuse the target
repository's effective configuration, or the current control repository's
configuration for bulk rollout or recovery from an empty target override.
Inherited organization defaults participate in that resolution. Missing or
unreadable configuration fails explicitly; there is no guessed public server.
An explicit `--url` always overrides the default.

## 3. Exercise a real participating workflow

Use the canary's normal trigger. For an already reviewed manual-dispatch
workflow, the standard GitHub CLI commands are:

```sh
gh workflow run triage.lock.yml --repo acme/control
gh run list --repo acme/control --workflow triage.lock.yml --limit 5
gh run view RUN_ID --repo acme/control
```

Do not add inputs that widen CAO policy. Check the activation result and the
existing dependent jobs. A valid approval supersedes daily-AIC validation;
the client applies the supplied allowances to the corresponding runtime caps.
Omitted optional detection/evals allowances preserve existing component policy.

Do not use a PAT, dashboard capability, or fabricated OIDC token with `curl`
as a positive admission test. Identity must come from the real admission job.
`status` verifies managed settings, not workflow compilation, enrollment, or an
admission decision. A successful workflow run alone is not proof of admission:
check whether the helper was skipped or used outage `ignore`.

## 4. Roll out beyond the canary

Select exactly one scope. Preview first, then remove `--dry-run` to apply.
These examples reuse the server URL configured in the current control checkout:

```sh
# Repositories selected by this control repository's global CAO policy.
gh cao setup admission --all-repos --dry-run
gh cao setup admission --all-repos

# Organization defaults, including future repositories.
gh cao setup admission --org acme --org engineering --dry-run
gh cao setup admission --org acme --org engineering

# Organization defaults for every organization discovered in an enterprise.
gh cao setup admission --enterprise company --dry-run
gh cao setup admission --enterprise company
```

`--all-repos` uses exact `control-plane.scope.allowed-repositories` when
present; otherwise it enumerates visible repositories in each allowed
organization. Personal owners require explicit repository lists. This is global
CAO scope, not a campaign's current rollout percentage. The operator credential
must see the full intended inventory; hidden repositories cannot be inferred.

`--org` configures organization variables with **visibility all**, including
future repositories. Existing selected/private visibility is deliberately
changed to all. Repository overrides are preserved and take precedence.
An organization default therefore cannot guarantee participation or disable
a repository that has its own nonempty endpoint.

`--enterprise` enumerates organizations through GitHub GraphQL, then configures
each organization's variables. There is no enterprise Actions-variable scope.
Enterprise read access and each organization's Variables write permission are
separate requirements.

Discovery and variable-read preflight finish before the first mutation.
Rollouts are sorted, deduplicated, limited to 1000 targets, and sequential.
Writes are not atomic across variables or targets. On failure, completed
targets are reported, the command exits nonzero, and no automatic retry or
rollback occurs; inspect the failed target before retrying.

## 5. Monitor, disable, and recover

Successful decisions also appear immediately in canonical `runs` as
`status: admitted`. Blocked attempts do not create rows. These sparse records
show that the gate approved an invocation, **not** that an agent started or
completed, and contain no invented usage, conclusion or execution timestamps.
Normal collection replaces them with authoritative run evidence; a snapshot
that has not observed a pending run yet does not delete it. Retried approvals
do not duplicate rows or overwrite collected state.

The same scope selectors work for status and disable:

```sh
gh cao admission status --all-repos --json
gh cao admission status --org acme --json
gh cao admission status --enterprise company --json

gh cao admission disable --repo acme/project --dry-run
gh cao admission disable --repo acme/project
gh cao admission status --repo acme/project --json

# Restore the endpoint from the current control repository's setup.
gh cao setup admission --repo acme/project
```

Repository disable writes an **empty URL override**, not a deletion. Deleting
the override could reactivate an inherited organization endpoint. Other credit
controls remain in force when admission is disabled. Organization disable sets
an empty organization default but cannot override a repository's own endpoint.

Organization status reports defaults, not every repository's effective state
or variable visibility. For a canary, use repository status to see the effective
settings and their sources.

| Symptom | Operator action |
| --- | --- |
| Gate skipped | Check effective URL and whether the compiled workflow contains admission support. |
| Activation blocked | Check protected enrollment, exact workflow SHA/subject, original actor ID and budget. Public denial intentionally has no reason. |
| Authority unavailable | Inspect issuer key access, PostgreSQL capacity, protected snapshot readability and accounting freshness. Preserve `block` for strict deployments. |
| Changed workflow denied | Review and publish enrollment for its new signed revision; an unchanged body hash is not authorization. |
| Partial bulk failure | Inspect completed and failed targets with `status`, repair permissions, then rerun deliberately. |
| Need to pause a workflow | Use normal workflow/control-policy disable procedures; cooperative admission is not an emergency-stop authority. |

Admission has no financial side effects. Concurrent starts can overshoot a
threshold; hard caps belong at the controlled backend. PostgreSQL preserves the
first successful response across retries, with a fixed five-minute authorization
window and permanent expiry/revocation tombstones. Do not clear those records
or reset the namespace to recover capacity while invocations may still be
eligible. See the [retention contract](https://github.com/githubnext/gh-cao/blob/main/server/ADMISSION.md#replay-expiry-and-revocation).

## What has been simulated

The automated operator journey uses isolated GitHub-variable fixtures to
exercise preview, canary setup, default URL reuse, fleet rollout, status,
disable and recovery. Separate server tests use synthetic signed OIDC and
accounting fixtures, with real PostgreSQL for durable replay. These checks
do not substitute for the live canary above, actual GitHub permission grants,
or deploying a trusted accounting publisher.
