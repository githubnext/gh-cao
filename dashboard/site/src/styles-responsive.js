export const responsiveStyles = `@media (min-width: 701px) and (max-width: 900px) {
  .pie-chart-card, .chart-horizontal-card { grid-template-columns: 1fr; }
  .pie-chart-layout { grid-column: 1; grid-row: auto; }
  .pie-chart-card > .view-source, .pie-chart-card > .view-metadata, .pie-chart-card > .view-context,
  .chart-horizontal-layout { grid-column: 1; }
  .dashboard-root.dashboard-full-view-scrolled .app-shell { grid-template-columns: minmax(0, 1fr); }
  .dashboard-root.dashboard-full-view-scrolled .org-sidebar { display: none; }
}
@media (max-width: 700px) {
  html { font-size: 112.5%; }
  .semantic-prompt-action { position: static; margin: 8px 0; }
  .mobile-table-card-toolbar > .semantic-prompt-action { margin: 0 0 0 auto; }
  .chart-prompt-heading { flex-wrap: wrap; gap: 8px; }
  :is(.semantic-prompt-action, .chart-prompt-action) .table-intent-button {
    width: 44px; min-height: 44px; max-width: 100%; justify-content: center; padding: 8px;
    border-color: var(--border); background: var(--canvas-subtle); color: var(--fg);
  }
  :is(.semantic-prompt-action, .chart-prompt-action) .table-intent-button span { display: none; }
  .cli-action-trigger, .table-intent-button { min-height: 44px; max-width: 100%; white-space: normal; overflow-wrap: anywhere; }
  .cli-action-trigger-copy strong { white-space: normal; overflow-wrap: anywhere; }
  .document-list-header { align-items: flex-start; gap: 12px; padding-inline: 16px; }
  .document-list-header .cli-action-trigger { width: 44px; min-height: 44px; justify-content: center; padding: 0; }
  .document-list-header .cli-action-trigger > .octicon { margin-top: 0; }
  .document-list-header .cli-action-trigger-copy { display: none; }
  .marketplace-page .custom-view > .document-list-header { padding: 4px 16px 16px; }
  .marketplace-page .document-list-header > p { font-size: .875rem; line-height: 1.6; }
  .entity-card-list.entity-card-list-marketplace { grid-template-columns: minmax(0, 1fr); gap: 12px; }
  .entity-card-list-marketplace .entity-card-list-card, .entity-card-list-marketplace .entity-card-list-card:first-child { min-height: 0; grid-template-columns: 48px minmax(0, 1fr); grid-template-rows: auto auto; gap: 12px; padding: 16px; }
  .entity-card-list-marketplace .issue-list-card-icon { width: 48px; height: 48px; }
  .entity-card-list-marketplace .issue-list-card-icon .octicon { width: 24px; height: 24px; }
  .entity-card-list-marketplace .issue-list-card-content { grid-column: 2; grid-row: 1; gap: 6px; }
  .entity-card-list-marketplace .issue-list-card-title a { min-height: 44px; display: inline-flex; align-items: center; }
  .entity-card-list-marketplace .issue-list-card-meta { margin-top: 0; }
  .entity-card-list-marketplace .issue-list-labels { grid-column: 1 / -1; grid-row: 2; }
  .marketplace-detail-page .entity-card-list-marketplace .entity-card-list-card { grid-template-columns: 48px minmax(0, 1fr); gap: 12px; padding: 16px; }
  .marketplace-detail-page .entity-card-list-marketplace .issue-list-card-icon { width: 48px; height: 48px; grid-row: 1; }
  .marketplace-detail-page .entity-card-list-marketplace .issue-list-labels { grid-column: 1 / -1; }
  .marketplace-detail-page .entity-card-list-marketplace .entity-card-list-actions { grid-column: 1 / -1; grid-row: 3; justify-self: stretch; margin: 0; }
  .marketplace-detail-page .entity-card-list-actions .table-cli-action-button { width: 100%; min-height: 44px; justify-content: center; }
  .marketplace-detail-page .dashboard-markdown { padding: 16px; }
  .custom-table :is(th.table-compact-column, td.table-cli-action-cell) { width: auto; min-width: 120px; max-width: none; }
  .table-cli-action-button { width: auto; min-height: 44px; padding: 8px; text-align: left; }
  .table-cli-action-control { max-width: 100%; }
  .cli-action-dialog-footer, .table-intent-dialog-footer { flex-wrap: wrap; }
  .cli-action-running .cli-action-dialog-footer { padding-block: 8px; }
  .cli-action-cancel, .cli-action-confirm, .table-intent-copy-button { min-height: 44px; }
  .cli-action-dialog-body { overflow: auto; }
  .card-filter-bar { padding: 6px 12px; }
  .card-filter-menu > summary, .card-filter-bar button, .card-filter-group label { min-height: 44px; }
  .card-filter-menu > summary { border-radius: 22px; padding-inline: 14px; background: var(--canvas); }
  .card-filter-menu > summary:has(.count-badge:not([hidden])) { border-color: var(--accent); background: var(--accent-muted); color: var(--accent); }
  .card-filter-menu { position: static; }
  .card-filter-popover { top: calc(100% + 6px); left: 0; width: 100%; max-height: min(480px, 55dvh); box-sizing: border-box; border-radius: 12px; }
  .card-filter-group legend { text-transform: uppercase; letter-spacing: .04em; }
  .card-filter-group label + label { border-top: 1px solid var(--border-muted); }
  .card-filter-group input { width: 18px; height: 18px; }
  .mobile-table-card-toolbar { padding: 8px 12px; background: var(--canvas); }
  .dashboard-page > .page-chrome { display: none; }
  .view-mode-control { display: none; }
  body, .dashboard-root { font-size: 1rem; }
  .campaign-tabs { min-height: 0; display: grid; gap: 0; padding-top: 0; overflow: hidden; border: 1px solid var(--border); border-radius: 6px; background: var(--canvas); }
  .campaign-tabs a { min-height: 44px; padding: 10px 12px; border-radius: 0; background: transparent; font-size: .875rem; }
  .campaign-tabs a + a { border-top: 1px solid var(--border-muted); }
  .campaign-tabs a:hover { background: var(--neutral-muted); }
  .campaign-tabs a:focus { outline-offset: -3px; }
  .campaign-tabs a[aria-current="page"] { background: var(--canvas-subtle); color: var(--fg); font-weight: 600; }
  .campaign-tabs a[aria-current="page"] > .octicon:first-child { color: var(--accent); }
  .campaign-tabs a[aria-current="page"]::after { display: none; }
  .campaign-tabs .tab-trailing-icon { width: 12px; height: 12px; display: block; margin-left: auto; color: var(--muted); }
  .cao-memory-browser { min-height: 0; }
  .cao-memory-layout, .campaign-memory-layout { min-height: 0; grid-template-columns: 1fr; grid-template-rows: minmax(0, 1fr); position: relative; }
  .cao-memory-tree, .cao-memory-file-content,
  .campaign-memory-files, .campaign-memory-content {
    width: 100%;
    min-height: 0;
    grid-area: 1 / 1;
  }
  .cao-memory-tree, .campaign-memory-files { z-index: 1; max-height: none; border: 0; transition: transform 180ms cubic-bezier(.2, 0, 0, 1), visibility 0s; }
  .cao-memory-file-content, .campaign-memory-content { z-index: 2; padding: 0; background: var(--canvas); transform: translateX(100%); visibility: hidden; transition: transform 180ms cubic-bezier(.2, 0, 0, 1), visibility 0s linear 180ms; }
  :is(.cao-memory-layout, .campaign-memory-layout)[data-memory-view="file"] :is(.cao-memory-tree, .campaign-memory-files) { transform: translateX(-30%); visibility: hidden; transition-delay: 0s, 180ms; }
  :is(.cao-memory-layout, .campaign-memory-layout)[data-memory-view="file"] :is(.cao-memory-file-content, .campaign-memory-content) { transform: translateX(0); visibility: visible; transition-delay: 0s; }
  .cao-memory-tree, .campaign-memory-files { padding: 8px; }
  .cao-memory-tree h2 { margin: 8px 8px 12px; }
  .cao-memory-campaign-branch > summary, .cao-memory-campaign-disabled, .campaign-memory-directory > summary, .campaign-memory-file { min-height: 44px; }
  .memory-file-header { min-height: 52px; display: grid; grid-template-columns: minmax(0, 1fr); align-items: center; position: sticky; z-index: 1; top: 0; margin: 0; padding: 6px 12px; border-bottom: 1px solid var(--border); background: var(--canvas); }
  .memory-file-header h2 { overflow: hidden; font-size: .875rem; text-overflow: ellipsis; white-space: nowrap; }
  .cao-memory-file-content pre, .campaign-memory-content pre { min-height: 0; padding: 16px; border-radius: 0; white-space: pre-wrap; overflow-wrap: anywhere; tab-size: 2; }
  :root[data-navigation-direction="forward"]::view-transition-old(root) { z-index: 1; animation-name: dashboard-view-slide-out-left; animation-timing-function: cubic-bezier(.4, 0, .2, 1); }
  :root[data-navigation-direction="forward"]::view-transition-new(root) { z-index: 2; animation-name: dashboard-view-slide-in-right; animation-timing-function: cubic-bezier(.4, 0, .2, 1); }
  :root[data-navigation-direction="backward"]::view-transition-old(root) { z-index: 2; animation-name: dashboard-view-slide-out-right; animation-timing-function: cubic-bezier(.4, 0, .2, 1); }
  :root[data-navigation-direction="backward"]::view-transition-new(root) { z-index: 1; animation-name: dashboard-view-slide-in-left; animation-timing-function: cubic-bezier(.4, 0, .2, 1); }
  .timeline-chart-axis span:not(:first-child):not(:last-child):nth-child(even) { display: none; }
  .horizontal-bar-chart-row { grid-template-columns: minmax(0, 1fr) minmax(56px, 32%) auto; gap: 6px; }
  .horizontal-bar-chart-label { direction: ltr; text-align: left; }
  .horizontal-bar-chart-label-text {
    display: -webkit-box;
    overflow: hidden;
    overflow-wrap: anywhere;
    white-space: normal;
    -webkit-box-orient: vertical;
    -webkit-line-clamp: 2;
  }
  :is(.mode-badge, .mode-indicator) .octicon { display: none; }
  .dashboard-root { --dashboard-page-padding-inline: 14px; --dashboard-mobile-page-padding-top: 16px; --dashboard-mobile-page-padding-bottom: 28px; height: auto; min-height: 100vh; overflow: visible; }
  .app-shell { height: auto; min-height: 100vh; display: block; overflow: visible; }
  .dashboard-root.dashboard-full-view { height: 100dvh; min-height: 0; overflow: hidden; }
  .dashboard-full-view .app-shell { height: 100%; min-height: 0; display: grid; grid-template-columns: minmax(0, 1fr); grid-template-rows: auto minmax(0, 1fr); overflow: hidden; }
  .org-sidebar { height: auto; display: block; overflow: visible; margin-bottom: 8px; padding: 14px 12px 10px; border-right: 0; border-bottom: 1px solid var(--border); background: var(--canvas); }
  /* The Overview hero is full-bleed, so the header must sit flush against it. */
  .app-shell:has(.dashboard-overview-page:not([hidden])) > .org-sidebar { margin-bottom: 0; }
  /* --full-view-sidebar-max-height bounds the full-view mobile header (a single title/back/menu
     row, see .sidebar-header), which stays well under this cap, so animating max-height here can
     never clip real content; it only exists to give the collapse-on-scroll transition below a
     finite value to animate toward, since CSS cannot transition to/from "none". */
  .dashboard-full-view .org-sidebar { --full-view-sidebar-max-height: 480px; /* generous headroom over the ~70px .sidebar-header row this cap actually bounds */ max-height: var(--full-view-sidebar-max-height); overflow: hidden; transition: max-height 200ms ease, padding 200ms ease, opacity 160ms ease, border-color 200ms ease, visibility 0s linear 0s; }
  .dashboard-root.dashboard-full-view-scrolled .org-sidebar { max-height: 0; overflow: hidden; margin-bottom: 0; padding-top: 0; padding-bottom: 0; border-color: transparent; opacity: 0; visibility: hidden; pointer-events: none; transition: max-height 200ms ease, padding 200ms ease, opacity 160ms ease, border-color 200ms ease, visibility 0s linear 200ms; }
  /* Let the viewport-height navigation drawer escape the full-view header's clipping.
     The collapse-on-scroll state keeps its clip because the menu collapses with the header. */
  .dashboard-root.dashboard-full-view:not(.dashboard-full-view-scrolled) .org-sidebar:has(.mobile-nav-menu[open]) { overflow: visible; }
  .dashboard-root.dashboard-full-view:not(.dashboard-full-view-scrolled):has(.mobile-nav-menu[open]), .dashboard-root.dashboard-full-view:not(.dashboard-full-view-scrolled):has(.mobile-nav-menu[open]) .app-shell { overflow: visible; }
  .sidebar-header { position: relative; margin: 0 0 8px; }
  .mobile-history-back:not([hidden]) { width: 44px; height: 44px; display: grid; flex: 0 0 44px; place-items: center; padding: 0; border: 1px solid var(--border); border-radius: 50%; background: var(--canvas-subtle); color: var(--fg); cursor: pointer; }
  .mobile-history-back:hover { background: var(--neutral-muted); }
  .mobile-history-back-icon { width: 28px; height: 28px; flex-basis: 28px; }
  .sidebar-brand { display: none; }
  .mobile-page-header { min-width: 0; display: flex; flex: 1 1 auto; flex-direction: column; align-items: flex-start; justify-content: center; overflow: hidden; margin: 0 4px; }
  .mobile-brand-name { max-width: 100%; display: block; overflow: hidden; color: var(--muted); font-size: .75rem; line-height: 1.25; text-overflow: ellipsis; white-space: nowrap; }
  .mobile-page-header .overview-header { width: 100%; min-width: 0; flex: none; }
  .mobile-page-header .breadcrumb-context { display: none; }
  .mobile-page-header .breadcrumb-context:has([data-route-breadcrumb] ~ [data-route-breadcrumb]) { display: flex; max-width: 100%; gap: 0 8px; }
  .mobile-page-header .breadcrumb-context > [data-route-breadcrumb] { display: inline-flex; align-items: center; min-height: 44px; white-space: normal; }
  /* Desktop reserves hidden descriptions as a stable spacer; collapse it in the compact mobile title bar without adding another display override. */
  .mobile-page-header .overview-header .lede { height: 0; min-height: 0; margin: 0; overflow: hidden; line-height: 0; visibility: hidden; }
  .dashboard-root:has(.command-terminal) .mobile-page-header .overview-header .lede { height: auto; min-height: 1.25rem; margin-top: 3px; line-height: 1.25rem; visibility: visible; }
  .mobile-page-header .overview-header .title-area { display: flex; align-items: center; gap: 4px; min-width: 0; }
  .mobile-page-header .overview-header h1 { margin: 0; overflow: hidden; color: var(--fg); font-size: 1rem; font-weight: 600; line-height: 1.25; text-overflow: ellipsis; white-space: nowrap; }
  .mobile-view-mode-toggle:not([hidden]) { width: 44px; height: 44px; display: grid; flex: 0 0 44px; place-items: center; padding: 0; border: 1px solid var(--border); border-radius: 50%; background: var(--canvas-subtle); color: var(--fg); cursor: pointer; }
  .mobile-view-mode-toggle:hover { background: var(--neutral-muted); }
  .mobile-nav-menu-actions { min-width: 0; display: flex; margin: 0 0 8px; padding: 0 0 8px; border-bottom: 1px solid var(--border-muted); }
  .mobile-nav-menu-actions .report-actions { width: 100%; flex-direction: column; flex-wrap: nowrap; align-items: stretch; position: static; margin-left: 0; gap: 2px; }
  .mobile-nav-menu-actions .report-actions > .filter-bar { width: 100%; margin: 0; }
  .mobile-nav-menu-actions .dashboard-horizon { width: 100%; max-width: 100%; padding-right: 0; border-right: 0; }
  .mobile-nav-menu-actions .horizon-summary { width: 100%; position: static; }
  .mobile-nav-menu-actions .dashboard-horizon.filter-bar-expanded > .filter-tuning-controls { position: static; width: 100%; max-height: none; overflow: visible; padding: 8px; box-shadow: none; }
  .mobile-nav-menu-actions .horizon-tooltip { top: calc(100% + 4px); right: 0; left: auto; }
  .mobile-nav-menu-actions .account-menu { width: 100%; }
  .mobile-nav-menu-actions .dashboard-current-status { width: 100%; display: block; }
  .mobile-nav-menu-actions .dashboard-current-status .tooltip-trigger { width: 100%; height: auto; min-height: 44px; display: flex; align-items: center; justify-content: flex-start; gap: 10px; padding: 8px; border: 0; border-radius: 6px; background: transparent; text-align: left; }
  .mobile-nav-menu-actions .dashboard-current-status .tooltip-trigger:hover { background: var(--neutral-muted); }
  .mobile-nav-menu-actions .dashboard-current-status .tooltip-trigger > .octicon { width: 16px; height: 16px; flex: none; }
  .mobile-nav-menu-actions .dashboard-current-status .action-label { position: static; width: auto; height: auto; overflow: visible; margin: 0; padding: 0; clip: auto; color: var(--fg); font-size: .8125rem; font-weight: 500; white-space: normal; }
  .mobile-nav-menu-actions :is(.horizon-toggle, .repository-link, .account-menu-avatar) { width: 100%; height: auto; min-height: 32px; display: flex; flex: none; align-items: center; justify-content: flex-start; place-items: unset; gap: 10px; padding: 6px 8px; border: 0; border-radius: 6px; background: transparent; color: var(--fg); font-weight: 500; text-align: left; }
  .mobile-nav-menu-actions :is(.horizon-toggle, .repository-link, .account-menu-avatar):hover { background: var(--neutral-muted); }
  .mobile-nav-menu-actions :is(.horizon-toggle, .repository-link, .account-menu-avatar) > .octicon { width: 16px; height: 16px; color: var(--muted); }
  .mobile-nav-menu-actions .account-menu-avatar { border-radius: 6px; box-shadow: none; }
  .mobile-nav-menu-actions .account-menu-avatar-image { width: 20px; height: 20px; border-radius: 50%; }
  .mobile-nav-menu-actions :is(.horizon-toggle, .repository-link, .account-menu-avatar) .action-label { position: static; width: auto; height: auto; overflow: visible; margin: 0; padding: 0; clip: auto; color: var(--fg); font-size: .8125rem; font-weight: 500; white-space: normal; }
  .mobile-nav-menu-actions .account-menu-popover { width: 100%; position: static; margin-top: 4px; box-shadow: none; }
  .sidebar-toggle { display: none; }
  .sidebar-collapsed .org-sidebar { padding: 14px 12px 10px; }
  .sidebar-collapsed .sidebar-brand > span, .sidebar-collapsed .nav-label { display: initial; }
  .primary-nav { display: none; }
  .nav-section { display: flex; flex: 0 0 auto; flex-direction: row; }
  .nav-section-toggle { display: none; }
  .nav-section-items, .nav-section:not([open]) > .nav-section-items { display: flex; flex-direction: row; gap: 4px; }
  .primary-nav .nav-item, .sidebar-collapsed .primary-nav .nav-item { width: 44px; min-height: 44px; flex: 0 0 44px; justify-content: center; gap: 0; padding: 0; }
  .primary-nav .nav-item.mobile-nav-overflow { display: none; }
  .primary-nav .nav-item .nav-label { display: none; }
  .primary-nav a[aria-current="page"]::before { content: none; }
  .mobile-nav-menu { width: 44px; height: 44px; display: block; flex: 0 0 44px; position: relative; margin-left: 0; }
  .mobile-nav-menu > summary { width: 44px; height: 44px; display: grid; place-items: center; position: relative; border: 1px solid var(--border); border-radius: 50%; background: var(--canvas-subtle); color: var(--fg); cursor: pointer; list-style: none; }
  .mobile-nav-menu > summary .dashboard-offline-status:not([hidden]) { display: inline-flex; position: absolute; right: -5px; bottom: -5px; padding: 2px; border-radius: 50%; background: var(--canvas); color: var(--muted); }
  .org-sidebar > .dashboard-offline-status { display: none; }
  .mobile-nav-menu > summary::-webkit-details-marker { display: none; }
  .mobile-nav-menu > summary:hover, .mobile-nav-menu[open] > summary { background: var(--neutral-muted); }
  .mobile-nav-menu[open] > summary { position: fixed; z-index: 31; top: max(14px, env(safe-area-inset-top)); right: max(12px, env(safe-area-inset-right)); }
  .mobile-nav-menu-list { width: min(320px, calc(100vw - 44px)); height: 100dvh; max-height: none; display: flex; flex-direction: column; gap: 2px; overflow-y: auto; overscroll-behavior: contain; position: fixed; z-index: 30; top: 0; right: 0; padding: calc(max(14px, env(safe-area-inset-top)) + 52px) max(8px, env(safe-area-inset-right)) max(8px, env(safe-area-inset-bottom)) 8px; border: 0; border-left: 1px solid var(--border); border-radius: 0; background: var(--canvas); box-shadow: -8px 0 24px color-mix(in srgb, var(--canvas-inset) 45%, transparent); }
  .mobile-nav-menu-list a { width: 100%; min-height: 44px; display: flex; flex: none; align-items: center; justify-content: flex-start; gap: 10px; padding: 8px; border-radius: 6px; color: var(--fg); font-weight: 500; text-decoration: none; }
  .mobile-nav-menu-list a > .octicon { flex: none; color: var(--muted); }
  .mobile-nav-menu-list a:hover { background: var(--neutral-muted); }
  .mobile-nav-menu-list a[aria-current="page"] { background: var(--neutral-muted); font-weight: 600; }
  .mobile-nav-section-label { margin: 8px 8px 2px; color: var(--muted); font-size: .6875rem; font-weight: 700; letter-spacing: .08em; text-transform: uppercase; }
  .mobile-nav-section-label:first-child { margin-top: 2px; }
  .app-main > .top-nav { display: none; }
  .breadcrumb-context > :is([data-breadcrumb-root], [data-breadcrumb-dashboard]) { min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .report-actions { width: 100%; position: relative; margin-left: 0; }
  .report-actions .tooltip-help { position: static; }
  .report-actions .tooltip-content { width: min(320px, 100%); right: auto; left: 0; }
  .report-footer { justify-content: flex-start; }
  .report-footer-versions { justify-content: flex-start; }
  /* Keep full-width wrapping for the desktop header only; the same header moves into .mobile-page-header where it must stay content-sized. */
  .app-main .overview-header { flex-basis: 100%; }
  .toolbar { align-items: stretch; flex-wrap: wrap; }
  .dashboard-page > .page-chrome > .filter-bar { display: none; }
  .dashboard-page > .page-chrome > .filter-bar .filter-tuning-controls { display: grid; grid-template-columns: minmax(0, 1fr); }
  .filter-control { min-width: 0; flex-basis: 100%; }
  .filter-toggle[aria-expanded="true"] { background: var(--neutral-muted); }
  .scope-period { min-height: 44px; }
  .scope-period { flex: 1; justify-content: center; }
  .report-actions > .filter-bar { position: static; }
  .filter-bar-expanded .filter-tuning-controls { width: 100%; display: grid; grid-template-columns: minmax(0, 1fr); right: 0; left: 0; }
  .filter-bar-expanded .horizon-details { grid-template-columns: minmax(0, 1fr); }
  .horizon-details-values { justify-content: flex-start; flex-wrap: wrap; }
  .filter-bar-expanded .time-window-control { display: grid; flex: 1 1 100%; grid-template-columns: minmax(0, 1fr); min-width: 0; }
  .time-window-control label { min-height: 44px; }
  .mode-filter-control { display: flex; flex-wrap: wrap; }
  .time-window-control :is(select, input) { width: 100%; }
  .mode-filter-control input { width: auto; }
  .time-window-custom { display: grid; grid-template-columns: minmax(0, 1fr); }
  .time-window-custom > button { min-height: 44px; }
  .app-main { height: auto; overflow: visible; }
  .app-main:has(.dashboard-overview-page:not([hidden])) { min-height: 100vh; min-height: 100dvh; }
  .dashboard-full-view .app-main { height: 100%; min-height: 0; overflow: hidden; }
  main.dashboard-prototype { overflow: visible; overflow-x: clip; padding: var(--dashboard-mobile-page-padding-top) var(--dashboard-page-padding-inline) var(--dashboard-mobile-page-padding-bottom); }
  .dashboard-full-view main.dashboard-prototype { padding: 0 var(--dashboard-page-padding-inline); }
  main.dashboard-prototype:has(.dashboard-overview-page:not([hidden])) { padding: 0; }
  .dashboard-full-view .custom-view-grid > .custom-view { padding-inline: var(--dashboard-page-padding-inline); }
  .dashboard-full-view .custom-view-grid > .chart-view-swimlane { padding-bottom: 12px; }
  .dashboard-page:is([data-view-mode="table"], [data-view-mode="card"]) > .custom-view-grid { margin-inline: calc(-1 * var(--dashboard-page-padding-inline)); }
  .dashboard-page:is([data-view-mode="table"], [data-view-mode="card"]) > .custom-view-grid > .custom-view { padding-inline: 0; }
  .campaign-insights-page:is([data-view-mode="table"], [data-view-mode="card"]) > .custom-view-grid > .custom-view { padding-inline: var(--dashboard-page-padding-inline); }
  .data-state-summary, .metrics { grid-template-columns: repeat(2, minmax(0, 1fr)); }
  .layout-section[data-section-layout="wide"], .layout-section[data-section-layout="narrow"] { grid-column: span 12; }
  .custom-view[data-view-layout="half"], .custom-view[data-view-layout="third"] { grid-column: span 12; }
  .custom-view[data-view-layout="full-view"] { min-height: 100%; }
  .workflow-runtime-metrics { grid-template-columns: 1fr; }
  .workflow-identity { align-items: flex-start; flex-direction: column; gap: 10px; }
  .value-chart > dl { grid-template-columns: repeat(2, minmax(0, 1fr)); }
  .repository-health .section-heading { align-items: flex-start; flex-direction: column; }
  .outcome-view { grid-template-columns: 1fr; }
  .outcome-meta { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 0 20px; }
  .problem-view-highlights { grid-template-columns: repeat(2, minmax(0, 1fr)); }
  .problem-view-highlights > div:nth-child(2) { border-right: 0; }
  .problem-view-highlights > div:nth-child(-n+2) { border-bottom: 1px solid var(--border); }
  .problem-view-sections { grid-template-columns: 1fr; }
  /* Desktop card gutters stack on top of the page inset; narrow them so mobile content keeps the viewport width. */
  .layout-section { padding: 12px; }
  .pie-chart-card, .chart-horizontal-card { grid-template-columns: 1fr; padding: 16px 12px; }
  .pie-chart-layout { grid-column: 1; grid-row: auto; }
  .pie-chart-layout .chart-widget, .pie-chart-table-toggle { grid-column: 1; grid-row: 1; }
  .pie-chart-table-toggle { width: 100%; height: 100%; display: block; z-index: 1; padding: 0; border: 0; background: transparent; cursor: pointer; }
  .pie-chart-table-toggle:focus-visible { outline: 2px solid var(--focus); outline-offset: 2px; border-radius: 6px; }
  .pie-chart-layout[data-chart-table-hidden] .chart-legend-pie { display: none; }
  .pie-chart-card > .view-source, .pie-chart-card > .view-metadata, .pie-chart-card > .view-context,
  .chart-horizontal-layout { grid-column: 1; }
  .control-plane-status > header { min-height: 0; padding: 14px; }
  .control-plane-heading { align-items: flex-start; }
  .control-plane-heading .scope-kicker { display: none; }
  .control-plane-heading p { font-size: .75rem; }
  .control-plane-vitals { grid-template-columns: repeat(2, minmax(0, 1fr)); }
  .dashboard-callout { grid-template-columns: 1fr; gap: 10px; }
  .control-plane-vitals > div { padding: 10px 12px; }
  .control-plane-vitals p { min-height: 0; }
  .execution-health-heading { align-items: flex-start; flex-direction: column; gap: 2px; }
  .execution-legend { display: none; }
  .managed-campaign-card dl { gap: 8px; }
  .overview-observability > .section-heading { align-items: flex-start; flex-direction: column; }
  .dashboard-view-skeleton { grid-template-columns: 1fr; }
  .dashboard-view-skeleton-block { grid-column: 1 / -1; }
  .skeleton-panel { grid-column: auto; }
  .workflow-attention > .section-heading { align-items: flex-start; flex-direction: column; }
  .workflow-attention-list a, .workflow-attention-static { grid-template-columns: 20px minmax(0, 1fr); }
  .notifications-toolbar { grid-template-columns: minmax(0, 1fr) auto; }
  .notifications-filter-toggle { min-height: 44px; display: inline-flex; align-items: center; justify-content: center; gap: 6px; padding: 0 12px; border: 1px solid var(--border); border-radius: 6px; background: var(--canvas); color: var(--fg); font: inherit; font-size: .75rem; font-weight: 600; cursor: pointer; }
  .notifications-filter-toggle[aria-expanded="true"] { background: var(--neutral-muted); }
  .notifications-filter-toggle .octicon { width: 14px; height: 14px; }
  .notifications-advanced-filters { display: none; }
  .notifications-advanced-filters.is-expanded { min-width: 0; display: grid; grid-column: 1 / -1; gap: 8px; }
  .home-catchup-controls { width: 100%; }
  .home-catchup-controls select { min-width: 0; flex: 1; }
  .home-catchup-charts { grid-template-columns: minmax(0, 1fr); }
  .home-catchup-side-charts { grid-template-columns: repeat(2, minmax(0, 1fr)); grid-template-rows: none; }
  .home-catchup-stories { display: none; }
  .home-catchup-mobile { min-width: 0; display: grid; gap: 10px; }
  .home-catchup-mobile > header { display: flex; align-items: baseline; justify-content: space-between; gap: 12px; }
  .home-catchup-mobile h3 { margin: 0; font-size: .875rem; }
  .home-catchup-mobile > header span { color: var(--muted); font-size: .75rem; font-variant-numeric: tabular-nums; }
  .home-catchup-mobile-card { min-width: 0; display: grid; gap: 12px; padding: 16px; border: 1px solid var(--border); border-radius: 0; background: var(--canvas); box-shadow: 0 4px 12px color-mix(in srgb, var(--canvas-inset) 12%, transparent); touch-action: pan-y; animation: catch-up-card-entry 180ms ease-out; }
  .home-catchup-mobile-link { min-width: 0; display: grid; gap: 8px; color: inherit; text-decoration: none; }
  .home-catchup-mobile-link:hover > strong { color: var(--accent); }
  .home-catchup-mobile-link:focus-visible { outline: 2px solid var(--focus); outline-offset: 4px; border-radius: 2px; }
  .home-catchup-mobile-meta { display: flex; align-items: center; justify-content: space-between; gap: 8px; }
  .home-catchup-classification { color: var(--muted); font-size: .6875rem; font-weight: 700; letter-spacing: .06em; text-transform: uppercase; }
  .home-catchup-mobile-link > strong { font-size: 1rem; }
  .home-catchup-mobile-link > p { margin: 0; color: var(--muted); font-size: .8125rem; }
  .home-catchup-mobile-link > small { color: var(--muted); font-size: .6875rem; }
  .home-catchup-mobile-hint { color: var(--muted); font-size: .6875rem; text-align: center; }
  .home-catchup-mobile-actions { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 8px; }
  .home-catchup-mobile-action { min-height: 44px; display: inline-flex; align-items: center; justify-content: center; gap: 6px; border: 1px solid var(--border); border-radius: 7px; background: var(--canvas); color: var(--fg); font: inherit; font-size: .75rem; font-weight: 700; cursor: pointer; }
  .home-catchup-mobile-action:hover { background: var(--canvas-subtle); }
  .home-catchup-mobile-action:focus-visible { outline: 2px solid var(--focus); outline-offset: 2px; }
  .home-catchup-mobile-action .octicon { width: 14px; height: 14px; }
  .home-catchup-mobile-done { border-color: color-mix(in srgb, var(--success) 45%, var(--border)); color: var(--success); }
  .insights-section-heading { align-items: start; flex-direction: column; gap: 14px; }
  .insights-lead-metrics { width: 100%; justify-content: space-between; gap: 12px; }
  .insights-lead-metrics dd, .insights-inline-metrics dd { font-size: 1rem; }
  .insights-value-lead > .chart-legend { display: none; }
  .insights-plot-grid { grid-template-columns: minmax(0, 1fr); gap: 24px; }
  .insights-measure-rows { gap: 24px; }
  .temporal-metric-plot { padding: 12px 8px; }
  .temporal-plot-heading { grid-template-columns: minmax(0, 1fr); padding: 0 4px; }
  .temporal-plot-summary { display: flex; align-items: baseline; justify-content: space-between; }
  .insights-measure-plot { grid-template-columns: minmax(0, 1fr); }
  .insights-axis-y { writing-mode: horizontal-tb; transform: none; justify-self: start; }
  .insights-axis-x { grid-column: 1; }
  .insights-value-lead .chart-widget, .insights-plot-panel .chart-widget, .insights-experiment-band .chart-widget { min-height: 190px; }
  .notifications-search { grid-column: 1; }
  .notifications-state-tabs { width: max-content; }
  .notifications-select { justify-content: space-between; }
  .notifications-select select { min-width: 0; margin-left: auto; }
  .notification-item { grid-template-columns: 8px 24px auto minmax(0, 1fr) auto; }
  .notification-meta { grid-column: 3 / 5; justify-items: start; text-align: left; }
  .notification-actions { grid-column: 5; grid-row: 1 / span 2; flex-direction: column; opacity: 1; }
  .work-project-view { gap: 12px; }
  .work-project-tabs { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); overflow: visible; border: 1px solid var(--border); border-radius: 8px; background: var(--canvas-subtle); }
  .work-project-tabs a { min-width: 0; min-height: 44px; justify-content: center; padding: 6px; overflow: hidden; text-overflow: ellipsis; }
  .work-project-tabs a[aria-current="page"] { border-radius: 7px; background: var(--canvas); box-shadow: 0 0 0 1px var(--border); }
  .work-project-tabs a[aria-current="page"]::after { content: none; }
  .work-filter-bar { grid-template-columns: minmax(0, 1fr) auto auto auto; padding: 0; border: 0; background: transparent; }
  .work-filter-search { height: 44px; }
  .work-filter-search input { height: 42px; }
  .work-filter-mobile-toggle { min-height: 44px; display: inline-flex; align-items: center; gap: 6px; padding: 0 12px; border: 1px solid var(--border); border-radius: 6px; background: var(--canvas); color: var(--fg); font: inherit; font-size: .75rem; font-weight: 600; }
  .work-filter-mobile-icon { display: grid; }
  .work-filter-facets { display: none; position: fixed; z-index: 50; inset: 0; align-items: end; overflow: hidden; background: color-mix(in srgb, var(--canvas-inset) 72%, transparent); }
  .work-filter-facets.is-open { display: grid; }
  .work-filter-sheet-panel { display: grid; grid-template-columns: minmax(0, 1fr); gap: 10px; padding: 16px 14px max(18px, env(safe-area-inset-bottom)); border-top: 1px solid var(--border); border-radius: 14px 14px 0 0; background: var(--canvas); box-shadow: 0 -8px 24px color-mix(in srgb, var(--canvas-inset) 45%, transparent); }
  .work-filter-facets select { width: 100%; max-width: none; min-height: 44px; }
  .work-mobile-sheet-header { display: flex; align-items: center; justify-content: space-between; gap: 12px; margin-bottom: 2px; }
  .work-mobile-sheet-close, .work-mobile-detail-close { width: 44px; height: 44px; display: grid; place-items: center; padding: 0; border: 0; border-radius: 6px; background: transparent; color: var(--fg); }
  .work-filter-count { min-width: 0; }
  .work-filter-clear { width: 44px; height: 44px; }
  .work-board { display: grid; grid-template-columns: minmax(0, 1fr); gap: 10px; overflow: visible; padding: 0; }
  .work-board-group-tabs { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); gap: 3px; padding: 3px; border-radius: 8px; background: var(--canvas-subtle); }
  .work-board-group-tab { min-width: 0; min-height: 44px; display: grid; place-items: center; gap: 0; padding: 4px 2px; border: 0; border-radius: 6px; background: transparent; color: var(--muted); font: inherit; font-size: .6875rem; font-weight: 600; }
  .work-board-group-tab .count-badge { padding: 0; background: transparent; font-size: .625rem; }
  .work-board-group-tab[aria-selected="true"] { background: var(--canvas); color: var(--fg); box-shadow: 0 0 0 1px var(--border); }
  .work-board-column { min-width: 0; height: auto; display: grid; grid-template-rows: auto auto; }
  .work-board-column[data-mobile-active="false"] { display: none; }
  .work-board-cards { overflow: visible; scrollbar-gutter: auto; }
  .work-card dl { display: none; }
  .work-card { gap: 7px; }
  .work-mobile-item-actions { display: flex; justify-content: flex-end; gap: 8px; padding-top: 4px; }
  .work-mobile-details-button { min-width: 0; min-height: 44px; padding: 0 10px; border: 1px solid var(--border); border-radius: 6px; background: var(--canvas); color: var(--fg); font: inherit; font-size: .75rem; font-weight: 600; }
  .work-mobile-details-button { display: inline-flex; align-items: center; gap: 4px; }
  .work-mobile-details-icon { display: grid; }
  .work-mobile-detail[open] { width: 100vw; max-width: none; height: 100dvh; max-height: none; display: grid; grid-template-rows: auto minmax(0, 1fr); inset: 0; margin: 0; padding: 0; overflow: hidden; border: 0; background: var(--canvas); color: var(--fg); }
  .work-mobile-detail::backdrop { background: var(--canvas); }
  .work-mobile-detail > header { min-height: 60px; display: flex; align-items: center; justify-content: space-between; gap: 10px; padding: 8px 12px; border-bottom: 1px solid var(--border); }
  .work-mobile-detail > header > div { min-width: 0; display: flex; align-items: center; gap: 10px; }
  .work-mobile-detail h2 { margin: 0; overflow: hidden; font-size: 1rem; text-overflow: ellipsis; white-space: nowrap; }
  .work-mobile-detail > main { display: grid; align-content: start; gap: 18px; overflow-y: auto; padding: 18px 14px max(24px, env(safe-area-inset-bottom)); }
  .work-mobile-detail-status { display: flex; align-items: center; justify-content: space-between; gap: 12px; color: var(--muted); font-size: .75rem; }
  .work-mobile-detail dl { display: grid; gap: 0; margin: 0; border: 1px solid var(--border); border-radius: 8px; }
  .work-mobile-detail dl > div { display: grid; gap: 3px; padding: 11px 12px; }
  .work-mobile-detail dl > div + div { border-top: 1px solid var(--border); }
  .work-mobile-detail dt { color: var(--muted); font-size: .6875rem; font-weight: 600; }
  .work-mobile-detail dd { margin: 0; font-size: .8125rem; font-weight: 600; }
  .work-tasks { overflow: visible; border: 0; background: transparent; }
  .work-task-viewbar { width: 100%; position: static; border: 1px solid var(--border); border-radius: 8px; }
  .work-task-settings { display: block; margin-left: auto; }
  .work-task-settings-toggle { min-height: 44px; display: inline-flex; align-items: center; gap: 6px; padding: 0 10px; border: 1px solid var(--border); border-radius: 6px; background: var(--canvas); color: var(--fg); font: inherit; font-size: .6875rem; font-weight: 600; }
  .work-task-settings-icon { display: grid; }
  .work-task-settings-sheet { display: none; position: fixed; z-index: 50; inset: 0; align-items: end; background: color-mix(in srgb, var(--canvas-inset) 72%, transparent); }
  .work-task-settings-sheet.is-open { display: grid; }
  .work-task-settings-panel { display: grid; gap: 14px; padding: 16px 14px max(18px, env(safe-area-inset-bottom)); border-top: 1px solid var(--border); border-radius: 14px 14px 0 0; background: var(--canvas); }
  .work-mobile-field-settings { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 8px; margin: 0; padding: 0; border: 0; }
  .work-mobile-field-settings legend { grid-column: 1 / -1; margin-bottom: 2px; color: var(--muted); font-size: .6875rem; font-weight: 600; }
  .work-mobile-field-settings label { min-height: 40px; display: flex; align-items: center; gap: 8px; padding: 7px 9px; border: 1px solid var(--border); border-radius: 6px; }
  .work-mobile-field-settings input { width: 16px; height: 16px; accent-color: var(--accent); }
  .work-task-sort-controls { display: grid; grid-template-columns: minmax(0, 1fr) 44px; }
  .work-task-sort { min-height: 44px; justify-content: space-between; }
  .work-task-sort-direction { width: 44px; height: 44px; }
  .work-task-scroll { min-width: 0; max-height: none; overflow: visible; }
  .work-task-table-header { display: none; }
  .work-task-list { width: 100%; min-width: 0; display: grid; gap: 10px; padding-top: 10px; }
  .work-task-row { width: 100%; min-width: 0; display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 8px; padding: 12px; border: 1px solid var(--border); border-radius: 0; box-shadow: 0 1px 0 var(--border-muted); }
  .work-task-row::before { display: none; }
  .work-task-row > * { padding: 0; border: 0; }
  .work-task-main { grid-column: 1 / -1; }
  .work-task-title { gap: 2px; }
  .work-task-type, .work-task-owner { display: none; }
  .work-mobile-hide-dates .work-task-row > time, .work-mobile-hide-dates .work-task-end { display: none; }
  .work-mobile-owner { display: grid; }
  .work-task-status-cell, .work-task-labels, .work-mobile-owner { min-width: 0; align-content: start; gap: 3px; overflow: hidden; font-size: .6875rem; }
  .work-task-status-cell::before, .work-task-labels::before, .work-mobile-owner::before { color: var(--muted); font-size: .625rem; font-weight: 600; }
  .work-task-status-cell::before { content: "Status"; }
  .work-task-labels::before { content: "Label"; }
  .work-mobile-owner::before { content: "Owner"; }
  .work-task-row > .work-mobile-item-actions { grid-column: 1 / -1; display: grid; }
  .work-mobile-hide-repository .work-task-title small, .work-mobile-hide-status .work-task-status-cell, .work-mobile-hide-owner .work-mobile-owner, .work-mobile-hide-label .work-task-labels { display: none; }
  .work-roadmap { overflow: visible; border: 0; background: transparent; }
  .work-roadmap-toolbar { min-height: 52px; border: 1px solid var(--border); border-radius: 8px; }
  .work-roadmap-zoom, .work-roadmap-today-button { display: none; }
  .work-roadmap-visual-toggle { min-height: 40px; display: inline-flex; align-items: center; gap: 6px; padding: 0 10px; border: 1px solid var(--border); border-radius: 6px; background: var(--canvas); color: var(--fg); font: inherit; font-size: .6875rem; font-weight: 600; }
  .work-roadmap-visual-icon { display: grid; }
  .work-roadmap-scroll { max-height: none; overflow: visible; background: transparent; }
  .work-roadmap-timeline { min-width: 0; display: block; }
  .work-roadmap-calendar { display: none; }
  .work-roadmap-period-heading { display: block; margin: 18px 0 8px; color: var(--muted); font-size: .75rem; }
  .work-roadmap-lane { min-height: 0; display: block; margin-left: 5px; padding: 0 0 10px 13px; border: 0; border-left: 2px solid var(--border); }
  .work-roadmap-label { min-width: 0; display: grid; grid-template-columns: 22px minmax(0, 1fr); gap: 9px; position: static; padding: 12px; border: 1px solid var(--border); border-radius: 0; background: var(--canvas); box-shadow: 0 1px 0 var(--border-muted); }
  .work-roadmap-index { display: none; }
  .work-roadmap-label .work-avatar { width: 22px; height: 22px; }
  .work-roadmap-label-copy small { white-space: normal; }
  .work-roadmap-mobile-meta { grid-column: 2; display: flex; flex-wrap: wrap; align-items: center; gap: 6px 10px; color: var(--muted); font-size: .6875rem; }
  .work-roadmap-track { display: none; }
  .work-roadmap-lane > .work-mobile-item-actions { margin-top: 7px; }
  .work-roadmap-mobile-controls { display: none; }
  .work-roadmap-visual .work-roadmap-toolbar > div:first-child, .work-roadmap-visual .work-roadmap-period-heading, .work-roadmap-visual .work-roadmap-label, .work-roadmap-visual .work-roadmap-lane > .work-mobile-item-actions { display: none; }
  .work-roadmap-visual .work-roadmap-mobile-controls { min-width: 0; display: grid; grid-template-columns: 40px minmax(0, 1fr) 40px; align-items: center; gap: 4px; margin-right: auto; }
  .work-roadmap-mobile-controls button { width: 40px; height: 40px; display: grid; place-items: center; padding: 0; border: 0; border-radius: 6px; background: transparent; color: var(--fg); }
  .work-roadmap-mobile-period { overflow: hidden; font-size: .6875rem; font-weight: 600; text-align: center; text-overflow: ellipsis; white-space: nowrap; }
  .work-roadmap-visual .work-roadmap-scroll { overflow: hidden; border: 1px solid var(--border); border-radius: 8px; background: var(--canvas); }
  .work-roadmap-visual .work-roadmap-timeline { width: 100%; display: grid; }
  .work-roadmap-visual .work-roadmap-calendar { min-height: 88px; display: grid; grid-template-columns: minmax(0, 1fr); position: static; }
  .work-roadmap-visual .work-roadmap-corner { display: none; }
  .work-roadmap-visual .work-roadmap-lane { width: 100%; min-height: 40px; display: grid; grid-template-columns: minmax(0, 1fr); margin: 0; padding: 0; border: 0; border-top: 1px solid var(--border); }
  .work-roadmap-visual .work-roadmap-track { min-height: 40px; display: block; }
  .work-roadmap-today { display: none; }
  .workflow-identity { align-items: flex-start; flex-direction: column; }
}
@media (max-width: 420px) {
  .primary-nav .nav-item.narrow-mobile-nav-overflow { display: none; }
  .data-state-summary, .metrics { grid-template-columns: 1fr; }
  .notifications-inbox.has-notifications .notifications-main { order: -1; }
  .home-catchup-controls { align-items: stretch; flex-direction: column; }
  .home-catchup-metrics { grid-template-columns: repeat(2, minmax(0, 1fr)); }
  .home-catchup-metric:nth-child(3) { border-left: 0; }
  .home-catchup-metric:nth-child(n + 3) { border-top: 1px solid var(--border-muted); }
  .home-catchup-side-charts { grid-template-columns: minmax(0, 1fr); }
  .home-momentum-panel { padding: 13px 10px; }
  .home-momentum-chart { height: 116px; }
  .notification-item { grid-template-columns: 8px 24px minmax(0, 1fr) auto; }
  .notification-item > .home-origin-badge { grid-column: 3; grid-row: 1; justify-self: start; }
  .notification-content { grid-column: 3 / 5; grid-row: 2; }
  .notification-meta { grid-column: 3 / 5; grid-row: 3; }
  .notification-actions { grid-column: 4; grid-row: 1; }
  .workflow-run-health > .chart-legend, .value-chart > dl { grid-template-columns: 1fr; }
  .pie-chart-layout { grid-template-columns: 1fr; }
  /* Match the loading footprint and scroll long legends within the reserved area. */
  .chart-view-pie > .pie-chart-layout:not([data-chart-table-hidden]) { aspect-ratio: 1; grid-template-rows: 140px minmax(0, 1fr); }
  .chart-view-pie > .pie-chart-layout:not([data-chart-table-hidden])::before { content: ""; grid-column: 1; grid-row: 1 / span 2; width: 100%; aspect-ratio: 1; pointer-events: none; }
  .chart-view-pie > .pie-chart-layout > .chart-legend-pie { grid-column: 1; grid-row: 2; min-height: 0; max-height: 100%; overflow-y: auto; }
  .custom-view.chart-view-pie > .dashboard-view-skeleton { height: auto; min-height: 280px; aspect-ratio: 1; }
  .pie-chart-layout .chart-widget { min-height: 140px; }
  .pie-chart-layout .chart-widget svg { max-width: 140px; }
  .document-list-card { grid-template-columns: auto minmax(0, 1fr); }
  .document-list-card-actions { grid-column: 2; justify-content: flex-start; }
  .issue-list-card { grid-template-columns: 20px minmax(0, 1fr); padding: 10px 14px; }
  .issue-list-labels { grid-column: 2; justify-content: flex-start; }
  .entity-card-list-card:has(.entity-card-list-timing) { grid-template-columns: 20px minmax(0, 1fr); }
  .entity-card-list-timing { grid-column: 2; }
  .outcome-meta { grid-template-columns: 1fr; }
  .problem-view-header { align-items: stretch; flex-direction: column; }
  .problem-view-highlights { grid-template-columns: 1fr; }
  .problem-view-highlights > div, .problem-view-highlights > div:nth-child(2) { border-right: 0; border-bottom: 1px solid var(--border); }
  .problem-view-highlights > div:last-child { border-bottom: 0; }
  .configuration-editor-toolbar { align-items: stretch; flex-direction: column; }
  .configuration-editor-toolbar > div { justify-content: space-between; }
  .configuration-editor-actions { justify-content: flex-start; }
  .configuration-setting-row { grid-template-columns: 1fr; gap: 12px; }
  .configuration-setting-toggle { justify-self: start; }
  .configuration-setting-group .configuration-setting-group { margin-inline: 8px; }
  .dashboard-markdown { padding: 20px 16px 24px; }
  .markdown-body { padding: 20px 16px 24px; }
}
@media (max-width: 350px) {
  .report-actions { flex-wrap: wrap; }
  .dashboard-horizon { max-width: 100%; gap: 2px; padding-right: 4px; }
}
@media (max-width: 340px) {
  .primary-nav, .nav-section-items { gap: 2px; }
}
@keyframes catch-up-card-entry {
  from { opacity: 0; transform: translateX(18px); }
}
@keyframes dashboard-view-slide-out-left {
  to { transform: translateX(-30%); }
}
@keyframes dashboard-view-slide-in-right {
  from { transform: translateX(100%); }
}
@keyframes dashboard-view-slide-out-right {
  to { transform: translateX(100%); }
}
@keyframes dashboard-view-slide-in-left {
  from { transform: translateX(-30%); }
}
`;
