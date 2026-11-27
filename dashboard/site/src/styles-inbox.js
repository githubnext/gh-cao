export const inboxStyles = `.notifications-inbox { min-width: 0; display: grid; gap: 32px; }
.notifications-main { min-width: 0; }
.notifications-health { min-width: 0; }
.home-catchup { display: grid; gap: 22px; padding: 6px 0 28px; border-bottom: 1px solid var(--border-muted); }
.home-catchup-header { display: flex; justify-content: flex-end; }
.home-catchup-controls { display: flex; align-items: center; gap: 8px; flex: none; }
.home-catchup-controls select, .home-catchup-done { min-height: 34px; border: 1px solid var(--border); border-radius: 6px; background: var(--canvas); color: var(--fg); font: inherit; font-size: .75rem; font-weight: 600; }
.home-catchup-controls select { padding: 0 28px 0 9px; }
.home-catchup-done { display: inline-flex; align-items: center; gap: 6px; padding: 0 10px; cursor: pointer; }
.home-catchup-done:hover { background: var(--canvas-subtle); }
.home-catchup-done:disabled { color: var(--success); cursor: default; }
.home-catchup-content > div { display: grid; gap: 18px; }
.home-catchup-metrics { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); margin: 0; border-top: 1px solid var(--border-muted); border-bottom: 1px solid var(--border-muted); }
.home-catchup-metric { position: relative; display: grid; gap: 1px; padding: 13px 16px; }
.home-catchup-metric + .home-catchup-metric { border-left: 1px solid var(--border-muted); }
.home-catchup-metric dt { grid-row: 2; color: var(--muted); font-size: .6875rem; }
.home-catchup-metric dd { margin: 0; font-size: 1.25rem; font-weight: 700; font-variant-numeric: tabular-nums; }
.home-catchup-metric-success dd, .home-positive { color: var(--success); }
.home-catchup-metric-attention dd { color: var(--attention); }
.home-catchup-metric-accent dd { color: var(--accent); }
.home-catchup-charts { min-width: 0; display: grid; grid-template-columns: minmax(0, 1.75fr) minmax(250px, .75fr); gap: 18px; }
.home-momentum-panel, .home-mini-chart { min-width: 0; padding: 16px; border: 1px solid var(--border); border-radius: 6px; background: var(--canvas); }
.home-momentum-panel > header { display: grid; grid-template-columns: minmax(0, 1fr) auto; align-items: start; gap: 2px 12px; }
.home-momentum-panel > header > div, .home-mini-chart > header { display: flex; align-items: center; gap: 9px; }
.home-momentum-panel h3, .home-mini-chart h3 { margin: 0; font-size: .8125rem; }
.home-momentum-panel > header > strong { font-size: 1rem; text-align: right; }
.home-momentum-panel > header > small { grid-column: 2; color: var(--muted); font-size: .75rem; }
.home-momentum-chart { width: 100%; height: 150px; margin-top: 10px; overflow: visible; }
.home-chart-grid { stroke: var(--border-muted); stroke-width: 1; vector-effect: non-scaling-stroke; }
.home-chart-delivered { fill: var(--success); }
.home-chart-pending { fill: var(--attention); }
.home-chart-legend { display: flex; gap: 16px; color: var(--muted); font-size: .6875rem; }
.home-chart-legend span { display: inline-flex; align-items: center; gap: 5px; }
.home-chart-legend i { width: 8px; height: 8px; border-radius: 2px; }
.home-legend-delivered { background: var(--success); }
.home-legend-pending { background: var(--attention); }
.home-catchup-side-charts { display: grid; grid-template-rows: repeat(2, minmax(0, 1fr)); gap: 12px; }
.home-work-now { display: grid; grid-template-columns: 82px minmax(0, 1fr); align-items: center; gap: 14px; margin-top: 14px; }
.home-work-ring { width: 76px; aspect-ratio: 1; display: grid; place-content: center; border-radius: 50%; background: radial-gradient(circle, var(--canvas) 55%, transparent 57%), conic-gradient(var(--accent) 0 var(--running), var(--attention) var(--running) var(--review), var(--border-muted) var(--review)); text-align: center; }
.home-work-ring strong { font-size: 1.125rem; line-height: 1; }
.home-work-ring span { color: var(--muted); font-size: .75rem; }
.home-work-now dl { display: grid; gap: 7px; margin: 0; }
.home-work-now dl div { display: flex; justify-content: space-between; gap: 10px; font-size: .6875rem; }
.home-work-now dt { color: var(--muted); }
.home-work-now dd { margin: 0; font-weight: 700; }
.home-value-gain { display: grid; grid-template-columns: auto minmax(80px, 1fr); align-items: end; gap: 12px; margin-top: 12px; }
.home-value-gain strong { font-size: 1.125rem; white-space: nowrap; }
.home-value-gain svg { width: 100%; height: 50px; overflow: visible; }
.home-value-line { fill: none; stroke: var(--success); stroke-width: 3; stroke-linecap: round; stroke-linejoin: round; vector-effect: non-scaling-stroke; }
.home-catchup-stories { min-width: 0; display: grid; gap: 9px; }
.home-catchup-stories > header { display: flex; align-items: baseline; justify-content: space-between; gap: 12px; }
.home-catchup-stories h3 { margin: 0; font-size: .875rem; }
.home-catchup-stories > header span { color: var(--muted); font-size: .6875rem; }
.home-catchup-mobile { display: none; }
.home-story-rail { min-width: 0; display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); border-top: 1px solid var(--border-muted); }
.home-catchup-story { min-width: 0; display: flex; align-items: center; gap: 10px; padding: 12px 4px; color: var(--fg); }
.home-catchup-story:nth-child(odd) { padding-right: 16px; }
.home-catchup-story:nth-child(even) { padding-left: 16px; border-left: 1px solid var(--border-muted); }
.home-catchup-story:nth-child(n + 3) { border-top: 1px solid var(--border-muted); }
.home-catchup-story:hover .home-story-copy strong { color: var(--accent); }
.home-catchup-story .octicon { width: 12px; height: 12px; color: var(--muted); }
.home-story-link { min-width: 0; flex: 1 1 auto; display: grid; grid-template-columns: auto minmax(0, 1fr) 14px; align-items: center; gap: 10px; color: inherit; text-decoration: none; }
.home-story-copy { min-width: 0; display: grid; }
.home-story-copy strong, .home-story-copy small { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.home-story-copy strong { font-size: .75rem; }
.home-story-copy small, .home-catchup-quiet { color: var(--muted); font-size: .6875rem; }
.home-catchup-quiet { margin: 0; padding: 16px 0; border-top: 1px solid var(--border-muted); }
.home-origin-badge { min-width: 76px; min-height: 28px; display: inline-flex; align-items: center; justify-content: center; gap: 5px; padding: 0 9px; border: 1px solid currentColor; border-radius: 999px; font-size: .6875rem; font-weight: 700; white-space: nowrap; }
.home-origin-badge .octicon { width: 11px; height: 11px; }
.home-origin-agents { background: color-mix(in srgb, var(--accent) 9%, var(--canvas)); color: var(--accent); }
.home-origin-work { background: color-mix(in srgb, var(--success) 8%, var(--canvas)); color: var(--success); }
.home-origin-insights { background: color-mix(in srgb, var(--attention) 10%, var(--canvas)); color: color-mix(in srgb, var(--attention) 75%, var(--fg)); }
.notifications-health-heading { min-width: 0; display: flex; align-items: flex-start; gap: 10px; }
.notifications-health-icon { width: 32px; height: 32px; display: grid; place-items: center; flex: 0 0 32px; border-radius: 50%; background: var(--neutral-muted); color: var(--muted); }
.notifications-health.is-healthy .notifications-health-icon { background: color-mix(in srgb, var(--success) 12%, transparent); color: var(--success); }
.notifications-health-heading h2 { margin: 0 0 3px; font-size: .875rem; }
.notifications-health-heading p { margin: 0; color: var(--muted); font-size: .75rem; line-height: 1.4; }
.notifications-health-chart { width: 100%; height: 68px; overflow: visible; }
.notifications-health-baseline { stroke: var(--border); stroke-width: 1; vector-effect: non-scaling-stroke; }
.notifications-health-bar { fill: var(--muted); }
.notifications-health-success { fill: var(--success); }
.notifications-health-other { fill: var(--muted); opacity: .55; }
.notifications-health-failed { fill: var(--danger); }
.notifications-health-metrics { display: flex; gap: 22px; margin: 0; }
.notifications-health-metrics > div { min-width: 62px; display: grid; gap: 2px; }
.notifications-health-metrics dt { grid-row: 2; color: var(--muted); font-size: .6875rem; white-space: nowrap; }
.notifications-health-metrics dd { grid-row: 1; margin: 0; color: var(--fg); font-size: 1rem; font-weight: 600; font-variant-numeric: tabular-nums; }
.notifications-toolbar { display: grid; grid-template-columns: auto minmax(180px, 1fr) auto auto; gap: 8px; margin-bottom: 12px; }
.notifications-advanced-filters { display: contents; }
.notifications-filter-toggle { display: none; }
.notifications-state-tabs { display: flex; overflow: hidden; border: 1px solid var(--border); border-radius: 6px; }
.notifications-state-tabs button { padding: 5px 11px; border: 0; background: var(--canvas); color: var(--fg); font: inherit; font-size: .75rem; font-weight: 600; cursor: pointer; }
.notifications-state-tabs button + button { border-left: 1px solid var(--border); }
.notifications-state-tabs button:hover { background: var(--canvas-subtle); }
.notifications-search { min-width: 0; min-height: 32px; display: flex; align-items: center; gap: 7px; padding: 0 9px; border: 1px solid var(--border); border-radius: 6px; background: var(--canvas-inset); }
.notifications-search:focus-within { border-color: var(--focus); outline: 1px solid var(--focus); }
.notifications-search .octicon { flex: none; color: var(--muted); }
.notifications-search input { width: 100%; min-width: 0; border: 0; outline: 0; background: transparent; color: var(--fg); font: inherit; font-size: .8125rem; }
.notifications-select { min-height: 32px; display: flex; align-items: center; gap: 3px; padding: 0 5px 0 9px; border: 1px solid var(--border); border-radius: 6px; background: var(--canvas-subtle); color: var(--muted); font-size: .6875rem; white-space: nowrap; }
.notifications-select:focus-within { border-color: var(--focus); outline: 1px solid var(--focus); }
.notifications-select select { max-width: 130px; border: 0; outline: 0; background: transparent; color: var(--fg); font: inherit; font-size: .75rem; font-weight: 600; }
.notifications-selection-bar { min-height: 42px; display: flex; align-items: center; gap: 14px; padding: 7px 12px; border: 1px solid var(--border); border-radius: 6px 6px 0 0; background: var(--canvas-subtle); font-size: .75rem; }
.notifications-selection-bar label { min-height: 24px; display: flex; align-items: center; gap: 8px; font-weight: 600; }
.notifications-result-count { margin-left: auto; color: var(--muted); }
.notifications-list { min-width: 0; }
.notifications-load-boundary { min-height: 48px; display: flex; align-items: center; justify-content: center; gap: 12px; padding: 8px 12px; border: 1px solid var(--border); border-top: 0; background: var(--canvas-subtle); color: var(--muted); font-size: .75rem; }
.notifications-load-boundary button { min-height: 32px; padding: 0 10px; border: 1px solid var(--border); border-radius: 6px; background: var(--canvas); color: var(--fg); font: inherit; font-weight: 600; cursor: pointer; }
.notifications-load-boundary button:hover { background: var(--neutral-muted); }
.notifications-group-heading { margin: 0; padding: 8px 12px; border: 1px solid var(--border); border-top: 0; background: var(--canvas-inset); color: var(--muted); font-size: .6875rem; font-weight: 600; }
.notifications-group { margin: 0; padding: 0; border: 1px solid var(--border); border-top: 0; list-style: none; }
.notifications-group:last-child { border-radius: 0 0 6px 6px; }
.notifications-priority-group { border-top: 1px solid var(--border); border-radius: 6px; }
.notifications-cause-cluster { border: 1px solid var(--border); border-top: 0; background: var(--canvas); }
.notifications-cause-cluster:first-child, .notifications-priority-group + .notifications-cause-cluster { border-top: 1px solid var(--border); border-radius: 6px 6px 0 0; }
.notifications-cause-cluster:last-child { border-radius: 0 0 6px 6px; }
.notifications-cause-summary { min-height: 58px; display: grid; grid-template-columns: 20px minmax(0, 1fr) 16px; align-items: center; gap: 10px; padding: 8px 12px; cursor: pointer; list-style: none; }
.notifications-cause-summary::-webkit-details-marker { display: none; }
.notifications-cause-summary:hover { background: var(--canvas-subtle); }
.notifications-cause-copy { min-width: 0; display: grid; }
.notifications-cause-copy strong { overflow: hidden; font-size: .8125rem; text-overflow: ellipsis; white-space: nowrap; }
.notifications-cause-copy small { color: var(--muted); font-size: .75rem; }
.notifications-cause-chevron { color: var(--muted); transition: transform 120ms ease; }
.notifications-cause-cluster[open] .notifications-cause-chevron { transform: rotate(90deg); }
.notifications-cause-cluster > .notifications-group { border-right: 0; border-bottom: 0; border-left: 0; }
.notification-item { min-height: 62px; display: grid; grid-template-columns: 8px 24px auto minmax(0, 1fr) minmax(110px, auto) auto; align-items: center; gap: 10px; padding: 8px 10px; background: var(--canvas); }
.notification-item input[type="checkbox"] { width: 24px; height: 24px; margin: 0; }
.notification-item + .notification-item { border-top: 1px solid var(--border-muted); }
.notification-item.unread { background: color-mix(in srgb, var(--accent) 7%, var(--canvas)); }
.notification-unread-dot { width: 7px; height: 7px; border-radius: 50%; background: transparent; }
.notification-item.unread .notification-unread-dot { background: var(--accent); }
.notification-kind { color: var(--muted); }
.notification-item.unread .notification-kind { color: var(--accent); }
.notification-content { min-width: 0; display: grid; color: var(--fg); text-decoration: none; }
.notification-content:hover strong { color: var(--accent); text-decoration: underline; }
.notification-repository { color: var(--muted); font-size: .6875rem; font-weight: 600; }
.notification-content strong, .notification-content small { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.notification-content strong { font-size: .8125rem; }
.notification-content small { color: var(--muted); font-size: .75rem; }
.notification-meta { display: grid; justify-items: end; color: var(--fg); font-size: .6875rem; font-weight: 600; text-align: right; }
.notification-meta small { color: var(--muted); font-weight: 400; }
.notification-actions { display: flex; gap: 4px; opacity: .35; }
.notification-item:hover .notification-actions, .notification-actions:focus-within { opacity: 1; }
.notifications-icon-button { width: 28px; height: 28px; display: grid; place-items: center; padding: 0; border: 1px solid transparent; border-radius: 6px; background: transparent; color: var(--muted); cursor: pointer; }
.notifications-icon-button:hover, .notifications-icon-button.active { border-color: var(--border); background: var(--neutral-muted); color: var(--fg); }
.notifications-icon-button:disabled { opacity: .4; cursor: default; }
.notifications-empty { min-height: 260px; display: grid; align-content: center; justify-items: center; gap: 8px; border: 1px solid var(--border); border-top: 0; color: var(--muted); }
.notifications-empty .octicon { width: 28px; height: 28px; color: var(--success); }
.overview-caught-up { min-height: min(56vh, 520px); display: grid; align-content: center; justify-items: center; gap: 10px; color: var(--muted); text-align: center; }
.overview-caught-up-icon { width: 56px; height: 56px; display: grid; place-items: center; border: 1px solid var(--border); border-radius: 50%; background: var(--canvas-subtle); color: var(--success); }
.overview-caught-up-unknown .overview-caught-up-icon { color: var(--muted); }
.overview-caught-up-icon .octicon { width: 28px; height: 28px; }
.overview-caught-up h2 { margin: 4px 0 0; color: var(--fg); font-size: 1rem; }
.overview-caught-up p { margin: 0; font-size: .8125rem; }
.dashboard-overview-page .custom-view[data-view-layout="half"].chart-view-pie .pie-chart-card { grid-template-columns: minmax(0, 1fr); padding: 16px; }
.dashboard-overview-page .custom-view[data-view-layout="half"].chart-view-pie .pie-chart-layout { grid-column: 1; grid-row: auto; grid-template-columns: minmax(120px, 160px) minmax(0, 1fr); gap: 12px; }
.dashboard-overview-page .custom-view[data-view-layout="half"].chart-view-pie .pie-chart-card > :is(.view-source, .view-metadata, .view-context) { grid-column: 1; }
`;
