import { describe, expect, it } from 'vitest';
import {
  elementHandlesEmptyRows,
  elementBindsPageSources,
  elementLoadsSourcesAsync,
  elementPageSubscriptionSourceNames,
  renderUiElement
} from '../../src/components/ui-elements.js';

const REMOVED_ELEMENTS = [
  'domain-attention',
  'campaign-status-grid',
  'summary-grid',
  'readiness-verdict',
  'context-summary',
  'anomaly-readiness',
  'signal-list',
  'needs-attention-list',
  'campaign-activity',
  'campaign-utilization',
  'campaign-run-trend',
  'campaign-summary-table',
  'campaign-activity-shell',
  'workflow-route',
  'configuration-actions',
  'work-project-view',
  'insights-overview',
  'outcomes-overview',
  'local-database'
];

describe('UI element registry', () => {
  it('delegates missing outcome sections to the shared empty-state renderer', () => {
    expect(elementHandlesEmptyRows('outcome-detail')).toBe(true);
    expect(elementHandlesEmptyRows('outcome-detail-section')).toBe(false);
  });

  it.each(REMOVED_ELEMENTS)('does not expose the unreferenced %s renderer', (element) => {
    expect(renderUiElement(element, /** @type {never} */ ({}))).toBeNull();
    expect(elementHandlesEmptyRows(element)).toBe(false);
    expect(elementLoadsSourcesAsync(element)).toBe(false);
  });

  it.each(['factory-header', 'factory-floor', 'all-campaign-memory', 'link-button-list'])(
    'keeps independently loaded sources for the live %s renderer',
    (element) => {
      expect(elementLoadsSourcesAsync(element)).toBe(true);
      expect(elementHandlesEmptyRows(element)).toBe(true);
    }
  );

  it('retains problem detail DOM while the page owns its query subscription', () => {
    expect(elementBindsPageSources('problem-detail')).toBe(true);
    expect(elementLoadsSourcesAsync('problem-detail')).toBe(false);
    expect(elementHandlesEmptyRows('problem-detail')).toBe(true);
  });

  it.each(['factory-header', 'factory-floor'])(
    'keeps the initial %s load independent and subscribes its retained DOM',
    (element) => {
      expect(elementBindsPageSources(element)).toBe(true);
      expect(elementLoadsSourcesAsync(element)).toBe(true);
      expect(elementPageSubscriptionSourceNames(element, ['setup', 'metric-a', 'metric-b'])).toEqual(['setup']);
    }
  );
});
