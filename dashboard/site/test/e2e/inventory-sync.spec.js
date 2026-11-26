import { build } from 'esbuild';
import { expect, test } from '@playwright/test';
import { fileURLToPath } from 'node:url';

const bundle = await build({
  entryPoints: [fileURLToPath(new URL('../../src/components/inventory-sync.js', import.meta.url))],
  bundle: true, write: false, format: 'esm', platform: 'browser'
});
const moduleUrl = `data:text/javascript;charset=utf-8,${encodeURIComponent(bundle.outputFiles[0].text)}`;

for (const width of [1440, 390]) {
  test(`inventory Sync uses the production control and transport at ${width}px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 900 });
    let requests = 0;
    let finish = /** @type {(() => void) | undefined} */ (undefined);
    const accepted = new Promise((resolve) => { finish = () => resolve(undefined); });
    await page.route('https://inventory-sync.test/**', async (route) => {
      if (route.request().url().endsWith('/api/admin/collection/refresh')) {
        requests += 1;
        expect(route.request().method()).toBe('POST');
        await accepted;
        await route.fulfill({ status: requests === 1 ? 202 : 503, json: requests === 1 ? { queued: true } : { error: 'Collection unavailable' } });
        return;
      }
      await route.fulfill({ contentType: 'text/html', body: `
        <meta name="dashboard-data-backend" content="server-http">
        <main id="app"></main>
        <script type="module">
          import { renderInventorySync } from ${JSON.stringify(moduleUrl)};
          globalThis.__INVENTORY_REFRESH__ = true;
          document.querySelector('#app').append(renderInventorySync({}));
        </script>
      ` });
    });
    await page.goto('https://inventory-sync.test/');
    const button = page.getByRole('button', { name: 'Sync', exact: true });
    await expect(button).toBeVisible();
    await expect(button).toBeInViewport();
    await button.focus();
    await page.keyboard.press('Enter');
    await expect(button).toBeDisabled();
    await expect(button).toHaveAttribute('aria-busy', 'true');
    expect(requests).toBe(1);
    finish?.();
    await expect(page.locator('#app [role="status"]')).toContainText('Inventory sync queued');
    await expect(button).toBeEnabled();
    await button.click();
    await expect(page.locator('#app [role="alert"]')).toContainText('Unable to request inventory sync');
    await expect(button).toBeEnabled();
    expect(requests).toBe(2);
    await page.locator('#app').evaluate((root) => root.replaceChildren());
    await expect(button).toHaveCount(0);
  });
}
