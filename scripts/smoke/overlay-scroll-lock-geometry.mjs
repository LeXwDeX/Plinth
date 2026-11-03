// 弹层滚动锁几何回归：真实浏览器、零费用、不提交任务。
//
// 工作台右栏「功能」选「绑骨」后打开「执行服务」下拉，从点击前到弹层稳定
// 打开、再到关闭，在每次 DOM 变化与每一帧采样 document.documentElement、
// body 与工作台根容器（studio-shell）的宽度和 scrollLeft，以及 <html> 的
// 溢出量与内联样式。断言：
//   - 三者宽度、左边界与 scrollLeft 在打开前后和打开过程中都不变；
//   - 打开过程中 <html> 从不溢出（scrollWidth/scrollHeight 不超过 client 尺寸）；
//   - <html> 没有被写入 scrollbar-gutter（Base UI 滚动锁只在 <html> 当时
//     有经典滚动条时才写，写了就会在右侧留下一条空白竖条）；
//   - 下拉定位层是 position: fixed。
// 后两条针对根因：首次打开时 absolute 定位层可能短暂越出视口，让文档溢出；
// 滚动锁恰好在这一瞬间采样时就会锁住 gutter。这是时序竞态，无头 Chromium
// 不一定撞上，且其滚动条不占宽度，所以宽度断言单独抓不到回归；定位层
// position 是确定性断言，旧实现（absolute）每次都会 FAIL。
// 只保存「功能」草稿状态，不点保存、不发消息、不调用模型。
//
// 用法（本地或服务器）：
//   BASE=<环境 base_url> ADMIN_USERNAME=... ADMIN_PASSWORD=... \
//   [SMOKE_EVIDENCE_DIR=/abs/path] [ROUNDS=5] node scripts/smoke/overlay-scroll-lock-geometry.mjs
//
// 凭据只用于本进程内的登录请求，不写日志、不写报告。
import { createRequire } from 'node:module';
import { homedir } from 'node:os';
import fs from 'node:fs/promises';
import path from 'node:path';

const require = createRequire(import.meta.url);

const base = (process.env.BASE || 'http://127.0.0.1:13333').replace(/\/+$/, '');
const username = process.env.ADMIN_USERNAME || '';
const password = process.env.ADMIN_PASSWORD || '';
const browserName = (process.env.SMOKE_BROWSERS || 'chromium').split(',')[0].trim();
const evidenceDir = process.env.SMOKE_EVIDENCE_DIR || '';
const rounds = Math.max(1, Number(process.env.ROUNDS || 5));
const TOLERANCE = 0.5;

// playwright 不进 package.json 依赖：与 L0 一致的解析顺序。
function resolvePlaywright() {
  const candidates = [];
  if (process.env.PLAYWRIGHT_MODULE_PATH) candidates.push(process.env.PLAYWRIGHT_MODULE_PATH);
  try {
    candidates.push(require.resolve('playwright'));
  } catch {}
  candidates.push(path.join(homedir(), '.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright'));
  for (const candidate of candidates) {
    try {
      return require(candidate);
    } catch {}
  }
  throw new Error('未找到 playwright：请安装浏览器驱动或设置 PLAYWRIGHT_MODULE_PATH');
}

let pass = 0;
let fail = 0;
const ok = (name, detail) => {
  console.log(`PASS: ${name}${detail ? ` — ${detail}` : ''}`);
  pass += 1;
};
const bad = (name, detail) => {
  console.log(`FAIL: ${name}${detail ? ` — ${detail}` : ''}`);
  fail += 1;
};

// Records every DOM mutation and animation frame until stopped, so a
// transient overflow between two paints is still caught.
const startProbe = (page) => page.evaluate(() => {
  const html = document.documentElement;
  const shell = document.querySelector('[data-testid="studio-shell"]');
  const sample = (source) => {
    const rect = shell.getBoundingClientRect();
    return {
      source,
      htmlWidth: html.clientWidth,
      bodyWidth: document.body.clientWidth,
      shellWidth: rect.width,
      shellLeft: rect.left,
      htmlScrollLeft: html.scrollLeft,
      bodyScrollLeft: document.body.scrollLeft,
      shellScrollLeft: shell.scrollLeft,
      overflowX: html.scrollWidth - html.clientWidth,
      overflowY: html.scrollHeight - html.clientHeight,
      gutter: html.style.scrollbarGutter,
    };
  };
  const probe = { samples: [sample('baseline')], running: true };
  const observer = new MutationObserver(() => probe.samples.push(sample('mutation')));
  observer.observe(html, { subtree: true, childList: true, attributes: true, attributeFilter: ['style', 'class'] });
  const frame = () => {
    if (!probe.running) return;
    probe.samples.push(sample('frame'));
    requestAnimationFrame(frame);
  };
  requestAnimationFrame(frame);
  probe.stop = () => {
    probe.running = false;
    observer.disconnect();
    probe.samples.push(sample('final'));
    return probe.samples;
  };
  window.__overlayProbe = probe;
});

const stopProbe = (page) => page.evaluate(() => window.__overlayProbe.stop());

const check = (tag, samples) => {
  const baseline = samples[0];
  const keys = ['htmlWidth', 'bodyWidth', 'shellWidth', 'shellLeft', 'htmlScrollLeft', 'bodyScrollLeft', 'shellScrollLeft'];
  const drift = keys.map((key) => ({
    key,
    max: Math.max(...samples.map((item) => Math.abs(item[key] - baseline[key]))),
  })).filter((item) => item.max > TOLERANCE);
  const overflow = samples.filter((item) => item.overflowX > 0 || item.overflowY > 0);
  const gutter = samples.filter((item) => item.gutter);
  const detail = `${samples.length} 个采样`;
  if (drift.length) bad(`${tag} 宽度与 scrollLeft 不变`, JSON.stringify(drift));
  else ok(`${tag} 宽度与 scrollLeft 不变`, detail);
  if (overflow.length) bad(`${tag} 打开过程 <html> 不溢出`, JSON.stringify(overflow.slice(0, 3)));
  else ok(`${tag} 打开过程 <html> 不溢出`, detail);
  if (gutter.length) bad(`${tag} <html> 未写入 scrollbar-gutter`, JSON.stringify(gutter.slice(0, 1)));
  else ok(`${tag} <html> 未写入 scrollbar-gutter`, detail);
  return { drift, overflow: overflow.length, gutter: gutter.length };
};

const playwright = resolvePlaywright();
if (!username || !password) throw new Error('需要 ADMIN_USERNAME / ADMIN_PASSWORD 登录测试账号');
if (evidenceDir) await fs.mkdir(evidenceDir, { recursive: true, mode: 0o700 });

let browser;
try {
  browser = await playwright[browserName].launch({ headless: true });
  console.log(`INFO: ${browserName} ${browser.version()} · ${base}`);

  for (const [width, height] of [[1440, 900], [1280, 720], [1024, 768]]) {
    const tag = `${width}x${height}`;
    const context = await browser.newContext({ viewport: { width, height }, serviceWorkers: 'block' });
    try {
      const login = await context.request.post(base + '/api/auth/login', { data: { username, password } });
      if (!login.ok()) throw new Error(`登录失败：HTTP ${login.status()}`);
      const page = await context.newPage();
      const summaries = [];
      // The race only shows on a select's first open after load, so each
      // round reloads. Opening `/` starts a new conversation and then
      // remounts the workbench on `?session=`; later rounds reuse that one.
      let sessionUrl = base + '/';
      for (let round = 1; round <= rounds; round += 1) {
        await page.goto(sessionUrl, { waitUntil: 'domcontentloaded' });
        await page.waitForURL(/[?&]session=/, { timeout: 30000 });
        sessionUrl = page.url();
        await page.locator('[data-testid="studio-shell"]').waitFor({ timeout: 30000 });
        await page.waitForTimeout(1000);
        // The panel remembers its last tab, and clicking the open tab folds it.
        const tab = page.locator('#right-panel-tab-operations');
        await tab.waitFor({ timeout: 20000 });
        if (await tab.getAttribute('aria-selected') !== 'true') await tab.click();
        const panel = page.locator('[data-testid="harness-panel"]');
        await panel.locator('input[type="radio"][value="rigging"]').click({ timeout: 20000 });
        // The trigger stays disabled while the harness draft is saving.
        const trigger = panel.locator('[role="combobox"]:not([data-disabled])').first();
        await trigger.waitFor({ state: 'visible', timeout: 20000 });
        await trigger.scrollIntoViewIfNeeded();
        await startProbe(page);
        await trigger.click();
        await page.locator('[role="listbox"]').waitFor({ state: 'visible', timeout: 10000 });
        await page.waitForTimeout(400);
        // The race above depends on timing, so also pin its cause: an
        // absolutely positioned popup can extend the document, a fixed one cannot.
        const position = await page.locator('[role="listbox"]').evaluate((element) => {
          let node = element;
          while (node && node !== document.body && !node.style.position) node = node.parentElement;
          return node && node !== document.body ? getComputedStyle(node).position : '';
        });
        if (position === 'fixed') ok(`${tag} 第 ${round} 次 下拉定位层 position`, position);
        else bad(`${tag} 第 ${round} 次 下拉定位层 position`, `期望 fixed，实际 ${position || '未找到'}`);
        if (evidenceDir && round === 1) {
          await page.screenshot({ path: path.join(evidenceDir, `overlay-scroll-lock-${tag}-open.png`) });
        }
        await page.keyboard.press('Escape');
        await page.locator('[role="listbox"]').waitFor({ state: 'hidden', timeout: 10000 });
        await page.waitForTimeout(200);
        const samples = await stopProbe(page);
        summaries.push({ round, ...check(`${tag} 第 ${round} 次`, samples) });
        if (evidenceDir) {
          await fs.writeFile(path.join(evidenceDir, `overlay-scroll-lock-${tag}-${round}.json`), JSON.stringify(samples, null, 2));
        }
      }
      console.log(`INFO: ${tag} ${JSON.stringify(summaries.map(({ round, overflow, gutter }) => ({ round, overflow, gutter })))}`);
    } catch (error) {
      bad(`${tag} 验收`, error.message);
    } finally {
      await context.close();
    }
  }
} finally {
  await browser?.close();
}

console.log(`\nRESULT: ${fail ? `FAIL (${fail})` : 'PASS'} — PASS=${pass} FAIL=${fail}`);
process.exit(fail ? 1 : 0);
