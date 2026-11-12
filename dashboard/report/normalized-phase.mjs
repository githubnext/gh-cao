import { NORMALIZED_COLLECTIONS } from './collections.mjs';

const RUN_COLLECTIONS = new Set([
  'campaigns', 'repositories', 'workflows', 'runs', 'experiments', 'experimentAssignments'
]);
const STRUCTURAL_COLLECTIONS = new Set(['campaigns', 'repositories', 'workflows', 'experiments', 'graders', 'evals', 'operationalValueEvidence', 'workflowValueCollections']);
export const STRUCTURAL_CONSOLIDATION_BUCKET = '0000-00-00';
const TIMESTAMP_FIELDS = ['startedAt', 'observedAt', 'timestamp', 'completedAt', 'createdAt', 'updatedAt'];

/**
 * Stable day buckets preserve historical shard hashes across publications.
 * @param {string} collection
 * @param {Record<string, unknown>} record
 */
export function consolidationBucket(collection, record) {
  if (STRUCTURAL_COLLECTIONS.has(collection)) return STRUCTURAL_CONSOLIDATION_BUCKET;
  for (const field of TIMESTAMP_FIELDS) {
    const value = record[field];
    if (typeof value !== 'string') continue;
    const parsed = Date.parse(value);
    if (Number.isFinite(parsed)) return new Date(parsed).toISOString().slice(0, 10);
  }
  return STRUCTURAL_CONSOLIDATION_BUCKET;
}

const EVIDENCE_RELATIONSHIPS = [
  ['experimentAssignments', 'experiments', 'experimentId'],
  ['graderObservations', 'graders', 'graderId'],
  ['evalObservations', 'evals', 'evalId']
];

export function relationshipSafeEvidenceBatch(batch, runWorkflowIds = new Map(
  (batch.runs ?? []).map((run) => [String(run.id), String(run.workflowId)])
)) {
  const safe = { ...batch };
  for (const [childCollection, parentCollection, parentField] of EVIDENCE_RELATIONSHIPS) {
    const parentWorkflowIds = new Map(
      (batch[parentCollection] ?? []).map((definition) =>
        [String(definition.id), String(definition.workflowId)])
    );
    safe[childCollection] = (batch[childCollection] ?? []).filter((record) => {
      const runWorkflowId = runWorkflowIds.get(String(record.runId));
      const definitionWorkflowId = parentWorkflowIds.get(String(record[parentField]));
      return runWorkflowId !== undefined && definitionWorkflowId === runWorkflowId;
    });
  }
  return safe;
}

export function normalizedPhaseBatch(batch, phase) {
  if (!['runs', 'records'].includes(phase)) throw new TypeError(`Unsupported normalized phase: ${phase}`);
  return Object.fromEntries(NORMALIZED_COLLECTIONS.map((collection) => {
    if (RUN_COLLECTIONS.has(collection) !== (phase === 'runs')) return [collection, []];
    const records = batch[collection] ?? [];
    return [collection, collection === 'audits'
      ? records.filter((audit) => String(audit.status ?? '').trim().toLowerCase() !== 'info')
      : records];
  }));
}
