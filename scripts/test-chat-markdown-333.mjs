#!/usr/bin/env node
/**
 * Real-browser check for #333: an Agent reply shows its Markdown subset (bold, italic, lists, inline code,
 * code blocks, http(s) links in a new window) instead of raw marks, safely; the person's own words stay as typed.
 *
 * Needs a local server with mock providers, the mock Agent and mock login (no credentials, no paid
 * calls), e.g.
 *   APP_ENV=development PROVIDER_MODE=mock AGENT_MODE=mock AUTH_MODE=development DEV_MOCK_AUTH=true \
 *   DEV_MOCK_AUTH_BYPASS_USER_STATE=true APP_ADDR=127.0.0.1:18333 WEB_ASSETS_ROOT=build/go-web \
 *   DATABASE_PATH=<scratch>/a.db STORAGE_ROOT=<scratch>/storage LOG_DIR=<scratch>/logs go run ./cmd/art-ai
 * then
 *   BASE=http://127.0.0.1:18333 node scripts/test-chat-markdown-333.mjs
 *
 * Optional env: ENGINES (default "chromium,webkit"), VIEWPORTS (default "1366x768,1920x1080"),
 * LOCALES (default "zh,en"), OUT (screenshots and report.json; default a temp folder),
 * PLAYWRIGHT_MODULE_PATH. Every request outside BASE is refused and counted.
 *
 * "[mock:markdown]" in a message makes the mock Agent stream a fixed Markdown reply in pieces (one cut inside
 * a bold run, one inside inline code, one inside a link) with XSS lines: <script>, <img onerror>, javascript:
 * and data: links, HTML entities and picture syntax; it ends with the handles the message named.
 * Per engine, viewport and UI language, in a session with an uploaded picture (@img1):
 *  1. While the reply streams, no state of the reply shows "**", and nothing it carries runs.
 *  2. The finished reply shows strong/em/lists/code/one http(s) link with target=_blank and
 *     rel="noopener noreferrer"; the XSS lines are text; no picture, script or other link is made; the
 *     @img1 handle in bold is a tag. The person's message with Markdown marks stays as typed.
 *  3. After a reload (the stored history) the reply renders the same.
 *  4. Copying the whole reply puts its Markdown source on the clipboard; copying a part leaves the copy to
 *     the browser.
 *  5. No page errors and no outside requests.
 */
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { mkdtempSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import zlib from 'node:zlib';

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
  console.error('BASE must be a loopback server with mock providers, e.g. http://127.0.0.1:18333');
  process.exit(2);
}
const ENGINES = (process.env.ENGINES || 'chromium,webkit').split(',').map((item) => item.trim()).filter(Boolean);
const VIEWPORTS = (process.env.VIEWPORTS || '1366x768,1920x1080').split(',').map((item) => item.trim().split('x').map(Number));
const LOCALES = (process.env.LOCALES || 'zh,en').split(',').map((item) => item.trim()).filter(Boolean);
const OUT = process.env.OUT || mkdtempSync(path.join(tmpdir(), 'chat-markdown-333-'));
mkdirSync(OUT, { recursive: true });

const { application } = JSON.parse(readFileSync(path.join(root, 'internal/catalog/default_catalog.json'), 'utf8'));
const legacy = application.runtime.http.legacy_routes;
const localeCookie = application.localization.preference.cookie;
const englishGenerating = application.localization.bundles.en.messages['ui.chat.message.generating'];
const chineseGenerating = application.content.messages['ui.chat.message.generating'];

function png(width, height, seed) {
  const rows = [];
  for (let y = 0; y < height; y += 1) {
    const row = Buffer.alloc(1 + width * 3);
    for (let x = 0; x < width; x += 1) {
      row[1 + x * 3] = (x * 255 / width + seed * 40) & 255;
      row[2 + x * 3] = (y * 255 / height) & 255;
      row[3 + x * 3] = (seed * 90) & 255;
    }
    rows.push(row);
  }
  const chunk = (type, data) => {
    const length = Buffer.alloc(4);
    length.writeUInt32BE(data.length);
    const body = Buffer.concat([Buffer.from(type, 'ascii'), data]);
    const crc = Buffer.alloc(4);
    crc.writeUInt32BE(zlib.crc32(body));
    return Buffer.concat([length, body, crc]);
  };
  const header = Buffer.alloc(13);
  header.writeUInt32BE(width, 0);
  header.writeUInt32BE(height, 4);
  header[8] = 8; header[9] = 2;
  return Buffer.concat([
    Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]),
    chunk('IHDR', header), chunk('IDAT', zlib.deflateSync(Buffer.concat(rows))), chunk('IEND', Buffer.alloc(0)),
  ]);
}

async function api(context, method, route, data) {
  for (let attempt = 1; ; attempt += 1) {
    const response = await context.request.fetch(`${BASE}${route}`, { method, data, headers: { Origin: BASE } });
    if (response.status() === 429 && attempt < 5) {
      await new Promise((resolve) => setTimeout(resolve, 15000));
      continue;
    }
    const body = await response.json().catch(() => ({}));
    assert(response.ok(), `${method} ${route}: HTTP ${response.status()} ${JSON.stringify(body).slice(0, 200)}`);
    return body;
  }
}

async function login(context, user) {
  const response = await context.request.post(`${BASE}${legacy.auth_mock}`, { data: { name: user, email: `${user}@example.test` }, headers: { Origin: BASE } });
  assert.equal(response.status(), 200, 'mock login');
}

async function waitFor(fn, message, timeout = 30000) {
  const deadline = Date.now() + timeout;
  for (;;) {
    const value = await fn();
    if (value) return value;
    assert(Date.now() < deadline, message);
    await new Promise((resolve) => setTimeout(resolve, 300));
  }
}

const results = [];
async function record(engine, viewport, name, fn) {
  const started = Date.now();
  try {
    const note = await fn();
    results.push({ engine, viewport, name, passed: true, ms: Date.now() - started, ...(note ? { note } : {}) });
  } catch (error) {
    results.push({ engine, viewport, name, passed: false, ms: Date.now() - started, error: String(error?.message ?? error).split('\n')[0] });
  }
}

async function guardedContext(browser, viewport, outside, locale) {
  const context = await browser.newContext({ viewport, serviceWorkers: 'block', locale: locale === 'en' ? 'en-US' : 'zh-CN' });
  await context.route('**/*', (route) => {
    const url = route.request().url();
    if (url.startsWith(BASE) || url.startsWith('data:') || url.startsWith('blob:')) return route.continue();
    outside.push(url);
    return route.abort();
  });
  await context.addCookies([{ name: localeCookie, value: locale === 'en' ? 'en' : application.localization.default_locale, url: BASE }]);
  return context;
}

function studio(page) {
  const box = page.locator('[data-testid=studio-composer] textarea');
  const send = page.locator('[data-testid=composer-send]');
  const idle = () => page.waitForFunction(() => !document.querySelector('[data-testid=composer-stop]'), undefined, { timeout: 60000 });
  const say = async (words) => {
    for (let attempt = 1; ; attempt += 1) {
      await waitFor(async () => {
        if ((await box.inputValue()) !== words) await box.fill(words);
        await page.waitForTimeout(100);
        return (await box.inputValue()) === words && await send.isEnabled();
      }, `the box holds "${words}" and can send`, 20000);
      const posted = page.waitForResponse((response) => response.request().method() === 'POST' &&
        /\/api\/chat\/sessions\/[^/]+\/messages$/.test(new URL(response.url()).pathname), { timeout: 15000 });
      await send.click();
      const status = (await posted).status();
      if (status === 429 && attempt < 4) {
        await idle();
        await page.waitForTimeout(15000);
        continue;
      }
      assert.equal(status, 202, `sending "${words}"`);
      break;
    }
    await page.waitForFunction(() => document.querySelector('[data-testid=composer-stop]'), undefined, { timeout: 5000 }).catch(() => undefined);
    await idle();
  };
  return { box, idle, say };
}

/** What the last Agent reply shows, read from the page. */
const lastReply = (page) => page.evaluate(() => {
  const replies = document.querySelectorAll('[data-testid=chat-message][data-role=assistant] [data-chat-markdown]');
  const reply = replies[replies.length - 1];
  if (!reply) return null;
  const texts = (selector) => Array.from(reply.querySelectorAll(selector)).map((node) => node.textContent);
  return {
    text: reply.innerText,
    strong: texts('strong'),
    em: texts('em'),
    inlineCode: texts(':not(pre) > code'),
    pre: texts('pre'),
    orderedItems: reply.querySelectorAll('ol > li').length,
    nestedItems: reply.querySelectorAll('ol ul > li').length,
    bulletItems: reply.querySelectorAll(':scope > ul > li').length,
    links: Array.from(reply.querySelectorAll('a')).map((a) => ({ href: a.getAttribute('href'), target: a.getAttribute('target'), rel: a.getAttribute('rel'), text: a.textContent })),
    made: reply.querySelectorAll('script, img, iframe, object, embed, svg:not([aria-hidden]), b, i').length,
    attributes: Array.from(reply.querySelectorAll('*')).flatMap((node) => Array.from(node.attributes).map((attribute) => attribute.name)).filter((name) => name.startsWith('on')),
    handleInBold: Array.from(reply.querySelectorAll('strong [data-testid=chat-text-handle]')).map((node) => node.getAttribute('data-handle')),
    xss: window.__xss333 ?? null,
  };
});

function assertFinished(reply) {
  assert(reply, 'no rendered reply');
  assert(!reply.text.includes('**'), `raw ** in ${reply.text}`);
  for (const words of ['Markdown 检查 / check', '粗体', '注意：', '下划线粗体']) assert(reply.strong.includes(words), `strong ${JSON.stringify(reply.strong)} lacks ${words} in "${reply.text.slice(0, 80)}"`);
  assert(reply.strong.some((words) => words.includes('@img1')), `strong ${JSON.stringify(reply.strong)} lacks @img1`);
  assert.deepEqual(reply.em, ['斜体']);
  assert.deepEqual(reply.inlineCode, ['inline <code>', 'code']);
  assert.equal(reply.pre.length, 1);
  assert.equal(reply.pre[0].trim(), 'fmt.Println("<b>not bold</b>")');
  assert.equal(reply.orderedItems, 2);
  assert.equal(reply.nestedItems, 1);
  assert.equal(reply.bulletItems, 2);
  assert.deepEqual(reply.links, [{ href: 'https://example.com/docs?a=1&b=2', target: '_blank', rel: 'noopener noreferrer', text: '安全链接 safe link' }]);
  assert.equal(reply.made, 0, 'an element was made from the reply');
  assert.deepEqual(reply.attributes, [], 'event handler attributes');
  assert.deepEqual(reply.handleInBold, ['@img1']);
  assert.equal(reply.xss, null, 'reply script ran');
  for (const words of ['bad js', 'bad data', '<script>window.__xss333=1</script>', '<img src=x onerror="window.__xss333=2">', '&lt;b&gt;entity&lt;/b&gt; &amp;', 'image']) {
    assert(reply.text.includes(words), `text lacks ${words}`);
  }
  assert(!reply.text.includes('javascript:') && !reply.text.includes('data:text/html') && !reply.text.includes('example.com/x.png'), 'link addresses shown as text');
}

async function checks(engine, browser, [width, height], locale) {
  const viewport = `${width}x${height}-${locale}`;
  const outside = [];
  const context = await guardedContext(browser, { width, height }, outside, locale);
  try {
  const user = `markdown-333-${engine}-${viewport}-${Date.now()}`;
  await login(context, user);
  const session = await api(context, 'POST', legacy.chat_sessions, { title: `#333 ${engine} ${viewport}` });
  const page = await context.newPage();
  const errors = [];
  page.on('pageerror', (error) => errors.push(error.message));
  page.on('dialog', (dialog) => { errors.push(`dialog ${dialog.message()}`); void dialog.dismiss(); });
  await page.goto(`${BASE}/?session=${encodeURIComponent(session.id)}`);
  await page.waitForSelector('[data-testid=studio-shell]', { timeout: 30000 });
  const step = (name, fn) => record(engine, viewport, name, fn);
  const { say } = studio(page);
  const shot = (name) => page.screenshot({ path: path.join(OUT, `${engine}-${viewport}-${name}.png`) });

  await step('the UI is in the chosen language; an uploaded picture becomes @img1', async () => {
    const lang = await page.evaluate(() => document.documentElement.lang);
    assert(locale === 'en' ? lang.startsWith('en') : lang.startsWith('zh'), `html lang ${lang}`);
    await page.locator('input[type=file][multiple]').first().setInputFiles({ name: 'cat.png', mimeType: 'image/png', buffer: png(96, 96, 3) });
    await page.locator('[data-testid=composer-attachment-chip]').first().waitFor({ timeout: 20000 });
    await waitFor(async () => await page.locator('[data-testid=composer-send]').isEnabled(), 'the upload finishes', 20000);
    await say('记住：参考这张猫图');
    await page.locator('[data-testid=chat-sent-handle][data-handle="@img1"]').first().waitFor({ timeout: 20000 });
    return `lang ${lang}`;
  });

  const typed = '用 **Markdown** 总结 `@img1` 和 @img1 [mock:markdown]';
  let samples = [];
  await step('while the reply streams, no state shows ** and nothing it carries runs', async () => {
    await page.evaluate(() => {
      window.__mdSamples = [];
      const list = document.querySelector('[data-testid=chat-messages]');
      const sample = () => {
        const replies = list.querySelectorAll('[data-testid=chat-message][data-role=assistant]');
        const reply = replies[replies.length - 1];
        const words = reply?.querySelector('[data-chat-markdown]');
        if (!words) return;
        const text = words.innerText;
        if (window.__mdSamples[window.__mdSamples.length - 1]?.text !== text) {
          window.__mdSamples.push({ text, streaming: Boolean(document.querySelector('[data-testid=composer-stop]')), xss: window.__xss333 ?? null });
        }
      };
      window.__mdObserver = new MutationObserver(sample);
      window.__mdObserver.observe(list, { subtree: true, childList: true, characterData: true });
    });
    await say(typed);
    samples = await page.evaluate(() => {
      window.__mdObserver.disconnect();
      return window.__mdSamples;
    });
    const streamed = samples.filter((sample) => sample.streaming);
    assert(streamed.length >= 3, `only ${streamed.length} streaming states seen`);
    const raw = samples.find((sample) => sample.text.includes('**'));
    assert(!raw, `raw ** while streaming: ${raw?.text}`);
    assert(samples.every((sample) => sample.xss === null), 'reply script ran while streaming');
    const half = streamed.find((sample) => sample.text.includes('这是 粗') && !sample.text.includes('粗体'));
    return `${samples.length} states (${streamed.length} while streaming)${half ? '; saw the half bold as 粗' : ''}`;
  });

  await step('the finished reply shows the Markdown subset, its XSS lines as text; the person\'s words stay as typed', async () => {
    // The streamed message is swapped for the stored one when the turn ends; read the reply once it settled.
    await waitFor(async () => (await lastReply(page))?.handleInBold?.length === 1, 'the finished reply with its handle', 20000)
      .catch(async (error) => assert.fail(`${error.message}: last reply "${(await lastReply(page))?.text?.slice(0, 120)}"`));
    const reply = await lastReply(page);
    assertFinished(reply);
    const mine = page.locator('[data-testid=chat-message][data-role=user]').last();
    const words = await mine.innerText();
    assert(words.includes('用 **Markdown** 总结 `@img1`'), `user message ${words}`);
    assert.equal(await mine.locator('strong, code, a, [data-chat-markdown]').count(), 0, 'user message formatted');
    const spinner = await page.locator(`[aria-label="${locale === 'en' ? englishGenerating : chineseGenerating}"]`).count();
    assert.equal(spinner, 0, 'spinner left after the reply');
    await page.locator('[data-testid=chat-message][data-role=assistant]').last().scrollIntoViewIfNeeded();
    await shot('1-finished');
    return `strong ${reply.strong.length}, links ${reply.links.length}`;
  });

  await step('after a reload the stored reply renders the same', async () => {
    // The whole matrix comes from one client address, so the server's per-minute API limit may answer
    // 429 to the reloaded page's history read; the page is then loaded again after the window moves on.
    let reloads = 0;
    for (;;) {
      reloads += 1;
      await page.reload();
      await page.waitForSelector('[data-testid=studio-shell]', { timeout: 30000 });
      const shown = await waitFor(async () => (await lastReply(page))?.handleInBold?.length === 1, 'the stored reply with its handle', 20000)
        .then(() => true, async (error) => {
          if (reloads < 3) return false;
          return assert.fail(`${error.message}: last reply "${(await lastReply(page))?.text?.slice(0, 120)}"`);
        });
      if (shown) break;
      await page.waitForTimeout(20000);
    }
    assertFinished(await lastReply(page));
    await page.locator('[data-testid=chat-message][data-role=assistant]').last().scrollIntoViewIfNeeded();
    await shot('2-reloaded');
    return reloads > 1 ? `loaded again ${reloads - 1} time(s) after the API limit` : undefined;
  });

  await step('copying the whole reply gives its Markdown source; a part is left to the browser', async () => {
    const copied = await page.evaluate(() => {
      const replies = document.querySelectorAll('[data-testid=chat-message][data-role=assistant] [data-chat-markdown]');
      const reply = replies[replies.length - 1];
      const copy = (range) => {
        const selection = window.getSelection();
        selection.removeAllRanges();
        selection.addRange(range);
        const data = new DataTransfer();
        reply.dispatchEvent(new ClipboardEvent('copy', { clipboardData: data, bubbles: true, cancelable: true }));
        const kept = selection.toString();
        selection.removeAllRanges();
        return { plain: data.getData('text/plain'), html: data.getData('text/html'), kept };
      };
      const whole = document.createRange();
      whole.selectNodeContents(reply);
      const part = document.createRange();
      part.selectNodeContents(reply.querySelector('strong'));
      return { whole: copy(whole), part: copy(part) };
    });
    const source = copied.whole.plain;
    assert(source.startsWith('**Markdown 检查 / check**\n\n这是 **粗体**、*斜体* 和 `inline <code>`'), `copied ${source.slice(0, 80)}`);
    for (const words of ['1. 第一步 first', '   - 嵌套 nested', '```go', '[安全链接 safe link](https://example.com/docs?a=1&b=2)', '[bad js](javascript:alert(1))', '<script>window.__xss333=1</script>', '![image](https://example.com/x.png)', '**@img1**']) {
      assert(source.includes(words), `copied source lacks ${words}`);
    }
    assert(copied.whole.html.startsWith('<div>**Markdown') && !copied.whole.html.includes('<script>'), `html ${copied.whole.html.slice(0, 80)}`);
    assert(copied.whole.kept.length > 0, 'the selection was not restored');
    assert.equal(copied.part.plain, '', 'a partial copy was rewritten');
    return `${source.length} characters`;
  });

  await step('no page errors and no outside requests', async () => {
    assert.equal(errors.length, 0, `page errors: ${errors.join(' | ')}`);
    assert.equal(outside.length, 0, `outside requests: ${outside.join(' | ')}`);
  });
  } finally {
    await context.close();
  }
}

let blocks = 0;
for (const engine of ENGINES) {
  const browser = await pw[engine].launch();
  try {
    for (const viewport of VIEWPORTS) {
      for (const locale of LOCALES) {
        // The whole matrix comes from one client address, and the server allows
        // 1200 API requests a minute; space the ~155-request blocks apart.
        if (blocks) await new Promise((resolve) => setTimeout(resolve, 10000));
        await checks(engine, browser, viewport, locale);
        blocks += 1;
      }
    }
  } finally {
    await browser.close();
  }
}
writeFileSync(path.join(OUT, 'report.json'), `${JSON.stringify({ base: BASE, engines: ENGINES, viewports: VIEWPORTS.map(([w, h]) => `${w}x${h}`), locales: LOCALES, results }, null, 2)}\n`);
for (const result of results) console.log(`${result.passed ? 'PASS' : 'FAIL'} ${result.engine} ${result.viewport} ${result.name}${result.note ? ` — ${result.note}` : ''}${result.error ? ` — ${result.error}` : ''}`);
console.log(`report: ${path.join(OUT, 'report.json')}`);
process.exit(results.every((result) => result.passed) ? 0 : 1);
