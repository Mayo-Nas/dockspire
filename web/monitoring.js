'use strict';

window.monitorUI = (() => {
  const dialog = $('container-dialog'), body = $('container-body');
  let dispose = () => {};
  function button(label, action) {
    const result = node('button', label, 'button'); result.type = 'button';
    result.addEventListener('click', action); return result;
  }
  function field(label, input) {
    const result = node('label', undefined, 'form-field'); result.append(node('span', label), input); return result;
  }
  function list(entries) {
    const result = node('dl');
    entries.forEach(([key, value]) => {
      const row = node('div', undefined, 'detail-row'); row.append(node('dt', key), node('dd', value ?? '—')); result.append(row);
    }); return result;
  }
  function date(value) { return value && !value.startsWith('0001-') ? new Date(value).toLocaleString() : '—'; }
  function uptime(detail) {
    if (detail.state !== 'running' && detail.state !== 'paused') return 'Not running';
    const seconds = Math.max(0, Math.floor((Date.now() - Date.parse(detail.startedAt)) / 1000));
    if (!Number.isFinite(seconds)) return '—';
    return `${Math.floor(seconds / 86400)}d ${Math.floor(seconds / 3600) % 24}h ${Math.floor(seconds / 60) % 60}m`;
  }
  function chart(values, label, minimum = 1) {
    const svg = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
    svg.setAttribute('viewBox', '0 0 300 65'); svg.setAttribute('role', 'img'); svg.setAttribute('aria-label', label);
    const line = document.createElementNS(svg.namespaceURI, 'polyline');
    const max = Math.max(minimum, ...values);
    line.setAttribute('points', values.map((value, i) => `${i * 300 / Math.max(values.length - 1, 1)},${60 - value / max * 54}`).join(' '));
    svg.append(line); return svg;
  }
  function open(item, reviewUpdate, initial = 'overview', lifecycleAction = null) {
    dispose(); body.replaceChildren(); $('container-title').textContent = item.name;
    $('container-subtitle').textContent = `${item.stack || 'Standalone'}${item.service ? ` / ${item.service}` : ''}${snapshot?.mode === 'demo' ? ' · Sample data' : ''}`;
    let closed = false, tab = '', epoch = 0, timer, controller, busy = false, paused = false;
    let detail, logs = [], logResult, previous, samples = [];
    const base = `/api/containers/${encodeURIComponent(item.id)}`;
    const lifecycleButtons = new Map();
    const controls = node('div', undefined, 'form-actions lifecycle-controls');
    if (lifecycleAction) ['start', 'stop', 'restart'].forEach(action => {
      const control = button(`${action[0].toUpperCase() + action.slice(1)} container…`, () => { dialog.close(); lifecycleAction(action); });
      lifecycleButtons.set(action, control); controls.append(control);
    });
    function updateControls(state) { lifecycleButtons.forEach((control, action) => { control.disabled = action === 'start' ? !['created', 'exited'].includes(state) : state !== 'running'; }); }
    updateControls(item.state); if (lifecycleAction) body.append(controls);
    const tabs = node('div', undefined, 'monitor-tabs'), panel = node('section', undefined, 'monitor-content');
    const status = node('p', 'Loading diagnostics…', 'muted monitor-status'); status.setAttribute('role', 'status');
    const tabButtons = new Map();
    [['overview', 'Overview & health'], ['resources', 'Resources'], ['logs', 'Logs']].forEach(([key, label]) => {
      const control = button(label, () => select(key)); control.setAttribute('aria-controls', 'monitor-panel');
      tabButtons.set(key, control); tabs.append(control);
    }); panel.id = 'monitor-panel'; body.append(tabs, status, panel);
    const tools = node('div', undefined, 'monitor-log-tools');
    const range = node('select'); [['all', 'Recent logs'], ['5m', 'Last 5 minutes'], ['1h', 'Last hour'], ['24h', 'Last 24 hours']].forEach(([value, label]) => { const option = node('option', label); option.value = value; range.append(option); });
    const tail = node('select'); [100, 300, 1000, 2000].forEach(value => { const option = node('option', `Last ${value} lines`); option.value = value; tail.append(option); }); tail.value = '300';
    const search = node('input'); search.type = 'search'; search.placeholder = 'Search displayed logs';
    const pause = button('Pause live logs', () => { paused = !paused; pause.textContent = paused ? 'Resume live logs' : 'Pause live logs'; restart(!paused); if (paused) status.textContent = 'Live logs paused. Search or refresh the captured lines.'; });
    tools.append(field('Time range', range), field('Line limit', tail), field('Search', search), pause, button('Refresh logs', () => restart(true)));
    const logNote = node('p', '', 'muted');
    const logBox = node('div', undefined, 'log-view'); logBox.tabIndex = 0; logBox.setAttribute('aria-label', 'Container log lines');
    search.addEventListener('input', renderLogs);
    range.addEventListener('change', () => restart(true)); tail.addEventListener('change', () => restart(true));
    function active(current) { return !closed && dialog.open && current === epoch; }
    function renderLogs() {
      const query = search.value.toLowerCase(), visible = logs.filter(line => `${line.time || ''} ${line.stream} ${line.text}`.toLowerCase().includes(query));
      const atBottom = logBox.scrollHeight - logBox.scrollTop - logBox.clientHeight < 50;
      logBox.replaceChildren(...visible.map(line => {
        const row = node('div', undefined, `log-line ${line.stream}`);
        row.append(node('span', line.time || '', 'log-time'), node('span', line.stream, 'log-stream'), node('span', line.text, 'log-text')); return row;
      }));
      if (!visible.length) logBox.append(node('p', query ? 'No displayed lines match your search.' : 'No logs in this time range.', 'empty'));
      logNote.textContent = `${visible.length} / ${logs.length} captured lines${logResult?.truncated ? ' · Output limited; choose a shorter time range or fewer lines.' : ''} · Refreshes every 3 seconds. Logs can contain sensitive application output.`;
      if (!paused && atBottom) logBox.scrollTop = logBox.scrollHeight;
    }
    function renderOverview() {
      const health = detail.health === 'none' ? 'No health check configured' : detail.health;
      const top = node('div', undefined, 'monitor-health'); top.append(badge(detail.state), badge(health));
      if (detail.health === 'unhealthy') top.append(node('p', `Health check is failing (${detail.failingStreak} consecutive failures). Open Logs to investigate.`, 'error'));
      if (detail.oomKilled) top.append(node('p', 'Docker reports this container was killed after running out of memory.', 'error'));
      panel.replaceChildren(top, list([
        ['Container ID', detail.id], ['Image', detail.image], ['Image ID', detail.imageId], ['Uptime', uptime(detail)],
        ['Restarts', detail.restartCount], ['Restart policy', detail.restartPolicy || 'none'], ['Exit code', detail.state === 'exited' || detail.state === 'dead' ? detail.exitCode : '— (container has not exited)'],
        ['Created', date(detail.createdAt)], ['Started', date(detail.startedAt)], ['Finished', date(detail.finishedAt)],
        ['Last health check', date(detail.lastHealthAt)], ['Health check exit', detail.lastHealthExit],
        ['Published / exposed ports', detail.ports.join(', ') || 'None'], ['Logging driver', detail.logDriver || 'Host default'],
      ]));
      panel.append(node('h3', 'Volume mounts'));
      if (!detail.mounts.length) panel.append(node('p', 'No mounts.', 'muted'));
      detail.mounts.forEach(mount => panel.append(list([[mount.destination, `${mount.type} · ${mount.source || 'temporary storage'} · ${mount.readOnly ? 'read-only' : 'read/write'}`]])));
      if (reviewUpdate) panel.append(button('Review service update', () => { dialog.close(); reviewUpdate(); }));
      else panel.append(node('p', item.stack ? 'Monitoring only. Management of this stack remains with its current manager.' : 'Standalone container updates are not available yet.', 'muted'));
    }
    function renderResources(data) {
      if (!data.available) { panel.replaceChildren(node('p', 'Resource samples are available only while the container is running.', 'empty')); previous = null; return; }
      const elapsed = previous ? (Date.parse(data.readAt) - Date.parse(previous.readAt)) / 1000 : 0;
      const rate = key => previous && elapsed > 0 && data[key] >= previous[key] ? (data[key] - previous[key]) / elapsed : null;
      let rx = rate('networkRx'), tx = rate('networkTx'), read = rate('blockRead'), write = rate('blockWrite');
      if (rx === null || tx === null) rx = tx = null;
      if (read === null || write === null) read = write = null;
      if (!previous || Date.parse(data.readAt) > Date.parse(previous.readAt)) {
        samples.push({ cpu: data.cpuPercent, memory: data.memoryUsage, network: rx === null ? null : rx + tx, disk: read === null ? null : read + write }); samples = samples.slice(-40); previous = data;
      }
      const cards = node('div', undefined, 'monitor-metrics');
      const metrics = [
        ['CPU', data.cpuPercent === null ? 'Warming up…' : `${data.cpuPercent.toFixed(1)}%`, '100% = one CPU core', 'cpu', 100],
        ['Memory', bytes(data.memoryUsage), `${data.memoryLimit ? `${data.memoryPercent.toFixed(1)}% of ${bytes(data.memoryLimit)}` : 'Limit unavailable'} · cache excluded`, 'memory', 1],
        ['Network', rx === null ? 'Collecting…' : `${bytes(rx + tx)}/s`, rx === null ? 'Waiting for a second sample' : `↓ ${bytes(rx)}/s · ↑ ${bytes(tx)}/s`, 'network', 1],
        ['Disk I/O', read === null ? 'Collecting…' : `${bytes(read + write)}/s`, read === null ? 'Waiting for a second sample' : `Read ${bytes(read)}/s · Write ${bytes(write)}/s`, 'disk', 1],
      ];
      metrics.forEach(([label, value, note, key, minimum]) => {
        const card = node('div', undefined, 'stat'); const values = samples.map(sample => sample[key]).filter(Number.isFinite);
        card.append(node('div', label, 'stat-label'), node('div', value, 'metric-value'), node('div', note, 'stat-note'), chart(values, `${label}, last ${values.length} samples`, minimum)); cards.append(card);
      });
      panel.replaceChildren(cards, list([
        ['Processes / threads', data.pids], ['Network totals', `Received ${bytes(data.networkRx)} · Sent ${bytes(data.networkTx)}`],
        ['Disk totals', `Read ${bytes(data.blockRead)} · Written ${bytes(data.blockWrite)}`],
      ]), node('p', 'Recent samples from this visit. Charts refresh every 3 seconds; rates start with the second sample. No historical metrics are stored.', 'muted'));
    }
    async function tick(manual = false) {
      const current = epoch;
      if (closed || document.hidden || busy || (tab === 'logs' && paused && !manual)) return;
      busy = true;
      try {
        if (tab === 'logs') {
          const data = await api(`${base}/logs?tail=${tail.value}&range=${range.value}`, 'GET', undefined, controller.signal);
          if (!active(current)) return; logResult = data; logs = data.lines; renderLogs();
          status.textContent = `${paused ? 'Paused snapshot' : 'Live logs'} · Captured ${date(data.checkedAt)}`;
        } else {
          const data = await api(`${base}/details`, 'GET', undefined, controller.signal);
          if (!active(current)) return; detail = data; updateControls(detail.state);
          if (tab === 'overview') renderOverview();
          else if (detail.state !== 'running') renderResources({ available: false });
          else {
            const stats = await api(`${base}/stats`, 'GET', undefined, controller.signal);
            if (!active(current)) return; renderResources(stats);
          }
          status.textContent = `Live diagnostics · Updated ${new Date().toLocaleTimeString()}`;
        }
        status.classList.remove('error');
      } catch (error) {
        if (active(current) && !controller.signal.aborted) { status.textContent = `Refresh failed. Any displayed data is from the previous sample. ${error.message}`; status.classList.add('error'); }
      } finally {
        if (current === epoch) { busy = false; if (!closed && !document.hidden && !(tab === 'logs' && paused)) timer = setTimeout(() => tick(), 3000); }
      }
    }
    function restart(run) {
      epoch++; clearTimeout(timer); controller?.abort(); controller = new AbortController(); busy = false;
      if (run) tick(true);
    }
    function select(next) {
      tab = next; previous = null; samples = []; status.classList.remove('error'); status.textContent = 'Loading diagnostics…';
      tabButtons.forEach((control, key) => control.setAttribute('aria-pressed', String(next === key)));
      panel.replaceChildren();
      if (tab === 'logs') { panel.append(tools, logNote, logBox); renderLogs(); }
      restart(true);
    }
    function visibility() {
      previous = null;
      restart(!document.hidden && !(tab === 'logs' && paused));
      if (document.hidden) status.textContent = 'Monitoring paused while this page is hidden.';
    }
    document.addEventListener('visibilitychange', visibility);
    dispose = () => { closed = true; epoch++; clearTimeout(timer); controller?.abort(); document.removeEventListener('visibilitychange', visibility); };
    if (!dialog.open) dialog.showModal(); select(initial);
  }
  function stackServices(name) {
    const section = node('section', undefined, 'stack-monitor'); section.append(node('h3', 'Service health'));
    const containers = (snapshot?.containers || []).filter(item => item.stack === name);
    if (!containers.length) { section.append(node('p', 'No containers found in the current inventory.', 'muted')); return section; }
    const unhealthy = containers.filter(item => item.health === 'unhealthy').length;
    section.append(node('p', unhealthy ? `${unhealthy} container(s) have failing health checks. Select Logs to investigate.` : 'Health from the latest inventory snapshot. Containers without checks show “No check”.', unhealthy ? 'error' : 'muted'));
    containers.forEach(item => {
      const row = node('div', undefined, 'stack-service-row');
      row.append(nameCell(item.name, item.service || '', () => window.stackUI.container(item)), badge(item.state), badge(!item.health || item.health === 'none' ? 'No check' : item.health), button('Logs', () => window.stackUI.container(item, 'logs'))); section.append(row);
    }); return section;
  }
  $('close-container').addEventListener('click', () => dialog.close()); dialog.addEventListener('close', () => dispose());
  return { open, stackServices };
})();
