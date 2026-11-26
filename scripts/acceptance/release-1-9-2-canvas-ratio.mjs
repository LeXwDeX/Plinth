// #653 的 DEV 浏览器验收（只用 fake 上游，零费用）：画布以比例为准、与模型分开，按当前模型最高画质出图。
//
// 覆盖（每项 PASS/FAIL/SKIP 带截图）：
//   （C2 到 C4 先经 API 造一张 fake 图并双击版本瓦片放上画板：空画板不能 @画布 发送。）
//   C1  新画布默认 1:1：新会话打开设置弹窗「生成参数」页，「画布比例」选中 1:1，页面上不显示画布像素。
//   C2  OpenAI 系模型（GPT Image）下把画布设成 16:9，经 @画布 发 refine：画板上方提示「按画布比例 16:9、以当前模型的
//       最高画质出图」与 3840×2160，冻结参数 size=3840x2160。
//   C3  同一画布切到 Gemini：画布比例仍是 16:9（切模型不改画布）；@画布 仍可发送，提示写明最高画质与 16:9（5504×3072），
//       冻结参数 aspect_ratio=16:9、image_size=4K（最高档）。
//   C4  无该比例时的提示：Nano Banana 下把画布设成 21:9，参数页提示「当前模型没有画布比例 21:9」与最接近的 16:9，
//       有「画布改为 16:9」；经 @画布 发送时画板上方是 follows_nearest 提示，冻结参数 aspect_ratio=16:9；
//       点「画布改为 16:9」后画布比例变成 16:9、提示消失。目标环境没有 Nano Banana 时记 SKIP。
//   C5  多张图加矢量标记（dev_test.md §10.3e 的固定验收项）：上传三张纯色图（红、绿、蓝，各 240×240）放在画板框内，再画一笔铅笔
//       和一个矩形；@画布 发送，按消息里的快照地址取回并解码：三张图的像素数各接近 57600、标记像素 > 300、声明 5 层；换到 16:9
//       后撤销、重做比例来回、图层行不变；再发，快照 16:9、三张图和标记像素基本不变、声明的图层与来源不变；再切到 Gemini，
//       快照与换前逐项相同（只有出图尺寸变）。
//
// 用法（地址与 Agent 专用账号由 release-env.py 注入，凭据只在子进程环境里；要在 dev-test 发布 #653 之后跑）：
//   python3 scripts/release-env.py smoke dev-test -- node scripts/acceptance/release-1-9-2-canvas-ratio.mjs
// 环境变量：
//   R192_ITEMS=c1,c2,c3,c4,c5  只跑其中几项（默认全部；c3 依赖 c2 建的会话，单跑 c3 时会先跑 c2 的准备步骤）
//   OUT=<目录>  截图与 results.json 的位置（默认系统临时目录 plinth-acceptance-r192-*，不要写进仓库）
//   PLAYWRIGHT_MODULE_PATH  Playwright 的位置（工作树没有 node_modules 时指向主检出的 node_modules/playwright）
//
// 造数：都在 Agent 专用账号下新建会话，标题带 r192-<时间戳>；工作区的模型选择存在浏览器本地，结束时放回原值。
// 目标环境不是 fake 上游形态（ART_AI_UPSTREAM_SHAPE!=fake）时拒绝运行（退出码 2），不会发起真实生成。
// 不打印口令、Cookie 或签名 URL。最后一行输出 `PASS=n FAIL=n SKIP=n`；有 FAIL 时退出码 1。
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import zlib from 'node:zlib';
import { resolvePlaywright } from './browser_matrix/lib.mjs';

const base = (process.env.BASE || '').replace(/\/+$/, '');
const missing = ['BASE', 'AGENT_TEST_USERNAME', 'AGENT_TEST_PASSWORD'].filter((name) => !process.env[name]);
if (missing.length) {
  console.error(`missing environment: ${missing.join(', ')}\nrun through: python3 scripts/release-env.py smoke dev-test -- node ${path.relative(process.cwd(), new URL(import.meta.url).pathname)}`);
  process.exit(2);
}
if (process.env.ART_AI_UPSTREAM_SHAPE !== 'fake') {
  console.error(`refusing to run: the target upstream shape is "${process.env.ART_AI_UPSTREAM_SHAPE || 'unknown'}", not fake; the turns would start real generations`);
  process.exit(2);
}

const ITEMS = (process.env.R192_ITEMS || 'c1,c2,c3,c4,c5').split(',').map((item) => item.trim()).filter(Boolean);
const tag = `r192-${new Date().toISOString().replace(/[-:T]/g, '').slice(0, 14)}`;
const OUT = process.env.OUT || fs.mkdtempSync(path.join(os.tmpdir(), 'plinth-acceptance-r192-'));
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
  if (!value) throw new Error(`Schema message ${key} missing on the page (is #653 deployed?)`);
  return value;
}
const fill = (template, values) => Object.entries(values).reduce((result, [name, value]) => result.replaceAll(`{${name}}`, value), template);

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
const PREFS_KEY = 'art-ai-workspace-prefs';
const readPrefs = async () => page.evaluate((key) => { try { return localStorage.getItem(key); } catch { return null; } }, PREFS_KEY);
async function writePrefs(value) {
  await page.evaluate(([key, next]) => {
    try { if (next === null) localStorage.removeItem(key); else localStorage.setItem(key, next); } catch {}
  }, [PREFS_KEY, value]);
}
/** Picks a 2D model in the settings dialog by the first row whose text matches; null when no row matches. */
async function chooseImageModel(pattern) {
  await openSettings('models');
  const rows = page.locator('[data-testid=workspace-model-picker-image] [role=menuitemradio]');
  const texts = await rows.evaluateAll((items) => items.map((item) => item.innerText.replace(/\s+/g, ' ').trim()));
  const index = texts.findIndex((value) => pattern.test(value));
  if (index < 0) { await closeSettings(); return null; }
  await rows.nth(index).click();
  await page.waitForTimeout(1200);
  await closeSettings();
  return texts[index];
}
/** The canvas ratio the 生成参数 page shows (#653), and the notice under it, read with the dialog open. */
async function canvasPanel() {
  await openSettings('params');
  const picker = tid('canvas-ratio-picker').first();
  await picker.waitFor({ timeout: 10000 });
  const ratio = await picker.getAttribute('data-ratio');
  const checked = await picker.locator('[role=radio][aria-checked=true]').allInnerTexts();
  const notice = tid('canvas-size-notice', settings).first();
  const kind = await notice.count() ? await notice.getAttribute('data-kind') : null;
  const noticeText = await notice.count() ? (await notice.innerText()).replace(/\s+/g, ' ').trim() : '';
  const detail = tid('unavailable-detail', notice);
  const detailText = await detail.count() ? (await detail.first().textContent() || '').replace(/\s+/g, ' ').trim() : '';
  const dialogText = await settings.innerText();
  return { picker, ratio, checked: checked.map((value) => value.trim()), kind, noticeText, detailText, dialogText };
}
async function pickRatio(ratio) {
  const { picker } = await canvasPanel();
  await picker.getByRole('radio', { name: ratio, exact: true }).click();
  await page.waitForTimeout(1500);
  const after = await canvasPanel();
  await closeSettings();
  return after.ratio;
}
const composerBox = () => page.locator('[data-testid=studio-composer] textarea').first();
/** Puts @画布 into the box (the canvas turn of #644), then returns the board notice the page shows for it. */
async function mentionBoard() {
  const boardToken = `@${await text('ui.studio.composer.mention.token_board')}`;
  const box = composerBox();
  await box.fill('');
  await box.click();
  await box.pressSequentially('@');
  const list = tid('composer-mention-list');
  await list.waitFor({ timeout: 10000 });
  await list.locator(`[data-testid=composer-mention-option][data-handle="${boardToken}"]`).first().click();
  await page.waitForTimeout(1500);
  const notice = page.locator('[data-testid=canvas-size-notice]').first();
  if (!await notice.count()) return { kind: null, text: '' };
  const detail = tid('unavailable-detail', notice);
  const body = await detail.count() ? await detail.first().textContent() : await notice.innerText();
  return { kind: await notice.getAttribute('data-kind'), text: String(body || '').replace(/\s+/g, ' ').trim() };
}
/** Sends what is in the box plus `content` and returns the posted body and the new task's frozen parameters. */
async function sendBoardTurn(sid, content) {
  const known = (await sessionTasks(sid)).map((task) => task.id);
  const before = await finalAssistantCount(sid);
  const box = composerBox();
  await box.press('End');
  const posted = page.waitForRequest((request) => request.method() === 'POST' && request.url().includes(`/api/chat/sessions/${sid}/messages`), { timeout: 45000 });
  await page.keyboard.insertText(` ${content}`);
  await tid('composer-send').first().click();
  const request = await posted;
  let body = {};
  try { body = request.postDataJSON() || {}; } catch {}
  await waitFor(async () => (await finalAssistantCount(sid)) > before, 150);
  const made = await waitFor(async () => (await sessionTasks(sid)).find((task) => !known.includes(task.id) && ['SUCCEEDED', 'FAILED'].includes(task.state)) || null, 150, 1500);
  const [, detail] = made ? await api('GET', `/api/v1/tasks/${made.id}`) : [0, {}];
  return { body, task: made, parameters: detail?.input?.parameters || {} };
}
async function inputSourceField() {
  const schema = await publicSchema();
  return { field: schema?.chat?.api?.fields?.input_source || 'input_source', canvas: schema?.chat?.context?.canvas_input_source || 'canvas' };
}
// ---------------------------------------------------------------- a picture on the board (an empty board refuses @画布)
/** One fake refine_image turn through the API; returns the new picture's task ID. */
async function seedPicture(sid, label) {
  const before = await finalAssistantCount(sid);
  for (let attempt = 0; attempt < 60; attempt += 1) {
    const [status] = await api('POST', `/api/chat/sessions/${sid}/messages`, { content: `画一个${label} [[fake:tool:refine_image ${JSON.stringify({ prompt: `a ${label} on a white background` })}]]`, creative_domain: 'image' });
    if (status !== 409) break;
    await sleep(1000);
  }
  if (!await waitFor(async () => (await finalAssistantCount(sid)) > before, 150)) throw new Error('the seed turn produced no reply');
  const made = await waitFor(async () => (await sessionTasks(sid)).find((task) => task.state === 'SUCCEEDED') || null, 150, 1500);
  if (!made) throw new Error('the fake picture did not succeed');
  return made.id;
}
/** Double-clicks the version tile so the picture sits on the board (the strip alone never puts it there, #559). */
async function putOnBoard(taskId) {
  const tile = page.locator(`[data-testid=version-tile][data-key="n:${taskId}"]`);
  await tile.waitFor({ timeout: 30000 });
  await tile.locator('button').first().dblclick();
  await page.waitForTimeout(2500);
  const layers = await tid('layer-row').count();
  if (layers < 1) throw new Error('the picture did not reach the board');
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
const ratioOutput = async (ratio, pixels) => fill(await text('ui.canvas.size.ratio_output'), { ratio, pixels });

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

// ---------------------------------------------------------------- C1: a new canvas is 1:1
async function itemC1() {
  const sid = await createSession(`${tag} 新画布`);
  await openSession(sid);
  await check('c1', '新画布默认 1:1：「画布比例」选中 1:1，参数页不显示画布像素', async () => {
    const panel = await canvasPanel();
    const file = await shot('c1', 'new-canvas');
    await closeSettings();
    const pixelsShown = /2048\s*[×x]\s*2048/.test(panel.dialogText);
    return {
      ok: panel.ratio === '1:1' && panel.checked.length === 1 && panel.checked[0] === '1:1' && !pixelsShown,
      detail: `data-ratio=${panel.ratio} 选中=${JSON.stringify(panel.checked)} 参数页出现 2048×2048=${pixelsShown} 截图 ${file}`,
    };
  });
}

// ---------------------------------------------------------------- C2/C3: a 16:9 canvas under GPT Image, then Gemini
const shared = {};
async function prepareC2() {
  if (shared.sid) return shared;
  shared.sid = await createSession(`${tag} 换模型`);
  const picture = await seedPicture(shared.sid, '红色方块');
  await openSession(shared.sid);
  await putOnBoard(picture);
  shared.gpt = await chooseImageModel(/gpt[\s-]*image/i);
  if (!shared.gpt) throw new Error('no GPT Image row in the 2D model picker');
  shared.ratio = await pickRatio('16:9');
  return shared;
}
async function itemC2() {
  const { sid, gpt, ratio } = await prepareC2();
  const { field, canvas } = await inputSourceField();
  await check('c2', `「${gpt}」下画布设为 16:9，@画布 发送：提示最高画质 3840×2160，冻结 size=3840x2160`, async () => {
    const notice = await mentionBoard();
    const want = fill(await text('ui.canvas.size.follows'), { ratio: '16:9', output: '3840×2160' });
    const file = await shot('c2', 'notice');
    const sent = await sendBoardTurn(sid, `把画面改成夜景 [[fake:tool:refine_image ${JSON.stringify({ prompt: 'make it a night scene' })}]]`);
    return {
      ok: ratio === '16:9' && notice.kind === 'follows' && notice.text === want && sent.body[field] === canvas
        && sent.task?.state === 'SUCCEEDED' && sent.parameters.size === '3840x2160',
      detail: `画布比例=${ratio} 提示 ${notice.kind}「${notice.text}」 期望「${want}」 POST ${field}=${JSON.stringify(sent.body[field] ?? null)} `
        + `任务=${sent.task ? `${sent.task.offering_id} ${sent.task.state}` : '无'} 冻结参数=${JSON.stringify(sent.parameters)} 截图 ${file}`,
    };
  });
}
async function itemC3() {
  const { sid } = await prepareC2();
  const { field, canvas } = await inputSourceField();
  const gemini = await chooseImageModel(/gemini/i);
  if (!gemini) { record('SKIP', 'c3', '切到 Gemini', '2D 模型列表里没有 Gemini'); return; }
  await check('c3', `切到「${gemini}」不改画布：画布比例仍是 16:9`, async () => {
    const panel = await canvasPanel();
    const file = await shot('c3', 'switched');
    await closeSettings();
    return { ok: panel.ratio === '16:9' && panel.kind === null, detail: `data-ratio=${panel.ratio} 提示=${panel.kind ?? '无'} 截图 ${file}` };
  });
  await check('c3', '@画布 仍可发送：提示最高画质 16:9（5504×3072），冻结 aspect_ratio=16:9、image_size=4K', async () => {
    const notice = await mentionBoard();
    const want = fill(await text('ui.canvas.size.follows'), { ratio: '16:9', output: await ratioOutput('16:9', '5504×3072') });
    const file = await shot('c3', 'notice');
    const sent = await sendBoardTurn(sid, `把画面改成雪景 [[fake:tool:refine_image ${JSON.stringify({ prompt: 'make it a snowy scene' })}]]`);
    return {
      ok: notice.kind === 'follows' && notice.text === want && sent.body[field] === canvas && sent.task?.state === 'SUCCEEDED'
        && /gemini/i.test(String(sent.task?.offering_id || '')) && sent.parameters.aspect_ratio === '16:9' && sent.parameters.image_size === '4K',
      detail: `提示 ${notice.kind}「${notice.text}」 期望「${want}」 POST ${field}=${JSON.stringify(sent.body[field] ?? null)} `
        + `任务=${sent.task ? `${sent.task.offering_id} ${sent.task.state}` : '无'} 冻结参数=${JSON.stringify(sent.parameters)} 截图 ${file}`,
    };
  });
}

// ---------------------------------------------------------------- C4: a model without the canvas ratio
async function itemC4() {
  const sid = await createSession(`${tag} 无该比例`);
  const picture = await seedPicture(sid, '蓝色球');
  await openSession(sid);
  await putOnBoard(picture);
  const banana = await chooseImageModel(/nano[\s-]*banana/i);
  if (!banana) { record('SKIP', 'c4', '无该比例时的提示', '2D 模型列表里没有 Nano Banana'); return; }
  const { field, canvas } = await inputSourceField();
  const ratio = await pickRatio('21:9');
  await check('c4', `「${banana}」下画布设为 21:9：参数页提示没有 21:9、按最接近的 16:9 出图，并有「画布改为 16:9」`, async () => {
    const panel = await canvasPanel();
    const want = fill(await text('ui.canvas.size.ratio_missing'), { ratio: '21:9', nearest: '16:9', output: await ratioOutput('16:9', '1344×768') });
    const adopt = tid('canvas-size-adopt', settings).first();
    const adoptText = await adopt.count() ? (await adopt.innerText()).trim() : '';
    const wantAdopt = fill(await text('ui.canvas.size.adopt'), { ratio: '16:9' });
    const file = await shot('c4', 'missing');
    await closeSettings();
    return {
      ok: ratio === '21:9' && panel.kind === 'ratio_missing' && panel.detailText === want && adoptText === wantAdopt,
      detail: `画布比例=${ratio} 提示 ${panel.kind}「${panel.detailText}」 期望「${want}」 按钮「${adoptText}」 截图 ${file}`,
    };
  });
  await check('c4', '@画布 发送：画板上方是最接近比例的提示，冻结 aspect_ratio=16:9', async () => {
    const notice = await mentionBoard();
    const want = fill(await text('ui.canvas.size.follows_nearest'), { ratio: '21:9', nearest: '16:9', output: await ratioOutput('16:9', '1344×768') });
    const file = await shot('c4', 'board-notice');
    const sent = await sendBoardTurn(sid, `把画面改成黄昏 [[fake:tool:refine_image ${JSON.stringify({ prompt: 'make it dusk' })}]]`);
    return {
      ok: notice.kind === 'follows_nearest' && notice.text === want && sent.body[field] === canvas && sent.task?.state === 'SUCCEEDED'
        && sent.parameters.aspect_ratio === '16:9',
      detail: `提示 ${notice.kind}「${notice.text}」 期望「${want}」 POST ${field}=${JSON.stringify(sent.body[field] ?? null)} `
        + `任务=${sent.task ? `${sent.task.offering_id} ${sent.task.state}` : '无'} 冻结参数=${JSON.stringify(sent.parameters)} 截图 ${file}`,
    };
  });
  await check('c4', '点「画布改为 16:9」：画布比例变成 16:9，缺比例提示消失', async () => {
    await openSettings('params');
    await tid('canvas-size-adopt', settings).first().click();
    await page.waitForTimeout(1500);
    const panel = await canvasPanel();
    const file = await shot('c4', 'adopted');
    await closeSettings();
    return { ok: panel.ratio === '16:9' && panel.kind === null, detail: `data-ratio=${panel.ratio} 提示=${panel.kind ?? '无'} 截图 ${file}` };
  });
}

// ---------------------------------------------------------------- run
// ---------------------------------------------------------------- C5: several pictures + vector marks survive ratio and model switches (dev_test.md §10.3e)
const crcTable = (() => { const table = []; for (let n = 0; n < 256; n += 1) { let c = n; for (let k = 0; k < 8; k += 1) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1; table[n] = c >>> 0; } return table; })();
const crc32 = (buffer) => { let c = 0xffffffff; for (const byte of buffer) c = crcTable[(c ^ byte) & 0xff] ^ (c >>> 8); return (c ^ 0xffffffff) >>> 0; };
const pngChunk = (type, data) => { const length = Buffer.alloc(4); length.writeUInt32BE(data.length); const body = Buffer.concat([Buffer.from(type), data]); const sum = Buffer.alloc(4); sum.writeUInt32BE(crc32(body)); return Buffer.concat([length, body, sum]); };
/** A solid-colour RGBA PNG, so the snapshot's pixels can be told apart by layer. */
function solidPng(width, height, [red, green, blue]) {
  const row = Buffer.alloc(1 + width * 4);
  for (let x = 0; x < width; x += 1) { row[1 + x * 4] = red; row[2 + x * 4] = green; row[3 + x * 4] = blue; row[4 + x * 4] = 255; }
  const header = Buffer.alloc(13); header.writeUInt32BE(width, 0); header.writeUInt32BE(height, 4); header[8] = 8; header[9] = 6;
  return Buffer.concat([Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]), pngChunk('IHDR', header), pngChunk('IDAT', zlib.deflateSync(Buffer.concat(Array.from({ length: height }, () => row)))), pngChunk('IEND', Buffer.alloc(0))]);
}
const SOLID = [['c5-red.png', [230, 30, 30]], ['c5-green.png', [30, 200, 30]], ['c5-blue.png', [30, 60, 230]]];
const SOLID_AREA = 240 * 240;
/** Size and pixel classes of a snapshot PNG, decoded in the page: the three picture colours, see-through, and the rest (marks). */
async function snapshotPixels(png) {
  return page.evaluate(async (base64) => {
    const bytes = Uint8Array.from(atob(base64), (char) => char.charCodeAt(0));
    const bitmap = await createImageBitmap(new Blob([bytes], { type: 'image/png' }));
    const canvas = new OffscreenCanvas(bitmap.width, bitmap.height);
    const context = canvas.getContext('2d');
    context.drawImage(bitmap, 0, 0);
    const { data } = context.getImageData(0, 0, bitmap.width, bitmap.height);
    const near = (r, g, b, [tr, tg, tb]) => Math.abs(r - tr) < 40 && Math.abs(g - tg) < 40 && Math.abs(b - tb) < 40;
    const out = { width: bitmap.width, height: bitmap.height, red: 0, green: 0, blue: 0, clear: 0, marks: 0 };
    for (let i = 0; i < data.length; i += 4) {
      const [r, g, b, a] = [data[i], data[i + 1], data[i + 2], data[i + 3]];
      if (a < 20) out.clear += 1;
      else if (near(r, g, b, [230, 30, 30])) out.red += 1;
      else if (near(r, g, b, [30, 200, 30])) out.green += 1;
      else if (near(r, g, b, [30, 60, 230])) out.blue += 1;
      else out.marks += 1;
    }
    return out;
  }, png.toString('base64'));
}
const layerRows = () => tid('layer-row').count();
/** One @画布 refine turn; returns the board snapshot the turn sent (fetched back by the asset URL in its message body, decoded in the page) and its layer claim. */
async function boardSnapshot(sid, label) {
  const sent = await sendBoardTurn(sid, `${label} [[fake:tool:refine_image ${JSON.stringify({ prompt: label })}]]`);
  const selection = (sent.body.selection || [])[0] || {};
  let pixels = null;
  if (selection.url) {
    const response = await ctx.request.get(base + selection.url, { headers: ORIGIN });
    const bytes = await response.body();
    if (response.status() === 200 && bytes.subarray(1, 4).toString() === 'PNG') pixels = await snapshotPixels(bytes);
  }
  return { sent, pixels, layers: selection.canvasLayers ?? null, sources: [...(selection.canvasSources || [])].sort().join(',') };
}
const within = (value, reference, tolerance) => Math.abs(value - reference) <= reference * tolerance;
async function itemC5() {
  const sid = await createSession(`${tag} 多图加标记`);
  await openSession(sid);
  const gpt = await chooseImageModel(/gpt[\s-]*image/i);
  if (!gpt) throw new Error('no GPT Image row in the 2D model picker');
  // Three differently coloured pictures in a row inside the frame, then a pencil stroke and a rectangle (vector marks).
  const dir = path.join(OUT, 'c5-inputs');
  fs.mkdirSync(dir, { recursive: true });
  const files = SOLID.map(([name, color]) => { const file = path.join(dir, name); fs.writeFileSync(file, solidPng(240, 240, color)); return file; });
  const chooser = page.waitForEvent('filechooser', { timeout: 15000 });
  await tid('canvas-tool-upload').click();
  await (await chooser).setFiles(files);
  await page.waitForTimeout(5000);
  const stage = await page.locator('canvas').first().boundingBox();
  if (!stage) throw new Error('no canvas stage');
  const cx = stage.x + stage.width / 2, cy = stage.y + stage.height / 2;
  await tid('canvas-tool-pencil').click();
  await page.mouse.move(cx - 120, cy + 70); await page.mouse.down(); await page.mouse.move(cx, cy + 90, { steps: 12 }); await page.mouse.move(cx + 120, cy + 70, { steps: 12 }); await page.mouse.up();
  await page.waitForTimeout(600);
  await tid('canvas-tool-shape').click();
  await page.mouse.move(cx - 100, cy - 110); await page.mouse.down(); await page.mouse.move(cx - 20, cy - 60, { steps: 10 }); await page.mouse.up();
  await page.waitForTimeout(600);
  await tid('canvas-tool-select').click();
  const rowsBefore = await layerRows();
  const base1 = await shot('c5', 'board-before');

  await mentionBoard();
  const a = await boardSnapshot(sid, '多图加标记 1:1');
  const rowsA = await layerRows();
  await check('c5', `1:1 画板：三张图加铅笔与矩形标记，@画布 快照含三张图和标记（声明 ${a.layers} 层）`, async () => {
    const px = a.pixels;
    const ok = !!px && px.width === px.height && a.layers === 5
      && [px.red, px.green, px.blue].every((value) => within(value, SOLID_AREA, 0.1)) && px.marks > 300;
    return { ok, detail: `快照 ${px ? `${px.width}×${px.height} 红 ${px.red} 绿 ${px.green} 蓝 ${px.blue} 透明 ${px.clear} 标记 ${px.marks}` : '没有取回'}（每张图 ${SOLID_AREA} 像素；声明 5 层＝3 张图＋铅笔＋矩形）截图 ${base1}` };
  });

  const ratio = await pickRatio('16:9');
  const rowsRatio = await layerRows();
  await tid('canvas-tool-undo').click();
  await page.waitForTimeout(1200);
  const undone = await canvasPanel(); await closeSettings();
  const rowsUndone = await layerRows();
  await tid('canvas-tool-redo').click();
  await page.waitForTimeout(1200);
  const redone = await canvasPanel(); await closeSettings();
  const rowsRedone = await layerRows();
  await check('c5', '换到 16:9 后撤销、重做：比例 1:1 与 16:9 来回，图层行始终不变', async () => {
    const file = await shot('c5', 'undo-redo');
    return { ok: ratio === '16:9' && undone.ratio === '1:1' && redone.ratio === '16:9' && [rowsRatio, rowsUndone, rowsRedone].every((rows) => rows === rowsA),
      detail: `比例 ${ratio} → 撤销 ${undone.ratio} → 重做 ${redone.ratio}；图层行 ${rowsA} → ${rowsRatio}、${rowsUndone}、${rowsRedone} 截图 ${file}` };
  });

  await mentionBoard();
  const b = await boardSnapshot(sid, '多图加标记 16:9');
  await check('c5', '16:9 画板：快照比例 16:9，三张图与标记仍完整，声明的图层与来源不变', async () => {
    const pa = a.pixels, pb = b.pixels;
    const ok = !!pa && !!pb && within(pb.width / pb.height, 16 / 9, 0.01) && b.layers === a.layers && b.sources === a.sources
      && [['red'], ['green'], ['blue']].every(([key]) => within(pb[key], pa[key], 0.03)) && within(pb.marks, pa.marks, 0.1);
    return { ok, detail: `快照 ${pb ? `${pb.width}×${pb.height} 红 ${pb.red} 绿 ${pb.green} 蓝 ${pb.blue} 标记 ${pb.marks}` : '没有取回'}（1:1 时 红 ${pa?.red} 绿 ${pa?.green} 蓝 ${pa?.blue} 标记 ${pa?.marks}）声明 ${b.layers} 层、来源 ${b.sources === a.sources ? '与 1:1 相同' : '不同'}` };
  });

  const gemini = await chooseImageModel(/gemini/i);
  await mentionBoard();
  const c = await boardSnapshot(sid, '多图加标记 Gemini');
  await check('c5', `换到「${gemini}」：快照与换模型前逐项相同，声明的图层与来源不变，只有出图尺寸随模型变`, async () => {
    const pb = b.pixels, pc = c.pixels;
    const same = !!pb && !!pc && ['width', 'height', 'red', 'green', 'blue', 'marks'].every((key) => pb[key] === pc[key]);
    return { ok: !!gemini && same && c.layers === b.layers && c.sources === b.sources, detail: `快照 ${pc ? `${pc.width}×${pc.height} 红 ${pc.red} 绿 ${pc.green} 蓝 ${pc.blue} 标记 ${pc.marks}` : '没有取回'}（换前 ${pb ? `${pb.width}×${pb.height} 红 ${pb.red} 绿 ${pb.green} 蓝 ${pb.blue} 标记 ${pb.marks}` : '无'}）声明 ${c.layers} 层 冻结参数 ${JSON.stringify(c.sent.parameters)}` };
  });
}

// ---------------------------------------------------------------- run
const runners = { c1: itemC1, c2: itemC2, c3: itemC3, c4: itemC4, c5: itemC5 };
await page.goto(`${base}/`, { waitUntil: 'load' });
const savedPrefs = await readPrefs();
try {
  for (const item of ITEMS) {
    const runner = runners[item];
    if (!runner) { record('FAIL', item, '未知的验收项', `可选：${Object.keys(runners).join(',')}`); continue; }
    try {
      await runner();
    } catch (error) {
      const file = await shot(item, 'error');
      record('FAIL', item, '运行中断', `${error.message.split('\n')[0]} (screenshot ${file})`);
    }
  }
} finally {
  // Put this browser's workspace model choice back.
  try {
    await closeSettings();
    await writePrefs(savedPrefs);
  } catch (error) { console.log(`cleanup: workspace model choice ${error.message.split('\n')[0]}`); }
  await browser.close();
}

if (pageErrors.length) console.log(`INFO: 页面未捕获异常 ${pageErrors.length} 条：${[...new Set(pageErrors)].slice(0, 5).join(' | ')}`);
fs.writeFileSync(path.join(OUT, 'results.json'), JSON.stringify({ base, tag, version: process.env.EXPECT_SHA || '', results, pageErrors }, null, 2));
const count = (status) => results.filter((result) => result.status === status).length;
console.log(`results: ${path.join(OUT, 'results.json')}`);
console.log(`PASS=${count('PASS')} FAIL=${count('FAIL')} SKIP=${count('SKIP')}`);
process.exit(count('FAIL') ? 1 : 0);
