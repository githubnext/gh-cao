/** @type {Array<[string, number]>} */
const barColors = [['yellow', 0], ['pink', 33.333], ['blue', 66.667]];
/** @param {string} color @param {number} intensity */
const barGradient = (color, intensity) =>
  `linear-gradient(180deg, color-mix(in srgb, var(--first-load-rider-${color}) ${Math.round(intensity * 100)}%, var(--lime)), color-mix(in srgb, var(--first-load-rider-${color}) ${Math.round(intensity * 100)}%, var(--success)))`;

const barColorStyles = Array.from({ length: 13 }, (_, index) => {
  const position = index / 12;
  const frames = barColors.flatMap(([color, start]) => {
    const peak = start + 3.03 + 24.242 * position;
    return [
      `${start}% { background: ${barGradient(color, 0)}; }`,
      `${(start + 3.03).toFixed(3)}% { background: ${barGradient(color, 1 - position)}; }`,
      ...(peak > start + 3.03 && peak < start + 29.505
        ? [`${peak.toFixed(3)}% { background: ${barGradient(color, 1)}; }`]
        : []),
      `${(start + 29.505).toFixed(3)}% { background: ${barGradient(color, Math.max(position, .2))}; }`,
      `${(start + 32.535).toFixed(3)}% { background: ${barGradient(color, 0)}; }`
    ];
  });
  return `
.first-load-agentic-rhythm i:nth-of-type(${index + 1}) { animation-delay: ${(-6 + index * .4).toFixed(1)}s; }
.first-load-agentic-rhythm i:nth-of-type(${index + 1})::after { animation-name: first-load-bar-color-${index + 1}; }
@keyframes first-load-bar-color-${index + 1} { ${frames.join(' ')} }`;
}).join('');

export const firstLoadStyles = `
.factory-intro-importing { grid-template-columns: minmax(0, 1fr); background-image: none; }
.factory-intro-copy > p { max-width: 640px; color: var(--muted); line-height: 1.6; }
.first-load-overlay { position: fixed; inset: 0; box-sizing: border-box; width: 100%; max-width: none; height: 100%; height: 100dvh; max-height: none; margin: 0; padding: 24px 32px; border: 0; overflow-y: auto; background: var(--canvas-subtle); color: var(--fg); font-family: var(--font-sans); }
.first-load-overlay[open] { display: grid; grid-template-columns: minmax(0, 1fr); grid-template-rows: auto 1fr; gap: 24px; align-items: center; }
.first-load-overlay [hidden] { display: none; }
.first-load-overlay::backdrop { background: var(--canvas); }
.first-load-background { position: fixed; inset: 0; overflow: hidden; pointer-events: none; }
.first-load-background::before { animation: first-load-grid-breathe 24s ease-in-out infinite; }
@keyframes first-load-grid-breathe { 0%, 100% { opacity: .65; } 50% { opacity: .85; } }
.first-load-header { position: relative; z-index: 1; }
.first-load-brand { display: flex; align-items: center; gap: 10px; color: var(--fg); font-size: 1rem; font-weight: 600; line-height: 1.5; }
.first-load-brand .sidebar-brand-mark { width: 28px; height: 28px; flex: 0 0 auto; }
.first-load-card { position: relative; z-index: 1; box-sizing: border-box; min-width: 0; width: min(100%, 600px); margin: auto; padding: 36px; border: 1px solid var(--border-muted); border-radius: 12px; background: var(--canvas); box-shadow: 0 4px 24px color-mix(in srgb, var(--canvas-inset) 15%, transparent); }
.first-load-compact-copy { display: none; }
.first-load-eyebrow { margin: 0 0 12px; color: var(--accent); font-size: .8125rem; font-weight: 600; }
.first-load-card h2 { margin: 0; font-size: clamp(1.75rem, 5vw, 2.5rem); line-height: 1.15; font-weight: 600; }
.first-load-description { margin: 16px 0 24px; color: var(--muted); line-height: 1.6; text-wrap: pretty; }
.first-load-agentic-loader { --first-load-rider-yellow: #d8dd57; --first-load-rider-pink: #d13ef4; --first-load-rider-blue: #58a6ff; display: grid; justify-items: center; gap: 8px; margin: -4px 0 18px; }
.first-load-agentic-rhythm { position: relative; display: flex; width: min(100%, 420px); height: 66px; align-items: flex-end; justify-content: space-around; gap: 5px; }
.first-load-agentic-rhythm i { position: relative; width: 16px; height: 42px; flex: 0 1 16px; overflow: hidden; border-radius: 3px; background: linear-gradient(180deg, var(--lime), var(--success)); box-shadow: 0 0 5px color-mix(in srgb, var(--success) 28%, transparent); transform: scaleY(.28); transform-origin: center bottom; animation: first-load-bar-wave 6.6s ease-in-out infinite; }
.first-load-agentic-rhythm i::after { position: absolute; inset: 0; content: ''; animation-duration: 19.8s; animation-timing-function: linear; animation-iteration-count: infinite; }
.first-load-agentic-riders { position: absolute; z-index: 1; inset: 0 0 12px; pointer-events: none; }
.first-load-agentic-rider { position: absolute; top: 0; left: 0; width: 36px; height: 30px; opacity: 0; animation: first-load-rider-cruise 19.8s linear infinite; }
.first-load-agentic-rider-octocat { animation-delay: -13.2s; }
.first-load-agentic-rider-copilot { animation-delay: -6.6s; }
.first-load-agentic-caption { display: grid; gap: 1px; color: var(--muted); font-size: .625rem; font-weight: 600; letter-spacing: .12em; text-align: center; text-transform: uppercase; }
.first-load-agentic-caption strong { color: var(--fg); font-size: .8125rem; letter-spacing: normal; text-transform: none; }
@keyframes first-load-bar-wave { 0%, 100% { opacity: .5; transform: scaleY(.28); } 50% { opacity: .9; transform: scaleY(.82); } }
@keyframes first-load-rider-cruise {
  0% { opacity: 0; left: 0; }
  3.03% { opacity: 1; left: 9%; }
  29.505% { opacity: 1; left: 82%; }
  32.535% { opacity: 0; left: calc(100% - 36px); }
  100% { opacity: 0; left: calc(100% - 36px); }
}
${barColorStyles}
.first-load-message { display: flex; align-items: center; min-height: 3em; margin: 20px 0; padding: 12px 16px; border-radius: 6px; background: var(--canvas-subtle); color: var(--muted); font-size: .875rem; line-height: 1.5; }
.first-load-status { margin: 8px 0 0; color: var(--muted); font-size: .75rem; line-height: 1.5; }
.first-load-ingestion-details { margin: 0 0 12px; font-size: .75rem; }
.first-load-ingestion-details summary { width: fit-content; padding: 6px 0; color: var(--accent); cursor: pointer; }
.first-load-ingestion-details summary:focus-visible { outline: 2px solid var(--focus); outline-offset: 3px; }
.first-load-ingestion-details .dashboard-notification-details { max-height: min(180px, 25vh); }
.first-load-ingestion-details .dashboard-notification-actions { justify-content: flex-end; margin-top: 10px; }
.first-load-about { margin-top: 24px; padding-top: 12px; border-top: 1px solid var(--border-muted); }
.first-load-about summary { display: list-item; width: fit-content; padding: 8px 0; color: var(--muted); font-size: .75rem; line-height: 1.5; cursor: pointer; }
.first-load-about summary:hover { color: var(--fg); }
.first-load-about summary:focus-visible { outline: 2px solid var(--focus); outline-offset: 3px; border-radius: 3px; }
.first-load-steps { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 20px; margin: 16px 0; padding: 0; list-style: none; }
.first-load-steps strong, .first-load-steps span { display: block; font-size: .8125rem; line-height: 1.5; }
.first-load-steps span { margin-top: 6px; color: var(--muted); }
.first-load-note { margin: 12px 0; color: var(--muted); font-size: .75rem; line-height: 1.6; }
.first-load-copy-status { display: block; margin-top: 8px; color: var(--muted); font-size: .75rem; }
.first-load-server-option a { text-decoration: underline; }
.first-load-details-link { border: 0; padding: 0; background: none; color: var(--accent); font: inherit; text-decoration: underline; cursor: pointer; }
.first-load-details-link:hover { color: var(--fg); }
.first-load-details-link:focus-visible { outline: 2px solid var(--focus); outline-offset: 3px; border-radius: 3px; }
.first-load-browse, .first-load-details { min-height: 44px; padding: 8px 16px; border: 1px solid var(--border); border-radius: 6px; background: var(--canvas-subtle); color: var(--fg); font: inherit; font-size: .875rem; font-weight: 600; cursor: pointer; }
.first-load-browse { display: inline-flex; align-items: center; justify-content: center; gap: 8px; margin-top: 8px; border-color: var(--accent); background: var(--accent); color: var(--canvas); }
.first-load-browse .octicon { width: 16px; height: 16px; }
.first-load-browse:hover { filter: brightness(.95); }
.first-load-details:hover { background: var(--accent-muted); }
.first-load-browse:focus-visible, .first-load-details:focus-visible { outline: 2px solid var(--focus); outline-offset: 3px; }
@media (max-width: 700px) {
  .first-load-overlay { padding: 12px; }
  .first-load-overlay[open] { gap: 16px; }
  .first-load-brand { font-size: .875rem; }
  .first-load-brand .sidebar-brand-mark { width: 24px; height: 24px; }
  .first-load-card { padding: 20px; }
  .first-load-wide-copy, .first-load-steps { display: none; }
  .first-load-compact-copy { display: inline; }
  .first-load-eyebrow { margin-bottom: 8px; font-size: .75rem; }
  .first-load-card h2 { font-size: 1.5rem; }
  .first-load-description { margin: 12px 0 16px; font-size: .875rem; line-height: 1.5; }
  .first-load-agentic-loader { margin: -6px 0 10px; }
  .first-load-agentic-rhythm { height: 42px; }
  .first-load-agentic-rhythm i { height: 30px; }
  .first-load-agentic-rider { width: 28px; height: 25px; }
  .first-load-agentic-caption { display: none; }
  .first-load-message { margin: 16px 0; padding: 10px 12px; font-size: .8125rem; }
  .first-load-message-ingesting { display: none; }
  .first-load-status { font-size: .75rem; }
  .first-load-about { margin-top: 16px; }
  .first-load-browse { width: 100%; }
  .first-load-note { margin-bottom: 0; }
}
@media (max-width: 700px) and (max-height: 650px) {
  .first-load-card { padding-block: 10px; }
  .first-load-description { margin-block: 8px; }
  .first-load-agentic-loader { margin-block: -4px 6px; }
  .first-load-agentic-rhythm { height: 34px; }
  .first-load-message { min-height: 2em; margin-block: 8px; }
  .first-load-about { margin-top: 12px; }
}
@media (prefers-reduced-motion: reduce) {
  .first-load-background::before { animation: none; opacity: .75; }
  .first-load-agentic-rhythm i, .first-load-agentic-rhythm i::after, .first-load-agentic-rider { animation: none; }
  .first-load-agentic-riders { display: flex; align-items: flex-start; justify-content: space-around; }
  .first-load-agentic-rider { position: static; opacity: 1; }
}
`;
