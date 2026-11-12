---
name: analyze-cao
description: Download and query CAO activity data with gh cao and retained pipeline tools.
allowed-tools: bash jq
metadata:
  argument-hint: "[dashboard-data-url-or-owner/repo]"
  version: "1.0.0"
---

# Analyze CAO Data

In agentic workflows, query explicitly prepared snapshots with an already
available native `gh cao` or read-only MCP. Never build or install the `gh cao`
extension as a fallback. Follow [workflow runtime](../cao-cli/references/workflow-runtime.md);
the acquisition procedure below is for separately authorized operator use.

## Procedure

1. Choose shell access (`gh cao`) or MCP-only access (`cao_catalog`, then
   `cao_query`).
2. Establish snapshot freshness and completeness.
3. Prefer a reviewed named query; otherwise use a bounded canonical collection
   query.
4. Preserve availability and freshness metadata in the result.
5. Report source identifiers, timestamps, filters, and uncertainty.

## Deterministic boundary

Use `gh cao download` and reviewed `gh cao query`
commands for acquisition, filtering, and projection. The agent
chooses the question and interprets availability, freshness, completeness, and
results. Do not reproduce Dashboard Language queries or filter canonical rows
in ad hoc code when an existing CLI query can answer the request.

## Data contract

- Treat downloaded data as local, disposable development evidence, not schema authority.
- Keep code and conclusions aligned with the dashboard data specifications and the activity shard schema.
- Preserve the distinction between missing, stale, partial, zero, and complete evidence.
- Do not infer rollout authority, target-writing authority, operational value, or repository eligibility from activity data.
- Never print credentials, tokens, authorization headers, raw prompts, transcripts, or other secret values.

## Shell procedure

Decide how you reach the data first:

```
Do you have shell access?
  YES           -> gh cao, steps 1-10 below
  NO / MCP only -> cao_catalog to discover pages and queries, then cao_query
```

Without a shell, use `cao_catalog` and `cao_query` against the prepared snapshot.
See [Agent analysis](https://githubnext.github.io/gh-cao/agent-analysis/).

1. Verify `gh cao --version`; operators may use the [installer](https://github.com/githubnext/gh-cao/blob/main/docs/cao-cli.md#github-cli-extension) if absent.
2. With Actions read access, select a successful default-branch Activity run:

   ```bash
   gh run download RUN_ID --repo OWNER/CONTROL --name cao-activity-index --dir .cao
   ```

   This supplies verified canonical JSONL under `.cao/`, not a database file.
   An authorized operator must ingest it into the selected PostgreSQL namespace.
3. Or select a trusted operational manifest:

   ```bash
   gh cao download --url URL
   ```

   `DASHBOARD_DATA_URL=URL` is equivalent. URLs must end in `/payload-hashes.json`.
   No default Pages source, directory discovery, or hosted export is implied.
   Synthetic manifests and missing canonical checksums fail closed. Pages supplies
   neither operational exports nor an agent summary. Use `--output DIRECTORY`
   only for an explicit destination.
4. Check query availability, completeness, freshness, and source scope before
   treating the snapshot as complete. Require `CAO_POSTGRES_URL` and its exact
   `CAO_DATABASE_NAMESPACE`; queries do not bootstrap storage or start containers.
   Missing evidence is not zero.
5. Prefer the reviewed named queries the dashboard itself renders. Discover and run them with:

   ```bash
   gh cao pages
   gh cao queries
   gh cao query-info QUERY_ID
   gh cao query QUERY_ID --limit 50
   ```

   Add `--json` to any of these for machine-readable output, and `--param NAME=VALUE`
   to narrow a query by a declared parameter. Prefer a query whose `execution.local`
   is `true`, and preserve the `availability`, `completeness`, `freshness`, and
   `as-of` metadata each result carries.
6. When no named query answers the question, query PostgreSQL through canonical collection names:

   ```bash
   gh cao query --collection runs --limit 20
   ```

7. Add filters with repeated exact-match `--where FIELD=VALUE` options. Use dotted fields for nested values when needed:

   ```bash
   gh cao query \
     --collection runs \
     --where conclusion=failure \
     --limit 20
   ```

   Use `--postgres-url` only to select another explicitly authorized connection.
8. For generated or complex queries, pass a raw Dashboard Language query object through standard input:

   ```bash
   jq -n \
     --arg conclusion failure \
     '{
       name: "failed-runs",
       from: "runs",
       filter: {predicates: [{field: "conclusion", equals: $conclusion}]},
       limit: 20
     }' |
     gh cao query --stdin
   ```

   `--database FILE` may be combined with `--stdin`; do not combine `--collection`, `--id`, `--where`, or `--limit` with it.
9. Query by ID when the user asks about one known record:

   ```bash
   gh cao query --collection sessions --id SESSION_ID
   ```

10. If database health is in doubt, treat the snapshot as unavailable and
    reacquire verified evidence through an authorized preparation step. The
    legacy JavaScript audit and repair commands are retired; never mutate a
    workflow's supplied snapshot.

## Collections

Use `cao help` for the current command syntax and collection list. The canonical query collections are:

- `repositories`
- `workflows`
- `runs`
- `jobs`
- `sessions`
- `events`
- `transactions`

## Analysis guidance

- Start broad with counts and recent records, then narrow with `--where` filters or record IDs.
- Use the native PostgreSQL canonical query boundary for summaries and joins outside the browser.
- Use JSONL audit results to explain source completeness and enrichment coverage.
- Attribute findings to the collection, record ID, workflow, run ID, and timestamp whenever available.
- Report uncertainty explicitly when evidence is unavailable, stale, partial, or outside the requested scope.
- Keep downloaded `.cao/` files uncommitted.
