// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('../../src/data-processor.js', () => ({
  cliQueueAvailable: () => false,
  enqueueCliCommand: vi.fn(),
  attachCliCommand: vi.fn(),
  subscribeCanonicalDashboardView: vi.fn()
}));

const configuration = {
  intro: 'SECRET-INTRO',
  prompt: { label: 'Prompt', instructions: 'SECRET-INSTRUCTIONS' },
  steps: [
    { id: 'one', title: 'One', fields: [{ id: 'purpose', label: 'Purpose', control: 'text', required: true }] },
    { id: 'two', title: 'Two', fields: [{ id: 'scope', label: 'Scope', control: 'text', required: false }] }
  ]
};

beforeEach(() => {
  document.body.replaceChildren();
});

afterEach(() => {
  vi.doUnmock('../../src/debug.js');
  vi.resetModules();
  document.body.replaceChildren();
});

/** @param {string} search */
async function mount(search) {
  const output = { debug: vi.fn(), category: '' };
  vi.doMock('../../src/debug.js', async () => {
    const actual = /** @type {typeof import('../../src/debug.js')} */ (await vi.importActual('../../src/debug.js'));
    return {
      ...actual,
      createDebug: (/** @type {string} */ category) => {
        output.category = category;
        return actual.createDebug(category, { search: () => search, output });
      }
    };
  });
  vi.resetModules();
  const { parsePromptWizard } = await import('../../src/prompt-wizard.js');
  const { renderPromptWizard } = await import('../../src/components/prompt-wizard.js');
  const root = renderPromptWizard({
    pageId: 'page', viewId: 'view', title: 'Wizard', sourceNames: [], sources: {},
    contextDetails: [], elementConfig: { wizard: parsePromptWizard(configuration) }, headingTag: 'h2'
  });
  document.body.append(root);
  return { root, output };
}

const PREFIX = '[cao:components:prompt-wizard]';

/** @param {{ debug: import('vitest').Mock }} output */
function wizardCalls(output) {
  return output.debug.mock.calls.filter(([prefix]) => prefix === PREFIX);
}

/** @param {HTMLElement} root @param {string} label */
function button(root, label) {
  const found = [...root.querySelectorAll('button')].find((item) => item.textContent?.trim() === label);
  if (!found) throw new Error(`Missing button: ${label}`);
  return found;
}

/** @param {HTMLElement} root @param {string} value */
function fillPurpose(root, value) {
  const control = /** @type {HTMLInputElement} */ (root.querySelector('input[type="text"], input:not([type]), textarea'));
  control.value = value;
  control.dispatchEvent(new Event('input', { bubbles: true }));
}

describe('prompt wizard component debug logging', () => {
  it('is silent by default and when another category is selected', async () => {
    for (const search of ['', '?debug=auth', '?debug=*,-components:prompt-wizard']) {
      const { root, output } = await mount(search);
      button(root, 'Next').click();
      fillPurpose(root, 'SECRET-ANSWER');
      button(root, 'Next').click();
      expect(wizardCalls(output)).toEqual([]);
      document.body.replaceChildren();
    }
  });

  it('uses the directory-qualified category because prompt-wizard.js is a duplicated basename', async () => {
    const { output } = await mount('?debug=components:prompt-wizard');
    expect(output.category).toBe('components:prompt-wizard');
  });

  it('logs scalar validation and step metadata without user-entered text', async () => {
    const { root, output } = await mount('?debug=components:prompt-wizard');
    button(root, 'Next').click();
    expect(wizardCalls(output)).toEqual([[PREFIX, { event: 'validation-failed', step: 0, scope: 'step' }]]);

    fillPurpose(root, 'SECRET-ANSWER');
    button(root, 'Next').click();
    expect(wizardCalls(output).at(-1)).toEqual([PREFIX, { event: 'step-advanced', from: 0, to: 1, total: 2 }]);

    expect(JSON.stringify(wizardCalls(output))).not.toMatch(/SECRET/);
    for (const [, payload] of wizardCalls(output)) {
      for (const value of Object.values(payload)) expect(['string', 'number']).toContain(typeof value);
    }
  });
});
