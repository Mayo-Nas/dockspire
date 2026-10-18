import fs from 'node:fs/promises';
import vm from 'node:vm';
import assert from 'node:assert/strict';

// Exercise the real revision UI against fictional responses; no Docker or browser.
class Element {
  constructor(tag, text = '', className = '') {
    Object.assign(this, { tag, textContent: text, className, children: [], events: {}, value: '', open: false, disabled: false });
    this.classList = { toggle() {} };
  }
  append(...children) { this.children.push(...children); }
  replaceChildren(...children) { this.children = children; }
  addEventListener(name, callback) { this.events[name] = callback; }
  setAttribute() {}
  scrollIntoView() {}
  showModal() { this.open = true; }
  close() { this.open = false; this.events.close?.(); }
  querySelector() { return null; }
  querySelectorAll(selector) {
    return this.children.flatMap(child => [...(child.tag === selector ? [child] : []), ...child.querySelectorAll(selector)]);
  }
}
const elements = new Map();
const get = id => { if (!elements.has(id)) elements.set(id, new Element(id)); return elements.get(id); };
const body = get('stack-dialog-body'), dialog = get('stack-dialog');
const texts = (element = body) => [element.textContent, ...element.children.flatMap(texts)].filter(Boolean);
const buttons = () => body.querySelectorAll('button');
const click = text => {
  const result = buttons().find(button => button.textContent === text);
  assert(result, `Missing button: ${text}`);
  return result.events.click();
};
const historical = 'a'.repeat(64), selected = 'b'.repeat(64), oldest = 'c'.repeat(64);
const requests = [];
let pending, failRestore = false, emptyCleanup = false, imported = false;
const record = { name: 'example', origin: 'new', compose: 'services:\n  web:\n    image: fixture:v2\n', fingerprint: selected, services: [], warnings: [], environmentNames: [] };
const context = vm.createContext({
  window: { monitorUI: { stackServices: () => new Element('section') } }, $: get,
  node: (tag, text, className) => new Element(tag, text, className),
  snapshot: { containers: [], stacks: [] }, session: { deployEnabled: false },
  refresh: async () => {},
  api: async (path, method = 'GET', input) => {
    requests.push({ path, method, input });
    const url = new URL(path, 'http://fixture');
    if (pending && pending.path === url.pathname) return new Promise(resolve => { pending.resolve = resolve; pending.ready?.(); });
    if (url.pathname.endsWith('/operations')) return { jobs: [], warnings: [] };
    if (url.pathname.endsWith('/restore')) {
      if (failRestore) throw new Error('This stack changed; reopen the revision');
      return { ...record, fingerprint: historical };
    }
    if (url.pathname.endsWith('/cleanup')) return method === 'POST' ? { removed: 1 } : { token: 'fixture-cleanup-review', keep: 20, remove: emptyCleanup ? [] : [oldest], retained: 23, eligible: emptyCleanup ? 0 : 1 };
    if (url.pathname.endsWith('/revisions')) return url.searchParams.has('cursor')
      ? { revisions: [{ revision: oldest, storedAt: '2026-01-01T00:00:00Z', initial: true }] }
      : { revisions: [{ revision: historical, storedAt: '2026-01-03T00:00:00Z' }, { revision: selected, storedAt: '2026-01-02T00:00:00Z', selected: true, applied: true }], nextCursor: selected };
    if (url.pathname.endsWith('/' + historical)) return {
      revision: historical, recordVersion: 'fixture-record-stamp', canRestore: !imported,
      compose: 'services:\n  web:\n    environment: {TOKEN: "[hidden]"}\n',
      comparison: { status: 'available', changes: [{ scope: 'Service web', field: 'Image', kind: 'changed', before: 'fixture:v2', after: 'fixture:v1' }, { scope: '.env', field: 'Environment · TOKEN', kind: 'changed' }] },
    };
    return { ...record, origin: imported ? 'portainer' : 'new' };
  },
});
vm.runInContext(await fs.readFile(new URL('../web/stacks.js', import.meta.url), 'utf8'), context);
const openSaved = () => context.window.stackUI.open({ name: 'example', definition: 'Saved' });
const openHistory = async () => { await openSaved(); await click('Configuration history'); };
const openRevision = async () => { await openHistory(); await buttons().find(button => button.textContent.startsWith('aaaaaaaaaaaa')).events.click(); };
const confirm = text => { const input = body.querySelectorAll('input')[0]; input.value = text; input.events.input(); };

await openHistory();
assert(texts().some(text => text.includes('Selected') && text.includes('Last applied')));
assert.equal(requests.filter(request => request.method === 'POST').length, 0);
await click('Load older configurations');
assert(texts().some(text => text.includes('Initial')));
assert(!buttons().some(button => button.textContent === 'Load older configurations'));
assert(requests.some(request => request.path.includes('cursor=' + selected)));

await openRevision();
assert(texts().includes('Selected saved configuration → Historical revision'));
assert(texts().includes('Details hidden'));
assert(texts().some(text => text.includes('replaces any unapplied saved edits')));
assert(body.querySelectorAll('textarea')[0].value.includes('[hidden]'));
assert.equal(buttons().find(button => button.textContent === 'Restore as draft').disabled, true);
confirm('wrong'); assert.equal(buttons().find(button => button.textContent === 'Restore as draft').disabled, true);
confirm('example'); assert.equal(buttons().find(button => button.textContent === 'Restore as draft').disabled, false);
await click('Restore as draft');
const restore = requests.find(request => request.path.endsWith('/restore'));
assert.equal(restore.method, 'POST');
assert.deepEqual(JSON.parse(JSON.stringify(restore.input)), { recordVersion: 'fixture-record-stamp', confirmation: 'example' });
assert(texts().some(text => text.includes('restored as the saved draft')));
assert(!requests.some(request => request.path.endsWith('/operations') && request.method === 'POST'));

await openHistory(); await click('Review revision cleanup');
assert(texts().some(text => text.includes('20 most recently stored')));
assert(texts().includes(oldest));
assert.equal(buttons().find(button => button.textContent === 'Remove reviewed configurations').disabled, true);
confirm('example'); await click('Remove reviewed configurations');
const cleanup = requests.find(request => request.path.endsWith('/cleanup') && request.method === 'POST');
assert.deepEqual(JSON.parse(JSON.stringify(cleanup.input)), { token: 'fixture-cleanup-review', confirmation: 'example' });
assert(texts().some(text => text.includes('1 old configurations removed')));
emptyCleanup = true;
await openHistory(); await click('Review revision cleanup');
assert(!buttons().some(button => button.textContent === 'Remove reviewed configurations'));

failRestore = true;
await openRevision(); confirm('example'); await click('Restore as draft');
assert(texts().some(text => text.includes('This stack changed')));
assert.equal(buttons().find(button => button.textContent === 'Restore as draft').disabled, false);
failRestore = false;
imported = true;
await openHistory(); assert(!buttons().some(button => button.textContent === 'Review revision cleanup'));
await buttons().find(button => button.textContent.startsWith('aaaaaaaaaaaa')).events.click();
assert(!buttons().some(button => button.textContent === 'Restore as draft'));
imported = false;

// A closed or replaced dialog must not render a late page, preview, or restore.
for (const phase of ['history', 'preview', 'restore']) {
  await openSaved();
  if (phase !== 'history') await click('Configuration history');
  if (phase === 'restore') { await buttons().find(button => button.textContent.startsWith('aaaaaaaaaaaa')).events.click(); confirm('example'); }
  const path = '/api/stacks/example/revisions' + (phase === 'preview' ? '/' + historical : phase === 'restore' ? '/' + historical + '/restore' : '');
  const started = new Promise(resolve => { pending = { path, ready: resolve }; });
  const action = phase === 'history' ? click('Configuration history') : phase === 'preview' ? buttons().find(button => button.textContent.startsWith('aaaaaaaaaaaa')).events.click() : click('Restore as draft');
  await started;
  dialog.close();
  pending.resolve(phase === 'history' ? { revisions: [] } : {});
  await action;
  assert.equal(dialog.open, false, `${phase} reopened a closed dialog`);
  pending = undefined;
}
console.log('Revision browsing, draft restore, reviewed cleanup, and close-during-load UI checks passed');
