const {chromium}=require('playwright');
const assert=require('node:assert/strict');
function assertSafeStorage(storage, path) {
 for (const entries of Object.values(storage)) for (const [key,value] of entries) {
  assert(!/PRIVATE|CERT|-----BEGIN|expected|nps_|npat_|otpauth:|<[^>]+>/i.test(key+' '+value),'secret or cached HTML in browser storage');
 }
 assert.deepEqual(storage,{localStorage:[],sessionStorage:[['htmx-current-path-for-history',path]]},'only HTMX current non-secret route may persist');
}
// The guard must reject both secret material and sensitive cached HTML.
for (const value of ['-----BEGIN PRIVATE KEY-----','<main data-csrf="expected">secret</main>']) {
 assert.throws(()=>assertSafeStorage({localStorage:[],sessionStorage:[['htmx-current-path-for-history',value]]},value));
}
(async()=>{
 const browser=await chromium.launch({executablePath:process.env.NOVAPANEL_CHROME||'/usr/bin/google-chrome',args:['--no-sandbox']});
 try {
 const origin=process.argv[2];const context=await browser.newContext({ignoreHTTPSErrors:true});
 await context.addCookies([{name:'__Host-novapanel',value:'fixture',url:origin,secure:true,httpOnly:true,sameSite:'Strict'}]);
 const page=await context.newPage();const errors=[];page.on('pageerror',e=>errors.push(e.message));
 await page.goto(origin+'/sites');await page.locator('[name=primary_domain]').fill('example.test');
 await page.locator('form[action="/sites/create"] button').click();
 await page.getByText('Job completed.',{exact:true}).waitFor();
 await page.getByRole('link',{name:'Return to sites and refresh state'}).click();
 await page.getByRole('link',{name:'demo',exact:true}).click();
 await page.getByRole('heading',{name:'Domains',exact:true}).waitFor();
 await page.getByText('/old → /new · 301 · ID: REDIRECT', {exact:true}).waitFor();
 await page.locator('[name=primary_domain_id]').selectOption('DOMAIN');
 await page.getByRole('button',{name:'Promote existing domain',exact:true}).click();
 await page.getByText('Job completed.',{exact:true}).waitFor();
 await page.goto(origin+'/sites/SITE');
 await page.getByRole('link',{name:'SSL',exact:true}).click();
 await page.getByText('Certificate source: none · Auto-renew: false · Force HTTPS: false',{exact:true}).waitFor();
 assert.equal(await page.locator('[name=enabled]').inputValue(),'false');
 await page.locator('form[action$="/ssl/renew"] button').click();
 await page.getByRole('status').filter({hasText:'unknown_verb'}).waitFor();
 await page.goto(origin+'/sites/SITE/ssl');
 await page.locator('[name=cert_pem]').fill('CERT');await page.locator('[name=key_pem]').fill('PRIVATE');
 await page.locator('form[data-secret-form] button').click();
 await page.getByRole('link',{name:'Verify your authenticator code to continue'}).waitFor();
 assert(!((await page.content()).includes('PRIVATE')));
 const storage=await page.evaluate(()=>Object.fromEntries(['localStorage','sessionStorage'].map(name=>[name,Object.entries(window[name])])));
 assertSafeStorage(storage,'/sites/SITE/ssl');
 console.log('Storage verified: only sessionStorage htmx-current-path-for-history=/sites/SITE/ssl; no secrets or cached HTML.');
 await page.goto(origin+'/sites/SITE');await page.setViewportSize({width:390,height:844});
 assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=window.innerWidth),true,'mobile overflow');
 assert.deepEqual(errors,[]);
 console.log('PASS Chromium console fixture: create -> queued -> poll done -> list -> domains -> SSL; unknown verb visible; MFA upload refusal visible, secrets absent, storage limited to non-secret HTMX route; mobile fit; no JS errors.');
 } finally {await browser.close()}
})().catch(e=>{console.error(e);process.exit(1)});
