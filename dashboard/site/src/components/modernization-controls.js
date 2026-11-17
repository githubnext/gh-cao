import { h } from '../dom.js';
import { state, render, effect, onCleanup } from '../reactive.js';
import { cliQueueAvailable } from '../data-processor.js';
import { executeDeclaredCliAction } from './cli-actions.js';
import { bindFactorySources, createFactoryScope } from './factory-elements.js';
import { isSafeHttpsUrl } from './ui-primitives.js';
import { publishNotification } from '../notification-service.js';
import { createDebug, diagnosticErrorName } from '../debug.js';
import { parseModernizationResult } from '../modernization-protocol.js';
import { renderInteractiveTabs } from './tab-nav.js';
import { renderModernizationPlanOverview } from './modernization-plan.js';
import { renderStatusBadge } from './badge.js';

const debug = createDebug('modernization:controls');
const labels = { assess: 'Assess system', 'approve-plan': 'Publish approved plan', run: 'Run next batch', verify: 'Verify results' };
const repositoryPattern = /^[A-Za-z0-9_.-]+\/[A-Za-z0-9_.-]+$/;
let nextId = 0;

/** @param {string | null | undefined} href @param {string} label */
function externalLink(href, label) {
  return href && isSafeHttpsUrl(href) ? h('a', { href, target: '_blank', rel: 'noopener noreferrer' }, label) : null;
}

/** @param {import('../modernization-protocol.js').ModernizationTask[]} tasks */
function taskList(tasks) {
  return h('ol', { className: 'modernization-tasks', 'aria-label': 'Server-checked task scope and phase' },
    ...tasks.map(task => {
      const issue = externalLink(task.issueUrl, 'Issue');
      const pull = externalLink(task.pullRequestUrl, 'Pull request');
      issue?.setAttribute('aria-label', `Issue for task ${task.id}`);
      pull?.setAttribute('aria-label', `Pull request for task ${task.id}`);
      const badge = renderStatusBadge(task.state ?? 'State not reported');
      badge.classList.add('modernization-task-state');
      badge.dataset.nativeState = typeof task.state === 'string' ? task.state : '';
      return h('li', { className: 'modernization-task', 'data-task-id': task.id },
        h('div', { className: 'modernization-task-main' },
          h('h5', { className: 'modernization-task-title' }, h('code', {}, task.id), h('span', {}, task.title)),
          h('div', { className: 'modernization-task-meta' },
            h('span', { className: 'modernization-task-repository' }, task.repository),
            h('span', { className: 'modernization-task-evidence' },
              ...(issue || pull ? [issue, pull] : ['No linked evidence'])))),
        h('div', { className: 'modernization-task-state-group' },
          badge,
          h('span', { className: 'modernization-task-phase' }, task.phase === 'issue' ? 'Issue phase'
            : task.phase === 'implement' ? 'Implementation phase'
              : task.phase === 'unavailable' ? 'Phase unavailable'
                : typeof task.phase === 'string' && task.phase ? `Phase: ${task.phase}` : 'Phase not reported')));
    }));
}

/** @param {import('./ui-elements.js').ElementRenderContext} context */
export function renderModernizationControls(context) {
  if (context.elementConfig?.body === 'campaign'
    && (context.routeParameters?.campaign ?? context.routeParameter) !== 'modernization') {
    return h('div', { hidden: true });
  }
  const scope = createFactoryScope();
  const sources = bindFactorySources(context.sources, context.sourceNames, context, {
    requestMissingSources: false, preserveSubscribedSources: context.sourcesSubscribed === true
  });
  const farms = () => [...new Set(context.sourceNames.flatMap(name => sources[name].rows())
    .map(row => String(row['control-repository'] ?? ''))
    .filter(value => repositoryPattern.test(value) && !value.includes('..')))];
  const farmsPending = () => context.sourceNames.some(name => sources[name].pending());
  const farmsUnavailable = () => context.sourceNames.some(name => sources[name].unavailable());
  const id = `modernization-controls-${++nextId}`;
  const actions = context.elementConfig?.actions ?? {};
  const available = cliQueueAvailable();
  const ui = state({
    repository: '',
    mode: /** @type {'review'|'live'} */ ('review'),
    busy: false, error: '', message: '', planText: '', planOpen: false, helpOpen: false, expired: false,
    planTab: 'overview',
    revisionOpen: false,
    status: /** @type {import('../modernization-protocol.js').ModernizationResult | null} */ (null),
    preview: /** @type {import('../modernization-protocol.js').ModernizationResult | null} */ (null),
    execution: /** @type {import('../modernization-protocol.js').ModernizationResult | null} */ (null)
  });
  const root = h('section', { className: 'modernization-controls', 'aria-label': 'Modernization actions' });
  const farm = () => {
    const known = farms();
    const chosen = ui.get().repository;
    return known.includes(chosen) ? chosen : known.length === 1 ? known[0] : '';
  };
  const validRepository = () => Boolean(farm());
  const authorized = (/** @type {import('../modernization-protocol.js').ModernizationOperation} */ action) =>
    ui.get().status?.capabilities?.[action]?.authorized === true;

  /**
   * @param {'status'|'preview'|'execution'} kind
   * @param {Record<string, string>} values
   * @param {string | undefined} [input]
   */
  async function request(kind, values, input) {
    const actionId = actions[kind === 'execution' ? 'execute' : kind];
    if (!actionId) throw new Error('The Modernization action is not declared by this dashboard.');
    let output = '';
    const result = await executeDeclaredCliAction(actionId, values, {
      input, signal: scope.signal,
      onOutput: event => { if (event.stream === 'stdout') output += event.data; }
    });
    if (scope.signal.aborted) return null;
    if (!result.ok) {
      if (output.includes('CAO_MODERNIZATION_RESULT:')) parseModernizationResult(output, kind, values.repository);
      throw new Error(result.error || 'The native action failed. Check command output before retrying.');
    }
    return parseModernizationResult(output, kind, values.repository);
  }

  /** @param {() => Promise<void>} action */
  async function perform(action) {
    if (ui.get().busy || scope.signal.aborted) return;
    ui.set(current => ({ ...current, busy: true, error: '', execution: null }));
    try { await action(); } catch (error) {
      if (scope.signal.aborted) return;
      const message = error instanceof Error ? error.message : 'Modernization action unavailable.';
      ui.set(current => ({ ...current, error: message, preview: null, message: '' }));
      publishNotification({ message, tone: 'error' });
      debug({ event: 'request-failed', errorName: diagnosticErrorName(error) });
    } finally {
      if (!scope.signal.aborted) ui.set(current => ({ ...current, busy: false }));
    }
  }

  const check = () => perform(async () => {
    ui.set(current => ({ ...current, preview: null, message: 'Checking current plan, policy and task evidence…' }));
    const result = await request('status', { repository: farm() });
    if (result) ui.set(current => ({
      ...current, status: result, planText: result.plan ? JSON.stringify(result.plan, null, 2) : '',
      message: result.reason || 'Current native checks loaded. Retained dashboard evidence is not execution authority.'
    }));
  });
  const preview = (/** @type {import('../modernization-protocol.js').ModernizationOperation} */ operation) => perform(async () => {
    const current = ui.get();
    const mode = operation === 'run' ? current.mode : 'review';
    ui.set(value => ({ ...value, preview: null, message: 'Preparing a current, revision-bound preview…' }));
    const result = await request('preview', { repository: farm(), operation, mode },
      operation === 'approve-plan' ? current.planText : undefined);
    if (result) {
      if (result.action !== operation || result.mode !== mode) throw new Error('The preview does not match the requested action and mode.');
      ui.set(value => ({ ...value, preview: result, expired: false, message: result.reason || 'Review the exact effects before confirming.' }));
      queueMicrotask(() => {
        if (!scope.signal.aborted) /** @type {HTMLElement | null} */ (root.querySelector('[data-preview-title]'))?.focus();
      });
    }
  });
  const confirm = () => perform(async () => {
    const current = ui.get();
    const reviewed = current.preview;
    if (!reviewed?.canExecute || !reviewed.previewId || Date.parse(reviewed.expiresAt ?? '') <= Date.now()) {
      throw new Error('This preview is unavailable or expired. Review the action again.');
    }
    ui.set(value => ({ ...value, preview: null, message: 'Submitting the confirmed action. Do not retry if the outcome is uncertain…' }));
    const result = await request('execution', { repository: farm(), preview: reviewed.previewId });
    if (result) {
      if (result.action !== reviewed.action) throw new Error('The native execution result does not match the confirmed action. Check command history before retrying.');
      if (result.accepted !== true) throw new Error(result.reason || 'The native operator did not confirm acceptance.');
      ui.set(value => ({ ...value, status: null, execution: result,
        message: result.action === 'approve-plan' ? 'The approved plan was published. Check readiness before the next action.'
          : 'Dispatch accepted—not proof of workflow success or completed work. Follow execution and resulting outputs below.' }));
      publishNotification({ message: 'Modernization action accepted.', tone: 'success' });
    }
  });

  render(root, () => {
    const current = ui.get();
    const disabled = !available || current.busy || !validRepository();
    const reviewed = current.preview;
    const known = farms();
    const selected = farm();
    return h('div', {},
      h(context.headingTag, {}, 'Modernization'),
      h('p', { className: 'modernization-description' }, 'Runs across every repository in this farm\'s approved plan. Check farm readiness, then review the next authorized action.'),
      !available ? h('p', { className: 'modernization-notice', role: 'status' },
        'Read-only deployment. Authorized native operator actions are unavailable here; no browser GitHub credentials or execution fallback is used.') : null,
      h('div', { className: 'modernization-inputs' },
        known.length > 1
          ? h('label', { htmlFor: `${id}-repository` }, 'Farm',
            h('select', { id: `${id}-repository`, value: selected, disabled: !available || current.busy,
              onChange: (/** @type {Event} */ event) => ui.set(value => ({ ...value,
                repository: /** @type {HTMLSelectElement} */ (event.target).value,
                status: null, preview: null, execution: null, planOpen: false, message: '', error: '' })) },
            h('option', { value: '' }, 'Choose a farm'), ...known.map(value => h('option', { value }, value))))
          : h('div', { className: 'modernization-farm' },
            h('span', { className: 'modernization-farm-label' }, 'Farm'),
            selected
              ? h('span', { className: 'modernization-farm-name' }, h('code', {}, selected),
                h('span', { className: 'modernization-description' }, ' control repository'))
              : h('span', { className: 'modernization-description' }, farmsPending() ? 'Finding this farm\'s Modernization coordinator…'
                : farmsUnavailable() ? 'Farm coordinator evidence is unavailable. Actions stay disabled until it loads.'
                  : 'No Modernization coordinator is recorded in this farm\'s retained evidence.')),
        h('button', { type: 'button', className: current.status ? 'cli-action-cancel' : 'cli-action-confirm',
          disabled, onClick: check }, current.busy ? 'Working…' : 'Check farm readiness'),
        h('label', { htmlFor: `${id}-mode` }, 'Next-batch mode',
          h('select', { id: `${id}-mode`, value: current.mode, disabled: !available || current.busy,
            onChange: (/** @type {Event} */ event) => ui.set(value => ({ ...value, preview: null,
              mode: /** @type {'review'|'live'} */ (/** @type {HTMLSelectElement} */ (event.target).value) })) },
          h('option', { value: 'review' }, 'Review only'), h('option', { value: 'live' }, 'Live issues / draft PRs')))
      ),
      current.error ? h('p', { role: 'alert', className: 'modernization-error' }, current.error) : null,
      h('output', { role: 'status', 'aria-live': 'polite', className: 'modernization-status' },
        current.message || (current.status || !available ? '' : validRepository()
          ? 'Check farm readiness to enable authorized actions.' : known.length > 1 ? 'Choose a farm to check available actions.' : '')),
      h('div', { className: 'modernization-action-bar', role: 'group', 'aria-label': 'Modernization lifecycle' },
        .../** @type {const} */ (['assess', 'approve-plan', 'run', 'verify']).map(operation => operation === 'approve-plan'
          ? h('button', { type: 'button', className: 'cli-action-cancel', disabled,
            'aria-expanded': current.planOpen && !reviewed, 'aria-controls': `${id}-approval`,
            onClick: () => {
              const opening = !ui.get().planOpen || Boolean(ui.get().preview);
              ui.set(value => ({ ...value, planOpen: value.preview ? true : !value.planOpen, preview: null, message: '' }));
              if (opening && !ui.get().status && !ui.get().planText.trim()) check();
            } }, 'Review / approve plan')
          : h('div', {},
          h('button', { type: 'button', className: operation === 'run' ? 'cli-action-confirm' : 'cli-action-cancel',
            disabled: disabled || !authorized(operation), onClick: () => preview(operation) },
          operation === 'run' ? 'Review next run' : labels[operation]),
          !authorized(operation) && current.status?.capabilities?.[operation]?.reason ? h('p', { className: 'modernization-description' },
            current.status.capabilities[operation]?.reason) : null
        ))
      ),
      h('div', { className: 'modernization-footer' },
        h('nav', { className: 'modernization-links', 'aria-label': 'Modernization evidence' },
          h('a', { href: '#page-campaign-issues?campaign=modernization' }, 'Retained reports'),
          h('a', { href: '#page-campaign-runs?campaign=modernization' }, 'Run history')),
        h('details', { className: 'modernization-help', open: current.helpOpen,
          onToggle: (/** @type {Event} */ event) => {
            const open = /** @type {HTMLDetailsElement} */ (event.target).open;
            if (open !== ui.get().helpOpen) ui.set(value => ({ ...value, helpOpen: open }));
          } },
        h('summary', {}, 'Scope and safeguards'),
        h('p', {}, 'Assess → review and approve a concrete plan → run the next batch → verify. Every write needs a fresh native policy check and your confirmation.'),
        h('p', {}, 'The farm is identified from retained inventory, not authorization; the native operator re-checks it. Review is the default; live mode cannot exceed checked-in policy.'))
      ),
      current.status ? h('section', { className: 'modernization-current', 'aria-label': 'Current Modernization readiness' },
        h('div', { className: 'modernization-check-header' },
          h('div', {},
            h('h4', {}, 'Current check: ', renderStatusBadge(current.status.status)),
            h('p', { className: 'modernization-checked-repository' }, current.status.controlRepository),
            current.status.repositories?.length ? h('p', { className: 'modernization-description' },
              `Plan covers ${current.status.repositories.length} ${current.status.repositories.length === 1 ? 'repository' : 'repositories'}: `,
              current.status.repositories.join(', ')) : null),
          h('details', { className: 'modernization-revision', open: current.revisionOpen,
            onToggle: (/** @type {Event} */ event) => {
              const open = /** @type {HTMLDetailsElement} */ (event.target).open;
              if (open !== ui.get().revisionOpen) ui.set(value => ({ ...value, revisionOpen: open }));
            } },
          h('summary', {}, 'Revision and plan details'),
          h('dl', { className: 'modernization-plan-fields' },
            h('dt', {}, 'Control revision'), h('dd', {}, h('code', {}, current.status.controlSha)),
            ...(current.status.planDigest ? [
              h('dt', {}, 'Approved plan digest'), h('dd', {}, h('code', {}, current.status.planDigest))
            ] : [])))),
        !current.planOpen && !reviewed && current.status.tasks?.length ? taskList(current.status.tasks) : null,
        h('div', { className: 'modernization-links' },
          externalLink(current.status.links?.issuesUrl, 'Inspect issues'),
          externalLink(current.status.links?.pullRequestsUrl, 'Inspect pull requests'))
      ) : null,
      current.planOpen ? h('div', { id: `${id}-approval`, className: 'modernization-plan', hidden: Boolean(reviewed) },
        h('h4', {}, 'Concrete plan approval'),
        h('p', {}, 'Paste or import the concrete engagement JSON. The native operator validates scope, exact baselines, specialists and dependencies. Confirmation publishes only the reviewed plan, never policy or rollout changes.'),
        current.status?.capabilities?.['approve-plan']?.reason ? h('p', { className: 'modernization-description' },
          current.status.capabilities['approve-plan'].reason) : null,
        h('label', { htmlFor: `${id}-file` }, 'Import plan JSON',
          h('input', { id: `${id}-file`, type: 'file', accept: '.json,application/json', disabled: current.busy,
            onChange: async (/** @type {Event} */ event) => {
              const file = /** @type {HTMLInputElement} */ (event.target).files?.[0];
              if (!file) return;
              if (file.size > 100000) { ui.set(value => ({ ...value, error: 'Choose a plan file no larger than 100,000 bytes.' })); return; }
              try {
                const text = await file.text();
                if (!scope.signal.aborted) ui.set(value => ({ ...value, planText: text, preview: null, error: '' }));
              } catch {
                if (!scope.signal.aborted) ui.set(value => ({ ...value, error: 'The selected plan file could not be read. Paste the JSON or choose the file again.' }));
              }
            } })),
        renderInteractiveTabs({
          className: 'modernization-plan-tabs', ariaLabel: 'Engagement plan view', panelId: `${id}-plan-panel`,
          tabs: [
            { label: 'Overview', value: 'overview', selected: current.planTab === 'overview' },
            { label: 'JSON', value: 'json', selected: current.planTab === 'json' }
          ],
          onSelect: planTab => {
            ui.set(value => ({ ...value, planTab }));
            queueMicrotask(() => {
              if (!scope.signal.aborted) /** @type {HTMLElement | null} */ (root.querySelector('.modernization-plan-tabs [aria-selected="true"]'))?.focus();
            });
          }
        }),
        h('div', { id: `${id}-plan-panel`, className: 'modernization-plan-panel', role: 'tabpanel',
          tabIndex: current.planTab === 'overview' ? 0 : -1,
          'aria-label': current.planTab === 'json' ? 'Plan JSON' : 'Plan overview' },
        h('div', { hidden: current.planTab !== 'overview' },
          current.planTab !== 'overview' ? null
            : !current.planText.trim() && current.busy ? h('p', { className: 'modernization-description', role: 'status' }, 'Loading the farm\'s approved plan. The native readiness check reads plan and task state from GitHub and can take about 30 seconds.')
              : !current.planText.trim() && current.status && !current.status.plan
                ? h('p', { className: 'modernization-description' }, 'This farm has no approved plan yet. Import a plan or open the JSON tab to start drafting.')
                : renderModernizationPlanOverview(current.planText,
                  Boolean(current.status?.plan) && current.planText === JSON.stringify(current.status?.plan, null, 2))),
        h('div', { hidden: current.planTab !== 'json' },
          h('label', { htmlFor: `${id}-plan` }, 'Engagement JSON',
            h('textarea', { id: `${id}-plan`, value: current.planText, rows: 14, disabled: current.busy, spellcheck: false,
              onInput: (/** @type {Event} */ event) => ui.set(value => ({ ...value,
                planText: /** @type {HTMLTextAreaElement} */ (event.target).value, preview: null })) })))),
        h('button', { type: 'button', className: 'cli-action-cancel',
          disabled: disabled || !current.planText.trim() || !authorized('approve-plan'), onClick: () => preview('approve-plan') }, 'Review approval')
      ) : null,
      reviewed ? h('section', { className: 'modernization-preview', 'aria-label': 'Review Modernization action' },
        h('h4', { tabIndex: -1, 'data-preview-title': '' }, labels[reviewed.action ?? 'run']),
        h('p', {}, 'Control revision ', h('code', {}, reviewed.controlSha), ' · mode ', h('strong', {}, reviewed.mode)),
        h('p', {}, 'Exact repository scope:'),
        h('ul', {}, ...(reviewed.repositories ?? []).map(repository => h('li', {}, repository))),
        reviewed.planDigest ? h('p', {}, 'Plan digest ', h('code', {}, reviewed.planDigest)) : null,
        reviewed.tasks?.length ? taskList(reviewed.tasks) : null,
        h('ul', {}, ...(reviewed.effects ?? []).map(text => h('li', {}, text))),
        h('ul', { className: 'modernization-notice' }, ...(reviewed.warnings ?? []).map(text => h('li', {}, text))),
        h('p', {}, reviewed.canExecute !== true ? reviewed.reason || 'Execution is unavailable; nothing can be confirmed.'
          : current.expired ? 'Preview expired. Review the action again.' : `Expires ${reviewed.expiresAt}. Any revision, policy or plan change requires a new review.`),
        h('div', { className: 'modernization-action-bar' },
          h('button', { type: 'button', className: 'cli-action-confirm',
            disabled: disabled || current.expired || reviewed.canExecute !== true, onClick: confirm }, labels[reviewed.action ?? 'run']),
          h('button', { type: 'button', className: 'cli-action-cancel', disabled: current.busy,
            onClick: () => ui.set(value => ({ ...value, preview: null, message: 'Preview dismissed. Nothing was dispatched or published.' })) }, 'Dismiss')
        )
      ) : null,
      current.execution ? h('div', { className: 'modernization-current' },
        externalLink(current.execution.runUrl ?? current.execution.workflowUrl, 'Follow workflow execution'), ' ',
        externalLink(current.execution.approvalCommitUrl, 'View approved plan commit')) : null
    );
  }, { signal: scope.signal });
  effect(() => {
    const expiresAt = ui.get().preview?.expiresAt;
    if (!expiresAt) return;
    const timer = setTimeout(() => {
      if (!scope.signal.aborted) ui.set(value => ({ ...value, expired: true }));
    }, Math.max(0, Math.min(2147483647, Date.parse(expiresAt) - Date.now())));
    onCleanup(() => clearTimeout(timer));
  }, { signal: scope.signal });
  scope.bind(root);
  return root;
}
