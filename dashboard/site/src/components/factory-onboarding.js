import { h } from '../dom.js';
import { createCopyControl, renderIconSpan } from './ui-primitives.js';

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
  return {
    pending,
    show: !pending && !unavailable && row['show-onboarding'] === true
  };
}

/**
 * @param {{
 *   number: string,
 *   title: string,
 *   description: string,
 *   action: Node
 * }} step
 */
function renderOnboardingStep(step) {
  return h(
    'li',
    { className: 'factory-onboarding-step' },
    h(
      'span',
      { className: 'factory-onboarding-step-icon', 'aria-hidden': 'true' },
      step.number
    ),
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

export function renderFactoryOnboarding() {
  const setup = createCopyControl({
    getContent: () => 'gh cao setup',
    label: 'Copy gh cao setup',
    buttonClassName: 'factory-onboarding-action',
    statusClassName: 'factory-onboarding-copy-status',
    successText: 'Command copied.',
    trackState: true
  });
  return h(
    'div',
    { className: 'factory-onboarding', 'aria-label': 'Get started with Central Agentic Ops' },
    h(
      'ol',
      { className: 'factory-onboarding-steps' },
      renderOnboardingStep({
        number: '0',
        title: 'Set up CAO',
        description: 'Run gh cao setup in this checkout to create the control policy and configure access.',
        action: h('div', { className: 'factory-onboarding-action-row' }, setup.button, setup.status)
      }),
      renderOnboardingStep({
        number: '1',
        title: 'Choose repositories',
        description: 'Review or add the repositories this control plane should include.',
        action: h(
          'a',
          { className: 'factory-onboarding-action', href: '#page-repositories' },
          'Choose repositories',
          renderIconSpan('factory-onboarding-action-icon', 'arrow-right', { ariaHidden: true })
        )
      }),
      renderOnboardingStep({
        number: '2',
        title: 'Add workflows or campaigns',
        description: 'After setup, browse the Marketplace and add an Agentic Workflow or campaign.',
        action: h(
          'a',
          { className: 'factory-onboarding-action', href: '#page-marketplace' },
          'Browse Marketplace',
          renderIconSpan('factory-onboarding-action-icon', 'arrow-right', { ariaHidden: true })
        )
      })
    )
  );
}
