// Real Chromium/WebKit UI regression with loopback-only fixture APIs.
// No Provider, Generation, approval, or non-loopback request is permitted.
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { mkdir, readFile } from 'node:fs/promises';
import { homedir, tmpdir } from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const root = fileURLToPath(new URL('..', import.meta.url));
const require = createRequire(path.join(root, 'package.json'));
const { createServer } = await import(require.resolve('vite'));
const pw = require(process.env.PLAYWRIGHT_MODULE_PATH || path.join(homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'));
const { application: schema } = JSON.parse(await readFile(path.join(root, 'internal/catalog/default_catalog.json'), 'utf8'));
delete schema.runtime.configuration;
const label = (key) => {
  const value = schema.content.messages[key];
  assert.equal(typeof value, 'string', `missing schema message ${key}`);
  return value;
};
const nativeQuote = {
  version: 1, unit: 'hyper3d.credit', unit_label: 'Hyper3D credits',
  base_amount: '0.5', extra_amount: '2.5', total_amount: '3',
  components: [{ id: 'extreme_texture', label: 'Extreme texture', amount: '2.5' }],
};
const upperQuote = { ...nativeQuote, base_amount: '0.5', extra_amount: '3', total_amount: '3.5',
  components: [{ id: 'extreme_texture', label: 'Extreme texture', amount: '3' }] };
const offering = {
  id: 'fixture/native-text-to-3d', revision: 'r1', provider_id: 'hyper3d', provider_name: 'Hyper3D',
  model_id: 'rodin-gen2.5', model_name: 'Rodin Gen-2.5', capability: 'text-to-3d', available: true,
  credits_metered: true, price_mode: 'native', default_native_quote: nativeQuote, upper_native_quote: upperQuote,
  parameters: [{ name: 'quality', label: 'Quality', type: 'select', default: 'standard', options: [{ value: 'standard', label: 'Standard' }, { value: 'detail', label: 'Detail' }] }],
};
const virtualID = '\0native-pricing-fixture';
const fixtureApp = `
  import React, { StrictMode } from 'react';
  import { createRoot } from 'react-dom/client';
  import '${path.join(root, 'src/app/globals.css')}';
  import { ProductSchemaProvider } from '${path.join(root, 'src/components/product-schema-provider.tsx')}';
  import { ApprovalCard } from '${path.join(root, 'src/components/studio/approval-card.tsx')}';
  import { ParamsDomainSection } from '${path.join(root, 'src/components/studio/params-popover.tsx')}';
  import { buildGenerationParams, effectiveOfferingValues, estimateCredits } from '${path.join(root, 'src/lib/workspace/generation-params.ts')}';
  import { formatNativePrice, formatPriceTotal } from '${path.join(root, 'src/lib/pricing.ts')}';
  import { sessionPriceTotalsText } from '${path.join(root, 'src/lib/chat/approval-modes.ts')}';
  import AccountPage from '${path.join(root, 'src/app/account/page.tsx')}';
  import { ThreeViewCardView } from '${path.join(root, 'src/components/studio/three-view-card.tsx')}';
  import { parseThreeViewGroup } from '${path.join(root, 'src/lib/workspace/three-view.ts')}';
  import { createChatBrowserClient, paidStepFromTaskRegistered } from '${path.join(root, 'src/lib/chat/browser-client.ts')}';
  import { useChatStore } from '${path.join(root, 'src/store/chat-store.ts')}';
  import { PaidStepNotes } from '${path.join(root, 'src/components/chat/paid-step-notes.tsx')}';
  const schema = window.__ART_AI_PRODUCT_SCHEMA__;
  const offering = window.__NATIVE_OFFERING__;
  const quote = window.__NATIVE_QUOTE__;
  const model = buildGenerationParams([offering]);
  const values = effectiveOfferingValues(offering, {});
  const estimate = estimateCredits(offering, values);
  const generation = { offering, model, values, estimate, setValue: () => {}, flush: async () => {} };
  const params = { ...generation, domain: 'model-3d', modelName: offering.model_name, ready: true, allowed: true,
    restrictedReason: '', failed: false, saveError: null, offerings: [offering], selectOffering: () => {}, nextTurn: generation };
  const approvalModel = { requestId: 'fixture-request', source: 'detached', status: 'pending', capability: 'text-to-3d',
    model: offering.model_id, provider: offering.provider_name, providerId: offering.provider_id, credits: 0, extra: null,
    prompt: 'fixture prompt', inputs: [], sourceTaskId: '', expiresAt: '2026-10-09T01:00:00Z', priceMode: 'native', nativeQuote: quote };
  const half = {version:1,unit:'vendor.credit',unit_label:'Vendor credits',base_amount:'0.5',extra_amount:'0',total_amount:'0.5'};
  const views = parseThreeViewGroup({run_id:'fixture-views',slots:[
    {key:'front',label:'Front',status:'not_started',startable:true,credits_metered:true,native_quote:half,price_source:'default'},
    {key:'side',label:'Side',status:'failed',retryable:true,task_id:'frozen-second',credits_metered:true,native_quote:{...half,base_amount:'1',total_amount:'1'},price_source:'frozen'},
    {key:'back',label:'Back',status:'not_started',startable:true,credits_metered:true,price_mode:'native',native_quote:null}
  ],model_source:{slot:'front',available:false}});
  function LiveNotes() {
    const buffer=useChatStore(s=>s.streamingBuffers['native-stream-fixture']);
    React.useEffect(()=>{
      useChatStore.getState().initStreaming('native-stream-fixture',{content:'fixture'});
      const source={onmessage:null,onerror:null,close(){}};
      const stop=createChatBrowserClient(schema,{eventSourceFactory:()=>source}).subscribe('native-stream-fixture',{onEvent:event=>{
        if(event.type==='taskRegistered'){const step=paidStepFromTaskRegistered(event);if(step)useChatStore.getState().addStreamPaidStep('native-stream-fixture',step);}
      }});
      window.__NATIVE_EMIT__=(unknown)=>source.onmessage?.({data:JSON.stringify({[schema.chat.stream.event_fields.type]:schema.chat.stream.event_types.task_registered,[schema.chat.stream.event_fields.task_id]:unknown?'unknown-live':'known-live',[schema.chat.stream.event_fields.approval_source]:'policy',native_quote:unknown?null:half})});
      return stop;
    },[]);
    return React.createElement('section',{'data-testid':'live-native-stream-notes'},
      React.createElement('button',{'data-testid':'emit-native-price',onClick:()=>window.__NATIVE_EMIT__(false)},'Receive fixture event'),
      React.createElement('button',{'data-testid':'emit-unknown-price',onClick:()=>window.__NATIVE_EMIT__(true)},'Receive unknown fixture event'),
      React.createElement(PaidStepNotes,{steps:buffer?.paidSteps??[],creditsText:n=>String(n),nativeText:amount=>schema.content.messages['ui.pricing.frozen_estimate'].replace('{amount}',amount),unknownPriceText:schema.content.messages['ui.pricing.unknown'],autoApprovedText:schema.content.messages['ui.workspace.approval.auto_approved_note']}));
  }
  function Fixture() {
    const state = window.__NATIVE_STATE__;
    const totals = [{ unit: 'hyper3d.credit', unit_label: 'Hyper3D credits', amount: '3' },
      { unit: 'legacy.credits', unit_label: 'Ignored backend label', amount: '7' }];
    return React.createElement('main', { className: 'mx-auto flex max-w-5xl flex-col gap-5 p-6', 'data-testid': 'native-pricing-fixture' },
      React.createElement('h1', null, 'Native pricing UI fixture'),
      React.createElement('section', { className: 'rounded-lg border p-4', 'data-testid': 'shared-native-price' },
        React.createElement('h2', null, '共享原生报价'),
        React.createElement('p', { 'data-testid': 'shared-native-total' }, formatNativePrice(quote.total_amount, quote.unit_label)),
        React.createElement('p', { 'data-testid': 'mixed-unit-totals' }, sessionPriceTotalsText(schema, totals)),
        React.createElement('p', { 'data-testid': 'legacy-zero' }, schema.content.messages['ui.pricing.legacy_estimate'] + ': 0')),
      React.createElement('section', { className: 'rounded-lg border p-4', 'data-testid': 'native-price-panel' },
        React.createElement(ParamsDomainSection, { params, hasSession: true, skill: null })),
      React.createElement(ApprovalCard, { model: { ...approvalModel, nativeQuote: state === 'unknown' ? null : quote }, busy: false,
        now: Date.parse('2026-10-09T00:00:00Z'), onDecide: approved => { window.__NATIVE_DECISIONS__.push(approved); }, onRerequest: () => {} }),
      React.createElement('section',{'data-testid':'native-account'},React.createElement(AccountPage)),
      React.createElement(ThreeViewCardView,{schema,group:views,busy:null,thumbnail:()=>'',onAdvance:slot=>{window.__NATIVE_VIEW_ACTIONS__=(window.__NATIVE_VIEW_ACTIONS__??[]).concat(slot.key)},onView:()=>{},onToThreeD:()=>{}}),
      React.createElement(LiveNotes),
      React.createElement('button', { type: 'button', 'data-testid': 'refresh-state', onClick: () => window.location.reload() }, 'Refresh'));
  }
  document.documentElement.classList.add('dark');
  createRoot(document.getElementById('root')).render(React.createElement(StrictMode, null,
    React.createElement(ProductSchemaProvider, { initialSchema: schema }, React.createElement(Fixture))));
`;
const output = process.env.NATIVE_PRICING_UI_EVIDENCE_DIR || path.join(tmpdir(), 'art187188-native-pricing-ui');
assert(path.isAbsolute(output), 'evidence directory must be absolute');
await mkdir(output, { recursive: true, mode: 0o700 });

const server = await createServer({
  configFile: false,
  root: path.join(root, 'web'),
  publicDir: path.join(root, 'public'),
  cacheDir: path.join(output, '.vite'),
  resolve: { alias: { '@': path.join(root, 'src') } },
  logLevel: 'error',
  server: { host: '127.0.0.1', port: 0 },
  plugins: [{
    name: 'native-pricing-fixture',
    resolveId(id) { if (id === 'virtual:native-pricing-fixture') return virtualID; },
    load(id) { return id === virtualID ? fixtureApp : null; },
    transformIndexHtml(html) { return html.replace('{{.Schema}}', JSON.stringify(schema)).replace('src="/src/main.tsx"', 'src="/@id/virtual:native-pricing-fixture"'); },
  }],
});
await server.listen();
const base = server.resolvedUrls.local[0];
const report = [];
try {
  for (const engine of ['chromium', 'webkit']) {
    const browser = await pw[engine].launch({ headless: true });
    try {
      for (const { width, height } of [{ width: 1366, height: 768 }, { width: 1440, height: 900 }, { width: 1920, height: 1080 }]) {
        const viewport = `${width}x${height}`;
        const page = await browser.newPage({ viewport: { width, height }, serviceWorkers: 'block' });
        const state = { external: [], writes: [] };
        page.on('pageerror', (error) => { state.external.push(`pageerror:${error.message}`); console.error(error); });
        await page.addInitScript(({ schema: initialSchema, offering: fixtureOffering, quote: fixtureQuote }) => {
          window.__ART_AI_PRODUCT_SCHEMA__ = initialSchema;
          window.__NATIVE_OFFERING__ = fixtureOffering;
          window.__NATIVE_QUOTE__ = fixtureQuote;
          window.__NATIVE_STATE__ = new URLSearchParams(location.search).get('state') || 'valid';
          window.__NATIVE_DECISIONS__ = [];
        }, { schema, offering, quote: nativeQuote });
        await page.route('**/*', async (route) => {
          const request = route.request();
          const url = new URL(request.url());
          if (url.origin !== new URL(base).origin) { state.external.push(url.origin); return route.abort(); }
          if (!url.pathname.startsWith('/api/')) {
            if (request.method() !== 'GET') state.writes.push(`${request.method()} ${url.pathname}`);
            return route.continue();
          }
          const json = (value) => route.fulfill({ json: value });
          if (request.method() !== 'GET') { state.writes.push(`${request.method()} ${url.pathname}`); return route.fulfill({ status: 204 }); }
          if (url.pathname === schema.runtime.http.routes.product_schema) return json({ schema });
          if (url.pathname === schema.runtime.http.legacy_routes.auth_session) return json({ user: { id:'fixture-owner',name:'Fixture',email:'fixture@invalid.test' }, isAdmin: false });
          if (url.pathname === schema.runtime.http.routes.account_usage) return json({user_id:'fixture-owner',total_tasks:1,succeeded_tasks:1,failed_tasks:0,price_totals:[{unit:'vendor.credit',unit_label:'Vendor credits',amount:'0.5'}],last_active_at:null});
          if (url.pathname === schema.runtime.http.routes.account_resources) return json({tasks:[],next_cursor:'',uploads:{data:[],total:0,page:1,page_size:25}});
          if (url.pathname === schema.runtime.http.legacy_routes.provider_balance) return json({ accounts: [] });
          if (url.pathname === schema.runtime.http.routes.offerings) return json({ offerings: [offering] });
          return json({});
        });
        await page.goto(`${base}?state=valid`);
        await page.waitForTimeout(1000);
        if (await page.getByTestId('shared-native-total').count() === 0) console.error('FIXTURE BODY', (await page.locator('body').innerText()).slice(0, 500), state.external);
        await page.getByTestId('shared-native-total').getByText('3 Hyper3D credits').waitFor();
        const frozenText = await page.getByTestId('studio-approval-native-price').innerText();
        assert(frozenText.includes('0.5 Hyper3D credits') && frozenText.includes('2.5 Hyper3D credits'), 'frozen approval must show base and component amounts');
        assert(!frozenText.includes('{amount}'), 'frozen approval must not expose Schema interpolation tokens');
        const aggregateText = await page.getByTestId('mixed-unit-totals').innerText();
        assert(aggregateText.includes('3 Hyper3D credits') && aggregateText.includes('7'), 'mixed-unit aggregates must stay separated');
        assert.equal(await page.getByTestId('legacy-zero').innerText(), `${label('ui.pricing.legacy_estimate')}: 0`);
        await page.getByTestId('studio-approval-native-price').waitFor();
        await page.getByTestId('studio-approval-approve').focus();
        await page.keyboard.press('Enter');
        await page.waitForFunction(() => window.__NATIVE_DECISIONS__.length === 1 && window.__NATIVE_DECISIONS__[0] === true);
        await page.getByTestId('refresh-state').click();
        await page.getByTestId('studio-approval-native-price').waitFor();
        assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true, `horizontal overflow at ${engine}/${width}`);
        assert.deepEqual(state.external, [], `external requests/page errors at ${engine}/${width}`);
        assert.deepEqual(state.writes, [], `unexpected write at ${engine}/${width}`);
        await page.getByTestId('native-account').getByText(`${label('ui.pricing.totals_heading')}: 0.5 Vendor credits`, { exact: true }).waitFor();
        const front=page.locator('[data-slot="front"]'); const side=page.locator('[data-slot="side"]'); const back=page.locator('[data-slot="back"]');
        await front.getByText('默认估算：0.5 Vendor credits',{exact:true}).waitFor();
        await side.getByText('已冻结估算：1 Vendor credits',{exact:true}).waitFor();
        assert.equal(await back.getByTestId('three-view-start').isDisabled(),true);
        await front.getByTestId('three-view-start').focus(); await page.keyboard.press('Enter');
        await page.waitForFunction(()=>window.__NATIVE_VIEW_ACTIONS__?.includes('front'));
        await page.getByTestId('emit-native-price').click();
        await page.getByTestId('live-native-stream-notes').getByText(/已冻结估算：0.5 Vendor credits/).waitFor();
        assert((await page.getByTestId('live-native-stream-notes').innerText()).includes(label('ui.workspace.approval.auto_approved_note')));
        await page.getByTestId('emit-unknown-price').click();
        await page.getByTestId('live-native-stream-notes').getByText(`${label('ui.pricing.unknown')} · ${label('ui.workspace.approval.auto_approved_note')}`,{exact:true}).waitFor();
        assert.deepEqual(state.external,[]); assert.deepEqual(state.writes,[]);
        await page.screenshot({ path: path.join(output, `${engine}-${viewport}-native-valid.png`), fullPage: true });
        report.push(`${engine}-${viewport}: valid unit price + actual account page + native start/retry + live SSE fee + keyboard + refresh + no overflow`);

        await page.goto(`${base}?state=unknown`);
        await page.getByTestId('studio-approval-native-price-unknown').waitFor();
        assert.equal(await page.getByTestId('studio-approval-approve').isDisabled(), true);
        const unknownApprovalText = await page.getByTestId('studio-approval-card').innerText();
        assert.doesNotMatch(unknownApprovalText, /(?:^|\s)0\s*(?:积分|credits)/i);
        assert(!unknownApprovalText.includes('{amount}'), 'unknown approval must not expose Schema interpolation tokens');
        assert(unknownApprovalText.includes(`${label('ui.pricing.frozen_estimate').replace('{amount}', label('ui.pricing.unknown'))}`), 'unknown approval must show a formatted frozen-estimate label');
        await page.getByTestId('shared-native-total').getByText('3 Hyper3D credits').waitFor();
        await page.screenshot({ path: path.join(output, `${engine}-${viewport}-native-unknown.png`), fullPage: true });
        report.push(`${engine}-${viewport}: unknown quote remains distinct from legacy zero`);
        await page.close();
      }
    } finally { await browser.close(); }
  }
} finally { await server.close(); }
for (const result of report) console.log(`PASS ${result}`);
console.log(`Evidence: ${output}`);
