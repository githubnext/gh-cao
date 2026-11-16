import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  DEFERRED_STYLES_PREFETCH_DELAY_MS,
  observeDeferredStyles,
  prefetchDeferredStylesWhenIdle,
} from '../../src/deferred-styles.js';

function createBuiltDocument() {
  const document = window.document.implementation.createHTMLDocument('dashboard');
  const styles = document.createElement('style');
  styles.dataset.dashboardStyles = '';
  styles.dataset.deferredHref = './styles-deferred.css';
  document.head.append(styles);
  return document;
}

/** @param {Document} document @param {string} className */
const element = (document, className) => Object.assign(document.createElement('div'), { className });
/** @param {Document} document */
const deferredLink = (document) => document.querySelector('link[data-dashboard-deferred-styles]');
const flushMutations = () => new Promise((resolve) => setTimeout(resolve, 0));

describe('deferred styles', () => {
  afterEach(() => vi.useRealTimers());

  it('requests the sheet only after a deferred selector can match', async () => {
    const document = createBuiltDocument();
    document.body.append(element(document, 'dashboard-page view-description'));
    observeDeferredStyles(document, ['chart-widget', 'item legend']);
    const legend = element(document, 'legend');
    document.body.append(legend);
    await flushMutations();
    expect(deferredLink(document)).toBeNull();

    legend.append(element(document, 'item'));
    await flushMutations();
    expect(deferredLink(document)?.getAttribute('href')).toBe('./styles-deferred.css');
    expect(document.documentElement.dataset.deferredStyles).toBe('pending');

    deferredLink(document)?.dispatchEvent(new Event('load'));
    expect(document.documentElement.dataset.deferredStyles).toBeUndefined();
    document.body.append(element(document, 'chart-widget'));
    await flushMutations();
    expect(document.documentElement.dataset.deferredStyles).toBeUndefined();
    expect(document.querySelectorAll('link[data-dashboard-deferred-styles]')).toHaveLength(1);
  });

  it('hides content rendered while an idle prefetch is still loading', async () => {
    vi.useFakeTimers();
    const document = createBuiltDocument();
    Object.defineProperty(document, 'defaultView', { value: window });
    observeDeferredStyles(document, ['chart-widget']);
    prefetchDeferredStylesWhenIdle(document);
    vi.advanceTimersByTime(DEFERRED_STYLES_PREFETCH_DELAY_MS - 1);
    expect(deferredLink(document)).toBeNull();
    vi.advanceTimersByTime(1);
    vi.runOnlyPendingTimers();
    expect(deferredLink(document)).not.toBeNull();
    expect(document.documentElement.dataset.deferredStyles).toBeUndefined();

    vi.useRealTimers();
    document.body.append(element(document, 'chart-widget'));
    await flushMutations();
    expect(document.documentElement.dataset.deferredStyles).toBe('pending');
    deferredLink(document)?.dispatchEvent(new Event('error'));
    expect(document.documentElement.dataset.deferredStyles).toBeUndefined();
  });

  it('does nothing for unbuilt pages that inline every style', () => {
    const document = window.document.implementation.createHTMLDocument('dashboard');
    observeDeferredStyles(document, ['chart-widget']);
    document.body.append(element(document, 'chart-widget'));
    expect(deferredLink(document)).toBeNull();
  });
});
