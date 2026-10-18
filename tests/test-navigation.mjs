import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { setImmediate } from 'node:timers/promises';
import vm from 'node:vm';

// Exercise the real navigation/API code with an isolated DOM and fictional data.
// No browser, Docker connection, or additional package is required.
class Element {
  children = [];
  textContent = '';
  value = '';
  classList = { add() {}, toggle() {} };
  append(...children) { this.children.push(...children); }
  replaceChildren(...children) { this.children = children; }
  setAttribute() {}
  removeAttribute() {}
  addEventListener() {}
}

const elements = new Map();
const element = id => {
  if (!elements.has(id)) elements.set(id, new Element());
  return elements.get(id);
};
let inventoryReads = 0;
let versionReads = 0;
const context = vm.createContext({
  Node: Element,
  AbortSignal,
  location: { hash: '#system' },
  document: {
    getElementById: element,
    createElement: () => new Element(),
    querySelector: element,
    querySelectorAll: () => [],
  },
  window: { addEventListener() {}, scrollTo() {} },
  fetch: async path => {
    let data;
    if (path === '/api/session') data = { csrfToken: 'fixture', deployEnabled: false };
    else if (path === '/api/system/installation') {
      versionReads++;
      data = { mode: 'live', checkedAt: new Date().toISOString(), checks: [{ name: 'Docker Engine', state: 'ready', version: 'fixture-version' }] };
    } else if (path === '/api/inventory') {
      inventoryReads++;
      data = { mode: 'live', updatedAt: new Date().toISOString(), host: {}, containers: [], stacks: [{ name: 'fixture-stack', state: 'draft', running: 0, containers: 0, definition: 'Saved' }], images: [], networks: [], volumes: [] };
    } else throw new Error(`Unexpected request: ${path}`);
    return { ok: true, json: async () => data };
  },
});

async function settle() {
  for (let i = 0; i < 8; i++) await setImmediate();
}
function navigate(hash) {
  context.location.hash = hash;
  vm.runInContext('navigate()', context);
}
function text(element) {
  return [element.textContent, ...element.children.map(text)].join(' ');
}

for (const name of ['app', 'system']) {
  vm.runInContext(await readFile(new URL(`../web/${name}.js`, import.meta.url), 'utf8'), context, { filename: `${name}.js` });
}
await settle();
assert.equal(inventoryReads, 0, 'Docker page should not load broad inventory');
assert.equal(versionReads, 1);
assert.match(text(element('system-panel')), /fixture-version/);

navigate('#stacks');
await settle();
assert.equal(inventoryReads, 1, 'Leaving the Docker page must load inventory without a manual Refresh');
assert.match(text(element('inventory')), /fixture-stack/);
assert.match(element('notice').textContent, /LIVE DOCKER/);
assert.match(element('updated').textContent, /Last snapshot/);
assert.equal(element('table-title').textContent, 'Stacks');

navigate('#system');
await settle();
assert.equal(versionReads, 2);
assert.equal(inventoryReads, 1);
assert.match(element('notice').textContent, /Installed Docker Engine/);
navigate('#stacks');
await settle();
assert.equal(inventoryReads, 2, 'Returning to inventory must replace the version-only notice and timestamp');
assert.match(element('notice').textContent, /LIVE DOCKER/);
assert.match(element('updated').textContent, /Last snapshot/);

navigate('#images');
await settle();
assert.equal(inventoryReads, 2, 'Inventory views should reuse the current snapshot');
assert.equal(element('table-title').textContent, 'Images');
console.log('Navigation regression checks passed');
