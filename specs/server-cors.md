---
title: Central Agentic Ops Server Cross-Origin Resource Sharing Specification
description: Normative requirements for cross-origin request handling and unauthenticated subresource responses in the hosted CAO server.
version: 1.0.0
status: Working Draft
editors:
  - GitHub Next
---

# Central Agentic Ops Server Cross-Origin Resource Sharing Specification

**Version:** 1.0.0
**Status:** Working Draft
**Latest Version:** https://github.com/githubnext/gh-cao/blob/main/specs/server-cors.md
**Editors:** GitHub Next

## Abstract

This specification defines how the Central Agentic Ops (CAO) dashboard server
handles cross-origin requests. It defines the same-origin default, the
reviewed `control-plane.web.host.cors` policy in `cao.json`, origin
validation, preflight and actual-request responses, the interaction with
authentication, and the rule that unauthenticated subresource requests are
never redirected to the cross-origin GitHub OAuth authorize endpoint.

## Status of This Document

This document is a Working Draft. It is the canonical normative contract for
cross-origin request handling in the CAO server. `server/README.md`,
`server/SECURITY.md`, and `docs/configuration.md` are explanatory and MUST NOT
override this specification.

## 1. Status and conformance

The key words MUST, MUST NOT, REQUIRED, SHALL, SHALL NOT, SHOULD, SHOULD NOT,
RECOMMENDED, MAY, and OPTIONAL are to be interpreted as described in RFC 2119.

A conforming server implementation satisfies every MUST in this document for
both the hosted (GitHub OAuth) profile and the loopback-only local profile,
unless a requirement is scoped to one profile.

A conforming policy validator (`.github/workflows/shared/policy.mjs` and
`.github/workflows/shared/cao.schema.json`) satisfies Section 3.

## 2. Same-origin default

The dashboard is a same-origin application. When no `cors` member is present
in `control-plane.web.host`, or when it declares no `allowed-origins`, the
server:

1. MUST NOT emit any `Access-Control-*` response header;
2. MUST NOT answer CORS preflight requests specially; and
3. MUST continue to emit `Cross-Origin-Resource-Policy: same-origin`.

Absence of policy MUST NOT be interpreted as permission for any cross-origin
caller.

## 3. Policy

### 3.1 Location

The cross-origin policy is the optional `cors` member of
`control-plane.web.host`, beside `target` and `operational-store`. It MAY be supplied in
`cao.json` or a reviewed host overlay that `extends` it. It is non-secret,
reviewed configuration and MUST be resolved at process start from the same
host policy document as the target and operational store. It MUST NOT be read from
environment variables, request data, steering files, or target repositories.

### 3.2 Members

| Member | Type | Default | Meaning |
| --- | --- | --- | --- |
| `allowed-origins` | array of strings | none | Exact origins permitted to make cross-origin requests. |
| `max-age` | integer | `600` | Preflight cache lifetime in seconds. |

Unknown members MUST be rejected. In particular, `allow-credentials` is not a
member of this policy and MUST be rejected; see Section 3.5.

### 3.3 Validation

An implementation MUST reject the entire host policy, and the server MUST
refuse to start, when any of the following holds:

1. `allowed-origins` has more than 32 entries;
2. an entry contains `*`, is `null` or empty, or is not an absolute URL;
3. an entry has user information, a path other than `/`, a query, or a
   fragment;
4. an entry's scheme is not `https`, except that `http` is permitted only for
   `localhost`, a loopback IPv4 address, or `[::1]`;
5. an entry's port is outside 1–65535;
6. an entry's host is neither an IP literal nor an ASCII DNS name of
   non-empty labels of letters, digits, and `-` (internationalized names MUST
   be written in punycode; trailing dots and `_` are rejected);
7. `max-age` is present and outside 1–86400.

### 3.4 Normalization

The server MUST normalize each origin to the serialized form browsers send in
the `Origin` header: lowercase scheme and host, no trailing `/`, and no port
when the port is the scheme default (443 for `https`, 80 for `http`). The policy
validator MUST additionally reject entries that are equal ignoring case, so
reviewed policy stays unambiguous; the server MUST merge entries that are
equal after normalization (for example `https://a.example` and
`https://a.example:443`) rather than fail. Origin comparison
MUST be an exact string match against the normalized list; prefix, suffix, and
pattern matching MUST NOT be used.

### 3.5 Credential-less CORS

Cross-origin access MUST be credential-less. The server MUST NOT emit
`Access-Control-Allow-Credentials` on any response, and no configuration MAY
enable it.

Browsers enforce this: per the Fetch Standard, a response to a request whose
credentials mode is `include` is a network error to the caller unless it
carries `Access-Control-Allow-Credentials: true`. A listed origin can
therefore read only responses an anonymous caller could read (public health
endpoints and `401` bodies). It can never read session-authenticated data,
including the CSRF token served by `/api/auth/session`, and so can never
satisfy the CSRF check on a state-changing request. Session cookies also
remain `SameSite=Lax`, so cross-site subrequests do not carry them.

Integrations that need a user's data MUST use a server-to-server channel
with its own credential, not the dashboard's browser session.

## 4. Request handling

### 4.1 Placement

Cross-origin handling MUST run after the pre-authentication edge rate limit
defined by `specs/server-rate-limiting.md` and before authentication, so that
preflights never require credentials and never bypass the edge bucket.

Before emitting any `Access-Control-*` header, the server MUST apply the same
request-host validation as its access middleware: forwarded-host and HTTPS
proxy validation in the hosted profile, and loopback host validation in the
local profile. A request that fails host validation MUST receive no CORS
headers and MUST be passed to access control, which rejects it.

### 4.2 Vary

When `allowed-origins` is non-empty, every response MUST include
`Vary: Origin`. Preflight responses MUST additionally vary on
`Access-Control-Request-Method` and `Access-Control-Request-Headers`.

### 4.3 Unlisted origins

A request with no `Origin` header, or whose `Origin` is not in the normalized
list, MUST receive no `Access-Control-*` headers and MUST otherwise be handled
exactly as if no policy were configured.

### 4.4 Preflight

A request is a preflight when its method is `OPTIONS` and it carries
`Access-Control-Request-Method`. For a preflight from a listed origin with a
valid host, the server MUST respond `204 No Content` without invoking
authentication or the application handler, and MUST set:

- `Access-Control-Allow-Origin` to the request's `Origin` value.

When the requested method is `GET` or `HEAD`, the response MUST also set:

- `Access-Control-Allow-Methods: GET, HEAD`;
- `Access-Control-Allow-Headers: Traceparent`;
- `Access-Control-Max-Age` to the resolved `max-age`.

When the requested method is any other method, the response MUST omit
`Access-Control-Allow-Methods`, `Access-Control-Allow-Headers`, and
`Access-Control-Max-Age`, so the browser denies the actual request.

### 4.5 Actual requests

For a non-preflight request from a listed origin with a valid host, the server
MUST set `Access-Control-Allow-Origin` to the request's `Origin` value and
MUST then apply authentication, CSRF validation, authorization, and rate
limiting unchanged. The server MUST NOT emit `Access-Control-Allow-Origin: *`.

## 5. Unauthenticated subresources

Browsers fetch some same-origin subresources, including the web app manifest,
in CORS mode. Following a redirect to `https://github.com/login/oauth/authorize`
from such a fetch fails because GitHub does not grant CORS access.

In the hosted profile, when a request has no valid session:

1. `/api/` requests and `POST /auth/logout` MUST receive `401 Unauthorized`;
2. requests whose `Sec-Fetch-Mode` header is present and is not `navigate`
   MUST receive `401 Unauthorized` and MUST NOT be redirected; and
3. only top-level navigations (`Sec-Fetch-Mode: navigate`, or clients that
   omit `Sec-Fetch-Mode`) MAY be redirected to `/auth/login`.

The dashboard MUST declare its manifest with
`<link rel="manifest" href="./manifest.webmanifest" crossorigin="use-credentials">`
so that the browser sends the session cookie with the manifest request.

## 6. Conformance tests

A conforming implementation MUST test:

1. origin normalization, including case, default ports, trailing `/`,
   loopback `http`, bracketed IPv6, and deduplication;
2. rejection of every invalid form in Section 3.3, in both the Go server and
   the JavaScript policy validator;
3. that `cors` is resolved from `cao.json` through the host policy loader;
4. that the same-origin default emits no `Access-Control-*` headers;
5. preflight success for a listed origin and allowed method, including
   max-age and the absence of `Access-Control-Allow-Credentials`;
6. preflight denial for `POST` and other unsupported requested methods;
7. no CORS headers for unlisted origins, with `Vary: Origin` present;
8. no CORS headers and a misdirected-request rejection for a preflight with
   an invalid host;
9. a readable `401` with `Access-Control-Allow-Origin` for an unauthenticated
   cross-origin request from a listed origin; and
10. `401` without `Location` for unauthenticated `cors`, `no-cors`, and
    `same-origin` fetch modes, and a `/auth/login` redirect for navigations.

## 7. Security and privacy considerations

CORS is a browser read-permission mechanism, not an authorization mechanism.
It MUST NOT replace or relax authentication, organization and team
authorization, administrator checks, CSRF validation, webhook signature
validation, host validation, or rate limiting.

Credentialed CORS is intentionally unsupported (Section 3.5): a credentialed
grant would let script on every listed origin act as, and read the data of,
any signed-in user, which turns a single cross-site scripting flaw on any
listed origin into a dashboard account takeover. Operators SHOULD still list
only origins under the same administrative control as the dashboard.
Wildcards are forbidden because they would make the grant unreviewable.

Logs MUST NOT include `Origin` values, cookies, or credentials; only fixed
authentication branch names such as `access.subresource_unauthorized` MAY be
logged.

## 8. References

### 8.1 Normative references

- [RFC 2119](https://www.rfc-editor.org/rfc/rfc2119) — requirement keywords.
- [Fetch Standard §3.2 CORS protocol](https://fetch.spec.whatwg.org/#http-cors-protocol)
  — preflight and response headers.
- [RFC 6454](https://www.rfc-editor.org/rfc/rfc6454) — web origin
  serialization.
- [Fetch Metadata Request Headers](https://www.w3.org/TR/fetch-metadata/)
  — `Sec-Fetch-Mode`.
- [Web Application Manifest](https://www.w3.org/TR/appmanifest/) — manifest
  fetch with `crossorigin`.

### 8.2 Informative references

- `specs/server-rate-limiting.md` — pre-authentication edge bucket.
- `server/SECURITY.md` — deployment security profiles and trust boundaries.
- `server/README.md` — operational configuration.
- `docs/configuration.md` — host policy overlays.

## 9. Change log

### Version 1.0.0 (Working Draft)

- Defined the same-origin default and the `control-plane.web.host.cors`
  policy.
- Defined origin validation, normalization, preflight, and actual-request
  handling.
- Required `401` instead of an OAuth redirect for unauthenticated
  subresources, and a credentialed manifest fetch.
- Defined credential-less cross-origin access restricted to `GET` and
  `HEAD`.
