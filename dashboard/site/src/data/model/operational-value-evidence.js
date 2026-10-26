import { createDebug } from '../../debug.js';
import { repositoryCoordinateId, workflowCoordinateId } from './ids.js';

const debugEvidence = createDebug('operational-value-evidence');
const kinds = new Set(['contract', 'intent-source', 'requirement', 'output', 'output-implementation',
  'output-evidence', 'implementation', 'implementation-evidence', 'implementation-lineage',
  'snapshot', 'snapshot-item', 'quality-check', 'coverage', 'limitation']);
export const EVIDENCE_FACT_FIELDS = [
  'fact-kind', 'fact-id', 'parent-id', 'related-id', 'label', 'title', 'url', 'at', 'end-at', 'accepted-at',
  'value', 'ref', 'state', 'origin', 'scope', 'coverage-status', 'status', 'detail',
  'goal', 'acceptance', 'metric-id', 'metric-name', 'metric-unit', 'metric-direction',
  'metric-version', 'metric-formula', 'result-label', 'measurement-basis',
  'snapshot-name', 'snapshot-unit', 'snapshot-direction', 'snapshot-id', 'snapshot-version',
  'snapshot-formula', 'outputs-available', 'repository-state-available', 'checks-available',
  'items-available', 'output-unit', 'implementation-unit', 'output-scope', 'output-coverage',
  'workflow-name', 'cadence-days', 'maturation-days'
];
const numbers = new Set(['value', 'cadence-days', 'maturation-days']);
const booleans = new Set(['outputs-available', 'repository-state-available', 'checks-available', 'items-available']);
const timestamps = new Set(['at', 'end-at', 'accepted-at']);
const coordinate = /^[A-Za-z0-9][A-Za-z0-9-]*\/[A-Za-z0-9._-]+$/;
const slug = /^[a-z0-9]+(?:-[a-z0-9]+)*$/;
/** @param {unknown} value */
const string = (value) => typeof value === 'string' && !!value.trim() && value.length <= 16384;
/** @param {unknown} value */
const instant = (value) => typeof value === 'string'
  && /^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(?:\.\d+)?Z$/.test(value) && Number.isFinite(Date.parse(value))
  && new Date(value).toISOString().slice(0, 19) === value.slice(0, 19);

/** Stable identity excludes capture time: a new packet replaces the entire revision.
 * @param {string} repository @param {string} workflow @param {string} metric @param {string} version */
export function operationalValueEvidenceId(repository, workflow, metric, version) {
  return `operational-value-evidence:${encodeURIComponent(`${repository.toLowerCase()}:${workflow}:${metric}:${version}`)}`;
}

/** Validate bounded acquired facts without deriving report state or inventing absent evidence.
 * @param {Record<string, any>} packet */
export function validateOperationalValueEvidence(packet) {
  /** @param {string} reason @returns {never} */
  const fail = (reason) => { throw new TypeError(`Invalid operational value evidence: ${reason}`); };
  if (!packet || typeof packet !== 'object' || packet.schemaVersion !== 1
    || !coordinate.test(packet.repository) || !slug.test(packet.workflow)
    || packet.sourcePath !== `.github/workflows/${packet.workflow}.md`
    || !instant(packet.observedAt) || !instant(packet.windowStart) || !instant(packet.windowEnd)
    || Date.parse(packet.windowStart) >= Date.parse(packet.windowEnd)
    || Date.parse(packet.windowEnd) > Date.parse(packet.observedAt)
    || !/^[a-f0-9]{64}$/.test(packet.contractDigest)
    || !Array.isArray(packet.facts) || !packet.facts.length || packet.facts.length > 20000
    || new TextEncoder().encode(JSON.stringify(packet)).length > 8 * 1024 * 1024) fail('packet identity, bounds or capture');
  if (Object.keys(packet).some((field) => !['schemaVersion', 'id', 'repository', 'workflow', 'sourcePath',
    'observedAt', 'windowStart', 'windowEnd', 'contractDigest', 'facts', 'repositoryId', 'workflowId', 'provenance'].includes(field))) fail('unknown packet field');
  const [owner, repository] = packet.repository.split('/');
  if (packet.repositoryId !== undefined && packet.repositoryId !== repositoryCoordinateId(owner, repository)
    || packet.workflowId !== undefined && packet.workflowId !== workflowCoordinateId(owner, repository, packet.sourcePath)) fail('structural parent identity');
  const facts = /** @type {Record<string, any>[]} */ (packet.facts);
  const headers = facts.filter((fact) => fact['fact-kind'] === 'contract');
  if (headers.length !== 1) fail('one contract header is required');
  const header = headers[0];
  for (const key of ['goal', 'acceptance', 'workflow-name', 'metric-id', 'metric-name', 'metric-unit', 'metric-direction',
    'metric-version', 'metric-formula', 'measurement-basis']) if (!string(header[key])) fail(`contract ${key}`);
  if (!['increase', 'decrease'].includes(header['metric-direction'])
    || !['repository-outcome', 'workflow-contribution'].includes(header['measurement-basis'])
    || [...booleans].filter((key) => key !== 'items-available').some((key) => typeof header[key] !== 'boolean')
    || packet.id !== operationalValueEvidenceId(packet.repository, packet.workflow, header['metric-id'], header['metric-version'])) fail('contract identity');
  if (typeof header['cadence-days'] !== 'number' || header['cadence-days'] <= 0
    || (header['maturation-days'] !== undefined && (!Number.isSafeInteger(header['maturation-days']) || header['maturation-days'] < 0))) fail('cadence or maturation');
  if (header['repository-state-available'] && ['snapshot-id', 'snapshot-name', 'snapshot-unit',
    'snapshot-version', 'snapshot-formula'].some((field) => !string(header[field]))) fail('snapshot contract');
  if (header['repository-state-available'] && !['increase', 'decrease'].includes(header['snapshot-direction'])) fail('snapshot direction');
  if (!facts.some((fact) => fact['fact-kind'] === 'coverage')) fail('coverage facts are required');
  const keys = new Set();
  const parents = new Map();
  for (const fact of facts) {
    if (!fact || !kinds.has(fact['fact-kind']) || !string(fact['fact-id'])) fail('fact identity');
    const key = JSON.stringify([fact['fact-kind'], fact['parent-id'] ?? '', fact['fact-id']]);
    if (keys.has(key)) fail('duplicate fact identity');
    keys.add(key);
    for (const [field, value] of Object.entries(fact)) {
      if (!EVIDENCE_FACT_FIELDS.includes(field)) fail(`unknown fact field ${field}`);
      if (numbers.has(field) ? typeof value !== 'number' || !Number.isFinite(value)
        : booleans.has(field) ? typeof value !== 'boolean'
          : timestamps.has(field) ? !instant(value) : !string(value)) fail(`fact ${field}`);
      if (timestamps.has(field) && Date.parse(value) > Date.parse(packet.observedAt)) fail('fact after capture');
      if (field === 'url') {
        let url;
        try { url = new URL(value); } catch { fail('URL'); }
        if (url.protocol !== 'https:' || url.username || url.password) fail('unsafe URL');
      }
      if (field === 'ref' && (!/^[A-Za-z0-9][A-Za-z0-9:._/-]*$/.test(value)
        || /^(?:HEAD|main|master|refs\/heads\/)/i.test(value))) fail('mutable reference');
    }
    if (['output', 'implementation', 'snapshot'].includes(fact['fact-kind'])) {
      parents.set(`${fact['fact-kind']}:${fact['fact-id']}`, fact);
    }
    if (['implementation', 'snapshot'].includes(fact['fact-kind'])
      && (!instant(fact.at) || !string(fact.ref) || typeof fact.value !== 'number')) fail('measurement facts');
    if (fact['fact-kind'] === 'snapshot' && typeof fact['items-available'] !== 'boolean') fail('snapshot item availability');
    if (fact['fact-kind'] === 'implementation'
      && (Date.parse(fact['accepted-at']) !== Date.parse(fact.at)
        || !['receipt-verified', 'corroborated', 'not-applicable'].includes(fact.origin))) fail('original acceptance');
    if (fact['fact-kind'] === 'output'
      && (!instant(fact.at) || !string(fact.label) || !string(fact.title) || !string(fact.url)
        || !['open', 'closed', 'unknown'].includes(fact.state)
        || !['receipt-verified', 'corroborated'].includes(fact.origin))) fail('output lifecycle facts');
    if (['implementation', 'output', 'snapshot'].includes(fact['fact-kind'])
      && (Date.parse(fact.at) < Date.parse(packet.windowStart)
        || Date.parse(fact.at) > Date.parse(packet.windowEnd))) fail('fact outside window');
    if (fact['fact-kind'] === 'coverage'
      && (!instant(fact.at) || !instant(fact['end-at']) || Date.parse(fact.at) >= Date.parse(fact['end-at'])
        || Date.parse(fact.at) < Date.parse(packet.windowStart) || Date.parse(fact['end-at']) > Date.parse(packet.windowEnd)
        || !['complete', 'partial', 'missing', 'unknown'].includes(fact['coverage-status']))) fail('coverage');
    if (fact['fact-kind'] === 'quality-check' && !['verified', 'not-verified'].includes(fact.status)) fail('quality status');
  }
  for (const fact of facts) {
    const kind = fact['fact-kind'];
    const parentKind = kind.startsWith('output-') ? 'output'
      : kind.startsWith('implementation-') ? 'implementation' : kind === 'snapshot-item' ? 'snapshot' : null;
    if (parentKind && !parents.has(`${parentKind}:${fact['parent-id']}`)) fail('missing parent');
    if (kind === 'snapshot-item' && !parents.get(`snapshot:${fact['parent-id']}`)['items-available']) fail('snapshot item availability contradiction');
    if (kind === 'implementation-lineage' && header['outputs-available']
      && !parents.has(`output:${fact['related-id']}`)) fail('missing lineage output');
    if (kind === 'output-implementation') {
      const event = parents.get(`implementation:${fact['related-id']}`);
      const output = parents.get(`output:${fact['parent-id']}`);
      if (!event || Date.parse(event.at) < Date.parse(output.at)) fail('implementation edge');
    }
  }
  const coverage = facts.filter((fact) => fact['fact-kind'] === 'coverage')
    .sort((left, right) => Date.parse(left.at) - Date.parse(right.at));
  if (coverage.some((fact, index) => index > 0
    && Date.parse(fact.at) < Date.parse(coverage[index - 1]['end-at']))) fail('overlapping coverage');
  if (!header['outputs-available'] && facts.some((fact) => fact['fact-kind'] === 'output')
    || !header['repository-state-available'] && facts.some((fact) => fact['fact-kind'] === 'snapshot')
    || !header['checks-available'] && facts.some((fact) => fact['fact-kind'] === 'quality-check')) fail('availability contradiction');
  return packet;
}

/** Pure source normalization; all selection, aggregation and comparison remain declarative.
 * @param {Record<string, any>[]} packets */
export function operationalValueEvidenceRows(packets) {
  return packets.flatMap((packet) => {
    validateOperationalValueEvidence(packet);
    const [organization, repository] = packet.repository.split('/');
    return /** @type {Record<string, any>[]} */ (packet.facts).map((fact) => ({
      'report-id': packet.id, organization, repository, workflow: packet.workflow, 'workflow-path': packet.sourcePath,
      'workflow-route': `${packet.repository}:${packet.sourcePath}`,
      'observed-at': packet.observedAt, 'window-start': packet.windowStart,
      'window-end': packet.windowEnd, 'contract-digest': packet.contractDigest, ...fact
    }));
  });
}

/** Capture provenance does not assert complete evidence or current freshness.
 * @param {Record<string, any>[]} packets
 * @returns {{'as-of': string, 'retrieved-at': string, completeness: 'unknown', freshness: 'unknown'}} */
export function operationalValueEvidenceMetadata(packets) {
  /** @param {string} field */
  const oldest = (field) => packets.map((packet) => packet[field])
    .sort((left, right) => Date.parse(left) - Date.parse(right))[0] ?? '';
  return {
    'as-of': oldest('windowEnd'), 'retrieved-at': oldest('observedAt'),
    completeness: 'unknown', freshness: 'unknown'
  };
}

/** @param {Record<string, any> | undefined} previous @param {Record<string, any>} incoming */
export function mergeOperationalValueEvidence(previous, incoming) {
  validateOperationalValueEvidence(incoming);
  if (!previous) return incoming;
  if (previous.contractDigest !== incoming.contractDigest) {
    debugEvidence({ event: 'merge', outcome: 'contract-changed' });
    throw new TypeError('Frozen evidence contract changed without a metric version change');
  }
  if (Date.parse(previous.observedAt) === Date.parse(incoming.observedAt)
    && ['windowStart', 'windowEnd', 'sourcePath', 'repository', 'workflow', 'facts']
      .some((field) => JSON.stringify(previous[field]) !== JSON.stringify(incoming[field]))) {
    debugEvidence({ event: 'merge', outcome: 'conflict' });
    throw new TypeError('Conflicting evidence packet at the same capture time');
  }
  const keepPrevious = Date.parse(previous.observedAt) > Date.parse(incoming.observedAt);
  debugEvidence({ event: 'merge', outcome: keepPrevious ? 'kept-previous' : 'replaced', factCount: incoming.facts.length });
  return keepPrevious ? previous : incoming;
}
