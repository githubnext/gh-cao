import { spawn, spawnSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { readFile, readdir, mkdir, rename, rm, stat, writeFile } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { executeCanonicalQuery } from './canonical-query.mjs';
import { canonicalTimestamp, operationalValuePeriod } from '../dashboard/site/src/data/model/schema.js';
import { readCollection } from '../dashboard/site/src/data/storage/indexeddb.js';

export const REPOSITORY_COORDINATE = /^[A-Za-z0-9][A-Za-z0-9-]*\/[A-Za-z0-9._-]+$/;
const OPERATIONAL_VALUE_ID = /^[a-z0-9]+(?:[._-][a-z0-9]+)*$/;
const OPERATIONAL_VALUE_SLUG = /^[a-z0-9]+(?:-[a-z0-9]+)*$/;
const DAY_MS = 86_400_000;
const BASELINE_OBSERVATIONS = 3;
const MAX_HISTORY_OBSERVATIONS = 366;
const WORKER_TIMEOUT_MS = 2 * 60 * 1000;
const RATE_LIMIT_TIMEOUT_MS = 30 * 1000;
// Older/custom adapters may ignore localOnly. Admit only the reviewed standard adapter.
export const LOCAL_VALUE_ADAPTER_DIGEST = '771ab1363f035e771684fc334c38dfeeee1855d691758055d4637373d3617416';
export const MAX_LOCAL_VALUE_BYTES = 16 * 1024 * 1024;
const WORKER_ENVIRONMENT = [
  'CI',
  'HOME',
  'LANG',
  'LC_ALL',
  'PATH',
  'Path',
  'SystemRoot',
  'TEMP',
  'TMP',
  'TMPDIR',
  'USERPROFILE',
  'GH_HOST',
  'GITHUB_API_URL',
  'GITHUB_SERVER_URL',
  'CAO_DEPENDABOT_ALERTS_MAX_PAGES',
  'CAO_DASHBOARD_QUERY_SOCKET'
];

function commandFailureMessage(result, fallback) {
  return (result.stderr || '').trim() || result.error?.message || fallback;
}

async function discoverOperationalValueScripts(root) {
  const directory = path.resolve(root);
  const entries = await readdir(directory, { withFileTypes: true });
  const scripts = [];
  for (const entry of entries.sort((left, right) => left.name.localeCompare(right.name))) {
    if (!entry.isDirectory() || entry.name.startsWith('.')) continue;
    const script = path.join(directory, entry.name, 'operational-value.mjs');
    try {
      if ((await stat(script)).isFile()) scripts.push({ package: entry.name, script });
    } catch (error) {
      if (!(error && error.code === 'ENOENT')) throw error;
    }
  }
  return scripts;
}

async function githubApiRemaining(env, signal) {
  const result = await runChildProcess('gh', ['api', 'rate_limit', '--jq', '.resources.core.remaining'], {
    env,
    signal,
    timeoutMs: RATE_LIMIT_TIMEOUT_MS
  });
  signal?.throwIfAborted();
  if (result.error || result.status !== 0) {
    const message = result.timedOut
      ? `timed out after ${RATE_LIMIT_TIMEOUT_MS} ms`
      : commandFailureMessage(result, 'gh api rate_limit failed');
    throw new Error(`Unable to read GitHub API rate limit: ${message}`);
  }
  const remaining = Number(String(result.stdout).trim());
  if (!Number.isSafeInteger(remaining) || remaining < 0) {
    throw new Error('GitHub API returned an invalid core rate limit');
  }
  return remaining;
}

function operationalValueWorkerEnvironment(databasePath, observedAt, rateLimitReserve, localOnly) {
  const env = Object.fromEntries(WORKER_ENVIRONMENT.flatMap((name) => (
    process.env[name] === undefined ? [] : [[name, process.env[name]]]
  )));
  const token = localOnly ? undefined : process.env.CAO_OPERATIONAL_VALUE_GH_TOKEN || process.env.GH_TOKEN;
  if (token) env.GH_TOKEN = token;
  if (databasePath) env.CAO_DATABASE = path.resolve(databasePath);
  env.CAO_ACTIVITY_CLI = fileURLToPath(new URL('./cao.mjs', import.meta.url));
  env.CAO_OPERATIONAL_VALUE_TIMESTAMP = observedAt;
  if (rateLimitReserve !== undefined) {
    env.CAO_GITHUB_API_MIN_REMAINING = String(rateLimitReserve);
  }
  return { env, token };
}

function canonicalOperationalValueEnvelope(row) {
  const value = {
    timestamp: row.timestamp,
    repository: row.repository,
    campaign: row.campaign,
    campaign_id: row.campaign ? `campaign:${row.campaign}` : undefined,
    value_id: row.valueId,
    value: row.value,
    metric_role: row['operational-value-role'],
    metric_name: row['operational-value-name'],
    metric_unit: row['operational-value-unit'],
    metric_direction: row['operational-value-direction'],
    maturity_status: row['maturity-status'],
    adoption_at: row['adoption-at'],
    evaluation_mode: row['evaluation-mode'],
    workflow_slug: row['workflow-slug'],
    workflow_name: row['workflow-name'],
    evaluator_digest: row['evaluator-digest'],
    cadence_days: row['cadence-days'],
    window_start: row['window-start'],
    window_end: row['window-end'],
    measurement_kind: row['measurement-kind'],
    coverage_status: row['coverage-status'],
    missing_reason: row['missing-reason'],
    eligible_count: row['eligible-count'],
    population_digest: row['population-digest'],
    evidence_digest: row['evidence-digest'],
    rollup_numerator: row['rollup-numerator'],
    rollup_denominator: row['rollup-denominator'],
  };
  return {
    schema_version: 2,
    kind: 'operational_value',
    operational_value: Object.fromEntries(
      Object.entries(value).filter(([, field]) => field !== undefined)
    ),
  };
}

async function retainedCanonicalOperationalValues(cutoff, signal) {
  const predicates = [{ field: 'campaign', gte: '' }];
  if (Number.isFinite(cutoff)) {
    predicates.push({ field: 'timestamp', gte: new Date(cutoff).toISOString() });
  }
  const source = await executeCanonicalQuery({
    name: 'retained-operational-values',
    from: 'operationalValues',
    filter: { predicates },
    'order-by': [
      { field: 'campaign' },
      { field: 'repository' },
      { field: 'valueId' },
      { field: 'timestamp' },
    ],
  }, { signal });
  return source.rows.map(canonicalOperationalValueEnvelope);
}

function redactToken(message, token) {
  return token ? String(message).replaceAll(token, '***') : String(message);
}

function terminateProcessTree(child) {
  if (!child.pid) return;
  if (process.platform === 'win32') {
    const script = [
      '$processes = Get-CimInstance Win32_Process',
      '$children = @{}',
      'foreach ($process in $processes) { $children[$process.ParentProcessId] += @($process.ProcessId) }',
      'function Stop-Tree([int]$id) {',
      '  foreach ($descendant in @($children[$id])) { Stop-Tree $descendant }',
      '  Stop-Process -Id $id -Force -ErrorAction SilentlyContinue',
      '}',
      `Stop-Tree ${child.pid}`
    ].join('; ');
    spawnSync('powershell.exe', ['-NoProfile', '-NonInteractive', '-Command', script], {
      windowsHide: true
    });
    return;
  }
  try {
    process.kill(-child.pid, 'SIGKILL');
    return;
  } catch {}
  const processes = spawnSync('ps', ['-eo', 'pid=,ppid='], { encoding: 'utf8' });
  const children = new Map();
  for (const line of String(processes.stdout ?? '').split(/\r?\n/)) {
    const [pid, parent] = line.trim().split(/\s+/).map(Number);
    if (!Number.isSafeInteger(pid) || !Number.isSafeInteger(parent)) continue;
    const siblings = children.get(parent) ?? [];
    siblings.push(pid);
    children.set(parent, siblings);
  }
  const tree = [];
  const visit = (pid) => {
    tree.push(pid);
    for (const descendant of children.get(pid) ?? []) visit(descendant);
  };
  visit(child.pid);
  for (const pid of tree) {
    try {
      process.kill(pid, 'SIGSTOP');
    } catch {}
  }
  for (const pid of tree.reverse()) {
    try {
      process.kill(pid, 'SIGKILL');
    } catch {}
  }
}

function runChildProcess(command, args, {
  env,
  input,
  maxBuffer = 16 * 1024 * 1024,
  signal,
  timeoutMs
} = {}) {
  const timeoutSignal = AbortSignal.timeout(timeoutMs);
  const workerSignal = signal
    ? AbortSignal.any([signal, timeoutSignal])
    : timeoutSignal;
  return new Promise((resolve) => {
    const child = spawn(command, args, {
      detached: process.platform !== 'win32' && process.env.CAO_VALUE_SHARED_PROCESS_GROUP !== '1',
      env,
      stdio: ['pipe', 'pipe', 'pipe'],
      windowsHide: true
    });
    const stdout = [];
    const stderr = [];
    let stdoutLength = 0;
    let stderrLength = 0;
    let error;
    const collect = (chunks, chunk, stream) => {
      chunks.push(chunk);
      if (stream === 'stdout') stdoutLength += chunk.length;
      else stderrLength += chunk.length;
      if (stdoutLength > maxBuffer || stderrLength > maxBuffer) {
        error ??= new Error(`${stream} exceeded maxBuffer`);
        terminateProcessTree(child);
      }
    };
    child.stdout.on('data', (chunk) => collect(stdout, chunk, 'stdout'));
    child.stderr.on('data', (chunk) => collect(stderr, chunk, 'stderr'));
    child.once('error', (cause) => {
      error = cause;
    });
    child.once('exit', () => terminateProcessTree(child));
    child.once('close', (status) => {
      workerSignal.removeEventListener('abort', abortWorker);
      terminateProcessTree(child);
      resolve({
        error,
        status,
        stdout: Buffer.concat(stdout).toString('utf8'),
        stderr: Buffer.concat(stderr).toString('utf8'),
        timedOut: timeoutSignal.aborted && !signal?.aborted
      });
    });
    const abortWorker = () => terminateProcessTree(child);
    workerSignal.addEventListener('abort', abortWorker, { once: true });
    if (workerSignal.aborted) abortWorker();
    child.stdin.end(input, () => {});
  });
}

async function runOperationalValueWorker(entry, request, env, { signal, timeoutMs = WORKER_TIMEOUT_MS } = {}) {
  const result = await runChildProcess(process.execPath, [entry.script], {
    env,
    input: request,
    signal,
    timeoutMs
  });
  signal?.throwIfAborted();
  if (result.error || result.status !== 0) {
    const message = result.timedOut
      ? `timed out after ${timeoutMs} ms`
      : commandFailureMessage(result, 'operational-value.mjs failed');
    throw new Error(`${entry.script} failed: ${message}`, { cause: result.error });
  }
  return result.stdout;
}

export function operationalValueReserve(value, UsageError = Error) {
  if (value === undefined) return undefined;
  const parsed = Number(value);
  if (!Number.isSafeInteger(parsed) || parsed === 0) {
    throw new UsageError('--max-github-api-rate-limit must be a non-zero integer');
  }
  return Math.abs(parsed);
}

function parseOperationalValueOutput(content, source, repositories) {
  const allowedRepositories = new Set(repositories.map((repository) => repository.toLowerCase()));
  const definitions = [];
  const deferred = [];
  const definedIds = new Set();
  const values = String(content).split(/\r?\n/).flatMap((line, index) => {
    if (!line.trim()) return [];
    let record;
    try {
      record = JSON.parse(line);
    } catch (error) {
      throw new Error(`${source}:${index + 1} emitted invalid JSON: ${error.message}`);
    }
    if (!record || typeof record !== 'object' || Array.isArray(record)) {
      throw new Error(`${source}:${index + 1} must emit a JSON object`);
    }
    if (record.kind === 'operational_value_deferred') {
      if (!OPERATIONAL_VALUE_SLUG.test(record.workflowSlug ?? '') || typeof record.reason !== 'string'
        || !record.reason.trim() || record.reason.length > 1024) throw new Error('Invalid operational value deferral');
      deferred.push(`${record.workflowSlug}: ${record.reason}`);
      return [];
    }
    if (record.kind === 'operational_value_definition') {
      const workflowSlug = String(record.workflowSlug ?? '');
      const evaluationMode = record.evaluationMode;
      const cadenceDays = record.cadenceDays;
      if (
        !OPERATIONAL_VALUE_SLUG.test(workflowSlug)
        || !['baseline-comparable', 'attainment-only'].includes(evaluationMode)
        || typeof cadenceDays !== 'number'
        || !Number.isFinite(cadenceDays)
        || cadenceDays <= 0
        || !Array.isArray(record.repositories)
        || record.repositories.length === 0
        || record.repositories.some((repository) => (
          typeof repository !== 'string' || !REPOSITORY_COORDINATE.test(repository)
        ))
        || !Array.isArray(record.valueIds)
        || record.valueIds.length === 0
        || record.valueIds.some((valueId) => (
          typeof valueId !== 'string'
          || !OPERATIONAL_VALUE_ID.test(valueId)
          || !valueId.startsWith(`${workflowSlug}.`)
        ))
      ) {
        throw new Error(`${source}:${index + 1} must emit a valid operational value definition`);
      }
      const adoptedAt = canonicalTimestamp(
        record.adoptedAt,
        `${source}:${index + 1}.adoptedAt`
      );
      for (const valueId of record.valueIds) {
        if (definedIds.has(valueId)) {
          throw new Error(`${source}:${index + 1} emitted a duplicate operational value definition ID: ${valueId}`);
        }
        definedIds.add(valueId);
      }
      definitions.push({
        adoptedAt,
        cadenceDays,
        evaluationMode,
        repositories: [...new Set(record.repositories.map((repository) => repository.toLowerCase()))],
        valueIds: [...record.valueIds],
        historyBatch: record.historyBatch === true,
        workflowSlug
      });
      return [];
    }
    const timestamp = canonicalTimestamp(record.timestamp, `${source}:${index + 1}.timestamp`);
    const repository = String(record.repository ?? '');
    const valueId = String(record.valueId ?? '');
    const value = record.value;
    const metricRole = record.metricRole ?? 'primary';
    const metricName = record.metricName ?? valueId;
    const metricUnit = record.metricUnit;
    const metricDirection = record.metricDirection ?? 'increase';
    const maturityStatus = record.maturityStatus ?? 'matured';
    const adoptionAt = record.adoptionAt;
    const evaluationMode = record.evaluationMode;
    const workflowSlug = record.workflowSlug;
    const workflowName = record.workflowName;
    const evaluatorDigest = record.evaluatorDigest;
    const cadenceDays = record.cadenceDays;
    if (cadenceDays !== undefined && (
      typeof cadenceDays !== 'number' || !Number.isFinite(cadenceDays) || cadenceDays <= 0
    )) {
      throw new Error(`${source}:${index + 1}.cadenceDays must be a positive finite number`);
    }
    const hasRollupNumerator = Object.hasOwn(record, 'rollupNumerator');
    const hasRollupDenominator = Object.hasOwn(record, 'rollupDenominator');
    const rollupNumerator = record.rollupNumerator;
    const rollupDenominator = record.rollupDenominator;
    if (!REPOSITORY_COORDINATE.test(repository) || !allowedRepositories.has(repository.toLowerCase())) {
      throw new Error(`${source}:${index + 1}.repository must identify a requested repository`);
    }
    if (!OPERATIONAL_VALUE_ID.test(valueId)) {
      throw new Error(`${source}:${index + 1}.valueId must be a lowercase metric identifier`);
    }
    const period = operationalValuePeriod(record, timestamp, `${source}:${index + 1}`);
    const unavailable = value === null && period.windowStart && maturityStatus === 'unavailable'
      && period.missingReason;
    if (!unavailable && (typeof value !== 'number' || !Number.isFinite(value))) {
      throw new Error(`${source}:${index + 1}.value must be a finite number`);
    }
    if (!['primary', 'diagnostic'].includes(metricRole)) {
      throw new Error(`${source}:${index + 1}.metricRole must be primary or diagnostic`);
    }
    if (typeof metricName !== 'string' || !metricName.trim()) {
      throw new Error(`${source}:${index + 1}.metricName must be a non-empty string`);
    }
    if (metricUnit !== undefined && (typeof metricUnit !== 'string' || !metricUnit.trim())) {
      throw new Error(`${source}:${index + 1}.metricUnit must be a non-empty string`);
    }
    if (!['increase', 'decrease', 'maintain', 'target'].includes(metricDirection)) {
      throw new Error(`${source}:${index + 1}.metricDirection is invalid`);
    }
    if (!['matured', 'interim', 'unavailable'].includes(maturityStatus)) {
      throw new Error(`${source}:${index + 1}.maturityStatus is invalid`);
    }
    if (adoptionAt !== undefined && !Number.isFinite(Date.parse(adoptionAt))) {
      throw new Error(`${source}:${index + 1}.adoptionAt must be an ISO-8601 timestamp`);
    }
    if (evaluationMode !== undefined && !['baseline-comparable', 'attainment-only'].includes(evaluationMode)) {
      throw new Error(`${source}:${index + 1}.evaluationMode is invalid`);
    }
    for (const [field, candidate] of [['workflowSlug', workflowSlug], ['workflowName', workflowName]]) {
      if (candidate !== undefined && (typeof candidate !== 'string' || !candidate.trim())) {
        throw new Error(`${source}:${index + 1}.${field} must be a non-empty string`);
      }
    }
    if (evaluatorDigest !== undefined && (
      typeof evaluatorDigest !== 'string' || !/^[0-9a-f]{64}$/.test(evaluatorDigest)
    )) {
      throw new Error(`${source}:${index + 1}.evaluatorDigest must be a SHA-256 fingerprint`);
    }
    if (
      hasRollupNumerator !== hasRollupDenominator
      || (value === null && hasRollupNumerator)
      || (hasRollupNumerator && (
        typeof rollupNumerator !== 'number'
        || !Number.isFinite(rollupNumerator)
        || rollupNumerator < 0
        || typeof rollupDenominator !== 'number'
        || !Number.isFinite(rollupDenominator)
        || rollupDenominator <= 0
      ))
    ) {
      throw new Error(`${source}:${index + 1} must emit a valid rollup numerator and denominator together`);
    }
    return [{
      timestamp,
      repository,
      valueId,
      value,
      ...(Object.hasOwn(record, 'metricRole') ? { metricRole } : {}),
      ...(Object.hasOwn(record, 'metricName') ? { metricName: metricName.trim() } : {}),
      ...(Object.hasOwn(record, 'metricUnit') ? { metricUnit: metricUnit.trim() } : {}),
      ...(Object.hasOwn(record, 'metricDirection') ? { metricDirection } : {}),
      ...(Object.hasOwn(record, 'maturityStatus') ? { maturityStatus } : {}),
      ...(Object.hasOwn(record, 'adoptionAt') ? { adoptionAt: canonicalTimestamp(adoptionAt, `${source}:${index + 1}.adoptionAt`) } : {}),
      ...(Object.hasOwn(record, 'evaluationMode') ? { evaluationMode } : {}),
      ...(Object.hasOwn(record, 'workflowSlug') ? { workflowSlug: workflowSlug.trim() } : {}),
      ...(Object.hasOwn(record, 'workflowName') ? { workflowName: workflowName.trim() } : {}),
      ...(evaluatorDigest !== undefined ? { evaluatorDigest } : {}),
      ...(cadenceDays !== undefined ? { cadenceDays } : {}),
      ...period,
      ...(hasRollupNumerator ? { rollupNumerator, rollupDenominator } : {})
    }];
  });
  return { definitions, values, deferred };
}

function historicalObservationTimes(definition, observedAt, retentionWindow) {
  const cadence = definition.cadenceDays * DAY_MS;
  const adoption = Date.parse(definition.adoptedAt);
  const origin = definition.evaluationMode === 'attainment-only'
    ? adoption + cadence
    : adoption - BASELINE_OBSERVATIONS * cadence;
  const cutoff = Math.max(origin, Date.parse(observedAt) - retentionWindow);
  const times = [];
  for (let timestamp = origin; timestamp < Date.parse(observedAt); timestamp += cadence) {
    if (timestamp >= cutoff) times.push(new Date(timestamp).toISOString());
    if (times.length > MAX_HISTORY_OBSERVATIONS) {
      throw new Error(`Operational value history exceeds ${MAX_HISTORY_OBSERVATIONS} observations`);
    }
  }
  return times;
}

function operationalValueKey(campaign, repository, valueId, timestamp) {
  const campaignKey = String(campaign ?? '').trim().toLowerCase();
  if (!campaignKey) throw new Error('Operational value campaign is required for cache key');
  return `${campaignKey}\0${String(repository).toLowerCase()}\0${valueId}\0${timestamp}`;
}

function operationalValueDefinitionKey(campaign, valueId) {
  return `${String(campaign).toLowerCase()}\0${valueId}`;
}

function operationalValueCadenceKey(value, definition) {
  if (value.window_start && value.window_end && value.measurement_kind) {
    return operationalValueKey(value.campaign, value.repository, value.value_id, [
      value.evaluator_digest ?? 'unversioned',
      value.measurement_kind,
      value.window_start,
      value.window_end
    ].join('\0'));
  }
  const cadence = definition.cadenceDays * DAY_MS;
  const adoption = Date.parse(definition.adoptedAt);
  const timestamp = Date.parse(value.timestamp);
  const bucket = Math.ceil((timestamp - adoption) / cadence);
  const bucketEnd = new Date(adoption + bucket * cadence).toISOString();
  return `${String(value.campaign).toLowerCase()}\0${String(value.repository).toLowerCase()}\0${value.value_id}\0${bucketEnd}`;
}

async function retainedOperationalValueEnvelopes(outputPath, cutoff) {
  let content;
  try {
    content = await readFile(outputPath, 'utf8');
  } catch (error) {
    if (error && error.code === 'ENOENT') return [];
    throw error;
  }
  const retained = [];
  let legacyPruned = 0;
  content.split(/\r?\n/).forEach((line, index) => {
    if (!line.trim()) return [];
    const envelope = JSON.parse(line);
    if (envelope?.schema_version !== 2 || envelope?.kind !== 'operational_value') {
      throw new Error(`${outputPath}:${index + 1} is not an operational value envelope`);
    }
    const timestamp = Date.parse(envelope.operational_value?.timestamp);
    if (!Number.isFinite(timestamp)) {
      throw new Error(`${outputPath}:${index + 1} has an invalid operational value timestamp`);
    }
    const campaign = String(envelope.operational_value?.campaign ?? '').trim();
    // Legacy retained envelopes written before campaign-scoped value identity
    // cannot be matched safely and are pruned as obsolete retained evidence.
    if (timestamp >= cutoff && campaign) retained.push(envelope);
    else if (timestamp >= cutoff) legacyPruned += 1;
  });
  if (legacyPruned > 0) {
    console.warn(`Warning: pruned ${legacyPruned} legacy operational value envelope(s) without campaign identity from ${outputPath}`);
  }
  return retained;
}

export async function runOperationalValue({
  campaign,
  historyCampaign,
  indexedDB,
  databasePath,
  root = '.',
  outputPath,
  timestamp = new Date().toISOString(),
  repositories = [],
  rateLimitReserve,
  localOnly = false,
  disabledCampaigns = [],
  disabledWorkflows = [],
  disabledCollections = [],
  retentionWindow,
  signal,
  workerTimeoutMs = WORKER_TIMEOUT_MS
}) {
  signal?.throwIfAborted();
  const observedAt = canonicalTimestamp(timestamp, '--timestamp');
  if (historyCampaign && retentionWindow === undefined) {
    throw new Error('Operational value history requires a retention window');
  }
  const selectedRepositories = repositories.length > 0
    ? repositories
    : (await readCollection(indexedDB, 'repositories'))
      .map((record) => `${String(record.owner ?? '')}/${String(record.name ?? '')}`)
      .filter((repository) => REPOSITORY_COORDINATE.test(repository));
  const uniqueRepositories = [...new Map(
    selectedRepositories.map((repository) => [repository.toLowerCase(), repository])
  ).values()].sort((left, right) => left.localeCompare(right));
  if (uniqueRepositories.length === 0) {
    throw new Error('Operational value requires at least one repository in the dashboard database or --repository');
  }
  const discoveredScripts = await discoverOperationalValueScripts(root);
  const scripts = (campaign
    ? discoveredScripts.filter((entry) => entry.package === campaign)
    : discoveredScripts).filter(entry => !disabledCampaigns.includes(entry.package));
  if (campaign && scripts.length === 0) {
    throw new Error(`Operational value campaign not found: ${campaign}`);
  }
  if (historyCampaign && !scripts.some((entry) => entry.package === historyCampaign)) {
    throw new Error(`Operational value history campaign not found: ${historyCampaign}`);
  }
  const output = outputPath ? path.resolve(outputPath) : undefined;
  if (localOnly && output) {
    try {
      if ((await stat(output)).size > MAX_LOCAL_VALUE_BYTES) throw new Error('Retained local Value exceeds its byte budget');
    } catch (error) {
      if (error.code !== 'ENOENT') throw error;
    }
  }
  const cutoff = retentionWindow === undefined
    ? Number.NEGATIVE_INFINITY
    : Date.parse(observedAt) - retentionWindow;
  const retained = retentionWindow === undefined
    ? []
    : process.env.CAO_DASHBOARD_QUERY_SOCKET
      ? await retainedCanonicalOperationalValues(cutoff, signal)
      : output
        ? await retainedOperationalValueEnvelopes(output, cutoff)
        : [];
  const retainedKeys = new Set(retained.map((envelope) => {
    const value = envelope.operational_value;
    return operationalValueKey(value.campaign, value.repository, value.value_id, value.timestamp);
  }));
  const requestDocument = {
    schemaVersion: 1,
    timestamp: observedAt,
    repositories: uniqueRepositories,
    ...(localOnly ? { localOnly: true, disabledWorkflows, disabledCollections } : {})
  };
  if (databasePath) requestDocument.database = path.resolve(databasePath);
  const request = `${JSON.stringify(requestDocument)}\n`;
  const values = [];
  const warnings = [];
  const activeValueIds = new Map();
  const cadenceDefinitions = new Map();
  let historyValues = 0;
  let localOutputBytes = 0;
  const worker = operationalValueWorkerEnvironment(databasePath, observedAt, rateLimitReserve, localOnly);
  const warn = (entry, error) => {
    const message = redactToken(error instanceof Error ? error.message : error, worker.token);
    const warning = { package: entry?.package ?? null, message };
    warnings.push(warning);
    console.warn(`Warning: ${message}`);
  };
  for (const entry of scripts) {
    try {
      signal?.throwIfAborted();
      if (localOnly && createHash('sha256').update(await readFile(entry.script)).digest('hex') !== LOCAL_VALUE_ADAPTER_DIGEST) {
        throw new Error(`${entry.package}: local computation deferred because the adapter has no reviewed local-only boundary`);
      }
      if (!localOnly && rateLimitReserve !== undefined && await githubApiRemaining(worker.env, signal) <= rateLimitReserve) {
        throw new Error(`GitHub API core remaining is at or below the reserved ${rateLimitReserve} requests`);
      }
      const workerOutput = await runOperationalValueWorker(entry, request, worker.env, {
        signal,
        timeoutMs: workerTimeoutMs
      });
      if (localOnly && (localOutputBytes += Buffer.byteLength(workerOutput)) > MAX_LOCAL_VALUE_BYTES) {
        throw new Error('Local Value batch exceeded its output budget; accepted evidence retained');
      }
      const parsed = parseOperationalValueOutput(workerOutput, entry.script, uniqueRepositories);
      for (const reason of parsed.deferred) warn(entry, reason);
      values.push(...parsed.values.map((record) => ({ ...record, campaign: entry.package })));
      if (parsed.definitions.length > 0) {
        activeValueIds.set(entry.package, new Set(
          parsed.definitions.flatMap((definition) => definition.valueIds)
        ));
        for (const definition of parsed.definitions) {
          for (const valueId of definition.valueIds) {
            cadenceDefinitions.set(
              operationalValueDefinitionKey(entry.package, valueId),
              definition
            );
          }
        }
      }
      if (entry.package === historyCampaign) {
        for (const definition of parsed.definitions) {
          const supportedRepositories = uniqueRepositories.filter((repository) => (
            definition.repositories.includes(repository.toLowerCase())
          ));
          if (supportedRepositories.length === 0) continue;
          if (definition.historyBatch) {
            for (const { operational_value: value } of retained) {
              if (
                value.campaign === entry.package
                && definition.valueIds.includes(value.value_id)
              ) {
                retainedKeys.add(operationalValueCadenceKey(value, definition));
              }
            }
          }
          const timestamps = historicalObservationTimes(
            definition,
            observedAt,
            retentionWindow
          ).filter((historyTimestamp) => supportedRepositories.some((repository) => (
            definition.valueIds.some((valueId) => (
              !retainedKeys.has(operationalValueKey(entry.package, repository, valueId, historyTimestamp))
            ))
          )));
          const historyEnvironment = {
            ...worker.env,
            CAO_OPERATIONAL_VALUE_MODULE: definition.workflowSlug
          };
          const batches = definition.historyBatch ? [timestamps] : timestamps.map((time) => [time]);
          for (const batch of batches) {
            if (batch.length === 0) continue;
            signal?.throwIfAborted();
            const historyRequestDocument = {
              schemaVersion: 1,
              timestamp: batch.at(-1),
              ...(definition.historyBatch ? { historyTimestamps: batch } : {}),
              repositories: supportedRepositories,
              ...(localOnly ? { localOnly: true, disabledWorkflows, disabledCollections } : {})
            };
            if (databasePath) historyRequestDocument.database = path.resolve(databasePath);
            const historyRequest = `${JSON.stringify(historyRequestDocument)}\n`;
            const historyOutput = await runOperationalValueWorker(
              entry,
              historyRequest,
              historyEnvironment,
              { signal, timeoutMs: workerTimeoutMs }
            );
            if (localOnly && (localOutputBytes += Buffer.byteLength(historyOutput)) > MAX_LOCAL_VALUE_BYTES) {
              throw new Error('Local Value batch exceeded its output budget; accepted evidence retained');
            }
            const historyParsed = parseOperationalValueOutput(
              historyOutput,
              entry.script,
              supportedRepositories
            );
            const expectedIds = new Set(definition.valueIds);
            if (historyParsed.values.some((record) => (
              !batch.includes(record.timestamp) || !expectedIds.has(record.valueId)
            ))) {
              throw new Error(`${entry.script} emitted values outside the requested history observation`);
            }
            const records = historyParsed.values.filter((record) => !retainedKeys.has(
              operationalValueKey(entry.package, record.repository, record.valueId, record.timestamp)
            ) && !(record.windowStart && retainedKeys.has(operationalValueCadenceKey({
              campaign: entry.package,
              repository: record.repository,
              value_id: record.valueId,
              evaluator_digest: record.evaluatorDigest,
              measurement_kind: record.measurementKind,
              window_start: record.windowStart,
              window_end: record.windowEnd
            }, definition)))).map((record) => ({
              ...record,
              campaign: entry.package
            }));
            values.push(...records);
            historyValues += records.length;
            for (const record of records) {
              retainedKeys.add(operationalValueKey(
                entry.package,
                record.repository,
                record.valueId,
                record.timestamp
              ));
            }
          }
        }
      }
    } catch (error) {
      signal?.throwIfAborted();
      if (localOnly && localOutputBytes > MAX_LOCAL_VALUE_BYTES) throw error;
      warn(entry, error);
    }
  }
  signal?.throwIfAborted();
  if (!localOnly && rateLimitReserve !== undefined) {
    try {
      if (await githubApiRemaining(worker.env, signal) < rateLimitReserve) {
        throw new Error(`Operational value crossed the reserved GitHub API floor of ${rateLimitReserve} requests`);
      }
    } catch (error) {
      warn(null, error);
    }
  }
  signal?.throwIfAborted();
  if (output) {
    await mkdir(path.dirname(output), { recursive: true });
    const envelopes = values.map((record) => ({
      schema_version: 2,
      kind: 'operational_value',
      operational_value: {
        timestamp: record.timestamp,
        repository: record.repository,
        campaign: record.campaign,
        campaign_id: `campaign:${record.campaign}`,
        value_id: record.valueId,
        value: record.value,
        metric_role: record.metricRole ?? 'primary',
        metric_name: record.metricName ?? record.valueId,
        metric_unit: record.metricUnit,
        metric_direction: record.metricDirection ?? 'increase',
        maturity_status: record.maturityStatus ?? 'matured',
        adoption_at: record.adoptionAt,
        evaluation_mode: record.evaluationMode,
        workflow_slug: record.workflowSlug,
        workflow_name: record.workflowName,
        evaluator_digest: record.evaluatorDigest,
        cadence_days: record.cadenceDays,
        window_start: record.windowStart,
        window_end: record.windowEnd,
        measurement_kind: record.measurementKind,
        coverage_status: record.coverageStatus,
        missing_reason: record.missingReason,
        eligible_count: record.eligibleCount,
        population_digest: record.populationDigest,
        evidence_digest: record.evidenceDigest,
        rollup_numerator: record.rollupNumerator,
        rollup_denominator: record.rollupDenominator
      }
    }));
    const currentRetained = retained.filter((envelope) => {
      const value = envelope.operational_value;
      const activeIds = activeValueIds.get(value.campaign);
      return !activeIds || activeIds.has(value.value_id);
    });
    const merged = new Map();
    for (const envelope of [...currentRetained, ...envelopes]) {
      const value = envelope.operational_value;
      const definition = cadenceDefinitions.get(
        operationalValueDefinitionKey(value.campaign, value.value_id)
      );
      const key = definition
        ? operationalValueCadenceKey(value, definition)
        : operationalValueKey(value.campaign, value.repository, value.value_id, value.timestamp);
      const existing = merged.get(key);
      if (
        !existing
        || Date.parse(value.timestamp) >= Date.parse(existing.operational_value.timestamp)
      ) {
        merged.set(key, envelope);
      }
    }
    const jsonl = [...merged.values()].map((envelope) => JSON.stringify(envelope)).join('\n');
    if (localOnly && Buffer.byteLength(jsonl) + 1 > MAX_LOCAL_VALUE_BYTES) {
      throw new Error('Combined local Value exceeds its output budget; accepted evidence retained');
    }
    const temporary = `${output}.tmp-${process.pid}-${Date.now()}`;
    try {
      await writeFile(temporary, jsonl ? `${jsonl}\n` : '', { flag: 'wx' });
      await rename(temporary, output);
    } finally {
      await rm(temporary, { force: true });
    }
  }
  signal?.throwIfAborted();
  return {
    command: 'operational-value',
    timestamp: observedAt,
    repositories: uniqueRepositories.length,
    scripts: scripts.map((entry) => entry.package),
    historyValues,
    values,
    warnings,
    output: output ?? null
  };
}
