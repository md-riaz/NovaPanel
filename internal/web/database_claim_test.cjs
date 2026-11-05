// Run with node internal/web/database_claim_test.cjs
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');

async function run(navigate) {
  const handlers = {};
  const result = {textContent:'', isConnected:true, setAttribute() {}};
  const job = {dataset:{jobId:'JOB',jobState:'done'}, querySelector:()=>result};
  const body = {children:[], prepend(element) { this.children.unshift(element); element.isConnected = true; }};
  const document = {
    body,
    querySelectorAll:()=>[],
    addEventListener:(n,f)=>(handlers[n] ||= []).push(f),
    querySelector:s=>s==='[data-job-id]'?job:null,
    getElementById:()=>({dataset:{csrf:'csrf'}}),
    createElement:()=>({textContent:'', attributes:{}, setAttribute(k,v){this.attributes[k]=v;}, focus(){this.focused=true;}})
  };
  const window = {addEventListener(){},clearTimeout(){},setTimeout(){}};
  let resolveFetch, calls = 0;
  const fetch = (url, opts) => {
    calls++;
    assert.equal(url,'/jobs/JOB/claim');
    assert.equal(opts.headers['X-CSRF-Token'],'csrf');
    assert.equal(opts.body.get('claim'),'token');
    return new Promise(resolve => { resolveFetch = resolve; });
  };
  vm.runInNewContext(fs.readFileSync('internal/web/assets/app.js','utf8'), {document,window,fetch,URLSearchParams,console});
  handlers['htmx:beforeSwap'].forEach(f=>f({detail:{xhr:{status:202,getResponseHeader:()=> 'token'}}}));
  const claim = Promise.all(handlers['htmx:afterSwap'].map(f=>f()));
  if (navigate) result.isConnected = false; // An HTMX body swap removes the old job view.
  resolveFetch({ok:true,json:async()=>({password:'<secret>'})});
  await claim;
  const visible = navigate ? body.children[0] : result;
  assert.ok(visible && visible.isConnected, 'the password must be in the current document');
  assert.equal(visible.textContent,'Password (shown once; save it now): <secret>');
  if (navigate) assert.equal(visible.attributes.role, 'alert');
  await Promise.all(handlers['htmx:afterSwap'].map(f=>f()));
  assert.equal(calls,1);
}
(async()=>{ await run(false); await run(true); console.log('one-shot browser claim and navigation race passed'); })().catch(e=>{console.error(e);process.exitCode=1});
