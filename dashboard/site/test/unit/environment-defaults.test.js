// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
const queue = vi.hoisted(() => ({ enqueue: vi.fn(), attach: vi.fn() }));
vi.mock('../../src/data-processor.js', () => ({
  cliQueueAvailable: () => Reflect.get(globalThis, '__CLI_QUEUE__') === true,
  enqueueCliCommand: queue.enqueue, attachCliCommand: queue.attach, subscribeCanonicalDashboardView: vi.fn()
}));
vi.mock('../../src/notification-service.js', () => ({ publishNotification: vi.fn() }));
import { authoritativeDashboard, authoritativeDashboardSource } from '../authoritative-dashboard.js';
import contract from '../fixtures/governance-contract.json' with { type: 'json' };
import { renderEnvironmentDefaults } from '../../src/components/environment-defaults.js';
import { setDeclaredCliActions } from '../../src/components/cli-actions.js';
import { environmentDefaultsCommand, parseEnvironmentDefaults } from '../../src/environment-defaults.js';
import { validateDashboardDocument } from '../../src/validator.js';
import { publishNotification } from '../../src/notification-service.js';

const yaml = 'default_max_turns: "20"\ndefault_model_copilot: null\n';
const context = {
  pageId: contract.page, title: 'Compiler defaults', sourceNames: [], sources: {}, contextDetails: [],
  elementConfig: { actions: contract.actions }, headingTag: /** @type {const} */ ('h2')
};
const button = (/** @type {HTMLElement} */ root, /** @type {string} */ label) => {
  const found = [...root.querySelectorAll('button')].find((item) => item.textContent === label);
  if (!found) throw new Error(`Missing button ${label}`);
  return found;
};
/** @param {HTMLElement} root @param {string} name @param {string} value */
function input(root, name, value) {
  const element = /** @type {HTMLInputElement} */ (root.querySelector(`[aria-label="${name}"]`));
  element.value = value;
  element.dispatchEvent(new Event(element.tagName === 'SELECT' ? 'change' : 'input', { bubbles: true }));
  return element;
}
function mount() {
  const root = renderEnvironmentDefaults(context);
  document.body.append(root);
  return root;
}
async function load(/** @type {HTMLElement} */ root, scope = 'org', target = 'acme') {
  input(root, 'Variable scope', scope);
  input(root, 'Target', target);
  button(root, 'Load defaults').click();
  await vi.waitFor(() => expect(root.getAttribute('aria-busy')).toBe('false'));
}

beforeEach(() => {
  document.body.replaceChildren();
  vi.stubGlobal('__CLI_QUEUE__', true);
  vi.stubGlobal('__LOCAL_CLI_QUEUE__', true);
  queue.enqueue.mockReset().mockImplementation(async (payload) => ({ id: payload.id }));
  queue.attach.mockReset().mockImplementation(async (id, output) => {
    output({ stream: 'stderr', data: 'Command diagnostic\n' });
    output({ stream: 'stdout', data: id.includes('-get-') ? yaml : 'Changes previewed.\n' });
    return { command: { status: 'completed' }, result: { ok: true } };
  });
  vi.mocked(publishNotification).mockClear();
  setDeclaredCliActions(authoritativeDashboard.dashboard['cli-actions']);
});
afterEach(() => {
  document.body.replaceChildren();
  document.head.querySelector('meta[name="dashboard-data-backend"]')?.remove();
  vi.unstubAllGlobals();
  setDeclaredCliActions([]);
});

describe('Governance contract', () => {
  it('declares source-free configuration and explicit matching commands under Maintenance', () => {
    const dashboard = authoritativeDashboard.dashboard;
    const page = dashboard.pages.find((/** @type {{ id: string }} */ page) => page.id === contract.page);
    expect(page.requires).toEqual(contract.requires);
    expect(dashboard.navigation.find((/** @type {{ label: string }} */ section) => section.label === 'Maintenance').pages)
      .toContain(contract.page);
    expect(page.views[0]).toMatchObject({
      element: contract.element, data: { sources: contract.sources }, config: { actions: contract.actions }
    });
    expect(validateDashboardDocument(authoritativeDashboardSource).ok).toBe(true);
    const invalid = structuredClone(authoritativeDashboard);
    invalid.dashboard.pages.find((/** @type {{ id: string }} */ page) => page.id === contract.page)
      .views[0].config.actions['get-org'] = 'upgrade-repository';
    expect(validateDashboardDocument(JSON.stringify(invalid)).ok).toBe(false);
    const missing = structuredClone(authoritativeDashboard);
    delete missing.dashboard.pages.find((/** @type {{ id: string }} */ page) => page.id === contract.page).views[0].config;
    expect(validateDashboardDocument(JSON.stringify(missing)).ok).toBe(false);
    const previewOnly = structuredClone(authoritativeDashboard);
    previewOnly.dashboard['cli-actions'].find((/** @type {{ id: string }} */ action) =>
      action.id === 'governance-update-org').command += ' --dry-run';
    expect(validateDashboardDocument(JSON.stringify(previewOnly)).ok).toBe(false);
  });

  it('validates complete flat default mappings without embedding a compiler key registry', () => {
    expect(parseEnvironmentDefaults(yaml)).toEqual({ default_max_turns: '20', default_model_copilot: null });
    expect(parseEnvironmentDefaults('default_future_setting: "supported by a future compiler"')).toBeTruthy();
    for (const text of ['', '{}', '[]', 'default_max_turns: 20', 'token: "secret"',
      'default_max_turns: "1"\ndefault_max_turns: "2"', 'default_model: [a, b]',
      'default_model: &value "x"\ndefault_other: *value']) {
      expect(() => parseEnvironmentDefaults(text)).toThrow();
    }
  });

  it('parses comments, CRLF, nulls and quoted string contents without general YAML', () => {
    expect(parseEnvironmentDefaults([
      '# Compiler defaults',
      '',
      'default_max_turns: "20" # keep as a string',
      "default_model: 'it''s a model: #1'  ",
      'default_empty: ""',
      "default_single_empty: ''",
      'default_unset: null\t# reset',
      'default_literal_null: "null"',
      `default_escapes: ${JSON.stringify('line\n"quoted"\\path\t☺')}`,
      '  # trailing comment',
      ''
    ].join('\r\n'))).toEqual({
      default_max_turns: '20', default_model: "it's a model: #1", default_empty: '',
      default_single_empty: '', default_unset: null, default_literal_null: 'null',
      default_escapes: 'line\n"quoted"\\path\t☺'
    });
  });

  it.each([
    '# comments only', 'default_model: plain', 'default_model: true',
    'default_model:', 'default_model: ~', 'default_model: NULL',
    'default_model: {nested: "value"}', 'default_model:\n  nested: "value"',
    ' default_model: "indented"', '- default_model: "sequence"',
    'default_model: |\n  multiline', 'default_model: >\n  multiline',
    'default_model: "unterminated', "default_model: 'unterminated",
    'default_model: "bad\\q"', 'default_model: "trailing" garbage',
    'default_model:"value"', 'default_model:null',
    'default_model: "value"#missing-separator',
    "default_model: 'literal\tcontrol'",
    'default_model: !!str "tagged"', 'default_model: *alias',
    'default_model: "first"\ndefault_model: null',
    'default_model: "value"\n---\ndefault_other: "document"',
    '__proto__: "value"', 'default_Model: "value"'
  ])('rejects unsupported or malformed defaults: %s', (text) => {
    expect(() => parseEnvironmentDefaults(text)).toThrow();
  });

  it('enforces the document size limit', () => {
    const text = `default_model: "${'x'.repeat(100_000 - 'default_model: ""'.length)}"`;
    expect(parseEnvironmentDefaults(text).default_model).toHaveLength(99_983);
    expect(() => parseEnvironmentDefaults(`${text}\n`)).toThrow('100,000 characters');
  });

  it('recognizes only scoped managed-file transport', () => {
    expect(environmentDefaultsCommand('gh aw env get cao-governance.yml --scope org --org acme'))
      .toEqual({ operation: 'get', scope: 'org' });
    for (const command of [
      'gh aw env get ../../file.yml --scope org --org acme',
      'gh aw env get cao-governance.yml --scope org',
      'gh aw env get cao-governance.yml --scope org --org acme --repo acme/other',
      'gh aw env get cao-governance.yml --scope org --org --yes',
      'gh aw env update cao-governance.yml --scope org --org acme',
      'gh aw env update cao-governance.yml --scope org --org acme --visibility selected',
      'gh aw env get cao-governance.yml --scope org --org acme --scope ent'
    ]) expect(environmentDefaultsCommand(command)).toBeNull();
  });
});

describe('compiler defaults editor', () => {
  it.each([['org', 'acme', 'organization'], ['ent', 'acme-enterprise', 'enterprise'], ['repo', 'acme/target', 'repository']])(
    'loads, previews and explicitly approves the exact %s scope', async (scope, target, field) => {
      const root = mount();
      expect(button(root, 'Review and apply').disabled).toBe(true);
      await load(root, scope, target);
      expect(queue.enqueue.mock.calls[0][0]).toMatchObject({ id: `governance-get-${scope}`, values: { [field]: target } });
      const editor = /** @type {HTMLTextAreaElement} */ (root.querySelector('textarea'));
      expect(editor.value).toBe(yaml);
      expect(root.textContent).toContain('Command diagnostic');
      input(root, 'Compiler defaults YAML', yaml.replace('"20"', '"30"'));
      button(root, 'Preview changes').click();
      await vi.waitFor(() => expect(button(root, 'Review and apply').disabled).toBe(false));
      expect(queue.enqueue.mock.calls[1][0]).toMatchObject({
        id: `governance-update-${scope}`, input: yaml.replace('"20"', '"30"'), arguments: { 'dry-run': true },
        values: { [field]: target, ...(scope === 'repo' ? {} : { visibility: 'all' }) }
      });
      button(root, 'Review and apply').click();
      expect(queue.enqueue).toHaveBeenCalledTimes(2);
      const dialog = root.querySelector('dialog');
      expect(dialog?.getAttribute('open')).toBe('');
      expect(dialog?.textContent).toContain(yaml.replace('"20"', '"30"'));
      expect(dialog?.textContent).toContain('outside CAO');
      button(root, 'Apply defaults').click();
      await vi.waitFor(() => expect(root.textContent).toContain('Defaults updated. Reload to verify'));
      expect(queue.enqueue.mock.calls[2][0].arguments).toEqual({ 'dry-run': false });
      expect(editor.disabled).toBe(true);
      expect(button(root, 'Review and apply').disabled).toBe(true);
    }
  );

  it('preserves dirty edits, invalidates previews and restores control state when discarded', async () => {
    const root = mount();
    await load(root);
    input(root, 'Compiler defaults YAML', yaml.replace('"20"', '"30"')).focus();
    expect(/** @type {HTMLInputElement} */ (root.querySelector('[aria-label="Target"]')).disabled).toBe(true);
    expect(button(root, 'Load defaults').disabled).toBe(true);
    button(root, 'Preview changes').click();
    await vi.waitFor(() => expect(button(root, 'Review and apply').disabled).toBe(false));
    input(root, 'New variable visibility', 'private');
    expect(button(root, 'Review and apply').disabled).toBe(true);
    button(root, 'Preview changes').click();
    await vi.waitFor(() => expect(button(root, 'Review and apply').disabled).toBe(false));
    expect(queue.enqueue.mock.calls[2][0].values.visibility).toBe('private');
    input(root, 'Compiler defaults YAML', yaml);
    expect(button(root, 'Review and apply').disabled).toBe(true);
    input(root, 'Compiler defaults YAML', yaml.replace('"20"', '"31"'));
    button(root, 'Discard edits').click();
    expect(/** @type {HTMLTextAreaElement} */ (root.querySelector('textarea')).value).toBe(yaml);
    input(root, 'Target', 'different');
    expect(/** @type {HTMLTextAreaElement} */ (root.querySelector('textarea')).disabled).toBe(true);
  });

  it('fails closed on invalid YAML, missing downloads and failed previews', async () => {
    const root = mount();
    await load(root);
    input(root, 'Compiler defaults YAML', 'default_max_turns: 20');
    button(root, 'Preview changes').click();
    await vi.waitFor(() => expect(root.textContent).toContain('quoted strings or null'));
    expect(queue.enqueue).toHaveBeenCalledTimes(1);
    input(root, 'Compiler defaults YAML', yaml);
    queue.attach.mockResolvedValue({ command: { status: 'failed' }, result: { ok: false, error: 'Permission denied' } });
    button(root, 'Preview changes').click();
    await vi.waitFor(() => expect(root.textContent).toContain('Permission denied'));
    expect(button(root, 'Review and apply').disabled).toBe(true);
    queue.attach.mockResolvedValue({ command: { status: 'completed' }, result: { ok: true } });
    button(root, 'Load defaults').click();
    await vi.waitFor(() => expect(root.textContent).toContain('Defaults YAML must contain'));
    expect(/** @type {HTMLTextAreaElement} */ (root.querySelector('textarea')).disabled).toBe(true);
  });

  it.each([false, true])('keeps published and hosted execution unavailable (remote=%s)', (remote) => {
    if (remote) {
      vi.stubGlobal('__LOCAL_CLI_QUEUE__', false);
      const meta = document.createElement('meta');
      meta.name = 'dashboard-data-backend';
      meta.content = 'server-http';
      document.head.append(meta);
    } else vi.stubGlobal('__CLI_QUEUE__', false);
    const root = mount();
    expect(root.textContent).toContain('Editing compiler defaults is unavailable in local previews.');
    expect([...root.querySelectorAll('input, select, textarea, button')]
      .filter((control) => control.getAttribute('aria-label') !== null)
      .every((control) => Reflect.get(control, 'disabled'))).toBe(true);
    expect(queue.enqueue).not.toHaveBeenCalled();
  });

  it('aborts attachment on detachment and ignores late results', async () => {
    let finish = () => {};
    queue.attach.mockImplementation(() => new Promise((resolve) => { finish = () => resolve({
      command: { status: 'completed' }, result: { ok: true }
    }); }));
    const root = mount();
    input(root, 'Target', 'acme');
    button(root, 'Load defaults').click();
    await vi.waitFor(() => expect(queue.attach).toHaveBeenCalledOnce());
    const signal = queue.attach.mock.calls[0][2];
    root.remove();
    await vi.waitFor(() => expect(signal.aborted).toBe(true));
    finish();
    await Promise.resolve();
    expect(publishNotification).not.toHaveBeenCalled();
  });
});
