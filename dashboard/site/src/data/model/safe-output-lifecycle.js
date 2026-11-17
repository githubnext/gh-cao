import { createDebug } from '../../debug.js';
import { canonicalTimestamp, requiredString } from './schema.js';
import registry from './safe-output-adoption-rules.json' with { type: 'json' };

const debugLifecycle = createDebug('safe-output-lifecycle:merge-conflict');

export const SAFE_OUTPUT_ADOPTION_RULE_VERSION = registry.version;

const RULES = new Map(registry.rules.map((rule) => [rule.type, Object.freeze({ ...rule })]));
const FAILED_DELIVERY_STATES = new Set(['error', 'failure', 'failed', 'skipped']);
const ADOPTION_FIELDS = [
  'adoptionState', 'adoptionSignal', 'adoptionAt', 'adoptionObservedAt', 'adoptionSource'
];
const SURVIVAL_FIELDS = ['survivalState', 'survivalSignal', 'survivalObservedAt'];
const ADOPTION_SOURCE_AUTHORITY = new Map([
  ['adoption-registry', 1],
  ['delivery-rule', 1],
  ['github-webhook', 2],
  ['github-api', 3]
]);
const BASE64URL_ALPHABET = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_';

/** @param {Uint8Array} bytes */
function base64UrlEncode(bytes) {
  let encoded = '';
  for (let index = 0; index < bytes.length; index += 3) {
    const first = bytes[index];
    const second = bytes[index + 1];
    const third = bytes[index + 2];
    encoded += BASE64URL_ALPHABET[first >> 2];
    encoded += BASE64URL_ALPHABET[((first & 3) << 4) | ((second ?? 0) >> 4)];
    if (second !== undefined) {
      encoded += BASE64URL_ALPHABET[((second & 15) << 2) | ((third ?? 0) >> 6)];
    }
    if (third !== undefined) encoded += BASE64URL_ALPHABET[third & 63];
  }
  return encoded;
}

/** @param {string} outputType */
export function safeOutputAdoptionRule(outputType) {
  return RULES.get(outputType) ?? null;
}

/** @param {string} runId @param {string} outputType @param {string | undefined} targetUrl @param {string} evidenceId */
export function outputLifecycleId(runId, outputType, targetUrl, evidenceId) {
  const proposalIdentity = ['create_issue', 'create_pull_request'].includes(outputType) && targetUrl
    ? targetUrl
    : evidenceId;
  const encoded = base64UrlEncode(new TextEncoder().encode(`${runId}:${outputType}:${proposalIdentity}`));
  return `output-lifecycle:${encoded}`;
}

/** @param {string} outputType @param {string} deliveredAt */
export function evaluateSafeOutputDelivery(outputType, deliveredAt) {
  const rule = safeOutputAdoptionRule(outputType);
  if (!rule) {
    return { state: 'unknown', signal: 'unsupported_safe_output', source: 'adoption-registry' };
  }
  if (!rule.attributionEligible) {
    return { state: 'not_applicable', signal: 'no_repository_value_effect', source: 'adoption-registry' };
  }
  if (rule.effectClass === 'proposal' && !rule.fallbackToDelivery) {
    return { state: 'pending', signal: 'human_decision_pending', source: 'adoption-registry' };
  }
  if (rule.fallbackToDelivery) {
    return {
      state: 'accepted',
      signal: 'delivered',
      at: canonicalTimestamp(deliveredAt, 'safe output deliveredAt'),
      source: 'delivery-rule'
    };
  }
  return { state: 'unknown', signal: 'missing_acceptance_rule', source: 'adoption-registry' };
}

/** @param {unknown} value */
function safeTargetUrl(value) {
  if (typeof value !== 'string') return undefined;
  try {
    const url = new URL(value);
    return url.protocol === 'https:' && !url.username && !url.password
      && !url.search && !url.hash ? url.href : undefined;
  } catch {
    return undefined;
  }
}

/** @param {string | undefined} value */
function repositoryFromTargetUrl(value) {
  if (!value) return undefined;
  try {
    const url = new URL(value);
    if (url.hostname.toLowerCase() !== 'github.com') return undefined;
    const parts = url.pathname.split('/').filter(Boolean);
    return parts.length >= 2 ? `${parts[0]}/${parts[1]}`.toLowerCase() : undefined;
  } catch {
    return undefined;
  }
}

/** @param {Record<string, unknown>} record */
function successfulSafeOutput(record) {
  return record.type === 'safe_output.result'
    && typeof record.safeOutputType === 'string'
    && record.safeOutputType.trim() !== ''
    && !FAILED_DELIVERY_STATES.has(String(record.status ?? '').toLowerCase());
}

/**
 * @param {import('./schema.js').CanonicalObservation} observation
 * @param {Record<string, unknown>} record
 * @param {Record<string, unknown> | undefined} run
 */
export function outputLifecycleFromSafeOutput(observation, record, run) {
  if (!successfulSafeOutput(record) || !run) return null;
  const outputType = requiredString(record.safeOutputType, 'safeOutputType');
  const deliveredAt = canonicalTimestamp(record.timestamp ?? observation.observedAt, 'safe output deliveredAt');
  const observedAt = canonicalTimestamp(observation.observedAt, 'safe output observedAt');
  const evaluation = evaluateSafeOutputDelivery(outputType, deliveredAt);
  const targetUrl = safeTargetUrl(record.safeOutputUrl ?? record.correlationId);
  const repository = record.targetRepository ?? record.repo ?? repositoryFromTargetUrl(targetUrl);
  const targetRepository = typeof repository === 'string' && repository.trim()
    ? repository.trim().toLowerCase()
    : undefined;
  return {
    id: outputLifecycleId(String(record.runId), outputType, targetUrl, observation.sourceId),
    repositoryId: requiredString(run.repositoryId, 'output lifecycle repositoryId'),
    workflowId: requiredString(run.workflowId, 'output lifecycle workflowId'),
    runId: requiredString(record.runId, 'output lifecycle runId'),
    safeOutputType: outputType,
    targetRepository,
    targetUrl,
    title: typeof record.summary === 'string' && record.summary.trim() ? record.summary.trim() : undefined,
    deliveryState: 'delivered',
    deliverySignal: 'safe_output.result',
    deliveredAt,
    deliveryObservedAt: observedAt,
    deliverySource: String(record.source ?? observation.source),
    deliveryEvidenceId: observation.sourceId,
    adoptionState: evaluation.state,
    adoptionSignal: evaluation.signal,
    adoptionAt: evaluation.at,
    adoptionObservedAt: observedAt,
    adoptionSource: evaluation.source,
    ruleVersion: SAFE_OUTPUT_ADOPTION_RULE_VERSION,
    survivalState: 'unknown',
    observedAt
  };
}

/** @param {unknown} value */
function instant(value) {
  return typeof value === 'string' && Number.isFinite(Date.parse(value)) ? Date.parse(value) : Number.NEGATIVE_INFINITY;
}

/** @param {unknown} left @param {unknown} right */
function earliestTimestamp(left, right) {
  if (instant(left) === Number.NEGATIVE_INFINITY) return right;
  if (instant(right) === Number.NEGATIVE_INFINITY) return left;
  return instant(left) <= instant(right) ? left : right;
}

/** @param {unknown} left @param {unknown} right */
function latestTimestamp(left, right) {
  if (instant(left) === Number.NEGATIVE_INFINITY) return right;
  if (instant(right) === Number.NEGATIVE_INFINITY) return left;
  return instant(left) >= instant(right) ? left : right;
}

/** @param {Record<string, unknown>} left @param {Record<string, unknown>} right @param {string[]} fields */
function fieldsAgree(left, right, fields) {
  return fields.every((field) => left[field] === undefined || right[field] === undefined || left[field] === right[field]);
}

/** @param {unknown} source */
function adoptionAuthority(source) {
  return ADOPTION_SOURCE_AUTHORITY.get(String(source)) ?? 0;
}

/**
 * @param {unknown} id
 * @param {string} conflict Fixed field or evidence label, never record data.
 */
function lifecycleConflict(id, conflict) {
  debugLifecycle({ event: 'merge-conflict', conflict });
  return new TypeError(`output lifecycle ${String(id)} has conflicting ${conflict}`);
}

/**
 * @param {Record<string, unknown> | undefined} previous
 * @param {Record<string, unknown>} incoming
 */
export function mergeOutputLifecycle(previous, incoming) {
  if (!previous) return incoming;
  for (const field of [
    'id', 'repositoryId', 'workflowId', 'runId', 'safeOutputType',
    'deliveryState', 'deliverySignal', 'ruleVersion'
  ]) {
    if (previous[field] !== undefined && incoming[field] !== undefined && previous[field] !== incoming[field]) {
      throw lifecycleConflict(incoming.id, field);
    }
  }
  for (const field of ['targetRepository', 'targetUrl']) {
    if (previous[field] !== undefined && incoming[field] !== undefined && previous[field] !== incoming[field]) {
      throw lifecycleConflict(incoming.id, field);
    }
  }
  let merged = { ...previous, ...incoming };
  const previousAuthority = adoptionAuthority(previous.adoptionSource);
  const incomingAuthority = adoptionAuthority(incoming.adoptionSource);
  const previousAdoption = instant(previous.adoptionObservedAt);
  const incomingAdoption = instant(incoming.adoptionObservedAt);
  if (previousAuthority > incomingAuthority
      || previousAuthority === incomingAuthority && previousAdoption > incomingAdoption) {
    merged = { ...merged, ...Object.fromEntries(ADOPTION_FIELDS.map((field) => [field, previous[field]])) };
  } else if (previousAuthority === incomingAuthority && previousAdoption === incomingAdoption
      && !fieldsAgree(previous, incoming, ADOPTION_FIELDS)) {
    throw lifecycleConflict(incoming.id, 'adoption evidence');
  }
  const previousSurvival = instant(previous.survivalObservedAt);
  const incomingSurvival = instant(incoming.survivalObservedAt);
  if (previousSurvival > incomingSurvival) {
    merged = { ...merged, ...Object.fromEntries(SURVIVAL_FIELDS.map((field) => [field, previous[field]])) };
  } else if (previousSurvival === incomingSurvival && !fieldsAgree(previous, incoming, SURVIVAL_FIELDS)) {
    throw lifecycleConflict(incoming.id, 'survival evidence');
  }
  if (instant(previous.reversedAt) > instant(incoming.reversedAt)) {
    merged.reversalSignal = previous.reversalSignal;
    merged.reversedAt = previous.reversedAt;
  } else if (instant(previous.reversedAt) === instant(incoming.reversedAt)
      && !fieldsAgree(previous, incoming, ['reversalSignal', 'reversedAt'])) {
    throw lifecycleConflict(incoming.id, 'reversal evidence');
  }
  merged.deliveryObservedAt = earliestTimestamp(previous.deliveryObservedAt, incoming.deliveryObservedAt);
  merged.deliveredAt = earliestTimestamp(previous.deliveredAt, incoming.deliveredAt);
  merged.observedAt = latestTimestamp(previous.observedAt, incoming.observedAt);
  return Object.fromEntries(Object.entries(merged).filter(([, value]) => value !== undefined));
}

export function safeOutputAdoptionRules() {
  return [...RULES.values()].map((rule) => ({ ...rule }));
}
