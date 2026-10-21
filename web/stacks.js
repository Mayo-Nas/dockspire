'use strict';

window.stackUI = (() => {
  const dialog = $('stack-dialog'), body = $('stack-dialog-body');
  const example = 'services:\n  web:\n    image: nginx:alpine\n    ports:\n      - "127.0.0.1:8080:80"\n    restart: unless-stopped\n';
  const lifecycle = kind => ['start', 'stop', 'restart'].includes(kind);
  const verb = kind => ({ deploy: 'Deploy', recover: 'Recover', takeover: 'Take over', update: 'Update', 'service-update': 'Update service', start: 'Start', stop: 'Stop', restart: 'Restart' }[kind] || 'Operation');
  const managed = record => record.origin === 'new' || record.managed === true;
  let generation = 0;
  let archiveController;
  function cancelArchive() { archiveController?.abort(); archiveController = undefined; }
  function open(title) {
    cancelArchive();
    generation++;
    $('stack-dialog-title').textContent = title; body.replaceChildren();
    if (!dialog.open) dialog.showModal();
    dialog.scrollTop = 0;
  }
  function button(text, action, primary = false) {
    const result = node('button', text, primary ? 'button primary' : 'button');
    result.type = 'button'; result.addEventListener('click', action); return result;
  }
  function field(label, input) {
    const result = node('label', undefined, 'form-field'); result.append(node('span', label), input); return result;
  }
  function message(text, error = false) {
    let result = body.querySelector('.operation-message');
    if (!result) { result = node('div', undefined, 'operation-message notice'); result.setAttribute('role', 'status'); body.append(result); }
    result.classList.toggle('error', error); result.textContent = text; result.scrollIntoView({ block: 'nearest' });
  }
  async function operation(action) {
    const current = generation, buttons = [...body.querySelectorAll('button')].map(item => [item, item.disabled]);
    buttons.forEach(([item]) => item.disabled = true);
    try { await action(); } catch (error) { if (current === generation) message(error.message, true); }
    finally { buttons.forEach(([item, disabled]) => item.disabled = disabled); }
  }
  async function downloadArchive(name) {
    const current = generation;
    if (!session) await api('/api/session');
    if (current !== generation || !dialog.open) return false;
    const controller = new AbortController();
    archiveController = controller;
    try {
      const response = await fetch(name ? `/api/stacks/${encodeURIComponent(name)}/export` : '/api/library/backup', {
        method: 'POST', cache: 'no-store',
        headers: { 'Content-Type': 'application/json', 'X-Stacklane-CSRF': session.csrfToken },
        body: JSON.stringify({ includeSecrets: true }),
        signal: AbortSignal.any([controller.signal, AbortSignal.timeout(175000)]),
      });
      requireSignIn(response);
      if (!response.ok) {
        if (response.status === 403) session = null;
        const error = await response.json();
        throw new Error(error.error || 'Archive download failed. Retry when no container operation is running.');
      }
      if (response.headers.get('Content-Type')?.split(';')[0] !== 'application/zip') throw new Error('The server did not return a ZIP archive.');
      const blob = await response.blob();
      const expectedLength = Number(response.headers.get('Content-Length'));
      if (expectedLength <= 0 || blob.size > 132 * 1024 * 1024 || blob.size !== expectedLength) throw new Error('The archive download was incomplete or too large. Retry the download.');
      if (controller.signal.aborted || current !== generation || !dialog.open) return false;
      const url = URL.createObjectURL(blob), link = node('a');
      link.href = url;
      link.download = name ? `dockspire-${name}-compose.zip` : `dockspire-library-${new Date().toISOString().replace(/[:.]/g, '-')}.zip`;
      document.body.append(link);
      try { link.click(); } finally { link.remove(); setTimeout(() => URL.revokeObjectURL(url), 1000); }
      return true;
    } finally { if (archiveController === controller) archiveController = undefined; }
  }
  function archiveDialog(name = '') {
    open(name ? `Export ${name}` : 'Back up stack library');
    body.append(node('p', 'This ZIP contains plaintext Compose configuration and environment values, including secrets. Store it privately and share it only with people who should have those values.', 'notice'));
    body.append(node('p', name
      ? 'Exports this stack’s selected compose.yaml, .env and stack.env files when present, plus its saved metadata. Previous revisions and operation history are excluded. This is a portable Compose export, not a full library restore archive.'
      : 'Includes saved stack records, initial files, every retained configuration revision, and retained operation history. Only definitions already saved in Dockspire are included.', 'muted'));
    body.append(node('p', 'Container volumes, bind-mounted application data, images, Docker credentials, and external manager files are not included. Back up workload data separately.', 'muted'));
    body.append(node('p', 'Dockspire stays running. Wait for container operations or another archive download to finish before downloading. Restore remains an offline command; nothing is deployed or changed by this download.', 'muted'));
    body.append(button(name ? 'Download Compose export' : 'Download library backup', () => operation(async () => {
      message('Preparing a private snapshot… Closing this dialog cancels the download.');
      if (await downloadArchive(name)) message('Archive handed to your browser. Check Downloads and keep the ZIP private.');
    }), true));
  }
  function definition(record) {
    body.append(node('p', `${record.services.length} services · saved previews hide environment values`, 'muted'));
    const services = node('ul', undefined, 'service-list');
    record.services.forEach(item => services.append(node('li', `${item.name} — ${item.image || 'image defined elsewhere'}`))); body.append(services);
    const editor = node('textarea', undefined, 'compose-editor'); editor.value = record.compose; editor.readOnly = true; editor.spellcheck = false;
    body.append(field('Compose definition · environment values hidden', editor));
    body.append(node('p', `Environment variable names: ${(record.environmentNames || []).join(', ') || 'none in associated files'}`, 'muted'));
    if (record.sourceDirectory) body.append(node('p', `Original project directory: ${record.sourceDirectory}`, 'muted'));
    const warnings = node('ul', undefined, 'warnings'); (record.warnings || []).forEach(item => warnings.append(node('li', item))); body.append(warnings);
  }
  function upload(label, target, accept, afterLoad) {
    const input = node('input'); input.type = 'file'; input.accept = accept;
    input.addEventListener('change', () => operation(async () => {
      const file = input.files[0]; if (!file) return;
      if (file.size > 512 * 1024) throw new Error('Choose a text file smaller than 512 KiB.');
      const current = generation, text = await file.text();
      if (current !== generation || !dialog.open) return;
      target.value = text; if (afterLoad) afterLoad();
      message(`${file.name} loaded into the editor. Validate and save when ready.`); input.value = '';
    })); return field(label, input);
  }
  function editorForm(record, importSource = '') {
    const editing = Boolean(record); open(editing ? `Edit ${record.name}` : importSource ? `Import from ${importSource}` : 'Add a Compose stack');
    if (importSource) {
      body.append(button('Back to import sources', () => importStack()));
      body.append(node('p', `Load or paste a standard Compose file from ${importSource}, with an optional environment file. This creates a new draft; it does not adopt an existing stack or change running containers.`, 'notice'));
      body.append(node('p', 'Choose an unused stack name. Review ports, host paths, volumes, and required variables before any later deployment. Manager backup archives and proprietary formats are not supported.', 'muted'));
    }
    const name = node('input'); name.placeholder = 'my-stack'; name.maxLength = 63; name.autocomplete = 'off'; name.value = record?.name || ''; name.readOnly = editing;
    const compose = node('textarea', undefined, 'compose-editor'); compose.value = record?.compose || (importSource ? '' : example); compose.placeholder = 'Load a Compose file above or paste its YAML here.'; compose.spellcheck = false;
    const environment = node('textarea', undefined, 'environment-editor'); environment.placeholder = 'EXAMPLE=value'; environment.spellcheck = false;
    const replace = node('input'); replace.type = 'checkbox';
    const optional = node('details'); optional.append(node('summary', editing ? 'Environment files · keep existing values by default' : 'Optional environment variables'));
    if (editing) {
      const label = node('label', undefined, 'check-field'); label.append(replace, node('span', 'Replace all saved environment file values')); optional.append(label);
      environment.disabled = true; replace.addEventListener('change', () => { environment.disabled = !replace.checked; });
    }
    optional.append(upload('Load an environment file', environment, '.env,.txt', () => { replace.checked = true; environment.disabled = false; }));
    optional.append(field('One NAME=value entry per line', environment), node('p', 'Values stay in private files on the VM. Use ${NAME} in Compose, or env_file: stack.env.', 'muted'));
    body.append(field('Stack name', name), upload('Load a Compose file', compose, '.yaml,.yml'), field('Compose YAML', compose), optional);
    if (editing) body.append(node('p', 'Leave [hidden] values unchanged to preserve them. Saving creates a configuration revision; it does not change running containers.', 'notice'));
    body.append(node('p', 'Use images, named volumes, and absolute Linux host paths. Builds and additional file attachments are not supported yet.', 'muted'));
    const actions = node('div', undefined, 'form-actions');
    const input = () => editing ? { compose: compose.value, environment: environment.value, replaceEnvironment: replace.checked, revision: record.fingerprint } : { name: name.value.trim(), compose: compose.value, environment: environment.value };
    const base = editing ? `/api/stacks/${encodeURIComponent(record.name)}` : '/api/stacks';
    actions.append(button('Validate Compose', () => operation(async () => {
      const current = generation;
      const result = await api(editing ? `${base}/validate-edit` : `${base}/validate`, 'POST', input());
      if (current !== generation || !dialog.open) return;
      message(`${result.services.length} service(s) validated${snapshot?.mode === 'demo' ? ' for YAML structure; live Compose validation runs on the VM' : ' with Docker Compose'}. No containers changed.`);
    })), button('Save and review', () => operation(async () => {
      const current = generation;
      const result = await api(editing ? `${base}/edit` : base, 'POST', input());
      environment.value = ''; compose.value = ''; await refresh();
      if (current !== generation || !dialog.open) return;
      await showSaved(result.name);
    }), true)); body.append(actions); (editing ? compose : name).focus();
  }
  async function watchJob(initial) {
    open(`${verb(initial.kind)} ${initial.containerName || initial.name}`); const current = generation;
    body.append(node('p', 'This operation continues if you close this window. Reopen it from the stack’s operation history.', 'muted'));
    if (initial.targets?.length) body.append(node('p', `Targeted containers: ${initial.targets.join(', ')}`, 'muted'));
    const steps = node('ol', undefined, 'job-steps');
    const stages = initial.kind === 'takeover'
      ? [['queued', 'Queued'], ['validating', 'Recheck source and resource identities'], ['applying', 'Recreate with existing images'], ['verifying', 'Verify ownership and preserved resources'], ['recording', 'Save management state']]
      : lifecycle(initial.kind)
      ? [['queued', 'Queued'], ['validating', 'Recheck ownership and scope'], ['applying', `${verb(initial.kind)} reviewed containers`], ['verifying', 'Verify container state'], ['recording', 'Save result']]
      : [['queued', 'Queued'], ['validating', 'Validate configuration'], ['pulling', 'Download images'], ['applying', 'Apply changes and check health'], ['recording', 'Save result']];
    stages.forEach(([, label]) => steps.append(node('li', label))); body.append(steps);
    const status = node('div', undefined, 'notice'); status.setAttribute('role', 'status'); body.append(status);
    const images = node('section'); body.append(images);
    const actions = node('div', undefined, 'form-actions'); body.append(actions);
    async function poll(job) {
      if (current !== generation || !dialog.open) return;
      const index = stages.findIndex(([stage]) => stage === job.stage);
      [...steps.children].forEach((step, i) => { step.classList.toggle('complete', job.status === 'succeeded' || i < index); step.classList.toggle('active', job.status === 'running' && i === index); });
      status.textContent = `${job.status.toUpperCase()} · ${job.message}${job.total ? ` · ${job.completed || 0}/${job.total} verified` : ''}`; status.classList.toggle('error', ['failed', 'interrupted'].includes(job.status));
      images.replaceChildren();
      if (job.images) {
        images.append(node('h3', 'Observed images'), node('p', 'Docker image IDs observed immediately before and after Compose apply. Image sets are compared per service; tags can change later.', 'muted'));
        if (job.images.warning) images.append(node('p', job.images.warning, 'notice'));
        const wrap = node('div', undefined, 'configuration-changes'), table = node('table'), head = node('tr');
        table.append(node('caption', 'Observed image changes'));
        ['Service', 'Image result', 'Before apply', 'After apply'].forEach(text => head.append(node('th', text))); table.append(head);
        const labels = { changed: 'Changed', unchanged: 'Image set unchanged', added: 'Newly observed', removed: 'No longer observed', unavailable: 'Unavailable' };
        job.images.services.forEach(item => {
          const row = node('tr');
          row.append(node('th', item.service), node('td', labels[item.status] || 'Unavailable'), node('td', item.beforeComplete ? item.before.join(', ') || 'No containers observed' : 'Unavailable'), node('td', item.afterComplete ? item.after.join(', ') || 'No containers observed' : 'Unavailable'));
          table.append(row);
        });
        wrap.append(table); images.append(wrap);
      } else if (job.kind === 'takeover' && job.status === 'succeeded') images.append(node('p', 'Existing image identities were verified; no images were downloaded.', 'muted'));
      else if (job.kind !== 'takeover' && !lifecycle(job.kind) && job.status !== 'running') images.append(node('p', 'Image IDs were not recorded for this operation.', 'muted'));
      if (job.status !== 'running') { actions.replaceChildren(button('View stack', () => operation(() => showSaved(job.name)), true)); await refresh(); return; }
      setTimeout(async () => {
        if (current !== generation || !dialog.open) return;
        try { await poll(await api(`/api/stacks/${encodeURIComponent(job.name)}/operations/${job.id}`)); }
        catch (error) { if (current === generation) { status.textContent = `Progress connection lost. The operation may still be running. ${error.message}`; actions.replaceChildren(button('Reconnect', () => watchJob(job))); } }
      }, 1000);
    } await poll(initial);
  }
  function revisionComparison(comparison, historical = false) {
    body.append(node('h3', 'Configuration changes'));
    if (!comparison || comparison.status === 'unavailable') {
      body.append(node('p', comparison?.warning || 'Configuration comparison is unavailable. Inspect the saved configuration before continuing.', 'notice'));
      return;
    }
    body.append(node('p', comparison.status === 'initial'
      ? 'No successful whole-stack deployment is recorded. Saved configuration is shown as additions; existing containers may reflect a partial attempt.'
      : historical ? 'Selected saved configuration → Historical revision' : 'Last successfully applied configuration → Saved configuration', 'muted'));
    if (!comparison.changes.length) {
      body.append(node('p', comparison.sourceOnly
        ? 'Only comments, formatting, or environment-file presentation changed. No parsed settings differ.'
        : historical ? 'No saved configuration settings differ.' : 'No saved configuration changes. An update may still pull newer images for unchanged tags.', 'notice'));
    } else {
      const wrap = node('div', undefined, 'configuration-changes');
      const table = node('table');
      table.append(node('caption', historical ? 'Selected versus historical configuration' : 'Saved versus last applied configuration'));
      const header = node('thead'), headings = node('tr');
      ['Setting', 'Change', historical ? 'Selected' : 'Last applied', historical ? 'Historical' : 'Saved'].forEach(text => headings.append(node('th', text)));
      header.append(headings); table.append(header);
      const rows = node('tbody');
      comparison.changes.forEach(change => {
        const row = node('tr', undefined, `configuration-change ${change.kind}`);
        row.append(node('th', `${change.scope} · ${change.field}`), node('td', change.kind));
        const value = (side, absent) => change[side] || (absent ? '—' : change.field === 'Service' || change.field === 'File' ? 'Present' : 'Details hidden');
        row.append(node('td', value('before', change.kind === 'added')), node('td', value('after', change.kind === 'removed')));
        rows.append(row);
      });
      table.append(rows); wrap.append(table); body.append(wrap);
    }
    if (comparison.truncated) body.append(node('p', 'The first 200 changes are shown. Inspect the full saved configuration before continuing.', 'notice'));
    body.append(node('p', 'Environment values stay hidden; variable additions, removals, and value changes are listed by name. Other sensitive settings show change status only. This compares saved inputs before interpolation, not live container drift or image contents.', 'muted'));
  }
  async function configurationHistory(name, managed, notice = '') {
    open(`${name} · configuration history`);
    const current = generation, path = `/api/stacks/${encodeURIComponent(name)}/revisions`;
    await operation(async () => {
      const page = await api(`${path}?limit=10`);
      if (current !== generation || !dialog.open) return;
      body.append(button('Back to stack', () => operation(() => showSaved(name))));
      body.append(node('p', 'Each entry is a unique saved configuration. Storage dates describe when bundles were written, not every save or restore. Environment values stay hidden.', 'muted'));
      const list = node('div', undefined, 'job-history'), pager = node('div', undefined, 'form-actions');
      function appendPage(page) {
        page.revisions.forEach(row => {
          const badges = [row.initial && 'Initial', row.selected && 'Selected', row.applied && 'Last applied', row.recovery && 'Deployment attempt'].filter(Boolean).join(' · ');
          list.append(button(`${row.revision.slice(0, 12)} · ${new Date(row.storedAt).toLocaleString()}${badges ? ` · ${badges}` : ''}`, () => configurationRevision(name, row.revision, managed)));
        });
        pager.replaceChildren();
        if (page.nextCursor) pager.append(button('Load older configurations', () => operation(async () => {
          const next = await api(`${path}?limit=10&cursor=${encodeURIComponent(page.nextCursor)}`);
          if (current !== generation || !dialog.open) return;
          appendPage(next);
        })));
      }
      appendPage(page); body.append(list, pager);
      if (managed) body.append(button('Review revision cleanup', () => configurationCleanup(name)));
      if (notice) message(notice);
    });
  }
  async function configurationRevision(name, revision, managed) {
    open(`${name} · revision ${revision.slice(0, 12)}`);
    const current = generation, path = `/api/stacks/${encodeURIComponent(name)}/revisions/${encodeURIComponent(revision)}`;
    await operation(async () => {
      const preview = await api(path);
      if (current !== generation || !dialog.open) return;
      body.append(button('Back to configuration history', () => configurationHistory(name, managed)));
      revisionComparison(preview.comparison, true);
      const compose = node('textarea', undefined, 'compose-editor'); compose.value = preview.compose; compose.readOnly = true; compose.spellcheck = false;
      body.append(field('Historical Compose · environment values hidden', compose));
      body.append(node('p', 'Restoring selects these exact Compose and environment files as the saved draft. It replaces any unapplied saved edits. Running containers, applied state, and workload data stay unchanged. Review a deployment or update separately to apply this draft.', 'notice'));
      if (!preview.canRestore) { body.append(node('p', 'Restoring is available only for an idle Dockspire-managed stack.', 'muted')); return; }
      const confirmation = node('input'); confirmation.placeholder = name;
      const restore = button('Restore as draft', () => operation(async () => {
        await api(`${path}/restore`, 'POST', { recordVersion: preview.recordVersion, confirmation: confirmation.value });
        if (current !== generation || !dialog.open) return;
        await refresh();
        if (current !== generation || !dialog.open) return;
        await showSaved(name, 'Historical configuration restored as the saved draft. Running containers were unchanged.');
      }), true);
      restore.disabled = true; confirmation.addEventListener('input', () => { restore.disabled = confirmation.value !== name; });
      body.append(field(`Type ${name} to restore this draft`, confirmation), restore);
    });
  }
  async function configurationCleanup(name) {
    open(`${name} · revision cleanup`);
    const current = generation, path = `/api/stacks/${encodeURIComponent(name)}/revisions/cleanup`;
    await operation(async () => {
      const plan = await api(path);
      if (current !== generation || !dialog.open) return;
      body.append(button('Back to configuration history', () => configurationHistory(name, true)));
      body.append(node('p', `Keeps the ${plan.keep} most recently stored configurations plus the initial, selected, applied, recovery, and retained-operation versions. Cleanup is explicit; saving never automatically deletes configurations.`, 'muted'));
      body.append(node('p', `${plan.remove.length} old configurations will be removed; ${plan.retained} will remain. Protected versions can exceed the retention target.`, 'notice'));
      if (plan.eligible > plan.remove.length) body.append(node('p', 'Cleanup handles at most 100 versions per review. Review again to remove more eligible versions.', 'muted'));
      if (!plan.remove.length) return;
      body.append(node('p', 'Removed configurations cannot be restored from this library. Download a full library backup first if you want to keep them. Containers and workload data are unaffected.', 'notice'));
      const candidates = node('ul', undefined, 'service-list'); plan.remove.forEach(revision => candidates.append(node('li', revision))); body.append(candidates);
      const confirmation = node('input'); confirmation.placeholder = name;
      const clean = button('Remove reviewed configurations', () => operation(async () => {
        const result = await api(path, 'POST', { token: plan.token, confirmation: confirmation.value });
        if (current !== generation || !dialog.open) return;
        await configurationHistory(name, true, `${result.removed || 0} old configurations removed.`);
      }));
      clean.className += ' danger';
      clean.disabled = true; confirmation.addEventListener('input', () => { clean.disabled = confirmation.value !== name; });
      body.append(field(`Type ${name} to confirm cleanup`, confirmation), clean);
    });
  }
  async function review(name, containerId = '', action = '') {
    const current = generation;
    const plan = await api(`/api/stacks/${encodeURIComponent(name)}/review`, 'POST', { containerId, action });
    if (current !== generation || !dialog.open) return;
    open(`${verb(plan.kind)} ${plan.confirmation || name} · review`);
    if (plan.kind === 'takeover') { takeoverReview(plan); return; }
    if (lifecycle(plan.kind)) { lifecycleReview(plan); return; }
    const services = node('ul', undefined, 'service-list'); plan.services.forEach(item => services.append(node('li', `${item.name} — ${item.image}`))); body.append(services);
    if (plan.targets.length) body.append(node('p', `Current containers: ${plan.targets.join(', ')}`, 'muted'));
    if (plan.kind === 'recover') body.append(node('p', 'The previous deployment failed, was interrupted, or has an unavailable result. Inspect existing containers and persistent data first. Recovery applies the saved configuration to this managed project, keeps existing images when available, and downloads missing images. Services may be recreated or started. It does not remove the stack or roll back application data.', 'notice'));
    else body.append(node('p', plan.kind === 'deploy' ? 'Creates the declared containers, networks, and volumes. Review host paths and published ports in the saved definition first.' : 'Pulls the images named above and applies the saved configuration. Changed services may restart with brief downtime. Named volumes and host bind data are retained. Application upgrades may still change stored data.', 'notice'));
    if (plan.service) body.append(node('p', `Updates only Compose service “${plan.service}”, including all its replicas. Dependencies are not updated.`, 'muted'));
    else body.append(node('p', 'Applies all enabled services. Containers removed from the definition are left in place for manual review.', 'muted'));
    revisionComparison(plan.comparison);
    body.append(node('p', 'Image tags stay as written. To move from a pinned version to a newer release, edit the image tag first. This operation has a five-minute limit; automatic rollback is not available.', 'muted'));
    body.append(node('p', 'Use named volumes or host binds for persistent data. Anonymous volumes are not guaranteed to be reused after configuration changes.', 'muted'));
    const confirmation = node('input'); confirmation.placeholder = name; body.append(field(`Type ${name} to confirm`, confirmation));
    body.append(button(plan.kind === 'recover' ? 'Recover stack' : plan.kind === 'deploy' ? 'Deploy stack' : 'Update now', () => operation(async () => {
      const current = generation;
      const job = await api(`/api/stacks/${encodeURIComponent(name)}/operations`, 'POST', { planId: plan.id, confirmation: confirmation.value });
      if (current !== generation || !dialog.open) return;
      await watchJob(job);
    }), true));
  }
  function takeoverReview(plan) {
    body.append(node('p', 'Recreates every listed service under the same Compose project and container names. There will be downtime; changes stored only in container writable layers are lost. Back up mounted application data first.', 'notice'));
    const targets = node('ul', undefined, 'service-list'); plan.targets.forEach(name => targets.append(node('li', name))); body.append(targets);
    body.append(node('p', 'Existing image IDs, named volumes, networks and host bind paths are retained. Images become pinned locally: edit their references explicitly before future registry upgrades. Unsupported or mismatched dependencies block takeover.', 'muted'));
    body.append(node('p', 'Disable Portainer GitOps, webhooks and every other writer before continuing. Portainer can still display and control this project: Dockspire cannot revoke its rights. Do not delete the Portainer stack to hand it off; deletion can remove the running workload. Do not manage it in both tools.', 'notice'));
    body.append(node('p', 'If an attempt was interrupted, this reapplies only its recorded configuration to the captured original containers or verified Dockspire replacements. Inspect the workload first. No automatic rollback or volume deletion is performed.', 'muted'));
    const writers = node('input'); writers.type = 'checkbox';
    body.append(field('I backed up the data and disabled Portainer automation, webhooks and all other writers. I accept recreation and downtime.', writers));
    const confirmation = node('input'); confirmation.placeholder = plan.name; body.append(field(`Type ${plan.name} to confirm takeover`, confirmation));
    const submit = button('Take over and redeploy', () => operation(async () => {
      const current = generation;
      const job = await api(`/api/stacks/${encodeURIComponent(plan.name)}/operations`, 'POST', { planId: plan.id, confirmation: confirmation.value, portainerWritersDisabled: writers.checked });
      if (current !== generation || !dialog.open) return;
      await watchJob(job);
    }), true);
    const validate = () => { submit.disabled = !writers.checked || confirmation.value !== plan.name; };
    writers.addEventListener('change', validate); confirmation.addEventListener('input', validate); validate(); body.append(submit);
  }
  function lifecycleReview(plan) {
    body.append(node('p', `${plan.containerId ? 'One container' : 'Existing containers in this stack'} · ${plan.containers.length} target(s)`, 'muted'));
    const targets = node('ul', undefined, 'service-list');
    plan.containers.forEach(item => targets.append(node('li', `${item.name} — ${item.state}`))); body.append(targets);
    const explanation = {
      start: 'Starts the stopped containers listed above. Missing services require a separate deployment or stack update.',
      stop: 'Stops the running containers listed above. Their services will be unavailable until started again.',
      restart: 'Restarts the running containers listed above with brief downtime. Stopped containers stay stopped.',
    };
    body.append(node('p', explanation[plan.kind], 'notice'));
    if (plan.containerId) body.append(node('p', 'This action targets this exact container. Other replicas of its service are not included.', 'muted'));
    if (plan.kind !== 'start') body.append(node('p', 'Docker allows up to 30 seconds for graceful shutdown before forcing the container to stop.', 'muted'));
    body.append(node('p', 'These controls keep the existing containers, images, and storage. Saved configuration edits are not applied. Containers are processed in the listed order; dependency readiness and health checks are not coordinated.', 'muted'));
    body.append(node('p', 'The review expires after five minutes. A partial failure stops further actions and is recorded in the stack’s history.', 'muted'));
    const confirmation = node('input'); confirmation.placeholder = plan.confirmation;
    body.append(field(`Type ${plan.confirmation} to confirm`, confirmation));
    const submit = button(`${verb(plan.kind)} ${plan.containerId ? 'container' : 'stack'}`, () => operation(async () => {
      const current = generation;
      const job = await api(`/api/stacks/${encodeURIComponent(plan.name)}/operations`, 'POST', { planId: plan.id, confirmation: confirmation.value });
      if (current !== generation || !dialog.open) return;
      await watchJob(job);
    }), true);
    if (plan.kind !== 'start') submit.className = 'button warning';
    submit.disabled = true; confirmation.addEventListener('input', () => { submit.disabled = confirmation.value !== plan.confirmation; }); body.append(submit);
  }
  async function showSaved(name, notice = '') {
    const current = generation;
    const [record, historyResult] = await Promise.all([api(`/api/stacks/${encodeURIComponent(name)}`), api(`/api/stacks/${encodeURIComponent(name)}/operations?details=1&limit=10`)]);
    if (current !== generation) return;
    const history = historyResult.jobs; open(name); body.append(window.monitorUI.stackServices(name));
    historyResult.warnings.forEach(warning => body.append(node('p', warning, 'notice')));
    if (!managed(record)) {
      body.append(node('p', record.takeoverStatus === 'pending' ? 'Takeover is incomplete. Inspect existing containers and data before reviewing takeover recovery. Editing and lifecycle controls stay locked until ownership is verified.' : 'Imported configuration copy · Portainer still manages the workload. Takeover is a separate explicit operation; importing alone never changes containers.', 'notice'));
      const active = history.find(job => job.status === 'running');
      if (active) body.append(button('View active operation', () => watchJob(active), true));
      else if (session.deployEnabled) body.append(button(record.takeoverStatus === 'pending' ? 'Review takeover recovery' : 'Review takeover', () => operation(() => review(name, '', 'takeover')), true));
    }
    else {
      const active = history.find(job => job.status === 'running'), pending = record.deployedAt && record.appliedRevision !== record.fingerprint;
      body.append(node('p', active ? 'A container operation is in progress. Wait for its result before making changes.' : record.recoveryRequired ? 'A previous deployment needs inspection. Existing containers may have changed. Review whole-stack recovery or use the reviewed controls for existing containers.' : record.deployedAt ? (pending ? 'Saved configuration has unapplied changes. Review and update the whole stack to apply them.' : `Last successful deployment: ${new Date(record.deployedAt).toLocaleString()}`) : 'Saved draft. No containers have been deployed by this draft.', 'notice'));
      const actions = node('div', undefined, 'form-actions');
      if (active) actions.append(button('View active operation', () => watchJob(active), true));
      else { actions.append(button('Edit configuration', () => editorForm(record))); if (session.deployEnabled) actions.append(button(record.recoveryRequired ? 'Review stack recovery' : record.deployedAt ? 'Review stack update' : 'Review deployment', () => operation(() => review(name)), true)); else body.append(node('p', 'Container operations are disabled in demo mode.', 'muted')); }
      body.append(actions);
      if (!active && (record.deployedAt || record.lastDeploymentAttempt) && session.deployEnabled) {
        const containers = (snapshot?.containers || []).filter(item => item.stack === name);
        const controls = node('div', undefined, 'form-actions lifecycle-controls');
        ['start', 'stop', 'restart'].forEach(action => {
          const control = button(`${verb(action)} stack…`, () => operation(() => review(name, '', action)));
          control.disabled = !containers.some(item => action === 'start' ? ['created', 'exited'].includes(item.state) : item.state === 'running'); controls.append(control);
        });
        body.append(node('h3', 'Start, stop & restart'), node('p', 'Operate on existing containers without deploying saved edits. Each action opens a review.', 'muted'), controls);
      }
    }
    body.append(button('Configuration history', () => configurationHistory(name, managed(record))), button('Export Compose', () => archiveDialog(name)));
    if (history.length || historyResult.nextCursor) {
      body.append(node('h3', 'Recent operations')); const list = node('div', undefined, 'job-history');
      const pager = node('div', undefined, 'form-actions');
      function appendHistory(page) {
        page.jobs.forEach(job => list.append(button(`${job.status} · ${job.kind}${job.containerName ? ` · ${job.containerName}` : job.service ? ` · ${job.service}` : ''} · ${new Date(job.startedAt).toLocaleString()}`, () => watchJob(job))));
        pager.replaceChildren();
        if (page.nextCursor) pager.append(button('Load older operations', () => operation(async () => {
          const current = generation;
          const next = await api(`/api/stacks/${encodeURIComponent(name)}/operations?details=1&limit=10&cursor=${encodeURIComponent(page.nextCursor)}`);
          if (generation !== current || !dialog.open) return;
          appendHistory(next); next.warnings.forEach(warning => message(warning));
        })));
      }
      appendHistory(historyResult); body.append(list, pager);
    }
    body.append(node('h3', 'Saved configuration')); definition(record);
    if (notice) message(notice);
  }
  async function openContainer(item, initial = 'overview') {
    const stack = snapshot.stacks.find(stack => stack.name === item.stack && stack.definition === 'Saved' && managed(stack));
    if (dialog.open) dialog.close();
    window.monitorUI.open(item, stack && item.service && session.deployEnabled ? () => {
      open(`Review ${item.service}`); operation(() => review(item.stack, item.id));
    } : null, initial, stack && session.deployEnabled ? action => {
      open(`${verb(action)} ${item.name}`); operation(() => review(item.stack, item.id, action));
    } : null);
  }
  function importStack(selected) {
    open('Import a stack');
    body.append(node('p', 'Choose where your stack configuration comes from.', 'muted'));
    body.append(node('p', 'Import copies configuration. Existing containers and their manager remain unchanged; Portainer takeover requires a separate explicit review after importing.', 'notice'));
    const sources = node('div', undefined, 'import-sources');
    [
      ['Portainer', session?.importEnabled ? 'Direct source connected' : 'Compose file upload available', 'Load an exported Compose file as a new draft, or copy a discovered same-host definition when direct source access is configured. Portainer keeps managing its containers.', () => importPortainer(selected)],
      ['Dockge', 'Compose file → new draft', 'Load or paste the stack’s compose.yaml and optionally its environment file.', () => editorForm(null, 'Dockge')],
      ['Docker Compose', 'Compose file → new draft', 'Load compose.yaml or docker-compose.yml from a manually managed project.', () => editorForm(null, 'Docker Compose')],
      ['Other manager', 'Compose file → new draft', 'Use a standard Docker Compose file from another manager. Proprietary backup formats are not supported.', () => editorForm(null, 'another manager')],
    ].forEach(([name, availability, description, action]) => {
      const card = node('section', undefined, 'import-source');
      card.append(node('h3', name), node('p', availability, 'import-availability'), node('p', description, 'muted'), button(`Choose ${name}`, action));
      sources.append(card);
    });
    body.append(sources);
  }
  function importPortainer(selected) {
    open('Import from Portainer');
    body.append(button('Back to import sources', () => importStack()));
    body.append(button('Load exported Compose file', () => editorForm(null, 'Portainer')));
    body.append(node('p', 'A file import creates a new draft with an unused stack name. It does not adopt, deploy or stop existing workloads.', 'muted'));
    if (!session?.importEnabled) { body.append(node('p', 'Direct Portainer source access is not connected. File upload above works without it. For same-host discovery, enable the optional compose.portainer.yaml configuration described in the installation guide, with an authorized read-only mount of only Portainer’s compose directory. Opening this screen does not read Portainer files.', 'notice')); return; }
    body.append(node('p', 'Import saves a configuration copy. Portainer keeps managing the stack until you choose Review takeover and confirm Take over and redeploy.', 'notice'));
    const environmentNotice = session.environmentImportEnabled ? 'Associated .env and stack.env files will be copied, when present, into Dockspire’s private library. Exported backups can contain those values.' : 'Associated .env and stack.env files are excluded. An operator must explicitly enable environment-file import to include them.';
    body.append(node('p', environmentNotice, 'muted'));
    const choices = (snapshot?.stacks || []).filter(item => item.importId && item.definition !== 'Saved');
    if (!choices.length) { body.append(node('p', 'No supported, unimported Portainer Compose stacks were found. Discovery requires existing containers with a /data/compose/<numeric-id>/docker-compose.yml or .yaml source label. Swarm stacks and stacks without containers are not supported.', 'notice')); return; }
    const select = node('select'); choices.forEach(item => { const option = node('option', item.name); option.value = item.name; select.append(option); }); if (selected) select.value = selected;
    body.append(field('Existing Portainer stack', select));
    body.append(button('Preview import', () => operation(async () => {
      const current = generation, name = select.value;
      const preview = await api(`/api/stacks/${encodeURIComponent(name)}/preview-import`, 'POST');
      if (current !== generation || !dialog.open) return;
      open(`Import ${name}`); definition(preview); body.append(node('p', environmentNotice, 'notice'));
      body.append(button('Import definition · leave containers running', () => operation(async () => { const current = generation; await api(`/api/stacks/${encodeURIComponent(name)}/import`, 'POST', { fingerprint: preview.fingerprint }); await refresh(); if (current !== generation || !dialog.open) return; await showSaved(name); }), true));
    }), true));
  }
  async function openStack(item) {
    const current = generation;
    try {
      if (item.definition === 'Unavailable') { open(item.name); body.append(window.monitorUI.stackServices(item.name), node('p', 'Saved stack metadata is unavailable. Restore this entry from a backup before changing it. Other stacks remain accessible.', 'notice')); return; }
      if (item.definition === 'Saved') { await showSaved(item.name); return; }
      open(item.name); body.append(window.monitorUI.stackServices(item.name)); body.append(node('p', `${item.running} / ${item.containers} containers running`, 'muted')); body.append(node('p', 'Discovered Docker project. Migration is deferred; this app will not edit or update it.', 'notice'));
    } catch (error) { if (current === generation) { open(item.name); message(error.message, true); } }
  }
  $('new-stack').addEventListener('click', () => editorForm()); $('import-stack').addEventListener('click', () => importStack());
  $('backup-library').addEventListener('click', () => archiveDialog());
  $('close-stack-dialog').addEventListener('click', () => dialog.close()); dialog.addEventListener('close', () => { generation++; cancelArchive(); });
  return { open: openStack, container: openContainer };
})();
