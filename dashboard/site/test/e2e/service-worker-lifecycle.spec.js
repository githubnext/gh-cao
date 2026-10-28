import { readFileSync } from 'node:fs';
import { createServer } from 'node:http';
import { expect, test } from '@playwright/test';

const workerSource = readFileSync(new URL('../../service-worker.js', import.meta.url), 'utf8');

for (const scenario of ['slow installation', 'slow activation', 'recovery']) {
  test(`service worker remains registered and becomes active after ${scenario}`, async ({ page }) => {
    /** @type {string[]} */
    const workerRequests = [];
    /** @type {string[]} */
    const errors = [];
    page.on('pageerror', (error) => errors.push(error.message));
    const server = createServer((request, response) => {
      const url = new URL(request.url ?? '/', 'http://localhost');
      response.setHeader('Cache-Control', 'no-store');
      if (url.pathname === '/dashboard/service-worker.js') {
        workerRequests.push(url.search);
        let worker = workerSource;
        if (scenario === 'slow installation') {
          worker = worker.replace('const APP_ASSETS = [];', "const APP_ASSETS = ['./slow-asset.json'];");
        } else {
          worker = worker.replace('const keys = await caches.keys();',
            'await new Promise((resolve) => setTimeout(resolve, 4000));\n    const keys = await caches.keys();');
        }
        if (scenario === 'recovery' && !url.searchParams.has('force-update')) {
          worker = worker.replace("type: 'CANARY_OK'", "type: 'CANARY_BROKEN'");
        }
        response.writeHead(200, { 'Content-Type': 'text/javascript' });
        response.end(worker);
      } else if (url.pathname === '/dashboard/slow-asset.json') {
        response.writeHead(200, { 'Content-Type': 'application/json' });
        const timer = setTimeout(() => response.end('{}'), 4000);
        response.on('close', () => clearTimeout(timer));
      } else if (/^\/dashboard\/src\/[a-z/-]+\.(js|json)$/.test(url.pathname)) {
        response.writeHead(200, {
          'Content-Type': url.pathname.endsWith('.json') ? 'application/json' : 'text/javascript'
        });
        response.end(readFileSync(new URL(`../../${url.pathname.slice('/dashboard/'.length)}`, import.meta.url)));
      } else if (url.pathname === '/dashboard/') {
        response.writeHead(200, { 'Content-Type': 'text/html' });
        response.end('<!doctype html><title>Service worker lifecycle</title>');
      } else {
        response.writeHead(404);
        response.end();
      }
    });
    await new Promise((resolve) => server.listen(0, '127.0.0.1', () => resolve(undefined)));
    try {
      const address = server.address();
      if (!address || typeof address === 'string') throw new Error('Test server has no port.');
      await page.goto(`http://127.0.0.1:${address.port}/dashboard/`);
      if (scenario === 'recovery') {
        await page.evaluate(async () => {
          await navigator.serviceWorker.register('./service-worker.js');
          await navigator.serviceWorker.ready;
        });
      }
      const result = await page.evaluate(async () => {
        const { ensureHealthyDashboardServiceWorker } = await import(
          new URL('./src/dashboard-data-updates.js', location.href).href
        );
        const { registration, worker } = await ensureHealthyDashboardServiceWorker(
          navigator.serviceWorker, new URL('./service-worker.js', location.href)
        );
        return {
          state: worker.state,
          active: registration.active === worker,
          registered: (await navigator.serviceWorker.getRegistration())?.scope === registration.scope,
          controlled: navigator.serviceWorker.controller === worker
        };
      });
      expect(result).toEqual({ state: 'activated', active: true, registered: true, controlled: true });
      expect(workerRequests.filter((search) => search.includes('force-update=')))
        .toHaveLength(scenario === 'recovery' ? 1 : 0);
      expect(errors).toEqual([]);
    } finally {
      await page.close();
      await new Promise((resolve, reject) => server.close((error) => error ? reject(error) : resolve(undefined)));
    }
  });
}
