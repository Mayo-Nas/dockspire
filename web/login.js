'use strict';

const form = document.getElementById('login-form');
const submit = document.getElementById('sign-in');
const errorMessage = document.getElementById('login-error');
let submitting = false;

form.addEventListener('submit', async event => {
  event.preventDefault();
  if (submitting) return;
  submitting = true;
  submit.disabled = true;
  submit.setAttribute('aria-busy', 'true');
  errorMessage.hidden = true;
  try {
    const response = await fetch('/api/auth/login', {
      method: 'POST', cache: 'no-store', credentials: 'same-origin',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ username: document.getElementById('username').value, password: document.getElementById('password').value }),
      signal: AbortSignal.timeout(25000),
    });
    const result = await response.json();
    if (!response.ok) throw new Error(result.error || 'Could not sign in.');
    document.getElementById('password').value = '';
    location.replace('/');
  } catch (error) {
    errorMessage.textContent = error.name === 'TimeoutError' ? 'Sign-in timed out. Try again.' : error.message;
    errorMessage.hidden = false;
  } finally {
    submitting = false;
    submit.disabled = false;
    submit.removeAttribute('aria-busy');
  }
});
