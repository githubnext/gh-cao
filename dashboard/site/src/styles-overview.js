import { firstLoadStyles } from './styles-first-load.js';
import { overviewGridStyles } from './styles-overview-grid.js';

export const overviewStyles = `main.dashboard-prototype:has(.dashboard-overview-page:not([hidden])) { padding: 0; scrollbar-gutter: auto; background-image: var(--overview-page-glows), linear-gradient(var(--overview-grid-major) 1px, transparent 1px), linear-gradient(90deg, var(--overview-grid-major) 1px, transparent 1px), linear-gradient(var(--overview-grid-minor) 1px, transparent 1px), linear-gradient(90deg, var(--overview-grid-minor) 1px, transparent 1px); background-position: center top; background-size: auto, auto, 160px 160px, 160px 160px, 32px 32px, 32px 32px; background-attachment: local; }
.dashboard-overview-page { margin: 0; }
.dashboard-overview-page > .custom-view-grid { display: block; background: transparent; }
.dashboard-overview-page .custom-view { margin: 0; }
.dashboard-overview-page > .custom-view-grid > .custom-view { overflow: hidden; border: 0; border-radius: 0; background: transparent; }
.dashboard-overview-page .link-button-list-view > header { position: absolute; width: 1px; height: 1px; padding: 0; overflow: hidden; clip-path: inset(50%); white-space: nowrap; }
.dashboard-overview-page :is(.link-button-list, .link-button-list-skeleton, .link-button-list-empty) { border: 0; border-radius: 0; background: transparent; }
.dashboard-overview-page .link-button-list-item > a { background: var(--canvas-subtle); }
.dashboard-overview-page .link-button-list-item > a:is(:hover, :active) { background: color-mix(in srgb, var(--fg) 12%, var(--canvas-subtle)); }
.dashboard-overview-page .factory-floor { border: 0; }
.factory-intro { min-height: 210px; display: grid; grid-template-columns: minmax(0, 1fr) minmax(280px, 420px); align-items: center; gap: 48px; padding: 32px 40px; background-color: var(--canvas); background-image: var(--overview-page-glows); }
.factory-intro h2 { max-width: 680px; margin: 0; font-size: clamp(2rem, 3.5vw, 3.25rem); font-weight: 600; letter-spacing: 0; line-height: 1.05; }
.factory-intro-onboarding { min-height: 230px; grid-template-columns: minmax(0, 1fr); }
.factory-intro-onboarding .factory-intro-copy { max-width: 760px; }
${firstLoadStyles}
.factory-intro h2.factory-heading-pending { width: min(100%, 560px); height: 3.25rem; border-radius: 6px; background: linear-gradient(90deg, var(--canvas-subtle) 25%, var(--neutral-muted) 50%, var(--canvas-subtle) 75%); background-size: 200% 100%; animation: dashboard-skeleton-pulse 1.5s ease-in-out infinite; }
.factory-floor { min-height: 250px; position: relative; display: grid; align-items: center; padding: 38px 48px; overflow: hidden; border-block: 1px solid var(--border); background: var(--canvas-subtle); }
${overviewGridStyles}
.dashboard-root { --overview-glow: color-mix(in srgb, var(--success) 22%, transparent); --overview-secondary-glow: color-mix(in srgb, var(--success) 14%, transparent); --overview-hero-glow: color-mix(in srgb, var(--success) 13%, transparent); --overview-grid-minor: color-mix(in srgb, var(--success) 9%, transparent); --overview-grid-major: color-mix(in srgb, var(--success) 22%, transparent); --overview-page-glows: radial-gradient(ellipse 58rem 42rem at 76% 8%, var(--overview-glow), transparent 72%), radial-gradient(ellipse 54rem 46rem at 16% 48%, var(--overview-secondary-glow), transparent 74%); }
.dashboard-root[data-theme="light"] { --overview-glow: color-mix(in srgb, var(--success) 12%, transparent); --overview-secondary-glow: color-mix(in srgb, var(--success) 5.5%, transparent); --overview-hero-glow: color-mix(in srgb, var(--success) 10%, transparent); --overview-grid-minor: color-mix(in srgb, var(--success) 5.5%, transparent); --overview-grid-major: color-mix(in srgb, var(--success) 13%, transparent); --overview-page-glows: radial-gradient(circle at 76% 7%, var(--overview-glow), transparent 31rem), radial-gradient(circle at 12% 42%, var(--overview-secondary-glow), transparent 27rem); }
@media (prefers-color-scheme: light) {
  .dashboard-root:not([data-theme]) { --overview-glow: color-mix(in srgb, var(--success) 12%, transparent); --overview-secondary-glow: color-mix(in srgb, var(--success) 5.5%, transparent); --overview-hero-glow: color-mix(in srgb, var(--success) 10%, transparent); --overview-grid-minor: color-mix(in srgb, var(--success) 5.5%, transparent); --overview-grid-major: color-mix(in srgb, var(--success) 13%, transparent); --overview-page-glows: radial-gradient(circle at 76% 7%, var(--overview-glow), transparent 31rem), radial-gradient(circle at 12% 42%, var(--overview-secondary-glow), transparent 27rem); }
}
.factory-floor-active { background-image: radial-gradient(circle at 50% 100%, var(--overview-hero-glow), transparent 34rem); }
.factory-floor-active::before { border-color: var(--overview-grid-major); background-image: linear-gradient(var(--overview-grid-major) 1px, transparent 1px), linear-gradient(90deg, var(--overview-grid-major) 1px, transparent 1px), linear-gradient(var(--overview-grid-minor) 1px, transparent 1px), linear-gradient(90deg, var(--overview-grid-minor) 1px, transparent 1px); background-size: 160px 160px, 160px 160px, 32px 32px, 32px 32px; }
.dashboard-overview-page .factory-floor::before { content: none; }
.factory-floor-onboarding { min-height: 300px; background: var(--canvas-subtle); }
.factory-floor-onboarding-pending { min-height: 300px; }
.factory-onboarding { width: min(100%, 1100px); position: relative; z-index: 1; margin: 0 auto; }
.factory-onboarding-steps { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 18px; margin: 0; padding: 0; list-style: none; }
.factory-onboarding-step { min-width: 0; display: grid; grid-template-columns: auto minmax(0, 1fr); align-items: start; gap: 14px; padding: 20px; border: 1px solid var(--border); border-radius: 12px; background: var(--canvas); }
.factory-onboarding-step-icon { width: 36px; height: 36px; display: grid; place-items: center; border: 1px solid var(--accent); border-radius: 50%; background: var(--accent-muted); color: var(--accent); font-size: .875rem; font-weight: 700; }
.factory-onboarding-step-copy { min-width: 0; display: grid; gap: 8px; }
.factory-onboarding-step-heading { display: flex; align-items: center; justify-content: space-between; gap: 12px; }
.factory-onboarding-step h3 { margin: 0; font-size: 1rem; line-height: 1.3; }
.factory-onboarding-step p { margin: 0; color: var(--muted); font-size: .8125rem; line-height: 1.5; }
.factory-onboarding-action-row { min-width: 0; display: flex; flex-wrap: wrap; align-items: center; gap: 8px; margin-top: 4px; }
.factory-onboarding-action { width: fit-content; min-height: 32px; display: inline-flex; align-items: center; gap: 6px; margin-top: 4px; padding: 5px 10px; border: 1px solid var(--accent); border-radius: 6px; background: var(--accent); color: var(--canvas); font: inherit; font-size: .75rem; font-weight: 600; text-decoration: none; cursor: pointer; }
.factory-onboarding-action-row .factory-onboarding-action { margin-top: 0; }
.factory-onboarding-action:hover { color: var(--canvas); text-decoration: none; filter: brightness(1.08); }
.factory-onboarding-action:focus-visible { outline: 2px solid var(--focus); outline-offset: 2px; }
.factory-onboarding-action-icon { display: inline-flex; }
.factory-onboarding-copy-status { color: var(--muted); font-size: .75rem; }
.factory-stations { position: relative; z-index: 1; display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); gap: 28px; margin: 0; padding: 0; list-style: none; }
.factory-stations[hidden] { display: none; }
.factory-floor-compact .factory-stations { grid-template-columns: repeat(2, minmax(0, 1fr)); }
.factory-station { min-width: 0; display: grid; justify-items: center; text-align: center; animation: factory-station-enter 420ms cubic-bezier(.2, .7, .2, 1) both; }
.factory-station:nth-child(2) { animation-delay: 70ms; }
.factory-station:nth-child(3) { animation-delay: 140ms; }
.factory-station:nth-child(4) { animation-delay: 210ms; }
.factory-station:nth-child(5) { animation-delay: 280ms; }
.factory-station:nth-child(6) { animation-delay: 350ms; }
@keyframes factory-station-enter {
  from { opacity: 0; transform: translateY(8px); }
  to { opacity: 1; transform: translateY(0); }
}
.factory-station-icon { width: 58px; height: 58px; display: grid; place-items: center; margin-bottom: 14px; border: 1px solid var(--border); border-radius: 50%; background: var(--canvas); color: var(--accent); }
.factory-station-icon .octicon { width: 22px; height: 22px; }
.factory-station:nth-child(2) .factory-station-icon { border-color: color-mix(in srgb, var(--accent) 42%, var(--border)); background: color-mix(in srgb, var(--accent) 8%, var(--canvas)); color: var(--accent); }
.factory-station:nth-child(3) .factory-station-icon { border-color: color-mix(in srgb, var(--purple) 42%, var(--border)); background: color-mix(in srgb, var(--purple) 8%, var(--canvas)); color: var(--purple); }
.factory-station-final .factory-station-icon { border-color: color-mix(in srgb, var(--attention) 46%, var(--border)); background: color-mix(in srgb, var(--attention) 9%, var(--canvas)); color: var(--attention); }
.factory-station-empty .factory-station-icon { border-color: var(--border); background: var(--canvas); color: var(--muted); box-shadow: none; }
.factory-station-pending .factory-station-icon { border-color: var(--border-muted); background: var(--canvas-subtle); color: var(--muted); box-shadow: none; }
.factory-station-pending strong { min-width: 48px; height: 1.75rem; border-radius: 6px; background: linear-gradient(90deg, var(--canvas-subtle) 25%, var(--neutral-muted) 50%, var(--canvas-subtle) 75%); background-size: 200% 100%; animation: dashboard-skeleton-pulse 1.5s ease-in-out infinite; }
.factory-station > span:nth-child(2) { color: var(--muted); font-size: .6875rem; font-weight: 700; text-transform: uppercase; letter-spacing: .04em; }
.factory-station strong { margin-top: 3px; font-size: 1.75rem; font-variant-numeric: tabular-nums; line-height: 1; }
.factory-station small { margin-top: 5px; color: var(--muted); font-size: .6875rem; }
.factory-station a { min-width: 24px; min-height: 24px; display: inline-flex; align-items: center; justify-content: center; color: inherit; text-decoration: none; }
.factory-station a:hover { color: var(--accent); text-decoration: underline; }
.factory-rhythm { min-width: 0; display: grid; grid-template-columns: minmax(180px, .35fr) minmax(0, 1fr); align-items: center; gap: 32px; padding: 24px 40px; border-bottom: 1px solid var(--border); background: var(--canvas); }
.factory-intro .factory-rhythm { grid-template-columns: minmax(0, 1fr); align-self: stretch; gap: 16px; padding: 0; border: 0; background: transparent; }
.factory-intro .factory-rhythm[hidden] { display: none; }
.factory-rhythm-heading { display: grid; gap: 5px; }
.factory-rhythm-heading > span { color: var(--muted); font-size: .6875rem; font-weight: 700; text-transform: uppercase; letter-spacing: .04em; }
.factory-rhythm-heading strong, .factory-rhythm-legend { color: var(--muted); font-size: .6875rem; font-weight: 400; }
.factory-rhythm-legend { display: flex; flex-wrap: wrap; gap: 5px 12px; margin: 0; padding: 0; list-style: none; }
.factory-rhythm-legend li { display: inline-flex; align-items: center; gap: 5px; }
.factory-rhythm-legend i { width: 9px; height: 9px; display: inline-block; border-radius: 2px; }
.factory-rhythm-legend-current { background: color-mix(in srgb, var(--success) 72%, var(--accent)); }
.factory-rhythm-legend-previous { border: 1px solid var(--border); background: var(--canvas-subtle); }
.factory-rhythm-summary { margin: 0; color: var(--muted); font-size: .75rem; }
.factory-rhythm-bars { height: 74px; position: relative; display: grid; grid-template-columns: repeat(7, minmax(18px, 1fr)); align-items: end; gap: 9px; }
.overview-campaigns-view-all { display: inline-flex; justify-self: start; margin-top: 12px; color: var(--accent); font-size: .8125rem; font-weight: 600; text-decoration: none; }
.overview-campaigns-view-all:hover { text-decoration: underline; }
.factory-rhythm-bars > .factory-rhythm-day { height: 100%; position: relative; display: grid; grid-template-rows: 1fr auto; align-items: end; gap: 5px; border: 0; border-radius: 3px; background: transparent; text-align: center; cursor: default; }
.factory-rhythm-bar-pair { height: 100%; position: relative; display: block; }
.factory-rhythm-bar-pair i { position: absolute; bottom: 0; left: 50%; min-width: 4px; min-height: 5px; display: block; border-radius: 3px 3px 1px 1px; transform: translateX(-50%); transform-origin: center bottom; animation: factory-rhythm-bar-grow 360ms cubic-bezier(.2, .7, .2, 1) both; animation-delay: calc((var(--factory-rhythm-day, 1) - 1) * 35ms); }
.factory-rhythm-bar-pair i[hidden] { display: none; }
.factory-rhythm-day:nth-child(2) { --factory-rhythm-day: 2; }
.factory-rhythm-day:nth-child(3) { --factory-rhythm-day: 3; }
.factory-rhythm-day:nth-child(4) { --factory-rhythm-day: 4; }
.factory-rhythm-day:nth-child(5) { --factory-rhythm-day: 5; }
.factory-rhythm-day:nth-child(6) { --factory-rhythm-day: 6; }
.factory-rhythm-day:nth-child(7) { --factory-rhythm-day: 7; }
.factory-rhythm-pending .factory-rhythm-bar-pair i { animation-play-state: paused; }
@keyframes factory-rhythm-bar-grow {
  from { opacity: .4; transform: translateX(-50%) scaleY(0); }
}
.factory-rhythm-bar-pair .factory-rhythm-baseline { width: 58%; border: 1px solid var(--border); background: var(--canvas-subtle); }
.factory-rhythm-bar-pair .factory-rhythm-current { z-index: 1; width: 58%; background: color-mix(in srgb, var(--success) 72%, var(--accent)); }
.factory-rhythm-bars small { color: var(--muted); font-size: .625rem; font-style: normal; }
.factory-rhythm-pending .factory-rhythm-bars { border-radius: 6px; background: linear-gradient(90deg, var(--canvas-subtle) 25%, var(--neutral-muted) 50%, var(--canvas-subtle) 75%); background-size: 200% 100%; animation: dashboard-skeleton-pulse 1.5s ease-in-out infinite; }
.factory-rhythm-pending .factory-rhythm-bars > * { visibility: hidden; }
@media (max-width: 900px) {
  .factory-intro { grid-template-columns: minmax(0, 1fr); gap: 28px; padding: 32px; }
  .factory-rhythm { grid-template-columns: minmax(0, 1fr); gap: 16px; }
  .factory-onboarding-steps { grid-template-columns: minmax(0, 1fr); }
}
@media (max-width: 700px) {
  .factory-intro { min-height: 0; gap: 24px; padding: 24px 20px; }
  .factory-intro h2 { font-size: 2rem; }
  .factory-floor { min-height: 0; padding: 28px 20px; }
  .factory-floor-onboarding { min-height: 0; }
  .factory-onboarding-step { padding: 18px; }
  .factory-onboarding-action { min-height: 44px; justify-content: center; }
  .factory-stations { grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 28px 16px; }
  .factory-station-icon { width: 48px; height: 48px; margin-bottom: 10px; }
  .factory-station strong { font-size: 1.5rem; }
  .factory-rhythm { padding: 0; }
  .factory-rhythm-bars { gap: 5px; }
  .factory-rhythm-bars { height: 58px; }
  .overview-campaign-status .campaign-status-card:nth-child(n + 4) { display: none; }
  .link-button-list-view > header { margin-top: 24px; }
}
@media (prefers-reduced-motion: reduce) {
  .factory-station, .factory-rhythm-bar-pair i, .link-button-list-skeleton-row > span { animation: none; }
}
`;
