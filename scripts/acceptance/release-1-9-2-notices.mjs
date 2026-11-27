// #651 的 DEV 浏览器验收（只用 fake 上游，零费用）：失败通知只提醒一次、统一停留 3 秒，任务管理列表宽度不随滚动条变化。
//
// 覆盖（每项 PASS/FAIL 带截图）：
//   N1  刷新后不重复弹：造 4 个已成功的 fake 任务，把任务列表读取改写成其中两个「已失败」（上游身份验证失败）、一个「运行中」，
//       刷新页面：通知区不出现任何失败通知（回灌的历史失败不再算刚失败）。
//   N2  本页任务立刻失败：经输入框发 fake 出图，把该任务的详情读取改写成「先运行中、后失败」：失败通知出现且只出现 1 条，
//       约 3 秒后自行消失（Schema runtime.frontend.toaster.duration_milliseconds，3000）。
//   N3  经典滚动条下，任务管理列表的卡片宽度在点 X 删卡片前后一致，各行 X 的右边缘在同一条竖线上（#651；v1.9.1 上宽度会因
//       滚动条出现或消失而变，每行 X 随之移动）。
//
//   N4  #633：三视图卡片标题随请求：只做三视图时卡片标题是「三视图」；点可选的右侧视图「继续生成」（四视图请求）后标题变成「四视图」，
//       读屏标签同步。
//   N5  #655：系统上下文写明「关键提醒用 **粗体**，其余不加粗，不用 HTML」（经 [[fake:system-contains …]] 读系统上下文）。
//       模型是否照做要真实模型才能看出，不在 fake 验收范围。
//
// 用法（地址与 Agent 专用账号由 release-env.py 注入，凭据只在子进程环境里）：
//   python3 scripts/release-env.py smoke dev-test -- node scripts/acceptance/release-1-9-2-notices.mjs
// 环境变量：R192N_ITEMS=n1,n2,n3,n4,n5  只跑其中几项；OUT=<目录>  截图与 results.json 的位置（不要写进仓库）；
//   PLAYWRIGHT_MODULE_PATH  Playwright 的位置。
// 目标环境不是 fake 上游形态（ART_AI_UPSTREAM_SHAPE!=fake）时拒绝运行（退出码 2）。最后一行输出 `PASS=n FAIL=n`；有 FAIL 时退出码 1。
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { resolvePlaywright } from './browser_matrix/lib.mjs';

const base = (process.env.BASE || '').replace(/\/+$/, '');
const missing = ['BASE', 'AGENT_TEST_USERNAME', 'AGENT_TEST_PASSWORD'].filter((name) => !process.env[name]);
if (missing.length) { console.error(`missing environment: ${missing.join(', ')}`); process.exit(2); }
if (process.env.ART_AI_UPSTREAM_SHAPE !== 'fake') { console.error('refusing to run: the target upstream shape is not fake'); process.exit(2); }

const ITEMS = (process.env.R192N_ITEMS || 'n1,n2,n3,n4,n5').split(',').map((item) => item.trim()).filter(Boolean);
const tag = `r192n-${new Date().toISOString().replace(/[-:T]/g, '').slice(0, 14)}`;
const OUT = process.env.OUT || fs.mkdtempSync(path.join(os.tmpdir(), 'plinth-acceptance-r192n-'));
fs.mkdirSync(OUT, { recursive: true });
const ORIGIN = { Origin: new URL(base).origin };
const results = [];
const record = (status, item, name, detail = '') => { results.push({ status, item, name, detail }); console.log(`${status}: ${item} ${name}${detail ? ` — ${detail}` : ''}`); };
const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

const { chromium } = resolvePlaywright();
// Classic (always visible) scrollbars: what a Mac with a mouse or the "always show scrollbars" setting shows (N3).
const browser = await chromium.launch({ ignoreDefaultArgs: ['--hide-scrollbars'], args: ['--disable-features=OverlayScrollbar'] });
const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 }, locale: 'zh-CN', serviceWorkers: 'block' });
const page = await ctx.newPage();
const pageErrors = [];
page.on('pageerror', (error) => pageErrors.push(error.message));

async function api(method, route, data) {
  const response = await ctx.request.fetch(base + route, { method, data, headers: ORIGIN });
  let body = {};
  try { body = await response.json(); } catch {}
  return [response.status(), body];
}
const tid = (id) => page.locator(`[data-testid="${id}"]`);
const messagesOf = async (sid) => { const [, body] = await api('GET', `/api/chat/sessions/${sid}/messages`); return Array.isArray(body) ? body : (body.messages || body.data || []); };
const finalReplies = async (sid) => (await messagesOf(sid)).filter((m) => m.role === 'assistant' && !String(m.content || '').startsWith('__GENERATING__')).length;
const tasksOf = async (sid) => { const [, body] = await api('GET', `/api/v1/tasks?session_id=${sid}&limit=50`); return body.tasks || body.data || []; };
async function waitFor(check, seconds, step = 1000) { const end = Date.now() + seconds * 1000; while (Date.now() < end) { const value = await check(); if (value) return value; await sleep(step); } return null; }
async function shot(item, name) { const file = `${item}-${name}.png`; await page.screenshot({ path: path.join(OUT, file) }); return file; }
async function check(item, name, body) {
  try { const { ok, detail } = await body(); record(ok ? 'PASS' : 'FAIL', item, name, detail); }
  catch (error) { record('FAIL', item, name, error.message.split('\n')[0]); }
}
async function sendTurn(sid, content) {
  const before = await finalReplies(sid);
  for (let attempt = 0; attempt < 60; attempt += 1) {
    const [status] = await api('POST', `/api/chat/sessions/${sid}/messages`, { content, creative_domain: 'image' });
    if (status !== 409) break;
    await sleep(1000);
  }
  if (!await waitFor(async () => (await finalReplies(sid)) > before, 150)) throw new Error('the turn produced no reply');
}
async function seedSession(title, count) {
  const [, session] = await api('POST', '/api/chat/sessions', { title });
  for (let i = 0; i < count; i += 1) await sendTurn(session.id, `画第 ${i} 个 [[fake:tool:refine_image ${JSON.stringify({ prompt: `${tag} picture ${i}` })}]]`);
  await waitFor(async () => (await tasksOf(session.id)).filter((task) => task.state === 'SUCCEEDED').length >= count, 150, 1500);
  return session.id;
}
async function openSession(sid) {
  await page.goto(`${base}/?session=${sid}`, { waitUntil: 'load' });
  await tid('composer-send').first().waitFor({ timeout: 30000 });
  await sleep(2500);
}
const toastCount = () => page.locator('[data-sonner-toast]').count();
/** Rewrites the session task list read: items 1 and 3 become failed (上游身份验证失败), item 2 runs (as a reload sees a mix). */
async function mockHistory() {
  await page.route(/\/api\/v1\/tasks\?.*session_id=/, async (route) => {
    const response = await route.fetch(); const body = await response.json();
    const list = body.tasks || body.data || [];
    list.forEach((task, index) => {
      if (index % 2 === 1) { task.state = 'FAILED'; task.progress = 0; task.assets = []; task.downloadables = []; task.failure = { class: 'authentication', detail: '上游身份验证失败', retryable: false }; }
      if (index === 2) { task.state = 'RUNNING'; task.progress = 30; task.failure = null; }
    });
    await route.fulfill({ response, json: body });
  });
}

let sessionId = null;
async function itemN1() {
  sessionId = await seedSession(`${tag} 通知`, 4);
  await mockHistory();
  await openSession(sessionId);
  await sleep(5000);
  await check('n1', '刷新后回灌的历史失败任务不弹失败通知', async () => {
    const count = await toastCount();
    const file = await shot('n1', 'after-reload');
    return { ok: count === 0, detail: `页面刷新 5 秒后通知 ${count} 条 截图 ${file}` };
  });
  await page.unrouteAll({ behavior: 'ignoreErrors' });
}
async function itemN2() {
  const sid = await seedSession(`${tag} 失败通知`, 1);
  await openSession(sid);
  const polls = {};
  await page.route(/\/api\/v1\/tasks\/task_[0-9a-f]+$/, async (route) => {
    const id = route.request().url().split('/').pop();
    const response = await route.fetch(); const body = await response.json();
    polls[id] = (polls[id] || 0) + 1;
    if (body && body.id === id && sessionId !== id && polls[id] <= 100) {
      if (polls[id] <= 2) { body.state = 'RUNNING'; body.progress = 10; body.assets = []; body.downloadables = []; body.failure = null; }
      else { body.state = 'FAILED'; body.progress = 0; body.assets = []; body.downloadables = []; body.failure = { class: 'authentication', detail: '上游身份验证失败', retryable: false }; }
    }
    await route.fulfill({ response, json: body });
  });
  // A new task this page submits: it is read as running first, then failed (the page saw it run).
  const known = (await tasksOf(sid)).map((task) => task.id);
  const box = page.locator('[data-testid=studio-composer] textarea').first();
  await box.fill(`再画一个 [[fake:tool:refine_image ${JSON.stringify({ prompt: `${tag} failing picture` })}]]`);
  await tid('composer-send').first().click();
  const appeared = await waitFor(async () => (await toastCount()) > 0, 60, 300);
  const shownAt = Date.now();
  const first = await toastCount();
  const file = await shot('n2', 'toast');
  const gone = await waitFor(async () => (await toastCount()) === 0, 12, 200);
  const lasted = Date.now() - shownAt;
  const newTask = (await tasksOf(sid)).find((task) => !known.includes(task.id));
  await check('n2', '本页任务失败：通知只出现 1 条，约 3 秒后自行消失', async () => ({
    ok: !!appeared && first === 1 && !!gone && lasted >= 2000 && lasted <= 6500,
    detail: `出现=${!!appeared} 同时 ${first} 条，消失用时约 ${lasted} ms（Schema 3000 ms，含出入动画） 新任务=${newTask?.id || '无'} 截图 ${file}`,
  }));
  await page.unrouteAll({ behavior: 'ignoreErrors' });
}
async function itemN3() {
  const sid = await seedSession(`${tag} 宽度`, 8);
  await openSession(sid);
  await tid('workspace-task-center').first().click();
  await tid('workspace-task-drawer').waitFor();
  await sleep(1500);
  const measure = async () => {
    const rows = await tid('workspace-task-card').evaluateAll((els) => els.map((el) => { const r = el.getBoundingClientRect(); const button = [...el.querySelectorAll('header button')].pop(); const b = button ? button.getBoundingClientRect() : null; return { w: Math.round(r.width), right: Math.round(r.right), x: b ? Math.round(b.right) : null }; }));
    const list = await page.locator('[data-testid=workspace-task-drawer] > div').last().evaluate((el) => ({ client: el.clientWidth, offset: el.offsetWidth, scrollHeight: el.scrollHeight, clientHeight: el.clientHeight }));
    return { rows, list, widths: [...new Set(rows.map((row) => row.w))], xs: [...new Set(rows.map((row) => row.x))] };
  };
  const first = await measure();
  const history = [first];
  for (let i = 0; i < 5; i += 1) {
    const buttons = page.locator('[data-testid=workspace-task-card] header button');
    if (!await buttons.count()) break;
    await buttons.nth(0).click();
    await sleep(500);
    history.push(await measure());
  }
  const file = await shot('n3', 'after-clicks');
  await check('n3', '经典滚动条下：点 X 删卡片前后卡片宽度一致，各行 X 在同一条竖线上', async () => {
    const widths = [...new Set(history.flatMap((state) => state.widths))];
    const sameLine = history.every((state) => state.xs.length <= 1);
    const overflowed = history[0].list.scrollHeight > history[0].list.clientHeight;
    return { ok: widths.length === 1 && sameLine && history.length > 3, detail: `共 ${history.length} 次测量，卡片宽 ${JSON.stringify(widths)}，X 右边缘每次测量都只有一个值=${sameLine}，起始列表${overflowed ? '有' : '没有'}滚动条（clientWidth ${history[0].list.client}/${history[0].list.offset}）截图 ${file}` };
  });
}

const text = async (key) => page.evaluate((name) => window.__ART_AI_PRODUCT_SCHEMA__?.content?.messages?.[name] ?? '', key);
async function approvePending(sid) {
  const [, schema] = await api('GET', '/api/v1/schema');
  const decision = (schema.schema || schema)?.chat?.api?.fields?.decision || 'decision';
  const [, pending] = await api('GET', `/api/v1/chat/sessions/${sid}/pending-approvals`);
  for (const card of (pending.items || []).filter((item) => item.status === 'pending')) await api('PUT', `/api/v1/tasks/grants/${card.request_id}`, { [decision]: 'approve' });
}
async function itemN4() {
  const [, session] = await api('POST', '/api/chat/sessions', { title: `${tag} 四视图标题` });
  const sid = session.id;
  await sendTurn(sid, '画一把剑 [[fake:tool:refine_image {"prompt":"a plain steel sword on a white background"}]]');
  await waitFor(async () => (await tasksOf(sid)).some((task) => task.state === 'SUCCEEDED'), 150, 1500);
  await openSession(sid);
  const box = page.locator('[data-testid=studio-composer] textarea').first();
  await box.fill('做三视图 [[fake:tool:create_three_view {"reference_handles":["@img1"]}]]');
  await tid('composer-send').first().click();
  await waitFor(async () => { await approvePending(sid); return await finalReplies(sid) >= 2; }, 150, 1500);
  await openSession(sid);
  const card = tid('three-view-card').last();
  await card.waitFor({ timeout: 60000 });
  const titleOf = async () => ({ header: (await card.locator('header span').first().innerText()).trim(), aria: await card.getAttribute('aria-label') });
  const three = await titleOf();
  await check('n4', '只做三视图（右侧视图没开始）：卡片标题是「三视图」', async () => {
    const want = await text('ui.studio.three_view.title');
    return { ok: three.header === want && three.aria === want, detail: `标题「${three.header}」读屏「${three.aria}」期望「${want}」` };
  });
  const start = card.locator('[data-testid=three-view-slot]').filter({ has: page.locator('[data-testid=three-view-optional]') }).locator('[data-testid=three-view-start]');
  await start.first().click();
  await waitFor(async () => { await approvePending(sid); return false; }, 6, 1500);
  await check('n4', '点右侧视图「继续生成」（四视图请求）：卡片标题变成「四视图」', async () => {
    const want = await text('ui.studio.three_view.title_four');
    const four = await waitFor(async () => { const t = await titleOf(); return t.header === want ? t : null; }, 30, 1000) || await titleOf();
    const file = await shot('n4', 'four-view-title');
    return { ok: four.header === want && four.aria === want, detail: `标题「${four.header}」读屏「${four.aria}」期望「${want}」 截图 ${file}` };
  });
}
async function itemN5() {
  const [, session] = await api('POST', '/api/chat/sessions', { title: `${tag} 提示词` });
  const sid = session.id;
  for (const marker of ['关键提醒用', '其余不加粗，不用']) {
    await sendTurn(sid, `你好 [[fake:system-contains ${marker}]]`);
    const reply = String([...(await messagesOf(sid))].reverse().find((m) => m.role === 'assistant')?.content || '');
    await check('n5', `系统上下文含「${marker}」`, async () => ({ ok: reply.includes(`present: ${marker}`), detail: `回复「${reply.slice(0, 80)}」` }));
  }
}

const login = await ctx.request.post(`${base}/api/auth/login`, { data: { username: process.env.AGENT_TEST_USERNAME, password: process.env.AGENT_TEST_PASSWORD }, headers: ORIGIN });
if (login.status() !== 200) { console.error(`login failed: HTTP ${login.status()}`); await browser.close(); process.exit(1); }
const [, version] = await api('GET', '/api/version');
console.log(`target ${base} version=${version.version || '?'} expect=${process.env.EXPECT_SHA || '?'} out=${OUT} tag=${tag}`);
const runners = { n1: itemN1, n2: itemN2, n3: itemN3, n4: itemN4, n5: itemN5 };
try {
  for (const item of ITEMS) {
    const runner = runners[item];
    if (!runner) { record('FAIL', item, '未知的验收项'); continue; }
    try { await runner(); } catch (error) { record('FAIL', item, '运行中断', error.message.split('\n')[0]); }
  }
} finally { await browser.close(); }
if (pageErrors.length) console.log(`INFO: 页面未捕获异常 ${pageErrors.length} 条：${[...new Set(pageErrors)].slice(0, 5).join(' | ')}`);
fs.writeFileSync(path.join(OUT, 'results.json'), JSON.stringify({ base, tag, results, pageErrors }, null, 2));
const count = (status) => results.filter((result) => result.status === status).length;
console.log(`PASS=${count('PASS')} FAIL=${count('FAIL')}`);
process.exit(count('FAIL') ? 1 : 0);
