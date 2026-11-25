// 长程浏览器验收驱动（#407）：用 Agent 专用账号扮演一位真实用户，一步一步地在浏览器里和 Agent 对话、操作版本条与
// 详情、分支、Skill、批准卡和 3D 任务，每步截图、读 DOM 与公开 API，并把结果追加到 OUT/log.jsonl。
//
// 这是“人在回路”的驱动器：每条命令只做一步，下一步说什么由操作者（人或上层 Agent）看了上一步的结果再决定；
// 会话、开场余额等跨命令状态存在 OUT/state.json，浏览器用 OUT/profile 的持久配置（像同一个用户的同一个浏览器）。
//
// 用法（地址与 Agent 专用账号由 release-env.py 注入，凭据只在子进程环境里）：
//   python3 scripts/release-env.py smoke <环境> -- node scripts/acceptance/prod-long-run.mjs <命令> [参数]
//
// 命令（--session <名字> 选会话，缺省 main）：
//   init --title T                 新建会话、设为手工批准方式，记入 state.json
//   say --text "…"                 经输入框发一条消息，按 --sample 秒采样（截图、任务、批准卡、会话积分），等回复与本轮任务结束
//       [--skill 四视图]            发送前在设置的 Skills 页手选一个 Skill
//       [--on-approval stop|deny|approve:<上限>]  出现批准卡时：停在卡上（缺省）、拒绝、或报价不超过上限才批准
//       [--reload-at S] [--switch-at S]  进行到 S 秒时刷新页面 / 切到别的会话再回来，记录前后历史消息与图片数
//       [--max-new-tasks N]        本轮新任务超过 N 个就点停止并记 ALERT（防失控，缺省 12）
//       [--timeout S] [--sample S] [--no-wait-tasks]
//   choose --option 文本 [say 的采样参数]  在最新的选择卡（ask_choice）上点含该文本的选项并「确认」，然后像 say 一样观察这一轮
//   click --button 文本 [say 的采样参数]  点对话栏里最后一个名字含该文本的按钮（如三视图卡片的「继续生成」），然后像 say 一样观察
//   approve --max-credits N        批准页面上的待批准卡（报价超过上限的卡改为拒绝）；deny 拒绝全部待批准卡
//   watch [--reload-at S]          只观察：按采样间隔记录本会话未结束任务的进度，直到全部结束
//   strip                          读版本条（瓦片、类型、句柄、「更多」分组）
//   tile --index I [--double] [--in-more]   单击/双击第 I 个瓦片（或「更多」弹层里的第 I 个），记录引用芯片、代表图、顺序、图层
//   detail --index I [--in-more] [--insert] 打开详情，读信息行；--insert 点「插入到画布」
//   more                           打开「更多」弹层，读标题与全部条目
//   branch [--message I] [--go]    读某条消息的「分支到新对话」是否可用；--go 时点它，并核对新会话的历史
//   reload-check / switch-check    刷新页面 / 切走再切回，比较前后历史消息、图片与结果卡
//   skill-create --name N --requirement R [--save]  设置 → Skills 新建：只填名称和要求，点「生成」读草稿；--save 保存
//       [--manual --slug S --description D --prompt P]  改点「我自己写」，手填标识、说明、正文
//   pick-model --name N [--domain image|model-3d]  设置 → 模型页手选工作区模型（存在浏览器本地，像用户自己选的一样）
//   pick-chat-model [--name N]     设置 → 对话模型页：列出本会话可选的对话模型；给 --name 时选中它（会话级，记入 log）
//   task --id T                    只读：打印一个任务的公开投影（输入、参数、产物；URL 只留路径）
//   asset-check --id T             只读：逐个下载任务产物，核对字节数与 sha256
//   observe                        截图并记下会话积分、任务与消息数
//   admin                          只读看管理端：概览/用量、用户、Agent 配置页（含 Skills），截图并读用量分组表
//   balances --label L             读 /api/meshy/balance（各家原生余额），记入 state.json
//   coverage [--since ISO]         3D 覆盖率：/api/v1/offerings 的 3D Offering 全集 × 本账号 since 之后的任务（成功/失败/未测）
//   report                         由 log.jsonl、state.json、coverage.json 生成 report-auto.md（报告骨架与自动收集的异常）
//   dry-run                        只在 fake 形态目标上：用 fake 对话模型的工具标记把上面的命令串一遍，校验选择器（零费用）
//
// 门禁（AGENTS.md「上游接口与 Fake」）：目标形态来自 ART_AI_UPSTREAM_SHAPE（release-env.py smoke 设置）。
//   fake 形态不需要开关，且拒绝 --live；dry-run 只在 fake 形态运行。
//   其他形态（真实对话模型与真实生成）下，除只读命令（strip、observe、admin、balances、coverage、report）外，
//   都要 --live 与 ART_AI_LIVE_ACK=1 同时给出，缺一个就以退出码 2 拒绝，不发请求。每次真实运行前须取得用户当次同意。
// 输出：OUT（环境变量或 --out，缺省系统临时目录 plinth-prod-long-*）下的截图（时间戳命名）、log.jsonl、state.json。
// 不打印口令、Cookie 或签名 URL；资产 URL 只记路径。退出码：0 正常，1 本步出现 FAIL/ALERT，2 被拒绝或用法错误。
import crypto from 'node:crypto';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { resolvePlaywright } from './browser_matrix/lib.mjs';

// ---------------------------------------------------------------- arguments and gate
function parseArgs(argv) {
  const out = { _: [] };
  for (let i = 0; i < argv.length; i += 1) {
    const item = argv[i];
    if (!item.startsWith('--')) { out._.push(item); continue; }
    const key = item.slice(2);
    const next = argv[i + 1];
    if (next === undefined || next.startsWith('--')) out[key] = true;
    else { out[key] = next; i += 1; }
  }
  return out;
}
const args = parseArgs(process.argv.slice(2));
const command = args['dry-run'] ? 'dry-run' : args._[0];
const COMMANDS = new Set(['init', 'asset-check', 'say', 'choose', 'click', 'approve', 'deny', 'watch', 'strip', 'tile', 'detail', 'more', 'branch', 'reload-check',
  'switch-check', 'skill-create', 'pick-model', 'pick-chat-model', 'task', 'observe', 'admin', 'balances', 'coverage', 'report', 'dry-run']);
const READ_ONLY = new Set(['task', 'asset-check', 'strip', 'observe', 'admin', 'balances', 'coverage', 'report']);
const refuse = (message) => { console.error(`refusing: ${message}`); process.exit(2); };
if (!COMMANDS.has(command)) refuse(`unknown command "${command || ''}"; see the header of ${path.basename(new URL(import.meta.url).pathname)}`);

const base = (process.env.BASE || '').replace(/\/+$/, '');
const shape = process.env.ART_AI_UPSTREAM_SHAPE || 'unknown';
if (command !== 'report') {
  const missing = ['BASE', 'AGENT_TEST_USERNAME', 'AGENT_TEST_PASSWORD'].filter((name) => !process.env[name]);
  if (missing.length) refuse(`missing environment: ${missing.join(', ')} (run through python3 scripts/release-env.py smoke <env> -- node …)`);
  if (shape === 'fake') {
    if (args.live) refuse('the target is fake-shaped; --live is for a real target only');
  } else if (command === 'dry-run') {
    refuse(`dry-run runs on a fake-shaped target only; this target is "${shape}"`);
  } else if (!READ_ONLY.has(command) && (!args.live || process.env.ART_AI_LIVE_ACK !== '1')) {
    refuse(`the target shape is "${shape}": "${command}" may call the real chat model or start paid generations; it needs --live and ART_AI_LIVE_ACK=1, and the user's consent for this run`);
  }
}

const OUT = args.out || process.env.OUT || fs.mkdtempSync(path.join(os.tmpdir(), 'plinth-prod-long-'));
fs.mkdirSync(OUT, { recursive: true });
const STATE_FILE = path.join(OUT, 'state.json');
const LOG_FILE = path.join(OUT, 'log.jsonl');
const loadState = () => { try { return JSON.parse(fs.readFileSync(STATE_FILE, 'utf8')); } catch { return { sessions: {}, balances: {} }; } };
const state = loadState();
const saveState = () => fs.writeFileSync(STATE_FILE, `${JSON.stringify(state, null, 2)}\n`);
const sessionName = String(args.session || 'main');
const stamp = () => new Date().toISOString().replace(/[-:]/g, '').replace(/\..*/, '').replace('T', '-');
const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
const num = (value, fallback) => (value === undefined || value === true ? fallback : Number(value));
const TERMINAL = /SUCCEEDED|FAILED|CANCEL|EXPIRED|REJECTED|DENIED/;
const assetIdOf = (src) => (String(src || '').match(/asset_[0-9a-f]+/) || [''])[0];
const pathOnly = (url) => { try { return new URL(url, base || 'http://x').pathname; } catch { return String(url).split('?')[0]; } };
const redactText = (value, limit = 600) => String(value || '').replace(/https?:\/\/\S+/g, (url) => pathOnly(url)).replace(/\s+/g, ' ').trim().slice(0, limit);

let exitCode = 0;
const findings = [];
const allFindings = [];
const note = (status, name, detail = '') => {
  findings.push({ status, name, detail });
  allFindings.push({ status, name, detail });
  if (['FAIL', 'ALERT'].includes(status)) exitCode = 1;
  console.log(`${status}: ${name}${detail ? ` — ${detail}` : ''}`);
};
function logEntry(entry) {
  fs.appendFileSync(LOG_FILE, `${JSON.stringify({ at: new Date().toISOString(), command, session: sessionName, ...entry })}\n`);
}

// ---------------------------------------------------------------- 3D coverage (no browser needed)
const THREE_D_PROVIDERS = new Set(['meshy', 'tripo', 'hyper3d']);
const TWO_D_CAPABILITIES = new Set(['text-to-image', 'image-to-image']);
/** The 3D universe: every Offering of the three 3D providers except their 2D picture Offerings. */
const isThreeDOffering = (offering) => THREE_D_PROVIDERS.has(offering.provider_id) && !TWO_D_CAPABILITIES.has(offering.capability);

/**
 * The ledger of 3D feature points: one row per provider × capability (several Offerings of one capability count once).
 * A task counts as the browser's when its session is one this driver created, otherwise as another path's (the L5 chains).
 */
function coverageTable(offerings, tasks, browserSessions = new Set()) {
  const units = new Map();
  for (const offering of offerings.filter(isThreeDOffering)) {
    const key = `${offering.provider_id}|${offering.capability}`;
    const unit = units.get(key) || { provider: offering.provider_id, capability: offering.capability, offerings: [], available: false };
    unit.offerings.push(offering.id);
    unit.available ||= offering.available !== false;
    units.set(key, unit);
  }
  const rows = [...units.values()].map((unit) => {
    const own = tasks.filter((task) => unit.offerings.includes(task.offering_id));
    const succeeded = own.filter((task) => task.state === 'SUCCEEDED');
    const failed = own.filter((task) => task.state !== 'SUCCEEDED' && TERMINAL.test(task.state));
    const running = own.filter((task) => !TERMINAL.test(task.state));
    const pathOf = (task) => (browserSessions.has(task.session_id) ? '浏览器' : 'L5/其他');
    const status = succeeded.length ? `${pathOf(succeeded[0])}成功` : failed.length ? '失败' : running.length ? '进行中' : '未测';
    return {
      ...unit, status, succeeded: succeeded.length, failed: failed.length, running: running.length,
      tasks: own.map((task) => `${task.id}:${task.state}:${pathOf(task)}:${task.offering_id}`),
    };
  }).sort((a, b) => a.provider.localeCompare(b.provider) || a.capability.localeCompare(b.capability));
  const byProvider = {};
  for (const row of rows) {
    const entry = byProvider[row.provider] ||= { total: 0, succeeded: 0, failed: 0, untested: 0 };
    entry.total += 1;
    if (row.succeeded) entry.succeeded += 1;
    else if (row.failed) entry.failed += 1;
    else entry.untested += 1;
  }
  const total = rows.length;
  const succeeded = rows.filter((row) => row.succeeded).length;
  return { rows, byProvider, total, succeeded, ratio: total ? succeeded / total : 0 };
}
function coverageMarkdown(table, since) {
  const percent = (a, b) => (b ? `${((a / b) * 100).toFixed(1)}%` : '—');
  const lines = [`3D 功能点台账（全集：公开 /api/v1/offerings 中 meshy、tripo、hyper3d 的非 2D Offering，按 provider × capability 计；任务：本账号 ${since} 之后创建的）`, '',
    '| 家 | 能力 | Offering | 成功 | 失败 | 进行中 | 状态 | 任务（id:状态:路径） |', '|---|---|---|---|---|---|---|---|'];
  for (const row of table.rows) {
    lines.push(`| ${row.provider} | ${row.capability} | ${row.offerings.map((id) => `\`${id}\``).join('<br>')} | ${row.succeeded} | ${row.failed} | ${row.running} | ${row.status}${row.available ? '' : '（不可用）'} | ${row.tasks.slice(0, 6).join('<br>')}${row.tasks.length > 6 ? '<br>…' : ''} |`);
  }
  lines.push('', '| 家 | 功能点 | 成功 | 失败 | 未测 | 覆盖率 |', '|---|---|---|---|---|---|');
  for (const [provider, entry] of Object.entries(table.byProvider)) {
    lines.push(`| ${provider} | ${entry.total} | ${entry.succeeded} | ${entry.failed} | ${entry.untested} | ${percent(entry.succeeded, entry.total)} |`);
  }
  const failed = Object.values(table.byProvider).reduce((sum, entry) => sum + entry.failed, 0);
  lines.push(`| 合计 | ${table.total} | ${table.succeeded} | ${failed} | ${table.total - table.succeeded - failed} | ${percent(table.succeeded, table.total)} |`);
  return `${lines.join('\n')}\n`;
}

// ---------------------------------------------------------------- report skeleton (no network)
function readLog() {
  try { return fs.readFileSync(LOG_FILE, 'utf8').split('\n').filter(Boolean).map((line) => JSON.parse(line)); } catch { return []; }
}
function summaryOf(file) {
  try {
    const text = fs.readFileSync(path.join(OUT, file), 'utf8');
    const lines = text.split('\n');
    const pick = lines.filter((line) => /PASS=\d+ FAIL=\d+|^(FAIL|SKIP|SOFT-FAIL|PAID-TASK-ALERT|RECONCILE)|^exit=/.test(line));
    return pick.length ? pick.map((line) => `    ${line.slice(0, 220)}`).join('\n') : '    （日志里没有汇总行）';
  } catch { return '    （没有该日志）'; }
}
function writeReport() {
  const entries = readLog();
  const lines = ['# PROD 长程验收报告（自动骨架）', '',
    `- 生成时间：${new Date().toISOString()}`, `- 目标：${state.base || base || '（未记录）'}，revision ${state.revision || process.env.EXPECT_SHA || '?'}，形态 ${state.shape || shape}`,
    `- 会话：${Object.entries(state.sessions || {}).map(([name, sid]) => `${name}=${sid}`).join('，') || '（无）'}`, '',
    '## A. 冒烟', '', '```', 'L1:', summaryOf('l1-free.log'), 'L1 Agent:', summaryOf('l1-agent.log'), '```', '',
    '## L5 三家 3D 端点链', '', '```'];
  for (const name of ['l5-meshy.log', 'l5-rodin.log', 'l5-tripo.log']) lines.push(`${name}:`, summaryOf(name));
  lines.push('```', '', '## 长程分步记录', '', '| 时间 | 会话 | 命令 | 输入 | 结果摘要 | 新任务 | 会话积分 | 异常 | 截图 |', '|---|---|---|---|---|---|---|---|---|');
  for (const entry of entries) {
    const tasks = (entry.newTasks || []).map((task) => `${task.capability}:${task.state}${task.dims ? `(${task.dims})` : ''}`).join(' ');
    const anomalies = [...(entry.netErrors || []).map((item) => `${item.status} ${item.method} ${item.path}${item.requestId ? ` rid=${item.requestId}` : ''}`),
      ...(entry.consoleErrors || []).map((item) => `console: ${item.slice(0, 80)}`), ...(entry.alerts || [])].join('<br>');
    const cell = (value) => String(value ?? '').replace(/\|/g, '\\|').replace(/\n/g, ' ');
    lines.push(`| ${cell(entry.at?.slice(11, 19))} | ${cell(entry.session)} | ${cell(entry.command)} | ${cell(redactText(entry.input, 80))} | ${cell(redactText(entry.summary, 200))} | ${cell(tasks)} | ${cell(entry.credits)} | ${cell(anomalies)} | ${cell((entry.shots || []).slice(-2).join(' '))} |`);
  }
  lines.push('', '## 3D 覆盖率', '');
  try { lines.push(fs.readFileSync(path.join(OUT, 'coverage.md'), 'utf8')); } catch { lines.push('（还没有运行 coverage）'); }
  lines.push('', '## 余额对账（/api/meshy/balance，原生单位）', '', '| 标签 | 时间 | 各家余额 |', '|---|---|---|');
  for (const [label, reading] of Object.entries(state.balances || {})) {
    lines.push(`| ${label} | ${reading.at} | ${Object.entries(reading.accounts || {}).map(([provider, amount]) => `${provider} ${amount}`).join('，')} |`);
  }
  lines.push('', '## Agent 行为评价', '', '- 场景图：', '- 原画（角色/道具概念）：', '- UI（图标/界面元素）：', '- 3D：', '',
    '## 缺陷清单', '', '| # | 严重程度 | 现象 | 复现步骤 | request id | 截图 |', '|---|---|---|---|---|---|', '',
    '## 费用对账', '', '| 项 | 张数/次数 | 积分 | 上限 |', '|---|---|---|---|', '');
  const file = path.join(OUT, String(args.file || 'report-auto.md'));
  fs.writeFileSync(file, `${lines.join('\n')}\n`);
  console.log(`report skeleton: ${file}`);
}
if (command === 'report') { writeReport(); process.exit(0); }

// ---------------------------------------------------------------- browser and API
const { chromium } = resolvePlaywright();
const ctx = await chromium.launchPersistentContext(path.join(OUT, 'profile'), {
  viewport: { width: 1440, height: 900 }, locale: 'zh-CN', serviceWorkers: 'block',
});
const page = ctx.pages()[0] || await ctx.newPage();
const ORIGIN = { Origin: new URL(base).origin };
const netErrors = [];
const consoleErrors = [];
const pageErrors = [];
page.on('dialog', (dialog) => dialog.dismiss().catch(() => {}));
page.on('pageerror', (error) => pageErrors.push(error.message.slice(0, 300)));
page.on('console', (message) => { if (message.type() === 'error') consoleErrors.push(redactText(message.text(), 300)); });
page.on('response', (response) => {
  if (response.status() < 400 || !response.url().startsWith(base)) return;
  netErrors.push({ status: response.status(), method: response.request().method(), path: pathOnly(response.url()), requestId: response.headers()['x-request-id'] || '' });
});

async function api(method, route, data) {
  for (let attempt = 0; attempt < 4; attempt += 1) {
    const response = await ctx.request.fetch(base + route, { method, data, headers: ORIGIN });
    if (response.status() === 429) { note('INFO', `429 on ${method} ${pathOnly(route)}`, 'waiting 10 s'); await sleep(10000); continue; }
    let body = {};
    try { body = await response.json(); } catch {}
    if (response.status() >= 400) netErrors.push({ status: response.status(), method, path: pathOnly(route), requestId: response.headers()['x-request-id'] || '', via: 'api' });
    return [response.status(), body];
  }
  return [429, {}];
}
const messagesOf = async (sid) => {
  const [, body] = await api('GET', `/api/chat/sessions/${sid}/messages`);
  return Array.isArray(body) ? body : (body.messages || body.data || []);
};
const sessionTasks = async (sid) => {
  const [, body] = await api('GET', `/api/v1/tasks?session_id=${sid}&limit=100`);
  return body.tasks || body.data || [];
};
const pendingCards = async (sid) => {
  const [, body] = await api('GET', `/api/v1/chat/sessions/${sid}/pending-approvals`);
  return (body.items || []).filter((item) => item.status === 'pending');
};
async function sessionRecord(sid) {
  let cursor = '';
  for (let pageIndex = 0; pageIndex < 10; pageIndex += 1) {
    const [, body] = await api('GET', `/api/chat/sessions?limit=50${cursor ? `&cursor=${encodeURIComponent(cursor)}` : ''}`);
    const found = (body.sessions || []).find((item) => item.id === sid);
    if (found || !body.next_cursor) return found || null;
    cursor = body.next_cursor;
  }
  return null;
}
async function waitFor(check, seconds, step = 1000) {
  const end = Date.now() + seconds * 1000;
  while (Date.now() < end) {
    const value = await check();
    if (value) return value;
    await sleep(step);
  }
  return null;
}
async function login() {
  const response = await ctx.request.post(`${base}/api/auth/login`, {
    data: { username: process.env.AGENT_TEST_USERNAME, password: process.env.AGENT_TEST_PASSWORD }, headers: ORIGIN,
  });
  if (response.status() !== 200) { console.error(`Agent 专用账号登录失败：HTTP ${response.status()}`); await ctx.close(); process.exit(1); }
  const [, version] = await api('GET', '/api/version');
  state.base = base; state.shape = shape; state.revision = version.revision || version.commit || process.env.EXPECT_SHA || '';
  console.log(`target ${base} shape=${shape} version=${version.version || '?'} out=${OUT}`);
}

// Image dimensions from the bytes (PNG, JPEG, WebP); null when unknown.
function imageSize(buffer) {
  const b = Buffer.from(buffer);
  if (b.length > 24 && b.readUInt32BE(0) === 0x89504e47) return [b.readUInt32BE(16), b.readUInt32BE(20)];
  if (b.length > 4 && b[0] === 0xff && b[1] === 0xd8) {
    let offset = 2;
    while (offset + 9 < b.length) {
      if (b[offset] !== 0xff) { offset += 1; continue; }
      const marker = b[offset + 1];
      const length = b.readUInt16BE(offset + 2);
      if (marker >= 0xc0 && marker <= 0xcf && ![0xc4, 0xc8, 0xcc].includes(marker)) return [b.readUInt16BE(offset + 7), b.readUInt16BE(offset + 5)];
      offset += 2 + length;
    }
  }
  if (b.length > 30 && b.toString('ascii', 0, 4) === 'RIFF' && b.toString('ascii', 8, 12) === 'WEBP') {
    const kind = b.toString('ascii', 12, 16);
    if (kind === 'VP8X') return [1 + b.readUIntLE(24, 3), 1 + b.readUIntLE(27, 3)];
    if (kind === 'VP8L') { const bits = b.readUInt32LE(21); return [1 + (bits & 0x3fff), 1 + ((bits >> 14) & 0x3fff)]; }
    if (kind === 'VP8 ') return [b.readUInt16LE(26) & 0x3fff, b.readUInt16LE(28) & 0x3fff];
  }
  return null;
}
async function describeTask(task) {
  const out = { id: task.id, capability: task.capability, offering: task.offering_id, state: task.state, progress: task.progress, assets: (task.assets || []).length };
  const picture = (task.assets || []).find((asset) => /^image\//.test(asset.media_type || ''));
  if (picture && task.state === 'SUCCEEDED' && /image/.test(task.capability || '')) {
    try {
      const response = await ctx.request.get(base + picture.url, { headers: ORIGIN });
      const size = imageSize(await response.body());
      if (size) out.dims = `${size[0]}x${size[1]}`;
    } catch {}
  }
  if (task.error || task.failure) out.error = redactText(JSON.stringify(task.error || task.failure), 200);
  return out;
}

// ---------------------------------------------------------------- page helpers
const tid = (id, scope = page) => scope.locator(`[data-testid="${id}"]`);
const shots = [];
async function shot(name, fullPage = false) {
  const file = path.join(OUT, `${stamp()}-${sessionName}-${command}-${name}.png`.replace(/[^\w.\-一-鿿]/g, '_'));
  try { await page.screenshot({ path: file, fullPage }); shots.push(path.basename(file)); } catch {}
  return path.basename(file);
}
async function text(key) {
  return page.evaluate((name) => window.__ART_AI_PRODUCT_SCHEMA__?.content?.messages?.[name] ?? '', key);
}
async function watchToasts() {
  await page.evaluate(() => {
    if (window.__plToastObserver) return;
    window.__plToasts = window.__plToasts || [];
    window.__plToastObserver = new MutationObserver(() => {
      for (const toast of document.querySelectorAll('[data-sonner-toast]')) {
        const value = toast.innerText.trim();
        if (value) window.__plToasts.push(value);
      }
    });
    window.__plToastObserver.observe(document.body, { childList: true, subtree: true, characterData: true });
  }).catch(() => {});
}
const toastsSeen = async () => page.evaluate(() => window.__plToasts || []).catch(() => []);
async function openSession(sid) {
  await page.goto(`${base}/?session=${sid}`, { waitUntil: 'load' });
  await tid('composer-send').first().waitFor({ timeout: 45000 });
  await page.waitForTimeout(2500);
  await watchToasts();
}
const composer = () => page.locator('[data-testid=studio-composer] textarea').first();
const isRunning = async () => (await tid('composer-stop').count()) > 0;
const sessionCredits = async () => (await tid('studio-session-credits').first().innerText({ timeout: 2000 }).catch(() => '')).trim();
const layerNames = async () => tid('layer-row').evaluateAll((rows) => rows.map((row) => row.innerText.trim())).catch(() => []);
const chipAsset = async () => assetIdOf(await tid('composer-object-chip').first().locator('img').first().getAttribute('src', { timeout: 2000 }).catch(() => ''));
async function chatCounts() {
  return page.evaluate(() => {
    const root = document.querySelector('[data-testid=chat-messages]');
    return {
      messages: root ? root.querySelectorAll('[data-testid=chat-message]').length : 0,
      img: root ? root.querySelectorAll('img').length : 0,
      cards: root ? root.querySelectorAll('[data-testid=chat-image-result]').length : 0,
      handles: root ? [...root.querySelectorAll('[data-testid=chat-result-handle]')].map((node) => node.innerText.trim()) : [],
      blank: document.body.innerText.trim().length < 20,
    };
  }).catch(() => ({ messages: -1, img: -1, cards: -1, handles: [], blank: true }));
}
const settings = () => tid('composer-settings-dialog');
async function openSettings(tab) {
  if (!await settings().count()) {
    await tid('composer-settings-open').first().click();
    await settings().waitFor({ timeout: 10000 });
  }
  await tid(`composer-settings-tab-${tab}`).click();
  await page.locator(`[data-testid=composer-settings-tab-${tab}][aria-selected=true]`).waitFor({ timeout: 10000 });
  await page.waitForTimeout(800);
}
async function closeSettings() {
  for (let attempt = 0; attempt < 3 && await settings().count(); attempt += 1) {
    await page.keyboard.press('Escape');
    await page.waitForTimeout(400);
  }
}
async function pickSkill(name) {
  await openSettings('skills');
  await tid('workspace-skill-picker-panel').first().locator('[role=menuitemradio]', { hasText: name }).first().click();
  await page.waitForTimeout(1000);
  await closeSettings();
  return (await tid('composer-skill-chip').first().innerText().catch(() => '')).replace(/\s+/g, ' ').trim();
}
const strip = () => tid('version-strip').first();
const foldList = () => tid('version-fold-list').first();
async function stripState() {
  return strip().evaluate((root) => {
    const tile = (node) => ({
      key: node.dataset.key,
      kind: (node.querySelector('[data-testid=kind-badge]')?.innerText || '').trim(),
      handle: (node.querySelector('[data-testid=version-handle-badge]')?.innerText || '').trim(),
      asset: ((node.querySelector('img')?.getAttribute('src') || '').match(/asset_[0-9a-f]+/) || [''])[0],
    });
    return {
      tiles: [...root.querySelectorAll('[data-testid=version-tile]')].map(tile),
      folds: [...root.querySelectorAll('[data-testid=version-column]')].filter((column) => column.querySelector('[data-testid=version-fold]'))
        .map((column) => ({ visible: [...column.querySelectorAll('[data-testid=version-tile]')].map((node) => node.dataset.key), trigger: (column.querySelector('[data-testid=version-fold-trigger]')?.innerText || '').trim() })),
    };
  }).catch((error) => ({ tiles: [], folds: [], error: error.message.split('\n')[0] }));
}
async function openFold(index = 0) {
  if (!await foldList().isVisible().catch(() => false)) {
    const column = strip().locator('[data-testid=version-column]').filter({ has: page.locator('[data-testid=version-fold]') }).nth(index);
    await column.locator('[data-testid=version-fold-trigger]').click();
  }
  await foldList().waitFor({ timeout: 10000 });
  await page.waitForTimeout(500);
}
async function closeFold() {
  for (let attempt = 0; attempt < 2 && await foldList().isVisible().catch(() => false); attempt += 1) {
    await page.keyboard.press('Escape');
    await page.waitForTimeout(300);
  }
}
const tileAt = (index, inMore) => (inMore ? foldList() : strip()).locator('[data-testid=version-tile]').nth(index);

async function currentSession() {
  const sid = args['session-id'] || state.sessions?.[sessionName];
  if (!sid) refuse(`no session "${sessionName}" in ${STATE_FILE}; run init first or pass --session-id`);
  return sid;
}
/** One log line per step; the step's screenshots, HTTP errors, console errors and findings go with it and are reset. */
function record(entry) {
  logEntry({ ...entry, shots: [...shots], netErrors: [...netErrors], consoleErrors: [...consoleErrors, ...pageErrors.map((item) => `pageerror: ${item}`)], findings: [...findings] });
  for (const list of [shots, netErrors, consoleErrors, pageErrors, findings]) list.length = 0;
}

// ---------------------------------------------------------------- commands
async function cmdInit(title) {
  const [status, body] = await api('POST', '/api/chat/sessions', { title });
  if (![200, 201].includes(status) || !body.id) throw new Error(`create session ${status}`);
  const sid = body.id;
  const [approvalStatus, approval] = await api('PUT', `/api/v1/chat/sessions/${sid}/approval`, { approvalMode: 'manual' });
  if (approvalStatus !== 200 || approval.approvalMode !== 'manual') {
    note('ALERT', 'set manual approval', `HTTP ${approvalStatus} mode=${approval.approvalMode}; do not send generation requests to this session`);
  } else note('PASS', 'session created and set to manual approval', sid);
  state.sessions[sessionName] = sid;
  state.startedAt ||= new Date().toISOString();
  saveState();
  await openSession(sid);
  await shot('opened');
  record({ input: title, summary: `session ${sid} manual=${approval.approvalMode}`, sessionId: sid });
  return sid;
}

async function cardSnapshot() {
  return tid('studio-approval-card').evaluateAll((cards) => cards.map((card) => ({
    text: card.innerText.replace(/\s+/g, ' ').trim().slice(0, 400),
    credits: (card.querySelector('[data-testid=studio-approval-credits]')?.innerText || '').trim(),
    kind: (card.querySelector('[data-testid=studio-approval-kind]')?.innerText || '').trim(),
    model: (card.querySelector('[data-testid=studio-approval-model]')?.innerText || '').trim(),
    canApprove: Boolean(card.querySelector('[data-testid=studio-approval-approve]')),
  }))).catch(() => []);
}
const creditsNumber = (value) => { const match = String(value || '').replace(/,/g, '').match(/(\d+(?:\.\d+)?)/); return match ? Number(match[1]) : NaN; };
/** Approve every visible card whose quote is within the ceiling; a card over it, or without a readable quote, is denied. */
async function decideCards(ceiling, decision) {
  const decided = [];
  for (let round = 0; round < 10; round += 1) {
    const card = tid('studio-approval-card').filter({ has: page.locator('[data-testid=studio-approval-approve]') }).first();
    if (!await card.count()) break;
    const info = { text: (await card.innerText()).replace(/\s+/g, ' ').trim().slice(0, 300), credits: (await card.locator('[data-testid=studio-approval-credits]').first().innerText().catch(() => '')).trim() };
    const quote = creditsNumber(info.credits);
    const approve = decision === 'approve' && Number.isFinite(quote) && quote <= ceiling;
    info.quote = quote;
    info.decision = approve ? 'approve' : 'deny';
    if (decision === 'approve' && !approve) note('ALERT', 'approval card over the ceiling or without a readable quote — denied', `quote=${info.credits} ceiling=${ceiling}`);
    await shot(`card-${info.decision}`);
    await card.locator(`[data-testid=studio-approval-${info.decision}]`).first().click();
    await page.waitForTimeout(2500);
    decided.push(info);
  }
  return decided;
}

async function sampleOnce(sid, known, startedAt) {
  const tasks = await sessionTasks(sid);
  const fresh = tasks.filter((task) => !known.has(task.id));
  return {
    t: Math.round((Date.now() - startedAt) / 1000),
    running: await isRunning(),
    credits: await sessionCredits(),
    tasks: fresh.map((task) => ({ id: task.id, capability: task.capability, state: task.state, progress: task.progress })),
    cards: (await pendingCards(sid)).map((card) => ({ id: card.request_id, capability: card.capability })),
    counts: await chatCounts(),
  };
}
async function bounce(sid, how) {
  const before = await chatCounts();
  if (how === 'reload') {
    await page.reload({ waitUntil: 'load' });
  } else {
    const other = Object.values(state.sessions).find((item) => item !== sid);
    if (other) await openSession(other); else await page.goto(`${base}/`, { waitUntil: 'load' });
    await page.waitForTimeout(3000);
    await page.goto(`${base}/?session=${sid}`, { waitUntil: 'load' });
  }
  await tid('composer-send').first().waitFor({ timeout: 45000 });
  await page.waitForTimeout(3000);
  await watchToasts();
  const after = await chatCounts();
  const lost = after.messages < before.messages || after.img < before.img || after.cards < before.cards || before.handles.some((handle) => !after.handles.includes(handle));
  note(lost ? 'FAIL' : 'PASS', `${how === 'reload' ? '刷新' : '切走再切回'}后历史消息与图片不减`, `前 ${JSON.stringify(before)} 后 ${JSON.stringify(after)}`);
  await shot(`${how}-after`);
  return { how, before, after, lost };
}

async function cmdSay(sid, content, options = {}) {
  await openSession(sid);
  let skillChip = '';
  if (options.skill) {
    skillChip = await pickSkill(options.skill);
    note(skillChip.includes(options.skill) ? 'PASS' : 'FAIL', `手选 Skill「${options.skill}」`, `芯片「${skillChip}」`);
  }
  const beforeMessages = await messagesOf(sid);
  const known = new Set((await sessionTasks(sid)).map((task) => task.id));
  await shot('before');
  const posted = page.waitForResponse((response) => response.url().includes(`/api/chat/sessions/${sid}/`) && response.request().method() === 'POST', { timeout: 30000 }).catch(() => null);
  if (options.clickButton) {
    const button = tid('studio-chat-column').first().getByRole('button', { name: String(options.clickButton) }).last();
    await button.scrollIntoViewIfNeeded().catch(() => {});
    await button.click();
  } else if (options.choose) {
    const card = tid('chat-choice-card').last();
    await card.scrollIntoViewIfNeeded().catch(() => {});
    await card.locator('[data-testid=chat-choice-option]', { hasText: String(options.choose) }).first().click();
    await page.waitForTimeout(500);
    await card.locator('[data-testid=chat-choice-confirm]').first().click();
  } else {
    await composer().fill(content);
    await tid('composer-send').first().click();
  }
  const response = await posted;
  const startedAt = Date.now();
  if (!response || ![200, 201, 202].includes(response.status())) note(options.choose || options.clickButton ? 'INFO' : 'FAIL', 'message accepted', `POST ${response ? `${response.status()} ${pathOnly(response.url())}` : 'no request'}`);
  const timeout = num(options.timeout, 900) * 1000;
  const sampleEvery = num(options.sample, 10) * 1000;
  const maxNew = num(options['max-new-tasks'], 12);
  const policy = String(options['on-approval'] || 'stop');
  const samples = [];
  const bounces = [];
  const decided = [];
  const alerts = [];
  let reloadAt = options['reload-at'] ? num(options['reload-at']) * 1000 : 0;
  let switchAt = options['switch-at'] ? num(options['switch-at']) * 1000 : 0;
  let lastSample = 0;
  let idle = 0;
  let stoppedOnCard = false;
  while (Date.now() - startedAt < timeout) {
    const elapsed = Date.now() - startedAt;
    if (reloadAt && elapsed >= reloadAt) { bounces.push(await bounce(sid, 'reload')); reloadAt = 0; }
    if (switchAt && elapsed >= switchAt) { bounces.push(await bounce(sid, 'switch')); switchAt = 0; }
    const sample = await sampleOnce(sid, known, startedAt);
    if (Date.now() - lastSample >= sampleEvery) {
      sample.shot = await shot(`t${String(sample.t).padStart(4, '0')}`);
      samples.push(sample);
      lastSample = Date.now();
      console.log(`  t=${sample.t}s running=${sample.running} tasks=${sample.tasks.map((task) => `${task.capability}:${task.state}:${task.progress ?? ''}`).join(',')} cards=${sample.cards.length} ${sample.credits}`);
    }
    if (sample.tasks.length > maxNew) {
      alerts.push(`本轮新任务 ${sample.tasks.length} 个，超过 ${maxNew}：点停止`);
      note('ALERT', 'too many new tasks in one turn — stopped', `${sample.tasks.length} > ${maxNew}`);
      await tid('composer-stop').first().click().catch(() => {});
      break;
    }
    if (sample.cards.length) {
      if (policy === 'stop') { stoppedOnCard = true; await page.waitForTimeout(1500); break; }
      const [mode, ceiling] = policy.split(':');
      decided.push(...await decideCards(Number(ceiling || 0), mode === 'approve' ? 'approve' : 'deny'));
      continue;
    }
    const unfinished = sample.tasks.filter((task) => !TERMINAL.test(task.state));
    const settled = !sample.running && (options['no-wait-tasks'] || unfinished.length === 0);
    idle = settled && elapsed > 4000 ? idle + 1 : 0;
    if (idle >= 2) break;
    await sleep(2000);
  }
  if (Date.now() - startedAt >= timeout) { alerts.push(`超时 ${timeout / 1000}s`); note('ALERT', 'turn or its tasks did not end in time', `${timeout / 1000}s`); }
  await page.waitForTimeout(1500);
  const finalShot = await shot('final');
  const messages = await messagesOf(sid);
  const added = messages.slice(beforeMessages.length);
  const replies = added.filter((message) => message.role === 'assistant');
  const tools = replies.flatMap((message) => {
    let calls = message.toolCalls;
    if (typeof calls === 'string') { try { calls = JSON.parse(calls); } catch { calls = []; } }
    return (Array.isArray(calls) ? calls : []).map((call) => {
      const extra = Object.fromEntries(Object.entries(call).filter(([key]) => !['name', 'arguments', 'id', 'type'].includes(key)));
      return { name: call.name, args: redactText(typeof call.arguments === 'string' ? call.arguments : JSON.stringify(call.arguments || {}), 400), ...(Object.keys(extra).length ? { extra: redactText(JSON.stringify(extra), 300) } : {}) };
    });
  });
  // Attachments the turn added (results, crops, references), with their role and handle.
  const attachments = added.flatMap((message) => (message.attachments || []).map((item) => ({
    from: message.role, role: item.role || '', kind: item.kind || '', handle: item.handle || '', task: item.task_id || item.taskId || '', asset: item.assetId || item.asset_id || '',
  })));
  const newTasks = [];
  for (const task of (await sessionTasks(sid)).filter((item) => !known.has(item.id)).reverse()) newTasks.push(await describeTask(task));
  const usedSkills = await tid('chat-used-skills').last().innerText({ timeout: 1500 }).catch(() => '');
  const cards = await cardSnapshot();
  const toasts = [...new Set(await toastsSeen())];
  const reply = replies.map((message) => message.content || '').join('\n---\n');
  const session = await sessionRecord(sid);
  console.log(`reply: ${redactText(reply, 800)}`);
  console.log(`tools: ${tools.map((tool) => `${tool.name}(${tool.args.slice(0, 160)})`).join(' | ') || 'none'}`);
  console.log(`new tasks: ${newTasks.map((task) => `${task.capability}/${task.offering}:${task.state}${task.dims ? ` ${task.dims}` : ''}`).join(', ') || 'none'}`);
  console.log(`attachments: ${attachments.map((item) => `${item.from}:${item.role || item.kind}:${item.handle}`).join(', ') || 'none'}`);
  console.log(`session credits: ${await sessionCredits()} (estimatedCredits=${session?.estimatedCredits ?? '?'}) used-skills="${usedSkills.trim()}" cards=${cards.length} stoppedOnCard=${stoppedOnCard} toasts=${JSON.stringify(toasts)}`);
  record({
    input: content, skill: options.skill || '', skillChip, summary: redactText(reply, 1200), reply: redactText(reply, 4000), tools, newTasks, attachments,
    credits: `${await sessionCredits()} est=${session?.estimatedCredits ?? '?'}`, samples, bounces, decided, cards, stoppedOnCard,
    usedSkills: usedSkills.trim(), toasts, alerts, finalShot, durationSeconds: Math.round((Date.now() - startedAt) / 1000),
  });
}

async function cmdApprove(sid, decision) {
  await openSession(sid);
  const before = await cardSnapshot();
  const decided = await decideCards(num(args['max-credits'], 0), decision);
  if (!decided.length) note('INFO', 'no approval card with buttons on the page', JSON.stringify(before).slice(0, 300));
  for (const item of decided) note('INFO', `card ${item.decision}`, `${item.credits} — ${item.text.slice(0, 160)}`);
  await page.waitForTimeout(3000);
  await shot('after');
  record({ input: `${decision} max=${args['max-credits'] ?? '-'}`, summary: decided.map((item) => `${item.decision} ${item.credits}`).join('; '), cards: before, decided });
}

async function cmdWatch(sid) {
  await openSession(sid);
  const startedAt = Date.now();
  const timeout = num(args.timeout, 3600) * 1000;
  const sampleEvery = num(args.sample, 10) * 1000;
  let reloadAt = args['reload-at'] ? num(args['reload-at']) * 1000 : 0;
  const samples = [];
  const bounces = [];
  while (Date.now() - startedAt < timeout) {
    if (reloadAt && Date.now() - startedAt >= reloadAt) { bounces.push(await bounce(sid, 'reload')); reloadAt = 0; }
    const tasks = await sessionTasks(sid);
    const open = tasks.filter((task) => !TERMINAL.test(task.state));
    const progress = await tid('chat-task-progress').evaluateAll((nodes) => nodes.map((node) => node.innerText.replace(/\s+/g, ' ').trim().slice(0, 160))).catch(() => []);
    const sample = { t: Math.round((Date.now() - startedAt) / 1000), open: open.map((task) => ({ id: task.id, capability: task.capability, state: task.state, progress: task.progress })), progressCards: progress, credits: await sessionCredits() };
    sample.shot = await shot(`t${String(sample.t).padStart(4, '0')}`);
    samples.push(sample);
    console.log(`  t=${sample.t}s open=${sample.open.map((task) => `${task.capability}:${task.state}:${task.progress ?? ''}`).join(',') || 'none'} ui=${JSON.stringify(progress).slice(0, 200)}`);
    if (!open.length) break;
    await sleep(sampleEvery);
  }
  const tasks = [];
  for (const task of (await sessionTasks(sid)).slice(0, 10)) tasks.push(await describeTask(task));
  record({ input: 'watch', summary: `samples=${samples.length} last tasks: ${tasks.map((task) => `${task.capability}:${task.state}`).join(', ')}`, samples, bounces, newTasks: tasks });
}

async function cmdStrip(sid) {
  await openSession(sid);
  const stateNow = await stripState();
  const file = await shot('strip');
  console.log(JSON.stringify(stateNow, null, 1).slice(0, 3000));
  record({ input: 'strip', summary: `${stateNow.tiles.length} tiles, ${stateNow.folds.length} folds`, strip: stateNow, file });
  return stateNow;
}

async function cmdTile(sid, index, options = {}) {
  await openSession(sid);
  const before = { strip: await stripState(), chip: await chipAsset(), layers: await layerNames() };
  if (options['in-more']) await openFold(num(options.fold, 0));
  const tile = tileAt(index, options['in-more']);
  const key = await tile.getAttribute('data-key');
  const asset = assetIdOf(await tile.locator('img').first().getAttribute('src').catch(() => ''));
  const button = tile.locator('button').first();
  if (options.double) await button.dblclick(); else await button.click();
  await page.waitForTimeout(2500);
  await closeFold();
  const after = { strip: await stripState(), chip: await chipAsset(), layers: await layerNames(), stage: (await tid('stage-version').first().innerText().catch(() => '')).trim() };
  const orderSame = JSON.stringify(before.strip.tiles.map((item) => item.key)) === JSON.stringify(after.strip.tiles.map((item) => item.key));
  const representativeSame = JSON.stringify(before.strip.folds.map((fold) => fold.visible[0])) === JSON.stringify(after.strip.folds.map((fold) => fold.visible[0]));
  const action = options.double ? '双击' : '单击';
  if (options.double) {
    note(after.layers.length > before.layers.length || after.layers.join() !== before.layers.join() ? 'PASS' : 'INFO', `${action}瓦片 ${key} 插入画布`, `图层 ${JSON.stringify(before.layers)} → ${JSON.stringify(after.layers)}`);
  } else {
    note(after.chip === asset ? 'PASS' : 'FAIL', `${action}瓦片 ${key} 后输入框引用芯片换成它`, `芯片 ${before.chip} → ${after.chip}，瓦片资产 ${asset}`);
  }
  note(orderSame && representativeSame ? 'PASS' : 'FAIL', `${action}后版本条顺序与代表图不变`, `顺序不变=${orderSame} 代表图不变=${representativeSame}`);
  await shot(`${options.double ? 'dbl' : 'click'}-${index}`);
  record({ input: `${action} ${options['in-more'] ? '更多弹层' : '版本条'}#${index}`, summary: `key=${key} chip ${before.chip}→${after.chip} layers ${before.layers.length}→${after.layers.length} stage=${after.stage}`, before, after });
}

async function cmdDetail(sid, index, options = {}) {
  await openSession(sid);
  if (options['in-more']) await openFold(num(options.fold, 0));
  const tile = tileAt(index, options['in-more']);
  await tile.locator('[data-testid=version-detail]').click();
  const dialog = tid('result-detail').first();
  await dialog.waitFor({ timeout: 15000 });
  await page.waitForTimeout(800);
  const rows = (await tid('result-detail-rows').first().innerText().catch(() => '')).replace(/\s+/g, ' ').trim();
  const action = (await tid('result-detail-open-canvas').first().innerText().catch(() => '')).trim();
  const file = await shot(`detail-${index}`);
  let inserted = null;
  if (options.insert && action) {
    const before = await layerNames();
    await tid('result-detail-open-canvas').first().click();
    await page.waitForTimeout(2500);
    await page.keyboard.press('Escape').catch(() => {});
    inserted = { before, after: await layerNames() };
    note(inserted.after.join() !== inserted.before.join() ? 'PASS' : 'INFO', `详情「${action}」`, `图层 ${JSON.stringify(inserted.before)} → ${JSON.stringify(inserted.after)}`);
  } else {
    await page.keyboard.press('Escape').catch(() => {});
  }
  await page.waitForTimeout(500);
  await closeFold();
  note(rows ? 'PASS' : 'FAIL', `详情打开（瓦片 #${index}）`, `按钮「${action}」 信息 ${rows.slice(0, 200)}`);
  record({ input: `detail #${index}${options['in-more'] ? ' in-more' : ''}`, summary: `button=${action} rows=${rows.slice(0, 300)}`, rows, action, inserted, file });
}

async function cmdMore(sid) {
  await openSession(sid);
  const before = await stripState();
  if (!before.folds.length) { note('INFO', '版本条没有「更多」分组', JSON.stringify(before.tiles.map((item) => item.key))); record({ input: 'more', summary: 'no fold', strip: before }); return; }
  await openFold(num(args.fold, 0));
  const list = foldList();
  const title = (await list.innerText()).split('\n')[0].trim();
  const keys = await list.locator('[data-testid=version-tile]').evaluateAll((items) => items.map((item) => item.dataset.key));
  const box = await list.boundingBox();
  const size = page.viewportSize();
  const inside = Boolean(box) && box.x >= 0 && box.y >= 0 && box.x + box.width <= size.width + 1 && box.y + box.height <= size.height + 1;
  const file = await shot('more');
  await closeFold();
  const expected = await text('ui.studio.strip.group_all_title');
  note(title === expected && keys.length >= 2 && inside ? 'PASS' : 'FAIL', '「更多」弹层', `标题「${title}」(期望「${expected}」) 条目 ${keys.length} 在视口内=${inside} 首个是代表图=${keys[0] === before.folds[0].visible[0]}`);
  record({ input: 'more', summary: `title=${title} items=${keys.length}`, keys, box, file });
}

async function branchItem(index) {
  const messages = tid('chat-message');
  const count = await messages.count();
  const message = messages.nth(index < 0 ? count + index : index);
  await message.scrollIntoViewIfNeeded().catch(() => {});
  await message.hover();
  await message.locator('[data-testid=chat-more]').first().click();
  const item = tid('chat-branch').first();
  await item.waitFor({ timeout: 5000 });
  const runningText = await text('ui.unavailable.reason.branch.running');
  return {
    item,
    disabled: (await item.getAttribute('aria-disabled')) === 'true' || (await item.getAttribute('data-disabled')) !== null,
    label: ((await item.getAttribute('aria-label')) || (await item.innerText())).trim(),
    runningHint: Boolean(runningText) && (await page.getByText(runningText).count()) > 0,
  };
}
async function cmdBranch(sid) {
  await openSession(sid);
  const index = num(args.message, -1);
  const unfinished = (await sessionTasks(sid)).filter((task) => !TERMINAL.test(task.state)).length;
  const branch = await branchItem(index);
  const file = await shot('branch-menu');
  const usable = !branch.disabled && !branch.runningHint;
  note(unfinished === 0 && !usable ? 'FAIL' : 'PASS', '「分支到新对话」状态', `未结束任务=${unfinished} 禁用=${branch.disabled} 尚未结束提示=${branch.runningHint} 标签「${branch.label}」`);
  let result = null;
  if (args.go && usable) {
    const source = await chatCounts();
    await branch.item.click();
    const moved = await waitFor(async () => {
      const current = new URL(page.url()).searchParams.get('session');
      return current && current !== sid ? current : null;
    }, 30, 500);
    await page.waitForTimeout(4000);
    const target = await chatCounts();
    const newFile = await shot('branch-new');
    result = { newSession: moved, source, target, file: newFile };
    note(moved ? 'PASS' : 'FAIL', '分支到新对话后进入新会话', `新会话 ${moved}；源 ${JSON.stringify(source)} 新 ${JSON.stringify(target)}`);
    if (moved) { state.sessions[`${sessionName}-branch-${stamp()}`] = moved; saveState(); }
  } else {
    await page.keyboard.press('Escape').catch(() => {});
  }
  record({ input: `branch message=${index}${args.go ? ' go' : ''}`, summary: `unfinished=${unfinished} disabled=${branch.disabled} hint=${branch.runningHint} ${result ? `new=${result.newSession}` : ''}`, branch: { disabled: branch.disabled, label: branch.label, runningHint: branch.runningHint }, result, file });
}

async function cmdBounce(sid, how) {
  await openSession(sid);
  const outcome = await bounce(sid, how);
  record({ input: how, summary: `lost=${outcome.lost} before=${JSON.stringify(outcome.before)} after=${JSON.stringify(outcome.after)}`, outcome });
}

async function cmdSkillCreate(sid, name, requirement, save) {
  await openSession(sid);
  await openSettings('skills');
  await page.getByRole('button', { name: await text('ui.workspace.skills.create'), exact: true }).click();
  const creator = tid('workspace-skill-creator');
  await creator.waitFor({ timeout: 10000 });
  await page.locator('#personal-skill-name').fill(name);
  await page.locator('#personal-skill-requirement').fill(requirement);
  const startedAt = Date.now();
  if (args.manual) {
    // The user writes the Skill without the draft (the button the form offers when drafting failed or is not wanted).
    await creator.getByRole('button', { name: await text('ui.workspace.skills.write_myself') }).click();
    await page.locator('#personal-skill-slug').waitFor({ timeout: 15000 });
    if (args.slug) await page.locator('#personal-skill-slug').fill(String(args.slug));
    if (args.description) await page.locator('#personal-skill-description').fill(String(args.description));
    await page.locator('#personal-skill-prompt').fill(String(args.prompt || requirement));
  } else {
    await tid('skill-generate').click();
    await page.locator('#personal-skill-slug').waitFor({ timeout: num(args.timeout, 240) * 1000 });
  }
  const draft = {
    seconds: Math.round((Date.now() - startedAt) / 1000),
    slug: await page.locator('#personal-skill-slug').inputValue(),
    description: await page.locator('#personal-skill-description').inputValue(),
    prompt: await page.locator('#personal-skill-prompt').inputValue(),
    apply: await page.locator('#personal-skill-apply').inputValue().catch(() => ''),
  };
  await shot('draft');
  console.log(`draft: ${JSON.stringify(draft).slice(0, 2000)}`);
  note(draft.slug && draft.description && draft.prompt ? 'PASS' : 'FAIL', 'Skill 草稿生成（标识、说明、正文）', `${draft.seconds}s slug=${draft.slug} apply=${draft.apply}`);
  let saved = null;
  if (save) {
    await creator.getByRole('button', { name: await text('ui.workspace.skills.save') }).click();
    await creator.waitFor({ state: 'detached', timeout: 20000 }).catch(() => {});
    const [, body] = await api('GET', '/api/v1/chat/skills/personal');
    saved = (body.skills || []).find((skill) => skill.name === name) || null;
    note(saved ? 'PASS' : 'FAIL', 'Skill 保存', saved ? `slug=${saved.slug} apply=${saved.apply}` : '列表里没有');
    await shot('saved');
  }
  await closeSettings();
  record({ input: `${name}: ${requirement}`, summary: `slug=${draft.slug} desc=${draft.description}`, draft, saved: saved ? { slug: saved.slug, description: saved.description, apply: saved.apply } : null });
}

async function cmdPickModel(sid, name, domain) {
  await openSession(sid);
  await openSettings('models');
  const rows = page.locator(`[data-testid=workspace-model-picker-${domain}] [role=menuitemradio]`);
  const names = await rows.evaluateAll((items) => items.map((item) => item.innerText.replace(/\s+/g, ' ').trim().slice(0, 80)));
  await rows.filter({ hasText: name }).first().click();
  await page.waitForTimeout(1500);
  const checked = await page.locator(`[data-testid=workspace-model-picker-${domain}] [role=menuitemradio][aria-checked=true]`).first().innerText().catch(() => '');
  await shot(`model-${domain}`);
  await closeSettings();
  note(checked.includes(name) ? 'PASS' : 'FAIL', `工作区 ${domain} 模型选为「${name}」`, `已选「${checked.replace(/\s+/g, ' ').slice(0, 80)}」；可选 ${JSON.stringify(names)}`);
  record({ input: `pick-model ${domain} ${name}`, summary: `checked=${checked.replace(/\s+/g, ' ').slice(0, 80)}`, options: names });
}

async function cmdPickChatModel(sid, name) {
  await openSession(sid);
  await openSettings('chat');
  const panel = tid('workspace-chat-model-picker-panel').first();
  const rows = panel.locator('[role=menuitemradio]');
  const names = await rows.evaluateAll((items) => items.map((item) => `${item.getAttribute('aria-checked') === 'true' ? '*' : ''}${item.innerText.replace(/\s+/g, ' ').trim().slice(0, 80)}`));
  const before = names.find((item) => item.startsWith('*')) || '';
  let after = before;
  if (name) {
    await rows.filter({ hasText: name }).first().click();
    await page.waitForTimeout(2000);
    after = await panel.locator('[role=menuitemradio][aria-checked=true]').first().innerText().catch(() => '');
  }
  const notice = (await tid('workspace-chat-model-notice').first().innerText().catch(() => '')).trim();
  await shot('chat-model');
  await closeSettings();
  if (name) note(after.includes(name) ? 'PASS' : 'FAIL', `对话模型选为「${name}」`, `之前「${before}」 之后「${after.replace(/\s+/g, ' ').slice(0, 80)}」`);
  console.log(`chat models: ${JSON.stringify(names)} notice="${notice}"`);
  record({ input: `pick-chat-model ${name || '(list)'}`, summary: `before=${before} after=${after.replace(/\s+/g, ' ').slice(0, 80)}`, options: names, notice });
}

async function cmdObserve(sid) {
  await openSession(sid);
  const counts = await chatCounts();
  const tasks = [];
  for (const task of await sessionTasks(sid)) tasks.push(await describeTask(task));
  const session = await sessionRecord(sid);
  const file = await shot('observe');
  console.log(`counts ${JSON.stringify(counts)} credits "${await sessionCredits()}" est=${session?.estimatedCredits} tasks: ${tasks.map((task) => `${task.capability}/${task.offering}:${task.state}${task.dims ? ` ${task.dims}` : ''}`).join(', ')}`);
  record({ input: 'observe', summary: `messages=${counts.messages} img=${counts.img} tasks=${tasks.length}`, counts, newTasks: tasks, credits: `${await sessionCredits()} est=${session?.estimatedCredits ?? '?'}`, file });
}

async function cmdAdmin() {
  const visited = [];
  for (const route of ['/admin', '/admin/users', '/admin/agent-configuration', '/admin/generations', '/admin/llm']) {
    await page.goto(base + route, { waitUntil: 'load' });
    await page.waitForTimeout(4000);
    const body = (await page.locator('main').first().innerText().catch(() => page.locator('body').innerText())).replace(/\s+/g, ' ').trim();
    const groups = route === '/admin' ? (await tid('admin-usage-groups').first().innerText().catch(() => '')).replace(/\s+/g, ' ').trim() : '';
    const total = route === '/admin' ? (await tid('admin-usage-total').first().innerText().catch(() => '')).replace(/\s+/g, ' ').trim() : '';
    const file = await shot(route.replace(/\//g, '_') || 'admin', true);
    visited.push({ route, chars: body.length, head: body.slice(0, 400), groups: groups.slice(0, 1200), total, file });
    const notFound = /页面未找到|无权|forbidden|not found/i.test(body.slice(0, 200));
    note(body.length > 20 && !notFound ? 'PASS' : 'FAIL', `管理端 ${route} 渲染`, `${body.length} 字；${body.slice(0, 120)}`);
  }
  // Agent configuration: the Skills part, when the page has a tab or heading for it (read only, nothing is saved).
  await page.goto(`${base}/admin/agent-configuration`, { waitUntil: 'load' });
  await page.waitForTimeout(3000);
  const skillsTab = page.getByRole('tab', { name: /Skill/i }).first();
  if (await skillsTab.count()) {
    await skillsTab.click();
    await page.waitForTimeout(2000);
    visited.push({ route: '/admin/agent-configuration#skills', head: (await page.locator('main').first().innerText().catch(() => '')).replace(/\s+/g, ' ').slice(0, 600), file: await shot('agent-config-skills', true) });
  }
  // Without a tab: list the page's section headings and the text of every section whose heading names Skill.
  const sections = await page.locator('main h2, main h3').evaluateAll((nodes) => nodes.map((node) => {
    const box = node.closest('section') || node.parentElement;
    return { heading: node.innerText.trim(), text: (box?.innerText || '').replace(/\s+/g, ' ').slice(0, 900) };
  })).catch(() => []);
  for (const section of sections.filter((item) => /skill/i.test(item.heading))) {
    await page.getByRole('heading', { name: section.heading }).first().scrollIntoViewIfNeeded().catch(() => {});
    section.file = await shot(`agent-config-${section.heading.replace(/\W+/g, '-').slice(0, 20)}`);
    note('INFO', `Agent 管理「${section.heading}」`, section.text.slice(0, 400));
  }
  visited.push({ route: '/admin/agent-configuration#sections', head: sections.map((item) => item.heading).join(' | ').slice(0, 600), sections: sections.filter((item) => /skill/i.test(item.heading)) });
  const [usageStatus, usage] = await api('GET', '/api/admin/usage');
  console.log(`admin usage HTTP ${usageStatus}: ${JSON.stringify(usage).slice(0, 1500)}`);
  record({ input: 'admin (read only)', summary: visited.map((item) => `${item.route}:${item.chars ?? ''}`).join(' '), visited, usage: { status: usageStatus, body: JSON.stringify(usage).slice(0, 4000) } });
}

async function cmdBalances(label) {
  const [status, body] = await api('GET', '/api/meshy/balance');
  const accounts = {};
  for (const account of body.accounts || []) accounts[account.provider] = account.native_balance?.amount ?? account.balance ?? null;
  state.balances[label] = { at: new Date().toISOString(), status, accounts };
  saveState();
  console.log(`balances[${label}] HTTP ${status}: ${JSON.stringify(accounts)}`);
  record({ input: `balances ${label}`, summary: JSON.stringify(accounts) });
  return accounts;
}

async function cmdCoverage() {
  const since = String(args.since || state.startedAt || new Date(Date.now() - 86400000).toISOString());
  const [, body] = await api('GET', '/api/v1/offerings');
  const offerings = body.offerings || body.data || body.items || (Array.isArray(body) ? body : []);
  const tasks = [];
  let cursor = '';
  for (let pageIndex = 0; pageIndex < 50; pageIndex += 1) {
    const [, list] = await api('GET', `/api/v1/tasks?limit=100${cursor ? `&cursor=${encodeURIComponent(cursor)}` : ''}`);
    const items = list.tasks || list.data || [];
    tasks.push(...items.filter((task) => String(task.created_at) >= since));
    if (!list.next_cursor || !items.length || items.some((task) => String(task.created_at) < since)) break;
    cursor = list.next_cursor;
  }
  const table = coverageTable(offerings, tasks, new Set(Object.values(state.sessions || {})));
  fs.writeFileSync(path.join(OUT, 'coverage.json'), `${JSON.stringify({ since, offerings: offerings.length, tasks: tasks.length, ...table }, null, 2)}\n`);
  const markdown = coverageMarkdown(table, since);
  fs.writeFileSync(path.join(OUT, 'coverage.md'), markdown);
  console.log(markdown);
  record({ input: `coverage since ${since}`, summary: `${table.succeeded}/${table.total} (${(table.ratio * 100).toFixed(1)}%) from ${tasks.length} tasks`, byProvider: table.byProvider });
  return table;
}

// ---------------------------------------------------------------- dry run on the fake (selector check, zero cost)
async function dryRun() {
  const steps = [];
  const step = async (name, body) => {
    const before = allFindings.length;
    try { await body(); steps.push({ name, ok: !allFindings.slice(before).some((item) => item.status === 'FAIL') }); } catch (error) {
      await shot('error');
      note('FAIL', `dry-run ${name}`, error.message.split('\n')[0]);
      steps.push({ name, ok: false });
    }
  };
  const marker = (tool, payload) => `[[fake:tool:${tool} ${JSON.stringify(payload)}]]`;
  let sid = '';
  await step('balances start', () => cmdBalances('dry-start'));
  await step('init', async () => { sid = await cmdInit(`prod-long dry-run ${stamp()}`); });
  // An empty session's first picture through the API, as a seed for the image stage.
  await step('seed picture', async () => {
    const [status] = await api('POST', `/api/chat/sessions/${sid}/messages`, { content: `一把长剑图标 ${marker('refine_image', { prompt: 'a long sword icon, plain background' })}`, creative_domain: 'image' });
    if (![200, 201, 202].includes(status)) throw new Error(`seed POST ${status}`);
    if (!await waitFor(async () => (await sessionTasks(sid)).some((task) => task.state === 'SUCCEEDED'), 120, 1500)) throw new Error('seed picture did not succeed');
  });
  await step('say: three edits of @img1 in one turn', () => cmdSay(sid, `再来三张 ${[1, 2, 3].map((index) => marker('refine_image', { prompt: `variant ${index} of the sword icon`, reference_handles: ['@img1'] })).join(' ')}`, { timeout: 240, sample: 5 }));
  await step('strip', () => cmdStrip(sid));
  await step('more', () => cmdMore(sid));
  await step('tile click 0', () => cmdTile(sid, 0, {}));
  await step('tile click in more #2', () => cmdTile(sid, 2, { 'in-more': true }));
  await step('tile double in more #1', () => cmdTile(sid, 1, { 'in-more': true, double: true }));
  await step('detail #0 insert', () => cmdDetail(sid, 0, { insert: true }));
  await step('say: crop @img1', () => cmdSay(sid, `把这张裁成左右两半 ${marker('crop_image_regions', { image: '@img1', regions: [{ label: 'left', region: { x: 0, y: 0, width: 0.5, height: 1 } }, { label: 'right', region: { x: 0.5, y: 0, width: 0.5, height: 1 } }] })}`, { timeout: 180, sample: 5, 'reload-at': 3 }));
  await step('reload-check', () => cmdBounce(sid, 'reload'));
  await step('branch (state only)', () => cmdBranch(sid));
  await step('skill-create (fake draft)', () => cmdSkillCreate(sid, `dry-${stamp()}`, `图标统一风格：金属质感、深色描边 [[fake:skill-draft ${JSON.stringify({ slug: `dry-${Date.now()}`, description: 'dry-run 草稿说明' })}]]`, true));
  await step('say with hand-picked skill 四视图 (no picture)', () => cmdSay(sid, '我要一把剑的四视图', { skill: '四视图', timeout: 180, sample: 5 }));
  await step('say: 3D with approval (fake, free)', () => cmdSay(sid, `把第一张转成 3D ${marker('create_model_3d', { prompt: 'a long sword' })}`, { 'on-approval': 'approve:1000', timeout: 300, sample: 5, 'switch-at': 8 }));
  await step('watch', () => cmdWatch(sid));
  await step('observe', () => cmdObserve(sid));
  await step('admin', () => cmdAdmin());
  await step('balances end', () => cmdBalances('dry-end'));
  await step('coverage', () => cmdCoverage());
  writeReport();
  console.log(`dry-run steps: ${steps.map((item) => `${item.ok ? 'ok' : 'FAIL'} ${item.name}`).join('; ')}`);
  console.log(`PASS=${allFindings.filter((item) => item.status === 'PASS').length} FAIL=${allFindings.filter((item) => item.status === 'FAIL').length} ALERT=${allFindings.filter((item) => item.status === 'ALERT').length}`);
}

// ---------------------------------------------------------------- run
await login();
try {
  switch (command) {
    case 'init': await cmdInit(String(args.title || `prod-long ${stamp()}`)); break;
    case 'say': await cmdSay(await currentSession(), String(args.text || refuse('say needs --text')), args); break;
    case 'click': await cmdSay(await currentSession(), `[点按钮] ${args.button}`, { ...args, clickButton: String(args.button || refuse('click needs --button')) }); break;
    case 'choose': await cmdSay(await currentSession(), `[选择卡] ${args.option}`, { ...args, choose: String(args.option || refuse('choose needs --option')) }); break;
    case 'approve': await cmdApprove(await currentSession(), 'approve'); break;
    case 'deny': await cmdApprove(await currentSession(), 'deny'); break;
    case 'watch': await cmdWatch(await currentSession()); break;
    case 'strip': await cmdStrip(await currentSession()); break;
    case 'tile': await cmdTile(await currentSession(), num(args.index, 0), args); break;
    case 'detail': await cmdDetail(await currentSession(), num(args.index, 0), args); break;
    case 'more': await cmdMore(await currentSession()); break;
    case 'branch': await cmdBranch(await currentSession()); break;
    case 'reload-check': await cmdBounce(await currentSession(), 'reload'); break;
    case 'switch-check': await cmdBounce(await currentSession(), 'switch'); break;
    case 'skill-create': await cmdSkillCreate(await currentSession(), String(args.name), String(args.requirement), Boolean(args.save)); break;
    case 'pick-model': await cmdPickModel(await currentSession(), String(args.name), String(args.domain || 'image')); break;
    case 'pick-chat-model': await cmdPickChatModel(await currentSession(), args.name ? String(args.name) : ''); break;
    case 'task': {
      const [status, body] = await api('GET', `/api/v1/tasks/${args.id}`);
      console.log(`task HTTP ${status}: ${redactText(JSON.stringify(body), 6000)}`);
      break;
    }
    case 'asset-check': {
      // 只读：逐个下载任务产物，核对字节数与 sha256 是否与任务投影一致。
      const [status, body] = await api('GET', `/api/v1/tasks/${args.id}`);
      if (status !== 200) { note('FAIL', `读取任务 ${args.id}`, `HTTP ${status}`); break; }
      for (const asset of body.assets || []) {
        const response = await ctx.request.fetch(base + asset.url, { method: 'GET', headers: ORIGIN });
        const bytes = response.status() === 200 ? await response.body() : Buffer.alloc(0);
        const digest = crypto.createHash('sha256').update(bytes).digest('hex');
        const ok = response.status() === 200 && bytes.length === asset.size && digest === asset.sha256;
        note(ok ? 'PASS' : 'FAIL', `产物 ${asset.variant || asset.role} 下载`, `HTTP ${response.status()} ${bytes.length}/${asset.size} B sha256 ${ok ? '一致' : `不一致 ${digest.slice(0, 12)}`} ${response.headers()['content-type'] || ''}`);
      }
      record({ input: `asset-check ${args.id}`, summary: `${body.state} ${body.offering_id} assets=${(body.assets || []).length}` });
      break;
    }
    case 'observe': await cmdObserve(await currentSession()); break;
    case 'admin': await cmdAdmin(); break;
    case 'balances': await cmdBalances(String(args.label || stamp())); break;
    case 'coverage': await cmdCoverage(); break;
    case 'dry-run': await dryRun(); break;
    default: break;
  }
} catch (error) {
  await shot('error');
  note('FAIL', command, error.message.split('\n')[0]);
  record({ input: process.argv.slice(3).join(' ').slice(0, 300), summary: `error: ${error.message.split('\n')[0]}` });
}
if (netErrors.length) console.log(`HTTP errors: ${netErrors.map((item) => `${item.status} ${item.method} ${item.path}${item.requestId ? ` rid=${item.requestId}` : ''}`).join('; ')}`);
if (consoleErrors.length || pageErrors.length) console.log(`console errors: ${[...consoleErrors, ...pageErrors].slice(0, 10).join(' | ')}`);
await ctx.close();
process.exit(exitCode);
