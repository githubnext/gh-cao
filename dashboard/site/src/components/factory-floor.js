import { renderFactoryElement } from './factory-elements.js';
import { renderFactoryStation } from './factory-station.js';
import { renderReactiveGrid } from './reactive-grid.js';
import { createDebug } from '../debug.js';

const debugFactoryFloor = createDebug('factory-floor');

/** @typedef {{ rows: () => Record<string, unknown>[], pending: () => boolean, unavailable: () => boolean }} SourceBinding */
/** @typedef {Record<string, SourceBinding>} SourceBindings */
/** @typedef {{ signal: AbortSignal }} FactoryFloorScope */
/** @typedef {'campaigns'|'repositories'|'issues'|'value-gains'} FactoryStationId */

/** @type {Record<FactoryStationId, { icon: string, label: string, href: string, format: 'count'|'percent', unavailableDetail: string, unavailableDescription: string }>} */
const STATION_DEFINITIONS = {
  campaigns: {
    icon: 'workflow',
    label: 'Factory health',
    href: '#page-workflows',
    format: 'percent',
    unavailableDetail: '0/0 healthy workflows',
    unavailableDescription: 'Factory health unavailable'
  },
  repositories: {
    icon: 'repo',
    label: 'Repository coverage',
    href: '#page-repositories',
    format: 'percent',
    unavailableDetail: '0/0 repositories reached',
    unavailableDescription: 'Repository coverage unavailable'
  },
  issues: {
    icon: 'issue',
    label: 'Issues & PRs',
    href: '#page-issues',
    format: 'count',
    unavailableDetail: '',
    unavailableDescription: 'Issue and pull request count unavailable'
  },
  'value-gains': {
    icon: 'trophy',
    label: 'Grader observations',
    href: '#page-graders',
    format: 'count',
    unavailableDetail: '',
    unavailableDescription: 'Grader result count unavailable'
  }
};

/**
 * @param {SourceBindings} sources
 * @param {boolean} animateNumbers
 * @param {FactoryFloorScope} scope
 * @param {Record<string, string>} roleNames
 * @param {FactoryStationId[]} selectedStations
 */
export function renderFactoryFloor(sources, animateNumbers, scope, roleNames, selectedStations) {
  const stations = selectedStations.map((id) => {
    const definition = STATION_DEFINITIONS[id];
    const source = sources[roleNames[id]];
    const station = renderFactoryStation(definition.icon, {
      animate: animateNumbers,
      format: definition.format,
      final: id === 'value-gains',
      href: definition.href,
      signal: scope.signal
    });
    station.bind(() => {
      const row = source.rows()[0] ?? {};
      const value = Number(row.value);
      return {
        pending: source.pending(),
        unavailable: source.unavailable(),
        label: definition.label,
        value: Number.isFinite(value) ? value : 0,
        displayValue: typeof row['display-value'] === 'string' ? row['display-value'] : undefined,
        detail: {
          text: typeof row.detail === 'string' ? row.detail : definition.unavailableDetail
        }
      };
    });
    return { id, definition, source, element: station.element };
  });
  debugFactoryFloor({ event: 'composed', stationCount: stations.length, animate: animateNumbers });

  return renderReactiveGrid({
    className: stations.length <= 2 ? 'factory-floor factory-floor-compact' : 'factory-floor',
    activeClassName: 'factory-floor-active',
    listClassName: 'factory-stations',
    items: () => stations,
    key: (station) => station.id,
    renderItem: (station) => station.element,
    active: () => stations.some(({ source }) => source.rows()[0]?.active === true),
    ariaLabel: () => `${stations.map(({ definition, source }) => {
      const row = source.rows()[0];
      return !source.pending() && !source.unavailable() && typeof row?.description === 'string'
        ? row.description
        : definition.unavailableDescription;
    }).join(', ')}.`,
    signal: scope.signal
  });
}

/**
 * Default role-to-source-name bindings for the overview page. A view may
 * override any entry through `config.sources` to bind the same element to
 * differently named sources.
 * @type {Record<string, string>}
 */
export const FLOOR_DEFAULT_SOURCES = {
  campaigns: 'overview-factory-station',
  repositories: 'overview-repository-station',
  issues: 'overview-issue-station',
  'value-gains': 'overview-value-station'
};

/**
 * Renders the JSON-selected factory floor from its declared query payloads.
 * @param {import('./ui-elements.js').ElementRenderContext} context
 */
export function renderFactoryFloorElement(context) {
  const configuredStations = Array.isArray(context.elementConfig?.stations) ? context.elementConfig.stations : null;
  const selectedStations = configuredStations
    ? /** @type {FactoryStationId[]} */ (configuredStations.filter((station) => Object.hasOwn(STATION_DEFINITIONS, station)))
    : /** @type {FactoryStationId[]} */ (['campaigns', 'repositories']);
  if (configuredStations && selectedStations.length !== configuredStations.length) {
    debugFactoryFloor({
      event: 'station-config-filtered',
      pageId: context.pageId,
      viewId: context.viewId,
      configuredCount: configuredStations.length,
      selectedCount: selectedStations.length
    });
  }
  debugFactoryFloor({ event: 'element-selected', pageId: context.pageId, viewId: context.viewId, stations: selectedStations.join(',') });
  return renderFactoryElement(
    context,
    FLOOR_DEFAULT_SOURCES,
    (roleNames) => selectedStations.map((station) => roleNames[station]),
    (sources, scope, roleNames) => renderFactoryFloor(
      sources,
      context.elementConfig?.animate === 'number',
      scope,
      roleNames,
      selectedStations
    )
  );
}
