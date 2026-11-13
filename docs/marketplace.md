---
title: Browse Campaigns and Workflows
description: Configure registries and browse campaigns and individual workflows without granting the dashboard installation authority.
---

The **Marketplace** page appears under **Updates** in the CAO dashboard. It
provides a read-only view of campaign packages and individual workflows from an
ordered set of registries. New CAO policies include both the official CAO catalog
and the [Agentics collection](https://github.com/githubnext/agentics).
Selecting a package shows its published README beside an **About** panel
carrying its normalized metadata, provenance, contents, and immutable source
coordinate, with an **Add** button above both.

A package README is the `README.md` published beside its `aw.yml` manifest. A
package without one simply shows no README preview.
Agentics workflow previews use their matching `docs/NAME.md` documentation, or
the workflow prompt body when no documentation is available.

The dashboard does not contact registries or install packages. Its **Add**
action only copies the canonical command:

```sh
gh cao add OWNER/REPOSITORY[/PATH]@COMMIT
```

Individual workflow entries instead copy:

```sh
gh aw add githubnext/agentics/workflows/WORKFLOW.md@COMMIT
```

Review the command and run it separately from a trusted checkout.
`gh cao add` and `gh aw add` install packages without using AI. AI is used only
when installed agentic workflows run; copying or running an add command does
not start those workflows.

The marketplace presents a declarative list of package cards from the
resolved marketplace table, without search, filters, or ranking controls.

Each package declares an `icon` in its `aw.yml` using a supported Primer
Octicon in gh-aw's quoted, colon-wrapped syntax, such as `icon: ":dependabot:"`,
`icon: ":shield:"`, or `icon: ":meter:"`. Both resolvers normalize these to bare
Octicon names for the package card and detail page. Official campaign packages
use the same icon as their configured dashboard navigation entry. Packages
without an explicit icon retain the default `workflow` icon.

Public stars and forks are shown only when GitHub confirms public visibility.
Missing repository data does not imply zero stars or forks.

## Configure registries

Declare registries in `.github/workflows/cao.json`. Array order defines
precedence: when registries publish the same package coordinate, the first
registry wins.

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
          "id": "agentics",
          "name": "Agentics workflows",
          "repository": "githubnext/agentics",
          "path": "workflows",
          "format": "workflows",
          "ref": "main",
          "auth": { "type": "none" }
        },
        {
          "id": "internal",
          "name": "Internal campaigns",
          "repository": "acme/cao-packages",
          "path": "packages",
          "ref": "stable",
          "api-url": "https://github.acme.example/api/v3",
          "auth": {
            "type": "pat",
            "secret": "CAO_INTERNAL_REGISTRY_PAT"
          }
        }
      ]
    }
  }
}
```

The official registry is ordinary configuration. Remove its entry to omit it,
or replace it with public, private, or GitHub Enterprise registries. Registry
IDs must be unique and stable.
For existing control repositories, add the Agentics entry above to enable its
discovery. `format` defaults to `packages` for existing registries; `workflows`
discovers Markdown workflows directly under the configured path, not nested
shared fragments. Registry discovery never enrolls repositories or enables
workflow execution.

## Authenticate private registries

Registry authentication is scoped to that registry:

- `none` needs no credential.
- `pat` references one environment secret with `secret`.
- `github-app` references `app-id-secret`, `private-key-secret`, and
  `installation-id-secret`.

Configuration contains secret names, never secret values. Make those referenced
secrets available only to the trusted Activity or hosted resolver. Credentials,
installation tokens, and authentication headers are excluded from marketplace
rows, browser storage, dashboard state, and diagnostics.

Set `api-url` on each GitHub Enterprise registry. Do not use it as a global
GitHub API override.

## Resolution modes

Both dashboard backends expose the same package fields through the data worker:

- The Activity backend resolves registries during data collection and retains
  normalized rows in the dashboard's IndexedDB database.
- The hosted Go backend resolves registries at runtime and caches safe,
  normalized results in PostgreSQL. Cache entries are isolated by registry and
  source generation.

Resolvers turn mutable refs into commit identities when possible. The copied
add command uses that normalized source coordinate rather than rebuilding it
from presentation state.

## Troubleshoot unavailable packages

One unavailable registry does not hide packages from healthy registries.
Registry diagnostics report safe status information without credential values.
Check, in order:

1. the registry `repository`, optional `path`, `ref`, and `api-url`;
2. that every referenced secret exists in the trusted resolver environment;
3. token or GitHub App access to commits, trees, and package manifests;
4. that package manifests use supported gh-aw package metadata; and
5. registry order when a duplicate package is resolved from an earlier entry.

Package installation and rollout policy remain separate reviewed operations.
