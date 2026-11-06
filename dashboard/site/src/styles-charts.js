export const chartStyles = `.chart-widget { min-height: 230px; display: grid; place-items: center; margin: 12px 0; border: 0; background: transparent; }
.chart-facet-grid { display: grid; gap: 16px; min-width: 0; }
.chart-facet-wrap { grid-template-columns: repeat(var(--chart-facet-columns), minmax(0, 1fr)); }
.chart-facet-matrix { grid-auto-columns: minmax(240px, 1fr); overflow-x: auto; }
.chart-facet-panel { min-width: 0; margin: 0; padding: 12px; border: 1px solid var(--border-muted); border-radius: 6px; background: var(--canvas); }
.chart-facet-matrix .chart-facet-panel { grid-row: var(--chart-facet-row); grid-column: var(--chart-facet-column); }
.chart-facet-header { margin-bottom: 8px; color: var(--fg); font-size: .8125rem; font-weight: 600; overflow-wrap: anywhere; }
.chart-facet-content { min-width: 0; }
@media (max-width: 600px) {
  .chart-facet-grid { grid-template-columns: minmax(0, 1fr); grid-auto-columns: auto; overflow-x: visible; }
  .chart-facet-matrix .chart-facet-panel { grid-row: auto; grid-column: auto; }
}
.chart-clustering-progress { min-height: 230px; display: grid; place-content: center; justify-items: center; gap: 10px; margin: 12px 0; color: var(--muted); font-size: .8125rem; }
.chart-clustering-progress progress { width: min(240px, 70vw); }
.chart-widget svg { width: min(100%, 420px); max-height: 220px; overflow: visible; }
.area-chart-widget, .line-chart-widget, .dot-chart-widget, .scatter-chart-widget { min-width: 0; overflow: hidden; }
.area-chart-widget svg, .line-chart-widget svg, .dot-chart-widget svg, .scatter-chart-widget svg { width: 100%; max-height: none; }
.line-chart-plot { position: relative; width: 100%; }
.line-chart-plot svg { display: block; }
.layer-chart-widget { min-width: 0; overflow: hidden; }
.layer-chart-widget svg { width: 100%; max-height: none; }
.layer-chart-widget .chart-axis { width: calc(100% - var(--line-chart-left)); margin-left: var(--line-chart-left); }
.layer-chart-scale-key { display: flex; flex-wrap: wrap; gap: 8px 16px; color: var(--muted); font-size: .6875rem; font-variant-numeric: tabular-nums; }
.line-chart-y-labels { position: absolute; inset: 0; pointer-events: none; color: var(--muted); font-size: .6875rem; font-variant-numeric: tabular-nums; }
.line-chart-y-labels span { position: absolute; right: calc(100% - var(--line-chart-left) + 1.5%); transform: translateY(-50%); white-space: nowrap; }
.line-chart-y-labels span:first-child { top: 9.5238%; }
.line-chart-y-labels span:nth-child(2) { top: 50%; }
.line-chart-y-labels span:last-child { top: 90.4762%; }
.pie-chart-track { stroke: var(--border-muted); }
.pie-chart-segment { stroke: var(--accent); }
.pie-chart-total-value { fill: var(--fg); font-size: 5px; font-weight: 700; }
.pie-chart-total-label { fill: var(--muted); font-size: 2.75px; text-transform: uppercase; letter-spacing: .04em; }
.chart-legend { display: flex; flex-wrap: wrap; gap: 12px; margin: 8px 0 12px; padding: 0; list-style: none; color: var(--muted); font-size: .75rem; }
.chart-legend li { display: inline-flex; align-items: center; gap: 6px; }
.chart-legend i { width: 18px; height: 0; border-top-width: 2px; border-top-style: solid; }
.chart-legend-scatter i.chart-grid-key { border-color: var(--border); border-top-style: dashed; }
.scatter-size-key { color: var(--muted); font-size: .75rem; }
.chart-legend-size svg { width: 32px; height: 32px; flex: none; }
.chart-legend-size circle { fill: var(--canvas); stroke: var(--muted); stroke-width: .5; }
.chart-legend-dot i { width: 10px; height: 10px; border: 0; border-radius: 50%; background: currentColor; }
.chart-legend-bar i, .chart-legend-horizontal-bar i, .chart-legend-pie i { height: 10px; border-top-width: 0; border-radius: 999px; background: currentColor; }
.chart-legend-pie strong { color: var(--fg); font-variant-numeric: tabular-nums; }
.chart-legend-pie small { color: var(--muted); }
.chart-axis { display: flex; justify-content: space-between; margin-top: 4px; color: var(--muted); font-size: .6875rem; }
.area-chart-widget .chart-axis, .line-chart-widget .chart-axis, .dot-chart-widget .chart-axis, .scatter-chart-widget .chart-axis { width: calc(100% - var(--line-chart-left) - var(--line-chart-right, 0%)); margin-left: var(--line-chart-left); }
.timeline-chart-axis { position: relative; width: 100%; margin: -12px 0 12px; padding-top: 9px; border-top: 1px solid var(--border); font-variant-numeric: tabular-nums; }
.timeline-chart-axis span { position: relative; white-space: nowrap; }
.timeline-chart-axis span::before { position: absolute; top: -10px; left: 50%; width: 1px; height: 5px; background: var(--border); content: ""; }
.timeline-chart-axis span:first-child::before { left: 0; }
.timeline-chart-axis span:last-child::before { right: 0; left: auto; }
.histogram-chart-widget { grid-template-rows: minmax(0, 1fr) auto; align-content: center; padding: 16px 18px 12px; }
.histogram-chart-widget svg, .histogram-chart-widget .chart-axis { width: min(100%, 420px); }
.histogram-chart-widget .chart-axis { box-sizing: border-box; margin-top: 0; padding: 6px 0 0 9%; border-top: 1px solid var(--border-muted); font-variant-numeric: tabular-nums; }
.histogram-chart-y-label { fill: var(--muted); font-size: 2.5px; font-variant-numeric: tabular-nums; }
.heatmap-chart-widget { min-width: 0; padding: 16px; place-items: stretch; }
.heatmap-scroll-region { overflow-x: auto; border-radius: 6px; }
.heatmap-scroll-region:focus-visible { outline: 2px solid var(--focus); outline-offset: 2px; }
.chart-widget svg.heatmap-chart { width: max(100%, calc((var(--heatmap-columns) * 72px) + 150px)); max-height: none; }
.heatmap-axis-label { fill: var(--muted); font-size: 3px; font-weight: 600; }
.heatmap-cell rect { fill: color-mix(in srgb, var(--accent) var(--heatmap-intensity, 18%), var(--canvas)); stroke: color-mix(in srgb, var(--accent) 42%, var(--border)); stroke-width: .5; }
.heatmap-cell text { fill: var(--fg); font-size: 3px; font-weight: 600; font-variant-numeric: tabular-nums; pointer-events: none; }
.heatmap-cell-empty rect { fill: var(--canvas-subtle); stroke: var(--border-muted); }
.heatmap-cell-empty text { fill: var(--muted); font-weight: 400; }
.heatmap-cell:focus-visible { outline: none; }
.heatmap-cell:focus-visible rect { stroke: var(--focus); stroke-width: 1; }
.treemap-chart-widget { display: block; min-width: 0; min-height: 0; }
.treemap-plot { position: relative; width: 100%; aspect-ratio: 5 / 3; isolation: isolate; }
.treemap-group, .treemap-tile { position: absolute; left: var(--treemap-x); top: var(--treemap-y); width: var(--treemap-width); height: var(--treemap-height); box-sizing: border-box; }
.treemap-group { overflow: hidden; }
.treemap-tile { display: block; }
.treemap-tile:hover, .treemap-tile:focus-within { z-index: 2; }
.treemap-tile .treemap-tooltip { font-size: .8125rem; overflow-wrap: anywhere; pointer-events: auto; }
.treemap-tooltip::before { position: absolute; inset: -10px 0; z-index: -1; content: ""; }
.treemap-tile.treemap-tooltip-dismissed .treemap-tooltip { visibility: hidden; opacity: 0; pointer-events: none; }
.treemap-group { border: 1px solid var(--border); background: var(--canvas-subtle); }
.treemap-group-label { display: block; padding: 2px 6px; color: var(--muted); font-size: .6875rem; font-weight: 600; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.treemap-leaf { display: grid; place-items: center; width: 100%; height: 100%; padding: 0; box-sizing: border-box; overflow: hidden; border: 1px solid color-mix(in srgb, var(--treemap-color) 60%, var(--border)); border-radius: 0; background: color-mix(in srgb, var(--treemap-color) 18%, var(--canvas)); color: var(--fg); font: inherit; text-decoration: none; cursor: pointer; }
.treemap-leaf-content { display: grid; gap: 2px; max-width: 100%; padding: 3px; box-sizing: border-box; text-align: center; pointer-events: none; }
.treemap-leaf-label { display: block; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-size: .75rem; font-weight: 600; }
.treemap-leaf-value { font-size: .6875rem; font-variant-numeric: tabular-nums; }
.treemap-leaf-small .treemap-leaf-content { visibility: hidden; }
.treemap-leaf:hover { background: color-mix(in srgb, var(--treemap-color) 28%, var(--canvas)); }
.treemap-leaf:focus-visible { z-index: 1; outline: 2px solid var(--focus); outline-offset: -2px; }
.treemap-leaf.chart-series-1 { --treemap-color: var(--success); }
.treemap-leaf.chart-series-2 { --treemap-color: var(--attention); }
.treemap-leaf.chart-series-3 { --treemap-color: var(--danger); }
.treemap-leaf.chart-series-4 { --treemap-color: var(--accent); }
.treemap-leaf.chart-series-5 { --treemap-color: var(--muted); }
.treemap-leaf.chart-series-6 { --treemap-color: var(--purple); }
.treemap-leaf.chart-series-7 { --treemap-color: var(--pink); }
.treemap-leaf.chart-series-8 { --treemap-color: var(--coral); }
.treemap-leaf.chart-series-9 { --treemap-color: var(--yellow); }
.treemap-leaf.chart-series-10 { --treemap-color: var(--cyan); }
.treemap-leaf.chart-series-11 { --treemap-color: var(--lime); }
.treemap-leaf.chart-series-12 { --treemap-color: var(--violet); }
.treemap-leaf.chart-series-semantic-failure { --treemap-color: var(--danger); }
.treemap-leaf.chart-series-semantic-success, .treemap-leaf.chart-series-semantic-waiting { --treemap-color: var(--accent); }
.treemap-leaf.chart-series-semantic-attention { --treemap-color: var(--attention); }
.treemap-leaf.chart-series-semantic-neutral { --treemap-color: var(--muted); }
.chart-legend-treemap i { height: 10px; border-top-width: 0; background: currentColor; }
.horizontal-bar-chart-widget { min-width: 0; max-height: 560px; place-items: stretch; overflow-y: auto; padding: 8px 4px; }
.horizontal-bar-chart-list { display: grid; gap: 6px; width: 100%; margin: 0; padding: 0; list-style: none; }
.horizontal-bar-chart-section { display: grid; gap: 6px; min-width: 0; list-style: none; }
.horizontal-bar-chart-section + .horizontal-bar-chart-section { margin-top: 8px; padding-top: 12px; border-top: 1px solid var(--border-muted); }
.horizontal-bar-chart-section-title { margin: 0; color: var(--fg); font-size: .75rem; font-weight: 600; overflow-wrap: anywhere; }
.horizontal-bar-chart-section-list { display: grid; gap: 6px; min-width: 0; margin: 0; padding: 0; list-style: none; }
.horizontal-bar-chart-row { min-width: 0; display: grid; grid-template-columns: minmax(120px, 52%) minmax(48px, 1fr) auto; align-items: center; gap: 8px; }
.horizontal-bar-chart-label { overflow: hidden; color: var(--fg); font-size: .75rem; text-align: right; text-overflow: ellipsis; white-space: nowrap; }
.horizontal-bar-chart-label-text { display: block; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.horizontal-bar-chart-track { height: 14px; overflow: hidden; border-radius: 3px; background: var(--canvas-subtle); }
.horizontal-bar-chart-bar { display: block; width: var(--horizontal-bar-size); height: 100%; transform-origin: left center; background: var(--accent); }
.horizontal-bar-chart-value { min-width: 4ch; color: var(--muted); font-size: .6875rem; font-variant-numeric: tabular-nums; }
.chart-series-1 { stroke: var(--success); }
.chart-series-2 { stroke: var(--attention); }
.chart-series-3 { stroke: var(--danger); }
.chart-series-4 { stroke: var(--accent); }
.chart-series-5 { stroke: var(--muted); }
.chart-series-6 { stroke: var(--purple); }
.chart-series-7 { stroke: var(--pink); }
.chart-series-8 { stroke: var(--coral); }
.chart-series-9 { stroke: var(--yellow); }
.chart-series-10 { stroke: var(--cyan); }
.chart-series-11 { stroke: var(--lime); }
.chart-series-12 { stroke: var(--violet); }
.line-chart-axis { stroke: var(--border); stroke-width: 1; }
.line-chart-grid { stroke: var(--border-muted); stroke-width: .5; stroke-dasharray: 2 2; }
.histogram-chart-grid { stroke: var(--border-muted); stroke-width: .5; stroke-dasharray: 1.5 2; }
.line-chart-series { stroke: var(--accent); stroke-width: 2; vector-effect: non-scaling-stroke; }
.area-chart-area { stroke-width: 1; fill-opacity: .52; vector-effect: non-scaling-stroke; }
.area-chart-point { stroke-width: 5; stroke-linecap: round; vector-effect: non-scaling-stroke; }
.line-chart-point { stroke-width: var(--chart-point-size, 4px); stroke-linecap: round; vector-effect: non-scaling-stroke; }
.dot-chart-point, .scatter-chart-point { fill: var(--canvas); stroke-width: 2; vector-effect: non-scaling-stroke; }
.dot-chart-reference { stroke-width: 1; stroke-dasharray: 4 3; opacity: .72; vector-effect: non-scaling-stroke; }
.line-chart-window-band { fill: var(--accent); opacity: .055; }
.line-chart-temporal-marker line { stroke: var(--purple); stroke-width: 1.5; stroke-dasharray: 4 3; vector-effect: non-scaling-stroke; }
.line-chart-temporal-marker text { fill: var(--purple); font-size: 1.8px; font-weight: 600; }
.insights-temporal-plot-panel, .operational-value-native-plots { min-width: 0; width: 100%; }
.insights-temporal-plot-panel { overflow: hidden; }
.measure-history-operational-value { display: grid; gap: 16px; padding-bottom: 16px; }
.operational-value-native-plots { display: grid; grid-template-columns: repeat(auto-fit, minmax(min(100%, 360px), 1fr)); gap: 12px; }
.temporal-metric-plot { min-width: 0; width: 100%; overflow: hidden; padding: 16px; border: 1px solid var(--border); border-radius: 8px; background: var(--canvas); }
.temporal-plot-heading { min-width: 0; display: grid; grid-template-columns: minmax(0, 1fr) auto; align-items: start; gap: 12px; margin-bottom: 4px; }
.temporal-plot-heading-copy { min-width: 0; }
.temporal-plot-heading h3 { margin: 0; overflow-wrap: anywhere; color: var(--fg); font-size: .875rem; line-height: 1.25; text-wrap: balance; }
.temporal-plot-heading p { margin: 5px 0 0; overflow-wrap: anywhere; color: var(--muted); font-size: .75rem; line-height: 1.4; }
.temporal-plot-summary { min-width: 0; display: grid; justify-items: end; gap: 6px; }
.temporal-plot-current { display: flex; align-items: baseline; justify-content: flex-end; gap: 5px; margin: 0; color: var(--muted); font-size: .6875rem; font-variant-numeric: tabular-nums; }
.temporal-plot-current strong { color: var(--fg); font-size: 1.375rem; line-height: 1; }
.temporal-plot-trend { min-width: 0; max-width: 100%; display: inline-flex; align-items: center; gap: 5px; margin: 0; color: var(--muted); font-size: .6875rem; font-variant-numeric: tabular-nums; line-height: 1.25; }
.temporal-plot-trend strong { color: var(--fg); }
.temporal-plot-trend-arrow { display: inline-block; flex: none; font-size: .875rem; font-weight: 700; }
.temporal-plot-trend-improving, .temporal-plot-trend-improving strong,
.temporal-plot-trend-improving .temporal-plot-trend-arrow { color: var(--success); }
.temporal-plot-trend-worsening, .temporal-plot-trend-worsening strong,
.temporal-plot-trend-worsening .temporal-plot-trend-arrow { color: var(--danger); }
.temporal-plot-trend-stable .temporal-plot-trend-arrow,
.temporal-plot-trend-neutral .temporal-plot-trend-arrow { color: var(--muted); }
.temporal-plot-trend-interim, .temporal-plot-trend-interim strong,
.temporal-plot-trend-interim .temporal-plot-trend-arrow { color: var(--attention); }
.temporal-metric-plot svg { min-width: 0; max-width: 100%; display: block; width: 100%; height: auto; overflow: hidden; }
.temporal-plot-axis { fill: var(--muted); font-size: 19px; }
.temporal-plot-baseline { fill: var(--canvas-subtle); }
.temporal-plot-grid { stroke: var(--border); stroke-width: 1; }
.temporal-plot-adoption { stroke: var(--purple); stroke-width: 3; stroke-dasharray: 5 6; }
.temporal-plot-metric { stroke-width: 3; stroke-linejoin: round; stroke-linecap: round; vector-effect: non-scaling-stroke; }
.temporal-plot-point { stroke: var(--canvas); stroke-width: 2; vector-effect: non-scaling-stroke; }
.temporal-plot-chart { position: relative; }
.temporal-plot-point-tooltip { position: absolute; margin: -12px 0 0 -12px; }
.temporal-plot-point-trigger { width: 24px; height: 24px; padding: 0; border: 0; border-radius: 50%; background: transparent; cursor: pointer; }
.temporal-plot-point-trigger:focus-visible { outline: 2px solid var(--focus); outline-offset: 2px; }
.temporal-plot-point-tooltip:hover, .temporal-plot-point-tooltip:focus-within { z-index: 1; }
.temporal-plot-point-tooltip:hover .tooltip-content, .temporal-plot-point-tooltip:focus-within .tooltip-content { pointer-events: auto; }
.temporal-plot-point-tooltip .tooltip-content { font-size: .8125rem; }
.temporal-plot-point-tooltip .tooltip-content::before { position: absolute; inset: -10px 0; z-index: -1; content: ""; }
.temporal-plot-point-tooltip .tooltip-content a { color: var(--accent); }
.temporal-plot-run-outcome-track { stroke: var(--border); stroke-width: 12; stroke-linecap: round; opacity: .7; vector-effect: non-scaling-stroke; }
.temporal-plot-run-outcome-success, .temporal-plot-run-outcome-failure, .temporal-plot-run-outcome-failure-inset { stroke-width: 10; stroke-linecap: butt; vector-effect: non-scaling-stroke; }
.temporal-plot-run-outcome-success { stroke: var(--success); }
.temporal-plot-run-outcome-failure { stroke: var(--danger); }
.temporal-plot-run-outcome-failure-inset { stroke: var(--canvas); stroke-width: 4; }
.temporal-plot-run-outcome-legend { font-size: 17px; font-weight: 600; }
.temporal-plot-run-outcome-legend-label { fill: var(--muted); }
.temporal-plot-run-outcome-legend-success { fill: var(--success); }
.temporal-plot-run-outcome-legend-failure { fill: var(--danger); }
@media (min-width: 1440px) {
  .temporal-plot-axis { font-size: 10px; }
  .temporal-plot-run-outcome-legend { font-size: 10px; }
}
.temporal-metric-plot .chart-series-1 { fill: var(--accent); stroke: var(--accent); }
.temporal-metric-plot .chart-series-2 { fill: var(--success); stroke: var(--success); }
.temporal-metric-plot .chart-series-3 { fill: var(--attention); stroke: var(--attention); }
.temporal-metric-plot .chart-series-4 { fill: var(--danger); stroke: var(--danger); }
.temporal-metric-plot .chart-series-5 { fill: var(--cyan); stroke: var(--cyan); }
.temporal-metric-plot .chart-series-6 { fill: var(--pink); stroke: var(--pink); }
.temporal-metric-plot .chart-series-7 { fill: var(--coral); stroke: var(--coral); }
.temporal-metric-plot .chart-series-8 { fill: var(--yellow); stroke: var(--yellow); }
.temporal-metric-plot .chart-series-9 { fill: var(--lime); stroke: var(--lime); }
.temporal-metric-plot .chart-series-10 { fill: var(--violet); stroke: var(--violet); }
.temporal-metric-plot .chart-series-11 { fill: var(--muted); stroke: var(--muted); }
.temporal-metric-plot .chart-series-12 { fill: var(--fg); stroke: var(--fg); }
.temporal-metric-plot .temporal-plot-metric { fill: none; }
.temporal-metric-plot.temporal-metric-plot-provisional .temporal-plot-metric { stroke: var(--attention); stroke-dasharray: 8 6; }
.temporal-metric-plot.temporal-metric-plot-provisional .temporal-plot-point { fill: var(--attention-muted); stroke: var(--attention); stroke-width: 3; }
.temporal-plot-metric.temporal-plot-metric-provisional { stroke: var(--attention); stroke-dasharray: 8 6; }
.temporal-plot-point.temporal-plot-point-provisional { fill: var(--attention-muted); stroke: var(--attention); stroke-width: 3; }
.line-chart-context { opacity: .3; stroke-width: 1.1; }
.chart-point-context { opacity: .35; }
.line-chart-current { opacity: 1; stroke-width: 2; }
.chart-window-key { display: flex; justify-content: flex-end; gap: 14px; margin-top: 5px; color: var(--fg); font-size: .6875rem; }
.chart-window-key span, .chart-window-key strong { display: inline-flex; align-items: center; gap: 5px; font-weight: 600; }
.chart-window-key span::before, .chart-window-key strong::before { width: 14px; border-top: 2px solid var(--accent); content: ''; }
.chart-window-key span { color: var(--muted); }
.chart-window-key span::before { border-color: var(--muted); opacity: .55; }
.chart-point { cursor: crosshair; }
.pie-chart-segment { animation: pie-chart-entry 420ms ease-out both; animation-delay: calc(var(--chart-entry-index, 0) * 45ms); }
.area-chart-area { transform-box: fill-box; transform-origin: center bottom; animation: area-chart-entry 520ms ease-out both; animation-delay: calc(var(--chart-entry-index, 0) * 70ms); }
.line-chart-series { animation: line-chart-entry 600ms ease-out both; animation-delay: calc(var(--chart-entry-index, 0) * 70ms); }
.line-chart-series.line-chart-context { animation-name: line-chart-context-entry; }
.line-chart-point, .dot-chart-point, .scatter-chart-point { transform-box: fill-box; transform-origin: center; animation: line-chart-point-entry 280ms ease-out both; animation-delay: calc(180ms + var(--chart-entry-index, 0) * 35ms); }
.bar-chart-bar, .histogram-chart-bar, .table-summary-histogram rect { transform-box: fill-box; transform-origin: center bottom; animation: histogram-chart-entry 360ms ease-out both; animation-delay: calc(var(--chart-entry-index, 0) * 35ms); }
.bar-chart-bar.horizontal-bar-chart-bar { transform-origin: left center; animation-name: horizontal-bar-chart-entry; }
.pie-chart-segment { transition: opacity 120ms ease, filter 120ms ease; }
.pie-chart-mark:hover .pie-chart-segment, .pie-chart-mark:focus-visible .pie-chart-segment { filter: brightness(1.08); opacity: .82; }
.point-tooltip { opacity: 0; pointer-events: none; transition: opacity 80ms linear; }
.point-tooltip rect { fill: var(--canvas-subtle); stroke: var(--border); vector-effect: non-scaling-stroke; }
.point-tooltip text { fill: var(--fg); font-size: 3px; font-weight: 600; }
.pie-chart-tooltip text { font-size: 2.25px; }
.chart-point:hover .point-tooltip, .chart-point:focus-visible .point-tooltip { opacity: 1; }
.chart-point:focus-visible .line-chart-point { stroke: var(--focus); stroke-width: calc(var(--chart-point-size, 4px) + 2px); }
.chart-point:focus-visible .dot-chart-point, .chart-point:focus-visible .scatter-chart-point { stroke: var(--focus); stroke-width: 3; }
.chart-point-link:focus-visible .point-tooltip { opacity: 1; }
.chart-point-link:focus-visible .line-chart-point, .chart-point-link:focus-visible .dot-chart-point, .chart-point-link:focus-visible .bar-chart-bar { stroke: var(--focus); stroke-width: 3; }
.bar-chart-axis { stroke: var(--border); stroke-width: .75; vector-effect: non-scaling-stroke; }
.bar-chart-grid { stroke: var(--border-muted); stroke-width: .5; stroke-dasharray: 1.5 2; vector-effect: non-scaling-stroke; }
.bar-chart-y-axis text, .bar-chart-x-axis text { fill: var(--muted); font-size: 2.6px; font-variant-numeric: tabular-nums; }
.bar-chart-bar { fill: var(--accent); stroke: var(--canvas); stroke-width: .5; }
.histogram-chart-bar { fill: var(--accent); stroke: color-mix(in srgb, var(--success) 72%, var(--canvas)); stroke-width: .65; vector-effect: non-scaling-stroke; }
.chart-widget [tabindex]:focus-visible { outline: 2px solid var(--focus); outline-offset: 2px; stroke: var(--focus); stroke-width: 3; }
.chart-widget .chart-series-1 { stroke: var(--success); }
.chart-widget .chart-series-2 { stroke: var(--attention); }
.chart-widget .chart-series-3 { stroke: var(--danger); }
.chart-widget .chart-series-4 { stroke: var(--accent); }
.chart-widget .chart-series-5 { stroke: var(--muted); }
.chart-widget .chart-series-6 { stroke: var(--purple); }
.swimlane-chart-widget { display: block; min-height: 0; margin: 8px 0 0; padding: 8px 0 4px; }
.swimlane-empty-state { min-height: 180px; display: grid; place-content: center; text-align: center; }
.swimlane-empty-state p { margin: 4px 0 0; color: var(--muted); }
.swimlane-chart-widget svg { width: 100%; max-height: 220px; overflow: visible; }
.swimlane-summary { width: 100%; display: flex; flex-wrap: wrap; gap: 6px 18px; margin: 0 0 8px; padding: 0; color: var(--muted); font-size: .75rem; font-variant-numeric: tabular-nums; list-style: none; }
.swimlane-summary li:first-child { color: var(--fg); font-weight: 600; }
.swimlane-label, .swimlane-time-label { fill: var(--muted); font-size: 2.4px; }
.swimlane-label { font-weight: 600; }
.swimlane-separator { stroke: var(--border-muted); stroke-width: .45; }
.swimlane-axis, .swimlane-tick { stroke: var(--border); stroke-width: .55; }
.swimlane-run-mark { stroke-width: 3; stroke-linecap: round; vector-effect: non-scaling-stroke; transition: filter 120ms ease, stroke-width 120ms ease; }
.swimlane-mark-action-required { stroke: var(--accent); }
.swimlane-mark-failure { stroke: var(--danger); }
.swimlane-mark-cancelled { stroke: var(--attention); }
.swimlane-mark-skipped { stroke: var(--muted); }
.swimlane-mark-success { stroke: var(--accent); }
.swimlane-mark-unknown { stroke: var(--muted); }
.swimlane-mark:hover, .swimlane-mark:focus-visible { filter: brightness(1.2); stroke-width: 4; }
.swimlane-mark:focus-visible { outline: none; }
.dashboard-full-view .custom-view-grid > .custom-view { padding-inline: var(--dashboard-page-padding-inline); }
.dashboard-full-view .custom-view-grid > .chart-view-swimlane { padding-bottom: 14px; }
.chart-widget .chart-series-7 { stroke: var(--pink); }
.chart-widget .chart-series-8 { stroke: var(--coral); }
.chart-widget .chart-series-9 { stroke: var(--yellow); }
.chart-widget .chart-series-10 { stroke: var(--cyan); }
.chart-widget .chart-series-11 { stroke: var(--lime); }
.chart-widget .chart-series-12 { stroke: var(--violet); }
.bar-chart-bar.chart-series-1 { fill: var(--success); background: var(--success); }
.bar-chart-bar.chart-series-2 { fill: var(--attention); background: var(--attention); }
.bar-chart-bar.chart-series-3 { fill: var(--danger); background: var(--danger); }
.bar-chart-bar.chart-series-4 { fill: var(--accent); background: var(--accent); }
.bar-chart-bar.chart-series-5 { fill: var(--muted); background: var(--muted); }
.bar-chart-bar.chart-series-6 { fill: var(--purple); background: var(--purple); }
.bar-chart-bar.chart-series-7 { fill: var(--pink); background: var(--pink); }
.bar-chart-bar.chart-series-8 { fill: var(--coral); background: var(--coral); }
.bar-chart-bar.chart-series-9 { fill: var(--yellow); background: var(--yellow); }
.bar-chart-bar.chart-series-10 { fill: var(--cyan); background: var(--cyan); }
.bar-chart-bar.chart-series-11 { fill: var(--lime); background: var(--lime); }
.bar-chart-bar.chart-series-12 { fill: var(--violet); background: var(--violet); }
.area-chart-area.chart-series-1 { fill: var(--success); }
.area-chart-area.chart-series-2 { fill: var(--attention); }
.area-chart-area.chart-series-3 { fill: var(--danger); }
.area-chart-area.chart-series-4 { fill: var(--accent); }
.area-chart-area.chart-series-5 { fill: var(--muted); }
.area-chart-area.chart-series-6 { fill: var(--purple); }
.area-chart-area.chart-series-7 { fill: var(--pink); }
.area-chart-area.chart-series-8 { fill: var(--coral); }
.area-chart-area.chart-series-9 { fill: var(--yellow); }
.area-chart-area.chart-series-10 { fill: var(--cyan); }
.area-chart-area.chart-series-11 { fill: var(--lime); }
.area-chart-area.chart-series-12 { fill: var(--violet); }
.histogram-chart-bar.chart-series-1 { fill: var(--success); }
.chart-legend i.chart-series-1 { border-color: var(--success); color: var(--success); }
.chart-legend i.chart-series-2 { border-color: var(--attention); color: var(--attention); }
.chart-legend i.chart-series-3 { border-color: var(--danger); color: var(--danger); }
.chart-legend i.chart-series-4 { border-color: var(--accent); color: var(--accent); }
.chart-legend i.chart-series-5 { border-color: var(--muted); color: var(--muted); }
.chart-legend i.chart-series-6 { border-color: var(--purple); color: var(--purple); }
.chart-legend i.chart-series-7 { border-color: var(--pink); color: var(--pink); }
.chart-legend i.chart-series-8 { border-color: var(--coral); color: var(--coral); }
.chart-legend i.chart-series-9 { border-color: var(--yellow); color: var(--yellow); }
.chart-legend i.chart-series-10 { border-color: var(--cyan); color: var(--cyan); }
.chart-legend i.chart-series-11 { border-color: var(--lime); color: var(--lime); }
.chart-legend i.chart-series-12 { border-color: var(--violet); color: var(--violet); }
.chart-widget .chart-series-semantic-failure { stroke: var(--danger); }
.chart-widget .chart-series-semantic-success, .chart-widget .chart-series-semantic-waiting { stroke: var(--accent); }
.chart-widget .chart-series-semantic-attention { stroke: var(--attention); }
.chart-widget .chart-series-semantic-neutral { stroke: var(--muted); }
.area-chart-area.chart-series-semantic-failure { fill: var(--danger); }
.area-chart-area.chart-series-semantic-success, .area-chart-area.chart-series-semantic-waiting { fill: var(--accent); }
.area-chart-area.chart-series-semantic-attention { fill: var(--attention); }
.area-chart-area.chart-series-semantic-neutral { fill: var(--muted); }
.bar-chart-bar.chart-series-semantic-failure { fill: var(--danger); background: var(--danger); }
.bar-chart-bar.chart-series-semantic-success, .bar-chart-bar.chart-series-semantic-waiting { fill: var(--accent); background: var(--accent); }
.bar-chart-bar.chart-series-semantic-attention { fill: var(--attention); background: var(--attention); }
.bar-chart-bar.chart-series-semantic-neutral { fill: var(--muted); background: var(--muted); }
.chart-legend i.chart-series-semantic-failure { border-color: var(--danger); color: var(--danger); }
.chart-legend i.chart-series-semantic-success, .chart-legend i.chart-series-semantic-waiting { border-color: var(--accent); color: var(--accent); }
.chart-legend i.chart-series-semantic-attention { border-color: var(--attention); color: var(--attention); }
.chart-legend i.chart-series-semantic-neutral { border-color: var(--muted); color: var(--muted); }
@keyframes pie-chart-entry {
  from { opacity: 0; }
}
@keyframes area-chart-entry {
  from { opacity: 0; transform: translateY(3px); }
}
@keyframes line-chart-entry {
  from { opacity: 0; }
  to { opacity: 1; }
}
@keyframes line-chart-context-entry {
  from { opacity: 0; }
  to { opacity: .3; }
}
@keyframes line-chart-point-entry {
  from { opacity: 0; transform: scale(0); }
}
@keyframes histogram-chart-entry {
  from { opacity: 0; transform: scaleY(0); }
}
@keyframes horizontal-bar-chart-entry {
  from { opacity: 0; transform: scaleX(0); }
}
@media (prefers-reduced-motion: reduce) {
  .pie-chart-segment, .area-chart-area, .line-chart-series, .line-chart-point, .dot-chart-point, .scatter-chart-point, .bar-chart-bar, .histogram-chart-bar, .table-summary-histogram rect, .metric-number-animated { animation: none; }
  .metric-number-animated { --metric-number: var(--metric-number-target); }
  .pie-chart-segment, .point-tooltip, .swimlane-run-mark, .dashboard-notification-chevron { transition: none; }
}
.view-description { margin: 3px 0 0; color: var(--muted); }
.chart-view-pie { display: grid; gap: 16px; }
.pie-chart-card { display: grid; grid-template-columns: minmax(190px, .65fr) minmax(0, 1.35fr); align-items: center; gap: 4px 24px; padding: 20px 24px; }
.layout-section .pie-chart-card { padding: 0; border: 0; }
#page-preview .pie-chart-card { padding: 0; border: 0; }
.pie-chart-card > :is(h2, h3, h4) { align-self: end; margin: 0; font-size: 1.25rem; }
.pie-chart-card > .chart-prompt-heading { align-self: end; }
.pie-chart-card > .chart-prompt-heading > :is(h2, h3, h4) { font-size: 1.25rem; }
.pie-chart-card > .view-description { align-self: start; }
.pie-chart-card > .view-source, .pie-chart-card > .view-metadata, .pie-chart-card > .view-context { grid-column: 1; margin: 0; font-size: .6875rem; }
.pie-chart-layout { min-width: 0; display: grid; grid-column: 2; grid-row: 1 / span 6; grid-template-columns: minmax(120px, 180px) minmax(0, 1fr); align-items: center; gap: 20px; }
.pie-chart-layout > .view-state-card { grid-column: 1 / -1; grid-row: 1 / span 2; align-self: start; margin: 0; }
.chart-view-pie[data-view-layout="full"] > .pie-chart-layout { grid-column: 1; grid-row: auto; }
.pie-chart-layout .chart-widget { min-width: 0; min-height: 160px; margin: 0; }
#page-preview .pie-chart-layout .chart-widget { border: 0; background: transparent; }
.pie-chart-layout .chart-widget svg { width: 100%; max-width: 160px; height: auto; max-height: none; }
.pie-chart-table-toggle { display: none; }
.pie-chart-layout .chart-legend-pie { min-width: 0; width: 100%; display: block; margin: 0; }
.pie-chart-layout .chart-legend-pie li { min-height: 30px; display: grid; grid-template-columns: 10px minmax(0, 1fr) auto 54px; gap: 9px; border-bottom: 1px solid var(--border-muted); }
.pie-chart-layout .chart-legend-pie li:last-child { border-bottom: 0; }
.pie-chart-layout .chart-legend-pie i { width: 9px; height: 9px; border-radius: 2px; }
.pie-chart-layout .chart-legend-pie span { min-width: 0; overflow-wrap: anywhere; }
.pie-chart-layout .chart-legend-pie strong, .pie-chart-layout .chart-legend-pie small { font-variant-numeric: tabular-nums; text-align: right; }
.chart-horizontal-card { display: grid; grid-template-columns: minmax(190px, .65fr) minmax(0, 1.35fr); align-items: start; gap: 24px; padding: 20px 24px; }
.chart-horizontal-copy > :is(h2, h3, h4) { margin: 0; font-size: 1.25rem; }
.chart-horizontal-copy > .chart-prompt-heading > :is(h2, h3, h4) { font-size: 1.25rem; }
.chart-horizontal-copy > .view-description { margin-top: 3px; }
.chart-horizontal-copy > .view-source, .chart-horizontal-copy > .view-metadata, .chart-horizontal-copy > .view-context { margin: 0; font-size: .6875rem; }
.chart-horizontal-layout { min-width: 0; }
`;
