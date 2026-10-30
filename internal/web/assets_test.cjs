// Scratch-only test dependencies: jsdom 26.1.0 and jsqr 1.4.0.
// NODE_PATH=/path/to/scratch/node_modules node internal/web/assets_test.cjs
const assert = require('node:assert/strict');
const fs = require('node:fs');
const {JSDOM} = require('jsdom');
const dom = new JSDOM('<main id="main" data-csrf="expected"><div id="enrollment-qr" data-uri="otpauth://totp/Panel?secret=ABCDEFGHIJKLMNOP"></div></main>', {runScripts:'outside-only',url:'https://panel.example'});
dom.window.eval(fs.readFileSync('internal/web/assets/qrcode.js','utf8'));
dom.window.eval(fs.readFileSync('internal/web/assets/app.js','utf8'));
const event = new dom.window.CustomEvent('htmx:configRequest',{detail:{headers:{}}});
dom.window.document.dispatchEvent(event);
assert.equal(event.detail.headers['X-CSRF-Token'],'expected');
dom.window.document.dispatchEvent(new dom.window.Event('DOMContentLoaded'));
assert.ok(dom.window.document.querySelector('#enrollment-qr svg path'));
assert.equal(dom.window.document.querySelectorAll('img').length,0);
// Independently decode the rendered SVG cells, not the encoder's own matrix.
const jsQR = require('jsqr');
const svg = dom.window.document.querySelector('#enrollment-qr svg');
const viewBox = svg.getAttribute('viewBox').split(' ').map(Number);
const scale = 6;
const width = viewBox[2] * scale;
const pixels = new Uint8ClampedArray(width * width * 4).fill(255);
for (const match of svg.querySelector('path').getAttribute('d').matchAll(/M(\d+) (\d+)h1v1h-1z/g)) {
  const x = (Number(match[1]) - viewBox[0]) * scale;
  const y = (Number(match[2]) - viewBox[1]) * scale;
  for (let dy = 0; dy < scale; dy++) for (let dx = 0; dx < scale; dx++) {
    const offset = ((y + dy) * width + x + dx) * 4;
    pixels[offset] = pixels[offset + 1] = pixels[offset + 2] = 0;
  }
}
const decoded = jsQR(pixels, width, width);
assert.ok(decoded, 'independent QR decoder rejected enrollment SVG');
assert.equal(decoded.data, dom.window.document.getElementById('enrollment-qr').dataset.uri);
console.log('CSRF event, local SVG QR, and independent jsQR decode passed');
