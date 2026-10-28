export const workflowValueStyles = `
.workflow-value-report { max-width: 1000px; margin: 0 auto; color: var(--fg); }
.workflow-value-report h2 { font-size: 1.5rem; margin: 16px 0 8px; }
.workflow-value-report h3 { font-size: .875rem; margin: 0 0 8px; }
.workflow-value-report p { line-height: 1.5; }
.value-report-heading { margin-bottom: 24px; }
.value-report-heading > p:first-of-type { font-size: .75rem; margin-bottom: 24px; }
.value-report-trail { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 24px; border-top: 1px solid var(--border); padding-top: 20px; }
.value-report-trail > div > span { display: block; font-size: .8125rem; font-weight: 600; }
.value-report-trail button { padding: 8px 0; border: 0; background: none; color: var(--fg); text-align: left; cursor: pointer; font: inherit; }
.value-report-trail strong { font-size: 1.75rem; border-bottom: 1px dotted var(--muted); }
.value-report-trail p { font-size: .75rem; margin: 0; }
.value-report-scope { font-size: .75rem; padding-bottom: 16px; }
.value-report-outcome { padding: 20px; border: 1px solid var(--border); border-radius: 6px 6px 0 0; }
.value-report-outcome h3 { display: flex; gap: 8px; align-items: center; }
.value-report-icon { display: inline-flex; color: var(--muted); }
.value-report-outcome-body { display: grid; grid-template-columns: minmax(0, 1fr) minmax(0, 1.5fr); gap: 24px; align-items: center; }
.value-report-number { display: inline-block; font-size: 3.5rem; font-weight: 600; color: var(--fg); text-decoration: none; }
.value-report-number > span { font-size: .875rem; font-weight: 400; color: var(--muted); }
.value-report-movement { font-size: .875rem; font-weight: 600; }
.value-report-movement[data-movement="Worse"] { color: var(--attention); }
.value-report-movement[data-movement="Better"] { color: var(--success); }
.value-report-outcome-body > div > .muted { font-size: .75rem; }
.value-report-plot svg { display: block; width: 100%; overflow: visible; }
.value-report-plot circle { fill: var(--accent); stroke: var(--canvas); stroke-width: 2; }
.value-snapshot-guide { stroke: var(--muted); stroke-width: 1.2; stroke-dasharray: 4 4; vector-effect: non-scaling-stroke; pointer-events: none; }
.value-snapshot-direction, .value-snapshot-label { fill: var(--fg); font-size: 14px; font-weight: 600; }
.value-snapshot-date { fill: var(--muted); font-size: 13px; }
.value-report-plot > p { font-size: .6875rem; text-align: right; }
.value-report-recent { border: 1px solid var(--border); border-top: 0; padding: 16px 20px; border-radius: 0 0 6px 6px; display: flex; align-items: center; justify-content: space-between; gap: 16px; }
.value-report-recent h3, .value-report-recent p { margin: 0; }
.value-report-quality { border: 0; background: none; color: var(--muted); text-decoration: underline; cursor: pointer; padding: 14px 0; font-size: .75rem; }
.value-report-evidence { margin-top: 8px; overflow: hidden; border: 1px solid var(--border); border-radius: 6px; }
.value-report-evidence > summary, .value-report-evidence-category > summary { box-sizing: border-box; min-height: 44px; cursor: pointer; }
.value-report-evidence > summary { padding: 12px 16px; color: var(--accent); font-weight: 600; }
.value-report-evidence[open] > summary { border-bottom: 1px solid var(--border); }
.value-report-evidence-count { float: right; margin-left: 12px; color: var(--muted); font-size: .75rem; font-weight: 400; }
.value-report-evidence-intro { margin: 0; padding: 16px; font-size: .8125rem; }
.value-report-evidence-category { border-top: 1px solid var(--border); scroll-margin-top: 24px; }
.value-report-evidence-category > summary { padding: 12px 16px; font-size: .875rem; font-weight: 600; }
.value-report-evidence-category[open] > summary { background: var(--canvas-subtle); }
.value-report-evidence-content { padding: 0 16px 16px; }
.value-report-evidence-content > :first-child { margin-top: 12px; }
.value-report-table-region { margin: 12px 0; }
.value-report-table-region table { width: 100%; border-collapse: collapse; font-size: .8125rem; }
.value-report-table-region th, .value-report-table-region td { padding: 8px; border-bottom: 1px solid var(--border); text-align: left; vertical-align: top; }
.value-report-table-region th { background: var(--canvas-subtle); }
.value-report-table-region a, .value-report-items a { overflow-wrap: anywhere; }
.value-report-evidence summary:focus-visible, .workflow-value-report button:focus-visible, .workflow-value-report a:focus-visible { outline: 2px solid var(--focus); outline-offset: 3px; }
@media (max-width: 700px) {
  .value-report-trail { gap: 12px; }
  .value-report-trail button { font-size: .75rem; min-height: 44px; }
  .value-report-outcome-body { grid-template-columns: minmax(0, 1fr); gap: 12px; }
  .value-report-recent { display: block; }
  .value-report-recent p { margin-top: 8px; }
  .value-report-evidence .table-filter { min-width: 0; flex-direction: column; align-items: stretch; gap: 6px; }
  .value-report-evidence .table-filter label { min-width: 0; width: 100%; }
  .value-report-evidence .table-filter-result { align-self: flex-end; }
  .value-snapshot-direction, .value-snapshot-label, .value-snapshot-date { font-size: 24px; }
}
`;
