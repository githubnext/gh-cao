import { orderRunRecords } from '../normalize/index.js';
import { EVIDENCE_DEFINITION_STORES, mergeEvidenceDefinition, timestampMilliseconds } from '../model/schema.js';
import { createDebug } from '../../debug.js';
import { mergeOperationalValueEvidence } from '../model/operational-value-evidence.js';
import { mergeWorkflowValueCollection } from '../model/workflow-value-collection.js';

const debugRetention = createDebug('retention');

export const RETENTION_WINDOW_DAYS = 30;
export const RETENTION_WINDOW_MS = RETENTION_WINDOW_DAYS * 24 * 60 * 60 * 1000;
export const OPERATIONAL_VALUE_RETENTION_DAYS = 365;
export const OPERATIONAL_VALUE_RETENTION_MS = OPERATIONAL_VALUE_RETENTION_DAYS * 24 * 60 * 60 * 1000;

/**
 * Stores whose retention is decided by observation time. Structural parents
 * are never TTLed: repositories and workflows are retained by reachability so
 * a retained run never loses its hierarchy, while campaigns persist across imports.
 * @type {Record<string, string[]>}
 */
export const RETENTION_TIMESTAMPS = {
  runs: ['completedAt', 'startedAt', 'observedAt'],
  domains: ['timestamp', 'observedAt'],
  tools: ['timestamp', 'observedAt'],
  skills: ['timestamp', 'observedAt'],
  friction: ['timestamp', 'observedAt'],
  audits: ['timestamp', 'observedAt'],
  issues: ['timestamp', 'observedAt'],
  operationalValues: ['timestamp', 'observedAt'],
  experimentAssignments: ['timestamp', 'observedAt'],
  graderObservations: ['timestamp', 'observedAt'],
  evalObservations: ['timestamp', 'observedAt']
};

const STORES = /** @type {const} */ ([
  'campaigns',
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
  'marketplacePackages',
  'experiments', 'experimentAssignments', 'graders', 'graderObservations', 'evals', 'evalObservations'
]);
const RUN_LINKED_STORES = /** @type {const} */ ([
  'domains',
  'tools',
  'skills',
  'friction',
  'audits',
  'issues', 'experimentAssignments', 'graderObservations', 'evalObservations'
]);
export const BROWSER_RETENTION_WINDOWS_MS = Object.freeze({
  runs: Number.MAX_SAFE_INTEGER,
  operationalValues: OPERATIONAL_VALUE_RETENTION_MS,
  ...Object.fromEntries(RUN_LINKED_STORES.map((store) => [store, 7 * 24 * 60 * 60 * 1000]))
});

/**
 * @param {string} storeName
 * @param {{ retentionWindowMs?: number, retentionWindowMsByStore?: Record<string, number> }} options
 */
export function retentionWindowForStore(storeName, options = {}) {
  const configured = options.retentionWindowMsByStore?.[storeName]
    ?? (Number.isFinite(options.retentionWindowMs)
      ? options.retentionWindowMs
      : BROWSER_RETENTION_WINDOWS_MS[/** @type {keyof typeof BROWSER_RETENTION_WINDOWS_MS} */ (storeName)]);
  return Number.isFinite(configured) ? Math.max(0, Number(configured)) : RETENTION_WINDOW_MS;
}

/**
 * @param {string} storeName
 * @param {Record<string, unknown>} record
 * @param {{ now?: number, retentionWindowMs?: number, retentionWindowMsByStore?: Record<string, number> }} options
 */
export function isExpiredCanonicalRecord(storeName, record, options = {}) {
  if (!Object.hasOwn(RETENTION_TIMESTAMPS, storeName)) return false;
  const timestamp = recordTimestamp(storeName, record);
  return timestamp === null
    || timestamp < (options.now ?? Date.now()) - retentionWindowForStore(storeName, options);
}
const WORKFLOW_INVENTORY_FIELDS = /** @type {const} */ ([
  'campaignId',
  'githubId',
  'registryState',
  'sourceKind',
  'sourceCoordinate',
  'sourceVersion',
  'sourceCurrentVersion',
  'sourceUpdateState',
  'createdAt',
  'updatedAt'
]);
const RECORD_OVERHEAD_BYTES = 512;

/**
 * Preserves inventory-owned fields when an Activity observation updates an
 * existing structural record.
 * @param {'repositories' | 'workflows'} storeName
 * @param {Record<string, unknown> | undefined} existing
 * @param {Record<string, unknown>} incoming
 */
export function mergeActivityStructuralRecord(storeName, existing, incoming) {
  if (!existing) return incoming;
  if (storeName === 'repositories') return existing;
  const inventoryFields = existing.registryState === undefined
    ? WORKFLOW_INVENTORY_FIELDS
    : [...WORKFLOW_INVENTORY_FIELDS, 'state', 'name', 'path', 'workflowLink'];
  const preserved = Object.fromEntries(inventoryFields
    .filter((field) => existing[field] !== undefined)
    .map((field) => [field, existing[field]]));
  return { ...existing, ...incoming, ...preserved };
}

/** @param {Record<string, unknown>} record */
function recordSize(record) {
  return new TextEncoder().encode(JSON.stringify(record)).byteLength + RECORD_OVERHEAD_BYTES;
}

/** @param {import('../model/schema.js').CanonicalBatch} batch */
export function estimateCanonicalBatchBytes(batch) {
  return STORES.reduce((total, storeName) =>
    total + (batch[storeName] ?? []).reduce((storeTotal, record) => storeTotal + recordSize(record), 0), 0);
}

/**
 * Drops whole run subtrees from oldest to newest until the conservative
 * serialized estimate fits. Structural parents remain so future incremental
 * collections can reconnect to them.
 *
 * @param {import('../model/schema.js').CanonicalBatch} batch
 * @param {number} maxBytes
 * @returns {import('../model/schema.js').CanonicalBatch}
 */
export function capCanonicalBatchSize(batch, maxBytes) {
  if (!Number.isFinite(maxBytes) || maxBytes < 0) throw new TypeError('Canonical database byte limit must be non-negative');
  let estimatedBytes = estimateCanonicalBatchBytes(batch);
  if (estimatedBytes <= maxBytes) return batch;

  /** @param {Record<string, unknown>[]} records @param {string} field */
  const groupBy = (records, field) => {
    /** @type {Map<string, Record<string, unknown>[]>} */
    const grouped = new Map();
    for (const record of records) {
      const key = String(record[field]);
      grouped.set(key, [...(grouped.get(key) ?? []), record]);
    }
    return grouped;
  };
  const recordsByRun = Object.fromEntries(RUN_LINKED_STORES.map((storeName) => [
    storeName,
    groupBy(batch[storeName] ?? [], 'runId')
  ]));
  const evictedRuns = new Set();
  const oldestRuns = [...batch.runs].sort((left, right) =>
    (recordTimestamp('runs', left) ?? Number.NEGATIVE_INFINITY)
      - (recordTimestamp('runs', right) ?? Number.NEGATIVE_INFINITY)
    || String(left.id).localeCompare(String(right.id)));

  for (const run of oldestRuns) {
    if (estimatedBytes <= maxBytes) break;
    const runId = String(run.id);
    evictedRuns.add(runId);
    estimatedBytes -= recordSize(run);
    for (const storeName of RUN_LINKED_STORES) {
      for (const record of recordsByRun[storeName].get(runId) ?? []) estimatedBytes -= recordSize(record);
    }
  }

  debugRetention({
    event: 'batch-capped',
    maxBytes,
    evictedRunCount: evictedRuns.size,
    finalBytes: estimatedBytes
  });

  return /** @type {import('../model/schema.js').CanonicalBatch} */ ({
    campaigns: batch.campaigns,
    repositories: batch.repositories,
    workflows: batch.workflows,
    runs: batch.runs.filter((record) => !evictedRuns.has(String(record.id))),
    operationalValues: batch.operationalValues ?? [],
    operationalValueEvidence: batch.operationalValueEvidence ?? [],
    workflowValueCollections: batch.workflowValueCollections ?? [],
    marketplacePackages: batch.marketplacePackages ?? [],
    experiments: batch.experiments ?? [],
    graders: batch.graders ?? [],
    evals: batch.evals ?? [],
    ...Object.fromEntries(RUN_LINKED_STORES.map((storeName) => [
      storeName,
      (batch[storeName] ?? []).filter((record) => !evictedRuns.has(String(record.runId)))
    ]))
  });
}

/**
 * Reports the observation time that bounds a record's retention. Records in a
 * time-bounded store without a usable timestamp are treated as expired so
 * retention can never grow without limit.
 *
 * @param {string} storeName
 * @param {Record<string, unknown>} record
 * @returns {number | null}
 */
export function recordTimestamp(storeName, record) {
  for (const field of RETENTION_TIMESTAMPS[storeName] ?? []) {
    const value = record[field];
    if (typeof value !== 'string' && typeof value !== 'number') continue;
    const milliseconds = Date.parse(String(value));
    if (Number.isFinite(milliseconds)) return milliseconds;
  }
  return null;
}

const FAILURE_CONCLUSIONS = new Set(['failure', 'startup-failure', 'startup_failure', 'stale', 'timed-out', 'timed_out']);

/** @typedef {{ scope: string, timestamp: number, githubRunId: string, attempt: number }} FailureLogBoundary */

/**
 * Uses the runtime-health run order, with stricter scope matching for destructive
 * retention. Unknown worker targets cannot establish a recovery boundary.
 * @param {Record<string, unknown>} run
 * @param {Record<string, unknown> | undefined} workflow
 * @returns {FailureLogBoundary | null}
 */
export function failureLogBoundary(run, workflow) {
  if (!workflow || workflow.id !== run.workflowId || workflow.repositoryId !== run.repositoryId
      || typeof run.repositoryId !== 'string' || typeof run.workflowId !== 'string'
      || typeof run.event !== 'string' || !run.event || run.event === 'unknown'
      || !Number.isSafeInteger(run.attempt) || Number(run.attempt) < 1) return null;
  const target = run.targetRepositoryId ?? run.targetRepository;
  const knownTarget = typeof target === 'string' && target.length > 0 && target !== 'unknown';
  const branch = run.branch ?? run.headBranch;
  if (typeof branch !== 'string' || !branch || branch === 'unknown') return null;
  if ((workflow.role === 'worker' || workflow.role == null || workflow.role === '' || workflow.role === 'unknown')
      && !knownTarget) return null;
  if ((workflow.role === 'worker' || knownTarget)
      && (typeof run.rolloutMode !== 'string' || !run.rolloutMode || run.rolloutMode === 'unknown')) return null;
  const timestamp = ['startedAt', 'createdAt', 'updatedAt']
    .map((field) => timestampMilliseconds(run[field])).find((value) => value !== null);
  const githubRunId = String(run.githubRunId ?? '');
  if (timestamp === undefined || !/^[1-9][0-9]*$/.test(githubRunId)
      || (typeof run.githubRunId === 'number' && !Number.isSafeInteger(run.githubRunId))) return null;
  return {
    scope: JSON.stringify([
      run.repositoryId, run.workflowId, workflow.campaignId ?? null, workflow.role ?? null,
      run.targetRepositoryId ?? null, run.targetRepository ?? null,
      run.event, branch, run.rolloutMode ?? null
    ]),
    timestamp, githubRunId, attempt: Number(run.attempt)
  };
}

/** @param {FailureLogBoundary} left @param {FailureLogBoundary} right */
export function compareFailureLogBoundaries(left, right) {
  return left.timestamp - right.timestamp
    || left.githubRunId.localeCompare(right.githubRunId, undefined, { numeric: true })
    || left.attempt - right.attempt;
}

/** @param {Record<string, unknown>} run */
export function isFailureLogRecovery(run) {
  return run.status === 'completed' && run.conclusion === 'success'
    && (run.terminalOutcome == null || run.terminalOutcome === '')
    && (run.failureKind == null || run.failureKind === '');
}

/** @param {Record<string, unknown>} run */
export function hasFailedConclusion(run) {
  return run.status === 'completed' && FAILURE_CONCLUSIONS.has(String(run.conclusion));
}

/**
 * @param {import('../model/schema.js').CanonicalBatch} batch
 * @returns {number | null}
 */
function newestObservation(batch) {
  let newest = null;
  for (const storeName of STORES) {
    for (const record of batch[storeName] ?? []) {
      const timestamp = recordTimestamp(storeName, record);
      if (timestamp !== null && (newest === null || timestamp > newest)) newest = timestamp;
    }
  }
  return newest;
}

/**
 * @param {import('../model/schema.js').CanonicalBatch} previous
 * @param {import('../model/schema.js').CanonicalBatch} incoming
 * @param {number} reference
 * @param {number} defaultRetentionWindowMs
 * @param {Partial<Record<typeof STORES[number], number>>} retentionWindowMsByStore
 * @param {boolean} preserveWorkflowCampaignMappings
 * @param {boolean} preserveRepositoryRecords
 */
function upsertRecords(
  previous,
  incoming,
  reference,
  defaultRetentionWindowMs,
  retentionWindowMsByStore,
  preserveWorkflowCampaignMappings,
  preserveRepositoryRecords
) {
  /** @type {Record<string, Map<string, Record<string, unknown>>>} */
  const merged = {};
  for (const storeName of STORES) {
    /** @type {Map<string, Record<string, unknown>>} */
    const records = new Map();
    const timeBound = storeName in RETENTION_TIMESTAMPS;
    const configuredWindow = retentionWindowMsByStore[storeName];
    const retentionWindowMs = Number.isFinite(configuredWindow)
      ? Math.max(0, Number(configuredWindow))
      : defaultRetentionWindowMs;
    const horizon = reference - retentionWindowMs;
    for (const record of previous[storeName] ?? []) {
      const timestamp = recordTimestamp(storeName, record);
      if (timeBound && (timestamp === null || timestamp < horizon)) continue;
      records.set(String(record.id), record);
    }
    for (const record of incoming[storeName] ?? []) {
      const timestamp = recordTimestamp(storeName, record);
      if (timeBound && (timestamp === null || timestamp < horizon)) continue;
      const id = String(record.id);
      // Discovery owns repository metadata, so run-derived observations may only
      // backfill missing repository records and must never overwrite existing ones.
      if (storeName === 'repositories' && preserveRepositoryRecords && records.has(id)) {
        records.set(id, mergeActivityStructuralRecord('repositories', records.get(id), record));
        continue;
      }
      // Inventory discovery owns campaign membership and registry metadata.
      // Run-derived observations may enrich other workflow fields only.
      if (storeName === 'workflows' && preserveWorkflowCampaignMappings) {
        const existing = records.get(id);
        if (existing) {
          records.set(id, mergeActivityStructuralRecord('workflows', existing, record));
          continue;
        }
      }
      records.set(id, storeName === 'operationalValueEvidence'
        ? mergeOperationalValueEvidence(records.get(id), record) : storeName === 'workflowValueCollections'
        ? mergeWorkflowValueCollection(records.get(id), record) : EVIDENCE_DEFINITION_STORES.has(storeName)
        ? mergeEvidenceDefinition(records.get(id), record) : record);
    }
    merged[storeName] = records;
  }
  return merged;
}

/**
 * Drops retained records whose mandatory parents did not survive so the merged
 * batch remains relationship-safe.
 *
 * @param {Record<string, Map<string, Record<string, unknown>>>} merged
 */
function pruneOrphans(merged) {
  const campaigns = merged.campaigns;
  const repositories = merged.repositories;
  const workflows = merged.workflows;
  const runs = merged.runs;
  let orphanedRunCount = 0;

  for (const [id, workflow] of workflows) {
    if (!repositories.has(String(workflow.repositoryId))
      || (workflow.campaignId !== undefined
        && workflow.campaignId !== null
        && !campaigns.has(String(workflow.campaignId)))) workflows.delete(id);
  }
  for (const [id, run] of runs) {
    const workflow = workflows.get(String(run.workflowId));
    if (!repositories.has(String(run.repositoryId))
      || !workflow
      || workflow.repositoryId !== run.repositoryId) {
      runs.delete(id);
      orphanedRunCount += 1;
    }
  }
  for (const storeName of RUN_LINKED_STORES) {
    for (const [id, record] of merged[storeName]) {
      if (!runs.has(String(record.runId))) merged[storeName].delete(id);
    }
  }
  for (const storeName of ['experiments', 'graders', 'evals']) {
    for (const [id, record] of merged[storeName]) {
      if (!workflows.has(String(record.workflowId))) merged[storeName].delete(id);
    }
  }
  for (const [child, parent, field] of [
    ['experimentAssignments', 'experiments', 'experimentId'],
    ['graderObservations', 'graders', 'graderId'],
    ['evalObservations', 'evals', 'evalId']
  ]) {
    for (const [id, record] of merged[child]) {
      const definition = merged[parent].get(String(record[field]));
      const run = runs.get(String(record.runId));
      if (!definition || !run || definition.workflowId !== run.workflowId) merged[child].delete(id);
    }
  }
  for (const [id, record] of merged.operationalValues) {
    if (!repositories.has(String(record.repositoryId))) merged.operationalValues.delete(id);
  }
  for (const [id, record] of merged.operationalValueEvidence) {
    if (!repositories.has(String(record.repositoryId))
      || workflows.get(String(record.workflowId))?.repositoryId !== record.repositoryId) merged.operationalValueEvidence.delete(id);
  }
  for (const [id, record] of merged.workflowValueCollections) {
    if (!repositories.has(String(record.repositoryId))
      || workflows.get(String(record.workflowId))?.repositoryId !== record.repositoryId) merged.workflowValueCollections.delete(id);
  }
  if (orphanedRunCount > 0) debugRetention({ event: 'orphans-pruned', orphanedRunCount });
}

/**
 * Collects repository and workflow parents that neither the current collection
 * nor any retained descendant still references. Campaign records are durable
 * inventory and are never collected during imports.
 *
 * @param {Record<string, Map<string, Record<string, unknown>>>} merged
 * @param {import('../model/schema.js').CanonicalBatch} incoming
 */
function collectUnreferencedParents(merged, incoming) {
  const incomingWorkflows = new Set(incoming.workflows.map((record) => String(record.id)));
  const incomingRepositories = new Set(incoming.repositories.map((record) => String(record.id)));
  const referencedWorkflows = new Set([
    ...[...merged.runs.values()].map((run) => String(run.workflowId)),
    ...[...merged.operationalValueEvidence.values()].map((packet) => String(packet.workflowId)),
    ...[...merged.workflowValueCollections.values()].map((record) => String(record.workflowId)),
    ...['experiments', 'graders', 'evals'].flatMap((store) =>
      [...merged[store].values()].map((record) => String(record.workflowId)))
  ]);
  for (const [id] of merged.workflows) {
    if (!incomingWorkflows.has(id) && !referencedWorkflows.has(id)) merged.workflows.delete(id);
  }
  const referencedRepositories = new Set([
    ...[...merged.workflows.values()].map((workflow) => String(workflow.repositoryId)),
    ...[...merged.runs.values()].map((run) => String(run.repositoryId)),
    ...[...merged.operationalValues.values()].map((value) => String(value.repositoryId)),
    ...[...merged.operationalValueEvidence.values()].map((value) => String(value.repositoryId)),
    ...[...merged.workflowValueCollections.values()].map((value) => String(value.repositoryId))
  ]);
  for (const [id] of merged.repositories) {
    if (!incomingRepositories.has(id) && !referencedRepositories.has(id)) merged.repositories.delete(id);
  }
}

/**
 * Upserts one freshly collected batch onto the previously stored records so
 * partial collections never drop retained history, prunes records observed
 * outside the 30-day retention window, and keeps every surviving record
 * relationship-safe.
 *
 * The window ends at the newest incoming observation whenever the collection
 * reports observations later than `now`, so a browser clock behind the
 * producer's clock cannot prune records the current collection still reports.
 *
 * @param {import('../model/schema.js').CanonicalBatch} previous
 * @param {import('../model/schema.js').CanonicalBatch} incoming
 * @param {{
 *   now?: number,
 *   retentionWindowMs?: number,
 *   retentionWindowMsByStore?: Partial<Record<typeof STORES[number], number>>,
 *   includePreviousInReference?: boolean,
 *   preserveUnreferencedParents?: boolean,
 *   preserveWorkflowCampaignMappings?: boolean,
 *   preserveRepositoryRecords?: boolean
 * }} [options]
 * @returns {import('../model/schema.js').CanonicalBatch}
 */
export function mergeRetainedRecords(previous, incoming, options = {}) {
  const now = Number.isFinite(options.now) ? Number(options.now) : Date.now();
  const reference = Math.max(
    now,
    newestObservation(incoming) ?? now,
    options.includePreviousInReference ? (newestObservation(previous) ?? now) : now
  );
  const retentionWindowMs = Number.isFinite(options.retentionWindowMs)
    ? Math.max(0, Number(options.retentionWindowMs))
    : RETENTION_WINDOW_MS;
  const merged = upsertRecords(
    previous,
    incoming,
    reference,
    retentionWindowMs,
    options.retentionWindowMsByStore ?? {},
    options.preserveWorkflowCampaignMappings === true,
    options.preserveRepositoryRecords === true
  );
  pruneOrphans(merged);
  if (!options.preserveUnreferencedParents) collectUnreferencedParents(merged, incoming);

  const batch = /** @type {import('../model/schema.js').CanonicalBatch} */ (Object.fromEntries(
    STORES.map((storeName) => [
      storeName,
      [...merged[storeName].values()].sort((left, right) => String(left.id).localeCompare(String(right.id)))
    ])
  ));
  for (const storeName of RUN_LINKED_STORES) {
    batch[storeName] = orderRunRecords(batch[storeName] ?? []);
  }
  debugRetention({
    event: 'merged',
    retentionWindowMs,
    runCount: batch.runs.length,
    repositoryCount: batch.repositories.length,
    workflowCount: batch.workflows.length
  });
  return batch;
}
