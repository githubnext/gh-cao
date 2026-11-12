import { existsSync } from 'node:fs';
import path from 'node:path';
import { doctorSqliteDatabase } from '../../dashboard/site/src/data/storage/sqlite-doctor.js';
import { operationalValueReserve, REPOSITORY_COORDINATE, runOperationalValue } from '../operational-value.mjs';
import { runProblemClustering } from '../problem-clustering.mjs';
import { hasComputation, queryComputation } from '../computations/index.mjs';
import { DEFAULT_DATABASE_PATH, DEFAULT_SHARDS_PATH, USAGE } from '../cli-usage.mjs';
import { commandHandlers } from '../commands/index.mjs';
import { sqliteRetention } from '../sqlite-retention.mjs';
import { DAY_MS, UsageError, parseOptions, rawQueryFromStdin, option, rejectUnknownOptions, ttlDays, retentionWindowMs, runRetentionWindowMs, runTtlDays } from './options.mjs';
import { DEFAULT_COMPACTED_JSONL_SHARD_BYTES, auditJsonlDirectory, compactJsonlShards } from './jsonl.mjs';
import { databaseCounts, ingestGhAwLogDirectory, ingestJsonlShardDirectory, ingestNormalizedShardDirectories, ingestJsonlFile, createDatabase } from './ingestion.mjs';
import { downloadDeployedDashboardData } from './download.mjs';
import { hashActivityPayloads } from './payloads.mjs';
import { queryCanonicalData, queryGhData, queryRawCanonicalData } from './queries.mjs';
import { updateIssueStatuses } from './issue-status.mjs';
import { discoverWorkflows, pruneDashboardFile, analyzeDashboardComplexityFile } from './dashboard.mjs';

// Commands whose first argument may be an identifier instead of an option.
const POSITIONAL_COMMANDS = new Set(['dashboard-complexity', 'pages', 'query-info', 'query', 'prompt']);

const RETIRED_OPERATOR_COMMANDS = new Map([
  ['init', 'gh cao init'],
  ['setup', 'gh cao setup'],
  ['setup-auth', 'gh cao setup auth'],
  ['add', 'gh cao add CAMPAIGN (repository enrollment: gh cao repos add OWNER/REPO)'],
  ['mode', 'gh cao campaigns mode'],
  ['enable', 'gh cao campaigns enable'],
  ['disable', 'gh cao campaigns disable'],
  ['update', 'gh cao update'],
  ['upgrade-gh-aw', 'gh cao upgrade-gh-aw'],
]);

export async function runCli(arguments_, input = process.stdin, { signal } = {}) {
  const [command, ...rawOptionArguments] = arguments_;
  const replacement = RETIRED_OPERATOR_COMMANDS.get(command);
  if (replacement) throw new UsageError(`Node cao ${command} has been retired; use ${replacement}`);
  if (!command || command === '--help' || command === 'help') return USAGE;
  if (command === 'doctor') throw new UsageError('Unknown command: doctor; use repair-database for SQLite backup, repair, and retention');
  if (command === 'quickstart') throw new UsageError(`Unknown command: ${command}`);
  const handler = commandHandlers.get(command);
  if (!handler) throw new UsageError(`Unknown command: ${command}`);
  const optionArguments = [...rawOptionArguments];
  const positional = POSITIONAL_COMMANDS.has(command) && optionArguments[0] && !optionArguments[0].startsWith('--')
    ? optionArguments.shift()
    : undefined;
  const resource = command === 'gh' ? optionArguments[0] : undefined;
  if (command === 'gh' && (!resource || resource === 'help' || resource === '--help')) return USAGE;
  const computation = command === 'computation' ? optionArguments[0] : undefined;
  if (command === 'computation' && (!computation || computation === 'help' || computation === '--help')) return USAGE;
  const options = parseOptions(
    command === 'gh' || command === 'computation' ? optionArguments.slice(1) : optionArguments
  );
  if (options.help) return USAGE;
  const rawQuery = command === 'query' && options.stdin
    ? await rawQueryFromStdin(options, input)
    : undefined;
  const databasePath = option(options, 'database', false) || DEFAULT_DATABASE_PATH;
  if (['query', 'mcp'].includes(command) && options.database === undefined
    && (!existsSync(databasePath) || ((command === 'mcp' || positional || options.id)
      && !existsSync(path.join(path.dirname(databasePath), 'payload-hashes.json'))))) {
    throw new UsageError(`No downloaded CAO snapshot at ${databasePath}; run cao download before querying, or pass --database FILE for an explicitly prepared database`);
  }
  if (command === 'prompt' && options.database !== undefined && !existsSync(databasePath)) {
    throw new UsageError(`No CAO snapshot at ${databasePath}; pass --database FILE for an existing snapshot or omit it for a definition-only prompt`);
  }
  const databaseCommands = new Set(['issue-status', 'gh', 'computation', 'operational-value', 'ingest', 'ingest-jsonl', 'query', 'mcp']);
  const indexedDB = databaseCommands.has(command) || (command === 'prompt' && options.database !== undefined)
    ? await createDatabase(databasePath) : undefined;
  return handler({
    options,
    positional,
    resource,
    computation,
    rawQuery,
    databasePath,
    indexedDB,
    signal,
    UsageError,
    DEFAULT_SHARDS_PATH,
    DEFAULT_COMPACTED_JSONL_SHARD_BYTES,
    REPOSITORY_COORDINATE,
    option,
    rejectUnknownOptions,
    ttlDays,
    runTtlDays,
    sqliteRetention: (options) => sqliteRetention(retentionWindowMs(options), runRetentionWindowMs(options), DAY_MS),
    retentionWindowMs,
    downloadDeployedDashboardData,
    discoverWorkflows,
    analyzeDashboardComplexityFile,
    pruneDashboardFile,
    auditJsonlDirectory,
    compactJsonlShards,
    hashActivityPayloads,
    doctorSqliteDatabase,
    runProblemClustering,
    updateIssueStatuses,
    queryGhData,
    hasComputation,
    queryComputation,
    runOperationalValue,
    operationalValueReserve,
    ingestGhAwLogDirectory,
    ingestNormalizedShardDirectories,
    ingestJsonlFile,
    ingestJsonlShardDirectory,
    databaseCounts,
    queryRawCanonicalData,
    queryCanonicalData,
  });
}
