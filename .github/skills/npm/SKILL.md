---
name: npm
description: "Install npm dependencies from the Microsoft-approved feed. Fix registry, proxy, and certificate failures."
---

# npm installation: Microsoft-approved feed

In the package directory, install from the existing lockfile with `npm ci`. The root, dashboard site, and server specification packages select the Microsoft-approved feed in `.npmrc`, matching [githubnext.com](https://github.com/githubnext/githubnext.com/blob/main/.npmrc):

```sh
registry=https://packagefeedproxy.microsoft.io/npm/
npm ci
```

- If diagnosing feed access, check a pinned package from the lockfile with `npm view yaml@2.9.0 version --registry="$registry" --fetch-retries=0 --fetch-timeout=15000` (substitute the actual pinned package and version).
- Feed is npm registry, not HTTP proxy. Never put feed URL in `HTTPS_PROXY`.
- npm's default `replace-registry-host=npmjs` reroutes npmjs downloads through the configured feed. Private registries and postinstall hosts stay separate. Do not edit lockfile URLs or manifests to work around network or authentication failures.
- Use `npm view`, not `npm ping`; feed may lack ping endpoint. If a pinned release is unavailable, inspect versions with `npm view PACKAGE versions --json --registry="$registry"` and check a compatible candidate's dependencies and engines. Prefer an available release within the consumer's supported range, including compatible transitive dependencies. Update constraints or overrides only as needed and regenerate the lockfile with npm, never by hand-editing URLs or integrity values. Verify tarball availability with `npm ci` and run the affected tests or build.
- Report a blocker when no compatible release is available or authentication/access fails. No guessed mirrors or credentials.
- Need forward proxy? Get approved URL from IT. Set shell `HTTPS_PROXY` / `HTTP_PROXY`; check `NO_PROXY`. Keep machine config and secrets out of repo, commands, and logs.
- Certificate failure? Retry with `NODE_USE_SYSTEM_CA=1` on supported Node. Or set `NODE_EXTRA_CA_CERTS` to IT-provided PEM path before npm starts. Check npm `cafile` override.
- TLS stays on. No `strict-ssl=false`, `NODE_TLS_REJECT_UNAUTHORIZED=0`, `curl -k`, or HTTP registry.
- `ENOTCONN`, reset, timeout, firewall denial? Network broken, not lockfile. Report host and error, no secrets. Ask IT for HTTPS access to `packagefeedproxy.microsoft.io`. No reinstall loop.
