import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { IDBFactory } from 'fake-indexeddb';
const queue = vi.hoisted(() => ({ enqueue: vi.fn(), attach: vi.fn() }));
vi.mock('../../src/data-processor.js', () => ({
  cliQueueAvailable: () => Reflect.get(globalThis, '__CLI_QUEUE__') === true,
  enqueueCliCommand: queue.enqueue, attachCliCommand: queue.attach, subscribeCanonicalDashboardView: vi.fn()
}));
import { authoritativeDashboard, authoritativeDashboardSource } from '../authoritative-dashboard.js';
import contract from '../fixtures/wizard-contract.json' with { type: 'json' };
import { parsePromptWizard, initialWizardValues, updateWizardValues, wizardDesign, wizardReview, buildWizardPrompt } from '../../src/prompt-wizard.js';
import { renderPromptWizard } from '../../src/components/prompt-wizard.js';
import { setDeclaredCliActions } from '../../src/components/cli-actions.js';
import { validateDashboardDocument } from '../../src/validator.js';
import { compileDashboardViewPayloadQueries } from '../../src/data/queries/view-payload-compiler.js';
import { processDataRequest } from '../../src/data-worker.js';

const page = authoritativeDashboard.dashboard.pages.find((/** @type {{ id: string }} */ page) => page.id === contract.page);
const view = page.views[0];
const wizard = parsePromptWizard(view.config.wizard);
/** @returns {import('../../src/prompt-wizard.js').WizardValues} */
function completeValues() {
  return { ...initialWizardValues(wizard), pattern: 'custom', purpose: 'Triage issues.', success: 'Never close an issue.', scope: 'Only the triggering issue.' };
}
/** @param {import('../../src/prompt-wizard.js').PromptWizard} [configuration] */
function mount(configuration = wizard) {
  const root = renderPromptWizard({
    pageId: page.id, viewId: view.id, title: view.title, sourceNames: [], sources: {},
    contextDetails: [], elementConfig: { wizard: configuration }, headingTag: 'h2'
  });
  document.body.append(root);
  return root;
}
/** @param {HTMLElement} root @param {string} label */
function button(root, label) {
  const button = [...root.querySelectorAll('button')].find((button) => button.textContent?.trim() === label
    || button.getAttribute('aria-label') === label || button.querySelector('.issue-list-card-title')?.textContent === label);
  if (!button) throw new Error(`Missing button: ${label}`);
  return button;
}
/** @param {HTMLElement} root @param {string} label @param {string} value */
function input(root, label, value) {
  const field = [...root.querySelectorAll('label')].find((node) => [label, `${label} (required)`, `${label} (optional)`].includes(node.textContent ?? ''));
  const control = /** @type {HTMLInputElement|HTMLSelectElement|HTMLTextAreaElement} */ (root.querySelector(`#${field?.htmlFor}`));
  if (!control) throw new Error(`Missing input: ${label}`);
  control.value = value;
  control.dispatchEvent(new Event(control.tagName === 'SELECT' ? 'change' : 'input', { bubbles: true }));
  return control;
}
/** @param {HTMLElement} root @param {string} [artifact] @param {string} [pattern] */
function fillGoal(root, artifact = 'workflow', pattern = 'custom') {
  const choice = /** @type {HTMLButtonElement} */ (root.querySelector(`.prompt-wizard-card[value="${pattern}"]`));
  choice.click();
  input(root, 'What do you want to generate?', artifact);
  input(root, 'What should the agent do?', 'Triage issues.');
  input(root, 'Intent and acceptance criteria', 'Never close issues.');
  button(root, 'Next').click();
}
/** @param {HTMLElement} root */
function fillBehavior(root) {
  fillGoal(root);
  input(root, 'Per-run scope and limits', 'Only the triggering issue.');
}

beforeEach(() => {
  document.body.replaceChildren();
  vi.stubGlobal('__CLI_QUEUE__', true);
  vi.stubGlobal('indexedDB', new IDBFactory());
  queue.enqueue.mockReset().mockResolvedValue({ id: 'generation-command' });
  queue.attach.mockReset().mockResolvedValue({ command: { status: 'completed' }, result: { ok: true } });
  setDeclaredCliActions(authoritativeDashboard.dashboard['cli-actions']);
});
afterEach(() => {
  document.body.replaceChildren();
  document.querySelector('meta[name="dashboard-copilot-session"]')?.remove();
  vi.unstubAllGlobals();
  setDeclaredCliActions([]);
});

describe('declarative wizard contract', () => {
  it('registers a navigable, source-free JSON page with pinned mined patterns and the standard prompt button', async () => {
    expect(validateDashboardDocument(authoritativeDashboardSource).errors).toEqual([]);
    expect(page).toMatchObject({ id: contract.page, kind: 'custom', experimental: true, views: [{
      id: contract.view, element: contract.element, data: { sources: contract.sources }, prompt: 'none'
    }] });
    expect(authoritativeDashboard.dashboard.navigation[0].pages).not.toContain(contract.page);
    expect(authoritativeDashboard.dashboard.navigation.find(
      (/** @type {{ experimental?: boolean }} */ section) => section.experimental === true
    )?.pages).toContain(contract.page);
    expect(wizard.steps.map((step) => step.id)).toEqual(contract.steps);
    expect(wizard.steps.flatMap((step) => step.fields.map((field) => field.id))).toEqual(contract.fields);
    expect(wizard.steps[0].fields[0]).toMatchObject({ id: 'pattern', control: 'cards', required: true, 'advance-on-select': true });
    expect(wizard.steps[0].fields[0]).not.toHaveProperty('default');
    expect(wizard.steps.flatMap((step) => step.fields.map((field) => field.id))).not.toContain('name');
    for (const id of contract['optional-fields']) {
      expect(wizard.steps.flatMap((step) => step.fields).find((field) => field.id === id)?.required).toBe(false);
    }
    expect(wizard.provenance).toMatchObject({ repository: contract.upstream, revision: contract.revision });
    expect(wizard.provenance?.license).toContain('Copyright (c) 2026 GitHub Next');
    expect(wizard.steps[0].fields.find((field) => field.id === 'pattern')?.options).toHaveLength(contract['pattern-count'] + 1);
    expect(wizard.prompt.label).toBe(contract['prompt-label']);
    expect(wizard.prompt).not.toHaveProperty('action');
    const copyOnly = {
      'language-version': '0.1.0',
      dashboard: { id: 'wizard-only', title: 'Wizard only', pages: [page] }
    };
    expect(validateDashboardDocument(JSON.stringify(copyOnly)).ok).toBe(true);
    const compiled = compileDashboardViewPayloadQueries(page, page.id);
    expect(compiled.queries).toEqual([]);
    const result = await processDataRequest({
      operation: 'query-canonical-dashboard', sourceNames: [], pageId: page.id,
      context: { pages: [page], queries: compiled.queries }
    });
    expect(result).toEqual({});
  });

  it.each([
    (/** @type {any} */ doc) => { doc.dashboard.pages.find((/** @type {any} */ page) => page.id === contract.page).views[0].config.wizard.prompt.action = 'upgrade-repository'; },
    (/** @type {any} */ doc) => { doc.dashboard.pages.find((/** @type {any} */ page) => page.id === contract.page).views[0].data.sources = ['runs']; },
    (/** @type {any} */ doc) => { delete doc.dashboard.pages.find((/** @type {any} */ page) => page.id === contract.page).views[0].config; }
  ])('rejects command bindings or data queries on the prompt wizard', (change) => {
    const document = structuredClone(authoritativeDashboard);
    change(document);
    expect(validateDashboardDocument(JSON.stringify(document)).ok).toBe(false);
  });

  it('preserves explicit choices and bounds Campaign generation without elevating mined recommendations', () => {
    const values = { ...completeValues(), artifact: 'campaign', targets: 'acme/one\nacme/two', pattern: 'issue-triage', outputs: ['add-comment'] };
    const prompt = buildWizardPrompt(wizard, values);
    expect(prompt).toMatch(/^Create a Pull Request for the following task:\n/);
    expect(prompt).toContain('aw.yml');
    expect(prompt).toContain('default to review mode');
    expect(prompt).toContain('Honor the selected safe outputs and capabilities');
    expect(prompt).toContain('Campaign target repositories');
    expect(prompt).not.toContain('recommended_triggers');
    expect(prompt).not.toContain('success_rate');
    expect(prompt).not.toContain(contract.revision);
    expect(prompt).toContain('gh aw compile');
    expect(prompt).toContain('draft pull request');
    expect(prompt).toContain('Infer a concise title and kebab-case campaign package name');
    expect(wizardDesign(wizard, values)).not.toHaveProperty('name');
    expect(prompt).not.toContain('MIT License');
    expect(values.outputs).toEqual(['add-comment']);
    expect(wizardDesign(wizard, { ...values, artifact: 'workflow' })).not.toHaveProperty('targets');
  });

  it('declares precise editable output defaults for every scenario, not broad permission categories', () => {
    const patterns = wizard.steps[0].fields[0].options ?? [];
    for (const option of patterns) {
      const pattern = /** @type {{ pattern?: { recommended_tools: string[] } }} */ (option.context)?.pattern;
      expect(option.defaults?.outputs).toEqual(pattern?.recommended_tools ?? []);
    }
    const field = wizard.steps[0].fields[0];
    const selected = updateWizardValues(field, completeValues(), 'issue-triage');
    expect(selected.outputs).toEqual(['add-comment', 'add-labels']);
    expect(updateWizardValues(field, { ...selected, outputs: [] }, 'issue-triage').outputs).toEqual([]);
    expect(updateWizardValues(field, selected, 'code-improvement').outputs).toEqual(['create-pull-request']);
    expect(updateWizardValues(field, selected, 'custom').outputs).toEqual([]);
  });

  it('describes the review in human terms without showing the mined JSON context', () => {
    const entries = wizardReview(wizard, {
      ...completeValues(), pattern: 'issue-triage', outputs: ['add-comment', 'add-labels'],
      purpose: '', success: '', scope: '', constraints: ''
    });
    expect(entries.find((entry) => entry.label === 'Workflow type')).toMatchObject({
      value: 'Issue Triage', description: 'Classify and label new issues'
    });
    expect(entries.find((entry) => entry.label === 'Agent engine')?.value).toBe('GitHub Copilot');
    expect(entries.find((entry) => entry.label === 'Allowed safe outputs')?.value).toBe('add comment, add labels');
    expect(entries.find((entry) => entry.label === 'Per-run scope and limits (optional)')?.value).toContain('Infer conservative');
    expect(JSON.stringify(entries)).not.toContain('recommended_tools');
    expect(JSON.stringify(entries)).not.toContain('success_rate');
    expect(wizardReview(wizard, { ...completeValues(), outputs: [] })
      .find((entry) => entry.label === 'Allowed safe outputs')?.value).toContain('Read-only');
  });

  it('explains each optional capability while preserving its upstream tool or safe-output context', () => {
    const extras = wizard.steps.flatMap((step) => step.fields).find((field) => field.id === 'extras');
    expect(extras?.default).toEqual([]);
    expect(extras?.options).toHaveLength(5);
    for (const option of extras?.options ?? []) {
      expect(option.label).not.toBe(option.value);
      expect(option.description?.length).toBeGreaterThan(90);
      expect(option.description).not.toBe(option.value);
      expect(option.context).toHaveProperty('requirement');
    }
    const prompt = buildWizardPrompt(wizard, { ...completeValues(), extras: ['memory', 'charts'] });
    expect(prompt).toContain('Remember across runs');
    expect(prompt).toContain('cache-memory tool');
    expect(prompt).toContain('upload-assets safe output');
  });

  it('rejects missing, forged, duplicate and oversized answers instead of generating a partial prompt', () => {
    const changes = /** @type {import('../../src/prompt-wizard.js').WizardValues[]} */ ([
      { pattern: '' }, { pattern: 'not-a-pattern' }, { outputs: ['not-an-output'] },
      { outputs: ['add-comment', 'add-comment'] }, { artifact: 'campaign', targets: '' },
      { artifact: 'campaign', targets: 'acme/*' },
      { purpose: 'x'.repeat(6001) }, { scope: 'x'.repeat(3001) }, { constraints: 'x'.repeat(4001) }, { triggers: 'issues' }
    ]);
    for (const changed of changes) expect(() => buildWizardPrompt(wizard, { ...completeValues(), ...changed })).toThrow();
  });

  it.each(['workflow', 'campaign'])('uses the selected scenario for %s when all optional refinements are blank', (artifact) => {
    const values = { ...completeValues(), artifact, pattern: 'issue-triage', purpose: '', success: '', scope: '', constraints: '', targets: 'acme/one' };
    const prompt = buildWizardPrompt(wizard, values);
    expect(prompt).toContain('Classify and label new issues');
    expect(prompt).toContain('infer them from the selected scenario');
    expect(prompt).toContain('do not require the user to fill them in');
    const design = wizardDesign(wizard, values);
    expect(design.purpose.value).toBe('');
    expect(design.success.value).toBe('');
    expect(design.scope.value).toBe('');
    expect(design.constraints.value).toBe('');
    expect(prompt).toContain('fixed safety and rollout boundaries below always apply');
    expect(prompt).toContain('Use conservative limits.');
  });

  it('fails closed on malformed and unknown declarative configuration', () => {
    for (const change of [
      (/** @type {any} */ value) => { value.extra = 'unknown'; },
      (/** @type {any} */ value) => { value.steps[1].fields[0].id = 'pattern'; },
      (/** @type {any} */ value) => { value.steps[0].fields[0].control = 'script'; },
      (/** @type {any} */ value) => { value.steps[0].fields[0]['max-length'] = -1; },
      (/** @type {any} */ value) => { value.steps[0].fields[0].when = { field: 'missing', equals: 'x' }; },
      (/** @type {any} */ value) => { value.steps[0].fields[0].default = 'missing'; },
      (/** @type {any} */ value) => { value.steps[0].fields[0].options[0].callback = 'script'; },
      (/** @type {any} */ value) => { value.steps[0].fields[0].options[0].icon = 'not-an-icon'; },
      (/** @type {any} */ value) => { value.steps[0].fields[0].options[0].instructions = {}; },
      (/** @type {any} */ value) => { value.steps[0].fields[0].options[0].defaults = { missing: [] }; },
      (/** @type {any} */ value) => { value.steps[0].fields[0].options[0].defaults = { pattern: 'custom' }; },
      (/** @type {any} */ value) => { value.steps[0].fields[0].options[0].defaults = { outputs: ['missing'] }; },
      (/** @type {any} */ value) => { value.steps[0].fields[0].options[0].defaults = { outputs: 'add-comment' }; },
      (/** @type {any} */ value) => { value.steps[0].fields[0]['empty-message'] = false; },
      (/** @type {any} */ value) => { value.steps[1].fields[0]['advance-on-select'] = true; },
      (/** @type {any} */ value) => { value.provenance.revision = 'main'; }
    ]) {
      const invalid = structuredClone(wizard);
      change(invalid);
      expect(() => parsePromptWizard(invalid)).toThrow();
    }
  });
});

describe('wizard interaction and lifecycle', () => {
  it('labels every invalid field and keeps remaining errors visible after an answer is corrected', () => {
    const root = mount();
    fillGoal(root, 'campaign');
    const trigger = /** @type {HTMLInputElement} */ (root.querySelector('input[value="workflow_dispatch"]'));
    trigger.click();
    button(root, 'Generate').click();
    const targets = /** @type {HTMLTextAreaElement} */ (root.querySelector('[id$="-targets"]'));
    const summary = root.querySelector('[role="alert"]');
    expect(summary?.textContent).toContain('Cannot generate prompt');
    expect(summary?.textContent).toContain('Campaign target repositories is required.');
    expect(summary?.textContent).toContain('When should it run? is required.');
    expect(targets.getAttribute('aria-invalid')).toBe('true');
    expect(trigger.getAttribute('aria-invalid')).toBe('true');
    for (const control of [targets, trigger]) {
      const errorId = control.getAttribute('aria-describedby')?.split(' ').find((id) => id.endsWith('-error'));
      expect(document.getElementById(errorId ?? '')?.textContent).toContain('is required.');
    }
    expect(document.activeElement).toBe(targets);
    input(root, 'Campaign target repositories', 'acme/one');
    expect(targets.hasAttribute('aria-invalid')).toBe(false);
    expect(summary?.textContent).not.toContain('Campaign target repositories is required.');
    expect(summary?.textContent).toContain('When should it run? is required.');
    button(root, 'Fix When should it run? - Step 3: Scope and behavior').click();
    expect(document.activeElement).toBe(root.querySelector('input[value="issues"]'));
    trigger.click();
    expect(summary?.hasAttribute('hidden')).toBe(true);
    button(root, 'Generate').click();
    expect(root.querySelector('dialog')?.hasAttribute('open')).toBe(true);
  });

  it('validates all panes on Generate and reveals an earlier answer invalidated by a later choice', () => {
    const configuration = structuredClone(wizard);
    const engine = configuration.steps.flatMap((step) => step.fields).find((field) => field.id === 'engine');
    if (!engine?.options?.[1]) throw new Error('Expected an alternative engine.');
    engine.options[1].defaults = { artifact: '' };
    const root = mount(configuration);
    fillBehavior(root);
    input(root, 'Agent engine', engine.options[1].value);
    button(root, 'Generate').click();
    expect(root.querySelector('dialog')?.hasAttribute('open')).toBe(false);
    expect(root.querySelector('[id$="-artifact"]')).toBe(document.activeElement);
    expect(root.querySelector('[id$="-artifact"]')?.closest('fieldset')?.hasAttribute('hidden')).toBe(false);
    expect(root.querySelector('[role="alert"]')?.textContent).toContain('What do you want to generate? is required.');
    expect(root.querySelector('[role="alert"]')?.textContent).toContain('Step 2: Describe your goal');
    input(root, 'What do you want to generate?', 'workflow');
    button(root, 'Next').click();
    button(root, 'Generate').click();
    expect(root.querySelector('dialog')?.hasAttribute('open')).toBe(true);
  });

  it('shows actionable format errors and removes failures for fields that become inapplicable', () => {
    const root = mount();
    fillGoal(root, 'campaign');
    input(root, 'Campaign target repositories', 'acme/*');
    button(root, 'Generate').click();
    expect(root.querySelector('[role="alert"]')?.textContent).toContain('must list exact owner/repository names');
    button(root, 'Back').click();
    input(root, 'What do you want to generate?', 'workflow');
    expect(root.querySelector('[role="alert"]')?.hasAttribute('hidden')).toBe(true);
    expect(root.querySelector('[id$="-targets"]')?.hasAttribute('aria-invalid')).toBe(false);
    button(root, 'Next').click();
    button(root, 'Generate').click();
    expect(root.querySelector('dialog')?.hasAttribute('open')).toBe(true);
    expect(root.querySelector('.table-intent-preview')?.textContent).not.toContain('acme/*');
  });

  it('starts an app session with the reviewed prompt only after an explicit canvas handoff', async () => {
    const meta = document.createElement('meta');
    meta.name = 'dashboard-copilot-session';
    meta.content = '/__copilot_session';
    document.head.append(meta);
    const fetch = vi.fn().mockResolvedValue({ ok: true, json: async () => ({ ok: true }) });
    vi.stubGlobal('fetch', fetch);
    const root = mount();
    fillBehavior(root);
    button(root, 'Generate').click();
    expect(fetch).not.toHaveBeenCalled();
    expect(root.textContent).not.toContain('Copy prompt');
    const start = button(root, 'Start session');
    start.click();
    start.click();
    await vi.waitFor(() => expect(root.textContent).toContain('Session started in the Copilot app.'));
    expect(fetch).toHaveBeenCalledTimes(1);
    expect(fetch.mock.calls[0][0].pathname).toBe('/__copilot_session');
    expect(JSON.parse(fetch.mock.calls[0][1].body).prompt).toMatch(/^Create a Pull Request for the following task:\n/);
    expect(JSON.parse(fetch.mock.calls[0][1].body).prompt).toContain('Triage issues.');
    expect(start.disabled).toBe(true);
    expect(queue.enqueue).not.toHaveBeenCalled();
  });

  it('reports canvas session failures and cancels detached handoffs', async () => {
    const meta = document.createElement('meta');
    meta.name = 'dashboard-copilot-session';
    meta.content = '/__copilot_session';
    document.head.append(meta);
    const fetch = vi.fn().mockResolvedValue({ ok: false, json: async () => ({ error: 'Session unavailable.' }) });
    vi.stubGlobal('fetch', fetch);
    const root = mount();
    fillBehavior(root);
    button(root, 'Generate').click();
    const start = button(root, 'Start session');
    start.click();
    await vi.waitFor(() => expect(root.textContent).toContain('Session unavailable.'));
    expect(start.disabled).toBe(false);
    root.remove();
    await new Promise((resolve) => setTimeout(resolve, 0));
    start.click();
    expect(fetch).toHaveBeenCalledTimes(1);
    expect(fetch.mock.calls[0][1].signal.aborted).toBe(true);
  });

  it('allows direct backward step navigation without losing answers or bypassing validation', async () => {
    const root = mount();
    const tabs = [...root.querySelectorAll('button')].filter((button) => button.closest('.prompt-wizard-progress'));
    expect(tabs).toHaveLength(wizard.steps.length);
    expect(tabs.every((tab) => tab.hasAttribute('disabled'))).toBe(true);
    fillGoal(root, 'campaign', 'issue-triage');
    const comment = /** @type {HTMLInputElement} */ (root.querySelector('input[value="add-comment"]'));
    comment.click();
    button(root, 'Generate').click();
    expect(root.querySelector('.prompt-wizard-status')?.textContent).toContain('Campaign target repositories is required.');
    tabs.at(-1)?.dispatchEvent(new MouseEvent('click'));
    expect(root.querySelector('dialog')?.hasAttribute('open')).toBe(false);
    input(root, 'Campaign target repositories', 'acme/one');
    button(root, 'Generate').click();
    expect(root.querySelector('dialog')?.hasAttribute('open')).toBe(true);
    expect(tabs.at(-1)?.getAttribute('aria-current')).toBe('step');
    expect(root.querySelector('.prompt-wizard-status')?.textContent).toBe('Step 3 of 3: Scope and behavior');
    button(root, 'Close prompt preview').click();
    tabs[1].click();
    expect(root.querySelector('[id$="-artifact"]')).toBe(document.activeElement);
    expect(root.querySelector('[id$="-purpose"]')).toHaveProperty('value', 'Triage issues.');
    expect(root.querySelector('.prompt-wizard-status')?.textContent).not.toContain('is required');
    tabs[0].click();
    expect(document.activeElement).toBe(button(root, 'Issue Triage'));
    button(root, 'Issue Triage').click();
    button(root, 'Next').click();
    expect(comment.checked).toBe(false);
    expect(root.querySelector('[id$="-targets"]')).toHaveProperty('value', 'acme/one');
    button(root, 'Generate').click();
    button(root, 'Close prompt preview').click();
    expect(tabs[2].disabled).toBe(true);
    expect(document.activeElement).toBe(button(root, 'Generate'));
    expect(queue.enqueue).not.toHaveBeenCalled();
    root.remove();
    await new Promise((resolve) => setTimeout(resolve, 0));
    tabs[0].click();
    expect(tabs[2].getAttribute('aria-current')).toBe('step');
  });

  it('generates directly from the last pane and refreshes the prompt after edits', () => {
    const root = mount();
    button(root, 'Generate').click();
    expect(root.querySelector('dialog')?.hasAttribute('open')).toBe(false);
    fillBehavior(root);
    button(root, 'Generate').click();
    expect(root.querySelector('.table-intent-preview')?.textContent).toContain('Only the triggering issue.');
    button(root, 'Close prompt preview').click();
    input(root, 'Per-run scope and limits', 'Only issues with the help-wanted label.');
    button(root, 'Generate').click();
    expect(root.querySelector('.table-intent-preview')?.textContent).toContain('Only issues with the help-wanted label.');
    expect(root.querySelector('.table-intent-preview')?.textContent).not.toContain('Only the triggering issue.');
    expect(root.querySelector('.prompt-wizard-review')).toBeNull();
    expect(root.querySelector('.prompt-wizard-status')?.textContent).toBe('Step 3 of 3: Scope and behavior');
  });

  it.each(['workflow', 'campaign'])('uses the standard prompt button for %s even when command execution is available', async (artifact) => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    vi.stubGlobal('navigator', { clipboard: { writeText } });
    const root = mount();
    expect(root.querySelector('.prompt-wizard-actions')?.hasAttribute('hidden')).toBe(true);
    fillGoal(root, artifact);
    input(root, 'Per-run scope and limits', 'Only the triggering issue.');
    if (artifact === 'campaign') input(root, 'Campaign target repositories', 'acme/one');
    expect(root.querySelector('.prompt-wizard-review')).toBeNull();
    expect(root.querySelector('.prompt-wizard-summary')).toBeNull();
    expect(root.querySelector('.prompt-wizard-actions > .cli-action-confirm')?.hasAttribute('hidden')).toBe(true);
    expect(queue.enqueue).not.toHaveBeenCalled();
    const promptButton = button(root, contract['prompt-label']);
    expect(promptButton.closest('.prompt-wizard-actions')).not.toBeNull();
    expect(promptButton.getAttribute('data-intent-presentation')).toBe(contract.presentation);
    expect(promptButton.getAttribute('data-action-level')).toBe('propose');
    promptButton.click();
    expect(root.querySelector('dialog')?.hasAttribute('open')).toBe(true);
    expect(root.querySelector('.table-intent-preview')?.textContent).toContain('Triage issues.');
    expect(queue.enqueue).not.toHaveBeenCalled();
    expect(writeText).not.toHaveBeenCalled();
    expect(root.querySelector('.cli-action-output')).toBeNull();
    expect(root.textContent).not.toContain('Start agent task');
    button(root, 'Copy prompt').click();
    await vi.waitFor(() => expect(writeText).toHaveBeenCalledTimes(1));
    expect(writeText.mock.calls[0][0]).toContain('Triage issues.');
    expect(writeText.mock.calls[0][0]).toContain(artifact === 'workflow' ? 'Agentic Workflow' : 'Campaign');
    expect(queue.enqueue).not.toHaveBeenCalled();
    expect(queue.attach).not.toHaveBeenCalled();
    button(root, 'Close prompt preview').click();
    button(root, 'Back').click();
    expect(root.querySelector('[id$="-scope"]')).toHaveProperty('value', 'Only the triggering issue.');
    expect(root.querySelector('[id$="-purpose"]')).toHaveProperty('value', 'Triage issues.');
  });

  it('writes a focused natural-language AW prompt without campaign or historical metadata', () => {
    const prompt = buildWizardPrompt(wizard, {
      ...completeValues(), pattern: 'issue-triage', purpose: '', success: '', scope: '', constraints: '',
      outputs: ['add-comment', 'add-labels'], extras: ['memory']
    });
    expect(prompt).toMatch(/^Create a Pull Request for the following task:\n/);
    expect(prompt).toContain('Issue Triage\nClassify and label new issues');
    expect(prompt).toContain('`add-comment` safe output');
    expect(prompt).toContain('cache-memory tool');
    for (const forbidden of [
      'Proposal only.', 'Historical pattern provenance', 'top_repos', 'anti_patterns',
      'recommended_tools', '"config":', 'User design (JSON)', contract.revision, 'gh-cao'
    ]) expect(prompt).not.toContain(forbidden);
    expect(prompt).not.toMatch(/campaign/i);
    expect(prompt).not.toContain('### What should the agent do?');
    expect(prompt).not.toContain('### Intent and acceptance criteria');
    const campaign = buildWizardPrompt(wizard, {
      ...completeValues(), pattern: 'issue-triage', artifact: 'campaign', targets: 'acme/one'
    });
    expect(campaign).toContain('aw.yml campaign manifest');
    expect(campaign).toContain('default to review mode');
    expect(campaign).toContain('acme/one');
    expect(campaign).toContain('gh-cao');
  });

  it('requires Campaign targets and preselects scenario outputs but not optional capabilities', () => {
    const root = mount();
    fillGoal(root, 'campaign', 'issue-triage');
    input(root, 'Per-run scope and limits', 'One dispatched target per worker.');
    button(root, 'Generate').click();
    expect(root.textContent).toContain('Campaign target repositories is required.');
    expect(root.querySelectorAll('.prompt-wizard-options input:checked')).toHaveLength(3);
    input(root, 'Campaign target repositories', 'acme/one');
    button(root, contract['prompt-label']).click();
    expect(root.querySelector('.table-intent-preview')?.textContent).toContain('acme/one');
  });

  it('retains output edits on the same type and replaces them when a different type is chosen', () => {
    const root = mount();
    fillGoal(root, 'workflow', 'issue-triage');
    const comment = /** @type {HTMLInputElement} */ (root.querySelector('.prompt-wizard-options input[value="add-comment"]'));
    const labels = /** @type {HTMLInputElement} */ (root.querySelector('.prompt-wizard-options input[value="add-labels"]'));
    expect(comment.checked).toBe(true);
    expect(labels.checked).toBe(true);
    comment.click();
    labels.click();
    button(root, 'Back').click();
    button(root, 'Back').click();
    button(root, 'Issue Triage').click();
    button(root, 'Next').click();
    expect(comment.checked).toBe(false);
    expect(labels.checked).toBe(false);
    button(root, 'Back').click();
    button(root, 'Back').click();
    button(root, 'Code Improvement').click();
    button(root, 'Next').click();
    expect(comment.checked).toBe(false);
    expect(/** @type {HTMLInputElement} */ (root.querySelector('.prompt-wizard-options input[value="create-pull-request"]')).checked).toBe(true);
  });

  it('uses declarative advance-on-select and preserves an explicitly reviewed card choice on Back', () => {
    const root = mount();
    button(root, 'Issue Triage').click();
    expect(root.querySelector('[id$="-artifact"]')).toBe(document.activeElement);
    expect(root.querySelector('.prompt-wizard-cards')?.closest('fieldset.prompt-wizard-step')?.hasAttribute('hidden')).toBe(true);
    button(root, 'Back').click();
    const chosen = button(root, 'Issue Triage');
    expect(chosen.getAttribute('aria-pressed')).toBe('true');
    expect(document.activeElement).toBe(chosen);
    chosen.click();
    expect(root.querySelector('[id$="-artifact"]')).toBe(document.activeElement);
    root.remove();
    const explicit = structuredClone(wizard);
    explicit.steps[0].fields[0]['advance-on-select'] = false;
    const explicitRoot = mount(explicit);
    button(explicitRoot, 'Issue Triage').click();
    expect(explicitRoot.querySelector('.prompt-wizard-actions')?.hasAttribute('hidden')).toBe(false);
    expect(explicitRoot.querySelector('.prompt-wizard-cards')?.closest('fieldset.prompt-wizard-step')?.hasAttribute('hidden')).toBe(false);
    button(explicitRoot, 'Next').click();
    expect(explicitRoot.querySelector('[id$="-artifact"]')).toBe(document.activeElement);
  });

  it('keeps navigation at the bottom and omits the visible provenance/license footer', () => {
    const root = mount();
    const navigation = root.querySelector('.prompt-wizard-actions');
    expect(root.lastElementChild).toBe(navigation);
    expect(root.querySelector('.prompt-wizard-provenance')).toBeNull();
    expect(root.textContent).not.toContain('Patterns from gh-aw-wizard');
    expect(root.textContent).not.toContain('Upstream dataset license');
    expect(wizard.provenance?.license).toContain('MIT License');
    button(root, 'Issue Triage').click();
    button(root, 'Next').click();
    input(root, 'Per-run scope and limits', '');
    input(root, 'Safety constraints and non-goals', '');
    button(root, 'Generate').click();
    expect(root.querySelector('dialog')?.hasAttribute('open')).toBe(true);
  });

  it('provides copy-only handoff when execution is unavailable and treats input as text', async () => {
    vi.stubGlobal('__CLI_QUEUE__', false);
    const writeText = vi.fn().mockResolvedValue(undefined);
    vi.stubGlobal('navigator', { clipboard: { writeText } });
    const root = mount();
    fillGoal(root);
    input(root, 'Per-run scope and limits', '<img src=x onerror=alert(1)> $(touch /tmp/not-run)');
    button(root, contract['prompt-label']).click();
    expect(root.querySelector('img')).toBeNull();
    button(root, 'Copy prompt').click();
    await vi.waitFor(() => expect(writeText).toHaveBeenCalledTimes(1));
    expect(writeText.mock.calls[0][0]).toContain('$(touch /tmp/not-run)');
    expect(queue.enqueue).not.toHaveBeenCalled();
  });

  it('reports copy failure and stops detached prompt interaction', async () => {
    const writeText = vi.fn().mockRejectedValue(new Error('Clipboard unavailable.'));
    vi.stubGlobal('navigator', { clipboard: { writeText } });
    const root = mount();
    fillBehavior(root);
    button(root, contract['prompt-label']).click();
    button(root, 'Copy prompt').click();
    await vi.waitFor(() => expect(root.textContent).toContain('Could not copy prompt.'));
    root.remove();
    await new Promise((resolve) => setTimeout(resolve, 0));
    button(root, 'Back').click();
    button(root, contract['prompt-label']).click();
    expect(root.querySelector('dialog')?.hasAttribute('open')).toBe(false);
    expect(queue.enqueue).not.toHaveBeenCalled();
  });

  it('ignores in-flight clipboard completion after detachment without queuing a command', async () => {
    /** @type {((value: unknown) => void) | undefined} */
    let finish;
    const writeText = vi.fn().mockImplementation(() => new Promise((resolve) => { finish = resolve; }));
    vi.stubGlobal('navigator', { clipboard: { writeText } });
    const root = mount();
    fillBehavior(root);
    button(root, contract['prompt-label']).click();
    button(root, 'Copy prompt').click();
    await vi.waitFor(() => expect(writeText).toHaveBeenCalledTimes(1));
    root.remove();
    await new Promise((resolve) => setTimeout(resolve, 0));
    finish?.(undefined);
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(root.textContent).not.toContain('Prompt copied.');
    expect(queue.enqueue).not.toHaveBeenCalled();
  });
});
