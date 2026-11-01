const test=require('node:test');
const assert=require('node:assert/strict');
const {sanitize,observe}=require('./console_diagnostics.cjs');
test('diagnostics allow only fixed metadata, never fixture secrets',()=>{
 const secrets=['fixture-password','-----BEGIN PRIVATE KEY-----','-----BEGIN CERTIFICATE-----','csrf-fixture','nps_session_fixture','otpauth://fixture','backup-fixture'];
 for(const secret of secrets){
  const report=sanitize({event:secret,path:'/jobs/'+secret+'?csrf='+secret,method:secret,status:secret,jobID:secret,state:secret,poll:secret,connected:secret,done:secret,alert:secret,ms:secret,body:secret,headers:{cookie:secret},error:secret});
  assert(!JSON.stringify(report).includes(secret));
 }
 assert.deepEqual(sanitize({event:'htmx:afterSwap',path:'/jobs/01ARZ3NDEKTSV4RRFFQ69G5FAV?csrf=hidden',method:'GET',status:200,jobID:'01ARZ3NDEKTSV4RRFFQ69G5FAV',state:'done',poll:false,connected:false,done:true,alert:false,ms:12}),{event:'htmx:afterSwap',path:'/jobs/:id',method:'GET',status:200,jobID:'01ARZ3NDEKTSV4RRFFQ69G5FAV',state:'done',poll:false,connected:false,done:true,alert:false,ms:12});
});
test('blocked polling keeps timeout failure and produces secret-free evidence',async()=>{
 const {chromium}=require('playwright'),http=require('node:http');
 const secret='fixture-password-csrf-nps_session-backup-PEM';
 const server=http.createServer((req,res)=>{
  res.writeHead(202,{'content-type':'text/html','set-cookie':'session='+secret});
  res.end(`<section aria-live="polite"><h2>Job 01ARZ3NDEKTSV4RRFFQ69G5FAV</h2><strong>queued</strong><div data-job-poll="/jobs/01ARZ3NDEKTSV4RRFFQ69G5FAV"></div><textarea>${secret}</textarea></section><script>document.dispatchEvent(new Event('htmx:afterSwap'))</script>`);
 });
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 const browser=await chromium.launch({executablePath:process.env.NOVAPANEL_CHROME||'/usr/bin/google-chrome',args:['--no-sandbox']});
 try{
  const page=await browser.newPage(),diagnostics=await observe(page);
  await page.goto('http://127.0.0.1:'+server.address().port+'/sites/create');
  // This isolated fixture deliberately has no poll script, not a production fault switch.
  await assert.rejects(page.waitForFunction(()=>document.body.textContent.includes('Job completed.')||document.querySelector('[role=alert]'),null,{timeout:100}),{name:'TimeoutError'});
  const report=await diagnostics();
  assert(report.some(r=>r.event==='response'&&r.status===202&&r.path==='/sites/create'));
  assert(report.some(r=>r.event==='htmx:afterSwap'&&r.poll&&r.state==='queued'));
  assert(report.some(r=>r.event==='snapshot'&&r.jobID==='01ARZ3NDEKTSV4RRFFQ69G5FAV'&&r.connected&&!r.done));
  assert(!JSON.stringify(report).includes(secret));
  console.log('[fixture timeout diagnostics] '+JSON.stringify(report));
 }finally{await browser.close();await new Promise(resolve=>server.close(resolve))}
});
