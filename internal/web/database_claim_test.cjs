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
async function failedJob(path, hasToken, claimAvailable) {
  const handlers = {}, result = {textContent:'', isConnected:true, setAttribute(){}};
  const job = {dataset:{jobId:'JOB',jobState:'queued'}, querySelector:()=>result};
  const document = {body:{prepend(){}}, querySelectorAll:()=>[], addEventListener:(n,f)=>(handlers[n] ||= []).push(f), querySelector:s=>s==='[data-job-id]'?job:null, getElementById:()=>({dataset:{csrf:'csrf'}})};
  const window = {addEventListener(){},clearTimeout(){},setTimeout(){}};
  let calls = 0;
  const fetch = async (url,opts)=>{ calls++; assert.equal(url,'/jobs/JOB/claim'); assert.equal(opts.body.get('claim'),'token'); return claimAvailable ? {ok:true,json:async()=>({password:'recovered'})} : {ok:false}; };
  vm.runInNewContext(fs.readFileSync('internal/web/assets/app.js','utf8'),{document,window,fetch,URLSearchParams});
  const form = {matches:s=>s==='form[hx-post^="/databases/"]' || (s==='form[hx-post$="/rotate"]' && path==='rotate')};
  handlers['htmx:beforeSwap'].forEach(f=>f({detail:{requestConfig:{elt:form},xhr:{status:202,getResponseHeader:()=>hasToken?'token':null}}}));
  await Promise.all(handlers['htmx:afterSwap'].map(f=>f()));
  job.dataset.jobState='failed';
  await Promise.all(handlers['htmx:afterSwap'].map(f=>f()));
  await Promise.all(handlers['htmx:afterSwap'].map(f=>f()));
  assert.equal(calls,path==='rotate' && hasToken ? 1 : 0);
  if (calls && claimAvailable) assert.match(result.textContent,/recovered/);
  if (calls && !claimAvailable) { assert.match(result.textContent,/Could not retrieve/); assert.doesNotMatch(result.textContent,/recovered/); }
  if (!calls) assert.equal(result.textContent,'');
}
(async()=>{ await run(false); await run(true); await failedJob('rotate',true,true); await failedJob('rotate',true,false); await failedJob('create',true,true); await failedJob('rotate',false,true); console.log('one-shot browser claim and failure handling passed'); })().catch(e=>{console.error(e);process.exitCode=1});
