import { expect, it } from 'vitest';
import { renderDashboard } from '../../src/presenter.js';
import { authoritativeDashboard } from '../authoritative-dashboard.js';

it('renders the cost estimate disclaimer before charts without requiring cost data', () => {
  const page = authoritativeDashboard.dashboard.pages.find(
    (/** @type {{ id: string }} */ page) => page.id === 'cost'
  );
  const disclaimer = 'All costs shown are estimates. Review your billing dashboard for precise numbers.';
  expect(page.sections).toBeUndefined();
  expect(page.views[0].description).toContain(disclaimer);

  const rendered = renderDashboard({
    document: {
      languageVersion: '0.1.0',
      dashboard: {
        id: 'cost-disclaimer-test',
        title: 'Cost',
        pages: [page]
      }
    },
    sources: {}
  });
  expect(rendered.querySelector('[data-section-id="cost-estimates"]')).toBeNull();
  const firstView = rendered.querySelector('[data-view-id]');
  expect(firstView?.getAttribute('data-view-id')).toBe('cost-by-repository');
  expect(firstView?.textContent).toContain(disclaimer);
  expect(firstView?.textContent?.indexOf(disclaimer)).toBeLessThan(
    firstView?.textContent?.indexOf('This view cannot be shown') ?? -1);
});
