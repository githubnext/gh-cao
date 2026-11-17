export const modernizationStyles = `
.modernization-controls{min-width:0}
.modernization-controls h2,.modernization-controls h3,.modernization-controls h4{margin:0 0 12px}
.modernization-controls p{margin:8px 0;overflow-wrap:anywhere}
.modernization-description{color:var(--fgColor-muted,var(--color-fg-muted));max-width:75ch;font-size:13px}
.modernization-inputs,.modernization-action-bar{display:flex;flex-wrap:wrap;gap:12px;align-items:end;margin:16px 0}
.modernization-inputs label,.modernization-plan label{display:flex;flex-direction:column;gap:6px;min-width:0;font-weight:600}
.modernization-inputs label:first-child{flex:1 1 300px}
.modernization-controls input,.modernization-controls select,.modernization-controls textarea{font:inherit;color:var(--fgColor-default,var(--color-fg-default));background:var(--bgColor-default,var(--color-canvas-default));border:1px solid var(--borderColor-default,var(--color-border-default));border-radius:6px;padding:8px 12px;min-height:44px;max-width:100%;box-sizing:border-box}
.modernization-controls textarea{width:100%;font-family:var(--fontStack-monospace,monospace);font-size:13px}
.modernization-plan,.modernization-preview,.modernization-current{margin-top:20px;padding-top:20px;border-top:1px solid var(--borderColor-default,var(--color-border-default))}
.modernization-status{display:block;overflow-wrap:anywhere}
.modernization-error{color:var(--fgColor-danger,var(--color-danger-fg))}
.modernization-notice{color:var(--fgColor-attention,var(--color-attention-fg))}
.modernization-controls code{overflow-wrap:anywhere;white-space:normal}
.modernization-controls button{min-height:44px}
.modernization-table-scroll{max-width:100%;overflow-x:auto;margin:12px 0}
.modernization-task-table{width:100%;border-collapse:collapse;font-size:13px}
.modernization-task-table th,.modernization-task-table td{text-align:left;vertical-align:top;padding:8px;border-bottom:1px solid var(--borderColor-default,var(--color-border-default));overflow-wrap:anywhere;min-width:100px}
.modernization-task-table th{color:var(--fgColor-muted,var(--color-fg-muted))}
.modernization-plan label{margin:12px 0}
@media(max-width:600px){.modernization-inputs{align-items:stretch}.modernization-inputs label,.modernization-inputs button{flex:1 1 100%}.modernization-action-bar{align-items:stretch}.modernization-action-bar>div{flex:1 1 100%}.modernization-action-bar button{width:100%}}
`;
