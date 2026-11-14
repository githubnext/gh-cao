# ADR 20939: Derive Local Dashboard Workflow Roles from `gh aw list` and Checked-in Campaign Declarations

## Status

Draft

## Context

The local dashboard (`gh cao dashboard`) collected repository workflow inventory from GitHub but did not show the repository's agentic workflows or campaigns. Collected workflow records carried no `role`, and campaign declarations in the local checkout were never read.

`specs/dashboard-data.md` already sets the boundary rules this change must follow. Campaign configuration and declared control-plane workflow metadata are inventory inputs (§5.4). A workflow record keeps only an optional `campaignId` foreign key to its owning Campaign (§10.1). A local workflow without source provenance stays visible as local, with no synthetic Campaign membership. Missing or incomparable source evidence stays `unknown`.

Pull request #20939 adds two inputs to the local collector:

- `gh aw list --repo owner/repo --json` for each collected repository. The pull request body states that this output is not source evidence.
- For the local checkout only, each top-level `<campaign>/cao.json`, read through the existing validated `readCampaignDeclaration`, plus optional `<campaign>/aw.yml` display metadata.

## Decision

1. **Standalone classification comes from the `gh aw list` result.** The collector runs `gh aw list` once per collected repository, passing a plain `owner/repo`. Listed workflow IDs are normalized to lowercase without the `.md` suffix. A workflow that is not a campaign member and is listed by `gh aw list` receives `role=standalone`. A non-list response, or an empty, path-like, or `..` identity, fails that repository's collection and preserves the previous snapshot. When `GH_HOST` names a host other than github.com, the collector fails closed, because `gh aw list` cannot be pinned to that host.
2. **Campaign membership comes only from the local checkout's declarations.** For the local checkout's repository, each top-level directory with a valid `cao.json` produces one campaign record with the ID `campaign:dashboard-sources:<slug>`. Declared orchestrators receive `role=orchestrator`, declared workers receive `role=worker`, and both receive `campaignId`. Entries under `standalone-workflows` receive no campaign membership and become `standalone` only when `gh aw list` reports them. Remote repositories receive no campaign membership, so their membership is recorded as unknown.
3. **Conflicting claims fail closed.** A workflow declared by two campaigns fails collection instead of selecting one owner.
4. **Source provenance is not inferred.** Workflows classified from `gh aw list` keep `sourceKind` unset, which means unknown.

## Alternatives Considered

- **Infer `sourceKind` for standalone workflows from the `gh aw list` result.** Not chosen. The pull request states that `gh aw list` output is not source evidence. `specs/dashboard-data.md` requires source kind and coordinates to come from source evidence, and leaves them `unknown` when that evidence is absent.
No other alternative is supported by the pull request evidence. Whether fetching remote repositories' `cao.json` through the GitHub API was considered and rejected for cost, authority, or access reasons is Not inferable from current pull request evidence.

## Consequences

**Positive:**

- The local dashboard can show standalone and campaign workflows. Roles and `campaignId` follow the §10.1 foreign-key model, and campaign records come from declared sources.
- Invalid listings, host mismatches, malformed campaign declarations, and duplicate claims stop collection instead of publishing guessed membership. The collector keeps the previous snapshot in these cases, consistent with the `specs/dashboard-data.md` rule that a failed refresh must not publish a complete maintenance payload.
- Standalone classification and campaign membership stay separate. A workflow listed under `standalone-workflows` is not made campaign-owned.

**Negative:**

- Remote repositories show no campaign membership, so campaign views are complete only for the local checkout.
- Each collected repository adds one `gh aw list` call, so collection depends on the `gh aw` CLI being available. Whether every operator environment provides it is Not inferable from current pull request evidence.
- A malformed `cao.json` or a duplicate campaign claim blocks that repository's collection until it is fixed, so the dashboard may keep showing an older snapshot.
- The collector reads the checkout's working tree, so uncommitted declarations can appear as campaign membership. Whether membership should instead follow the committed control-repository policy is Not inferable from current pull request evidence.
- The pull request notes that the local server sometimes logs an ingestion failure that keeps previous data. This change does not address that failure.
