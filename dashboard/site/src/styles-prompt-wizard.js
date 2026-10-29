export const promptWizardStyles = `
.prompt-wizard{max-width:960px;margin:0 auto;min-width:0}
.prompt-wizard-progress{display:flex;gap:12px;list-style:none;padding:0;margin:24px 0;flex-wrap:wrap}
.prompt-wizard-progress button{padding:8px 12px;border:1px solid var(--border);border-radius:6px;color:var(--fg);background:var(--canvas);font:inherit;cursor:pointer;text-align:left}
.prompt-wizard-progress button:disabled{color:var(--muted);cursor:default}
.prompt-wizard-progress button:enabled:hover{border-color:var(--accent);background:var(--canvas-subtle)}
.prompt-wizard-progress [aria-current="step"]{color:var(--accent);border-color:var(--accent);font-weight:600}
.prompt-wizard-step{border:0;padding:0;min-width:0;margin:0}
.prompt-wizard-step>legend{font-size:20px;font-weight:600;padding:0}
.prompt-wizard-field{margin:20px 0;min-width:0}
.prompt-wizard-field>label,.prompt-wizard-field>legend{display:block;font-weight:600;margin-bottom:8px}
.prompt-wizard-field :is(input[type="text"],textarea,select){box-sizing:border-box;width:100%;min-height:44px;padding:8px 12px;font:inherit;color:var(--fg);background:var(--canvas);border:1px solid var(--border);border-radius:6px}
.prompt-wizard :is(input,textarea,select,button):focus-visible{outline:2px solid var(--focus);outline-offset:2px}
.prompt-wizard-field textarea{resize:vertical}
.prompt-wizard-help{white-space:pre-line;color:var(--muted);font-size:13px}
.prompt-wizard-help:empty{display:none}
.prompt-wizard-options{padding:12px;border:1px solid var(--border);border-radius:6px}
.prompt-wizard-options>.prompt-wizard-choice{display:flex;gap:12px;align-items:flex-start;padding:10px;margin:0;font-weight:400;min-height:44px;cursor:pointer}
.prompt-wizard-choice input{margin-top:4px;flex:none}
.prompt-wizard-choice small{display:block;color:var(--muted)}
.prompt-wizard-actions{display:flex;justify-content:space-between;gap:12px;margin-top:24px;padding:16px 0;border-top:1px solid var(--border)}
.prompt-wizard-cards{border:0;padding:0}
.prompt-wizard .prompt-wizard-cards .entity-card-list-marketplace .prompt-wizard-card{display:grid;grid-template-columns:minmax(0,1fr);grid-template-rows:48px 1fr;min-height:200px;gap:14px;padding:20px;border:1px solid var(--border);border-radius:6px;cursor:pointer;color:var(--fg);font:inherit;text-align:left}
.prompt-wizard-cards .prompt-wizard-card .issue-list-card-icon{grid-column:1;grid-row:1;width:48px;height:48px}
.prompt-wizard-cards .prompt-wizard-card .issue-list-card-content{grid-column:1;grid-row:2;gap:8px}
.prompt-wizard-cards .prompt-wizard-card .issue-list-card-subtitle{display:-webkit-box;-webkit-box-orient:vertical;-webkit-line-clamp:3;overflow:hidden}
.prompt-wizard .prompt-wizard-cards .prompt-wizard-card[aria-pressed="true"]{border-color:var(--accent);background:var(--canvas-subtle);box-shadow:inset 0 0 0 1px var(--accent)}
.prompt-wizard .prompt-wizard-cards .prompt-wizard-card:hover{border-color:var(--accent)}
.prompt-wizard :is(button,.table-intent-button){min-height:44px}
.prompt-wizard-actions>.table-intent-control{margin-left:auto}
.prompt-wizard-status{display:block;color:var(--muted)}
.prompt-wizard-field-error{color:var(--danger);font-weight:600;overflow-wrap:anywhere}
.prompt-wizard-field [aria-invalid="true"]{border-color:var(--danger)}
.prompt-wizard-errors{margin-top:16px;padding:16px;border:1px solid var(--danger);border-radius:6px;overflow-wrap:anywhere}
.prompt-wizard-errors>strong{color:var(--danger)}
.prompt-wizard-errors ul{padding-left:20px;margin-bottom:0}
.prompt-wizard-errors li+li{margin-top:12px}
.prompt-wizard-errors li p{margin:0}
.prompt-wizard-errors button{max-width:100%;padding:0;border:0;background:none;color:var(--accent);font:inherit;text-align:left;text-decoration:underline;cursor:pointer}
.prompt-wizard [hidden]{display:none!important}
@media(max-width:600px){.prompt-wizard-progress{gap:6px}.prompt-wizard-progress button{font-size:12px;padding:6px}.prompt-wizard-options{padding:6px}}
`;
