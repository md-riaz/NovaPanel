// Keep a key and payload in memory for the lifetime of an uncertain submission.
const databaseSubmissions = new WeakMap();
function databaseForm(form) {
  return form && form.matches && form.matches('form[hx-post^="/databases/"]');
}
document.addEventListener('htmx:configRequest', function (event) {
  const form = event.detail.elt;
  if (databaseForm(form)) {
    const parameters = new URLSearchParams(event.detail.parameters);
    parameters.delete('csrf_token');
    const payload = parameters.toString();
    let submission = databaseSubmissions.get(form);
    if (submission && Date.now() - submission.created >= 23 * 60 * 60 * 1000) {
      event.preventDefault();
      window.alert('The retry window has expired. Contact an administrator to check agent RPC job.list and database inventory before resubmitting. If the operation completed but its password claim expired, rotate the password.');
      return;
    }
    if (submission && submission.payload !== payload) {
      event.preventDefault();
      window.alert('The previous submission may have been accepted. If the session has not changed, restore the original values to retry. Otherwise, contact an administrator to check agent RPC job.list and database inventory before resubmitting. Signing in or reauthenticating changes the session.');
      return;
    }
    if (!submission) {
      submission = {key: crypto.randomUUID(), payload: payload, created: Date.now()};
      databaseSubmissions.set(form, submission);
    }
    event.detail.headers['Idempotency-Key'] = submission.key;
  }
  const main = document.getElementById('main');
  if (main && main.dataset.csrf) event.detail.headers['X-CSRF-Token'] = main.dataset.csrf;
});
function renderEnrollmentQR() {
  const target = document.getElementById('enrollment-qr');
  if (!target || !target.dataset.enrollment || typeof qrcode !== 'function') return;
  const qr = qrcode(0, 'M');
  qr.addData(target.dataset.enrollment);
  qr.make();
  const svg = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
  const size = qr.getModuleCount();
  svg.setAttribute('viewBox', '-4 -4 ' + (size + 8) + ' ' + (size + 8));
  svg.setAttribute('role', 'img');
  svg.setAttribute('aria-label', 'Authenticator enrollment QR code');
  const path = document.createElementNS(svg.namespaceURI, 'path');
  let d = '';
  for (let row = 0; row < size; row++) for (let col = 0; col < size; col++) {
    if (qr.isDark(row, col)) d += 'M' + col + ' ' + row + 'h1v1h-1z';
  }
  path.setAttribute('d', d);
  svg.append(path);
  target.replaceChildren(svg);
}
document.addEventListener('DOMContentLoaded', renderEnrollmentQR);
document.addEventListener('htmx:afterSwap', renderEnrollmentQR);

// Error responses include MFA and authentication refusals, never hide them behind HTMX's default.
document.addEventListener('htmx:beforeSwap', function (event) {
  const status = event.detail.xhr.status;
  const source = event.detail.requestConfig && event.detail.requestConfig.elt;
  if (status >= 400 && databaseForm(source)) {
    const submission = databaseSubmissions.get(source);
    if (status === 408 || status >= 500 || (submission && submission.uncertain)) {
      if (submission) submission.uncertain = true;
      event.detail.shouldSwap = false;
      if ((status === 401 || status === 403) && !source.querySelector('[data-recovery-link]')) {
        const link = document.createElement('a');
        link.href = status === 401 ? '/login' : '/reauth';
        link.dataset.recoveryLink = '';
        link.textContent = status === 401 ? 'Sign in' : 'Reauthenticate';
        source.append(link);
      }
      window.alert('The submission may have been accepted. If the session has not changed, retry the same values. Otherwise, contact an administrator to check agent RPC job.list and database inventory before resubmitting. Signing in or reauthenticating changes the session.');
      return;
    }
    databaseSubmissions.delete(source);
  }
  if ((status === 429 || status === 503) && source && source.matches('[data-job-poll]')) {
    const job = source.closest('[data-job-id]');
    if (job && source.isConnected) {
      pollFailures++;
      job.querySelector('[data-poll-feedback]').textContent = pollFailures <= 3
        ? 'Job status temporarily unavailable. Retrying; keep this page open.'
        : 'Automatic retries stopped. Try again and keep this page open.';
      job.querySelector('[data-poll-retry]').hidden = pollFailures <= 3;
      if (pollFailures <= 3) pollJob();
      return;
    }
  }
  if (status >= 400) {
    event.detail.shouldSwap = true;
    event.detail.isError = false;
  }
});
function uncertainDatabaseReply(event) {
  const form = event.detail.elt;
  if (databaseForm(form)) {
    const submission = databaseSubmissions.get(form);
    if (submission) submission.uncertain = true;
    window.alert('The submission reply was lost. The operation may or may not have been admitted. If the session has not changed, retrying the same values may recover an existing job and its claim if still valid; the claim may have expired or been consumed. Contact an administrator to check agent RPC job.list and database inventory before any other resubmission. Signing in or reauthenticating changes the session; do not retry blindly after that.');
  }
}
document.addEventListener('htmx:sendError', uncertainDatabaseReply);
document.addEventListener('htmx:timeout', uncertainDatabaseReply);
document.addEventListener('htmx:beforeSend', function (event) {
  const form = event.detail.elt;
  if (form && form.matches('[data-secret-form]')) {
    form.querySelectorAll('textarea').forEach(function (field) { field.value = ''; });
  }
});
document.addEventListener('submit', function (event) {
  const form = event.target;
  if (form.dataset.confirm && !window.confirm(form.dataset.confirm)) {
    event.preventDefault();
    event.stopImmediatePropagation();
  }
}, true);
// Claim tokens live only in this page's JavaScript memory, never in storage or URLs.
let pendingClaim;
document.addEventListener('htmx:beforeSwap', function (event) {
  const token = event.detail.xhr.getResponseHeader && event.detail.xhr.getResponseHeader('X-Job-Claim');
  if (token) pendingClaim = { token: token };
});
async function claimCompletedJob() {
  const job = document.querySelector('[data-job-id]');
  if (!pendingClaim || !job) return;
  if (!pendingClaim.id) pendingClaim.id = job.dataset.jobId;
  if (pendingClaim.id !== job.dataset.jobId) { pendingClaim = undefined; return; }
  if (job.dataset.jobState === 'failed') { pendingClaim = undefined; return; }
  if (job.dataset.jobState !== 'done' || pendingClaim.started) return;
  const claim = pendingClaim;
  claim.started = true;
  let result = job.querySelector('[data-claim-result]');
  function showResult(message) {
    // An HTMX navigation can replace the job view while the one-shot claim is in flight.
    if (!result || !result.isConnected) {
      result = document.createElement('section');
      result.setAttribute('role', 'alert');
      result.setAttribute('tabindex', '-1');
      document.body.prepend(result);
      result.textContent = message;
      result.focus();
    } else {
      result.textContent = message;
    }
  }
  try {
    const csrf = document.getElementById('main').dataset.csrf;
    const response = await fetch('/jobs/' + encodeURIComponent(claim.id) + '/claim', {
      method: 'POST', headers: {'Content-Type': 'application/x-www-form-urlencoded', 'X-CSRF-Token': csrf},
      body: new URLSearchParams({claim: claim.token})
    });
    if (!response.ok) throw new Error('claim failed');
    const data = await response.json();
    if (typeof data.password !== 'string' || !data.password) throw new Error('invalid claim');
    showResult('Password (shown once; save it now): ' + data.password);
  } catch (_) {
    if (result) result.setAttribute('role', 'alert');
    showResult('Could not retrieve the one-time password. Rotate the password to try again.');
  } finally { if (pendingClaim === claim) pendingClaim = undefined; }
}
document.addEventListener('htmx:afterSwap', claimCompletedJob);
window.addEventListener('pagehide', function () { pendingClaim = undefined; });
let jobTimer;
let pollFailures = 0;
document.addEventListener('click', function (event) {
  if (event.target.matches('[data-poll-retry]')) {
    pollFailures = 0;
    event.target.hidden = true;
    event.target.closest('[data-job-id]').querySelector('[data-poll-feedback]').textContent = 'Retrying job status; keep this page open.';
    pollJob();
  }
});
function pollJob() {
  window.clearTimeout(jobTimer);
  const target = document.querySelector('[data-job-poll]');
  if (!target || typeof htmx === 'undefined') return;
  jobTimer = window.setTimeout(function () {
    if (target.isConnected) htmx.ajax('GET', target.dataset.jobPoll, {source:target, target:'body', swap:'innerHTML'});
  }, 2000);
}
document.addEventListener('DOMContentLoaded', pollJob);
document.addEventListener('htmx:afterSwap', function () {
  pollFailures = 0;
  pollJob();
});
window.addEventListener('pagehide', function () {
  document.querySelectorAll('[data-secret-form] textarea').forEach(function (field) { field.value = ''; });
});
