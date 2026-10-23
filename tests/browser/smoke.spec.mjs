import { test, expect } from '@playwright/test';
import { spawn } from 'node:child_process';
import { mkdir, realpath } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import path from 'node:path';

const root = fileURLToPath(new URL('../../', import.meta.url));
const temporary = path.join(root, '.local', 'browser-smoke', 'tmp');
let child, origin;
test.beforeAll(async () => {
 const binary = await realpath(process.env.DOCKSPIRE_BROWSER_FIXTURE_BINARY || '');
 const allowed = await realpath(path.join(root, '.local'));
 const relative = path.relative(allowed, binary);
 if (!relative || relative.startsWith('..') || path.isAbsolute(relative)) throw new Error('Use the compiled test fixture under the project .local directory');
 await mkdir(temporary, { recursive: true });
 child = spawn(binary, ['-test.run=^TestBrowserSmokeFixture$', '-test.v', '-test.timeout=110s'], {
  cwd: root,
  env: { ...process.env, DOCKSPIRE_BROWSER_FIXTURE: '1', TMP: temporary, TEMP: temporary, TMPDIR: temporary },
  stdio: ['pipe', 'pipe', 'pipe'],
 });
 origin = await new Promise((resolve, reject) => {
  let output = '';
  const timer = setTimeout(() => reject(new Error('Browser fixture did not start')), 20_000);
  child.on('error', error => { clearTimeout(timer); reject(error); });
  child.on('exit', () => { clearTimeout(timer); reject(new Error('Browser fixture exited before readiness')); });
  child.stderr.on('data', () => {});
  child.stdout.on('data', chunk => {
   output = (output + chunk.toString()).slice(-65_536);
   const match = output.match(/DOCKSPIRE_BROWSER_URL=(http:\/\/127\.0\.0\.1:[0-9]+)/);
   if (match) { clearTimeout(timer); resolve(match[1]); }
  });
 });
});
test.afterAll(async () => {
 if (!child) return;
 const finished = new Promise((resolve, reject) => {
  const timer = setTimeout(() => { child.kill(); reject(new Error('Browser fixture did not stop')); }, 5_000);
  child.once('exit', code => { clearTimeout(timer); code === 0 ? resolve() : reject(new Error('Browser fixture failed')); });
 });
 child.stdin.end();
 await finished;
});
test('setup, sign-in, draft edit and operation review in a real browser', async ({ page, context }) => {
 await context.route('**/*', route => route.request().url().startsWith(origin + '/') ? route.continue() : route.abort());
 const errors = [];
 page.on('pageerror', error => errors.push(error.message));
 const password = 'fictional browser fixture password';
 await page.goto(origin + '/');
 await expect(page).toHaveURL(origin + '/setup');
 await page.getByLabel('Password', { exact: true }).fill(password);
 await page.getByLabel('Confirm password', { exact: true }).fill(password);
 await page.getByRole('button', { name: 'Create administrator', exact: true }).click();
 await expect(page).toHaveURL(origin + '/login');
 await page.getByLabel('Password', { exact: true }).fill(password);
 await page.getByRole('button', { name: 'Sign in', exact: true }).click();
 await expect(page.getByRole('heading', { name: 'Dashboard', exact: true })).toBeVisible();
 const session = await (await page.request.get(origin + '/api/session')).json();
 expect(session.deployEnabled).toBe(true);
 await page.locator('nav a[href="#stacks"]').click();
 await page.getByRole('button', { name: 'Add stack', exact: true }).click();
 await page.getByLabel('Stack name', { exact: true }).fill('browser-fixture');
 await page.getByLabel('Compose YAML', { exact: true }).fill('services:\n  web:\n    image: fixture/web:v1\n');
 await page.getByRole('button', { name: 'Save and review', exact: true }).click();
 await expect(page.getByText('Saved draft. No containers have been deployed by this draft.', { exact: true })).toBeVisible();
 await page.getByRole('button', { name: 'Edit configuration', exact: true }).click();
 await page.getByLabel('Compose YAML', { exact: true }).fill('services:\n  web:\n    image: fixture/web:v2\n');
 await page.getByRole('button', { name: 'Save and review', exact: true }).click();
 await page.getByRole('button', { name: 'Review deployment', exact: true }).click();
 await expect(page.getByText('Configuration changes', { exact: true })).toBeVisible();
 await expect(page.getByText(/fixture\/web:v2/).first()).toBeVisible();
 const confirmation = page.getByLabel('Type browser-fixture to confirm', { exact: true });
 await expect(confirmation).toHaveValue('');
 // Invalid confirmation is rejected by the real server before any job or fake-runner mutation.
 await page.getByRole('button', { name: 'Deploy stack', exact: true }).click();
 await expect(page.locator('.operation-message')).toContainText(/confirm|Type|review/i);
 await confirmation.fill('browser-fixture');
 await expect(confirmation).toHaveValue('browser-fixture');
 // Stop at review: no confirmed deployment is submitted.
 await page.getByRole('button', { name: 'Close', exact: true }).filter({ visible: true }).first().click();
 await page.getByRole('button', { name: 'Sign out', exact: true }).click();
 await expect(page).toHaveURL(origin + '/login');
 expect(errors).toEqual([]);
});
