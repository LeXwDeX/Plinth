#!/usr/bin/env node
/**
 * Captures the README screenshots in this directory from a local Mock stack (no keys, no cost).
 *
 * 1. Start an empty stack in Mock mode on a loopback address, for example the self-deploy
 *    compose stack (README "Docker Compose 自部署", default PROVIDER_MODE=mock / AGENT_MODE=mock),
 *    or a local build: APP_ENV=production AUTH_MODE=session PROVIDER_MODE=mock AGENT_MODE=mock
 *    SESSION_SECRET=… ADMIN_PASSWORD=… WEB_ASSETS_ROOT=build/go-web DATABASE_PATH=<tmp>/a.db
 *    STORAGE_ROOT=<tmp>/storage go run ./cmd/art-ai
 * 2. BASE=http://127.0.0.1:13333 ADMIN_PASSWORD=… node docs/screenshots/capture.mjs
 *
 * Per language (zh-CN, en) it signs in as the administrator and, in one session, asks the Mock
 * Agent for an image and a 3D model (approved), then for a second 3D model left waiting for
 * approval. It saves 1440×900 page screenshots (no browser chrome): workspace, formats,
 * approval, admin, as <name>-zh.png and <name>-en.png.
 * Optional env: ADMIN_USERNAME (default admin), OUT (default this directory), LOCALES
 * (default "zh-CN,en"), PLAYWRIGHT_MODULE_PATH. Requests to any origin other than BASE are
 * refused, and each file must stay under 400 KB.
 */
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { statSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const here = path.dirname(fileURLToPath(import.meta.url));
const root = path.resolve(here, '../..');

function loadPlaywright() {
  const candidates = [process.env.PLAYWRIGHT_MODULE_PATH, path.join(root, 'package.json'), path.join(process.env.HOME || '', 'package.json')];
  for (const candidate of candidates.filter(Boolean)) {
    try {
      return candidate.endsWith('package.json') ? createRequire(candidate)('playwright') : createRequire(path.join(root, 'package.json'))(candidate);
    } catch { /* try the next place */ }
  }
  throw new Error('playwright not found: install it or set PLAYWRIGHT_MODULE_PATH');
}

const BASE = (process.env.BASE || 'http://127.0.0.1:13333').replace(/\/$/, '');
if (!/^http:\/\/(127\.0\.0\.1|localhost|\[::1\]):\d+$/.test(BASE)) {
  console.error('BASE must be a loopback Mock stack, e.g. http://127.0.0.1:13333');
  process.exit(2);
}
const USER = process.env.ADMIN_USERNAME || 'admin';
const PASSWORD = process.env.ADMIN_PASSWORD || '';
if (!PASSWORD) {
  console.error('ADMIN_PASSWORD is required');
  process.exit(2);
}
const OUT = process.env.OUT || here;
const LOCALES = (process.env.LOCALES || 'zh-CN,en').split(',').map((item) => item.trim()).filter(Boolean);
const MAX_BYTES = 400 * 1024;

const TEXT = {
  'zh-CN': {
    suffix: 'zh',
    props: '道具：灯笼与木凳',
    image: '画一盏挂在木梁上的红色纸灯笼，暖色灯光，游戏道具风格',
    model: '做一个低多边形风格的木凳 3D 模型',
    chest: '做一个带金属包边的木制宝箱 3D 模型',
  },
  en: {
    suffix: 'en',
    props: 'Props: lantern and stool',
    image: 'Paint a red paper lantern hanging from a wooden beam, warm light, game prop style',
    model: 'Make a low-poly wooden stool as a 3D model',
    chest: 'Make a wooden treasure chest with metal trim as a 3D model',
  },
};

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

async function api(context, method, route, data) {
  const response = await context.request.fetch(`${BASE}${route}`, { method, data, headers: { Origin: BASE } });
  const body = await response.json().catch(() => ({}));
  assert(response.ok(), `${method} ${route}: HTTP ${response.status()} ${JSON.stringify(body).slice(0, 300)}`);
  return body;
}

async function until(what, fn, timeout = 120000) {
  const deadline = Date.now() + timeout;
  for (;;) {
    const value = await fn();
    if (value) return value;
    assert(Date.now() < deadline, `timed out waiting for ${what}`);
    await sleep(1000);
  }
}

async function firstModel(context, capability) {
  const { offerings } = await api(context, 'GET', `/api/v1/offerings?capability=${capability}`);
  const available = offerings.filter((item) => item.available).sort((a, b) => Number(Boolean(b.recommended)) - Number(Boolean(a.recommended)));
  assert(available.length, `no available ${capability} offering`);
  return available[0].model_id;
}

const sessionTasks = async (context, sid) => (await api(context, 'GET', `/api/v1/tasks?session_id=${encodeURIComponent(sid)}&limit=20`)).tasks ?? [];
const pendingCards = async (context, sid) => ((await api(context, 'GET', `/api/v1/chat/sessions/${sid}/pending-approvals`)).items ?? []).filter((item) => item.status === 'pending');

async function say(context, sid, content, domain, model) {
  const response = await context.request.post(`${BASE}/api/chat/sessions/${sid}/messages`, {
    data: { content, creative_domain: domain, ai_model: model },
    headers: { Origin: BASE },
  });
  assert.equal(response.status(), 202, `message: HTTP ${response.status()}`);
}

async function succeeded(context, sid, capability) {
  return until(`${capability} task`, async () => {
    const task = (await sessionTasks(context, sid)).find((item) => item.capability === capability);
    assert(!task || !['FAILED', 'CANCELED'].includes(task.state), `${capability} task ${task?.state}`);
    return task?.state === 'SUCCEEDED' ? task : null;
  }, 180000);
}

/** Seeds the two sessions through the Mock Agent, the way a person would in the workspace. */
async function seed(context, text) {
  const imageModel = await firstModel(context, 'text-to-image');
  const modelModel = await firstModel(context, 'text-to-3d');
  const props = await api(context, 'POST', '/api/chat/sessions', { title: text.props });
  await api(context, 'PUT', `/api/v1/chat/sessions/${props.id}/approval`, { approvalMode: 'manual' });
  await say(context, props.id, text.image, 'image', imageModel);
  await succeeded(context, props.id, 'text-to-image');
  await sleep(3000);
  await say(context, props.id, text.model, 'model-3d', modelModel);
  const [card] = await until('3D approval card', () => pendingCards(context, props.id).then((cards) => (cards.length ? cards : null)));
  await api(context, 'PUT', `/api/v1/tasks/grants/${card.request_id}`, { decision: 'approve' });
  await succeeded(context, props.id, 'text-to-3d');
  await sleep(4000);
  return { session: props.id, modelModel };
}

/** A second 3D request in the same session, left waiting for approval. */
async function requestApproval(context, seeded, text) {
  await say(context, seeded.session, text.chest, 'model-3d', seeded.modelModel);
  await until('pending approval card', () => pendingCards(context, seeded.session).then((cards) => (cards.length ? cards : null)));
  await sleep(3000);
}

async function shot(page, name, suffix) {
  const file = path.join(OUT, `${name}-${suffix}.png`);
  await page.waitForTimeout(1500);
  await page.screenshot({ path: file });
  const size = statSync(file).size;
  assert(size < MAX_BYTES, `${file} is ${size} bytes (limit ${MAX_BYTES})`);
  console.log(`${path.relative(root, file)} ${Math.round(size / 1024)} KB`);
}

const pw = loadPlaywright();
const browser = await pw.chromium.launch();
let refused = 0;
try {
  for (const locale of LOCALES) {
    const text = TEXT[locale];
    assert(text, `no texts for locale ${locale}`);
    const context = await browser.newContext({ viewport: { width: 1440, height: 900 }, deviceScaleFactor: 1, locale, colorScheme: 'dark' });
    await context.route('**/*', (route) => {
      if (route.request().url().startsWith(`${BASE}/`)) return route.continue();
      refused += 1;
      return route.abort();
    });
    await context.addCookies([{ name: 'plinth_locale', value: locale, url: BASE }]);
    const login = await context.request.post(`${BASE}/api/auth/login`, { data: { username: USER, password: PASSWORD }, headers: { Origin: BASE } });
    assert.equal(login.status(), 200, 'administrator sign-in');
    const seeded = await seed(context, text);
    const page = await context.newPage();

    await page.goto(`${BASE}/?session=${encodeURIComponent(seeded.session)}`);
    await page.locator('[data-testid=chat-message]').first().waitFor({ timeout: 30000 });
    await page.locator('[data-testid=version-tile]').filter({ hasText: /3D/ }).locator('button').first().click({ timeout: 30000 });
    await page.waitForTimeout(4000);
    await shot(page, 'workspace', text.suffix);

    await page.locator('[data-testid=right-panel-tab-formats]').click({ timeout: 20000 });
    await page.locator('[data-testid=model-formats]').first().waitFor({ timeout: 20000 });
    await shot(page, 'formats', text.suffix);

    await requestApproval(context, seeded, text);
    await page.goto(`${BASE}/?session=${encodeURIComponent(seeded.session)}`);
    await page.locator('[data-testid=studio-approval-card]').first().waitFor({ timeout: 30000 });
    await page.locator('[data-testid=version-tile]').filter({ hasText: /2D/ }).locator('button').first().click({ timeout: 30000 });
    await shot(page, 'approval', text.suffix);

    await page.goto(`${BASE}/admin`);
    await page.waitForLoadState('networkidle');
    await shot(page, 'admin', text.suffix);
    await context.close();
  }
} finally {
  await browser.close();
}
assert.equal(refused, 0, `${refused} request(s) to another origin were refused`);
