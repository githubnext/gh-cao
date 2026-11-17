export const modernizationStyles = `
.modernization-controls{min-width:0}
.modernization-controls h2,.modernization-controls h3,.modernization-controls h4{margin:0 0 8px}
.modernization-controls p{margin:8px 0;overflow-wrap:anywhere}
.modernization-description{color:var(--muted);max-width:75ch;font-size:13px}
.modernization-inputs,.modernization-action-bar{display:flex;flex-wrap:wrap;gap:12px;margin:16px 0}
.modernization-inputs{align-items:end}
.modernization-action-bar{align-items:start;gap:8px}
.modernization-action-bar>div{min-width:0}
.modernization-action-bar .modernization-description{max-width:28ch;margin-bottom:0;font-size:12px}
.modernization-inputs label,.modernization-plan label{display:flex;flex-direction:column;gap:6px;min-width:0;font-weight:600}
.modernization-inputs label:first-child{flex:1 1 300px}
.modernization-farm{display:flex;flex:1 1 300px;flex-direction:column;gap:6px;min-width:0}
.modernization-farm-label{font-weight:600}
.modernization-farm-name{font-size:14px;overflow-wrap:anywhere}
.modernization-farm-name code{font-size:14px}
.modernization-controls input,.modernization-controls select,.modernization-controls textarea{font:inherit;font-weight:400;color:var(--fg);background:var(--canvas-inset);border:1px solid var(--border);border-radius:6px;padding:8px 12px;min-height:44px;max-width:100%;box-sizing:border-box}
.modernization-controls input::placeholder{color:var(--muted);opacity:1}
.modernization-controls :is(input,select,textarea,summary):focus-visible{outline:2px solid var(--focus);outline-offset:2px}
.modernization-controls textarea{width:100%;font-family:var(--font-mono);font-size:13px;resize:vertical}
.modernization-plan,.modernization-preview,.modernization-current{margin-top:20px;padding-top:20px;border-top:1px solid var(--border)}
.modernization-status{display:block;color:var(--muted);font-size:13px;overflow-wrap:anywhere}
.modernization-status:empty{display:none}
.modernization-error{color:var(--danger)}
.modernization-notice{color:var(--attention)}
.modernization-footer{display:flex;flex-wrap:wrap;gap:12px 24px;align-items:baseline;padding-top:12px;border-top:1px solid var(--border-muted);font-size:13px}
.modernization-links{display:flex;flex-wrap:wrap;gap:16px}
.modernization-help{flex:1 1 200px;color:var(--muted);min-width:0}
.modernization-help summary{width:fit-content;min-height:24px;cursor:pointer}
.modernization-help p{max-width:75ch}
.modernization-controls code{overflow-wrap:anywhere;white-space:normal}
.modernization-controls button{min-height:44px;justify-content:center}
.modernization-controls button:disabled{background:var(--canvas-subtle);border-color:var(--border-muted);color:var(--muted);opacity:1;filter:none;cursor:not-allowed}
.modernization-check-header{display:flex;flex-wrap:wrap;justify-content:space-between;align-items:start;gap:12px 24px}
.modernization-check-header>div{flex:1 1 300px;min-width:0}
.modernization-checked-repository{font-size:13px;overflow-wrap:anywhere}
.modernization-revision{flex:0 1 480px;min-width:0;font-size:12px;color:var(--muted)}
.modernization-revision summary{cursor:pointer;min-height:32px;width:fit-content;padding:4px 0}
.modernization-tasks{list-style:none;padding:0;margin:16px 0}
.modernization-task{display:grid;grid-template-columns:minmax(0,1fr) max-content;gap:12px 24px;padding:16px 0;border-top:1px solid var(--border-muted)}
.modernization-task-main{min-width:0}
.modernization-task-title{display:flex;align-items:baseline;gap:10px;margin:0;font-size:14px;font-weight:600;line-height:1.5}
.modernization-task-title>code{flex:none}
.modernization-task-title>span{min-width:0;overflow-wrap:anywhere}
.modernization-task-meta{display:flex;flex-wrap:wrap;gap:4px 20px;margin-top:6px;color:var(--muted);font-size:12px;line-height:1.5}
.modernization-task-repository{overflow-wrap:anywhere;min-width:0}
.modernization-task-evidence{display:inline-flex;flex-wrap:wrap;gap:12px}
.modernization-task-state-group{display:flex;flex-direction:column;align-items:end;gap:6px}
.modernization-task-phase{font-size:12px;color:var(--muted)}
.modernization-task-state[data-native-state="ready"]{color:var(--accent);background:var(--accent-muted);border-color:color-mix(in srgb,var(--accent) 45%,var(--border))}
.modernization-task-state:is([data-native-state="blocked"],[data-native-state="in-review"],[data-native-state="closed-issue"]){color:var(--attention);background:var(--attention-muted);border-color:color-mix(in srgb,var(--attention) 45%,var(--border))}
.modernization-revision summary:focus-visible{outline:2px solid var(--focus);outline-offset:2px}
.modernization-plan label{margin:12px 0}
.modernization-plan [hidden]{display:none!important}
.modernization-plan-tabs{display:flex;gap:20px;border-bottom:1px solid var(--border);margin-top:20px}
.modernization-plan-tabs button{background:none;color:var(--muted);border:0;border-bottom:2px solid transparent;padding:8px 4px;font:inherit;cursor:pointer}
.modernization-plan-tabs button[aria-selected="true"]{color:var(--fg);border-bottom-color:var(--accent);font-weight:600}
.modernization-plan-tabs button:hover{color:var(--fg)}
.modernization-plan-tabs button:focus-visible{outline:2px solid var(--focus);outline-offset:2px}
.modernization-plan-panel{padding:20px 0;min-width:0;max-height:65vh;overflow:auto;scrollbar-gutter:stable}
.modernization-plan-panel:focus-visible{outline:2px solid var(--focus);outline-offset:2px;border-radius:6px}
.modernization-plan-panel textarea{min-height:320px}
.modernization-plan-overview{padding-right:12px;overflow-wrap:anywhere}
.modernization-plan-overview h5{font-size:15px;font-weight:600;margin:0 0 12px}
.modernization-plan-overview h6{font-size:14px;font-weight:600;margin:0}
.modernization-plan-overview section+section,.modernization-plan-intro+section{margin-top:24px;padding-top:20px;border-top:1px solid var(--border-muted)}
.modernization-plan-intro>p{max-width:75ch;line-height:1.6}
.modernization-plan-objective{max-width:75ch;line-height:1.6;margin:8px 0}
.modernization-plan-fields{display:grid;grid-template-columns:minmax(100px,150px) minmax(0,1fr);gap:8px 16px;font-size:13px;margin:12px 0 0}
.modernization-plan-fields dt{color:var(--muted)}
.modernization-plan-fields dd{margin:0;min-width:0}
.modernization-plan-fields ul{margin:0;padding-left:18px}
.modernization-plan-fields li+li{margin-top:6px}
.modernization-plan-repositories,.modernization-plan-tasks{list-style:none;padding:0;margin:0}
.modernization-plan-repositories>li+li,.modernization-plan-tasks>li+li{margin-top:20px;padding-top:20px;border-top:1px solid var(--border-muted)}
@media(max-width:600px){.modernization-inputs{align-items:stretch}.modernization-inputs label,.modernization-inputs button,.modernization-farm{flex:1 1 100%}.modernization-action-bar{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));align-items:start}.modernization-action-bar button{width:100%;height:auto}.modernization-footer{gap:12px}.modernization-help{flex-basis:100%}}
@media(max-width:600px){.modernization-plan-fields{grid-template-columns:minmax(0,1fr);gap:4px}.modernization-plan-fields dd+dt{margin-top:8px}}
@media(max-width:600px){.modernization-task{grid-template-columns:minmax(0,1fr);gap:10px}.modernization-task-state-group{flex-direction:row;align-items:center;gap:10px}.modernization-revision{flex-basis:100%}}
.modernization-loading{display:flex;align-items:flex-start;gap:10px}
.modernization-spinner{flex:none;width:16px;height:16px;margin-top:2px;border:2px solid var(--border);border-top-color:var(--accent);border-radius:50%;animation:dashboard-refresh-spin .8s linear infinite}
@media(prefers-reduced-motion:reduce){.modernization-spinner{animation:none;border-top-color:var(--border);border-right-color:var(--accent)}}
`;
