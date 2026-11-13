# Local Docker Desktop Kubernetes

This is an operator-run, **single-node, non-HA local evaluation**, not a new
`gh cao deploy` backend or a production Kubernetes platform. It runs the actual
Go `serve-hosted` application with native GitHub OAuth, direct HTTPS and one
PostgreSQL 16 instance holding both canonical and durable operational tables.
There is no Redis, mock identity, executor, host socket, cloud deployment,
public tunnel, live collector or automatic workflow dispatch.

All Kubernetes commands select `--context docker-desktop`. The helper owns only
`cao-local` (web/ingestion) and `cao-local-system` (database/owner jobs); it refuses
existing namespaces without its ownership label. Existing roles, policy and
other local repositories are not deployment inputs. The checked-in `policy.json`
is the build-time and mounted runtime policy: one container replica, PostgreSQL,
only `githubnext/gh-cao` evidence and no enabled operations.

## Prerequisites and preparation

Use Docker Desktop's **kind** Kubernetes provisioner with its
`desktop-control-plane` node, Docker context `desktop-linux`, kubectl, Bash and
OpenSSL supporting `req -addext`. A default dynamic StorageClass is required.
Other provisioners deliberately fail image import instead of silently pulling
another image. The importer streams only the two built images into this exact
node's containerd image store; it mounts no host socket or filesystem.

From the repository root:

```sh
bash server/kubernetes/local.sh prepare
bash server/kubernetes/local.sh build
bash server/kubernetes/local.sh database
```

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

The web uses external ingestion and never receives a source directory or collector.
Ingestion, web and maintenance use stable namespaces/capacity inputs. PostgreSQL
state survives web/database pod replacement while the same PVC exists; deleting
the PVC, cluster or Docker Desktop data can destroy it. This is not a backup.
The local cluster administrator remains trusted: namespace separation and
disabled ServiceAccount-token mounts are not a complete RBAC/network-security
installation. No enforced NetworkPolicy or egress-isolation claim is made.

`build` updates local image tags and imports them; restart the web after rebuilding.
Certificate or Secret updates also require a controlled web restart; TLS files
are loaded at startup, not automatically reloaded. Development certificates expire
after 30 days. Port-forward must remain running, binds only to `127.0.0.1`, and
must be restarted after its selected pod is replaced. Public routing and trusted
public certificates require a separate deployment decision, not a VPN/auth proxy.
Inspect completed/failed Jobs before removing only those named resources.
There is deliberately no cluster-reset or automatic destructive cleanup command.
