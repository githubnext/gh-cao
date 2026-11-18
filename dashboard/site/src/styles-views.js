export const viewStyles = `.dashboard-pages { display: flex; flex-direction: column; gap: 24px; }
.dashboard-page { padding: 0; }
.dashboard-page[hidden] { display: none; }
.dashboard-page > h2 { margin: 0 0 14px; font-size: 1.25rem; font-weight: 600; }
.page-layout-grid { display: grid; grid-template-columns: repeat(12, minmax(0, 1fr)); gap: 16px; align-items: start; }
.layout-section { min-width: 0; grid-column: span 12; padding: 16px; border: 1px solid var(--border); border-radius: 0; background: var(--canvas); }
.layout-section[data-section-layout="wide"] { grid-column: span 7; }
.layout-section[data-section-layout="narrow"] { grid-column: span 5; }
.layout-section[data-section-layout="horizontal"] { padding: 0; border: 0; background: transparent; }
.layout-section[data-section-layout="horizontal"] > .custom-view-grid { grid-template-columns: repeat(auto-fit, minmax(min(100%, 220px), 1fr)); gap: 1px; overflow: hidden; border: 1px solid var(--border); border-radius: 0; background: var(--border); }
.layout-section[data-section-layout="horizontal"] > .custom-view-grid > .custom-view { grid-column: auto; }
.layout-section-header { margin-bottom: 12px; }
.layout-section-header :is(h2, h3) { margin: 0; font-size: 1rem; }
.layout-section-header-summary :is(h2, h3) { font-size: 1.25rem; }
.layout-section-header p { margin: 3px 0 0; color: var(--muted); font-size: .8125rem; }
.layout-section .page-section { min-width: 0; }
.custom-view.page-section > :is(h2, h3, h4) { width: 1px; height: 1px; position: absolute; overflow: hidden; margin: -1px; padding: 0; clip: rect(0 0 0 0); white-space: nowrap; border: 0; }
.custom-view[data-view-mark="chart"][data-disclosure="essential"] { min-height: 280px; }
.custom-view[data-view-mark="chart"] > .dashboard-view-skeleton { height: 280px; overflow: hidden; }
.layout-section .page-section > :is(h3, h4), .layout-section .page-section > .chart-prompt-heading > :is(h3, h4) { margin: 12px 0 8px; font-size: .875rem; font-weight: 600; }
.semantic-prompt-view { position: relative; }
.semantic-prompt-action { position: absolute; top: 0; right: 32px; display: block; width: fit-content; margin: 0; }
.semantic-prompt-action .table-intent-button { width: 32px; min-height: 32px; justify-content: center; padding: 0; border-color: transparent; background: transparent; color: var(--accent); }
.semantic-prompt-action .table-intent-button:hover { border-color: transparent; background: var(--accent-muted); color: var(--accent); }
.semantic-prompt-action .table-intent-button span { display: none; }
[data-view-mode-content="card"] .semantic-prompt-action,
.dashboard-page[data-view-mode="card"] [data-view-mode-content="table"] .semantic-prompt-action { display: none; }
.dashboard-page[data-view-mode="card"] [data-view-mode-content="table"] .mobile-table-card-toolbar > .semantic-prompt-action { display: block; }
.semantic-prompt-heading { display: flex; align-items: center; justify-content: space-between; gap: 12px; margin-bottom: 12px; }
.semantic-prompt-heading > :is(h2, h3, h4) { margin: 0; }
.chart-prompt-heading { display: flex; align-items: center; gap: 8px; min-width: 0; }
.custom-view[aria-busy="true"] > .chart-prompt-heading { min-height: 32px; }
.chart-prompt-heading > :is(h2, h3, h4) { min-width: 0; margin: 0; }
.chart-prompt-action { margin-left: auto; flex: none; }
.chart-prompt-action .table-intent-button { width: 32px; min-height: 32px; justify-content: center; padding: 0; border-color: transparent; background: transparent; color: var(--accent); }
.chart-prompt-action .table-intent-button:hover { border-color: transparent; background: var(--accent-muted); color: var(--accent); }
.chart-prompt-action .table-intent-button span { display: none; }
.semantic-prompt-view:has([aria-busy]:not([aria-busy="false"])) :is(.semantic-prompt-action, .chart-prompt-action) { display: none; }
.custom-view-grid { display: grid; grid-template-columns: repeat(12, minmax(0, 1fr)); gap: 16px; }
.card-filter-bar { flex: none; display: flex; flex-wrap: wrap; align-items: center; gap: 8px; position: relative; z-index: 5; padding: 8px 16px; border: 1px solid var(--border); border-radius: 6px 6px 0 0; background: var(--canvas-subtle); }
.card-filter-menu { position: relative; }
.card-filter-menu > summary { display: flex; align-items: center; gap: 6px; min-height: 32px; padding: 4px 12px; border: 1px solid var(--border); border-radius: 6px; background: var(--canvas-subtle); color: var(--fg); font-size: .875rem; font-weight: 600; list-style: none; cursor: pointer; }
.card-filter-menu > summary::-webkit-details-marker { display: none; }
.card-filter-menu > summary:hover, .card-filter-menu[open] > summary { background: var(--neutral-muted); color: var(--fg); }
.card-filter-menu > summary .octicon { width: 12px; height: 12px; }
.card-filter-popover { width: min(300px, calc(100vw - 64px)); max-height: min(480px, 65vh); overflow: auto; position: absolute; top: calc(100% + 4px); left: 0; z-index: 30; padding: 12px; border: 1px solid var(--border); border-radius: 6px; background: var(--canvas); box-shadow: 0 8px 24px color-mix(in srgb, var(--canvas-inset) 45%, transparent); }
.card-filter-group { min-width: 0; margin: 0 0 12px; padding: 0; border: 0; }
.card-filter-group legend { padding: 4px 0; color: var(--muted); font-size: .75rem; font-weight: 600; }
.card-filter-group label { display: flex; align-items: center; gap: 8px; min-height: 36px; padding: 4px; overflow-wrap: anywhere; cursor: pointer; }
.card-filter-group input { flex: none; accent-color: var(--accent); }
.card-filter-group p { margin: 4px 0; color: var(--muted); font-size: .8125rem; }
.card-filter-bar button { min-height: 32px; padding: 4px 10px; border: 1px solid var(--border); border-radius: 6px; background: var(--canvas-subtle); color: var(--fg); font: inherit; cursor: pointer; }
.card-filter-bar button:hover { background: var(--neutral-muted); }
.card-filter-bar :is(summary, button, input):focus-visible { outline: 2px solid var(--focus); outline-offset: 2px; }
.card-filter-clear { display: inline-flex; align-items: center; gap: 6px; margin-left: auto; }
.card-filter-bar [hidden] { display: none; }
.card-filter-bar + :is(.document-list, .table-region) { margin-top: 0; border-top: 0; border-radius: 0 0 6px 6px; }
.custom-view { min-width: 0; grid-column: span 12; }
.metric-card-widget { min-width: 0; min-height: 172px; display: grid; grid-template-rows: 2.5rem 2.7em 32px; align-content: center; justify-items: center; gap: 8px; padding: 24px 20px; background: var(--canvas-subtle); color: var(--fg); text-align: center; text-decoration: none; }
.metric-card-widget-active { --metric-card-color: var(--accent); background: color-mix(in srgb, var(--metric-card-color) 7%, var(--canvas-subtle)); box-shadow: inset 0 3px var(--metric-card-color); }
.metric-card-widget-danger { --metric-card-color: var(--danger); }
.metric-card-widget-attention { --metric-card-color: var(--attention); }
.metric-card-widget-review { --metric-card-color: var(--purple); }
.metric-card-widget[href]:hover { background: color-mix(in srgb, var(--metric-card-color, var(--accent)) 12%, var(--canvas-subtle)); }
.metric-card-widget:focus-visible { outline: 2px solid var(--focus); outline-offset: -2px; }
@property --metric-number { syntax: "<integer>"; inherits: false; initial-value: 0; }
@keyframes metric-number-count {
  from { --metric-number: 0; }
  to { --metric-number: var(--metric-number-target); }
}
.view-description { margin: 3px 0 0; color: var(--muted); }
.metric-card-widget-value { min-height: 1em; align-self: center; color: var(--muted); font-size: 2.5rem; font-weight: 600; line-height: 1; font-variant-numeric: tabular-nums; }
@supports (property: --metric-number) {
  .metric-number-animated { --metric-number: 0; position: relative; color: transparent; counter-reset: metric-number var(--metric-number); animation: metric-number-count 700ms ease-out both; }
  .metric-number-animated::after { content: counter(metric-number); position: absolute; inset: 0; color: var(--metric-card-color, var(--muted)); }
  .factory-station .metric-number-animated::after { color: var(--fg); }
}
@media (prefers-reduced-motion: reduce) {
  .metric-number-animated { animation: none; --metric-number: var(--metric-number-target); }
}
.metric-card-widget-active .metric-card-widget-value { color: var(--metric-card-color); }
.metric-card-widget-label { min-width: 0; height: 100%; display: flex; align-items: flex-end; justify-content: center; margin: 0; color: var(--muted); font-size: .8125rem; font-weight: 600; letter-spacing: .04em; line-height: 1.35; text-transform: uppercase; }
.metric-card-widget-active .metric-card-widget-label { color: var(--fg); }
.metric-card-widget-icon { display: flex; align-items: center; color: var(--muted); }
.metric-card-widget-active .metric-card-widget-icon { color: var(--metric-card-color); }
.metric-card-widget-icon .octicon { width: 32px; height: 32px; flex-basis: 32px; }
.metric-card-widget .view-source, .metric-card-widget .view-metadata { position: absolute; width: 1px; height: 1px; padding: 0; overflow: hidden; clip: rect(0, 0, 0, 0); white-space: nowrap; border: 0; }
.custom-view[data-view-layout="full-view"] { min-height: 100%; display: flex; flex-direction: column; }
.custom-view[data-view-layout="full-view"] > .table-region { min-height: 0; flex: 1; display: flex; flex-direction: column; }
.custom-view[data-view-layout="full-view"] > .document-list-header { flex: none; margin: 0; padding: 12px 24px; border-bottom: 1px solid var(--border); background: var(--canvas); }
.custom-view[data-view-layout="full-view"] > .document-list { min-height: 0; flex: 1; overflow: auto; }
.custom-view[data-view-layout="full-view"] .table-scroll { min-height: 0; flex: 1; overflow: auto; }
.dashboard-full-view .overview-header .lede,
.dashboard-full-view .report-footer,
.dashboard-full-view .custom-view[data-view-layout="full-view"] > :is(h2, h3, h4) { display: none; }
.dashboard-full-view main.dashboard-prototype { overflow: hidden; padding: 0; scrollbar-gutter: auto; }
.dashboard-full-view main.dashboard-prototype:has(.command-terminal) { padding: 0; }
.dashboard-full-view .custom-view-grid > .custom-view.command-terminal { padding: 0; }
.dashboard-root:has(.command-terminal) .overview-header .lede { display: block; visibility: visible; }
.dashboard-full-view :is(.report-body, .dashboard-pages, .dashboard-page:not([hidden]), .custom-view-grid) { height: 100%; min-height: 0; }
.dashboard-full-view .dashboard-page[data-view-mode]:not([hidden]) { display: flex; flex-direction: column; }
.dashboard-full-view .dashboard-page > :is(.custom-view-grid, .page-layout-grid) { flex: 1; min-height: 0; }
.dashboard-full-view .dashboard-page > :is(.page-chrome, .route-tab-navigation, [data-route-tabs]) { flex: none; }
.dashboard-full-view .dashboard-page[data-view-mode] > .page-chrome { padding-top: 12px; margin-bottom: 0; }
.dashboard-full-view .dashboard-pages,
.dashboard-full-view .custom-view-grid { gap: 0; }
.dashboard-full-view .custom-view-grid:has(> .custom-view[data-view-layout="full-view"]) {
  display: flex;
  flex-direction: column;
  overflow: hidden;
}
.dashboard-full-view .custom-view-grid > .custom-view[data-view-layout="full-view"] {
  min-height: 0;
  flex: 1;
  padding-inline: 0;
}
.dashboard-full-view .custom-view-grid:has(> .view-disclosure[open]) > .custom-view[data-view-layout="full-view"] { flex-basis: 50%; }
.dashboard-full-view .custom-view-grid > .view-disclosure[open] { min-height: 0; overflow-y: auto; }
.dashboard-full-view .dashboard-page[data-view-mode="chart"] > .custom-view-grid:has(> .custom-view[data-view-layout="full-view"]) {
  gap: 16px;
  overflow-y: auto;
}
.dashboard-full-view .dashboard-page[data-view-mode="chart"] > .custom-view-grid:has(> .custom-view[data-view-layout="full-view"]) > .custom-view {
  flex: none;
}
.dashboard-full-view .custom-view[data-view-layout="full-view"] > .view-state-card { margin-inline: var(--dashboard-page-padding-inline); }
.dashboard-full-view .custom-view[data-view-layout="full-view"] > .table-region {
  margin: 0;
  overflow: hidden;
  border: 0;
  border-radius: 0;
}
.dashboard-full-view .custom-view[data-view-layout="full-view"] > .document-list {
  margin: 0;
  border: 0;
  border-radius: 0;
}
.dashboard-full-view .card-filter-bar { border-inline: 0; border-radius: 0; }
.dashboard-full-view .custom-view[data-view-layout="full-view"] .table-scroll { max-height: none; }
/* Keep the search inside the table scroll surface without giving it a second horizontal scrollbar. */
.dashboard-full-view .custom-view[data-view-layout="full-view"] .table-filter { width: 100%; max-width: 100%; box-sizing: border-box; position: sticky; left: 0; z-index: 2; flex: none; min-width: 0; overflow-x: visible; }
.dashboard-root.dashboard-full-view-scrolled .app-main > .top-nav { display: none; }
.dashboard-full-view .custom-view[data-view-layout="full-view"] .table-scroll thead > tr:first-child > th,
.dashboard-full-view .custom-view[data-view-layout="full-view"] .table-summary-row > th { transition: opacity 160ms ease; }
.dashboard-root.dashboard-full-view-scrolled .custom-view[data-view-layout="full-view"] .table-scroll thead > tr:first-child > th { opacity: .8; }
.dashboard-root.dashboard-full-view-scrolled .custom-view[data-view-layout="full-view"] .table-summary-row > th { opacity: 0; pointer-events: none; }
.dashboard-root.dashboard-full-view-scrolled .custom-view-grid > :has(~ .custom-view[data-view-layout="full-view"]) { display: none; }
.custom-view[data-view-layout="half"] { grid-column: span 6; }
.custom-view[data-view-layout="third"] { grid-column: span 4; }
.view-state-card { display: grid; grid-template-columns: auto minmax(0, 1fr); align-items: start; gap: 10px; margin: 12px 0; padding: 12px 14px; border: 1px solid var(--border); border-left-width: 4px; border-radius: 0; background: var(--canvas-subtle); color: var(--fg); font-size: .875rem; }
.view-state-card[hidden], .page-layout-grid[hidden] { display: none; }
.package-recovery-actions { display: flex; flex-wrap: wrap; gap: 8px; margin-top: 12px; }
.package-recovery-actions :is(button, a) { display: inline-flex; min-height: 40px; align-items: center; padding: 8px 12px; border: 1px solid var(--border); border-radius: 6px; background: var(--canvas); color: var(--fg); font: inherit; text-decoration: none; cursor: pointer; }
.package-recovery-actions button { border-color: var(--accent); background: var(--accent); color: var(--canvas); }
.package-recovery-actions :is(button, a):focus-visible { outline: 2px solid var(--accent); outline-offset: 2px; }
.view-state-card > .octicon { width: 16px; height: 16px; margin-top: 1px; color: var(--muted); }
.view-state-card[data-view-state="unavailable"] { border-color: color-mix(in srgb, var(--attention) 45%, var(--border)); border-left-color: var(--attention); background: var(--attention-muted); }
.view-state-card[data-view-state="unavailable"] > .octicon { color: var(--attention); }
.view-state-card-body { min-width: 0; display: grid; gap: 4px; }
.view-state-card :is(p, ul) { margin: 0; }
.view-state-message { font-weight: 600; }
.view-state-card .view-source, .view-state-card .view-context, .view-state-card .view-diagnostic { color: var(--muted); font-size: .8125rem; }
.view-state-card .view-diagnostic { overflow-wrap: anywhere; }
.view-state-card .view-context { padding-inline-start: 1.25em; }
.view-metadata-summary { display: flex; flex-wrap: wrap; align-items: center; gap: 6px 16px; margin: 0 0 12px; color: var(--fg); }
.view-metadata-summary > div { display: inline-flex; align-items: center; gap: 7px; }
.view-metadata-summary dt { display: inline-flex; align-items: center; gap: 5px; color: var(--muted); font-size: .75rem; font-weight: 500; }
.view-metadata-summary dt .octicon { width: 14px; height: 14px; flex-basis: 14px; }
.view-metadata-summary dd { margin: 0; font-size: .75rem; font-weight: 600; }
.view-disclosure { overflow: hidden; border: 1px solid var(--border); border-radius: 6px; background: var(--canvas-subtle); }
.view-disclosure > summary { display: flex; align-items: center; justify-content: space-between; gap: 16px; padding: 12px 14px; color: var(--fg); font-weight: 600; cursor: pointer; transition: background-color 120ms ease; }
.view-disclosure > summary:hover { background: var(--neutral-muted); }
.view-disclosure > summary::marker { color: var(--muted); }
.view-disclosure[open] > summary { border-bottom: 1px solid var(--border); }
.view-disclosure-hint { color: var(--muted); font-size: .75rem; font-weight: 400; }
.view-disclosure[open] .view-disclosure-hint { font-size: 0; }
.view-disclosure[open] .view-disclosure-hint::after { content: "Hide details"; font-size: .75rem; }
.view-disclosure > .page-section { padding: 0 14px 14px; }
.dashboard-full-view .custom-view-grid > .custom-view { padding-inline: var(--dashboard-page-padding-inline); }
.chart-view-pie { display: grid; gap: 16px; }
`;
