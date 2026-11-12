---
title: Central Agentic Ops Server Rate Limiting Specification
description: Normative requirements for distributed inbound request rate limiting in the hosted CAO server.
version: 1.0.0
status: Working Draft
editors:
  - GitHub Next
---

# Central Agentic Ops Server Rate Limiting Specification

**Version:** 1.0.0
**Status:** Working Draft
**Latest Version:** https://github.com/githubnext/gh-cao/blob/main/specs/server-rate-limiting.md
**Editors:** GitHub Next

## Abstract

This specification defines distributed abuse protection for inbound requests to
the hosted Central Agentic Ops dashboard server. It defines request classes,
token-bucket behavior, identity, trusted enterprise proxy handling, response
fields, exemptions, failure behavior, and conformance tests.

The GitHub API installation-budget governor defined by
`specs/server-ingestion.md` is a separate outbound quota mechanism and is not
defined by this specification.

## Status of This Document

This document is a Working Draft. It is the canonical normative contract for
inbound HTTP request governance in the CAO server. `server/README.md` and
`server/SECURITY.md` are explanatory and MUST NOT override this specification.

## 1. Status and conformance

The key words MUST, MUST NOT, REQUIRED, SHALL, SHALL NOT, SHOULD, SHOULD NOT,
RECOMMENDED, MAY, and OPTIONAL are to be interpreted as described in RFC 2119.

A conforming hosted server implementation satisfies every MUST in this
document. The loopback-only local development profile MAY omit the
pre-authentication edge bucket because it admits requests only from the local
operator.

PostgreSQL is the default distributed implementation, enforcing atomic buckets
under the database clock; explicitly selected Redis uses Redis server time.
An explicitly acknowledged
single-process memory deployment MAY enforce these same token-bucket semantics
atomically under its process clock instead of Redis server time. Its active
buckets MUST NOT be evicted to reset quotas; capacity failure MUST fail closed.
Buckets are lost on restart and MUST NOT coordinate separate processes. This
limited exception is specified by
[`server-operational-storage.md`](server-operational-storage.md); it does not
change request identity, proxy trust, weighted costs, headers, or deadlines.

## 2. Request classes and limits

The server MUST apply the following token buckets.

| Class | Requests | Capacity | Refill period | Subject |
| --- | --- | ---: | ---: | --- |
| Query | `POST /api/v1/query`, `/mcp` query tools | 30 cost units | 1 minute | authenticated GitHub login or validated GitHub Actions actor |
| OAuth | `/auth/login`, `/auth/callback`, `/auth/select-account` | 10 | 5 minutes | client address for login; OAuth state for callback when present |
| General | other `/api/` and `/auth/` requests | 120 | 1 minute | authenticated GitHub login, otherwise client address |
| Hosted edge | non-public hosted requests before session loading | 1,200 | 1 minute | client address |
| Hosted public probes | `GET /api/health`, `GET /api/v1/health`, `GET /api/readiness` | 600 | 1 minute | client address, shared across probe paths |

Hosted health and readiness probes (including HEAD requests accepted by the
GET routes) MUST use the public probe bucket before their handler runs, without
requiring authentication. The loopback local
profile MUST leave these probes unmetered. Invalid host or proxy boundaries
MUST be rejected without consulting the limiter. The independently
signature-authenticated GitHub webhook endpoint MUST be exempt from inner
user/API quotas but MUST cross the hosted edge bucket before its body and
signature are processed. The hosted edge bucket MUST also cover static dashboard
requests because they can otherwise trigger session loading or token refresh
before authentication completes. The local profile MAY leave static assets
unmetered.

The hosted edge bucket and an applicable post-authentication bucket are
cumulative. The post-authentication response fields MUST describe the narrower
post-authentication policy.

## 3. Distributed token bucket

Each bucket MUST be stored in the deployment operational namespace and updated
by one atomic storage operation. The operation MUST:

1. use the selected database's server time so replicas share one clock;
2. refill continuously up to the configured capacity;
3. consume exactly one token for an ordinary allowed request;
4. for a completed query, atomically consume a total cost derived from the
   greatest of started execution seconds, measured row operations plus Redis
   rows, peak working rows, and estimated peak working bytes; the cost is capped
   at the query bucket capacity and the initial request token counts toward it;
5. preserve fractional tokens on ordinary one-token denials and drain available
   tokens on a denied additional query charge;
6. retain fractional tokens;
7. return the remaining whole-token count and durations until retry and reset;
8. expire an idle bucket no earlier than two refill periods; and
9. tolerate a server clock that moves backward without creating tokens.

Bucket keys MUST contain a cryptographic digest of the subject and MUST NOT
contain a GitHub login, client address, OAuth state, session identifier, or
credential in plaintext.

### 3.1 Query cost

The total cost of a completed query MUST be the greatest of:

- one;
- execution duration rounded up to seconds;
- measured row operations plus Redis rows, rounded up in units of 250,000;
- peak working rows, rounded up in units of 100,000;
- estimated peak working bytes, rounded up in units of 16 MiB.

The result MUST be capped at the query bucket capacity. Telemetry and rate-limit
accounting MUST contain only aggregate numeric properties, including
privacy-preserving structural counts used to observe and tune the separate
structural guardrails. They MUST never contain authored names, fields,
predicates, literals, parameters, or result values.

## 4. Enterprise proxy identity

Forwarding headers MUST be used only after the request has crossed a configured
trusted proxy boundary and its effective host and HTTPS protocol have been
validated. A directly exposed listener MUST ignore forwarding headers.

At a trusted boundary, the server MUST select only the final non-empty value
from the final header field. It MUST NOT scan leftward for a parseable value,
because preceding values can be supplied by a client or an upstream enterprise
proxy outside the deployment trust boundary.

The server MUST support:

- `X-Forwarded-For` with IPv4, IPv6, optional ports, comma-separated values, and
  repeated header fields; and
- RFC 7239 `Forwarded` with a final `for=` parameter, including quoted IPv6 with
  an optional port.

`X-Forwarded-For` takes precedence when both forms are present. An absent,
obfuscated, or malformed final forwarded address MUST fall back to the direct
peer address, not to an earlier forwarded value.

Authenticated quotas MUST use the GitHub login rather than an address so users
sharing an enterprise proxy do not share normal API quotas. OAuth callbacks
SHOULD use the opaque signed OAuth state cookie so simultaneous users behind one
enterprise egress address do not share the callback quota. The higher-capacity
hosted edge bucket provides the aggregate bound for a shared egress address.

## 5. Responses and failure behavior

Every metered response MUST include:

- `RateLimit-Limit`, the bucket capacity;
- `RateLimit-Remaining`, the remaining whole-token count;
- `RateLimit-Reset`, seconds until the bucket is full; and
- `RateLimit-Policy`, formatted as `<capacity>;w=<refill-period-seconds>`.

If a completed query's additional cost cannot be charged, its result MUST NOT be
returned. An exhausted bucket MUST return `429 Too Many Requests`, MUST include
`Retry-After` as the positive number of seconds until one token is available,
and MUST NOT invoke the protected handler.

The storage operation MUST have a deadline shorter than the server request
timeout. If storage cannot enforce the limit within that deadline, the request
MUST fail closed with `503 Service Unavailable`. Logs MUST identify only the
fixed policy name and MUST NOT include subjects, keys, headers, cookies, or
credentials.

## 6. Conformance tests

A conforming implementation MUST test:

1. atomic storage transitions, result validation, and invalid configuration;
2. allowed and exhausted responses and all required headers;
3. deterministic multi-factor query cost assignment and its capacity cap;
4. authenticated identity hashing;
5. pre-authentication coverage for rejected, static, and OAuth entry requests;
6. trusted versus untrusted forwarding headers;
7. repeated and comma-separated proxy headers, address-and-port forms, RFC 7239
   values, and malformed final-value fallback;
8. separate OAuth callback subjects behind one enterprise egress address; and
9. local health/readiness exemptions and pre-signature webhook edge coverage;
10. hosted public probe limits, local exemption, and shared client-address keys.

## 7. Security and privacy considerations

Rate-limit state MUST NOT be returned by health or diagnostic endpoints.
Operational logging MUST NOT contain raw or hashed subjects, forwarding
headers, cookies, operational keys, or storage error details. Implementations MUST
preserve the trusted-boundary checks in Section 4 when adding hosting profiles;
enabling a forwarding header by name alone is not a trust boundary.

Rate limiting is defense in depth and MUST NOT replace authentication,
authorization, CSRF validation, webhook signature validation, or the outbound
GitHub API budget governor.

## 8. References

### 8.1 Normative references

- [RFC 2119](https://www.rfc-editor.org/rfc/rfc2119) — requirement keywords.
- [RFC 7239](https://www.rfc-editor.org/rfc/rfc7239) — `Forwarded`.
- [RFC 6585](https://www.rfc-editor.org/rfc/rfc6585) — `429 Too Many Requests`.
- [RFC 9110 §10.2.3](https://www.rfc-editor.org/rfc/rfc9110#field.retry-after)
  — `Retry-After`.

### 8.2 Informative references

- `specs/server-ingestion.md` — outbound GitHub API rate governance.
- `server/SECURITY.md` — deployment security profiles and trust boundaries.
- `server/README.md` — operational configuration.

## 9. Change log

### Version 1.0.0 (Working Draft)

- Defined distributed token-bucket policies, identity, headers, and failures.
- Defined trusted enterprise proxy and shared-egress behavior.
- Added duration-weighted query costs.
