import { createDebug } from '../../debug.js';

const debugSchema = createDebug('schema');

export const CANONICAL_SCHEMA_VERSION = 36;

export const ENTITY_KINDS = /** @type {const} */ ([
  'campaign',
  'repository',
  'workflow',
  'run',
  'domain',
  'tool',
  'skill',
  'friction',
  'audit',
  'issue',
  'operational-value',
  'operational-value-evidence',
  'workflow-value-collection',
  'marketplace-package',
  'experiment', 'experiment-assignment', 'grader', 'grader-observation', 'eval', 'eval-observation'
]);
export const EVIDENCE_DEFINITION_STORES = new Set(['experiments', 'graders', 'evals']);

/** @param {Record<string, unknown> | undefined} previous @param {Record<string, unknown>} incoming */
export function mergeEvidenceDefinition(previous, incoming) {
  if (!previous) return incoming;
  const before = String(previous.firstObservedAt ?? previous.observedAt ?? '');
  const after = String(incoming.firstObservedAt ?? incoming.observedAt ?? '');
  const previousLast = String(previous.lastObservedAt ?? previous.observedAt ?? '');
  const incomingLast = String(incoming.lastObservedAt ?? incoming.observedAt ?? '');
  return {
    ...(previousLast > incomingLast ? previous : incoming),
    firstObservedAt: [before, after].filter(Boolean).sort()[0],
    lastObservedAt: [previousLast, incomingLast].filter(Boolean).sort().at(-1)
  };
}
const RUN_LINKED_COLLECTIONS = /** @type {const} */ ([
  'domains',
  'tools',
  'skills',
  'friction',
  'audits',
  'issues'
]);

/** @typedef {typeof ENTITY_KINDS[number]} EntityKind */

/**
 * Source-neutral input emitted by adapters. Undefined data fields are ignored
 * during enrichment; null remains an explicit observed value.
 *
 * @typedef {object} CanonicalObservation
 * @property {EntityKind} kind
 * @property {string} source
 * @property {string} sourceId
 * @property {string} observedAt
 * @property {Record<string, unknown>} data
 */

/**
 * @typedef {object} CanonicalBatch
 * @property {Record<string, unknown>[]} campaigns
 * @property {Record<string, unknown>[]} repositories
 * @property {Record<string, unknown>[]} workflows
 * @property {Record<string, unknown>[]} runs
 * @property {Record<string, unknown>[]} domains
 * @property {Record<string, unknown>[]} tools
 * @property {Record<string, unknown>[]} skills
 * @property {Record<string, unknown>[]} friction
 * @property {Record<string, unknown>[]} audits
 * @property {Record<string, unknown>[]} issues
 * @property {Record<string, unknown>[]} operationalValues
 * @property {Record<string, unknown>[]} [operationalValueEvidence]
 * @property {Record<string, unknown>[]} [workflowValueCollections]
 * @property {Record<string, unknown>[]} marketplacePackages
 * @property {Record<string, unknown>[]} [experiments]
 * @property {Record<string, unknown>[]} [experimentAssignments]
 * @property {Record<string, unknown>[]} [graders]
 * @property {Record<string, unknown>[]} [graderObservations]
 * @property {Record<string, unknown>[]} [evals]
 * @property {Record<string, unknown>[]} [evalObservations]
 */

/**
 * @param {unknown} value
 * @param {string} field
 * @returns {string}
 */
export function requiredString(value, field) {
  if (typeof value !== 'string' || !value.trim()) {
    throw new TypeError(`${field} is required`);
  }
  return value.trim();
}

/**
 * @param {unknown} value
 * @param {string} field
 * @returns {string}
 */
export function canonicalTimestamp(value, field) {
  const input = requiredString(value, field);
  const milliseconds = Date.parse(input);
  if (!Number.isFinite(milliseconds)) throw new TypeError(`${field} must be a valid timestamp`);
  return new Date(milliseconds).toISOString();
}

/** @param {unknown} value @returns {number | null} */
export function timestampMilliseconds(value) {
  if (typeof value !== 'string') return null;
  const parts = /^([0-9]{4})-(0[1-9]|1[0-2])-(0[1-9]|[12][0-9]|3[01])T(?:[01][0-9]|2[0-3]):[0-5][0-9]:[0-5][0-9](?:\.[0-9]+)?(?:Z|[+-](?:[01][0-9]|2[0-3]):[0-5][0-9])$/.exec(value);
  if (!parts) return null;
  const year = Number(parts[1]);
  const month = Number(parts[2]);
  const day = Number(parts[3]);
  const leap = year % 4 === 0 && (year % 100 !== 0 || year % 400 === 0);
  if (year === 0 || day > [31, leap ? 29 : 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31][month - 1]) return null;
  const milliseconds = Date.parse(value);
  return Number.isFinite(milliseconds) ? milliseconds : null;
}

export const OPERATIONAL_VALUE_PERIOD_FIELDS = {
  windowStart: 'window-start', windowEnd: 'window-end', measurementKind: 'measurement-kind',
  coverageStatus: 'coverage-status', missingReason: 'missing-reason', eligibleCount: 'eligible-count',
  populationDigest: 'population-digest', evidenceDigest: 'evidence-digest'
};

/** @param {Record<string, unknown>} record @param {string} observedAt @param {string} field */
export function operationalValuePeriod(record, observedAt, field) {
  /** @type {Record<string, string | number>} */
  const metadata = {};
  const required = ['windowStart', 'windowEnd', 'measurementKind', 'coverageStatus'];
  if (required.some((key) => Object.hasOwn(record, key))) {
    if (timestampMilliseconds(record.windowStart) === null || timestampMilliseconds(record.windowEnd) === null) {
      throw new TypeError(`${field} must contain valid RFC 3339 measurement timestamps`);
    }
    const start = canonicalTimestamp(record.windowStart, `${field}.windowStart`);
    const end = canonicalTimestamp(record.windowEnd, `${field}.windowEnd`);
    if (Date.parse(start) >= Date.parse(end) || Date.parse(end) > Date.parse(observedAt)
      || typeof record.measurementKind !== 'string'
      || !['interval', 'snapshot'].includes(record.measurementKind)
      || typeof record.coverageStatus !== 'string'
      || !['complete', 'partial', 'missing', 'unknown'].includes(record.coverageStatus)) {
      throw new TypeError(`${field} must contain a valid measurement period and coverage`);
    }
    Object.assign(metadata, { windowStart: start, windowEnd: end,
      measurementKind: record.measurementKind, coverageStatus: record.coverageStatus });
  }
  for (const key of ['populationDigest', 'evidenceDigest']) {
    if (!Object.hasOwn(record, key)) continue;
    const value = record[key];
    if (typeof value !== 'string' || !/^[0-9a-f]{64}$/.test(value)) {
      throw new TypeError(`${field}.${key} must be a SHA-256 fingerprint`);
    }
    metadata[key] = value;
  }
  if (Object.hasOwn(record, 'eligibleCount')) {
    const count = record.eligibleCount;
    if (typeof count !== 'number' || !Number.isSafeInteger(count) || count < 0) {
      throw new TypeError(`${field}.eligibleCount must be a non-negative safe integer`);
    }
    metadata.eligibleCount = count;
  }
  if (Object.hasOwn(record, 'missingReason')) {
    metadata.missingReason = requiredString(record.missingReason, `${field}.missingReason`);
  }
  return metadata;
}

/**
 * Reports mandatory relationship failures without preventing partial entities
 * from existing during normalization.
 *
 * @param {CanonicalBatch} batch
 * @returns {string[]}
 */
export function relationshipErrors(batch) {
  debugSchema({
    event: 'validation-start',
    workflowCount: batch.workflows.length,
    runCount: batch.runs.length
  });
  const campaignRecords = batch.campaigns ?? [];
  const workflowsById = new Map(batch.workflows.map((record) => [record.id, record]));
  const ids = {
    repositories: new Set(batch.repositories.map((record) => record.id)),
    campaigns: new Set(campaignRecords.map((record) => record.id)),
    workflows: new Set(batch.workflows.map((record) => record.id)),
    runs: new Set(batch.runs.map((record) => record.id))
  };
  const entityNames = {
    repositories: 'repository',
    campaigns: 'campaign',
    workflows: 'workflow',
    runs: 'run'
  };
  /** @type {string[]} */
  const errors = [];

  /**
   * @param {Record<string, unknown>} record
   * @param {string} field
   * @param {keyof typeof ids} collection
   */
  const requireReference = (record, field, collection) => {
    const id = String(record.id ?? '<unknown>');
    const reference = record[field];
    if (typeof reference !== 'string' || !reference || !ids[collection].has(reference)) {
      errors.push(`${id}.${field} does not reference an existing ${entityNames[collection]}`);
    }
  };

  for (const workflow of batch.workflows) {
    requireReference(workflow, 'repositoryId', 'repositories');
    if (workflow.campaignId !== undefined && workflow.campaignId !== null) {
      requireReference(workflow, 'campaignId', 'campaigns');
    }
  }
  for (const run of batch.runs) {
    requireReference(run, 'repositoryId', 'repositories');
    requireReference(run, 'workflowId', 'workflows');
    const workflow = workflowsById.get(run.workflowId);
    if (workflow && workflow.repositoryId !== run.repositoryId) {
      errors.push(`${String(run.id ?? '<unknown>')}.workflowId references a workflow from another repository`);
    }
  }
  for (const collection of RUN_LINKED_COLLECTIONS) {
    for (const record of batch[collection] ?? []) {
      requireReference(record, 'runId', 'runs');
    }
  }
  for (const record of [...batch.operationalValues ?? [], ...batch.operationalValueEvidence ?? [], ...batch.workflowValueCollections ?? []]) {
    requireReference(record, 'repositoryId', 'repositories');
  }
  for (const record of [...batch.operationalValueEvidence ?? [], ...batch.workflowValueCollections ?? []]) {
    requireReference(record, 'workflowId', 'workflows');
    if (workflowsById.get(record.workflowId)?.repositoryId !== record.repositoryId) {
      errors.push(`${String(record.id)}.workflowId references a workflow from another repository`);
    }
  }
  const experiments = new Map((batch.experiments ?? []).map((record) => [record.id, record]));
  const graders = new Map((batch.graders ?? []).map((record) => [record.id, record]));
  const evals = new Map((batch.evals ?? []).map((record) => [record.id, record]));
  const runsById = new Map(batch.runs.map((record) => [record.id, record]));
  for (const record of batch.experiments ?? []) requireReference(record, 'workflowId', 'workflows');
  for (const record of batch.graders ?? []) requireReference(record, 'workflowId', 'workflows');
  for (const record of batch.evals ?? []) requireReference(record, 'workflowId', 'workflows');
  for (const [collection, parentField, parents] of /** @type {[Record<string, unknown>[] | undefined, string, Map<unknown, Record<string, unknown>>][]} */ ([
    [batch.experimentAssignments, 'experimentId', experiments],
    [batch.graderObservations, 'graderId', graders],
    [batch.evalObservations, 'evalId', evals]
  ])) {
    for (const record of collection ?? []) {
      requireReference(record, 'runId', 'runs');
      if (!parents.has(record[parentField])) errors.push(`${String(record.id)}.${parentField} does not reference a definition`);
      const parent = parents.get(record[parentField]);
      const run = runsById.get(record.runId);
      if (parent && run && parent.workflowId !== run.workflowId) {
        errors.push(`${String(record.id)}.${parentField} references a different workflow`);
      }
    }
  }

  if (errors.length > 0) {
    debugSchema({ event: 'validation-failed', errorCount: errors.length });
  } else {
    debugSchema({ event: 'validation-passed' });
  }

  return errors;
}