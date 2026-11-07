import { h } from '../dom.js';

const riders = ['duck', 'octocat', 'copilot'];

/** Decorative animation for browser-local dashboard preparation. */
export function renderAgenticLoader() {
  return h('div', { className: 'first-load-agentic-loader', 'aria-hidden': 'true' },
    h('div', { className: 'first-load-agentic-rhythm' },
      h('div', { className: 'first-load-agentic-riders' },
        ...riders.map((name) => h('img', {
          className: `first-load-agentic-rider first-load-agentic-rider-${name}`,
          src: new URL(`./src/assets/loaders/${name}.svg`, document.baseURI).href,
          alt: '',
          draggable: false
        }))
      ),
      ...Array.from({ length: 13 }, () => h('i'))
    ),
    h('span', { className: 'first-load-agentic-caption' }, 'Agentic campaigns', h('strong', null, 'Loading activity...'))
  );
}
