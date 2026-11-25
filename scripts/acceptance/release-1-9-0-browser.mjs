// v1.9.0 发布前的 DEV 浏览器验收（只用 fake 上游，零费用）。
//
// 覆盖（用户可见的界面与行为，每项 PASS/FAIL/SKIP 带截图）：
//   批次 1
//     #586  设置弹窗「生成参数」页没有单次出图张数的加减控件；GPT Image 的 n、Seedream 的组图开关只读显示。
//     #559  版本条点 2D 版本只在舞台查看、不放上画布；从画布删掉该图层后再点（含刷新后）不复活；详情按钮是
//           「插入到画布」，点了才放上画布。3D 版本点击仍在舞台打开，详情按钮仍是「在舞台中打开」。
//     #587  Agent 裁图（fake 对话模型的 crop_image_regions 标记造出）出现在版本条与「全部产出」并标「裁图」，
//           不自动上画布；对话卡片上的裁图详情可「插入到画布」、可拖到画布、可「选用」；用户上传图不能选用。
//     #560  输入框打 @：列表分「画布中」「素材库」两段，「画布」条目第一，标题行不被上下键选中，句柄编号不变。
//   批次 2（目标环境的 Product Schema 没有对应能力时记 SKIP「待批次 2」）
//     #548  设置弹窗 Skills 页：名称加要求「生成」草稿，逐项改后保存；列表显示说明，派生说明标「建议补写」；
//           编辑时各字段分别保存；删除。
//     #548  对话里要三视图，助手消息出现「本轮使用 Skill」标记，刷新后仍在。
//
// 用法（地址与 Agent 专用账号由 release-env.py 注入，凭据只在子进程环境里）：
//   python3 scripts/release-env.py smoke dev-test -- node scripts/acceptance/release-1-9-0-browser.mjs
// 环境变量：
//   R190_ITEMS=586,559,587,560,548-skill,548-chip  只跑其中几项（默认全部）
//   R190_REQUIRE_BATCH2=1  目标环境缺批次 2 能力时记 FAIL 而不是 SKIP（批次 2 部署后用）
//   OUT=<目录>  截图与 results.json 的位置（默认系统临时目录 plinth-acceptance-r190-*，不要写进仓库）
//   PLAYWRIGHT_MODULE_PATH  Playwright 的位置（工作树没有 node_modules 时指向主检出的 node_modules/playwright）
//
// 造数（都在 Agent 专用账号下，每次新建，标题带 r190-<时间戳>）：
//   会话 A：两轮 fake refine_image 出两张 2D、一轮 fake create_model_3d 出一个 3D（若出批准卡就批准，fake 不计费）。
//   会话 B：上传一张 256x192 的双色 PNG，一轮 fake crop_image_regions 从中裁出左右两块（Agent 裁图）。
//   会话 C：空会话，只用来看设置弹窗（会在该会话上选一次 2D 模型）。
//   会话 D（批次 2）：一轮 fake use_skill 三视图。个人 Skill r190-*（结束时删除）。
// 目标环境不是 fake 上游形态（ART_AI_UPSTREAM_SHAPE!=fake）时拒绝运行（退出码 2），不会发起真实生成。
// 不打印口令、Cookie 或签名 URL。最后一行输出 `PASS=n FAIL=n SKIP=n`；有 FAIL 时退出码 1。
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { makePng } from './browser_matrix/fixtures.mjs';
import { resolvePlaywright } from './browser_matrix/lib.mjs';

const base = (process.env.BASE || '').replace(/\/+$/, '');
const missing = ['BASE', 'AGENT_TEST_USERNAME', 'AGENT_TEST_PASSWORD'].filter((name) => !process.env[name]);
if (missing.length) {
  console.error(`missing environment: ${missing.join(', ')}\nrun through: python3 scripts/release-env.py smoke dev-test -- node ${path.relative(process.cwd(), new URL(import.meta.url).pathname)}`);
  process.exit(2);
}
if (process.env.ART_AI_UPSTREAM_SHAPE !== 'fake') {
  console.error(`refusing to run: the target upstream shape is "${process.env.ART_AI_UPSTREAM_SHAPE || 'unknown'}", not fake; the seed turns would start real generations`);
  process.exit(2);
}

const ITEMS = (process.env.R190_ITEMS || '586,559,587,560,548-skill,548-chip').split(',').map((item) => item.trim()).filter(Boolean);
const REQUIRE_BATCH2 = process.env.R190_REQUIRE_BATCH2 === '1';
const tag = `r190-${new Date().toISOString().replace(/[-:T]/g, '').slice(0, 14)}`;
const OUT = process.env.OUT || fs.mkdtempSync(path.join(os.tmpdir(), 'plinth-acceptance-r190-'));
fs.mkdirSync(OUT, { recursive: true });
const ORIGIN = { Origin: new URL(base).origin };

const results = [];
const record = (status, item, name, detail = '') => {
  results.push({ status, item, name, detail });
  console.log(`${status}: ${item} ${name}${detail ? ` — ${detail}` : ''}`);
};
const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

const { chromium } = resolvePlaywright();
const browser = await chromium.launch();
const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 }, locale: 'zh-CN', serviceWorkers: 'block' });
const page = await ctx.newPage();
const pageErrors = [];
page.on('pageerror', (error) => pageErrors.push(error.message));
page.on('dialog', (dialog) => dialog.dismiss().catch(() => {}));

// ---------------------------------------------------------------- API helpers (share the browser's cookies)
async function api(method, route, data) {
  for (let attempt = 0; attempt < 4; attempt += 1) {
    const response = await ctx.request.fetch(base + route, { method, data, headers: ORIGIN });
    if (response.status() === 429) { await sleep(10000); continue; }
    let body = {};
    try { body = await response.json(); } catch {}
    return [response.status(), body];
  }
  return [429, {}];
}
const messagesOf = async (sid) => {
  const [, body] = await api('GET', `/api/chat/sessions/${sid}/messages`);
  return Array.isArray(body) ? body : (body.messages || body.data || []);
};
const finalAssistantCount = async (sid) => (await messagesOf(sid))
  .filter((message) => message.role === 'assistant' && !String(message.content || '').startsWith('__GENERATING__')).length;
const sessionTasks = async (sid) => {
  const [, body] = await api('GET', `/api/v1/tasks?session_id=${sid}&limit=50`);
  return body.tasks || body.data || [];
};
async function waitFor(check, seconds, step = 1000) {
  const end = Date.now() + seconds * 1000;
  while (Date.now() < end) {
    const value = await check();
    if (value) return value;
    await sleep(step);
  }
  return null;
}
async function createSession(title) {
  const [status, body] = await api('POST', '/api/chat/sessions', { title });
  if (![200, 201].includes(status) || !body.id) throw new Error(`create session ${status}`);
  return body.id;
}
/** One chat turn through the API; retries while the previous turn still owns the session; waits for the final reply. */
async function turn(sid, content, extra = {}) {
  const before = await finalAssistantCount(sid);
  let status = 0;
  for (let attempt = 0; attempt < 90; attempt += 1) {
    [status] = await api('POST', `/api/chat/sessions/${sid}/messages`, { content, creative_domain: 'image', ...extra });
    if (status !== 409) break;
    await sleep(1000);
  }
  if (![200, 201, 202].includes(status)) throw new Error(`send message ${status}`);
  if (!await waitFor(async () => (await finalAssistantCount(sid)) > before, 120)) throw new Error('the turn produced no final reply in 120 s');
}
async function waitTasks(sid, predicate, seconds = 180) {
  return waitFor(async () => {
    const tasks = await sessionTasks(sid);
    return predicate(tasks) ? tasks : null;
  }, seconds, 1500);
}

let schemaCache = null;
async function publicSchema() {
  if (!schemaCache) {
    const [, body] = await api('GET', '/api/v1/schema');
    schemaCache = body.schema || body;
  }
  return schemaCache;
}
/** A Schema message by key, read from the injected Schema of the open page (same source the UI uses). */
async function text(key) {
  const value = await page.evaluate((name) => window.__ART_AI_PRODUCT_SCHEMA__?.content?.messages?.[name] ?? '', key);
  if (!value) throw new Error(`Schema message ${key} missing on the page`);
  return value;
}

// ---------------------------------------------------------------- browser helpers
let shotIndex = 0;
async function shot(item, name) {
  const file = path.join(OUT, `${String(++shotIndex).padStart(2, '0')}-${item}-${name}.png`);
  try { await page.screenshot({ path: file }); } catch {}
  return path.basename(file);
}
const tid = (id, scope = page) => scope.locator(`[data-testid="${id}"]`);
async function openSession(sid) {
  await page.goto(`${base}/?session=${sid}`, { waitUntil: 'load' });
  await tid('composer-send').first().waitFor({ timeout: 30000 });
  await page.waitForTimeout(2500);
}
const layerNames = async () => tid('layer-row').evaluateAll((rows) => rows.map((row) => row.innerText.trim()));
const stageVersion = async () => (await tid('stage-version').first().innerText().catch(() => '')).trim();
const tileOf = (key) => page.locator(`[data-testid=version-tile][data-key="${key}"]`);
const settings = tid('composer-settings-dialog');
async function openSettings(tab) {
  if (!await settings.count()) {
    await tid('composer-settings-open').first().click();
    await settings.waitFor({ timeout: 10000 });
  }
  await tid(`composer-settings-tab-${tab}`).click();
  await page.locator(`[data-testid=composer-settings-tab-${tab}][aria-selected=true]`).waitFor({ timeout: 10000 });
  await page.waitForTimeout(800);
}
async function closeSettings() {
  for (let attempt = 0; attempt < 3 && await settings.count(); attempt += 1) {
    await page.keyboard.press('Escape');
    await page.waitForTimeout(400);
  }
}
/** Runs one check; an exception is that check's FAIL with the reason, never a silent pass. */
async function check(item, name, body) {
  try {
    const outcome = await body();
    const { ok, detail = '', skip = false } = typeof outcome === 'object' && outcome !== null ? outcome : { ok: Boolean(outcome) };
    record(skip ? 'SKIP' : ok ? 'PASS' : 'FAIL', item, name, detail);
  } catch (error) {
    const file = await shot(item, 'error');
    record('FAIL', item, name, `${error.message.split('\n')[0]} (screenshot ${file})`);
  }
}

// ---------------------------------------------------------------- login
{
  const response = await ctx.request.post(`${base}/api/auth/login`, {
    data: { username: process.env.AGENT_TEST_USERNAME, password: process.env.AGENT_TEST_PASSWORD }, headers: ORIGIN,
  });
  if (response.status() !== 200) {
    console.error(`Agent 专用账号登录失败：HTTP ${response.status()}`);
    await browser.close();
    process.exit(1);
  }
  const [, version] = await api('GET', '/api/version');
  console.log(`target ${base} version=${version.version || '?'} expect=${process.env.EXPECT_SHA || '?'} out=${OUT} tag=${tag}`);
}

// ---------------------------------------------------------------- seed: session A (two 2D pictures and one 3D model)
const seeded = {};
async function seedSessionA() {
  if (seeded.a) return seeded.a;
  const sid = await createSession(`${tag} 版本条`);
  await turn(sid, '画一个红色方块 [[fake:tool:refine_image {"prompt":"a red cube on a white background"}]]');
  await turn(sid, '再画一个蓝色球 [[fake:tool:refine_image {"prompt":"a blue sphere on a white background"}]]');
  const images = await waitTasks(sid, (tasks) => tasks.filter((task) => task.state === 'SUCCEEDED').length >= 2);
  if (!images) throw new Error('two 2D fake tasks did not succeed');
  // A 3D model for the "3D click is unchanged" half. Manual approval cards are approved: fake upstream, no charge.
  const schema = await publicSchema();
  const decision = schema?.chat?.api?.fields?.decision || 'decision';
  await turn(sid, '做一个简单的木凳 3D 模型 [[fake:tool:create_model_3d {"prompt":"a simple wooden stool"}]]', { creative_domain: 'model-3d' });
  const model = await waitFor(async () => {
    const [, pending] = await api('GET', `/api/v1/chat/sessions/${sid}/pending-approvals`);
    for (const card of (pending.items || []).filter((item) => item.status === 'pending')) {
      await api('PUT', `/api/v1/tasks/grants/${card.request_id}`, { [decision]: 'approve' });
    }
    const tasks = await sessionTasks(sid);
    const threeD = tasks.find((task) => /3d/i.test(String(task.capability || task.offering_id || '')) && task.state === 'SUCCEEDED');
    return threeD || null;
  }, 300, 2000);
  // The 2D pictures in the order the conversation shows them (@img1 first), with their handles.
  const handles = {};
  const order = [];
  for (const message of await messagesOf(sid)) {
    for (const attachment of message.attachments || []) {
      const taskId = attachment.task_id || attachment.taskId;
      if (!taskId || !attachment.handle) continue;
      handles[taskId] = attachment.handle;
      if (attachment.kind === 'image' && !order.includes(taskId)) order.push(taskId);
    }
  }
  const twoD = order.map((id) => ({ id }));
  seeded.a = { sid, first: twoD[0]?.id, second: twoD[1]?.id, model: model?.id || '', handles };
  console.log(`seed A ${sid}: 2D ${seeded.a.first} ${seeded.a.second}, 3D ${seeded.a.model || 'none'}`);
  return seeded.a;
}

// ---------------------------------------------------------------- seed: session B (an upload and two Agent crops)
async function seedSessionB() {
  if (seeded.b) return seeded.b;
  const upload = await ctx.request.post(`${base}/api/v1/assets`, {
    headers: ORIGIN, multipart: { file: { name: `${tag}-scene.png`, mimeType: 'image/png', buffer: makePng(256, 192) } },
  });
  const source = (await upload.json().catch(() => ({}))).id;
  if (![200, 201].includes(upload.status()) || !source) throw new Error(`upload ${upload.status()}`);
  const sid = await createSession(`${tag} 裁图`);
  const marker = `[[fake:tool:crop_image_regions ${JSON.stringify({
    image: '@img1',
    regions: [{ label: 'left', region: { x: 0, y: 0, width: 0.5, height: 1 } }, { label: 'right', region: { x: 0.5, y: 0, width: 0.5, height: 1 } }],
  })}]]`;
  await turn(sid, `把图里左右两块裁出来 ${marker}`, { image_urls: [`/api/v1/assets/${source}`] });
  const crops = await waitFor(async () => {
    const found = (await messagesOf(sid)).flatMap((message) => (message.attachments || [])
      .filter((attachment) => attachment.role === 'crop').map((attachment) => ({ asset: attachment.assetId || attachment.asset_id, handle: attachment.handle })));
    return found.length >= 2 ? found : null;
  }, 60);
  if (!crops) throw new Error('the fake crop turn left no two crop attachments');
  seeded.b = { sid, source, crops };
  console.log(`seed B ${sid}: upload ${source}, crops ${crops.map((crop) => `${crop.handle}=${crop.asset}`).join(' ')}`);
  return seeded.b;
}

// ---------------------------------------------------------------- #586
async function item586() {
  const sid = await createSession(`${tag} 生成参数`);
  await openSession(sid);
  const body = tid('composer-settings-body');
  const rows = page.locator('[data-testid=workspace-model-picker-image] [role=menuitemradio]');
  const choose = async (name) => {
    await openSettings('models');
    await rows.filter({ hasText: name }).first().click();
    await page.waitForTimeout(1500);
    await openSettings('params');
  };
  // Any counter in the panel: the retired test id, a number input or spinbutton, or a +/- button.
  const counters = async () => body.evaluate((root) => {
    const found = [];
    if (root.querySelector('[data-testid=params-count]')) found.push('params-count');
    for (const input of root.querySelectorAll('input[type=number],[role=spinbutton]')) found.push(`input ${input.name || input.id || input.getAttribute('aria-label') || ''}`);
    for (const button of root.querySelectorAll('button')) {
      const label = `${button.getAttribute('aria-label') || ''} ${button.innerText}`.trim();
      if (/张数|数量|增加|减少|^[+\-−]$/.test(label)) found.push(`button ${label}`);
    }
    return found;
  });
  const readonly = async () => body.locator('[data-testid=params-readonly]').evaluateAll((items) => items.map((item) => [item.dataset.parameter, item.innerText.trim()]));

  await check('#586', 'GPT Image：生成参数页没有张数加减控件，n 只读显示', async () => {
    await choose('GPT Image');
    const panel = (await body.innerText()).replace(/\s+/g, ' ');
    const found = await counters();
    const values = await readonly();
    const n = values.find(([name]) => name === 'n');
    const file = await shot('586', 'params-gpt-image');
    return {
      ok: found.length === 0 && Boolean(n),
      detail: `计数控件=[${found.join('; ')}] 只读=${JSON.stringify(values)} 含「单次出图张数」=${panel.includes('单次出图张数')}（应为只读行） 截图 ${file}`,
    };
  });
  await check('#586', 'Seedream：组图开关只读显示，无开关或计数控件', async () => {
    await choose('Seedream');
    const found = await counters();
    const values = await readonly();
    const sequential = values.find(([name]) => name === 'sequential_image_generation');
    const switches = await body.locator('[role=switch],input[type=checkbox]').count();
    const file = await shot('586', 'params-seedream');
    return {
      ok: found.length === 0 && Boolean(sequential) && switches === 0,
      detail: `计数控件=[${found.join('; ')}] 开关=${switches} 只读=${JSON.stringify(values)} 截图 ${file}`,
    };
  });
  await closeSettings();
}

// ---------------------------------------------------------------- #559
async function item559() {
  const seed = await seedSessionA();
  if (!seed.first || !seed.second) { record('FAIL', '#559', '造数：两张 2D', `tasks ${seed.first} ${seed.second}`); return; }
  await openSession(seed.sid);
  await tileOf(`n:${seed.second}`).waitFor({ timeout: 30000 });
  const handle = (seed.handles[seed.second] || '@img2').replace(/^@/, '');
  const ownLayer = async () => (await layerNames()).filter((name) => name === handle).length;
  const insertKey = await text('ui.result.detail.open_canvas');

  // Make sure the second picture is on the board first (normally the automatic join already put it there).
  if (!await ownLayer()) {
    await tileOf(`n:${seed.second}`).locator('[data-testid=version-detail]').click();
    await tid('result-detail-open-canvas').click();
    await page.keyboard.press('Escape');
    await page.waitForTimeout(1500);
  }
  const before = await layerNames();
  await check('#559', `前置：${handle} 已在画布`, async () => ({ ok: before.includes(handle), detail: `图层=${JSON.stringify(before)}` }));

  await check('#559', '从画布删除图层后点版本条只查看、不复活', async () => {
    const row = tid('layer-row').filter({ hasText: handle }).first();
    await row.hover();
    await row.getByRole('button', { name: await text('ui.canvas.action.delete') }).click();
    await page.waitForTimeout(1500);
    const afterDelete = await layerNames();
    await tileOf(`n:${seed.first}`).locator('button').first().click();
    await page.waitForTimeout(1500);
    const stageFirst = await stageVersion();
    const afterFirst = await layerNames();
    await tileOf(`n:${seed.second}`).locator('button').first().click();
    await page.waitForTimeout(2000);
    const stageSecond = await stageVersion();
    const afterSecond = await layerNames();
    const file = await shot('559', 'click-after-delete');
    return {
      ok: !afterDelete.includes(handle) && afterFirst.length === afterDelete.length && afterSecond.length === afterDelete.length
        && !afterSecond.includes(handle) && /v1/.test(stageFirst) && /v2/.test(stageSecond),
      detail: `删除后=${JSON.stringify(afterDelete)} 点 v1 后舞台「${stageFirst}」图层=${JSON.stringify(afterFirst)} 点 v2 后舞台「${stageSecond}」图层=${JSON.stringify(afterSecond)} 截图 ${file}`,
    };
  });

  await check('#559', '刷新后再点版本条，已删图层仍不复活', async () => {
    await page.waitForTimeout(2000); // let the board save
    await openSession(seed.sid);
    const reloaded = await layerNames();
    await tileOf(`n:${seed.second}`).locator('button').first().click();
    await page.waitForTimeout(2000);
    const after = await layerNames();
    const file = await shot('559', 'reload-click');
    return { ok: !reloaded.includes(handle) && !after.includes(handle), detail: `刷新后=${JSON.stringify(reloaded)} 点 v2 后=${JSON.stringify(after)} 截图 ${file}` };
  });

  await check('#559', `详情按钮是「${insertKey}」，点击后才放上画布`, async () => {
    await tileOf(`n:${seed.second}`).locator('[data-testid=version-detail]').click();
    const button = tid('result-detail-open-canvas');
    await button.waitFor({ timeout: 10000 });
    const label = (await button.innerText()).trim();
    const beforeInsert = await layerNames();
    const fileDetail = await shot('559', 'detail');
    await button.click();
    await page.waitForTimeout(2000);
    await page.keyboard.press('Escape');
    const after = await layerNames();
    const file = await shot('559', 'inserted');
    return {
      ok: label === insertKey && !beforeInsert.includes(handle) && after.includes(handle),
      detail: `按钮「${label}」 插入前=${JSON.stringify(beforeInsert)} 插入后=${JSON.stringify(after)} 截图 ${fileDetail} ${file}`,
    };
  });

  await check('#559', '3D 版本点击仍在舞台打开，不动画布；详情按钮仍是「在舞台中打开」', async () => {
    if (!seed.model) return { ok: false, detail: 'fake 3D 任务没有成功，无法核对 3D 点击' };
    const beforeClick = await layerNames();
    await tileOf(`n:${seed.model}`).locator('button').first().click();
    await page.waitForTimeout(2500);
    const stage = await stageVersion();
    const fileStage = await shot('559', '3d-click');
    await tileOf(`n:${seed.model}`).locator('[data-testid=version-detail]').click();
    const button = tid('result-detail-open-canvas');
    await button.waitFor({ timeout: 10000 });
    const label = (await button.innerText()).trim();
    const want = await text('ui.result.detail.open_stage');
    const file = await shot('559', '3d-detail');
    await page.keyboard.press('Escape');
    // The 3D stage hides the 2D layers panel: look at the board again from the 2D version (a view-only click).
    await tileOf(`n:${seed.second}`).locator('button').first().click();
    await page.waitForTimeout(2000);
    const after = await layerNames();
    return {
      ok: /3D/.test(stage) && JSON.stringify(after) === JSON.stringify(beforeClick) && label === want,
      detail: `舞台「${stage}」 图层前=${JSON.stringify(beforeClick)} 后=${JSON.stringify(after)} 详情按钮「${label}」 截图 ${fileStage} ${file}`,
    };
  });
}

// ---------------------------------------------------------------- #587
async function item587() {
  const seed = await seedSessionB();
  await openSession(seed.sid);
  const cropLabel = await text('ui.studio.version.crop');
  const handles = seed.crops.map((crop) => crop.handle.replace(/^@/, ''));

  await check('#587', `裁图出现在版本条并标「${cropLabel}」，不自动上画布`, async () => {
    const tiles = [];
    for (const crop of seed.crops) {
      const tile = tileOf(`u:${crop.asset}`);
      await tile.waitFor({ timeout: 20000 });
      tiles.push((await tile.innerText()).replace(/\s+/g, ' '));
    }
    const layers = await layerNames();
    const file = await shot('587', 'strip');
    return {
      ok: tiles.every((caption) => caption.includes(cropLabel)) && !layers.some((name) => handles.includes(name)),
      detail: `版本格=${JSON.stringify(tiles)} 图层=${JSON.stringify(layers)} 截图 ${file}`,
    };
  });

  await check('#587', `「全部产出」列出裁图并标「${cropLabel}」`, async () => {
    await tid('all-outputs-button').click();
    await tid('all-outputs').waitFor({ timeout: 10000 });
    await page.waitForTimeout(1000);
    const cards = await tid('all-outputs-card').evaluateAll((items) => items.map((item) => item.innerText.replace(/\s+/g, ' ').trim()));
    const file = await shot('587', 'all-outputs');
    await page.getByRole('button', { name: await text('ui.studio.outputs.close') }).click();
    await page.waitForTimeout(800);
    return { ok: cards.filter((card) => card.includes(cropLabel)).length >= seed.crops.length, detail: `卡片=${JSON.stringify(cards)} 截图 ${file}` };
  });

  const card = tid('chat-image-result').first();
  await check('#587', '对话卡片上的裁图没有「发送到画布不可用」提示，详情「插入到画布」后放上画布', async () => {
    const unavailable = await card.getByText(await text('ui.chat.image.send_to_canvas_unavailable')).count();
    await card.locator('[data-testid=chat-result-detail]').click();
    const button = tid('result-detail-open-canvas');
    await button.waitFor({ timeout: 10000 });
    const label = (await button.innerText()).trim();
    const before = await layerNames();
    await button.click();
    await page.waitForTimeout(2000);
    await page.keyboard.press('Escape');
    const after = await layerNames();
    const file = await shot('587', 'crop-inserted');
    return {
      ok: unavailable === 0 && label === await text('ui.result.detail.open_canvas') && !before.includes(handles[0]) && after.includes(handles[0]),
      detail: `不可用提示=${unavailable} 按钮「${label}」 前=${JSON.stringify(before)} 后=${JSON.stringify(after)} 截图 ${file}`,
    };
  });

  await check('#587', '裁图可「选用」', async () => {
    const pick = card.locator('[data-testid=candidate-pick]').first();
    const disabled = await pick.isDisabled();
    await pick.click();
    await page.waitForTimeout(2000);
    const picked = await pick.getAttribute('data-picked');
    const [status, body] = await api('GET', `/api/v1/chat/sessions/${seed.sid}/pick`);
    const file = await shot('587', 'crop-picked');
    const stored = body.pick || {};
    return {
      ok: !disabled && picked === 'true' && status === 200 && stored.assetId === seed.crops[0].asset && stored.role === 'crop',
      detail: `按钮禁用=${disabled} data-picked=${picked} 服务端 pick=${JSON.stringify({ assetId: stored.assetId, role: stored.role })} 截图 ${file}`,
    };
  });

  await check('#587', '裁图可拖到画布', async () => {
    const source = card.locator('[draggable=true]').nth(1);
    const stage = tid('studio-stage').first();
    const box = await stage.boundingBox();
    const before = await layerNames();
    await source.dragTo(stage, { targetPosition: { x: box.width / 2, y: box.height / 2 }, timeout: 15000 });
    await page.waitForTimeout(2500);
    const after = await layerNames();
    const file = await shot('587', 'crop-dragged');
    return { ok: !before.includes(handles[1]) && after.includes(handles[1]), detail: `拖入 @${handles[1]}：前=${JSON.stringify(before)} 后=${JSON.stringify(after)} 截图 ${file}` };
  });

  await check('#587', '用户上传图不能选用（界面不给选用按钮，服务端拒绝）', async () => {
    const userMessage = tid('chat-message').filter({ has: page.locator(`[data-testid=chat-sent-handle]`) }).first();
    const enabledPicks = await userMessage.locator('[data-testid=candidate-pick]:not([disabled])').count();
    const [status] = await api('PUT', `/api/v1/chat/sessions/${seed.sid}/pick`, { kind: 'image', assetId: seed.source });
    return {
      ok: enabledPicks === 0 && status === 400,
      detail: `上传所在消息里可点的「选用」=${enabledPicks}；PUT pick(上传) HTTP ${status}（界面上上传图只出现在用户消息里，没有「选用」按钮；置灰形态没有自然入口，只能以服务端拒绝佐证）`,
    };
  });
  await api('DELETE', `/api/v1/chat/sessions/${seed.sid}/pick`);
}

// ---------------------------------------------------------------- #560
async function item560() {
  const seed = await seedSessionA();
  await openSession(seed.sid);
  const handle = (seed.handles[seed.second] || '@img2');
  const onBoard = (await layerNames()).includes(handle.replace(/^@/, ''));
  if (!onBoard) {
    await tileOf(`n:${seed.second}`).locator('[data-testid=version-detail]').click();
    await tid('result-detail-open-canvas').click();
    await page.keyboard.press('Escape');
    await page.waitForTimeout(1500);
  }
  const input = page.locator('[data-testid=studio-composer] textarea').first();
  await input.click();
  await input.pressSequentially('@');
  const list = tid('composer-mention-list');
  await list.waitFor({ timeout: 10000 });
  await page.waitForTimeout(500);
  const read = async () => list.evaluate((root) => [...root.querySelectorAll('[data-testid=composer-mention-group-heading],[data-testid=composer-mention-option]')]
    .map((node) => node.dataset.testid === 'composer-mention-option'
      ? { kind: 'option', handle: node.dataset.handle, selected: node.getAttribute('aria-selected') === 'true', group: node.closest('[data-testid=composer-mention-group]')?.dataset.group || '' }
      : { kind: 'heading', text: node.innerText.trim(), role: node.getAttribute('role'), selected: node.getAttribute('aria-selected') === 'true' }));
  const entries = await read();
  const file = await shot('560', 'mention-list');
  const headings = entries.filter((entry) => entry.kind === 'heading').map((entry) => entry.text);
  const options = entries.filter((entry) => entry.kind === 'option');
  const canvasHeading = await text('ui.studio.composer.mention.group_canvas');
  const libraryHeading = await text('ui.studio.composer.mention.group_library');
  const boardToken = `@${await text('ui.studio.composer.mention.token_board')}`;

  await check('#560', `列表分「${canvasHeading}」「${libraryHeading}」两段，「画布」条目第一`, async () => ({
    ok: headings[0] === canvasHeading && headings[1] === libraryHeading && options[0]?.handle === boardToken && entries[0]?.kind === 'heading',
    detail: `顺序=${JSON.stringify(entries.map((entry) => entry.kind === 'heading' ? `[${entry.text}]` : entry.handle))} 截图 ${file}`,
  }));
  await check('#560', '画布上的图在「画布中」段，未上画布的图与 3D 在「素材库」段', async () => {
    const firstHandle = seed.handles[seed.first] || '@img1';
    const groupOf = (name) => options.find((option) => option.handle === name)?.group;
    const modelGroups = options.filter((option) => /model|3d/i.test(option.handle)).map((option) => `${option.handle}:${option.group}`);
    return {
      ok: groupOf(handle) === 'canvas' && groupOf(firstHandle) === 'library',
      detail: `${handle}→${groupOf(handle)} ${firstHandle}→${groupOf(firstHandle)} 3D=${JSON.stringify(modelGroups)}`,
    };
  });
  await check('#560', '句柄编号不变（与对话里的句柄一致）', async () => {
    const want = [seed.handles[seed.first], seed.handles[seed.second]].filter(Boolean);
    const got = options.map((option) => option.handle);
    return { ok: want.length === 2 && want.every((name) => got.includes(name)), detail: `对话句柄=${JSON.stringify(want)} 列表句柄=${JSON.stringify(got)}` };
  });
  await check('#560', '上下键只在选项间移动，标题行不被选中，末项后回到第一项', async () => {
    const trail = [];
    let headingSelected = false;
    for (let step = 0; step < options.length; step += 1) {
      await page.keyboard.press('ArrowDown');
      await page.waitForTimeout(150);
      const now = await read();
      headingSelected ||= now.some((entry) => entry.kind === 'heading' && entry.selected);
      const selected = now.filter((entry) => entry.kind === 'option' && entry.selected).map((entry) => entry.handle);
      trail.push(selected.join('+') || '(无)');
    }
    const headingRoles = entries.filter((entry) => entry.kind === 'heading').map((entry) => entry.role);
    await page.keyboard.press('Escape');
    await input.fill('');
    const expected = [...options.slice(1).map((option) => option.handle), options[0].handle];
    return {
      ok: !headingSelected && headingRoles.every((role) => role !== 'option') && JSON.stringify(trail) === JSON.stringify(expected),
      detail: `下移轨迹=${JSON.stringify(trail)} 期望=${JSON.stringify(expected)} 标题 role=${JSON.stringify(headingRoles)}`,
    };
  });
}

// ---------------------------------------------------------------- #548 (batch 2)
async function batch2Gate(item, name, needle) {
  if (!page.url().startsWith(base)) await page.goto(`${base}/`, { waitUntil: 'load' });
  const present = await page.evaluate((text) => JSON.stringify(window.__ART_AI_PRODUCT_SCHEMA__ || {}).includes(text), needle);
  if (present) return true;
  record(REQUIRE_BATCH2 ? 'FAIL' : 'SKIP', item, name, `待批次 2：目标环境的 Product Schema 没有 ${needle}（#548 未部署）`);
  return false;
}
async function item548Skill() {
  if (!await batch2Gate('#548', 'Skills 页起草、逐项修改、分别保存与删除', '/api/v1/chat/skills/personal/draft')) return;
  const sid = await createSession(`${tag} Skill`);
  await openSession(sid);
  const name = `${tag}-skill`;
  const slug = `${tag}-skill`;
  const draftDescription = `${tag} 验收草稿说明`;
  const listSkills = async () => (await api('GET', '/api/v1/chat/skills/personal'))[1].skills || [];
  const panel = tid('workspace-skill-picker-panel').first();
  const rowOf = (label) => panel.locator('div.relative').filter({ has: page.locator('[role=menuitemradio]', { hasText: label }) }).first();
  const openSkills = async () => { await closeSettings(); await openSettings('skills'); };

  await check('#548', '只填名称加要求，点「生成」出现草稿（标识、说明、正文、类型）', async () => {
    await openSkills();
    await page.getByRole('button', { name: await text('ui.workspace.skills.create'), exact: true }).click();
    const creator = tid('workspace-skill-creator');
    await creator.waitFor({ timeout: 10000 });
    await page.locator('#personal-skill-name').fill(name);
    await page.locator('#personal-skill-requirement').fill(`角色设定图一律柔和水彩、浅色背景 [[fake:skill-draft ${JSON.stringify({ slug, description: draftDescription })}]]`);
    await tid('skill-generate').click();
    await page.locator('#personal-skill-slug').waitFor({ timeout: 60000 });
    const draft = {
      slug: await page.locator('#personal-skill-slug').inputValue(),
      description: await page.locator('#personal-skill-description').inputValue(),
      prompt: await page.locator('#personal-skill-prompt').inputValue(),
      apply: await page.locator('#personal-skill-apply').inputValue(),
    };
    const file = await shot('548', 'draft');
    return { ok: draft.slug === slug && draft.description === draftDescription && draft.prompt.length > 0 && Boolean(draft.apply), detail: `草稿=${JSON.stringify(draft).slice(0, 300)} 截图 ${file}` };
  });

  const edited = { description: `${tag} 改过的说明`, prompt: `${tag} 改过的正文：柔和水彩，浅色背景。`, apply: 'generation-prompt' };
  await check('#548', '逐项修改草稿后保存，服务端按修改后的内容保存', async () => {
    await page.locator('#personal-skill-description').fill(edited.description);
    await page.locator('#personal-skill-prompt').fill(edited.prompt);
    await page.locator('#personal-skill-apply').selectOption(edited.apply);
    await tid('workspace-skill-creator').getByRole('button', { name: await text('ui.workspace.skills.save') }).click();
    await tid('workspace-skill-creator').waitFor({ state: 'detached', timeout: 15000 });
    const saved = (await listSkills()).find((skill) => skill.name === name);
    const file = await shot('548', 'saved');
    return {
      ok: Boolean(saved) && saved.slug === slug && saved.description === edited.description && saved.prompt === edited.prompt && saved.apply === edited.apply,
      detail: `服务端=${JSON.stringify(saved ? { slug: saved.slug, description: saved.description, apply: saved.apply } : null)} 截图 ${file}`,
    };
  });

  const derivedName = `${tag}-derived`;
  await check('#548', '列表显示说明；没写说明的 Skill 标「自动生成，建议补写」', async () => {
    const [status] = await api('POST', '/api/v1/chat/skills/personal', { name: derivedName, prompt: `${tag} 没有说明的旧式 Skill 正文` });
    await openSkills();
    await closeSettings();
    await openSkills();
    const own = (await rowOf(name).innerText()).replace(/\s+/g, ' ');
    const derived = (await rowOf(derivedName).innerText().catch(() => '')).replace(/\s+/g, ' ');
    const label = await text('ui.workspace.skills.description_derived');
    const file = await shot('548', 'list');
    return {
      ok: own.includes(edited.description) && !own.includes(label) && derived.includes(label),
      detail: `派生 Skill 经 API 不带 description 创建（HTTP ${status}）；行=${JSON.stringify(own).slice(0, 160)} 派生行=${JSON.stringify(derived).slice(0, 160)} 截图 ${file}`,
    };
  });

  await check('#548', '编辑时各字段分别保存', async () => {
    await rowOf(name).hover();
    await rowOf(name).locator('[data-testid=skill-edit]').click();
    const editor = tid('workspace-skill-editor');
    await editor.waitFor({ timeout: 10000 });
    const description = `${tag} 编辑后的说明`;
    await page.locator('#personal-skill-description').fill(description);
    await tid('skill-field-save-description').click();
    await tid('skill-field-saved-description').waitFor({ timeout: 15000 });
    const promptSaveDisabled = await tid('skill-field-save-prompt').isDisabled();
    const afterDescription = (await listSkills()).find((skill) => skill.name === name);
    const prompt = `${tag} 编辑后的正文`;
    await page.locator('#personal-skill-prompt').fill(prompt);
    await tid('skill-field-save-prompt').click();
    await tid('skill-field-saved-prompt').waitFor({ timeout: 15000 });
    const afterPrompt = (await listSkills()).find((skill) => skill.name === name);
    const file = await shot('548', 'edit-fields');
    await tid('skill-edit-cancel').first().click();
    await editor.waitFor({ state: 'detached', timeout: 10000 }).catch(() => {});
    return {
      ok: promptSaveDisabled && afterDescription?.description === description && afterDescription?.prompt === edited.prompt
        && afterPrompt?.prompt === prompt && afterPrompt?.description === description,
      detail: `存说明时正文保存按钮禁用=${promptSaveDisabled}；只存说明后正文未变=${afterDescription?.prompt === edited.prompt}；再存正文后=${afterPrompt?.prompt === prompt} 截图 ${file}`,
    };
  });

  await check('#548', '删除个人 Skill', async () => {
    await openSkills();
    for (const label of [name, derivedName]) {
      await rowOf(label).hover();
      await rowOf(label).locator('[data-testid=skill-delete]').click();
      await page.waitForTimeout(1500);
    }
    const left = (await listSkills()).filter((skill) => skill.name.startsWith(tag)).map((skill) => skill.name);
    const file = await shot('548', 'deleted');
    await closeSettings();
    return { ok: left.length === 0, detail: `剩余=${JSON.stringify(left)} 截图 ${file}` };
  });
}

async function item548Chip() {
  if (!await batch2Gate('#548', '三视图对话出现「本轮使用 Skill」标记，刷新后仍在', 'ui.chat.message.used_skills')) return;
  const sid = await createSession(`${tag} 三视图`);
  await openSession(sid);
  await check('#548', '三视图对话出现「本轮使用 Skill」标记，刷新后仍在', async () => {
    const before = await finalAssistantCount(sid);
    const input = page.locator('[data-testid=studio-composer] textarea').first();
    await input.fill('帮我做三视图 [[fake:tool:use_skill {"name":"three-view"}]]');
    await input.press('Enter');
    if (!await waitFor(async () => (await finalAssistantCount(sid)) > before, 120)) throw new Error('the turn produced no final reply in 120 s');
    const chip = tid('chat-used-skills').last();
    await chip.waitFor({ timeout: 20000 });
    const live = (await chip.innerText()).trim();
    const fileLive = await shot('548', 'used-skill');
    await openSession(sid);
    const reloaded = (await tid('chat-used-skills').last().innerText({ timeout: 15000 }).catch(() => '')).trim();
    const file = await shot('548', 'used-skill-reload');
    const prefix = (await text('ui.chat.message.used_skills')).split('{')[0];
    return {
      ok: live.startsWith(prefix) && /三视图/.test(live) && reloaded === live,
      detail: `标记「${live}」 刷新后「${reloaded}」（fake 对话模型用 use_skill 标记驱动，dev-test 不走 mock Agent 路由） 截图 ${fileLive} ${file}`,
    };
  });
}

// ---------------------------------------------------------------- run
const runners = { 586: item586, 559: item559, 587: item587, 560: item560, '548-skill': item548Skill, '548-chip': item548Chip };
try {
  for (const item of ITEMS) {
    const runner = runners[item];
    if (!runner) { record('FAIL', item, '未知的验收项', `可选：${Object.keys(runners).join(',')}`); continue; }
    try {
      await runner();
    } catch (error) {
      const file = await shot(item, 'error');
      record('FAIL', `#${item}`, '运行中断', `${error.message.split('\n')[0]} (screenshot ${file})`);
    }
  }
} finally {
  // Remove this run's personal Skills even when a check failed half-way.
  const [, mine] = await api('GET', '/api/v1/chat/skills/personal');
  for (const skill of mine.skills || []) {
    if (String(skill.name || '').startsWith(tag)) await api('DELETE', `/api/v1/chat/skills/personal/${encodeURIComponent(skill.id)}`);
  }
  await browser.close();
}

if (pageErrors.length) console.log(`INFO: 页面未捕获异常 ${pageErrors.length} 条：${[...new Set(pageErrors)].slice(0, 5).join(' | ')}`);
fs.writeFileSync(path.join(OUT, 'results.json'), JSON.stringify({ base, tag, version: process.env.EXPECT_SHA || '', results, pageErrors }, null, 2));
const count = (status) => results.filter((result) => result.status === status).length;
console.log(`results: ${path.join(OUT, 'results.json')}`);
console.log(`PASS=${count('PASS')} FAIL=${count('FAIL')} SKIP=${count('SKIP')}`);
process.exit(count('FAIL') ? 1 : 0);
