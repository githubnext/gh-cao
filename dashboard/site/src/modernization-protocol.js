/** @typedef {'assess'|'approve-plan'|'run'|'verify'} ModernizationOperation */
/** @typedef {{ id: string, repository: string, title: string, phase?: string, state?: string, issueUrl?: string|null, pullRequestUrl?: string|null }} ModernizationTask */
/**
 * @typedef {{
 * schema: string, kind: 'status'|'preview'|'execution', status: string,
 * controlRepository: string, controlSha: string, reason?: string,
 * capabilities?: Partial<Record<ModernizationOperation, { authorized: boolean, reason?: string }>>,
 * plan?: Record<string, unknown> | null, planDigest?: string|null,
 * repositories?: string[], tasks?: ModernizationTask[],
 * links?: Record<string, string>, previewId?: string, action?: ModernizationOperation,
 * mode?: 'review'|'live', effects?: string[], warnings?: string[],
 * expiresAt?: string, canExecute?: boolean, accepted?: boolean,
 * workflowUrl?: string, runUrl?: string, approvalCommitUrl?: string
 * }} ModernizationResult
 */

export const MODERNIZATION_RESULT_PREFIX = 'CAO_MODERNIZATION_RESULT:';
export const MODERNIZATION_OPERATIONS = /** @type {const} */ (['assess', 'approve-plan', 'run', 'verify']);

/**
 * Decode native operator output, not retained dashboard evidence.
 * @param {string} output
 * @param {'status'|'preview'|'execution'} kind
 * @param {string} repository
 * @returns {ModernizationResult}
 */
export function parseModernizationResult(output, kind, repository) {
  const frames = output.split('\n').filter(line => line.startsWith(MODERNIZATION_RESULT_PREFIX));
  if (frames.length !== 1) throw new Error('The native operator did not return one confirmed Modernization result. Check command output before retrying.');
  const value = JSON.parse(frames[0].slice(MODERNIZATION_RESULT_PREFIX.length));
  if (!value || value.schema !== 'cao-modernization-operator/v1' || value.kind !== kind
    || typeof value.status !== 'string' || typeof value.controlRepository !== 'string'
    || value.controlRepository.toLowerCase() !== repository.toLowerCase()) {
    throw new Error('The native Modernization result does not match the requested control repository.');
  }
  if (typeof value.controlSha !== 'string') {
    if (['unavailable', 'unconfirmed'].includes(value.status) && typeof value.reason === 'string') throw new Error(value.reason);
    throw new Error('The native Modernization control revision is unavailable.');
  }
  if (value.repositories !== undefined && (!Array.isArray(value.repositories)
    || !value.repositories.every((/** @type {unknown} */ item) => typeof item === 'string'))) {
    throw new Error('The native Modernization repository scope is invalid.');
  }
  for (const field of ['effects', 'warnings']) {
    if (value[field] !== undefined && (!Array.isArray(value[field])
      || !value[field].every((/** @type {unknown} */ item) => typeof item === 'string'))) {
      throw new Error('The native Modernization effects or warnings are invalid.');
    }
  }
  if (value.capabilities !== undefined && (!value.capabilities || typeof value.capabilities !== 'object'
    || Array.isArray(value.capabilities)
    || !Object.values(value.capabilities).every((/** @type {{authorized?: unknown}} */ capability) =>
      capability && typeof capability.authorized === 'boolean'))) {
    throw new Error('The native Modernization authorization result is invalid.');
  }
  if (value.tasks !== undefined && (!Array.isArray(value.tasks)
    || !value.tasks.every((/** @type {ModernizationTask} */ task) => task && typeof task.id === 'string'
      && typeof task.repository === 'string' && typeof task.title === 'string'))) {
    throw new Error('The native Modernization task preview is invalid.');
  }
  if (kind === 'preview' && (!MODERNIZATION_OPERATIONS.includes(value.action)
    || !['review', 'live'].includes(value.mode)
    || typeof value.canExecute !== 'boolean'
    || !/^[a-f0-9]{40}$/.test(value.controlSha)
    || value.canExecute && (typeof value.previewId !== 'string' || !Number.isFinite(Date.parse(value.expiresAt))))) {
    throw new Error('The native operator did not return a revision-bound, expiring preview.');
  }
  return value;
}
