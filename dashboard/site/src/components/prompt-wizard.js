import { h } from '../dom.js';
import { batch, effect, render, state } from '../reactive.js';
import { normalizeAction } from '../action-model.js';
import { octicon } from '../octicons.js';
import { parsePromptWizard, initialWizardValues, updateWizardValues, wizardFieldVisible, wizardFieldError, buildWizardPrompt } from '../prompt-wizard.js';
import { createFactoryScope } from './factory-elements.js';
import { renderPromptPreviewAction } from './data-view.js';
import { renderLiveRegion } from './ui-primitives.js';
import { publishNotification } from '../notification-service.js';
import { createDebug, diagnosticErrorName } from '../debug.js';

const debugWizard = createDebug('components:prompt-wizard');
let nextWizardId = 0;

/** @param {import('./ui-elements.js').ElementRenderContext} context */
export function renderPromptWizard(context) {
  const wizard = parsePromptWizard(context.elementConfig?.wizard);
  const scope = createFactoryScope();
  const values = state(initialWizardValues(wizard));
  const active = state(0);
  const error = state('');
  const fieldErrors = state(/** @type {Record<string, string>} */ ({}));
  const reviewedPrompt = state('');
  const fieldsById = new Map(wizard.steps.flatMap((step) => step.fields.map((field) => [field.id, field])));
  const prefix = `prompt-wizard-${++nextWizardId}`;
  /** @type {Map<string, { root: HTMLElement, control: HTMLInputElement|HTMLTextAreaElement|HTMLSelectElement|HTMLButtonElement, description: HTMLElement, error: HTMLElement }>} */
  const controls = new Map();
  /** @param {string} id @param {string|string[]} value */
  const update = (id, value) => {
    if (scope.signal.aborted) return;
    const field = fieldsById.get(id);
    if (!field) throw new Error('Wizard field is not declared.');
    batch(() => {
      const current = values.set((current) => updateWizardValues(field, current, value));
      const remaining = Object.fromEntries(Object.keys(fieldErrors.get()).flatMap((id) => {
        const field = fieldsById.get(id);
        const message = field && wizardFieldVisible(field, current) ? wizardFieldError(field, current[id]) : null;
        return message ? [[id, message]] : [];
      }));
      fieldErrors.set(remaining);
      reviewedPrompt.set('');
      if (!Object.keys(remaining).length) error.set('');
    });
  };
  const panels = wizard.steps.map((step, index) => /** @type {HTMLFieldSetElement} */ (h('fieldset', {
    className: 'prompt-wizard-step', 'aria-describedby': `${prefix}-${step.id}-description`
  }, h('legend', null, step.title),
  h('p', { id: `${prefix}-${step.id}-description` }, step.description ?? ''),
  ...step.fields.map((field) => {
    const id = `${prefix}-${field.id}`;
    const description = h('p', { id: `${id}-help`, className: 'prompt-wizard-help' });
    const fieldError = h('p', { id: `${id}-error`, className: 'prompt-wizard-field-error', hidden: true });
    const label = `${field.label}${field.required ? ' (required)' : ''}`;
    /** @type {HTMLInputElement|HTMLTextAreaElement|HTMLSelectElement|HTMLButtonElement} */
    let control;
    let root;
    if (field.control === 'cards') {
      const cards = (field.options ?? []).map((option) => {
        const titleId = `${id}-${option.value}-title`;
        const summaryId = `${id}-${option.value}-summary`;
        return /** @type {HTMLButtonElement} */ (h('button', {
          type: 'button', value: option.value,
          className: 'issue-list-card entity-card-list-card prompt-wizard-card',
          'aria-labelledby': titleId, 'aria-describedby': summaryId,
          'aria-pressed': String(values.get()[field.id] === option.value),
          onClick: () => {
            update(field.id, option.value);
            if (field['advance-on-select']) advance();
          }
        },
            h('span', { className: 'issue-list-card-icon', 'aria-hidden': 'true' }, octicon(option.icon ?? 'workflow')),
            h('span', { className: 'issue-list-card-content' },
              h('strong', { id: titleId, className: 'issue-list-card-title' }, option.label),
              h('span', { id: summaryId, className: 'issue-list-card-subtitle' }, option.summary ?? option.description ?? ''))));
      });
      control = cards[0];
      root = h('fieldset', { className: 'prompt-wizard-field prompt-wizard-cards' },
        h('legend', null, label), fieldError,
        h('div', { className: 'entity-card-list entity-card-list-marketplace' }, ...cards));
    } else if (field.control === 'checkboxes') {
      const inputs = (field.options ?? []).map((option) => {
        const input = /** @type {HTMLInputElement} */ (h('input', {
          type: 'checkbox', value: option.value,
          checked: Array.isArray(values.get()[field.id]) && values.get()[field.id].includes(option.value),
          onChange: () => update(field.id, inputs.filter(({ input }) => input.checked).map(({ input }) => input.value))
        }));
        return { input, root: h('label', { className: 'prompt-wizard-choice' }, input,
          h('span', null, h('strong', null, option.label), option.description ? h('small', null, option.description) : null)) };
      });
      control = inputs[0].input;
      root = h('fieldset', { className: 'prompt-wizard-field prompt-wizard-options' },
        h('legend', null, label), fieldError, ...inputs.map(({ root }) => root), description);
    } else {
      control = /** @type {HTMLInputElement|HTMLTextAreaElement|HTMLSelectElement} */ (h(
        field.control === 'select' ? 'select' : field.control === 'textarea' ? 'textarea' : 'input',
        {
          id, ...(field.control === 'text' ? { type: 'text' } : {}),
          ...(field.control === 'textarea' ? { rows: 4 } : {}),
          ...(field.control !== 'select' ? { maxlength: field['max-length'] ?? 8000, placeholder: field.placeholder ?? '' } : {}),
          'aria-describedby': `${id}-help`, required: field.required === true,
          [field.control === 'select' ? 'onChange' : 'onInput']: (/** @type {Event} */ event) => update(field.id, /** @type {HTMLInputElement} */ (event.currentTarget).value)
        },
        ...(field.control === 'select' ? [
          h('option', { value: '' }, 'Choose an option'),
          ...(field.options ?? []).map((option) => h('option', { value: option.value }, option.label))
        ] : [])
      ));
      control.value = String(values.get()[field.id]);
      root = h('div', { className: 'prompt-wizard-field' }, h('label', { for: id }, label), control, fieldError, description);
    }
    controls.set(field.id, { root, control, description, error: fieldError });
    root.dataset.wizardStep = String(index);
    return root;
  }))));
  const status = renderLiveRegion('output', 'prompt-wizard-status');
  const stepButtons = wizard.steps.map((step, index) =>
    /** @type {HTMLButtonElement} */ (h('button', {
      type: 'button', 'aria-label': `Return to ${step.title}`,
      onClick: () => goBack(index)
    }, `${index + 1}. ${step.title}`)));
  const progress = h('ol', { className: 'prompt-wizard-progress', 'aria-label': 'Wizard progress' },
    ...stepButtons.map((button) => h('li', null, button)));
  /** @param {string} id */
  const focusInvalidField = (id) => {
    const item = controls.get(id);
    if (!item || scope.signal.aborted) return;
    active.set(Number(item.root.dataset.wizardStep));
    item.control.focus();
    item.control.scrollIntoView?.({ block: 'center' });
  };
  /** @param {boolean} [allSteps] */
  const validateSteps = (allSteps = false) => {
    const errors = { ...fieldErrors.get() };
    /** @type {string | undefined} */
    let firstInvalid;
    wizard.steps.forEach((step, index) => {
      if (!allSteps && index !== active.get()) return;
      for (const field of step.fields) {
        delete errors[field.id];
        if (!wizardFieldVisible(field, values.get())) continue;
        const message = wizardFieldError(field, values.get()[field.id]);
        if (message) {
          errors[field.id] = message;
          firstInvalid ??= field.id;
        }
      }
    });
    batch(() => {
      fieldErrors.set(errors);
      error.set(firstInvalid ? allSteps ? 'Cannot generate prompt.' : 'Cannot continue.' : '');
    });
    if (firstInvalid) {
      focusInvalidField(firstInvalid);
      return false;
    }
    return true;
  };
  const focusPanel = () => {
    wizard.steps[active.get()]?.fields.some((field) => {
      if (!wizardFieldVisible(field, values.get())) return false;
      const item = controls.get(field.id);
      const selected = item?.root.querySelector('input:checked, button[aria-pressed="true"]');
      if (selected instanceof HTMLElement) selected.focus();
      else item?.control.focus();
      return true;
    });
  };
  /** @param {number} index */
  const goBack = (index) => {
    if (scope.signal.aborted || index < 0 || index >= active.get()) return;
    active.set(index);
    error.set('');
    focusPanel();
  };
  const back = /** @type {HTMLButtonElement} */ (h('button', {
    type: 'button', className: 'cli-action-trigger',
    onClick: () => goBack(active.get() - 1)
  }, 'Back'));
  const advance = () => {
    if (scope.signal.aborted || active.get() >= wizard.steps.length - 1 || !validateSteps()) return;
    active.set(active.get() + 1);
    error.set('');
    focusPanel();
  };
  const preparePrompt = () => {
    if (scope.signal.aborted || active.get() !== wizard.steps.length - 1 || !validateSteps(true)) return false;
    try {
      reviewedPrompt.set(buildWizardPrompt(wizard, values.get()));
      error.set('');
      return true;
    } catch (cause) {
      const message = cause instanceof Error ? cause.message : 'Could not prepare generation prompt.';
      error.set(message);
      publishNotification({ message, tone: 'error' });
      debugWizard({ event: 'prompt-rejected', errorName: diagnosticErrorName(cause) });
      return false;
    }
  };
  const next = /** @type {HTMLButtonElement} */ (h('button', {
    type: 'button', className: 'cli-action-confirm', onClick: advance
  }, 'Next'));
  const preview = renderPromptPreviewAction(normalizeAction({
    level: 'propose', label: wizard.prompt.label, presentation: 'copy-prompt'
  }, { id: `${context.viewId ?? 'wizard'}-generate`, type: 'prompt' }), reviewedPrompt.get,
  undefined, undefined, undefined, preparePrompt);
  const navigation = h('div', { className: 'prompt-wizard-actions' }, back, next, preview);
  const errorSummary = h('div', { className: 'prompt-wizard-errors', role: 'alert', hidden: true });
  render(errorSummary, () => [
    h('strong', null, error.get() || 'Check your answers before continuing.'),
    h('ul', null, ...wizard.steps.flatMap((step, index) => step.fields.flatMap((field) => {
      const message = fieldErrors.get()[field.id];
      return message ? [h('li', null, h('p', null, message), h('button', {
        type: 'button', onClick: () => focusInvalidField(field.id)
      }, `Fix ${field.label} - Step ${index + 1}: ${step.title}`))] : [];
    })))
  ], { signal: scope.signal });
  const root = h('section', { className: 'prompt-wizard', 'aria-label': context.title },
    h('p', null, wizard.intro),
    progress,
    ...panels, status, errorSummary,
    navigation);
  effect(() => {
    const current = active.get();
    const fields = wizard.steps[current]?.fields;
    navigation.hidden = current === 0 && fields?.length === 1 && fields[0]['advance-on-select'] === true;
    panels.forEach((panel, index) => { panel.hidden = index !== current; panel.disabled = index !== current; });
    stepButtons.forEach((item, index) => {
      const hasErrors = wizard.steps[index].fields.some((field) => fieldErrors.get()[field.id]);
      item.textContent = `${index + 1}. ${wizard.steps[index].title}${hasErrors ? ' (needs attention)' : ''}`;
      item.setAttribute('aria-label', `Return to ${wizard.steps[index].title}${hasErrors ? ', needs attention' : ''}`);
      item.disabled = index >= current;
      if (index === current) item.setAttribute('aria-current', 'step');
      else item.removeAttribute('aria-current');
    });
    back.disabled = current === 0;
    next.hidden = current === wizard.steps.length - 1;
    preview.hidden = current !== wizard.steps.length - 1;
    errorSummary.hidden = !error.get() && !Object.keys(fieldErrors.get()).length;
    status.textContent = Object.values(fieldErrors.get())[0] || error.get() || `Step ${current + 1} of ${wizard.steps.length}: ${wizard.steps[current].title}`;
    for (const step of wizard.steps) {
      for (const field of step.fields) {
        const item = controls.get(field.id);
        if (!item) continue;
        item.root.hidden = !wizardFieldVisible(field, values.get());
        const message = fieldErrors.get()[field.id];
        item.error.textContent = message ?? '';
        item.error.hidden = !message;
        for (const control of item.root.querySelectorAll('input, textarea, select, button')) {
          if (message) control.setAttribute('aria-invalid', 'true');
          else control.removeAttribute('aria-invalid');
          const descriptions = (control.getAttribute('aria-describedby') ?? '').split(' ')
            .filter((id) => id && id !== item.error.id);
          if (message) descriptions.push(item.error.id);
          if (descriptions.length) control.setAttribute('aria-describedby', descriptions.join(' '));
          else control.removeAttribute('aria-describedby');
        }
        if (field.control === 'select') {
          if (item.control.value !== values.get()[field.id]) item.control.value = String(values.get()[field.id]);
          item.description.textContent = field.options?.find((option) => option.value === values.get()[field.id])?.description ?? '';
        } else if (field.control === 'checkboxes') {
          const selected = values.get()[field.id];
          for (const input of item.root.querySelectorAll('input')) input.checked = Array.isArray(selected) && selected.includes(input.value);
        } else if (field.control === 'cards') {
          for (const choice of item.root.querySelectorAll('button')) {
            choice.setAttribute('aria-pressed', String(choice.value === values.get()[field.id]));
          }
        } else if (item.control.value !== values.get()[field.id]) {
          item.control.value = String(values.get()[field.id]);
        }
      }
    }
  }, { signal: scope.signal });
  scope.signal.addEventListener('abort', () => {
    for (const dialog of root.querySelectorAll('dialog')) dialog.removeAttribute('open');
  }, { once: true });
  scope.bind(root);
  return root;
}
