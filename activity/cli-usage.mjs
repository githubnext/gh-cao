/**
 * Default locations and the usage text of the `cao` CLI.
 *
 * The usage text is the entry point an unfamiliar agent reads first, so it is
 * kept beside the defaults it documents.
 */

export const ENTITY_COLLECTIONS = [
  'repositories',
  'workflows',
  'runs',
  'domains',
  'tools',
  'skills',
  'friction',
  'audits',
  'issues',
  'operationalValues',
  'operationalValueEvidence',
  'workflowValueCollections',
  'experiments',
  'experimentAssignments',
  'graders',
  'graderObservations',
  'evals',
  'evalObservations'
];
export const NORMALIZED_COLLECTIONS = ['campaigns', ...ENTITY_COLLECTIONS];
export const QUERY_COLLECTIONS = [...ENTITY_COLLECTIONS, 'transactions'];
export const DEFAULT_DEPLOYED_DATA_URL = 'https://githubnext.github.io/gh-cao/cao/payload-hashes.json';
export const DEFAULT_OUTPUT_DIRECTORY = '.cao';
export const DEFAULT_SHARDS_PATH = `${DEFAULT_OUTPUT_DIRECTORY}/gh-aw-logs-shards`;
export const DEFAULT_DATABASE_PATH = `${DEFAULT_OUTPUT_DIRECTORY}/gh-aw-logs.sqlite`;
export const DEFAULT_ACTIVITY_STATS_WORKFLOW = 'cao-activity.yml';
export const DEFAULT_ACTIVITY_STATS_ARTIFACT = 'cao-activity-index';
export const DEFAULT_ACTIVITY_STATS_LIMIT = 5;
export const DEFAULT_GH_LIMIT = 30;

export const USAGE = `Usage:
  cao discover-workflows --control-settings FILE --inventory FILE --output FILE --repo OWNER/REPO [--root DIRECTORY] [--offline]
  cao dashboard-complexity [QUERY_ID] --input FILE [--database FILE] [--format json|markdown] [--limit COUNT]
  cao prune-dashboard --input FILE [--output FILE]
  cao ingest-jsonl [--database FILE] [--input FILE|--input-dir SHARD_DIRECTORY|--runs-dir DIRECTORY --records-dir DIRECTORY] [--context CONTEXT_JSON] [--retention-days DAYS|all] [--run-retention-days DAYS|all]
  cao audit-jsonl [--input-dir SHARD_DIRECTORY]
  cao compact-jsonl --input-dir SHARD_DIRECTORY --group OWNER/REPOSITORY=SHARD_PREFIX [--group OWNER/REPOSITORY=SHARD_PREFIX...] [--max-bytes BYTES] [--exclude-out-of-scope]
  cao recycle-cache --input-dir SHARD_DIRECTORY [--retention-days DAYS|all] [--max-bytes BYTES]
  cao issue-status [--database FILE] --input-dir SHARD_DIRECTORY [--batch-size COUNT] [--graphql-cost-budget POINTS] [--graphql-min-remaining POINTS]
  cao pages [PAGE_ID] [--dashboard FILE] [--json]
  cao queries [--dashboard FILE] [--json]
  cao query-info QUERY_ID [--dashboard FILE] [--json]
  cao prompt QUERY_ID [--dashboard FILE] [--param NAME=VALUE...] [--database FILE]
  cao query QUERY_ID [--database FILE] [--dashboard FILE] [--param NAME=VALUE...] [--limit COUNT]
  cao query [--database FILE] (--collection NAME [--id ID] [--where FIELD=VALUE] [--limit COUNT] | --stdin)
  cao mcp [--database FILE] [--dashboard FILE] [--host HOST] [--port PORT]
  cao computation intelligence [--database FILE] [--inventory FILE] [--campaign SLUG] [--previous FILE] [--feedback FILE]
  cao computation runtime-health [--database FILE] [--inventory FILE] [--campaign SLUG] [--diagnose]
  cao operational-value [--database FILE] [--root DIRECTORY] [--output FILE] [--timestamp TIME] [--repository OWNER/REPO] [--campaign SLUG] [--retention-days DAYS|all] [--history-campaign SLUG] [--max-github-api-rate-limit LIMIT] [--local-only]
  cao cluster-problems [--database FILE] [--root DIRECTORY] [--timestamp TIME]
  cao repair-database [--database FILE] [--ttl-days DAYS|all] [--run-ttl-days DAYS|all]
    Mutates SQLite: backs up before repairs and prunes expired/orphaned records, even without corruption.
  cao validate [--json] [--strict-warnings]
  cao download [--url URL] [--output DIRECTORY] [--manifest-sha256 DIGEST]
  cao hash-payloads [--database FILE] [--shard-dir SHARD_DIRECTORY] [--normalized-dir DIRECTORY] [--runs-dir DIRECTORY] [--records-dir DIRECTORY] [--inventory FILE] [--output FILE] [--max-bytes BYTES] [--max-cache-bytes BYTES]
  cao activity-stats [--repo OWNER/REPO] [--workflow FILE] [--artifact NAME] [--limit COUNT] [--keep] [--output FILE]
  cao validate-activity-data --database FILE --shard-dir DIRECTORY --payload-hashes FILE --control-settings FILE --inventory FILE --memory-manifest FILE
  cao gh runs [--database FILE] [--repo OWNER/REPO] [--workflow NAME|FILE] [--status STATUS] [--since TIME] [--until TIME] [--limit COUNT]
  cao gh issues [--database FILE] [--repo OWNER/REPO] [--workflow NAME|FILE] [--since TIME] [--until TIME] [--limit COUNT]
  cao gh prs [--database FILE] [--repo OWNER/REPO] [--workflow NAME|FILE] [--since TIME] [--until TIME] [--limit COUNT]

Manual artifact import (supported; not used by the Activity or Dashboard pipelines):
  cao ingest [--database FILE] --context CONTEXT_JSON --logs LOG_DIRECTORY [--retention-days DAYS|all] [--run-retention-days DAYS|all]
  Imports an extracted gh-aw artifact directory with its context JSON into SQLite.
  The explicit ingest command remains supported; only the old positional CONTEXT_JSON LOG_DIRECTORY form is retired.

Query local CAO data as JSON. Download the deployed snapshot before querying:
  cao download
  cao dashboard-complexity --input dashboard/site/dashboard.json
  cao dashboard-complexity campaign-inventory --input dashboard/site/dashboard.json
  cao prune-dashboard --input dashboard.json --output dashboard.pruned.json
  cao issue-status --input-dir .cao/gh-aw-logs-shards --graphql-cost-budget 25 --graphql-min-remaining 500
  cao computation intelligence
  cao computation intelligence --campaign dependabot --previous prior-intelligence.json --feedback decision-feedback.json
  cao computation runtime-health
  cao computation runtime-health --campaign dependabot
  cao computation runtime-health --campaign dependabot --diagnose
  cao operational-value --output .cao/gh-aw-logs-shards/operational-values.jsonl --retention-days 30 --history-campaign optimization --max-github-api-rate-limit -2000
  cao cluster-problems
  cao gh runs -R githubnext/gh-cao -w cao-activity --status failure --since 2026-09-01 --until 2026-09-15
  cao gh issues -R githubnext/gh-cao --since 2026-09-01
  cao gh prs -R githubnext/gh-cao -w cao-activity -L 10

Operator onboarding and lifecycle commands run natively in gh cao.
Use gh cao --help; workflow and campaign installation is gh cao add SOURCE.

Agent analysis:
  These are supported evidence APIs for agents inside agentic workflows.
  Invoke the installed Node runtime as node activity/cao.mjs <command>.
  gh cao owns human/operator commands; cao-dashboard owns server hosting and
  administration. Similar operator reads do not replace these workflow APIs.
  Workflow agents use queries, query-info, prompt, query, gh runs/issues/prs,
  computation, mcp, and download without building or installing gh cao.
  Download the current CAO snapshot:
    cao download
  Discover dashboard pages:
    cao pages
  Discover named queries:
    cao queries
  Inspect a query:
    cao query-info QUERY_ID
  Render a query prompt (use --database FILE to include a bounded data preview):
    cao prompt QUERY_ID [--param NAME=VALUE]
  Run a named query:
    cao query QUERY_ID [--param NAME=VALUE]
  Start the read-only MCP server for agents without a shell:
    cao mcp
  The MCP endpoint speaks plain HTTP on http://127.0.0.1:8765/mcp and carries no
  credentials; reach it over loopback or an isolated job-local network.
  Every discovery command emits JSON with --json; diagnostics stay on stderr.
  Named queries are the reviewed Dashboard Language queries of the deployed
  dashboard, so the CLI, the browser, and MCP return the same evidence.
  Bootstrap guide: https://githubnext.github.io/gh-cao/agent-analysis/

Resources:
  runs    Workflow runs executed in --repo
  issues  Issues created through safe outputs in the target --repo
  prs     Pull requests created through safe outputs in the target --repo

gh query options:
  -R, --repo       Exact OWNER/REPO; execution repo for runs, target repo for issues and prs
  -w, --workflow   Producing workflow name, path, file name, or ID
  -s, --status     Runs only: workflow status or conclusion, such as completed or failure
  -L, --limit      Maximum results, newest first (default ${DEFAULT_GH_LIMIT})
  --since          Include results at or after ISO 8601 time (example: 2026-09-01T12:00:00Z)
  --until          Include results at or before ISO 8601 time; a date includes the full day
  --database       Local SQLite snapshot (default ${DEFAULT_DATABASE_PATH})

Data preparation:
  cao download writes the deployed JSONL and query-ready SQLite snapshot to ${DEFAULT_OUTPUT_DIRECTORY}/.
  To query other gh-aw JSONL shards, first run cao ingest-jsonl --input-dir SHARD_DIRECTORY [--database FILE].

Active ingestion and packaging:
  compact-jsonl compacts collected raw JSONL shards per repository.
  hash-payloads normalizes and consolidates raw shards into run/record shards and computes payload checksums.
  ingest-jsonl imports raw JSONL or normalized run/record shards into SQLite.

Collections: ${QUERY_COLLECTIONS.join(', ')}

Query stdin JSON:
  {"name":"failed-runs","from":"runs","filter":{"predicates":[{"field":"conclusion","equals":"failure"}]},"limit":20}

Download defaults:
  URL        DASHBOARD_DATA_URL or ${DEFAULT_DEPLOYED_DATA_URL}
  MANIFEST   Optional DASHBOARD_MANIFEST_SHA256 trust anchor
  DIRECTORY  ${DEFAULT_OUTPUT_DIRECTORY}
  SHARDS     ${DEFAULT_SHARDS_PATH}
  DATABASE   ${DEFAULT_DATABASE_PATH}

Activity stats defaults (uses the "gh" CLI and requires GH_TOKEN):
  REPO      GITHUB_REPOSITORY
  WORKFLOW  ${DEFAULT_ACTIVITY_STATS_WORKFLOW}
  ARTIFACT  ${DEFAULT_ACTIVITY_STATS_ARTIFACT}
  LIMIT     ${DEFAULT_ACTIVITY_STATS_LIMIT}

Operational value scripts:
  cao operational-value discovers <package>/operational-value.mjs below --root.
  Each script receives one JSON request on stdin and emits JSONL records with
  timestamp, repository, valueId, and a finite numeric value.
  --history-campaign queries missing cadence observations within the retention
  window from prefetched evidence and writes them only when --output is present.

Problem clustering scripts:
  cao cluster-problems discovers <package>/problem-clustering.mjs below --root.
  Each script receives one JSON request on stdin and emits bounded JSONL problem
  records with actionable fixPrompt fields. Successful output replaces that
  package's rows in cao_problems.

`;
