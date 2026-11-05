// Run with node internal/web/database_idempotency_test.cjs
const assert = require('node:assert/strict');
const vm = require('node:vm');
const fs = require('node:fs');
const handlers = {};
const document = {addEventListener:(n,f)=>(handlers[n] ||= []).push(f), getElementById:()=>null, createElement:()=>({dataset:{}})};
let count = 0, alerts = [];
const window = {addEventListener(){}, alert:s=>alerts.push(s)};
vm.runInNewContext(fs.readFileSync('internal/web/assets/app.js','utf8'), {document,window,URLSearchParams,crypto:{randomUUID:()=>`key-${++count}`}});
function form() { return {matches:s=>s.startsWith('form['), querySelector:()=>null, append(link){this.link=link}}; }
function configure(f, name) {
 const e = {detail:{elt:f,parameters:{name},headers:{}},preventDefault(){this.cancelled=true}};
 handlers['htmx:configRequest'].forEach(fn=>fn(e)); return e;
}
function swap(f, status) {
 const e = {detail:{xhr:{status},requestConfig:{elt:f}}};
 handlers['htmx:beforeSwap'].forEach(fn=>fn(e)); return e.detail;
}
for (const status of [400,401,403,409,429]) {
 const f = form(); configure(f,'demo');
 const result = swap(f,status);
 assert.equal(result.shouldSwap, true, `definitive ${status} must show response`);
 assert.equal(result.isError, false);
 assert.ok(configure(f,'changed').detail.headers['Idempotency-Key']);
}
const f = form();
const key = configure(f,'demo').detail.headers['Idempotency-Key'];
assert.equal(configure(f,'demo').detail.headers['Idempotency-Key'], key);
assert.equal(configure(f,'changed').cancelled, true);
assert.equal(swap(f,502).shouldSwap, false);
assert.equal(swap(f,403).shouldSwap, false);
assert.equal(f.link.href, '/reauth');
assert.equal(configure(f,'demo').detail.headers['Idempotency-Key'], key);
assert.equal(configure(f,'changed').cancelled, true);
const lost = form(); const lostKey = configure(lost,'demo').detail.headers['Idempotency-Key'];
for (const event of ['htmx:sendError', 'htmx:timeout']) {
 handlers[event].forEach(fn=>fn({detail:{elt:lost}}));
 const message = alerts.at(-1);
 assert.match(message, /may not have been admitted/i);
 assert.match(message, /session has not changed/i);
 assert.match(message, /claim if still valid/i);
 assert.match(message, /may recover/i);
 assert.match(message, /expired or.*consumed/i);
 assert.match(message, /contact an administrator.*job\.list and database inventory before/i);
 assert.match(message, /signing in or reauthenticating changes the session/i);
 assert.match(message, /do not retry blindly/i);
}
assert.equal(swap(lost,401).shouldSwap, false);
assert.equal(lost.link.href, '/login');
assert.equal(configure(lost,'demo').detail.headers['Idempotency-Key'], lostKey);
assert.ok(alerts.length);
console.log('database definitive and uncertain responses');
