// #653 的 DEV 浏览器验收（只用 fake 上游，零费用）：画布以比例为准、与模型分开，按当前模型最高画质出图。
//
// 覆盖（每项 PASS/FAIL/SKIP 带截图）：
//   C1  新画布默认 1:1：新会话打开设置弹窗「生成参数」页，「画布比例」选中 1:1，页面上不显示画布像素。
//   C2  OpenAI 系模型（GPT Image）下把画布设成 16:9，经 @画布 发 refine：画板上方提示「按画布比例 16:9、以当前模型的
//       最高画质出图」与 3840×2160，冻结参数 size=3840x2160。
//   C3  同一画布切到 Gemini：画布比例仍是 16:9（切模型不改画布）；@画布 仍可发送，提示写明最高画质与 16:9（5504×3072），
//       冻结参数 aspect_ratio=16:9、image_size=4K（最高档）。
//   C4  无该比例时的提示：Nano Banana 下把画布设成 21:9，参数页提示「当前模型没有画布比例 21:9」与最接近的 16:9，
//       有「画布改为 16:9」；经 @画布 发送时画板上方是 follows_nearest 提示，冻结参数 aspect_ratio=16:9；
//       点「画布改为 16:9」后画布比例变成 16:9、提示消失。目标环境没有 Nano Banana 时记 SKIP。
//
// 用法（地址与 Agent 专用账号由 release-env.py 注入，凭据只在子进程环境里；要在 dev-test 发布 #653 之后跑）：
//   python3 scripts/release-env.py smoke dev-test -- node scripts/acceptance/release-1-9-2-canvas-ratio.mjs
// 环境变量：
//   R192_ITEMS=c1,c2,c3,c4  只跑其中几项（默认全部；c3 依赖 c2 建的会话，单跑 c3 时会先跑 c2 的准备步骤）
//   OUT=<目录>  截图与 results.json 的位置（默认系统临时目录 plinth-acceptance-r192-*，不要写进仓库）
//   PLAYWRIGHT_MODULE_PATH  Playwright 的位置（工作树没有 node_modules 时指向主检出的 node_modules/playwright）
//
// 造数：都在 Agent 专用账号下新建会话，标题带 r192-<时间戳>；工作区的模型选择存在浏览器本地，结束时放回原值。
// 目标环境不是 fake 上游形态（ART_AI_UPSTREAM_SHAPE!=fake）时拒绝运行（退出码 2），不会发起真实生成。
// 不打印口令、Cookie 或签名 URL。最后一行输出 `PASS=n FAIL=n SKIP=n`；有 FAIL 时退出码 1。
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
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

const ITEMS = (process.env.R192_ITEMS || 'c1,c2,c3,c4').split(',').map((item) => item.trim()).filter(Boolean);
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
  await openSession(shared.sid);
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
  await openSession(sid);
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
const runners = { c1: itemC1, c2: itemC2, c3: itemC3, c4: itemC4 };
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
