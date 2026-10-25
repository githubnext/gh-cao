---
title: Glossary
description: Definitions for Central Agentic Ops terminology.
---

CAO names the work from the operator's point of view and preserves the canonical [GitHub Agentic Workflows terminology](https://github.github.com/gh-aw/reference/glossary/) for its implementation. A **campaign** is the capability being supervised. A **coordinator** selects work for that campaign, a **worker** performs one bounded task, and an **AI agent** reasons within an agentic workflow through a selected engine. An **operator** is always a person.

```text
human operator
  └─ supervises campaign
	  ├─ coordinator (orchestrator workflow)
	  │    └─ dispatches work
	  └─ worker workflow
		  └─ performs one bounded task using an AI agent and engine
```

## Action level

The shared `explore`, `propose`, or `operate` classification applied to every dashboard-declared action (CLI action, row action, or generated view prompt) independent of its presentation or executor. `explore` is a read-only investigation, `propose` previews a request such as a prompt that can produce a pull request, and `operate` shows the underlying command and requires explicit per-run confirmation. A separate `ui` level covers native dashboard-local and account-session controls (such as resetting local storage or logging out) that carry no operational or repository authority and never generate an agent prompt or CLI command. See the [Dashboard Language specification](dashboard-language-specification.md#111-syntax-and-view-classes).

## Administrator step-up

A hosted-server requirement that every administrator operation comes from a fresh, assured interactive GitHub OAuth authentication. Fresh means within `CAO_ADMIN_REAUTH_MAX_AGE`, which defaults to 15 minutes. Assured means GitHub explicitly reported two-factor authentication, or the user's membership was confirmed in an operator-attested SAML SSO organization. Token refresh never extends step-up. See the [hosted CLI actions specification](https://github.com/githubnext/gh-cao/blob/main/specs/server-hosted-cli.md).

## Admission gate

The optional hosted-server check, served at `POST /api/admission`, that decides whether a participating gh-aw execution may start. It authenticates the isolated admission job with GitHub Actions OIDC, requires reviewed workflow enrollment and fresh AI Credit accounting, and returns policy-defined allowances. It is a cooperative start-time check, not a billing API or reservation system, and it never enables a campaign, widens repository scope, promotes review to live, or grants writes; those remain control-policy decisions. See [Set up the admission gate](admission-gate.md).

## Agent catalog

The protocol-independent listing of agent-facing dashboard pages and named Dashboard Language queries, derived once from the dashboard page definitions and shared by every agent transport (the `cao` CLI, the read-only CAO MCP server, and WebMCP) so none of them maintains a second catalog. See [Agent analysis](agent-analysis.md).

## AI agent

The reasoning component that interprets an agentic workflow's instructions, uses its configured tools, and generates outputs from repository context. GitHub Actions runs the AI agent through a selected engine. An AI agent is not the campaign itself or the human supervising it. See the canonical gh-aw definition of [AI Agent](https://github.github.com/gh-aw/reference/glossary/#ai-agent).

## Agentic workflow smell

An evidence-backed warning that a workflow may be harder to control, secure, operate, or justify than necessary; a reason to investigate, not proof of a defect. The dashboard classifies each observation as an agent smell, workflow smell, security finding, or control-plane smell based on what the evidence describes, and normalizes all four into unified Home attention signals.

## Automation

A general description for work performed with limited manual intervention. Automation is not a distinct CAO entity or workflow role. Prefer the specific term **campaign**, **coordinator**, **worker**, or **run** when naming something in the product or documentation.

## Agentic campaign

Continuous centralized agentic work that pursues your goals for your enterprise as a whole.

## Bug Bash

The recommended getting-started campaign for a new control repository: it selects at most one target repository, runs one bounded defect investigation, verifies the result adversarially, and hands a maintainer either one reviewable finding or an honest `no-finding`. Nothing is written to the investigated repository; the campaign's `bug-bash` orchestrator and `bug-bash-investigation` worker publish review evidence only to the control repository. See [Bug Bash](https://github.com/githubnext/gh-cao/blob/main/bug-bash/README.md).

## Canonical data

The consistent entities, identities, and relationships produced by applying the dashboard data model to published activity evidence. Canonical data is source-neutral derived state, not a new source of authority.

## CAO runtime

The curated production bundle of canonical Activity and Dashboard code that a control repository installs and executes. It is packaged from `runtime/package.json` by `npm run runtime:pack -- DESTINATION REVISION` (see `scripts/build-cao-runtime.mjs`) and built and attested by `.github/workflows/cao-runtime.yml` as a verified exact-SHA artifact. The Release workflow verifies that artifact and attaches it to the draft before immutable publication, providing stable release assets for stable version references; successful Actions artifacts retained for 90 days serve main/SHA references. The trusted materializer verifies GitHub signed build provenance for the catalog, signer workflow, and source and signer SHAs on a GitHub-hosted runner before replacing an installed repository's bounded canonical destinations, failing closed without source or npm fallback when an artifact is missing or unverifiable. Normal installation needs Node.js and authenticated GitHub CLI artifact/attestation access, not npm; explicit local source materialization remains a developer path using npm. See [Repository Layout and Installation](https://github.com/githubnext/gh-cao/blob/main/specs/repository-layout-and-installation.md).

## CAO validation

The Actions-only `cao-validate.yml` workflow, implemented in the shipped JavaScript runtime, that checks policy resolution against the production resolver, the installed gh-aw compiler version, strict compilation and generated-workflow drift, campaign workflow identity and enablement, `gh aw doctor`, and bounded trust-boundary security rules. It reports emitted findings (severity, category, remediation) and never rewrites a workflow artifact; exit code `0` means no errors, `1` means a finding met the requested severity threshold, and `2` means the validator itself could not complete. Request a run with `gh workflow run cao-validate.yml` and inspect its `cao-validation` report artifact. See [Validate the Control Plane](cao-cli.md#validate-the-control-plane).

## CLI command queue

A durable first-in-first-out queue of declared `cao` actions, separate from the collection task queue. Local admin and trusted canvas sessions share one queue per checkout in `.cao/cli-actions.sqlite`, and the PostgreSQL operational store can provide a deployment-scoped queue. Queued commands survive restart, interrupted executions are recorded as failed and never retried automatically, and the online dashboard and read-only previews do not initialize it. See the [dashboard README](https://github.com/githubnext/gh-cao/blob/main/dashboard/README.md#cli-command-queue) and the [operational storage specification](https://github.com/githubnext/gh-cao/blob/main/specs/server-operational-storage.md).

## Collection health

The `collection-health` registered runtime source: a bounded, read-only snapshot of the server-side webhook and collection profile's queue depth, pending tasks, dead letters, backfill state, and recent webhook and collection outcomes. It is exposed only through the authorized Dashboard Language query boundary, never stored in browser IndexedDB, and returns no raw error messages or credentials. See the Dashboard Language Specification, Section 5.4.

## Coordinator

The CAO operator-facing name for the workflow that selects and dispatches work for a campaign. The canonical gh-aw term is [Orchestrator Workflow](https://github.github.com/gh-aw/reference/glossary/#orchestrator-workflow). Workflow source, policy, campaign manifests, and other technical contracts use the role name `orchestrator`.

## Control plane

The repository that hosts CAO workflows and policy. It coordinates work across explicitly enrolled target repositories. This is CAO's implementation of the gh-aw [Central Control Plane](https://github.github.com/gh-aw/reference/glossary/#central-control-plane) pattern.

## Copilot canvas

The local, loopback-only **Central Agentic Ops** Copilot SDK surface that hosts the experimental dashboard **Query editor** page for authoring and previewing custom views. It is not enabled on the static website, hosted server, or ordinary local preview, and its authoring sessions have no filesystem, shell, MCP, or repository-write tools. See [Design queries in a Copilot canvas](agent-plugin.md#design-queries-in-a-copilot-canvas).

## Daily partition

A UTC-day partition of a high-volume canonical PostgreSQL table, such as runs. Owner-run bootstrap initializes the partitions and owner-run maintenance keeps historical, current, and 28 upcoming days provisioned, expiring only complete days at each retention cutoff; runtime opens provisioned state without DDL. See [Architecture](architecture.md).

## Dashboard Language

The declarative YAML vocabulary used to define dashboard queries, pages, views, and presentation. It keeps data selection and operational calculations in the dashboard data worker rather than in UI components. See the [Dashboard Language guide](dashboard-language.md) and [specification](dashboard-language-specification.md).

## Dashboard fragment

An authoring-time partial `dashboard` JSON document, listed in a root document's top-level `fragments` array, whose array fields (such as `queries`, `views`, and `pages`) are appended in declaration order to keep a coherent feature slice together. Fragments cannot include other fragments and are fully composed into the single deployed `dashboard.json` and `dashboard-pages/*.json` runtime format before validation and page chunking; a fragment is an authoring convenience, not a distinct runtime artifact. See the [dashboard README](https://github.com/githubnext/gh-cao/blob/main/dashboard/site/README.md).

## Debug mode

A `safe_output_mode: debug` request for exactly one manually launched `workflow_dispatch` worker run, never a persistent policy value, campaign mode, or orchestrator mode. Admission requires the authoritative dispatch event, a matching target and output repository, and a human actor with current write, maintain, or admin access to the control repository; it carries no dispatcher correlation fields and cannot be synthesized. Compiled shared control forces global gh-aw safe-output staging and suppresses automatic activation and failure issues for the run. See [Local Worker Debugging](local-debugging.md) and the control architecture specification's run-scoped debug request definition.

## Declarative query

A reusable result declared in `dashboard.queries` as a closed, structured projection over database tables or earlier queries, using only named clauses (`from`, `joins`, `filter`, `compute`, `aggregate`, `select`, `order-by`, `limit`) rather than SQL text, scripts, callbacks, or templates. Dashboard views must derive their data through declarative queries executed by the canonical data model's query engine and Web Worker; JavaScript-based dashboard views are not permitted. See the Dashboard Language Specification, Section 5.5.

## Dispatch

The bounded handoff by which a coordinator starts a worker with one selected target and a resolved control envelope. A dispatch is an event within a campaign run, not a campaign or agent.

## Engine

The runtime and provider integration used to execute an AI agent. The engine is selected in workflow frontmatter and is distinct from the agent's reasoning role, the workflow being executed, and the campaign being supervised. See the canonical gh-aw definition of [Engine](https://github.github.com/gh-aw/reference/glossary/#engine).

## External hosting

The explicit `generic` HTTP target with `listener: external` that lets a separately owned Go host embed CAO without duplicating its authentication, policy, or ingestion logic. The public `server/hosting/` facade exposes only `New`, `Start`, the complete `http.Handler`, and `Stop`; CAO keeps OAuth, session/CSRF authorization, webhook verification, proxy trust, rate limits, Redis, and background-task cancellation, while the host owns the HTTP listener and must drain it before CAO stops. External hosting introduces no runtime-loaded modules or rollout authority.

## gh cao

The operator-facing `gh cao` extension, installed with `gh extension install githubnext/gh-cao`. The curl installer is a fallback for SAML-token installation failures. It is built from the independent Go module under `cmd/gh-cao/` and `internal/cli/` and published as versioned `gh-cao-OS-ARCH` binaries with SHA-256 checksums. All extension commands, including policy initialization and modes, workflow enablement, campaign installation, onboarding and authentication, lifecycle, read-only analysis, and diagnostics, run natively in Go without Node.js, npm, or the installed CAO runtime. Actions pipeline commands and SQLite snapshot `repair-database` invoke `node activity/cao.mjs <command>` explicitly; onboarding and lifecycle commands, including hosted-executor App setup, are not available in Node. No shell launcher is installed. See [GitHub CLI extension](cao-cli.md#github-cli-extension).

## GitHub quota service

The CAO-side authority in `server/internal/githubquota` that coordinates GitHub API rate-limit quota across replicas. A bucket is one independently metered quota identified by GitHub App identity, installation, and rate-limit resource, never by a token; admission requires remaining minus reserved minus cost to stay at or above a requested floor, and administrators read 24-hour peak usage through the `github-quota-usage` runtime source. Existing collection budgets do not use this service yet.

## History campaign

The single campaign selected for historical operational-value reconstruction in one collector invocation, as defined by the operational-value history protocol. Its adapter supplies historical evaluations at earlier scheduled cadence instants in addition to the current observation every campaign adapter provides; the reconstruction orchestrator schedules, validates, retains, deduplicates, retires, and publishes these observations without reinterpreting or recomputing a supplied metric value. See [Operational-Value History Reconstruction Specification](https://github.com/githubnext/gh-cao/blob/main/specs/operational-value-history.md).

## Hosted CLI actions

An experimental hosted-server mode, enabled with `serve-hosted --experimental --hosted-cli-actions`, in which step-up administrators queue declared `cao` actions. A separate `execute-cli` process runs each queued action with a single-use, repository-scoped GitHub App installation token that is revoked when the command finishes. Executor replicas coordinate through a durable, fenced PostgreSQL queue lease. See the [hosted CLI actions specification](https://github.com/githubnext/gh-cao/blob/main/specs/server-hosted-cli.md).

## Live authority

The control repository's exclusive right to admit a `live` worker for a campaign and target, decided solely from `.github/workflows/cao.json` at the exact workflow SHA. A target repository's files cannot widen, narrow, or veto this decision.

## Maintenance Governance

The dashboard page for gh-aw compiler defaults at organization, enterprise, or repository scope. It is visible only to an authorized hosted administrator with a current, assured session. Editing stays disabled because hosted repository installation tokens cannot administer organization or enterprise variables; use `gh aw env` with suitable administrative credentials outside the dashboard. It does not change CAO rollout policy or control-plane authority.

## Marketplace

The read-only dashboard catalog of campaign packages resolved from an operator-ordered list of registries declared in `control-plane.marketplace.registries`. It shows normalized package metadata, provenance, and an immutable source coordinate, and only ever offers a copy-only `gh cao add` command; it never installs a package or contacts a registry from the browser. Package rows carry declaratively sorted and filtered verification, maintenance, popularity, installation, and adoption signals, each reported as a known value with its provenance source or `unknown`; these signals describe discovery only and never grant or infer control-plane authority. See [Browse Campaign Packages](marketplace.md).

## Operator

A person who configures, supervises, pauses, reviews, or evaluates campaigns. Do not use **operator** as a synonym for coordinator, orchestrator, worker, or agent.

## Orchestrator

The technical workflow role that discovers, filters, ranks, selects, and dispatches work within resolved policy. An orchestrator does not mutate target repositories directly. In operator-facing interfaces, call this workflow the **coordinator**. See the canonical gh-aw definition of [Orchestrator Workflow](https://github.github.com/gh-aw/reference/glossary/#orchestrator-workflow).

## Outcome

A later repository-state observation of what happened to a safe output, such as accepted, rejected, ignored, pending, or closed. An outcome is distinct from the status or conclusion of the workflow run that produced the output.

## Operational value

A campaign-defined, timestamped numeric metric for one repository and campaign. An installed campaign computes these records through its `operational-value.mjs`; operational value is not inferred from run volume, safe-output count, grader output, or activity alone.

## Operational grader

The run-scoped result produced by gh-aw's upstream `operational-value` grader protocol. The protocol identifier remains `operational-value` for compatibility, but CAO refers to the resulting grader evidence as an operational grader so it is not confused with campaign-defined repository operational value.

## Operational store

The provider-neutral storage contract in `server/internal/operational/` that exposes immutable per-feature cache, request-limit, OAuth, quota, collection, coordination, and diagnostic capabilities. PostgreSQL is the default adapter for a selector-free reviewed host, using only its own `cao_operational_*` tables and never the canonical dashboard entity/query schema; explicit Redis configuration remains supported, and an explicit, acknowledged single-process in-memory adapter is a bounded, volatile alternative that loses all operational state on restart. See [Server operational storage](https://github.com/githubnext/gh-cao/blob/main/specs/server-operational-storage.md).

## Public control repository

A control repository whose own visibility is public, so its policy, workflow runs, operational metadata, dashboard data, and review outputs are also public. Activity and Server Ingestion both fail closed and refuse to collect evidence when a public control repository's credentials can reach any non-public repository, reporting only a rejected-repository count. See [Control Repository Visibility](authentication.md#control-repository-visibility).

## Public-only monitoring

A setup profile for a public control repository that monitors currently public repositories with the workflow `GITHUB_TOKEN` and no App or PAT. Policy records it as `control-plane.activity.public-only: true` with exact repository names, never a wildcard. Activity verifies public visibility and unchanged repository identity before discovery and again before log collection, and blocks publication if either check fails. It grants no campaign activation, safe output, or live authority. See [Public-only monitoring quickstart](authentication.md#public-only-monitoring-quickstart).

## Repair database

The Node-only `node activity/cao.mjs repair-database` command, also available as the `cao repair-database` bin, that repairs a SQLite snapshot and applies retention. It is not a read-only check: it creates a backup first and may prune expired or orphaned records, and it is distinct from `gh cao doctor`. See the [CLI reference](cao-cli.md).

## Repo memory

The canonical gh-aw capability that persists files with unlimited retention in a dedicated `memory/<campaign-slug>` Git branch, distinct from the 7-day `cache-memory`. CAO campaigns configure it with `repo-memory.branch-name` so a coordinator can read and update bounded, advisory cross-run state through `$GH_AW_MEMORY_DIR` before dispatch selection; workers share the branch only when their work benefits from that shared history. Repo memory is never policy, target authority, credential storage, or a substitute for current repository evidence. See the canonical gh-aw definition of [Repo Memory](https://github.github.com/gh-aw/reference/glossary/#repo-memory).

## Run

One execution of a coordinator, worker, or standalone workflow. A coordinator run may produce many dispatches; each dispatch starts a separate worker run. A run records activity and evidence, but successful completion alone does not prove operational value. Use **run** rather than **session**: a run is the canonical execution entity, and canonical Domain, Tool, Audit, and Issue records link directly to it.

## Run-linked detail

The Domain, Tool, Skill, Friction, Audit, Issue, experiment assignment, grader observation, and eval observation records attached to a Run. Browser IndexedDB and the Activity SQLite projection expire run-linked detail after a bounded 7-day window by default while retaining the Run and Operational Value records themselves for 30 days; the Go server's canonical Postgres schema expires the same run-linked tables independently from run retention on its own configurable window. Expiring run-linked detail MUST NOT remove its retained Run or the Run's structural parents. See `specs/dashboard-data.md` and [Activity](activity.md).

## Rollout mode

The effective mode in which a campaign runs for an admitted target: `review`, `live`, or `unknown` when retained evidence does not identify the mode. Review mode directs safe outputs to a review destination; live mode requires explicit authority in the control repository's reviewed policy.

## Safe output

A declared, bounded way for a workflow to produce an external effect, such as creating an issue or dispatching a worker. See the canonical gh-aw definition of [Safe Outputs](https://github.github.com/gh-aw/reference/glossary/#safe-outputs).

## Standalone workflow

An agentic workflow that participates directly in CAO without a campaign, orchestrator, or worker. It imports shared control with the `standalone` role, declares its exact compiled workflow slug, and runs only when `control-plane.workflows.<slug>` admits it. It receives the same policy validation, bounded repository admission, and per-target mode and output routing as an orchestrator.

## Statistical runs

The `statistical-runs` source: a separate, bounded sampling layer over the current canonical snapshot that never replaces exact `runs`, retention, or linked detail. Selection runs inside the canonical storage query boundary or PostgreSQL publication transaction against a recorded logical UTC watermark, so every consumer of a snapshot and sampling policy shares one frame. It draws a deterministic, seeded sample of terminal Runs from the seven days before the watermark, stratified by execution UTC date and outcome, and is marked unavailable rather than exact-empty when no recorded clock or sampling input limit is exceeded. See the Dashboard Language Data Specification, Section 5.1.1.

## Target repository

A repository enrolled for a campaign. A target can provide data and receive declared safe outputs, but does not run the control plane's workflows and does not declare live authority in its own files.

## Unified session

gh-aw's version-1 `usage/aw_session.jsonl` evidence for a run. CAO collection validates it against the run's execution identity and stores only a safe projection of allowlisted event metadata, never message bodies, prompts, or tool inputs and outputs. An available unified session replaces legacy MCP, firewall, and skill event projections for that run; when none exists, the legacy parsers remain in use. See the [gh-aw JSONL mapping specification](https://github.com/githubnext/gh-cao/blob/main/specs/dashboard-gh-aw-jsonl-mapping.md).

## Webhook gateway

An opt-in, disabled-by-default GitHub App webhook receiver and durable dispatcher that routes authenticated deliveries to explicitly approved workflow subscriptions in a control repository. It identifies sources and destinations by immutable IDs rather than names, and is separate from the dashboard collector's webhook endpoint. CAO rollout policy and gh-aw capabilities remain independent ceilings; the gateway grants no permission or target authority. See [RFC-033](https://github.com/githubnext/gh-cao/blob/main/specs/rfc-033-webhook-gateway.md).

## WebMCP

The generated, read-only browser tool interface that exposes each agent-facing dashboard page as one `cao_<page id>` tool, so a browser-based AI agent can discover and run dashboard pages the way an operator does. WebMCP tools are generated adapters over the same page definitions and query execution path as human rendering, so there is no second tool catalog to maintain; it is experimental and available only in browsers that implement the underlying API. See [WebMCP](dashboard-webmcp.md).

## Workflow Value collection

The current bounded record of whether outcome measurement is registered and healthy for one exact repository and workflow filename stem. It carries the registration (`enabled`, `disabled`, or `unconfigured`), the latest decision and state (such as `never-captured`, `deferred`, `failed`, or `available`), and the last actual collector attempt, each with its own clock. It is retained independently of operational value evidence and does not itself measure outcomes, promote completeness or freshness, or authorize running a target workflow. See [Dashboard data model](dashboard-data-model.md).

## Worker

A workflow that receives one selected target and performs one bounded task for a campaign. Workers revalidate their control envelope and can only narrow the policy they receive. **Worker** is both the operator-facing and technical term; it is not synonymous with AI agent or engine because it describes the workflow's role. See the canonical gh-aw definition of [Worker Workflow](https://github.github.com/gh-aw/reference/glossary/#worker-workflow).
