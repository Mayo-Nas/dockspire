import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import vm from 'node:vm';

const source = await readFile(new URL('../web/setup.js', import.meta.url), 'utf8');
function fixture({ required = true, expired = false, status = 201 } = {}) {
  const elements = new Map();
  const get = id => {
    if (!elements.has(id)) elements.set(id, { value: '', events: {}, disabled: true, addEventListener(name, callback) { this.events[name] = callback; }, setAttribute() {}, removeAttribute() {} });
    return elements.get(id);
  };
  const requests = [], redirects = [];
  const context = vm.createContext({
    AbortSignal, document: { getElementById: get }, location: { replace: path => redirects.push(path) },
    fetch: async (path, options) => {
      requests.push({ path, options });
      return options.method === 'POST'
        ? { ok: status === 201, status, json: async () => ({ error: 'Setup timed out. Restart Dockspire.' }) }
        : { ok: true, json: async () => ({ required, expired }) };
    },
  });
  return { get, requests, redirects, context };
}
async function load(f) {
  vm.runInContext(source, f.context);
  await new Promise(resolve => setImmediate(resolve));
}
for (const options of [{}, { expired: true }, { required: false }]) {
  const f = fixture(options);
  await load(f);
  assert.equal(f.requests[0].path, '/api/auth/setup');
  if (options.expired) {
    assert.equal(f.get('create-admin').disabled, true);
    assert.match(f.get('setup-error').textContent, /timed out/);
  } else if (options.required === false) assert.deepEqual(f.redirects, ['/login']);
  else assert.equal(f.get('create-admin').disabled, false);
}
for (const status of [201, 400, 403, 409]) {
  const f = fixture({ status });
  await load(f);
  f.get('password').value = 'fictional setup password only';
  f.get('confirmation').value = 'different';
  const submit = f.get('setup-form').events.submit;
  await submit({ preventDefault() {} });
  assert.equal(f.requests.length, 1, 'Mismatched passwords must stay in the browser');
  f.get('confirmation').value = f.get('password').value;
  f.get('setup-token').value = 'fictional-setup-token';
  const pending = submit({ preventDefault() {} });
  await submit({ preventDefault() {} });
  await pending;
  assert.equal(f.requests.length, 2, 'Repeated submit must not overlap');
  assert.equal(JSON.parse(f.requests[1].options.body).token, 'fictional-setup-token');
  if (status === 201) {
    assert.deepEqual(f.redirects, ['/login']);
    assert.equal(f.get('password').value, '');
    assert.equal(f.get('confirmation').value, '');
    assert.equal(f.get('setup-token').value, '');
  } else if (status === 409) assert.deepEqual(f.redirects, ['/login']);
  else {
    assert.equal(f.get('setup-error').hidden, false);
    assert.equal(f.get('create-admin').disabled, status === 403);
  }
}
console.log('Browser administrator setup regression checks passed');
