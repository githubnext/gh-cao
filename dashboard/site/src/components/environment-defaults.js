import { h } from '../dom.js';
import { batch, effect, state } from '../reactive.js';
import { localCliQueueAvailable } from '../backend-mode.js';
import { environmentDefaultsCommand, parseEnvironmentDefaults } from '../environment-defaults.js';
import { declaredCliActionCommand, executeDeclaredCliAction } from './cli-actions.js';
import { createFactoryScope } from './factory-elements.js';
import { createModalDialog, renderLiveRegion } from './ui-primitives.js';
import { publishNotification } from '../notification-service.js';
import { createDebug, diagnosticErrorName } from '../debug.js';

const debugDefaults = createDebug('components:environment-defaults');

/** @param {import('./ui-elements.js').ElementRenderContext} context */
export function renderEnvironmentDefaults(context) {
  const lifetime = createFactoryScope();
  const actions = context.elementConfig?.actions ?? {};
  const available = localCliQueueAvailable();
  const scope = state('org');
  const target = state('');
  const visibility = state('all');
  const draft = state('');
  const original = state('');
  const loaded = state(false);
  const busy = state(false);
  const previewed = state('');
  const output = state('');
  const status = state(available ? 'Select a scope and target, then load its current defaults.'
    : 'Editing compiler defaults is unavailable in local previews. Hosted repository installation tokens cannot administer organization or enterprise variables; use gh aw env with administrative credentials outside the dashboard.');
  const snapshot = () => JSON.stringify([scope.get(), target.get(), visibility.get(), draft.get()]);
  const validTarget = () => (scope.get() === 'repo'
    ? /^[A-Za-z0-9][A-Za-z0-9-]*\/[A-Za-z0-9._-]+$/
    : /^[A-Za-z0-9][A-Za-z0-9-]*$/).test(target.get());
  const values = (update = false) => ({
    [scope.get() === 'repo' ? 'repository' : scope.get() === 'org' ? 'organization' : 'enterprise']: target.get(),
    ...(update && scope.get() !== 'repo' ? { visibility: visibility.get() } : {})
  });
  const actionId = (update = false) => {
    const id = actions[`${update ? 'update' : 'get'}-${scope.get()}`];
    if (!id) throw new Error('The dashboard has not declared this defaults action.');
    return id;
  };
  const command = (update = false, dryRun = true) => {
    const text = declaredCliActionCommand(actionId(update), values(update), update ? { 'dry-run': dryRun } : {});
    const parsed = environmentDefaultsCommand(text);
    if (!parsed || parsed.operation !== (update ? 'update' : 'get') || parsed.scope !== scope.get()
      || update && text.split(' ').includes('--dry-run') !== dryRun) {
      throw new Error('The declared action does not match the selected defaults scope.');
    }
    return text;
  };
  const fail = (/** @type {unknown} */ error) => {
    if (lifetime.signal.aborted) return;
    const message = error instanceof Error ? error.message : 'Compiler defaults command failed.';
    status.set(message);
    publishNotification({ message, tone: 'error' });
  };
  const invalidate = () => {
    batch(() => { loaded.set(false); draft.set(''); original.set(''); previewed.set(''); output.set(''); });
    status.set('Scope changed. Load its current defaults before editing.');
  };
  const execute = async (update = false, dryRun = true) => {
    if (busy.get() || !available || !validTarget() || lifetime.signal.aborted) return;
    if (update && !loaded.get()) return;
    if (update && !dryRun && previewed.get() !== snapshot()) return;
    let startedAt = 0;
    try {
      if (update) parseEnvironmentDefaults(draft.get());
      const text = command(update, dryRun);
      const reviewed = snapshot();
      busy.set(true);
      startedAt = Date.now();
      debugDefaults({ event: 'execute-started', operation: update ? 'update' : 'get', scope: scope.get(), dryRun: update ? dryRun : null });
      output.set(`$ ${text}\n`);
      previewed.set('');
      status.set(update ? dryRun ? 'Previewing changes...' : 'Applying defaults...' : 'Loading current defaults...');
      let yaml = '';
      const result = await executeDeclaredCliAction(actionId(update), values(update), {
        arguments: update ? { 'dry-run': dryRun } : {},
        ...(update ? { input: draft.get() } : {}),
        signal: lifetime.signal,
        onOutput: ({ stream, data }) => {
          if (lifetime.signal.aborted) return;
          if (!update && stream === 'stdout') yaml += data;
          else output.set((current) => current + data);
        }
      });
      if (lifetime.signal.aborted) return;
      debugDefaults({ event: 'execute-finished', operation: update ? 'update' : 'get', ok: Boolean(result.ok), durationMs: Date.now() - startedAt });
      if (!result.ok) throw new Error(result.error || 'Compiler defaults command failed. Review the command output.');
      if (!update) {
        parseEnvironmentDefaults(yaml);
        batch(() => { draft.set(yaml); original.set(yaml); loaded.set(true); });
        status.set('Current defaults loaded. Edit quoted string values or use null to remove an override.');
      } else if (dryRun) {
        previewed.set(reviewed);
        status.set('Preview complete. Review every set and deletion before applying.');
      } else {
        batch(() => { original.set(draft.get()); loaded.set(false); });
        status.set('Defaults updated. Reload to verify the saved values. Recompile workflows to incorporate changed compiler defaults.');
        publishNotification({ message: 'Compiler defaults updated.', tone: 'success' });
      }
    } catch (error) {
      debugDefaults({ event: 'execute-failed', operation: update ? 'update' : 'get', errorName: diagnosticErrorName(error) });
      if (!update) loaded.set(false);
      fail(error);
    } finally {
      if (!lifetime.signal.aborted) busy.set(false);
    }
  };
  const scopeInput = /** @type {HTMLSelectElement} */ (h('select', {
    'aria-label': 'Variable scope',
    onChange: (/** @type {Event} */ event) => {
      scope.set(/** @type {HTMLSelectElement} */ (event.currentTarget).value);
      target.set('');
      targetInput.value = '';
      invalidate();
    }
  }, h('option', { value: 'org', selected: true }, 'Organization'),
  h('option', { value: 'ent' }, 'Enterprise'), h('option', { value: 'repo' }, 'Repository')));
  const targetInput = /** @type {HTMLInputElement} */ (h('input', {
    type: 'text', 'aria-label': 'Target', autocomplete: 'off', spellcheck: false,
    onInput: (/** @type {Event} */ event) => {
      target.set(/** @type {HTMLInputElement} */ (event.currentTarget).value.trim());
      invalidate();
    }
  }));
  const visibilityInput = /** @type {HTMLSelectElement} */ (h('select', {
    'aria-label': 'New variable visibility',
    onChange: (/** @type {Event} */ event) => {
      visibility.set(/** @type {HTMLSelectElement} */ (event.currentTarget).value);
      previewed.set('');
    }
  }, h('option', { value: 'all' }, 'All repositories'), h('option', { value: 'private' }, 'Private repositories')));
  const visibilityControl = h('label', null, 'New variable visibility', visibilityInput);
  const editor = /** @type {HTMLTextAreaElement} */ (h('textarea', {
    'aria-label': 'Compiler defaults YAML', spellcheck: false, rows: 18,
    onInput: (/** @type {Event} */ event) => {
      draft.set(/** @type {HTMLTextAreaElement} */ (event.currentTarget).value);
      previewed.set('');
    }
  }));
  const load = /** @type {HTMLButtonElement} */ (h('button', {
    type: 'button', className: 'cli-action-trigger', onClick: () => void execute()
  }, 'Load defaults'));
  const discard = /** @type {HTMLButtonElement} */ (h('button', {
    type: 'button', className: 'cli-action-trigger',
    onClick: () => { draft.set(original.get()); previewed.set(''); status.set('Unsaved edits discarded.'); }
  }, 'Discard edits'));
  const preview = /** @type {HTMLButtonElement} */ (h('button', {
    type: 'button', className: 'cli-action-trigger', onClick: () => void execute(true)
  }, 'Preview changes'));
  const { dialog, open, close } = createModalDialog({
    className: 'cli-action-dialog', ariaLabel: 'Approve compiler defaults', onFallbackClose: () => apply.focus()
  });
  const approval = h('pre', { className: 'cli-action-command' });
  const confirm = /** @type {HTMLButtonElement} */ (h('button', {
    type: 'button', className: 'cli-action-confirm',
    onClick: () => { close(); void execute(true, false); }
  }, 'Apply defaults'));
  dialog.append(h('div', { className: 'cli-action-dialog-body' },
    h('h2', null, 'Apply compiler defaults'),
    h('p', null, 'This updates or deletes GitHub Actions variables at the exact scope below. Organization and enterprise changes may affect repositories outside CAO. Omitted and null fields delete overrides.'),
    approval,
    h('div', { className: 'environment-defaults-actions' },
      h('button', { type: 'button', className: 'cli-action-trigger', onClick: close }, 'Cancel'), confirm)));
  const apply = /** @type {HTMLButtonElement} */ (h('button', {
    type: 'button', className: 'cli-action-confirm',
    onClick: () => {
      try {
        parseEnvironmentDefaults(draft.get());
        approval.textContent = `$ ${command(true, false)}\n\n${draft.get()}`;
        open();
        confirm.focus();
      } catch (error) { fail(error); }
    }
  }, 'Review and apply'));
  const statusNode = renderLiveRegion('output', 'environment-defaults-status');
  const transcript = h('pre', { className: 'cli-action-output' });
  const root = h('section', { className: 'environment-defaults', 'aria-label': 'Compiler defaults editor' },
    h('p', null, 'Organization and enterprise defaults are inherited by repositories with access to those variables, including CAO repositories. Existing narrower overrides still apply. This is not a CAO-only bulk update.'),
    h('p', null, 'Use administrator credentials for the chosen scope. Values are non-secret Actions variables; never enter tokens or credentials. Changes do not grant CAO rollout authority.'),
    h('div', { className: 'environment-defaults-controls' },
      h('label', null, 'Variable scope', scopeInput), h('label', null, 'Target', targetInput), visibilityControl),
    h('p', null, 'Visibility applies only to newly created organization or enterprise variables. Existing visibility and selected-repository access lists are preserved.'),
    h('div', { className: 'environment-defaults-actions' }, load, discard),
    h('label', null, 'Compiler defaults YAML', editor),
    h('p', null, 'The complete gh-aw default_ document is loaded from your installed compiler. Quote strings. Null or omitted fields delete values at this scope; they do not disable inherited defaults.'),
    h('div', { className: 'environment-defaults-actions' }, preview, apply),
    statusNode, transcript, dialog);
  effect(() => {
    const dirty = draft.get() !== original.get();
    scopeInput.disabled = targetInput.disabled = !available || busy.get() || dirty;
    targetInput.placeholder = scope.get() === 'repo' ? 'owner/repository' : scope.get() === 'org' ? 'organization' : 'enterprise-slug';
    visibilityControl.hidden = scope.get() === 'repo';
    visibilityInput.disabled = !available || busy.get();
    load.disabled = !available || busy.get() || dirty || !validTarget();
    discard.disabled = busy.get() || !dirty;
    editor.disabled = !available || busy.get() || !loaded.get();
    if (editor.value !== draft.get()) editor.value = draft.get();
    preview.disabled = !available || busy.get() || !loaded.get();
    apply.disabled = !available || busy.get() || !loaded.get() || previewed.get() !== snapshot();
    statusNode.textContent = status.get();
    transcript.textContent = output.get();
    transcript.hidden = !output.get();
    root.setAttribute('aria-busy', String(busy.get()));
  }, { signal: lifetime.signal });
  lifetime.signal.addEventListener('abort', close, { once: true });
  lifetime.bind(root);
  return root;
}
