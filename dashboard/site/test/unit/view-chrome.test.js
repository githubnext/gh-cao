// @vitest-environment jsdom
import { describe, expect, it } from 'vitest';
import { customViewAvailabilityMessage, renderContextChrome, renderContextList, renderCustomViewStateDetails, renderDefinitionList, renderDefinitionListRows, renderLayoutSectionChrome, renderMetadataSection, renderPageSection, renderProvenanceList, renderProvenanceSection, renderSummaryList, renderSummaryRegion, renderTitledBodySection, renderTitledRegion, renderViewChrome, renderViewDisclosure, renderViewSectionChrome } from '../../src/components/view-chrome.js';

describe('view chrome component helpers', () => {
  it('DLS-SAFE-007 renders focusable labeled page sections with deterministic heading ids', () => {
    const section = renderPageSection('runs', 'Run Status Counts', []);

    expect(section.className).toBe('page-section');
    expect(section.getAttribute('tabindex')).toBe('0');
    expect(section.getAttribute('aria-labelledby')).toBe('runs-run-status-counts-heading');
    expect(section.querySelector('h3')?.id).toBe('runs-run-status-counts-heading');
    expect(section.querySelector('h3')?.textContent).toBe('Run Status Counts');
    expect(section.hasAttribute('aria-description')).toBe(false);
  });

  it('keeps section descriptions accessible without inserting a help button', () => {
    const description = 'Observed run conclusions.';
    const section = renderPageSection('runs', 'Run Status Counts', [], 'h2', description);

    expect(section.className).toBe('page-section');
    expect(section.getAttribute('aria-description')).toBe(description);
    expect(section.querySelector('h2')?.textContent).toBe('Run Status Counts');
    expect(section.querySelector('button, [role="tooltip"]')).toBeNull();
    expect(section.children).toHaveLength(1);
  });

  it('DLS-PAGE-014 renders provenance list items and the conservative empty fallback', () => {
    const populated = renderProvenanceList([
      {
        sourceName: 'runs',
        sourceId: 'runs-fixture',
        sourceKind: 'fixture',
        asOf: '2026-08-29T20:00:00Z'
      }
    ]);
    const empty = renderProvenanceList([]);

    expect(populated.textContent).toContain('runs: runs-fixture (fixture) — as of 2026-08-29T20:00:00Z');
    expect(empty.textContent).toContain('No source provenance available for this page.');
  });

  it('renders reusable view chrome paragraphs for populated and empty metadata lines', () => {
    const rendered = renderViewChrome([
      'As of 2026-08-29T20:00:00Z • completeness complete • freshness fresh',
      'Additional detail'
    ]);
    const empty = renderViewChrome([]);

    expect(rendered).toHaveLength(2);
    expect(rendered[0]?.className).toBe('view-metadata');
    expect(rendered[0]?.textContent).toBe('As of 2026-08-29T20:00:00Z • completeness complete • freshness fresh');
    expect(rendered[1]?.className).toBe('view-metadata');
    expect(rendered[1]?.textContent).toBe('Additional detail');
    expect(empty).toHaveLength(0);
  });

  it('DLS-VIEW-013 renders reusable custom-view context lists including empty input', () => {
    const populated = renderContextList(['Source: usage', 'Scope: {"organization":"github"}']);
    const empty = renderContextList([]);

    expect(populated.className).toBe('view-context');
    expect(populated.querySelectorAll('li')).toHaveLength(2);
    expect(populated.textContent).toContain('Source: usage');
    expect(populated.textContent).toContain('Scope: {"organization":"github"}');
    expect(empty.className).toBe('view-context');
    expect(empty.querySelectorAll('li')).toHaveLength(0);
    expect(empty.textContent).toBe('');
  });

  it('DLS-VIEW-013 renders reusable context chrome around the shared context list', () => {
    const populated = renderContextChrome(['Source: usage', 'Filters: {"status":"open"}']);
    const empty = renderContextChrome([]);

    expect(populated).toHaveLength(1);
    expect(populated[0]?.className).toBe('view-context');
    expect(populated[0]?.querySelectorAll('li')).toHaveLength(2);
    expect(populated[0]?.textContent).toContain('Source: usage');
    expect(populated[0]?.textContent).toContain('Filters: {"status":"open"}');
    expect(empty).toHaveLength(0);
  });

  it('DLS-VIEW-013 renders reusable view section context without repeated data status', () => {
    const chrome = renderViewSectionChrome(
      {
        'as-of': '2026-08-29T20:00:00Z',
        completeness: 'complete',
        freshness: 'fresh'
      },
      ['Source: usage', 'Scope: {"organization":"github"}']
    );

    expect(chrome).toHaveLength(1);
    expect(chrome[0]?.className).toBe('view-context');
    expect(chrome[0]?.textContent).toContain('Scope: {"organization":"github"}');
    expect(chrome[0]?.textContent).not.toContain('As of');
  });

  it.each([
    ['partial', 'fresh'],
    ['complete', 'stale'],
    ['unknown', 'unknown']
  ])('keeps %s / %s evidence visible rather than implying complete fresh coverage', (completeness, freshness) => {
    const chrome = renderViewSectionChrome({ 'as-of': '2026-08-29T20:00:00Z', completeness, freshness }, []);
    expect(chrome[0]?.getAttribute('role')).toBe('status');
    expect(chrome[0]?.textContent).toBe(`Evidence completeness: ${completeness}; freshness: ${freshness}.`);
  });

  it('DLS-VIEW-013 renders reusable custom-view availability messages and affected-source details', () => {
    expect(customViewAvailabilityMessage('available')).toBe('Data available.');
    expect(customViewAvailabilityMessage('empty')).toBe('No observations matched the effective context.');
    expect(customViewAvailabilityMessage('unavailable')).toBe('This view cannot be shown because its data source is unavailable.');

    const withSource = renderCustomViewStateDetails('usage', ['Filters: {"status":"open"}']);
    const withoutSource = renderCustomViewStateDetails(null, []);

    expect(withSource).toHaveLength(2);
    expect(withSource[0]?.className).toBe('view-source');
    expect(withSource[0]?.textContent).toBe('Affected source: usage');
    expect(withSource[1]?.className).toBe('view-context');
    expect(withSource[1]?.textContent).toContain('Filters: {"status":"open"}');
    expect(withoutSource).toHaveLength(0);
  });

  it('identifies unavailable query dependencies from structured errors without displaying arbitrary text', () => {
    const details = renderCustomViewStateDetails('mcp-top-tools', [], {
      code: 'input-unavailable', source: 'mcp-tool-totals'
    }, '$.dashboard.queries[mcp-top-tools]: input source "mcp-tool-totals" is unavailable.');
    expect(details.map((item) => item.textContent)).toEqual([
      'Affected source: mcp-top-tools',
      'Query diagnostic: $.dashboard.queries[mcp-top-tools]: input source "mcp-tool-totals" is unavailable.',
      'Unavailable query dependency: mcp-tool-totals'
    ]);
    expect(renderCustomViewStateDetails('mcp-top-tools', [], {
      code: 'input-unavailable', source: 'mcp-top-tools'
    })).toHaveLength(1);
    expect(renderCustomViewStateDetails('mcp-top-tools', [], {
      code: 'other-error', source: 'mcp-tool-totals'
    })).toHaveLength(1);
    expect(renderCustomViewStateDetails('mcp-top-tools', [], {
      code: 'input-unavailable', source: '<secret>'
    })).toHaveLength(1);
    expect(renderCustomViewStateDetails('mcp-top-tools', [], 'input source "mcp-tool-totals" is unavailable')).toHaveLength(1);
    expect(renderCustomViewStateDetails('mcp-top-tools', [], null, 'arbitrary error text')).toHaveLength(1);
    expect(renderCustomViewStateDetails('mcp-top-tools', [], null, '$.dashboard.queries[x]: malformed\nerror.')).toHaveLength(1);
    expect(renderCustomViewStateDetails('mcp-top-tools', [], null, `$.dashboard.queries[x]: ${'x'.repeat(1024)}`)).toHaveLength(1);
  });

  it('wraps a rendered supplemental view in the shared view-disclosure summary toggle', () => {
    const rendered = document.createElement('div');
    rendered.textContent = 'body content';

    const details = renderViewDisclosure(rendered, 'half', 'supplemental', 'View title');

    expect(details.tagName).toBe('DETAILS');
    expect(details.className).toBe('custom-view view-disclosure');
    expect(details.getAttribute('data-view-layout')).toBe('half');
    expect(details.getAttribute('data-disclosure')).toBe('supplemental');
    const summary = details.querySelector('summary');
    expect(summary?.className).toBe('view-disclosure-summary');
    expect(summary?.textContent).toBe('View titleShow details');
    expect(details.lastElementChild).toBe(rendered);
  });

  it('renders reusable definition-list rows for summary-style key/value grids including empty input', () => {
    const populated = renderDefinitionListRows([
      { label: 'Approval gates', value: '2' },
      { label: 'Explicit warnings', value: '1' }
    ]);
    const empty = renderDefinitionListRows([]);

    expect(populated).toHaveLength(2);
    expect(populated[0]?.tagName).toBe('DIV');
    expect(populated[0]?.textContent).toContain('Approval gates2');
    expect(populated[1]?.textContent).toContain('Explicit warnings1');
    expect(empty).toHaveLength(0);
  });

  it('renders reusable definition lists around the shared definition-list rows', () => {
    const populated = renderDefinitionList('summary-grid', [
      { label: 'Approval gates', value: '2' },
      { label: 'Explicit warnings', value: '1' }
    ]);
    const empty = renderDefinitionList('domain-summary', []);

    expect(populated.className).toBe('summary-grid');
    expect(populated.querySelectorAll('div')).toHaveLength(2);
    expect(populated.textContent).toContain('Approval gates2');
    expect(populated.textContent).toContain('Explicit warnings1');
    expect(empty.className).toBe('domain-summary');
    expect(empty.querySelectorAll('div')).toHaveLength(0);
    expect(empty.textContent).toBe('');
  });

  it('DLS-SAFE-007 wraps single-content titled regions with the shared page-section markup', () => {
    const region = renderTitledRegion('usage', 'Usage Totals', renderProvenanceList([]));

    expect(region.className).toBe('page-section');
    expect(region.getAttribute('aria-labelledby')).toBe('usage-usage-totals-heading');
    expect(region.querySelector('h3')?.textContent).toBe('Usage Totals');
    expect(region.querySelector('.provenance-list')?.textContent).toContain('No source provenance available for this page.');
  });

  it('DLS-VIEW-013 renders reusable summary lists including empty counts', () => {
    const populated = renderSummaryList('overview-rollout-mode-counts', new Map([
      ['shadow', 2],
      ['full', 1]
    ]));
    const empty = renderSummaryList('run-outcome-counts', new Map());

    expect(populated.className).toBe('overview-rollout-mode-counts');
    expect(populated.querySelectorAll('li')).toHaveLength(2);
    expect(populated.textContent).toContain('shadow: 2');
    expect(populated.textContent).toContain('full: 1');
    expect(empty.className).toBe('run-outcome-counts');
    expect(empty.textContent).toContain('No data available.');
  });

  it('DLS-VIEW-013 renders reusable summary regions including empty counts', () => {
    const populated = renderSummaryRegion('overview', 'Rollout Mode Filtering', 'overview-rollout-mode-counts', new Map([
      ['shadow', 2],
      ['full', 1]
    ]));
    const empty = renderSummaryRegion('runs', 'Outcome Counts', 'run-outcome-counts', new Map());

    expect(populated.className).toBe('page-section');
    expect(populated.getAttribute('aria-labelledby')).toBe('overview-rollout-mode-filtering-heading');
    expect(populated.querySelector('h3')?.textContent).toBe('Rollout Mode Filtering');
    expect(populated.querySelector('ul')?.className).toBe('overview-rollout-mode-counts');
    expect(populated.textContent).toContain('shadow: 2');
    expect(populated.textContent).toContain('full: 1');
    expect(empty.querySelector('ul')?.className).toBe('run-outcome-counts');
    expect(empty.textContent).toContain('No data available.');
  });

  it('DLS-PAGE-014 renders the provenance heading plus list as a reusable section', () => {
    const section = renderProvenanceSection('evals', [
      {
        sourceName: 'evals',
        sourceId: 'evals-fixture',
        sourceKind: 'fixture',
        asOf: '2026-08-29T20:00:00Z'
      }
    ]);

    expect(section.querySelector('h3')?.textContent).toBe('Provenance');
    expect(section.getAttribute('aria-labelledby')).toBe('evals-provenance-heading');
    expect(section.querySelector('.provenance-list')?.textContent).toContain('evals: evals-fixture (fixture) — as of 2026-08-29T20:00:00Z');
  });

  it('renders metadata sections with configurable heading levels', () => {
    const defaultHeading = renderMetadataSection('Status', document.createElement('p'));
    defaultHeading.querySelector('p')?.append('Closed');
    const customHeading = renderMetadataSection('Workflow', document.createElement('p'), 'h3');
    customHeading.querySelector('p')?.append('Daily review');

    expect(defaultHeading.querySelector('h2')?.textContent).toBe('Status');
    expect(defaultHeading.textContent).toContain('Closed');
    expect(customHeading.querySelector('h3')?.textContent).toBe('Workflow');
    expect(customHeading.textContent).toContain('Daily review');
  });

  it('renders titled body sections with optional section and body chrome', () => {
    const populated = renderTitledBodySection(
      'workflow-observations-heading',
      'Workflow observations',
      [document.createElement('p'), document.createElement('table')],
      {
        sectionClassName: 'value-details-section',
        headingTag: 'h4',
        bodyClassName: 'value-details-body',
        bodyAttributes: { role: 'group', 'aria-label': 'Observation details' }
      }
    );
    populated.querySelector('p')?.append('Missing, failed, and null grader results are excluded rather than scored as zero.');
    const plain = renderTitledBodySection('', 'Workflow observations', [document.createElement('p')]);
    plain.querySelector('p')?.append('Body');

    expect(populated.className).toBe('value-details-section');
    expect(populated.querySelector('h4')?.id).toBe('workflow-observations-heading');
    expect(populated.querySelector('.value-details-body')?.getAttribute('role')).toBe('group');
    expect(populated.querySelector('.value-details-body')?.getAttribute('aria-label')).toBe('Observation details');
    expect(populated.querySelector('.value-details-body')?.querySelectorAll('p, table')).toHaveLength(2);
    expect(plain.querySelector('h3')?.textContent).toBe('Workflow observations');
    expect(plain.querySelector('h3')?.hasAttribute('id')).toBe(false);
    expect(plain.textContent).toContain('Body');
  });

  it('renders layout section headers through the shared section-heading helper', () => {
    const header = renderLayoutSectionChrome('campaigns', {
      id: 'run-trend',
      title: 'Campaign run trend',
      description: 'Thirty-day retained campaign run totals.',
      layout: 'full',
      views: ['campaigns-run-trend'],
      'count-label': 'records'
    }, 12);

    expect(header.className).toBe('layout-section-header');
    expect(header.querySelector('.section-heading .scope-kicker')?.textContent).toBe('Run Trend');
    expect(header.querySelector('h2')?.id).toBe('campaigns-run-trend-layout-heading');
    expect(header.querySelector('h2')?.textContent).toBe('Campaign run trend');
    expect(header.querySelector('.section-heading p')?.textContent).toBe('Thirty-day retained campaign run totals.');
    expect(header.querySelector('.layout-section-header > strong')?.textContent).toBe('12 records');
  });

  it('renders a horizontal metric total as the primary section heading', () => {
    const header = renderLayoutSectionChrome('overview', {
      id: 'attention',
      layout: 'horizontal',
      views: ['failed-runs', 'blocked-work'],
      'count-sources': ['failed-run-count', 'blocked-work-count'],
      'count-field': 'count',
      'count-label': 'items need your attention'
    }, 80);

    expect(header.className).toBe('layout-section-header layout-section-header-summary');
    expect(header.querySelector('.scope-kicker')).toBeNull();
    expect(header.querySelector('h2')?.textContent).toBe('80 items need your attention');
  });
});
