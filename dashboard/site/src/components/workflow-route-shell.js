/**
 * Shared workflow-route shell primitives for declarative composition.
 */

import { h } from '../dom.js';
import { text } from './count-formatters.js';
import { renderWorkflowIdentity } from './workflow-identity.js';
import { createRoutePageShell } from './route-page-shell.js';
import { findLink } from './link-content.js';
import { parseWorkflowRoute, workflowRouteValue } from './workflow-route.js';
import { createDebug } from '../debug.js';

const debugWorkflowRouteShell = createDebug('workflow-route-shell');

/**
 * @typedef {{
 *   rootClassName: string,
 *   contentClassName: string,
 *   selectMessage: string,
 *   description: string,
 *   navigationPage: 'campaigns'|'repositories',
 *   breadcrumbs: Array<{ label: string, href: string }> | undefined,
 *   currentTab: 'workflow-runtime'|'workflow-detail'|'workflow-runs',
 *   bodyRenderer: WorkflowRouteBodyRenderer | undefined
 * }} WorkflowRouteShellConfig
 */

/**
 * @typedef {(args: {
 *   context: import('./ui-elements.js').ElementRenderContext,
 *   route: { repository: string, workflow: string },
 *   workflow: Record<string, unknown>
 * }) => HTMLElement | null} WorkflowRouteBodyRenderer
 */

/**
 * @param {import('./ui-elements.js').ElementRenderContext} context
 * @param {WorkflowRouteShellConfig} config
 * @returns {HTMLElement}
 */
export function renderWorkflowRouteShell(context, config) {
  const workflow = context.sources[context.sourceNames[0]]?.rows[0];
  debugWorkflowRouteShell({
    event: 'initialized',
    currentTab: config.currentTab,
    workflowCount: workflow ? 1 : 0
  });
  return createRoutePageShell(context, {
    rootClassName: config.rootClassName,
    datasetKey: 'workflow',
    selectMessage: config.selectMessage,
    notFoundMessage: 'Workflow not found.',
    hasSelection: (routeValue) => parseWorkflowRoute(routeValue) !== null,
    currentTab: config.currentTab,
    tabListClassName: 'repository-tabs workflow-tabs',
    tabListAriaLabel: (title, routeValue) => {
      const route = parseWorkflowRoute(routeValue);
      return `${config.currentTab === 'workflow-runtime' ? title : route?.workflow ?? title} views`;
    },
    tabs: ({ routeValue, title }) => workflowTabs(routeValue, title),
    renderMatched: (routeValue) => {
      const route = parseWorkflowRoute(routeValue);
      if (!workflow || !route
          || qualifiedRepository(workflow).toLowerCase() !== route.repository.toLowerCase()
          || text(workflow.workflow) !== route.workflow) {
        debugWorkflowRouteShell({ event: 'not-found', currentTab: config.currentTab });
        return null;
      }
      debugWorkflowRouteShell({ event: 'matched', currentTab: config.currentTab });
      const name = workflowName(workflow);
      return {
        allocation: workflowRouteAllocation(config, route, workflow, name),
        content: renderWorkflowContent(context, config, route, workflow)
      };
    }
  });
}

/**
 * @param {WorkflowRouteShellConfig} config
 * @param {{ repository: string, workflow: string }} route
 * @param {Record<string, unknown>} workflow
 * @param {string} title
 */
function workflowRouteAllocation(config, route, workflow, title) {
  const workflowLink = routeTitleLink(workflow, `${title} workflow`);
  return {
    title,
    description: config.description
      .replace('{workflow}', text(workflow.workflow))
      .replace('{repository}', route.repository),
    ...(workflowLink ? { titleLink: workflowLink } : {}),
    ...(['review', 'live'].includes(text(workflow['rollout-mode']))
      ? { mode: text(workflow['rollout-mode']) }
      : {}),
    navigationPage: config.navigationPage === 'campaigns' && workflow.campaign ? 'campaigns' : 'repositories',
    ...(config.breadcrumbs
      ? {
          breadcrumbs: config.breadcrumbs.map((crumb) => ({
            label: crumb.label.replace('{repository}', route.repository),
            href: crumb.href.replace('{repository-encoded}', encodeURIComponent(route.repository))
          }))
        }
      : {})
  };
}

/**
 * @param {Record<string, unknown>} row
 * @param {string} label
 * @returns {{ href: string, label: string } | null}
 */
function routeTitleLink(row, label) {
  const link = findLink(row, 'workflow-link');
  const href = link?.externalHref ?? link?.href;
  return href && !href.startsWith('#')
    ? { href, label: `Open ${label} on GitHub` }
    : null;
}

/**
 * @param {string} routeValue
 * @param {string} _displayName
 */
function workflowTabs(routeValue, _displayName) {
  const route = parseWorkflowRoute(routeValue);
  if (!route) return [];
  const workflowQuery = `?workflow=${encodeURIComponent(workflowRouteValue(route.repository, route.workflow))}`;
  return [
    workflowTab('workflow-runtime', 'Insights', 'graph', workflowQuery),
    workflowTab('workflow-detail', 'Reports', 'issue', workflowQuery),
    workflowTab('workflow-runs', 'Runs', 'play', workflowQuery)
  ];
}

/**
 * @param {'workflow-runtime'|'workflow-detail'|'workflow-runs'} pageId
 * @param {string} label
 * @param {string} icon
 * @param {string} workflowQuery
 * @returns {{ id: string, label: string, icon: string, href: string }}
 */
function workflowTab(pageId, label, icon, workflowQuery) {
  return {
    id: pageId,
    label,
    icon,
    href: `#page-${pageId}${workflowQuery}`
  };
}

/**
 * @param {import('./ui-elements.js').ElementRenderContext} context
 * @param {WorkflowRouteShellConfig} config
 * @param {{ repository: string, workflow: string }} route
 * @param {Record<string, unknown>} workflow
 */
function renderWorkflowContent(context, config, route, workflow) {
  return h(
    'div',
    { className: config.contentClassName },
    renderWorkflowIdentity(workflow),
    config.bodyRenderer?.({ context, route, workflow }) ?? null
  );
}

/** @param {Record<string, unknown>} row */
function qualifiedRepository(row) {
  const repository = text(row.repository);
  if (repository.includes('/')) return repository;
  const organization = text(row.organization);
  return organization && repository ? `${organization}/${repository}` : repository;
}

/** @param {Record<string, unknown>} workflow */
function workflowName(workflow) {
  return text(workflow['workflow-name']) || text(workflow.workflow) || 'Unknown workflow';
}
