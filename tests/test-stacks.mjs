import fs from 'node:fs/promises';
import vm from 'node:vm';
import assert from 'node:assert/strict';

// Isolated UI regression checks using the actual stack UI and fictional API data.
class Element {
  constructor(tag, text = '', className = '') {
    Object.assign(this, { tag, textContent: text, className, children: [], events: {}, value: '', open: false, disabled: false });
    this.classList = { toggle() {} };
  }
  append(...children) { this.children.push(...children); }
  replaceChildren(...children) { this.children = children; }
  addEventListener(name, callback) { this.events[name] = callback; }
  setAttribute() {}
  focus() {}
  scrollIntoView() {}
  showModal() { this.open = true; }
  close() { this.open = false; this.events.close?.(); }
  click() { if (this.tag === 'a') archiveLinks.push({ href: this.href, filename: this.download }); }
  remove() { this.removed = true; }
  querySelectorAll(selector) {
    return this.children.flatMap(child => [...(child.tag === selector ? [child] : []), ...child.querySelectorAll(selector)]);
  }
  querySelector() { return null; }
}
const elements = new Map();
const get = id => {
  if (!elements.has(id)) elements.set(id, new Element(id));
  return elements.get(id);
};
const body = get('stack-dialog-body');
const texts = element => [element.textContent, ...element.children.flatMap(texts)].filter(Boolean);
const buttons = () => body.querySelectorAll('button');
let recoveryRequired = true;
let submitted;
let jobImages;
let pendingPage;
let pendingReview;
let pendingImport, importedCopy = false;
let takeoverStatus = '', takenOver = false;
const importRequests = [];
let emptyFirstPage = false;
let comparison = { status: 'available', changes: [
  { scope: 'Service web', field: 'Image', kind: 'changed', before: 'fixture/web:v1', after: 'fixture/web:v2' },
  { scope: 'Service web', field: 'Environment · TOKEN', kind: 'changed' },
  { scope: '.env', field: 'Environment · NEW', kind: 'added' },
], truncated: true };
const pageRequests = [];
const archiveRequests = [], archiveLinks = [], revokedURLs = [], timers = [];
let archiveStatus = 200;
let archiveType = 'application/zip';
let archiveLength = '9';
let delayedBlob;
const record = {
  name: 'example', origin: 'new', compose: 'services:\n  web:\n    image: fixture\n', fingerprint: 'fixture-revision',
  services: [{ name: 'web', image: 'fixture' }], warnings: [], environmentNames: [], lastDeploymentAttempt: { operationId: 'fixture-attempt' },
};
const job = index => ({ id: `fixture-${index}`, name: 'example', status: 'failed', kind: 'deploy', startedAt: '2026-10-16T12:00:00Z' });
const context = vm.createContext({
  window: { monitorUI: { stackServices: () => new Element('section', 'Fixture service health') } },
  $: get,
  node: (tag, text, className) => new Element(tag, text, className),
  snapshot: { containers: [], stacks: [] },
  session: { deployEnabled: true, csrfToken: 'fixture-csrf' },
  AbortController, AbortSignal,
  URL: { createObjectURL: () => 'blob:fixture-private-archive', revokeObjectURL: url => revokedURLs.push(url) },
  document: { body: new Element('html-body') },
  setTimeout: callback => { timers.push(callback); },
  fetch: async (path, options) => {
    archiveRequests.push({ path, options });
    return {
      ok: archiveStatus === 200, status: archiveStatus,
      headers: new Map([['Content-Type', archiveType], ['Content-Length', archiveLength]]),
      json: async () => ({ error: 'Fixture archive busy; retry later' }),
      blob: async () => delayedBlob ? new Promise(resolve => { delayedBlob.resolve = resolve; delayedBlob.ready(); }) : { size: 9 },
    };
  },
  refresh: async () => {},
  requireSignIn: response => { assert.notEqual(response.status, 401, 'Archive session unexpectedly expired'); },
  api: async (path, method, input) => {
    const url = new URL(path, 'http://fixture');
    if (url.pathname.endsWith('/preview-import')) {
      importRequests.push({ path, method });
      if (pendingImport) return new Promise(resolve => { pendingImport.resolve = resolve; });
      return { ...record, origin: 'portainer', fingerprint: 'fixture-import-fingerprint' };
    }
    if (url.pathname.endsWith('/import')) {
      importRequests.push({ path, method, input }); importedCopy = true;
      return { ...record, origin: 'portainer' };
    }
    if (url.pathname.endsWith('/operations') && method !== 'POST') {
      pageRequests.push(url.searchParams.get('cursor'));
      assert.equal(url.searchParams.get('details'), '1');
      assert.equal(url.searchParams.get('limit'), '10');
      if (pendingPage && url.searchParams.has('cursor')) return new Promise(resolve => { pendingPage.resolve = resolve; });
      return url.searchParams.has('cursor')
        ? { jobs: [job(11), job(12)], warnings: [] }
        : { jobs: emptyFirstPage ? [] : Array.from({ length: 10 }, (_, index) => job(index)), warnings: ['History fixture warning'], nextCursor: 'fixture-cursor' };
    }
    if (url.pathname.endsWith('/review') && pendingReview) return new Promise(resolve => { pendingReview.resolve = resolve; });
    if (url.pathname.endsWith('/review')) return { name: 'example', kind: input?.action === 'takeover' ? 'takeover' : recoveryRequired ? 'recover' : 'update', services: record.services, targets: ['example-web-1'], id: 'review-fixture', comparison };
    if (url.pathname.endsWith('/operations') && method === 'POST') {
      submitted = input;
      return { name: 'example', kind: input.portainerWritersDisabled ? 'takeover' : 'recover', status: 'succeeded', stage: 'complete', message: 'Fixture recovery succeeded', images: input.portainerWritersDisabled ? undefined : jobImages };
    }
    return { ...record, origin: importedCopy ? 'portainer' : 'new', managed: takenOver, takeoverStatus, recoveryRequired, deployedAt: recoveryRequired ? '' : '2026-10-16T12:00:00Z' };
  },
});
vm.runInContext(await fs.readFile(new URL('../web/stacks.js', import.meta.url), 'utf8'), context, { filename: 'stacks.js' });
await context.window.stackUI.open({ name: 'example', definition: 'Saved' });
assert(texts(body).some(text => text.includes('previous deployment needs inspection')));
assert(texts(body).includes('History fixture warning'));
assert.equal(buttons().filter(button => button.textContent.startsWith('failed')).length, 10);
await buttons().find(button => button.textContent === 'Load older operations').events.click();
assert.deepEqual(pageRequests, [null, 'fixture-cursor']);
assert.equal(buttons().filter(button => button.textContent.startsWith('failed')).length, 12);
assert(!buttons().some(button => button.textContent === 'Load older operations'));

await buttons().find(button => button.textContent === 'Review stack recovery').events.click();
assert(texts(body).some(text => text.includes('keeps existing images when available')));
assert(texts(body).includes('Configuration changes'));
assert(texts(body).includes('fixture/web:v1'));
assert(texts(body).includes('fixture/web:v2'));
assert(texts(body).includes('Service web · Environment · TOKEN'));
assert(texts(body).includes('Details hidden'));
assert(texts(body).some(text => text.includes('first 200 changes')));
assert.equal(body.querySelectorAll('tr').length, 4);
body.querySelectorAll('input')[0].value = 'example';
await buttons().find(button => button.textContent === 'Recover stack').events.click();
assert.equal(submitted.planId, 'review-fixture');
assert.equal(submitted.confirmation, 'example');
assert(texts(body).some(text => text.includes('SUCCEEDED')));

await context.window.stackUI.open({ name: 'unavailable-fixture', definition: 'Unavailable' });
assert(texts(body).some(text => text.includes('Restore this entry from a backup')));
assert.equal(buttons().length, 0);
recoveryRequired = false;
await context.window.stackUI.open({ name: 'example', definition: 'Saved' });
assert(buttons().some(button => button.textContent === 'Review stack update'));

pendingPage = {};
const loading = buttons().find(button => button.textContent === 'Load older operations').events.click();
get('stack-dialog').close();
pendingPage.resolve({ jobs: [job(100)], warnings: [] });
await loading;
assert.equal(get('stack-dialog').open, false);
assert.equal(buttons().filter(button => button.textContent.startsWith('failed')).length, 10);
pendingPage = null;
emptyFirstPage = true;
await context.window.stackUI.open({ name: 'example', definition: 'Saved' });
assert.equal(buttons().filter(button => button.textContent.startsWith('failed')).length, 0);
await buttons().find(button => button.textContent === 'Load older operations').events.click();
assert.equal(buttons().filter(button => button.textContent.startsWith('failed')).length, 2);

for (const [value, expected] of [
  [{ status: 'available', changes: [] }, 'No saved configuration changes.'],
  [{ status: 'available', changes: [], sourceOnly: true }, 'Only comments, formatting, or environment-file presentation changed.'],
  [{ status: 'initial', changes: [] }, 'No successful whole-stack deployment is recorded.'],
  [{ status: 'unavailable', warning: 'Fixture baseline is missing', changes: [] }, 'Fixture baseline is missing'],
  [undefined, 'Configuration comparison is unavailable.'],
]) {
  comparison = value;
  await context.window.stackUI.open({ name: 'example', definition: 'Saved' });
  await buttons().find(button => button.textContent === 'Review stack update').events.click();
  assert(texts(body).some(text => text.startsWith(expected)), expected);
  assert(buttons().some(button => button.textContent === 'Update now'));
}
comparison = { status: 'available', changes: [{ scope: 'Service web', field: 'Image', kind: 'changed', before: '<img src=x onerror=fixture>', after: 'fixture:v2' }] };
await context.window.stackUI.open({ name: 'example', definition: 'Saved' });
await buttons().find(button => button.textContent === 'Review stack update').events.click();
assert(texts(body).includes('<img src=x onerror=fixture>'));
assert.equal(body.querySelectorAll('img').length, 0);

get('backup-library').events.click();
assert(texts(body).some(text => text.includes('including secrets')));
assert(texts(body).some(text => text.includes('Back up workload data separately')));
assert(texts(body).some(text => text.includes('Restore remains an offline command')));
assert.equal(archiveRequests.length, 0, 'Opening the disclosure must not download secrets');
await buttons().find(button => button.textContent === 'Download library backup').events.click();
assert.equal(archiveRequests.at(-1).path, '/api/library/backup');
assert.equal(archiveRequests.at(-1).options.method, 'POST');
assert.equal(archiveRequests.at(-1).options.headers['X-Stacklane-CSRF'], 'fixture-csrf');
assert.deepEqual(JSON.parse(archiveRequests.at(-1).options.body), { includeSecrets: true });
assert(archiveLinks.at(-1).filename.startsWith('dockspire-library-'));
assert(texts(body).some(text => text.includes('handed to your browser')));
timers.splice(0).forEach(callback => callback());
assert.deepEqual(revokedURLs, ['blob:fixture-private-archive']);

await context.window.stackUI.open({ name: 'example', definition: 'Saved' });
await buttons().find(button => button.textContent === 'Export Compose').events.click();
assert(texts(body).some(text => text.includes('Previous revisions and operation history are excluded')));
await buttons().find(button => button.textContent === 'Download Compose export').events.click();
assert.equal(archiveRequests.at(-1).path, '/api/stacks/example/export');
assert.equal(archiveLinks.at(-1).filename, 'dockspire-example-compose.zip');

for (const [status, type, length, expected] of [
  [409, 'application/json', '9', 'Fixture archive busy'],
  [200, 'text/html', '9', 'did not return a ZIP'],
  [200, 'application/zip', '12', 'incomplete or too large'],
]) {
  archiveStatus = status; archiveType = type; archiveLength = length;
  get('backup-library').events.click();
  const count = archiveLinks.length;
  const button = buttons().find(button => button.textContent === 'Download library backup');
  await button.events.click();
  assert(texts(body).some(text => text.includes(expected)), expected);
  assert.equal(archiveLinks.length, count);
  assert.equal(button.disabled, false, 'Failure must allow retry');
}
archiveStatus = 200; archiveType = 'application/zip'; archiveLength = '9';
get('backup-library').events.click();
const blobStarted = new Promise(resolve => { delayedBlob = { ready: resolve }; });
const beforeClose = archiveLinks.length;
const downloading = buttons().find(button => button.textContent === 'Download library backup').events.click();
await blobStarted;
const signal = archiveRequests.at(-1).options.signal;
get('stack-dialog').close();
assert.equal(signal.aborted, true);
delayedBlob.resolve({ size: 9 });
await downloading;
assert.equal(archiveLinks.length, beforeClose, 'Closed dialog must not hand off a late secret download');
for (const [images, expected] of [
  [undefined, 'Image IDs were not recorded for this operation.'],
  [{ services: [{ service: 'web', status: 'changed', beforeComplete: true, afterComplete: true, before: ['sha256:' + 'a'.repeat(64)], after: ['sha256:' + 'b'.repeat(64)] }] }, 'Changed'],
  [{ warning: 'Fixture after read unavailable', services: [{ service: 'web', status: 'unavailable', beforeComplete: true, afterComplete: false, before: ['sha256:' + 'a'.repeat(64)], after: [] }] }, 'Unavailable'],
]) {
  jobImages = images;
  await context.window.stackUI.open({ name: 'example', definition: 'Saved' });
  await buttons().find(button => button.textContent === 'Review stack update').events.click();
  body.querySelectorAll('input')[0].value = 'example';
  await buttons().find(button => button.textContent === 'Update now').events.click();
  assert(texts(body).includes(expected));
  if (images) {
    assert(texts(body).includes('Observed image changes'));
    assert(texts(body).includes('sha256:' + 'a'.repeat(64)));
    assert.equal(body.querySelectorAll('tr').length, 2);
  }
}
for (const supersede of ['close', 'newer']) {
  await context.window.stackUI.open({ name: 'example', definition: 'Saved' });
  pendingReview = {};
  const reviewing = buttons().find(button => button.textContent === 'Review stack update').events.click();
  if (supersede === 'close') get('stack-dialog').close();
  else get('backup-library').events.click();
  const title = get('stack-dialog-title').textContent;
  pendingReview.resolve({ name: 'example', kind: 'update', services: record.services, targets: [], id: 'stale-review' });
  await reviewing;
  assert.equal(get('stack-dialog').open, supersede !== 'close');
  assert.equal(get('stack-dialog-title').textContent, title, 'Delayed review must not replace a newer dialog');
  pendingReview = null;
}
console.log('Stack history, recovery, comparison, private archive, and observed-image UI checks passed');

// Portainer source access is optional; opening disabled setup must read nothing.
get('import-stack').events.click();
assert(texts(body).includes('Compose file upload available'));
await buttons().find(button => button.textContent === 'Choose Portainer').events.click();
assert(texts(body).some(text => text.includes('compose.portainer.yaml')));
assert(!buttons().some(button => button.textContent === 'Preview import'));
assert.equal(importRequests.length, 0);
await buttons().find(button => button.textContent === 'Load exported Compose file').events.click();
assert.equal(get('stack-dialog-title').textContent, 'Import from Portainer');
assert(texts(body).some(text => text.includes('Choose an unused stack name')));
assert(texts(body).some(text => text.includes('does not adopt an existing stack')));
assert.equal(body.querySelectorAll('textarea')[0].value, '');
assert.equal(importRequests.length, 0, 'Manual file import must not read direct source files');
context.session.importEnabled = true;
get('import-stack').events.click();
await buttons().find(button => button.textContent === 'Choose Portainer').events.click();
assert(texts(body).some(text => text.includes('No supported, unimported')));
assert(texts(body).some(text => text.includes('Swarm stacks')));
context.snapshot.stacks = [{ name: 'example', importId: '4', definition: 'Not imported' }, { name: 'saved', importId: '5', definition: 'Saved' }];
function choosePortainer() {
  get('import-stack').events.click();
  buttons().find(button => button.textContent === 'Choose Portainer').events.click();
}
for (const include of [false, true]) {
  context.session.environmentImportEnabled = include;
  choosePortainer();
  assert(texts(body).some(text => text.includes(include ? 'files will be copied, when present' : 'files are excluded')));
  assert.equal(body.querySelectorAll('option').length, 1, 'Saved copies must not be imported again');
}
for (const supersede of ['close', 'newer']) {
  choosePortainer(); body.querySelectorAll('select')[0].value = 'example';
  pendingImport = {};
  const loading = buttons().find(button => button.textContent === 'Preview import').events.click();
  if (supersede === 'close') get('stack-dialog').close(); else get('backup-library').events.click();
  const title = get('stack-dialog-title').textContent;
  pendingImport.resolve({ ...record, origin: 'portainer' }); await loading;
  assert.equal(get('stack-dialog').open, supersede !== 'close');
  assert.equal(get('stack-dialog-title').textContent, title);
  pendingImport = null;
}
choosePortainer(); body.querySelectorAll('select')[0].value = 'example';
await buttons().find(button => button.textContent === 'Preview import').events.click();
assert.equal(importedCopy, false, 'Preview must not save the copy');
assert(texts(body).some(text => text.includes('files will be copied, when present')), 'The final preview must retain the environment-copy disclosure');
await buttons().find(button => button.textContent === 'Import definition · leave containers running').events.click();
assert.equal(importedCopy, true);
assert.deepEqual(JSON.parse(JSON.stringify(importRequests.at(-1))), { path: '/api/stacks/example/import', method: 'POST', input: { fingerprint: 'fixture-import-fingerprint' } });
assert(texts(body).some(text => text.includes('importing alone never changes containers')));
assert(!buttons().some(button => /Edit configuration|Review stack|Start stack|Stop stack|Restart stack/.test(button.textContent)));
await buttons().find(button => button.textContent === 'Review takeover').events.click();
assert(texts(body).some(text => text.includes('Do not delete the Portainer stack')));
const takeoverSubmit = buttons().find(button => button.textContent === 'Take over and redeploy');
const [writers, project] = body.querySelectorAll('input');
assert.equal(takeoverSubmit.disabled, true);
project.value = 'example'; project.events.input(); assert.equal(takeoverSubmit.disabled, true);
writers.checked = true; writers.events.change(); assert.equal(takeoverSubmit.disabled, false);
project.value = 'wrong'; project.events.input(); assert.equal(takeoverSubmit.disabled, true);
project.value = 'example'; project.events.input(); await takeoverSubmit.events.click();
assert.deepEqual(JSON.parse(JSON.stringify(submitted)), { planId: 'review-fixture', confirmation: 'example', portainerWritersDisabled: true });
assert(!texts(body).includes('Download images')); assert(texts(body).some(text => text.includes('no images were downloaded')));
takeoverStatus = 'pending'; await context.window.stackUI.open({ name: 'example', definition: 'Saved' });
assert(buttons().some(button => button.textContent === 'Review takeover recovery'));
assert(!buttons().some(button => button.textContent === 'Edit configuration'));
takeoverStatus = 'managed'; takenOver = true; await context.window.stackUI.open({ name: 'example', definition: 'Saved' });
assert(buttons().some(button => button.textContent === 'Edit configuration'));
assert(buttons().some(button => button.textContent === 'Review stack update'));
context.snapshot.stacks = [{ name: 'example', definition: 'Saved', origin: 'portainer', managed: true }];
let updateAction, lifecycleAction;
context.window.monitorUI.open = (_item, update, _initial, lifecycle) => { updateAction = update; lifecycleAction = lifecycle; };
await context.window.stackUI.container({ id: 'fixture-container', stack: 'example', name: 'example-web-1', service: 'web' });
assert.equal(typeof updateAction, 'function'); assert.equal(typeof lifecycleAction, 'function');
console.log('Optional Portainer import setup, preview, copy, stale response, and read-only UI checks passed');
