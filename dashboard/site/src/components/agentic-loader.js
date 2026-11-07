import { h } from '../dom.js';

const riders = [
  { name: 'duck', src: new URL('../assets/loaders/duck.svg', import.meta.url).href },
  { name: 'octocat', src: new URL('../assets/loaders/octocat.svg', import.meta.url).href },
  { name: 'copilot', src: new URL('../assets/loaders/copilot.svg', import.meta.url).href }
];

/** Decorative animation for browser-local dashboard preparation. */
export function renderAgenticLoader() {
  return h('div', { className: 'first-load-agentic-loader', 'aria-hidden': 'true' },
    h('div', { className: 'first-load-agentic-rhythm' },
      h('div', { className: 'first-load-agentic-riders' },
        ...riders.map(({ name, src }) => h('img', {
          className: `first-load-agentic-rider first-load-agentic-rider-${name}`,
          src,
          alt: '',
          draggable: false
        }))
      ),
      ...Array.from({ length: 13 }, () => h('i'))
    ),
    h('span', { className: 'first-load-agentic-caption' }, 'Agentic campaigns', h('strong', null, 'Loading activity...'))
  );
}
