// #681 点击矩阵回归（dev-test，fake 上游，零费用；浏览器 Chromium 1440×900）。
//
// 规则（AGENTS.md「操作一致性」）：单击永不改变画板；双击一张图只插入被双击的这一张；「选用」只写会话选用状态、
// 不放进画板；用户删掉的图不会因单击或选用复活；Agent 新结果只自动加入画板一次；舞台上的图双击是看大图。
//
// 覆盖与期望：
//   M  经输入框发 4 个 fake refine_image：自动加入画板 ≥1 层；清空画板后——结果卡单击 4 张、版本条单击每一格、
//      结果卡「选用」第 3 张（data-picked=true，fake 系统上下文含 @img3）都不改画板；结果卡双击第 2 张只插入 @img2；
//      删掉后再单击/选用第 2 张不复活；版本条双击第 3 格只插入 @img3；选中第 4 张后点结果卡「放入画板」只放 @img4；
//      「全部产出」单击一张打开详情、不改画板，详情里有可见的「插入到画布」按钮，点它只放入这一张。
//   A  一个任务 4 张的候选组（经公开任务 API 的 fake Seedream 组图）：候选弹层有 4 个候选，单击每一张不改画板；
//      弹层「选用」第 3 张不改画板且显示已选用；灯箱「选用」不改画板；版本条单击该格不改画板、双击只插入 1 张（不是 4 张）。
//   B  重新打开会话（恢复态）后单击加选用不改画板；「放入画板」只放当前高亮的 @img2；舞台上的图双击打开大图。
//
// 用法（地址与 Agent 专用账号由 release-env.py 注入）：
//   python3 scripts/release-env.py smoke dev-test -- node scripts/regression/click-matrix.mjs
// 环境变量：CLICK_PARTS=m,a,b 只跑其中几段；OUT 截图与 results.json 目录（默认系统临时目录）；PLAYWRIGHT_MODULE_PATH。
// 造数：Agent 专用账号下标题带 rg-<时间戳> 的 3 个会话，fake 2D 共约 12 张。目标不是 fake 形态时退出码 2。
// 最后一行 `=== click-matrix: PASS=n FAIL=n ===`；有 FAIL 时退出码 1。
import { requireFake, launch, newContext, helpers, reporter, outDir, sleep, tag } from './lib.mjs';

requireFake();
const PARTS = (process.env.CLICK_PARTS || 'm,a,b').split(',').map((p) => p.trim());
const dir = outDir('click-matrix');
const { rec, finish } = reporter('click-matrix');
const browser = await launch();
const ctx = await newContext(browser);
const page = await ctx.newPage();
const h = helpers(ctx, page, dir);

const layers = async () => (await page.locator('[data-testid=layer-row]').allInnerTexts()).map((t) => t.trim().split('\n')[0]).filter((n) => n && n !== '画布图层');
const clearBoard = async () => {
  for (let i = 0; i < 12; i++) {
    if (!(await layers()).length) return true;
    const row = page.locator('[data-testid=layer-row]').filter({ hasNotText: '画布图层' }).first();
    await row.hover(); await sleep(200);
    await row.locator('button').last().click(); await sleep(900);
  }
  return (await layers()).length === 0;
};
const result = () => h.tid('chat-image-result').last();
const pics = () => result().locator('[data-testid=chat-result-picture]');
const picks = () => result().locator('[data-testid=candidate-pick]');
const fourPictures = (label) => [1, 2, 3, 4].map((i) => `[[fake:tool:refine_image ${JSON.stringify({ prompt: `${tag} ${label} candidate ${i} cube` })}]]`).join(' ');
const boardUnchanged = async (label) => { const l = await layers(); return rec(l.length === 0, label, `图层=${JSON.stringify(l)}`); };
const onlyOne = async (label, handle) => { const l = await layers(); return rec(l.length === 1 && (!handle || l[0].includes(handle)), label, `图层=${JSON.stringify(l)}`); };

async function partM() {
  const [, s] = await h.api('POST', '/api/chat/sessions', { title: `${tag} matrix` });
  const sid = s.id;
  await h.openSession(sid);
  await page.locator('[data-testid=studio-composer] textarea').first().fill(`出4张候选 ${fourPictures('m')}`);
  await h.tid('composer-send').first().click();
  await h.waitFor(async () => (await h.tasksOf(sid)).filter((t) => t.state === 'SUCCEEDED').length >= 4 && (await h.finalReplies(sid)) >= 1, 180, 1500);
  await sleep(3000);
  rec((await layers()).length >= 1, 'M0 Agent 新结果一次性自动加入画板', JSON.stringify(await layers()));
  rec(await clearBoard(), 'M0b 手动清空画板');
  for (let i = 0; i < 4; i++) { await pics().nth(i).scrollIntoViewIfNeeded(); await pics().nth(i).click(); await sleep(900); await boardUnchanged(`M1 结果卡单击第 ${i + 1} 张：画板不变`); }
  await picks().nth(2).click(); await sleep(1500);
  rec((await layers()).length === 0 && (await picks().nth(2).getAttribute('data-picked')) === 'true', 'M2 结果卡「选用」第 3 张：画板不变、已选用', `data-picked=${await picks().nth(2).getAttribute('data-picked')}`);
  try {
    await h.sendTurn(sid, '我选了哪一张 [[fake:system-contains @img3]]', 120);
    const last = String([...(await h.messagesOf(sid))].reverse().find((m) => m.role === 'assistant')?.content || '');
    rec(/present/.test(last), 'M2b Agent 的系统上下文含所选 @img3', last.slice(0, 80));
  } catch (error) { rec(false, 'M2b Agent 知道选用', error.message); }
  await boardUnchanged('M2c 再说一轮后画板仍不变');
  const tiles = h.tid('version-tile');
  for (let i = 0; i < await tiles.count(); i++) { await tiles.nth(i).locator('button').first().click(); await sleep(900); await boardUnchanged(`M3 版本条单击第 ${i + 1} 格：画板不变`); }
  await pics().nth(1).scrollIntoViewIfNeeded(); await pics().nth(1).dblclick(); await sleep(1800);
  await onlyOne('M4 结果卡双击第 2 张：只插入这一张', 'img2');
  await clearBoard();
  await pics().nth(1).click(); await sleep(800); await picks().nth(1).click(); await sleep(1200);
  await boardUnchanged('M5 删除后再单击、选用第 2 张：不复活');
  await tiles.nth(2).locator('button').first().dblclick(); await sleep(1800);
  await onlyOne('M6 版本条双击第 3 格：只插入这一张', 'img3');
  await clearBoard();
  await picks().nth(3).click(); await sleep(1000);
  await h.tid('chat-result-insert-canvas').last().scrollIntoViewIfNeeded();
  await h.tid('chat-result-insert-canvas').last().click(); await sleep(1800);
  await onlyOne('M7 结果卡「放入画板」：只放当前这一张 @img4', 'img4');
  await h.shot('m7-insert-button');
  await clearBoard();
  await h.tid('all-outputs-button').click(); await sleep(1200);
  await h.tid('all-outputs-tile').nth(1).click(); await sleep(1500);
  await boardUnchanged('M8 全部产出单击一张（打开详情）：画板不变');
  const open = h.tid('result-detail-open-canvas');
  const label = (await open.innerText().catch(() => '')).trim();
  rec((await page.locator('[role=dialog]').count()) > 0 && !!label, 'M8b 详情里有可见的插入到画布按钮', `按钮「${label}」`);
  if (label) { await open.click(); await sleep(1800); await onlyOne('M8c 详情里点按钮：只放入这一张'); }
  await page.keyboard.press('Escape'); await sleep(400); await page.keyboard.press('Escape'); await sleep(400);
}

async function partA() {
  const [, s] = await h.api('POST', '/api/chat/sessions', { title: `${tag} group` });
  const sid = s.id;
  const reason = '回归：需要一组变体';
  await h.directTask(sid, 'jimeng/seedream-4-5/text-to-image', 'text-to-image', { prompt: `${tag} group cube`, size: '1920x1920', sequential_image_generation: 'auto', max_images: 4 }, { sequential_image_generation: reason, max_images: reason });
  await h.waitFor(async () => (await h.tasksOf(sid)).some((t) => t.state === 'SUCCEEDED'), 90, 1500);
  await h.openSession(sid); await sleep(1500);
  rec(await clearBoard(), 'A0 清空画板');
  await h.tid('version-expand').first().click(); await sleep(900);
  const cands = page.locator('[data-testid=version-candidates] li');
  rec((await cands.count()) === 4, 'A1 候选弹层有 4 个候选', `n=${await cands.count()}`);
  for (let i = 0; i < await cands.count(); i++) {
    await cands.nth(i).locator('button').first().click(); await sleep(800);
    await boardUnchanged(`A2 候选弹层单击第 ${i + 1} 张：画板不变`);
    if (!(await page.locator('[data-testid=version-candidates]').count())) { await h.tid('version-expand').first().click(); await sleep(700); }
  }
  const repick = cands.nth(2).locator('[data-testid=version-repick]');
  await repick.click(); await sleep(1500);
  rec((await layers()).length === 0 && /选用/.test(await repick.innerText()), 'A3 候选弹层「选用」第 3 张：画板不变、已选用', `按钮「${(await repick.innerText()).trim()}」`);
  await page.keyboard.press('Escape'); await sleep(500);
  await h.tid('version-expand').first().click(); await sleep(700);
  await cands.nth(0).locator('[data-testid=version-enlarge]').click(); await sleep(1200);
  if (await h.tid('lightbox-pick').count()) { await h.tid('lightbox-pick').click(); await sleep(1200); await boardUnchanged('A4 灯箱「选用」：画板不变'); }
  await page.keyboard.press('Escape'); await sleep(500); await page.keyboard.press('Escape'); await sleep(500);
  await clearBoard();
  const tile = h.tid('version-tile').first().locator('button').first();
  await tile.click(); await sleep(800);
  await boardUnchanged('A5 版本条单击候选组这一格：画板不变');
  await tile.dblclick(); await sleep(1800);
  await onlyOne('A6 版本条双击候选组这一格：只插入 1 张（不是 4 张）');
  await h.shot('a6-group-dblclick');
}

async function partB() {
  const [, s] = await h.api('POST', '/api/chat/sessions', { title: `${tag} restored` });
  const sid = s.id;
  await h.openSession(sid);
  await page.locator('[data-testid=studio-composer] textarea').first().fill(`出4张 ${fourPictures('b')}`);
  await h.tid('composer-send').first().click();
  await h.waitFor(async () => (await h.finalReplies(sid)) >= 1, 150, 1500);
  await h.waitFor(async () => (await h.tasksOf(sid)).filter((t) => t.state === 'SUCCEEDED').length >= 4, 150, 1500);
  await h.openSession(sid); await sleep(1500);
  rec(await clearBoard(), 'B0 重新打开会话后清空画板');
  await pics().nth(0).click(); await sleep(700); await picks().nth(0).click(); await sleep(900);
  await boardUnchanged('B1 恢复态单击加选用：画板不变');
  await pics().nth(1).click(); await sleep(700);
  await h.tid('chat-result-insert-canvas').last().click(); await sleep(1800);
  await onlyOne('B2 「放入画板」：只放当前高亮的 @img2', 'img2');
  await clearBoard();
  await pics().nth(0).dblclick(); await sleep(1500);
  const box = await page.locator('[data-testid=studio-stage] canvas').first().boundingBox().catch(() => null);
  if (box) { await page.mouse.dblclick(box.x + box.width / 2, box.y + box.height / 2); await sleep(1500); }
  rec((await page.locator('[data-testid=studio-lightbox]').count()) > 0, 'B3 舞台上的图双击：打开大图', `图层=${JSON.stringify(await layers())}`);
  await h.shot('b3-stage-dblclick');
}

const errors = []; page.on('pageerror', (e) => errors.push(e.message));
for (const [key, run] of [['m', partM], ['a', partA], ['b', partB]]) {
  if (!PARTS.includes(key)) continue;
  try { await run(); } catch (error) { rec(false, `${key.toUpperCase()} 段脚本错误`, error.message.slice(0, 160)); }
}
if (errors.length) console.log('INFO page errors:', JSON.stringify(errors.slice(0, 5)));
await browser.close();
process.exit(finish(dir));
