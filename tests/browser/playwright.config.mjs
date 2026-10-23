import { defineConfig } from '@playwright/test';
import { fileURLToPath } from 'node:url';
export default defineConfig({
 testDir: '.',
 testMatch: 'smoke.spec.mjs',
 timeout: 45_000,
 globalTimeout: 90_000,
 workers: 1,
 retries: 0,
 reporter: 'line',
 outputDir: fileURLToPath(new URL('../../.local/browser-smoke/results/', import.meta.url)),
 use: {
  headless: true,
  viewport: { width: 1280, height: 900 },
  trace: 'off', screenshot: 'off', video: 'off',
  launchOptions: process.env.DOCKSPIRE_BROWSER_EXECUTABLE ? { executablePath: process.env.DOCKSPIRE_BROWSER_EXECUTABLE } : {},
 },
});
