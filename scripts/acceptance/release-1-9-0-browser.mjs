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
//   第三批（#593、#591、三视图/四视图不强制先有图）
//     N1  详情里「插入到画布」/「在舞台中打开」在预览正下方（预览 → 按钮 → 信息行，不在底部动作行），2D 与 3D 各一次。
//     N2  版本条 2D 瓦片双击把图插回画布（单击仍只查看）；3D 瓦片双击不插入；裁图瓦片双击能插入。
//     N3  瓦片下的「详情」是按钮，宽度等于瓦片宽度（±2px）。
//     N4  输入框上方的图片芯片是 68px 宽、高大于宽的竖排卡片（缩略图在上、徽标在下、× 在右上），不随输入框拉伸。
//     N5  聊天栏最窄 440、最宽 640；窄栏下结果卡「选用」「不要这个」不重叠。
//     N6  手选「四视图」「三视图」后不给图直接发送：不弹“需要一张图”，消息发出。
//   批次 4（PR #598；Schema 没有 ui.studio.strip.group_all_title 时记 SKIP「待批次 4」）
//     M1  #596：同一来源 N 张结果（N=4/6/8/10）在版本条只占代表图一格加「更多」；弹层标题「这一步生成的全部内容」、
//         列出全部 N 个、在视口内可滚动（1440 与 1280 宽）；单击第 3 张只换引用芯片、代表图与顺序不变、不进画布；
//         双击第 4 张插入画布；「详情」打开详情。
//     M2  #597：经输入框连发 4 轮 fake refine_image，第 4 轮结束 <1 秒与 7 秒时「分支到新对话」可用、无“尚未结束”；
//         第 5 轮进行中，前几轮的结果卡与图片一直显示。
//   #599 项目组 KEY 与支出占比（要管理员账号：ADMIN_USERNAME/ADMIN_PASSWORD，release-env.py smoke 注入；缺少时 SKIP）
//     P1  管理 API 建 3 个 r190- 用户，A、B 各设一个项目组、C 未分组；非法 KEY（大写、空格、超长、连字符开头）400 带
//         Schema 文案；空串、null、首尾空格与不带该字段的写入只记录（INFO，不计数）。
//     P2  三个用户各自登录做不同张数的 fake 文生图；管理端用量 byGroup 有两组加一行未分组（key=null），每组金额 = 组内
//         用户之和、各组合计 = total、占比与金额一致（±1 bp）；账户用量接口不含分组字段。
//     P3  A 改到 B 的组后，A 的历史金额随当前组归集。
//     P4  管理端用户页设组保存、刷新仍在；用量页组表（未分组文案、两位小数百分比、用户表带组列）截图；普通用户访问
//         管理接口 401/403。
//
// 用法（地址与 Agent 专用账号由 release-env.py 注入，凭据只在子进程环境里）：
//   python3 scripts/release-env.py smoke dev-test -- node scripts/acceptance/release-1-9-0-browser.mjs
// 环境变量：
//   R190_ITEMS=586,…,n6,m1,m2,p1,…,p4  只跑其中几项（默认全部）
//   R190_REQUIRE_BATCH4=1  目标环境缺批次 4（PR #598）时 M1/M2 记 FAIL 而不是 SKIP
//   R190_M1_COUNTS=4,6,8,10  M1 每组的结果张数；R190_M1_WIDTHS=1440,1280  M1 看弹层的窗口宽度
//   R190_REQUIRE_BATCH2=1  目标环境缺批次 2 能力时记 FAIL 而不是 SKIP（批次 2 部署后用）
//   OUT=<目录>  截图与 results.json 的位置（默认系统临时目录 plinth-acceptance-r190-*，不要写进仓库）
//   PLAYWRIGHT_MODULE_PATH  Playwright 的位置（工作树没有 node_modules 时指向主检出的 node_modules/playwright）
//
// 造数（都在 Agent 专用账号下，每次新建，标题带 r190-<时间戳>）：
//   会话 A：两轮 fake refine_image 出两张 2D、一轮 fake create_model_3d 出一个 3D（若出批准卡就批准，fake 不计费）。
//   会话 B：上传一张 256x192 的双色 PNG，一轮 fake crop_image_regions 从中裁出左右两块（Agent 裁图）。
//   会话 C：空会话，只用来看设置弹窗（会选一次 2D 模型；工作区的模型选择存在浏览器本地，项目结束时放回原值）。
//   会话 D（批次 2）：一轮 fake use_skill 三视图。个人 Skill r190-*（结束时删除）。
//   N6：两个空会话，各手选一个系统 Skill 后发一条文字（fake 对话模型只回文字）。N4/N5 会拖动聊天栏宽度，结束时拖回最窄。
//   M1：每个 N 一个会话：一张 fake 文生图（@img1）加 N 个以 @img1 为参考的 fake refine_image（一轮里 N 个工具标记）。
//   M2：一个会话，先经 API 出一张 fake 文生图（@img1），再经输入框发 5 轮以 @img1 为参考的 fake refine_image。
//   P1–P4：用户 r190-<时间戳>-a/b/c（随机口令只在内存里），各一个会话做 1/2/3 张 fake 文生图；结束时经管理 API 逐个
//   删除（产品是软删除，这些用户的历史用量仍按原组留在 dev-test 的统计里，dev-test 下次发布即清空）。
// 目标环境不是 fake 上游形态（ART_AI_UPSTREAM_SHAPE!=fake）时拒绝运行（退出码 2），不会发起真实生成。
// 不打印口令、Cookie 或签名 URL。最后一行输出 `PASS=n FAIL=n SKIP=n`；有 FAIL 时退出码 1。
import { randomBytes } from 'node:crypto';
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

const ITEMS = (process.env.R190_ITEMS || '586,559,587,560,548-skill,548-chip,n1,n2,n3,n4,n5,n6,m1,m2,p1,p2,p3,p4').split(',').map((item) => item.trim()).filter(Boolean);
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
async function shotOf(target, item, name, fullPage = false) {
  const file = path.join(OUT, `${String(++shotIndex).padStart(2, '0')}-${item}-${name}.png`);
  try { await target.screenshot({ path: file, fullPage }); } catch {}
  return path.basename(file);
}
const shot = (item, name) => shotOf(page, item, name);
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
  // The 2D model picked here is the workspace's choice for later turns too (kept in this browser's local storage,
  // `art-ai-workspace-prefs`); later items send through the box with fake markers that name no model, and the product
  // refuses such a tool call while another model is the workspace choice (model_choice_workspace_conflict). So the
  // stored preference from before this item is put back at the end and the page reloaded.
  const PREFS_KEY = 'art-ai-workspace-prefs';
  const savedPrefs = await page.evaluate((key) => { try { return localStorage.getItem(key); } catch { return null; } }, PREFS_KEY);

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
  await page.evaluate(([key, value]) => {
    try { if (value === null) localStorage.removeItem(key); else localStorage.setItem(key, value); } catch {}
  }, [PREFS_KEY, savedPrefs]);
  await page.reload({ waitUntil: 'load' });
  await tid('composer-send').first().waitFor({ timeout: 30000 });
  const now = await page.evaluate((key) => { try { return localStorage.getItem(key); } catch { return 'unreadable'; } }, PREFS_KEY);
  console.log(`#586: workspace model choice put back to ${savedPrefs === null ? 'none' : savedPrefs} (now ${now === null ? 'none' : now})`);
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
    const [status] = await api('POST', '/api/v1/chat/skills/personal', { name: derivedName, prompt: `${tag} 没有说明的旧式 Skill 正文`, recommended_size: '2048x2048' });
    // The list is a cached read: an API-side create shows after a reload (the UI itself creates through the dialog).
    await closeSettings();
    await page.reload({ waitUntil: 'domcontentloaded' });
    await page.waitForTimeout(1500);
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

// ---------------------------------------------------------------- N1–N6（#593、#591 与三视图/四视图不强制先有图）
const boxOf = async (locator) => {
  const found = await locator.boundingBox();
  if (!found) throw new Error('element has no bounding box (not rendered)');
  return found;
};
const round = (b) => `${Math.round(b.x)},${Math.round(b.y)} ${Math.round(b.width)}x${Math.round(b.height)}`;
const intersects = (a, b) => a.x < b.x + b.width && b.x < a.x + a.width && a.y < b.y + b.height && b.y < a.y + a.height;
const layerRow = (label) => tid('layer-row').filter({ hasText: new RegExp(`^\\s*${label}\\s*$`) }).first();
async function removeLayer(label) {
  const row = layerRow(label);
  await row.hover();
  await row.getByRole('button', { name: await text('ui.canvas.action.delete') }).click();
  await page.waitForTimeout(1500);
}
const tileThumb = (key) => tileOf(key).locator('button').first();
const chatHandle = () => tid('chat-resize-handle').first();
async function dragChat(dx) {
  const handle = await boxOf(chatHandle());
  const x = handle.x + handle.width / 2;
  const y = handle.y + handle.height / 2;
  await page.mouse.move(x, y);
  await page.mouse.down();
  await page.mouse.move(x + dx, y, { steps: 12 });
  await page.mouse.up();
  await page.waitForTimeout(800);
}
const chatWidth = async () => ({
  column: Math.round((await boxOf(tid('studio-chat-column').first())).width),
  value: Number(await chatHandle().getAttribute('aria-valuenow')),
});

async function itemN1() {
  const seed = await seedSessionA();
  await openSession(seed.sid);
  // DOM order inside the detail: preview → place row (the button) → info rows; the button is not in the action row.
  const layout = async () => {
    const order = await page.evaluate(() => {
      const root = document.querySelector('[data-testid=result-detail]');
      const q = (id) => root?.querySelector(`[data-testid=${id}]`) || null;
      const preview = q('result-detail-preview');
      const button = q('result-detail-open-canvas');
      const rows = q('result-detail-rows');
      const actions = q('result-detail-actions');
      const before = (a, b) => Boolean(a && b && !a.contains(b) && (a.compareDocumentPosition(b) & Node.DOCUMENT_POSITION_FOLLOWING));
      return {
        found: { preview: Boolean(preview), button: Boolean(button), rows: Boolean(rows) },
        previewThenButton: before(preview, button),
        buttonThenRows: before(button, rows),
        inActions: Boolean(actions && button && actions.contains(button)),
        label: button?.innerText.trim() || '',
      };
    });
    const preview = await boxOf(tid('result-detail-preview').first());
    const button = await boxOf(tid('result-detail-open-canvas').first());
    const rows = await boxOf(tid('result-detail-rows').first());
    const centre = button.x + button.width / 2;
    const geometry = button.y >= preview.y + preview.height - 2 && button.y - (preview.y + preview.height) < 80
      && centre > preview.x && centre < preview.x + preview.width && rows.y >= button.y + button.height - 2;
    return { ...order, geometry, boxes: `预览 ${round(preview)} 按钮 ${round(button)} 信息行 ${round(rows)}` };
  };
  for (const [kind, key, want] of [['2D', `n:${seed.second}`, 'ui.result.detail.open_canvas'], ['3D', `n:${seed.model}`, 'ui.result.detail.open_stage']]) {
    await check('N1', `${kind} 详情：按钮在预览正下方（预览 → 按钮 → 信息行，不在底部动作行）`, async () => {
      if (kind === '3D' && !seed.model) return { ok: false, detail: 'fake 3D 任务没有成功' };
      await tileOf(key).locator('[data-testid=version-detail]').click();
      await tid('result-detail-open-canvas').first().waitFor({ timeout: 10000 });
      await page.waitForTimeout(800);
      const found = await layout();
      const label = await text(want);
      const file = await shot('N1', `detail-${kind}`);
      await page.keyboard.press('Escape');
      await page.waitForTimeout(500);
      return {
        ok: found.label === label && found.previewThenButton && found.buttonThenRows && !found.inActions && found.geometry,
        detail: `按钮「${found.label}」 预览→按钮=${found.previewThenButton} 按钮→信息行=${found.buttonThenRows} 在动作行=${found.inActions} 位置正下方=${found.geometry} ${found.boxes} 截图 ${file}`,
      };
    });
  }
}

async function itemN2() {
  const seed = await seedSessionA();
  await openSession(seed.sid);
  const label = (seed.handles[seed.second] || '@img2').replace(/^@/, '');
  const count = async (name) => (await layerNames()).filter((item) => item === name).length;
  await check('N2', '2D 瓦片：单击只查看，双击把已删出画布的图插回画布', async () => {
    if (!await count(label)) await tileThumb(`n:${seed.second}`).dblclick();
    await page.waitForTimeout(1500);
    if (await count(label)) await removeLayer(label);
    const removed = await layerNames();
    await tileThumb(`n:${seed.second}`).click();
    await page.waitForTimeout(1800);
    const afterClick = await layerNames();
    await tileThumb(`n:${seed.second}`).dblclick();
    await page.waitForTimeout(2500);
    const afterDouble = await layerNames();
    const file = await shot('N2', 'double-click-2d');
    return {
      ok: !removed.includes(label) && !afterClick.includes(label) && afterDouble.filter((item) => item === label).length === 1,
      detail: `删除后=${JSON.stringify(removed)} 单击后=${JSON.stringify(afterClick)} 双击后=${JSON.stringify(afterDouble)} 截图 ${file}`,
    };
  });
  await check('N2', '3D 瓦片双击不往画布插入', async () => {
    if (!seed.model) return { ok: false, detail: 'fake 3D 任务没有成功' };
    const before = await layerNames();
    await tileThumb(`n:${seed.model}`).dblclick();
    await page.waitForTimeout(2500);
    const stage = await stageVersion();
    const fileStage = await shot('N2', 'double-click-3d');
    // The 3D stage hides the layers panel: read the board again from a 2D version (a view-only click).
    await tileThumb(`n:${seed.first}`).click();
    await page.waitForTimeout(2000);
    const after = await layerNames();
    return { ok: /3D/.test(stage) && JSON.stringify(after) === JSON.stringify(before), detail: `舞台「${stage}」 图层前=${JSON.stringify(before)} 后=${JSON.stringify(after)} 截图 ${fileStage}` };
  });
  const crops = await seedSessionB();
  await openSession(crops.sid);
  const crop = crops.crops[0];
  const cropLabel = crop.handle.replace(/^@/, '');
  await check('N2', '裁图瓦片双击能插入画布', async () => {
    if (await count(cropLabel)) await removeLayer(cropLabel);
    const before = await layerNames();
    await tileThumb(`u:${crop.asset}`).click();
    await page.waitForTimeout(1800);
    const afterClick = await layerNames();
    await tileThumb(`u:${crop.asset}`).dblclick();
    await page.waitForTimeout(2500);
    const after = await layerNames();
    const file = await shot('N2', 'double-click-crop');
    return {
      ok: !before.includes(cropLabel) && !afterClick.includes(cropLabel) && after.includes(cropLabel),
      detail: `@${cropLabel}：插入前=${JSON.stringify(before)} 单击后=${JSON.stringify(afterClick)} 双击后=${JSON.stringify(after)} 截图 ${file}`,
    };
  });
}

async function itemN3() {
  const sessions = [['会话 A（2D/3D）', (await seedSessionA()).sid], ['会话 B（上传/裁图）', (await seedSessionB()).sid]];
  for (const [name, sid] of sessions) {
    await check('N3', `${name}：瓦片下的「详情」是按钮，宽度等于瓦片宽度（±2px）`, async () => {
      await openSession(sid);
      const tiles = tid('version-tile');
      const total = await tiles.count();
      const rows = [];
      for (let index = 0; index < total; index += 1) {
        const tile = tiles.nth(index);
        const detail = tile.locator('[data-testid=version-detail]');
        if (!await detail.count()) { rows.push({ key: await tile.getAttribute('data-key'), missing: true }); continue; }
        const tileBox = await boxOf(tile);
        const detailBox = await boxOf(detail);
        rows.push({
          key: await tile.getAttribute('data-key'),
          tag: await detail.evaluate((node) => node.tagName),
          tile: Math.round(tileBox.width), detail: Math.round(detailBox.width),
        });
      }
      const file = await shot('N3', `detail-width-${sid.slice(-6)}`);
      const bad = rows.filter((row) => row.missing || row.tag !== 'BUTTON' || Math.abs(row.tile - row.detail) > 2);
      return { ok: total > 0 && bad.length === 0, detail: `${JSON.stringify(rows.map((row) => row.missing ? `${row.key}:无详情` : `${row.tag} 瓦片${row.tile}/详情${row.detail}`))} 截图 ${file}` };
    });
  }
}

async function itemN4() {
  const seed = await seedSessionA();
  await openSession(seed.sid);
  await check('N4', '输入框上方的图片芯片是固定宽高的竖排卡片，宽度不随输入框拉伸', async () => {
    const chip = tid('composer-object-chip').first();
    await chip.waitFor({ timeout: 15000 });
    const measure = async () => {
      const card = await boxOf(chip);
      const thumb = await boxOf(chip.locator('img').first());
      const badge = await boxOf(chip.locator('[data-testid=kind-badge]').first());
      const remove = await boxOf(chip.locator('[data-testid=chip-card-remove]').first());
      const composer = await boxOf(tid('studio-composer').first());
      return { card, thumb, badge, remove, composer };
    };
    const narrow = await measure();
    await dragChat(400);
    const wide = await measure();
    const fileWide = await shot('N4', 'chip-wide-composer');
    await dragChat(-600);
    const { card, thumb, badge, remove } = narrow;
    const shape = Math.abs(card.width - 68) <= 2 && card.height > card.width;
    const stacked = thumb.y + thumb.height <= badge.y + 1 && thumb.y < badge.y;
    const corner = remove.x + remove.width >= card.x + card.width - 8 && remove.y <= card.y + 8 && remove.x > card.x + card.width / 2;
    const fixed = Math.abs(wide.card.width - card.width) <= 1 && wide.composer.width > narrow.composer.width + 50;
    const file = await shot('N4', 'chip');
    return {
      ok: shape && stacked && corner && fixed,
      detail: `卡片 ${round(card)} 缩略图 ${round(thumb)} 徽标 ${round(badge)} × ${round(remove)}；竖排=${stacked} ×在右上=${corner}；`
        + `输入框 ${Math.round(narrow.composer.width)}→${Math.round(wide.composer.width)}px 时卡片宽 ${Math.round(card.width)}→${Math.round(wide.card.width)}px 截图 ${file} ${fileWide}`,
    };
  });
}

async function itemN5() {
  const seed = await seedSessionA();
  const crops = await seedSessionB();
  await openSession(crops.sid);
  await check('N5', '聊天栏拖到最窄宽度 ≥ 440（最宽 ≤ 640）', async () => {
    await dragChat(500);
    const widest = await chatWidth();
    await dragChat(-800);
    const narrowest = await chatWidth();
    const file = await shot('N5', 'chat-narrowest');
    return {
      ok: narrowest.column >= 439 && narrowest.value >= 440 && widest.value <= 640 && widest.value > narrowest.value,
      detail: `最窄：栏宽 ${narrowest.column}px aria-valuenow=${narrowest.value}；最宽：栏宽 ${widest.column}px aria-valuenow=${widest.value} 截图 ${file}`,
    };
  });
  for (const [name, sid] of [['裁图卡（两张候选）', crops.sid], ['生成结果卡', seed.sid]]) {
    await check('N5', `窄栏下${name}的「选用」「不要这个」不重叠`, async () => {
      if (sid !== crops.sid) await openSession(sid);
      if ((await chatWidth()).value > 441) await dragChat(-800);
      const cards = tid('chat-image-result');
      const total = await cards.count();
      const pairs = [];
      for (let index = 0; index < total; index += 1) {
        const picks = cards.nth(index).locator('[data-testid=candidate-pick]');
        const rejects = cards.nth(index).locator('[data-testid=candidate-reject]');
        const n = Math.min(await picks.count(), await rejects.count());
        for (let item = 0; item < n; item += 1) {
          const pick = await boxOf(picks.nth(item));
          const reject = await boxOf(rejects.nth(item));
          pairs.push({ pick: round(pick), reject: round(reject), overlap: intersects(pick, reject) });
        }
      }
      await cards.last().scrollIntoViewIfNeeded().catch(() => {});
      const file = await shot('N5', `buttons-${sid.slice(-6)}`);
      return { ok: pairs.length > 0 && pairs.every((pair) => !pair.overlap), detail: `栏宽 ${(await chatWidth()).column}px 按钮对=${JSON.stringify(pairs)} 截图 ${file}` };
    });
  }
}

async function itemN6() {
  for (const skill of ['四视图', '三视图']) {
    await check('N6', `手选「${skill}」、不给图直接发送：不弹“需要一张图”，消息发出`, async () => {
      const sid = await createSession(`${tag} 手选${skill}`);
      await openSession(sid);
      const pictures = await page.locator('[data-testid=composer-object-chip],[data-testid=composer-attachment-chip]').count();
      await openSettings('skills');
      await tid('workspace-skill-picker-panel').first().locator('[role=menuitemradio]', { hasText: skill }).first().click();
      await page.waitForTimeout(1000);
      await closeSettings();
      const chip = (await tid('composer-skill-chip').first().innerText().catch(() => '')).replace(/\s+/g, ' ').trim();
      // Record every toast that shows up from here on (sonner removes them after a few seconds).
      await page.evaluate(() => {
        window.__r190Toasts = [];
        new MutationObserver(() => {
          for (const toast of document.querySelectorAll('[data-sonner-toast]')) {
            const value = toast.innerText.trim();
            if (value && !window.__r190Toasts.includes(value)) window.__r190Toasts.push(value);
          }
        }).observe(document.body, { childList: true, subtree: true, characterData: true });
      });
      const content = `${tag} 做一个木头小人的${skill}`;
      await page.locator('[data-testid=studio-composer] textarea').first().fill(content);
      const posted = page.waitForResponse((response) => response.url().includes(`/api/chat/sessions/${sid}/messages`) && response.request().method() === 'POST', { timeout: 20000 }).catch(() => null);
      await tid('composer-send').first().click();
      const response = await posted;
      const shown = await waitFor(async () => (await tid('chat-message').filter({ hasText: content }).count()) > 0, 30);
      await page.waitForTimeout(1500);
      const toasts = await page.evaluate(() => window.__r190Toasts || []);
      const stored = (await messagesOf(sid)).some((message) => message.role === 'user' && String(message.content || '').includes(content));
      const file = await shot('N6', `send-${skill}`);
      const blocked = toasts.filter((item) => /需要一张图/.test(item));
      return {
        ok: pictures === 0 && chip.includes(skill) && [200, 201, 202].includes(response?.status()) && Boolean(shown) && stored && blocked.length === 0,
        detail: `输入区图片=${pictures} Skill 芯片「${chip}」 POST HTTP ${response ? response.status() : '无请求'} 对话里显示=${Boolean(shown)} 服务端存了=${stored} toast=${JSON.stringify(toasts)} 截图 ${file}`,
      };
    });
  }
}

// ---------------------------------------------------------------- 批次 4：M1（#596 版本条「更多」）、M2（#597 轮次收尾）
const REQUIRE_BATCH4 = process.env.R190_REQUIRE_BATCH4 === '1';
const M1_COUNTS = (process.env.R190_M1_COUNTS || '4,6,8,10').split(',').map(Number).filter((count) => count >= 3);
const M1_WIDTHS = (process.env.R190_M1_WIDTHS || '1440,1280').split(',').map(Number).filter(Boolean);
async function batch4Gate(item, name) {
  if (!page.url().startsWith(base)) await page.goto(`${base}/`, { waitUntil: 'load' });
  const needle = 'ui.studio.strip.group_all_title';
  if (await page.evaluate((key) => Boolean(window.__ART_AI_PRODUCT_SCHEMA__?.content?.messages?.[key]), needle)) return true;
  record(REQUIRE_BATCH4 ? 'FAIL' : 'SKIP', item, name, `待批次 4：目标环境的 Product Schema 没有 ${needle}（PR #598 未部署）`);
  return false;
}
const assetIdOf = (url) => (String(url || '').match(/\/assets\/([A-Za-z0-9_-]+)/) || [])[1] || '';
const inViewport = (b, size) => b.x >= 0 && b.y >= 0 && b.x + b.width <= size.width + 0.5 && b.y + b.height <= size.height + 0.5;

/**
 * One source picture (@img1, fake text-to-image) and `count` fake edits of it (refine_image with
 * reference_handles @img1), sent as markers in one turn (the Schema allows 20 generation calls a turn); a turn that
 * starts fewer is followed by another with the rest. Nothing else is made between them, so the edits are adjacent
 * results of one source: one column of the strip.
 */
async function seedEdits(count) {
  const sid = await createSession(`${tag} 更多 ${count} 张`);
  await turn(sid, '画一个红色方块 [[fake:tool:refine_image {"prompt":"a red cube on a white background"}]]');
  if (!await waitTasks(sid, (tasks) => tasks.some((task) => task.state === 'SUCCEEDED'))) throw new Error('the source picture did not succeed');
  const edits = async () => (await sessionTasks(sid)).filter((task) => task.capability === 'image-to-image');
  // A turn registers its tasks one by one, so a count read right after it can be short. Wait until the count
  // reaches the target or holds still for a while before topping up; topping up early made 12 edits for N=10.
  const settled = async () => {
    let last = -1;
    let still = 0;
    for (let index = 0; index < 90; index += 1) {
      const now = (await edits()).length;
      if (now >= count) return now;
      still = now === last ? still + 1 : 0;
      if (now > 0 && still >= 10) return now;
      last = now;
      await sleep(1000);
    }
    return last;
  };
  for (let round = 0; round < 3; round += 1) {
    const have = round === 0 ? 0 : await settled();
    if (have >= count) break;
    const missing = count - have;
    const markers = Array.from({ length: missing }, (_, index) => `[[fake:tool:refine_image ${JSON.stringify({ prompt: `variant ${round}-${index + 1} of the red cube`, reference_handles: ['@img1'] })}]]`);
    await turn(sid, `同一张图出 ${missing} 个改法 ${markers.join(' ')}`);
    console.log(`seed M1 N=${count} ${sid}: round ${round + 1} asked for ${missing} edits`);
  }
  const done = await waitTasks(sid, (tasks) => tasks.filter((task) => task.capability === 'image-to-image' && task.state === 'SUCCEEDED').length >= count, 240);
  const made = await edits();
  if (!done) throw new Error(`only ${made.length} of ${count} fake edits were made (session ${sid})`);
  if (made.length !== count) throw new Error(`${made.length} fake edits were made for N=${count} (session ${sid})`);
  // The same-source count the strip should show: lineage edges out of the source picture.
  const [, lineage] = await api('GET', `/api/v1/chat/sessions/${sid}/lineage`);
  const source = (await sessionTasks(sid)).find((task) => task.capability === 'text-to-image');
  const sourceNode = (lineage?.nodes || []).find((node) => source && String(node.id).includes(source.id));
  const fromSource = (lineage?.edges || []).filter((edge) => sourceNode && edge.from === sourceNode.id).length;
  if (fromSource !== count) throw new Error(`lineage has ${fromSource} edges out of the source picture, not ${count} (session ${sid})`);
  return { sid, keys: made.map((task) => `n:${task.id}`) };
}

async function itemM1() {
  if (!await batch4Gate('M1', '版本条同源多张结果收进「更多」')) return;
  const moreText = await text('ui.studio.strip.group_all');
  const titleText = await text('ui.studio.strip.group_all_title');
  for (const count of M1_COUNTS) {
    let seed;
    try {
      seed = await seedEdits(count);
    } catch (error) {
      record('FAIL', 'M1', `N=${count} 造数`, error.message);
      continue;
    }
    console.log(`seed M1 N=${count} ${seed.sid}: ${seed.keys.length} edits`);
    await page.setViewportSize({ width: 1440, height: 900 });
    await openSession(seed.sid);
    const strip = tid('version-strip').first();
    const stripKeys = async () => strip.locator('[data-testid=version-tile]').evaluateAll((items) => items.map((item) => item.dataset.key));
    const column = strip.locator('[data-testid=version-column]').filter({ has: page.locator('[data-testid=version-fold]') }).first();
    const list = tid('version-fold-list').first();
    const openList = async () => {
      if (!await list.isVisible().catch(() => false)) await column.locator('[data-testid=version-fold-trigger]').click();
      await list.waitFor({ timeout: 10000 });
      await page.waitForTimeout(500);
    };
    const closeList = async () => {
      for (let attempt = 0; attempt < 2 && await list.isVisible().catch(() => false); attempt += 1) {
        await page.keyboard.press('Escape');
        await page.waitForTimeout(300);
      }
    };
    const listKeys = async () => list.locator('[data-testid=version-tile]').evaluateAll((items) => items.map((item) => item.dataset.key));
    let representative = '';
    let orderBefore = [];

    await check('M1', `N=${count}：版本条里这一组只占代表图一格加「${moreText}」`, async () => {
      await column.waitFor({ timeout: 20000 });
      const visible = await column.locator('[data-testid=version-tile]').evaluateAll((items) => items.map((item) => item.dataset.key));
      const trigger = (await column.locator('[data-testid=version-fold-trigger]').innerText()).trim();
      const stripText = await strip.innerText();
      const oldWords = /\+\d+/.test(stripText) || (await page.locator('body').innerText()).includes('同一来源的其他结果');
      representative = visible[0] || '';
      orderBefore = await stripKeys();
      const file = await shot('M1', `N${count}-strip`);
      return {
        ok: visible.length === 1 && seed.keys.includes(representative) && trigger === moreText && !oldWords,
        detail: `组内可见格=${JSON.stringify(visible)} 按钮「${trigger}」 出现「+数字」或旧标题=${oldWords} 截图 ${file}`,
      };
    });

    for (const width of M1_WIDTHS) {
      await check('M1', `N=${count}，窗口 ${width}：弹层标题「${titleText}」，列出全部 ${count} 个结果，在视口内、可滚动`, async () => {
        await closeList();
        await page.setViewportSize({ width, height: 900 });
        await page.waitForTimeout(800);
        await openList();
        const title = (await list.innerText()).split('\n')[0].trim();
        const keys = await listKeys();
        const size = page.viewportSize();
        const popup = await boxOf(list);
        const scroll = await list.evaluate((node) => {
          const style = getComputedStyle(node);
          const overflowing = node.scrollHeight > node.clientHeight + 1;
          const before = node.scrollTop;
          node.scrollTop = node.scrollHeight;
          const moved = node.scrollTop > before;
          node.scrollTop = before;
          return { overflowY: style.overflowY, overflowing, moved, scrollHeight: node.scrollHeight, clientHeight: node.clientHeight };
        });
        const file = await shot('M1', `N${count}-popover-${width}`);
        const all = seed.keys.every((key) => keys.includes(key)) && keys.length === count && keys[0] === representative;
        const scrolls = ['auto', 'scroll'].includes(scroll.overflowY) && (!scroll.overflowing || scroll.moved);
        return {
          ok: title === titleText && all && inViewport(popup, size) && scrolls,
          detail: `标题「${title}」 列出 ${keys.length} 个（首个是代表图=${keys[0] === representative}） 弹层 ${round(popup)} 视口 ${size.width}x${size.height} `
            + `overflow-y=${scroll.overflowY} 内容 ${scroll.scrollHeight}/${scroll.clientHeight}px 可滚=${scroll.overflowing ? scroll.moved : '无需'} 截图 ${file}`,
        };
      });
    }
    await closeList();
    await page.setViewportSize({ width: 1440, height: 900 });
    await page.waitForTimeout(800);

    await check('M1', `N=${count}：弹层里单击第 3 张，代表图与顺序不变，输入框引用换成它，不放进画布`, async () => {
      await openList();
      const third = list.locator('[data-testid=version-tile]').nth(2);
      const thirdKey = await third.getAttribute('data-key');
      const thirdAsset = assetIdOf(await third.locator('img').first().getAttribute('src'));
      const layersBefore = await layerNames();
      const chipSrc = () => tid('composer-object-chip').first().locator('img').first().getAttribute('src', { timeout: 3000 }).catch(() => '');
      const chipBefore = assetIdOf(await chipSrc());
      await third.locator('button').first().click();
      await page.waitForTimeout(2000);
      const stillOpen = await list.isVisible().catch(() => false);
      await closeList();
      const chipAsset = assetIdOf(await chipSrc());
      const visibleAfter = await column.locator('[data-testid=version-tile]').evaluateAll((items) => items.map((item) => item.dataset.key));
      const orderAfter = await stripKeys();
      const layersAfter = await layerNames();
      const file = await shot('M1', `N${count}-click-3rd`);
      return {
        ok: visibleAfter[0] === representative && JSON.stringify(orderAfter) === JSON.stringify(orderBefore)
          && Boolean(thirdAsset) && chipAsset === thirdAsset && JSON.stringify(layersAfter) === JSON.stringify(layersBefore),
        detail: `第 3 张 ${thirdKey} 资产 ${thirdAsset}；芯片资产 ${chipBefore}→${chipAsset}；代表图 ${visibleAfter[0] === representative ? '不变' : `变成 ${visibleAfter[0]}`}；`
          + `顺序不变=${JSON.stringify(orderAfter) === JSON.stringify(orderBefore)}；图层 ${JSON.stringify(layersBefore)}→${JSON.stringify(layersAfter)}；单击后弹层仍开=${stillOpen} 截图 ${file}`,
      };
    });

    await check('M1', `N=${count}：弹层里双击第 4 张插入画布`, async () => {
      await openList();
      const fourth = list.locator('[data-testid=version-tile]').nth(3);
      const handle = ((await fourth.locator('[data-testid=version-handle-badge]').first().innerText().catch(() => '')) || '').replace(/^@/, '').trim();
      if (!handle) return { ok: false, detail: '第 4 张没有句柄标记，无法在图层里认出它' };
      // The automatic join may already have put it on the board: take it off first, so the double click has to bring it.
      if ((await layerNames()).includes(handle)) {
        await closeList();
        await removeLayer(handle);
        await openList();
      }
      const before = await layerNames();
      await list.locator('[data-testid=version-tile]').nth(3).locator('button').first().dblclick();
      await page.waitForTimeout(2500);
      await closeList();
      const after = await layerNames();
      const file = await shot('M1', `N${count}-dblclick-4th`);
      return { ok: !before.includes(handle) && after.includes(handle), detail: `第 4 张 ${handle}：前=${JSON.stringify(before)} 后=${JSON.stringify(after)} 截图 ${file}` };
    });

    await check('M1', `N=${count}：弹层里「详情」打开详情`, async () => {
      await openList();
      const item = list.locator('[data-testid=version-tile]').nth(1);
      await item.locator('[data-testid=version-detail]').click();
      const detail = tid('result-detail').first();
      await detail.waitFor({ timeout: 10000 });
      const file = await shot('M1', `N${count}-detail`);
      const titleShown = (await tid('result-detail-dialog').first().innerText().catch(() => '')).split('\n')[0];
      await page.keyboard.press('Escape');
      await page.waitForTimeout(400);
      await closeList();
      return { ok: true, detail: `详情已打开（标题「${titleShown}」） 截图 ${file}` };
    });
  }
  await page.setViewportSize({ width: 1440, height: 900 });
}

async function itemM2() {
  if (!await batch4Gate('M2', '连续多轮后分支按钮与历史结果')) return;
  const sid = await createSession(`${tag} 轮次收尾`);
  // An empty session's composer is in free chat, where refine_image makes no task; seed @img1 first so the stage
  // is an image stage and every round edits it.
  await turn(sid, '画一个木头小玩具 [[fake:tool:refine_image {"prompt":"a small wooden toy, plain background"}]]');
  if (!await waitTasks(sid, (tasks) => tasks.some((task) => task.state === 'SUCCEEDED'))) {
    record('FAIL', 'M2', '造 @img1', `fake 文生图没有成功（会话 ${sid}）`);
    return;
  }
  await openSession(sid);
  const input = page.locator('[data-testid=studio-composer] textarea').first();
  const running = async () => (await tid('composer-stop').count()) > 0;
  const sendRound = async (index) => {
    await input.fill(`第 ${index} 轮 [[fake:tool:refine_image ${JSON.stringify({ prompt: `round ${index}: the wooden toy, another color`, reference_handles: ['@img1'] })}]]`);
    await input.press('Enter');
  };
  // Until the UI says the turn ended: the stop button showed and is gone again.
  const waitEnded = async (index) => {
    await waitFor(running, 15, 100);
    const ended = await waitFor(async () => !(await running()), 180, 100);
    if (!ended) throw new Error(`round ${index} did not end in 180 s`);
    return Date.now();
  };
  const waitTask = async (index) => {
    // @img1 is one success already; round n makes the (n+1)th.
    const tasks = await waitTasks(sid, (list) => list.filter((task) => task.state === 'SUCCEEDED').length >= index + 1, 120);
    if (!tasks) throw new Error(`round ${index}: its fake task did not succeed`);
  };
  const count = async () => page.evaluate(() => {
    const root = document.querySelector('[data-testid=chat-messages]');
    return {
      img: root ? root.querySelectorAll('img').length : 0,
      cards: root ? root.querySelectorAll('[data-testid=chat-image-result]').length : 0,
      handles: root ? [...root.querySelectorAll('[data-testid=chat-result-handle]')].map((node) => node.innerText.trim()) : [],
    };
  });
  const branchState = async () => {
    const message = tid('chat-message').last();
    await message.hover();
    await message.locator('[data-testid=chat-more]').first().click();
    const item = tid('chat-branch').first();
    await item.waitFor({ timeout: 5000 });
    const state = {
      disabled: (await item.getAttribute('aria-disabled')) === 'true' || (await item.getAttribute('data-disabled')) !== null,
      label: (await item.getAttribute('aria-label')) || '',
    };
    const runningText = await text('ui.unavailable.reason.branch.running');
    state.runningHint = state.label.includes(runningText) || (await page.getByText(runningText).count()) > 0;
    await page.keyboard.press('Escape');
    await page.waitForTimeout(200);
    return state;
  };
  let endedAt = 0;
  let immediate = null;
  await check('M2', '连续 4 轮 fake refine_image 都完成', async () => {
    for (let index = 1; index <= 4; index += 1) {
      await sendRound(index);
      endedAt = await waitEnded(index);
      // Round 4: read the branch item at once and keep reading until it is usable (at most 6 s). The page rereads
      // the newest messages 400 ms after `done` (#597, the server marks the turn ended just after it sends `done`),
      // so the first read may still be disabled; the item must be usable before one second is over. Each read also
      // notes the page's running-task cards (the branch is held while a tracked task is not terminal) and, at the
      // first read, the server's own view, to tell which of the two holds it.
      if (index === 4) {
        const shown = await count();
        const [, body] = await api('GET', `/api/chat/sessions/${sid}/messages`);
        const reply = (Array.isArray(body) ? body : (body.messages || [])).filter((item) => item.role === 'assistant').slice(-1)[0] || {};
        const serverTasks = await sessionTasks(sid);
        const server = { at: Date.now() - endedAt, replyPending: Boolean(reply.isPending), unfinished: serverTasks.filter((task) => !['SUCCEEDED', 'FAILED', 'CANCELED', 'CANCELLED'].includes(task.state)).length };
        const reads = [];
        while (Date.now() - endedAt < 6000) {
          const at = Date.now() - endedAt;
          const state = await branchState();
          const runningCards = await tid('chat-task-progress').count();
          reads.push({ at, disabled: state.disabled, runningHint: state.runningHint, runningCards });
          if (!state.disabled && !state.runningHint) break;
        }
        const usable = reads.find((read) => !read.disabled && !read.runningHint);
        immediate = { shown, server, reads, usable, file: await shot('M2', 'branch-immediate') };
      }
      await waitTask(index);
    }
    const tasks = await sessionTasks(sid);
    return { ok: true, detail: `4 轮都结束，任务都已成功（会话 ${sid}：${tasks.filter((task) => task.capability === 'image-to-image').length} 个改图、${tasks.filter((task) => task.capability === 'text-to-image').length} 个文生图）` };
  });
  await check('M2', '第 4 轮结束后 1 秒内：「分支到新对话」变为可用、无“回复或任务尚未结束”', async () => {
    if (!immediate) return { ok: false, detail: '前 4 轮没有跑完，无法读第 4 轮结束时的状态' };
    return {
      // A real read: the chat already shows earlier results; an empty chat would make this pass for nothing.
      ok: immediate.shown.img > 0 && immediate.shown.cards > 0 && Boolean(immediate.usable) && immediate.usable.at < 1000,
      detail: `结束时对话里 img=${immediate.shown.img} 卡=${immediate.shown.cards}；服务端（结束后 ${immediate.server.at}ms）回复进行中=${immediate.server.replyPending} 未结束任务=${immediate.server.unfinished}；`
        + `读分支项：${immediate.reads.map((read) => `${read.at}ms 禁用=${read.disabled} 提示=${read.runningHint} 页面进行中任务卡=${read.runningCards}`).join('，')}；`
        + `${immediate.usable ? `结束后 ${immediate.usable.at}ms 起可用` : '6 秒内一直不可用'} 截图 ${immediate.file}`,
    };
  });
  await check('M2', '第 4 轮结束 7 秒后：「分支到新对话」可用、无“回复或任务尚未结束”', async () => {
    const wait = endedAt + 7000 - Date.now();
    if (wait > 0) await page.waitForTimeout(wait);
    const state = await branchState();
    const file = await shot('M2', 'branch-7s');
    return { ok: !state.disabled && !state.runningHint, detail: `禁用=${state.disabled} 有“尚未结束”提示=${state.runningHint} 截图 ${file}` };
  });
  await check('M2', '第 5 轮进行中，前几轮的结果卡与图片一直显示（数量不减）', async () => {
    const before = await count();
    await sendRound(5);
    const samples = [];
    const end = Date.now() + 180000;
    let seenRunning = false;
    while (Date.now() < end) {
      const isRunning = await running();
      seenRunning ||= isRunning;
      if (seenRunning && !isRunning) break;
      if (isRunning) samples.push(await count());
      await page.waitForTimeout(150);
    }
    const dropped = samples.filter((sample) => sample.img < before.img || sample.cards < before.cards || before.handles.some((handle) => !sample.handles.includes(handle)));
    const file = await shot('M2', 'round5');
    return {
      ok: before.img > 0 && before.cards > 0 && samples.length > 0 && dropped.length === 0,
      detail: `发送前 img=${before.img} 卡=${before.cards} 句柄=${JSON.stringify(before.handles)}；进行中采样 ${samples.length} 次（约 ${(samples.length * 0.15).toFixed(1)}s 以上），`
        + `其中少于发送前的 ${dropped.length} 次${dropped[0] ? `（如 img=${dropped[0].img} 卡=${dropped[0].cards}）` : ''}；fake 没有可从产品侧触发的慢任务标记，进行中窗口是本轮对话与任务轮询的自然时长 截图 ${file}`,
    };
  });
}

// ---------------------------------------------------------------- #599 项目组 KEY 与支出占比（P1–P4）
// The administrator's credentials come from the environment `release-env.py smoke` sets up; never printed. Test users
// are named <tag>-a/-b/-c with a random password kept only in memory, and are deleted by name at the end. Group KEYs
// are new for each run (<tag>-ga/gb/gc): a deleted user keeps its group (#599), so a fixed KEY would also carry the
// spending of earlier runs' users.
const p599 = { admin: null, users: [], strays: [], groups: { a: `${tag}-ga`, b: `${tag}-gb`, c: `${tag}-gc` }, counts: { a: 1, b: 2, c: 3 } };
const ADMIN_READY = Boolean(process.env.ADMIN_USERNAME && process.env.ADMIN_PASSWORD);
const ADMIN_USERS = '/api/admin/users';
const ADMIN_USAGE = '/api/admin/usage';
const ACCOUNT_USAGE = '/api/v1/account/usage';

/** A request through a given browser context (its own cookies), the same way `api` uses the main one. */
async function callAs(context, method, route, data) {
  for (let attempt = 0; attempt < 4; attempt += 1) {
    const response = await context.request.fetch(base + route, { method, data, headers: ORIGIN });
    if (response.status() === 429) { await sleep(10000); continue; }
    let body = {};
    try { body = await response.json(); } catch {}
    return [response.status(), body];
  }
  return [429, {}];
}
/** A fresh signed-in browser context; the password is only passed to the login request. */
async function signedInContext(username, password) {
  const context = await browser.newContext({ viewport: { width: 1440, height: 900 }, locale: 'zh-CN', serviceWorkers: 'block' });
  const response = await context.request.post(`${base}/api/auth/login`, { data: { username, password }, headers: ORIGIN });
  if (response.status() !== 200) {
    await context.close();
    throw new Error(`login as ${username} returned ${response.status()}`);
  }
  return context;
}
async function adminContext() {
  if (!p599.admin) p599.admin = await signedInContext(process.env.ADMIN_USERNAME, process.env.ADMIN_PASSWORD);
  return p599.admin;
}
const adminApi = async (method, route, data) => callAs(await adminContext(), method, route, data);
const p599User = (suffix) => p599.users.find((user) => user.suffix === suffix);
async function adminUserList() {
  const [status, body] = await adminApi('GET', ADMIN_USERS);
  if (status !== 200) throw new Error(`admin user list ${status}`);
  return body.data || [];
}
async function storedGroup(user) {
  const row = (await adminUserList()).find((item) => item.id === user.id);
  return row ? row.projectGroupKey : undefined;
}
const patchGroup = async (user, value) => adminApi('PATCH', `${ADMIN_USERS}/${encodeURIComponent(user.id)}`, { projectGroupKey: value });

/** The per-unit amounts of a usage row (a user row or the total): one credit estimate, or Provider-native totals. */
function unitAmounts(row) {
  if (Array.isArray(row?.price_totals)) return Object.fromEntries(row.price_totals.map((item) => [item.unit, Number(item.amount)]));
  if (row && row.estimatedCredits !== undefined) return { 'legacy.credits': Number(row.estimatedCredits) };
  return {};
}
const close = (left, right) => Math.abs(left - right) < 1e-6;
const amountsText = (amounts) => Object.entries(amounts).map(([unit, amount]) => `${unit}=${amount}`).join(' ') || '无金额';

/**
 * Checks one administration usage response: every group's amount per unit equals the sum of its users' rows, the
 * groups add up to the total, and each share matches amount/total in basis points (±1 for rounding; null only when
 * the unit's total is 0). Returns the problems found, empty when it holds.
 */
function usageProblems(usage) {
  const problems = [];
  const users = usage.data || [];
  const groups = usage.byGroup || [];
  const total = unitAmounts(usage.total);
  for (const group of groups) {
    const key = group.projectGroupKey ?? null;
    const members = users.filter((user) => (user.projectGroupKey ?? null) === key);
    const expected = {};
    for (const member of members) for (const [unit, amount] of Object.entries(unitAmounts(member))) expected[unit] = (expected[unit] || 0) + amount;
    const actual = Object.fromEntries((group.amounts || []).map((item) => [item.unit, Number(item.amount)]));
    for (const unit of new Set([...Object.keys(expected), ...Object.keys(actual)])) {
      if (!close(expected[unit] || 0, actual[unit] || 0)) problems.push(`组 ${key ?? '未分组'} ${unit}=${actual[unit] ?? '无'}，组内用户合计 ${expected[unit] || 0}`);
    }
    const tasks = members.reduce((sum, member) => sum + Number(member.totalTasks || 0), 0);
    if (tasks !== Number(group.totalTasks)) problems.push(`组 ${key ?? '未分组'} 任务数 ${group.totalTasks}，组内用户合计 ${tasks}`);
    for (const item of group.amounts || []) {
      const denominator = total[item.unit] || 0;
      const share = item.share_basis_points;
      if (denominator === 0) {
        if (share !== null) problems.push(`组 ${key ?? '未分组'} ${item.unit} 单位总额为 0，占比却是 ${share}`);
      } else if (share === null || Math.abs(share - (Number(item.amount) / denominator) * 10000) > 1) {
        problems.push(`组 ${key ?? '未分组'} ${item.unit} 占比 ${share}，按金额应约为 ${((Number(item.amount) / denominator) * 10000).toFixed(1)}`);
      }
    }
  }
  const sums = {};
  for (const group of groups) for (const item of group.amounts || []) sums[item.unit] = (sums[item.unit] || 0) + Number(item.amount);
  for (const unit of new Set([...Object.keys(total), ...Object.keys(sums)])) {
    if (!close(total[unit] || 0, sums[unit] || 0)) problems.push(`${unit} 各组合计 ${sums[unit] || 0}，total ${total[unit] || 0}`);
  }
  return problems;
}
/**
 * Reads the administration usage until it is self-consistent (at most 3 reads): other accounts on the environment
 * may finish tasks between the user rows and the group rows of one read.
 */
async function consistentUsage() {
  let last = null;
  for (let attempt = 1; attempt <= 3; attempt += 1) {
    const [status, usage] = await adminApi('GET', `${ADMIN_USAGE}?days=7`);
    if (status !== 200) throw new Error(`admin usage ${status}`);
    last = { usage, problems: usageProblems(usage), attempt };
    if (!last.problems.length) return last;
    await sleep(3000);
  }
  return last;
}
const groupRow = (usage, key) => (usage.byGroup || []).find((group) => (group.projectGroupKey ?? null) === key);
const groupText = (usage, key) => {
  const row = groupRow(usage, key);
  if (!row) return `${key ?? '未分组'}：无此行`;
  return `${key ?? '未分组'}：任务 ${row.totalTasks}，${(row.amounts || []).map((item) => `${item.unit}=${item.amount}（${item.share_basis_points ?? 'null'} bp）`).join(' ') || '无金额'}`;
};
/** Keys anywhere in a JSON value, to look for group fields in the account usage. */
function jsonKeys(value, keys = []) {
  if (Array.isArray(value)) value.forEach((item) => jsonKeys(item, keys));
  else if (value && typeof value === 'object') for (const [key, item] of Object.entries(value)) { keys.push(key); jsonKeys(item, keys); }
  return keys;
}

async function itemP1() {
  if (!ADMIN_READY) {
    record('SKIP', 'P1', '#599 管理端项目组 KEY', '没有 ADMIN_USERNAME/ADMIN_PASSWORD：经 release-env.py smoke 运行才会注入');
    return;
  }
  const messages = (await publicSchema())?.administration?.users?.messages || {};
  const invalidText = messages.invalid_project_group || '';
  await check('P1', '建 3 个测试用户（r190- 前缀，初始未分组）', async () => {
    for (const suffix of ['a', 'b', 'c']) {
      const username = `${tag}-${suffix}`;
      const password = randomBytes(18).toString('base64url');
      const [status, body] = await adminApi('POST', ADMIN_USERS, { username, password, name: `${tag} ${suffix}`, email: `${username}@example.test`, isAdmin: false });
      if (status !== 201 || !body.data?.id) return { ok: false, detail: `建 ${username} 返回 ${status} ${body.error || ''}` };
      p599.users.push({ suffix, username, password, id: body.data.id });
    }
    const groups = await Promise.all(p599.users.map((user) => storedGroup(user)));
    return { ok: groups.every((group) => group === null), detail: p599.users.map((user, index) => `${user.username}（${user.id}）组=${JSON.stringify(groups[index])}`).join('；') };
  });
  if (p599.users.length !== 3) return;
  const [a, b, c] = ['a', 'b', 'c'].map(p599User);
  await check('P1', `合法 KEY 的 PATCH 成功：A→${p599.groups.a}、B→${p599.groups.b}，C 不设（未分组）`, async () => {
    const replies = [await patchGroup(a, p599.groups.a), await patchGroup(b, p599.groups.b)];
    const stored = [await storedGroup(a), await storedGroup(b), await storedGroup(c)];
    return {
      ok: replies.every(([status]) => status === 200) && replies[0][1].data?.projectGroupKey === p599.groups.a && replies[1][1].data?.projectGroupKey === p599.groups.b
        && stored[0] === p599.groups.a && stored[1] === p599.groups.b && stored[2] === null,
      detail: `PATCH ${replies.map(([status, body]) => `${status}→${JSON.stringify(body.data?.projectGroupKey)}`).join('，')}；列表里 A=${JSON.stringify(stored[0])} B=${JSON.stringify(stored[1])} C=${JSON.stringify(stored[2])}`,
    };
  });
  const invalid = { 大写: 'R190-GRP', 空格: 'r190 grp', 超长33位: `r${'1'.repeat(32)}`, 连字符开头: '-r190-grp' };
  for (const [kind, value] of Object.entries(invalid)) {
    await check('P1', `非法 KEY（${kind} ${JSON.stringify(value)}）PATCH 被拒 400 并有文案，值不变`, async () => {
      const [status, body] = await patchGroup(c, value);
      const after = await storedGroup(c);
      const textOk = invalidText ? body.error === invalidText : Boolean(body.error);
      return {
        ok: status === 400 && textOk && after === null,
        detail: `${status} 文案=${JSON.stringify(body.error || '')}${invalidText ? `（Schema：${JSON.stringify(invalidText)}）` : '（公开 Schema 没有该文案，只核对非空）'}；之后 C=${JSON.stringify(after)}`,
      };
    });
  }
  await check('P1', '建用户时带非法 KEY 也被拒 400，不建出用户', async () => {
    const username = `${tag}-x`;
    const [status, body] = await adminApi('POST', ADMIN_USERS, { username, password: randomBytes(18).toString('base64url'), name: `${tag} x`, email: `${username}@example.test`, isAdmin: false, projectGroupKey: 'Bad Key' });
    const made = (await adminUserList()).find((user) => user.username === username);
    if (made) p599.strays.push({ username, id: made.id });
    return { ok: status === 400 && Boolean(body.error) && !made, detail: `${status} 文案=${JSON.stringify(body.error || '')}；用户被建出=${Boolean(made)}` };
  });
  // Observed only, not judged: what an empty string and null write.
  const observe = async (label, payload) => {
    await patchGroup(c, 'r190-tmp');
    const [status, body] = await adminApi('PATCH', `${ADMIN_USERS}/${encodeURIComponent(c.id)}`, payload);
    const returned = body.data && 'projectGroupKey' in body.data ? JSON.stringify(body.data.projectGroupKey) : `（无该字段）${body.error ? ` 错误=${JSON.stringify(body.error)}` : ''}`;
    return `${label}：${status}，返回 ${returned}，列表里存为 ${JSON.stringify(await storedGroup(c))}`;
  };
  const observed = [
    await observe('""', { projectGroupKey: '' }),
    await observe('null', { projectGroupKey: null }),
    await observe('"  r190-trim  "', { projectGroupKey: '  r190-trim  ' }),
    await observe('不带该字段（只改 name）', { name: `${tag} c` }),
  ];
  record('INFO', 'P1', '空串、null、首尾空格与不带字段的写入（只记录，不判）', `先设 r190-tmp 再 PATCH ${observed.join('；')}`);
  if (await storedGroup(c) !== null) await patchGroup(c, '');
  if (await storedGroup(c) !== null) record('FAIL', 'P1', 'C 回到未分组', `清空后 C 仍是 ${JSON.stringify(await storedGroup(c))}，P2 的未分组核对不可信`);
}

async function itemP2() {
  if (p599.users.length !== 3) { record('SKIP', 'P2', '#599 项目组支出占比', '需要先跑 P1 建出 3 个用户'); return; }
  await check('P2', `三个用户各自登录做 fake 文生图（A ${p599.counts.a} 张、B ${p599.counts.b} 张、C ${p599.counts.c} 张）`, async () => {
    const lines = [];
    for (const user of p599.users) {
      const context = await signedInContext(user.username, user.password);
      try {
        const count = p599.counts[user.suffix];
        const [created, session] = await callAs(context, 'POST', '/api/chat/sessions', { title: `${tag} 项目组 ${user.suffix}` });
        if (![200, 201].includes(created) || !session.id) throw new Error(`${user.username} create session ${created}`);
        const markers = Array.from({ length: count }, (_, index) => `[[fake:tool:refine_image ${JSON.stringify({ prompt: `group test ${user.suffix} ${index + 1}: a small clay cup` })}]]`);
        const [sent] = await callAs(context, 'POST', `/api/chat/sessions/${session.id}/messages`, { content: `出 ${count} 张图 ${markers.join(' ')}`, creative_domain: 'image' });
        if (![200, 201, 202].includes(sent)) throw new Error(`${user.username} send ${sent}`);
        const done = await waitFor(async () => {
          const [, pending] = await callAs(context, 'GET', `/api/v1/chat/sessions/${session.id}/pending-approvals`);
          for (const card of (pending.items || []).filter((item) => item.status === 'pending')) {
            await callAs(context, 'PUT', `/api/v1/tasks/grants/${card.request_id}`, { decision: 'approve' });
          }
          const [, body] = await callAs(context, 'GET', `/api/v1/tasks?session_id=${session.id}&limit=50`);
          const tasks = body.tasks || body.data || [];
          return tasks.filter((task) => task.state === 'SUCCEEDED').length >= count && tasks.every((task) => ['SUCCEEDED', 'FAILED', 'CANCELED', 'CANCELLED'].includes(task.state)) ? tasks : null;
        }, 180, 2000);
        if (!done) throw new Error(`${user.username}: ${count} fake tasks did not succeed in 180 s`);
        user.tasks = done.length;
        lines.push(`${user.username} 会话 ${session.id}：${done.length} 个任务（${done.map((task) => `${task.capability}:${task.state}`).join(',')}）`);
      } finally {
        await context.close();
      }
    }
    return { ok: true, detail: lines.join('；') };
  });
  const read = await consistentUsage();
  const usage = read.usage;
  const rowOf = (user) => (usage.data || []).find((row) => row.userId === user.id);
  const mine = p599.users.map((user) => ({ user, row: rowOf(user), amounts: unitAmounts(rowOf(user)) }));
  p599.before = mine;
  await check('P2', '三个用户的金额都大于 0 且各不相同', async () => {
    const texts = mine.map(({ amounts }) => amountsText(amounts));
    const positive = mine.every(({ amounts }) => Object.values(amounts).some((amount) => amount > 0));
    return { ok: positive && new Set(texts).size === 3, detail: mine.map(({ user, row }, index) => `${user.suffix}（组 ${JSON.stringify(row?.projectGroupKey ?? null)}，任务 ${row?.totalTasks ?? '无行'}）${texts[index]}`).join('；') };
  });
  await check('P2', `byGroup 里有 ${p599.groups.a}、${p599.groups.b} 和一行未分组（key 为 null）`, async () => {
    const keys = (usage.byGroup || []).map((group) => group.projectGroupKey ?? null);
    const nullRows = keys.filter((key) => key === null).length;
    return { ok: keys.includes(p599.groups.a) && keys.includes(p599.groups.b) && nullRows === 1, detail: `byGroup 的 key=${JSON.stringify(keys)}` };
  });
  await check('P2', '每组金额 = 组内用户金额之和，各组合计 = total；各行占比与金额占比一致（±1 bp）', async () => ({
    ok: read.problems.length === 0,
    detail: `第 ${read.attempt} 次读取；${read.problems.length ? `不一致：${read.problems.join('；')}` : '全部一致'}；${(usage.byGroup || []).map((group) => groupText(usage, group.projectGroupKey ?? null)).join('；')}；total ${amountsText(unitAmounts(usage.total))}`,
  }));
  await check('P2', `${p599.groups.a} 组的金额就是 A 的金额、${p599.groups.b} 组就是 B 的金额`, async () => {
    const a = unitAmounts({ price_totals: (groupRow(usage, p599.groups.a)?.amounts || []) });
    const b = unitAmounts({ price_totals: (groupRow(usage, p599.groups.b)?.amounts || []) });
    const same = (left, right) => Object.keys({ ...left, ...right }).every((unit) => close(left[unit] || 0, right[unit] || 0));
    return { ok: same(a, mine[0].amounts) && same(b, mine[1].amounts), detail: `${groupText(usage, p599.groups.a)}，A ${amountsText(mine[0].amounts)}；${groupText(usage, p599.groups.b)}，B ${amountsText(mine[1].amounts)}` };
  });
  await check('P2', '分母为 0（无任何用量）时占比为 null', async () => {
    const [status, empty] = await adminApi('GET', `${ADMIN_USAGE}?startDate=2020-01-01&endDate=2020-01-07`);
    const shares = (empty.byGroup || []).flatMap((group) => (group.amounts || []).map((item) => item.share_basis_points));
    if (status === 200 && shares.length === 0) {
      return { skip: true, detail: `没有用量的范围（2020-01-01～2020-01-07）返回 byGroup=${JSON.stringify(empty.byGroup)}、total=${JSON.stringify(empty.total)}：没有组行也就没有占比字段；产品侧造不出“单位总额为 0 而仍有组行”的数据，null 分支只能由服务端单测覆盖（有数据时的 null⇔总额 0 已在上一项逐行核对）` };
    }
    return { ok: status === 200 && shares.every((share) => share === null), detail: `${status} 占比=${JSON.stringify(shares)}` };
  });
  await check('P2', '账户用量接口（各用户读自己的）不含任何分组字段', async () => {
    const lines = [];
    let ok = true;
    for (const user of p599.users) {
      const context = await signedInContext(user.username, user.password);
      try {
        const [status, body] = await callAs(context, 'GET', ACCOUNT_USAGE);
        const keys = jsonKeys(body);
        const leaks = keys.filter((key) => /group/i.test(key));
        const valueLeak = Object.values(p599.groups).some((key) => JSON.stringify(body).includes(key));
        ok &&= status === 200 && !leaks.length && !valueLeak;
        lines.push(`${user.suffix}：${status} 字段=${JSON.stringify([...new Set(keys)])}${leaks.length || valueLeak ? ` 含分组：${JSON.stringify(leaks)} 值含 KEY=${valueLeak}` : ''}`);
      } finally {
        await context.close();
      }
    }
    return { ok, detail: lines.join('；') };
  });
}

async function itemP3() {
  if (!p599.before) { record('SKIP', 'P3', '#599 改组后归集', '需要先跑 P1、P2'); return; }
  const [a, b] = [p599User('a'), p599User('b')];
  await check('P3', `A 从 ${p599.groups.a} 改到 ${p599.groups.b} 后，A 的历史金额随当前组归到 ${p599.groups.b}，${p599.groups.a} 为空或消失`, async () => {
    const [status] = await patchGroup(a, p599.groups.b);
    const read = await consistentUsage();
    const usage = read.usage;
    const amountsOf = (key) => unitAmounts({ price_totals: (groupRow(usage, key)?.amounts || []) });
    const rowA = (usage.data || []).find((row) => row.userId === a.id);
    const rowB = (usage.data || []).find((row) => row.userId === b.id);
    const expected = {};
    for (const amounts of [unitAmounts(rowA), unitAmounts(rowB)]) for (const [unit, amount] of Object.entries(amounts)) expected[unit] = (expected[unit] || 0) + amount;
    const groupB = amountsOf(p599.groups.b);
    const sumOk = Object.keys({ ...expected, ...groupB }).every((unit) => close(expected[unit] || 0, groupB[unit] || 0));
    const rowGroupA = groupRow(usage, p599.groups.a);
    const goneA = !rowGroupA || (Number(rowGroupA.totalTasks) === 0 && (rowGroupA.amounts || []).every((item) => Number(item.amount) === 0));
    const unchangedA = close(Object.values(unitAmounts(rowA)).reduce((sum, value) => sum + value, 0), Object.values(p599.before[0].amounts).reduce((sum, value) => sum + value, 0));
    return {
      ok: status === 200 && rowA?.projectGroupKey === p599.groups.b && sumOk && goneA && unchangedA && read.problems.length === 0,
      detail: `PATCH ${status}；A 行的组=${JSON.stringify(rowA?.projectGroupKey)}，A 金额 ${amountsText(unitAmounts(rowA))}（改组前 ${amountsText(p599.before[0].amounts)}）；`
        + `${groupText(usage, p599.groups.b)}，应为 A+B ${amountsText(expected)}；${groupText(usage, p599.groups.a)}；整体一致性：${read.problems.length ? read.problems.join('；') : '一致'}`,
    };
  });
}

async function itemP4() {
  if (p599.users.length !== 3) { record('SKIP', 'P4', '#599 管理页', '需要先跑 P1 建出 3 个用户'); return; }
  const c = p599User('c');
  const adminPage = await (await adminContext()).newPage();
  adminPage.on('dialog', (dialog) => dialog.dismiss().catch(() => {}));
  const adminText = async (pathKeys) => adminPage.evaluate((keys) => keys.reduce((node, key) => node?.[key], window.__ART_AI_PRODUCT_SCHEMA__) ?? '', pathKeys);
  try {
    await check('P4', `管理端用户页给 C 设项目组 ${p599.groups.c} 并保存，刷新后仍在`, async () => {
      await adminPage.goto(`${base}/admin/users`, { waitUntil: 'load' });
      const row = adminPage.locator('tbody tr').filter({ hasText: c.username }).first();
      await row.waitFor({ timeout: 30000 });
      const editLabel = await adminText(['administration', 'users', 'presentation', 'text', 'edit']);
      const saveLabel = await adminText(['administration', 'users', 'presentation', 'text', 'save']);
      await row.getByRole('button', { name: editLabel, exact: true }).click();
      const input = adminPage.locator('#dlg-project-group');
      await input.waitFor({ timeout: 10000 });
      await input.fill(p599.groups.c);
      const dialog = adminPage.getByRole('dialog');
      await dialog.getByRole('button', { name: saveLabel, exact: true }).click();
      await dialog.waitFor({ state: 'hidden', timeout: 15000 });
      await adminPage.waitForTimeout(1000);
      const cell = async () => (await adminPage.locator('tbody tr').filter({ hasText: c.username }).first().innerText()).includes(p599.groups.c);
      const shownBefore = await cell();
      await adminPage.reload({ waitUntil: 'load' });
      await adminPage.locator('tbody tr').filter({ hasText: c.username }).first().waitFor({ timeout: 30000 });
      const shownAfter = await cell();
      const stored = await storedGroup(c);
      const file = await shotOf(adminPage, 'P4', 'admin-users');
      return { ok: shownBefore && shownAfter && stored === p599.groups.c, detail: `保存后行内显示=${shownBefore}，刷新后=${shownAfter}，接口存为 ${JSON.stringify(stored)} 截图 ${file}` };
    });
    await check('P4', '用量页有「项目组支出占比」表：组行、未分组行用 Schema 文案、百分比两位小数且与接口一致；用户表带项目组列', async () => {
      await adminPage.goto(`${base}/admin`, { waitUntil: 'load' });
      const card = adminPage.locator('[data-testid=admin-usage-groups]');
      await card.locator('tbody tr').first().waitFor({ timeout: 30000 });
      await adminPage.waitForTimeout(1500);
      const usageText = ['administration', 'usage', 'presentation', 'text'];
      const title = await adminText([...usageText, 'group_title']);
      const ungrouped = await adminText([...usageText, 'group_ungrouped']);
      const rows = await card.locator('tbody tr').evaluateAll((items) => items.map((item) => [...item.querySelectorAll('td')].map((cell) => cell.innerText.trim())));
      const [, usage] = await adminApi('GET', `${ADMIN_USAGE}?days=7`);
      const problems = [];
      const cardTitle = (await card.innerText()).includes(title);
      for (const key of [p599.groups.b, p599.groups.c, null]) {
        const label = key ?? ungrouped;
        const shown = rows.find((cells) => cells[0] === label);
        const apiRow = groupRow(usage, key);
        if (!shown) { problems.push(`没有「${label}」行`); continue; }
        const percents = shown[4].match(/\d+(?:\.\d+)?%/g) || [];
        if (!percents.length || percents.some((percent) => !/^\d+\.\d{2}%$/.test(percent))) problems.push(`「${label}」的占比不是两位小数：${shown[4]}`);
        const expected = (apiRow?.amounts || []).map((item) => (item.share_basis_points === null ? null : `${(item.share_basis_points / 100).toFixed(2)}%`)).filter(Boolean);
        if (JSON.stringify(percents) !== JSON.stringify(expected)) problems.push(`「${label}」页面 ${JSON.stringify(percents)}，接口 ${JSON.stringify(expected)}`);
      }
      // The users table further down carries each user's group.
      const userRows = await adminPage.locator('tbody tr').evaluateAll((items) => items.map((item) => item.innerText.replace(/\s+/g, ' ')));
      for (const user of p599.users) {
        const expectedGroup = { a: p599.groups.b, b: p599.groups.b, c: p599.groups.c }[user.suffix];
        if (!userRows.some((line) => line.includes(user.username) && line.includes(expectedGroup)) && !userRows.some((line) => line.includes(`${tag} ${user.suffix}`) && line.includes(expectedGroup))) {
          problems.push(`用户表没有 ${user.username} 带组 ${expectedGroup} 的行`);
        }
      }
      const file = await shotOf(adminPage, 'P4', 'admin-usage-groups', true);
      return { ok: cardTitle && !problems.length, detail: `标题「${title}」出现=${cardTitle}；组表 ${JSON.stringify(rows)}；${problems.length ? problems.join('；') : '组行、未分组文案、两位小数与接口一致'} 截图 ${file}` };
    });
  } finally {
    await adminPage.close();
  }
  await check('P4', '普通用户（Agent 测试账号）访问管理接口得到 401/403，改不了别人的组', async () => {
    const replies = [
      ['GET 用户列表', await api('GET', ADMIN_USERS)],
      ['GET 用量', await api('GET', `${ADMIN_USAGE}?days=7`)],
      ['PATCH 用户组', await api('PATCH', `${ADMIN_USERS}/${encodeURIComponent(c.id)}`, { projectGroupKey: 'r190-hijack' })],
    ];
    const stored = await storedGroup(c);
    return {
      ok: replies.every(([, [status]]) => [401, 403].includes(status)) && stored === p599.groups.c,
      detail: `${replies.map(([name, [status, body]]) => `${name} ${status}${body.data ? '（有数据返回）' : ''}`).join('，')}；之后 C=${JSON.stringify(stored)}`,
    };
  });
}

/** Deletes the users this run made, one by one by id (the users page's delete; the product keeps them soft-deleted). */
async function cleanupP599() {
  if (!p599.admin) return;
  for (const user of [...p599.users, ...p599.strays]) {
    const [status] = await adminApi('DELETE', `${ADMIN_USERS}/${encodeURIComponent(user.id)}`);
    console.log(`cleanup: delete ${user.username} ${status}`);
  }
  await p599.admin.close().catch(() => {});
}

// ---------------------------------------------------------------- run
const runners = {
  586: item586, 559: item559, 587: item587, 560: item560, '548-skill': item548Skill, '548-chip': item548Chip,
  n1: itemN1, n2: itemN2, n3: itemN3, n4: itemN4, n5: itemN5, n6: itemN6, m1: itemM1, m2: itemM2,
  p1: itemP1, p2: itemP2, p3: itemP3, p4: itemP4,
};
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
  // Remove this run's test users and personal Skills even when a check failed half-way.
  try { await cleanupP599(); } catch (error) { console.log(`cleanup: test users ${error.message.split('\n')[0]}`); }
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
