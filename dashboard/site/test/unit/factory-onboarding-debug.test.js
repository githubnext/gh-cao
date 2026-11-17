// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from 'vitest';

/** @param {string} search */
async function renderOnboarding(search) {
  vi.resetModules();
  window.history.replaceState(null, '', `/${search}`);
  const debug = vi.spyOn(console, 'debug').mockImplementation(() => {});
  const { state } = await import('../../src/reactive.js');
  const { renderFactoryOnboarding } = await import('../../src/components/factory-onboarding.js');
  const rows = state(/** @type {Record<string, unknown>[]} */ ([
    { 'show-onboarding': true, 'current-step': 'setup', secret: 'private-row-value' }
  ]));
  const controller = new AbortController();
  const root = renderFactoryOnboarding(
    { rows: () => rows.get(), pending: () => false, unavailable: () => false },
    { signal: controller.signal }
  );
  return { debug, rows, root, controller };
}

/** @param {{ mock: { calls: unknown[][] } }} debug */
const categories = (debug) => debug.mock.calls.map(([category]) => category);

afterEach(() => {
  window.history.replaceState(null, '', '/');
  vi.restoreAllMocks();
});

describe('factory onboarding debug logging', () => {
  it('is silent by default', async () => {
    const { debug, rows, controller } = await renderOnboarding('');
    rows.set([{ 'show-onboarding': true, 'current-step': 'repositories' }]);
    controller.abort();
    expect(categories(debug)).not.toContain('[cao:factory-onboarding]');
  });

  it('logs the synchronized step under factory-onboarding without row content', async () => {
    const { debug, rows, root, controller } = await renderOnboarding('?debug=factory-onboarding');
    expect(debug).toHaveBeenCalledWith('[cao:factory-onboarding]', { event: 'step-synchronized', step: 'setup' });
    rows.set([{ 'show-onboarding': true, 'current-step': 'unexpected-private-step' }]);
    expect(debug).toHaveBeenLastCalledWith('[cao:factory-onboarding]', { event: 'step-synchronized', step: 'none' });
    expect(root.dataset.currentStep).toBeUndefined();
    controller.abort();
    expect(JSON.stringify(debug.mock.calls)).not.toContain('private');
  });

  it('is excluded by a negated category filter', async () => {
    const { debug, controller } = await renderOnboarding('?debug=*,-factory-onboarding');
    controller.abort();
    expect(categories(debug)).not.toContain('[cao:factory-onboarding]');
  });
});
