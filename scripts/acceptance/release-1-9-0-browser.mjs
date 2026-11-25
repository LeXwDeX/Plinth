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
//   批次 F（PR #627：#610 至 #624；全部经输入框发带 fake 标记的消息，只验平台侧；要真实模型才能看出的行为记 INFO「需真实模型复测」）
//     F610  #610：画布留着弓、单击版本条里的斧再发 refine：POST 不带画布来源、无快照上传，新版本血缘只连到斧；@画布 仍按画布发送。
//     F611  #611：手选四视图/三视图：系统上下文点名所选、不列兄弟、带“先简短问一句”；标记显示所选、卡片视角数对；
//           手选四视图时 use_skill(three-view) 不记三视图。
//     F612  #612/#613：工作区没选 2D 模型时文生图与图生图照常出任务；用途（立绘）与工作区 GPT Image 冲突时两种工具都出
//           模型选择卡，点选后续跑、按所点模型出任务。
//     F614  #614：工作区 GPT Image，Agent set_params size=3840x2160 后画布轮次 refine，冻结参数为 3840x2160。
//     F615  #615：同一轮两次 ask_choice（分两步、同一回应并列）只出一张卡，刷新后仍一张。
//     F616  #616：打开普通会话 /steps 不是 404、控制台无相关 error。
//     F617  #617（PR #627 修的部分）：「更多」代表格有句柄徽标；文生图详情来源写 no_source 文案；裁图详情有类型、来源、尺寸。
//     F618  #618：可选右侧先完成时卡片头只算必需视角；最窄聊天栏下按钮不越界不重叠；插到画布的视角图层名带视角。
//     F619  #619：起草请求带当前会话 ID；[[fake:quota:insufficient_quota]] 时 503 与 Schema 文案。
//     F621  #621/#620：系统上下文含语言规则；Agent 自读 Skill 的标记是「本轮使用」。#620 的回执只回给模型，记 INFO。
//     F623  #623：管理首页余额卡逐供应商一行、带单位或占位（要管理员账号，缺少时 SKIP）。
//     F624  #624：3D 瓦片有预览或 3D 占位；拦掉预览后瓦片与输入框芯片都是 3D 占位。
//   批次 H（#629 至 #632 的 fake 复现：PROD 抽样发现、批次 F 的 fake 验收没抓到的缺陷；修复前的版本上这些检查应 FAIL）
//     F629  #629：上传一张 3072x5504、超过图生图上限的噪点 PNG 作三视图正面源图（锁上限最低的可用模型，触发冻结前压缩），
//           卡片上依次点正面、左侧、背面的「继续生成」：不返回 409，各视角都开始，卡片状态与服务端一致；
//           变体：点「继续生成」后立刻刷新（请求中途断开），同一视角仍可再启动或已在后台继续，其余视角不 409。
//     F630  #630：卡片上推进完视角后，3D 轮次的系统上下文含本组运行 ID；Agent 传视角 Task ID 时拒绝文案点名运行 ID
//           （经 [[fake:echo-tool-results]] 读工具结果）；传运行 ID 时多视图 3D 提交成功。
//     F631  #631：v1 是画板上唯一一层时单击版本条 v1、经 @画布 发 refine（reference_handles ["@img1"]）：新版本只连到 v1、
//           提示词含「image 1:」不含「image 2」、详情来源「来自 v1」；对照组画板两层时快照仍在前（image 2 是 v1）。
//     F632  #632：中文会话里 inspect_image、inspect_model、record_self_check 交回给模型的结果没有连续 6 个以上英文单词
//           （标识符、数字、句柄不算词，同 #407 评测口径；经 [[fake:echo-tool-results]] 读工具结果）。
//     目标环境的 fake 早于 [[fake:echo-tool-results]] 时，依赖它的检查记 INFO（读不到工具结果），不记 PASS。
//     批次 H 的会话结束时删除（R190_KEEP_SESSIONS=1 保留，便于人工查看）；上传的大图留在账号素材里，dev-test 下次发布即清空。
//
// 用法（地址与 Agent 专用账号由 release-env.py 注入，凭据只在子进程环境里）：
//   python3 scripts/release-env.py smoke dev-test -- node scripts/acceptance/release-1-9-0-browser.mjs
// 环境变量：
//   R190_ITEMS=586,…,n6,m1,m2,p1,…,p4,f610,…,f624,f629,…,f632  只跑其中几项（默认全部）
//   R190_KEEP_SESSIONS=1  不删除批次 H 建的会话
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
import { randomBytes, randomFillSync } from 'node:crypto';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { deflateSync } from 'node:zlib';
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

const ITEMS = (process.env.R190_ITEMS || '586,559,587,560,548-skill,548-chip,n1,n2,n3,n4,n5,n6,m1,m2,p1,p2,p3,p4,f610,f611,f612,f614,f615,f616,f617,f618,f619,f621,f623,f624,f629,f630,f631,f632').split(',').map((item) => item.trim()).filter(Boolean);
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
    const { ok, detail = '', skip = false, info = false } = typeof outcome === 'object' && outcome !== null ? outcome : { ok: Boolean(outcome) };
    // INFO: an observation recorded beside the results, not counted as PASS, FAIL or SKIP.
    record(info ? 'INFO' : skip ? 'SKIP' : ok ? 'PASS' : 'FAIL', item, name, detail);
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
      // Anchored at the row's start: since #611 the three-view row's description mentions 四视图 too.
      await tid('workspace-skill-picker-panel').first().locator('[role=menuitemradio]', { hasText: new RegExp(`^\\s*${skill}`) }).first().click();
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
  await check('P2', '分母为 0（无任何用量）时占比为 null（INFO，不计数）', async () => {
    const [status, empty] = await adminApi('GET', `${ADMIN_USAGE}?startDate=2020-01-01&endDate=2020-01-07`);
    const shares = (empty.byGroup || []).flatMap((group) => (group.amounts || []).map((item) => item.share_basis_points));
    if (status === 200 && shares.length === 0) {
      return {
        info: true,
        detail: `产品侧造不出单位总额为 0 仍有组行的数据：没有用量的范围（2020-01-01～2020-01-07）返回 byGroup=${JSON.stringify(empty.byGroup)}、`
          + `total=${JSON.stringify(empty.total)}，没有组行也就没有占比字段。null 分支由服务端单测 TestUsageGroupsLeaveShareNullWhenUnitTotalIsZero599`
          + `（internal/administration/usage_project_group_test.go）覆盖；运行中每次读取用量都逐行核对了“null 当且仅当单位总额为 0”（见上方一致性一项）`,
      };
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

// ---------------------------------------------------------------- 批次 F（PR #627：#610 至 #624）
// Every turn goes through the box with fake markers (the fake chat model answers fixed text and calls only the tools a
// marker names), so these checks prove the platform side of each fix: what the page sends, what the server freezes,
// what the conversation shows. What a real model would decide on its own (pick four-view from the wording, ask first,
// keep the language) is out of reach here and is reported as needing a real-model rerun, never as passed.
const PREFS_KEY = 'art-ai-workspace-prefs';
const composerBox = () => page.locator('[data-testid=studio-composer] textarea').first();
const readPrefs = async () => page.evaluate((key) => { try { return localStorage.getItem(key); } catch { return null; } }, PREFS_KEY);
async function writePrefs(value) {
  await page.evaluate(([key, next]) => {
    try { if (next === null) localStorage.removeItem(key); else localStorage.setItem(key, next); } catch {}
  }, [PREFS_KEY, value]);
}
/** Runs `body` and puts this browser's workspace model choice back afterwards (later items send without a model). */
async function keepingPrefs(body) {
  if (!page.url().startsWith(base)) await page.goto(`${base}/`, { waitUntil: 'load' });
  const saved = await readPrefs();
  try {
    return await body();
  } finally {
    await closeSettings().catch(() => {});
    await writePrefs(saved);
    await page.reload({ waitUntil: 'load' }).catch(() => {});
    console.log(`batch F: workspace model choice put back to ${saved === null ? 'none' : saved}`);
  }
}
/** No 2D model in the workspace picker (the state #612 starts from); the page must be reloaded to read it. */
async function clearWorkspaceImageModel() {
  const saved = await readPrefs();
  let parsed = { state: { modelByDomain: { image: null, 'model-3d': null } }, version: 2 };
  try { if (saved) parsed = JSON.parse(saved); } catch {}
  parsed.state = { ...(parsed.state || {}), modelByDomain: { ...(parsed.state?.modelByDomain || {}), image: null } };
  await writePrefs(JSON.stringify(parsed));
}
/** Picks a 2D model in the settings dialog by the first row whose text matches; returns the row's text. */
async function chooseImageModel(pattern) {
  await openSettings('models');
  const rows = page.locator('[data-testid=workspace-model-picker-image] [role=menuitemradio]');
  const texts = await rows.evaluateAll((items) => items.map((item) => item.innerText.replace(/\s+/g, ' ').trim()));
  const index = texts.findIndex((value) => pattern.test(value));
  if (index < 0) throw new Error(`no 2D model row matches ${pattern} (rows: ${texts.join(' | ').slice(0, 300)})`);
  await rows.nth(index).click();
  await page.waitForTimeout(1200);
  await closeSettings();
  return texts[index];
}
/** Hand-picks a system Skill for the next turn (the settings dialog's Skills tab); returns the composer chip text. */
async function pickSkill(skill) {
  await openSettings('skills');
  await tid('workspace-skill-picker-panel').first().locator('[role=menuitemradio]', { hasText: new RegExp(`^\\s*${skill}`) }).first().click();
  await page.waitForTimeout(1000);
  await closeSettings();
  return (await tid('composer-skill-chip').first().innerText().catch(() => '')).replace(/\s+/g, ' ').trim();
}
/**
 * Sends one turn through the box (as a person would) and returns the JSON body the page posted plus the other POSTs it
 * made while sending (a board snapshot is uploaded before the message). Waits for the final reply unless told not to.
 */
async function sendThroughBox(sid, content, { wait = true, until = null } = {}) {
  const before = await finalAssistantCount(sid);
  const others = [];
  const onRequest = (request) => {
    if (request.method() === 'POST' && !request.url().includes('/messages')) others.push(new URL(request.url()).pathname);
  };
  page.on('request', onRequest);
  try {
    const posted = page.waitForRequest((request) => request.method() === 'POST' && request.url().includes(`/api/chat/sessions/${sid}/messages`), { timeout: 45000 });
    await composerBox().fill(content);
    await tid('composer-send').first().click();
    const request = await posted;
    let body = {};
    try { body = request.postDataJSON() || {}; } catch {}
    const response = await request.response().catch(() => null);
    if (wait) {
      const done = await waitFor(async () => ((await finalAssistantCount(sid)) > before) || (until ? await until() : false), 150);
      if (!done) throw new Error('the turn produced no final reply in 150 s');
    }
    return { body, status: response ? response.status() : 0, others };
  } finally {
    page.off('request', onRequest);
  }
}
const lastReply = async (sid) => [...await messagesOf(sid)].reverse()
  .find((message) => message.role === 'assistant' && !String(message.content || '').startsWith('__GENERATING__')) || {};
async function approvePending(sid) {
  const schema = await publicSchema();
  const decision = schema?.chat?.api?.fields?.decision || 'decision';
  const [, pending] = await api('GET', `/api/v1/chat/sessions/${sid}/pending-approvals`);
  for (const card of (pending.items || []).filter((item) => item.status === 'pending')) {
    await api('PUT', `/api/v1/tasks/grants/${card.request_id}`, { [decision]: 'approve' });
  }
}
async function inputSourceField() {
  const schema = await publicSchema();
  return { field: schema?.chat?.api?.fields?.input_source || 'input_source', canvas: schema?.chat?.context?.canvas_input_source || 'canvas' };
}
const incomingEdges = async (sid, taskId) => {
  const [, lineage] = await api('GET', `/api/v1/chat/sessions/${sid}/lineage`);
  return (lineage?.edges || []).filter((edge) => String(edge.to).includes(taskId));
};
const newTasks = async (sid, known) => (await sessionTasks(sid)).filter((task) => !known.includes(task.id));
async function handleTasks(sid) {
  const order = [];
  for (const message of await messagesOf(sid)) {
    for (const attachment of message.attachments || []) {
      const taskId = attachment.task_id || attachment.taskId;
      if (taskId && attachment.handle && attachment.kind === 'image' && !order.some((entry) => entry.id === taskId)) order.push({ id: taskId, handle: attachment.handle });
    }
  }
  return order;
}

// #610: a version clicked on the strip, with another picture left on the board, is the turn's only input.
async function itemF610() {
  const sid = await createSession(`${tag} 弓与斧`);
  await turn(sid, '画一把弓 [[fake:tool:refine_image {"prompt":"a wooden bow on a white background"}]]');
  await turn(sid, '再画一把斧 [[fake:tool:create_concept_sketch {"prompt":"a steel axe on a white background"}]]');
  if (!await waitTasks(sid, (tasks) => tasks.filter((task) => task.state === 'SUCCEEDED').length >= 2)) throw new Error('two fake pictures did not succeed');
  const pictures = await handleTasks(sid);
  const bow = pictures[0];
  const axe = pictures[1];
  if (!bow || !axe) throw new Error(`the seed has no two handled pictures (${JSON.stringify(pictures)})`);
  console.log(`seed #610 ${sid}: bow ${bow.handle}=${bow.id} axe ${axe.handle}=${axe.id}`);
  await openSession(sid);
  const bowName = bow.handle.replace(/^@/, '');
  const axeName = axe.handle.replace(/^@/, '');
  if (!(await layerNames()).includes(bowName)) { await tileThumb(`n:${bow.id}`).dblclick(); await page.waitForTimeout(2000); }
  if ((await layerNames()).includes(axeName)) await removeLayer(axeName);
  await tileThumb(`n:${axe.id}`).click();
  await page.waitForTimeout(2000);
  const { field, canvas } = await inputSourceField();
  let refined = '';

  await check('#610', '画布留着弓、单击版本条里的斧再发 refine：本轮不带画布快照，参考图只有斧', async () => {
    const layers = await layerNames();
    const chip = (await tid('composer-object-chip').first().innerText().catch(() => '')).replace(/\s+/g, ' ').trim();
    const known = (await sessionTasks(sid)).map((task) => task.id);
    const sent = await sendThroughBox(sid, `把斧头改成金色 [[fake:tool:refine_image ${JSON.stringify({ prompt: 'make the axe golden', reference_handles: [axe.handle] })}]]`);
    const made = await waitFor(async () => (await newTasks(sid, known)).find((task) => task.state === 'SUCCEEDED') || null, 120, 1500);
    refined = made?.id || '';
    const edges = refined ? await incomingEdges(sid, refined) : [];
    const sources = edges.map((edge) => String(edge.from));
    const file = await shot('610', 'refine-axe');
    const snapshot = sent.others.filter((route) => /asset|upload|capture|canvas/.test(route));
    return {
      ok: layers.includes(bowName) && !layers.includes(axeName) && sent.body[field] !== canvas && Boolean(refined)
        && sources.length === 1 && sources[0].includes(axe.id) && snapshot.length === 0,
      detail: `画布图层=${JSON.stringify(layers)} 输入区芯片「${chip}」 POST ${field}=${JSON.stringify(sent.body[field] ?? null)} 发送时其他 POST=${JSON.stringify(sent.others)} `
        + `新任务=${refined || '无'} 入边来源=${JSON.stringify(sources)}（斧=${axe.id}） 截图 ${file}`,
    };
  });

  await check('#610', '版本条：新版本只连到斧（不连弓、不连画布）', async () => {
    if (!refined) return { ok: false, detail: '上一项没有产出新任务' };
    await openSession(sid);
    const tile = tileOf(`n:${refined}`);
    await tile.waitFor({ timeout: 20000 });
    const edges = await incomingEdges(sid, refined);
    const file = await shot('610', 'strip');
    return {
      ok: edges.length === 1 && String(edges[0].from).includes(axe.id) && !edges.some((edge) => String(edge.from).includes(bow.id)),
      detail: `血缘入边=${JSON.stringify(edges.map((edge) => ({ from: edge.from, kind: edge.kind, evidence: edge.evidence })))} 截图 ${file}`,
    };
  });

  await check('#610', '@画布 仍按画布发送（本轮来源是画布快照）', async () => {
    const boardToken = `@${await text('ui.studio.composer.mention.token_board')}`;
    const box = composerBox();
    await box.fill('');
    await box.click();
    await box.pressSequentially('@');
    const list = tid('composer-mention-list');
    await list.waitFor({ timeout: 10000 });
    await list.locator(`[data-testid=composer-mention-option][data-handle="${boardToken}"]`).first().click();
    await page.waitForTimeout(500);
    const before = await finalAssistantCount(sid);
    const known = (await sessionTasks(sid)).map((task) => task.id);
    const posted = page.waitForRequest((request) => request.method() === 'POST' && request.url().includes(`/api/chat/sessions/${sid}/messages`), { timeout: 45000 });
    await box.press('End');
    await page.keyboard.insertText(' 整张画布调暖 [[fake:tool:refine_image {"prompt":"warm up the whole board"}]]');
    const typed = await box.inputValue();
    await tid('composer-send').first().click();
    const request = await posted;
    let body = {};
    try { body = request.postDataJSON() || {}; } catch {}
    await waitFor(async () => (await finalAssistantCount(sid)) > before, 150);
    const made = await waitFor(async () => (await newTasks(sid, known)).find((task) => task.state === 'SUCCEEDED') || null, 120, 1500);
    const edges = made ? await incomingEdges(sid, made.id) : [];
    const file = await shot('610', 'board-mention');
    return {
      ok: typed.includes(boardToken) && body[field] === canvas && Boolean(made),
      detail: `输入框「${typed.slice(0, 60)}」 POST ${field}=${JSON.stringify(body[field] ?? null)} 新任务=${made?.id || '无'} 入边=${JSON.stringify(edges.map((edge) => edge.from))} 截图 ${file}`,
    };
  });
}

// #611 (and #618 on the same cards): a hand-picked view Skill is the one the turn uses and shows.
async function itemF611() {
  for (const [skill, slug, sibling, siblingSlug] of [['四视图', 'four-view', '三视图', 'three-view'], ['三视图', 'three-view', '四视图', 'four-view']]) {
    const sid = await createSession(`${tag} 手选${skill}`);
    await turn(sid, '画一把剑 [[fake:tool:refine_image {"prompt":"a plain steel sword on a white background"}]]');
    if (!await waitTasks(sid, (tasks) => tasks.some((task) => task.state === 'SUCCEEDED'))) throw new Error('the sword picture did not succeed');
    await openSession(sid);
    const ensurePicked = async () => {
      const chip = (await tid('composer-skill-chip').first().innerText().catch(() => '')).replace(/\s+/g, ' ').trim();
      return chip.includes(skill) ? chip : pickSkill(skill);
    };
    for (const [marker, want] of [[`「${skill}」（${slug}）`, 'present'], [`- ${siblingSlug}（`, 'absent'], ['先简短问一句想要的样子', 'present']]) {
      await check('#611', `手选「${skill}」：系统上下文 ${want === 'present' ? '含' : '不含'}「${marker}」`, async () => {
        const chip = await ensurePicked();
        await sendThroughBox(sid, `我要一把剑的${skill} [[fake:system-contains ${marker}]]`);
        const reply = String((await lastReply(sid)).content || '');
        return { ok: chip.includes(skill) && reply.includes(`${want}: ${marker}`), detail: `芯片「${chip}」 回复「${reply.slice(0, 80)}」` };
      });
    }
    await check('#611', `手选「${skill}」后要${skill}：对话标记显示「${skill}」，不显示「${sibling}」，卡片视角数对`, async () => {
      const chip = await ensurePicked();
      await sendThroughBox(sid, `我要一把剑的${skill} [[fake:tool:create_three_view {"reference_handles":["@img1"]}]]`);
      await approvePending(sid);
      const card = tid('three-view-card').last();
      const live = await card.waitFor({ timeout: 20000 }).then(() => true, () => false);
      if (!live) await openSession(sid);
      await card.waitFor({ timeout: 60000 });
      const used = (await tid('chat-used-skills').last().innerText({ timeout: 20000 }).catch(() => '')).trim();
      const stored = (await lastReply(sid)).used_skills || [];
      const slots = await card.locator('[data-testid=three-view-slot]').evaluateAll((items) => items.map((item) => ({
        slot: item.dataset.slot, optional: Boolean(item.querySelector('[data-testid=three-view-optional]')),
      })));
      const required = slots.filter((slot) => !slot.optional).length;
      const file = await shot('611', `marker-${slug}`);
      return {
        // The card is the same three-view card for both Skills: front, left, back required and the right view on it
        // (marked optional by the card's design). Four views need the right slot there; three need three required.
        ok: chip.includes(skill) && used.includes(skill) && !used.includes(sibling) && required === 3
          && (slug === 'four-view' ? slots.some((slot) => slot.slot === 'right') : true),
        detail: `芯片「${chip}」 标记「${used}」 服务端 used_skills=${JSON.stringify(stored).slice(0, 200)} 视角=${JSON.stringify(slots)} 卡片不刷新即出现=${live} 截图 ${file}`,
      };
    });
    if (slug === 'four-view') {
      await check('#611', '手选「四视图」时 Agent 读 three-view：不记「三视图」标记', async () => {
        const chip = await ensurePicked();
        await sendThroughBox(sid, '我要一把剑的四视图 [[fake:tool:use_skill {"name":"three-view"}]]');
        const reply = await lastReply(sid);
        const used = (reply.used_skills || []).map((item) => item.id || item.name || '');
        return { ok: chip.includes('四视图') && !used.some((id) => /three-view|三视图/.test(id)), detail: `芯片「${chip}」 本轮 used_skills=${JSON.stringify(reply.used_skills || [])}` };
      });
    }
  }
  record('INFO', '#611', '观察', '手选「四视图」出的卡片与三视图同一张：右侧视角仍标「可选」，必需计数 3；四视图要不要把右侧算必需不在 #611 的验收范围，留给产品判断');
  record('INFO', '#611', '需真实模型复测', '用户说「我要一把剑的四视图」时 Agent 自己选 four-view、没给图且主体笼统时先问：fake 对话模型只按标记调工具，只验了平台侧（手选块、目录、标记、卡片）');
}

// #612/#613: no 2D model in the workspace no longer refuses the turn; a purpose/workspace clash opens a model card.
async function itemF612() {
  await keepingPrefs(async () => {
    await clearWorkspaceImageModel();
    const sid = await createSession(`${tag} 无工作区模型`);
    await openSession(sid);
    const prefs = await readPrefs();
    for (const [tool, args, capability] of [
      ['create_concept_sketch', { prompt: 'a wooden barrel concept' }, 'text-to-image'],
      ['refine_image', { prompt: 'make the barrel iron', reference_handles: ['@img1'] }, 'image-to-image'],
    ]) {
      await check('#612', `工作区没选 2D 模型：${tool}（${capability}）照常出任务，不整轮拒绝`, async () => {
        const known = (await sessionTasks(sid)).map((task) => task.id);
        const sent = await sendThroughBox(sid, `${tool} [[fake:tool:${tool} ${JSON.stringify(args)}]]`);
        const made = await waitFor(async () => (await newTasks(sid, known)).find((task) => task.state === 'SUCCEEDED') || null, 120, 1500);
        const reply = String((await lastReply(sid)).content || '');
        const file = await shot('612', `no-model-${tool}`);
        return {
          ok: Boolean(made) && made.capability === capability && !/请先在工作区选择生成模型/.test(reply),
          detail: `工作区偏好=${prefs} POST 模型字段=${JSON.stringify(Object.fromEntries(Object.entries(sent.body).filter(([key]) => /model/i.test(key))))} 新任务=${made ? `${made.capability} ${made.offering_id}` : '无'} 回复「${reply.slice(0, 60)}」 截图 ${file}`,
        };
      });
    }

    // A purpose the person set (illustration recommends Gemini / Nano Banana Pro) against a workspace GPT Image pick.
    // A pick made in a session is that session's explicit lock, which wins without asking; the confirmation is for
    // the model the picker merely shows (remembered by this browser). So the pick is made in the session above and
    // the clash is played in a new session that only sees it. Its picture and brief are seeded through the API.
    const picked = await chooseImageModel(/gpt[\s-]*image/i);
    const sid2 = await createSession(`${tag} 用途与工作区冲突`);
    await turn(sid2, '画一个骑士 [[fake:tool:refine_image {"prompt":"a knight standing"}]]');
    if (!await waitTasks(sid2, (tasks) => tasks.some((task) => task.state === 'SUCCEEDED'))) throw new Error('the knight picture did not succeed');
    await turn(sid2, '用途是角色立绘 [[fake:tool:update_production_brief {"image_use":"illustration"}]]');
    await openSession(sid2);
    const visible = await readPrefs();
    console.log(`#613 session ${sid2}: workspace shows ${visible}`);
    await conflictCards(sid2, picked);
    record('INFO', '#613', '需真实模型复测', '点卡后由 Agent 读到所选并用它重新调用工具：fake 对话模型对答复只回固定文字（答复那轮工具调用 0 次），续跑由标记代演；真实模型是否照所选续跑要真实模型复测');
  });
}
async function conflictCards(sid, picked) {
  {
    for (const [tool, args, capability, answer] of [
      ['create_concept_sketch', { prompt: 'a knight portrait' }, 'text-to-image', 'recommended'],
      ['refine_image', { prompt: 'give the knight a red cape', reference_handles: ['@img1'] }, 'image-to-image', 'workspace'],
    ]) {
      await check('#613', `用途推荐与工作区「${picked}」冲突：${tool} 在对话里出模型选择卡，点选后按所选模型续跑`, async () => {
        const known = (await sessionTasks(sid)).map((task) => task.id);
        const cardsBefore = await tid('chat-choice-card').count();
        const sent = await sendThroughBox(sid, `${tool} [[fake:tool:${tool} ${JSON.stringify(args)}]]`, { until: async () => (await tid('chat-choice-card').count()) > cardsBefore });
        const card = tid('chat-choice-card').last();
        if (!await waitFor(async () => (await tid('chat-choice-card').count()) > cardsBefore, 30)) {
          const made = await waitFor(async () => (await newTasks(sid, known))[0] || null, 20, 1500);
          const fileNone = await shot('613', `no-card-${tool}`);
          const brief = (await tid('chat-production-brief').first().innerText().catch(() => '')).replace(/\s+/g, ' ').slice(0, 200);
          return {
            ok: false,
            detail: `没有出模型选择卡；POST 模型字段=${JSON.stringify(Object.fromEntries(Object.entries(sent.body).filter(([key]) => /model/i.test(key))))} `
              + `直接出的任务=${made ? `${made.capability} ${made.offering_id}` : '无'} 回复「${String((await lastReply(sid)).content || '').slice(0, 80)}」 设定摘要「${brief}」 截图 ${fileNone}`,
          };
        }
        await page.waitForTimeout(1500);
        const options = await card.locator('[data-testid=chat-choice-option]').evaluateAll((items) => items.map((item) => item.innerText.replace(/\s+/g, ' ').trim()));
        const beforeAnswer = (await newTasks(sid, known)).length;
        const fileCard = await shot('613', `card-${tool}`);
        const index = answer === 'workspace' ? options.findIndex((option) => /gpt[\s-]*image/i.test(option)) : 0;
        if (index < 0) return { ok: false, detail: `卡上没有工作区模型：${JSON.stringify(options)} 截图 ${fileCard}` };
        // Answering sends the pick as the person's message; the Agent then calls the tool again with that model
        // (docs/product.md, #612/#613). The fake chat model answers that message with fixed text, so the Agent's
        // second call is played by a marker naming the picked model, as a real model would.
        const before = await finalAssistantCount(sid);
        await card.locator('[data-testid=chat-choice-option]').nth(index).click();
        await card.locator('[data-testid=chat-choice-confirm]').click();
        await waitFor(async () => (await finalAssistantCount(sid)) > before, 120);
        const answerText = String([...await messagesOf(sid)].reverse().find((message) => message.role === 'user')?.content || '');
        // By model id: the turn's shape (text- or image-to-image, the board picture binds as a source) picks the Offering.
        const model = answer === 'workspace' ? 'gpt-image-2.6-sunburst' : 'gemini-3-pro-image';
        const offering = answer === 'workspace' ? 'openai/gpt-image-2/' : 'gemini/gemini-3-pro-image/';
        const cardsNow = await tid('chat-choice-card').count();
        await sendThroughBox(sid, `用所选模型继续 [[fake:tool:${tool} ${JSON.stringify({ ...args, model })}]]`);
        const made = await waitFor(async () => (await newTasks(sid, known)).find((task) => task.state === 'SUCCEEDED') || null, 150, 1500);
        const extraCard = (await tid('chat-choice-card').count()) > cardsNow;
        const file = await shot('613', `answered-${tool}`);
        return {
          ok: options.length >= 2 && /推荐/.test(options[0]) && options.some((option) => /gpt[\s-]*image/i.test(option)) && beforeAnswer === 0
            && answerText.includes(options[index].split(' 预计')[0].split(' · ').pop().split(' ')[0]) && Boolean(made)
            && String(made.offering_id).startsWith(offering) && !extraCard,
          detail: `选项=${JSON.stringify(options)} 点选前新任务=${beforeAnswer} 点「${options[index]}」后发出「${answerText.slice(0, 60)}」 `
            + `Agent 带 model=${model} 续跑后任务=${made ? `${made.capability} ${made.offering_id}` : '无'} 又出卡=${extraCard} 截图 ${fileCard} ${file}`,
        };
      });
    }
  }
}

// #614: a size the Agent set wins over the board on a canvas turn.
async function itemF614() {
  await keepingPrefs(async () => {
    const sid = await createSession(`${tag} 3840x2160`);
    await openSession(sid);
    const picked = await chooseImageModel(/gpt[\s-]*image/i);
    const known = [];
    await sendThroughBox(sid, '画一座城堡 [[fake:tool:refine_image {"prompt":"a castle on a hill"}]]');
    const first = await waitFor(async () => (await sessionTasks(sid)).find((task) => task.state === 'SUCCEEDED') || null, 120, 1500);
    if (!first) throw new Error('the castle picture did not succeed');
    known.push(first.id);
    const offering = String(first.offering_id || '').replace(/text-to-image$/, 'image-to-image');
    await openSession(sid);
    await tileThumb(`n:${first.id}`).dblclick();
    await page.waitForTimeout(2000);
    await tileThumb(`n:${first.id}`).click();
    await page.waitForTimeout(1500);
    const { field, canvas } = await inputSourceField();
    await check('#614', `工作区「${picked}」，Agent 设 size=3840x2160 后画布轮次 refine：冻结参数是 3840x2160`, async () => {
      const layers = await layerNames();
      // A picture double-clicked onto the board can sit across the dashed frame's edge, which (#610) no longer makes
      // the board the turn's source; @画布 forces the canvas turn the issue is about.
      const script = `[[fake:script:set_params ${JSON.stringify({ offering_id: offering, params: { size: '3840x2160' } })}|refine_image ${JSON.stringify({ prompt: 'make it a night scene' })}|done]]`;
      const boardToken = `@${await text('ui.studio.composer.mention.token_board')}`;
      const box = composerBox();
      await box.fill('');
      await box.click();
      await box.pressSequentially('@');
      const list = tid('composer-mention-list');
      await list.waitFor({ timeout: 10000 });
      await list.locator(`[data-testid=composer-mention-option][data-handle="${boardToken}"]`).first().click();
      await page.waitForTimeout(500);
      await box.press('End');
      const before = await finalAssistantCount(sid);
      const posted = page.waitForRequest((request) => request.method() === 'POST' && request.url().includes(`/api/chat/sessions/${sid}/messages`), { timeout: 45000 });
      await page.keyboard.insertText(` 设成 3840x2160 再改成夜景 ${script}`);
      await tid('composer-send').first().click();
      const request = await posted;
      const sent = { body: {} };
      try { sent.body = request.postDataJSON() || {}; } catch {}
      await waitFor(async () => (await finalAssistantCount(sid)) > before, 150);
      const made = await waitFor(async () => (await newTasks(sid, known)).find((task) => task.state === 'SUCCEEDED' || task.state === 'FAILED') || null, 150, 1500);
      const [, detail] = made ? await api('GET', `/api/v1/tasks/${made.id}`) : [0, {}];
      const parameters = detail?.input?.parameters || {};
      let pixels = '';
      const asset = (detail.assets || []).find((item) => item.role === 'image');
      if (asset?.url) {
        const bytes = await (await ctx.request.get(base + asset.url)).body().catch(() => Buffer.alloc(0));
        if (bytes.length > 24 && bytes.readUInt32BE(12) === 0x49484452) pixels = `${bytes.readUInt32BE(16)}x${bytes.readUInt32BE(20)}`;
      }
      const file = await shot('614', 'size');
      return {
        ok: sent.body[field] === canvas && made?.state === 'SUCCEEDED' && made.offering_id === offering && parameters.size === '3840x2160',
        detail: `图层=${JSON.stringify(layers)} POST ${field}=${JSON.stringify(sent.body[field] ?? null)} 任务=${made ? `${made.offering_id} ${made.state}` : '无'} 冻结参数=${JSON.stringify(parameters)} 产物像素=${pixels || '非 PNG 未读'}（fake 产物尺寸固定，只看冻结参数） 截图 ${file}`,
      };
    });
  });
}

// #615: one turn opens at most one ask_choice card.
async function itemF615() {
  const sid = await createSession(`${tag} 两张选择卡`);
  await openSession(sid);
  await check('#615', '同一轮连发两次 ask_choice：对话里只出一张卡（第一张），刷新后仍是一张', async () => {
    const first = { kind: 'next_step', options: [{ id: 'sword', label: `${tag} 甲：剑` }, { id: 'axe', label: `${tag} 乙：斧` }] };
    const second = { kind: 'next_step', options: [{ id: 'bow', label: `${tag} 丙：弓` }, { id: 'spear', label: `${tag} 丁：矛` }] };
    await sendThroughBox(sid, `给我选一个方向 [[fake:script:ask_choice ${JSON.stringify(first)}|ask_choice ${JSON.stringify(second)}|done]]`);
    await page.waitForTimeout(2500);
    const live = await tid('chat-choice-card').evaluateAll((items) => items.map((item) => item.innerText.replace(/\s+/g, ' ').trim()));
    const fileLive = await shot('615', 'one-card');
    await openSession(sid);
    const reloaded = await tid('chat-choice-card').evaluateAll((items) => items.map((item) => item.innerText.replace(/\s+/g, ' ').trim()));
    const file = await shot('615', 'one-card-reload');
    return {
      ok: live.length === 1 && live[0].includes('甲：剑') && !live[0].includes('丙：弓') && reloaded.length === 1,
      detail: `卡片=${JSON.stringify(live).slice(0, 200)} 刷新后=${reloaded.length} 张 截图 ${fileLive} ${file}`,
    };
  });
  // The same with both calls in one model response (two markers: two tool calls of one round).
  const other = await createSession(`${tag} 两张选择卡 同一回应`);
  await openSession(other);
  await check('#615', '同一回应里并列两次 ask_choice：对话里只出一张卡', async () => {
    const first = { kind: 'next_step', options: [{ id: 'red', label: `${tag} 红` }, { id: 'blue', label: `${tag} 蓝` }] };
    const second = { kind: 'next_step', options: [{ id: 'green', label: `${tag} 绿` }, { id: 'gold', label: `${tag} 金` }] };
    await sendThroughBox(other, `选个颜色 [[fake:tool:ask_choice ${JSON.stringify(first)}]] [[fake:tool:ask_choice ${JSON.stringify(second)}]]`);
    await page.waitForTimeout(2500);
    await openSession(other);
    const cards = await tid('chat-choice-card').evaluateAll((items) => items.map((item) => item.innerText.replace(/\s+/g, ' ').trim()));
    const file = await shot('615', 'one-card-parallel');
    return { ok: cards.length === 1, detail: `卡片=${JSON.stringify(cards).slice(0, 200)} 截图 ${file}` };
  });
}

// #616: opening a plain session reads /steps without a 404 or a console error.
async function itemF616() {
  const sid = await createSession(`${tag} steps`);
  await check('#616', '打开普通会话：/steps 不是 404，控制台没有相关 error', async () => {
    const errors = [];
    const steps = [];
    const onConsole = (message) => { if (message.type() === 'error') errors.push(message.text()); };
    const onResponse = (response) => { if (response.url().includes(`/sessions/${sid}/steps`)) steps.push(`${response.request().method()} ${response.status()}`); };
    page.on('console', onConsole);
    page.on('response', onResponse);
    try {
      await openSession(sid);
      await page.waitForTimeout(2500);
    } finally {
      page.off('console', onConsole);
      page.off('response', onResponse);
    }
    const related = errors.filter((message) => /404|steps/i.test(message));
    return { ok: steps.length > 0 && steps.every((entry) => !/ 404$/.test(entry)) && related.length === 0, detail: `/steps 应答=${JSON.stringify(steps)} 控制台 error=${JSON.stringify(errors).slice(0, 300)}` };
  });
}

// #617 (the parts PR #627 fixed): compact-tile handle badge, "no source" for text-to-image, crop detail rows.
async function itemF617() {
  const seed = await seedEdits(4);
  await openSession(seed.sid);
  await check('#617', '「更多」组留在外面那格有句柄徽标', async () => {
    const column = tid('version-strip').first().locator('[data-testid=version-column]').filter({ has: page.locator('[data-testid=version-fold]') }).first();
    await column.waitFor({ timeout: 20000 });
    const outside = column.locator('[data-testid=version-tile]').first();
    const badge = (await outside.locator('[data-testid=version-handle-badge]').first().innerText({ timeout: 5000 }).catch(() => '')).trim();
    const file = await shot('617', 'group-badge');
    return { ok: /^@?img\d+$/.test(badge), detail: `代表格 ${await outside.getAttribute('data-key')} 徽标「${badge}」 截图 ${file}` };
  });
  const noSource = await text('ui.result.detail.no_source');
  await check('#617', `文生图详情来源写「${noSource}」`, async () => {
    const source = (await sessionTasks(seed.sid)).find((task) => task.capability === 'text-to-image');
    if (!source) return { ok: false, detail: '会话里没有文生图任务' };
    await tileOf(`n:${source.id}`).locator('[data-testid=version-detail]').click();
    const rows = tid('result-detail-rows').first();
    await rows.waitFor({ timeout: 15000 });
    await page.waitForTimeout(1000);
    const values = await tid('result-detail-row').evaluateAll((items) => items.map((item) => [item.dataset.key, item.innerText.replace(/\s+/g, ' ').trim()]));
    const file = await shot('617', 'no-source');
    await page.keyboard.press('Escape');
    return { ok: values.some(([, value]) => value.includes(noSource)) && !values.some(([, value]) => /来源未知/.test(value)), detail: `详情行=${JSON.stringify(values).slice(0, 400)} 截图 ${file}` };
  });
  const crops = await seedSessionB();
  await openSession(crops.sid);
  await check('#617', '裁图详情有类型、来源（可跳转）与尺寸', async () => {
    const crop = crops.crops[0];
    await tileOf(`u:${crop.asset}`).locator('[data-testid=version-detail]').click();
    const rows = tid('result-detail-rows').first();
    await rows.waitFor({ timeout: 15000 });
    await page.waitForTimeout(1000);
    const values = Object.fromEntries(await tid('result-detail-row').evaluateAll((items) => items.map((item) => [item.dataset.key, item.innerText.replace(/\s+/g, ' ').trim()])));
    const jump = await tid('result-detail-source-jump').count();
    const file = await shot('617', 'crop-detail');
    await page.keyboard.press('Escape');
    return {
      ok: Boolean(values.type) && Boolean(values.source) && jump > 0 && /128x192/.test(values.size || ''),
      detail: `详情行=${JSON.stringify(values)} 来源可跳转=${jump}（源图 256x192 左半应为 128x192） 截图 ${file}`,
    };
  });
  record('INFO', '#617', '未在本批修复的部分', '裁图组徽标显示「2D」与选定后舞台未跟随仍开启（Issue 保持 OPEN），本次不验');
}

// #618: the three-view card counts required views only, its buttons stay in their columns, layers are named by view.
async function itemF618() {
  const sid = await createSession(`${tag} 三视图卡片`);
  await turn(sid, '画一把剑 [[fake:tool:refine_image {"prompt":"a plain steel sword on a white background"}]]');
  if (!await waitTasks(sid, (tasks) => tasks.some((task) => task.state === 'SUCCEEDED'))) throw new Error('the sword picture did not succeed');
  const seedIds = (await sessionTasks(sid)).map((task) => task.id);
  await openSession(sid);
  const baseline = await layerNames();
  await sendThroughBox(sid, '做三视图 [[fake:tool:create_three_view {"reference_handles":["@img1"]}]]');
  await approvePending(sid);
  const card = tid('three-view-card').last();
  // Found on dev-test e83af476: the session's three-view list is read once when the session opens and is not read
  // again after a turn, so a run started in the open session shows no card (and its view layers no view name) until
  // the page is reloaded. Recorded as its own check, then the page is reloaded to go on with the #618 checks.
  await check('#618', '出三视图后不刷新：对话里出现三视图卡片，画布上的视角图层名带视角', async () => {
    const live = await card.waitFor({ timeout: 30000 }).then(() => true, () => false);
    const added = (await layerNames()).filter((name) => !baseline.includes(name));
    const file = await shot('618', 'live-card');
    const generic = await page.evaluate(() => window.__ART_AI_PRODUCT_SCHEMA__?.content?.messages?.['ui.canvas.layer.image'] ?? '图片');
    return { ok: live && !added.includes(generic) && !added.includes('图片'), detail: `30 秒内卡片出现=${live} 新图层=${JSON.stringify(added)} 截图 ${file}` };
  });
  if (!await card.isVisible().catch(() => false)) await openSession(sid);
  await card.waitFor({ timeout: 60000 });
  const slotState = async () => card.locator('[data-testid=three-view-slot]').evaluateAll((items) => items.map((item) => ({
    slot: item.dataset.slot, status: item.dataset.status, optional: Boolean(item.querySelector('[data-testid=three-view-optional]')),
    label: item.querySelector('.font-medium')?.innerText.trim() || '',
  })));
  const progress = async () => (await card.locator('[data-testid=three-view-progress]').innerText()).trim();
  const completeText = await page.evaluate(() => window.__ART_AI_PRODUCT_SCHEMA__?.content?.messages?.['ui.studio.three_view.complete'] ?? '');
  const counted = (header, slots) => {
    const required = slots.filter((slot) => !slot.optional);
    const done = required.filter((slot) => slot.status === 'succeeded').length;
    const numbers = (header.match(/\d+/g) || []).map(Number);
    // The finished card says so in words (ui.studio.three_view.complete) instead of a count.
    if ((completeText && header === completeText) || numbers.length === 0) return { ok: done === required.length && Boolean(header), want: `${done}/${required.length}（完成时为文字）` };
    return { ok: numbers.length >= 2 && numbers[0] === done && numbers[1] === required.length, want: `${done}/${required.length}` };
  };
  await waitFor(async () => (await slotState()).some((slot) => slot.status === 'succeeded'), 90, 1500);

  await check('#618', '可选右侧先完成时，卡片头计数仍只算必需视角', async () => {
    const before = await slotState();
    const right = before.find((slot) => slot.optional);
    if (!right) return { ok: false, detail: `没有可选视角：${JSON.stringify(before)}` };
    const start = card.locator(`[data-testid=three-view-slot][data-slot="${right.slot}"] [data-testid=three-view-start]`);
    if (await start.count()) await start.click();
    await approvePending(sid);
    await waitFor(async () => (await slotState()).find((slot) => slot.slot === right.slot)?.status === 'succeeded', 120, 1500);
    const slots = await slotState();
    const header = await progress();
    const verdict = counted(header, slots);
    const file = await shot('618', 'count-optional');
    return { ok: verdict.ok && slots.find((slot) => slot.slot === right.slot)?.status === 'succeeded', detail: `卡片头「${header}」 应为 ${verdict.want} 视角=${JSON.stringify(slots)} 截图 ${file}` };
  });

  await check('#618', '聊天栏最窄（440）时各视角按钮不越界、不互相压住', async () => {
    await dragChat(-800);
    await page.waitForTimeout(800);
    const boxes = await card.locator('[data-testid=three-view-slot]').evaluateAll((items) => items.map((item) => {
      const column = item.getBoundingClientRect();
      return {
        slot: item.dataset.slot,
        column: { x: column.x, width: column.width },
        buttons: [...item.querySelectorAll('button')].filter((button) => button.offsetParent).map((button) => {
          const b = button.getBoundingClientRect();
          return { text: button.innerText.trim(), x: b.x, y: b.y, width: b.width, height: b.height };
        }),
      };
    }));
    const problems = [];
    for (const [index, slot] of boxes.entries()) {
      for (const button of slot.buttons) {
        if (button.x < slot.column.x - 1 || button.x + button.width > slot.column.x + slot.column.width + 1) problems.push(`${slot.slot}「${button.text}」越出本列`);
        for (const other of boxes.slice(index + 1)) {
          for (const target of other.buttons) if (intersects(button, target)) problems.push(`${slot.slot}「${button.text}」压住 ${other.slot}「${target.text}」`);
        }
      }
    }
    const width = await chatWidth();
    const file = await shotOf(card, '618', 'buttons-narrow');
    return { ok: problems.length === 0 && boxes.length >= 3, detail: `聊天栏 ${width.value}px 问题=${JSON.stringify(problems)} 截图 ${file}` };
  });

  await check('#618', '生成其余视角后卡片头显示完成；插到画布的视角图层名带视角', async () => {
    const rest = card.locator('[data-testid=three-view-start-remaining]');
    if (await rest.count()) await rest.click();
    for (let round = 0; round < 6; round += 1) {
      await approvePending(sid);
      const starts = card.locator('[data-testid=three-view-slot]:not([data-status=succeeded]) [data-testid=three-view-start]');
      if (await starts.count()) await starts.first().click().catch(() => {});
      if (await waitFor(async () => (await slotState()).filter((slot) => !slot.optional).every((slot) => slot.status === 'succeeded'), 20, 1500)) break;
    }
    const slots = await slotState();
    const header = await progress();
    const verdict = counted(header, slots);
    const labels = slots.map((slot) => slot.label);
    const views = (await sessionTasks(sid)).filter((task) => !seedIds.includes(task.id) && task.state === 'SUCCEEDED');
    // Each view's 「查看」 puts that view on the stage (the board, for a picture); the layers it adds are counted as
    // a multiset difference, so a second layer with an existing name still counts.
    const before = await layerNames();
    const opened = [];
    for (const slot of slots.filter((entry) => entry.slot !== 'front' && entry.status === 'succeeded')) {
      const view = card.locator(`[data-testid=three-view-slot][data-slot="${slot.slot}"] [data-testid=three-view-open]`);
      if (!await view.count()) continue;
      await view.click();
      await page.waitForTimeout(2000);
      opened.push(slot.label);
    }
    const after = await layerNames();
    const remaining = [...before];
    const added = after.filter((name) => {
      const index = remaining.indexOf(name);
      if (index >= 0) { remaining.splice(index, 1); return false; }
      return true;
    });
    const all = await layerNames();
    const file = await shot('618', 'layers');
    const wanted = opened.filter((label) => !all.includes(label));
    // Which picture each layer holds: select it and read the stage's version label (the fake views are identical
    // pictures, so the bytes cannot tell them apart).
    const held = [];
    const rows = tid('layer-row');
    for (let index = 0; index < await rows.count(); index += 1) {
      const row = rows.nth(index);
      const name = (await row.innerText()).trim();
      if (name === 'img1' || /画布图层/.test(name)) continue;
      await row.click().catch(() => {});
      await page.waitForTimeout(800);
      held.push(`${name}→${await stageVersion()}`);
    }
    return {
      ok: verdict.ok && opened.length > 0 && wanted.length === 0 && !all.includes('图片'),
      detail: `卡片头「${header}」 应为 ${verdict.want} 视角名=${JSON.stringify(labels)} 视角任务=${views.length} 点了「查看」=${JSON.stringify(opened)} `
        + `新图层=${JSON.stringify(added)} 全部图层=${JSON.stringify(all)} 缺视角名=${JSON.stringify(wanted)} 选中图层→舞台版本=${JSON.stringify(held)} 截图 ${file}`,
    };
  });
}

// #619: the Skill draft names the session it was opened from; a spent quota says so in Schema words.
async function itemF619() {
  const sid = await createSession(`${tag} Skill 草稿`);
  await openSession(sid);
  const openCreator = async () => {
    await closeSettings();
    await openSettings('skills');
    await page.getByRole('button', { name: await text('ui.workspace.skills.create'), exact: true }).click();
    await tid('workspace-skill-creator').waitFor({ timeout: 10000 });
    await page.waitForTimeout(800);
  };
  // In the full run the requirement once landed in the name field (the creator was still settling its focus): fill,
  // read both fields back, and fill again when they are not what was typed.
  const fillDraft = async (name, requirement) => {
    for (let attempt = 0; attempt < 3; attempt += 1) {
      await page.locator('#personal-skill-name').fill(name);
      await page.locator('#personal-skill-requirement').fill(requirement);
      await page.waitForTimeout(300);
      if (await page.locator('#personal-skill-name').inputValue() === name
        && await page.locator('#personal-skill-requirement').inputValue() === requirement) return;
    }
    throw new Error('the Skill creator did not keep the typed name and requirement');
  };
  await check('#619', '起草请求带上当前会话 ID，草稿生成成功', async () => {
    await openCreator();
    await fillDraft(`${tag}-draft`, `道具图一律白底 [[fake:skill-draft ${JSON.stringify({ slug: `${tag}-draft`, description: `${tag} 草稿` })}]]`);
    const posted = page.waitForRequest((request) => request.method() === 'POST' && request.url().includes('/skills/personal/draft'), { timeout: 30000 });
    await tid('skill-generate').click();
    const request = await posted;
    let body = {};
    try { body = request.postDataJSON() || {}; } catch {}
    const response = await request.response();
    await page.locator('#personal-skill-slug').waitFor({ timeout: 60000 }).catch(() => {});
    const slug = await page.locator('#personal-skill-slug').inputValue().catch(() => '');
    const file = await shot('619', 'draft');
    const sessionField = Object.entries(body).find(([key]) => /session/i.test(key));
    return { ok: sessionField?.[1] === sid && response?.status() === 200 && slug === `${tag}-draft`, detail: `请求会话字段=${JSON.stringify(sessionField || null)}（当前 ${sid}） HTTP ${response?.status()} 草稿标识「${slug}」 截图 ${file}` };
  });
  await check('#619', '对话连接额度用完：起草提示 Schema 文案（503），不是笼统失败', async () => {
    await openCreator();
    const want = await text('ui.workspace.skills.draft_quota_exhausted');
    await fillDraft(`${tag}-quota`, '道具图一律白底 [[fake:quota:insufficient_quota]]');
    const answered = page.waitForResponse((response) => response.request().method() === 'POST' && response.url().includes('/skills/personal/draft'), { timeout: 60000 });
    await tid('skill-generate').click();
    const response = await answered;
    const alert = await waitFor(async () => {
      const value = (await tid('workspace-skill-creator').getByRole('alert').first().innerText().catch(() => '')).trim();
      return value || null;
    }, 15);
    const file = await shot('619', 'quota');
    await closeSettings();
    return { ok: response.status() === 503 && Boolean(alert) && alert.includes(want), detail: `HTTP ${response.status()} 提示「${alert || ''}」 应含「${want}」 截图 ${file}` };
  });
  record('INFO', '#619', '需多连接复测', 'dev-test 的 fake 栈只有一个对话连接：只验了起草带会话 ID 与额度文案，「换到另一连接后草稿跟着换」要有第二个对话连接的环境复测');
}

// #620/#621: what the Agent is told; the Agent's own wording needs a real model.
async function itemF621() {
  const sid = await createSession(`${tag} 语言与本轮使用`);
  await openSession(sid);
  await check('#621', '系统上下文写明「工具结果是英文也不改」回复语言', async () => {
    const marker = '工具结果是英文也不改';
    await sendThroughBox(sid, `你好 [[fake:system-contains ${marker}]]`);
    const reply = String((await lastReply(sid)).content || '');
    return { ok: reply.includes(`present: ${marker}`), detail: `回复「${reply.slice(0, 80)}」` };
  });
  await check('#621', 'Agent 自己读的 Skill：对话标记是「本轮使用」，不是「你选的」', async () => {
    await sendThroughBox(sid, '帮我做三视图 [[fake:tool:use_skill {"name":"three-view"}]]');
    const chip = (await tid('chat-used-skills').last().innerText({ timeout: 20000 }).catch(() => '')).trim();
    const prefix = (await text('ui.chat.message.used_skills')).split('{')[0];
    const file = await shot('621', 'used-skill');
    return { ok: chip.startsWith(prefix) && !/你选/.test(chip), detail: `标记「${chip}」 截图 ${file}` };
  });
  record('INFO', '#621', '需真实模型复测', '回复不中英混杂、不把没选的 Skill 说成「你选的」、「背包图标」按物品图标理解：都是模型措辞，fake 对话模型只回固定文字');
  record('INFO', '#620', '无法在 fake 浏览器验证', 'list_offerings 的 style_skill 标注与 use_skill 回执里的可套用模型只回给模型（工具结果不进对话投影），fake 对话模型拿到后只回固定文字；由 CI fake 栈冒烟 style-skill 场景覆盖。「不再连试 5 个模型」需真实模型复测');
}

// #623: the admin home's balance card lists every provider account with its unit or a placeholder.
async function itemF623() {
  if (!ADMIN_READY) { record('SKIP', '#623', '管理首页余额卡', '缺管理员账号环境变量'); return; }
  const adminPage = await (await adminContext()).newPage();
  try {
    await check('#623', '管理首页「API 当前余额」逐供应商一行、带单位或占位', async () => {
      const balances = [];
      adminPage.on('response', async (response) => {
        if (/balance/i.test(response.url()) && response.request().method() === 'GET') {
          balances.push({ path: new URL(response.url()).pathname, status: response.status() });
        }
      });
      await adminPage.goto(`${base}/admin`, { waitUntil: 'load' });
      const card = adminPage.locator('[data-testid=admin-balance]');
      await card.waitFor({ timeout: 30000 });
      await adminPage.waitForTimeout(2000);
      const rows = await card.locator('li').evaluateAll((items) => items.map((item) => item.innerText.trim()));
      const messages = await adminPage.evaluate(() => window.__ART_AI_PRODUCT_SCHEMA__?.content?.messages || {});
      const placeholders = [messages['ui.pricing.balance_unknown'], messages['ui.workspace.balance.unavailable']].filter(Boolean);
      const bad = rows.filter((row) => {
        const value = row.split(/[:：]\s*/).slice(1).join(':').trim();
        if (!value) return true;
        if (placeholders.some((placeholder) => value.includes(placeholder))) return false;
        return /^[\d.,\s]+$/.test(value);
      });
      const file = await shotOf(adminPage, '623', 'balance');
      return {
        ok: rows.length >= 2 && bad.length === 0,
        detail: `余额行=${JSON.stringify(rows)} 无单位行=${JSON.stringify(bad)} 余额请求=${JSON.stringify(balances)} 截图 ${file}`,
      };
    });
  } finally {
    await adminPage.close().catch(() => {});
  }
}

// #624: a 3D tile and a 3D object chip without a preview show the 3D placeholder, never a black square.
async function itemF624() {
  const seed = await seedSessionA();
  if (!seed.model) { record('FAIL', '#624', '3D 占位', 'fake 3D 任务没有成功'); return; }
  const thumbState = async (scope) => scope.locator('[data-testid=object-thumb]').first().evaluate((node) => {
    const image = node.querySelector('img');
    return {
      state: node.dataset.state, kind: node.dataset.kind, icon: Boolean(node.querySelector('svg')),
      loaded: image ? image.complete && image.naturalWidth > 0 : false, src: image ? new URL(image.src).pathname : '',
      background: getComputedStyle(node).backgroundColor,
    };
  });
  const sound = (thumb) => thumb.kind === 'model' && ((thumb.state === 'image' && thumb.loaded) || (thumb.state === 'placeholder' && thumb.icon));
  await openSession(seed.sid);
  const tile = tileOf(`n:${seed.model}`);
  let normal = null;
  await check('#624', '3D 瓦片：有预览显示预览，没有时显示 3D 占位', async () => {
    await tile.waitFor({ timeout: 20000 });
    await page.waitForTimeout(1500);
    normal = await thumbState(tile);
    const file = await shotOf(tile, '624', 'tile');
    return { ok: sound(normal), detail: `${JSON.stringify(normal)} 截图 ${file}` };
  });
  await check('#624', '3D 预览加载失败：瓦片与输入框芯片都回到 3D 占位（不是黑块）', async () => {
    const blocked = normal?.src || '';
    if (blocked) await page.route((url) => url.pathname === blocked, (route) => route.abort());
    try {
      await openSession(seed.sid);
      await tile.waitFor({ timeout: 20000 });
      await tileThumb(`n:${seed.model}`).click();
      await page.waitForTimeout(2500);
      const tileThumbState = await thumbState(tile);
      const chip = tid('composer-object-chip').first();
      const chipState = await chip.count() ? await thumbState(chip).catch((error) => ({ error: error.message.split('\n')[0] })) : { missing: true };
      const file = await shot('624', 'placeholder');
      const placeholder = (thumb) => thumb.kind === 'model' && thumb.state === 'placeholder' && thumb.icon;
      return {
        ok: placeholder(tileThumbState) && (blocked ? placeholder(chipState) : sound(chipState)),
        detail: `拦截预览=${blocked || '（本来就没有预览）'} 瓦片=${JSON.stringify(tileThumbState)} 芯片=${JSON.stringify(chipState)} 截图 ${file}`,
      };
    } finally {
      if (blocked) await page.unroute((url) => url.pathname === blocked).catch(() => {});
      await page.unrouteAll({ behavior: 'ignoreErrors' }).catch(() => {});
    }
  });
}

// ---------------------------------------------------------------- batch H (#629 to #632)
// Defects PROD sampling found on d68d1020 that the batch F fake run did not catch. Every check is meant to fail on a
// revision without the batch H fixes and pass with them, on the fake stack alone. What the fake cannot show is INFO.
const KEEP_SESSIONS = process.env.R190_KEEP_SESSIONS === '1';
const batchHSessions = [];
async function batchHSession(title) {
  const sid = await createSession(title);
  batchHSessions.push(sid);
  return sid;
}
async function cleanupBatchH() {
  if (KEEP_SESSIONS) {
    if (batchHSessions.length) console.log(`batch H: kept sessions ${batchHSessions.join(' ')}`);
    return;
  }
  for (const sid of batchHSessions) {
    const [status] = await api('DELETE', `/api/chat/sessions/${sid}`);
    if (![200, 204].includes(status)) console.log(`cleanup: session ${sid} DELETE ${status}`);
  }
}

// The fake-only convention that answers the round after a tool result with that round's tool results (fake.go
// ToolResultsEchoMarker). The browser cannot reach the fake's control plane, so this is how it reads what a tool
// handed back to the model. A fake that predates it answers the fixed text: the caller reports INFO, never PASS.
const ECHO = '[[fake:echo-tool-results]]';
const ECHO_PREFIX = 'tool-results: ';
async function echoedToolResults(sid) {
  const reply = String((await lastReply(sid)).content || '');
  const at = reply.indexOf(ECHO_PREFIX);
  if (at < 0) return { results: null, reply };
  const rest = reply.slice(at + ECHO_PREFIX.length).trim();
  try {
    const parsed = JSON.parse(rest);
    if (Array.isArray(parsed)) return { results: parsed.map(String), reply };
  } catch {}
  return { results: [rest], reply }; // the reply was cut or reworded: scan it as one text
}
// #407's mixed-language mark, as review_language_632_test.go reads it: six or more English words in a row, where
// identifiers (snake_case, handles, placeholders, numbers, file and model names) are machine tokens, not words.
const ENGLISH_SENTENCE = /[A-Za-z][A-Za-z'’]*(?:[ ,;:()/-]+[A-Za-z][A-Za-z'’]*){5,}/;
const MACHINE_TOKEN = /[A-Za-z0-9_@#./{}=<>%-]*[_@#{}0-9=<>%][A-Za-z0-9_@#./{}=<>%-]*/g;
const englishSentenceIn = (value) => (String(value).replace(MACHINE_TOKEN, '|').match(ENGLISH_SENTENCE) || [''])[0];
/** Every string leaf of the tool results (JSON or plain text) that carries an English sentence, with its path. */
function englishLeaves(results) {
  const found = [];
  const walk = (value, where) => {
    if (Array.isArray(value)) value.forEach((child) => walk(child, `${where}[]`));
    else if (value && typeof value === 'object') for (const [key, child] of Object.entries(value)) walk(child, `${where}.${key}`);
    else if (typeof value === 'string') {
      const match = englishSentenceIn(value);
      if (match) found.push(`${where || '.'}: ${match}`);
    }
  };
  results.forEach((result, index) => {
    let parsed = result;
    try { parsed = JSON.parse(result); } catch {}
    walk(parsed, `#${index}`);
  });
  return found;
}

// ---- #629 helpers: a picture big in bytes (not in the repository), and the three-view card driven by hand.
const CRC_TABLE = (() => {
  const table = new Int32Array(256);
  for (let n = 0; n < 256; n += 1) {
    let c = n;
    for (let k = 0; k < 8; k += 1) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1;
    table[n] = c;
  }
  return table;
})();
function crc32(parts) {
  let c = -1;
  for (const part of parts) for (let index = 0; index < part.length; index += 1) c = CRC_TABLE[(c ^ part[index]) & 0xff] ^ (c >>> 8);
  return (c ^ -1) >>> 0;
}
function pngChunk(type, data) {
  const name = Buffer.from(type, 'ascii');
  const length = Buffer.alloc(4);
  length.writeUInt32BE(data.length);
  const crc = Buffer.alloc(4);
  crc.writeUInt32BE(crc32([name, data]));
  return Buffer.concat([length, name, data, crc]);
}
/**
 * An RGB PNG of width x height, about `bytes` long: a centered block of random noise (incompressible, so the file
 * really weighs that much, like the PROD front picture) on a flat light background, so the subject stays inside the
 * frame. Generated per run; nothing large is committed.
 */
function noisyPng(width, height, bytes) {
  const stride = width * 3 + 1;
  const raw = Buffer.alloc(stride * height, 232);
  for (let y = 0; y < height; y += 1) raw[y * stride] = 0;
  const left = Math.floor(width * 0.15);
  const span = width - 2 * left;
  const rows = Math.min(Math.floor(height * 0.9), Math.ceil(bytes / (span * 3)));
  const top = Math.floor((height - rows) / 2);
  for (let y = top; y < top + rows; y += 1) randomFillSync(raw, y * stride + 1 + left * 3, span * 3);
  const header = Buffer.alloc(13);
  header.writeUInt32BE(width, 0);
  header.writeUInt32BE(height, 4);
  header[8] = 8;
  header[9] = 2;
  return Buffer.concat([Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]), pngChunk('IHDR', header),
    pngChunk('IDAT', deflateSync(raw, { level: 1 })), pngChunk('IEND', Buffer.alloc(0))]);
}
const mb = (value) => `${(value / 1e6).toFixed(1)} MB`;
/** The available image-to-image model whose reference limit is lowest (the one a big source picture overflows). */
async function smallestImageLimit() {
  const [, body] = await api('GET', '/api/v1/offerings?capability=image-to-image');
  const rows = (body.offerings || []).filter((offering) => offering.available).map((offering) => {
    const parameter = (offering.parameters || []).find((entry) => entry.name === 'reference_assets') || {};
    return { model: offering.model_id, offering: offering.id, limit: Number(parameter.max_bytes || 0) };
  }).filter((row) => row.limit > 0 && row.limit < 30_000_000).sort((a, b) => a.limit - b.limit);
  return rows[0] || null;
}
async function threeViewGroups(sid) {
  const [, body] = await api('GET', `/api/v1/chat/sessions/${sid}/three-views`);
  return body.items || [];
}
const groupOf = async (group) => (await threeViewGroups(group.sid)).find((entry) => entry.run_id === group.run) || { slots: [] };
const slotsText = (slots) => (slots || []).map((slot) => `${slot.key}=${slot.status}${slot.status === 'not_started' ? (slot.startable ? '/可启动' : '/不可启动') : ''}`).join(' ');
const cardOf = () => tid('three-view-card').last();
const cardSlots = async () => cardOf().locator('[data-testid=three-view-slot]').evaluateAll((items) => items.map((item) => ({
  key: item.dataset.slot, status: item.dataset.status, start: Boolean(item.querySelector('[data-testid=three-view-start]')),
})));
async function openCard(sid) {
  // After a reload the session list can load after the first open: open the session again if the card does not show.
  for (let attempt = 0; attempt < 3; attempt += 1) {
    await openSession(sid);
    try { await cardOf().waitFor({ timeout: attempt < 2 ? 20000 : 60000 }); return; } catch (error) { if (attempt === 2) throw error; }
  }
}
/** Clicks a view's 「继续生成」 and returns the answer of its POST; `drop` reloads the page at once instead. */
async function startView(key, { drop = false } = {}) {
  const button = cardOf().locator(`[data-testid=three-view-slot][data-slot="${key}"] [data-testid=three-view-start]`);
  if (!await button.count()) return { key, clicked: false };
  const answered = page.waitForResponse((response) => response.request().method() === 'POST'
    && new URL(response.url()).pathname.endsWith(`/views/${key}`) && response.url().includes('/three-views/'),
  { timeout: drop ? 3000 : 240000 }).catch(() => null);
  await button.click();
  if (drop) {
    // The person leaves at once: the reload drops the request while the server is still freezing the input.
    await page.waitForTimeout(400);
    await page.reload({ waitUntil: 'load' }).catch(() => {});
    const early = await answered;
    return { key, clicked: true, status: early ? early.status() : 'dropped' };
  }
  const response = await answered;
  let code = '';
  if (response) {
    try {
      const body = await response.json();
      code = body.code || body.error?.code || (typeof body.error === 'string' ? body.error : '') || '';
    } catch {}
  }
  return { key, clicked: true, status: response ? response.status() : 0, code: String(code) };
}
const STOPPED = ['not_started', 'failed', 'denied', 'canceled'];
/** Waits until the view has started (approving what it asks for); returns its slot or null. */
async function viewStarted(group, key, seconds = 240) {
  return waitFor(async () => {
    await approvePending(group.sid);
    const slot = (await groupOf(group)).slots.find((entry) => entry.key === key);
    return slot && slot.status !== 'not_started' ? slot : null;
  }, seconds, 2000);
}
/** Starts one view from the card the way a person does, once the server says it can start. */
async function advanceView(group, key, answers, reached) {
  // A required view that never started holds back the ones after it: do not wait 180 seconds for each of them.
  const blocked = Object.entries(reached).find(([, status]) => status === 'not_started');
  if (blocked) {
    answers.push({ key, note: `前面的 ${blocked[0]} 没开始，未尝试` });
    reached[key] = 'not_started';
    return;
  }
  const ready = await waitFor(async () => {
    await approvePending(group.sid);
    const slot = (await groupOf(group)).slots.find((entry) => entry.key === key);
    return slot && (slot.status !== 'not_started' || slot.startable) ? slot : null;
  }, 180, 2000);
  if (!ready) {
    answers.push({ key, note: '180 秒内既没开始也不可启动' });
    reached[key] = 'not_started';
    return;
  }
  if (ready.status === 'not_started') {
    await openCard(group.sid);
    answers.push(await startView(key));
  } else {
    answers.push({ key, note: `已是 ${ready.status}` });
  }
  reached[key] = (await viewStarted(group, key))?.status || 'not_started';
}
/** The card says what the server says: each view's status, and a start button exactly where it can start. */
async function cardMatchesServerOnce(group) {
  const before = await groupOf(group);
  const card = await cardSlots();
  const after = await groupOf(group);
  const problems = [];
  for (const slot of after.slots) {
    const shown = card.find((entry) => entry.key === slot.key);
    const earlier = before.slots.find((entry) => entry.key === slot.key);
    if (!shown) { problems.push(`${slot.key} 卡片上没有`); continue; }
    if (shown.status !== slot.status && shown.status !== earlier?.status) problems.push(`${slot.key} 卡片=${shown.status} 服务端=${slot.status}`);
    if (slot.status === 'not_started' && shown.status === 'not_started' && Boolean(slot.startable) !== shown.start) {
      problems.push(`${slot.key} 服务端 startable=${slot.startable} 卡片按钮=${shown.start}`);
    }
  }
  return { problems, server: slotsText(after.slots), card: card.map((entry) => `${entry.key}=${entry.status}${entry.start ? '/按钮' : ''}`).join(' ') };
}
/** The card re-reads when a task ends, so right after one finished it may lag the server by a few seconds: wait for the two to agree (at most 20 s). */
async function cardMatchesServer(group) {
  let read = await cardMatchesServerOnce(group);
  for (let attempt = 0; attempt < 10 && read.problems.length; attempt += 1) {
    await page.waitForTimeout(2000);
    read = await cardMatchesServerOnce(group);
  }
  return read;
}
const answersText = (answers) => answers.map((answer) => (answer.clicked ? `${answer.key}:${answer.status}${answer.code ? ` ${answer.code}` : ''}` : `${answer.key}:${answer.note || '无按钮'}`)).join(' ');
const answeredWell = (answers) => answers.every((answer) => !answer.clicked || answer.status === 'dropped' || (answer.status >= 200 && answer.status < 300));
const requiredKeys = (group) => (group.slots || []).filter((slot) => !slot.optional).map((slot) => slot.key);

// #629: a source picture over the model's byte limit is frozen as a compressed copy; the card's views must still start.
async function itemF629() {
  const pick = await smallestImageLimit();
  if (!pick) {
    record('FAIL', '#629', '大图三视图', 'fake 栈没有上限低于 30 MB 的可用图生图模型，上传的大图触发不了压缩');
    return;
  }
  const bytes = Math.min(Math.round(pick.limit * 1.5), 45_000_000);
  const startGroup = async (title, name) => {
    const png = noisyPng(3072, 5504, bytes);
    const upload = await ctx.request.post(`${base}/api/v1/assets`, {
      headers: ORIGIN, timeout: 300000, multipart: { file: { name: `${tag}-${name}.png`, mimeType: 'image/png', buffer: png } },
    });
    const source = (await upload.json().catch(() => ({}))).id;
    if (![200, 201].includes(upload.status()) || !source) throw new Error(`upload of the ${png.length}-byte picture: ${upload.status()}`);
    const sid = await batchHSession(`${tag} ${title}`);
    await turn(sid, '用这张图做三视图 [[fake:tool:create_three_view {"reference_handles":["@img1"]}]]',
      { image_urls: [`/api/v1/assets/${source}`], ai_model: pick.model, explicit_model_2d: pick.model });
    await approvePending(sid);
    const found = await waitFor(async () => (await threeViewGroups(sid))[0] || null, 60, 2000);
    if (!found) throw new Error(`no three-view group (reply 「${String((await lastReply(sid)).content || '').slice(0, 120)}」)`);
    console.log(`#629 ${title} ${sid}: run ${found.run_id}, source ${source} ${png.length} bytes, model ${pick.model} (limit ${pick.limit})`);
    await openCard(sid);
    return { sid, run: found.run_id, size: png.length, keys: requiredKeys(found), initial: slotsText(found.slots) };
  };

  const main = await startGroup('大图三视图', 'front');
  await check('#629', `3072x5504 正面源图（${mb(main.size)}，超过「${pick.model}」上限 ${mb(pick.limit)}）：依次点各视角「继续生成」不返回 409，各视角都开始，卡片与服务端一致`, async () => {
    const answers = [];
    const reached = {};
    for (const key of main.keys) await advanceView(main, key, answers, reached);
    await openCard(main.sid);
    const consistency = await cardMatchesServer(main);
    const file = await shot('629', 'large-front');
    return {
      ok: answers.every((answer) => answer.status !== 409) && answeredWell(answers)
        && main.keys.every((key) => !STOPPED.includes(reached[key])) && consistency.problems.length === 0,
      detail: `必需视角=${main.keys.join(',')} 初始=${main.initial} 点击应答=${answersText(answers)} 到达=${JSON.stringify(reached)} `
        + `服务端=${consistency.server} 卡片=${consistency.card} 不一致=${JSON.stringify(consistency.problems)} 截图 ${file}`,
    };
  });

  const dropped = await startGroup('大图三视图 中途断开', 'front-drop');
  await check('#629', '点「继续生成」后立刻刷新（请求中途断开）：同一视角仍可再启动或已在后台继续，之后各视角不 409，卡片与服务端一致', async () => {
    const answers = [];
    const reached = {};
    const first = await waitFor(async () => (await groupOf(dropped)).slots.find((slot) => slot.status === 'not_started' && slot.startable) || null, 120, 2000);
    if (!first) return { ok: false, detail: `没有可启动的视角：${slotsText((await groupOf(dropped)).slots)}` };
    answers.push(await startView(first.key, { drop: true }));
    await cardOf().waitFor({ timeout: 60000 });
    await page.waitForTimeout(5000);
    const afterDrop = (await groupOf(dropped)).slots.find((slot) => slot.key === first.key) || {};
    const stuck = afterDrop.status === 'not_started' && !afterDrop.startable;
    if (afterDrop.status === 'not_started' && afterDrop.startable) {
      await openCard(dropped.sid);
      answers.push(await startView(first.key));
    }
    reached[first.key] = (await viewStarted(dropped, first.key))?.status || 'not_started';
    for (const key of dropped.keys.filter((entry) => entry !== first.key)) await advanceView(dropped, key, answers, reached);
    await openCard(dropped.sid);
    const consistency = await cardMatchesServer(dropped);
    const file = await shot('629', 'dropped-request');
    return {
      ok: !stuck && answers.every((answer) => answer.status !== 409) && answeredWell(answers)
        && dropped.keys.every((key) => !STOPPED.includes(reached[key])) && consistency.problems.length === 0,
      detail: `断开的视角=${first.key} 断开 5 秒后服务端=${afterDrop.status}${afterDrop.status === 'not_started' ? `/startable=${afterDrop.startable}` : ''} `
        + `点击应答=${answersText(answers)} 到达=${JSON.stringify(reached)} 服务端=${consistency.server} 卡片=${consistency.card} `
        + `不一致=${JSON.stringify(consistency.problems)} 截图 ${file}`,
    };
  });
  record('INFO', '#629', 'fake 缺口', 'fake 生图字节很小（平铺色块），生成图作正面时不触发压缩，这里改用上传的噪点大图；压缩耗时（PROD 约 51 秒）fake 也不模拟。见 fake 登记表 openai-images-v1/generated-picture-byte-weight');
}

// #630: the Agent can name the three-view group the person advanced on the card.
async function itemF630() {
  const sid = await batchHSession(`${tag} 三视图运行 ID`);
  await turn(sid, '画一把剑 [[fake:tool:refine_image {"prompt":"a plain steel sword on a white background"}]]');
  if (!await waitTasks(sid, (tasks) => tasks.some((task) => task.state === 'SUCCEEDED'))) throw new Error('the sword picture did not succeed');
  await turn(sid, '做三视图 [[fake:tool:create_three_view {"reference_handles":["@img1"]}]]');
  await approvePending(sid);
  const found = await waitFor(async () => (await threeViewGroups(sid))[0] || null, 60, 2000);
  if (!found) throw new Error('no three-view group');
  const group = { sid, run: found.run_id };
  console.log(`#630 ${sid}: run ${group.run}`);
  // The person advances the views on the card; the Agent is not involved, so no tool result names the run.
  await openCard(sid);
  const answers = [];
  const reached = {};
  for (const key of requiredKeys(found)) await advanceView(group, key, answers, reached);
  const done = await waitFor(async () => {
    await approvePending(sid);
    const slots = (await groupOf(group)).slots.filter((slot) => !slot.optional);
    return slots.length && slots.every((slot) => slot.status === 'succeeded') ? slots : null;
  }, 180, 2000);
  const views = done || (await groupOf(group)).slots;
  const viewTask = (views.find((slot) => slot.key !== 'front' && slot.task_id) || views.find((slot) => slot.task_id) || {}).task_id;
  const advanced = `卡片点击=${answersText(answers)} 视角=${slotsText((await groupOf(group)).slots)}`;

  await check('#630', '卡片上推进完视角后，3D 轮次的系统上下文列出本组运行 ID', async () => {
    await turn(sid, `用三视图生成 3D [[fake:system-contains ${group.run}]]`, { creative_domain: 'model-3d' });
    const reply = String((await lastReply(sid)).content || '');
    return { ok: Boolean(done) && reply.includes(`present: ${group.run}`), detail: `${advanced} 回复「${reply.slice(0, 120)}」` };
  });

  await check('#630', 'Agent 把视角 Task ID 当 three_view_group：拒绝、不提交，工具结果点名本组运行 ID', async () => {
    if (!viewTask) return { ok: false, detail: `没有视角任务：${advanced}` };
    const known = (await sessionTasks(sid)).map((task) => task.id);
    await turn(sid, `用三视图生成 3D [[fake:tool:create_model_3d_from_views ${JSON.stringify({ three_view_group: viewTask })}]] ${ECHO}`, { creative_domain: 'model-3d' });
    const made = await newTasks(sid, known);
    const { results, reply } = await echoedToolResults(sid);
    if (!results) return { info: true, detail: `目标 fake 不支持 ${ECHO}（早于本批 fake），读不到工具结果；新任务=${made.length} 回复「${reply.slice(0, 80)}」` };
    const resultText = results.join('\n');
    return {
      ok: made.length === 0 && resultText.includes(group.run),
      detail: `传入=${viewTask} 运行 ID=${group.run} 新任务=${made.length} 工具结果「${resultText.slice(0, 300)}」`,
    };
  });

  await check('#630', '传本组运行 ID：多视图 3D 提交成功（不被 group_not_found 拒绝）', async () => {
    const known = (await sessionTasks(sid)).map((task) => task.id);
    await turn(sid, `用三视图生成 3D [[fake:tool:create_model_3d_from_views ${JSON.stringify({ three_view_group: group.run })}]]`, { creative_domain: 'model-3d' });
    const made = await waitFor(async () => {
      await approvePending(sid);
      return (await newTasks(sid, known)).find((task) => /3d/i.test(String(task.capability || '')) && ['SUCCEEDED', 'FAILED'].includes(task.state)) || null;
    }, 300, 2000);
    const reply = String((await lastReply(sid)).content || '');
    return {
      ok: made?.state === 'SUCCEEDED',
      detail: `运行 ID=${group.run} 新任务=${made ? `${made.offering_id} ${made.state}` : '无'} 回复「${reply.slice(0, 120)}」`,
    };
  });
  record('INFO', '#630', '需真实模型复测', 'Agent 自己从系统上下文读出运行 ID 并用它调用：fake 对话模型只按标记调工具，这里验的是上下文里有运行 ID、拒绝文案点名运行 ID、用运行 ID 能提交');
}

// #631: a version that is the board's only layer, clicked on the strip and named by the Agent, is sent once.
async function itemF631() {
  const versionLabel = async (version) => (await text('ui.studio.version.label_2d')).replace('{version}', String(version));
  const madeFrom = async (parent) => (await text('ui.result.detail.made_from')).replace('{parent}', parent);
  const marker = (handle) => `[[fake:tool:refine_image ${JSON.stringify({ prompt: 'add a brass buckle', reference_handles: [handle], reference_purposes: { [handle]: 'the original pouch' } })}]]`;
  const { field } = await inputSourceField();
  const onBoard = async (picture) => {
    const name = picture.handle.replace(/^@/, '');
    if (!(await layerNames()).includes(name)) {
      await tileThumb(`n:${picture.id}`).dblclick();
      await page.waitForTimeout(2000);
    }
    return name;
  };
  // Sent with @画布: on dev-test (d68d1020) a plain send after the strip click went out without the canvas source
  // (inputSource null), so the PROD path, a canvas turn whose board shows the named version, was never reached.
  // @画布 makes it a canvas turn as it was in PROD; the Agent still names the version alone.
  const sendOnBoard = async (sid, content) => {
    const boardToken = `@${await text('ui.studio.composer.mention.token_board')}`;
    const box = composerBox();
    await box.fill('');
    await box.click();
    await box.pressSequentially('@');
    const list = tid('composer-mention-list');
    await list.waitFor({ timeout: 10000 });
    await list.locator(`[data-testid=composer-mention-option][data-handle="${boardToken}"]`).first().click();
    await page.waitForTimeout(500);
    await box.press('End');
    const before = await finalAssistantCount(sid);
    const posted = page.waitForRequest((request) => request.method() === 'POST' && request.url().includes(`/api/chat/sessions/${sid}/messages`), { timeout: 45000 });
    await page.keyboard.insertText(` ${content}`);
    const typed = await box.inputValue();
    await tid('composer-send').first().click();
    const request = await posted;
    let body = {};
    try { body = request.postDataJSON() || {}; } catch {}
    await waitFor(async () => (await finalAssistantCount(sid)) > before, 150);
    return { body, typed, others: [] };
  };
  const refine = async (sid, picture) => {
    const known = (await sessionTasks(sid)).map((task) => task.id);
    const sent = await sendOnBoard(sid, `给小袋加一个铜扣 ${marker(picture.handle)}`);
    const made = await waitFor(async () => (await newTasks(sid, known)).find((task) => task.state === 'SUCCEEDED') || null, 120, 1500);
    const [, detail] = made ? await api('GET', `/api/v1/tasks/${made.id}`) : [0, {}];
    const edges = made ? await incomingEdges(sid, made.id) : [];
    return { sent, made, prompt: String(detail?.input?.prompt || ''), sources: edges.map((edge) => String(edge.from)) };
  };

  const sid = await batchHSession(`${tag} 版本条单层画板`);
  await turn(sid, '画一个皮质小袋 [[fake:tool:refine_image {"prompt":"a small leather pouch on a white background"}]]');
  if (!await waitTasks(sid, (tasks) => tasks.some((task) => task.state === 'SUCCEEDED'))) throw new Error('the pouch picture did not succeed');
  const [v1] = await handleTasks(sid);
  if (!v1) throw new Error('the pouch picture has no handle');
  await openSession(sid);
  const name = await onBoard(v1);
  for (const other of (await layerNames()).filter((layer) => layer !== name)) await removeLayer(other).catch(() => {});
  await tileThumb(`n:${v1.id}`).click();
  await page.waitForTimeout(1500);
  const stage = await stageVersion();
  let made = null;
  await check('#631', 'v1 是画板唯一一层，单击版本条 v1 后经 @画布 refine（reference_handles 只有 v1）：新版本只连到 v1，提示词「image 1:」、不含「image 2」', async () => {
    const layers = await layerNames();
    const result = await refine(sid, v1);
    made = result.made;
    const file = await shot('631', 'single-layer');
    return {
      ok: Boolean(made) && result.sources.length === 1 && result.sources[0].includes(v1.id) && /image 1:/.test(result.prompt) && !/image 2/.test(result.prompt),
      detail: `画布图层=${JSON.stringify(layers)} 舞台「${stage}」 POST ${field}=${JSON.stringify(result.sent.body[field] ?? null)} 发送时其他 POST=${JSON.stringify(result.sent.others)} `
        + `新任务=${made?.id || '无'} 入边来源=${JSON.stringify(result.sources)}（v1=${v1.id}） 提示词「${result.prompt.slice(0, 200)}」 截图 ${file}`,
    };
  });
  await check('#631', '新版本详情的来源是 v1，不是上传', async () => {
    if (!made) return { ok: false, detail: '上一项没有产出新任务' };
    const want = await madeFrom(await versionLabel(1));
    const upload = await madeFrom(await text('ui.studio.version.upload'));
    await openSession(sid);
    await tileOf(`n:${made.id}`).locator('[data-testid=version-detail]').click();
    await tid('result-detail-rows').first().waitFor({ timeout: 15000 });
    await page.waitForTimeout(1000);
    const values = Object.fromEntries(await tid('result-detail-row').evaluateAll((items) => items.map((item) => [item.dataset.key, item.innerText.replace(/\s+/g, ' ').trim()])));
    const file = await shot('631', 'detail-source');
    await page.keyboard.press('Escape');
    const source = values.source || '';
    return { ok: source.includes(want) && !source.includes(upload), detail: `来源行「${source}」 应含「${want}」、不含「${upload}」 截图 ${file}` };
  });

  // Control: with a second picture on the board the board is more than the named version, and #266 still leads
  // with the snapshot: the named version is image 2.
  const control = await batchHSession(`${tag} 版本条两层画板`);
  await turn(control, '画一个皮质小袋 [[fake:tool:refine_image {"prompt":"a small leather pouch on a white background"}]]');
  await turn(control, '再画一个水壶 [[fake:tool:create_concept_sketch {"prompt":"a metal canteen on a white background"}]]');
  if (!await waitTasks(control, (tasks) => tasks.filter((task) => task.state === 'SUCCEEDED').length >= 2)) throw new Error('two control pictures did not succeed');
  const [c1, c2] = await handleTasks(control);
  if (!c1 || !c2) throw new Error('the control pictures have no handles');
  await openSession(control);
  await onBoard(c1);
  await onBoard(c2);
  await tileThumb(`n:${c1.id}`).click();
  await page.waitForTimeout(1500);
  await check('#631', '对照：画板两层（v1 与另一张）时单击 v1 经 @画布 refine：快照仍在前（入边为上传快照与 v1），v1 是 image 2', async () => {
    const layers = await layerNames();
    const result = await refine(control, c1);
    const file = await shot('631', 'two-layers');
    return {
      // The snapshot has no purpose of its own, so only the named version's line shows, numbered 2; the lineage
      // has the snapshot upload and the version.
      ok: Boolean(result.made) && /image 2:/.test(result.prompt) && result.sources.length === 2
        && result.sources.some((source) => source.startsWith('upload:')) && result.sources.some((source) => source.includes(c1.id)),
      detail: `画布图层=${JSON.stringify(layers)} POST ${field}=${JSON.stringify(result.sent.body[field] ?? null)} 新任务=${result.made?.id || '无'} `
        + `入边来源=${JSON.stringify(result.sources)} 提示词「${result.prompt.slice(0, 200)}」 截图 ${file}`,
    };
  });
  record('INFO', '#631', '需真实模型复测', '真实 GPT/QWEN 对话下 Agent 自己选 @img1 并写 reference_purposes：fake 对话模型按标记调用，这里只验平台侧的参考图、编号与来源');
}

// #632: what the read-only check tools hand back to the model in a Chinese session is not English prose.
async function itemF632() {
  const sid = await batchHSession(`${tag} 检查结果语言`);
  await turn(sid, '画一个木箱 [[fake:tool:refine_image {"prompt":"a wooden crate on a white background"}]]');
  if (!await waitTasks(sid, (tasks) => tasks.some((task) => task.state === 'SUCCEEDED'))) throw new Error('the crate picture did not succeed');
  const [picture] = await handleTasks(sid);
  await turn(sid, '做一个木箱 3D 模型 [[fake:tool:create_model_3d {"prompt":"a wooden crate"}]]', { creative_domain: 'model-3d' });
  const model = await waitFor(async () => {
    await approvePending(sid);
    return (await sessionTasks(sid)).find((task) => /3d/i.test(String(task.capability || '')) && task.state === 'SUCCEEDED') || null;
  }, 300, 2000);
  const calls = [
    ['inspect_image', { image: picture?.handle || '@img1', checklist: '3d_input' }, 'image'],
    ['inspect_model', { model: model?.id || '' }, 'model-3d'],
    ['record_self_check', { model: model?.id || '', status: 'passed', summary: '轮廓与参考图一致' }, 'model-3d'],
  ];
  for (const [tool, args, domain] of calls) {
    await check('#632', `中文会话 ${tool}：交回给模型的结果没有连续 6 个以上英文单词`, async () => {
      if (domain === 'model-3d' && !model) return { ok: false, detail: 'fake 3D 任务没有成功' };
      await turn(sid, `检查一下 [[fake:tool:${tool} ${JSON.stringify(args)}]] ${ECHO}`, { creative_domain: domain });
      const { results, reply } = await echoedToolResults(sid);
      if (!results) {
        return { info: true, detail: `目标 fake 不支持 ${ECHO}（早于本批 fake），fake 控制面也不对浏览器开放，读不到工具结果；回复「${reply.slice(0, 80)}」` };
      }
      const found = englishLeaves(results);
      return { ok: results.join('').length > 0 && found.length === 0, detail: `英文整句 ${found.length} 处 ${JSON.stringify(found.slice(0, 6))} 结果共 ${results.join('').length} 字` };
    });
  }
  record('INFO', '#632', '需真实模型复测', 'QWEN-MAX 等真实模型在工具结果为中文后回复不再夹英文整段：fake 对话模型只回固定文字或回显，回复措辞要真实模型复测');
}

// ---------------------------------------------------------------- run
const runners = {
  586: item586, 559: item559, 587: item587, 560: item560, '548-skill': item548Skill, '548-chip': item548Chip,
  n1: itemN1, n2: itemN2, n3: itemN3, n4: itemN4, n5: itemN5, n6: itemN6, m1: itemM1, m2: itemM2,
  p1: itemP1, p2: itemP2, p3: itemP3, p4: itemP4,
  f610: itemF610, f611: itemF611, f612: itemF612, f614: itemF614, f615: itemF615, f616: itemF616, f617: itemF617,
  f618: itemF618, f619: itemF619, f621: itemF621, f623: itemF623, f624: itemF624,
  f629: itemF629, f630: itemF630, f631: itemF631, f632: itemF632,
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
  try { await cleanupBatchH(); } catch (error) { console.log(`cleanup: batch H sessions ${error.message.split('\n')[0]}`); }
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
