// Large-GLB viewer spot check in the system Chrome (docs/acceptance-1.0.md M3D-01/M3D-03/DLV-01/DLV-02, WebKit-vs-Chrome
// large-blob follow-up). Free: it only opens, rotates, downloads and tampers (via request interception) with models that
// already exist in the test account; it creates no Task.
//
// Run: CV_SESSION=<session id> CV_BIG_TASK=<task id of a large GLB> CV_BIG_TILE='3D v5' \
//        CV_SMALL_TASK=<task id of a small GLB> CV_SMALL_TILE='3D v6' \
//        python3 scripts/release-env.py smoke <env> -- node scripts/acceptance/chrome_viewer.mjs
//   (or python3 scripts/acceptance/chrome_viewer.py --help)
// Both tasks must belong to CV_SESSION; the tiles are the version-tile labels the stage shows for them.
// Optional: OUT (default: system temp dir), CELLS='zh-CN:1366x768,en:1366x768', FULLDL='zh-CN:1366x768' (cell that also
// downloads the ZIP archive), CV_CHANNEL=chrome (Playwright channel; 'chromium' uses the bundled build).
import fs from 'node:fs'; import os from 'node:os'; import path from 'node:path'; import crypto from 'node:crypto';
import { base, loginAdmin, resolvePlaywright, requireEnv } from './browser_matrix/lib.mjs';
requireEnv('BASE', 'ADMIN_USERNAME', 'ADMIN_PASSWORD', 'CV_SESSION', 'CV_BIG_TASK', 'CV_BIG_TILE', 'CV_SMALL_TASK', 'CV_SMALL_TILE');
const pw = resolvePlaywright();
const OUT = process.env.OUT || path.join(os.tmpdir(), 'plinth-acceptance-chrome-viewer', new Date().toISOString().replace(/[:.]/g, '-')); fs.mkdirSync(OUT,{recursive:true});
const SID=process.env.CV_SESSION, BIG=process.env.CV_BIG_TASK, SMALL=process.env.CV_SMALL_TASK;
const BIG_TILE=new RegExp(process.env.CV_BIG_TILE), SMALL_TILE=new RegExp(process.env.CV_SMALL_TILE);
const CHANNEL=process.env.CV_CHANNEL||'chrome';
const cells = (process.env.CELLS||'zh-CN:1366x768,en:1366x768,zh-CN:1920x1080,en:1920x1080').split(',').map(s=>{const [l,v]=s.split(':');return {loc:l,W:+v.split('x')[0],H:+v.split('x')[1]};});
const FULL = process.env.FULLDL||'zh-CN:1366x768';
const san = (s)=>String(s).split(new URL(base).origin).join('<BASE>').replace(/\?[^\s'"]*/g,'?…').slice(0,260);
// Expected size and digest come from the public Task, not from constants.
const probe = await pw.chromium.launch(CHANNEL==='chromium'?{}:{channel:CHANNEL});
const pctx = await probe.newContext(); if(await loginAdmin(pctx)!==200) throw new Error('login');
const bigMeta = await (await pctx.request.get(`${base}/api/v1/tasks/${BIG}`)).json();
const bigGlb = (bigMeta.assets||[]).find(a=>a.variant==='glb'); if(!bigGlb) throw new Error('CV_BIG_TASK has no glb asset');
const BIGSHA=bigGlb.sha256, BIGSIZE=bigGlb.size; await probe.close();
const report=[]; const failures=[];
for (const {loc,W,H} of cells) {
  const tag=`${loc}-${W}x${H}`; const dir=path.join(OUT,tag); fs.mkdirSync(dir,{recursive:true});
  const res={cell:tag,checks:{},console:[],failed:[],http:[],glb:[],posts:[]}; let phase='A-D';
  const b = await pw.chromium.launch(CHANNEL==='chromium'?{}:{channel:CHANNEL});
  const ctx = await b.newContext({viewport:{width:W,height:H},locale:loc==='en'?'en-US':loc,acceptDownloads:true});
  if(await loginAdmin(ctx)!==200) throw new Error('login');
  const page = await ctx.newPage();
  page.on('console',m=>{ if(['error','warning'].includes(m.type())) res.console.push(phase+' '+m.type()+': '+san(m.text())); });
  page.on('pageerror',e=>res.console.push(phase+' pageerror: '+san(e.message)));
  page.on('requestfailed',r=>res.failed.push(phase+' '+san(r.url())+' '+(r.failure()?.errorText||'')));
  page.on('response',r=>{ const u=r.url(); if(r.status()>=400) res.http.push(phase+' '+r.status()+' '+san(u)); if(r.request().method()==='POST') res.posts.push(phase+' '+r.status()+' '+san(u)); if(/\/tasks\/task_[0-9a-f]+\/assets\//.test(u)&&r.status()<400) res.glb.push(san(u).replace(/^<BASE>\/api\/v1\/tasks\//,'')+' len='+r.headers()['content-length']); });
  const shot=async(n)=>{const f=path.join(dir,n+'.png'); await page.screenshot({path:f}); return f;};
  const zh=loc!=='en';
  const statsText=async()=>page.locator('[data-testid=stage-model-stats]').innerText().catch(()=>'');
  const orbit=()=>page.evaluate(()=>{const m=document.querySelector('[data-testid=stage-3d] model-viewer'); if(!m) return null; const o=m.getCameraOrbit(); return [o.theta,o.phi,o.radius];});
  const open=async()=>{ await page.goto(`${base}/?session=${SID}`,{waitUntil:'load'}); await page.locator('[data-testid=studio-stage]').waitFor({timeout:60000}); await page.waitForTimeout(2500); };
  const selectBig=async()=>{ await page.locator('[data-testid=version-fold-trigger]').first().click().catch(()=>{}); await page.waitForTimeout(800);
    const tile=page.locator('[data-testid=version-tile]').filter({hasText:BIG_TILE}).first(); await tile.locator('[data-testid=version-thumb]').first().click({force:true}); await page.locator('[data-testid=stage-3d]').waitFor({timeout:30000}); };
  // ---- A: big GLB
  const t0=Date.now(); await open(); await selectBig();
  let ready=false;
  for(let i=0;i<120;i++){ await page.waitForTimeout(2000); const loading=await page.locator('[data-testid=model-viewer-loading]').count(); const err=await page.locator('[data-testid=model-viewer-error]').count(); const st=await statsText();
    if(err){res.checks.bigErr=true;break;} if(!loading && /\d/.test(st) && /\d+x\d+/.test(st)){ready=true;break;} }
  res.checks.bigReadySeconds=((Date.now()-t0)/1000).toFixed(1); res.checks.bigReady=ready; res.checks.bigStats=(await statsText()).replace(/\n/g,' ');
  res.checks.bigGlbRequested=res.glb.some(x=>x.includes(BIG)&&x.includes('len='+BIGSIZE));
  await page.waitForTimeout(2500); await shot('A-big-glb');
  // ---- B: rotate / zoom / reset
  const vb=await page.locator('[data-testid=stage-3d] model-viewer').boundingBox();
  const cx=vb.x+vb.width/2, cy=vb.y+vb.height/2;
  const o0=await orbit();
  await page.mouse.move(cx,cy); await page.mouse.down(); await page.mouse.move(cx+220,cy+40,{steps:12}); await page.mouse.up(); await page.waitForTimeout(600);
  const o1=await orbit(); await shot('B-rotated');
  await page.mouse.move(cx,cy); await page.mouse.wheel(0,-600); await page.waitForTimeout(900); const o2=await orbit(); await shot('B-zoom-in');
  await page.mouse.wheel(0,1200); await page.waitForTimeout(900); const o3=await orbit(); await shot('B-zoom-out');
  res.checks.orbit={start:o0,afterDrag:o1,afterZoomIn:o2,afterZoomOut:o3};
  res.checks.rotateChanged = !!(o0&&o1&&Math.abs(o1[0]-o0[0])>0.2);
  res.checks.zoomChanged = !!(o1&&o2&&o2[2]<o1[2]-0.01) && !!(o3&&o3[2]>o2[2]+0.01);
  await page.locator('[data-testid=model-viewer-reset]').click(); await page.waitForTimeout(1500); const o4=await orbit(); res.checks.orbitAfterReset=o4; await shot('B-reset');
  // ---- C: view modes
  for (const m of ['clay','wire','texture']) { await page.locator(`[data-testid=stage-3d-view-${m}]`).click(); await page.waitForTimeout(1800); await shot('C-view-'+m); }
  res.checks.statsAfterViews=(await statsText()).replace(/\n/g,' ');
  // ---- D: download GLB
  await page.getByRole('tab',{name:zh?/格式/:/Formats/i}).click(); await page.waitForTimeout(800);
  const dlLink=page.locator(`a[href*="${BIG}"]`).first();
  const full=(`${loc}:${W}x${H}`===FULL);
  try{
    const [dl]=await Promise.all([page.waitForEvent('download',{timeout:240000}), dlLink.click()]);
    const p=await dl.path(); const st=fs.statSync(p); let sha=null;
    if(full||true){ const h=crypto.createHash('sha256'); await new Promise((ok,ko)=>fs.createReadStream(p).on('data',d=>h.update(d)).on('end',ok).on('error',ko)); sha=h.digest('hex'); }
    res.checks.glbDownload={name:dl.suggestedFilename(),bytes:st.size,sizeOk:st.size===BIGSIZE,shaOk:sha===BIGSHA,failure:await dl.failure()};
    fs.rmSync(p,{force:true});
  }catch(e){ res.checks.glbDownload={error:String(e.message).slice(0,200)}; }
  await shot('D-after-download');
  if(full){ try{
    const [dl]=await Promise.all([page.waitForEvent('download',{timeout:300000}), page.getByRole('tabpanel').locator('[data-testid=model-archive-link]').first().click()]);
    const p=await dl.path(); res.checks.zipDownload={name:dl.suggestedFilename(),bytes:fs.statSync(p).size,failure:await dl.failure()}; fs.rmSync(p,{force:true});
  }catch(e){ res.checks.zipDownload={error:String(e.message).slice(0,200)}; } }
  console.log('A-D',JSON.stringify(res.checks)); phase='E-negative';
  // ---- E: negative: corrupted embedded texture / truncated body on the small model
  const meta=await (await ctx.request.get(`${base}/api/v1/tasks/${SMALL}`)).json();
  const glbA=meta.assets.find(a=>a.variant==='glb'); const url=base+glbA.url;
  const orig=Buffer.from(await (await ctx.request.get(url)).body());
  const corrupt=Buffer.from(orig); const sig=Buffer.from([0x89,0x50,0x4e,0x47,0x0d,0x0a,0x1a,0x0a]); let idx=corrupt.indexOf(sig); res.checks.negPngFound=idx>=0;
  if(idx>=0){ for(let i=idx+60;i<Math.min(idx+4000,corrupt.length);i++) corrupt[i]^=0xff; }
  const trunc=orig.subarray(0,Math.floor(orig.length*0.6));
  const selectSmall=async()=>{ const t=page.locator('[data-testid=version-tile]').filter({hasText:SMALL_TILE}).first(); await t.locator('[data-testid=version-thumb]').first().click({force:true}); };
  for (const [name,body] of [['corrupt-texture',corrupt],['truncated',trunc]]) {
    let active=true; const h=async route=>{ if(active) await route.fulfill({status:200,contentType:'model/gltf-binary',body}); else await route.continue(); };
    await page.route(u=>u.pathname===new URL(url).pathname,h);
    await open(); await selectSmall(); await page.waitForTimeout(500);
    let seen=false; for(let i=0;i<30;i++){ if(await page.locator('[data-testid=model-viewer-error]').count()){seen=true;break;} await page.waitForTimeout(1000); }
    const errTxt=seen?(await page.locator('[data-testid=model-viewer-error]').innerText()).replace(/\n/g,' | '):null;
    const cap=await page.locator('[data-testid=stage-capture]').first().isDisabled().catch(()=>null);
    const rt=await page.locator('[data-testid=model-viewer-retry]').count();
    res.checks['neg-'+name]={errorShown:seen,text:errTxt,stats:(await statsText()).replace(/\n/g,' '),captureButtonDisabled:cap,retryButton:rt,loadingStuck:await page.locator('[data-testid=model-viewer-loading]').count()};
    await shot('E-'+name); console.log('phase E',name,JSON.stringify(res.checks['neg-'+name]));

    if(name==='corrupt-texture'){ active=false; await page.locator('[data-testid=model-viewer-retry]').click().catch(()=>{}); let ok=false; for(let i=0;i<30;i++){ await page.waitForTimeout(1000); if(!(await page.locator('[data-testid=model-viewer-error]').count()) && !(await page.locator('[data-testid=model-viewer-loading]').count()) && /\d+x\d+/.test(await statsText())){ok=true;break;} } res.checks.retryRecovered=ok; await shot('E-retry-recovered'); }
    else { active=false; }
  }
  // Every required check must hold; any failed check or download error makes the whole run exit non-zero.
  const c=res.checks, need={
    bigLoaded: c.bigReady===true && !c.bigErr, bigGlbRequested: c.bigGlbRequested===true,
    rotate: c.rotateChanged===true, zoom: c.zoomChanged===true,
    glbDownload: !!c.glbDownload && !c.glbDownload.error && !c.glbDownload.failure && c.glbDownload.sizeOk===true && c.glbDownload.shaOk===true,
    zipDownload: !full || (!!c.zipDownload && !c.zipDownload.error && !c.zipDownload.failure && c.zipDownload.bytes>0),
    negCorruptTexture: !!c['neg-corrupt-texture']?.errorShown, negTruncated: !!c['neg-truncated']?.errorShown, retryRecovered: c.retryRecovered===true };
  for (const [k,v] of Object.entries(need)) if(!v) failures.push(`${tag}: ${k}`);
  res.checks.verdict=need;
  report.push(res); await b.close();
  fs.writeFileSync(path.join(OUT,'results.json'),JSON.stringify(report,null,1));
  console.log(tag,'done');
}
console.log(JSON.stringify(report,null,1));
if(failures.length){ console.error('FAILED checks:\n  '+failures.join('\n  ')); process.exit(1); }
console.log('all required checks passed');
