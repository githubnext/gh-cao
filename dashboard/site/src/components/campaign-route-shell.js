/**
 * Shared campaign-route shell primitives for declarative composition.
 */

import { createRoutePageShell } from './route-page-shell.js';
import { normalizeCampaignRoute, campaignNameForRoute } from './campaign-route-composition.js';
import { CAMPAIGN_ROUTE_TABS } from './route-body-specification.js';
import { renderEmptyMessage, renderLoadingMessage } from './ui-primitives.js';
import { text } from './count-formatters.js';
import { findLink } from './link-content.js';
import { bindFactorySources, createFactoryScope } from './factory-elements.js';
import { effect } from '../reactive.js';
import { createDebug } from '../debug.js';

const CAMPAIGN_TAB_COUNT_SOURCES = Object.freeze({
  insights: 'campaign-insight-tab-counts',
  problems: 'campaign-problem-tab-counts',
  issues: 'campaign-issue-tab-counts'
});

const debugCampaignRouteShell = createDebug('campaign-route-shell');

/**
 * @typedef {{
 *   rootClassName: string,
 *   selectMessage: string,
 *   description: string,
 *   currentTab: 'overview'|'workflows'|'runs'|'issues'|'repositories'|'insights'|'problems'|'reports'|'memory',
 *   bodyRenderer: CampaignRouteBodyRenderer | undefined
 * }} CampaignRouteShellConfig
 */

/**
 * @typedef {(args: {
 *   context: import('./ui-elements.js').ElementRenderContext,
 *   campaignId: string,
 *   campaignName: string,
 *   workflows: Array<Record<string, unknown>>
 * }) => HTMLElement | null} CampaignRouteBodyRenderer
 */

/**
 * @param {import('./ui-elements.js').ElementRenderContext} context
 * @param {CampaignRouteShellConfig} config
 * @returns {HTMLElement}
 */
export function renderCampaignRouteShell(context, config) {
  const bindingOptions = context.sourcesSubscribed
    ? { requestMissingSources: false, preserveSubscribedSources: true } : undefined;
  const workflowBindings = bindFactorySources(context.sources, ['workflows'], context, bindingOptions);
  const declaredCounts = Object.values(CAMPAIGN_TAB_COUNT_SOURCES)
    .filter((name) => context.sourceNames.includes(name));
  const otherCounts = Object.values(CAMPAIGN_TAB_COUNT_SOURCES)
    .filter((name) => !context.sourceNames.includes(name));
  const countBindings = {
    ...bindFactorySources(context.sources, declaredCounts, context, bindingOptions),
    ...bindFactorySources(context.sources, otherCounts, context)
  };
  const bindings = { ...workflowBindings, ...countBindings };
  const scope = createFactoryScope();
  const root = createRoutePageShell(context, {
    rootClassName: config.rootClassName,
    datasetKey: 'campaign',
    selectMessage: config.selectMessage,
    notFoundMessage: 'Campaign not found.',
    unavailableMessage: 'Campaign data is unavailable.',
    isUnavailable: () => {
      if (bindings.workflows.unavailable()) return true;
      return !bindings.workflows.pending()
        && bindings.workflows.empty()
        && bindings.workflows.source()?.metadata?.completeness === 'complete';
    },
    hasSelection: (routeValue) => normalizeCampaignRoute(routeValue).length > 0,
    currentTab: config.currentTab,
    tabListClassName: 'campaign-tabs',
    tabListAriaLabel: (title) => `${title} views`,
    tabs: ({ routeValue, title, description }) => campaignTabs(
      routeValue,
      title,
      description,
      campaignTabCounts(bindings, routeValue),
      Object.fromEntries(Object.entries(CAMPAIGN_TAB_COUNT_SOURCES)
        .map(([tabId, sourceName]) => [tabId, bindings[sourceName].unavailable()]))
    ),
    pageLevelTabs: true,
    renderMatched: (routeValue) => {
      const campaignId = normalizeCampaignRoute(routeValue);
      const workflows = bindings.workflows.rows();
      if (workflows.length === 0) {
        const databaseLoading = bindings.workflows.pending();
        if (databaseLoading) {
          const campaignName = campaignNameForRoute(campaignId, workflows);
          debugCampaignRouteShell({ event: 'route-render', tab: config.currentTab, status: 'loading' });
          return {
            allocation: {
              title: campaignName,
              description: config.description.replace('{campaignName}', campaignName),
              navigationPage: 'campaigns'
            },
            content: renderLoadingMessage('Loading campaign data...')
          };
        }
        const workflowsSource = bindings.workflows.source();
        const stillCollecting = workflowsSource?.metadata?.availability !== 'unavailable'
          && workflowsSource?.metadata?.completeness !== 'complete';
        if (stillCollecting && workflowsSource?.metadata?.completeness === 'partial') {
          debugCampaignRouteShell({ event: 'route-render', tab: config.currentTab, status: 'loading-partial' });
          const campaignName = campaignNameForRoute(campaignId, workflows);
          return {
            allocation: {
              title: campaignName,
              description: config.description.replace('{campaignName}', campaignName),
              navigationPage: 'campaigns'
            },
            content: renderLoadingMessage('Loading campaign data...')
          };
        }
        if (bindings.workflows.empty()) {
          debugCampaignRouteShell({ event: 'route-render', tab: config.currentTab, status: 'empty' });
          const campaignName = campaignNameForRoute(campaignId, workflows);
          return {
            allocation: {
              title: campaignName,
              description: config.description.replace('{campaignName}', campaignName),
              navigationPage: 'campaigns'
            },
            content: renderEmptyMessage('Campaign data will appear after the first data load completes.')
          };
        }
        debugCampaignRouteShell({ event: 'route-render', tab: config.currentTab, status: 'not-found' });
        return null;
      }
      const campaignName = campaignNameForRoute(campaignId, workflows);
      const titleLink = campaignTitleLink(campaignName, workflows);
      debugCampaignRouteShell({ event: 'route-render', tab: config.currentTab, status: 'matched', workflowCount: workflows.length });
      return {
        allocation: {
          title: campaignName,
          description: config.description.replace('{campaignName}', campaignName),
          ...(titleLink ? { titleLink } : {}),
          navigationPage: 'campaigns'
        },
        content: config.bodyRenderer?.({ context, campaignId, campaignName, workflows }) ?? null
      };
    }
  });
  effect(() => {
    for (const binding of Object.values(bindings)) {
      binding.rows();
      binding.pending();
      binding.empty();
      binding.unavailable();
    }
    const routeValue = root.dataset.campaign ?? '';
    if (!routeValue) return;
    root.dispatchEvent(new CustomEvent('dashboard-route-change', {
      detail: {
        parameter: context.routeParameter,
        value: routeValue
      }
    }));
  }, { signal: scope.signal });
  scope.bind(root);
  return root;
}

/**
 * @param {string} campaignName
 * @param {Array<Record<string, unknown>>} workflows
 * @returns {{ href: string, label: string } | null}
 */
function campaignTitleLink(campaignName, workflows) {
  for (const workflow of workflows) {
    const repositoryLink = findLink(workflow, 'repository-link');
    const repositoryHref = repositoryLink?.externalHref ?? repositoryLink?.href;
    if (!repositoryHref || repositoryHref.startsWith('#')) continue;
    const href = campaignFolderHref(repositoryHref, text(workflow['campaign-readme-path']));
    if (href) {
      return {
        href,
        label: `Open ${campaignName} campaign source on GitHub`
      };
    }
  }
  return null;
}

/**
 * Resolves the source folder that contains a campaign aw.yml by assuming it
 * lives beside the campaign README path supplied by inventory. Returns an
 * empty string when the repository URL is not an owner/repo root or the README
 * path does not identify a campaign folder.
 * @param {string} repositoryHref
 * @param {string} readmePath
 * @returns {string}
 */
function campaignFolderHref(repositoryHref, readmePath) {
  try {
    const url = new URL(repositoryHref);
    if (url.protocol !== 'https:') return '';
    const repositorySegments = url.pathname.split('/').filter(Boolean);
    if (repositorySegments.length !== 2) return '';
    const directory = readmePath.includes('/') ? readmePath.slice(0, readmePath.lastIndexOf('/')) : '';
    if (!directory) return '';
    const encodedDirectory = directory.split('/').map(encodeURIComponent).join('/');
    url.pathname = `${url.pathname.replace(/\/$/, '')}/tree/HEAD/${encodedDirectory}`;
    return url.href;
  } catch {
    return '';
  }
}

/**
 * @param {Record<string, { rows: () => Array<Record<string, unknown>> }>} bindings
 * @param {string} campaignId
 */
function campaignTabCounts(bindings, campaignId) {
  return Object.fromEntries(Object.entries(CAMPAIGN_TAB_COUNT_SOURCES).map(([tabId, sourceName]) => {
    const row = bindings[sourceName].rows().find((candidate) => candidate.campaign === campaignId);
    const count = Number(row?.items);
    return [tabId, Number.isFinite(count) && count > 0 ? count : 0];
  }));
}

/**
 * @param {string} campaignId
 * @param {string} campaignName
 * @param {string} campaignDescription
 * @param {Record<string, number>} counts
 * @param {Record<string, boolean>} unavailableCounts
 */
function campaignTabs(campaignId, campaignName, campaignDescription, counts, unavailableCounts) {
  const campaignQuery = `?campaign=${encodeURIComponent(campaignId)}`;
  return CAMPAIGN_ROUTE_TABS.map((tab) => ({
    id: tab.id,
    label: unavailableCounts[tab.id] ? `${tab.label} (count unavailable)` : tab.label,
    icon: tab.icon,
    href: `#page-${tab.page}${campaignQuery}`,
    count: (counts[tab.id] ?? 0) > 0 ? counts[tab.id] : undefined,
    trailingIcon: 'chevron-right',
    routeTitle: campaignName,
    routeDescription: campaignDescription
  }));
}
