---
name: npm
description: "npm behind Microsoft firewall: use 1ES feed. Fix registry, proxy, and certificate failures."
---

# npm installation: default or Microsoft network

In the package directory, install from the existing lockfile with `npm ci` when the default npm registry is reachable. On the Microsoft corporate network, if npmjs access is blocked, use the 1ES feed instead:

```sh
registry=https://ms-feed-25.pkgs.visualstudio.com/1es-public/_packaging/npm-public/npm/registry/
npm ci --registry="$registry" --replace-registry-host=npmjs
```

- If diagnosing feed access, check a pinned package from the lockfile with `npm view yaml@2.9.0 version --registry="$registry" --fetch-retries=0 --fetch-timeout=15000` (substitute the actual pinned package and version).
- Feed is npm registry, not HTTP proxy. Never put feed URL in `HTTPS_PROXY`.
- `replace-registry-host=npmjs` reroutes npmjs downloads. Private registries and postinstall hosts stay separate. Do not edit lockfile URLs or manifests to work around network or authentication failures.
- Use `npm view`, not `npm ping`; feed may lack ping endpoint. If a pinned release is unavailable, inspect versions with `npm view PACKAGE versions --json --registry="$registry"` and check a compatible candidate's dependencies and engines. Prefer an available release within the consumer's supported range, including compatible transitive dependencies. Update constraints or overrides only as needed and regenerate the lockfile with npm, never by hand-editing URLs or integrity values. Verify tarball availability with `npm ci` and run the affected tests or build.
- Report a blocker when no compatible release is available or authentication/access fails. No guessed mirrors or credentials. [Upstream caching needs feed permissions](https://learn.microsoft.com/en-us/azure/devops/artifacts/concepts/upstream-sources?view=azure-devops).
- Need forward proxy? Get approved URL from IT. Set shell `HTTPS_PROXY` / `HTTP_PROXY`; check `NO_PROXY`. Keep machine config and secrets out of repo, commands, and logs.
- Certificate failure? Retry with `NODE_USE_SYSTEM_CA=1` on supported Node. Or set `NODE_EXTRA_CA_CERTS` to IT-provided PEM path before npm starts. Check npm `cafile` override.
- TLS stays on. No `strict-ssl=false`, `NODE_TLS_REJECT_UNAUTHORIZED=0`, `curl -k`, or HTTP registry.
- `ENOTCONN`, reset, timeout, firewall denial? Network broken, not lockfile. Report host and error, no secrets. Ask IT for HTTPS access to `ms-feed-25.pkgs.visualstudio.com`. No reinstall loop.
