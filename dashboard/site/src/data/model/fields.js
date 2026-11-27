import { createDebug } from '../../debug.js';
import { sourceId } from './ids.js';
import { alignSessionEventRecord } from './session-types.js';

const debugFields = createDebug('fields');

/** Raw payloads remain upstream; query-reconstructible values are not stored. */
export const PRUNED_CANONICAL_FIELDS = /** @type {Readonly<Record<string, readonly string[]>>} */ (Object.freeze({
  runs: Object.freeze([
    'aic', 'number', 'intentionalFailure', 'tokenUsage', 'ambientContext',
    'workingSet', 'behaviorFingerprint', 'taskDomain', 'comparison',
    'agenticAssessments', 'graders', 'context', 'data', 'logsPayload',
    'logsPath', 'auditPath', 'repositoryFullName'
  ]),
  repositories: Object.freeze(['fullName']),
  workflows: Object.freeze([
    'ghAwMetadata', 'ghAwManifest', 'campaign', 'campaignName', 'campaignIcon',
    'campaignReadmePath', 'campaignAiCreditAllowance', 'campaignWorkerCount',
    'campaignInventoryWarnings', 'ghAwVersionLabel'
  ]),
  tools: Object.freeze(['isSkill']),
  skills: Object.freeze(['isSkill']),
  issues: Object.freeze([
    'issueState', 'issueClosed', 'issueStateReason', 'issueClosedAt',
    'issueStatusObservedAt'
  ]),
  audits: Object.freeze(['targetRepo']),
  graderObservations: Object.freeze(['sourceGraderId', 'resultTimestamp']),
  evalObservations: Object.freeze(['sourceEvalId', 'resultTimestamp', 'answer', 'requestedModel', 'resolvedModel'])
}));

/** @param {Record<string, unknown>} record @param {string} field @param {string} copy @param {boolean} [timestamp] */
function retainFact(record, field, copy, timestamp = false) {
  if (!Object.hasOwn(record, copy) || record[copy] === undefined) return record;
  if (record[field] === undefined) return { ...record, [field]: record[copy] };
  const fact = record[field];
  const duplicate = record[copy];
  const agrees = fact === duplicate || (timestamp && typeof fact === 'string' && typeof duplicate === 'string'
    && Number.isFinite(Date.parse(fact)) && Date.parse(fact) === Date.parse(duplicate));
  if (!agrees) {
    debugFields({ event: 'retain-fact-rejected', field, copy });
    throw new TypeError(`${field} conflicts with recomputable ${copy}`);
  }
  return record;
}

/** @param {Record<string, unknown>} record @param {string} copy @param {string} owner @param {string} repository */
function retainCoordinates(record, copy, owner, repository) {
  const value = record[copy];
  if (value === undefined || value === null) return record;
  if (typeof value !== 'string') throw new TypeError(`${copy} must be an owner/repository coordinate`);
  const coordinate = value.match(/^([A-Za-z0-9][A-Za-z0-9-]*)\/([A-Za-z0-9._-]+)$/);
  if (!coordinate) {
    debugFields({ event: 'retain-coordinates-rejected', copy, reason: 'unmatched-coordinate' });
    throw new TypeError(`${copy} must be an owner/repository coordinate`);
  }
  for (const [field, value] of [[owner, coordinate[1]], [repository, coordinate[2]]]) {
    if (record[field] !== undefined && record[field] !== value) {
      throw new TypeError(`${field} conflicts with recomputable ${copy}`);
    }
  }
  return { ...record, [owner]: coordinate[1], [repository]: coordinate[2] };
}

/**
 * Applies the same pruning to newly normalized observations and published shards.
 * Unknown evidence is retained; this is not a query-only allowlist.
 * @param {string} collection
 * @param {Record<string, unknown>} record
 * @returns {Record<string, unknown>}
 */
export function pruneCanonicalRecord(collection, record) {
  if (['domains', 'tools', 'skills', 'audits', 'issues'].includes(collection)) {
    record = alignSessionEventRecord(record);
  }
  const fields = PRUNED_CANONICAL_FIELDS[collection];
  if (!fields?.some((field) => Object.hasOwn(record, field))) return record;
  if (collection === 'audits') record = retainCoordinates(record, 'targetRepo', 'targetOrganization', 'targetRepository');
  if (collection === 'repositories') record = retainCoordinates(record, 'fullName', 'owner', 'name');
  if (collection === 'runs') record = retainCoordinates(record, 'repositoryFullName', 'owner', 'repository');
  if (collection === 'workflows' && record.campaignId == null && record.campaign != null && record.campaign !== '') {
    if (typeof record.campaign !== 'string' || !record.campaign.trim()) {
      debugFields({ event: 'campaign-slug-rejected' });
      throw new TypeError('workflow.campaign must be a nonempty slug');
    }
    record = { ...record, campaignId: sourceId('campaign', 'dashboard-sources', record.campaign.trim()) };
  }
  if (collection === 'graderObservations' || collection === 'evalObservations') {
    record = retainFact(record, 'timestamp', 'resultTimestamp', true);
  }
  if (collection === 'evalObservations') record = retainFact(record, 'evalResult', 'answer');
  return Object.fromEntries(Object.entries(record).filter(([field]) => !fields.includes(field)));
}

/**
 * Missing usage in a refresh is not a retraction of measured usage. Reruns
 * have separate usage, even though their canonical run identity is shared.
 * @param {Record<string, unknown> | undefined} existing
 * @param {Record<string, unknown>} incoming
 */
export function mergeCanonicalRun(existing, incoming) {
  if (!existing || !Number.isInteger(existing.attempt) || Number(existing.attempt) < 1
      || existing.attempt !== incoming.attempt) return incoming;
  let merged = incoming;
  if (incoming.routingAggregateVersion === undefined && existing.routingAggregateVersion !== undefined) {
    merged = { ...incoming, ...Object.fromEntries(Object.entries(existing)
      .filter(([field]) => field.startsWith('routing'))) };
  }
  for (const field of ['aicTotal', 'inputTokens', 'outputTokens', 'cacheReadTokens',
    'cacheWriteTokens', 'reasoningTokens']) {
    if (incoming[field] == null && typeof existing[field] === 'number'
        && Number.isFinite(existing[field]) && existing[field] >= 0) {
      if (merged === incoming) merged = { ...incoming };
      merged[field] = existing[field];
    }
  }
  return merged;
}

/** @param {unknown} value */
function knownCompilerVersion(value) {
  return typeof value === 'string' && value.trim() !== '' && value.trim().toLowerCase() !== 'unknown';
}

/**
 * @param {Record<string, unknown> | undefined} existing
 * @param {Record<string, unknown>} incoming
 */
export function mergeWorkflowCompilerEvidence(existing, incoming) {
  const result = { ...existing, ...incoming };
  for (const prefix of ['ghAwInventory', 'ghAwRun']) {
    const version = `${prefix}Version`;
    const observed = `${version}ObservedAt`;
    const previousAt = Date.parse(String(existing?.[observed] ?? ''));
    const incomingAt = Date.parse(String(incoming[observed] ?? ''));
    const takeIncoming = knownCompilerVersion(incoming[version]) && Number.isFinite(incomingAt)
      && (!knownCompilerVersion(existing?.[version]) || !Number.isFinite(previousAt) || incomingAt >= previousAt);
    const source = takeIncoming ? incoming : existing;
    for (const field of [version, observed, ...(prefix === 'ghAwRun'
      ? ['ghAwRunVersionRunId'] : ['ghAwInventoryUpdateState'])]) {
      if (source?.[field] !== undefined) result[field] = source[field];
      else delete result[field];
    }
  }
  const runAt = Date.parse(String(result.ghAwRunVersionObservedAt ?? ''));
  const inventoryAt = Date.parse(String(result.ghAwInventoryVersionObservedAt ?? ''));
  if (knownCompilerVersion(result.ghAwRunVersion) && Number.isFinite(runAt)
      && (!knownCompilerVersion(result.ghAwInventoryVersion) || !Number.isFinite(inventoryAt) || runAt >= inventoryAt)) {
    result.ghAwVersion = result.ghAwRunVersion;
    result.ghAwVersionSource = 'run';
    result.ghAwUpdateState = result.ghAwRunVersion === result.ghAwInventoryVersion
      ? result.ghAwInventoryUpdateState ?? 'unknown' : 'unknown';
  } else if (knownCompilerVersion(result.ghAwInventoryVersion) && Number.isFinite(inventoryAt)) {
    result.ghAwVersion = result.ghAwInventoryVersion;
    result.ghAwVersionSource = 'inventory';
    result.ghAwUpdateState = result.ghAwInventoryUpdateState ?? 'unknown';
  } else if (existing && knownCompilerVersion(existing.ghAwVersion) && !knownCompilerVersion(incoming.ghAwVersion)) {
    result.ghAwVersion = existing.ghAwVersion;
    result.ghAwUpdateState = existing.ghAwUpdateState;
    result.ghAwVersionSource = existing.ghAwVersionSource;
  }
  return result;
}
