// Run with node internal/web/job_poll_test.cjs
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const handlers = {};
const feedback = {textContent:''};
const retry = {hidden:true, matches:s=>s==='[data-poll-retry]', closest:()=>job};
const job = {dataset:{jobId:'JOB', jobState:'queued'}, querySelector:s=>s==='[data-poll-feedback]'?feedback:retry};
const target = {dataset:{jobPoll:'/jobs/JOB'}, isConnected:true, matches:s=>s==='[data-job-poll]', closest:()=>job};
let currentJob = job, timer, requests = [], claimCalls = 0;
const document = {addEventListener:(n,f)=>(handlers[n] ||= []).push(f), querySelector:s=>s==='[data-job-id]'?currentJob:s==='[data-job-poll]'&&currentJob===job?target:null, getElementById:()=>({dataset:{csrf:'csrf'}})};
const window = {addEventListener(){}, clearTimeout(){timer=undefined;}, setTimeout(fn,ms){timer={fn,ms};}};
const htmx = {ajax:(method,url,opts)=>requests.push({method,url,opts})};
const fetch = async()=>{claimCalls++; return {ok:true,json:async()=>({password:'secret'})};};
vm.runInNewContext(fs.readFileSync('internal/web/assets/app.js','utf8'),{document,window,htmx,fetch,URLSearchParams});
const fire=(name,detail)=>handlers[name].forEach(f=>f({detail}));
const tick=()=>{assert.ok(timer); const t=timer; timer=undefined; t.fn();};
(async()=>{
  fire('htmx:beforeSwap',{xhr:{status:202,getResponseHeader:()=> 'token'}});
  fire('htmx:afterSwap',{}); tick();
  assert.equal(requests[0].opts.source,target);
  for (const status of [429,503,503,503]) {
    const detail={elt:{},requestConfig:{elt:target},xhr:{status},shouldSwap:false,isError:true};
    fire('htmx:beforeSwap',detail);
    assert.equal(detail.shouldSwap,false); assert.equal(detail.isError,true);
    assert.ok(feedback.textContent);
    if (status===429 || requests.length<4) tick();
  }
  assert.equal(requests.length,4,'automatic retries must be bounded');
  assert.equal(timer,undefined); assert.equal(retry.hidden,false);
  handlers.click.forEach(f=>f({target:retry})); tick();
  assert.equal(requests.length,5);
  const permanent={elt:{},requestConfig:{elt:target},xhr:{status:403},shouldSwap:false,isError:true};
  fire('htmx:beforeSwap',permanent); assert.equal(permanent.shouldSwap,true);
  const unrelated={elt:{matches:()=>false},xhr:{status:503},shouldSwap:false,isError:true};
  fire('htmx:beforeSwap',unrelated); assert.equal(unrelated.shouldSwap,true);
  const result={isConnected:true,textContent:'',setAttribute(){}};
  currentJob={dataset:{jobId:'JOB',jobState:'done'},querySelector:()=>result};
  await Promise.all(handlers['htmx:afterSwap'].map(f=>f()));
  assert.equal(claimCalls,1); assert.match(result.textContent,/secret/);
  console.log('bounded poll retry preserves job and one-shot claim');
})().catch(e=>{console.error(e);process.exitCode=1;});
