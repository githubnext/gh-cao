# Server security

For the component inventory, evidence-driven readiness procedure, and operator
decision register, see [SOC 2 readiness assessment](SOC2.md). That assessment
does not attest compliance or authorize remediation.

The Go dashboard server has three security profiles:

- the default local profile used by `cao-dashboard serve`, for one trusted
  operator on loopback; and
- the explicit Azure Functions profile, for remote access through GitHub OAuth,
  server-side sessions, Azure trusted-proxy headers, and Azure Managed Redis; and
- the host-neutral `serve-hosted` profile, with the same GitHub identity boundary
  behind an explicitly trusted HTTPS proxy and any compatible managed Redis.

The hosted single-process memory alternative retains every OAuth, HTTPS,
organization/team, administrator, CSRF, proxy, signature, and quota boundary.
It requires explicit single-process and volatile-state acknowledgements and
cannot run as an admission-only, platform-scaled, or multi-process deployment.
Its sessions and pending OAuth login state become invalid on restart. This
does **not** expire or revoke GitHub-issued tokens. Pending encrypted revocation
retries and acknowledged unfinished webhook work may be lost; complete fresh
GitHub scope reconstruction gates collection admission after restart. Memory
limits fail closed and do not evict active limiter buckets or protected work.
The normative exception is [server operational storage](../specs/server-operational-storage.md).

Every administrator operation in the hosted profiles requires a fresh, assured
interactive GitHub OAuth authentication (administrator step-up). The experimental
`serve-hosted --experimental --hosted-cli-actions` mode, which is off by default, lets
step-up administrators queue dashboard-declared CLI actions. A separate
`execute-cli` process runs them with single-use, repository-scoped GitHub App
installation tokens. See [Hosted CLI actions](#hosted-cli-actions) and the
normative [hosted CLI actions specification](../specs/server-hosted-cli.md).

## Supported security boundary

The supported deployment is:

- one trusted operator;
- one local checkout or worktree;
- an HTTP listener on loopback for debugging, or HTTPS with an
  operator-supplied certificate and key;
- a read-only SQLite snapshot for default local serving, or an explicit
  PostgreSQL profile with access-controlled operational storage;
- dashboard artifacts obtained from a trusted local workflow or download;
- browser access through the capability URL printed by the server.

GitHub authentication, organization authorization, multi-user access, public
hosting, reverse proxies, tunnels, and webhook ingestion are not part of the
local profile. Use `serve-hosted` rather than exposing `serve` through a tunnel
or public reverse proxy.

Default local startup downloads and verifies a deployed snapshot only when the
cache is missing; existing invalid or unreadable caches fail closed without an
automatic repair download. `--download-sqlite` explicitly refreshes the cache
before opening it read-only and does not relax the local security boundary.

## Repository metadata input limits

Local repository-memory manifests default to a 2 MiB limit before JSON decoding,
independently of the existing 400-files-per-campaign, 1 MiB-per-file and 64 MiB
aggregate content limits. The metadata budget leaves room for many campaigns
without treating file-content limits as a bound on manifest size.

The Go marketplace resolver defaults ordinary GitHub JSON responses (commit,
blob and installation-token exchange) to a 512 KiB limit before decoding.
This allows base64 wrapping and metadata around 256 KiB manifests/READMEs
without granting individual packages a multi-megabyte response budget.
Recursive repository-tree discovery defaults to a separate 8 MiB ceiling
because it lists paths across the entire repository, not just one package.
Artwork fields are references, not image downloads through this JSON reader.
Public repository signals retain their separate 64 KiB limit.

These three metadata-read budgets are centralized in
`internal/inputlimits/limits.go` and can be overridden independently:

| Environment variable | Default | Input |
| --- | --- | --- |
| `CAO_REPOSITORY_MEMORY_MANIFEST_MAX_BYTES` | 2097152 (2 MiB) | Local repository-memory manifest |
| `CAO_MARKETPLACE_JSON_MAX_BYTES` | 524288 (512 KiB) | Ordinary marketplace GitHub JSON |
| `CAO_MARKETPLACE_TREE_JSON_MAX_BYTES` | 8388608 (8 MiB) | Recursive marketplace GitHub tree |

Overrides are positive decimal byte counts below the platform integer maximum,
leaving room for the overflow-detection byte. Unset variables use the defaults;
empty, zero, negative, malformed or overflowing values fail explicitly before
the relevant file is opened or HTTP request is issued. Overrides are resolved
when the input is read, not cached at package initialization. Raising a budget
is an operator-controlled capacity tradeoff, not evidence of workload safety.
Existing file-content, package-content and repository-signal limits are unchanged.

Both readers consume at most their limit plus one byte and reject oversized
input, including a valid JSON prefix followed by excess whitespace. The
marketplace checks actual response bytes rather than trusting Content-Length;
complete JSON validation also rejects trailing documents or garbage. Read
errors and request cancellation remain failures. Optional README failures keep
the existing logged, empty-content behavior; required registry responses fail
that registry with credential-free diagnostics.

These are per-input byte bounds, not process-heap, concurrency, filesystem I/O
deadline or deployment capacity guarantees. Decoded structures and subsequent
normalization require additional memory. Deployed workload suitability and
operating effectiveness still require operator evidence.

## Host-neutral hosted profile

Hosted mode rejects local bearer capabilities and requires GitHub OAuth,
explicit organization or team authorization, and an exact trusted-host policy.
Administrative rebuilds additionally require an explicit GitHub login in
`CAO_GITHUB_ADMIN_USERS`.
Hosted rebuilds additionally require durable audit admission/start evidence.
Their `admin.rebuild.*` events retain the initiating actor and independent
rebuild correlation through terminal success, failure or cancellation.
An audit outage before reconciliation prevents the rebuild; a terminal outage
cannot undo an already committed canonical revision and is reported separately
through status `auditState` and audit failure telemetry. JSON mirrors alone do
not satisfy the hosted requirement. Local loopback rebuilds retain the explicit
capability-only, non-durable exception. Operators must independently preserve
committed audit rows and reconcile lifecycle IDs missing terminal evidence after
crashes/outages; see the
[runtime contract](../specs/server-operational-storage.md#administrative-rebuild-evidence).
Mutating browser requests require the session-bound CSRF token. The webhook
route is exempt from browser authentication only because it independently
requires a valid `X-Hub-Signature-256` signature and delivery identity.
Active sessions recheck GitHub membership at most five minutes after the last
successful authorization, independently of access-token refresh. Failed checks
remove session authority and revoke or durably queue its credentials. OAuth
callbacks that cannot establish an authorized, stored session likewise revoke
or durably queue issued credentials; a background worker retries pending
revocations without delaying the login redirect.

OAuth revision streams use the same authorization window: they close no later
than five minutes after the last successful membership authorization, or earlier
when access-token refresh is due. Streams admitted with administrator health
metadata also close at the administrator assurance deadline. Reconnecting runs
ordinary request authentication, including membership revalidation, token
refresh and cookie renewal; none of those renewals occur after stream headers
have been flushed. Expired administrator assurance permits a viewer reconnect
without health metadata, not continued administrator entitlement.

Each OAuth stream reads its encrypted session record once per 20-second
heartbeat, with a five-second timeout bounded by its authorization deadline.
Logout, account switching, removed session authority and session-store errors
close the stream at that check; metadata already admitted can continue for up
to 25 seconds after session invalidation. A membership change not yet reflected
in session authority remains bounded by the ordinary five-minute authorization
window. Checks do not call GitHub or refresh tokens, and revision traffic adds
no session-store reads. The stream context and supported HTTP write deadline
share the fixed authorization deadline, so active traffic cannot extend it.
Cancellation and server drain still unsubscribe immediately. Local bearer
capability streams do not acquire an OAuth lifetime or session-store dependency.
These are application authorization bounds, not an attestation of deployed
proxy buffering, connection lifetimes or delivery of previously written bytes.

API and OAuth abuse is bounded with atomic Redis token buckets shared by all
server replicas. Authenticated buckets use a digest of the GitHub login, while
OAuth login uses a digest of the client IP and valid OAuth callbacks use a digest
of the signed state. Trusted enterprise proxy headers are used only at the
configured boundary: the final `X-Forwarded-For` value takes precedence, RFC
7239 `Forwarded` is supported as a fallback, and malformed final values fall
back to the direct peer rather than an attacker-controlled earlier hop. Query
requests receive the tightest limit because they consume the most server and
Redis work. The limiter fails
closed when Redis is unavailable, and `429` responses communicate the cooldown
with `Retry-After` plus the standard `RateLimit-*` headers. Hosted public
health/readiness probes share a separate per-client-address bucket; local
probes remain exempt. GitHub webhooks are exempt from user quotas but cross the
high-capacity edge bucket before signature validation, bounding invalid request
bodies and HMAC work without disrupting ordinary delivery retries.

A separate client-IP edge bucket runs before session loading and refresh, so
invalid or expired sessions cannot bypass abuse controls by failing
authentication early. At trusted proxy boundaries the server reads forwarded
addresses from the final header value, including address-and-port forms, and
ignores caller-supplied earlier entries. Authenticated and valid OAuth callback
quotas do not coalesce users behind one enterprise egress address. Limiter Redis
operations have a dedicated two-second deadline to prevent a degraded Redis
service from holding request workers for the full server I/O timeout. The
complete normative contract is `specs/server-rate-limiting.md`.

The server is same-origin by default and emits no CORS headers. A reviewed
`control-plane.web.host.cors` policy may list exact origins; wildcards and
plaintext non-loopback origins are rejected. CORS is credential-less: the
server never sends `Access-Control-Allow-Credentials`, so listed origins cannot
read session-authenticated responses or the CSRF token. CORS never relaxes
authentication, CSRF, host validation, or rate limiting. Unauthenticated
subresource requests receive `401` instead of a redirect to GitHub OAuth. The
complete normative contract is `specs/server-cors.md`.

Webhook delivery IDs and projection leases are stored in the deployment Redis
namespace. Failed reconciliation removes its delivery marker so GitHub can
retry. Full rebuilds and webhook reconciliation share a distributed lease;
each Postgres replacement is committed atomically. Redis remains disposable,
and health distinguishes an available service from ready data.

`CAO_REDIS_URL`, OAuth secrets, the webhook secret, and session secrets are
process-only configuration resolved by the deployment's secret manager. They
are never accepted as hosted command-line flags, returned by APIs, or written
to logs. Hosted Redis uses `rediss://` with certificate and hostname
verification by default; only the explicit private-network opt-in
permits plaintext. The core service imports no cloud identity or
secret-management SDK.

## Dashboard access capability

For the distinct, opt-in OAuth development exception, see
[Single-user local OAuth](#single-user-local-oauth). It does not relax remote
hosted authorization.

The server protects every data API except the minimal health probe with a
bearer capability. Static application assets contain no dashboard records and
may be loaded without the capability.

- `serve` generates a cryptographically random access token unless an explicit
  token of at least 32 characters is provided with `--access-token`.
- The server prints an HTTP or explicitly configured HTTPS capability URL
  containing the token.
- Opening that URL writes the token to origin-scoped `localStorage` and removes
  it from the current address with `history.replaceState`, without navigation.
  This preserves access across ordinary page refreshes.
- Browser queries, diagnostics, refreshes, and the streamed revision request
  explicitly send `Authorization: Bearer <token>`.
- The server does not place the bearer capability in a cookie; loopback cookies
  are host-scoped rather than port-scoped and could leak to another local HTTPS
  service.
- Token comparisons use constant-time comparison.
- The unauthenticated health response contains only operational connectivity,
  per-feature guarantees, recovery readiness, and whether PostgreSQL has ready dashboard data.
  Counts and revision details
  require the capability cookie.

Treat the capability URL and browser session as credentials. Do not paste the URL into
issues, logs, screenshots, shell history shared with others, or browser
telemetry. Restart the server to rotate an automatically generated token.

When local MCP is explicitly enabled, `/mcp` uses this same bearer capability
and loopback host restriction. In GitHub Actions it may alternatively use the
server's `GITHUB_TOKEN` and `GITHUB_ACTOR`: clients must provide the token as a
bearer credential and the matching actor in `X-GitHub-Actor`. This pair is
constant-time checked, scoped to `/mcp`, never logged, and used as the
rate-limiting identity. Before accepting this authentication mode, startup
probes the configured `GITHUB_REPOSITORY` with the token and fails closed unless
`actions`, `contents`, `issues`, and `pull-requests` all have read access.
The actor header is an assertion bound to the server-configured value, not an
independently trusted client identity. MCP is disabled by default and is read-only.
When enabled on a hosted server, `/mcp` instead requires a per-request GitHub
Actions bearer token and a signed Actions OIDC token in `X-GitHub-OIDC-Token`.
The server validates the OIDC issuer, signature, audience, expiry, and allowed
repository provenance. It confirms that the bearer is an installation token
with access to that repository, queries its stable repository and owner IDs plus
its default branch, and verifies the signed IDs, ref, and legacy or immutable
subject against that metadata. It then probes read access to Actions, contents,
issues, and pull requests. This identity is scoped to `/mcp`; it
cannot authenticate dashboard JSON APIs or replace hosted browser OAuth
sessions. Keep both tokens out of logs and URLs.
Hosted MCP authentication refusals return a bounded `code` identifying the
rejected stage (`credentials_missing`, `oidc_invalid`, `repository_unavailable`,
`provenance_mismatch`, or `permissions_denied`) and, when tracing is active, a
`traceId` matching the `X-Trace-Id` response header. These diagnostics never
include tokens or upstream error bodies.

## HTTP and TLS protections

### Outbound trust and CA compromise

Build the server and native CLI with Go 1.27.3 or newer. All three Go modules
require this version so that an installed, unpatched Go 1.27 toolchain cannot
satisfy a lower Go 1.26 minimum. Go 1.27.3 is a patched release identified by the
[October 2026 eMudhra incident report](https://emudhra.we-have-a-problem.com/).
Keep build toolchains, operating-system trust stores, and container CA packages
current. Rebuild and redeploy existing binaries and images: changing `go.mod`
does not repair already deployed executables. Browser revocation updates do not
establish the trust behavior of Go, Node.js, `gh`, or other subprocesses; update
those runtimes independently.

Remote PostgreSQL connections require `sslmode=verify-full`, including every
fallback, for both canonical and operational storage. `require` encrypts the
connection but can skip certificate verification; `verify-ca` does not verify
the hostname. Neither is accepted. Use `sslrootcert` to trust an operator-owned
CA without bypassing verification. Unix sockets and loopback retain their local
development exception.

Snapshot download redirects must not downgrade HTTPS to HTTP. Use a trusted
HTTPS origin: a downloaded manifest and its SHA-256 checksums share the same
trust boundary and are not a publisher signature. The native CLI additionally
supports `--manifest-sha256` / `DASHBOARD_MANIFEST_SHA256` for a manifest digest
obtained independently. The server SQLite downloader does not provide this
independent pin; use an independently verified local snapshot for that boundary.

### Listener and response protections

- The listener must be `localhost` or a loopback IP address. Non-loopback bind
  addresses are rejected even when a certificate is configured.
- Requests with a non-loopback `Host` header are rejected, reducing DNS
  rebinding exposure.
- The server never generates certificates. HTTP is the localhost debugging
  default. When certificate and key files are supplied together, TLS 1.2 or
  newer is required.
- The HTTP server configures read-header, read, write, idle, and shutdown
  timeouts.
- API responses use `Cache-Control: no-store`, and the dashboard service worker
  excludes `/api/`.
- Responses set `X-Content-Type-Options`, `X-Frame-Options`,
  `Referrer-Policy`, `Permissions-Policy`, and
  `Cross-Origin-Resource-Policy`.
- Request methods and body sizes are bounded. Static files are constrained to
  the configured built-site directory.

The capability mechanism is local access control, not a replacement for
identity-aware authentication in a remote service.

## Single-user local OAuth

`CAO_GITHUB_LOCAL_USER` explicitly authorizes one GitHub login for OAuth
development without organization access. Only this development mode replaces
membership checks: classic login requests `read:user` without `read:org`,
and GitHub App login keeps `offline_access`. The authenticated account must
match the configured login and disclose a valid numeric ID. No membership
read or SSO attestation is used.

Startup requires a process-owned listener on `localhost` or IPv4 loopback,
direct TLS, one matching loopback allowed host, and a callback exactly at
`https://<listen-address>/auth/callback`. It rejects proxy trust, external or
platform listeners, public/private-network binds, CORS, and simultaneous
organization/team/SSO policy. Each request must also have a loopback peer,
actual TLS, and the configured origin authority. Operators must not expose
the listener through tunnels or proxies.

Local sessions record their authorization mode and configured developer;
they cannot authenticate an ordinary hosted server or a different local
developer, even if the same session store and encryption secret are reused.
Switching modes requires new sign-in. Use separate development namespaces
and secrets anyway, and revoke old GitHub grants when removing access.

This mode does not bypass GitHub authentication, Secure/HttpOnly cookies,
CSRF, administrator allowlisting, fresh assurance, or live target writer
probes. Explicit administrator consent adds classic `repo` only after the
existing server-verified consent checks. Missing 2FA evidence still denies
administrator operations; SSO configuration is rejected rather than silently
ignored. The ordinary bearer-based `serve` mode remains OAuth-free, and
remote hosted profiles still require organization or team authorization.
See the [local setup instructions](README.md#local-oauth-without-organization-access).

## Hosted transport boundary

Hosted mode has no developer override for public transport protections:

- Redis-backed profiles require `rediss://` by default;
- plaintext Redis requires `allow-private-plaintext: true` in the `cao.json`
  Redis module and is then restricted to a private IP or single-label service
  name;
- it requires HTTPS and rejects attempts to disable that policy;
- it binds to loopback by default; without direct TLS, forwarded host/protocol
  headers are allowed only across that local process or pod boundary;
- direct TLS uses the actual TLS transport without requiring forwarded headers,
  unless `CAO_TRUSTED_PROXY_CIDRS` explicitly configures a trusted proxy; and
- a non-loopback bind requires an operator-supplied TLS certificate and key,
  or explicit private `CAO_TRUSTED_PROXY_CIDRS`. Forwarded headers are accepted
  only from a direct peer in those CIDRs and must identify an allow-listed host
  over HTTPS.

Generated bearer capabilities and optional local TLS belong only to the
separate `serve` developer profile. The private plaintext Redis opt-in is for a
deployment-managed private service network, never public or cross-network
Redis.

## Container deployments

The host-neutral image runs as numeric user/group `65532`. The deployment
operator must drop Linux capabilities, enable `no-new-privileges`, use a
read-only root filesystem, and restrict ingress to the configured TLS or
trusted-proxy boundary. Inject secrets at runtime through the host's secret
manager, never through Dockerfile instructions, image labels, or health checks.

Stage the complete trusted dashboard artifact in an unattached volume, verify
its `payload-hashes.json` manifest, and select it atomically before mounting it
read-only. Never populate a live attached artifact volume or treat a placeholder
as deployment data.

Use the private `collector-runtime` target for standalone collection and
backfill roles, with a persistent evidence volume and no public ingress.
Admission-only web replicas must not receive the collection App private key.

Official package scans, hardening checks, SBOMs, and attestations apply to
published packages, not to locally source-built images. Source review, builder
isolation, deployment promotion, and rollback remain operator responsibilities.
After rollback, verify readiness, OAuth authorization, queries, webhook
signatures, and rate limits; never treat operational caches as rollback
authority.

## Upstash profile

Upstash provides causal consistency within one TCP session rather than across
independent connections. Its Redis module therefore uses a serialized session
for projection data and hosted security state. It does not recycle or retry that
connection. Any transport loss permanently fails that client, so health and
readiness fail until the process restarts.

Restart recovery uses a fresh random internal namespace and rebuilds from the
verified artifact. This isolates the process from stale state written through an
earlier TCP session and invalidates all existing CAO sessions. Old namespaces
remain disposable storage until an operator removes them while the application
is stopped. The encrypted pending-revocation queue is the exception: it uses a
stable deployment-scoped prefix so GitHub credentials queued for revocation
remain available to the bounded retry worker after restart.

The mode fails startup unless the endpoint uses `rediss://` on an Upstash host
and the target declares `replicas: 1`. The deployment must independently enforce
that replica count and use a dedicated Upstash database. Server-side collection
and standalone collection roles are unsupported. Declaring one replica does not
turn Upstash into a cross-process coordination service and must not be used to
justify additional replicas.

## Azure Functions profile

> [!WARNING]
> Azure Functions mode is experimental. It defines a security baseline for
> review and staged evaluation, not a blanket production approval. Keep the
> profile disabled for live use until the target tenant has completed security,
> compliance, privacy, network, monitoring, incident-response, and rollback
> reviews.

Azure Functions mode is enabled by using
`NewAzureFunctionsHandlerFromEnv`, which selects the platform-listener,
GitHub-OAuth host capability profile. It does not start its own listener, does
not accept `--access-token`, and does not support PATs. Requests are handled by
the Azure Functions HTTP runtime and the same Go dashboard HTTP handler.

Azure mode fails closed unless `cao.json` selects the `azure-functions` target
and configuration includes:

- `rediss://` Redis transport and a Redis namespace;
- an explicit trusted host allow-list from `CAO_AZURE_ALLOWED_HOSTS`;
- HTTPS forwarded-protocol enforcement;
- GitHub OAuth App or GitHub App client ID, client secret, redirect URL, and a session secret
  of at least 32 characters; and
- at least one allowed GitHub organization or `org/team-slug` value.

The trusted-proxy policy is intentionally separate from local `Host` checks.
Local requests must still use loopback hosts. Azure requests may use forwarded
host/protocol headers only when the forwarded host exactly matches the
configured allow-list and the forwarded protocol is HTTPS.

### GitHub OAuth and authorization

Azure mode implements the GitHub OAuth authorization-code flow. OAuth Apps
request `read:org read:user` for active organization or team membership checks
and disclosure of the private `/user.two_factor_authentication` field.
`read:org` alone cannot disclose that field. Existing grants require a new
interactive authorization with consent to `read:user`; token refresh cannot
add the scope. The broader `user` scope is not requested. GitHub
App client IDs (`Iv...`) instead request `offline_access`, which opts that
authorization into expiring user tokens and refresh tokens; their registration
must grant organization **Members: read** with an approved installation in each
allowed organization. This scope neither adds repository
permissions nor grants offline dashboard authority, and requires GitHub.com or
GHES 3.23 or later. Successful GitHub authentication alone is insufficient: the
server must verify an allowed organization or team membership before creating a
session. Do not add PAT handling to Azure mode; PATs bypass the required
browser login, refresh-token rotation, revocation, and explicit membership
authorization controls.

Ordinary classic OAuth App login never requests `repo`, including on a
hosted-CLI-enabled deployment. Only the explicit
`/auth/login?hosted_cli=1` reauthentication/consent flow may request it:
hosted CLI must be enabled, and an existing server-side session must identify
a configured administrator with a known numeric GitHub account ID.
The signed, HttpOnly state cookie binds the initiating session, account ID,
login, and ten-minute expiry. The callback rechecks the original session,
subject, current administrator allowlist, feature enablement, membership, and
factor evidence. Selecting another account, even another administrator,
fails closed and revokes or queues issued credentials. Consent metadata
cannot be stripped or moved into the provider state to downgrade the flow
to ordinary login. No client query flag grants authority by itself.

`repo` is needed to read private target repository metadata with the
requester's classic token. It grants broad repository read/write access
to the consenting administrator's accessible repositories;
GitHub offers no equivalent classic OAuth scope limited to private metadata
or selected repositories. Prefer a browser sign-in GitHub App with
organization **Members: read**, repository **Metadata: read**, and approved
installations covering the selected targets when that broad grant is
unacceptable. Its user token remains limited by the user's own access and
the App's permissions and installations. App-only credentials never substitute
for the requester's live `admin`, `maintain`, or `push` permission.
Missing access, withheld permission fields, and failed probes remain denials.
Existing sessions lacking account-ID binding require ordinary sign-in before
administrator consent. Private-target probes remain denied without actual
token access. Disabling the feature
does not revoke prior GitHub grants. See the
[onboarding instructions](README.md#oauth-permissions-and-onboarding).

Organization and team authorization is revalidated whenever an OAuth access
token is refreshed. Failed revalidation deletes the session and revokes both
the previous and newly issued credentials.

Users with multiple personal or managed-user GitHub identities can explicitly
switch accounts from the dashboard. Switching is a CSRF-protected mutation that
revokes and deletes the current server session before redirecting to GitHub's
account chooser; CAO never combines authority or tokens from multiple accounts
in one browser session. If GitHub revocation is unavailable, logout and account
switching atomically remove the active session, clear its cookies, and retain
the encrypted credentials only in the selected operational pending-revocation
queue. Redis retries survive restart; opted-in memory retries do not.
Subsequent OAuth entry retries queued revocation without restoring session
authority.

Encrypted session and pending-revocation records carry a non-secret key
identifier. During controlled rotation, configure the old key through
`CAO_SESSION_SECRET_PREVIOUS` while `CAO_SESSION_SECRET` contains the new key;
remove the previous key only after active sessions and queued revocations using
it have drained or every remaining credential has provider-confirmed expiry.
The revocation worker runs at startup and every minute and retries bounded
batches. Local one-hour access and thirty-day refresh/session deadlines bound
CAO authority; they never prove that GitHub credentials expired. Missing,
nonpositive, overflowing, and legacy expiry metadata is unknown for cleanup.
An unknown-expiry credential remains encrypted cleanup debt until GitHub
accepts revocation (including already-absent responses); elapsed local deadlines
do not release the record or permit key retirement. Confirmed expiry releases
a record only when every credential actually present has expired.

Inactive sessions retain their final encrypted cleanup owner after their
local authority expires. PostgreSQL and memory stage expired sessions from
protected records; Redis retains persistent encrypted session envelopes behind
a stable deployment-scoped due index. Reads and CAS reject expired sessions
before the worker runs, and maintenance cannot delete their cleanup debt.
Session admission reserves the configured cleanup destination's capacity;
pending debt backpressures new admission rather than evicting credentials.
No-refresh and failed-refresh paths use atomic invalidation, just like logout.
Refresh persistence uses an atomic compare-and-swap against the encrypted
session record, so concurrent logout cannot be overwritten by a late token
refresh; superseded credentials are revoked or queued separately.

If both revocation and protected persistence fail for newly exchanged tokens,
the request keeps a cleanup owner and retries independently of client
cancellation until revocation, durable queuing, or confirmed provider expiry.
At most sixteen exchanges per process may hold such ownership; exhausted
capacity refuses further exchanges before asking GitHub for tokens. These
requests can remain outstanding during a sustained outage. A forced process
kill between provider issuance and durable persistence can still lose that
in-memory owner; graceful shutdown must allow cleanup to finish. The opted-in
memory backend additionally loses all retained debt on restart.

Roll out the new session-envelope format with old replicas stopped: older
executables cannot read the new Redis records and can still TTL-delete older
ones. Preserve the stable revocation prefix, storage, and current/previous keys.
Existing queued records without provider-expiry fields are conservatively
unknown; do not delete them on their old local deadlines. Existing pre-upgrade
sessions need interactive reauthentication/reconciliation: already deleted
sessions or abandoned grants cannot be reconstructed from this fix. Operators
must reconcile outstanding GitHub grants through a separately authorized
procedure; deployment alone does not revoke them.

Access tokens and refresh tokens remain server-side. They are encrypted with an
AES-GCM key derived from `CAO_SESSION_SECRET` before being stored in Redis under
the deployment namespace. Browser cookies contain only an opaque session ID and
a CSRF token; GitHub tokens are never placed in browser-readable storage,
URLs, API responses, logs, telemetry, or Bicep outputs.

#### Administrator step-up

Administrator authority requires a listed administrator login *and* a fresh,
assured interactive authentication. This applies to `/mcp`, collection, quota,
operations, and hosted CLI routes. The session records `AuthenticatedAt` and an
assurance only when a full authorization-code flow completes. Token refresh
and revalidation never extend either value.

- **Fresh** means within `CAO_ADMIN_REAUTH_MAX_AGE`, which defaults to `15m`
  and must be between `1m` and `1h`. Malformed or out-of-range values fail
  startup. Timestamps in the future are rejected.
- **Assured** means GitHub's `/user` response explicitly reported
  `two_factor_authentication: true` (`2fa`), or that a membership read with the
  fresh token succeeded for an organization listed in
  `CAO_GITHUB_SSO_ORGANIZATIONS` (`sso:<org>`). A missing or false 2FA field
  cannot establish `2fa` assurance, even if the requested scopes included
  `read:user`. GitHub App tokens do not use OAuth user scopes; if the provider
  omits the field, an accepted SSO attestation path is required instead.
  The SSO list is an operator attestation that those
  organizations enforce SAML SSO for OAuth access.
- A listed administrator without step-up receives `401` with
  `WWW-Authenticate: CAO-Step-Up realm="admin", max_age=<seconds>` and
  `{"code":"admin_step_up_required","reauthenticate":"/auth/login"}`. All
  other principals receive `403`, so step-up state is never disclosed to them.
  `GET /api/auth/session` exposes `adminAssuranceExpiresAt`.

GitHub OAuth cannot force password or 2FA re-entry. Step-up therefore proves
a recent completed authorization, not a new second-factor challenge.

`read:user` exposes 2FA enrollment, and `prompt=select_account` selects an
account; neither proves a fresh MFA ceremony. Operators who require a fresh
second-factor or SSO challenge need a provider/session policy that enforces
it; organization 2FA enrollment alone is insufficient. This server does not
verify a provider MFA ceremony timestamp. The local loopback profile has no
OAuth and is unaffected.

Operational-value publication (`POST /api/v1/value`) is also an administrator
mutation with the same step-up and session-bound CSRF requirements. Local
PostgreSQL accepts only the existing loopback bearer capability; hosted
publication has no PAT or Actions-token bypass. The bounded request contains
one original evidence envelope, never evaluator code or a database credential.
The target repository/workflow must already be observed. Publication validates
contract identity, evidence relationships, immutable history and capture order
before atomic namespace-scoped writes; Activity replacement cannot erase owned
publications. Publishing does not grant collection or workflow execution
authority. Recurring acquisition requires a separately reviewed, confined local
collector and exact policy opt-in intersected with the existing Activity scope.
Disabling collection retains history. Collection failures never advance the
reported evidence timestamp.

Session cookies are `Secure`, `HttpOnly`, and `SameSite=Lax`. Mutating
endpoints require the session-bound `X-CSRF-Token` header. The injected
dashboard bootstrap adds this header for same-origin browser requests.

When an access token is near expiry, the server uses the refresh token, stores
rotated token values, renews both browser cookies, and continues the request.
The readable CSRF cookie expires one day before the session cookie; if it is
missing, the dashboard first makes an authenticated read to restore both cookies
before sending a mutation. A missing session or failed renewal cannot authorize
a mutation. If refresh fails or the
refresh token has expired, session authority is removed, cleanup is staged,
cookies are cleared, and the
client must reauthenticate. Logout revokes both the current access token and
the current refresh token when GitHub accepts revocation, deletes the Redis
session, and clears cookies.

### Azure lifecycle and transport limits

Cold starts rebuild the app from environment configuration, validate Redis
connectivity before serving. Request cancellation is propagated through
`request.Context()` to Redis calls.

Health checks remain at `GET /api/v1/health`. Unauthenticated health responses
report operational connectivity and capabilities, recovery readiness, and data availability.
Authenticated sessions
also receive revision and count metadata.

Server-Sent Events at `GET /api/v1/events` are best-effort in Azure Functions.
The endpoint works while the platform keeps the invocation alive, but scale-in,
cold starts, idle timeouts, proxies, and plan limits can terminate long-lived
connections. Clients must continue to use `/api/v1/refresh` as the reliable
revision check. WebSockets are not part of this profile.

### Threat model

The Azure Functions profile assumes the following actors:

| Actor | Capabilities | Security expectation |
| --- | --- | --- |
| Authorized dashboard user | Opens the hosted dashboard and makes browser API requests. | Must authenticate through GitHub OAuth, satisfy explicit org/team authorization, send CSRF headers for mutation, and receive no Redis credentials or GitHub tokens. |
| Unauthenticated or unauthorized user | Can reach the public Function App URL. | Can read only minimal health status; all static dashboard access redirects to login and all data APIs fail closed. |
| Browser attacker | Can attempt CSRF, stale session reuse, URL injection, or token exfiltration through browser storage. | Session authority is in `Secure`, `HttpOnly`, `SameSite=Lax` cookies; CSRF tokens are session-bound; GitHub tokens and Redis URLs are never browser-readable. |
| Network attacker | Can observe or interfere with traffic outside Azure/GitHub TLS channels. | HTTPS-only Function App, TLS Redis, verified Redis certificates, and no plaintext remote Redis are required. |
| Azure platform/operator | Can deploy Bicep, configure app settings, rotate keys, and view platform metadata. | Uses reviewed Bicep, managed identity, Key Vault RBAC, non-secret outputs, and secret rotation procedures; does not copy secret values into logs, tickets, or checked-in files. |
| GitHub OAuth/API | Issues tokens and reports membership. | OAuth client secret remains in Key Vault, tokens remain server-side, refresh failures clear sessions, and membership is rechecked before session creation. |
| Redis | Stores operational state, bounded caches, and encrypted OAuth session records. | Does not store dashboard entities or control dashboard-data readiness; access is TLS-only and namespace-scoped. |
| Telemetry/diagnostics reader | Can view Application Insights and operational logs. | Sees only structured, non-secret operational metadata; no tokens, cookies, Redis URLs, prompt contents, source records, or secret values are logged. |

Protected assets:

- GitHub OAuth client secret, access tokens, refresh tokens, and membership
  authorization decisions;
- `CAO_SESSION_SECRET`, encrypted session records, session cookies, and CSRF
  tokens;
- Redis URL/access key and the PostgreSQL dashboard projection;
- compacted dashboard source artifacts, logical source rows, diagnostics, and
  query results;
- Bicep, app settings, Key Vault RBAC assignments, deployment history, and
  telemetry used as compliance evidence.

Primary threats and mitigations:

- **Credential disclosure**: mitigated by mandatory Key Vault references,
  managed identity/RBAC, non-secret Bicep outputs, server-side token storage,
  AES-GCM encryption before Redis persistence, and no secrets in telemetry or
  browser-readable state.
- **PAT or bearer bypass**: mitigated by rejecting local bearer capabilities in
  Azure mode and documenting PATs as unsupported. Only GitHub OAuth plus
  explicit org/team authorization can create a session.
- **CSRF and session fixation**: mitigated by signed OAuth state, opaque session
  IDs, `Secure`/`HttpOnly`/`SameSite=Lax` cookies, per-session CSRF tokens, and
  session deletion on refresh failure/logout.
- **Untrusted proxy headers**: mitigated by an explicit
  `CAO_AZURE_ALLOWED_HOSTS` allow-list and required HTTPS forwarded protocol.
  The local profile keeps separate loopback `Host` protections.
- **Redis compromise or data confusion**: mitigated by treating Redis as
  disposable derived state, using TLS-only Redis, namespacing every key,
  disabling Redis public network access in Bicep, and rebuilding from trusted
  artifacts when needed.
- **Long-lived connection assumptions**: mitigated by treating SSE as
  best-effort in Functions and requiring `/api/v1/refresh` polling as the
  reliable revision check.
- **Over-broad observability**: mitigated by logging only structured
  operational metadata and excluding tokens, cookies, Redis URLs, source
  records, and prompt contents from Application Insights and diagnostics.
- **Stale or stolen administrator session**: mitigated by administrator
  step-up. Administrator authority expires within `CAO_ADMIN_REAUTH_MAX_AGE`
  of the last interactive authentication and is never extended by refresh.
- **Hosted CLI execution abuse**: mitigated by an opt-in flag, catalog-only
  actions, requester repository permission at admission, a live permission
  recheck before execution, single-use repository-scoped App tokens revoked
  after each command, and isolated, deadline-bound subprocesses. See
  [Hosted CLI actions](#hosted-cli-actions).
- **Deployment drift**: mitigated by using checked-in Bicep as the reviewed
  contract, focused Bicep contract tests for Key Vault and platform security
  controls, and compliance review of Azure activity logs and Function App
  configuration changes.

Open experimental risks that must be accepted or closed before live production
use:

- Azure Functions cold starts, scale-in, and platform timeouts may interrupt
  long-lived SSE even when query/refresh endpoints continue to work.
- The Bicep template is a baseline and does not by itself prove tenant-specific
  network isolation, private endpoint reachability, cost limits, backup
  posture, data residency, or regulatory compliance.
- PostgreSQL stores the rebuildable dashboard projection; Redis stores only
  operational state, bounded caches, and encrypted sessions. An organization
  must validate whether its data classification permits those stores and
  retention models.
- The profile has focused automated tests and Bicep contract checks, but it
  still requires staged deployment, OAuth callback validation, key rotation
  exercises, logging review, and rollback rehearsal with the actual Azure
  tenant and GitHub organization.

### Azure secure-computing and compliance controls

Key Vault is mandatory for every secret-bearing Azure setting. The deployment
contract keeps the GitHub OAuth client secret, session secret, Redis
`rediss://` URL, and Functions runtime storage connection in Key Vault and wires
the Function App through versionless Key Vault references. Do not emit these
values from Bicep, commit them in parameter files, copy them into app settings
as literals, or log them during deployment.

Use a system-assigned managed identity and Key Vault RBAC for secret reads.
Review Key Vault access policies/role assignments, Azure activity logs, and
Function App configuration changes as part of compliance evidence. Secret
rotation should happen through GitHub OAuth settings, Azure Redis/storage key
rotation, and new Key Vault secret versions. Versionless references allow the
platform to resolve current versions without changing application settings.

Keep the Azure secure-computing baseline enabled:

- HTTPS-only Function App, TLS 1.2 or newer, disabled FTPS, and no local bearer
  capability in Azure mode;
- Redis encrypted client protocol, disabled Redis public network access, and no
  Redis credentials in browser payloads;
- storage HTTPS enforcement and no public blob access for Functions runtime
  state;
- Key Vault RBAC authorization, soft delete, and no secret values in Bicep
  outputs; and
- Application Insights/telemetry with structured operational metadata only,
  never GitHub tokens, Redis URLs, session secrets, cookies, authorization
  headers, source records, or prompt contents.

Application Insights app settings alone do not activate Go exporters. Use the
[reviewed HTTPS OTLP route](../docs/deployment-azure.md#explicit-go-telemetry-route)
for the Function App and collection/backfill roles, with secret-manager input
for collector authentication and secret-scoped Key Vault resolution. Go log
export requires explicit opt-in and uses bounded namespaces, not the workers'
broad debug selection. Collector/backend delivery and alert acknowledgements
still require deployment receipts; platform logs and independent audit delivery
remain separate. Exporter availability must not gate request readiness.

The Azure Elastic Premium baseline keeps one always-on instance for the
process-lifetime revocation worker. Redis remains the durable queue across
process replacement; each startup resumes bounded retry batches before the
minute interval begins.

## Redis transport and isolation

- Plaintext `redis://` connections are accepted by default only for `localhost`
  or a literal loopback IP address. `serve-hosted` additionally accepts a
  private IP or single-label service hostname only when
  `allow-private-plaintext: true` is set in the `cao.json` Redis module.
- All other Redis connections require `rediss://` with normal certificate-chain
  and hostname verification. Azure always follows this path. There is no
  insecure TLS mode.
- Redis usernames and passwords remain in the Go process and are never returned
  in HTML, browser configuration, API payloads, or query URLs.
- Every deployment uses a validated Redis namespace. The default is derived
  deterministically from the absolute checkout/worktree path, and
  `--redis-namespace` may supply an explicit deployment identifier.
- The namespace applies to operational keys, including sessions, queues,
  coordination, rate limits, and bounded caches.

Namespace isolation prevents accidental collisions between local deployments.
It does not replace Redis ACLs. Use a dedicated Redis instance or database and
credentials restricted to the deployment namespace for separate trust
boundaries.

For Azure, use encrypted Redis client protocol. `server/azure/main.bicep` sets
Redis public network access to disabled and expects the Redis URL to be
delivered through Key Vault-backed app settings. Redis client authentication
currently relies on access keys; do not output them. To rotate, regenerate the
Redis access key, update
the `cao-redis-url` Key Vault secret with the new `rediss://` URL, then restart
the Function App so Key Vault references resolve the latest secret version.

## Artifact ingestion protections

Ingestion accepts only the compacted deployed dashboard contract:

```text
payload-hashes.json
inventory-sources.json
gh-aw-logs-runs/*.jsonl
gh-aw-logs-records/*.jsonl
```

The ingester:

- rejects absolute paths, traversal paths, and raw Activity shard paths;
- validates every manifested SHA-256 hash;
- requires run-information shards before record shards;
- bounds JSONL scanner records;
- accepts only known canonical collections;
- validates and projects data before activation;
- writes the current entities, quality metadata, diagnostics, and revision state
  in one PostgreSQL transaction.

A failed ingestion rolls back and leaves the previously committed PostgreSQL
state available.

Artifact hashes provide integrity relative to the manifest; they do not prove
the manifest's publisher identity. Obtain the artifact through a trusted
channel.

## Query protections

Dashboard Language queries are treated as untrusted structured input after the
capability check.

- The server validates query names, dependencies, join shape, operators,
  reducers, computed functions, and temporal-series definitions.
- Limits apply to definitions, joins, predicates, alternatives, aggregate
  values, source rows, output rows, body size, and total operations.
- Unsupported prediction queries fail closed.
- Invalid, cyclic, over-budget, stale-pagination, and unavailable-source
  requests return explicit errors rather than partial results.
- Filtering, aggregation, sorting, limiting, joins, unions, and computed fields
  execute as bounded SQL over PostgreSQL. Redis may cache completed query
  responses but does not evaluate queries or store dashboard entities.

The access capability authorizes the holder to query all data in the current
local dashboard dataset. There is no per-source or per-row authorization.

## Hosted CLI actions

Hosted CLI actions are an experimental feature and are disabled by default.
`serve-hosted` requires both `--experimental` and `--hosted-cli-actions`;
without either flag, the `/__cli_commands` routes are not registered. Enabling
them requires GitHub OAuth, the PostgreSQL operational store,
`CAO_HOSTED_CLI_CONTROL_REPOSITORY`, and the action catalog named by
`--cli-actions-catalog`. The normative
contract is [`specs/server-hosted-cli.md`](../specs/server-hosted-cli.md).

- **Admission** happens in the web process. It requires administrator step-up,
  resolves the request only against a catalog-declared action, plans the
  command for exactly one target repository, and confirms the requester's own
  `admin`, `maintain`, or `push` permission with their OAuth token. The web
  process never holds the App private key. Admission persists the authenticated
  numeric account ID with the login used for attribution; clients cannot set it.
- **Execution** happens in the `execute-cli` process, which holds the single
  queue lease. Other replicas stand by. Before issuing any work, it re-plans
  the command and rechecks the actor's live `admin` or `write` collaborator
  permission with a separate `metadata: read` token that is revoked at once.
  The same permission response must identify the admitted numeric account ID.
  A matching login with a different or absent ID cannot authorize work.
  No work-scoped token is minted, pooled, or pre-minted for a requester who
  fails that check, and cancel also requires the requester's write access to
  the command's repository. Step-up is not rechecked at run time: admission is the
  authorization point, and the recheck and command deadline (default 15
  minutes) bound execution.
- **Legacy requester identity** is never inferred from a current login lookup.
  Commands without an admitted account ID fail explicitly before token issuance
  or subprocess startup and remain available for review and fresh, explicit
  retry. All web/executor replicas must be upgraded together with admissions
  paused and old executors stopped/reconciled; old executors ignore the new ID.
  See the [upgrade contract](../specs/server-hosted-cli.md#42-queued-identity-continuity-and-upgrades).
- **Durability and replica coordination** use one shared PostgreSQL operational
  namespace. The transactionally acquired, fenced queue lease elects one
  executor replica; persisted execution claims and the configured concurrency
  limit prevent another replica from claiming running commands. If a lease
  expires while a subprocess may still be running, the queue enters blocked
  recovery rather than automatically replaying it. An operator must confirm
  that the old process stopped before acknowledging recovery. This avoids
  duplicate dispatch but cannot guarantee exactly-once external side effects
  across a process crash.
- **Tokens** are installation tokens minted for exactly one repository with
  the plan's narrowest permissions. A token serves one command and is revoked
  when that command reaches a terminal state. It never returns to the pool.
  The broker checks the grant GitHub returns. If it covers other
  repositories, or permissions beyond those requested, the token is revoked
  at once and the command fails. Every received token has a cleanup owner,
  including rejected grants and failed mint-audit appends. Before use, tokens
  are encrypted with the current session key and retained in the protected
  operational revocation queue, isolated from OAuth cleanup. The executor
  holding the command queue lease retries interrupted owners' tokens; active
  local tokens are not revoked by periodic maintenance. After confirmed
  revocation or expiry, the encrypted credential is replaced by a
  credential-free receipt without waiting for audit availability. Receipt
  removal and its audit append are atomic; receipts have no expiry and replay
  without another GitHub effect. The first observed revoke failure per token
  is also journaled and acknowledged atomically. A crash between the external
  effect and receipt persistence remains uncertain: recovery may repeat
  revocation or observe expiry, explicitly recording the later observation.
  Unpersisted cleanup failures stop
  further token issuance; graceful shutdown retains ownership while retrying
  until persistence, revocation, or expiry succeeds (at most the last received
  token's one-hour lifetime, plus bounded in-flight requests). Missing receipt
  persistence remains an explicit evidence error after expiry. Cancellation of
  `Broker.Close` reports unresolved cleanup and keeps its owner; executor
  graceful shutdown ignores command cancellation for this cleanup. An abrupt process kill in
  the non-atomic GitHub-issuance/local-persistence window still relies on
  GitHub's token expiry. Pre-minted backlog
  tokens are bounded by `--max-pooled-tokens` (default 8), two per scope, and
  a two-minute idle lifetime. Idle tokens are revoked and are all revoked at
  shutdown.
- **Isolation**: each command runs in a fresh `0700` workspace inside the
  digest-pinned `ghcr.io/github/gh-aw` image; the workspace is removed only after
  confirmed workload termination. The container
  has a read-only root, no added capabilities, resource limits, and only the
  per-command workspace mounted writable. Only the `gh` and `gh-aw` entrypoints
  are allowed, following a fixed in-container Git initialization bootstrap
  that preserves their positional arguments. A temporary mode-`0600` environment file supplies the scoped
  token and minimal command environment; the App key, database credentials,
  executor home, and Docker socket are not mounted. The root Docker context must
  be local because it bind-mounts a host workspace. On cancellation, timeout, or
  lease loss, the executor attempts bounded forced removal. Durable container
  name and daemon identity are registered before creation; unconfirmed
  termination retains the execution claim and workspace. Successful Docker
  startup or log-stream closure alone cannot settle a command. The lifecycle
  interface keeps startup, output transport, and workload evidence separate;
  a stop request is not confirmation of termination. Recovery must confirm termination on
  the original daemon after the old executor and its requests have stopped.
  Output redaction applies before persistence.
- **Production Docker egress**: startup requires an exact-host policy and the
  reviewed, SHA-256-verified static Linux relay. Workloads use `--network none`;
  clearing proxy variables cannot restore IP, UDP, or DNS reach. A private
  per-command Unix socket and read-only relay (with no writable workspace alias)
  expose only a bounded HTTPS CONNECT proxy. Every resolved address is validated
  before numeric dialing; private GHES needs exact host/IP approval, never a
  subnet, metadata, loopback, or link-local exception. This is destination
  control, not TLS SNI or content inspection. Cancellation closes tunnels but
  cannot settle an unconfirmed workload. Native development profiles are
  unchanged; SQLite Docker alone retains its previous bridge mode when no
  egress settings are supplied. See the
  [production rollout contract](README.md#production-docker-egress-rollout)
  for provenance, limits, compatibility, and real-runtime evidence requirements.
- **Native development profiles**: only `execute-cli --sqlite` may explicitly
  select Apple container on Apple Silicon or Windows Sandbox on Windows.
  Apple uses the pinned Linux image, a private VM, resource limits, and a fixed
  in-guest no-new-privileges bootstrap. Its API's stopped state must be backed
  by absence of the VM's launchd service. Windows Sandbox instead requires an
  explicitly approved SHA-256-pinned Windows tools archive; it never falls
  back to host binaries or claims to execute the Linux image. Sandbox ownership
  spans local executor namespaces, and unrelated/user-created sandboxes must
  never be adopted or stopped. The conservative Windows profile rejects other
  HCS-managed VMs and requires independent host-compute VM absence before
  settlement; it never guesses a Sandbox-to-host-VM ID mapping.
  Native guest result files are not termination
  evidence. Both profiles preserve scoped credentials, lease fencing, and
  retained claims on uncertain cleanup; neither is a production deployment
  option or an automatic fallback from Docker.
- **Output safety**: stdout and stderr independently retain unfinished known
  credentials and credential-shaped spans before UTF-8-safe framing. Writes,
  newlines, and size-based flushes cannot expose a credential split between
  stored chunks. Unfinished registered-credential prefixes are masked at close.
  Retention is bounded by the longest registered credential
  plus two 8 KiB buffers per stream. If an unresolved credential-shaped span
  exceeds 8 KiB, that stream emits a redaction/suppression notice and discards
  further output until command completion; the other stream remains available.
  This fails closed instead of guessing where an unbounded or overlapping
  credential ends. Queue output limits still apply.
- **Native command planning**: Go re-plans every command against the catalog
  allowlist. Campaign-package and standalone-workflow source updates become
  native `gh-aw update` invocations constrained to the configured control
  repository and exact retained GitHub source coordinate.
  The backend does not run repository shell scripts, check out the control repository, or
  execute scripts from it. The image reference is pinned by digest and included
  in command-start audit records. An operator-selected mirror must also use a
  reviewed digest-pinned reference.
- **App setup**: native Go `gh cao setup auth hosted-cli` creates a least-privilege
  App (actions, contents, pull requests, and workflows write; metadata read).
  It refuses sensitive or organization permissions and installs the App only
  on the control repository and named same-owner targets. The private-key file
  it writes with mode `0600` is a temporary bootstrap copy, not secret storage:
  import it into the executor deployment's vault and remove the local copy.
  Existing files and symlinks are never overwritten. Browser approval remains
  required; all-repository installations and incomplete selected scope fail
  closed. This setup does not change CAO policy, Actions credentials, or hosted
  enablement. The retired Node profile is rejected before side effects.
  Make the vaulted value available only to `execute-cli`, preferably through a
  secret-manager-mounted file configured with `CAO_HOSTED_CLI_PRIVATE_KEY_FILE`.
  Do not place it in the dashboard web process, checked-in configuration,
  command arguments, or logs. GitHub allows two active
  App keys during rotation; deploy the replacement to every executor, restart
  only after admissions are paused and commands have drained, verify token
  minting, then revoke the old key. The executor reads its key at startup.

- **Audit trail**: both processes append audit events to the PostgreSQL
  table `cao_operational_audit_events`. Rows carry a gap-free sequence and a
  SHA-256 hash chain, a trigger rejects updates and deletes outside retention
  (at least 400 days), and `VerifyAudit` detects edits, gaps, and reordering.
  Hosted CLI actions refuse to start without the table, and admission, cancel,
  retry, token minting, and command start fail closed when their event cannot
  be stored. Result commits and execution-claim settlement now append their
  lifecycle events atomically, including interrupted-command recovery. An
  unavailable audit table cannot release an uncertain claim. Replays do not
  duplicate semantic completions; stored cancellation wins a late success.
  Logger-produced events are mirrored as a JSON line (`"audit":"cao"`) to
  standard error or to `CAO_AUDIT_LOG`, opened for append with mode `0600`,
  for SIEM forwarding. The events cover admin denials (with the
  step-up or not-admin reason), admission, rejection, cancel, retry, token
  mint and revocation, the command's start, permission denial, and finish.
  They carry the actor, repository, assurance, and execution image reference,
  but no credentials. Tokens appear only as `hashed_token`, in GitHub's audit-log
  format, so CAO events join GitHub's record of the App's actions. Storage-owned
  lifecycle events are in the committed audit stream, not the inline mirror;
  consume committed rows (including `audit export`) for complete forwarding.
  Forward committed evidence to write-once SIEM storage when you need protection against a
  database superuser rewriting the whole chain.
- **Audit verification**: run `cao-dashboard audit verify` on a schedule. It
  recomputes the chain, prints the head `lastSequence` and `lastHash`, and
  records `audit.verified` or `audit.verify_failed` with that head, so heads
  also reach the mirror and OTLP. Keep recent heads in write-once storage and
  pass them back with `--anchor SEQUENCE:HASH`. Anchors detect a consistent
  rewrite of the whole chain and a rollback to an earlier head, which the
  chain alone cannot detect. `cao-dashboard audit export` writes rows as JSON
  Lines for evidence requests.
- **Independent delivery**: the opt-in
  [`audit deliver` job](audit-delivery/README.md) replays bounded committed rows
  only after anchored verification, requires exact independent read-back, and
  advances a destination-resident conditional checkpoint only after delivery.
  Missing/stale anchors, retention gaps, interrupted verification, delivery
  failures and checkpoint conflicts fail closed with a nonzero exit and
  structured operational status. The HTTPS object adapter, its durable
  conditional-write semantics, independent administration, retained checkpoint
  versions, write-once retention and alert routing remain explicit deployment
  dependencies. The bounded JSON mirror has no retry queue; OTLP emissions are
  not acknowledgements.
- **Telemetry**: logger-produced audit events are also OTLP log records (`cao.audit.<event>`)
  with the active `trace_id` and `span_id`, which are also stored in the row.
  Command and token spans and metrics carry repository, command ID, and
  `hashed_token`, never tokens, output, or actor logins. Subprocesses receive
  only `TRACEPARENT`, never exporter headers. Alert on sustained
  `cao_hostedcli.token.pending_revocations`, on
  `cao_audit.event.count{stored="false"}`, on `audit.verify_failed`, and on
  spikes in `admin.denied`.
- **Missing rebuild evidence**: `cao-dashboard audit lifecycles` uses the
  SELECT-only evidence reader for a bounded, read-only check of admitted/started
  rebuild IDs without terminal evidence beyond grace. Alert on missing terminals,
  per-ID uncertainty, incomplete coverage and missing-check heartbeats. Recent
  pending IDs may still be active; no result infers an outcome or repairs history.
  Follow the [operator reconciliation runbook](README.md#missing-rebuild-terminal-evidence);
  independent verification/delivery and deployment cadence remain operator-owned.
- **SQLite debug store**: `--hosted-cli-sqlite` and `execute-cli --sqlite`
  replace the hosted-CLI PostgreSQL queue/audit/journal with one local SQLite
  file for debugging. Explicit `database bootstrap --store operational
  --sqlite PATH` creates it with mode `0600`; runtime opens only existing state.
  Bootstrap, runtime, and appends never perform audit retention. Local
  `database retain-audit --store operational --sqlite PATH` performs a bounded
  retention batch. `audit deliver --sqlite PATH` uses a read-only connection,
  unlike `audit verify`, which appends its own verification result.
  The server refuses it unless it listens on loopback. Its append-only
  triggers protect only against accidental edits, because anyone who can
  write the file can drop them or run retention; SQLite does not model
  PostgreSQL database-role separation. Do not use it in production.

The executor's authorization and token lifecycle are model-checked in
[`specs/tla/HostedCLIActions.tla`](../specs/tla/README.md#hosted-cli-actions-model).

### Operational security review

A SecOps, DevOps, and ServerOps review of hosted CLI actions found that the
hash chain was never verified in operation, that no external anchor protected
against a whole-chain rewrite, and that the executor, token broker, and audit
trail emitted no telemetry or trace correlation. Audit verification, anchors,
export, OTLP audit logs, and hosted CLI telemetry address those findings.
Recommended next steps, in priority order:

1. Evaluate a stronger gVisor or microVM boundary against the deployment's
   threat model; it is not a universal SOC 2 mandate. Production Docker egress
   restriction is now required, with live boundary evidence collected during rollout.
2. Evaluate managed App JWT signing in a KMS or HSM against the deployment's
   threat model. It is not a universal SOC 2 mandate or a supported runtime
   option today; see [App key custody and rollover preflight](#app-key-custody-and-rollover-preflight).
3. Deploy the [PostgreSQL privilege boundary](POSTGRES.md): owner-only bootstrap
   and canonical partition maintenance, runtime audit `INSERT`/`SELECT`, a
   separate retention-function executor, and SELECT-only evidence access.
   Runtime startup performs no schema/trigger DDL or audit retention. Apply
   the shipped grants, remove owner credentials from services, and schedule
   maintenance; code changes alone do not establish deployed role isolation.
4. Forward audit records to write-once storage or a SIEM, and schedule
   `audit verify` with external anchors.
5. Propagate the admission trace context into the queued command.
6. Expose executor liveness and readiness that reflect store, App, and
   revocation health.

### App key custody and rollover preflight

The supported signing path loads an unencrypted RSA PEM at process startup.
Managed **secret storage** is not managed **signing**: the Azure templates use
Key Vault secret references and managed identity to deliver PEM material into
the collector process. They do not invoke Key Vault keys or keep the signing
key outside process memory. Hosted CLI execution uses its dedicated App key;
the web admission process must not receive it. Gateway production receive
processes likewise must not receive App private keys; only dispatch loads the
receiving-App and dispatch-App keys. Preserve these boundaries during rotation.

`cao-dashboard app-key-check` is an explicit, offline preflight for an
operator-selected PEM file. It does not read credential environment defaults,
initialize telemetry or storage, mint tokens, contact GitHub or a vault, change
keys, or modify runtime behavior. Run it only in an authorized credential
handling environment, not on a web admission or gateway receive replica:

```sh
cao-dashboard app-key-check --key-file /run/secrets/replacement-app.pem
cao-dashboard app-key-check --key-file /run/secrets/replacement-app.pem \
  --expected-public-key-sha256 "$REVIEWED_REPLACEMENT_FINGERPRINT" \
  --previous-public-key-sha256 "$RECORDED_PREVIOUS_FINGERPRINT"
```

The JSON result contains public metadata only: `algorithm` (`RS256`), `bits`,
`publicKeySHA256`, whether each optional fingerprint check was requested and
passed (`expectedKeyChecked`, `rotationKeyChecked`), and
`scope: "offline-key-material-only"`. A fingerprint is exactly 64 lowercase hex
characters: SHA-256 of the DER SubjectPublicKeyInfo public key, not the PEM,
private key, PKCS#1 public-key encoding, or a vault version identifier.
PKCS#1 and PKCS#8 encodings of the same private key produce the same fingerprint;
re-encoding is not rotation. Obtain the expected fingerprint from an independently
reviewed public-key record, not from the candidate file being checked. Never put
private-key bytes in arguments, output, tickets or logs.

The diagnostic accepts one unencrypted RSA PKCS#1 or PKCS#8 block in a regular
file of at most 64 KiB, checks RSA consistency and a diagnostic key-size range
of 2048-8192 bits, and rejects malformed/unsupported material, mismatched pins,
and unchanged rollover keys with a nonzero exit and no success report. This is
an opt-in diagnostic policy, not a new runtime acceptance policy. It does not
establish the App ID, GitHub registration, granted permissions, provider
availability, revocation, absence of other private copies, or which key a
running replica has loaded. File reads are bounded in size, not a guarantee
against an unavailable filesystem; use a trusted local secret mount.

For a planned rollover, preserve the existing hosted CLI drain/restart
requirement in [the credential contract](../specs/server-hosted-cli.md#5-token-broker).
Before retiring the old key, retain reviewed App identity, secret version,
old/new **public** fingerprints, approvals, replica inventory and restart
records, and authorized fresh token-mint evidence for every signing role.
Changing the mounted file alone does not reload a running process. Verify the
replacement's least-privilege token grant and cleanup without running an
unapproved command. After the operator revokes the old key in GitHub, retain
evidence that a fresh mint signed with it is denied and the replacement still
works. Do not run such probes as part of this offline command, and do not retain
or restore retired private material solely to obtain evidence.

Key retirement is not installation-token revocation. Drain and revoke already
minted/prefetched tokens through the existing broker and durable cleanup
journal; do not infer token invalidation from changing or revoking an App key.
Emergency response may require immediate revocation and interruption rather
than a planned overlap; the incident owner must choose and record that action.
Synthetic rotation/refusal tests support design only, not deployed or
historical operating effectiveness.

**Managed-signing design blocker:** the current `githubapp` client and gateway
scoped transports construct `ghinstallation` App authentication from PEM.
There is no deployed provider-signing adapter, exact key/version/public-key
binding, or managed signing identity/permission contract. Key Vault **secret**
access grants do not imply key `sign` permission. A future adapter requires an
explicit provider/deployment decision, confirmation that a GitHub-registered
private key can be imported under the chosen provider's constraints (creating
an arbitrary KMS key does not register it with GitHub), exact version and RS256
(RSA PKCS#1 v1.5 with SHA-256) validation, verified signature/public-key identity,
bounded context-aware calls, role-isolated authorization, and failure without
local-key fallback. The existing `ghinstallation.WithSigner` extension takes a
JWT `Sign(claims)` interface without request context; it is a possible future
integration seam, not a provider adapter or request-cancellation guarantee.
A `crypto.Signer` seam alone likewise would not deliver these guarantees.
Keep cloud SDKs outside the vendor-neutral core.
No KMS support, permissions, rollout, or production validation is introduced by
this diagnostic.

### SOC 2 evidence boundary

CAO supplies technical controls and evidence sources; it does not make a
deployment SOC 2 compliant or provide an attestation. The organization and its
auditor define the system boundary, applicable criteria, control owners, and
review period, and assess whether controls operated effectively throughout that
period. The following mapping is limited to the hosted CLI:

| Area | CAO mechanism | Evidence to retain | Deployment responsibility |
| --- | --- | --- | --- |
| Logical access (CC6.1) | GitHub OAuth organization/team authorization, explicit administrator allowlisting, repository permission checks, and least-privilege installation tokens | Effective authorization configuration and retained access/admission audit events | Review membership, administrator lists, App installation scope, and access changes on the organization's approved cadence |
| Privileged access (CC6.3) | Assured administrator step-up, live execution permission checks, and durable denial/admission events | `admin.denied`, `cli_command.admitted`, and execution permission events correlated with GitHub audit records | Operate identity lifecycle, access reviews, and timely removal of departed or transferred personnel |
| Change management (CC8.1) | Fixed Go command planning and a digest-pinned execution image recorded at command start; CAO does not enforce an organization's review policy | Pull requests, required review/check results, release/deployment approvals, and image digest records | Enforce branch protection and deployment approval policies; preserve the evidence for each release |
| Monitoring (CC7.2) | Hash-chained audit rows, anchored verification, bounded restart-safe `audit deliver`, audit export, OTLP records, and documented alert signals | Independently preserved verification results, acknowledged event objects and checkpoints, scheduler/heartbeat and alert records | Provision and validate the independent destination and retention, enroll a trusted initial anchor, enable scheduling, and monitor failures plus missing heartbeats |
| Event evaluation (CC7.3) | Delivery/verification fail-closed status and documented gap, stale-anchor, backlog and failure signals | Alert delivery/acknowledgement, investigation disposition, recovery exercises and incident follow-up | Route and investigate alerts, evaluate effects on the system, and preserve response evidence; the application cannot perform or attest these organizational activities |

The audit chain is tamper-evident only while an attacker cannot rewrite the
entire chain. A database superuser can rewrite it consistently; anchors kept
outside the database and write-once forwarding are required to detect that
threat. The chain, exporter, metrics, and documentation do not provide evidence
that the organization performed access reviews, approved deployments, or
responded to alerts.

Personnel controls, risk assessments, vendor management, incident-response
execution, business continuity, and independent audit activities are outside
this application. The recommended hardening items above (especially independent
write-once destination configuration, activation of scheduled anchored delivery,
separate database roles, and managed key signing) still require deployment
work and real control-operation evidence. Software support and test fixtures do
not prove those controls operated. Do not represent this mapping as a SOC 2
report, certification, or independent assessment.

## Secrets and logging

- Never place Redis credentials, GitHub OAuth secrets, GitHub access/refresh
  tokens, session secrets, or the local dashboard access token in committed
  files, dashboard documents, test fixtures intended for production, query
  payloads, or browser-readable metadata.
- Never log hosted CLI installation tokens or the hosted CLI App private key.
  Tokens are redacted from command output and revoked after use. Audit events
  identify tokens only by their SHA-256 `hashed_token`.
- Keep the hosted CLI App private key in the deployment's managed secret store;
  expose it only to the isolated `execute-cli` service. The setup command's
  owner-only local output is temporary bootstrap material, not a vault.
- Prefer environment variables or local command arguments supplied by a secret
  manager for Redis credentials.
- In Azure, prefer Key Vault references in app settings. Bicep outputs must not
  include secret values or secret-bearing connection strings.
- The server intentionally prints the local capability URL for the operator.
  Protect terminal logs accordingly.
- Error responses should describe request failures without including Redis
  credentials, GitHub tokens, raw RESP traffic, private keys, or complete
  source records.

## Validation

Security-relevant validation is included in:

- Go unit tests for listen and Host restrictions, capability bootstrap and
  authorization headers, unauthenticated API rejection, TLS certificates, Redis URL policy,
  namespace isolation, request bounds, ingestion integrity, and query budgets;
- Go tests for administrator step-up (`admin_step_up_test.go`) and hosted CLI
  admission, broker, and executor (`hosted_cli_test.go`, `internal/hostedcli`);
- TLC model checking of `specs/tla/HostedCLIActions.tla` in both enabled and
  disabled configurations;
- golangci-lint with `gosec`, `bodyclose`, `contextcheck`, `errorlint`,
  `noctx`, and the standard correctness analyzers;
- the Redis-backed Playwright test in `.github/workflows/cgo.yml`.

Run:

```bash
npm run dashboard:server:lint
npm run dashboard:server:test
npm run test:e2e:dashboard-server
```

## Reporting a vulnerability

Report suspected vulnerabilities privately through a GitHub Security Advisory
for `githubnext/gh-cao`. Do not include credentials, access tokens, private
dashboard data, or exploit details in a public issue.

Include the affected commit, deployment topology, reproduction steps, impact,
and whether the issue is reachable within the supported local-only boundary.
