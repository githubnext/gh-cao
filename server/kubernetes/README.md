# Local Kubernetes evaluation

This is an operator-run, **single-node, non-HA local evaluation**, not a new
`gh cao deploy` backend or a production Kubernetes platform. It runs the actual
Go `serve-hosted` application with native GitHub OAuth, direct HTTPS and one
PostgreSQL 16 instance holding both canonical and durable operational tables.
There is no Redis, mock identity, executor, host socket, cloud deployment,
public tunnel or automatic workflow dispatch. An explicitly selected native
collector is an alternative to snapshot ingestion.

By default, the helper selects `--context docker-desktop`; the optional Aspire
workflow below selects a standalone kind context. It owns only `cao-local`
(web/ingestion) and `cao-local-system` (database/owner jobs); it refuses existing
namespaces without its ownership label. Existing roles, policy and other local
repositories are not deployment inputs. The checked-in `policy.json` is the
build-time and mounted runtime policy: one container replica, PostgreSQL, only
`githubnext/gh-cao` evidence and no enabled operations.
Set `CAO_LOCAL_POLICY` to an explicitly reviewed policy path for another authorized
scope, using the same path for build, serve and native activation.

## Prerequisites and preparation

Use Docker Desktop's **kind** Kubernetes provisioner with its
`desktop-control-plane` node, Docker context `desktop-linux`, kubectl, Bash and
OpenSSL supporting `req -addext`, plus Node.js and the root package dependencies
for operator-side policy verification and manifest rendering. Restore those
dependencies through the configured Microsoft-approved npm feed. Neither
application container needs Node.js. A default dynamic StorageClass is required.
Other provisioners deliberately fail image import instead of silently pulling
another image. The importer streams only the two built images into this exact
node's containerd image store; it mounts no host socket or filesystem.

From the repository root:

```sh
bash server/kubernetes/local.sh prepare
bash server/kubernetes/local.sh build
bash server/kubernetes/local.sh database
```

### Optional: kind via Aspire instead of Docker Desktop Kubernetes

If you would rather not enable Docker Desktop's Kubernetes, a small Aspire
AppHost can create a standalone kind cluster named `cao-local` and run the
same `prepare`, `build` and `database` steps against it. Nothing else changes:
the same script, manifests, image checks, TLS, OAuth and readiness rules apply.

You need Docker, [kind](https://kind.sigs.k8s.io/), kubectl, Bash, OpenSSL,
Node.js with the root dependencies, the .NET 10 SDK and the
[Aspire CLI](https://aspire.dev). NuGet restores use the Microsoft feed in
`server/kubernetes/aspire/nuget.config`.

On a Microsoft-managed machine, start the Terrapin Go credential provider and
set the proxy address before starting Aspire:

```sh
export CAO_LOCAL_GO_PROXY=http://host.docker.internal:5000
```

Docker Desktop resolves that address to the host. Use a proxy address reachable
from the Docker builder on other hosts; do not put credentials in the URL.
From the repository root:

```sh
aspire start --apphost server/kubernetes/aspire/apphost.cs
```

Watch `prepare` (on the first run), `build` and `database` in the Aspire
dashboard. In the `database` logs, look for
`CAO database provisioning and explicit grants complete` and a successful
`cao-canonical-maintenance` update. A finished resource can also have failed.

For the remaining steps, point your kubeconfig at the cluster and tell the
script to use it. The export changes your current context; set `KUBECONFIG`
to a separate file first if you want to keep it unchanged.

```sh
kind export kubeconfig --name cao-local
export CAO_LOCAL_KIND_CLUSTER=cao-local
bash server/kubernetes/local.sh ingest /private/path/to/artifact
bash server/kubernetes/local.sh serve /private/path/to/oauth.env
bash server/kubernetes/local.sh forward
```

With `CAO_LOCAL_KIND_CLUSTER` set, every command uses context
`kind-cao-local` and node `cao-local-control-plane`, and images are imported
only when that node runs in your current Docker daemon. The cluster is kept
when Aspire stops so the database survives restarts; remove it explicitly with
`kind delete cluster --name cao-local`, which also deletes its data.

`prepare` generates independent administrator, owner, runtime, session and webhook
secrets plus a 30-day development CA and two server certificates, in the ignored,
owner-only `.tmp/cao-kubernetes-local/`. It refuses to overwrite existing state.
Keep this directory private and preserve it across retries; replacing passwords
does not rotate an initialized database. No credentials enter image layers.
Local Docker npm stages explicitly set and verify the Microsoft-approved feed;
GitHub Actions defaults are unchanged.

The database has a 10Gi PVC, no host port, and requires verified TLS with a
certificate matching `cao-postgres.cao-local-system.svc.cluster.local`.
Bootstrap runs the shared provisioning script as a separate Job, creating
non-login group roles, distinct service logins, existing-state schemas and
explicit grants. The web has no owner/admin secret or bootstrap init-container.
Only the separate owner CronJob maintains canonical partitions, daily at 02:00
UTC (retained history, today and 28 future UTC days). Run it manually after a
long cluster shutdown, before resuming the web:

```sh
kubectl --context docker-desktop -n cao-local-system create job \
  --from=cronjob/cao-canonical-maintenance cao-maintenance-manual
```

For the Aspire kind cluster, use `--context kind-cao-local` instead.

Audit retention/deletion and external evidence delivery are **not enabled**.
No independent audit archive, backup, disaster recovery or production capacity
claim is made. Do not grant the runtime retention authority to reclaim disk.
The fixed retention contract, if separately deployed, is older than 400 days,
at most 512 events per call, under its distinct retention role.

## Real evidence and native login

Acquire an explicitly authorized successful Actions snapshot separately. Retain
its repository, workflow/run/attempt, source SHA, artifact identity/digest and
manifest. For example, after verifying the selected run in GitHub:

```sh
gh run download RUN_ID --repo githubnext/gh-cao \
  --name central-agentic-ops-dashboard --dir /private/path/to/artifact
bash server/kubernetes/local.sh ingest /private/path/to/artifact
```

The helper uploads to a bounded ephemeral ingestion Job using **only runtime
database authority**, not web OAuth or owner credentials. Production `ingest`
validates compacted shard hashes and publishes the canonical revision. Keep the
original manifest intact, including `agent-summary.json` when present; direct
ingestion accepts non-shard entries, unlike the Azure copy helper's allowlist.
Non-shard entries are not all verified by the shard validator: the original
artifact provenance/integrity remains an acquisition responsibility.
Do not treat an expired artifact, failed download or empty bootstrap database
as populated evidence. The helper does not manufacture an empty/synthetic
snapshot or automatically start GitHub workflows.

Register a native GitHub OAuth app with homepage `https://localhost:8443` and
callback **`https://localhost:8443/auth/callback`**. Place its values in a private
env file outside the repository (do not paste secrets into chat):

```text
CAO_GITHUB_CLIENT_ID=your-real-app-client-id
CAO_GITHUB_CLIENT_SECRET=your-real-app-client-secret
CAO_GITHUB_ALLOWED_USERS=your-github-login
CAO_GITHUB_ADMIN_USERS=your-github-login
```

```sh
bash server/kubernetes/local.sh serve /private/path/to/oauth.env
bash server/kubernetes/local.sh forward
```

Trust the generated development CA in your browser/OS **only by explicit
operator action**, then visit **https://localhost:8443**. The helper does not
modify your trust store. On macOS, if you choose to trust this CA, add it to your
login keychain (not the system-wide trust domain):

```sh
security add-trusted-cert -r trustRoot \
  -k "$HOME/Library/Keychains/login.keychain-db" .tmp/cao-kubernetes-local/ca.crt
```

Remove that trust when this evaluation is finished:

```sh
security remove-trusted-cert .tmp/cao-kubernetes-local/ca.crt
```

Verify HTTPS without disabling certificate validation:

```sh
curl --cacert .tmp/cao-kubernetes-local/ca.crt \
  https://localhost:8443/api/readiness
```

Kubelet HTTPS probes do not verify server certificates; this curl check supplies
that independent check. Liveness is TCP-only to avoid restart storms on database
outages. GitHub OAuth, allowlists, CSRF and administrator assurance stay enabled;
an authenticated page does not establish admin step-up acceptance.

Before a real snapshot is ingested, `/api/readiness` deliberately returns **503**
with canonical data unavailable, even when operational storage and OAuth work.
The web pod remains Running but not Ready, and `serve`'s rollout wait times out.
You may use `forward` independently to verify native login; the authenticated
dashboard reports unavailable data rather than presenting an empty snapshot as
real evidence. Do not weaken the readiness probe to mask this state.

## Lifecycle and limitations

The default web profile uses external ingestion and receives no source directory
or collector credentials.
Ingestion, web and maintenance use stable namespaces/capacity inputs. PostgreSQL
state survives web/database pod replacement while the same PVC exists; deleting
the PVC, cluster or Docker Desktop data can destroy it. This is not a backup.
`database.env` selects the canonical dataset with `CAO_DATABASE_NAMESPACE` and
operational state with `CAO_OPERATIONAL_NAMESPACE`; the server rejects the
removed `REDIS_NAMESPACE`. `database` and `serve` rename that key in older local
state, preserving its value, and re-apply the `cao-database` ConfigMap.
The local cluster administrator remains trusted: namespace separation and
disabled ServiceAccount-token mounts are not a complete RBAC/network-security
installation. No enforced NetworkPolicy or egress-isolation claim is made.

`build` records content-addressed image tags and the exact policy SHA-256 in
`.tmp/cao-kubernetes-local/images.env`, and imports those images. The tag suffix
is the local Docker image ID, not a registry manifest digest. Activation checks
the image identity, policy label and policy bytes; a changed policy requires a
rebuild. Re-run `serve` after rebuilding, or `native` for a native installation,
to declaratively roll out the new references. No manual restart is needed for an
image-reference change.

The YAML files in this directory are **input templates**, not supported direct
apply inputs. The helper produces complete JSON manifests under
`.tmp/cao-kubernetes-local/rendered/{snapshot,native}/`, including exact images,
content-bound policy/configuration references and the selected ingestion mode.
Review those files, but activate native collection through the helper so its
preflight and inventory pin cannot be skipped. `serve` preserves the activated
profile recorded on the web Deployment; merely staging a native ConfigMap never
selects it. Existing native installations without that profile annotation must
run `native` once with their original reviewed inputs before using `serve`.

Certificate or Secret updates also require a controlled web restart; TLS files
are loaded at startup, not automatically reloaded. Development certificates expire
after 30 days. Port-forward must remain running, binds only to `127.0.0.1`, and
must be restarted after its selected pod is replaced. Public routing and trusted
public certificates require a separate deployment decision, not a VPN/auth proxy.
Inspect completed/failed Jobs before removing only those named resources.
There is deliberately no cluster-reset or automatic destructive cleanup command.

## Optional native inventory collection

When no retained snapshot exists, a separately authorized GitHub App can collect
real repository/workflow inventory. Native enrollment follows **every repository
in every installation of that App**; policy scope is not an installation-token
allowlist. Verify the complete App installation membership before activation.
The worker inherits the App's granted permissions, even if acquisition only reads.
Use a dedicated App; do not activate an existing broadly installed App accidentally.
OAuth login credentials are separate and do not authorize collection.

For bare monitoring, use a real committed Ops policy with explicit empty
`control-plane.campaigns`, no declared workflows/operations, an exact reviewed
scope and the container/PostgreSQL host settings above. Publishing that policy
requires operator authorization; no campaigns, Actions enablement or workflow
dispatch is necessary. Prepare immutable input from the actual GitHub commit:

```sh
node server/kubernetes/review-inventory.mjs OWNER/OPS_REPO COMMIT_SHA /private/path/inventory.json
```

This operator-only Node helper authenticates through `gh`, retrieves the exact
remote policy revision, validates it with the production CAO policy resolver,
and prints the inventory SHA-256. It refuses to overwrite a file. The runtime
does not require Node, GitHub CLI or local source files.
Create a non-secret env file with exactly these four reviewed settings:
Use canonical lowercase `owner/repository` for the control repository. Inventory
generation and App membership verification normalize GitHub's display casing
without changing the original reviewed policy bytes.

```text
CAO_COLLECT_APP_ID=your-real-numeric-app-id
CAO_COLLECT_CONTROL_REPOSITORY=OWNER/OPS_REPO
CAO_COLLECT_NATIVE_INVENTORY_DIGEST=the-printed-sha256
CAO_COLLECT_INVENTORY_LIMIT=the-reviewed-total-installed-repository-count
```

Build with the same reviewed host policy, then select native collection:

```sh
export CAO_LOCAL_POLICY=/private/checkout/.github/workflows/cao.json
bash server/kubernetes/local.sh build
bash server/kubernetes/local.sh native /private/path/inventory.json \
  /private/path/collector.env /private/path/app.private-key.pem
```

Before any activation writes, the helper checks both namespace owners, the
image/policy binding, reviewed inventory/configuration and the App's complete
installation membership against the exact repository scope. Verification uses
short-lived metadata-only tokens and revokes them, including on scope failure;
the collector itself still inherits the App's granted permissions. Pagination
follows terminal Link headers, including across empty pages, rather than trusting
the aggregate repository count. Missing access, extra repositories and token
cleanup failures block activation.

The provisioning Job validates the digest before database access and pins the
source through runtime PostgreSQL authority. Repeating the same pin is safe;
a different existing configuration fails and requires explicit expected-digest
replacement, not automatic rollover. This helper limits inventory to 900000
bytes for Kubernetes ConfigMap transport; the native contract's larger bound is
unchanged.

Configuration is staged under content-bound names without overwriting active
inputs. Only after a successful pin is the separately named App-key Secret
created and mounted by `cao-collector`. It clears its prior readiness marker on
every container start and performs a new successful backfill before becoming
Ready and running the durable worker. The web switches to credential-free native
admission only after that rollout succeeds, retaining HTTPS, OAuth and its
original readiness requirements. A failed preflight or pin does not switch the
active mode, mount the new key or start the collector; staged ConfigMaps/Jobs
may remain for inspection. Kubernetes multi-resource updates are not an atomic
transaction: a later rollout failure requires inspecting the retained resources.
Snapshot ingestion is blocked by an activated native profile or a nonzero
collector Deployment; native activation refuses an active helper-created snapshot
Job. Do not start out-of-band writers concurrently.
This optional path enables neither Value collection, delivery replay nor workflow
execution. New repository copies have no copied Actions run history: populated
inventory with no runs is not a successful historical-run acquisition claim.
