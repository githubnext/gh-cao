import { existsSync } from 'node:fs';
import { defineConfig, devices } from '@playwright/test';

const executablePath = process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH
  || (existsSync('/usr/bin/chromium') ? '/usr/bin/chromium' : undefined);

export default defineConfig({
  fullyParallel: true,
  workers: process.env.CI ? 1 : undefined,
  testMatch: ['**/*.spec.js'],
  testDir: './test/e2e',
  timeout: 30000,
  use: {
    headless: true
  },
  projects: [
    {
      name: 'desktop-chrome',
      use: {
        ...devices['Desktop Chrome'],
        launchOptions: {
          args: ['--no-sandbox'],
          ...(executablePath ? { executablePath } : {})
        }
      }
    },
    {
      name: 'desktop-edge',
      testMatch: ['**/pwa-compatibility.spec.js', '**/service-worker-app-mode.spec.js'],
      use: {
        ...devices['Desktop Edge'],
        launchOptions: {
          args: ['--no-sandbox'],
          ...(executablePath ? { executablePath } : {})
        }
      }
    },
    {
      name: 'desktop-safari',
      testMatch: ['**/pwa-compatibility.spec.js', '**/indexeddb-stress.spec.js', '**/service-worker-app-mode.spec.js', '**/repositories-configure.spec.js'],
      use: devices['Desktop Safari']
    },
    {
      name: 'iphone-safari',
      testMatch: ['**/ios-indexeddb-cache.spec.js', '**/cached-refresh.spec.js'],
      use: devices['iPhone 13']
    }
  ]
});
