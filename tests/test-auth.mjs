import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import vm from 'node:vm';

class Element {
  value = '';
  events = {};
  classList = { toggle() {} };
  addEventListener(name, callback) { this.events[name] = callback; }
  setAttribute() {}
  removeAttribute() {}
}
function fixture() {
  const elements = new Map();
  const get = id => {
    if (!elements.has(id)) elements.set(id, new Element());
    return elements.get(id);
  };
  const redirects = [], requests = [];
  let status = 200, failure;
  const context = vm.createContext({
    AbortSignal,
    document: { getElementById: get, querySelector: get, querySelectorAll: () => [] },
    window: { addEventListener() {}, scrollTo() {} },
    location: { hash: '#system', replace: path => redirects.push(path) },
    fetch: async (path, options) => {
      requests.push({ path, options });
      if (failure) throw failure;
      return { ok: status === 200, status, json: async () => ({ csrfToken: 'fixture-csrf', error: status === 429 ? 'Too many sign-in attempts.' : 'Incorrect username or password' }) };
    },
  });
  return { context, get, redirects, requests, status: value => { status = value; }, failure: value => { failure = value; } };
}

const loginSource = await readFile(new URL('../web/login.js', import.meta.url), 'utf8');
const appSource = await readFile(new URL('../web/app.js', import.meta.url), 'utf8');
for (const status of [200, 401, 429]) {
  const f = fixture();
  f.status(status);
  f.get('username').value = 'admin';
  f.get('password').value = 'fictional test password only';
  vm.runInContext(loginSource, f.context);
  let prevented = false;
  const submit = f.get('login-form').events.submit;
  const pending = submit({ preventDefault() { prevented = true; } });
  await submit({ preventDefault() {} });
  await pending;
  assert.equal(prevented, true);
  assert.equal(f.requests.length, 1, 'Repeated submit must not send overlapping requests');
  assert.equal(f.requests[0].path, '/api/auth/login');
  assert.equal(f.requests[0].options.method, 'POST');
  assert.equal(JSON.parse(f.requests[0].options.body).username, 'admin');
  assert.equal(f.get('sign-in').disabled, false);
  if (status === 200) {
    assert.deepEqual(f.redirects, ['/']);
    assert.equal(f.get('password').value, '');
  } else {
    assert.deepEqual(f.redirects, []);
    assert.equal(f.get('login-error').hidden, false);
    assert.match(f.get('login-error').textContent, status === 429 ? /Too many/ : /Incorrect/);
  }
}
{
  const f = fixture();
  f.failure(Object.assign(new Error('fixture timeout'), { name: 'TimeoutError' }));
  vm.runInContext(loginSource, f.context);
  await f.get('login-form').events.submit({ preventDefault() {} });
  assert.match(f.get('login-error').textContent, /timed out/);
  assert.equal(f.get('sign-in').disabled, false);
}
// Expiry can occur during initial session loading or during an ordinary request.
for (const initialized of [false, true]) {
  const f = fixture();
  f.status(401);
  vm.runInContext(appSource, f.context);
  if (initialized) vm.runInContext('session = { csrfToken: "fixture" }', f.context);
  await assert.rejects(vm.runInContext('api("/api/inventory")', f.context), /session ended/);
  assert.deepEqual(f.redirects, ['/login']);
  assert.equal(vm.runInContext('session', f.context), null);
  assert.equal(f.requests.length, 1, 'An expired bootstrap session must not request inventory');
}
{
  const f = fixture();
  vm.runInContext(appSource, f.context);
  await f.get('sign-out').events.click();
  assert.deepEqual(f.redirects, ['/login']);
  const logout = f.requests.at(-1);
  assert.equal(logout.path, '/api/auth/logout');
  assert.equal(logout.options.method, 'POST');
  assert.equal(logout.options.headers['X-Stacklane-CSRF'], 'fixture-csrf');
  assert.equal(vm.runInContext('session', f.context), null);
}
console.log('Authentication UI regression checks passed');
