export const governanceStyles = `.inventory-sync { display: flex; align-items: center; flex-wrap: wrap; gap: 12px; padding: 12px; }
.inventory-sync button { min-height: 44px; }
.inventory-sync button[aria-busy="true"] { cursor: wait; }
.inventory-sync [role="status"] { color: var(--muted); overflow-wrap: anywhere; }
.environment-defaults { display: grid; gap: 16px; min-width: 0; padding: 20px; }
.environment-defaults p { margin: 0; color: var(--muted); line-height: 1.5; overflow-wrap: anywhere; }
.environment-defaults-controls { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 16px; }
.environment-defaults label { display: grid; align-content: start; gap: 6px; min-width: 0; font-weight: 600; }
.environment-defaults [hidden] { display: none; }
.environment-defaults :is(input, select, textarea) { width: 100%; min-width: 0; min-height: 44px; padding: 8px 10px; border: 1px solid var(--border); border-radius: 6px; color: var(--fg); background: var(--canvas); font: inherit; }
.environment-defaults textarea { resize: vertical; font-family: var(--font-mono, monospace); font-size: .8125rem; line-height: 1.6; }
.environment-defaults :is(input, select, textarea):focus-visible { outline: 2px solid var(--focus); outline-offset: 2px; }
.environment-defaults-actions { display: flex; gap: 8px; flex-wrap: wrap; }
.environment-defaults-actions button { min-height: 44px; }
.environment-defaults-status { overflow-wrap: anywhere; }
@media (max-width: 700px) { .environment-defaults { padding: 16px; } .environment-defaults-controls { grid-template-columns: minmax(0, 1fr); } }
`;
