#!/usr/bin/env node
/**
 * Real-browser check for #177: while the reader is at the end of the message list, the list must not
 * jump up when the thinking status is replaced by streamed text, while the reply streams, or when the
 * live reply settles into its stored messages.
 *
 * Needs a local server with mock providers, the mock Agent and mock login (no credentials, no paid
 * calls):
 *   APP_ENV=development PROVIDER_MODE=mock AGENT_MODE=mock AUTH_MODE=development DEV_MOCK_AUTH=true \
 *   DEV_MOCK_AUTH_BYPASS_USER_STATE=true APP_ADDR=127.0.0.1:18326 WEB_ASSETS_ROOT=build/go-web \
 *   DATABASE_PATH=<scratch>/a.db STORAGE_ROOT=<scratch>/storage LOG_DIR=<scratch>/logs go run ./cmd/art-ai
 * then
 *   BASE=http://127.0.0.1:18326 node scripts/test-chat-scroll-jump.mjs
 *
 * Optional env: ENGINES (default "chromium,webkit"), VIEWPORTS (default "1366x768"), OUT (report.json
 * and the raw samples). Every request outside BASE is refused and counted. [mock:markdown] makes the
 * mock Agent stream a reply in six pieces, 150 ms apart, after showing the thinking status.
 *
 * Per engine and viewport, after two warm-up turns make the list scroll:
 *  1. The third turn shows the thinking status, then streams text; every animation frame records
 *     scrollTop, scrollHeight and clientHeight.
 *  2. While the reader follows, the end stays in view (gap at most 8 px) in every sample.
 *  3. The list never loses the turn (the message count never drops before the stored page arrives).
 *  4. scrollTop never moves up while the content only grows; a shrink may only come with a shorter list.
 */
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { mkdirSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const root = fileURLToPath(new URL('..', import.meta.url));
function loadPlaywright() {
  const candidates = [process.env.PLAYWRIGHT_MODULE_PATH, path.join(root, 'package.json'), path.join(process.cwd(), 'package.json'), path.join(process.env.HOME || '', 'package.json')];
  for (const candidate of candidates.filter(Boolean)) {
    try {
      return candidate.endsWith('package.json') ? createRequire(candidate)('playwright') : createRequire(path.join(root, 'package.json'))(candidate);
    } catch { /* try the next place */ }
  }
  throw new Error('playwright not found: set PLAYWRIGHT_MODULE_PATH');
}
const pw = loadPlaywright();

const BASE = (process.env.BASE || '').replace(/\/$/, '');
if (!/^http:\/\/(127\.0\.0\.1|localhost|\[::1\]):\d+$/.test(BASE)) {
  console.error('BASE must be a loopback server with mock providers, e.g. http://127.0.0.1:18326');
  process.exit(2);
}
const ENGINES = (process.env.ENGINES || 'chromium,webkit').split(',').map((item) => item.trim()).filter(Boolean);
const VIEWPORTS = (process.env.VIEWPORTS || '1366x768').split(',').map((item) => item.trim().split('x').map(Number));
const OUT = process.env.OUT || mkdtempSync(path.join(tmpdir(), 'chat-scroll-'));
mkdirSync(OUT, { recursive: true });
const report = [];

const GAP_LIMIT = 8;

async function waitFor(page, check, what, timeout = 20000) {
  const started = Date.now();
  while (Date.now() - started < timeout) {
    if (await page.evaluate(check)) return;
    await page.waitForTimeout(50);
  }
  throw new Error(`timed out waiting for ${what}`);
}

async function sendTurn(page, words) {
  const box = page.locator('[data-testid=studio-composer] textarea');
  await box.fill(words);
  await page.locator('[data-testid=composer-send]').click({ timeout: 10000 });
  await waitFor(page, () => !document.querySelector('[data-testid=composer-stop]'), 'the turn to finish', 60000);
  await page.waitForTimeout(400);
}

try {
  for (const engine of ENGINES) {
    const browser = await pw[engine].launch({ headless: true });
    try {
      for (const [width, height] of VIEWPORTS) {
        const viewport = `${width}x${height}`;
        const context = await browser.newContext({ viewport: { width, height }, serviceWorkers: 'block' });
        const page = await context.newPage();
        const external = [];
        page.on('pageerror', (error) => external.push(`pageerror:${error.message}`));
        page.on('request', (request) => {
          const url = new URL(request.url());
          if (url.origin !== new URL(BASE).origin) external.push(url.origin);
        });
        await context.route('**/*', (route) => {
          const url = new URL(route.request().url());
          if (url.origin !== new URL(BASE).origin) { external.push(url.origin); return route.abort(); }
          return route.continue();
        });
        const user = `scroll-${engine}-${width}-${Date.now()}`;
        const login = await context.request.post(`${BASE}/api/auth/mock`, { data: { name: user, email: `${user}@example.test` } });
        assert.equal(login.status(), 200, 'mock login');

        await page.goto(BASE);
        await page.waitForSelector('[data-testid=studio-shell]', { timeout: 30000 });
        await page.waitForSelector('[data-testid=studio-composer] textarea', { timeout: 30000 });
        await sendTurn(page, '[mock:markdown] 第一轮');
        await sendTurn(page, '[mock:markdown] 第二轮');

        const before = await page.evaluate(() => {
          const list = document.querySelector('[data-testid=chat-messages]');
          return { top: list.scrollTop, height: list.scrollHeight, client: list.clientHeight, messages: document.querySelectorAll('[data-testid=chat-message]').length };
        });
        assert(before.height > before.client + 100, `the list must scroll before the measured turn (${JSON.stringify(before)})`);
        assert.equal(Math.round(before.height - before.top - before.client), 0, 'the end is in view before the measured turn');

        await page.evaluate(() => {
          window.__scrollSamples = [];
          window.__scrollStop = false;
          const tick = () => {
            const list = document.querySelector('[data-testid=chat-messages]');
            const bubble = document.querySelector('[data-testid=chat-status-bubble]');
            window.__scrollSamples.push({
              t: Math.round(performance.now()),
              top: Math.round(list.scrollTop),
              height: list.scrollHeight,
              client: list.clientHeight,
              bubble: bubble ? bubble.innerText : '',
              messages: document.querySelectorAll('[data-testid=chat-message]').length,
            });
            if (!window.__scrollStop) requestAnimationFrame(tick);
          };
          requestAnimationFrame(tick);
        });
        await sendTurn(page, '[mock:markdown] 第三轮');
        await page.waitForTimeout(500);
        const samples = await page.evaluate(() => { window.__scrollStop = true; return window.__scrollSamples; });

        assert(samples.length > 20, `too few animation frames (${samples.length})`);
        assert(samples.some((sample) => sample.bubble !== ''), 'the thinking status must show before the text');
        // The reader follows from the moment the sent turn is on screen.
        const firstTurn = samples.findIndex((sample) => sample.messages > before.messages);
        assert(firstTurn >= 0, 'the sent turn must appear while sampling');
        const followed = samples.slice(firstTurn);
        const baseline = followed[0].messages;
        let maxGap = 0;
        let countDrop = 0;
        let unexplainedUp = 0;
        let previous = followed[0];
        for (const sample of followed) {
          const gap = sample.height - sample.top - sample.client;
          maxGap = Math.max(maxGap, gap);
          if (sample.messages < baseline) countDrop = Math.max(countDrop, baseline - sample.messages);
          if (sample.top < previous.top - GAP_LIMIT && sample.height >= previous.height) unexplainedUp = Math.max(unexplainedUp, previous.top - sample.top);
          previous = sample;
        }
        const final = followed.at(-1);
        const finalGap = final.height - final.top - final.client;

        writeFileSync(path.join(OUT, `${engine}-${viewport}.json`), JSON.stringify({ before, samples }, null, 1));
        report.push({ engine, viewport, frames: samples.length, maxGap, countDrop, unexplainedUp, finalGap, bubble: samples.some((sample) => sample.bubble !== ''), external });

        assert.equal(external.length, 0, `${engine}/${viewport}: requests outside BASE or page errors: ${external.join(', ')}`);
        assert(maxGap <= GAP_LIMIT, `${engine}/${viewport}: the end left the view by ${maxGap}px while following`);
        assert.equal(countDrop, 0, `${engine}/${viewport}: the list dropped ${countDrop} messages before the stored turn arrived`);
        assert.equal(unexplainedUp, 0, `${engine}/${viewport}: scrollTop moved up ${unexplainedUp}px while the content grew`);
        assert(finalGap <= GAP_LIMIT, `${engine}/${viewport}: the list did not end at the end (${finalGap}px)`);
        console.log(`ok ${engine}/${viewport}: ${samples.length} frames, max gap ${maxGap}px, final gap ${finalGap}px`);
        await context.close();
      }
    } finally {
      await browser.close();
    }
  }
} finally {
  writeFileSync(path.join(OUT, 'report.json'), JSON.stringify(report, null, 2));
  console.log(`report: ${OUT}`);
}
