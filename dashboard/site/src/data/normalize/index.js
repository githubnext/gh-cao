import { issueCoordinates, issueId, operationalValueId, repositoryId, runId, sourceId, workflowId } from '../model/ids.js';
import { canonicalTimestamp, requiredString } from '../model/schema.js';
import { mergeCanonicalRun, pruneCanonicalRecord } from '../model/fields.js';
import { createDebug } from '../../debug.js';
import { curateBatchAudits } from '../model/audit-curation.js';
import { mergeOperationalValueEvidence } from '../model/operational-value-evidence.js';
import { mergeWorkflowValueCollection } from '../model/workflow-value-collection.js';
import { mergeOutputLifecycle, outputLifecycleFromSafeOutput } from '../model/safe-output-lifecycle.js';
import { compactRoutingRuns } from './model-routing.js';

const debugNormalize = createDebug('normalize:index');

/** @type {Record<import('../model/schema.js').EntityKind, keyof import('../model/schema.js').CanonicalBatch>} */
const COLLECTIONS = {
  campaign: 'campaigns',
  repository: 'repositories',
  workflow: 'workflows',
  run: 'runs',
  domain: 'domains',
  tool: 'tools',
  skill: 'skills',
  friction: 'friction',
  audit: 'audits',
  issue: 'issues',
  'output-lifecycle': 'outputLifecycles',
  'operational-value': 'operationalValues',
  'operational-value-evidence': 'operationalValueEvidence',
  'workflow-value-collection': 'workflowValueCollections',
  'marketplace-package': 'marketplacePackages',
  experiment: 'experiments', 'experiment-assignment': 'experimentAssignments',
  grader: 'graders', 'grader-observation': 'graderObservations',
  eval: 'evals', 'eval-observation': 'evalObservations'
};
const RUN_LINKED_COLLECTIONS = /** @type {const} */ ([
  'domains',
  'tools',
  'skills',
  'friction',
  'audits',
  'issues'
]);
const EVIDENCE_DEFINITIONS = new Set(['experiments', 'graders', 'evals']);

/**
 * @param {unknown} value
 * @param {string} field
 */
function requiredIdentifier(value, field) {
  if ((typeof value !== 'string' && typeof value !== 'number') || !String(value).trim()) {
    throw new TypeError(`${field} is required`);
  }
  return value;
}

/** @param {Record<string, unknown>} value */
function withoutUndefined(value) {
  return Object.fromEntries(Object.entries(value).filter(([, field]) => field !== undefined));
}

/** @param {import('../model/schema.js').CanonicalObservation} observation */
function identityFor(observation) {
  const data = observation.data;
  if (!['run', 'issue', 'marketplace-package'].includes(observation.kind)
    && typeof data.id === 'string' && data.id.trim()) return data.id.trim();
  switch (observation.kind) {
    case 'campaign':
      return sourceId('campaign', observation.source, requiredIdentifier(data.slug, 'campaign.slug'));
    case 'repository': return repositoryId(requiredIdentifier(data.githubId, 'repository.githubId'));
    case 'workflow': return workflowId(requiredIdentifier(data.githubId, 'workflow.githubId'));
    case 'run': return runId(
      requiredString(data.owner, 'run.owner'),
      requiredString(data.repository, 'run.repository'),
      requiredIdentifier(data.githubRunId, 'run.githubRunId')
    );
    case 'marketplace-package': {
      const repository = requiredString(data.repository, 'marketplacePackage.repository').toLowerCase();
      const packagePath = typeof data.path === 'string'
        ? data.path.trim().replace(/^\/+|\/+$/g, '').toLowerCase()
        : '';
      return `${repository}${packagePath ? `/${packagePath}` : ''}`;
    }
    case 'issue': {
      const coordinates = data.owner !== undefined
        && data.repository !== undefined
        && data.number !== undefined
        ? data
        : issueCoordinates(requiredString(data.url, 'issue.url'));
      return issueId(
        requiredString(coordinates.owner, 'issue.owner'),
        requiredString(coordinates.repository, 'issue.repository'),
        requiredIdentifier(coordinates.number, 'issue.number')
      );
    }
    case 'operational-value':
      return operationalValueId({
        campaign: data.campaign === undefined ? undefined : requiredString(data.campaign, 'operationalValue.campaign'),
        repository: requiredString(data.repository, 'operationalValue.repository'),
        valueId: requiredString(data.valueId, 'operationalValue.valueId'),
        observedAt: requiredString(data.timestamp, 'operationalValue.timestamp')
      });
    case 'domain':
    case 'tool':
    case 'skill':
    case 'friction':
    case 'audit':
      return sourceId(observation.kind, observation.source, observation.sourceId);
  }
  throw new TypeError(`Unsupported observation kind: ${observation.kind}`);
}

/**
 * Sorts from least to most authoritative so deterministic enrichment applies
 * the winning observation last.
 *
 * @param {import('../model/schema.js').CanonicalObservation} left
 * @param {import('../model/schema.js').CanonicalObservation} right
 * @param {Record<string, number>} precedence
 */
function compareObservations(left, right, precedence) {
  return (precedence[left.source] ?? 0) - (precedence[right.source] ?? 0)
    || Date.parse(left.observedAt) - Date.parse(right.observedAt)
    || left.source.localeCompare(right.source)
    || left.sourceId.localeCompare(right.sourceId)
    || JSON.stringify(left.data).localeCompare(JSON.stringify(right.data));
}

/**
 * Assigns deterministic per-run sequence numbers to run-linked records.
 *
 * @param {Record<string, unknown>[]} records
 * @returns {Record<string, unknown>[]}
 */
export function orderRunRecords(records) {
  /** @type {Map<string, Record<string, unknown>[]>} */
  const byRun = new Map();
  for (const record of records) {
    const runId = requiredString(record.runId, 'record.runId');
    const runRecords = byRun.get(runId) ?? [];
    runRecords.push(record);
    byRun.set(runId, runRecords);
  }
  return [...byRun.entries()]
    .sort(([left], [right]) => left.localeCompare(right))
    .flatMap(([, runRecords]) => runRecords
      .sort((left, right) => {
        const sameSource = left.source === right.source;
        const leftSequence = Number(left.sourceSequence);
        const rightSequence = Number(right.sourceSequence);
        if (sameSource && Number.isFinite(leftSequence) && Number.isFinite(rightSequence)) {
          const difference = leftSequence - rightSequence;
          if (difference) return difference;
        }
        return String(left.timestamp).localeCompare(String(right.timestamp))
          || String(left.id).localeCompare(String(right.id));
      })
      .map((record, sequence) => record.sequence === sequence ? record : { ...record, sequence }));
}

/**
 * Purely converts source-neutral observations into deterministic canonical
 * entities. Persistence is intentionally a separate boundary.
 *
 * @param {import('../model/schema.js').CanonicalObservation[]} observations
 * @param {{ sourcePrecedence?: Record<string, number> }} [options]
 * @returns {import('../model/schema.js').CanonicalBatch}
 */
export function normalize(observations, options = {}) {
  debugNormalize({ event: 'normalize-start', observationCount: observations.length });
  const sourcePrecedence = options.sourcePrecedence ?? {};
  /** @type {Record<keyof import('../model/schema.js').CanonicalBatch, Map<string, Record<string, unknown>>>} */
  const entities = {
    campaigns: new Map(),
    repositories: new Map(),
    workflows: new Map(),
    runs: new Map(),
    domains: new Map(),
    tools: new Map(),
    skills: new Map(),
    friction: new Map(),
    audits: new Map(),
    issues: new Map(),
    outputLifecycles: new Map(),
    operationalValues: new Map(),
    operationalValueEvidence: new Map(),
    workflowValueCollections: new Map(),
    marketplacePackages: new Map(),
    experiments: new Map(), experimentAssignments: new Map(),
    graders: new Map(), graderObservations: new Map(), evals: new Map(), evalObservations: new Map()
  };

  const sorted = [...observations].sort((left, right) => compareObservations(left, right, sourcePrecedence));
  /** @type {{ observation: import('../model/schema.js').CanonicalObservation, record: Record<string, unknown> }[]} */
  const safeOutputs = [];
  for (const observation of sorted) {
    const collection = COLLECTIONS[observation.kind];
    if (!collection) {
      debugNormalize({ event: 'normalize-failed', reason: 'unsupported-kind', kind: observation.kind });
      throw new TypeError(`Unsupported observation kind: ${observation.kind}`);
    }
    const observedAt = canonicalTimestamp(observation.observedAt, 'observation.observedAt');
    const id = identityFor(observation);
    const current = entities[collection].get(id) ?? {};
    const definitionRange = EVIDENCE_DEFINITIONS.has(collection)
      ? {
          firstObservedAt: String(current.firstObservedAt ?? current.observedAt ?? observedAt) < observedAt
            ? String(current.firstObservedAt ?? current.observedAt) : observedAt,
          lastObservedAt: String(current.lastObservedAt ?? current.observedAt ?? observedAt) > observedAt
            ? String(current.lastObservedAt ?? current.observedAt) : observedAt
        }
      : {};
    const incoming = withoutUndefined(pruneCanonicalRecord(collection, observation.data));
    if ((collection === 'audits' || collection === 'issues') && incoming.type === 'safe_output.result') {
      safeOutputs.push({ observation: { ...observation, observedAt }, record: incoming });
    }
    const evidenceRecord = collection === 'operationalValueEvidence'
      ? mergeOperationalValueEvidence(entities[collection].get(id), incoming)
      : collection === 'workflowValueCollections'
      ? mergeWorkflowValueCollection(entities[collection].get(id), incoming)
      : collection === 'outputLifecycles'
      ? mergeOutputLifecycle(entities[collection].get(id), incoming) : null;
    entities[collection].set(id, {
      ...(collection === 'runs' ? mergeCanonicalRun(current, incoming)
        : evidenceRecord ?? { ...current, ...incoming }),
      ...definitionRange,
      id,
      observedAt: evidenceRecord ? evidenceRecord.observedAt : observedAt,
      provenance: evidenceRecord?.provenance ?? {
        source: observation.source,
        sourceId: observation.sourceId,
        observedAt
      }
    });
  }
  for (const { observation, record } of safeOutputs) {
    const lifecycle = outputLifecycleFromSafeOutput(
      observation,
      record,
      entities.runs.get(String(record.runId))
    );
    if (!lifecycle) continue;
    entities.outputLifecycles.set(
      String(lifecycle.id),
      mergeOutputLifecycle(entities.outputLifecycles.get(String(lifecycle.id)), lifecycle)
    );
  }

  const batch = /** @type {import('../model/schema.js').CanonicalBatch} */ (Object.fromEntries(
    Object.entries(entities).map(([collection, records]) => [
      collection,
      [...records.values()].sort((left, right) => String(left.id).localeCompare(String(right.id)))
    ])
  ));
  for (const collection of RUN_LINKED_COLLECTIONS) {
    batch[collection] = orderRunRecords(batch[collection]);
  }
  // Run-backed copies are curated after all Run observations have converged.
  batch.audits = curateBatchAudits({ ...batch, runs: [] });
  compactRoutingRuns(batch);
  const entityCount = Object.values(batch).reduce((total, records) => total + records.length, 0);
  debugNormalize({
    event: 'normalize-complete',
    observationCount: observations.length,
    entityCount,
    mergedCount: observations.length - entityCount
  });
  return batch;
}