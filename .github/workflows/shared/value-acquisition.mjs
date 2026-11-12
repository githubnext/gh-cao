import { createHash } from 'node:crypto';

const SOURCES = new Set(['canonical', 'github', 'git', 'archive']);
const POOLS = new Set(['local', 'core', 'search', 'graphql', 'git', 'archive']);
const CACHES = new Set(['canonical', 'invocation', 'immutable']);
const REQUIREMENT_KEYS = ['cache', 'pool', 'primitive', 'scope', 'source'];
const ACQUISITION_KEYS = ['requirements'];
const IDENTIFIER = /^[a-z0-9]+(?:[a-z0-9._:/-]*[a-z0-9])?$/;
const MAX_REQUIREMENTS = 64;

const mappings = {
  canonical: { pools: new Set(['local']), caches: new Set(['canonical']) },
  github: { pools: new Set(['core', 'search', 'graphql']), caches: new Set(['invocation']) },
  git: { pools: new Set(['git']), caches: new Set(['immutable']) },
  archive: { pools: new Set(['archive']), caches: new Set(['invocation']) },
};

function object(value, name) {
  if (!value || typeof value !== 'object' || Array.isArray(value)) {
    throw new Error(`${name} must be an object`);
  }
}

function exactKeys(value, expected, name) {
  const keys = Object.keys(value).sort();
  if (JSON.stringify(keys) !== JSON.stringify(expected)) {
    throw new Error(`${name} contains unknown or missing fields`);
  }
}

function identifier(value, name) {
  if (typeof value !== 'string' || value.length > 160 || !IDENTIFIER.test(value)) {
    throw new Error(`${name} must be a bounded lowercase identifier`);
  }
  return value;
}

export function valueAcquisitionRequirementKey(requirement) {
  return REQUIREMENT_KEYS.map(key => requirement[key]).join('\0');
}

export function valueAcquisitionRequirementId(requirement) {
  return createHash('sha256').update(valueAcquisitionRequirementKey(requirement)).digest('hex');
}

export function normalizeValueAcquisition(value) {
  if (value == null) return null;
  object(value, 'acquisition');
  exactKeys(value, ACQUISITION_KEYS, 'acquisition');
  if (!Array.isArray(value.requirements) || value.requirements.length === 0
    || value.requirements.length > MAX_REQUIREMENTS) {
    throw new Error(`acquisition.requirements must contain 1 through ${MAX_REQUIREMENTS} entries`);
  }
  const seen = new Set();
  const requirements = value.requirements.map((requirement, index) => {
    object(requirement, `acquisition.requirements[${index}]`);
    exactKeys(requirement, REQUIREMENT_KEYS, `acquisition.requirements[${index}]`);
    const normalized = {
      source: identifier(requirement.source, `acquisition.requirements[${index}].source`),
      pool: identifier(requirement.pool, `acquisition.requirements[${index}].pool`),
      primitive: identifier(requirement.primitive, `acquisition.requirements[${index}].primitive`),
      scope: identifier(requirement.scope, `acquisition.requirements[${index}].scope`),
      cache: identifier(requirement.cache, `acquisition.requirements[${index}].cache`),
    };
    if (!SOURCES.has(normalized.source) || !POOLS.has(normalized.pool) || !CACHES.has(normalized.cache)) {
      throw new Error(`acquisition.requirements[${index}] uses an unsupported source, pool, or cache`);
    }
    const mapping = mappings[normalized.source];
    if (!mapping.pools.has(normalized.pool) || !mapping.caches.has(normalized.cache)) {
      throw new Error(`acquisition.requirements[${index}] has an incompatible source, pool, or cache`);
    }
    const key = valueAcquisitionRequirementKey(normalized);
    if (seen.has(key)) throw new Error('acquisition.requirements contains duplicate entries');
    seen.add(key);
    return normalized;
  }).sort((left, right) =>
    valueAcquisitionRequirementKey(left).localeCompare(valueAcquisitionRequirementKey(right)));
  return { requirements };
}

export function valueAcquisitionDigest(value) {
  const normalized = normalizeValueAcquisition(value);
  if (normalized == null) return null;
  return createHash('sha256').update(JSON.stringify(normalized)).digest('hex');
}

export function sameValueAcquisition(left, right) {
  return JSON.stringify(normalizeValueAcquisition(left)) === JSON.stringify(normalizeValueAcquisition(right));
}

export function compileValueAcquisitionPlan(registrations) {
  if (!Array.isArray(registrations) || registrations.length > 1000) {
    throw new Error('Value acquisition registrations must be a bounded list');
  }
  const requirements = new Map();
  for (const entry of registrations) {
    const repository = String(entry?.repository ?? '').toLowerCase();
    const workflow = String(entry?.workflow ?? '');
    const acquisition = normalizeValueAcquisition(entry?.acquisition);
    if (!repository || !workflow || acquisition == null) continue;
    for (const requirement of acquisition.requirements) {
      const key = `${repository}\0${valueAcquisitionRequirementKey(requirement)}`;
      const current = requirements.get(key) ?? { repository, ...requirement, workflows: [] };
      current.workflows.push(workflow);
      requirements.set(key, current);
    }
  }
  return [...requirements.values()]
    .map(requirement => ({ ...requirement, workflows: [...new Set(requirement.workflows)].sort() }))
    .sort((left, right) =>
      `${left.repository}\0${valueAcquisitionRequirementKey(left)}`
        .localeCompare(`${right.repository}\0${valueAcquisitionRequirementKey(right)}`));
}
