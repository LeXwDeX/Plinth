// #693 三视图卡片界面回归（dev-test，fake 上游，零费用；Chromium 1440×900）。
//
// 期望：
//   AUTO    自动方式，经输入框发三视图请求，回合进行中每 40ms 采样卡片：出现「稍后自动生成」标记
//           （three-view-auto-later）的采样里，回合必须在跑（composer-stop 可见），且同时没有「继续生成」「生成剩余视图」
//           手动按钮；回复到达后三个视角都 succeeded。
//   BUDGET  审批方式：Agent 只出正面（2D 自动、无卡、无计划卡）；卡片上没有「稍后自动生成」、有「继续生成」；
//           用户点左侧「继续生成」直接执行、不出卡；「生成剩余视图」把其余视角做完、不出卡。
// 接口一侧（窗口点、计数、严格方式、#611 标记）见 scripts/regression/three-view-continuation.py。
//
// 用法（地址与 Agent 专用账号由 release-env.py 注入）：
//   python3 scripts/release-env.py smoke dev-test -- node scripts/regression/three-view-card.mjs
// 环境变量：TV_PARTS=auto,budget；OUT（默认系统临时目录）；PLAYWRIGHT_MODULE_PATH。
// 造数：Agent 专用账号下标题带 rg-<时间戳> 的 2 个会话，fake 2D 约 8 张。目标不是 fake 形态时退出码 2。
// 最后一行 `=== three-view-card: PASS=n FAIL=n ===`；有 FAIL 时退出码 1。
import { requireFake, launch, newContext, helpers, reporter, outDir, sleep, tag } from './lib.mjs';

requireFake();
const PARTS = (process.env.TV_PARTS || 'auto,budget').split(',').map((p) => p.trim());
const dir = outDir('three-view-card');
const { rec, finish } = reporter('three-view-card');
const browser = await launch();
const ctx = await newContext(browser);
const page = await ctx.newPage();
const h = helpers(ctx, page, dir);
const SOURCE = '[[fake:tool:refine_image {"prompt":"a plain standing character on a white background"}]]';
const TV = '[[fake:tool:create_three_view {"reference_handles":["@img1"],"subject_type":"humanoid"}]]';

async function prepare(mode) {
  const [, s] = await h.api('POST', '/api/chat/sessions', { title: `${tag} tv-${mode}` });
  await h.api('PUT', `/api/v1/chat/sessions/${s.id}/approval`, { approvalMode: mode });
  await h.sendTurn(s.id, `画一个角色 ${SOURCE}`, 90);
  await sleep(2000);
  return s.id;
}
const groupOf = async (sid) => { const [, b] = await h.api('GET', `/api/v1/chat/sessions/${sid}/three-views`); return (b.items || [])[0]; };
const states = (g) => Object.fromEntries((g?.slots || []).map((s) => [s.key, s.status]));
const pendingCount = async (sid) => ((await h.api('GET', `/api/v1/chat/sessions/${sid}/pending-approvals`))[1].items || []).length;
const sendThreeView = async () => {
  await page.locator('[data-testid=studio-composer] textarea').first().fill(`做三视图 ${TV}`);
  await h.tid('composer-send').first().click();
};

async function partAuto() {
  const sid = await prepare('auto');
  await h.openSession(sid);
  await page.evaluate(() => {
    window.__tvSamples = [];
    setInterval(() => {
      const card = document.querySelector('[data-testid=three-view-card]');
      if (!card) return;
      window.__tvSamples.push({ later: card.querySelectorAll('[data-testid=three-view-auto-later]').length,
        start: card.querySelectorAll('[data-testid=three-view-start]').length,
        rest: card.querySelectorAll('[data-testid=three-view-start-remaining]').length,
        stop: document.querySelectorAll('[data-testid=composer-stop]').length });
    }, 40);
  });
  const before = await h.finalReplies(sid);
  await sendThreeView();
  await h.waitFor(async () => (await h.finalReplies(sid)) > before, 200, 1000);
  await sleep(1500);
  const samples = await page.evaluate(() => window.__tvSamples);
  const later = samples.filter((s) => s.later > 0);
  console.log(`INFO AUTO samples=${samples.length} with-auto-later=${later.length} (fake views can finish between samples)`);
  rec(samples.length > 0, 'AUTO 回合进行中采样到三视图卡片', `samples=${samples.length}`);
  rec(later.every((s) => s.stop > 0), 'AUTO 「稍后自动生成」只在回合在跑时出现', `later-without-stop=${later.filter((s) => !s.stop).length}`);
  rec(later.every((s) => !s.start && !s.rest), 'AUTO 「稍后自动生成」与手动按钮不同时出现', `with-buttons=${later.filter((s) => s.start || s.rest).length}`);
  const st = states(await groupOf(sid));
  rec(['front', 'left', 'back'].every((k) => st[k] === 'succeeded'), 'AUTO 回复到达时三个视角都已完成', JSON.stringify(st));
  await h.shot('auto-card');
}

async function partBudget() {
  const sid = await prepare('budget');
  await h.openSession(sid);
  await sendThreeView();
  let g;
  for (let i = 0; i < 60; i++) { g = await groupOf(sid); if (states(g).front === 'succeeded') break; await sleep(1000); }
  await sleep(5000); g = await groupOf(sid);
  const st = states(g);
  rec(st.front === 'succeeded' && st.left === 'not_started' && st.back === 'not_started' && await pendingCount(sid) === 0, 'BUDGET Agent 只出正面（2D 自动、无卡）', `${JSON.stringify(st)} pending=${await pendingCount(sid)}`);
  const card = h.tid('three-view-card').last();
  rec(await card.locator('[data-testid=three-view-auto-later]').count() === 0 && await card.locator('[data-testid=three-view-start]').count() >= 1, 'BUDGET 卡片有「继续生成」、没有「稍后自动生成」');
  await h.shot('budget-card');
  await card.locator('[data-testid=three-view-slot][data-slot=left] [data-testid=three-view-start]').click();
  for (let i = 0; i < 60; i++) { g = await groupOf(sid); if (states(g).left === 'succeeded') break; await sleep(1000); }
  rec(states(g).left === 'succeeded' && await pendingCount(sid) === 0 && await h.tid('studio-approval-card').count() === 0, 'BUDGET 用户点左侧「继续生成」直接执行、不出卡', JSON.stringify(states(g)));
  const rest = card.locator('[data-testid=three-view-start-remaining]');
  if (await rest.count()) { await rest.click(); for (let i = 0; i < 60; i++) { g = await groupOf(sid); if (states(g).back === 'succeeded') break; await sleep(1000); } }
  rec(states(g).back === 'succeeded' && await pendingCount(sid) === 0, 'BUDGET 「生成剩余视图」做完其余视角、不出卡', JSON.stringify(states(g)));
}

for (const [key, run] of [['auto', partAuto], ['budget', partBudget]]) {
  if (!PARTS.includes(key)) continue;
  try { await run(); } catch (error) { rec(false, `${key} 段脚本错误`, error.message.slice(0, 160)); }
}
await browser.close();
process.exit(finish(dir));
