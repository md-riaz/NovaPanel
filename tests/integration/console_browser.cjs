const {chromium}=require('playwright');
const assert=require('node:assert/strict');
const crypto=require('node:crypto');
const fixture=JSON.parse(require('node:fs').readFileSync(0,'utf8'));
function totp(secret) {
 let bits='';for(const c of secret)bits+='ABCDEFGHIJKLMNOPQRSTUVWXYZ234567'.indexOf(c).toString(2).padStart(5,'0');
 const key=Buffer.from(bits.match(/.{8}/g).map(x=>parseInt(x,2))),msg=Buffer.alloc(8);msg.writeBigUInt64BE(BigInt(Math.floor(Date.now()/30000)));
 const h=crypto.createHmac('sha1',key).update(msg).digest(),o=h[19]&15;return ((h.readUInt32BE(o)&0x7fffffff)%1000000).toString().padStart(6,'0');
}
let stage='login';
(async()=>{
 const browser=await chromium.launch({executablePath:process.env.NOVAPANEL_CHROME||'/usr/bin/google-chrome',args:['--no-sandbox']});
 try {
 const context=await browser.newContext({ignoreHTTPSErrors:true}),page=await context.newPage(),origin=process.argv[2],errors=[];
 page.on('pageerror',()=>errors.push('JavaScript error'));
 page.setDefaultTimeout(15000);
 const submit=async selector=>{const response=page.waitForResponse(r=>r.request().method()==='POST');await page.locator(selector+' button').click();const r=await response;if(selector==='form[data-secret-form]')console.log('Upload HTTP '+r.status())};
 const done=async()=>{await page.waitForFunction(()=>document.body.textContent.includes('Job completed.')||document.querySelector('[role=alert]'));if(await page.locator('[role=alert]').count()){const text=await page.locator('[role=alert]').first().textContent();throw new Error('job code '+(text.match(/^[a-z_]+:/)||['unknown'])[0])}};
 await page.goto(origin+'/login');await page.locator('[name=user]').fill('browserowner');await page.locator('[name=password]').fill('disposable browser password');
 await submit('form[action="/login"]');await page.waitForURL(origin+'/');
 stage='create';await page.goto(origin+'/sites');await page.locator('[name=name]').fill('browserdemo');await page.locator('[name=primary_domain]').fill('example.test');
 await submit('form[action="/sites/create"]');await done();
 await page.getByRole('link',{name:'Return to sites and refresh state'}).click();await page.getByRole('link',{name:'browserdemo',exact:true}).click();
 stage='maintenance';const siteURL=page.url();const sitePath=new URL(siteURL).pathname;
 await page.locator('[name=maintenance]').selectOption('true');await page.getByRole('button',{name:'Apply maintenance setting'}).click();await done();
 stage='domain';await page.goto(siteURL);await page.locator('[name=fqdn]').fill('alias.test');await submit('form[action$="/domains/add"]');await done();
 stage='redirect';await page.goto(siteURL);await page.locator('[name=source]').fill('/old');await page.locator('[name=target]').fill('/new');await submit('form[action$="/redirects/add"]');await done();
 await page.goto(siteURL);await page.getByText(/\/old → \/new · 301 · ID:/).waitFor();
 stage='promotion';await page.locator('[name=primary_domain_id]').selectOption({label:'alias.test'});await page.getByRole('button',{name:'Promote existing domain'}).click();await done();stage='SSL initial status';
 await page.goto(siteURL+'/ssl');await page.getByText('Certificate source: none · Auto-renew: true · Force HTTPS: false',{exact:true}).waitFor();
 await page.locator('[name=cert_pem]').fill(fixture.cert);await page.locator('[name=key_pem]').fill(fixture.key);await submit('form[data-secret-form]');
 await page.getByRole('link',{name:'Verify your authenticator code to continue'}).waitFor();
 assert.equal(await page.locator('textarea').count(),0);
 stage='delete refusal';
 // Delete has the same durable recent-MFA gate and must not enqueue before step-up.
 await page.goto(siteURL);page.once('dialog',d=>d.accept());await submit('form[action$="/delete"]');await page.getByRole('link',{name:'Verify your authenticator code to continue'}).waitFor();
 stage='enrollment';await page.goto(origin+'/mfa');await submit('form[action="/mfa/enroll"]');await page.locator('#enrollment-qr').waitFor();
 const uri=await page.locator('#enrollment-qr').getAttribute('data-enrollment');
 await page.locator('form[action="/mfa/confirm"] [name=totp]').fill(totp(new URL(uri).searchParams.get('secret')));await submit('form[action="/mfa/confirm"]');await page.locator('li code').first().waitFor();
 const backup=await page.locator('li code').first().textContent();
 stage='stepup';await page.goto(origin+'/reauth');await page.locator('[name=totp]').fill(backup);await submit('form[action="/reauth"]');await page.getByRole('status').waitFor();
 await page.goto(siteURL+'/ssl');await page.locator('[name=cert_pem]').fill(fixture.cert);await page.locator('[name=key_pem]').fill(fixture.key);await submit('form[data-secret-form]');stage='upload job';await done();
 await page.goto(siteURL+'/ssl');await page.getByText('Certificate source: manual · Auto-renew: true · Force HTTPS: false',{exact:true}).waitFor();
 await page.locator('[name=force_https]').selectOption('true');await page.getByRole('button',{name:'Apply force HTTPS',exact:true}).click();await done();
 await page.goto(siteURL+'/ssl');await page.getByText('Certificate source: manual · Auto-renew: true · Force HTTPS: true',{exact:true}).waitFor();
 stage='TLS primary promotion';await page.goto(siteURL);await page.locator('[name=primary_domain_id]').selectOption({label:'example.test'});await page.getByRole('button',{name:'Promote existing domain'}).click();await done();
 await page.goto(siteURL+'/ssl');await page.getByText('Certificate source: manual · Auto-renew: true · Force HTTPS: true',{exact:true}).waitFor();
 const storage=await page.evaluate(()=>({local:Object.entries(localStorage),session:Object.entries(sessionStorage)}));
 assert.deepEqual(storage,{local:[],session:[['htmx-current-path-for-history',sitePath+'/ssl']]});
 await page.setViewportSize({width:390,height:844});assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));assert.deepEqual(errors,[]);
 stage='delete';await page.goto(siteURL);page.once('dialog',d=>d.accept());await submit('form[action$="/delete"]');await done();await page.goto(origin+'/sites');assert.equal(await page.getByRole('link',{name:'browserdemo',exact:true}).count(),0);
 console.log('PASS actual panel-web -> UID-gated socket -> daemon -> queue -> SQLite: owner login; create/poll; maintenance; domain; redirect; HTTP and TLS primary promotion; upload/delete MFA refusal; MFA enrollment/backup step-up; manual certificate upload; force HTTPS; secret-free storage; mobile width; deletion; no JS errors.');
 } finally {await browser.close()}
})().catch(e=>{console.error('Real-backend browser acceptance failed at '+stage+' ('+(e.message.startsWith('job code ')?e.message:e.name)+'), source '+((e.stack||'').match(/console_browser.cjs:\d+:\d+/)||['unknown'])[0]);process.exitCode=1});
