import { createDebug } from '../../debug.js';
import { repositoryCoordinateId, workflowCoordinateId } from './ids.js';
import { timestampMilliseconds } from './schema.js';

const debug = createDebug('workflow-value-collection');

export const VALUE_COLLECTION_REASONS = {
  unconfigured: 'No collector is registered for this workflow in the reviewed policy.',
  disabled: 'The reviewed registration explicitly disables future collection; retained evidence is unchanged.',
  'activity-disabled': 'Activity collection is disabled in the reviewed policy.',
  'never-captured': 'No accepted capture is present in retained collection history.',
  'api-budget': 'GitHub API capacity is exhausted or reserved for ordinary Activity collection.',
  'budget-unavailable': 'GitHub API capacity could not be verified; retained evidence is unchanged.',
  'shared-budget-unavailable': 'Automatic remote acquisition is deferred until shared API request budgets can be enforced. Activity retains priority.',
  'fast-mode': 'Fast mode retains Value history without acquiring new evidence.',
  'public-only': 'Public-only collection retains Value history without acquiring new evidence.',
  timeout: 'The collector exceeded its time budget; previous accepted evidence is retained.',
  'collector-failed': 'The collector failed; previous accepted evidence is retained.',
  'invalid-evidence': 'The collector returned invalid or incomplete evidence; previous accepted evidence is retained.',
  'batch-budget': 'The collection batch exhausted its bounded time or output budget.',
  captured: 'Accepted evidence is available at its original capture and cutoff dates.',
  'not-due': 'The frozen cadence is already covered by retained evidence; no new evidence was acquired.'
};
const STATE_REASONS = {
  unconfigured: ['unconfigured'], disabled: ['disabled', 'activity-disabled'],
  'never-captured': ['never-captured'],
  deferred: ['api-budget', 'budget-unavailable', 'shared-budget-unavailable', 'fast-mode', 'public-only', 'batch-budget'],
  failed: ['api-budget', 'timeout', 'collector-failed', 'invalid-evidence', 'batch-budget'],
  available: ['captured', 'not-due']
};

/** @param {unknown} value */
function collectionTime(value) {
  if (typeof value !== 'string' || !value.endsWith('Z')) return null;
  const fraction = /\.(\d+)Z$/.exec(value)?.[1] ?? '';
  return /[1-9]/.test(fraction.slice(3)) ? null : timestampMilliseconds(value);
}

/** @param {string} repository @param {string} workflow */
export function workflowValueCollectionId(repository, workflow) {
  return `workflow-value-collection:${encodeURIComponent(`${repository.toLowerCase()}:${workflow}`)}`;
}

/** Collection facts never supply an outcome measurement or grant authority.
 * @param {Record<string, any>} record */
export function validateWorkflowValueCollection(record) {
  const fail = () => { debug({ operation: 'validate', status: 'rejected' }); throw new TypeError('Invalid workflow Value collection record'); };
  if (!record || record.schemaVersion !== 1
    || ['repository', 'workflow', 'observedAt', 'policyDigest'].some(field => typeof record[field] !== 'string')
    || !/^[A-Za-z0-9][A-Za-z0-9-]*\/[A-Za-z0-9._-]+$/.test(record.repository)
    || !/^[a-z0-9]+(?:-[a-z0-9]+)*$/.test(record.workflow)
    || record.sourcePath !== `.github/workflows/${record.workflow}.md`
    || record.id !== workflowValueCollectionId(record.repository, record.workflow)
    || collectionTime(record.observedAt) === null
    || !/^[a-f0-9]{64}$/.test(record.policyDigest)
    || !['enabled', 'disabled', 'unconfigured'].includes(record.registration)
    || !['unconfigured', 'disabled', 'never-captured', 'deferred', 'failed', 'available'].includes(record.state)
    || !Object.hasOwn(VALUE_COLLECTION_REASONS, record.reasonCode)
    || record.reason !== VALUE_COLLECTION_REASONS[/** @type {keyof typeof VALUE_COLLECTION_REASONS} */ (record.reasonCode)]
    || !STATE_REASONS[/** @type {keyof typeof STATE_REASONS} */ (record.state)]?.includes(record.reasonCode)) fail();
  const fields = new Set(['schemaVersion', 'id', 'repository', 'workflow', 'sourcePath', 'observedAt',
    'registration', 'collector', 'state', 'reasonCode', 'reason', 'policyDigest', 'lastAttemptAt',
    'lastAttemptOutcome', 'repositoryId', 'workflowId', 'provenance']);
  if (Object.keys(record).some(field => !fields.has(field))) fail();
  if (record.registration === 'unconfigured') {
    if (record.state !== 'unconfigured' || record.collector !== undefined) fail();
  } else if (record.collector !== undefined && (typeof record.collector !== 'string'
    || !/^[A-Za-z0-9._/-]+\.mjs$/.test(record.collector)
    || record.collector.split('/').some((/** @type {string} */ part) => !part || part === '.' || part === '..'))
    || record.registration === 'enabled' && record.collector === undefined
    || record.registration === 'disabled' && record.state !== 'disabled'
    || record.registration === 'enabled' && record.state === 'unconfigured') fail();
  if (record.lastAttemptAt !== undefined || record.lastAttemptOutcome !== undefined) {
    if (collectionTime(record.lastAttemptAt) === null
      || Date.parse(record.lastAttemptAt) > Date.parse(record.observedAt)
      || !['available', 'failed'].includes(record.lastAttemptOutcome)) fail();
  }
  const [owner, repository] = record.repository.split('/');
  if (record.repositoryId !== undefined && record.repositoryId !== repositoryCoordinateId(owner, repository)
    || record.workflowId !== undefined && record.workflowId !== workflowCoordinateId(owner, repository, record.sourcePath)) fail();
  return record;
}

/** Reconcile the decision and actual attempt independently of arrival order.
 * @param {Record<string, any> | undefined} previous @param {Record<string, any>} incoming
 * @returns {Record<string, any>} */
export function mergeWorkflowValueCollection(previous, incoming) {
  validateWorkflowValueCollection(incoming);
  if (previous && previous.id !== incoming.id) throw new TypeError('Cannot merge different workflow Value collection identities');
  const before = previous ? Date.parse(previous.observedAt) : -Infinity;
  const after = Date.parse(incoming.observedAt);
  const fields = ['registration', 'collector', 'state', 'reasonCode', 'policyDigest'];
  if (before === after && fields.some(field => previous?.[field] !== incoming[field])) {
    debug({ operation: 'merge', status: 'conflict', kind: 'decision', fields: fields.filter(field => previous?.[field] !== incoming[field]).join(',') });
    throw new TypeError('Conflicting workflow Value collection decisions at the same time');
  }
  const previousAttempt = timestampMilliseconds(previous?.lastAttemptAt);
  const incomingAttempt = timestampMilliseconds(incoming.lastAttemptAt);
  if (previousAttempt !== null && previousAttempt === incomingAttempt
    && previous?.lastAttemptOutcome !== incoming.lastAttemptOutcome) {
    debug({ operation: 'merge', status: 'conflict', kind: 'attempt' });
    throw new TypeError('Conflicting workflow Value collection outcomes for the same attempt');
  }
  const decision = before > after ? previous : incoming;
  const attempt = (previousAttempt ?? -Infinity) > (incomingAttempt ?? -Infinity) ? previous : incoming;
  return {
    ...decision,
    observedAt: new Date(Math.max(before, after)).toISOString(),
    ...(attempt?.lastAttemptAt ? {
      lastAttemptAt: new Date(attempt.lastAttemptAt).toISOString(),
      lastAttemptOutcome: attempt.lastAttemptOutcome,
    } : {}),
  };
}
