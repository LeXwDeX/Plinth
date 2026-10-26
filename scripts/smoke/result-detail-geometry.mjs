// 详情弹窗几何回归（#224）：真实浏览器、零费用、只读。
//
// 打开“我的资源”里一个多候选任务（同一任务有多个可预览资产），在
// 1440×900、1280×720 与 390×844 下逐个翻页，记录
// result-detail-prev / result-detail-next、预览框与弹窗的
// getBoundingClientRect()，断言翻页按钮与弹窗的位置和尺寸变化 ≤ 1px，
// 并核对预览框始终是固定高度（有预览 min(42dvh,24rem)，无预览 6rem）。
// 另打开一个单图任务，延迟图片响应后测图片加载前后的弹窗高度。
//
// 用法（本地或服务器）：
//   BASE=<环境 base_url，见 scripts/release-env.py show dev-test> \
//   ADMIN_USERNAME=... ADMIN_PASSWORD=... \
//   SMOKE_EVIDENCE_DIR=/abs/path node scripts/smoke/result-detail-geometry.mjs
//
// 凭据只用于本进程内的登录请求，不写日志、不写报告。脚本不创建任务、
// 不调用模型、不改变任何数据。
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
const MAX_ROW_PROBES = 20;
const TOLERANCE = 1;

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
const failures = [];
const ok = (name, detail) => {
  console.log(`PASS: ${name}${detail ? ` — ${detail}` : ''}`);
  pass += 1;
};
const bad = (name, detail) => {
  const line = `${name}${detail ? ` — ${detail}` : ''}`;
  console.log(`FAIL: ${line}`);
  failures.push(line);
  fail += 1;
};

const measure = (page) => page.evaluate(() => {
  const box = (query) => {
    const element = document.querySelector(query);
    if (!element) return null;
    const rect = element.getBoundingClientRect();
    return { x: rect.x, y: rect.y, width: rect.width, height: rect.height };
  };
  const body = document.querySelector('[data-testid="result-detail-body"]');
  const preview = document.querySelector('[data-testid="result-detail-preview"]');
  const image = preview?.querySelector('img');
  return {
    prev: box('[data-testid="result-detail-prev"]'),
    next: box('[data-testid="result-detail-next"]'),
    dialog: box('[data-testid="result-detail-dialog"]'),
    preview: box('[data-testid="result-detail-preview"]'),
    position: document.querySelector('[data-testid="result-detail-position"]')?.textContent?.trim() ?? '',
    kind: preview?.getAttribute('data-kind') ?? '',
    image: image ? {
      complete: image.complete,
      naturalWidth: image.naturalWidth,
      naturalHeight: image.naturalHeight,
    } : null,
    body: body ? { scrollTop: body.scrollTop, scrollHeight: body.scrollHeight, clientHeight: body.clientHeight } : null,
  };
});

// The dialog animates in (zoom-in). A baseline taken mid-animation moves by
// tens of pixels when compared with the settled frame, so wait until the
// dialog rect stops changing before any geometry is recorded.
const waitForStableDialog = (page) => page.evaluate(async () => {
  const rectOf = () => {
    const element = document.querySelector('[data-testid="result-detail-dialog"]');
    if (!element) return null;
    const rect = element.getBoundingClientRect();
    return { x: rect.x, y: rect.y, width: rect.width, height: rect.height };
  };
  const same = (a, b) => Math.abs(a.x - b.x) <= 0.5 && Math.abs(a.y - b.y) <= 0.5
    && Math.abs(a.width - b.width) <= 0.5 && Math.abs(a.height - b.height) <= 0.5;
  let last = rectOf();
  for (let attempt = 0; attempt < 40; attempt += 1) {
    await new Promise((resolve) => setTimeout(resolve, 100));
    const current = rectOf();
    if (last && current && same(last, current)) return true;
    last = current;
  }
  return false;
});

const settle = (page) => page.evaluate(async () => {
  const preview = document.querySelector('[data-testid="result-detail-preview"]');
  const image = preview?.querySelector('img');
  if (image) {
    try { await image.decode(); } catch {}
  }
  const video = preview?.querySelector('video');
  if (video && video.readyState < 1) {
    await new Promise((resolve) => {
      const timer = setTimeout(resolve, 5000);
      video.addEventListener('loadedmetadata', () => { clearTimeout(timer); resolve(); }, { once: true });
    });
  }
});

const delta = (a, b) => a && b ? Math.max(
  Math.abs(a.x - b.x), Math.abs(a.y - b.y),
  Math.abs(a.width - b.width), Math.abs(a.height - b.height),
) : Infinity;

const positionOf = (text) => {
  const match = /(\d+)\s*\/\s*(\d+)/.exec(text);
  return match ? { index: Number(match[1]), count: Number(match[2]) } : null;
};

const waitForPosition = async (page, previous) => {
  await page.waitForFunction((last) =>
    document.querySelector('[data-testid="result-detail-position"]')?.textContent?.trim() !== last,
  previous, { timeout: 15000 });
  await settle(page);
};

const compare = (label, baseline, current, keys) => {
  for (const key of keys) {
    const change = delta(baseline[key], current[key]);
    if (change > TOLERANCE) {
      bad(`${label} ${key}`, `移动/缩放 ${change.toFixed(2)}px（基线 ${JSON.stringify(baseline[key])}，当前 ${JSON.stringify(current[key])}）`);
    }
  }
};

// The preview is one of two fixed frames: min(42dvh,24rem) with a preview and
// 6rem without one. Both must be steady across candidates of the same kind.
const fixedPreviewHeight = (viewportHeight) => Math.min(viewportHeight * 0.42, 384);
const NO_PREVIEW_HEIGHT = 96;
const previewFrameOk = (preview, kind, viewportHeight) => {
  if (!preview) return false;
  const expected = kind === 'none' ? NO_PREVIEW_HEIGHT : fixedPreviewHeight(viewportHeight);
  return Math.abs(preview.height - expected) <= TOLERANCE;
};

const playwright = resolvePlaywright();
if (!username || !password) throw new Error('需要 ADMIN_USERNAME / ADMIN_PASSWORD 登录真实账号');
if (evidenceDir) await fs.mkdir(evidenceDir, { recursive: true, mode: 0o700 });

const imageCases = [];

let browser;
try {
  browser = await playwright[browserName].launch({ headless: true });
  console.log(`INFO: ${browserName} ${browser.version()} · ${base}`);

  for (const [width, height] of [[1440, 900], [1280, 720], [390, 844]]) {
    const tag = `${width}x${height}`;
    const context = await browser.newContext({ viewport: { width, height }, serviceWorkers: 'block' });
    const login = await context.request.post(base + '/api/auth/login', { data: { username, password } });
    if (!login.ok()) throw new Error(`登录失败：HTTP ${login.status()}`);
    const schemaResponse = await context.request.get(base + '/api/v1/schema');
    if (!schemaResponse.ok()) throw new Error(`Schema 不可读：HTTP ${schemaResponse.status()}`);
    const schemaPayload = await schemaResponse.json();
    const projected = schemaPayload.schema?.application ?? schemaPayload.schema ?? schemaPayload.application ?? {};
    const previousKey = projected.chat?.frontend?.lightbox_previous_key;
    const nextKey = projected.chat?.frontend?.lightbox_next_key;
    if (!previousKey || !nextKey) throw new Error('公开 Schema 没有声明详情翻页键');
    const page = await context.newPage();
    page.setDefaultTimeout(30000);
    const errors = [];
    page.on('pageerror', (error) => errors.push(error.message));
    try {
      await page.goto(base + '/account', { waitUntil: 'domcontentloaded' });
      const rows = page.getByTestId('account-task-open');
      await rows.first().waitFor();
      const rowCount = await rows.count();

      // 逐个打开任务，同时找多候选分组和单图任务。扫描不提前结束：找到分组后
      // 仍要继续记录单图任务，否则图片加载前后的高度检查会被静默跳过。
      let opened = -1;
      let imageRow = -1;
      for (let index = 0; index < Math.min(rowCount, MAX_ROW_PROBES); index += 1) {
        await rows.nth(index).click();
        const probeDialog = page.getByTestId('result-detail-dialog');
        await probeDialog.waitFor();
        await waitForStableDialog(page);
        const probe = await measure(page);
        const hasGroup = Boolean(await page.getByTestId('result-detail-group').count());
        if (opened < 0 && hasGroup) opened = index;
        if (imageRow < 0 && probe.kind === 'image' && !hasGroup) imageRow = index;
        await page.keyboard.press('Escape');
        await probeDialog.waitFor({ state: 'hidden' });
      }
      if (opened < 0) throw new Error(`前 ${MAX_ROW_PROBES} 个任务里没有多候选结果`);

      // 重新打开多候选任务；基线只在弹窗动画结束后采集。
      await rows.nth(opened).click();
      const dialog = page.getByTestId('result-detail-dialog');
      await dialog.waitFor();
      const stable = await waitForStableDialog(page);
      if (!stable) bad(`${tag} 弹窗动画结束`, '矩形在 4s 内仍在变化');
      await settle(page);
      const first = await measure(page);
      const position = positionOf(first.position);
      if (!position || position.count < 2) throw new Error(`翻页位置不可读：${JSON.stringify(first.position)}`);
      if (!first.prev || !first.next || !first.dialog || !first.preview) throw new Error('详情结构缺少翻页按钮、弹窗或预览框');
      if (!previewFrameOk(first.preview, first.kind, height)) {
        bad(`${tag} 预览框固定尺寸`, `${first.kind} ${JSON.stringify(first.preview)}`);
      } else {
        ok(`${tag} 预览框固定尺寸`, `${first.kind} ${first.preview.height.toFixed(1)}px`);
      }
      ok(`${tag} 打开多候选详情`, `${position.count} 个候选，第 ${opened + 1} 行，预览 ${first.kind}`);

      const baseline = first;
      const seen = [{ label: 'first', index: position.index, ...first }];
      const shot = async (name) => {
        if (!evidenceDir) return;
        await page.screenshot({ path: path.join(evidenceDir, `result-detail-${tag}-${name}.png`) });
      };
      await shot('page-1');

      // 验收框 1：连续点击“下一个”到末尾。
      let cursor = first;
      for (let index = position.index; index < position.count; index += 1) {
        const enabled = await page.getByTestId('result-detail-next').isEnabled();
        if (!enabled) break;
        await page.getByTestId('result-detail-next').click();
        await waitForPosition(page, cursor.position);
        cursor = await measure(page);
        const at = positionOf(cursor.position);
        seen.push({ label: `click ${at ? at.index : 'unknown'}`, ...cursor });
        await shot(`click-${at ? at.index : 'unknown'}`);
        compare(`${tag} 点击下一个`, baseline, cursor, ['prev', 'next', 'dialog']);
        if (!previewFrameOk(cursor.preview, cursor.kind, height)) {
          bad(`${tag} 翻页后预览框固定尺寸`, `${cursor.kind} ${JSON.stringify(cursor.preview)}`);
        }
      }
      const last = positionOf(cursor.position);
      if (last && last.index === last.count) {
        ok(`${tag} 点击“下一个”到末尾`, `第 ${last.index} / ${last.count} 个`);
      } else {
        bad(`${tag} 点击“下一个”到末尾`, JSON.stringify(cursor.position));
      }
      const lastPage = cursor;
      const kinds = [...new Set(seen.map((item) => item.kind))];

      // 验收框 1：按公开 Schema 声明的上一个键回到开头，再按下一个键前进一页。
      for (let index = last ? last.index : position.count; index > 1; index -= 1) {
        await page.keyboard.press(previousKey);
        await waitForPosition(page, cursor.position);
        cursor = await measure(page);
        seen.push({ label: `key ${positionOf(cursor.position)?.index ?? 'unknown'}`, ...cursor });
        compare(`${tag} ${previousKey} 返回`, baseline, cursor, ['prev', 'next', 'dialog']);
      }
      const backHome = positionOf(cursor.position);
      if (backHome?.index === 1) {
        ok(`${tag} ${previousKey} 回到第一个`, cursor.position);
      } else {
        bad(`${tag} ${previousKey} 回到第一个`, JSON.stringify(cursor.position));
      }
      if (position.count > 1) {
        await page.keyboard.press(nextKey);
        await waitForPosition(page, cursor.position);
        cursor = await measure(page);
        seen.push({ label: `key ${positionOf(cursor.position)?.index ?? 'unknown'}`, ...cursor });
        compare(`${tag} ${nextKey} 前进`, baseline, cursor, ['prev', 'next', 'dialog']);
        const forward = positionOf(cursor.position);
        if (forward?.index === 2) {
          ok(`${tag} ${nextKey} 前进一页`, cursor.position);
        } else {
          bad(`${tag} ${nextKey} 前进一页`, JSON.stringify(cursor.position));
        }
      }

      // 验收框 3：滚动到底再回到顶部，翻页行、预览与弹窗都不移动。
      const scrolled = await page.evaluate(() => {
        const body = document.querySelector('[data-testid="result-detail-body"]');
        if (!body) return null;
        const overflow = body.scrollHeight - body.clientHeight;
        body.scrollTop = body.scrollHeight;
        return {
          overflow,
          scrollTop: body.scrollTop,
          scrollHeight: body.scrollHeight,
          clientHeight: body.clientHeight,
        };
      });
      const afterScroll = await measure(page);
      compare(`${tag} 滚动到底`, baseline, afterScroll, ['prev', 'next', 'dialog']);
      await page.evaluate(() => {
        const body = document.querySelector('[data-testid="result-detail-body"]');
        if (body) body.scrollTop = 0;
      });
      const afterBack = await measure(page);
      compare(`${tag} 滚回顶部`, baseline, afterBack, ['prev', 'next', 'dialog']);
      if (scrolled && scrolled.overflow > 0) {
        ok(`${tag} 内容在固定框内滚动`, `可滚动 ${scrolled.overflow}px；滚到底与回到顶部时翻页行不动`);
      } else {
        ok(`${tag} 内容未超出固定框`, '当前页无需滚动；翻页行位置仍与基线一致');
      }
      if (width <= 390) {
        const dialogBottom = first.dialog.y + first.dialog.height;
        const usable = Boolean(afterBack.body) && afterBack.body.clientHeight >= 96;
        if (dialogBottom <= height + TOLERANCE && usable) {
          ok(`${tag} 小视口可用`, `弹窗 ${first.dialog.height.toFixed(1)}px 高，滚动区 ${afterBack.body.clientHeight}px`);
        } else {
          bad(`${tag} 小视口可用`, JSON.stringify({ dialog: first.dialog, body: afterBack.body }));
        }
      }

      const summary = {
        viewport: tag,
        task_row: opened,
        candidates: position.count,
        kinds,
        dialog: baseline.dialog,
        preview: baseline.preview,
        prev: baseline.prev,
        next: baseline.next,
        last_page: lastPage.position,
        scrolling: scrolled,
        page_errors: errors,
      };
      console.log(`INFO: ${tag} ${JSON.stringify(summary)}`);
      if (evidenceDir) {
        await fs.writeFile(path.join(evidenceDir, `result-detail-${tag}.json`), JSON.stringify({ summary, samples: seen }, null, 2));
      }
      if (errors.length) bad(`${tag} 页面错误`, errors.join(' | '));
      if (seen.some((item) => !item.prev || !item.next)) bad(`${tag} 翻页按钮缺失`, '');
      await page.keyboard.press('Escape');
      await dialog.waitFor({ state: 'hidden' });

      // 验收框 2/4：单图任务（含上传资源使用的同一弹窗）在图片加载前后高度不变。
      if (imageRow >= 0) {
        // Delay image responses so the settled dialog can be measured before the
        // preview decodes; the frame must already be the final height.
        await page.route('**/*', async (route) => {
          if (route.request().resourceType() === 'image') {
            await new Promise((resolve) => setTimeout(resolve, 1500));
          }
          await route.continue();
        });
        await rows.nth(imageRow).click();
        const imageDialog = page.getByTestId('result-detail-dialog');
        await imageDialog.waitFor();
        await waitForStableDialog(page);
        const loading = await measure(page);
        await settle(page);
        await page.waitForTimeout(1600);
        const loaded = await measure(page);
        await page.unroute('**/*');
        const heightChange = delta(loading.dialog, loaded.dialog);
        if (heightChange <= TOLERANCE) {
          ok(`${tag} 图片加载前后弹窗高度稳定`, `${loading.dialog.height.toFixed(1)}px`);
        } else {
          bad(`${tag} 图片加载前后弹窗高度`, `${JSON.stringify(loading.dialog)} → ${JSON.stringify(loaded.dialog)}`);
        }
        if (loading.image && loaded.image && loaded.image.naturalWidth > 0) {
          const ratio = `${loaded.image.naturalWidth}:${loaded.image.naturalHeight}`;
          if (previewFrameOk(loaded.preview, loaded.kind, height)) {
            ok(`${tag} 图片预览固定框`, `${ratio}，预览框 ${loaded.preview.height.toFixed(1)}px，加载${loading.image.complete ? '已完成' : '未完成'}`);
          } else {
            bad(`${tag} 图片预览固定框`, `${ratio} ${JSON.stringify(loaded.preview)}`);
          }
          imageCases.push({ viewport: tag, row: imageRow, ratio, preview: loaded.preview, dialog: loaded.dialog });
        } else {
          bad(`${tag} 图片预览信息`, JSON.stringify({ loading: loading.image, loaded: loaded.image }));
        }
        if (evidenceDir) {
          await page.screenshot({ path: path.join(evidenceDir, `result-detail-${tag}-image.png`) });
        }
        await page.keyboard.press('Escape');
        await imageDialog.waitFor({ state: 'hidden' });
      } else {
        ok(`${tag} 单图任务探测`, '前 20 个任务中没有可用的单图预览；跳过图片加载前后测量');
      }
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
if (imageCases.length) console.log(`INFO: image cases ${JSON.stringify(imageCases)}`);
process.exit(fail ? 1 : 0);
