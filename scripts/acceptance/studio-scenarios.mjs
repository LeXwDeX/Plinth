#!/usr/bin/env node
/**
 * Studio acceptance run: the four scenarios of ux-validation-prototype.md section 5,
 * driven through the real UI with Playwright (clicks, typing, keyboard).
 *
 * Needs a running server. The server must run with PROVIDER_MODE=mock and
 * AGENT_MODE=mock (the mock stack in internal/providers/mock and
 * internal/agents/mock routes the Chinese keywords used below).
 *
 *   BASE=http://127.0.0.1:18200 ADMIN_USERNAME=admin ADMIN_PASSWORD=... \
 *   PLAYWRIGHT_BROWSERS_PATH=/opt/pw-browsers \
 *   node scripts/acceptance/studio-scenarios.mjs
 *
 * Optional env:
 *   OUT   output folder (default /mnt/project-files/design/acceptance-run-1)
 *   ONLY  comma list of scenarios to run, e.g. "s1,s4"
 *   RUN_NUMBER    number shown in the README title (default 1)
 *   COMPARE_WITH  results.json of an earlier run: the README starts with a before/after table against it
 *   REPORT_ONLY=1 rebuild README.md from OUT/results.json without running the browser
 *
 * Nothing is intercepted or mocked in the browser. Every step records pass or
 * fail against the section 5 pass condition, the time it took and a 1440x900
 * screenshot (plus 1280x720 for key screens). Failures never stop the run;
 * steps whose earlier steps failed are marked as blocked.
 */
import { createRequire } from 'node:module';
import fs from 'node:fs';
import path from 'node:path';
import zlib from 'node:zlib';

const require = createRequire(import.meta.url);
function loadPlaywright() {
  for (const base of [process.cwd(), '/opt/node22/lib/node_modules/', '/usr/lib/node_modules/', '/usr/local/lib/node_modules/']) {
    try { return createRequire(base.endsWith('/') ? base : `${base}/`)('playwright'); } catch { /* try next */ }
  }
  return require('playwright');
}
const { chromium } = loadPlaywright();

const BASE = (process.env.BASE || '').replace(/\/$/, '');
const USERNAME = process.env.ADMIN_USERNAME || '';
const PASSWORD = process.env.ADMIN_PASSWORD || '';
const OUT = process.env.OUT || '/mnt/project-files/design/acceptance-run-1';
const ONLY = (process.env.ONLY || 's1,s2,s3,s4').split(',').map((item) => item.trim()).filter(Boolean);
if (!BASE || !USERNAME || !PASSWORD) {
  console.error('BASE, ADMIN_USERNAME and ADMIN_PASSWORD are required.');
  process.exit(2);
}
fs.mkdirSync(OUT, { recursive: true });

const BIG = { width: 1440, height: 900 };
const SMALL = { width: 1280, height: 720 };
const LAUNCH_ARGS = ['--use-gl=angle', '--use-angle=swiftshader', '--enable-unsafe-swiftshader', '--ignore-gpu-blocklist'];

// ---------------------------------------------------------------------------
// recording

const results = [];
const frictions = [];
/** step key to why it failed: 'check' (a pass condition did not hold) or 'error' (the script could not do the step). */
const failed = new Map();
let current = { scenario: '', id: '' };

function friction(text) {
  if (!frictions.some((item) => item.text === text)) frictions.push({ scenario: current.scenario, step: current.id, text });
}

const DEBUG = process.env.DEBUG === '1';
let lapAt = Date.now();
function lap(label) {
  if (DEBUG) console.log(`  [lap] ${label} +${Date.now() - lapAt} ms`);
  lapAt = Date.now();
}

class Check extends Error {}
/** A pass condition that did not hold. Collected, the step is marked failed. */
function expect(condition, message) {
  if (!condition) throw new Check(message);
}

async function shot(page, name, small = false) {
  const file = `${name}.png`;
  await page.screenshot({ path: path.join(OUT, file) });
  let smallFile = '';
  if (small) {
    await page.setViewportSize(SMALL);
    await page.waitForTimeout(500);
    smallFile = `${name}-1280.png`;
    await page.screenshot({ path: path.join(OUT, smallFile) });
    await page.setViewportSize(BIG);
    await page.waitForTimeout(300);
  }
  return { file, smallFile };
}

/**
 * Runs one step. `fn` returns an optional note and may call `expect`. `needs` lists
 * step ids of the same scenario that must have passed. `key` adds the 1280x720 shot.
 */
async function step(page, scenario, id, title, condition, fn, { needs = [], key = false } = {}) {
  current = { scenario, id };
  const name = `${scenario}-${id.replace(/\./g, '-')}`;
  const started = Date.now();
  const record = { scenario, id, title, condition, status: 'pass', ms: 0, shot: '', shotSmall: '', note: '' };
  // A pass condition that did not hold does not stop later steps; a step the script could not perform does.
  const blocked = needs.filter((need) => failed.get(`${scenario}-${need}`) === 'error');
  if (blocked.length > 0) {
    record.status = 'blocked';
    record.note = `前置步骤未通过：${blocked.join('、')}`;
  } else {
    try {
      const note = await fn();
      if (typeof note === 'string') record.note = note;
    } catch (error) {
      record.status = 'fail';
      record.kind = error instanceof Check ? 'check' : 'error';
      record.note = String(error instanceof Check ? error.message : `脚本错误：${error?.message ?? error}`).split('\n')[0].slice(0, 400);
    }
  }
  record.ms = Date.now() - started;
  try {
    const shots = await shot(page, name, key);
    record.shot = shots.file;
    record.shotSmall = shots.smallFile;
  } catch (error) {
    record.note += ` (截图失败：${error.message})`;
  }
  if (record.status !== 'pass') failed.set(`${scenario}-${id}`, record.status === 'blocked' ? 'error' : record.kind ?? 'error');
  results.push(record);
  const mark = record.status === 'pass' ? 'PASS' : record.status === 'fail' ? 'FAIL' : 'BLOCKED';
  console.log(`${mark} ${scenario} ${id} ${title} (${record.ms} ms)${record.note ? ` - ${record.note}` : ''}`);
}

// ---------------------------------------------------------------------------
// page helpers

const shell = (page) => page.locator('[data-testid=studio-shell]');
const composerBox = (page) => page.locator('[data-testid=studio-composer] textarea');

async function login(page) {
  await page.goto(`${BASE}/`);
  await page.waitForSelector('input[type=password]', { timeout: 20000 });
  await page.fill('input:not([type=password])', USERNAME);
  await page.fill('input[type=password]', PASSWORD);
  await page.keyboard.press('Enter');
  await page.waitForSelector('[data-testid=studio-shell]', { timeout: 20000 });
  await page.waitForTimeout(800);
}

async function newSession(page) {
  // New session sits inside the session list popover: two clicks.
  await page.click('[data-testid=studio-session-menu]');
  await page.click('[data-testid=studio-session-new]');
  await page.waitForTimeout(900);
  if (await page.locator('[data-testid=studio-session-new]').count()) await page.keyboard.press('Escape');
  await page.waitForTimeout(400);
}

async function openPicker(page) {
  if (!(await page.locator('[data-testid=workspace-model-picker-panel]').count())) await page.click('[data-testid=workspace-model-picker]');
  await page.waitForTimeout(300);
}

/** Picks a model by (part of) its name in the 2D or 3D table of the one model picker, then closes it. */
async function pickModel(page, which, name) {
  await openPicker(page);
  await page.locator(`[data-testid=workspace-model-picker-${which}] [role=menu]`).getByText(name).first().click();
  await page.keyboard.press('Escape');
  await page.waitForTimeout(500);
}

/** The full name of the model chosen in the 2D or 3D table. */
async function pickerLabel(page, which) {
  await openPicker(page);
  const checked = page.locator(`[data-testid=workspace-model-picker-${which}] [role=menuitemradio][aria-checked=true]`);
  const label = (await checked.count()) ? await checked.first().innerText() : '';
  await page.keyboard.press('Escape');
  return label.replace(/\s+/g, ' ').trim();
}

async function paramsChip(page) {
  return (await page.locator('[data-testid=composer-params]').innerText()).replace(/\s+/g, ' ').trim();
}

async function openParams(page) {
  if (!(await page.locator('[data-testid=composer-params-panel]').count())) {
    await page.click('[data-testid=composer-params]');
    await page.waitForSelector('[data-testid=composer-params-panel]');
  }
}

async function closeParams(page) {
  if (await page.locator('[data-testid=composer-params-panel]').count()) {
    await page.keyboard.press('Escape');
    await page.waitForTimeout(250);
  }
}

/**
 * Sets size or ratio and count in the parameter panel. GPT Image 2.5 lists pixel sizes in a
 * select (no ratio chips), Gemini and Nano Banana list ratio chips and have no count.
 * The store saves 500 ms after the last edit, so wait before the next send.
 */
async function setParams(page, { size, ratio, count }) {
  await openParams(page);
  const panel = page.locator('[data-testid=composer-params-panel]');
  if (ratio) await panel.getByRole('radio', { name: ratio }).first().click();
  if (size) await panel.getByLabel('输出尺寸').selectOption({ label: size });
  if (count) {
    for (let guard = 0; guard < 20; guard += 1) {
      const value = Number((await panel.locator('[data-testid=params-count-value]').innerText()).trim());
      if (value === count) break;
      await panel.getByLabel(value < count ? '增加数量' : '减少数量').click();
      await page.waitForTimeout(80);
    }
  }
  await closeParams(page);
  // A send inside the debounce window loses the edit (seen once while scripting).
  await page.waitForTimeout(900);
}

async function sessionCredits(page) {
  const text = await page.locator('[data-testid=studio-session-credits]').innerText();
  return Number((text.match(/-?\d+(\.\d+)?/) || ['0'])[0]);
}

async function chatBusy(page) {
  return (await page.locator('[data-testid=composer-stop]').count()) > 0;
}

/** Types into the composer and presses Enter, like a person would. */
async function send(page, text) {
  const box = composerBox(page);
  // A running turn locks the box; wait for it instead of failing (the lock itself is noted where it matters).
  await page.waitForFunction(() => !document.querySelector('[data-testid=studio-composer] textarea')?.disabled, null, { timeout: 140000 });
  await box.click();
  await box.fill(text);
  await page.keyboard.press('Enter');
  await page.waitForTimeout(300);
}

async function waitTurnEnd(page, timeout = 60000) {
  await page.waitForTimeout(500);
  await page.waitForFunction(() => !document.querySelector('[data-testid=composer-stop]'), null, { timeout });
  await page.waitForTimeout(600);
}

/** Waits for n result groups of generated images in the chat. */
async function waitImageResults(page, count, timeout = 60000) {
  await page.waitForFunction((n) => document.querySelectorAll('[data-testid=chat-image-result]').length >= n, count, { timeout });
  await page.waitForTimeout(500);
}

/** Waits until no version tile is queued or running; returns how long that took. */
async function waitStripSettled(page, timeout = 60000) {
  const started = Date.now();
  await page.waitForFunction(() => {
    const tiles = [...document.querySelectorAll('[data-testid=version-tile]')];
    return tiles.length > 0 && tiles.every((tile) => !['pending', 'running', 'queued'].includes(tile.getAttribute('data-status') || ''));
  }, null, { timeout });
  return Date.now() - started;
}

async function versionTexts(page) {
  return page.locator('[data-testid=version-tile]').evaluateAll((nodes) => nodes.map((node) => (node.innerText || '').replace(/\s+/g, ' ').trim()));
}

async function stageLabel(page) {
  return (await page.locator('[data-testid=stage-version]').first().innerText().catch(() => '')).trim();
}

async function lastAgentText(page) {
  const messages = page.locator('[data-testid=chat-message]');
  const count = await messages.count();
  return count ? (await messages.nth(count - 1).innerText()).replace(/\s+/g, ' ').trim() : '';
}

async function chatText(page) {
  return (await page.locator('[data-testid=chat-messages]').innerText().catch(() => '')).replace(/\s+/g, ' ');
}

async function imageNaturalSize(locator) {
  return locator.evaluate((img) => ({ w: img.naturalWidth, h: img.naturalHeight }));
}


/** File names inside a ZIP, read from its central directory (no library needed). */
function zipEntries(buffer) {
  let end = buffer.length - 22;
  while (end >= 0 && buffer.readUInt32LE(end) !== 0x06054b50) end -= 1;
  if (end < 0) return [];
  const count = buffer.readUInt16LE(end + 10);
  let offset = buffer.readUInt32LE(end + 16);
  const entries = [];
  for (let i = 0; i < count; i += 1) {
    if (buffer.readUInt32LE(offset) !== 0x02014b50) break;
    const nameLength = buffer.readUInt16LE(offset + 28);
    const extraLength = buffer.readUInt16LE(offset + 30);
    const commentLength = buffer.readUInt16LE(offset + 32);
    const flags = buffer.readUInt16LE(offset + 8);
    entries.push(buffer.toString(flags & 0x800 ? 'utf8' : 'utf8', offset + 46, offset + 46 + nameLength));
    offset += 46 + nameLength + extraLength + commentLength;
  }
  return entries;
}

/** Text of one entry of a ZIP (stored or deflated). */
function zipRead(buffer, wanted) {
  let end = buffer.length - 22;
  while (end >= 0 && buffer.readUInt32LE(end) !== 0x06054b50) end -= 1;
  if (end < 0) return '';
  const count = buffer.readUInt16LE(end + 10);
  let offset = buffer.readUInt32LE(end + 16);
  for (let i = 0; i < count; i += 1) {
    const method = buffer.readUInt16LE(offset + 10);
    const compressed = buffer.readUInt32LE(offset + 20);
    const nameLength = buffer.readUInt16LE(offset + 28);
    const extraLength = buffer.readUInt16LE(offset + 30);
    const commentLength = buffer.readUInt16LE(offset + 32);
    const local = buffer.readUInt32LE(offset + 42);
    const name = buffer.toString('utf8', offset + 46, offset + 46 + nameLength);
    if (name === wanted) {
      const start = local + 30 + buffer.readUInt16LE(local + 26) + buffer.readUInt16LE(local + 28);
      const data = buffer.subarray(start, start + compressed);
      return (method === 0 ? data : zlib.inflateRawSync(data)).toString('utf8');
    }
    offset += 46 + nameLength + extraLength + commentLength;
  }
  return '';
}

async function saveDownload(download, name) {
  const file = path.join(OUT, name);
  await download.saveAs(file);
  return { file, size: fs.statSync(file).size, suggested: download.suggestedFilename() };
}

/** Two strokes over the chest area of the picture on the stage (the mask tool must be on). */
async function paintChest(page) {
  const area = page.locator('[data-canvas-drawing-area]');
  const box = await area.boundingBox();
  const cx = box.x + box.width / 2;
  const cy = box.y + box.height * 0.4;
  for (const [dx, dy] of [[-40, -10], [-30, 30]]) {
    await page.mouse.move(cx + dx, cy + dy);
    await page.mouse.down();
    for (let i = 0; i <= 11; i += 1) await page.mouse.move(cx + dx + i * 7, cy + dy + (i % 2) * 12, { steps: 3 });
    await page.mouse.up();
  }
  await page.waitForTimeout(400);
}

/** Share of pixels that differ between two images on the page (same size), plus their size. */
async function compareImages(page, urlA, urlB) {
  return page.evaluate(async ([a, b]) => {
    const load = (url) => new Promise((resolve, reject) => { const img = new Image(); img.crossOrigin = 'anonymous'; img.onload = () => resolve(img); img.onerror = () => reject(new Error(`load ${url}`)); img.src = url; });
    const [imgA, imgB] = await Promise.all([load(a), load(b)]);
    const draw = (img) => { const c = document.createElement('canvas'); c.width = img.naturalWidth; c.height = img.naturalHeight; const ctx = c.getContext('2d'); ctx.drawImage(img, 0, 0); return ctx.getImageData(0, 0, c.width, c.height).data; };
    if (imgA.naturalWidth !== imgB.naturalWidth || imgA.naturalHeight !== imgB.naturalHeight) return { sameSize: false, a: [imgA.naturalWidth, imgA.naturalHeight], b: [imgB.naturalWidth, imgB.naturalHeight] };
    const dataA = draw(imgA); const dataB = draw(imgB);
    let diff = 0;
    for (let i = 0; i < dataA.length; i += 4) if (dataA[i] !== dataB[i] || dataA[i + 1] !== dataB[i + 1] || dataA[i + 2] !== dataB[i + 2]) diff += 1;
    return { sameSize: true, size: [imgA.naturalWidth, imgA.naturalHeight], changed: diff / (dataA.length / 4) };
  }, [urlA, urlB]);
}


/** Makes sure the 2D or 3D picker shows a model whose name contains `name`. */
async function ensureModel(page, which, name) {
  if (!(await pickerLabel(page, which)).includes(name)) await pickModel(page, which, name);
}

/** Top edge (screen y) of the first picture drawn on the board column at the board's centre, or null. */
async function pictureTopOnBoard(page) {
  return page.evaluate(() => {
    const area = document.querySelector('[data-canvas-drawing-area]');
    const box = area?.getBoundingClientRect();
    if (!area || !box) return null;
    for (const canvas of area.querySelectorAll('canvas')) {
      const context = canvas.getContext('2d');
      if (!context) continue;
      const scale = canvas.width / canvas.getBoundingClientRect().width;
      const x = Math.round((box.width / 2) * scale);
      const column = context.getImageData(x, 0, 1, canvas.height).data;
      const base = [column[0], column[1], column[2], column[3]];
      for (let row = 0; row < canvas.height; row += 1) {
        const at = row * 4;
        const delta = Math.abs(column[at] - base[0]) + Math.abs(column[at + 1] - base[1]) + Math.abs(column[at + 2] - base[2]);
        if (delta > 60 && column[at + 3] > 200) return box.top + row / scale;
      }
    }
    return null;
  });
}


/** Sends a text brief with one picture per request (size 1024x1024, count 1) and waits for the result. */
async function generateOneImage(page, text) {
  await ensureModel(page, 'image', 'GPT Image 2.5 Sunburst');
  await setParams(page, { size: '1024x1024', count: 1 });
  const before = await page.locator('[data-testid=chat-image-result]').count();
  await send(page, text);
  await waitImageResults(page, before + 1);
  await waitTurnEnd(page);
  await waitStripSettled(page);
}

/** Chooses the 三视图 Skill in the composer's Skill picker and sends with an empty box (the Schema's default text is used). */
async function startThreeViewSkill(page) {
  await page.click('[data-testid=workspace-skill-picker]');
  await page.locator('[data-testid=workspace-skill-picker-panel]').getByText('三视图', { exact: true }).first().click();
  await page.click('[data-testid=composer-send]');
}

/** Starts the three views of the picture on the stage and continues the other two. */
async function makeThreeViews(page) {
  await startThreeViewSkill(page);
  await page.waitForSelector('[data-testid=three-view-card]', { timeout: 60000 });
  const card = page.locator('[data-testid=three-view-card]').last();
  const succeeded = () => card.locator('[data-testid=three-view-slot][data-status=succeeded]').count();
  await page.waitForFunction(() => document.querySelector('[data-testid=three-view-card]:last-of-type [data-testid=three-view-slot][data-status=succeeded]') || [...document.querySelectorAll('[data-testid=three-view-slot][data-status=succeeded]')].length > 0, null, { timeout: 60000 });
  for (let round = 0; round < 2; round += 1) {
    const button = card.locator('[data-testid=three-view-start]').first();
    if (!(await button.count())) break;
    await button.click();
    await page.waitForTimeout(800);
    const approve = page.locator('[data-testid=studio-approval-approve]');
    if (await approve.count()) { await approve.first().click(); await page.waitForTimeout(600); }
    const target = round + 2;
    for (let tries = 0; tries < 60 && (await succeeded()) < target; tries += 1) await page.waitForTimeout(1000);
  }
  await waitTurnEnd(page).catch(() => {});
  await waitStripSettled(page).catch(() => {});
  await page.waitForTimeout(600);
}

/** Waits for the strip to hold a finished tile whose text matches. */
async function waitTile(page, pattern, timeout = 90000) {
  await page.waitForFunction(({ source, flags }) => {
    const expression = new RegExp(source, flags);
    return [...document.querySelectorAll('[data-testid=version-tile]')].some((tile) => expression.test(tile.textContent || '') && tile.getAttribute('data-status') === 'ready');
  }, { source: pattern.source, flags: pattern.flags }, { timeout });
}

/** Opens a version from the strip; a "drop the painted mask?" question is answered with drop. */
async function clickTile(page, pattern) {
  await page.locator('[data-testid=version-tile]').filter({ hasText: pattern }).locator('button').first().click();
  await page.waitForTimeout(500);
  const discard = page.locator('[data-testid=mask-discard-confirm]');
  if (await discard.count()) { await discard.click(); await page.waitForTimeout(400); }
  await page.waitForTimeout(500);
}

/** Reloads the page and waits until the studio is back. */
async function reloadStudio(page) {
  await page.reload();
  await page.waitForSelector('[data-testid=studio-shell]', { timeout: 20000 });
  await page.waitForTimeout(2500);
}

// The right panel's tab buttons (Schema canvas.frontend.panel_tabs); the picture's own default is 图层, so 详情 is opened on purpose.
async function rightPanelTab(page, id) {
  await page.click(`[data-testid=right-panel-tab-${id}]`);
  await page.waitForTimeout(700);
}

async function panelDetails(page) {
  await rightPanelTab(page, 'details');
  return (await page.locator('[data-testid=right-panel]').innerText().catch(() => '')).replace(/\s+/g, ' ');
}

// A tiny PNG writer so the upload step needs no asset on disk.
function crc32(buffer) {
  let crc = ~0;
  for (const byte of buffer) {
    crc ^= byte;
    for (let bit = 0; bit < 8; bit += 1) crc = (crc >>> 1) ^ (0xedb88320 & -(crc & 1));
  }
  return ~crc >>> 0;
}
function chunk(type, data) {
  const head = Buffer.alloc(8);
  head.writeUInt32BE(data.length, 0);
  head.write(type, 4, 'ascii');
  const tail = Buffer.alloc(4);
  tail.writeUInt32BE(crc32(Buffer.concat([head.subarray(4), data])), 0);
  return Buffer.concat([head, data, tail]);
}
/** A 640x360 white page with a rough black composition sketch (horizon, building blocks, a figure). */
function sketchPng() {
  const width = 640;
  const height = 360;
  const raw = Buffer.alloc((width * 3 + 1) * height, 255);
  const plot = (x, y) => {
    if (x < 0 || y < 0 || x >= width || y >= height) return;
    const offset = y * (width * 3 + 1) + 1 + x * 3;
    raw[offset] = 20; raw[offset + 1] = 20; raw[offset + 2] = 20;
  };
  for (let y = 0; y < height; y += 1) raw[y * (width * 3 + 1)] = 0;
  const rect = (x0, y0, x1, y1) => {
    for (let x = x0; x <= x1; x += 1) for (const w of [0, 1, 2]) { plot(x, y0 + w); plot(x, y1 - w); }
    for (let y = y0; y <= y1; y += 1) for (const w of [0, 1, 2]) { plot(x0 + w, y); plot(x1 - w, y); }
  };
  for (let x = 0; x < width; x += 1) for (const w of [0, 1, 2]) plot(x, 240 + w);
  rect(40, 90, 150, 240); rect(170, 40, 260, 240); rect(420, 70, 560, 240); rect(300, 150, 340, 240);
  for (let r = 0; r < 12; r += 1) { rect(300 + 14 - r, 120 - r + 10, 300 + 26 + r, 150 - r + 10); }
  const header = Buffer.alloc(13);
  header.writeUInt32BE(width, 0); header.writeUInt32BE(height, 4); header[8] = 8; header[9] = 2;
  return Buffer.concat([
    Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]),
    chunk('IHDR', header),
    chunk('IDAT', zlib.deflateSync(raw)),
    chunk('IEND', Buffer.alloc(0)),
  ]);
}

// ---------------------------------------------------------------------------
// scenarios

const scenarios = { s1: null, s2: null, s3: null, s4: null };

scenarios.s1 = async (page) => {
  const S = 's1';
  await newSession(page);
  const credits0 = await sessionCredits(page);

  await step(page, S, '1', '新建会话，点起步卡', '输入框被填好，积分不变，参数芯片显示 1:1 · 4 张', async () => {
    const problems = [];
    const card = page.locator('[data-testid=stage-starter-card]').first();
    const box = await card.boundingBox();
    // A real pointer click at the card, not Playwright's actionability check: the point is what a person's mouse does.
    await page.mouse.click(box.x + box.width / 2, box.y + box.height / 2);
    await page.waitForTimeout(500);
    let text = await composerBox(page).inputValue();
    if (text.length === 0) {
      const covered = await card.evaluate((el) => {
        const r = el.getBoundingClientRect();
        const top = document.elementFromPoint(r.x + r.width / 2, r.y + r.height / 2);
        return top && !el.contains(top) ? top.tagName : '';
      });
      problems.push(`点起步卡后输入框仍是空的（点击落在 ${covered || '别处'} 上：起步卡被画板的 canvas 盖住，鼠标点不到）`);
      friction('起步卡被画板的 canvas 盖在下面，鼠标点不到（DOM 里起步卡在 Stage 之前，没有 z-index）。键盘 Tab 可以聚焦并回车。');
      // Keyboard still reaches the card: use it so the rest of the scenario can run.
      await card.focus();
      await page.keyboard.press('Enter');
      await page.waitForTimeout(400);
      text = await composerBox(page).inputValue();
      if (text.length === 0) await composerBox(page).fill('一只青铜机械鸟，工作室光效');
    }
    if ((await sessionCredits(page)) !== credits0) problems.push('点起步卡后积分变了');
    const before = await paramsChip(page);
    // The chip only has a value once a 2D model is chosen.
    await pickModel(page, 'image', 'GPT Image 2.5 Sunburst');
    const afterPick = await paramsChip(page);
    if (!/\d:\d|\d+x\d+/.test(before)) friction('新会话没选模型时，参数芯片只显示“参数”，没有比例和数量；选了模型才出现。起步卡不会替用户选模型，用户得自己先去模型选择器。');
    if (!(/1:1/.test(afterPick) && /4/.test(afterPick))) problems.push(`选模型后参数芯片是“${afterPick}”，不是 1:1 · 4 张（选模型前是“${before}”）`);
    expect(problems.length === 0, problems.join('；'));
    return `芯片：${afterPick}`;
  }, { key: true });

  await step(page, S, '2', '把参数改成 2:3 · 6 张', '芯片更新（2:3 即 1024x1536，6 张）；发送按钮旁显示“生成约 36 积分”', async () => {
    await setParams(page, { size: '1024x1536', count: 6 });
    const chip = await paramsChip(page);
    expect(/1024x1536/.test(chip) && /6/.test(chip), `芯片是“${chip}”`);
    if (!/2:3/.test(chip)) friction('GPT Image 2.5 的参数面板只有“输出尺寸”下拉里的像素尺寸（1024x1536），没有 2:3 这样的比例；要出 6 张只有这类模型能选数量（Gemini、Nano Banana 只出 1 张，没有数量）。');
    const cost = (await page.locator('[data-testid=composer-send-cost]').innerText().catch(() => '')).trim();
    expect(/36/.test(cost), `发送按钮旁显示“${cost || '空'}”，不是 36 积分`);
    return `芯片：${chip}；发送旁：${cost}`;
  }, {});

  await step(page, S, '3', '输入“赛博朋克女刺客，全身立绘”，Enter 发送', '对话出现 6 张候选组；版本条出现 2D v1 · 6 张；本会话积分 +36；输入法组词时 Enter 不发送', async () => {
    const box = composerBox(page);
    await box.click();
    await box.fill('赛博朋克女刺客，全身立绘');
    // An IME confirmation Enter: composition events around the key, then Enter with isComposing.
    await box.evaluate((el) => {
      el.dispatchEvent(new CompositionEvent('compositionstart', { bubbles: true }));
      el.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', code: 'Enter', keyCode: 229, isComposing: true, bubbles: true, cancelable: true }));
    });
    await page.waitForTimeout(500);
    const sentDuringIme = (await page.locator('[data-testid=chat-message]').count()) > 0;
    await box.evaluate((el) => el.dispatchEvent(new CompositionEvent('compositionend', { bubbles: true })));
    expect(!sentDuringIme, '输入法组词时按 Enter 却发送了');
    const before = await sessionCredits(page);
    await page.keyboard.press('Enter');
    await waitImageResults(page, 1);
    await waitTurnEnd(page);
    const group = page.locator('[data-testid=chat-image-result]').last();
    const count = Number(await group.getAttribute('data-count'));
    expect(count === 6, `候选组有 ${count} 张，不是 6 张`);
    const tiles = await versionTexts(page);
    expect(tiles.some((text) => /2D v1/.test(text) && /6/.test(text)), `版本条是“${tiles.join(' / ')}”`);
    await page.waitForTimeout(1200);
    const after = await sessionCredits(page);
    expect(after - before === 36, `积分变化 ${after - before}，不是 +36（${before} 到 ${after}）`);
    return `版本条：${tiles.join(' / ')}；积分 ${before} 到 ${after}`;
  }, { needs: ['2'], key: true });

  await step(page, S, '4', 'Space 打开灯箱，翻 6 张，第 3 张点“选用”', '舞台显示第 3 张；芯片“当前：@img3”；右侧栏详情显示提示词和 2:3', async () => {
    // Space on the stage opens the large view of the picture on the stage.
    await page.evaluate(() => document.activeElement instanceof HTMLElement && document.activeElement.blur());
    await page.keyboard.press('Space');
    await page.waitForSelector('[data-testid=studio-lightbox]', { timeout: 5000 });
    lap('space opened');
    const spaceCounter = (await page.locator('[data-testid=lightbox-counter]').count()) ? await page.locator('[data-testid=lightbox-counter]').innerText() : '';
    await page.keyboard.press('Escape');
    await page.waitForSelector('[data-testid=studio-lightbox]', { state: 'detached', timeout: 3000 });
    const flipsCandidates = /\/\s*6/.test(spaceCounter);
    if (!flipsCandidates) friction('Space 打开的灯箱只看舞台上这一张，←/→ 在版本之间切换，不翻候选；翻 6 张候选要走版本条里这一组的展开层，逐张点“放大”（对话里的图点一下是上舞台，已经没有放大查看）。');
    // Candidates are flipped in the large view the version strip opens: the group's expand layer, then 放大 on its first candidate.
    lap('space closed');
    const group = page.locator('[data-testid=chat-image-result]').last();
    await page.locator('[data-testid=version-expand]').last().click();
    await page.waitForSelector('[data-testid=version-candidates]', { timeout: 5000 });
    await page.locator('[data-testid=version-candidates] [data-testid=version-enlarge]').first().click();
    lap('enlarge clicked');
    await page.waitForSelector('[data-testid=studio-lightbox]', { timeout: 5000 });
    const counter = () => page.locator('[data-testid=lightbox-counter]').innerText();
    await page.waitForTimeout(700);
    const seen = [await counter()];
    for (let i = 0; i < 5; i += 1) { await page.keyboard.press('ArrowRight'); await page.waitForTimeout(350); seen.push(await counter()); }
    for (let i = 0; i < 3; i += 1) { await page.keyboard.press('ArrowLeft'); await page.waitForTimeout(250); }
    await page.keyboard.press('Escape');
    await page.waitForSelector('[data-testid=studio-lightbox]', { state: 'detached', timeout: 3000 });
    const flipped = /6\s*\/\s*6/.test(seen[5]);
    lap('flipped');
    // The expand layer may still be open behind the large view; close it so the group in the chat can be clicked.
    if (await page.locator('[data-testid=version-candidates]').count()) {
      await page.keyboard.press('Escape');
      await page.waitForSelector('[data-testid=version-candidates]', { state: 'detached', timeout: 3000 }).catch(() => {});
    }
    if (!(await page.locator('[data-testid=lightbox-pick]').count())) friction('灯箱里没有“选用”按钮，只能回到对话候选组点“选用”；灯箱只看图。');
    const picks = group.locator('[data-testid=candidate-pick]');
    await picks.nth(2).click();
    lap('picked');
    await page.waitForTimeout(1200);
    const chip = (await page.locator('[data-testid=composer-object-chip]').innerText().catch(() => '')).replace(/\s+/g, ' ');
    expect(/@img3/.test(chip), `输入框芯片是“${chip || '无'}”`);
    const stage = await stageLabel(page);
    const details = await panelDetails(page);
    const problems = [];
    if (!/赛博朋克女刺客/.test(details)) problems.push('右侧栏详情没有显示提示词');
    if (!/2:3|2：3|1024\s*[x×]\s*1536/.test(details)) problems.push('右侧栏详情没有显示尺寸或比例');
    if (problems.length) friction('右侧栏“详情”页只有版本、类型、句柄、能力、候选数、创建时间、任务号；没有提示词，也没有尺寸或比例。');
    if (!flipped) problems.push(`灯箱翻不到 6/6：${seen.join(' ; ')}`);
    expect(problems.length === 0, problems.join('；'));
    return `舞台：${stage}；芯片：${chip}；Space 灯箱计数：${spaceCounter || '无（只有一张）'}`;
  }, { needs: ['3'], key: true });

  await step(page, S, '5', '回到候选组改选第 5 张', '允许改选；版本条组不变；芯片变 @img5', async () => {
    const before = (await versionTexts(page)).length;
    await page.locator('[data-testid=chat-image-result]').last().locator('[data-testid=candidate-pick]').nth(4).click();
    await page.waitForTimeout(1200);
    const chip = (await page.locator('[data-testid=composer-object-chip]').innerText().catch(() => '')).replace(/\s+/g, ' ');
    expect(/@img5/.test(chip), `输入框芯片是“${chip || '无'}”`);
    const after = (await versionTexts(page)).length;
    expect(after === before, `版本条数量从 ${before} 变成 ${after}`);
  }, { needs: ['3'] });

  await step(page, S, '6', '输入“换成红黑配色，出 3 张”', 'Agent 回复第一行写“基于 @img5”；新组 2D v2 · 3 张 作为 v1 的分支', async () => {
    await setParams(page, { count: 3 });
    await send(page, '换成红黑配色，出 3 张');
    await waitImageResults(page, 2);
    await waitTurnEnd(page);
    const lag = await waitStripSettled(page);
    if (lag > 3000) friction(`对话里候选图已经出来后，版本条的新版本还“排队中”约 ${Math.round(lag / 1000)} 秒才变成可用，舞台头仍停在旧版本。`);
    const reply = await lastAgentText(page);
    expect(/基于\s*@img5/.test(reply), `Agent 回复是“${reply.slice(0, 80)}”`);
    const tiles = await versionTexts(page);
    expect(tiles.some((text) => /2D v2/.test(text) && /3/.test(text)), `版本条是“${tiles.join(' / ')}”`);
    const links = await page.locator('[data-testid=version-link]').evaluateAll((nodes) => nodes.map((node) => node.getAttribute('data-full-label') || ''));
    return `版本条：${tiles.join(' / ')}；连线：${links.join('、')}`;
  }, { needs: ['3'], key: true });

  await step(page, S, '7', '选“三视图”Skill 发送', '三视图卡片出现；正面自动生成；侧、背各有“继续生成（约 6 积分）”；全部完成后版本条出现 2D v3 · 三视图', async () => {
    await startThreeViewSkill(page);
    await page.waitForSelector('[data-testid=three-view-card]', { timeout: 60000 });
    const card = page.locator('[data-testid=three-view-card]').last();
    await page.waitForFunction(() => {
      const slots = [...document.querySelectorAll('[data-testid=three-view-card]')].pop()?.querySelectorAll('[data-testid=three-view-slot]') ?? [];
      return slots.length === 3 && [...slots].some((slot) => slot.getAttribute('data-status') === 'succeeded');
    }, null, { timeout: 60000 });
    await page.waitForTimeout(600);
    const slotInfo = async () => card.locator('[data-testid=three-view-slot]').evaluateAll((nodes) => nodes.map((node) => ({ slot: node.getAttribute('data-slot'), status: node.getAttribute('data-status'), text: (node.innerText || '').replace(/\s+/g, ' ').trim() })));
    const first = await slotInfo();
    const startButtons = card.locator('[data-testid=three-view-start]');
    const startCount = await startButtons.count();
    const labels = await startButtons.allInnerTexts();
    const problems = [];
    if (startCount !== 2) problems.push(`有 ${startCount} 个“继续生成”按钮，应有 2 个（${JSON.stringify(first)}）`);
    if (!labels.every((label) => /继续生成/.test(label) && /6/.test(label))) problems.push(`按钮文字是“${labels.join(' / ')}”，没有写“约 6 积分”`);
    // Continue the other two views, approving the cards that ask.
    for (let round = 0; round < 2; round += 1) {
      const button = card.locator('[data-testid=three-view-start]').first();
      if (!(await button.count())) break;
      await button.click();
      await page.waitForTimeout(800);
      const approve = page.locator('[data-testid=studio-approval-approve]');
      if (await approve.count()) { await approve.first().click(); await page.waitForTimeout(600); }
      await page.waitForFunction((index) => {
        const slots = [...document.querySelectorAll('[data-testid=three-view-card]')].pop()?.querySelectorAll('[data-testid=three-view-slot]') ?? [];
        return [...slots].filter((slot) => slot.getAttribute('data-status') === 'succeeded').length >= index;
      }, round + 2, { timeout: 60000 });
    }
    await waitTurnEnd(page).catch(() => {});
    await waitStripSettled(page).catch(() => {});
    await page.waitForTimeout(800);
    const done = await slotInfo();
    if (!done.every((slot) => slot.status === 'succeeded')) problems.push(`三个视图没有全部完成：${JSON.stringify(done)}`);
    const tiles = await versionTexts(page);
    if (!tiles.some((text) => /三视图/.test(text) && /2D v3/.test(text))) problems.push(`版本条是“${tiles.join(' / ')}”，没有 2D v3 · 三视图`);
    expect(problems.length === 0, problems.join('；'));
    return `版本条：${tiles.join(' / ')}`;
  }, { needs: ['6'], key: true });

  await step(page, S, '8', '改 2D 模型为 Nano Banana Pro，舞台头点“局部重绘”', '提示不支持蒙版并提供一键切换；切换后进入蒙版', async () => {
    // Back to the picked candidate (@img5) in 2D v1.
    await page.locator('[data-testid=version-tile] button').first().click();
    await page.waitForTimeout(900);
    await pickModel(page, 'image', 'Nano Banana Pro');
    await page.getByLabel('局部重绘').first().click();
    const dialog = page.getByRole('dialog').filter({ hasText: '不支持局部重绘' });
    await dialog.waitFor({ timeout: 5000 });
    const switchButton = dialog.getByRole('button', { name: /切换并涂抹/ });
    expect(await switchButton.count() === 1, '提示里没有“切换并涂抹”按钮');
    await shot(page, `${S}-8-dialog`);
    await switchButton.click();
    await page.waitForSelector('[data-testid=composer-mask-chip]', { timeout: 5000 });
    const label = await pickerLabel(page, 'image');
    expect(/GPT Image 2\.5/.test(label), `切换后 2D 模型是“${label}”`);
    friction('不支持蒙版时弹出的是一个居中对话框（切换并涂抹 / 退出蒙版），不是参数条里的提示；一键切换本身可用。');
    return `切换后 2D 模型：${label}`;
  }, { needs: ['6'], key: true });

  await step(page, S, '9', '涂胸甲，按 Enter', '输入框上方出现“蒙版 · @img5”芯片', async () => {
    await paintChest(page);
    const chip = (await page.locator('[data-testid=composer-mask-chip]').innerText().catch(() => '')).replace(/\s+/g, ' ');
    const object = (await page.locator('[data-testid=composer-object-chip]').innerText().catch(() => '')).replace(/\s+/g, ' ');
    const problems = [];
    if (!/已涂/.test(chip)) problems.push(`涂抹后蒙版芯片是“${chip || '无'}”`);
    if (!/@img5/.test(object)) {
      problems.push(`对象芯片是“${object}”，不是 @img5：版本条点回 2D v1 后，舞台和芯片回到第 1 张，没有记住选用的第 5 张`);
      friction('点版本条回到有选用记录的 2D v1，舞台和输入框芯片落在第 1 张（@img1），不是之前选用的 @img5；版本条展开后重新“选用”才能回到那一张。');
    }
    await page.evaluate(() => document.activeElement instanceof HTMLElement && document.activeElement.blur());
    await page.keyboard.press('Enter');
    await page.getByRole('dialog').filter({ hasText: '局部重绘' }).waitFor({ timeout: 5000 });
    friction('涂完按 Enter 弹出“局部重绘”对话框，要在对话框里写重绘描述，不是回到主输入框；对话框里按钮叫“准备本次重绘”，实际点了就提交并扣费，名字容易让人以为还要再发送一次。');
    expect(problems.length === 0, problems.join('；'));
    return `芯片：${chip}；对象芯片：${object}`;
  }, { needs: ['8'], key: true });

  await step(page, S, '10', '写“改成金色花纹”，提交局部重绘', '新版本只改了蒙版区域；来源连线写“局部重绘”；积分 +6', async () => {
    const creditsBefore = await sessionCredits(page);
    const tilesBefore = (await versionTexts(page)).length;
    const dialog = page.getByRole('dialog').filter({ hasText: '局部重绘' });
    await dialog.getByPlaceholder(/描述涂抹区域/).fill('改成金色花纹');
    await dialog.getByRole('button', { name: '准备本次重绘' }).click();
    await page.waitForFunction((n) => document.querySelectorAll('[data-testid=version-tile]').length > n, tilesBefore, { timeout: 30000 });
    await waitStripSettled(page);
    await page.waitForTimeout(1500);
    const problems = [];
    const tiles = await versionTexts(page);
    const credits = await sessionCredits(page);
    if (credits - creditsBefore !== 6) {
      problems.push(`积分变化 ${credits - creditsBefore}，不是 +6（顶栏 ${creditsBefore} 到 ${credits}）`);
      friction('顶栏“本会话 N 积分”不会随三视图“继续生成”和局部重绘实时增加，只在对话回合结束后更新；刷新页面后才补上（本次脚本里差 18 积分）。');
    }
    const links = await page.locator('[data-testid=version-link]').evaluateAll((nodes) => nodes.map((node) => `${node.getAttribute('data-link')}|${node.getAttribute('data-full-label')}`));
    if (!/局部重绘/.test(links.join(' '))) {
      problems.push(`来源连线只写“${links[0] ?? ''}”，没有写“局部重绘”`);
      friction('版本之间的连线提示只有“由 2D v1 编辑而来”，局部重绘、改图、换色都是同一句，看不出是局部重绘。');
    }
    // Compare the picture the mask was painted on with the new picture: only the masked area may change.
    const handleNumber = Number((((await page.locator('[data-testid=composer-object-chip]').innerText().catch(() => '')).match(/@img(\d+)/)) || [])[1] ?? 1);
    const sources = await page.evaluate((index) => {
      const first = document.querySelectorAll('[data-testid=chat-image-result]')[0]?.querySelectorAll('img');
      const newest = [...document.querySelectorAll('[data-testid=version-tile]')].pop();
      const tile = [...document.querySelectorAll('[data-testid=version-tile]')].find((node) => /2D v4/.test(node.textContent || ''));
      return { reference: first?.[index - 1]?.src ?? '', result: (tile ?? newest)?.querySelector('img')?.src ?? '' };
    }, handleNumber);
    const diff = sources.reference && sources.result ? await compareImages(page, sources.reference, sources.result).catch((error) => ({ error: error.message })) : null;
    if (!diff || diff.error) problems.push(`找不到可以对比的两张图${diff?.error ? `（${diff.error}）` : ''}`);
    else if (!diff.sameSize) problems.push(`新图尺寸 ${diff.b?.join('x')} 与原图 ${diff.a?.join('x')} 不一致`);
    else if (!(diff.changed > 0 && diff.changed < 0.2)) problems.push(`有 ${(diff.changed * 100).toFixed(1)}% 的像素变了，不像只改了蒙版区域`);
    // Show the new picture on the stage for the screenshot.
    await page.locator('[data-testid=version-tile]').filter({ hasText: '2D v4' }).locator('button').first().click().catch(() => {});
    await page.waitForTimeout(1200);
    const changedText = diff?.changed !== undefined ? `${(diff.changed * 100).toFixed(1)}% 的像素与原图不同` : '未能对比像素';
    expect(problems.length === 0, `${problems.join('；')}（${changedText}）`);
    return `版本条：${tiles.join(' / ')}；连线：${links.join(' ; ')}；${changedText}`;
  }, { needs: ['8'], key: true });

  await step(page, S, '11', '切到别的版本再切回 @img5', '未发送的蒙版还在', async () => {
    // Paint a new mask on v1, switch to v2 and come back.
    await page.locator('[data-testid=version-tile] button').first().click();
    await page.waitForTimeout(900);
    if (!(await page.locator('[data-testid=composer-mask-chip]').count())) {
      await page.getByLabel('局部重绘').first().click();
      await page.waitForSelector('[data-testid=composer-mask-chip]', { timeout: 5000 });
    }
    await paintChest(page);
    const painted = await page.locator('[data-testid=composer-mask-chip]').innerText();
    await page.locator('[data-testid=version-tile]').nth(1).locator('button').first().click();
    await page.waitForTimeout(800);
    const asked = await page.getByRole('dialog').filter({ hasText: '放弃涂好的蒙版' }).count();
    if (asked) {
      await shot(page, `${S}-11-discard-dialog`);
      await page.getByRole('button', { name: '继续涂抹' }).click();
      await page.waitForTimeout(500);
    }
    if (asked) friction('涂了蒙版再点别的版本，界面弹出“放弃涂好的蒙版？”让用户选，而不是把蒙版留着等回来；用户只能“继续涂抹”留在原版本，没法离开再回来。');
    const still = (await page.locator('[data-testid=composer-mask-chip]').innerText().catch(() => '')).replace(/\s+/g, ' ');
    expect(!asked, `涂了蒙版（${painted.replace(/\s+/g, ' ')}）后切版本，弹出了“放弃蒙版”确认；选“继续涂抹”后蒙版仍在：“${still}”，但没有“切走再切回”的路径`);
    // Information only (not a pass condition): coming back to the version, is the mask still there?
    await page.locator('[data-testid=version-tile] button').first().click().catch(() => {});
    await page.waitForTimeout(900);
    const back = (await page.locator('[data-testid=composer-mask-chip]').innerText().catch(() => '')).replace(/\s+/g, ' ');
    return `取舍已接受：切版本时未发送的蒙版静默保留在原版本上，不弹确认，只给提示；切走时不应出现“放弃蒙版”询问。切走后芯片：“${still || '无'}”；切回后芯片：“${back || '无'}”。`;
  }, { needs: ['8'] });

  await step(page, S, '12', '全部产出里多选 4 项，下载所选', '得到 4 个 PNG，文件名含版本号', async () => {
    // Leave the mask first so the stage is free.
    const exit = page.locator('[data-testid=mask-exit]');
    if (await exit.count()) {
      await exit.click();
      await page.waitForTimeout(400);
      const confirm = page.locator('[data-testid=mask-discard-confirm]');
      if (await confirm.count()) { await confirm.click(); await page.waitForTimeout(400); }
    }
    await page.click('[data-testid=all-outputs-button]');
    await page.waitForSelector('[data-testid=all-outputs]');
    const checks = page.locator('[data-testid=all-outputs-check]');
    const total = await checks.count();
    const tileTexts = await page.locator('[data-testid=all-outputs-tile]').allInnerTexts();
    for (let i = 0; i < Math.min(4, total); i += 1) await checks.nth(i).click();
    const [download] = await Promise.all([
      page.waitForEvent('download', { timeout: 30000 }),
      page.click('[data-testid=all-outputs-download]'),
    ]);
    const saved = await saveDownload(download, `${S}-12-download.zip`);
    const entries = zipEntries(fs.readFileSync(saved.file));
    const png = entries.filter((name) => /\.png$/i.test(name));
    const problems = [];
    if (total < 4) problems.push(`全部产出里只有 ${total} 项，选不满 4 项`);
    if (png.length !== 4) problems.push(`ZIP 里有 ${png.length} 个 PNG：${entries.join('、')}`);
    if (!png.every((name) => /v\d/i.test(name))) problems.push(`文件名里没有版本号：${png.join('、')}`);
    expect(problems.length === 0, problems.join('；'));
    return `${saved.suggested}（${saved.size} 字节）：${entries.join('、')}；产出里共 ${total} 项：${tileTexts.map((t) => t.replace(/\s+/g, ' ')).join(' / ')}`;
  }, { needs: ['3'], key: true });
};

scenarios.s2 = async (page) => {
  const S = 's2';
  await newSession(page);
  await ensureModel(page, 'image', 'GPT Image 2.5 Sunburst');

  await step(page, S, '1', '参数选 16:9 · 4 张，输入“黄昏雨后的废墟街角”', '4 张都是 16:9', async () => {
    await setParams(page, { size: '2048x1152', count: 4 });
    await send(page, '黄昏雨后的废墟街角');
    await waitImageResults(page, 1);
    await waitTurnEnd(page);
    const sizes = await page.locator('[data-testid=chat-image-result]').last().locator('img').evaluateAll((nodes) => nodes.filter((img) => img.naturalWidth > 0).map((img) => [img.naturalWidth, img.naturalHeight]));
    expect(sizes.length === 4, `候选图有 ${sizes.length} 张，不是 4 张`);
    const bad = sizes.filter(([w, h]) => Math.abs(w / h - 16 / 9) > 0.02);
    expect(bad.length === 0, `有 ${bad.length} 张不是 16:9：${JSON.stringify(sizes)}`);
    return `尺寸：${sizes.map((size) => size.join('x')).join('、')}`;
  }, { key: true });

  await step(page, S, '2', '在 1280×720 窗口按 F（聚焦模式已取消，R17）', '对话栏和右侧栏都还在，舞台宽度不变；舞台头没有“聚焦”按钮', async () => {
    // right-panel-principles.md R17 (Lex 2026-10-10): focus mode is gone. F is an ordinary key on the stage;
    // the chat column and the right panel stay, and the stage header has no focus button.
    await page.setViewportSize(SMALL);
    await page.waitForTimeout(600);
    await page.evaluate(() => document.activeElement instanceof HTMLElement && document.activeElement.blur());
    const widthOf = async () => Math.round((await page.locator('[data-testid=studio-stage]').boundingBox()).width);
    const before = await widthOf();
    await page.keyboard.press('f');
    await page.waitForTimeout(600);
    const afterF = await widthOf();
    const chatVisible = await page.locator('[data-testid=studio-chat-column]').isVisible().catch(() => false);
    const panelVisible = await page.locator('[data-testid=right-panel]').isVisible().catch(() => false);
    const focusButtons = await page.locator('[data-testid=stage-focus]').count();
    await shot(page, `${S}-2-no-focus`);
    await page.setViewportSize(BIG);
    await page.waitForTimeout(400);
    const detail = `按 F 后舞台宽 ${afterF}px（按之前 ${before}px），对话栏${chatVisible ? '还在' : '不见了'}，右侧栏${panelVisible ? '还在' : '不见了'}，聚焦按钮 ${focusButtons} 个`;
    expect(afterF === before && chatVisible && panelVisible && focusButtons === 0, detail);
    return detail;
  }, { needs: ['1'] });

  await step(page, S, '3', '选用一张，输入“同一构图，出 3 个天气：雪夜、正午、雾”', '3 个新版本作为分支堆叠显示', async () => {
    await page.locator('[data-testid=chat-image-result]').last().locator('[data-testid=candidate-pick]').first().click();
    await page.waitForTimeout(1000);
    await setParams(page, { count: 1 });
    await send(page, '同一构图，出 3 个天气：雪夜、正午、雾');
    await waitImageResults(page, 2);
    await waitTurnEnd(page);
    await waitStripSettled(page);
    await page.waitForTimeout(800);
    const tiles = await versionTexts(page);
    const stacked = await page.locator('[data-testid=version-stack]').count();
    const labels = tiles.join(' / ');
    expect(/2D v2/.test(labels) && /2D v3/.test(labels) && /2D v4/.test(labels), `版本条是“${labels}”，没有 v2、v3、v4 三个新版本`);
    expect(stacked > 0, `三个新版本没有堆叠成一组（版本条：${labels}）`);
    return `版本条：${labels}；堆叠组：${stacked}`;
  }, { needs: ['1'], key: true });

  await step(page, S, '4', '回形针上传草图；在附件芯片选“参考构图”', '芯片带缩略图；版本条和全部产出里标“上传”，不出现“来源未知”', async () => {
    const sketch = path.join(OUT, 'sketch-input.png');
    fs.writeFileSync(sketch, sketchPng());
    await page.locator('[data-testid=studio-composer] input[type=file]').setInputFiles(sketch);
    await page.waitForTimeout(1200);
    const thumbs = await page.locator('[data-testid=studio-composer] img[alt*="附件"], [data-testid=studio-composer] img').count();
    const roleChoices = await page.locator('[data-testid=studio-composer]').getByText(/参考构图|参考|构图/).count();
    fs.unlinkSync(sketch);
    const problems = [];
    if (thumbs === 0) problems.push('上传后输入框里没有缩略图');
    if (roleChoices === 0) {
      problems.push('附件芯片上没有“参考构图”之类的用途选择，只有缩略图和删除按钮');
      friction('上传的图在输入框里只是一枚缩略图，没有“参考构图 / 风格参考 / 要修改的图”的用途选择；用途只能写在话里。');
    }
    expect(problems.length === 0, problems.join('；'));
  }, { needs: ['1'], key: true });

  await step(page, S, '5', '输入“按草图的构图重画”', '结果来源连线指向草图和当前图；版本条里草图标“上传”，不出现“来源未知”', async () => {
    await send(page, '按草图的构图重画');
    await waitImageResults(page, 3);
    await waitTurnEnd(page);
    await waitStripSettled(page);
    await page.waitForTimeout(1000);
    const tiles = await versionTexts(page);
    const labels = tiles.join(' / ');
    const unknown = await page.locator('[data-testid=stage-source-unknown]').count();
    const linkTitles = await page.locator('[data-testid=version-link]').evaluateAll((nodes) => nodes.map((node) => `${node.getAttribute('data-link')}:${node.getAttribute('data-known')}:${node.getAttribute('data-full-label')}`));
    const problems = [];
    if (!/上传/.test(labels)) problems.push(`版本条没有标“上传”的草图：“${labels}”`);
    if (unknown > 0 || linkTitles.some((item) => /来源未知|:false:/.test(item))) problems.push(`出现来源未知：${linkTitles.join(' ; ')}`);
    await page.click('[data-testid=all-outputs-button]');
    await page.waitForSelector('[data-testid=all-outputs]');
    const outputsText = (await page.locator('[data-testid=all-outputs]').innerText()).replace(/\s+/g, ' ');
    await page.getByLabel('关闭全部产出').click();
    await page.waitForTimeout(300);
    if (!/上传/.test(outputsText)) problems.push(`“全部产出”里也没有标“上传”的项：“${outputsText.slice(0, 120)}”`);
    const reply = await lastAgentText(page);
    if (!/@img/.test(reply)) problems.push('Agent 回复没有写出基于哪些图');
    if (problems.length) friction('上传的草图在版本条里没有自己的版本，参考关系只在 Agent 回复第一行写“基于 @imgN”，版本之间的连线只有一条父子线。');
    expect(problems.length === 0, problems.join('；'));
    return `版本条：${labels}；连线：${linkTitles.join(' ; ')}`;
  }, { needs: ['3'], key: true });
  await step(page, S, '6', '舞台头点“比较”，放入雪夜和正午两版', '两格同步缩放；滑杆对比可用；Esc 退出', async () => {
    await page.click('[data-testid=stage-compare]');
    await page.waitForSelector('[data-testid=compare-view]');
    // Add two versions by clicking them in the strip (there is no drag).
    const tileButtons = page.locator('[data-testid=version-tile]');
    const snow = tileButtons.filter({ hasText: '2D v2' }).locator('button').first();
    const noon = tileButtons.filter({ hasText: '2D v3' }).locator('button').first();
    await snow.click();
    await page.waitForTimeout(300);
    await noon.click();
    await page.waitForTimeout(500);
    const problems = [];
    const panes = await page.locator('[data-testid=compare-pane]').count();
    if (panes < 2) problems.push(`比较里只有 ${panes} 格`);
    // Zoom: wheel over the first picture and see whether either picture changes size.
    const images = page.locator('[data-testid=compare-pane] img');
    const sizeOf = async (index) => (await images.nth(index).boundingBox());
    const before = [await sizeOf(0), await sizeOf(1)];
    await images.nth(0).hover();
    await page.mouse.wheel(0, -600);
    await page.waitForTimeout(400);
    const after = [await sizeOf(0), await sizeOf(1)];
    const zoomed = after[0].width !== before[0].width || after[1].width !== before[1].width;
    if (!zoomed) {
      problems.push('比较视图里滚轮没有缩放，两格也没有同步缩放');
      friction('比较视图的两格只能看图，不能缩放或平移，也没有“同步缩放”；要看细节只能双击放大成灯箱。');
    }
    await shot(page, `${S}-6-compare`);
    await page.click('[data-testid=compare-mode-slider]');
    await page.waitForTimeout(500);
    const slider = await page.locator('[data-testid=compare-slider]').count();
    if (!slider) problems.push('没有滑杆对比');
    else {
      const handle = page.locator('[data-testid=compare-slider-handle]');
      const start = await handle.getAttribute('aria-valuenow');
      await handle.focus();
      await page.keyboard.press('ArrowRight');
      const moved = await handle.getAttribute('aria-valuenow');
      if (start === moved) problems.push(`滑杆用键盘没有移动（${start}）`);
    }
    await shot(page, `${S}-6-slider`);
    await page.evaluate(() => document.activeElement instanceof HTMLElement && document.activeElement.blur());
    await page.keyboard.press('Escape');
    await page.waitForTimeout(500);
    if (await page.locator('[data-testid=compare-view]').count()) problems.push('按 Esc 没有退出比较');
    expect(problems.length === 0, problems.join('；'));
  }, { needs: ['3'], key: true });

  await step(page, S, '7', '蒙版修左下角一块，发送', '参数条不遮住图的上沿；只有该区域变化', async () => {
    await page.locator('[data-testid=version-tile]').filter({ hasText: '2D v4' }).locator('button').first().click();
    await page.waitForTimeout(900);
    await page.getByLabel('局部重绘').first().click();
    await page.waitForSelector('[data-testid=composer-mask-chip]', { timeout: 5000 });
    const area = page.locator('[data-canvas-drawing-area]');
    const box = await area.boundingBox();
    const cx = box.x + box.width / 2;
    const cy = box.y + box.height / 2;
    for (const dy of [70, 95, 120]) {
      await page.mouse.move(cx - 230, cy + dy);
      await page.mouse.down();
      for (let i = 0; i <= 8; i += 1) await page.mouse.move(cx - 230 + i * 12, cy + dy + (i % 2) * 6, { steps: 2 });
      await page.mouse.up();
    }
    await page.waitForTimeout(400);
    const problems = [];
    const barBottom = await page.getByText('笔刷大小').first().evaluate((node) => { let element = node; while (element && element.getBoundingClientRect().height < 20) element = element.parentElement; return element.getBoundingClientRect().bottom; });
    const pictureTop = await pictureTopOnBoard(page);
    if (pictureTop === null) problems.push('读不出画面上沿的位置，无法核对参数条是否遮挡');
    else if (barBottom > pictureTop) problems.push(`参数条下沿 ${Math.round(barBottom)}px 压到图的上沿 ${Math.round(pictureTop)}px`);
    await shot(page, `${S}-7-mask`);
    const referenceTile = await page.locator('[data-testid=version-tile]').filter({ hasText: '2D v4' }).locator('img').first().getAttribute('src');
    await page.evaluate(() => document.activeElement instanceof HTMLElement && document.activeElement.blur());
    await page.keyboard.press('Enter');
    const dialog = page.getByRole('dialog').filter({ hasText: '局部重绘' });
    await dialog.waitFor({ timeout: 5000 });
    await dialog.getByPlaceholder(/描述涂抹区域/).fill('左下角补一盏亮着的路灯');
    const tilesBefore = (await versionTexts(page)).length;
    await dialog.getByRole('button', { name: '准备本次重绘' }).click();
    await page.waitForFunction((n) => document.querySelectorAll('[data-testid=version-tile]').length > n, tilesBefore, { timeout: 30000 });
    await waitStripSettled(page);
    await page.waitForTimeout(1200);
    const newest = await page.locator('[data-testid=version-tile]').last().locator('img').first().getAttribute('src');
    const diff = await compareImages(page, referenceTile, newest).catch((error) => ({ error: error.message }));
    if (diff.error) problems.push(`对比图片出错：${diff.error}`);
    else if (!diff.sameSize) problems.push('新图和原图尺寸不同');
    else if (!(diff.changed > 0 && diff.changed < 0.2)) problems.push(`有 ${(diff.changed * 100).toFixed(1)}% 的像素变了，不是只改了蒙版区域`);
    expect(problems.length === 0, `${problems.join('；')}`);
    return `变化像素：${(diff.changed * 100).toFixed(1)}%`;
  }, { needs: ['3'], key: true });

  await step(page, S, '8', '下载当前版本', 'PNG 尺寸与右侧栏显示一致', async () => {
    const [download] = await Promise.all([
      page.waitForEvent('download', { timeout: 15000 }),
      page.locator('[data-testid=stage-header]').getByLabel('下载').click(),
    ]);
    const saved = await saveDownload(download, `${S}-8-download.png`);
    const header = fs.readFileSync(saved.file).subarray(0, 32);
    const width = header.readUInt32BE(16);
    const height = header.readUInt32BE(20);
    const details = await panelDetails(page);
    const shown = (details.match(/(\d{3,4})\s*[x×]\s*(\d{3,4})/) || [])[0];
    const problems = [];
    if (!shown) {
      problems.push(`右侧栏没有显示尺寸，没法核对（下载的 PNG 是 ${width}x${height}，文件名 ${saved.suggested}）`);
      friction('右侧栏没有图片尺寸（像素宽高），下载的 PNG 尺寸只能自己打开文件看。');
    } else if (!shown.replace(/\s/g, '').includes(`${width}x${height}`)) problems.push(`右侧栏写 ${shown}，下载的是 ${width}x${height}`);
    expect(problems.length === 0, problems.join('；'));
    return `${saved.suggested}：${width}x${height}`;
  }, { needs: ['1'], key: true });
};

scenarios.s3 = async (page) => {
  const S = 's3';
  await newSession(page);
  await ensureModel(page, 'image', 'GPT Image 2.5 Sunburst');

  await step(page, S, '0', '准备：生成一张图并做出三视图（同场景一的做法）', '三视图卡片三张全部完成', async () => {
    await generateOneImage(page, '赛博朋克女刺客，全身立绘');
    await makeThreeViews(page);
    const done = await page.locator('[data-testid=three-view-slot][data-status=succeeded]').count();
    expect(done === 3, `三视图只完成了 ${done} 张`);
  });

  await step(page, S, '1', '版本条点三视图组', '舞台显示三张；舞台头“转 3D”可点', async () => {
    await clickTile(page, /2D v2/);
    const to3d = page.locator('[data-testid=stage-to-3d]');
    const disabled = await to3d.isDisabled();
    const label = await stageLabel(page);
    expect(!disabled, `“转 3D”是灰的（舞台：${label}）`);
    return `舞台：${label}`;
  }, { needs: ['0'], key: true });

  await step(page, S, '2', '3D 选 Meshy 7.1，点“转 3D”（手工模式）', '批准卡片显示输入图标“正面”、约 35 积分、Meshy 当前余额；余额低于 35 时“批准”置灰', async () => {
    await pickModel(page, 'model', 'Meshy 7.1');
    await page.click('[data-testid=stage-to-3d]');
    await page.waitForSelector('[data-testid=studio-approval-card]', { timeout: 30000 });
    const card = page.locator('[data-testid=studio-approval-card]').last();
    const text = (await card.innerText()).replace(/\s+/g, ' ');
    const problems = [];
    if (!/35/.test(text)) problems.push(`批准卡片没有写 35 积分：“${text}”`);
    if (!/正面/.test(text)) problems.push('输入图只写句柄，没有写“正面”');
    if (!/余额/.test(text)) problems.push('批准卡片没有写 Meshy 当前余额（只在余额不够时才出现；顶栏有“MeshyAI：1,000点”）');
    if (problems.length) friction('3D 批准卡片的输入图只显示 @img 句柄和缩略图，没有“正面”字样；余额只有不够时才写在卡片上，平时要看顶栏。');
    expect(problems.length === 0, problems.join('；'));
    return `卡片：${text}`;
  }, { needs: ['1'], key: true });

  await step(page, S, '3', '批准', '进度卡显示百分比和已用时间；版本条出现进度环占位；顶栏任务数 1；舞台可继续操作其他版本', async () => {
    await page.click('[data-testid=studio-approval-approve]');
    await page.waitForTimeout(2500);
    const problems = [];
    const progress = (await page.locator('[data-testid=chat-messages]').innerText()).replace(/\s+/g, ' ');
    const percent = /\d+\s*%/.test(progress);
    const elapsed = /已用|用时|\d+\s*秒|\d+:\d\d/.test(progress);
    await page.click('[data-testid=workspace-task-center]');
    await page.waitForTimeout(700);
    const centerText = (await page.locator('[data-testid=workspace-task-card]').first().innerText().catch(() => '')).replace(/\s+/g, ' ');
    await shot(page, `${S}-3-task-center`);
    await page.keyboard.press('Escape');
    await page.waitForTimeout(300);
    if (!percent) {
      problems.push(`对话里只有“正在生成…”，没有百分比和已用时间的进度卡（任务进度面板里是“${centerText.slice(0, 80)}”）`);
      friction('3D 批准后，对话里只有“正在生成…”和“本步消耗 35 积分”，没有进度卡；百分比和状态要点开顶栏“任务进度”才看得到。');
    } else if (!elapsed) problems.push('进度没有已用时间');
    const ring = await page.locator('[data-testid=version-tile] [role=progressbar]').count();
    const placeholder = await page.locator('[data-testid=version-tile][data-status=running], [data-testid=version-tile][data-status=pending]').count();
    if (!ring && !placeholder) problems.push('版本条没有占位');
    else if (!ring) friction('3D 生成中版本条只有灰色占位和“生成中”，没有进度环（进度环只在有百分比时出现）。');
    const active = (await page.locator('[data-testid=workspace-active-count]').innerText().catch(() => '')).trim();
    if (active !== '1') {
      problems.push(`顶栏任务数是“${active || '无'}”，不是 1`);
      friction(`批准一个 3D 任务后，顶栏“任务进度”的数字是 ${active || '空'}，不是 1（同时在算对话回合和任务）。`);
    }
    expect(problems.length === 0, problems.join('；'));
    return `进度：${progress.slice(-80)}`;
  }, { needs: ['2'], key: true });

  await step(page, S, '4', '生成期间在舞台上的图开蒙版涂抹（规格写 2D v1）；任务完成', '舞台不自动切换；出现“3D v1 已完成 [查看]”小条', async () => {
    // Painting happens on the object that is on the stage (the three views). Going to another
    // version first would drop the pending "show me the result" (seen in an earlier run).
    const onStage = await stageLabel(page);
    await page.getByLabel('局部重绘').first().click();
    await page.waitForSelector('[data-testid=composer-mask-chip]', { timeout: 5000 });
    await paintChest(page);
    await waitTile(page, /3D v1/);
    await page.waitForTimeout(1500);
    const label = await stageLabel(page);
    const pill = await page.locator('[data-testid=stage-pending-pill]').count();
    const problems = [];
    if (label !== onStage) problems.push(`舞台自动从“${onStage}”切到了“${label}”`);
    if (!pill) problems.push('没有“已完成，查看”小条');
    expect(problems.length === 0, problems.join('；'));
    return `舞台仍是：${label}；小条文字：${(await page.locator('[data-testid=stage-pending-pill]').innerText().catch(() => '')).replace(/\s+/g, ' ')}`;
  }, { needs: ['3'], key: true });

  await step(page, S, '5', '点“查看”', '舞台进入 3D；左侧贴图/白模/线框可切；右下显示面数和贴图尺寸，或“—”', async () => {
    await page.locator('[data-testid=stage-pending-pill]').getByRole('button', { name: /查看/ }).click().catch(async () => { await page.locator('[data-testid=stage-pending-pill]').click(); });
    await page.waitForSelector('[data-testid=stage-3d]', { timeout: 15000 });
    await page.waitForTimeout(4000);
    const problems = [];
    const stats = (await page.locator('[data-testid=stage-model-stats]').innerText().catch(() => '')).replace(/\s+/g, ' ');
    if (!/三角面\s*[\d.]+/.test(stats)) problems.push(`没有面数：“${stats}”`);
    const toggles = await page.locator('[data-testid=stage-3d] button, [data-testid=stage-3d] [role=radio], [data-testid=stage-3d] [role=tab]').count();
    if (toggles === 0) {
      problems.push('舞台左侧没有 贴图 / 白模 / 线框 的切换');
      friction('3D 舞台只有旋转查看和左上角的面数、顶点、材质、贴图数量；没有 贴图 / 白模 / 线框 的切换，也没有贴图尺寸。');
    }
    if (!/\d+\s*x\s*\d+|\d+×\d+/.test(stats)) problems.push('右下没有贴图尺寸（面数在左上角，贴图只给了数量）');
    expect(problems.length === 0, `${problems.join('；')}（面数信息：${stats}）`);
  }, { needs: ['4'], key: true });

  await step(page, S, '6', '再用 Tripo H3 生成一个 3D（批准卡片上没有“同时再生成”的勾选，改成手动换模型再转）', '两个 3D 版本作为 2D 的分支并列；比较视图里可同时转', async () => {
    // Accepted decisions (section 6): Tripo H3's extra credits are agreed on the one approval card in the chat
    // (it lists base and extra credits), and the approval card has no "also generate with another 3D model" box (that is a later item).
    const accepted = '取舍已接受：额外积分写在同一张批准卡片上（基础积分、额外积分、合计），点批准即同意；批准卡片上不要求“同时再生成”的勾选。';
    const problems = [];
    await clickTile(page, /2D v2/);
    await pickModel(page, 'model', 'Tripo H3');
    await page.click('[data-testid=stage-to-3d]');
    const approvalShown = await page.waitForSelector('[data-testid=studio-approval-card]', { timeout: 15000 }).then(() => true).catch(() => false);
    if (approvalShown) {
      await page.click('[data-testid=studio-approval-approve]');
    } else {
      problems.push('用 Tripo H3 转 3D 没有出现批准卡片');
      friction('选 Tripo H3 再点“转 3D”，没有批准卡片：服务端要先看到额外积分同意。');
      await pickModel(page, 'model', 'Meshy 7.1');
      await page.click('[data-testid=stage-to-3d]');
      await page.waitForSelector('[data-testid=studio-approval-card]', { timeout: 30000 });
      await page.click('[data-testid=studio-approval-approve]');
    }
    await waitTile(page, /3D v2/);
    await page.waitForTimeout(800);
    const tiles = (await versionTexts(page)).join(' / ');
    const links = await page.locator('[data-testid=version-link]').evaluateAll((nodes) => nodes.map((node) => node.getAttribute('data-full-label')));
    // Compare: a model cannot be put in the compare view (and the header has no compare button while a model is on the stage).
    const compareOnModel = await page.locator('[data-testid=stage-compare]').count();
    if (!compareOnModel) problems.push('舞台上是 3D 模型时，舞台头没有“比较”按钮');
    await clickTile(page, /2D v2/);
    await page.click('[data-testid=stage-compare]');
    await page.waitForSelector('[data-testid=compare-view]');
    await page.locator('[data-testid=version-tile]').filter({ hasText: /3D v1/ }).locator('button').first().click();
    await page.waitForTimeout(500);
    const skipped = await page.getByText(/3D 模型不能放进比较/).count();
    await page.keyboard.press('Escape');
    await page.waitForTimeout(400);
    if (skipped) problems.push('比较视图不接受 3D 模型（提示“3D 模型不能放进比较，请选图片版本”），没法并排看两个模型');
    friction('两个 3D 模型没法并排比较：比较视图只收图片，点 3D 版本只给一句提示。');
    if (!(/3D v1/.test(tiles) && /3D v2/.test(tiles))) problems.push(`版本条里不是两个 3D 版本：“${tiles}”`);
    expect(problems.length === 0, `${accepted}${problems.join('；')}。版本条：${tiles}；连线：${links.join('、')}`);
    return `${accepted}版本条：${tiles}；连线：${links.join('、')}`;
  }, { needs: ['5'], key: true });
  await step(page, S, '7', '右侧栏“加工”页勾选重拓扑并开始', '不弹批准；直接开始；完成后版本条出现 3D 新版本，连线写“重拓扑”；处理链状态显示“重拓扑 ✓”', async () => {
    await clickTile(page, /3D v1/);
    await rightPanelTab(page, 'process');
    const panel = page.locator('[data-testid=model-processing-panel]');
    await panel.getByText('重拓扑', { exact: true }).first().click();
    await page.waitForTimeout(500);
    const cost = (await page.locator('[data-testid=processing-cost]').innerText()).replace(/\s+/g, ' ');
    const approvalsBefore = await page.locator('[data-testid=studio-approval-card]').count();
    await panel.getByRole('button', { name: /确认步骤与费用/ }).click();
    await page.waitForTimeout(1500);
    const problems = [];
    if ((await page.locator('[data-testid=studio-approval-card]').count()) > approvalsBefore) problems.push('开始处理时弹出了批准卡片');
    if (!/5/.test(cost)) problems.push(`费用提示是“${cost}”，没有 5 积分`);
    await page.waitForFunction(() => /处理计划已完成|已完成/.test(document.querySelector('[data-testid=model-processing-panel]')?.textContent || ''), null, { timeout: 60000 });
    const chain = (await panel.innerText()).replace(/\s+/g, ' ');
    if (!/重拓扑\s*已完成/.test(chain)) problems.push(`处理链状态没有“重拓扑 已完成”：“${chain.slice(0, 100)}”`);
    await page.waitForTimeout(2500);
    const tilesLive = (await versionTexts(page)).join(' / ');
    if (!/3D v2/.test(tilesLive)) {
      problems.push(`处理完成后版本条没有新版本（“${tilesLive}”），刷新页面后才出现`);
      friction('处理（重拓扑、绑骨、动画）完成后，版本条不会自己多出新版本，要刷新页面才出现；处理任务没有登记到当前会话。');
    }
    await reloadStudio(page);
    const tiles = (await versionTexts(page)).join(' / ');
    const links = await page.locator('[data-testid=version-link]').evaluateAll((nodes) => nodes.map((node) => node.getAttribute('data-full-label')));
    if (!links.some((title) => /重拓扑/.test(title ?? ''))) {
      problems.push(`连线写的是“${links.join('、')}”，没有写“重拓扑”`);
      friction('处理出来的版本，连线只写“由 3D v1 处理而来”，不说是重拓扑、绑骨还是动画。');
    }
    if (!/3D v2/.test(tiles)) problems.push(`刷新后版本条仍没有 3D v2：“${tiles}”`);
    expect(problems.length === 0, `${problems.join('；')}（刷新后版本条：${tiles}）`);
  }, { needs: ['5'], key: true });

  await step(page, S, '8', '勾选动画生成（会自动连带骨骼绑定）并选“待机”', '勾选动画生成后自动连带骨骼绑定，一次提交两步，按顺序执行；绑骨和动画各生成一个版本', async () => {
    // Accepted decision (section 6): ticking 动画生成 also ticks 骨骼绑定; both are sent at once and run in order.
    // "Animation greyed out until rigging is done" is no longer required.
    const accepted = '取舍已接受：勾选动画生成自动连带骨骼绑定，两步按顺序执行，不再要求绑骨完成前置灰。';
    await clickTile(page, /3D v1/);
    await rightPanelTab(page, 'process');
    const panel = page.locator('[data-testid=model-processing-panel]');
    const animation = panel.getByText('动画生成', { exact: true }).first();
    await animation.click();
    await page.waitForTimeout(700);
    const planText = (await panel.innerText()).replace(/\s+/g, ' ');
    const rigChosen = await panel.getByRole('checkbox', { name: '骨骼绑定' }).isChecked().catch(() => false);
    const problems = [];
    // Pick the idle action.
    const select = panel.locator('select').last();
    const options = await select.locator('option').allInnerTexts();
    const idle = options.find((option) => /待机|Idle/i.test(option));
    if (!idle) problems.push(`动作列表里没有“待机”：${options.slice(0, 6).join('、')}`);
    else await select.selectOption({ label: idle });
    await page.waitForTimeout(400);
    await panel.getByRole('button', { name: /确认步骤与费用/ }).click();
    await page.waitForFunction(() => /动画生成\s*已完成/.test(document.querySelector('[data-testid=model-processing-panel]')?.textContent || ''), null, { timeout: 120000 }).catch(() => {});
    await page.waitForTimeout(2000);
    await reloadStudio(page);
    const tiles = (await versionTexts(page)).join(' / ');
    const count = (tiles.match(/3D v\d/g) || []).length;
    if (count < 4) problems.push(`刷新后只有 ${count} 个 3D 版本（“${tiles}”），应有 原图生 3D、重拓扑、绑骨、动画 四个`);
    expect(problems.length === 0, `${accepted}${problems.join('；')}（计划：${planText.slice(0, 160)}）`);
    return `${accepted}骨骼绑定${rigChosen ? '已自动勾上' : '没有自动勾上'}；版本条：${tiles}`;
  }, { needs: ['7'], key: true });

  await step(page, S, '9', '在绑骨版本上打开格式页', '只列 FBX 和 GLB；FBX 行有单独下载', async () => {
    const tiles = await versionTexts(page);
    // The rigged version is the third 3D version in strip order.
    const modelTiles = page.locator('[data-testid=version-tile]').filter({ hasText: /3D v\d/ });
    const total = await modelTiles.count();
    let found = null;
    const seen = [];
    for (let i = 0; i < total; i += 1) {
      await modelTiles.nth(i).locator('button').first().click();
      await page.waitForTimeout(700);
      await rightPanelTab(page, 'formats');
      const rows = await page.locator('[data-testid^=model-format-]:not([data-testid=model-formats]):not([data-testid=model-formats-panel]):not([data-testid=model-formats-complete]):not([data-testid=model-formats-converting]):not([data-testid=model-formats-empty]):not([data-testid=model-formats-paused]):not([data-testid=model-formats-failed])').evaluateAll((nodes) => nodes.filter((node) => node.querySelector('a[href]')).map((node) => node.getAttribute('data-testid').replace('model-format-', '')));
      seen.push(`${await stageLabel(page)}：${rows.join('、') || '无'}`);
      const sorted = rows.map((row) => row.toLowerCase()).sort().join(',');
      if (sorted === 'fbx,glb') { found = { index: i, rows, label: await stageLabel(page) }; break; }
    }
    expect(found !== null, `没有找到只列 FBX 和 GLB 的绑骨版本（${tiles.join(' / ')}）；各版本可下载的格式：${seen.join(' | ')}`);
    const problems = [];
    const names = found.rows.map((row) => row.toLowerCase()).sort();
    if (!(names.includes('fbx') && names.includes('glb') && names.length === 2)) problems.push(`${found.label} 的格式页列了：${names.join('、')}`);
    const fbxDownload = await page.locator('[data-testid=model-format-fbx] a').count();
    if (!fbxDownload) problems.push('FBX 行没有单独下载');
    expect(problems.length === 0, problems.join('；'));
    return `${found.label}：${names.join('、')}`;
  }, { needs: ['8'], key: true });

  await step(page, S, '10', 'Meshy 原始模型的格式页：glTF 不可用；补齐格式；下载 ZIP', 'glTF 标不可用并写原因；“补齐格式 约 N 积分”不弹批准，直接开始；ZIP 下载成功，README 列出缺失格式', async () => {
    await clickTile(page, /3D v1/);
    await rightPanelTab(page, 'formats');
    const panel = page.locator('[data-testid=model-formats-panel]');
    const gltf = (await page.locator('[data-testid=model-format-gltf]').innerText().catch(() => '')).replace(/\s+/g, ' ');
    const problems = [];
    if (!/不可用/.test(gltf) || !/没有适用|原因|转换/.test(gltf)) problems.push(`glTF 行是“${gltf}”`);
    const complete = panel.getByRole('button', { name: /补齐格式/ });
    if (!(await complete.count())) problems.push('没有“补齐格式”按钮');
    else {
      const label = (await complete.first().innerText()).replace(/\s+/g, ' ');
      if (!/\d/.test(label)) problems.push(`补齐按钮没有写积分：“${label}”`);
      const approvalsBefore = await page.locator('[data-testid=studio-approval-card]').count();
      await complete.first().click();
      await page.waitForTimeout(1500);
      if ((await page.locator('[data-testid=studio-approval-card]').count()) > approvalsBefore) problems.push('补齐格式弹出了批准卡片');
      await page.waitForFunction(() => !document.querySelector('[data-testid=model-formats-converting]'), null, { timeout: 60000 }).catch(() => {});
      await page.waitForTimeout(1500);
    }
    const link = page.locator('[data-testid=model-archive-link]');
    let zipNote = '';
    if (!(await link.count())) problems.push('没有“下载全部（ZIP）”');
    else {
      const [download] = await Promise.all([page.waitForEvent('download', { timeout: 30000 }), link.first().click()]);
      const saved = await saveDownload(download, `${S}-10-formats.zip`);
      const entries = zipEntries(fs.readFileSync(saved.file));
      zipNote = `${saved.suggested}（${saved.size} 字节）：${entries.join('、')}`;
      const readme = entries.find((name) => /readme/i.test(name));
      if (!readme) problems.push(`ZIP 里没有 README：${entries.join('、')}`);
      else {
        const body = zipRead(fs.readFileSync(saved.file), readme);
        zipNote += `；README：${body.replace(/\s+/g, ' ').slice(0, 200)}`;
        if (!/glTF|gltf/i.test(body)) problems.push('README 没有写出缺少 glTF');
      }
    }
    expect(problems.length === 0, `${problems.join('；')}（glTF：${gltf.slice(0, 60)}；${zipNote}）`);
    return zipNote;
  }, { needs: ['8'], key: true });
};

// ---------------------------------------------------------------------------
// scenario 4: the 2D <-> 3D loop

async function stageHandle(page) {
  return (await page.locator('[data-testid=stage-handle]').first().innerText().catch(() => '')).trim();
}

async function linkTitles(page) {
  return page.locator('[data-testid=version-link]').evaluateAll((nodes) => nodes.map((node) => node.getAttribute('data-full-label') || ''));
}

async function pendingApprovals(page) {
  return page.locator('[data-testid=studio-approval-card][data-status=pending]').count();
}

/** Sends a message that needs approval and waits for a new approval card. */
async function askForApproval(page, text) {
  const before = await page.locator('[data-testid=studio-approval-card]').count();
  await send(page, text);
  await page.waitForFunction((n) => document.querySelectorAll('[data-testid=studio-approval-card]').length > n, before, { timeout: 40000 });
  await page.waitForTimeout(400);
  return page.locator('[data-testid=studio-approval-card]').last();
}

async function setApprovalMode(page, label) {
  await page.click('[data-testid=studio-approval-menu]');
  await page.waitForTimeout(300);
  await page.getByRole('radio', { name: new RegExp(label) }).first().click();
  await page.waitForTimeout(500);
}

scenarios.s4 = async (page) => {
  const S = 's4';
  await newSession(page);
  await ensureModel(page, 'image', 'GPT Image 2.5 Sunburst');
  let imageHandle = '';
  let mergedLabel = '';

  await step(page, S, '0', '准备：生成一张图，转成 3D（Meshy，手工批准），舞台停在 3D v1', '舞台是 3D v1', async () => {
    await generateOneImage(page, '赛博朋克女刺客，全身立绘');
    imageHandle = await stageHandle(page);
    await pickModel(page, 'model', 'Meshy 7.1');
    await page.click('[data-testid=stage-to-3d]');
    await page.waitForSelector('[data-testid=studio-approval-card]', { timeout: 30000 });
    await page.click('[data-testid=studio-approval-approve]');
    await waitTile(page, /3D v1/);
    await clickTile(page, /3D v1/);
    await page.waitForSelector('[data-testid=stage-3d]', { timeout: 15000 });
    await page.waitForTimeout(3500);
    const label = await stageLabel(page);
    expect(/3D v1/.test(label), `舞台是“${label}”`);
    return `舞台：${label}；图的句柄：${imageHandle}`;
  }, { key: true });

  await step(page, S, '1', '舞台在 3D v1，点“截图到 2D”', '0 积分；新版本 2D v2 来源写“3D v1 截图”；右侧栏显示真实截图尺寸', async () => {
    const credits = await sessionCredits(page);
    const before = await page.locator('[data-testid=version-tile]').count();
    await page.getByRole('button', { name: '截图到 2D' }).first().click();
    await page.waitForTimeout(4500);
    const problems = [];
    const after = await page.locator('[data-testid=version-tile]').count();
    const label = await stageLabel(page);
    const kind = await page.locator('[data-testid=stage-header]').getAttribute('data-kind').catch(() => '');
    const tiles = (await versionTexts(page)).join(' / ');
    const links = await linkTitles(page);
    if (after <= before) {
      problems.push(`版本条没有新版本（“${tiles}”）；截图只落在画布上，舞台头写“${label}”（对象类型 ${kind}）`);
      friction('点“截图到 2D”后，截图只是落在画布上，版本条没有 2D v2，舞台头仍写“3D v1”，来源也不写“3D v1 截图”；要再点“合并为新版本”才会成为版本。');
    } else if (!links.some((title) => /3D v1/.test(title) && /截图/.test(title))) {
      problems.push(`连线写的是“${links.join('、')}”，没有“3D v1 截图”`);
    }
    const afterCredits = await sessionCredits(page);
    if (afterCredits !== credits) problems.push(`本会话积分从 ${credits} 变成 ${afterCredits}`);
    const details = await panelDetails(page);
    const size = (details.match(/(\d{3,4})\s*[x×]\s*(\d{3,4})/) || [])[0];
    if (!size) problems.push('右侧栏没有显示截图尺寸');
    expect(problems.length === 0, `${problems.join('；')}（连线：${links.join('、')}）`);
    return `${label}；积分 ${credits} -> ${afterCredits}；尺寸：${size}`;
  }, { needs: ['0'], key: true });

  await step(page, S, '2', '用画笔在胸口画，点“合并为新版本（0 积分）”', '出现 2D v3，名字带“手绘”', async () => {
    const brush = page.getByRole('button', { name: '画笔', exact: true }).first();
    await brush.click();
    await page.waitForTimeout(500);
    await paintChest(page);
    await shot(page, `${S}-2-painted`);
    const credits = await sessionCredits(page);
    const merge = page.locator('[data-testid=stage-merge]');
    const problems = [];
    if (!(await merge.count())) problems.push('画完后舞台头没有“合并为新版本”按钮');
    else {
      const label = (await merge.first().innerText()).replace(/\s+/g, ' ');
      if (!/0 积分/.test(label)) problems.push(`按钮写的是“${label}”，没写 0 积分`);
      const before = await page.locator('[data-testid=version-tile]').count();
      await merge.first().click();
      await page.waitForFunction((n) => document.querySelectorAll('[data-testid=version-tile]').length > n, before, { timeout: 30000 }).catch(() => problems.push('点合并后版本条没有新版本'));
    }
    await page.waitForFunction(() => [...document.querySelectorAll('[data-testid=version-tile]')].some((tile) => /手绘/.test(tile.textContent || '')), null, { timeout: 12000 }).catch(() => {});
    await page.waitForTimeout(500);
    const tiles = (await versionTexts(page)).join(' / ');
    mergedLabel = (await stageLabel(page)).trim();
    if (!/2D v3/.test(tiles)) problems.push(`版本条里没有 2D v3：“${tiles}”（合并出来的叫“${mergedLabel}”）`);
    if (!/手绘/.test(tiles + (await linkTitles(page)).join(' '))) {
      problems.push(`版本条里没有“手绘”字样：“${tiles}”`);
      friction('合并出来的版本在版本条里先只叫“2D v2”，没有“手绘”字样；刷新页面后才出现“手绘”，号码也变了。');
    }
    const after = await sessionCredits(page);
    if (after !== credits) problems.push(`合并花了积分：${credits} -> ${after}`);
    expect(problems.length === 0, `${problems.join('；')}（版本条：${tiles}）`);
    return `版本条：${tiles}`;
  }, { needs: ['1'], key: true });

  let cardBeforeWait = null;
  await step(page, S, '3', '在 2D v3 上找“用这张图换贴图”，没有就用对话要求“用这张图给 @model1 换贴图”', '批准卡片写明“整套贴图重生，不能只改局部”，约 10 积分；先选目标模型（默认 3D v1）', async () => {
    const numbers = (await versionTexts(page)).map((text) => Number((text.match(/2D v(\d+)/) || [])[1] || 0));
    const newest = Math.max(...numbers);
    await clickTile(page, new RegExp(`2D v${newest}\\b`)).catch(() => {});
    const handle = await stageHandle(page);
    const problems = [];
    const direct = await page.getByRole('button', { name: /用这张图换贴图/ }).count();
    if (!direct) {
      problems.push('2D 图的舞台头没有“用这张图换贴图”按钮');
      friction('2D 图上没有“用这张图换贴图”的入口；只能在对话里写“用 @img 给 @model 换贴图”，或者切到 3D 版本点“换贴图”。');
    }
    if (!handle) friction('合并出来的“手绘”版本没有 @img 句柄（舞台头不显示），没法在对话里用句柄点名“用这张图”。');
    const card = await askForApproval(page, `用${handle || '这张图'} 给 @model1 换贴图`);
    const text = (await card.innerText()).replace(/\s+/g, ' ');
    cardBeforeWait = text;
    if (!/10/.test(text)) problems.push(`批准卡片没有写约 10 积分：“${text}”`);
    if (!/整套|不能只改局部/.test(text)) {
      problems.push('批准卡片没有“整套贴图重生，不能只改局部”的说明');
      friction('换贴图的批准卡片没有说明这是“整套贴图重生”，用户可能以为只改局部。');
    }
    if (!/@model1|3D v1/.test(text)) {
      problems.push('批准卡片没写目标模型是哪一个（只在输入图里有句柄）');
    }
    expect(problems.length === 0, `${problems.join('；')}（卡片：${text.slice(0, 200)}）`);
    return `卡片：${text}`;
  }, { needs: ['2'], key: true });

  await step(page, S, '4', '不批准，继续发两条无关消息，等 130 秒后点批准', '已持久化则正常提交；已过期则按钮是“重新发起”，点它能重新提交；不得静默失败', async () => {
    const problems = [];
    const sentAt = Date.now();
    const lockNotes = [];
    const sendOne = async (text) => {
      const box = composerBox(page);
      if (await box.isDisabled().catch(() => false)) {
        const waited = Date.now();
        await page.waitForFunction(() => !document.querySelector('[data-testid=studio-composer] textarea')?.disabled, null, { timeout: 140000 }).catch(() => {});
        lockNotes.push(`输入框等批准时被锁住约 ${Math.round((Date.now() - waited) / 1000)} 秒`);
      }
      await send(page, text);
      await page.waitForTimeout(2500);
      return (await chatText(page)).includes(text.slice(0, 8));
    };
    const first = await sendOne('记住：主角是红发女剑士，红黑配色');
    const second = await sendOne('再记住：背景是霓虹雨夜，镜头偏仰');
    if (!first || !second) problems.push(`无关消息没有都出现在对话里（${first}/${second}）`);
    if (lockNotes.length) {
      problems.push(lockNotes.join('；'));
      friction(`挂着待批准卡片时，这一轮对话还在等批准，输入框被锁住（${lockNotes.join('；')}），没法边等边发别的消息。`);
    }
    const stillPending = await pendingApprovals(page);
    if (!stillPending) problems.push('发无关消息后，待批准卡片不见了');
    // Wait until 130 s after the card appeared, noting when it flips.
    let expiredAfter = null;
    while (Date.now() - sentAt < 130000) {
      const state = await page.locator('[data-testid=studio-approval-card]').last().getAttribute('data-status').catch(() => null);
      if (state === 'expired' && expiredAfter === null) expiredAfter = Math.round((Date.now() - sentAt) / 1000);
      await page.waitForTimeout(2000);
    }
    await shot(page, `${S}-4-after-wait`);
    const card = page.locator('[data-testid=studio-approval-card]').last();
    const state = await card.getAttribute('data-status').catch(() => null);
    const validity = (await card.locator('[data-testid=studio-approval-validity]').innerText().catch(() => '')).replace(/\s+/g, ' ');
    const beforeTiles = (await versionTexts(page)).length;
    let path = '';
    if (state === 'expired') {
      path = `卡片在约 ${expiredAfter ?? '?'} 秒时变“已过期”`;
      await card.locator('[data-testid=studio-approval-rerequest]').click();
      await page.waitForTimeout(800);
      const prefilled = await composerBox(page).inputValue();
      if (!/重新发起/.test(prefilled)) problems.push('点“重新发起”后输入框没有预填');
      else {
        problems.push('“重新发起”只是把话预填进输入框，还要自己再按 Enter，并不会直接重新冻结提交');
        friction('过期的批准卡片点“重新发起”，只是把一句话填进输入框，要再按一次 Enter 才会重新发起；规格写的是点它就重新冻结并提交。');
        await page.keyboard.press('Enter');
        await page.waitForFunction(() => document.querySelectorAll('[data-testid=studio-approval-card][data-status=pending]').length > 0, null, { timeout: 40000 }).catch(() => problems.push('重新发起后没有出现新的待批准卡片'));
        const fresh = page.locator('[data-testid=studio-approval-card][data-status=pending] [data-testid=studio-approval-approve]').last();
        if (await fresh.count()) await fresh.click();
      }
    } else if (state === 'pending') {
      path = `130 秒后卡片仍待批准（${validity}）`;
      await card.locator('[data-testid=studio-approval-approve]').click();
    } else problems.push(`130 秒后找不到批准卡片（状态 ${state}）`);
    const approvedAt = Date.now();
    await page.waitForTimeout(1500);
    const outcomeText = (await chatText(page)).slice(-300);
    let appearedAfter = null;
    await page.waitForFunction((n) => document.querySelectorAll('[data-testid=version-tile]').length > n, beforeTiles, { timeout: 45000 }).then(() => { appearedAfter = Math.round((Date.now() - approvedAt) / 1000); }).catch(() => {});
    if (appearedAfter === null) {
      // The result may be there and only the strip is stale: reload and look again.
      await reloadStudio(page);
      const afterReload = (await versionTexts(page)).length;
      if (afterReload > beforeTiles) {
        problems.push('晚批准后 45 秒内版本条没有新版本，刷新页面后才出现（任务其实已完成）');
        friction('对话已经结束后再点“批准”，界面写“已批准，开始生成”，但新版本一直不出现，也没有进度；刷新页面后才发现任务早就完成了。');
      } else problems.push('晚批准后刷新页面，版本条仍没有新版本');
    } else if (appearedAfter > 25) {
      friction(`批准换贴图后，新的 3D 版本过了约 ${appearedAfter} 秒才出现在版本条里（模拟服务商只要约 8 秒）；等待期间版本条没有占位。`);
    }
    await waitStripSettled(page, 90000).catch(() => {});
    await page.waitForTimeout(1500);
    const tiles = (await versionTexts(page)).join(' / ');
    expect(problems.length === 0, `${problems.join('；')}（${path}；版本条：${tiles}；对话末尾：${outcomeText.slice(-120)}）`);
    return `${path}；批准后版本条：${tiles}${appearedAfter !== null ? `（${appearedAfter} 秒后出现）` : ''}`;
  }, { needs: ['3'], key: true });

  await step(page, S, '5', '挂着一张待批准卡片，把顶栏批准方式切到自动', '弹二次确认；确认后那张卡片仍然待批准，没有被扣费', async () => {
    const handle = await stageHandle(page) || imageHandle;
    await askForApproval(page, `用 ${handle} 给 @model1 换贴图`);
    const credits = await sessionCredits(page);
    const pendingBefore = await pendingApprovals(page);
    const problems = [];
    await page.click('[data-testid=studio-approval-menu]');
    await page.waitForTimeout(300);
    await page.getByRole('radio', { name: /自动/ }).first().click();
    await page.waitForTimeout(500);
    const confirmShown = await page.locator('[data-testid=studio-approval-confirm]').count();
    if (!confirmShown) problems.push('点“自动”后没有二次确认');
    else {
      await shot(page, `${S}-5-confirm`);
      await page.locator('[data-testid=studio-approval-confirm]').getByRole('button', { name: /确认切换/ }).click();
      await page.waitForTimeout(1500);
    }
    await page.keyboard.press('Escape');
    await page.waitForTimeout(600);
    const pendingAfter = await pendingApprovals(page);
    const modeLabel = (await page.locator('[data-testid=studio-approval-menu]').innerText()).replace(/\s+/g, ' ');
    const creditsAfter = await sessionCredits(page);
    if (pendingAfter < pendingBefore || pendingAfter === 0) problems.push(`切到自动后待批准卡片从 ${pendingBefore} 张变成 ${pendingAfter} 张`);
    if (!/自动/.test(modeLabel)) problems.push(`顶栏写的是“${modeLabel}”，没切到自动`);
    if (creditsAfter !== credits) problems.push(`积分从 ${credits} 变成 ${creditsAfter}，卡片被扣费了`);
    await shot(page, `${S}-5-auto`);
    // Back to manual for the next steps; the card that was left pending is turned down so the turn ends.
    await setApprovalMode(page, '手工');
    await page.keyboard.press('Escape');
    await page.waitForTimeout(400);
    const deny = page.locator('[data-testid=studio-approval-card][data-status=pending] [data-testid=studio-approval-deny]').last();
    if (await deny.count()) { await deny.click(); await page.waitForTimeout(1500); }
    await page.keyboard.press('Escape');
    await page.waitForTimeout(500);
    const modeBack = (await page.locator('[data-testid=studio-approval-menu]').innerText()).replace(/\s+/g, ' ');
    expect(problems.length === 0, `${problems.join('；')}（切回：${modeBack}）`);
    return `待批准 ${pendingBefore} -> ${pendingAfter}；积分 ${credits} -> ${creditsAfter}`;
  }, { needs: ['4'], key: true });

  await step(page, S, '6', '点 2D v1，输入“换成绿色披风”，点“从这里继续”', '舞台头提示“从旧版本继续”；新版本是 2D v1 的分支，不接在最新版本后面', async () => {
    await clickTile(page, /2D v1/);
    const problems = [];
    const older = await page.locator('[data-testid=stage-older]').count();
    const cont = await page.locator('[data-testid=stage-continue]').count();
    if (!older) problems.push('舞台头没有“旧版本”提示');
    if (!cont) problems.push('舞台头没有“从这里继续”按钮');
    const handle = await stageHandle(page);
    const linksBefore = await linkTitles(page);
    if (cont) await page.click('[data-testid=stage-continue]');
    await page.waitForTimeout(500);
    await shot(page, `${S}-6-armed`);
    const before = await page.locator('[data-testid=chat-image-result]').count();
    await send(page, '换成绿色披风');
    await waitImageResults(page, before + 1);
    await waitTurnEnd(page);
    await waitStripSettled(page);
    await page.waitForTimeout(800);
    let linksAfter = await linkTitles(page);
    const countV1 = (list) => list.filter((title) => /2D v1/.test(title)).length;
    let reloaded = false;
    if (countV1(linksAfter) <= countV1(linksBefore)) {
      // Give the strip a chance to catch up before calling it missing.
      await reloadStudio(page);
      reloaded = true;
      linksAfter = await linkTitles(page);
    }
    const added = linksAfter.filter((title) => !linksBefore.includes(title));
    if (countV1(linksAfter) <= countV1(linksBefore)) {
      problems.push(`新版本没有连线指向 2D v1（${reloaded ? '刷新后也' : ''}没有新增连线：${linksAfter.join('、')}），在版本条里是排在末尾的一个独立方块，不是 2D v1 的分支`);
      friction('点“从这里继续”后发的“换成绿色披风”，新版本在版本条里没有连线，排在最后，看不出是从 2D v1 分出来的。');
    }
    if (mergedLabel) {
      const mergedNow = ((await versionTexts(page)).find((text) => /手绘/.test(text)) || '').replace(/\s+/g, ' ');
      const nowNumber = (mergedNow.match(/2D v(\d+)/) || [])[1];
      const thenNumber = (mergedLabel.match(/2D v(\d+)/) || [])[1];
      if (nowNumber && thenNumber && nowNumber !== thenNumber) friction(`同一个“手绘”版本，刚合并时叫 2D v${thenNumber}，后来变成 2D v${nowNumber}：版本号会随新分支重排，跟对话里说过的“v${thenNumber}”对不上。`);
    }
    const reply = await lastAgentText(page);
    if (handle && !reply.includes(`基于 ${handle}`) && !(await chatText(page)).includes(`基于 ${handle}`)) problems.push(`Agent 回复里没有“基于 ${handle}”`);
    const tiles = (await versionTexts(page)).join(' / ');
    expect(problems.length === 0, `${problems.join('；')}（版本条：${tiles}；新增连线：${added.join('、')}）`);
    return `版本条：${tiles}；新增连线：${added.join('、')}`;
  }, { needs: ['4'], key: true });

  await step(page, S, '7', '余额不足：发起换贴图并批准，服务商返回余额不足', '失败卡片不出现“重试”和“换模型重试”，只出现“联系管理员”；批准卡片可见余额', async () => {
    const problems = [];
    await clickTile(page, /3D v1/);
    const card = await askForApproval(page, '给 @model1 换贴图，做成金属质感 [mock:insufficient_balance]');
    const beforeFailed = await page.locator('[data-testid=version-tile][data-status=failed]').count();
    await card.locator('[data-testid=studio-approval-approve]').click();
    await page.waitForFunction((n) => document.querySelectorAll('[data-testid=version-tile][data-status=failed]').length > n, beforeFailed, { timeout: 60000 }).catch(() => problems.push('批准后版本条没有出现失败的占位'));
    await page.waitForTimeout(1500);
    const chat = await chatText(page);
    if (!/余额不足|欠费/.test(chat)) {
      problems.push('对话里没有任何失败提示（失败只在版本条的红色占位和任务进度里）');
      friction('余额不足的失败，对话里没有提示（Mock Agent 收尾话是固定的“已完成”，真实 Agent 会看到工具失败，这一条要用真实模型再看一次）；版本条只有红色“3D 失败”占位，要点开“任务进度”才看得到原因。');
    }
    await page.click('[data-testid=workspace-task-center]');
    await page.waitForTimeout(900);
    const cards = page.locator('[data-testid=workspace-task-card]');
    let failedText = '';
    let failedButtons = [];
    for (let index = 0; index < await cards.count(); index += 1) {
      const text = (await cards.nth(index).innerText()).replace(/\s+/g, ' ');
      if (/失败/.test(text)) {
        failedText = text;
        failedButtons = await cards.nth(index).locator('button').evaluateAll((nodes) => nodes.map((node) => (node.getAttribute('aria-label') || node.textContent || '').trim()));
        break;
      }
    }
    await shot(page, `${S}-7-task-center`);
    await page.keyboard.press('Escape');
    await page.waitForTimeout(400);
    if (!failedText) problems.push('任务进度里找不到失败的任务');
    if (failedButtons.some((text) => /重试|换模型/.test(text))) problems.push(`失败卡片有“${failedButtons.filter((text) => /重试|换模型/.test(text)).join('、')}”按钮`);
    if (failedText && !/联系管理员/.test(failedText)) problems.push('失败卡片没有写“联系管理员”');
    expect(problems.length === 0, `${problems.join('；')}（失败卡片：${failedText.slice(0, 160)}；按钮：${failedButtons.join('、') || '无'}）`);
    return `失败卡片：${failedText.slice(0, 160)}；按钮：${failedButtons.join('、') || '无'}`;
  }, { needs: ['5'], key: true });

  await step(page, S, '8', '舞台在 3D v1，芯片是 @model1；手打“用 @img 去背景”', '芯片变灰并提示以所写句柄为准；Agent 回复写“基于 @img”；去背景作用在那张图上', async () => {
    await clickTile(page, /3D v1/);
    const chipBefore = (await page.locator('[data-testid=composer-object-chip]').innerText().catch(() => '')).replace(/\s+/g, ' ');
    const handle = imageHandle || '@img1';
    const box = composerBox(page);
    await box.click();
    await box.fill(`用 ${handle} 去背景`);
    await page.waitForTimeout(500);
    const chip = page.locator('[data-testid=composer-object-chip]');
    const skipped = await chip.getAttribute('data-skipped').catch(() => null);
    const chipText = (await chip.innerText().catch(() => '')).replace(/\s+/g, ' ');
    await shot(page, `${S}-8-typed`);
    const problems = [];
    if (skipped !== 'true') {
      problems.push(`输入句柄后芯片没有变灰（芯片：“${chipText}”），也没有“以 ${handle} 为准”的提示`);
      friction('手打了 @img 句柄，输入框上方的 @model1 芯片不会自动变灰，也没有提示本轮以哪个对象为准；要用户自己点芯片上的 x 才跳过。');
    }
    const before = await page.locator('[data-testid=chat-image-result]').count();
    await page.keyboard.press('Enter');
    await waitImageResults(page, before + 1).catch(() => problems.push('没有出现去背景的结果'));
    await waitTurnEnd(page);
    await waitStripSettled(page).catch(() => {});
    const chat = await chatText(page);
    if (!chat.includes(`基于 ${handle}`)) problems.push(`Agent 回复里没有“基于 ${handle}”`);
    const links = await linkTitles(page);
    const tiles = (await versionTexts(page)).join(' / ');
    expect(problems.length === 0, `${problems.join('；')}（开始时芯片：${chipBefore}；连线：${links.slice(-3).join('、')}；版本条：${tiles}）`);
    return `芯片：${chipBefore}；回复里有“基于 ${handle}”`;
  }, { needs: ['0'], key: true });

  await step(page, S, '9', '[mock:slow] 慢任务期间继续聊天、切别的版本', '输入框不锁；版本条占位有进度；聊天可继续；完成后有“查看”小条或自动出现', async () => {
    const problems = [];
    const started = Date.now();
    const before = await page.locator('[data-testid=version-tile]').count();
    await send(page, '画一个中世纪骑士，半身像 [mock:slow]');
    await page.waitForTimeout(3000);
    const running = await page.locator('[data-testid=version-tile][data-status=running], [data-testid=version-tile][data-status=pending]').count();
    if (!running) problems.push('慢任务开始后版本条没有占位');
    const lockedAt3 = await composerBox(page).isDisabled().catch(() => false);
    const stopShown = await page.locator('[data-testid=composer-stop]').count();
    // Switching versions must work while the turn waits for the task.
    await clickTile(page, /2D v1/);
    const switched = await stageLabel(page);
    if (!/2D v1/.test(switched)) problems.push(`慢任务期间切版本失败，舞台是“${switched}”`);
    let lockSeconds = 0;
    if (lockedAt3) {
      const lockStart = Date.now();
      await page.waitForFunction(() => !document.querySelector('[data-testid=studio-composer] textarea')?.disabled, null, { timeout: 90000 }).catch(() => {});
      lockSeconds = Math.round((Date.now() - lockStart) / 1000) + 3;
      problems.push(`慢任务期间输入框被锁住约 ${lockSeconds} 秒（只剩“停止”键），不能边等边聊天`);
      friction(`一个慢任务（约 20 秒）整轮占着对话：输入框被锁住约 ${lockSeconds} 秒，只能点“停止”；规格希望边等边聊。`);
    }
    await send(page, '记住：骑士的盾牌是蓝色');
    await page.waitForTimeout(2500);
    if (!(await chatText(page)).includes('骑士的盾牌')) problems.push('锁解开后发的消息没有出现在对话里');
    const percent = await page.locator('[data-testid=version-tile] [role=progressbar]').count();
    await shot(page, `${S}-9-slow`);
    await page.waitForFunction((n) => document.querySelectorAll('[data-testid=version-tile]').length > n && ![...document.querySelectorAll('[data-testid=version-tile]')].some((t) => ['running', 'pending', 'queued'].includes(t.getAttribute('data-status') || '')), before, { timeout: 90000 }).catch(() => problems.push('慢任务 90 秒内没有完成'));
    const took = Math.round((Date.now() - started) / 1000);
    await page.waitForTimeout(1200);
    const pill = (await page.locator('[data-testid=stage-pending-pill]').innerText().catch(() => '')).replace(/\s+/g, ' ');
    const problemsText = problems.join('；');
    expect(problems.length === 0, `${problemsText}（用时 ${took} 秒；进度环 ${percent}；停止键 ${stopShown}；小条：${pill}）`);
    return `用时 ${took} 秒；进度环 ${percent}；完成后小条：${pill || '无'}`;
  }, { needs: ['0'], key: true });

  await step(page, S, '10', '刷新页面，再看会话列表', '会话列表有缩略图和时间；进入后舞台停在离开时的当前对象；全部产出按日期分组', async () => {
    const labelBefore = await stageLabel(page);
    await reloadStudio(page);
    const labelAfter = await stageLabel(page);
    const problems = [];
    if (labelBefore && labelAfter !== labelBefore) problems.push(`刷新前舞台是“${labelBefore}”，刷新后是“${labelAfter || '空'}”`);
    await page.click('[data-testid=studio-session-menu]');
    await page.waitForTimeout(600);
    const rows = await page.locator('[data-testid=studio-session-row]').count();
    const covers = await page.locator('[data-testid=studio-session-cover]').count();
    const times = await page.locator('[data-testid=studio-session-time]').count();
    const firstTime = (await page.locator('[data-testid=studio-session-time]').first().innerText().catch(() => '')).trim();
    await shot(page, `${S}-10-sessions`);
    if (!rows) problems.push('会话列表是空的');
    if (rows && covers < rows) problems.push(`${rows} 个会话只有 ${covers} 个缩略图`);
    if (rows && times < rows) problems.push(`${rows} 个会话只有 ${times} 个时间`);
    await page.keyboard.press('Escape');
    await page.waitForTimeout(400);
    await page.click('[data-testid=all-outputs-button]');
    await page.waitForSelector('[data-testid=all-outputs]');
    const outputs = (await page.locator('[data-testid=all-outputs]').innerText()).replace(/\s+/g, ' ');
    const grouped = /今天|昨天|\d{1,2}\s*月\s*\d{1,2}\s*日|\d{4}[-/.年]\d{1,2}/.test(outputs);
    await shot(page, `${S}-10-outputs`);
    if (!grouped) {
      problems.push('全部产出里没有按日期分组的标题');
      friction('“全部产出”没有按日期分组，只是按版本排成一片。');
    }
    await page.keyboard.press('Escape');
    await page.waitForTimeout(400);
    expect(problems.length === 0, `${problems.join('；')}（${rows} 个会话，缩略图 ${covers}，时间 ${times}，第一个时间“${firstTime}”）`);
    return `舞台：${labelAfter}；${rows} 个会话`;
  }, { needs: ['0'], key: true });

  await step(page, S, '11', '找“交给同事”的入口', '界面上没有假的分享按钮；若已接入交接包，ZIP 含文件、提示词、来源链 README', async () => {
    const candidates = await page.evaluate(() => [...document.querySelectorAll('button, a, [role=menuitem]')]
      .map((node) => `${node.getAttribute('aria-label') || ''} ${node.textContent || ''}`.replace(/\s+/g, ' ').trim())
      .filter((text) => /分享|交给同事|交接|share|handoff|协作|邀请/i.test(text)));
    await page.click('[data-testid=studio-session-menu]');
    await page.waitForTimeout(400);
    const inMenu = await page.locator('[data-testid=studio-session-row]').first().hover().then(() => page.evaluate(() => [...document.querySelectorAll('[role=menu] *, [data-radix-popper-content-wrapper] *')]
      .map((node) => (node.getAttribute('aria-label') || node.textContent || '').trim()).filter((text) => /分享|交给同事|交接|share|邀请/i.test(text)))).catch(() => []);
    await page.keyboard.press('Escape');
    expect(candidates.length === 0 && inMenu.length === 0, `界面上有疑似分享入口：${[...candidates, ...inMenu].join('、')}`);
    return '没有找到分享或交接入口（也没有假按钮）';
  }, { needs: ['0'] });
};

// ---------------------------------------------------------------------------
// main

async function main() {
  if (process.env.REPORT_ONLY === '1') {
    // Rebuild README.md from an earlier results.json without running the browser again.
    const saved = JSON.parse(fs.readFileSync(path.join(OUT, 'results.json'), 'utf8'));
    results.push(...saved.results);
    frictions.push(...saved.frictions);
    ONLY.splice(0, ONLY.length, ...saved.ran);
    writeReports({ pageErrors: saved.pageErrors ?? [], totalMs: saved.totalMs ?? 0 });
    return;
  }
  const browser = await chromium.launch({ args: LAUNCH_ARGS });
  const context = await browser.newContext({ viewport: BIG, acceptDownloads: true });
  const page = await context.newPage();
  const pageErrors = [];
  page.on('pageerror', (error) => pageErrors.push(error.message));
  await login(page);
  const started = Date.now();
  for (const key of ONLY) {
    if (!scenarios[key]) continue;
    console.log(`--- ${key}`);
    // A fresh page for each scenario, so an overlay left open by one cannot block the next.
    await page.goto(`${BASE}/`);
    await page.waitForSelector('[data-testid=studio-shell]', { timeout: 20000 });
    await page.waitForTimeout(1500);
    try {
      await scenarios[key](page);
    } catch (error) {
      console.log(`SCENARIO ERROR ${key}: ${error.message}`);
      results.push({ scenario: key, id: 'x', title: '场景脚本中断', condition: '', status: 'fail', ms: 0, shot: '', shotSmall: '', note: String(error.message).slice(0, 300) });
    }
  }
  await browser.close();
  writeReports({ pageErrors, totalMs: Date.now() - started });
}

const SCENARIO_TITLES = {
  s1: '场景一：2D 角色美术',
  s2: '场景二：2D 场景美术',
  s3: '场景三：3D 角色美术',
  s4: '场景四：2D↔3D 协作往返',
};

// Things seen while building this script that no single step records on its own.
const STATIC_FRICTIONS = [
  '登录后页面直接回到上次的会话，但 2D 和 3D 的模型选择器是空的，新会话也一样，每次都要先选模型才能发。',
  '单张下载的文件名是 asset_<id>.png，不带版本号和对象名。',
];

const NOT_EXERCISED = [
  'Provider 账户余额低于本步费用时，批准卡片上“批准”置灰并写余额：Mock 的余额固定 1000，走不到。代码和单元测试里有。',
  '“同意等待 120 秒”的覆盖：不改代码缩短不了，所以没覆盖；场景四第 4 步是真的等了 130 秒。',
  '真实服务商的 ZIP、README 内容和 Meshy 的真实序号：Mock 的 ZIP 是按同样的框架造的，内容不是真实的。',
  '失败时真实 Agent 的收尾话：Mock Agent 的收尾话是固定的“已完成”。',
  '“关掉浏览器，第二天打开”：用页面刷新代替，没有隔夜。',
  '触屏和手机宽度：这次只跑了 1440x900 和 1280x720。',
];

function mdCell(text, limit = 420) {
  const flat = String(text ?? '').replace(/\s+/g, ' ').replace(/\|/g, '/').trim();
  return flat.length > limit ? `${flat.slice(0, limit)}…` : flat;
}

function resultLabel(record) {
  const seconds = (record.ms / 1000).toFixed(1);
  if (record.status === 'pass') return `通过（${seconds} 秒）`;
  if (record.status === 'blocked') return `受阻（${seconds} 秒）`;
  return `未通过（${seconds} 秒）`;
}

const STATUS_WORD = { pass: '通过', fail: '未通过', blocked: '受阻' };

/** A before/after table against an earlier results.json (COMPARE_WITH), step by step. Empty without it. */
function compareLines() {
  const file = process.env.COMPARE_WITH;
  if (!file || !fs.existsSync(file)) return [];
  const before = JSON.parse(fs.readFileSync(file, 'utf8'));
  const earlier = new Map(before.results.map((record) => [`${record.scenario}-${record.id}`, record]));
  const word = (record) => (record ? STATUS_WORD[record.status] ?? record.status : '没有');
  const lines = ['## 和上一轮比较', '', `上一轮：${path.basename(path.dirname(file))}。下面是每个场景的通过数，和每一步的变化。`, ''];
  lines.push('| 场景 | 上一轮通过 | 这一轮通过 |', '| --- | --- | --- |');
  let beforeTotal = 0;
  let afterTotal = 0;
  for (const key of ONLY) {
    const now = results.filter((record) => record.scenario === key);
    if (!now.length) continue;
    const old = before.results.filter((record) => record.scenario === key);
    const oldPassed = old.filter((record) => record.status === 'pass').length;
    const nowPassed = now.filter((record) => record.status === 'pass').length;
    beforeTotal += oldPassed;
    afterTotal += nowPassed;
    lines.push(`| ${SCENARIO_TITLES[key] ?? key} | ${oldPassed} / ${old.length} | ${nowPassed} / ${now.length} |`);
  }
  lines.push(`| 合计 | ${beforeTotal} | ${afterTotal} |`, '');
  lines.push('| 步骤 | 上一轮 | 这一轮 |', '| --- | --- | --- |');
  for (const record of results) {
    const old = earlier.get(`${record.scenario}-${record.id}`);
    const change = old?.status === record.status ? '' : record.status === 'pass' ? '（新通过）' : '（退步）';
    lines.push(`| ${record.scenario} 第 ${record.id} 步：${mdCell(record.title, 50)} | ${word(old)} | ${word(record)}${change} |`);
  }
  lines.push('');
  return lines;
}

function buildReadme({ pageErrors, totalMs }) {
  const lines = [];
  const total = results.length;
  const passed = results.filter((r) => r.status === 'pass').length;
  const failedRecords = results.filter((r) => r.status === 'fail');
  const blockedRecords = results.filter((r) => r.status === 'blocked');
  lines.push(`# 验收跑 ${process.env.RUN_NUMBER || '1'}：新版 Studio 前端`, '');
  lines.push('用真实界面把设计稿 §5 的四个场景走了一遍。没有拦截请求，没有改页面。', '');
  lines.push(`- 共 ${total} 步，通过 ${passed} 步，未通过 ${failedRecords.length} 步，受阻 ${blockedRecords.length} 步。`);
  lines.push(`- 总用时约 ${Math.round(totalMs / 60000)} 分钟。`);
  lines.push('- 环境：本地服务，Provider 和 Agent 都是 Mock，数据库是 sqlite，Chromium 无头，窗口 1440x900。关键画面另有 1280x720。');
  lines.push('- 复现：`BASE=http://127.0.0.1:18200 ADMIN_USERNAME=... ADMIN_PASSWORD=... node scripts/acceptance/studio-scenarios.mjs`');
  lines.push('- “未通过”是指界面和 §5 的通过条件不一致。“受阻”是前面的步骤没走通，这一步没法做。', '');
  lines.push(...compareLines());
  lines.push('## 总览', '', '| 场景 | 通过 | 未通过 | 受阻 |', '| --- | --- | --- | --- |');
  for (const key of ONLY) {
    const rows = results.filter((r) => r.scenario === key);
    if (!rows.length) continue;
    lines.push(`| ${SCENARIO_TITLES[key] ?? key} | ${rows.filter((r) => r.status === 'pass').length} | ${rows.filter((r) => r.status === 'fail').length} | ${rows.filter((r) => r.status === 'blocked').length} |`);
  }
  lines.push('');
  for (const key of ONLY) {
    const rows = results.filter((r) => r.scenario === key);
    if (!rows.length) continue;
    lines.push(`## ${SCENARIO_TITLES[key] ?? key}`, '', '| 步骤 | 结果 | 截图 | 备注 |', '| --- | --- | --- | --- |');
    for (const record of rows) {
      const shots = [record.shot ? `[1440](${record.shot})` : '', record.shotSmall ? `[1280](${record.shotSmall})` : ''].filter(Boolean).join(' ');
      const stepCell = `${record.id}. ${mdCell(record.title, 120)}<br>通过条件：${mdCell(record.condition, 200)}`;
      lines.push(`| ${stepCell} | ${resultLabel(record)} | ${shots} | ${mdCell(record.note)} |`);
    }
    lines.push('');
  }
  const referenced = new Set(results.flatMap((r) => [r.shot, r.shotSmall]).filter(Boolean));
  const extras = fs.readdirSync(OUT).filter((name) => !referenced.has(name) && !['README.md', 'results.json'].includes(name)).sort();
  if (extras.length) {
    lines.push('## 其他截图和下载物', '', '步骤里中途拍的画面，和下载下来的文件。', '');
    for (const name of extras) lines.push(`- [${name}](${name})`);
    lines.push('');
  }
  lines.push('## 未通过和受阻的步骤', '');
  if (!failedRecords.length && !blockedRecords.length) lines.push('没有。', '');
  for (const record of [...failedRecords, ...blockedRecords]) {
    lines.push(`- ${record.scenario} 第 ${record.id} 步（${mdCell(record.title, 60)}）：${mdCell(record.note, 300)}`);
  }
  lines.push('', '## 体验摩擦清单', '', '这些不一定算失败。是用户会感到别扭的地方。', '');
  for (const item of frictions) lines.push(`- ${item.text}（${item.scenario} 第 ${item.step} 步）`);
  for (const text of STATIC_FRICTIONS) lines.push(`- ${text}`);
  lines.push('', '## 这次没有验证到的', '');
  for (const text of NOT_EXERCISED) lines.push(`- ${text}`);
  if (pageErrors.length) {
    lines.push('', '## 页面报错', '');
    for (const text of [...new Set(pageErrors)].slice(0, 10)) lines.push(`- ${mdCell(text, 300)}`);
  }
  lines.push('');
  return lines.join('\n');
}

function writeReports({ pageErrors, totalMs }) {
  fs.writeFileSync(path.join(OUT, 'results.json'), JSON.stringify({ base: BASE, ran: ONLY, totalMs, pageErrors, results, frictions }, null, 2));
  fs.writeFileSync(path.join(OUT, 'README.md'), buildReadme({ pageErrors, totalMs }));
  console.log(`results: ${results.filter((r) => r.status === 'pass').length}/${results.length} passed`);
}

await main();
