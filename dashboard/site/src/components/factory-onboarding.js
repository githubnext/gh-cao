import { h } from '../dom.js';
import { effect } from '../reactive.js';
import { renderDeclaredCliAction } from './cli-actions.js';
import { createCopyControl, renderIconSpan } from './ui-primitives.js';

const ONBOARDING_STEPS = new Set(['setup', 'repositories', 'artifacts']);
const ONBOARDING_SUMMARIES = {
  setup: 'Set up CAO in this checkout before choosing what it should operate.',
  repositories: 'Choose the repositories this control plane should include.',
  artifacts: 'Add an Agentic Workflow or campaign.'
};

/**
 * @typedef {{
 *   rows: () => Record<string, unknown>[],
 *   pending: () => boolean,
 *   unavailable: () => boolean
 * }} OnboardingSource
 */

/**
 * Reads the declarative onboarding decision without deriving inventory state
 * from source rows on the main thread.
 * @param {OnboardingSource | undefined} source
 */
export function factoryOnboardingState(source) {
  const pending = source?.pending() === true;
  const unavailable = source?.unavailable() === true;
  const row = source?.rows()[0] ?? {};
  const candidate = row['current-step'];
  return {
    pending,
    show: !pending && !unavailable && row['show-onboarding'] === true,
    currentStep: typeof candidate === 'string' && ONBOARDING_STEPS.has(candidate) ? candidate : undefined
  };
}

/** @param {string | undefined} step */
export function factoryOnboardingSummary(step) {
  return step && Object.hasOwn(ONBOARDING_SUMMARIES, step)
    ? ONBOARDING_SUMMARIES[/** @type {keyof typeof ONBOARDING_SUMMARIES} */ (step)]
    : 'Complete the next CAO setup step.';
}

/**
 * @param {{
 *   id: 'setup'|'repositories'|'artifacts',
 *   title: string,
 *   description: string,
 *   action: Node
 * }} step
 */
function renderOnboardingStep(step) {
  return h(
    'li',
    { className: 'factory-onboarding-step', 'data-onboarding-step': step.id },
    h(
      'div',
      { className: 'factory-onboarding-step-copy' },
      h(
        'div',
        { className: 'factory-onboarding-step-heading' },
        h('h3', {}, step.title)
      ),
      h('p', {}, step.description),
      step.action
    )
  );
}

/**
 * @param {OnboardingSource | undefined} source
 * @param {{ signal: AbortSignal }} scope
 */
export function renderFactoryOnboarding(source, scope) {
  const setup = createCopyControl({
    getContent: () => 'gh cao setup',
    label: 'Copy gh cao setup',
    buttonClassName: 'factory-onboarding-action',
    statusClassName: 'factory-onboarding-copy-status',
    successText: 'Command copied.',
    trackState: true
  });
  const setupAction = renderDeclaredCliAction('setup-cao')
    ?? h('div', { className: 'factory-onboarding-action-row' }, setup.button, setup.status);
  const steps = {
    setup: renderOnboardingStep({
      id: 'setup',
      title: 'Set up CAO',
      description: 'Run gh cao setup in this checkout to create the control policy and configure access.',
      action: h('div', { className: 'factory-onboarding-action-row' }, setupAction)
    }),
    repositories: renderOnboardingStep({
      id: 'repositories',
      title: 'Choose repositories',
      description: 'Review or add the repositories this control plane should include.',
      action: h(
        'a',
        { className: 'factory-onboarding-action', href: '#page-repositories' },
        'Choose repositories',
        renderIconSpan('factory-onboarding-action-icon', 'arrow-right', { ariaHidden: true })
      )
    }),
    artifacts: renderOnboardingStep({
      id: 'artifacts',
      title: 'Add workflows or campaigns',
      description: 'Browse the Marketplace and add an Agentic Workflow or campaign.',
      action: h(
        'a',
        { className: 'factory-onboarding-action', href: '#page-marketplace' },
        'Browse Marketplace',
        renderIconSpan('factory-onboarding-action-icon', 'arrow-right', { ariaHidden: true })
      )
    })
  };
  const root = h(
    'div',
    { className: 'factory-onboarding', 'aria-label': 'Welcome to CAO' },
    h(
      'ol',
      { className: 'factory-onboarding-steps' },
      steps.setup,
      steps.repositories,
      steps.artifacts
    )
  );
  effect(() => {
    const currentStep = factoryOnboardingState(source).currentStep;
    for (const [id, step] of Object.entries(steps)) {
      step.hidden = id !== currentStep;
    }
    if (currentStep) root.dataset.currentStep = currentStep;
    else delete root.dataset.currentStep;
  }, { signal: scope.signal });
  return root;
}
