// Mounted, loopback-only regression for format reasons and opt-in keyboard re-entry.
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { readFile, mkdir, mkdtemp, writeFile } from 'node:fs/promises';
import { homedir, tmpdir } from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
const root = fileURLToPath(new URL('..', import.meta.url));
const require = createRequire(path.join(root, 'package.json'));
const { createServer } = await import(require.resolve('vite'));
const pw = require(process.env.PLAYWRIGHT_MODULE_PATH || path.join(homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'));
const { application } = JSON.parse(await readFile(path.join(root, 'internal/catalog/default_catalog.json'), 'utf8'));
const schema = { ...application, delivery_formats: application.model_delivery.formats.map(({id,label}) => ({id,label})) };
delete schema.runtime.configuration; delete schema.chat.harness.context_template;
const reason = '当前来源不满足这个格式的转换要求；这里保留完整的禁止原因。'.repeat(5);
const wire = {task_id:'fixture-task', applicable:true, archive_available:false, formats:schema.delivery_formats.map(f=>({...f,status:'unavailable',reason_text:reason}))};
const output = process.env.FORMAT_TOOLTIP_EVIDENCE_DIR || await mkdtemp(path.join(tmpdir(),'format-tooltip-'));
assert(path.isAbsolute(output)); await mkdir(output,{recursive:true,mode:0o700});
const id = '\0format-tooltip-regression';
const server = await createServer({configFile:path.join(root,'vite.go-web.config.mjs'),cacheDir:path.join(output,'vite-cache'),logLevel:'error',server:{host:'127.0.0.1',port:0},plugins:[{
 name:'format-tooltip-regression',resolveId(value){if(value==='virtual:format-tooltip-regression')return id;},load(value){if(value!==id)return;return `
 import React from 'react';import{createRoot}from'react-dom/client';import '${root}/src/app/globals.css';
 import{ProductSchemaProvider}from'${root}/src/components/product-schema-provider.tsx';import{ModelFormatsView}from'${root}/src/components/workspace/model-formats.tsx';import{parseModelFormatCoverage}from'${root}/src/lib/workspace/model-formats.ts';import{HintTooltip}from'${root}/src/components/ui/hint-tooltip.tsx';
 window.focusCalls=[];const schema=${JSON.stringify(schema)},reason=${JSON.stringify(reason)};const mode=new URL(location.href).searchParams.get('mode');
 const trigger={tabIndex:0,'data-testid':'generic-trigger',onFocus:e=>{window.focusCalls.push({visible:e.currentTarget.matches(':focus-visible')});if(mode==='prevent')e.preventDefault();}};
 const generic=React.createElement(HintTooltip,{name:mode==='empty'?'':'Generic',reason,reopenOnKeyboardFocus:mode!=='default',triggerProps:trigger},'Generic target');
 document.documentElement.classList.add('dark');createRoot(document.getElementById('root')).render(React.createElement(ProductSchemaProvider,{initialSchema:schema},React.createElement('main',{style:{width:300,padding:8}},React.createElement('button',{'data-testid':'before'},'Before'),mode==='format'?React.createElement(ModelFormatsView,{schema,coverage:parseModelFormatCoverage(${JSON.stringify(wire)}),phase:'ready',busy:false,layout:'pinned'}):generic,React.createElement('button',{'data-testid':'after'},'After'))));
 `;},transformIndexHtml(html){return html.replace('{{.Schema}}',JSON.stringify(schema)).replace('src="/src/main.tsx"','src="/@id/virtual:format-tooltip-regression"');}
}]});
const results=[];await server.listen();const base=server.resolvedUrls.local[0];
const frame=page=>page.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));
async function tabTo(page,target){for(let i=0;i<32;i++){await page.keyboard.press('Tab');if(await target.evaluate(e=>e===document.activeElement))return;}assert.fail('keyboard did not reach trigger');}
try{for(const engine of ['chromium','webkit']){const browser=await pw[engine].launch({headless:true});try{for(const [width,height] of [[1366,768],[1440,900],[1920,1080]]){const page=await browser.newPage({viewport:{width,height},serviceWorkers:'block'});page.setDefaultTimeout(30000);const forbidden=[],errors=[];page.on('pageerror',()=>errors.push('runtime_error'));await page.route('**/*',route=>{const req=route.request(),url=new URL(req.url());if(url.origin!==new URL(base).origin||req.method()!=='GET'){forbidden.push('external_or_write');return route.abort();}if(url.pathname===schema.runtime.http.routes.product_schema)return route.fulfill({json:{schema}});if(url.pathname===schema.runtime.http.legacy_routes.version)return route.fulfill({json:{version:'fixture'}});if(url.pathname.startsWith('/api/')){forbidden.push('unexpected_api');return route.abort();}return route.continue();});
 try{for(const mode of ['format','default','prevent','empty','mouse']){await page.goto(base+'?mode='+mode);const before=page.getByTestId('before');await before.waitFor();const target=mode==='format'?page.getByTestId('model-format-glb').getByRole('img'):page.getByTestId('generic-trigger');const tip=page.getByRole('tooltip').filter({hasText:reason}).filter({has:page.getByTestId('hint-name').getByText(mode==='format'?schema.delivery_formats.find(f=>f.id==='glb').label:'Generic',{exact:true})});
 if(mode==='empty'){await tabTo(page,target);await frame(page);assert.equal(await tip.count(),0);assert.equal((await page.evaluate(()=>window.focusCalls)).length,1);results.push({engine,width,mode,passed:true});continue;}
 // Preserve the failing real sequence: hover, enter bubble, Escape, then first Tab.
 await target.hover();await tip.waitFor({state:'visible'});await tip.hover();assert.equal(await tip.getByTestId('hint-reason').innerText(),reason);await page.keyboard.press('Escape');await tip.waitFor({state:'hidden'});await page.mouse.move(width-5,5);
 if(mode==='mouse'){const box=await target.boundingBox();await page.mouse.click(box.x+box.width/2,box.y+box.height/2);await frame(page);const calls=await page.evaluate(()=>window.focusCalls);assert.deepEqual(calls,[{visible:false}]);assert.equal(await target.getAttribute('aria-describedby'),null);results.push({engine,width,mode,passed:true});continue;}
 await tabTo(page,target);await frame(page);assert(await target.evaluate(e=>e.matches(':focus-visible')));
 if(mode==='default'||mode==='prevent'){assert.equal(await tip.count(),0);assert.equal((await page.evaluate(()=>window.focusCalls)).length,1);if(mode==='default'){await page.keyboard.press('Tab');await page.keyboard.press('Shift+Tab');await tip.waitFor({state:'visible'});}results.push({engine,width,mode,passed:true});continue;}
 await tip.waitFor({state:'visible'});assert.equal(await tip.getByTestId('hint-reason').innerText(),reason);assert(await target.evaluate(e=>e.getAttribute('aria-describedby')?.split(/\s+/).some(id=>document.getElementById(id)?.getAttribute('role')==='tooltip')));await page.screenshot({path:path.join(output,`${engine}-${width}-keyboard.png`)});
 // Escape remains closed while focus stays put; only a new real focus opens it.
 await page.keyboard.press('Escape');await tip.waitFor({state:'hidden'});await frame(page);assert(await target.evaluate(e=>e===document.activeElement));assert.equal(await tip.count(),0);await page.keyboard.press('Tab');await page.keyboard.press('Shift+Tab');await tip.waitFor({state:'visible'});assert.equal(await tip.getByTestId('hint-reason').innerText(),reason);results.push({engine,width,mode,passed:true});
 }assert.deepEqual(forbidden,[]);assert.deepEqual(errors,[]);}finally{await page.close();}}}finally{await browser.close();}}}finally{await server.close();await writeFile(path.join(output,'report.json'),JSON.stringify({scope:'mounted loopback fixture only; no deployed or Generation claims',results},null,2),{mode:0o600});}
assert.equal(results.length,30);console.log(JSON.stringify({passed:true,cases:results.length,output,externalOrProviderRequests:0}));
