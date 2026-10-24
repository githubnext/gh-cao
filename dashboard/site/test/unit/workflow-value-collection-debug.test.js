// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from 'vitest';

/** @param {string} search */
async function load(search) {
  const output = { debug: vi.fn() };
  vi.doMock('../../src/debug.js', async () => {
    const actual = /** @type {typeof import('../../src/debug.js')} */ (await vi.importActual('../../src/debug.js'));
    return { ...actual, createDebug: (/** @type {string} */ category) => actual.createDebug(category, { search: () => search, output }) };
  });
  vi.resetModules();
  const module = await import('../../src/data/model/workflow-value-collection.js');
  return { ...module, output };
}

/** @param {Record<string, any>} overrides */
function record(overrides = {}) {
  const workflow = 'secret-workflow';
  const repository = 'octo/repo';
  return {
    schemaVersion: 1,
    id: `workflow-value-collection:${encodeURIComponent(`${repository}:${workflow}`)}`,
    repository, workflow,
    sourcePath: `.github/workflows/${workflow}.md`,
    observedAt: '2026-01-01T00:00:00Z',
    registration: 'unconfigured',
    state: 'unconfigured',
    reasonCode: 'unconfigured',
    reason: 'No collector is registered for this workflow in the reviewed policy.',
    policyDigest: 'a'.repeat(64),
    ...overrides
  };
}

afterEach(() => {
  vi.doUnmock('../../src/debug.js');
  vi.resetModules();
});

describe('workflow-value-collection debug logging', () => {
  it('is disabled by default', async () => {
    const { mergeWorkflowValueCollection, output } = await load('');
    expect(() => mergeWorkflowValueCollection(record(), record({ policyDigest: 'b'.repeat(64) }))).toThrow(TypeError);
    expect(output.debug).not.toHaveBeenCalled();
  });

  it('logs decision conflicts under the filename category', async () => {
    const { mergeWorkflowValueCollection, output } = await load('?debug=workflow-value-collection');
    expect(() => mergeWorkflowValueCollection(record(), record({ policyDigest: 'b'.repeat(64) }))).toThrow(TypeError);
    expect(output.debug).toHaveBeenCalledWith('[cao:workflow-value-collection]',
      { operation: 'merge', status: 'conflict', kind: 'decision', fields: 'policyDigest' });
  });

  it('logs attempt conflicts and rejected records without record content', async () => {
    const { mergeWorkflowValueCollection, validateWorkflowValueCollection, output } = await load('?debug=workflow-value-collection');
    const attempt = { lastAttemptAt: '2025-12-31T00:00:00Z' };
    expect(() => mergeWorkflowValueCollection(
      record({ ...attempt, lastAttemptOutcome: 'available' }),
      record({ ...attempt, lastAttemptOutcome: 'failed' })
    )).toThrow(TypeError);
    expect(() => validateWorkflowValueCollection(record({ schemaVersion: 2 }))).toThrow(TypeError);
    const payloads = output.debug.mock.calls.map(call => call[1]);
    expect(payloads).toContainEqual({ operation: 'merge', status: 'conflict', kind: 'attempt' });
    expect(payloads).toContainEqual({ operation: 'validate', status: 'rejected' });
    expect(JSON.stringify(payloads)).not.toMatch(/secret-workflow|octo/);
  });
});
