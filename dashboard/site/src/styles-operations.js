export const operationStyles = `.dashboard-next-work-page .custom-view-grid { display: block; }
.dashboard-next-insights-page .custom-view-grid { display: block; }
.insights-overview { display: grid; gap: 28px; }
.insights-value-lead { min-width: 0; display: grid; gap: 14px; padding-bottom: 24px; border-bottom: 1px solid var(--border); }
.insights-section-heading { display: flex; align-items: end; justify-content: space-between; gap: 24px; min-width: 0; }
.insights-section-heading h2, .insights-plot-panel :is(h2, h3) { margin: 2px 0 4px; font-size: 1rem; }
.insights-section-heading p, .insights-plot-panel header p { max-width: 680px; margin: 0; color: var(--muted); font-size: .75rem; line-height: 1.45; }
.operational-value-scope-control { min-width: min(320px, 100%); display: grid; gap: 6px; color: var(--muted); font-size: .6875rem; font-weight: 600; }
.operational-value-scope-select { min-height: 32px; width: 100%; padding: 5px 28px 5px 10px; border: 1px solid var(--border); border-radius: 6px; background: var(--canvas); color: var(--fg); font: inherit; font-size: .75rem; }
.operational-value-scope-select:focus-visible { outline: 2px solid var(--focus); outline-offset: 2px; }
.operational-value-scope-status { margin: 12px 0 0; color: var(--muted); font-size: .75rem; }
.insights-eyebrow { color: var(--accent); font-size: .6875rem; font-weight: 700; text-transform: uppercase; letter-spacing: .04em; }
.insights-lead-metrics, .insights-inline-metrics { display: flex; gap: 26px; margin: 0; }
.insights-lead-metrics > div, .insights-inline-metrics > div { display: grid; gap: 2px; }
.insights-lead-metrics dt, .insights-inline-metrics dt { grid-row: 2; color: var(--muted); font-size: .6875rem; white-space: nowrap; }
.insights-lead-metrics dd, .insights-inline-metrics dd { grid-row: 1; margin: 0; color: var(--fg); font-size: 1.125rem; font-weight: 600; font-variant-numeric: tabular-nums; }
.insights-value-chart { min-width: 0; }
.insights-value-lead .chart-widget { min-height: 260px; padding: 0; }
.insights-value-lead .chart-widget svg { max-height: 280px; }
.insights-series-selector { position: relative; justify-self: end; }
.insights-series-selector > summary { min-height: 30px; display: inline-flex; align-items: center; gap: 8px; padding: 4px 9px; border: 1px solid var(--border); border-radius: 6px; background: var(--canvas); color: var(--fg); font-size: .75rem; font-weight: 600; cursor: pointer; list-style: none; }
.insights-series-selector > summary::-webkit-details-marker { display: none; }
.insights-series-selector > summary::after { content: ""; width: 6px; height: 6px; border-right: 1.5px solid currentColor; border-bottom: 1.5px solid currentColor; transform: translateY(-2px) rotate(45deg); }
.insights-series-selector[open] > summary::after { transform: translateY(2px) rotate(225deg); }
.insights-series-count { color: var(--muted); font-weight: 400; font-variant-numeric: tabular-nums; }
.insights-series-menu { width: min(360px, calc(100vw - 28px)); max-height: min(420px, 60vh); display: grid; gap: 8px; overflow: auto; position: absolute; z-index: 15; top: calc(100% + 5px); right: 0; padding: 8px; border: 1px solid var(--border); border-radius: 6px; background: var(--canvas); box-shadow: 0 8px 24px color-mix(in srgb, var(--canvas-inset) 45%, transparent); }
.insights-series-actions { display: flex; justify-content: flex-end; gap: 4px; padding-bottom: 6px; border-bottom: 1px solid var(--border); }
.insights-series-actions button { padding: 3px 7px; border: 0; border-radius: 4px; background: transparent; color: var(--accent); font: inherit; font-size: .6875rem; font-weight: 600; cursor: pointer; }
.insights-series-actions button:hover { background: var(--accent-muted); }
.insights-series-menu fieldset { display: grid; gap: 2px; margin: 0; padding: 0; border: 0; }
.insights-series-menu legend { position: absolute; width: 1px; height: 1px; overflow: hidden; clip: rect(0 0 0 0); }
.insights-series-menu label { min-width: 0; display: grid; grid-template-columns: 16px 18px minmax(0, 1fr); align-items: center; gap: 8px; padding: 5px 6px; border-radius: 4px; color: var(--fg); font-size: .75rem; cursor: pointer; }
.insights-series-menu label:hover { background: var(--canvas-subtle); }
.insights-series-menu label i { width: 18px; height: 0; border-top-width: 2px; border-top-style: solid; }
.insights-series-menu label span { min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.insights-plot-grid { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 28px 32px; padding-top: 4px; }
.insights-plot-panel { min-width: 0; display: grid; align-content: start; gap: 12px; }
.insights-plot-panel[hidden] { display: none; }
.insights-plot-panel .chart-widget { min-height: 210px; padding: 0; }
.insights-plot-panel .pie-chart-widget { min-height: 190px; }
.insights-plot-panel .pie-chart-widget svg { max-height: 180px; }
.insights-plot-panel .swimlane-chart-widget svg { max-height: 210px; }
.insights-panel-stat { display: flex; align-items: baseline; gap: 7px; color: var(--muted); font-size: .75rem; }
.insights-panel-stat strong { color: var(--fg); font-size: 1.125rem; font-variant-numeric: tabular-nums; }
.insights-measure-rows { display: grid; grid-template-columns: minmax(0, 1fr); gap: 28px; padding-top: 4px; }
.insights-measure-row { padding-bottom: 24px; border-bottom: 1px solid var(--border); }
.insights-measure-row:last-child { padding-bottom: 0; border-bottom: 0; }
.insights-measure-heading { display: flex; align-items: center; justify-content: space-between; gap: 12px; }
.insights-measure-heading h3 { margin: 0; }
.insights-dubious-flag { display: inline-flex; align-items: center; gap: 8px; color: var(--attention); font-size: .75rem; font-weight: 600; text-transform: uppercase; letter-spacing: .04em; }
.insights-measure-plot { display: grid; grid-template-columns: auto minmax(0, 1fr); align-items: center; gap: 4px 8px; }
.insights-measure-canvas { min-width: 0; }
.insights-measure-row .chart-widget { min-height: 240px; padding: 0; }
.insights-axis-label { color: var(--muted); font-size: .6875rem; font-weight: 600; text-transform: uppercase; letter-spacing: .02em; }
.insights-axis-y { writing-mode: vertical-rl; transform: rotate(180deg); justify-self: center; text-align: center; }
.insights-axis-x { grid-column: 2; text-align: center; }
.insights-point-readout { display: flex; align-items: baseline; flex-wrap: wrap; gap: 4px 12px; margin: 0; color: var(--muted); font-size: .75rem; }
.insights-point-readout strong { color: var(--fg); font-size: 1.125rem; font-variant-numeric: tabular-nums; }
.insights-measure-row .chart-point[data-selected="true"] :is(.line-chart-point, .dot-chart-point) { stroke: var(--focus); stroke-width: calc(var(--chart-point-size, 4px) + 3px); }
.insights-measure-row .chart-point[data-selected="true"] .point-tooltip { opacity: 1; }
.insights-experiment-band { min-width: 0; display: grid; gap: 14px; padding-top: 4px; }
.insights-experiment-band .chart-widget { min-height: 230px; padding: 0; }
.insights-decision-count { font-size: 1.25rem; white-space: nowrap; }
.insights-decision-count small { color: var(--muted); font-size: .6875rem; font-weight: 400; }
.work-project-view { display: grid; gap: 16px; }
.work-project-tabs { display: flex; align-items: center; gap: 0; width: 100%; overflow-x: auto; border-bottom: 1px solid var(--border); }
.work-project-tabs a { min-height: 36px; display: inline-flex; align-items: center; gap: 6px; position: relative; padding: 5px 13px; color: var(--muted); font-size: .8125rem; font-weight: 600; text-decoration: none; white-space: nowrap; }
.work-project-tabs a:hover { color: var(--fg); background: var(--canvas-subtle); }
.work-project-tabs a[aria-current="page"] { color: var(--fg); }
.work-project-tabs a[aria-current="page"]::after { height: 2px; position: absolute; right: 10px; bottom: -1px; left: 10px; border-radius: 2px 2px 0 0; background: var(--accent); content: ""; }
.work-project-tab-icon { display: grid; }
.work-project-tab-icon .octicon { width: 14px; height: 14px; }
.work-filter-mobile-toggle, .work-mobile-sheet-header, .work-board-group-tabs, .work-task-settings-toggle, .work-mobile-field-settings, .work-mobile-owner, .work-mobile-item-actions, .work-roadmap-period-heading, .work-roadmap-mobile-meta, .work-roadmap-visual-toggle, .work-roadmap-mobile-controls { display: none; }
.work-filter-bar { min-width: 0; display: grid; grid-template-columns: minmax(220px, 1fr) auto auto auto; align-items: center; gap: 8px; padding: 8px; border: 1px solid var(--border); border-radius: 6px; background: var(--canvas-subtle); }
.work-filter-search { min-width: 0; height: 32px; display: grid; grid-template-columns: 18px minmax(0, 1fr); align-items: center; gap: 6px; padding: 0 9px; border: 1px solid var(--border); border-radius: 6px; background: var(--canvas); color: var(--muted); }
.work-filter-search:focus-within { border-color: var(--accent); box-shadow: 0 0 0 2px var(--accent-muted); }
.work-filter-search-icon { display: grid; }
.work-filter-search-icon .octicon { width: 14px; height: 14px; }
.work-filter-search input { min-width: 0; height: 30px; padding: 0; border: 0; outline: 0; background: transparent; color: var(--fg); font: inherit; font-size: .8125rem; }
.work-filter-search input::placeholder { color: var(--muted); }
.work-filter-facets { min-width: 0; display: flex; gap: 6px; overflow-x: auto; }
.work-filter-sheet-panel { display: flex; gap: 6px; }
.work-filter-facets select { max-width: 180px; height: 32px; padding: 0 28px 0 9px; border: 1px solid var(--border); border-radius: 6px; background: var(--canvas); color: var(--fg); font: inherit; font-size: .75rem; }
.work-filter-count { min-width: 58px; color: var(--muted); font-size: .75rem; font-variant-numeric: tabular-nums; text-align: right; white-space: nowrap; }
.work-filter-clear { width: 32px; height: 32px; display: grid; place-items: center; padding: 0; border: 1px solid var(--border); border-radius: 6px; background: var(--canvas); color: var(--fg); cursor: pointer; }
.work-filter-clear:hover:not(:disabled) { background: var(--neutral-muted); }
.work-filter-clear:disabled { color: var(--muted); cursor: default; opacity: .45; }
.work-filter-clear-icon { display: grid; }
.work-filter-clear-icon .octicon { width: 14px; height: 14px; }
.work-project-body { min-width: 0; }
.work-board { display: grid; grid-template-columns: repeat(4, minmax(180px, 1fr)); gap: 12px; align-items: stretch; overflow-x: auto; padding-bottom: 4px; }
.work-board-column { min-width: 180px; height: clamp(480px, calc(100vh - 270px), 760px); display: grid; grid-template-rows: auto minmax(0, 1fr); gap: 8px; padding: 10px; border: 1px solid var(--border); border-radius: 8px; background: var(--canvas-subtle); }
.work-board-column > header { display: flex; align-items: center; justify-content: space-between; gap: 8px; }
.work-board-column h4, .work-project-section-heading h4 { margin: 0; font-size: .875rem; }
.work-board-cards { min-height: 0; display: grid; align-content: start; gap: 8px; overflow-y: auto; scrollbar-gutter: stable; }
.work-card-stack { min-width: 0; display: grid; gap: 7px; padding-left: 8px; border-left: 2px solid color-mix(in srgb, var(--accent) 50%, var(--border)); }
.work-card-stack > header { display: flex; align-items: center; justify-content: space-between; gap: 8px; padding: 0 3px; color: var(--muted); font-size: .6875rem; }
.work-card-workers { min-width: 0; }
.work-card-workers > summary { min-height: 32px; display: grid; grid-template-columns: 14px minmax(0, 1fr) auto; align-items: center; gap: 6px; padding: 5px 8px; border: 1px dashed var(--border); border-radius: 6px; background: var(--canvas-inset); color: var(--fg); font-size: .6875rem; font-weight: 700; cursor: pointer; list-style: none; }
.work-card-workers > summary::-webkit-details-marker { display: none; }
.work-card-workers > summary:hover { border-style: solid; background: var(--canvas-subtle); }
.work-card-workers > summary small { color: var(--muted); font-size: .75rem; font-weight: 500; }
.work-card-workers-chevron { color: var(--muted); transition: transform 120ms ease; }
.work-card-workers-chevron .octicon { width: 12px; height: 12px; }
.work-card-workers[open] .work-card-workers-chevron { transform: rotate(90deg); }
.work-card-worker-list { display: grid; gap: 7px; padding-top: 7px; }
.work-card { min-width: 0; display: grid; gap: 8px; padding: 12px; border: 1px solid var(--border); border-radius: 0; background: var(--canvas); box-shadow: 0 1px 0 var(--border-muted); }
.work-card > header, .work-task-row, .work-roadmap-label { min-width: 0; display: flex; align-items: center; gap: 8px; }
.work-card > header > strong { flex: 1; }
.work-card strong, .work-task-main strong, .work-roadmap-label strong { min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.work-card p { margin: 0; overflow: hidden; color: var(--muted); font-size: .75rem; text-overflow: ellipsis; white-space: nowrap; }
.work-avatar { width: 28px; height: 28px; display: grid; flex: 0 0 28px; place-items: center; border: 1px solid var(--border); border-radius: 50%; background: var(--accent-muted); color: var(--accent); }
.work-avatar .octicon { width: 14px; height: 14px; flex-basis: 14px; }
.work-owner-avatar { width: 24px; height: 24px; display: grid; flex: 0 0 24px; place-items: center; border: 1px solid color-mix(in srgb, var(--accent) 35%, var(--border)); border-radius: 50%; background: color-mix(in srgb, var(--accent) 14%, var(--canvas)); color: var(--accent); font-size: .5625rem; font-weight: 700; }
.work-card-labels { min-height: 20px; display: flex; flex-wrap: wrap; gap: 5px; }
.work-card-label { max-width: 100%; display: inline-flex; align-items: center; min-height: 20px; padding: 1px 7px; overflow: hidden; border: 1px solid var(--border); border-radius: 999px; color: var(--muted); font-size: .625rem; font-weight: 700; line-height: 1; text-overflow: ellipsis; white-space: nowrap; }
.work-card-label-campaign { border-color: color-mix(in srgb, var(--attention) 45%, var(--border)); background: color-mix(in srgb, var(--attention) 10%, var(--canvas)); color: color-mix(in srgb, var(--attention) 75%, var(--fg)); }
.work-card-label-role { border-color: color-mix(in srgb, var(--accent) 40%, var(--border)); background: color-mix(in srgb, var(--accent) 9%, var(--canvas)); color: var(--accent); text-transform: capitalize; }
.work-card dl { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 8px; margin: 0; }
.work-card dt { color: var(--muted); font-size: .6875rem; font-weight: 600; }
.work-card dd { margin: 0; overflow: hidden; font-size: .75rem; font-weight: 600; text-overflow: ellipsis; white-space: nowrap; }
.work-tasks { --work-task-columns: 36px 224px 86px 74px 105px 115px 115px 145px; min-width: 0; display: grid; gap: 0; container-type: inline-size; overflow-x: auto; border: 1px solid var(--border); border-radius: 4px; background: var(--canvas); }
.work-task-viewbar { width: 100cqw; min-width: 0; min-height: 44px; display: flex; align-items: center; gap: 8px; position: sticky; z-index: 4; left: 0; padding: 6px 10px; border-bottom: 1px solid var(--border); background: var(--canvas-subtle); }
.work-task-view-name { display: flex; align-items: center; gap: 7px; margin-right: auto; }
.work-task-view-name strong { font-size: .75rem; }
.work-task-view-name > span:last-child { padding: 1px 6px; border-radius: 999px; background: var(--neutral-muted); color: var(--muted); font-size: .625rem; }
.work-task-view-icon, .work-task-sort-icon { display: grid; color: var(--muted); }
.work-task-settings, .work-task-settings-sheet, .work-task-settings-panel { display: contents; }
.work-task-sort-controls { display: flex; align-items: center; gap: 8px; }
.work-task-sort { min-height: 30px; display: flex; align-items: center; gap: 6px; padding: 0 4px 0 9px; border: 1px solid var(--border); border-radius: 6px; background: var(--canvas); color: var(--muted); font-size: .6875rem; }
.work-task-sort:focus-within { border-color: var(--accent); box-shadow: 0 0 0 2px var(--accent-muted); }
.work-task-sort select { border: 0; outline: 0; background: transparent; color: var(--fg); font: inherit; font-size: .6875rem; font-weight: 600; }
.work-task-sort-direction { width: 30px; height: 30px; display: grid; place-items: center; padding: 0; border: 1px solid var(--border); border-radius: 6px; background: var(--canvas); color: var(--muted); cursor: pointer; }
.work-task-sort-direction:hover { background: var(--neutral-muted); color: var(--fg); }
.work-task-scroll { min-width: 900px; max-height: max(420px, calc(100vh - 310px)); overflow-y: auto; }
.work-task-table-header { width: 900px; min-width: 900px; display: grid; grid-template-columns: var(--work-task-columns); position: sticky; z-index: 3; top: 0; border-bottom: 1px solid var(--border); background: var(--canvas-inset); }
.work-task-table-header > :is(span, button) { min-width: 0; min-height: 32px; display: flex; align-items: center; gap: 5px; padding: 7px 10px; border: 0; border-right: 1px solid var(--border); background: transparent; color: var(--muted); font: inherit; font-size: .6875rem; font-weight: 600; text-align: left; }
.work-task-table-header > :is(span, button):last-child { border-right: 0; }
.work-task-column-sort { cursor: pointer; }
.work-task-column-sort:hover { background: var(--neutral-muted); color: var(--fg); }
.work-task-header-sort-icon { width: 10px; height: 10px; margin-left: auto; opacity: .55; }
.work-roadmap { min-width: 0; display: grid; gap: 0; overflow: hidden; border: 1px solid var(--border); border-radius: 4px; background: var(--canvas); }
.work-roadmap-body { min-width: 0; }
.work-roadmap-toolbar { min-height: 44px; display: flex; align-items: center; gap: 8px; padding: 6px 10px; border-bottom: 1px solid var(--border); background: var(--canvas-subtle); }
.work-roadmap-toolbar > div { min-width: 0; display: flex; align-items: center; gap: 7px; margin-right: auto; }
.work-roadmap-toolbar strong { font-size: .75rem; }
.work-roadmap-toolbar > div > span:last-child { padding: 1px 6px; border-radius: 999px; background: var(--neutral-muted); color: var(--muted); font-size: .625rem; }
.work-roadmap-toolbar-icon { display: grid; color: var(--muted); }
.work-roadmap-zoom { position: relative; z-index: 20; }
.work-roadmap-zoom summary { min-height: 30px; display: flex; align-items: center; gap: 6px; padding: 0 9px; border: 1px solid var(--border); border-radius: 6px; background: var(--canvas); color: var(--fg); font-size: .6875rem; font-weight: 600; cursor: pointer; list-style: none; }
.work-roadmap-zoom summary::-webkit-details-marker { display: none; }
.work-roadmap-zoom summary:hover, .work-roadmap-zoom[open] summary { background: var(--neutral-muted); }
.work-roadmap-zoom summary:focus-visible { outline: 2px solid var(--accent); outline-offset: 2px; }
.work-roadmap-zoom-icon { width: 14px; height: 14px; color: var(--muted); }
.work-roadmap-zoom-popover { width: 194px; display: grid; gap: 8px; position: absolute; top: calc(100% + 7px); right: 0; padding: 12px 8px 8px; border: 1px solid var(--border); border-radius: 8px; background: var(--canvas-overlay, var(--canvas)); box-shadow: 0 8px 24px color-mix(in srgb, var(--canvas-inset) 45%, transparent); }
.work-roadmap-zoom-popover > strong { padding: 0 8px; color: var(--muted); font-size: .6875rem; font-weight: 600; }
.work-roadmap-zoom-menu { display: grid; }
.work-roadmap-zoom-menu button { min-height: 34px; display: grid; grid-template-columns: 18px minmax(0, 1fr); align-items: center; gap: 7px; padding: 0 8px; border: 0; border-radius: 5px; background: transparent; color: var(--fg); font: inherit; font-size: .75rem; font-weight: 600; text-align: left; cursor: pointer; }
.work-roadmap-zoom-menu button:hover, .work-roadmap-zoom-menu button:focus-visible { background: var(--neutral-muted); outline: 0; }
.work-roadmap-zoom-check { width: 14px; height: 14px; visibility: hidden; }
.work-roadmap-zoom-menu button[aria-checked="true"] .work-roadmap-zoom-check { visibility: visible; }
.work-roadmap-today-button { min-height: 30px; padding: 0 10px; border: 1px solid var(--border); border-radius: 6px; background: var(--canvas); color: var(--fg); font: inherit; font-size: .6875rem; font-weight: 600; cursor: pointer; }
.work-roadmap-today-button:hover { background: var(--neutral-muted); }
.work-project-section-heading { min-height: 28px; display: flex; align-items: center; justify-content: space-between; gap: 12px; }
.work-project-section-heading > span { color: var(--muted); font-size: .75rem; }
.work-task-list { width: 900px; min-width: 900px; display: grid; align-content: start; gap: 0; }
.work-roadmap-lanes { display: grid; gap: 0; border: 1px solid var(--border); border-radius: 8px; overflow: hidden; }
.work-task-list { counter-reset: work-task-index; }
.work-task-row, .work-task-group-summary { width: 900px; min-width: 900px; min-height: 40px; display: grid; grid-template-columns: var(--work-task-columns); align-items: stretch; gap: 0; padding: 0; border-top: 1px solid var(--border); background: var(--canvas); counter-increment: work-task-index; }
.work-task-row::before, .work-task-group-summary::before { content: counter(work-task-index); display: flex; align-items: center; justify-content: center; border-right: 1px solid var(--border); color: var(--muted); font-size: .6875rem; font-variant-numeric: tabular-nums; }
.work-task-row > *, .work-task-group-summary > * { min-width: 0; display: flex; align-items: center; padding: 6px 10px; overflow: hidden; border-right: 1px solid var(--border); font-size: .6875rem; }
.work-task-row > *:last-child, .work-task-group-summary > *:last-child { border-right: 0; }
.work-task-row:first-child, .work-roadmap-lane:first-child { border-top: 0; }
.work-task-group:first-child > .work-task-group-summary { border-top: 0; }
.work-task-group-summary { cursor: pointer; list-style: none; }
.work-task-group-summary::-webkit-details-marker { display: none; }
.work-task-row:hover, .work-task-group-summary:hover { background: var(--canvas-subtle); }
.work-task-group-items .work-task-row { background: var(--canvas-subtle); }
.work-task-group-items .work-task-main { padding-left: 28px; }
.work-group-chevron { display: grid; color: var(--muted); transition: transform 120ms ease; }
.work-group-chevron-placeholder { width: 16px; height: 16px; flex: 0 0 16px; }
.work-task-group[open] .work-group-chevron { transform: rotate(90deg); }
.work-task-main { min-width: 0; display: flex; align-items: center; gap: 7px; }
.work-task-title { min-width: 0; display: grid; gap: 1px; }
.work-task-title :is(strong, small), .work-task-repository > span:last-child, .work-task-row time, .work-task-end { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.work-task-title small, .work-task-owner, .work-task-row time, .work-task-end, .work-roadmap-time { color: var(--muted); font-size: .6875rem; }
.work-task-icon { width: 18px; height: 18px; display: grid; place-items: center; flex: 0 0 18px; color: var(--success); }
.work-task-icon .octicon { width: 13px; height: 13px; }
.work-task-type { color: var(--muted); text-transform: capitalize; }
.work-task-status-cell .work-state { min-height: 18px; flex: none; padding: 1px 6px; font-size: .625rem; }
.work-task-labels { gap: 4px; }
.work-task-labels .work-card-label { min-height: 18px; }
.work-task-owner a, .work-task-repository { min-width: 0; display: flex; align-items: center; gap: 6px; color: inherit; text-decoration: none; }
.work-task-owner a:hover span:last-child { color: var(--accent); text-decoration: underline; }
.work-task-repository-icon { width: 14px; height: 14px; display: grid; flex: 0 0 14px; color: var(--muted); }
.work-task-repository-icon .octicon { width: 14px; height: 14px; }
.work-state { display: inline-flex; align-items: center; justify-content: center; min-height: 22px; padding: 2px 8px; border-radius: 999px; background: var(--neutral-muted); color: var(--fg); font-size: .6875rem; font-weight: 700; text-transform: capitalize; }
.work-state-todo, .work-roadmap-bar.work-state-todo { background: var(--neutral-muted); color: var(--muted); }
.work-state-in-progress, .work-roadmap-bar.work-state-in-progress { background: var(--success-muted); color: var(--success); }
.work-state-needs-review, .work-roadmap-bar.work-state-needs-review { background: var(--attention-muted); color: var(--attention); }
.work-state-done, .work-roadmap-bar.work-state-done { background: var(--accent-muted); color: var(--accent); }
.work-roadmap-scroll { max-height: max(420px, calc(100vh - 280px)); overflow: auto; background: var(--canvas); }
.work-roadmap-timeline { min-width: calc(320px + var(--roadmap-grid-width)); display: grid; position: relative; }
.work-roadmap-calendar, .work-roadmap-lane { display: grid; grid-template-columns: 320px var(--roadmap-grid-width); }
.work-roadmap-group { min-width: calc(300px + var(--roadmap-grid-width)); }
.work-roadmap-group > summary { cursor: pointer; list-style: none; }
.work-roadmap-group > summary::-webkit-details-marker { display: none; }
.work-roadmap-group > .work-roadmap-lane:not(summary) .work-roadmap-label { padding-left: 24px; background: var(--canvas-subtle); }
.work-roadmap-group-summary .work-roadmap-index { font-size: 1rem; transition: transform 120ms ease; }
.work-roadmap-group[open] > .work-roadmap-group-summary .work-roadmap-index { transform: rotate(90deg); }
.work-roadmap-calendar { min-height: 88px; position: sticky; z-index: 6; top: 0; border-bottom: 1px solid var(--border); background: var(--canvas); }
.work-roadmap-corner { display: flex; align-items: end; position: sticky; z-index: 8; left: 0; padding: 0 14px 10px; border-right: 1px solid var(--border); background: var(--canvas-inset); color: var(--muted); font-size: .6875rem; font-weight: 600; }
.work-roadmap-calendar-grid { min-width: 0; position: relative; overflow: hidden; background: var(--canvas-subtle); }
.work-roadmap-quarters, .work-roadmap-periods, .work-roadmap-ticks { position: absolute; inset-inline: 0; }
.work-roadmap-quarters { height: 28px; top: 0; border-bottom: 1px solid var(--border); }
.work-roadmap-periods { height: 30px; top: 28px; border-bottom: 1px solid var(--border); }
.work-roadmap-quarters span, .work-roadmap-periods span { position: absolute; left: var(--period-start); width: var(--period-width); padding: 7px 8px 0; overflow: hidden; border-left: 1px solid var(--border); font-size: .6875rem; font-weight: 600; text-overflow: ellipsis; white-space: nowrap; }
.work-roadmap-quarters span { color: var(--success); }
.work-roadmap-periods span { color: var(--muted); }
.work-roadmap-ticks { height: 30px; top: 58px; background-image: linear-gradient(to right, var(--border-muted) 1px, transparent 1px); background-size: calc(100% / var(--roadmap-divisions)) 100%; }
.work-roadmap-ticks time { position: absolute; left: var(--tick-position); padding: 8px 0 0 6px; color: var(--muted); font-size: .75rem; font-variant-numeric: tabular-nums; white-space: nowrap; transform: translateX(-1px); }
.work-roadmap-lane { min-height: 40px; border-top: 1px solid var(--border); }
.work-roadmap-calendar + .work-roadmap-lane { border-top: 0; }
.work-roadmap-label { min-width: 0; display: grid; grid-template-columns: 30px 20px minmax(0, 1fr); align-items: center; gap: 7px; position: sticky; z-index: 4; left: 0; padding: 5px 10px 5px 4px; border-right: 1px solid var(--border); background: var(--canvas); }
.work-roadmap-index { color: var(--muted); font-size: .6875rem; font-variant-numeric: tabular-nums; text-align: right; }
.work-roadmap-label .work-avatar { width: 20px; height: 20px; flex-basis: 20px; border: 0; background: transparent; }
.work-roadmap-label-copy { min-width: 0; display: grid; gap: 1px; }
.work-roadmap-label-copy small { overflow: hidden; color: var(--muted); font-size: .75rem; text-overflow: ellipsis; white-space: nowrap; }
.work-roadmap-track { min-width: 0; position: relative; overflow: hidden; background-color: var(--canvas); background-image: linear-gradient(to right, var(--border) 1px, transparent 1px); background-size: calc(100% / var(--roadmap-divisions)) 100%; }
.work-roadmap-bar { height: 26px; min-width: 44px; display: flex; align-items: center; gap: 6px; position: absolute; z-index: 2; top: 7px; left: var(--work-start); width: var(--work-width); max-width: calc(100% - var(--work-start)); padding: 0 7px; overflow: hidden; border: 1px solid var(--border); border-radius: 4px; background: var(--canvas); color: var(--fg); box-shadow: 0 1px 2px color-mix(in srgb, var(--fg) 12%, transparent); }
.work-roadmap-bar.work-roadmap-point { width: max-content; min-width: 28px; max-width: min(280px, calc(100% - var(--work-start))); padding: 0 7px 0 4px; transform: translateX(-10px); }
.work-roadmap-primitive { width: 18px; height: 18px; display: inline-grid; place-items: center; flex: 0 0 18px; border: 1px solid var(--accent); border-radius: 50%; background: var(--canvas); color: var(--success); }
.work-roadmap-primitive-icon { width: 11px; height: 11px; }
.work-roadmap-primitive-icon .octicon { width: 11px; height: 11px; }
.work-roadmap-bar-title { min-width: 0; flex: 1; overflow: hidden; font-size: .6875rem; text-overflow: ellipsis; white-space: nowrap; }
.work-roadmap-actor { min-width: 0; display: inline-flex; align-items: center; gap: 4px; margin-left: auto; }
.work-roadmap-avatar { width: 18px; height: 18px; display: inline-grid; place-items: center; flex: 0 0 18px; border: 1px solid var(--border); border-radius: 50%; background: var(--neutral-muted); color: var(--fg); font-size: .5625rem; font-weight: 700; }
.work-roadmap-owner { font-size: .6875rem; }
.work-roadmap-owner { min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-weight: 600; }
.work-roadmap-end { width: 7px; height: 7px; position: absolute; z-index: 3; top: 18px; left: var(--work-stop); border: 2px solid var(--canvas); border-radius: 50%; transform: translate(-50%, -50%); }
.work-roadmap-today { width: 1px; position: absolute; z-index: 7; top: 0; bottom: 0; left: calc(320px + var(--today-position)); pointer-events: none; }
.work-roadmap-today::before { width: 8px; height: 8px; position: absolute; top: 84px; left: -3px; border-radius: 50%; background: var(--accent); content: ""; }
.work-roadmap-today::after { width: 1px; position: absolute; top: 88px; bottom: 0; left: 0; background: var(--accent); content: ""; }
.work-mobile-detail:not([open]) { display: none; }
.signal-clear { min-height: 68px; display: grid; grid-template-columns: 20px minmax(0, 1fr); align-items: center; gap: 10px; padding: 9px 14px; }
.signal-clear .signal-icon { color: var(--success); }
.managed-campaigns > header { min-height: 72px; padding: 10px 0; }
.managed-campaign-list { display: grid; gap: 10px; }
.managed-campaign-card { overflow: hidden; border: 1px solid var(--border); border-radius: 0; background: var(--canvas); }
.managed-campaign-card > header { min-height: 48px; display: flex; align-items: center; justify-content: space-between; gap: 12px; padding: 10px 14px; }
.managed-campaign-card > header > div { min-width: 0; display: flex; align-items: center; gap: 9px; }
.managed-campaign-card h4 { margin: 0; overflow: hidden; font-size: 1rem; text-overflow: ellipsis; white-space: nowrap; }
.managed-campaign-icon { display: grid; color: var(--fg); }
.managed-campaign-card .mode-badge { gap: 4px; padding-inline: 10px; font-size: .75rem; }
.managed-campaign-card dl { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 16px; margin: 0; padding: 8px 14px 14px; }
.managed-campaign-card dt { color: var(--muted); font-size: .75rem; }
.managed-campaign-card dd { margin: 2px 0 0; overflow: hidden; font-size: .875rem; font-weight: 600; text-overflow: ellipsis; white-space: nowrap; }
.managed-campaign-card .inventory-ready { color: var(--success); }
.managed-campaign-card .inventory-attention { color: var(--attention); }
.overview-content > .layout-section { padding: 0; border: 0; background: transparent; }
.overview-content > .layout-section > .layout-section-header { margin: 0 0 12px; padding-top: 20px; border-top: 1px solid var(--border); }
.table-region { overflow-x: auto; border: 1px solid var(--border); border-radius: 6px; margin: 12px 0 20px; background: var(--canvas); }
.table-scroll { max-height: 60vh; overflow: auto; overscroll-behavior: contain; }
.table-region[data-lazy-list] .table-scroll { overscroll-behavior-x: none; }
.table-region-static .table-scroll, .table-region-expanded .table-scroll { max-height: none; overflow: visible; overscroll-behavior: auto; }
.table-scroll:focus-visible { outline: 2px solid var(--focus); outline-offset: -2px; }
.table-scroll thead { position: sticky; top: 0; z-index: 1; }
.table-sort { display: inline-flex; align-items: center; gap: 4px; width: 100%; padding: 0; border: 0; background: none; color: inherit; font: inherit; text-align: left; cursor: pointer; }
.table-sort::after { content: "↕"; color: var(--muted); font-size: .6875rem; opacity: .5; }
.table-sort:hover { color: var(--fg); }
th[aria-sort="ascending"] .table-sort::after { content: "↑"; opacity: 1; }
th[aria-sort="descending"] .table-sort::after { content: "↓"; opacity: 1; }
.table-filter { min-width: 600px; display: flex; align-items: center; gap: 12px; padding: 6px 14px; border-bottom: 1px solid var(--border); background: var(--canvas-subtle); }
.table-filter label { min-width: 160px; flex: 1; }
.table-filter input { width: 100%; min-height: 32px; padding: 4px 9px; border: 1px solid var(--border); border-radius: 6px; background: var(--canvas); color: var(--fg); font: inherit; }
.table-filter :is(input, select):focus-visible { outline: 2px solid var(--focus); outline-offset: -1px; }
.table-filter-result { flex: none; color: var(--muted); font-size: .75rem; }
.table-filter-heading { padding-block: 6px; white-space: nowrap; }
.table-filter-heading .table-sort-icon { margin-left: 4px; vertical-align: middle; }
.filter-select-control { min-width: 0; max-width: 100%; display: inline-grid; grid-template-areas: "control"; align-items: center; border: 1px solid var(--border); border-radius: 6px; background: var(--canvas); box-shadow: 0 1px 0 color-mix(in srgb, var(--fg) 5%, transparent); color: var(--muted); }
.filter-select-control::after { width: 5px; height: 5px; grid-area: control; justify-self: end; margin: 0 10px 3px 0; border-right: 1.5px solid currentColor; border-bottom: 1.5px solid currentColor; content: ""; pointer-events: none; transform: rotate(45deg); }
.filter-select-control:hover { border-color: color-mix(in srgb, var(--fg) 30%, var(--border)); background: var(--canvas-subtle); color: var(--fg); }
.filter-select-control:focus-within { outline: 2px solid var(--focus); outline-offset: 1px; }
.filter-select-control-input { min-width: 0; max-width: 100%; grid-area: control; padding: 3px 26px 3px 10px; appearance: none; border: 0; border-radius: inherit; background: transparent; color: inherit; font: inherit; font-weight: 600; cursor: pointer; }
.filter-select-control-input option { background: var(--canvas); color: var(--fg); }
.filter-select-control-input:focus-visible { outline: 0; }
.table-sort-icon { width: 22px; min-width: 22px; height: 24px; justify-content: center; }
.table-sort-icon::after { margin: 0; }
.table-filter-more { min-height: 32px; margin: 10px 14px; padding: 5px 12px; border: 1px solid var(--border); border-radius: 6px; background: var(--canvas-subtle); color: var(--fg); font: inherit; font-size: .75rem; font-weight: 600; cursor: pointer; }
.table-filter-more:hover { background: var(--neutral-muted); }
.table-empty-action { min-height: 28px; margin-inline-start: 10px; padding: 4px 10px; border: 1px solid var(--border); border-radius: 6px; background: var(--canvas-subtle); color: var(--fg); font: inherit; font-size: .75rem; font-weight: 600; cursor: pointer; }
.table-empty-action:hover { background: var(--neutral-muted); }
`;
