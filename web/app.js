'use strict';

const $ = (id) => document.getElementById(id);
const views = {
  dashboard: ['Dashboard', 'A clear view of what’s running.'],
  stacks: ['Stacks', 'Compose projects on this Docker host.'],
  containers: ['Containers', 'All containers, including stopped workloads.'],
  images: ['Images', 'Images available on your Docker host.'],
  networks: ['Networks', 'The networks connecting your services.'],
  volumes: ['Volumes', 'Docker-managed volumes. Host bind mounts are separate.'],
  system: ['Docker', 'Installed Docker Engine version.'],
};
let snapshot;
let view = 'dashboard';
let loading = false;
let session;

function requireSignIn(response) {
  if (response.status === 401) {
    session = null;
    location.replace('/login');
    throw new Error('Your session ended. Sign in to continue.');
  }
}

async function api(path, method = 'GET', body, signal) {
  if (!session) {
    const response = await fetch('/api/session', { cache: 'no-store' });
    requireSignIn(response);
    if (!response.ok) throw new Error('Could not initialize this session.');
    session = await response.json();
    $('build-version').textContent = session.version || 'dev';
    $('sign-out').hidden = Boolean(session.passwordless);
  }
  const options = { method, cache: 'no-store', signal: AbortSignal.timeout(method === 'POST' ? 175000 : 25000) };
  if (signal) options.signal = AbortSignal.any([signal, options.signal]);
  if (method === 'POST') {
    options.headers = { 'Content-Type': 'application/json', 'X-Dockspire-CSRF': session.csrfToken };
    options.body = JSON.stringify(body || {});
  }
  const response = await fetch(path, options);
  requireSignIn(response);
  const data = await response.json();
  if (!response.ok) {
    if (response.status === 403) session = null;
    throw new Error(data.error || 'This operation did not complete.');
  }
  return data;
}

function node(tag, text, className) {
  const result = document.createElement(tag);
  if (text !== undefined) result.textContent = String(text);
  if (className) result.className = className;
  return result;
}
function bytes(value) {
  if (!Number.isFinite(value) || value <= 0) return '0 B';
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB'];
  const index = Math.min(Math.floor(Math.log(value) / Math.log(1024)), units.length - 1);
  return `${(value / 1024 ** index).toFixed(index > 1 ? 1 : 0)} ${units[index]}`;
}
function badge(value) {
  const result = node('span', value, 'badge');
  if (['running', 'partial', 'paused', 'exited', 'stopped', 'healthy', 'unhealthy', 'starting'].includes(value)) result.classList.add(value);
  return result;
}
function openDetail(title, entries) {
  $('detail-title').textContent = title;
  const list = node('dl');
  entries.forEach(([key, value]) => {
    const row = node('div', undefined, 'detail-row');
    row.append(node('dt', key), node('dd', value || '—'));
    list.append(row);
  });
  $('detail-body').replaceChildren(list);
  $('detail').showModal();
}
function nameCell(name, secondary, action) {
  const cell = node('div');
  if (action) {
    const button = node('button', name, 'text-button');
    button.type = 'button';
    button.addEventListener('click', action);
    cell.append(button);
  } else cell.append(node('span', name));
  if (secondary) cell.append(node('div', secondary, 'cell-secondary'));
  return cell;
}
function drawTable(headers, rows) {
  const target = $('inventory');
  if (!rows.length) {
    target.replaceChildren(node('p', $('search').value ? 'No matching resources.' : 'No resources in this view.', 'empty'));
    return;
  }
  const table = node('table');
  const caption = node('caption', `${views[view][0]} inventory`, 'sr-only');
  const head = node('thead');
  const header = node('tr');
  headers.forEach((label) => { const th = node('th', label); th.scope = 'col'; header.append(th); });
  head.append(header);
  const body = node('tbody');
  rows.forEach((cells) => {
    const row = node('tr');
    cells.forEach((content) => {
      const cell = node('td');
      cell.append(content instanceof Node ? content : node('span', content));
      row.append(cell);
    });
    body.append(row);
  });
  table.append(caption, head, body);
  target.replaceChildren(table);
}
function render() {
  if (view === 'system') return;
  if (!snapshot) return;
  const data = snapshot;
  const running = data.containers.filter((item) => item.state === 'running').length;
  const stats = [
    ['Containers', data.containers.length, `${running} running · ${data.containers.length - running} not running`],
    ['Compose stacks', data.stacks.length, 'Running projects and saved drafts'],
    ['Images', data.images.length, `${bytes(data.images.reduce((total, image) => total + image.size, 0))} summed image sizes`],
    ['Networks', data.networks.length, `${data.volumes.length} named ${data.volumes.length === 1 ? 'volume' : 'volumes'}`],
  ];
  $('summary').replaceChildren(...stats.map(([label, value, note]) => {
    const card = node('div', undefined, 'stat');
    card.append(node('div', label, 'stat-label'), node('div', value, 'stat-value'), node('div', note, 'stat-note'));
    return card;
  }));
  $('count-stacks').textContent = data.stacks.length;
  $('count-containers').textContent = data.containers.length;
  $('host-details').replaceChildren(...[
    ['Host', data.host.name], ['Docker Engine', data.host.version], ['Negotiated API', data.host.api],
    ['Platform', `${data.host.os} · ${data.host.architecture}`], ['Resources', `${data.host.cpus} CPUs · ${bytes(data.host.memory)}`],
    ['Connection', data.mode === 'demo' ? 'Sample data' : 'Local Docker socket'],
  ].map(([label, value]) => {
    const row = node('dl', undefined, 'host-row');
    row.append(node('dt', label), node('dd', value));
    return row;
  }));
  const query = $('search').value.trim().toLowerCase();
  const items = (key) => data[key].filter((item) => JSON.stringify(item).toLowerCase().includes(query));
  $('table-title').textContent = view === 'dashboard' ? 'Containers' : views[view][0];
  const descriptions = {
    dashboard: 'Select a container name for details.',
    containers: 'Select a container name for details.',
    stacks: 'Import existing definitions or add a new Compose stack.',
    images: 'Image tags, identifiers, and sizes.',
    networks: 'Network drivers, scope, and isolation.',
    volumes: 'Docker-managed storage. Host folders do not appear in this list.',
  };
  $('table-description').textContent = descriptions[view];
  if (view === 'dashboard' || view === 'containers') {
    drawTable(['Name', 'State', 'Health', 'Stack', 'Published / exposed ports'], items('containers').map((item) => [
      nameCell(item.name, item.image, () => window.stackUI.container(item)),
      badge(item.state), badge(item.health === 'none' || !item.health ? 'No check' : item.health), item.stack || 'Standalone', item.ports.join(', ') || '—',
    ]));
  } else if (view === 'stacks') {
    drawTable(['Name', 'State', 'Containers', 'Definition'], items('stacks').map((item) => [
      nameCell(item.name, 'Docker Compose', () => window.stackUI.open(item)),
      badge(item.state), `${item.running} / ${item.containers} running`, item.definition === 'Unavailable' ? 'Unavailable · restore from backup' : item.definition === 'Saved' ? (item.origin === 'portainer' ? item.managed ? 'Taken over · managed' : item.takeoverStatus === 'pending' ? 'Takeover needs recovery' : 'Imported · review takeover' : 'Saved') : 'Not imported',
    ]));
  } else if (view === 'images') {
    drawTable(['Image', 'Size'], items('images').map((item) => [nameCell(item.tags.join(', '), item.id), bytes(item.size)]));
  } else if (view === 'networks') {
    drawTable(['Name', 'Driver', 'Scope', 'Internal'], items('networks').map((item) => [nameCell(item.name, item.id), item.driver, item.scope, item.internal ? 'Yes' : 'No']));
  } else if (view === 'volumes') {
    drawTable(['Name', 'Driver', 'Scope'], items('volumes').map((item) => [item.name, item.driver, item.scope]));
  }
}
function navigate() {
  const previousView = view;
  const requested = location.hash.slice(1);
  view = Object.hasOwn(views, requested) ? requested : 'dashboard';
  $('page-title').textContent = views[view][0];
  $('crumb').textContent = views[view][0];
  $('page-description').textContent = views[view][1];
  $('stack-actions').hidden = view !== 'stacks';
  $('system-panel').hidden = view !== 'system';
  document.querySelector('.workspace-grid').hidden = view === 'system';
  const overview = view === 'dashboard';
  $('summary').hidden = !overview;
  $('host-panel').hidden = !overview;
  document.querySelector('.workspace-grid').classList.toggle('list-view', !overview);
  document.querySelectorAll('[data-view]').forEach((link) => {
    if (link.dataset.view === view) link.setAttribute('aria-current', 'page');
    else link.removeAttribute('aria-current');
  });
  $('search').value = '';
  if (view === 'system') window.systemUI?.load();
  else {
    render();
    if (!snapshot || previousView === 'system') refresh();
  }
  window.scrollTo({ top: 0, left: 0, behavior: 'instant' });
}
async function refresh() {
  if (view === 'system') { if (window.systemUI) await window.systemUI.load(); return; }
  if (loading) return;
  loading = true;
  $('refresh').disabled = true;
  $('refresh').setAttribute('aria-busy', 'true');
  try {
    const data = await api('/api/inventory');
    snapshot = data;
    $('notice').classList.toggle('live', data.mode === 'live');
    $('notice').textContent = data.mode === 'demo' ? 'DEMO DATA · Sample inventory and local drafts. Container deployment is disabled.' : 'LIVE DOCKER · Review deployments and updates before applying them. Existing Portainer stacks remain view-only.';
    if (data.warnings?.length) $('notice').textContent += ` ${data.warnings.join(' ')}`;
    $('error').hidden = true;
    $('updated').textContent = `Last snapshot ${new Date(data.updatedAt).toLocaleString()}`;
    render();
  } catch (error) {
    $('error').textContent = `${snapshot ? 'Refresh failed. The inventory below is from the previous snapshot. ' : ''}${error.message}`;
    $('error').hidden = false;
    if (!snapshot) {
      $('notice').textContent = 'DISCONNECTED · No Docker inventory available.';
      $('inventory').replaceChildren(node('p', 'Connect a Docker socket, or run with --demo to explore sample data.', 'empty'));
    }
  } finally {
    loading = false;
    $('refresh').disabled = false;
    $('refresh').removeAttribute('aria-busy');
  }
}
$('refresh').addEventListener('click', refresh);
$('sign-out').addEventListener('click', async () => {
  if (session?.passwordless) return;
  $('sign-out').disabled = true;
  try {
    await api('/api/auth/logout', 'POST');
    session = null;
    location.replace('/login');
  } catch (error) {
    $('error').textContent = error.message;
    $('error').hidden = false;
  } finally {
    $('sign-out').disabled = false;
  }
});
$('search').addEventListener('input', render);
$('close-detail').addEventListener('click', () => $('detail').close());
window.addEventListener('hashchange', navigate);
navigate();
refresh();
