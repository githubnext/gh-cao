// @vitest-environment jsdom
import { describe, expect, it, vi } from 'vitest';
import { coverageWindowHours, copyTextToClipboard, createCopyControl, formatMediumUtcDate, formatMediumUtcDateTime, formatShortDate, formatUtcDateTime, isPlainObject, isSafeHttpsUrl, renderActionLabel, renderCheckbox, renderCloseButton, renderDashboardViewSkeleton, renderDigest, renderDisclosureSummaryLabel, renderDlRow, renderEmptyTableRow, renderFilterSelect, renderFilterSelectControl, renderIconSpan, renderIdentityLink, renderLabeledControl, renderLabeledSpan, renderLegendList, renderLegendSwatch, renderListOrEmptyMessage, renderListWithFallback, renderLoadingMessage, renderLoadingPlaceholderBlocks, renderSearchInput, renderSectionHeading, renderSkeletonBars, renderTableHeadRow, renderTableSummaryEmpty, renderTooltip, renderVitalStat } from '../../src/components/ui-primitives.js';
import { h } from '../../src/dom.js';
import { effect, state } from '../../src/reactive.js';
import { createModalDialog } from '../../src/components/ui-primitives.js';

describe('ui primitives', () => {
  it('switches mobile tooltips to native popup buttons and restores desktop semantics', () => {
    let mobile = true;
    const media = new EventTarget();
    Object.defineProperty(media, 'matches', { get: () => mobile });
    vi.stubGlobal('matchMedia', () => media);
    const scope = new AbortController();
    const tooltip = renderTooltip({
      id: 'mobile-status', label: 'Data status', description: 'Progress',
      mobilePopup: true, signal: scope.signal
    });
    document.body.append(tooltip);
    const button = /** @type {HTMLElement} */ (tooltip.firstElementChild);
    const content = /** @type {HTMLElement} */ (tooltip.lastElementChild);
    const hide = vi.fn();
    Object.defineProperty(content, 'hidePopover', { value: hide });
    try {
      expect(button.getAttribute('popovertarget')).toBe('mobile-status');
      expect(button.getAttribute('aria-haspopup')).toBe('dialog');
      expect(button.getAttribute('aria-expanded')).toBe('false');
      expect(button.hasAttribute('aria-describedby')).toBe(false);
      expect(content.getAttribute('role')).toBe('dialog');
      content.dispatchEvent(Object.assign(new Event('toggle'), { newState: 'open' }));
      expect(button.getAttribute('aria-expanded')).toBe('true');
      mobile = false;
      media.dispatchEvent(new Event('change'));
      expect(hide).toHaveBeenCalledOnce();
      expect(button.hasAttribute('aria-haspopup')).toBe(false);
      expect(button.getAttribute('aria-describedby')).toBe('mobile-status');
      expect(content.getAttribute('role')).toBe('tooltip');
      expect(content.hasAttribute('popover')).toBe(false);
      scope.abort();
      mobile = true;
      media.dispatchEvent(new Event('change'));
      expect(content.getAttribute('role')).toBe('tooltip');
    } finally {
      scope.abort();
      tooltip.remove();
      vi.unstubAllGlobals();
    }
  });

  it('contains dialog clicks without preventing controls or their default actions', () => {
    const parentClick = vi.fn();
    const onChange = vi.fn();
    const onConfirm = vi.fn();
    const { dialog, open, close } = createModalDialog({ className: 'test-dialog', ariaLabel: 'Upgrade' });
    const checkbox = h('input', { type: 'checkbox', onChange });
    const labelText = h('strong', null, 'Create pull request');
    const label = h('label', null, checkbox, labelText);
    const command = h('code', null, 'gh aw upgrade');
    const confirm = h('button', { type: 'button', onClick: onConfirm }, 'Copy command');
    dialog.append(label, command, confirm);
    const parent = h('div', { onClick: parentClick }, dialog);
    document.body.append(parent);

    try {
      open();
      labelText.click();
      expect(checkbox).toHaveProperty('checked', true);
      expect(onChange).toHaveBeenCalledOnce();
      confirm.click();
      expect(onConfirm).toHaveBeenCalledOnce();
      for (const target of [label, command, dialog]) {
        const event = new MouseEvent('click', { bubbles: true, cancelable: true });
        target.dispatchEvent(event);
        expect(event.defaultPrevented).toBe(false);
      }
      expect(parentClick).not.toHaveBeenCalled();
      close();
      expect(dialog.open).toBe(false);
    } finally {
      parent.remove();
    }
  });

  it('positions viewport-anchored tooltips and stops tracking when their owner aborts', () => {
    const scope = new AbortController();
    const tooltip = renderTooltip({
      id: 'viewport-tooltip',
      label: 'Run observation',
      description: 'Observed value',
      viewportAnchored: true,
      signal: scope.signal
    });
    const content = /** @type {HTMLElement} */ (tooltip.lastElementChild);
    expect(content.classList.contains('tooltip-content-viewport')).toBe(true);
    const removeListener = vi.spyOn(window, 'removeEventListener');
    tooltip.dispatchEvent(new Event('pointerenter'));
    scope.abort();
    expect(removeListener).toHaveBeenCalledWith('resize', expect.any(Function));
    expect(removeListener).toHaveBeenCalledWith('scroll', expect.any(Function), true);
    removeListener.mockRestore();
  });

  it('renders a checkbox that can stop click propagation without blocking changes', () => {
    const onChange = vi.fn();
    const parentClick = vi.fn();
    const checkbox = renderCheckbox({
      id: 'background-updates',
      className: 'setting-toggle',
      checked: false,
      ariaLabel: 'Background updates',
      onChange,
      stopClickPropagation: true
    });
    const parent = h('div', { onClick: parentClick }, checkbox);
    document.body.append(parent);

    checkbox.click();

    expect(checkbox.id).toBe('background-updates');
    expect(checkbox.className).toBe('setting-toggle');
    expect(checkbox.getAttribute('aria-label')).toBe('Background updates');
    expect(checkbox.checked).toBe(true);
    expect(onChange).toHaveBeenCalledOnce();
    expect(parentClick).not.toHaveBeenCalled();
  });

  it('renders a loading placeholder announced as an in-progress status region', () => {
    const rendered = renderLoadingMessage('Loading campaign data...');

    expect(rendered.tagName).toBe('P');
    expect(rendered.className).toBe('empty');
    expect(rendered.textContent).toBe('Loading campaign data...');
    expect(rendered.getAttribute('role')).toBe('status');
    expect(rendered.getAttribute('aria-busy')).toBe('true');
  });

  it('renders shared section-heading markup with configurable heading levels', () => {
    const rendered = renderSectionHeading({
      kicker: 'Current decision window',
      id: 'overview-heading',
      title: 'Overview',
      description: 'Daily status',
      summary: '3 signals',
      headingTag: 'h2'
    });

    expect(rendered.className).toBe('section-heading');
    expect(rendered.querySelector('.scope-kicker')?.textContent).toBe('Current decision window');
    expect(rendered.querySelector('h2')?.id).toBe('overview-heading');
    expect(rendered.querySelector('h2')?.textContent).toBe('Overview');
    expect(rendered.querySelector('p')?.textContent).toBe('Daily status');
    expect(rendered.querySelector('strong')?.textContent).toBe('3 signals');
  });

  it('omits the summary node when no summary is provided', () => {
    const rendered = renderSectionHeading({
      kicker: 'Workflow topology',
      id: 'topology-heading',
      title: 'Orchestrator and workers'
    });

    expect(rendered.querySelector('h3')?.textContent).toBe('Orchestrator and workers');
    expect(rendered.querySelector('strong')).toBeNull();
  });

  it('renders shared vital stats with and without detail text', () => {
    const withDetail = renderVitalStat('Root episodes', 4, 'observed orchestrator runs');
    const withoutDetail = renderVitalStat('Measured AIC', '—');

    expect(withDetail.textContent).toBe('Root episodes4observed orchestrator runs');
    expect(withDetail.querySelector('dt')?.textContent).toBe('Root episodes');
    expect(withDetail.querySelector('dd')?.textContent).toBe('4');
    expect(withDetail.querySelector('p')?.textContent).toBe('observed orchestrator runs');
    expect(withoutDetail.textContent).toBe('Measured AIC—');
    expect(withoutDetail.querySelector('p')).toBeNull();
  });

  it('renders the shared dt/dd row primitive used across metadata and stat lists', () => {
    const row = renderDlRow('Freshness', 'Fresh', 'Updated moments ago');
    expect(row.querySelector('dt')?.textContent).toBe('Freshness');
    expect(row.querySelector('dd')?.textContent).toBe('Fresh');
    expect(row.querySelector('p')?.textContent).toBe('Updated moments ago');

    const compositeTerm = renderDlRow([document.createTextNode('!'), 'Label'], 'Value');
    expect(compositeTerm.querySelector('dt')?.textContent).toBe('!Label');
    expect(compositeTerm.querySelector('p')).toBeNull();
  });

  it('renders the shared inline labeled-span pair used by the horizon tooltip and readiness snapshot meta', () => {
    const span = renderLabeledSpan('Snapshot', ' 2 minutes ago');
    expect(span.tagName).toBe('SPAN');
    expect(span.querySelector('strong')?.textContent).toBe('Snapshot');
    expect(span.textContent).toBe('Snapshot 2 minutes ago');

    const withNode = renderLabeledSpan('Start', document.createElement('time'));
    expect(withNode.querySelector('time')).not.toBeNull();
  });

  it('renders a shared table head row of scope="col" cells from plain labels', () => {
    const headRow = renderTableHeadRow(['Campaign', 'Runs', 'Failed']);
    const headCells = headRow.querySelectorAll('th');

    expect(headRow.tagName).toBe('TR');
    expect(headCells).toHaveLength(3);
    expect(Array.from(headCells).map((cell) => cell.textContent)).toEqual(['Campaign', 'Runs', 'Failed']);
    expect(Array.from(headCells).every((cell) => cell.getAttribute('scope') === 'col')).toBe(true);
  });

  it('renders accessible tooltip semantics around arbitrary rich content', () => {
    const tooltip = renderTooltip({
      id: 'example-tooltip',
      label: 'Example details',
      description: 'Additional context.',
      icon: document.createTextNode('?'),
      content: document.createElement('strong')
    });

    expect(tooltip.querySelector('.tooltip-trigger')?.getAttribute('aria-label')).toBe('Example details');
    expect(tooltip.querySelector('.tooltip-trigger')?.getAttribute('aria-describedby')).toBe('example-tooltip');
    expect(tooltip.querySelector('.tooltip-content')?.getAttribute('role')).toBe('tooltip');
    expect(tooltip.querySelector('.tooltip-description')?.textContent).toBe('Additional context.');
    expect(tooltip.querySelector('.tooltip-content strong')).not.toBeNull();
  });

  it('keeps tooltips inside the viewport and flips above when there is no room below', () => {
    const tooltip = renderTooltip({
      id: 'placed-tooltip',
      label: 'Tooltip placement',
      icon: document.createTextNode('?'),
      content: document.createTextNode('Additional context.')
    });
    const trigger = /** @type {HTMLElement} */ (tooltip.querySelector('.tooltip-trigger'));
    const tooltipContent = /** @type {HTMLElement} */ (tooltip.querySelector('.tooltip-content'));
    vi.spyOn(tooltip, 'getBoundingClientRect').mockReturnValue({
      x: 20, y: 240, left: 20, top: 240, right: 44, bottom: 264, width: 24, height: 24, toJSON() {}
    });
    vi.spyOn(trigger, 'getBoundingClientRect').mockReturnValue({
      x: 20, y: 240, left: 20, top: 240, right: 44, bottom: 264, width: 24, height: 24, toJSON() {}
    });
    vi.spyOn(tooltipContent, 'getBoundingClientRect').mockReturnValue({
      x: 0, y: 0, left: 0, top: 0, right: 320, bottom: 100, width: 320, height: 100, toJSON() {}
    });
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: 400 });
    Object.defineProperty(window, 'innerHeight', { configurable: true, value: 300 });

    tooltip.dispatchEvent(new Event('pointerenter'));

    expect(tooltipContent.style.left).toBe('-6px');
    expect(tooltipContent.style.right).toBe('auto');
    expect(tooltipContent.style.top).toBe('-108px');

    tooltip.dispatchEvent(new Event('pointerleave'));
  });

  it('repositions a focused tooltip after the viewport changes', () => {
    const tooltip = renderTooltip({
      id: 'focused-tooltip',
      label: 'Focused tooltip',
      icon: document.createTextNode('?'),
      content: document.createTextNode('Additional context.')
    });
    const trigger = /** @type {HTMLElement} */ (tooltip.querySelector('.tooltip-trigger'));
    const tooltipContent = /** @type {HTMLElement} */ (tooltip.querySelector('.tooltip-content'));
    vi.spyOn(tooltip, 'getBoundingClientRect').mockReturnValue({
      x: 390, y: 20, left: 390, top: 20, right: 414, bottom: 44, width: 24, height: 24, toJSON() {}
    });
    vi.spyOn(trigger, 'getBoundingClientRect').mockReturnValue({
      x: 390, y: 20, left: 390, top: 20, right: 414, bottom: 44, width: 24, height: 24, toJSON() {}
    });
    vi.spyOn(tooltipContent, 'getBoundingClientRect').mockReturnValue({
      x: 0, y: 0, left: 0, top: 0, right: 320, bottom: 100, width: 320, height: 100, toJSON() {}
    });
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: 400 });
    Object.defineProperty(window, 'innerHeight', { configurable: true, value: 300 });

    tooltip.dispatchEvent(new FocusEvent('focusin', { bubbles: true }));

    expect(tooltipContent.style.left).toBe('-324px');
    expect(tooltipContent.style.top).toBe('32px');

    Object.defineProperty(window, 'innerWidth', { configurable: true, value: 500 });
    window.dispatchEvent(new Event('resize'));

    expect(tooltipContent.style.left).toBe('-296px');

    tooltip.dispatchEvent(new FocusEvent('focusout', { bubbles: true }));
  });

  it('composes caller-owned trigger and content nodes with reactive updates', () => {
    const value = state('Loading');
    const trigger = h('button', { type: 'button', className: 'custom-trigger' });
    const content = h('strong');
    const tooltip = renderTooltip({
      id: 'reactive-tooltip',
      label: 'Reactive details',
      trigger,
      content,
      className: 'custom-tooltip',
      contentClassName: 'custom-content'
    });
    const binding = effect(() => {
      content.textContent = value.get();
    });

    expect(tooltip.classList.contains('custom-tooltip')).toBe(true);
    expect(tooltip.querySelector('.custom-trigger')).toBe(trigger);
    expect(trigger.getAttribute('aria-describedby')).toBe('reactive-tooltip');
    expect(tooltip.querySelector('.custom-content')).toBe(content.parentElement);
    expect(content.textContent).toBe('Loading');

    value.set('Ready');

    expect(content.textContent).toBe('Ready');
    expect(tooltip.querySelector('.custom-trigger')).toBe(trigger);
    expect(tooltip.querySelector('.custom-content strong')).toBe(content);
    binding.stop();
  });

  it('stops tracking the viewport once a focused tooltip detaches from the document', async () => {
    const addSpy = vi.spyOn(window, 'addEventListener');
    const tooltip = renderTooltip({
      id: 'detached-tooltip',
      label: 'Detached tooltip',
      icon: document.createTextNode('?'),
      content: document.createTextNode('Additional context.')
    });
    document.body.append(tooltip);
    // Let the MutationObserver microtask backing createFactoryScope observe the attach.
    await new Promise((resolve) => queueMicrotask(() => resolve(undefined)));

    tooltip.dispatchEvent(new FocusEvent('focusin', { bubbles: true }));

    const resizeCall = addSpy.mock.calls.find(([type, , options]) => type === 'resize' && options && typeof options === 'object' && 'signal' in options);
    const scrollCall = addSpy.mock.calls.find(([type, , options]) => type === 'scroll' && options && typeof options === 'object' && 'signal' in options);
    expect(resizeCall).toBeDefined();
    expect(scrollCall).toBeDefined();
    const resizeOptions = /** @type {AddEventListenerOptions} */ (resizeCall?.[2]);
    const scope = /** @type {AbortSignal} */ (resizeOptions.signal);
    expect(scope.aborted).toBe(false);

    tooltip.remove();
    // Let the MutationObserver microtask backing createFactoryScope run.
    await new Promise((resolve) => queueMicrotask(() => resolve(undefined)));

    // The scope's AbortSignal removes window listeners natively once the
    // tooltip root leaves the document, instead of leaking on every
    // navigation that rebuilds renderPageSection/view-chrome tooltips.
    expect(scope.aborted).toBe(true);

    addSpy.mockRestore();
  });

  it('formats UTC date-time text and preserves the unavailable fallback', () => {
    expect(formatUtcDateTime('2026-08-30T10:00:00Z')).toBe('Aug 30, 2026, 10:00 AM');
    expect(formatUtcDateTime('not-a-date')).toBe('Time unavailable');
  });

  it('formats a Date or millisecond timestamp as medium-date, short-time UTC text', () => {
    expect(formatMediumUtcDateTime(new Date('2026-08-30T10:00:00Z'))).toBe('Aug 30, 2026, 10:00 AM');
    expect(formatMediumUtcDateTime(Date.parse('2026-08-30T10:00:00Z'))).toBe('Aug 30, 2026, 10:00 AM');
  });

  it('formats a Date or millisecond timestamp as medium-date-only UTC text', () => {
    expect(formatMediumUtcDate(new Date('2026-08-30T10:00:00Z'))).toBe('Aug 30, 2026');
    expect(formatMediumUtcDate(Date.parse('2026-08-30T10:00:00Z'))).toBe('Aug 30, 2026');
  });

  it('formats a Date, timestamp, or date string as a short local date', () => {
    const localDate = new Date(2026, 7, 30, 10);
    expect(formatShortDate(localDate, 'en-US')).toBe('Aug 30, 2026');
    expect(formatShortDate(localDate.getTime(), 'en-US')).toBe('Aug 30, 2026');
    expect(formatShortDate('2026-08-30T10:00:00', 'en-US')).toBe('Aug 30, 2026');
    expect(formatShortDate(localDate)).toBe(localDate.toLocaleDateString(undefined, { month: 'short', day: 'numeric', year: 'numeric' }));
  });

  it('computes whole-hour coverage windows and rejects invalid or non-increasing bounds', () => {
    expect(coverageWindowHours({ 'coverage-start': '2026-08-30T00:00:00Z', 'coverage-end': '2026-08-30T05:00:00Z' })).toBe(5);
    expect(coverageWindowHours({ 'coverage-start': '2026-08-30T05:00:00Z', 'coverage-end': '2026-08-30T00:00:00Z' })).toBeNull();
    expect(coverageWindowHours({ 'coverage-start': 'not-a-date', 'coverage-end': '2026-08-30T05:00:00Z' })).toBeNull();
    expect(coverageWindowHours(undefined)).toBeNull();
  });

  it('renders the shared table-summary empty-state placeholder with the given message', () => {
    const rendered = renderTableSummaryEmpty('No timestamps');
    expect(rendered.tagName).toBe('SPAN');
    expect(rendered.className).toBe('table-summary-empty');
    expect(rendered.textContent).toBe('No timestamps');
  });

  it('renders the shared three-bar skeleton placeholder with the given class', () => {
    const rendered = renderSkeletonBars('dashboard-lazy-view-skeleton');
    expect(rendered.tagName).toBe('DIV');
    expect(rendered.className).toBe('dashboard-lazy-view-skeleton');
    expect(rendered.getAttribute('aria-hidden')).toBe('true');
    expect(rendered.querySelectorAll('span')).toHaveLength(3);
  });

  it('renders a randomized "UI layout" page skeleton with staggered entrance blocks', () => {
    const rendered = renderDashboardViewSkeleton();
    expect(rendered.tagName).toBe('DIV');
    expect(rendered.className).toBe('dashboard-view-skeleton');
    expect(rendered.getAttribute('aria-hidden')).toBe('true');
    const blocks = [...rendered.querySelectorAll('span.dashboard-view-skeleton-block')]
      .filter((block) => block instanceof HTMLElement);
    expect(blocks.length).toBeGreaterThan(0);
    expect(blocks).toHaveLength(rendered.children.length);
    for (const [index, block] of blocks.entries()) {
      expect(block.style.getPropertyValue('--dashboard-view-skeleton-span')).not.toBe('');
      expect(block.style.getPropertyValue('--dashboard-view-skeleton-height')).toMatch(/^\d+px$/);
      expect(block.style.getPropertyValue('--dashboard-view-skeleton-delay')).toBe(`${index * 60}ms`);
    }
  });

  it('picks a different deterministic layout pattern and block height per Math.random draw', () => {
    const randomSpy = vi.spyOn(Math, 'random');

    randomSpy.mockReturnValueOnce(0).mockReturnValue(0);
    const first = renderDashboardViewSkeleton();
    const firstSpans = [...first.querySelectorAll('span.dashboard-view-skeleton-block')]
      .filter((block) => block instanceof HTMLElement)
      .map((block) => block.style.getPropertyValue('--dashboard-view-skeleton-span'));
    expect(firstSpans).toEqual(['12', '6', '6', '4', '4', '4']);
    expect(/** @type {HTMLElement} */ (first.firstElementChild).style.getPropertyValue('--dashboard-view-skeleton-height')).toBe('48px');

    randomSpy.mockReset();
    randomSpy.mockReturnValueOnce(0.99).mockReturnValue(1);
    const second = renderDashboardViewSkeleton();
    const secondSpans = [...second.querySelectorAll('span.dashboard-view-skeleton-block')]
      .filter((block) => block instanceof HTMLElement)
      .map((block) => block.style.getPropertyValue('--dashboard-view-skeleton-span'));
    expect(secondSpans).toEqual(['7', '5', '4', '4', '4', '12']);
    expect(/** @type {HTMLElement} */ (second.firstElementChild).style.getPropertyValue('--dashboard-view-skeleton-height')).toBe('112px');

    randomSpy.mockRestore();
  });

  it('renders the shared loading placeholder blocks used by the dashboard shell and page skeletons', () => {
    const blocks = renderLoadingPlaceholderBlocks();
    expect(blocks).toHaveLength(4);
    expect(blocks.map((block) => block.className)).toEqual([
      'skeleton-card',
      'skeleton-card',
      'skeleton-card',
      'skeleton-panel'
    ]);
    for (const block of blocks) {
      expect(block.tagName).toBe('DIV');
      expect(block.getAttribute('aria-hidden')).toBe('true');
    }
  });

  it('renders a truncated digest in a code element, or null when empty', () => {
    const rendered = renderDigest('1234567890abcdef');
    expect(rendered?.tagName).toBe('CODE');
    expect(rendered?.textContent).toBe('1234567890ab');
    expect(renderDigest('')).toBeNull();
    expect(renderDigest(null)).toBeNull();
    expect(renderDigest(undefined)).toBeNull();
  });

  it('renders the shared list-with-fallback pattern for populated and empty item sets', () => {
    const populated = renderListWithFallback('my-list', [1, 2], (value) => `item ${value}`, 'No items.');
    expect(populated.tagName).toBe('UL');
    expect(populated.className).toBe('my-list');
    expect([...populated.querySelectorAll('li')].map((li) => li.textContent)).toEqual(['item 1', 'item 2']);

    const empty = renderListWithFallback('my-list', [], (value) => `item ${value}`, 'No items.');
    expect(empty.querySelectorAll('li')).toHaveLength(1);
    expect(empty.textContent).toBe('No items.');
  });

  it('renders the shared list-or-empty-message pattern for populated and empty item sets', () => {
    const populated = renderListOrEmptyMessage('my-list', [1, 2], (value) => h('li', null, `item ${value}`), 'my-empty', 'No items.');
    expect(populated.tagName).toBe('UL');
    expect(populated.className).toBe('my-list');
    expect([...populated.querySelectorAll('li')].map((li) => li.textContent)).toEqual(['item 1', 'item 2']);

    const empty = renderListOrEmptyMessage('my-list', [], (value) => h('li', null, `item ${value}`), 'my-empty', 'No items.');
    expect(empty.tagName).toBe('P');
    expect(empty.className).toBe('my-empty');
    expect(empty.textContent).toBe('No items.');
  });

  it('renders the shared empty-table-row placeholder spanning the given column count', () => {
    const rendered = renderEmptyTableRow(8, 'No campaigns discovered.');

    expect(rendered.tagName).toBe('TR');
    const cell = rendered.querySelector('td');
    expect(cell?.getAttribute('colspan')).toBe('8');
    expect(rendered.textContent).toBe('No campaigns discovered.');
  });

  it('renders the shared decorative legend swatch with the requested class and aria-hidden', () => {
    const rendered = renderLegendSwatch('chart-series-a');

    expect(rendered.tagName).toBe('I');
    expect(rendered.className).toBe('chart-series-a');
    expect(rendered.getAttribute('aria-hidden')).toBe('true');
  });

  it('renders a legend list pairing a swatch with per-item content and any extra list attributes', () => {
    const rendered = renderLegendList(
      'chart-legend chart-legend-line',
      ['alpha', 'beta'],
      (item, index) => `chart-series-${index}`,
      (item) => [`label ${item}`],
      { 'data-chart-legend': 'visual' }
    );

    expect(rendered.tagName).toBe('UL');
    expect(rendered.className).toBe('chart-legend chart-legend-line');
    expect(rendered.getAttribute('data-chart-legend')).toBe('visual');
    const items = rendered.querySelectorAll('li');
    expect(items).toHaveLength(2);
    expect(items[0].querySelector('i')?.className).toBe('chart-series-0');
    expect(items[0].textContent).toBe('label alpha');
    expect(items[1].querySelector('i')?.className).toBe('chart-series-1');
    expect(items[1].textContent).toBe('label beta');
  });

  it('renders the shared icon span with the requested class and a single octicon', () => {
    const rendered = renderIconSpan('signal-icon', 'check-circle');

    expect(rendered.tagName).toBe('SPAN');
    expect(rendered.className).toBe('signal-icon');
    expect(rendered.hasAttribute('aria-hidden')).toBe(false);
    expect(rendered.querySelector('svg.octicon-check-circle path')).not.toBeNull();
  });

  it('renders the shared icon span with aria-hidden when requested', () => {
    const rendered = renderIconSpan('readiness-verdict-icon', 'x-circle', { ariaHidden: true });

    expect(rendered.getAttribute('aria-hidden')).toBe('true');
    expect(rendered.querySelector('svg.octicon-x-circle path')).not.toBeNull();
  });

  it('renders the shared icon element with a custom tag when requested', () => {
    const rendered = renderIconSpan('post-avatar', 'mark-github', { ariaHidden: true, tag: 'div' });

    expect(rendered.tagName).toBe('DIV');
    expect(rendered.className).toBe('post-avatar');
    expect(rendered.getAttribute('aria-hidden')).toBe('true');
    expect(rendered.querySelector('svg.octicon-mark-github path')).not.toBeNull();
  });

  it('renders a visually-hidden action label span with the given text', () => {
    const rendered = renderActionLabel('GitHub account');

    expect(rendered.tagName).toBe('SPAN');
    expect(rendered.className).toBe('sr-only action-label');
    expect(rendered.textContent).toBe('GitHub account');
  });

  it('renders the shared close/dismiss icon button with matching title and aria-label text', () => {
    const onClick = () => {};
    const rendered = renderCloseButton({
      className: 'site-callout-dismiss',
      label: 'Dismiss Notice',
      onClick
    });

    expect(rendered.tagName).toBe('BUTTON');
    expect(rendered.getAttribute('type')).toBe('button');
    expect(rendered.className).toBe('site-callout-dismiss');
    expect(rendered.getAttribute('title')).toBe('Dismiss Notice');
    expect(rendered.getAttribute('aria-label')).toBe('Dismiss Notice');
    expect(rendered.querySelector('svg.octicon-x path')).not.toBeNull();
  });

  it('renders the shared identity link with an icon, label element, and optional class name', () => {
    const withStrong = renderIdentityLink({
      href: '#page-campaign-insights?campaign=self-care',
      icon: 'goal',
      label: 'SelfCare',
      className: 'campaign-status-identity',
      labelTag: 'strong'
    });

    expect(withStrong.tagName).toBe('A');
    expect(withStrong.getAttribute('href')).toBe('#page-campaign-insights?campaign=self-care');
    expect(withStrong.className).toBe('campaign-status-identity');
    expect(withStrong.querySelector('svg.octicon-goal path')).not.toBeNull();
    expect(withStrong.querySelector('strong')?.textContent).toBe('SelfCare');

    const withDefaultLabelTag = renderIdentityLink({
      href: '#page-campaign-insights?campaign=dashboard',
      icon: 'graph',
      label: 'Dashboard'
    });

    expect(withDefaultLabelTag.className).toBe('');
    expect(withDefaultLabelTag.querySelector('span')?.textContent).toBe('Dashboard');
  });

  it('identifies plain objects while rejecting arrays and null', () => {
    expect(isPlainObject({})).toBe(true);
    expect(isPlainObject({ foo: 'bar' })).toBe(true);
    expect(isPlainObject([])).toBe(false);
    expect(isPlainObject(null)).toBe(false);
    expect(isPlainObject('string')).toBe(false);
    expect(isPlainObject(42)).toBe(false);
    expect(isPlainObject(undefined)).toBe(false);
  });

  it('accepts https URLs with no embedded credentials and rejects everything else', () => {
    expect(isSafeHttpsUrl('https://example.com/path')).toBe(true);
    expect(isSafeHttpsUrl('http://example.com')).toBe(false);
    expect(isSafeHttpsUrl('https://user:pass@example.com')).toBe(false);
    expect(isSafeHttpsUrl('javascript:alert(1)')).toBe(false);
    expect(isSafeHttpsUrl('not a url')).toBe(false);
    expect(isSafeHttpsUrl('')).toBe(false);
    expect(isSafeHttpsUrl(null)).toBe(false);
    expect(isSafeHttpsUrl(42)).toBe(false);
  });

  it('renders a labeled control wrapping the given control node', () => {
    const input = document.createElement('input');
    const rendered = renderLabeledControl('Filter rows', input);

    expect(rendered.tagName).toBe('LABEL');
    expect(rendered.className).toBe('');
    expect(rendered.querySelector('span')?.textContent).toBe('Filter rows');
    expect(rendered.querySelector('input')).toBe(input);
  });

  it('can visually hide a control label without removing it', () => {
    const input = document.createElement('input');
    const rendered = renderLabeledControl('Filter rows', input, { visuallyHiddenLabel: true });

    expect(rendered.textContent).toBe('Filter rows');
    expect(rendered.querySelector('span')?.classList.contains('sr-only')).toBe(true);
    expect(rendered.querySelector('input')).toBe(input);
  });

  it('renders a labeled control with an optional class name and prefix node', () => {
    const select = document.createElement('select');
    const prefix = document.createElement('svg');
    const rendered = renderLabeledControl('Window', select, { className: 'table-filter-facet', prefix });

    expect(rendered.className).toBe('table-filter-facet');
    expect(rendered.firstChild).toBe(prefix);
    expect(rendered.querySelector('span')?.textContent).toBe('Window');
    expect(rendered.querySelector('select')).toBe(select);
  });

  it('copies text to the clipboard and resolves true on success', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } });

    await expect(copyTextToClipboard('hello world')).resolves.toBe(true);
    expect(writeText).toHaveBeenCalledWith('hello world');
  });

  it('resolves false when the clipboard write rejects', async () => {
    const writeText = vi.fn().mockRejectedValue(new Error('denied'));
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } });

    await expect(copyTextToClipboard('hello world')).resolves.toBe(false);
  });

  it('resolves false when the Clipboard API is unavailable', async () => {
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: undefined });

    await expect(copyTextToClipboard('hello world')).resolves.toBe(false);
  });

  it('builds a copy control that reports success and resets its status', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } });

    const { button, status, reset } = createCopyControl({
      getContent: () => 'copy me',
      label: 'Copy prompt',
      buttonClassName: 'table-intent-copy-button',
      statusClassName: 'table-intent-copy-status',
      successText: 'Prompt copied.',
      failureText: 'Could not copy prompt.',
      trackState: true
    });

    expect(button.className).toBe('table-intent-copy-button');
    expect(status.className).toBe('table-intent-copy-status');
    expect(button.textContent).toBe('Copy prompt');

    button.click();
    await Promise.resolve();
    await Promise.resolve();

    expect(writeText).toHaveBeenCalledWith('copy me');
    expect(status.textContent).toBe('Prompt copied.');
    expect(button.getAttribute('data-copy-state')).toBe('success');

    reset();
    expect(status.textContent).toBe('');
    expect(button.hasAttribute('data-copy-state')).toBe(false);
  });

  it('reports failure text without tracking button state when trackState is unset', async () => {
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: undefined });

    const { button, status } = createCopyControl({
      getContent: () => 'copy me',
      label: 'Copy JSON',
      buttonClassName: 'configuration-copy-button',
      statusClassName: 'configuration-copy-status'
    });

    button.click();
    await Promise.resolve();
    await Promise.resolve();

    expect(status.textContent).toBe('Copy unavailable.');
    expect(button.hasAttribute('data-copy-state')).toBe(false);
  });

  it('renders a filter select with a placeholder option and sorted, de-duplicated values', () => {
    const select = renderFilterSelect('Filter operations by owner', 'All owners', ['bravo', 'Bravo', 'alpha', 'bravo']);

    expect(select.getAttribute('aria-label')).toBe('Filter operations by owner');
    const options = [...select.querySelectorAll('option')];
    expect(options.map((option) => option.value)).toEqual(['', 'Bravo', 'alpha', 'bravo']);
    expect(options.map((option) => option.textContent)).toEqual(['All owners', 'Bravo', 'alpha', 'bravo']);
  });

  it('renders a filter select with a caller-provided sort comparator', () => {
    const select = renderFilterSelect('Filter by owner', 'Owner', ['bravo', 'Bravo', 'alpha'],
      (left, right) => left.localeCompare(right));

    expect([...select.options].map((option) => option.value)).toEqual(['', 'alpha', 'bravo', 'Bravo']);
  });

  it('wraps a filter select in the shared rounded control', () => {
    const select = renderFilterSelect('Filter by mode', 'Mode', ['review', 'live']);
    const control = renderFilterSelectControl(select);

    expect(control.className).toBe('filter-select-control');
    expect(control.querySelector('select')).toBe(select);
    expect(select.classList.contains('filter-select-control-input')).toBe(true);
  });

  it('renders a search input with a shared placeholder and aria-label', () => {
    const input = renderSearchInput('Filter work items');

    expect(input.type).toBe('search');
    expect(input.placeholder).toBe('Filter work items');
    expect(input.getAttribute('aria-label')).toBe('Filter work items');
    expect(input.getAttribute('spellcheck')).toBe('false');
    expect(input.value).toBe('');
  });

  it('renders a search input with an initial value', () => {
    const input = renderSearchInput('Filter notifications', 'is:unread');

    expect(input.value).toBe('is:unread');
  });

  it('renders a shared disclosure summary label with a caller-classed "Show details" hint', () => {
    const nodes = /** @type {HTMLElement[]} */ (renderDisclosureSummaryLabel('Resources', 'campaign-readme-resources-hint'));

    expect(nodes).toHaveLength(2);
    expect(nodes[0].tagName).toBe('SPAN');
    expect(nodes[0].textContent).toBe('Resources');
    expect(nodes[1].tagName).toBe('SPAN');
    expect(nodes[1].className).toBe('campaign-readme-resources-hint');
    expect(nodes[1].textContent).toBe('Show details');
  });

  it('renders a disclosure summary label accepting a pre-built label node', () => {
    const label = h('span', null, 'View title');
    const nodes = /** @type {HTMLElement[]} */ (renderDisclosureSummaryLabel(label, 'view-disclosure-hint'));

    expect(nodes[0]).toBe(label);
    expect(nodes[1].className).toBe('view-disclosure-hint');
  });
});
