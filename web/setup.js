'use strict';

const form = document.getElementById('setup-form');
const submit = document.getElementById('create-admin');
const errorMessage = document.getElementById('setup-error');
let submitting = false;
let ready = false;

function showError(message) {
  errorMessage.textContent = message;
  errorMessage.hidden = false;
}

async function setupStatus() {
  try {
    const response = await fetch('/api/auth/setup', { cache: 'no-store', credentials: 'same-origin', signal: AbortSignal.timeout(10000) });
    if (!response.ok) throw new Error('Could not check setup. Reload the page to retry.');
    const status = await response.json();
    if (!status.required) { location.replace('/'); return; }
    if (status.expired) throw new Error('Setup timed out. Restart Dockspire to open a new five-minute setup window.');
    ready = true;
    submit.disabled = false;
  } catch (error) { showError(error.message); }
}

form.addEventListener('submit', async event => {
  event.preventDefault();
  if (submitting || !ready) return;
  const password = document.getElementById('password');
  const confirmation = document.getElementById('confirmation');
  if (password.value !== confirmation.value) { showError('Passwords must match.'); return; }
  submitting = true;
  submit.disabled = true;
  submit.setAttribute('aria-busy', 'true');
  errorMessage.hidden = true;
  try {
    const response = await fetch('/api/auth/setup', {
      method: 'POST', cache: 'no-store', credentials: 'same-origin',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ password: password.value, confirmation: confirmation.value }),
      signal: AbortSignal.timeout(25000),
    });
    const result = await response.json();
    if (!response.ok) {
      if (response.status === 409) { location.replace('/'); return; }
      if (response.status === 403) ready = false;
      throw new Error(result.error || 'Could not create administrator.');
    }
    password.value = '';
    confirmation.value = '';
    location.replace(result.passwordless ? '/' : '/login');
  } catch (error) {
    showError(error.name === 'TimeoutError' ? 'Setup request timed out. Reload to check whether setup completed.' : error.message);
  } finally {
    submitting = false;
    submit.disabled = !ready;
    submit.removeAttribute('aria-busy');
  }
});

setupStatus();
