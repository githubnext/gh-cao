---
title: Central Agentic Ops Hosted CLI Actions Specification
description: Normative requirements for executing dashboard CLI actions in the hosted CAO server with a GitHub App and single-use installation tokens.
version: 1.1.0
status: Working Draft
editors:
  - GitHub Next
---

# Central Agentic Ops Hosted CLI Actions Specification

**Version:** 1.1.0
**Status:** Working Draft
**Latest Version:** https://github.com/githubnext/gh-cao/blob/main/specs/server-hosted-cli.md
**Editors:** GitHub Next

## Abstract

This specification defines the optional hosted CLI actions mode of the CAO
dashboard server. Authorized administrators submit dashboard-declared CLI
actions. The server queues the actions, and a backend executor runs each one
with a short-lived, repository-scoped GitHub App installation token that is
revoked as soon as the command finishes. The specification covers enablement,
administrator step-up authentication, admission, the token broker, execution
isolation, and the required formal model.

## Status of This Document

This document is a Working Draft. It is the canonical normative contract for
hosted CLI actions and administrator step-up authentication. `server/README.md`
and `server/SECURITY.md` are explanatory and MUST NOT override this
specification.

## 1. Status and conformance

The key words MUST, MUST NOT, REQUIRED, SHALL, SHALL NOT, SHOULD, SHOULD NOT,
RECOMMENDED, MAY, and OPTIONAL are to be interpreted as described in RFC 2119.

A conforming implementation satisfies every MUST in this document. The
loopback-only local development profile is out of scope. Its CLI queue runs
on the operator's workstation with the operator's own credentials.

## 2. Enablement

1. Hosted CLI actions MUST be disabled by default and MUST be treated as an
   experimental server feature.
2. `serve-hosted` MUST admit CLI actions only when started with both
   `--experimental` and `--hosted-cli-actions`. Without either flag, the server
   MUST NOT register the `/__cli_commands` routes. The local SQLite debugging
   mode is subject to the same experimental gate.
3. Enablement MUST fail at startup unless GitHub OAuth and the PostgreSQL
   operational store are configured. The mode MUST NOT fall back to the
   memory or Redis operational backends.
4. The accepted actions MUST come from the dashboard document named by
   `--cli-actions-catalog`. A request MUST NOT name an arbitrary command.
5. A separate `execute-cli` process MUST drain the queue. The web server
   process MUST NOT hold the App private key.
6. For local debugging only, `serve-hosted --experimental
   --hosted-cli-actions --hosted-cli-sqlite PATH` and `execute-cli --sqlite
   PATH` MAY replace the PostgreSQL queue and audit
   table with one SQLite file shared by both processes on one host.
   `--hosted-cli-sqlite` MUST be rejected unless both `--experimental` and
   `--hosted-cli-actions` are present and MUST fail at startup unless `--listen`
   is a loopback address. The file
   MUST be created with mode `0600`, and the path MUST NOT carry URI
   parameters. The SQLite store MUST implement the same queue and audit
   contracts (§7 items 1–2), with these differences: one global namespace,
   timestamps from the host clock, and triggers that anyone with write access
   to the file can drop. It MUST NOT be used in production.
   `database bootstrap --store operational --sqlite PATH` MUST explicitly
   provision that file before runtime use. Runtime and evidence readers MUST
   NOT create missing state, execute schema DDL, or implicitly retain audit
   history. `database retain-audit --store operational --sqlite PATH` MUST
   perform only a bounded expired-prefix audit retention batch. SQLite
   `audit deliver` MUST use a read-only connection and deferred verification
   transaction; `audit verify` MUST remain writable to record its outcome.
   SQLite MUST NOT be presented as enforcing PostgreSQL role separation or
   as an alternative canonical dashboard store.

## 3. Administrator step-up

Every administrator operation MUST require a fresh, assured interactive
GitHub OAuth authentication. This covers `/mcp`, collection controls, quota
controls, operations, and all `/__cli_commands` routes.

1. **Freshness.** The session MUST record `AuthenticatedAt` only when a full
   interactive authorization-code flow completes. Token refresh and session
   revalidation MUST NOT update it. A session is fresh while
   `now - AuthenticatedAt <= CAO_ADMIN_REAUTH_MAX_AGE`. The default maximum
   age is 15 minutes, and the configured value MUST be between 1 minute and
   1 hour. Startup MUST fail on a malformed or out-of-range value.
   Timestamps in the future MUST be rejected.
2. **Assurance.** At the interactive callback, the session MUST record an
   assurance only from verified evidence:
   - `2fa` only when GitHub's `/user` response explicitly reports
     `two_factor_authentication: true`. A missing or false field MUST NOT
     count as two-factor evidence.
   - `sso:<org>` only when the organization is listed in
     `CAO_GITHUB_SSO_ORGANIZATIONS` and an organization membership read
     succeeds with the fresh token. The operator attests that the listed
     organizations enforce SAML SSO for OAuth access.
3. **Denial.** When a listed administrator lacks a fresh, assured session,
   the server MUST respond `401` with
   `WWW-Authenticate: CAO-Step-Up realm="admin", max_age=<seconds>` and a JSON
   body containing `code: "admin_step_up_required"` and
   `reauthenticate: "/auth/login"`. Every other principal MUST receive `403`.
   The server MUST NOT reveal step-up state to non-administrators.
4. `GET /api/auth/session` SHOULD expose `adminAssuranceExpiresAt` so the
   dashboard can prompt for re-authentication before denial.

GitHub OAuth cannot force password or two-factor re-entry. Freshness therefore
means a recently completed authorization flow, and assurance depends on what
GitHub discloses or on the operator's SSO attestation.

### 3.1. Provider permission contract

1. Ordinary hosted classic OAuth App authorization MUST request `read:org read:user`
   (the explicitly local exception is defined in section 3.2).
   GitHub's [authenticated-user endpoint](https://docs.github.com/en/rest/users/users#get-the-authenticated-user)
   requires `read:user` or the broader `user` scope to disclose private user
   fields, including `two_factor_authentication`. The server MUST NOT
   request the broader `user` scope when `read:user` suffices. Requested
   scopes MUST NOT themselves count as assurance; missing or false factor
   evidence MUST continue to fail closed.
2. GitHub App clients (`Iv...`) MUST retain `offline_access`, without adding
   classic OAuth user or repository scopes. This opts into expiring user
   tokens and refresh tokens on GitHub.com or GHES 3.23 and later; it does
   not grant user, organization, or repository permissions. For ordinary hosted
   membership checks, operators MUST configure organization **Members: read**
   and approved installations. If GitHub does not disclose 2FA, administrators MUST
   satisfy the attested SSO path in section 3 or be denied.
3. Operators MUST require existing users to complete a new authorization
   and consent when required permissions change. Refresh and revalidation
   MUST NOT be treated as scope upgrades or new authentication assurance.
   Adding `read:user` or using `prompt=select_account` MUST NOT be described
   as proof of a freshly performed MFA challenge.

### 3.2. Single-user local development

For local OAuth development without organization access, the operator MAY
set `CAO_GITHUB_LOCAL_USER` to one exact GitHub login. This option MUST NOT
be inferred from missing organization policy or enabled by request data.
When set, classic OAuth login MUST request `read:user` without `read:org`,
and the provider's authenticated user MUST match the configured login and
include a valid numeric account ID. Login, refresh, and revalidation MUST
NOT query organization or team membership. GitHub App clients MUST retain
`offline_access`.

Startup MUST reject the mode unless the server owns a process listener on
`localhost` or IPv4 loopback with direct TLS, one matching allowed host,
and an exact `https://<listen-address>/auth/callback` redirect without
credentials, query, or fragment. Proxy trust, external/platform listeners,
CORS, non-loopback listeners, and simultaneous organization/team/SSO
authorization MUST be rejected. Requests MUST have a loopback peer,
actual TLS, and the configured origin authority. Operators MUST NOT expose
this development listener through a tunnel or proxy.

Development sessions MUST be bound to their local authorization mode and
configured developer, and MUST NOT be reusable in ordinary hosted mode or
for another developer. Switching modes requires new authentication. Secure
session cookies, CSRF, administrator allowlisting, fresh OAuth authentication
with 2FA-enabled evidence, and live target writer probes MUST remain
unchanged. This exception grants no SSO assurance. Missing factor disclosure
MUST still deny administrator operations. Explicit classic administrator
consent MAY add `repo`, but MUST NOT add `read:org`. Disabling the option
MUST restore ordinary hosted organization/team authorization without reusing
local sessions.

## 4. Admission

`POST /__cli_commands` MUST perform these checks in order and reject the
request on the first failure:

1. Administrator step-up as defined in section 3, with a positive numeric
   GitHub account ID from the authenticated server-side OAuth session.
   Sessions without a verified account ID MUST sign in again before admission.
2. Resolve the request against the declared catalog action, then plan the
   command. Planning MUST bind the command to exactly one target repository
   and reject commands outside the supported command set.
3. Use the requester's own OAuth token to confirm `admin`, `maintain`, or
   `push` permission on the target repository.
4. Enqueue the command with its `Actor` attribution
   login, immutable `ActorID`, and `Repository`, using an optional idempotency
   key. Request data MUST NOT supply or override that identity.

List, detail, output, cancel, and retry routes MUST require the same
step-up. Retry MUST create a new admission subject to the same checks.
It MUST record the currently authorized requester without changing the
original command's actor login or account ID. The action, arguments, input, and
target MUST remain unchanged; idempotency keys MUST remain bound to both the
requesting actor login and numeric account ID.
Cancel MUST confirm, with the requester's own OAuth token, write access to
the command's target repository before changing the command.

Mutation request bodies MUST contain exactly one JSON document with an
`application/json` media type and MUST NOT exceed 1 MiB, including trailing
whitespace. Unknown fields and trailing non-whitespace content MUST be rejected.

The backend MUST validate the requester's write permission on the target
repository before it issues any work: before a command is queued, retried,
or canceled, before a work-scoped token is pre-minted or acquired, and before
a subprocess starts.

### 4.1. Requester repository access

Ordinary hosted classic OAuth App login MUST request only `read:org read:user`,
including on hosted-CLI-enabled deployments and for administrators. Only
the explicit `/auth/login?hosted_cli=1` administrator reauthentication and
consent flow MAY additionally request `repo` for private-target metadata.
The local exception in section 3.2 MUST omit `read:org` from both flows.
The server MUST require hosted CLI enablement and an existing authenticated
server-side session whose login is currently in `CAO_GITHUB_ADMIN_USERS`
and whose numeric GitHub account ID is known before initiating that flow.
Anonymous users, non-administrator viewers, disabled deployments, and
client query flags without verified session authority MUST NOT obtain a
repository-scope authorization redirect.

Consent state MUST bind the initiating session, numeric GitHub account ID,
login, and ten-minute expiry in a signed, HttpOnly cookie. Only opaque
random state MAY be sent to GitHub. At callback the server MUST require the
original session, matching account ID and login, current administrator
allowlisting, enabled hosted CLI, authorized membership (or the exact local
developer in section 3.2), and fresh factor evidence. A different account,
including another allowlisted administrator,
MUST be rejected. Altering or stripping the binding, replaying it after
expiry, or treating the bound payload as ordinary provider state MUST NOT
bypass these checks. Credentials issued to rejected callbacks MUST be
revoked or durably queued for revocation.

Operators MUST disclose that GitHub's classic `repo` scope grants broad
read/write access to accessible public and private repositories, is not
metadata-only or repository-selective, and is requested only for explicitly
consenting administrators. Existing sessions without stored account IDs
require ordinary sign-in before consent. Private-target probes MUST still
fail closed without actual token access; requested consent alone MUST NOT
establish write authority. Disabling the feature does not revoke previously
granted GitHub scopes.

A GitHub App used for browser sign-in SHOULD be preferred for
selected-repository access without a broad classic OAuth grant. Its
installation MUST cover the target, and its configured permissions MUST
allow repository metadata reads. User tokens are limited by both the user's
own access and the App's granted permissions and installation access.
The live response MUST still explicitly establish the user's `admin`,
`maintain`, or `push` permission; accessible metadata or an App installation
alone MUST NOT authorize the operation. Missing access, permission fields,
or organization approval MUST fail closed. The backend MUST NOT fall back
to the executor's App-only credential for requester authorization.

### 4.2. Queued identity continuity and upgrades

The queued `ActorID` MUST survive storage, claims, prefetch, and execution
unchanged. The admitted login remains attribution and the lookup coordinate,
not proof of account identity. If a provider resolves that login to a renamed
account, only a permission response identifying the original numeric ID MAY
authorize execution. An unavailable login, missing ID, different account, or
revoked writer permission MUST NOT authorize execution. No provider rename or
login-reassignment behavior is assumed by this contract.

Legacy queued records without a positive `ActorID` MUST remain readable and
MUST NOT acquire work tokens or start subprocesses. The executor MUST record
an explicit failed result with an identity/re-admission explanation and settle
the unstarted execution claim normally. It MUST NOT delete accepted work,
backfill an ID from the current owner of the login, or automatically retry.
An administrator MAY explicitly retry the failed command through fresh
admission, producing a new command with the currently verified identity and
preserving the original record and retry ancestry. Ordinary terminal retention
still applies.

The invocation JSON adds optional `actorId` without changing SQL columns,
namespace/schema versions, or HTTP request/response shapes. Missing IDs decode
as unknown, never as authorization. Operators MUST pause admissions, stop and
reconcile old executors, and upgrade every web/executor replica before resuming;
mixed versions do not enforce this contract because old executors ignore the
ID. Retained legacy pending commands require explicit operator review and
re-admission, not a production data backfill. This change authorizes no
deployment, migration, or rollout by itself.

## 5. Token broker

1. The executor MUST authenticate as the hosted CLI GitHub App, configured
   with `CAO_HOSTED_CLI_APP_ID` and `CAO_HOSTED_CLI_PRIVATE_KEY` or its
   `_FILE` or `_BASE64` variant. The App SHOULD be created with
   native Go `gh cao setup auth hosted-cli`, which requests only actions, contents,
   pull requests, and workflows write, plus metadata read.
   Operators MUST store the App private key in the executor deployment's
   managed secret store and expose it only to `execute-cli`, preferably through
   a secret-manager-mounted file. The setup command's mode-`0600` local output
   is temporary bootstrap material, not durable secret storage; operators
   MUST import it into the vault and remove the local copy. The key MUST NOT
   be configured on the `serve-hosted` process.
   Setup MUST preserve explicit same-owner repository scope, browser approval,
   and selected-repository verification without changing rollout policy or
   enabling hosted execution. Existing key files MUST NOT be overwritten.
   The retired Node `setup-auth hosted-cli` profile MUST fail before side effects.
2. Each command MUST use an installation token minted for exactly its target
   repository, with the narrowest permissions its plan requires.
3. The broker MUST verify the grant GitHub returns. If the response omits the
   granted permissions or repositories, grants a permission that was not
   requested or at a higher level, or covers different repositories, the
   broker MUST revoke the token immediately and fail the mint.
4. A token MUST be used by at most one command. After the command reaches a
   terminal state, the token MUST be revoked and MUST NOT return to the pool.
5. If revocation fails, the broker MUST retry until the token is revoked or
   expires.
6. The broker MAY pre-mint unused tokens for queued backlog. The pool MUST be
   bounded in total (`--max-pooled-tokens`, default 8, 0 disables pooling),
   per scope (default 2), and by idle lifetime (default 2 minutes). Idle
   tokens MUST be revoked when they expire from the pool and at shutdown.
   Disabled or saturated pools MUST skip speculative requester verification
   token minting; actual command execution MUST still revalidate access.
7. The broker MUST discard a pooled token that would expire before the
   command deadline plus one minute.
8. Concurrent token minting MUST be bounded (default 4).
9. Operators MUST rotate the App private key using a two-key rollover: create
   and deploy the new key while the old key remains active, restart every
   executor, verify token minting with the new key, and only then revoke the old
   key. The executor loads the configured key at startup; changing its
   environment or key file MUST NOT be assumed to update a running process.
   Because shutdown cancels in-flight commands, operators MUST pause admissions
   and wait for queued and running commands to finish before restarting the
   executor for rotation.

The optional offline `cao-dashboard app-key-check` diagnostic validates local
RSA key material, an expected public-key fingerprint, and a distinct replacement
fingerprint. It does not establish GitHub registration, remote revocation,
installation-token invalidation, or the key loaded by a running executor.
The [custody and rollover runbook](../server/SECURITY.md#app-key-custody-and-rollover-preflight)
defines its output, limitations, required operational evidence, and the current
managed-signing integration blocker. Existing PEM signing remains the supported
runtime path; managed secret storage MUST NOT be represented as managed signing.

## 6. Execution

1. In production, all web and executor replicas MUST use the same durable
   PostgreSQL operational namespace. Admission and queue state MUST survive process
   restart. Web replicas MAY admit requests concurrently; executor replicas
   MUST coordinate through one durable queue lease, and only its current holder
   may claim work. SQLite is limited to the single-host debugging profile.
2. The queue MUST serialize lease acquisition, renewal, release, and command
   claims transactionally. Each claim MUST atomically persist its running state
   and execution claim before the command starts. The execution claim MUST
   prevent another replica from running the same command, and active claims
   MUST count against the configured concurrency limit.
3. The executor MUST hold the single queue lease. Additional replicas stand by.
   Lease ownership MUST be fenced so a stale executor cannot write output,
   finish, or settle work after another replica acquires a newer lease.
4. Before running, the executor MUST re-plan the command, require an `Actor`,
   a positive `ActorID`, and a matching `Repository`, and recheck that the
   admitted account still has `admin` or `write` collaborator permission on the
   target repository. The permission response's numeric user ID MUST equal
   the queued `ActorID`; a login match alone MUST NOT authorize work. Missing
   or mismatched provider identity MUST fail closed. The recheck MUST use a
   separate installation token scoped to the target repository with only
   `metadata: read`, which is revoked as soon as the check returns. No
   work-scoped token may be minted, taken from the pool, or pre-minted for a
   command until its requester passes this check. If the recheck fails, the
   command MUST fail without running and without a work token.
5. The executor does not recheck administrator step-up at run time. Admission
   is the authorization point. The live permission recheck and the command
   deadline (default 15 minutes) bound execution. The deadline MUST be stored
   durably with the claim and MUST apply across executor restarts.
6. Each command MUST run in a new workspace with mode `0700`, which is removed
   only after workload termination is confirmed. An unconfirmed workload's
   workspace MUST remain intact for operator reconciliation. It MUST execute in the official `ghcr.io/github/gh-aw` image
   pinned by OCI SHA-256 digest (default
   `ghcr.io/github/gh-aw@sha256:7ff77dc03182d4edcebc902fa94b0239eb9cde65ea212509aef700bcab354090`),
   or in an operator-approved digest-pinned mirror. An unpinned image reference
   MUST fail startup. Except for the explicit local-development profiles in
   section 6a, the executor MUST use the local Docker `default` context;
   remote contexts MUST NOT be used because the command's workspace is a
   host-local bind mount.
7. The Go planner MUST accept only fixed `gh` and `gh-aw` container entrypoints.
   It MUST NOT execute repository shell scripts, arbitrary shell commands, or control-repository
   scripts. A fixed in-container bootstrap MUST initialize an empty private Git
   repository before invoking the planned entrypoint: the pinned `gh-aw update`
   and `upgrade --repo` implementations require a Git root before cloning their
   target. The bootstrap MUST disable Git templates and pass the planned
   program and arguments as positional parameters to the fixed entrypoint, never interpolate
   them into shell source. This bootstrap grants no host execution or
   repository-discovery authority. A campaign-package or standalone-workflow
   source update MUST be translated to a native `gh-aw update` command scoped to
   the configured control repository and the exact retained GitHub source
   coordinate selected from canonical maintenance evidence. The executor MUST
   NOT discover, substitute, or widen that source coordinate. The container
   image reference MUST be recorded in the audit
   event for command start.
8. Linux-image profiles MUST use a read-only root filesystem, drop all Linux
   capabilities, enable `no-new-privileges`, and apply process and memory
   limits. The per-command workspace MUST be its only writable host bind mount;
   `/tmp` MAY be a bounded tmpfs. The executor MUST NOT mount the host home,
   Docker socket, App key, database credentials, or other server files, except
   the reviewed relay and private proxy socket defined in section 6b.
9. The subprocess environment MUST be minimal: the single-use `GH_TOKEN`, an
   empty system git configuration, the `gh` credential helper, and the App bot
   identity. For Linux-image profiles, the executor MUST pass it through a temporary mode-`0600`
   environment file outside the mounted workspace, remove that file after
   container creation. Windows Sandbox MUST use the protected input mapping
   specified in section 6a instead. No profile may place token values in command arguments.
10. The executor MUST attempt bounded forced container removal on cancellation,
   deadline, or loss of the lease. Command output MUST redact the token. Logs MUST NOT
   contain tokens, private keys, or OAuth credentials.
11. Before Docker creation, the executor MUST durably register an immutable
   random container name and the originating Docker daemon identity under the
   current queue lease fence. Missing execution-storage support, failed
   registration, or a stale lease MUST prevent container creation.
   Docker CLI exit, including successful start or output collection, MUST NOT be treated
   as workload termination. The executor MUST inspect the retained container
   state for a confirmed workload exit code; automatic container removal MUST
   NOT discard that evidence. Successful forced removal or a successful empty
   exact-name inventory on the original daemon can confirm termination but
   MUST NOT fabricate a workload exit code. An unavailable daemon, failed
   inspection, or failed removal is not evidence of absence.
   A terminal result MUST NOT release its concurrency claim until container
   exit has been confirmed and settlement is durably stored. Unconfirmed
   termination MUST retain the claim and container identity, including after
   startup, output transport, cancellation, or deadline failures. If a lease expires
   while a command is still claimed or running, the replacement executor MUST
   mark the outcome interrupted, block further claims, and require explicit
   operator recovery acknowledgement after confirming the old executor and its
   outstanding Docker requests have stopped. The replacement MUST additionally
   reconcile every recorded unsettled container against its original daemon
   before clearing the block; a missing container on a different daemon is not
   proof. Failed reconciliation MUST leave claims and the block unchanged.
   Workspaces retained after a crash or unconfirmed exit require operator
   cleanup after reconciliation. It MUST NOT automatically retry the interrupted command: external
   side effects may have completed before the crash, so the system guarantees
   at-most-once dispatch per command ID, not exactly-once external effects. A retry MUST use a
   new command ID and explicit admission.
12. The container runner MUST delegate runtime operations through
   `ContainerLifecycle`: stable runtime identity, create, start, inspect, stop,
   and output collection. Registration MUST precede create. Start MUST NOT
   wait for workload completion; output collection MUST replay from startup
   and then follow, without treating transport closure as an exit result.
   Inspect MUST distinguish confirmed termination from an unknown state and
   MAY report termination without an exit code; a missing or invalid code MUST
   NOT become a successful workload result. Stop MUST confirm termination or
   absence, not merely submit a cancellation request. Cleanup MUST use a
   cancellation-detached, bounded context. Recovery through `RecoverableRunner`
   MUST retain the same original-runtime and lease-fencing requirements.
   Docker is the only production adapter and retains the local-default-context
   restriction and existing durable daemon IDs. The native development
   adapters in section 6a require their own backend-scoped runtime identity;
   they do not enable remote Docker or a new production backend. Hosted commands
   are non-interactive; supplied stdin MUST be rejected rather than discarded.
   The lifecycle boundary adds no exactly-once guarantee or cross-command
   deduplication: idempotent update commands may overlap within configured
   capacity. This does not make workflow dispatch idempotent, change retry
   admission, or release capacity occupied by an unconfirmed workload.

## 6a. Native local-development profiles

1. `CAO_HOSTED_CLI_CONTAINER_RUNTIME` MUST default to `docker`; unknown values
   MUST fail startup. Explicit `apple` or `windows-sandbox` selection MUST
   require `execute-cli --sqlite` and the matching host platform. Missing
   native prerequisites MUST fail closed, never select Docker or host execution
   automatically. Admission, live permission rechecks, token scope, audit
   requirements, deadlines, and retained-claim recovery remain unchanged.
2. Apple container MUST use the pinned Linux image in a private Linux VM on
   Apple Silicon. The adapter MUST select the Linux runtime explicitly, mount
   only the private workspace, drop capabilities, enforce no-new-privileges
   inside the guest, and bound VM CPU, memory, and the user's process count.
   The default image is AMD64 and requires Rosetta. Its temporary `/tmp`
   filesystem MUST be bounded; the native CLI does not expose Docker's exact
   tmpfs mount options.
3. Apple runtime identity MUST include its actual service-reported data root,
   local host/user/session identity, and a durable private random identity
   record. Changing or losing that identity MUST block old-runtime recovery.
   The adapter MUST preserve both output streams from startup without relying
   on the native CLI's indefinitely following log reader. The guest MUST write
   an identity-bound, bounded exit receipt. Neither that receipt nor a native
   `stopped` snapshot is sufficient: the corresponding launchd VM service MUST
   also be independently absent before termination is confirmed.
4. Windows Sandbox is an explicit development-only exception to the Linux
   image requirement. It MUST use a locally supplied, SHA-256-pinned archive of
   Windows `gh`, `gh-aw`, and portable Git, including their dependencies.
   The staged archive MUST be verified before VM creation and again in the
   guest. The executor and audit image identity MUST identify the Windows
   archive as `windows-bundle@sha256:<64 lowercase hex digits>`,
   not pretend it is the Linux OCI image. An OCI-image override MUST be rejected
   for this profile, and missing tools MUST NOT fall back to host binaries.
5. Windows Sandbox MUST enforce its single-instance limit across local
   executor namespaces and processes, not just a queue's concurrency setting.
   Its SQLite queue concurrency MUST be one and MUST match between admission
   and execution; existing queue configuration MUST NOT be silently rewritten.
   An unrelated or user-started Sandbox MUST cause admission to the native
   runtime to fail; it MUST NOT be attached to, reused, or stopped. Ownership
   MUST be durably recorded before launch and retained while termination is
   uncertain. Recovery MUST act only on the recorded owned VM.
   Until a Sandbox-to-host-VM identity mapping is verified, this profile MUST
   reject any pre-existing HCS-managed VM and require empty host-compute VM
   inventory after exact-owned-ID shutdown. It MUST NOT stop foreign VMs.
   Missing or malformed host-compute evidence MUST fail closed; current-user
   CLI inventory or absence of a GUI process is insufficient.
   Global HCS enumeration MUST require elevated administrator or effective
   Hyper-V Administrators privileges. An indeterminate `starting` reservation
   MUST remain fenced even when both inventories are empty; the current
   operator-acknowledged recovery path MUST NOT clear it.
6. Windows configuration MUST disable clipboard, audio, video, printer, and
   GPU sharing, enable the protected client, set an explicit memory limit,
   and map only private command inputs read-only and the command workspace
   writable. GitHub network access is permitted for the scoped command.
   A fixed guest bootstrap MUST read arguments and credentials from protected
   files, not interpolate them into scripts or launcher arguments. Guest
   stdout/stderr and exit receipts MUST be read with bounded memory and without
   following guest-controlled links outside the private workspace.
   Windows workspace privacy MUST be validated from ownership, DACLs, and
   reparse state before staging and immediately before VM creation, including
   existing descendants. The work root MUST have a pre-provisioned protected,
   inheritable private DACL; Unix file modes MUST NOT substitute for this check.
7. Guest receipts establish only the command's reported result, not VM
   termination. An owned VM MUST be independently confirmed stopped or absent
   before its receipt can become a settled result. Cancellation, failed launch,
   lost transport, and failed cleanup MUST preserve the same uncertainty and
   lease-fencing guarantees as the Docker adapter. Native prerequisites are
   operator-managed; the executor MUST NOT install OS features or change
   global runtime configuration automatically.

## 6b. Production Docker egress boundary

1. Production Docker execution MUST require an explicit exact-host policy,
   a static Linux relay built from reviewed server source, and its independently
   approved SHA-256 digest. Missing, malformed, unavailable or mismatched
   configuration MUST fail startup. The verified relay bytes and parsed policy
   MUST be snapshotted; later host-path replacement MUST NOT change a command.
   The relay architecture MUST match the local Linux daemon before creation.
2. Workloads MUST use Docker `--network none`, with no host networking, added
   interface, Docker socket, network-administration capabilities or unrestricted
   bridge fallback. Proxy environment variables alone MUST NOT constitute the
   boundary. Direct IPv4/IPv6, TCP/UDP and DNS bypasses MUST remain unavailable
   even if the workload ignores the proxy.
3. The only egress channel MUST be a per-command private Unix socket exposing
   an HTTPS CONNECT proxy. Its directory MUST be `0700`, its socket `0600`,
   and its relay and socket mount read-only. Any writable workspace alias of
   that directory MUST be hidden from the workload. No host administration API,
   policy mutation interface, App key or database credential may be exposed.
4. The proxy MUST admit only exact approved DNS hosts and TCP port 443. It MUST
   reject non-CONNECT methods, IP authorities, wildcards, alternate ports,
   invalid or unavailable DNS, and any resolution containing an unapproved
   address. It MUST normalize IPv4-mapped IPv6, validate every address, and dial
   a validated numeric address rather than resolving again. Public destinations
   MUST exclude private and special-use addresses. Private GHES exceptions MUST
   bind exact DNS names to exact RFC1918/ULA addresses; metadata, loopback and
   link-local addresses MUST remain denied. Policy MUST NOT broaden repository
   authority. CONNECT destination control MUST NOT be represented as TLS SNI
   inspection, application-content filtering or prevention of exfiltration to
   an approved endpoint.
5. Each proxy MUST bound concurrent connections (32), configured header bytes
   (8 KiB), header timeout (5 seconds), resolution and dialing (10 seconds each),
   tunnel I/O idle periods (30 seconds) and total lifetime (15 minutes or earlier
   command cancellation). Cancellation, stop and executor death MUST close
   existing tunnels and prevent new ones. Proxy loss MUST NOT imply workload
   termination, settle claims, revoke the requirement to reconcile, or delete an
   unconfirmed workload's workspace. Runtime identity, bounded cleanup, token
   lifetime, and original-daemon recovery MUST remain as defined in section 6.
6. Deployment MUST bind relay digest, architecture and source/build provenance
   to the reviewed executor release. Before admitting production work, operators
   MUST validate the real kernel boundary and allowed gh/Git traffic on their
   intended Linux executor. Mock command tests or missing-Docker skips MUST NOT
   count as real-boundary evidence. No global Docker or shared host-network
   modification is required or authorized by this mechanism.
7. SQLite-only Docker development MAY retain its previous bridge behavior only
   when every egress setting is absent. Any supplied setting MUST require the
   complete boundary with no silent fallback. The explicitly selected native
   profiles in section 6a remain development-only.

## 7. Audit trail

1. The authoritative audit trail is the PostgreSQL operational table
   `cao_operational_audit_events`. Each row has a per-namespace, gap-free
   `sequence`, a database-clock `occurred_at`, `event`, `outcome`
   (`success`, `denied`, or `failure`), `actor`, and the exact canonical JSON
   `record` that was hashed. `hash = SHA-256(prev_hash || record)` chains each
   row to its predecessor; the first row uses 32 zero bytes. Appends MUST
   serialize on the namespace row so the chain has no forks.
2. The table MUST be append-only. A trigger MUST reject `UPDATE`, and MUST
   reject `DELETE` except for retention maintenance and namespace removal.
   Retention MUST keep at least 400 days, MUST remove only a contiguous oldest
   prefix in bounded batches, and MUST always keep the newest row so sequences
   never restart. `VerifyAudit` MUST recompute the chain over retained rows and
   report a gap, broken link, hash mismatch, or column that differs from the
   hashed record as `ErrAuditTampered`. The chain detects tampering by anyone
   who cannot also rewrite every later hash; operators who need protection
   against a database superuser MUST also forward committed audit evidence (item 3) to
   write-once storage.
3. Events produced through the audit logger MUST also be mirrored as one JSON line with `"audit":"cao"`,
   `event`, `outcome`, `stored`, and, when stored, `sequence`. The mirror goes
   to standard error, or to the file named by `CAO_AUDIT_LOG`, opened for
   append with mode `0600`, for SIEM forwarding. Waiting for and writing the
   mirror MUST return within a separate 250-millisecond budget. Writers MUST
   share a single in-flight slot across logger copies: a stalled writer MUST
   NOT cause unbounded background writes or indefinitely block audit callers
   or shutdown.
   An arbitrary `io.Writer` cannot be forcibly canceled; at most one write
   may remain blocked until the sink resumes or the process exits. No retry
   queue is retained. A timeout, short write, or writer failure MUST return
   `ErrMirrorUnavailable` and increment `cao_audit.mirror.count` with a
   credential-free outcome. Mirror errors MUST NOT be represented as durable
   append failures or trigger compensation. A hosted mutation whose durable
   commit succeeded MUST report its committed command ID and state even when
   mirror confirmation fails; same-key replay MUST NOT duplicate or cancel it.
   Storage-owned lifecycle events in section 7.1 commit directly to the audit
   stream, not through this best-effort inline mirror. Forwarders MUST consume
   committed audit rows to include these events and recover missed mirror writes.
4. Enabling hosted CLI actions, in either process, MUST fail unless the
   operational store provides the audit table. Privileged steps MUST fail
   closed when their event cannot be stored: a minted token is revoked and not
   used; a command is failed before it runs when `cli_command.started` cannot
   be stored. Admission, retry, and cancellation MUST commit the queue mutation
   and its hash-chained event in the same transaction, in both PostgreSQL and
   SQLite. Hosted enablement MUST require `AuditedCLICommandQueue`; a separate
   enqueue followed by append and compensating cancellation is prohibited.
   Append failure MUST roll back the mutation, idempotency reservation, queue
   revision, and capacity accounting. A failed or uncertain commit returns
   `503` without compensation: either both records committed or neither did.
   The response MUST tell the requester to inspect the queue and reuse the
   original idempotency key, not infer that execution was prevented. Repeating
   a nonempty key with the same invocation and retry ancestry MUST return the
   same retained command, and MUST atomically audit the repeated request; a different
   invocation or retry ancestor MUST conflict. Audit failure on a replay MUST
   NOT cancel a previously committed command. Denial events remain
   best-effort, and a storage failure still appears in the mirror with
   `stored:false` (unconfirmed, not proof that no durable event exists).
5. The web process MUST record `admin.denied` with the reason
   (`step_up_required` or `not_admin`), the method, and the path for every
   refused administrator request. It MUST record `cli_command.admitted`,
   `cli_command.rejected`, `cli_command.cancel_requested`, and
   `cli_command.retried`. Each admitted event carries the actor, action,
   repository, assurance factors, and step-up time.
6. The executor MUST record `token.minted`, `token.revoked`, `token.expired`, and
   `token.revoke_failed` as applicable. It MUST also record `cli_command.started`,
   `cli_command.permission_denied`, `cli_command.result`, and `cli_command.finished`, with the
   actor, repository, installation, granted permissions, container image,
   status, and exit code.
7. Events MUST NOT contain tokens, keys, or OAuth credentials. Token events
   MUST identify the token by `hashed_token`, the Base64 SHA-256 digest that
   GitHub uses in its audit log. Operators can then join CAO events to
   GitHub's audit-log entries for the App's actions.
8. `cao-dashboard audit verify` MUST recompute the chain and print the
   verification as JSON, including `lastSequence` and the head `lastHash`. It
   MUST record `audit.verified` or `audit.verify_failed` with that head, so the
   head also reaches the mirror and OTLP. Each `--anchor SEQUENCE:HASH` MUST
   match the retained row at that sequence. An anchor beyond the chain head
   MUST fail as tampering (rollback), and an anchor older than the retained
   prefix is reported in `anchorsExpired`. Because a database superuser can
   rewrite the whole chain consistently, operators SHOULD run `audit verify`
   on a schedule and keep recent heads in write-once storage outside the
   database, passing them back as anchors.
9. `cao-dashboard audit export` MUST write retained rows as JSON Lines in
   sequence order, optionally filtered by `--after`, `--event`, and `--actor`,
   for evidence requests and SIEM backfill.
10. When OpenTelemetry is configured, audit-logger events MUST also be emitted as
    an OTLP log record named `cao.audit.<event>`, with severity `ERROR` for
    `failure`, `WARN` for `denied`, and `INFO` otherwise. Audit events recorded
    inside a span MUST carry `trace_id` and `span_id` in the stored record.
11. Durable audit writes MUST have a finite five-second budget, including
    connection acquisition, namespace locking, append, and commit. Cleanup
    auditing MAY detach from caller cancellation, but MUST acquire a fresh
    deadline; it MUST NOT use an unbounded `WithoutCancel` context for storage.
    Atomic administrator mutations MUST also honor earlier caller deadlines
    and cancellation. PostgreSQL MUST bound transaction-local lock, statement,
    and idle-in-transaction waits before acquiring the namespace lock. SQLite
    MUST bound its busy wait to one second. Deadline and storage failures MUST
    remain observable and MUST NOT be interpreted as confirmed rollback.
    PostgreSQL MUST preserve any stricter nonzero session timeout rather than
    replacing an operator's ceiling with a longer caller budget; limits MUST
    be transaction-local and MUST NOT change subsequent session defaults.

### 7.1. Durable completion and token cleanup

PostgreSQL and SQLite MUST commit a command's result transition with
`cli_command.result`, and its claim settlement with `cli_command.finished`,
in the same transaction as the corresponding audit append. Events MUST use
the stored actor, repository, queue, command ID, authoritative status and exit
code, not a late executor's requested status. A cancellation remains canceled
even if a later subprocess reports success. A result event with
`execution_claimed:true` is not evidence of workload termination.
Repeated transitions MUST NOT duplicate semantic lifecycle events.

An audit failure MUST roll back the associated result/settlement transition,
including the queue revision, and MUST NOT clear an execution claim. A crash
before result commit leaves the original claim, not permission to rerun work.
Lease recovery MUST atomically record interrupted running commands as failed
results with `recovered:true`; the original exit result is unknown. Recovery
MUST keep claims until explicit operator acknowledgement and the existing
runtime-specific reconciliation succeed. Acknowledgement MUST append the
corresponding finished events atomically with clearing claims. A crash after
result commit but before settlement preserves that known result. Failure to
record acknowledgement MUST leave recovery blocked, including interruptions
before container registration. Production execution MUST require
`AuditedCLICommandLifecycle` and `RevocationTransitions`.

Token cleanup MUST attempt GitHub revocation without waiting for audit
availability. On confirmed API success or observed expiry, the broker MUST
discard its plaintext credential and replace the encrypted journal value with
a credential-free terminal receipt, independently of the audit table. The
receipt MUST preserve the hashed token, installation, expiry, original
observation time, and known actor/repository/command identity. Pooled handout
MUST durably bind its actual requester before use. Terminal receipts and
unresolved encrypted ownership MUST have no automatic expiry; protected
capacity exhaustion MUST backpressure new issuance rather than evict receipts.

The broker MUST attempt to persist the first observed revocation failure per
token in the encrypted journal independently of audit availability. A journal
failure remains explicit and MUST NOT suppress remote cleanup retries.
`token.revoke_failed` denotes
that first observation, not an exact count of network attempts. Its audit row
is deferred until cleanup reaches revocation or expiry (normally within the
token's one-hour lifetime, plus any audit outage); broker failure telemetry and
errors remain immediate. Terminal receipt acknowledgement MUST append that failure and the
terminal event while deleting the receipt in one transaction. A changed or
missing expected version MUST NOT append an event or recreate a journal entry.
Initial ownership writes MUST be insert-only, never overwrite a more advanced
journal version. Retained audit history MUST also deduplicate terminal and
first-failure events by hashed token if an ambiguous initial insert is retried
after another process already completed cleanup. This lookup uses a nonunique
index so pre-existing history is not rewritten; its protection is bounded by
the audit retention horizon, not an infinite exactly-once guarantee.
Restart reconciliation MUST process receipts without minting, handing out, or
revoking tokens again. Audit outages MUST NOT prevent subsequent revocation
attempts for still-live credentials.
Maintenance and shutdown flush journal events outside the broker's cleanup
lock; a stalled append for one receipt MUST NOT delay another token's
revocation. Release hands off a saved terminal receipt without waiting for its
audit append.

`token.expired` denotes observed expiry, not a successful revoke request.
`token.revoked` carries
`confirmation:revoke_api_accepted_or_already_invalid`: GitHub may accept
revocation of an already-invalid credential. Recovery of encrypted ownership
sets `recovered:true`; `observed_at` describes the reconciliation observation,
not an invented historical result. Receipt replay preserves its original
observation rather than manufacturing a new external effect.

GitHub and local storage are not one transaction. Abrupt termination between
remote issuance and initial ownership persistence still relies on token expiry.
A crash or complete storage outage after revocation but before receipt
persistence may require a repeat revoke or expiry observation after restart.
A failed result commit similarly cannot preserve an exit result that never
became durable, and a crash before a failed revoke observation reaches the
journal cannot reconstruct that historical failure. These are explicit uncertainty windows, not exactly-once
external-effect guarantees. If a received token could neither be journaled nor
its terminal receipt persisted, shutdown reports incomplete evidence even when
the credential is no longer usable. No operating-effectiveness or SOC 2
certification claim follows from these implementation guarantees.

### Independent audit delivery

`cao-dashboard audit deliver` MUST remain opt-in and consume only committed
`AuditLog.ListAudit`/`VerifyAudit` results, without a second producer outbox or
application-store writes. Every bounded page MUST be verified against an
independent prior head and its reconstructed ending hash before delivery.
The delivery cursor MUST distinguish the last acknowledged event from the
independently verified high-water mark. Missing/stale/expired anchors, sequence
gaps, rollback, interrupted verification, failed acknowledgements and checkpoint
conflicts MUST fail closed and remain observable through a nonzero exit and
credential-free structured status.

Stream identities MUST be unique per deployment/namespace. Event objects MUST
bind that identity to the sequence and chain hashes, use create-only writes,
and require exact read-back before acknowledgement. A checkpoint MUST advance
only after event and verification evidence delivery, using destination-side
compare-and-swap with a strong version. Retries MUST reload and validate the
independent checkpoint, accept only identical existing objects, and never
overwrite another writer's progress. First enrollment MUST require an explicitly
approved independent anchor and retained history beginning at sequence 1; a
normal missing-checkpoint failure MUST NOT automatically rebaseline.

The [deployment and adapter contract](../server/audit-delivery/README.md) defines
bounded requests, restart behavior, conformance testing and scheduling.
Administrators MUST independently establish destination durability, conditional
write/read consistency, protected checkpoint versions, write-once retention,
least-privilege credentials and alert routing. A local file, SIEM label, or OTLP
emission MUST NOT be represented as a durable independent acknowledgement or
evidence of organizational monitoring/incident-response control operation.

## 7a. Telemetry

Hosted CLI telemetry uses the server's OpenTelemetry setup and MUST NOT
contain tokens, keys, OAuth credentials, command output, or actor logins.
Actors are joined from the audit trail by command ID.

| Signal | Name | Attributes |
|--------|------|------------|
| Span (consumer) | `cao_hostedcli.command.execute` | command ID, repository, status, exit code |
| Span | `cao_hostedcli.token.mint` | installation ID, repository, `hashed_token` |
| Span | `cao_hostedcli.token.revoke` | `hashed_token` |
| Counter | `cao_hostedcli.command.count` | status |
| Histogram (s) | `cao_hostedcli.command.duration` | status |
| Counter | `cao_hostedcli.token.operation.count` | operation (`mint`, `reuse`, `revoke`, `discard`), outcome |
| Gauge | `cao_hostedcli.token.pooled` | none |
| Gauge | `cao_hostedcli.token.pending_revocations` | none |
| UpDownCounter | `cao_hostedcli.lease.held` | none |
| Counter | `cao_audit.event.count` | event, outcome, stored |
| Histogram (s) | `cao_audit.append.duration` | stored |
| Counter | `cao_audit.mirror.count` | outcome (`success`, `timeout`, `error`) |

Span status descriptions MUST be generic. The executor MUST pass only a W3C
`TRACEPARENT` for the command span to the subprocess and MUST NOT forward
baggage, trace state, or exporter configuration.

Operators SHOULD alert when `pending_revocations` stays above zero, when
`cao_audit.event.count{stored="false"}` increases, on any
`cao_audit.mirror.count{outcome="timeout"}` or `outcome="error"`, on any
`audit.verify_failed` event, on spikes in `admin.denied`, and when
`lease.held` is zero while commands are queued.

## 8. Formal model

`specs/tla/HostedCLIActions.tla` models enablement, step-up admission, live
recheck, and the token broker. Its enabled and disabled configurations MUST
pass TLC with no violation of `Safety` or `TerminalTokenInvalidated` before
this specification or the corresponding implementation changes.
`specs/tla/README.md` maps each property to its implementation boundary.
Its abstract `Users` represent immutable accounts, not reusable login strings.
Provider login resolution and missing legacy IDs are covered by the concrete
Go conformance tests, not a modeled provider lifecycle.

`specs/tla/HostedCLIExecution.tla` models durable container identity, lease
fencing, occupied capacity, and explicit recovery separately from admission.
Its configuration MUST pass TLC `Safety` after changes to execution or
settlement. Runtime client exit MUST remain distinct from confirmed
workload termination in this model.

## 9. Conformance tests

- `server/internal/server/admin_step_up_test.go`: freshness, assurance,
  refresh non-extension, and the `401` and `403` responses.
- `server/internal/server/hosted_cli_test.go`: flag gating, admission order,
  and requester permission.
- `server/internal/server/hosted_cli_atomic_test.go`: real SQLite admission,
  retry and cancellation rollback, lost commit acknowledgements, same-key
  recovery, and rejection of non-atomic queues.
- `server/internal/operational/cli_command_audit*_test.go`: both
  store implementations, using independent executor handles and database
  triggers that pause/fail the actual audit insert; concurrent claims cannot
  pass uncommitted admission, rollback preserves queue and hash-chain state,
  repeated requests do not duplicate commands, cancellation retains execution
  claims, process/connection loss cannot leave partial admission, and
  namespace/statement contention cannot block persistence forever.
- `server/internal/hostedcli`: broker single use, revocation and retry, pool
  bounds, executor recheck, isolation, redaction, and audit events without
  secrets.
- `server/internal/githubapp/scoped_token_test.go`,
  `server/internal/hostedcli/requester_identity_test.go`, and
  `server/internal/operational/cli_command_identity_test.go`: same login with
  a different account ID, renamed-original provider responses, revoked writer,
  unknown identity, prefetch/pooled-token handout rechecks, identity round trips
  through both stores, and explicit legacy failure/fresh retry.
- `server/internal/hostedcli/container_runner_test.go` and `recovery_test.go`:
  private Git-root initialization, immutable pre-create identity registration,
  Docker/workload exit distinction, failed cleanup retaining capacity across
  SQLite restart, crash preservation, and original-daemon recovery.
- `server/internal/operational/{postgres,sqlite}/cli_execution_test.go`:
  durable execution identity, immutable registration, and lease-fenced recovery.
- `server/internal/hostedcli/executor_test.go`
  (`TestExecutorVerifiesRequesterBeforeIssuingWorkTokens`,
  `TestExecutorVerifiesWithMetadataTokenBeforeWorkToken`) and
  `server/internal/server/hosted_cli_test.go`: requesters without write access
  never receive a work-scoped or pre-minted token, and cannot cancel.
- `server/internal/githubapp/scoped_token_test.go`: over-broad grants are
  revoked and rejected, and `hashed_token` matches GitHub's format.
- `server/internal/audit`: event shape, store mapping, failure mirroring, and
  append-only `0600` file output.
- `server/internal/operational/postgres/audit_test.go`: gap-free chain, rejected
  `UPDATE`/`DELETE`, tamper detection, retention suffix, and namespace cascade.
- `server/internal/server/hosted_cli_test.go` and
  `server/internal/hostedcli/executor_test.go`: hosted CLI requires the table,
  and unaudited admission, cancel, token mint, and command start fail closed.
- `server/internal/operational/sqlite/store_test.go`: the SQLite debug store
  shares one file between two handles for queue leases, idempotent admission,
  recovery, the audit chain, rejected `UPDATE`/`DELETE`, tamper detection, and
  retention; `hosted_cli_test.go` rejects it on a non-loopback listener.
- `server/internal/operational/sqlite/store_test.go`
  (`TestAuditAnchorsDetectRewriteAndRollback`) and
  `server/cmd/cao-dashboard/audit_test.go`: anchors detect a consistent
  rewrite and a rollback, malformed anchors are rejected, verification is
  itself audited, and export filters and pages rows.
- `server/internal/hostedcli/telemetry_test.go`: command and token spans and
  metrics exist, the subprocess receives a valid `TRACEPARENT`, and no span
  carries a token or actor.
- `internal/cli/setup_hosted_cli_test.go`: native least-privilege App setup,
  browser approval, explicit installation scope, credential storage and failures.
- `server/internal/simulator/setup_hosted_cli_integration_test.go`: installed
  extension without Node, real gh and App JWTs, manifest/browser approval, selected
  repository additions and failures through the isolated GitHub simulator.
- `tests/integration/cao-runtime-installation.test.mjs`: installed runtime rejects
  the retired Node profile while retaining Actions credential inspection.

## 10. Security considerations

The App private key grants every permission the App has on every installed
repository. Operators MUST store it as a secret available only to the
`execute-cli` process. Operators SHOULD limit the installation to the control
repository and the explicitly enrolled targets. A compromised administrator
session is limited to the step-up window and to catalog-declared actions on
repositories the administrator can already write.

The command container runs as the executor's OS user, but receives only its
workspace, scoped token and the private egress relay/socket. The executor process and local Docker daemon remain
trusted: the executor can read the App private key and database credentials,
and a Docker administrator can inspect container configuration and its scoped
token. Operators SHOULD run `execute-cli` under a dedicated, unprivileged user
and restrict Docker daemon access to trusted operators. The gh-aw image MUST be
digest-pinned; the container is defense in depth, not a boundary against a
compromised host kernel or Docker daemon.

Operational hardening that this specification does not yet require, in
priority order:

1. Evaluate a gVisor or microVM boundary against the deployment's threat model;
   it is not a universal SOC 2 requirement. Production Docker egress restriction
   is required by section 6b.
2. Evaluate KMS or HSM App JWT signing against the deployment's threat model,
   rather than treating it as a universal SOC 2 mandate. It is not currently a
   supported runtime integration; see the
   [custody and rollover runbook](../server/SECURITY.md#app-key-custody-and-rollover-preflight).
3. Use separate database roles: the web and executor roles get `INSERT` and
   `SELECT` on the audit table only, and only a maintenance role can run
   retention.
4. Forward the audit mirror or OTLP audit logs to write-once storage or a SIEM,
   and schedule `audit verify` with external anchors.
5. Propagate the admission trace context into the queued command so
   admission and execution share one trace.
6. Expose executor liveness and readiness that reflect store, App, and
   revocation health.

### SOC 2 evidence boundary

The hosted CLI provides technical control mechanisms and evidence sources; it
does not establish SOC 2 compliance or provide an attestation. The organization
and its auditor determine the audited system boundary, applicable criteria,
control owners, and review period. In particular, PostgreSQL hash chaining
alone cannot detect a consistent rewrite by a database superuser. Operators
using these records for audit evidence SHOULD forward them to write-once storage
outside the database and retain external verification anchors, verification
results, access/configuration records, and change-approval evidence for the
applicable review period. Application audit and telemetry cannot prove that
organizational access reviews, approvals, or incident responses were performed.

## 11. References

- `server/SECURITY.md`
- `specs/server-operational-storage.md`
- `specs/tla/README.md`
- [GitHub App improvements for least privilege and management](https://github.blog/changelog/2026-10-13-github-app-improvements-for-least-privilege-and-management/)

## 12. Change log

### Version 1.1.0 (Working Draft)

- Bind hosted admission, durable queue identity, retry/idempotency, and live
  permission checks before work-token issuance to the admitted numeric account
  ID. Preserve login attribution and fail closed for unverifiable legacy work.
- Define coordinated upgrades and explicit legacy re-admission without an SQL
  schema migration or login-derived identity backfill.

### Version 1.0.0 (Working Draft)

- Initial hosted CLI actions, administrator step-up, and token broker
  contract.
- Added granted-scope verification and the audit trail.
- Added the loopback-only SQLite store for local debugging.
- Validate requester write permission before issuing any work, including
  prefetch and cancel, with a metadata-only verification token.
- Added audit verification with external anchors, audit export, OTLP audit
  logs, trace correlation, and hosted CLI telemetry.
