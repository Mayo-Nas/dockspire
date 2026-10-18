'use strict';

window.systemUI = (() => {
  const panel = $('system-panel');
  let loading = false;
  async function load() {
    if (loading) return;
    loading = true; $('refresh').disabled = true; $('error').hidden = true;
    try {
      const installation = await api('/api/system/installation');
      if (view !== 'system') return;
      const engine = installation.checks.find(check => check.name === 'Docker Engine');
      if (!engine) throw new Error('Docker version could not be checked.');
      $('notice').textContent = installation.mode === 'demo' ? 'DEMO · Host version is not inspected.' : 'Installed Docker Engine version on this host.';
      const card = node('article', undefined, 'panel system-card');
      card.append(node('h2', 'Docker Engine'), node('p', engine.version || (installation.mode === 'demo' ? 'Not inspected in demo' : 'Version unavailable'), 'system-version'));
      if (engine.state !== 'ready' && installation.mode !== 'demo') card.append(node('p', engine.message, 'notice error'));
      panel.replaceChildren(card);
      $('updated').textContent = `Version checked ${new Date(installation.checkedAt).toLocaleString()}`;
    } catch (error) {
      if (view === 'system') { panel.replaceChildren(); $('error').textContent = error.message; $('error').hidden = false; }
    } finally { loading = false; $('refresh').disabled = false; }
  }
  if (view === 'system') load(); return { load };
})();
