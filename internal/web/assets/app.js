document.addEventListener('htmx:configRequest', function (event) {
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
