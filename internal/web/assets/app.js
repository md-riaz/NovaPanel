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
