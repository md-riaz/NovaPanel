const { chromium } = require('playwright');
const assert = require('node:assert/strict');
const crypto = require('node:crypto');
const fs = require('node:fs');
function totp(secret) {
  const alphabet = 'ABCDEFGHIJKLMNOPQRSTUVWXYZ234567';
  let bits = ''; for (const ch of secret) bits += alphabet.indexOf(ch).toString(2).padStart(5, '0');
  const key = Buffer.from(bits.match(/.{8}/g).map(x => parseInt(x, 2)));
  const msg = Buffer.alloc(8); msg.writeBigUInt64BE(BigInt(Math.floor(Date.now()/30000)));
  const h = crypto.createHmac('sha1', key).update(msg).digest(); const o = h[19]&15;
  return ((h.readUInt32BE(o)&0x7fffffff)%1000000).toString().padStart(6,'0');
}
(async () => {
 const browser = await chromium.launch({executablePath:process.env.NOVAPANEL_CHROME || '/usr/bin/google-chrome', args:['--no-sandbox']});
 const context = await browser.newContext({ignoreHTTPSErrors:true}); const page = await context.newPage();
 const evidence = []; const errors = []; page.on('pageerror', e => errors.push(e.message));
 const origin = process.argv[2];
 const csrf = () => page.locator('#main').getAttribute('data-csrf');
 const cookie = async () => (await context.cookies()).find(c => c.name === '__Host-novapanel');
 const post = async (path, body='', token=null) => page.evaluate(async ({path,body,token}) => {
   const r = await fetch(path,{method:'POST',headers:{'Content-Type':'application/x-www-form-urlencoded',...(token===null?{}:{'X-CSRF-Token':token})},body});
   return {status:r.status, text:await r.text()};
 },{path,body,token});
 async function submit(selector) {
   const response = page.waitForResponse(r => r.request().method()==='POST');
   await page.locator(selector+' button').click(); return response;
 }
 try {
  const first = await page.goto(origin+'/login'); assert.equal(first.status(),200);
  assert.match(first.headers()['content-security-policy'],/script-src 'self'/); assert.equal(first.headers()['cache-control'],'no-store');
  await page.waitForFunction(() => typeof htmx !== 'undefined');
  const anon = await cookie(); assert(anon.secure && anon.httpOnly && anon.sameSite==='Strict' && anon.path==='/');
  const before = await csrf(); await page.reload(); assert.equal(await csrf(),before); assert.equal((await cookie()).value,anon.value);
  assert.equal(await page.evaluate(()=>document.cookie),'');
  for (const asset of ['app.js','htmx.min.js','qrcode.js','app.css']) assert.equal((await page.request.get(origin+'/assets/'+asset)).status(),200);
  assert.equal((await post('/login','user=reader&password=wrong')).status,403);
  assert.equal((await post('/login','user=reader&password=wrong','incorrect')).status,403);
  assert.equal((await post('/login','user=reader&password=wrong',before)).status,401);
  evidence.push('Anonymous GET reuses session and CSRF; hardened HttpOnly cookie; local assets; missing/wrong CSRF refuse; bad password refuses.');
  await page.locator('[name=user]').fill('reader'); await page.locator('[name=password]').fill('browser fixture password');
  const login = await submit('form[action="/login"]');
  assert.equal(login.request().headers()['x-csrf-token'],before);
  await page.waitForURL(origin+'/'); await page.locator('h1').filter({hasText:'Server overview'}).waitFor();
  assert.notEqual((await cookie()).value,anon.value); assert.notEqual(await csrf(),before); assert.match(await page.locator('body').innerText(),/read_only/);
  assert.equal(await page.locator('form').count(),1);
  evidence.push('Real HTMX password login sends synchronizer header, rotates cookie and CSRF, opens read-only dashboard.');
  await page.goto(origin+'/mfa');
  const enroll = await submit('form[action="/mfa/enroll"]'); assert.equal(enroll.status(),200);
  await page.locator('#enrollment-qr svg path').waitFor();
  const uri = await page.locator('#enrollment-qr').getAttribute('data-uri'); const secret = new URL(uri).searchParams.get('secret');
  await page.locator('form[action="/mfa/confirm"] [name=totp]').fill(totp(secret));
  assert.equal((await submit('form[action="/mfa/confirm"]')).status(),200);
  await page.locator('li code').first().waitFor(); const backups = await page.locator('li code').allTextContents(); assert(backups.length>=8);
  await page.reload(); assert.equal(await page.locator('li code').count(),0); assert.equal(await page.locator('#enrollment-qr').count(),0);
  evidence.push('MFA enroll renders local SVG QR; computed TOTP confirms; backup codes appear once and disappear on reload.');
  await page.goto(origin+'/reauth'); const old = await cookie(); const oldCSRF = await csrf();
  await page.locator('[name=totp]').fill(backups[0]); assert.equal((await submit('form[action="/reauth"]')).status(),200);
  await page.getByRole('status').waitFor(); assert.notEqual((await cookie()).value,old.value); assert.notEqual(await csrf(),oldCSRF);
  assert.equal((await post('/reauth','totp='+encodeURIComponent(backups[0]),await csrf())).status,401);
  assert.equal((await post('/logout','',oldCSRF)).status,403);
  const cross = await page.request.post(origin+'/logout',{headers:{Origin:'https://evil.example','X-CSRF-Token':await csrf(),'Content-Type':'application/x-www-form-urlencoded'},data:''}); assert.equal(cross.status(),403);
  const fetchSite = await page.request.post(origin+'/logout',{headers:{'Sec-Fetch-Site':'cross-site',Origin:origin,'X-CSRF-Token':await csrf(),'Content-Type':'application/x-www-form-urlencoded'},data:''}); assert.equal(fetchSite.status(),403);
  evidence.push('Backup step-up rotates cookie and header; backup replay, stale CSRF, cross-Origin and cross-site Fetch Metadata refuse.');
  assert.equal((await page.request.get(origin+'/mfa/disable')).status(),405);
  assert.equal((await page.request.post(origin+'/user/create',{data:''})).status(),404);
  await page.request.post(origin+'/__test/expire');
  assert.equal((await post('/logout','',await csrf())).status,401);
  await context.clearCookies(); await page.goto(origin+'/login');
  await page.locator('[name=user]').fill('reader'); await page.locator('[name=password]').fill('browser fixture password'); await page.locator('[name=totp]').fill(backups[1]);
  await submit('form[action="/login"]'); await page.waitForURL(origin+'/');
  await submit('form[action="/logout"]'); await page.waitForURL(origin+'/login');
  evidence.push('Safe-method mutation and unknown mutation route refuse; agent-clock session timeout refuses; MFA backup login and logout succeed.');
  assert.deepEqual(errors,[]);
  if(process.env.NOVAPANEL_BROWSER_ARTIFACTS) {
   fs.mkdirSync(process.env.NOVAPANEL_BROWSER_ARTIFACTS,{recursive:true});
   await page.screenshot({path:process.env.NOVAPANEL_BROWSER_ARTIFACTS+'/logged-out.png'});
   fs.writeFileSync(process.env.NOVAPANEL_BROWSER_ARTIFACTS+'/report.json',JSON.stringify({engine:'Chromium',evidence,consoleErrors:errors},null,2));
  }
  console.log(JSON.stringify({engine:'Chromium',evidence,consoleErrors:errors},null,2));
 } finally { await browser.close(); }
})().catch(e=>{console.error(e);process.exitCode=1;});
