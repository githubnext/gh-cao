---
title: Agent-readable documentation
description: Maintain generated discovery indexes and structured documentation metadata without creating another source of truth.
agent:
  type: maintainer-reference
---

Use this page when adding a documentation route or changing how coding agents
discover CAO guidance. Authoritative content remains in Markdown, Starlight
frontmatter, and the normative specifications under `specs/`.

## Generated outputs

The normal documentation build generates:

- `llms.txt` as a one-hop task router, `llms-small.txt` as compact project
  context, and `llms-full.txt` as the complete documentation corpus through
  `starlight-llms-txt`;
- `agent/llms.txt` as a compact index of prominent resources; and
- `agent/resources.json` as a deterministic compact index of every resource; and
- `index.json` beside each Starlight documentation route.

The root npm overrides replace only `starlight-llms-txt`'s `micromatch`
dependency with Picomatch. The plugin uses only the compatible `isMatch` API;
this removes the unused, vulnerable `braces` AST walkers (GHSA-vfj7-8cjw-p6xm)
without changing documentation generation. The focused
`tests/unit/docs-pattern-matching.test.mjs` suite checks this API boundary,
route matching, deeply nested patterns, and the absence of `braces`. Review
the override and these compatibility checks together when upgrading the plugin.

Do not edit or commit these outputs. The Pages workflow publishes them from the
same `dist/` tree as the human site.

Operational agent access routes to [Agent analysis](agent-analysis.md) at
`/agent-analysis/`, which describes authorized Activity artifacts, explicit
operational manifests, and bounded CLI/MCP queries. The dashboard under
`/cao/` is a synthetic enterprise demo, not an operational agent-summary
endpoint or bulk snapshot source. Documentation indexes must route operational
analysis to the access guide, not a demo agent-summary URL.

The validator limits `llms.txt` to 4 KiB, `llms-small.txt` to 64 KiB, and each
routed `skills/*/SKILL.md` entry point to 12 KiB. It also requires every common
task in `llms.txt` to resolve to exactly one skill in one hop and verifies that
every non-draft authoritative Markdown heading remains in `llms-full.txt`.
Specialized skill guidance belongs in a targeted `references/` document rather
than the skill entry point.

## Resource metadata

The local `starlight-agent` plugin derives each JSON representation from the
same content-collection entry used to render its HTML page. The projection
contains identity, type, title, description, canonical URL, bounded
relationships found in the Markdown, provenance, and freshness. It intentionally
omits the page body and all large operational datasets.

The projection hashes the authoritative Markdown source with SHA-256. Its
`source`, `provenance`, `freshness`, and `integrity` fields let clients locate
the exact source revision, determine when it last changed, and verify its bytes.

Every Starlight page advertises `agent/llms.txt` with `rel="describedby"` and
its JSON projection with `rel="alternate" type="application/json"`. Both links
use Astro's configured base path.

## Opting in and enriching a resource

All non-draft Starlight documents receive a JSON projection. Add the optional
`agent` frontmatter object to provide a stable domain `type`, mark an important
page as `prominent` in the scoped index, add bounded typed relationships, or
provide a reliable underlying `dataUpdatedAt` timestamp. These fields are the
plugin's extension hooks; do not create a separate resource registry.

Use `agent.binding` only when the page describes an existing dashboard catalog,
page, or named query. The gh-aw adapter derives exact `cao` CLI, local MCP, and
WebMCP bindings from that identifier; it does not define another operational
API. Build validation rejects unregistered commands and capabilities.

`freshness.generatedAt` is the representation build time.
`freshness.sourceCommittedAt` is the source commit time.
`freshness.dataUpdatedAt` appears only when authoritative underlying-data
freshness is supplied. Provenance identifies the repository, exact build commit
when available, source document, generator, and generator version.

Use [Agent analysis](agent-analysis.md) and its read-only CLI or MCP query
surface for large datasets and historical analysis instead of expanding static
JSON representations.

## Keeping the surfaces synchronized

There is no handwritten output registry. Synchronization is enforced at four
boundaries:

| Boundary | Source of truth | Enforcement |
| --- | --- | --- |
| Documentation routes | Astro content collection | The build requires every HTML-advertised JSON route and index entry to agree in both directions. |
| CLI bindings | `activity/commands/index.mjs` | The validator rejects unknown subcommands and malformed positional/option shapes. |
| CLI MCP bindings | `activity/mcp-server.mjs` | The validator checks capability names and arguments against each registered tool's input schema. |
| WebMCP bindings | Dashboard Language pages through `webmcp/manifest.js` | The validator requires the generated capability and page identifier to match the current dashboard manifest. |

`npm run docs:build` runs these checks on every documentation build, and
`npm run check` includes that build. The Pages workflow uses full Git history so
per-source timestamps are authoritative. The existing weekly SelfCare
documentation-discoverability worker samples the deployed index and a bound
resource; any publication drift becomes its stable tracking issue rather than a
second maintenance workflow.

When changing a CLI command, MCP schema, Dashboard Language page identifier, or
documentation binding, update the authoritative source and its focused tests in
the same pull request. Never patch generated JSON or compiled workflow output by
hand.

CLI `arguments.positional` values appear in order immediately after the
subcommand. Each `arguments.options` key is an option name; a `true` value emits
the key as a bare flag, while other values follow the option as its argument.
Bindings describe the interfaces at `provenance.commit`. An agent using a
different CAO revision must rediscover its local CLI or MCP catalog rather than
assuming forward compatibility.
