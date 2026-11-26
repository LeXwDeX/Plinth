// #663 的 DEV 浏览器验收（只用 fake 上游，零费用）：自动批准方式下三视图、四视图在同一回合里一次出完。
//
// 覆盖（每项 PASS/FAIL 带截图）：
//   T3  会话切到自动批准，先出一张源图，再经输入框发三视图请求（一次 create_three_view）：这一回合的回复到达时，
//       正面、左侧、背面都已完成，可选的右侧没开始；卡片标题「三视图」，进度 3 / 3；全程没有批准卡、没有点任何视图按钮。
//   T4  同样的会话发四视图请求（create_three_view 带 include_right=true）：回复到达时四个视角都已完成，卡片标题「四视图」，
//       进度 4 / 4；全程没有批准卡、没有点按钮。
//   工具调用由 fake 对话模型的 [[fake:tool:…]] 标记给出；真实模型是否会传 include_right 不在 fake 验收范围。
//
// 用法（地址与 Agent 专用账号由 release-env.py 注入，凭据只在子进程环境里）：
//   python3 scripts/release-env.py smoke dev-test -- node scripts/acceptance/release-1-9-3-three-view-auto.mjs
// 环境变量：R193T_ITEMS=t3,t4  只跑其中几项；OUT=<目录>  截图与 results.json 的位置（不要写进仓库）；
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

const ITEMS = (process.env.R193T_ITEMS || 't3,t4').split(',').map((item) => item.trim()).filter(Boolean);
const tag = `r193t-${new Date().toISOString().replace(/[-:T]/g, '').slice(0, 14)}`;
const OUT = process.env.OUT || fs.mkdtempSync(path.join(os.tmpdir(), 'plinth-acceptance-r193t-'));
fs.mkdirSync(OUT, { recursive: true });
const ORIGIN = { Origin: new URL(base).origin };
const results = [];
const record = (status, item, name, detail = '') => { results.push({ status, item, name, detail }); console.log(`${status}: ${item} ${name}${detail ? ` — ${detail}` : ''}`); };
const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

const { chromium } = resolvePlaywright();
const browser = await chromium.launch();
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
const userTurns = async (sid) => (await messagesOf(sid)).filter((m) => m.role === 'user').length;
const tasksOf = async (sid) => { const [, body] = await api('GET', `/api/v1/tasks?session_id=${sid}&limit=50`); return body.tasks || body.data || []; };
const pendingOf = async (sid) => { const [, body] = await api('GET', `/api/v1/chat/sessions/${sid}/pending-approvals`); return body.items || []; };
const groupOf = async (sid) => { const [, body] = await api('GET', `/api/v1/chat/sessions/${sid}/three-views`); return (body.items || [])[0] || null; };
async function waitFor(check, seconds, step = 1000) { const end = Date.now() + seconds * 1000; while (Date.now() < end) { const value = await check(); if (value) return value; await sleep(step); } return null; }
async function shot(item, name) { const file = `${item}-${name}.png`; await page.screenshot({ path: path.join(OUT, file), fullPage: true }); return file; }
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
async function openSession(sid) {
  await page.goto(`${base}/?session=${sid}`, { waitUntil: 'load' });
  await tid('composer-send').first().waitFor({ timeout: 30000 });
  await sleep(2500);
}
const text = async (key) => page.evaluate((name) => window.__ART_AI_PRODUCT_SCHEMA__?.content?.messages?.[name] ?? '', key);
/** The card's count (#633): every required view and a started optional one; done is the succeeded ones among them. */
const progressOf = (slots) => {
  const asked = slots.filter((slot) => !slot.optional || slot.status !== 'not_started');
  return { done: asked.filter((slot) => slot.status === 'succeeded').length, total: asked.length };
};

/**
 * One auto-mode session: a source picture, then one create_three_view call sent from the composer (include_right for the
 * four-view request). Nothing on the page is clicked but 发送; approval cards are watched for the whole turn.
 */
async function autoViews(item, includeRight) {
  const [, session] = await api('POST', '/api/chat/sessions', { title: `${tag} ${includeRight ? '四视图' : '三视图'}自动` });
  const sid = session.id;
  const [mode] = await api('PUT', `/api/v1/chat/sessions/${sid}/approval`, { approvalMode: 'auto' });
  if (mode !== 200) throw new Error(`auto approval mode: PUT ${mode}`);
  await sendTurn(sid, '画一个角色 [[fake:tool:refine_image {"prompt":"a plain standing character on a white background"}]]');
  await waitFor(async () => (await tasksOf(sid)).some((task) => task.state === 'SUCCEEDED'), 150, 1500);
  await openSession(sid);
  const repliesBefore = await finalReplies(sid);
  const usersBefore = await userTurns(sid);
  const args = { reference_handles: ['@img1'], subject_type: 'humanoid', ...(includeRight ? { include_right: true } : {}) };
  const box = page.locator('[data-testid=studio-composer] textarea').first();
  await box.fill(`${includeRight ? '做四视图' : '做三视图'} [[fake:tool:create_three_view ${JSON.stringify(args)}]]`);
  await tid('composer-send').first().click();
  // The turn's one reply arrives only after the tool call ended, which (auto mode) is after every asked-for view.
  const seenPending = [];
  const replied = await waitFor(async () => {
    for (const card of await pendingOf(sid)) seenPending.push(`${card.kind}:${card.capability || ''}`);
    return (await finalReplies(sid)) > repliesBefore;
  }, 300, 1500);
  const group = await groupOf(sid);
  const turns = (await userTurns(sid)) - usersBefore;
  return { sid, replied: !!replied, group, turns, seenPending };
}

async function itemViews(item, includeRight) {
  const want = includeRight ? 4 : 3;
  const keys = includeRight ? ['front', 'left', 'back', 'right'] : ['front', 'left', 'back'];
  const { sid, replied, group, turns, seenPending } = await autoViews(item, includeRight);
  const slots = (group?.slots || []).map((slot) => ({ key: slot.key, optional: !!slot.optional, status: slot.status }));
  const statuses = Object.fromEntries(slots.map((slot) => [slot.key, slot.status]));
  const server = progressOf(slots);
  await check(item, `同一回合的回复到达时 ${want} 个视角都已完成（${want} / ${want}），没有点任何视图按钮`, async () => ({
    ok: replied && turns === 1 && !!group && group.complete === true && server.done === want && server.total === want
      && keys.every((key) => statuses[key] === 'succeeded') && (includeRight || statuses.right === 'not_started'),
    detail: `回复=${replied} 本次发送 ${turns} 条 complete=${group?.complete} ${server.done}/${server.total} 视角 ${JSON.stringify(statuses)}`,
  }));
  const pendingNow = await pendingOf(sid);
  await check(item, '全程没有批准卡', async () => ({
    ok: seenPending.length === 0 && pendingNow.length === 0,
    detail: `回合中见到 ${JSON.stringify([...new Set(seenPending)])}，结束后待批准 ${pendingNow.length} 条`,
  }));
  await openSession(sid);
  const card = tid('three-view-card').last();
  await card.waitFor({ timeout: 60000 });
  await check(item, `卡片标题「${includeRight ? '四视图' : '三视图'}」，卡片上显示 ${want} / ${want}`, async () => {
    const wantTitle = await text(includeRight ? 'ui.studio.three_view.title_four' : 'ui.studio.three_view.title');
    const header = (await card.locator('header span').first().innerText()).trim();
    const aria = await card.getAttribute('aria-label');
    const dom = await card.locator('[data-testid=three-view-slot]').evaluateAll((els) => els.map((el) => ({
      key: el.getAttribute('data-slot'), status: el.getAttribute('data-status'),
      optional: !!el.querySelector('[data-testid=three-view-optional]'),
    })));
    const shown = progressOf(dom);
    const progress = (await card.locator('[data-testid=three-view-progress]').innerText()).trim();
    const complete = await text('ui.studio.three_view.complete');
    const counted = (await text('ui.studio.three_view.progress')).replace('{done}', String(want)).replace('{total}', String(want));
    const file = await shot(item, 'card');
    return {
      ok: header === wantTitle && aria === wantTitle && shown.done === want && shown.total === want
        && (progress === complete || progress === counted),
      detail: `标题「${header}」读屏「${aria}」期望「${wantTitle}」 卡片视角 ${shown.done}/${shown.total} 进度「${progress}」 截图 ${file}`,
    };
  });
}

const login = await ctx.request.post(`${base}/api/auth/login`, { data: { username: process.env.AGENT_TEST_USERNAME, password: process.env.AGENT_TEST_PASSWORD }, headers: ORIGIN });
if (login.status() !== 200) { console.error(`login failed: HTTP ${login.status()}`); await browser.close(); process.exit(1); }
const [, version] = await api('GET', '/api/version');
console.log(`target ${base} version=${version.version || '?'} expect=${process.env.EXPECT_SHA || '?'} out=${OUT} tag=${tag}`);
const runners = { t3: () => itemViews('t3', false), t4: () => itemViews('t4', true) };
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
