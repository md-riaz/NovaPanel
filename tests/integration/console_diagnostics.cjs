// Allowlist at collection boundaries: never retain bodies, headers or free-form errors.
const jobID=/^[0-7][0-9A-HJKMNP-TV-Z]{25}$/;
const events=['response','requestfailed','pageerror','DOMContentLoaded','htmx:afterSwap','htmx:afterRequest','htmx:sendError','htmx:responseError','snapshot','snapshot-unavailable'];
function sanitize(raw){
 const out={};
 if(events.includes(raw.event))out.event=raw.event;
 if(typeof raw.path==='string'){
  const path=raw.path.split('?')[0];
  out.path=/^\/jobs\/[0-7][0-9A-HJKMNP-TV-Z]{25}$/.test(path)?'/jobs/:id':path==='/sites/create'?path:'other';
 }
 if(['GET','POST'].includes(raw.method))out.method=raw.method;
 if(Number.isInteger(raw.status)&&raw.status>=100&&raw.status<=599)out.status=raw.status;
 if(typeof raw.jobID==='string'&&jobID.test(raw.jobID))out.jobID=raw.jobID;
 if(['queued','running','done','failed'].includes(raw.state))out.state=raw.state;
 for(const key of ['poll','connected','done','alert'])if(typeof raw[key]==='boolean')out[key]=raw[key];
 if(Number.isSafeInteger(raw.ms)&&raw.ms>=0)out.ms=raw.ms;
 return out;
}
async function observe(page){
 const start=Date.now(),records=[];
 const record=raw=>{records.push(sanitize({...raw,ms:Date.now()-start}));if(records.length>80)records.shift()};
 page.on('response',r=>record({event:'response',path:new URL(r.url()).pathname,method:r.request().method(),status:r.status()}));
 page.on('requestfailed',r=>record({event:'requestfailed',path:new URL(r.url()).pathname,method:r.method()}));
 page.on('pageerror',()=>record({event:'pageerror'}));
 await page.exposeBinding('__acceptanceDiagnostic',(_,raw)=>record(raw));
 await page.addInitScript(()=>{
  const snapshot=event=>{
   const marker=document.querySelector('[data-job-poll]'),card=document.querySelector('section[aria-live="polite"]');
   const heading=card?.querySelector('h2')?.textContent||'';
   const raw={event,poll:!!marker,connected:!!marker?.isConnected,done:!!card?.textContent.includes('Job completed.'),alert:!!document.querySelector('[role=alert]'),jobID:heading.startsWith('Job ')?heading.slice(4):'',state:card?.querySelector('strong')?.textContent};
   // Only metadata crosses into Node. Do not pass DOM text or event details.
   if(!/^[0-7][0-9A-HJKMNP-TV-Z]{25}$/.test(raw.jobID))delete raw.jobID;
   if(!['queued','running','done','failed'].includes(raw.state))delete raw.state;
   return raw;
  };
  window.__acceptanceSnapshot=snapshot;
  for(const event of ['DOMContentLoaded','htmx:afterSwap','htmx:afterRequest','htmx:sendError','htmx:responseError'])document.addEventListener(event,()=>{void window.__acceptanceDiagnostic(snapshot(event)).catch(()=>{})});
 });
 return async()=>{
  try{record(await page.evaluate(()=>window.__acceptanceSnapshot('snapshot')))}catch{record({event:'snapshot-unavailable'})}
  return records;
 };
}
module.exports={sanitize,observe};
