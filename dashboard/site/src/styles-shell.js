export const shellStyles = `.app-shell { height: 100vh; min-height: 0; display: grid; grid-template-columns: 200px minmax(0, 1fr); overflow: hidden; transition: grid-template-columns 120ms ease; }
.document-list-actions { display: flex; flex: none; flex-wrap: wrap; align-items: center; gap: 8px; }
.dialog-selector { display: grid; grid-template-rows: auto auto minmax(0, 1fr); flex-shrink: 0; min-height: 0; gap: 10px; }
.dialog-selector label { display: grid; gap: 6px; font-weight: 600; }
.dialog-selector-input { width: 100%; min-height: 44px; padding: 8px 12px; border: 1px solid var(--border); border-radius: 6px; background: var(--canvas); color: var(--fg); font: inherit; }
.dialog-selector-input:focus-visible, .dialog-selector-option:focus-visible { outline: 2px solid var(--accent); outline-offset: 2px; }
.dialog-selector-status { color: var(--muted); font-size: .8125rem; }
.dialog-selector-options { display: grid; min-height: 0; gap: 6px; max-height: min(280px, 35vh); overflow-y: auto; overscroll-behavior: contain; scrollbar-gutter: stable; scroll-padding: 4px; }
.dialog-selector-option { display: grid; gap: 4px; min-height: 44px; padding: 10px; border: 1px solid var(--border); border-radius: 6px; text-align: left; overflow-wrap: anywhere; background: var(--canvas); color: var(--fg); cursor: pointer; }
.dialog-selector-option[aria-pressed="true"] { border-color: var(--accent); background: var(--accent-muted); }
.dialog-selector-option small { color: var(--muted); }
.org-sidebar { min-width: 0; height: 100vh; display: flex; flex-direction: column; gap: 8px; overflow: visible; padding: 24px 16px 16px; border-right: 1px solid var(--border); background: var(--canvas-subtle); }
.dashboard-offline-status:not([hidden]) { display: inline-flex; align-items: center; gap: 8px; padding: 6px 8px; color: var(--muted); font-size: .75rem; }
.mobile-nav-menu .dashboard-offline-status { display: none; }
.sidebar-header { min-width: 0; display: flex; align-items: center; gap: 8px; margin: 0 0 10px 8px; }
.sidebar-brand { display: flex; align-items: center; gap: 6px; min-width: 0; flex: 1; overflow: hidden; color: var(--fg); font-size: 1rem; font-weight: 600; text-decoration: none; white-space: nowrap; }
.sidebar-brand-mark { width: 24px; height: 24px; flex: 0 0 24px; overflow: visible; }
.sidebar-brand > span { min-width: 0; overflow: hidden; text-overflow: ellipsis; }
.mobile-history-back:not([hidden]) { width: 28px; height: 28px; display: grid; flex: 0 0 28px; place-items: center; padding: 0; border: 1px solid var(--border); border-radius: 6px; background: var(--canvas-subtle); color: var(--fg); cursor: pointer; }
.mobile-history-back:hover { background: var(--neutral-muted); }
.mobile-view-mode-toggle { display: none; }
.sidebar-toggle { width: 28px; height: 28px; display: grid; flex: 0 0 28px; place-items: center; padding: 0; border: 0; border-radius: 6px; background: transparent; color: var(--muted); cursor: pointer; }
.sidebar-toggle:hover { background: var(--neutral-muted); color: var(--fg); }
.mobile-nav-menu-actions { display: none; }
.mobile-page-header { display: none; }
.dashboard-page[data-view-mode="chart"] [data-view-mode-content="table"],
.dashboard-page[data-view-mode="chart"] [data-view-mode-content="card"],
.dashboard-page[data-view-mode="table"] [data-view-mode-content="chart"],
.dashboard-page[data-view-mode="table"] [data-view-mode-content="card"],
.dashboard-page[data-view-mode="card"] [data-view-mode-content="chart"] { display: none; }
.dashboard-page[data-view-mode="table"] [data-mobile-card-list]:not(.table-card-list),
.dashboard-page[data-view-mode="card"] [data-view-mode-content="table"] > .table-region { display: none; }
.mobile-table-card-list { display: none; }
.mobile-table-card-list.table-card-list { display: flex; flex-direction: column; min-height: 0; border: 1px solid var(--border); border-radius: 6px; background: var(--canvas); }
.dashboard-page[data-view-mode="card"] [data-mobile-card-list] { display: flex; flex-direction: column; gap: 0; overflow: hidden; border: 1px solid var(--border); border-radius: 14px; background: var(--canvas); }
.mobile-table-card-toolbar { flex: none; display: flex; flex-wrap: wrap; align-items: center; gap: 8px; min-height: 48px; position: relative; z-index: 6; padding: 6px 16px; border-bottom: 1px solid var(--border); background: var(--canvas-subtle); }
.mobile-table-card-toolbar-title { color: var(--muted); font-size: .875rem; font-weight: 600; }
.mobile-table-card-toolbar > .card-filter-bar { flex: 1; min-width: 0; padding: 0; border: 0; border-radius: 0; background: transparent; }
.mobile-table-card-toolbar > .semantic-prompt-action { position: static; flex: none; margin: 0 0 0 auto; }
.dashboard-full-view .custom-view[data-view-layout="full-view"] > .mobile-table-card-list { min-height: 0; flex: 1; overflow: hidden; }
.dashboard-full-view .dashboard-page[data-view-mode="card"] > .custom-view-grid > .custom-view[data-view-layout="full-view"]:has(> .mobile-table-card-list) { padding: 16px 0 0; }
.dashboard-full-view .mobile-table-card-list-items { min-height: 0; flex: 1; overflow-y: auto; }
.mobile-table-card-list-items { display: block; min-height: 0; overflow-y: auto; border: 0; background: transparent; }
.mobile-table-card-list-boundary { min-height: 1px; list-style: none; }
.mobile-table-card-list-items .entity-card-list-card { border: 0; border-top: 1px solid var(--border); border-radius: 0; background: var(--canvas); box-shadow: none; }
.mobile-table-card-list-items .entity-card-list-card:first-child { border-top: 0; }
.entity-card-list-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(min(100%, 360px), 1fr)); gap: 14px; border: 0; background: transparent; }
.entity-card-list-grid .entity-card-list-card { grid-template-columns: 20px minmax(0, 1fr); align-content: start; border: 1px solid var(--border); background: var(--canvas-subtle); }
.entity-card-list-grid .issue-list-labels, .entity-card-list-grid .entity-card-list-timing { grid-column: 2; justify-content: flex-start; }
.issue-list-labels .entity-card-list-metric { display: inline-flex; align-items: baseline; gap: 4px; border-color: var(--border); background: var(--canvas-subtle); color: var(--muted); font-variant-numeric: tabular-nums; }
.issue-list-labels .entity-card-list-metric strong { color: var(--fg); }
.entity-card-list-card:has(.entity-card-list-timing) { grid-template-columns: 20px minmax(0, 1fr) auto auto; }
.entity-card-list-status { display: flex; align-items: flex-start; color: var(--muted); }
.entity-card-list-status-success { color: var(--success); }
.entity-card-list-status-danger { color: var(--danger); }
.entity-card-list-status-attention { color: var(--attention); }
.entity-card-list-status-accent { color: var(--accent); }
.entity-card-list-status-muted { color: var(--cancelled); }
.issue-list-labels .entity-card-list-ref, .ref-label { max-width: 240px; border-color: var(--accent-muted); background: var(--accent-muted); color: var(--accent); font-family: var(--font-mono, ui-monospace, monospace); font-size: .6875rem; font-weight: 400; }
.issue-list-labels .entity-card-list-badge { max-width: none; padding: 0; border: 0; background: transparent; font-weight: 400; line-height: 1; }
.ref-label { display: inline-block; padding: 0 7px; border: 1px solid var(--accent-muted); border-radius: 999px; line-height: 18px; }
.entity-card-list-timing { display: grid; align-content: start; gap: 2px; margin: 0; padding: 0; color: var(--muted); font-size: .75rem; list-style: none; }
.entity-card-list-timing-item { display: flex; align-items: center; gap: 6px; white-space: nowrap; }
.entity-card-list-timing-icon { display: inline-flex; color: var(--muted); }
.entity-card-list-timing-icon .octicon { width: 14px; height: 14px; }
.entity-card-list-timing-value { font-variant-numeric: tabular-nums; }
.entity-card-list-timing-value time { color: inherit; }
.entity-card-list-actions { display: flex; flex-wrap: wrap; gap: 6px; grid-column: 2 / -1; margin-top: 10px; } /* keep in sync with .issue-list-card's grid-template-columns (defined further below): column 1 is the status/type icon, column 2 is the card content, so "2 / -1" spans from the content column through the last column, forcing the actions to form their own full-width row instead of competing with the labels column for grid auto-placement */
.entity-card-list-actions .table-cli-action-button { width: auto; height: auto; min-height: 32px; display: inline-flex; align-items: center; justify-content: flex-start; padding: 6px 10px; border: 1px solid var(--border); }
.entity-card-list-actions .table-cli-action-button .cli-action-trigger-copy strong { font-size: .75rem; white-space: nowrap; }
.entity-card-list-grouped { overflow: hidden; border: 1px solid var(--border); border-radius: 14px; background: var(--canvas); }
.entity-card-list-grouped .entity-card-list-card { grid-template-columns: 20px minmax(0, 1fr) auto auto; align-items: center; padding: 12px 16px; }
.entity-card-list-grouped .entity-card-list-card:first-child { border-top: 0; }
.entity-card-list-grouped .entity-card-list-card:has([data-card-drill]):hover { background: var(--neutral-muted); }
.entity-card-list-grouped .issue-list-labels { justify-content: flex-end; }
.marketplace-page > .page-chrome { margin-bottom: 8px; padding: 40px 32px; border: 1px solid var(--border); border-radius: 12px; background: radial-gradient(circle at top right, var(--accent-muted), transparent 44%), var(--canvas-subtle); }
.marketplace-page > .page-chrome h1 { font-size: clamp(1.75rem, 4vw, 2.5rem); }
.marketplace-page > .page-chrome p { max-width: 640px; font-size: 1rem; }
.marketplace-page .custom-view-grid { max-width: 1012px; margin-inline: auto; }
.marketplace-page .custom-view { border: 0; background: transparent; box-shadow: none; }
.marketplace-page .custom-view > header { padding-inline: 0; }
.marketplace-page .document-list-header { padding: 0 0 16px; }
.entity-card-list.entity-card-list-marketplace { overflow: visible; display: grid; grid-template-columns: repeat(auto-fill, minmax(min(100%, 280px), 1fr)); gap: 16px; border: 0; background: transparent; }
.entity-card-list-marketplace .entity-card-list-card, .entity-card-list-marketplace .entity-card-list-card:first-child { min-height: 280px; grid-template-columns: minmax(0, 1fr); grid-template-rows: auto 1fr auto; align-content: start; justify-items: stretch; gap: 16px; padding: 24px; border: 1px solid var(--border); border-radius: 12px; background: var(--canvas); text-align: left; }
.entity-card-list-marketplace .entity-card-list-card:has([data-card-drill]) { cursor: pointer; transition: border-color 120ms ease, background-color 120ms ease; }
.entity-card-list-marketplace .entity-card-list-card:has([data-card-drill]):hover { border-color: var(--accent); background: var(--canvas-subtle); }
.entity-card-list-marketplace .entity-card-list-card:focus-within { outline: 2px solid var(--accent); outline-offset: 2px; }
.entity-card-list-marketplace .issue-list-card-icon { grid-column: 1; grid-row: 1; width: 56px; height: 56px; display: grid; place-items: center; padding: 0; border: 1px solid var(--accent-muted); border-radius: 12px; background: var(--accent-muted); color: var(--accent); }
.entity-card-list-marketplace .issue-list-card-icon .octicon { width: 28px; height: 28px; }
.entity-card-list-marketplace .issue-list-card-content { grid-column: 1; grid-row: 2; display: flex; flex-direction: column; align-items: flex-start; gap: 8px; }
.entity-card-list-marketplace .issue-list-card-title { color: var(--fg); font-size: 1.0625rem; }
.entity-card-list-marketplace .issue-list-card-title a { display: inline-block; }
.entity-card-list-marketplace .issue-list-card-subtitle { max-width: 100%; color: var(--muted); font-size: .875rem; line-height: 1.5; }
.entity-card-list-marketplace .entity-card-list-card:has([data-card-drill]) .issue-list-card-subtitle { display: -webkit-box; -webkit-box-orient: vertical; -webkit-line-clamp: 3; overflow: hidden; }
.entity-card-list-marketplace .issue-list-card-meta { max-width: 100%; justify-content: flex-start; margin-top: auto; padding-top: 4px; color: var(--muted); overflow-wrap: anywhere; }
.entity-card-list-marketplace .issue-list-card-meta dt { text-transform: none; }
.entity-card-list-marketplace .issue-list-labels { grid-column: 1; grid-row: 3; min-width: 0; max-width: 100%; margin: 0; padding-top: 12px; border-top: 1px solid var(--border-muted); justify-self: stretch; justify-content: flex-start; }
.entity-card-list-marketplace .issue-list-labels li { min-width: 0; max-width: 100%; }
.entity-card-list-marketplace .issue-list-labels .entity-card-list-ref { background: var(--canvas-subtle); border-color: var(--border); color: var(--muted); }
.entity-card-list-marketplace .entity-card-list-chevron { display: none; }
.entity-card-list-boundary { grid-column: 1 / -1; display: grid; justify-items: center; gap: 8px; padding: 16px; list-style: none; }
.entity-card-list-load-more { min-height: 44px; padding: 8px 20px; border: 1px solid var(--border); border-radius: 6px; background: var(--canvas-subtle); color: var(--fg); font: inherit; font-size: .875rem; cursor: pointer; }
.entity-card-list-load-more:focus-visible { outline: 2px solid var(--accent); outline-offset: 2px; }
.entity-card-list-load-more:disabled { cursor: progress; color: var(--muted); }
.entity-card-list-load-status { color: var(--muted); font-size: .75rem; text-align: center; }
.marketplace-detail-page .custom-view { border: 0; background: transparent; box-shadow: none; }
.marketplace-detail-page .custom-view > header { padding-inline: 0; }
.marketplace-detail-page .layout-section { padding: 0; border: 0; background: transparent; }
.marketplace-detail-page .layout-section > .layout-section-header { display: none; }
.marketplace-detail-page .entity-card-list-marketplace { display: block; }
.marketplace-detail-page .entity-card-list-marketplace .entity-card-list-card { min-height: 0; grid-template-columns: 56px minmax(0, 1fr) auto; grid-template-rows: auto auto; align-items: center; justify-items: start; gap: 4px 16px; padding: 20px 24px; text-align: left; }
.marketplace-detail-page .entity-card-list-marketplace .issue-list-card-icon { grid-column: 1; grid-row: 1 / span 2; }
.marketplace-detail-page .entity-card-list-marketplace .issue-list-card-content { grid-column: 2; grid-row: 1; justify-items: start; }
.marketplace-detail-page .entity-card-list-marketplace .issue-list-card-title { color: var(--fg); font-size: 1.5rem; font-weight: 600; }
.marketplace-detail-page .entity-card-list-marketplace .issue-list-card-subtitle { max-width: none; }
.marketplace-detail-page .entity-card-list-marketplace .issue-list-card-meta { justify-content: flex-start; margin-top: 0; }
.marketplace-detail-page .entity-card-list-marketplace .issue-list-labels { grid-column: 2; grid-row: 2; padding-top: 4px; border: 0; justify-self: start; justify-content: flex-start; }
.marketplace-detail-page .entity-card-list-marketplace .entity-card-list-actions { grid-column: 3; grid-row: 1 / span 2; align-self: center; margin: 0; }
.marketplace-detail-page .entity-card-list-actions .table-cli-action-button { min-height: 36px; padding: 6px 18px; border-color: var(--accent); background: var(--accent); color: var(--canvas); font-weight: 600; }
.marketplace-detail-page .entity-card-list-actions .table-cli-action-button:hover { filter: brightness(1.08); }
.marketplace-detail-page .entity-card-list-actions .table-cli-action-button > .octicon { color: inherit; }
.marketplace-detail-page .entity-card-list-actions .table-cli-action-button .cli-action-trigger-copy strong { font-size: .875rem; }
.marketplace-detail-page .dashboard-markdown { padding: 24px; border: 1px solid var(--border); border-radius: 6px; background: var(--canvas); }
.marketplace-detail-page .link-button-list { overflow: visible; border: 0; border-radius: 0; background: transparent; }
.marketplace-detail-page .link-button-list-view > header { padding: 0; }
.marketplace-detail-page .link-button-list-item > a { display: inline-flex; min-height: 36px; gap: 8px; padding: 6px 0; }
.marketplace-detail-page .link-button-list-item > a::after { display: none; }
.marketplace-detail-page .link-button-list-icon { width: 20px; height: 20px; background: transparent; }
.entity-card-list-chevron { display: flex; align-items: center; color: var(--muted); }
.entity-card-list-chevron .octicon { width: 14px; height: 14px; }
.link-button-list-view { display: grid; gap: 12px; }
.link-button-list-view > header { display: grid; gap: 4px; padding: 8px 16px; }
.link-button-list-view > header :is(h2, p) { margin: 0; }
.link-button-list-view > header h2 { font-size: .75rem; font-weight: 600; letter-spacing: .06em; text-transform: uppercase; color: var(--muted); }
.link-button-list-view > header p, .link-button-list-empty { color: var(--muted); font-size: .75rem; }
:is(.link-button-list,.link-button-list-skeleton) { overflow: hidden; margin: 0; padding: 0; border: 1px solid var(--border-muted); border-radius: 12px; background: var(--canvas-subtle); list-style: none; }
.link-button-list-item > a { position: relative; min-height: 44px; display: grid; grid-template-columns: 28px minmax(0, 1fr) auto 12px; align-items: center; gap: 12px; padding: 8px 16px; color: var(--fg); text-decoration: none; -webkit-tap-highlight-color: transparent; }
.link-button-list-item:not(:last-child) > a::after { content: ""; position: absolute; inset-inline: 56px 0; bottom: 0; height: 1px; background: var(--border-muted); }
.link-button-list-content { display: contents; }
.link-button-list-item > a:is(:hover, :active) { background: var(--neutral-muted); }
.link-button-list-item > a:focus-visible { outline: 2px solid var(--focus, Highlight); outline-offset: -3px; }
.link-button-list-icon { width: 28px; height: 28px; display: inline-flex; align-items: center; justify-content: center; border-radius: 8px; background: var(--accent-muted); color: var(--accent); }
.link-button-list-label { display: inline-flex; flex-wrap: wrap; align-items: center; gap: 8px; min-width: 0; }
.link-button-list-label-badge { padding: 2px 6px; border: 1px solid var(--border); border-radius: 6px; background: var(--neutral-muted); color: var(--muted); font-size: .75rem; line-height: 1.2; }
.link-button-list-indicator { grid-column: 3; display: inline-flex; color: var(--danger); }
.link-button-list-indicator .octicon { width: 16px; height: 16px; }
.link-button-list-chevron { grid-column: 4; display: inline-flex; color: color-mix(in srgb, var(--muted) 70%, transparent); }
.link-button-list-icon .octicon { width: 16px; height: 16px; }
.link-button-list-chevron .octicon { width: 12px; height: 12px; }
.link-button-list-empty { margin: 0; padding: 16px; border: 1px solid var(--border-muted); border-radius: 12px; background: var(--canvas-subtle); }
.link-button-list-skeleton-row { min-height: 44px; display: grid; grid-template-columns: 28px minmax(0, 1fr); align-items: center; gap: 12px; padding: 8px 16px; }
.link-button-list-skeleton-row > span { height: 16px; border-radius: 4px; background: linear-gradient(90deg, var(--canvas-subtle) 25%, var(--neutral-muted) 50%, var(--canvas-subtle) 75%); background-size: 200% 100%; animation: dashboard-skeleton-pulse 1.5s ease-in-out infinite; }
.link-button-list-skeleton-row > span:first-child { height: 28px; border-radius: 8px; }
.link-button-list-skeleton-row > span:last-child { width: 60%; }
.mobile-brand-name { display: none; }
.sidebar-collapsed { grid-template-columns: 64px minmax(0, 1fr); }
.sidebar-collapsed .org-sidebar { padding-inline: 8px 7px; }
.sidebar-collapsed .sidebar-header { flex-wrap: wrap; justify-content: center; gap: 0; margin-left: 0; }
.sidebar-collapsed .sidebar-brand { display: none; }
.sidebar-collapsed .sidebar-toggle { width: 32px; flex-basis: 32px; }
.sidebar-collapsed .nav-label, .sidebar-collapsed .nav-section-toggle, .sidebar-collapsed .experimental-page-label { display: none; }
.sidebar-collapsed .nav-section-items { display: flex !important; }
.sidebar-collapsed .primary-nav a { justify-content: center; gap: 0; padding-inline: 6px; }
.sidebar-collapsed .primary-nav a[aria-current="page"]::before { left: -8px; }
.primary-nav { min-height: 0; display: flex; flex: 1; flex-direction: column; gap: 2px; overflow-y: auto; scrollbar-width: thin; }
[data-experimental-navigation][hidden] { display: none !important; }
.nav-section { display: flex; flex-direction: column; }
.nav-section-toggle { min-height: 28px; display: flex; align-items: center; justify-content: space-between; gap: 8px; margin-top: 8px; padding: 4px 8px; border-radius: 6px; color: var(--muted); cursor: pointer; list-style: none; }
.nav-section:first-child > .nav-section-toggle { margin-top: 0; }
.nav-section-toggle::-webkit-details-marker { display: none; }
.nav-section-toggle:hover { background: var(--neutral-muted); color: var(--fg); }
.nav-section-toggle > .octicon { width: 12px; height: 12px; flex-basis: 12px; transition: transform 120ms ease; }
.nav-section[open] > .nav-section-toggle > .octicon { transform: rotate(90deg); }
.nav-section-label { min-width: 0; overflow: hidden; font-size: .6875rem; font-weight: 700; letter-spacing: .08em; text-overflow: ellipsis; text-transform: uppercase; white-space: nowrap; }
.nav-section-items { display: flex; flex-direction: column; gap: 2px; }
.nav-section:not([open]) > .nav-section-items { display: none; }
.primary-nav > .nav-section-bottom { min-height: max-content; flex: 0 0 auto; margin-top: auto; padding-top: 8px; border-top: 1px solid var(--border-muted); }
.sidebar-collapsed .nav-section-bottom::before { content: attr(data-nav-section); display: block; overflow-wrap: anywhere; padding: 4px 0; color: var(--muted); font-size: .5625rem; font-weight: 700; letter-spacing: .08em; line-height: 1.1; text-align: center; text-transform: uppercase; }
.primary-nav a, .nav-parent { min-height: 32px; display: flex; align-items: center; gap: 10px; position: relative; padding: 6px 8px; border-radius: 6px; color: var(--fg); font-weight: 500; text-decoration: none; transition: background-color 120ms ease, color 120ms ease; }
.primary-nav :is(a, .nav-parent) > .octicon { color: var(--muted); }
.primary-nav a:hover { background: var(--neutral-muted); }
.primary-nav a[aria-current="page"] { background: var(--neutral-muted); font-weight: 600; }
.primary-nav a[aria-current="page"]::before { content: ""; width: 3px; position: absolute; top: 5px; bottom: 5px; left: -16px; border-radius: 0 4px 4px 0; background: var(--accent); }
.experimental-page-label { display: inline-flex; flex: none; align-items: center; justify-content: center; min-height: 18px; padding: 2px; border: 1px solid color-mix(in srgb, var(--attention) 45%, var(--border)); border-radius: 2em; background: var(--attention-muted); color: var(--attention); font-size: .625rem; font-weight: 600; line-height: 1; white-space: nowrap; }
.nav-item .experimental-page-label, .mobile-nav-item .experimental-page-label { margin-left: auto; }
.org-sidebar :is([data-nav-page-id], [data-mobile-nav-page-id])[aria-disabled="true"] { color: var(--fg-muted); cursor: not-allowed; }
.experimental-page-label .octicon { width: 12px; height: 12px; flex: 0 0 12px; color: inherit; }
.experimental-page-label[hidden] { display: none; }
.nav-indicator { width: 8px; height: 8px; flex: 0 0 8px; margin-left: auto; border-radius: 50%; background: var(--accent); box-shadow: 0 0 0 2px var(--canvas-subtle); }
.nav-indicator[hidden] { display: none; }
.sidebar-collapsed .nav-indicator { position: absolute; top: 6px; right: 7px; margin-left: 0; }
.mobile-nav-menu { display: none; }
.mobile-nav-menu-indicator { position: absolute; top: 6px; right: 6px; margin-left: 0; }
.app-main { min-width: 0; height: 100vh; display: flex; flex-direction: column; overflow: hidden; }
.app-main > .top-nav { position: relative; z-index: 20; border-bottom: 1px solid var(--border); }
.app-main > .top-nav .shell { display: flex; align-items: center; gap: 24px; width: 100%; padding: 14px var(--dashboard-page-padding-inline); }
.breadcrumb-context { min-height: 18px; display: flex; align-items: center; gap: 8px; color: var(--muted); font-size: .75rem; }
.breadcrumb-context:not(:has(> :not([hidden]))) { visibility: hidden; }
.breadcrumb-context > a:not([hidden]) { min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.breadcrumb-context > :not([hidden]) ~ :not([hidden])::before { content: "/"; margin-right: 8px; color: var(--muted); }
.breadcrumb-context:has([data-route-breadcrumb]) { flex-wrap: wrap; }
.breadcrumb-context > [data-route-breadcrumb] { min-width: 0; overflow-wrap: anywhere; }
.report-actions { margin-left: auto; display: flex; align-items: center; gap: 10px; }
.cli-actions-menu { position: relative; flex: none; }
.cli-actions-menu > summary { list-style: none; }
.cli-actions-menu > summary::-webkit-details-marker { display: none; }
.cli-actions-toggle { min-height: 28px; display: inline-flex; align-items: center; gap: 6px; padding: 3px 10px; border: 1px solid var(--border); border-radius: 6px; background: var(--canvas-subtle); color: var(--fg); font-size: .75rem; font-weight: 600; cursor: pointer; }
.cli-commands-navigation[hidden] { display: none; }
.cli-actions-toggle:hover, .cli-actions-menu[open] .cli-actions-toggle { background: var(--neutral-muted); }
.cli-actions-toggle .octicon { width: 14px; height: 14px; }
.cli-actions-list { width: min(320px, calc(100vw - 28px)); display: grid; gap: 4px; position: absolute; z-index: 50; top: calc(100% + 8px); right: 0; padding: 6px; border: 1px solid var(--border); border-radius: 8px; background: var(--canvas); box-shadow: 0 8px 24px color-mix(in srgb, var(--canvas-inset) 45%, transparent); }
.cli-action-trigger { width: 100%; min-width: 0; display: flex; align-items: flex-start; gap: 9px; padding: 9px; border: 0; border-radius: 6px; background: transparent; color: var(--fg); font: inherit; text-align: left; cursor: pointer; }
.cli-action-trigger:hover { background: var(--neutral-muted); }
.cli-action-trigger:focus-visible, .cli-action-cancel:focus-visible, .cli-action-confirm:focus-visible { outline: 2px solid var(--focus); outline-offset: 2px; }
.cli-action-trigger > .octicon { width: 16px; height: 16px; flex: 0 0 16px; margin-top: 2px; color: var(--muted); }
.cli-action-trigger-copy { min-width: 0; display: grid; gap: 2px; }
.cli-action-trigger-copy strong { font-size: .8125rem; }
.cli-action-trigger-copy small { color: var(--muted); font-size: .6875rem; line-height: 1.35; }
.configuration-cli-action-list { display: grid; gap: 8px; }
.configuration-cli-action { align-items: center; border: 1px solid var(--border); background: var(--canvas); }
.configuration-cli-action:hover { background: var(--neutral-muted); }
.document-list-header { display: flex; align-items: center; justify-content: space-between; gap: 16px; margin-bottom: 12px; }
.document-list-header > p { min-width: 0; flex: 1; margin: 0; overflow-wrap: anywhere; color: var(--muted); font-size: .75rem; }
.document-list-header .declared-cli-action { flex: none; }
.document-list-header .cli-action-trigger { min-height: 32px; align-items: center; padding: 6px 10px; border: 1px solid var(--accent); background: var(--accent); color: var(--canvas); }
.document-list-header .cli-action-trigger:hover { border-color: var(--accent); background: color-mix(in srgb, var(--accent) 88%, var(--fg)); color: var(--canvas); }
.document-list-header .cli-action-trigger > .octicon { color: inherit; }
.document-list-header .cli-action-trigger-copy small { display: none; }
.document-list { overflow: hidden; display: grid; margin: 0; padding: 0; border: 1px solid var(--border); border-radius: 0; background: var(--canvas); list-style: none; }
.document-list-card { min-width: 0; display: grid; grid-template-columns: auto minmax(0, 1fr) auto; align-items: center; gap: 12px; padding: 14px 16px; border-top: 1px solid var(--border); }
.document-list-card:first-child { border-top: 0; }
.document-list-card-icon { width: 32px; height: 32px; display: grid; place-items: center; border-radius: 6px; background: var(--neutral-muted); color: var(--muted); }
.document-list-card-icon .octicon { width: 16px; height: 16px; }
.document-list-card-content { min-width: 0; display: grid; gap: 6px; }
.document-list-card-title { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-size: .8125rem; }
.document-list-card-details { display: flex; flex-wrap: wrap; gap: 8px 16px; margin: 0; }
.document-list-card-details > div { display: contents; }
.document-list-card-details dt { color: var(--muted); font-size: .6875rem; }
.document-list-card-details dd { margin: 0 8px 0 -11px; font-size: .6875rem; font-weight: 600; font-variant-numeric: tabular-nums; }
.document-list-card-actions { display: flex; flex-wrap: wrap; align-items: center; justify-content: flex-end; gap: 6px; }
.document-list-card .table-cli-action-button { width: auto; min-height: 32px; padding: 7px; border: 1px solid var(--border); }
.document-list-empty { margin: 0; padding: 16px; color: var(--muted); font-size: .75rem; }
.document-list-footer { display: flex; justify-content: flex-end; padding: 8px 14px; }
.document-list-footer a { display: inline-flex; align-items: center; gap: 5px; font-weight: 600; }
.issue-list { display: block; }
.issue-list-card { min-width: 0; display: grid; grid-template-columns: 20px minmax(0, 1fr) auto; gap: 8px; padding: 8px 16px; border-top: 1px solid var(--border); list-style: none; } /* .entity-card-list-actions (defined above) assumes column 2 is the content column; keep its grid-column value in sync if this column layout changes */
.issue-list-card:first-child { border-top: 0; }
.issue-list-card:hover { background: var(--canvas-subtle); }
.entity-card-list-card:has([data-card-drill]) { cursor: pointer; }
.issue-list-card-icon { padding-top: 2px; color: var(--success); }
.issue-list-card-icon .octicon { width: 16px; height: 16px; }
.issue-list-card-content { min-width: 0; display: grid; gap: 4px; }
.issue-list-card-title { min-width: 0; color: var(--fg); font-size: .875rem; font-weight: 600; line-height: 1.4; overflow-wrap: anywhere; }
.issue-list-card-title a { color: inherit; text-decoration: none; }
.issue-list-card-title a:hover { color: var(--accent); text-decoration: underline; text-underline-offset: 2px; }
.issue-list-card-title a .octicon { width: 12px; height: 12px; color: var(--muted); }
.issue-list-card-subtitle { min-width: 0; color: var(--muted); font-size: .75rem; line-height: 1.4; overflow-wrap: anywhere; }
.issue-list-card-meta { display: flex; flex-wrap: wrap; gap: 2px 6px; margin: 0; color: var(--muted); font-size: .75rem; }
.issue-list-card-meta > div { display: contents; }
.issue-list-card-meta dt { position: absolute; width: 1px; height: 1px; padding: 0; overflow: hidden; clip: rect(0, 0, 0, 0); white-space: nowrap; border: 0; }
.issue-list-card-meta dd { margin: 0; }
.issue-list-card-meta dd:not(:last-child)::after { margin-left: 6px; color: var(--muted); content: "·"; }
.issue-list-card-meta a { color: inherit; }
.issue-list-card-meta-labelled { gap: 2px 12px; }
.issue-list-card-meta-labelled > div { display: inline-flex; align-items: baseline; gap: 4px; }
.issue-list-card-meta-labelled dt { position: static; width: auto; height: auto; overflow: visible; clip: auto; color: var(--muted); }
.issue-list-card-meta-labelled dt::after { content: ":"; }
.issue-list-card-meta-labelled dd { color: var(--fg); }
.issue-list-card-meta-labelled dd:not(:last-child)::after { content: none; }
.issue-list-labels { display: flex; flex-wrap: wrap; align-items: center; justify-content: flex-end; gap: 6px; margin: 2px 0 0; padding: 0; list-style: none; }
.issue-list-labels li { max-width: 220px; padding: 0 9px; overflow: hidden; border: 1px solid var(--accent-muted); border-radius: 999px; background: var(--accent-muted); color: var(--accent); font-size: .6875rem; font-weight: 600; line-height: 18px; text-align: center; text-overflow: ellipsis; white-space: nowrap; }
.cli-action-dialog { width: min(720px, calc(100vw - 32px)); max-width: none; max-height: calc(100vh - 32px); height: fit-content; margin: auto; padding: 0; overflow: hidden; border: 1px solid var(--border); border-radius: 8px; background: var(--canvas); box-shadow: 0 16px 48px color-mix(in srgb, var(--canvas-inset) 70%, transparent); color: var(--fg); text-align: left; white-space: normal; }
.cli-action-dialog[open] { display: grid; grid-template-rows: auto minmax(0, 1fr) auto; }
.cli-action-dialog::backdrop { background: color-mix(in srgb, var(--canvas-inset) 72%, transparent); }
.cli-action-dialog-header { display: flex; align-items: center; justify-content: space-between; gap: 16px; padding: 14px 16px; border-bottom: 1px solid var(--border); background: var(--canvas-subtle); }
.cli-action-dialog-header h2 { margin: 0; font-size: 1rem; }
.command-terminal { position: relative; min-height: 0; flex: 1; display: flex; flex-direction: column; overflow: hidden; }
.command-terminal > .cli-action-dialog-body { min-height: 0; flex: 1; padding: 0; overflow: hidden; }
.command-terminal .cli-action-terminal-output { min-height: 0; height: 100%; }
.command-terminal .cli-action-terminal { min-height: 0; height: 100%; border: 0; border-radius: 0; }
.command-terminal-actions { position: absolute; right: 24px; bottom: 24px; z-index: 2; display: flex; gap: 8px; }
.command-terminal-actions[hidden] { display: none; }
.command-terminal-actions > :is(button, a) { min-height: 44px; box-shadow: 0 4px 12px color-mix(in srgb, var(--canvas-inset) 45%, transparent); }
.cli-action-dialog-close { width: 28px; height: 28px; display: grid; place-items: center; padding: 0; border: 0; border-radius: 6px; background: transparent; color: var(--muted); cursor: pointer; }
.cli-action-dialog-close:hover { background: var(--neutral-muted); color: var(--fg); }
.cli-action-dialog-body { min-height: 0; display: flex; flex-direction: column; gap: 12px; padding: 18px 16px; overflow-y: auto; line-height: 1.5; }
.cli-action-dialog-body p { margin: 0; }
.cli-action-arguments { display: grid; gap: 8px; margin: 0; padding: 12px; border: 1px solid var(--border); border-radius: 6px; }
.cli-action-arguments legend { padding: 0 4px; color: var(--muted); font-size: .75rem; font-weight: 600; }
.cli-action-argument { display: flex; align-items: flex-start; gap: 9px; cursor: pointer; }
.cli-action-argument input { margin-top: 3px; }
.cli-action-argument > span { display: grid; gap: 2px; }
.cli-action-argument strong { font-size: .8125rem; }
.cli-action-argument small { color: var(--muted); font-size: .75rem; }
.cli-action-command, .cli-action-preview-command, .cli-action-output { overflow: auto; padding: 10px 12px; border: 1px solid var(--border); border-radius: 6px; background: var(--canvas-inset); color: var(--fg); font-family: var(--font-mono, ui-monospace, monospace); font-size: .75rem; white-space: pre-wrap; word-break: break-word; }
.cli-action-command-details > summary { cursor: pointer; }
.cli-action-command-details > .cli-action-command { display: block; margin-top: 8px; }
.cli-action-output { min-height: 160px; flex: 1 1 auto; margin: 0; }
.cli-action-terminal { height: min(360px, 45dvh); min-height: 160px; flex: 0 0 auto; overflow: hidden; border: 1px solid var(--border); border-radius: 6px; background: #0d1117; }
.cli-action-terminal[hidden] { display: none; }
.cli-action-dialog.cli-action-running { inset: 0; width: 100vw; height: 100dvh; max-width: 100vw; max-height: 100dvh; margin: 0; border: 0; border-radius: 0; grid-template-rows: auto minmax(0, 1fr) auto; }
.cli-action-running > .cli-action-dialog-body { gap: 0; padding: 0; overflow: hidden; }
.cli-action-running > .cli-action-dialog-body > :not(.cli-action-terminal-output):not(.cli-action-terminal) { display: none; }
.cli-action-running .cli-action-terminal-output { min-height: 0; height: 100%; }
.cli-action-running .cli-action-terminal { min-height: 0; height: 100%; border: 0; border-radius: 0; }
.cli-action-dialog-footer { min-height: 58px; display: flex; align-items: center; justify-content: flex-end; gap: 8px; padding: 10px 16px; border-top: 1px solid var(--border); background: var(--canvas-subtle); }
.cli-action-status, .cli-action-terminal-status { min-width: 0; flex: 1; color: var(--muted); font-size: .75rem; }
.cli-action-cancel, .cli-action-confirm { min-height: 34px; display: inline-flex; align-items: center; gap: 7px; padding: 5px 12px; border: 1px solid var(--border); border-radius: 6px; font: inherit; font-weight: 600; cursor: pointer; }
.cli-action-cancel[hidden], .cli-action-confirm[hidden] { display: none; }
.cli-action-cancel { background: var(--canvas); color: var(--fg); }
.command-terminal-actions > .cli-action-cancel { border-color: var(--accent); background: var(--accent); color: var(--canvas); }
.cli-action-confirm { border-color: var(--accent); background: var(--accent); color: var(--canvas); }
.cli-action-cancel:hover { background: var(--neutral-muted); }
.command-terminal-actions > .cli-action-cancel:hover { background: var(--accent); filter: brightness(1.08); }
.cli-action-confirm:hover { filter: brightness(1.08); }
.cli-action-cancel:disabled, .cli-action-confirm:disabled { cursor: default; opacity: .6; }
.dashboard-horizon { position: relative; max-width: none; flex: none; display: flex; align-items: center; margin-bottom: 0; padding-right: 6px; border-right: 1px solid var(--border); color: var(--muted); font-size: .75rem; font-weight: 600; }
.dashboard-horizon.filter-bar-expanded > .filter-tuning-controls { z-index: 30; display: grid; grid-template-columns: minmax(0, 1fr); left: auto; right: 0; top: calc(100% + 8px); width: min(280px, calc(100vw - 28px)); max-height: calc(100dvh - 100px); overflow-y: auto; padding: 12px; border: 1px solid var(--border); border-radius: 6px; }
.dashboard-horizon.filter-bar-expanded .horizon-details { display: block; font-weight: 400; }
.dashboard-horizon .horizon-details > summary { color: var(--muted); font-size: .6875rem; cursor: pointer; }
.dashboard-horizon .horizon-details[open] > span { display: flex; margin-top: 6px; }
.dashboard-horizon .horizon-details-values { flex-wrap: wrap; justify-content: flex-start; gap: 4px 12px; }
.horizon-summary { display: flex; align-items: center; position: relative; }
.horizon-toggle { width: 28px; height: 28px; display: grid; flex: 0 0 28px; place-items: center; padding: 0; border: 0; border-radius: 6px; background: transparent; color: inherit; font: inherit; cursor: pointer; }
.horizon-toggle:hover, .horizon-toggle[aria-expanded="true"] { background: var(--neutral-muted); color: var(--fg); }
.horizon-toggle .octicon { width: 14px; height: 14px; }
.horizon-summary .horizon-tooltip { width: auto; min-width: 190px; display: grid; gap: 3px; position: absolute; z-index: 40; top: calc(100% + 8px); right: 0; padding: 9px 11px; border: 1px solid var(--border); border-radius: 6px; background: var(--canvas); box-shadow: 0 8px 24px color-mix(in srgb, var(--canvas-inset) 45%, transparent); color: var(--fg); font-size: .75rem; font-weight: 600; line-height: 1.35; white-space: nowrap; visibility: hidden; opacity: 0; pointer-events: none; transition: opacity 80ms linear, visibility 80ms linear; }
.horizon-tooltip > span { color: var(--muted); font-size: .6875rem; font-weight: 400; }
.horizon-summary:hover .horizon-tooltip, .horizon-summary:focus-within .horizon-tooltip { visibility: visible; opacity: 1; }
.filter-bar-expanded :is(.horizon-summary:hover, .horizon-summary:focus-within) .horizon-tooltip { visibility: hidden; opacity: 0; }
.horizon-details { display: none; }
.filter-bar-expanded .horizon-details { width: 100%; display: block; padding-top: 8px; border-top: 1px solid var(--border); }
.horizon-details-description { color: var(--muted); font-size: .75rem; }
.horizon-details-values { display: flex; align-items: center; justify-content: flex-end; gap: 12px; color: var(--muted); font-size: .6875rem; }
.horizon-details-values > span { display: flex; align-items: center; gap: 5px; }
.horizon-details-values strong { color: var(--fg); font-weight: 600; }
.dashboard-horizon-skeleton > span { width: 28px; height: 28px; border-radius: 6px; background: linear-gradient(90deg, var(--canvas-subtle) 25%, var(--neutral-muted) 50%, var(--canvas-subtle) 75%); background-size: 200% 100%; animation: dashboard-skeleton-pulse 1.5s ease-in-out infinite; }
.tooltip-help { position: relative; display: inline-flex; }
.tooltip-trigger { width: 24px; height: 24px; display: grid; place-items: center; padding: 0; border: 0; border-radius: 50%; background: transparent; color: var(--muted); cursor: help; }
.tooltip-trigger:hover { background: var(--neutral-muted); color: var(--fg); }
.tooltip-trigger .octicon { width: 14px; height: 14px; }
.dashboard-current-status .tooltip-trigger { color: var(--muted); }
.dashboard-current-status[data-tone="success"] .tooltip-trigger { color: var(--success); }
.dashboard-current-status[data-tone="attention"] .tooltip-trigger { color: var(--attention); }
.dashboard-current-status[data-tone="danger"] .tooltip-trigger { color: var(--danger); }
.dashboard-current-status[hidden] { display: none; }
.dashboard-current-status-refreshing .tooltip-trigger { color: var(--muted); }
.dashboard-current-status-refreshing .octicon-sync { animation: dashboard-refresh-spin 1s linear infinite; }
.dashboard-ingestion-status { display: grid; gap: 6px; margin-top: 20px; font-size: .75rem; }
.dashboard-ingestion-status-heading { display: flex; align-items: center; flex-wrap: wrap; gap: 8px; }
.dashboard-ingestion-status-heading .octicon { width: 16px; height: 16px; color: var(--muted); }
.dashboard-ingestion-status[data-tone="success"] .octicon { color: var(--success); }
.dashboard-ingestion-status[data-tone="attention"] .octicon { color: var(--attention); }
.dashboard-ingestion-status[data-tone="danger"] .octicon { color: var(--danger); }
.dashboard-ingestion-status p { margin: 0; color: var(--muted); line-height: 1.5; }
@keyframes dashboard-refresh-spin { to { transform: rotate(360deg); } }
@media (prefers-reduced-motion: reduce) {
  .dashboard-current-status-refreshing .octicon-sync { animation: none; }
}
.tooltip-content { width: min(320px, calc(100vw - 28px)); position: absolute; z-index: 20; top: calc(100% + 8px); right: 0; display: grid; gap: 10px; padding: 12px; border: 1px solid var(--border); border-radius: 6px; background: var(--canvas); box-shadow: 0 8px 24px color-mix(in srgb, var(--canvas-inset) 45%, transparent); color: var(--fg); font-weight: 400; line-height: 1.4; white-space: normal; visibility: hidden; opacity: 0; pointer-events: none; transition: opacity 80ms linear, visibility 80ms linear; }
.tooltip-help:hover .tooltip-content, .tooltip-help:focus-within .tooltip-content { visibility: visible; opacity: 1; }
.tooltip-content.tooltip-content-viewport { position: fixed; }
.tooltip-description { color: var(--muted); }
.refresh-button { display: inline-flex; align-items: center; gap: 6px; min-height: 28px; padding: 3px 12px; border: 1px solid var(--border); border-radius: 6px; background: var(--canvas-subtle); color: var(--fg); font: inherit; font-size: .75rem; font-weight: 500; text-decoration: none; cursor: pointer; transition: background-color 120ms ease; }
.refresh-button:hover { background: var(--neutral-muted); }
.refresh-button .octicon { width: 14px; height: 14px; }
.theme-control-options { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); width: min(100%, 320px); }
.theme-control-options button { min-width: 0; min-height: 34px; display: flex; align-items: center; justify-content: center; gap: 5px; padding: 5px 8px; border: 1px solid var(--border); background: var(--canvas); color: var(--fg); font: inherit; font-size: .75rem; cursor: pointer; }
.theme-control-options button:first-child { border-radius: 6px 0 0 6px; }
.theme-control-options button + button { margin-left: -1px; }
.theme-control-options button:last-child { border-radius: 0 6px 6px 0; }
.theme-control-options button[aria-pressed="true"] { position: relative; z-index: 1; border-color: var(--accent); background: var(--accent-muted); color: var(--accent); }
.theme-control-options button:focus-visible { position: relative; z-index: 2; outline: 2px solid var(--focus); outline-offset: 1px; }
.theme-control-options .octicon { width: 14px; height: 14px; }
.repository-link { width: 28px; height: 28px; display: grid; flex: 0 0 28px; place-items: center; border-radius: 6px; color: var(--muted); text-decoration: none; transition: background-color 120ms ease, color 120ms ease; }
.repository-link:hover { background: var(--neutral-muted); color: var(--fg); }
.repository-link .octicon { width: 18px; height: 18px; }
.account-menu { position: relative; flex: 0 0 auto; }
.account-menu > summary { list-style: none; }
.account-menu > summary::-webkit-details-marker { display: none; }
.account-menu-avatar { width: 30px; height: 30px; display: grid; place-items: center; padding: 0; border: 1px solid var(--border); border-radius: 50%; background: var(--accent-muted); color: var(--accent); cursor: pointer; }
.account-menu-avatar:hover, .account-menu[open] .account-menu-avatar { box-shadow: 0 0 0 2px var(--accent-muted); }
.account-menu-avatar:focus-visible { outline: 2px solid var(--accent); outline-offset: 2px; }
.account-menu-avatar .octicon { width: 16px; height: 16px; }
.account-menu-avatar-fallback { display: grid; place-items: center; }
.account-menu-avatar-fallback[hidden], .account-menu-avatar-image[hidden] { display: none; }
.account-menu-avatar-image { width: 100%; height: 100%; display: block; border-radius: inherit; object-fit: cover; }
.account-menu-icon { width: 28px; height: 28px; border: 0; border-radius: 6px; background: transparent; color: var(--muted); }
.account-menu-icon:hover, .account-menu[open] .account-menu-icon { background: var(--neutral-muted); color: var(--fg); box-shadow: none; }
.account-menu-popover { width: 260px; display: grid; gap: 8px; position: absolute; z-index: 50; top: calc(100% + 8px); right: 0; padding: 12px; border: 1px solid var(--border); border-radius: 6px; background: var(--canvas); box-shadow: 0 8px 24px color-mix(in srgb, var(--canvas-inset) 45%, transparent); }
.account-menu-heading { color: var(--muted); font-size: .75rem; font-weight: 600; letter-spacing: .04em; text-transform: uppercase; }
.account-menu-action { width: 100%; min-height: 34px; display: flex; align-items: center; gap: 9px; padding: 6px 8px; border: 0; border-radius: 6px; background: transparent; color: var(--fg); font: inherit; font-size: .8125rem; font-weight: 500; text-align: left; text-decoration: none; cursor: pointer; }
.account-menu-action:hover { background: var(--neutral-muted); }
.account-menu-action .octicon { width: 15px; height: 15px; color: var(--muted); }
.reset-dashboard-dialog { width: min(480px, calc(100vw - 32px)); max-width: none; max-height: calc(100vh - 32px); height: fit-content; margin: auto; padding: 0; overflow: hidden; border: 1px solid var(--border); border-radius: 8px; background: var(--canvas); box-shadow: 0 16px 48px color-mix(in srgb, var(--canvas-inset) 70%, transparent); color: var(--fg); }
.reset-dashboard-dialog[open] { display: grid; grid-template-rows: auto minmax(0, 1fr) auto; }
.reset-dashboard-dialog::backdrop { background: color-mix(in srgb, var(--canvas-inset) 72%, transparent); }
.reset-dashboard-dialog-header { display: flex; align-items: center; justify-content: space-between; gap: 16px; padding: 14px 16px; border-bottom: 1px solid var(--border); background: var(--canvas-subtle); }
.reset-dashboard-dialog-header h2 { margin: 0; font-size: 1rem; }
.reset-dashboard-dialog-close { width: 28px; height: 28px; display: grid; flex: 0 0 28px; place-items: center; padding: 0; border: 0; border-radius: 6px; background: transparent; color: var(--muted); cursor: pointer; }
.reset-dashboard-dialog-close:hover { background: var(--neutral-muted); color: var(--fg); }
.reset-dashboard-dialog-body { min-height: 0; display: grid; align-content: start; gap: 8px; padding: 18px 16px; overflow-y: auto; line-height: 1.5; }
.reset-dashboard-dialog-body p { margin: 0; }
.reset-dashboard-dialog-body strong { color: var(--danger); }
.reset-dashboard-dialog-footer { min-height: 58px; display: flex; align-items: center; justify-content: flex-end; gap: 8px; padding: 10px 16px; border-top: 1px solid var(--border); background: var(--canvas-subtle); }
.reset-dashboard-status { min-width: 0; flex: 1; color: var(--danger); font-size: .75rem; }
.reset-dashboard-cancel, .reset-dashboard-confirm { min-height: 34px; padding: 5px 12px; border: 1px solid var(--border); border-radius: 6px; font: inherit; font-weight: 600; cursor: pointer; }
.reset-dashboard-cancel { background: var(--canvas); color: var(--fg); }
.reset-dashboard-confirm { border-color: var(--danger); background: var(--danger); color: var(--canvas); }
.reset-dashboard-cancel:hover { background: var(--neutral-muted); }
.reset-dashboard-confirm:hover { filter: brightness(1.08); }
.reset-dashboard-cancel:disabled, .reset-dashboard-confirm:disabled { cursor: default; opacity: .6; }
main.dashboard-prototype { width: 100%; min-height: 0; flex: 1; overflow-y: auto; overscroll-behavior: contain; scrollbar-gutter: stable; padding: 24px var(--dashboard-page-padding-inline) 40px; }
.overview-pull-refresh { width: max-content; max-width: calc(100% - 32px); position: sticky; top: 0; z-index: 10; margin: -16px auto 8px; padding: 6px 12px; border: 1px solid var(--border); border-radius: 6px; background: var(--canvas); color: var(--muted); font-size: .75rem; font-weight: 600; text-align: center; box-shadow: 0 2px 8px color-mix(in srgb, var(--fg) 12%, transparent); }
.overview-pull-refresh-armed { color: var(--accent); border-color: var(--accent); }
.lede { color: var(--muted); }
.overview-header { min-width: 0; flex: 1; }
.overview-header h1 { margin: 0; font-size: 1.25rem; font-weight: 500; line-height: 1.25; }
.overview-header .lede { min-height: 1.25rem; margin: 3px 0 0; overflow: hidden; font-size: .875rem; line-height: 1.25rem; text-overflow: ellipsis; white-space: nowrap; }
.overview-header .lede[hidden] { display: block !important; visibility: hidden; }
.title-area { display: flex; align-items: center; gap: 8px; }
.title-link { width: 28px; height: 28px; display: grid; flex: 0 0 28px; place-items: center; border-radius: 6px; color: var(--muted); text-decoration: none; transition: background-color 120ms ease, color 120ms ease; }
.title-link:hover { background: var(--neutral-muted); color: var(--fg); }
.title-link:focus-visible { outline: 2px solid var(--focus); outline-offset: 2px; }
.title-link .octicon { width: 18px; height: 18px; }
.title-link[hidden] { display: none; }
.toolbar { display: flex; align-items: center; gap: 8px; margin-bottom: 16px; }
.report-actions > .filter-bar { position: relative; margin-bottom: 0; }
.filter-tuning-controls { display: none; }
.page-chrome { min-height: 44px; display: flex; align-items: center; gap: 8px; margin-bottom: 16px; padding-bottom: 12px; border-bottom: 1px solid var(--border); }
.page-chrome > .filter-bar { min-width: 0; flex: 1; justify-content: flex-end; margin-bottom: 0; }
.page-chrome > .filter-bar .filter-tuning-controls { min-width: 0; display: flex; flex: 1; align-items: center; gap: 8px; }
.dashboard-parameter-form { display: grid; gap: 12px; margin: 0 0 16px; padding: 16px; border: 1px solid var(--border); border-radius: 6px; background: var(--canvas-subtle); }
.dashboard-parameter-form-header { display: grid; gap: 4px; }
.dashboard-parameter-form-header :is(h2, p) { margin: 0; }
.dashboard-parameter-form-header h2 { font-size: 1rem; }
.dashboard-parameter-form-header p, .dashboard-parameter-description { color: var(--muted); font-size: .75rem; }
.dashboard-parameter-fields { display: grid; grid-template-columns: repeat(auto-fit, minmax(min(100%, 220px), 1fr)); gap: 12px 16px; align-items: start; }
.dashboard-parameter-field { min-width: 0; display: grid; gap: 6px; }
.dashboard-parameter-label { display: flex; justify-content: space-between; gap: 8px; font-weight: 600; }
.dashboard-parameter-slider input[type="range"] { width: 100%; }
.dashboard-parameter-checkbox label, .dashboard-parameter-radio-options label { display: inline-flex; align-items: center; gap: 8px; min-height: 32px; }
.dashboard-parameter-radio { margin: 0; padding: 0; border: 0; }
.dashboard-parameter-radio legend { padding: 0; font-weight: 600; }
.dashboard-parameter-radio-options { display: flex; flex-wrap: wrap; gap: 4px 16px; }
.view-mode-control { display: inline-flex; flex: none; align-items: stretch; overflow: hidden; border: 1px solid var(--border); border-radius: 6px; background: var(--canvas); }
.view-mode-option { min-height: 32px; display: inline-flex; align-items: center; gap: 7px; padding: 5px 10px; border: 0; border-right: 1px solid var(--border); background: transparent; color: var(--fg); font: inherit; font-size: .8125rem; font-weight: 500; cursor: pointer; }
.view-mode-option:last-child { border-right: 0; }
.view-mode-option:hover { background: var(--neutral-muted); }
.view-mode-option[aria-pressed="true"] { background: var(--neutral-muted); box-shadow: inset 0 0 0 1px var(--border-muted); font-weight: 600; }
.view-mode-option .octicon { color: var(--muted); }
.view-mode-option[aria-pressed="true"] .octicon { color: var(--fg); }
.report-actions > .dashboard-horizon.filter-bar-expanded { position: relative; }
.filter-bar-expanded .filter-tuning-controls { width: 100%; display: flex; flex-wrap: wrap; align-items: stretch; gap: 8px; position: absolute; z-index: 30; top: 100%; right: 0; left: 0; padding: 10px max(14px, calc((100% - 920px) / 2)); border: 1px solid var(--border); border-width: 0 0 1px; background: var(--canvas); box-shadow: 0 8px 24px color-mix(in srgb, var(--canvas-inset) 45%, transparent); animation: horizon-panel-drop 160ms ease-out; }
@keyframes horizon-panel-drop {
  from { transform: translateY(-10px); opacity: 0; }
  to { transform: translateY(0); opacity: 1; }
}
.filter-control { min-width: 240px; min-height: 30px; display: flex; flex: 1; align-items: stretch; position: relative; border: 1px solid var(--border); border-radius: 6px; background: var(--canvas); font-size: .75rem; }
.filter-control:focus-within { outline: 2px solid var(--focus); outline-offset: -2px; }
.scope-label, .scope-period, .search-control { display: inline-flex; align-items: center; gap: 7px; padding: 4px 12px; }
.scope-label { border-right: 1px solid var(--border); }
.filter-toggle { border-block: 0; border-left: 0; background: transparent; color: inherit; font: inherit; cursor: pointer; }
.count-badge { align-self: center; display: inline-flex; min-width: 20px; height: 20px; padding: 0 6px; align-items: center; justify-content: center; border-radius: 2em; background: var(--neutral-muted); font-size: .6875rem; text-align: center; line-height: 1; }
.filter-control > .count-badge { margin: 0 9px 0 3px; }
.filter-control input { min-width: 0; flex: 1; padding: 5px 12px; border: 0; outline: 0; background: transparent; color: var(--accent); font: inherit; }
.search-control { padding-inline: 9px; border-left: 1px solid var(--border); color: var(--muted); }
.scope-period { min-height: 30px; border: 1px solid var(--border); border-radius: 6px; background: var(--canvas-subtle); color: var(--fg); font-size: .75rem; font-weight: 600; white-space: nowrap; }
.time-window-control { width: 100%; min-width: 0; display: none; flex-wrap: wrap; align-items: stretch; gap: 6px; }
.filter-bar-expanded .time-window-control { display: flex; }
.time-window-control label { display: inline-flex; align-items: center; gap: 7px; min-height: 30px; padding: 3px 8px; border: 1px solid var(--border); border-radius: 6px; background: var(--canvas-subtle); color: var(--muted); font-size: .6875rem; font-weight: 600; white-space: nowrap; }
.time-window-control label:focus-within { outline: 2px solid var(--focus); outline-offset: -2px; }
.mode-filter-control { min-width: 0; display: flex; align-items: stretch; gap: 2px; margin: 0; padding: 0; border: 0; }
.mode-filter-control legend { position: absolute; width: 1px; height: 1px; padding: 0; overflow: hidden; clip: rect(0, 0, 0, 0); white-space: nowrap; border: 0; }
.mode-filter-control label { cursor: pointer; text-transform: capitalize; }
.time-window-control .mode-filter-control input { width: auto; flex: none; accent-color: var(--accent); }
.time-window-control :is(select, input) { min-width: 0; border: 0; outline: 0; background: transparent; color: var(--fg); font: inherit; font-weight: 600; }
.time-window-control select { max-width: 132px; }
.time-window-control input { width: 132px; }
.time-window-control input[aria-invalid="true"] { color: var(--danger); }
.time-window-custom { display: flex; flex-wrap: wrap; gap: 6px; }
.time-window-custom[hidden] { display: none; }
.time-window-custom > button { min-height: 30px; padding: 0 12px; border: 1px solid var(--border); border-radius: 6px; background: var(--canvas-subtle); color: var(--fg); font: inherit; font-size: .75rem; font-weight: 700; cursor: pointer; }
.time-window-custom > button:hover { background: var(--border-muted); }
.dashboard-horizon .time-window-control { display: grid; grid-template-columns: minmax(0, 1fr); gap: 10px; }
.dashboard-horizon .time-window-control label { min-width: 0; white-space: normal; font-size: .75rem; font-weight: 400; }
.dashboard-horizon .time-window-preset { padding: 0; }
.dashboard-horizon .time-window-preset select { width: 100%; max-width: none; min-height: 34px; padding: 6px 8px; font-size: .8125rem; font-weight: 400; }
.dashboard-horizon .mode-filter-control { flex-wrap: wrap; gap: 4px 10px; }
.dashboard-horizon .mode-filter-control label { padding: 0; border: 0; background: transparent; }
.dashboard-horizon .time-window-custom { min-width: 0; display: grid; grid-template-columns: minmax(0, 1fr); gap: 8px; }
.dashboard-horizon .time-window-custom[hidden] { display: none; }
.dashboard-horizon .time-window-custom label { display: grid; grid-template-columns: minmax(0, 1fr); gap: 4px; padding: 6px 8px; }
.dashboard-horizon .time-window-custom input { width: 100%; max-width: 100%; font-size: .75rem; font-weight: 400; }
.dashboard-horizon .time-window-custom > button { justify-self: end; min-height: 34px; }
.dashboard-horizon .time-window-custom [role="alert"] { color: var(--danger); font-weight: 400; overflow-wrap: anywhere; }
.dashboard-horizon .horizon-details-values { flex-direction: column; align-items: flex-start; }
.dashboard-horizon .horizon-details-values > span { max-width: 100%; flex-wrap: wrap; }
.dashboard-horizon .horizon-details time { overflow-wrap: anywhere; }
`;
