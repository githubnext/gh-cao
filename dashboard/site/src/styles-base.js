export const baseStyles = `:root {
  color-scheme: dark;
  --font-sans: "Mona Sans", -apple-system, BlinkMacSystemFont, "Segoe UI", Helvetica, Arial, sans-serif, "Apple Color Emoji", "Segoe UI Emoji";
  --font-mono: "Mona Sans Mono", ui-monospace, SFMono-Regular, Consolas, "Liberation Mono", monospace;
  --canvas: #0d1117;
  --canvas-subtle: #151b23;
  --canvas-inset: #010409;
  --header: #010409;
  --fg: #f0f6fc;
  --muted: #9198a1;
  --border: #3d444d;
  --border-muted: #21262d;
  --accent: #58a6ff;
  --accent-muted: #121d2f;
  --success: #3fb950;
  --success-muted: #12261e;
  --danger: #f85149;
  --cancelled: #8c959f;
  --purple: #a371f7;
  --pink: #db61a2;
  --coral: #f78166;
  --yellow: #e3b341;
  --cyan: #39c5cf;
  --lime: #56d364;
  --violet: #bc8cff;
  --attention: #d29922;
  --attention-muted: #272115;
  --neutral-muted: #6e768166;
  --focus: #58a6ff;
  --on-emphasis: #ffffff;
}
@media (prefers-color-scheme: light) {
  :root {
    color-scheme: light;
    --canvas: #ffffff;
    --canvas-subtle: #f6f8fa;
    --canvas-inset: #f6f8fa;
    --header: #f6f8fa;
    --fg: #1f2328;
    --muted: #59636e;
    --border: #d1d9e0;
    --border-muted: #d8dee4;
    --accent: #0969da;
    --accent-muted: #ddf4ff;
    --success: #1a7f37;
    --success-muted: #dafbe1;
    --danger: #cf222e;
    --cancelled: #656d76;
    --purple: #8250df;
    --pink: #bf3989;
    --coral: #bc4c00;
    --yellow: #7d4e00;
    --cyan: #007d8a;
    --lime: #2da44e;
    --violet: #6639ba;
    --attention: #9a6700;
    --attention-muted: #fff8c5;
    --neutral-muted: #afb8c133;
    --focus: #0969da;
  }
}
:is(.dashboard-root, .first-load-overlay, .dashboard-notifications)[data-theme="dark"] {
  color-scheme: dark;
  --canvas: #0d1117;
  --canvas-subtle: #151b23;
  --canvas-inset: #010409;
  --header: #010409;
  --fg: #f0f6fc;
  --muted: #9198a1;
  --border: #3d444d;
  --border-muted: #21262d;
  --accent: #58a6ff;
  --accent-muted: #121d2f;
  --success: #3fb950;
  --success-muted: #12261e;
  --danger: #f85149;
  --cancelled: #8c959f;
  --purple: #a371f7;
  --pink: #db61a2;
  --coral: #f78166;
  --yellow: #e3b341;
  --cyan: #39c5cf;
  --lime: #56d364;
  --violet: #bc8cff;
  --attention: #d29922;
  --attention-muted: #272115;
  --neutral-muted: #6e768166;
  --focus: #58a6ff;
  --on-emphasis: #ffffff;
}
:is(.dashboard-root, .first-load-overlay, .dashboard-notifications)[data-theme="light"] {
  color-scheme: light;
  --canvas: #ffffff;
  --canvas-subtle: #f6f8fa;
  --canvas-inset: #f6f8fa;
  --header: #f6f8fa;
  --fg: #1f2328;
  --muted: #59636e;
  --border: #d1d9e0;
  --border-muted: #d8dee4;
  --accent: #0969da;
  --accent-muted: #ddf4ff;
  --success: #1a7f37;
  --success-muted: #dafbe1;
  --danger: #cf222e;
  --cancelled: #656d76;
  --purple: #8250df;
  --pink: #bf3989;
  --coral: #bc4c00;
  --yellow: #7d4e00;
  --cyan: #007d8a;
  --lime: #2da44e;
  --violet: #6639ba;
  --attention: #9a6700;
  --attention-muted: #fff8c5;
  --neutral-muted: #afb8c133;
  --focus: #0969da;
  --on-emphasis: #ffffff;
}
* { box-sizing: border-box; }
html { scroll-behavior: smooth; -webkit-text-size-adjust: 100%; text-size-adjust: 100%; }
body { margin: 0; background: var(--canvas); color: var(--fg); font: .875rem/1.5 var(--font-sans); letter-spacing: 0; }
.dashboard-root { --dashboard-page-padding-inline: 24px; height: 100vh; min-height: 0; overflow: hidden; background: var(--canvas); color: var(--fg); font: .875rem/1.5 var(--font-sans); }
.octicon-sprite { width: 0; height: 0; position: absolute; overflow: hidden; }
.octicon { width: 16px; height: 16px; flex: 0 0 16px; fill: currentColor; vertical-align: text-bottom; }
a { color: var(--accent); text-decoration: none; text-underline-offset: 2px; transition: color 120ms ease; }
a:hover { text-decoration: underline; text-decoration-thickness: 2px; }
a:focus-visible, [tabindex]:focus-visible, button:focus-visible { outline: 2px solid var(--focus); outline-offset: 2px; }
::view-transition-old(root), ::view-transition-new(root) { animation-duration: 180ms; animation-timing-function: ease-out; }
:root[data-navigation-transition="tab"] { view-transition-name: none; }
:root[data-deferred-styles="pending"] .dashboard-page { visibility: hidden; }
:root[data-navigation-transition="tab"] .dashboard-page:not([hidden]) > :is(.custom-view-grid, .page-layout-grid, .dashboard-view-skeleton) { view-transition-name: dashboard-tab-content; }
::view-transition-group(dashboard-tab-content), ::view-transition-old(dashboard-tab-content), ::view-transition-new(dashboard-tab-content) { animation-duration: 120ms; animation-timing-function: ease-out; }
.skip-link { position: fixed; z-index: 10; top: -80px; left: 12px; padding: 7px 12px; border: 1px solid var(--focus); border-radius: 6px; background: var(--canvas); color: var(--accent); font-weight: 600; text-decoration: none; transition: top 120ms ease, color 120ms ease; }
.skip-link:focus { top: 8px; }
`;
