// Shared helpers for the browser regression scripts in scripts/regression (docs/regression-matrix.md).
//
// Environment (scripts/release-env.py smoke <env> injects it; there are no default addresses):
//   BASE, AGENT_TEST_USERNAME, AGENT_TEST_PASSWORD, ART_AI_UPSTREAM_SHAPE
//   OUT                     screenshots and results.json (default: a new system temp dir, never the repository)
//   PLAYWRIGHT_MODULE_PATH  Playwright, resolved like scripts/acceptance/browser_matrix/lib.mjs
// requireFake() refuses (exit 2) unless the target is in the fake upstream shape: /api/version carries no
// shape, so ART_AI_UPSTREAM_SHAPE decides, as in scripts/acceptance/release-1-9-*.mjs.
// Nothing here prints passwords, cookies or signed URLs.
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { resolvePlaywright } from '../acceptance/browser_matrix/lib.mjs';

export const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
export const base = (process.env.BASE || '').replace(/\/+$/, '');
export const tag = `rg-${new Date().toISOString().replace(/[-:T]/g, '').slice(0, 14)}`;

export function fakeRefusal(argv = process.argv.slice(2), env = process.env) {
  if (argv.includes('--live')) return 'refusing to run: this script is fake-only; --live is not accepted';
  if (env.ART_AI_UPSTREAM_SHAPE !== 'fake') {
    return `refusing to run: the target upstream shape is "${env.ART_AI_UPSTREAM_SHAPE || 'unknown'}", not fake; run through scripts/release-env.py smoke dev-test (fake release)`;
  }
  const missing = ['BASE', 'AGENT_TEST_USERNAME', 'AGENT_TEST_PASSWORD'].filter((name) => !env[name]);
  if (missing.length) return `missing environment: ${missing.join(', ')} (run through scripts/release-env.py smoke dev-test)`;
  try {
    const url = new URL(env.BASE);
    if (!/^https?:$/.test(url.protocol) || (url.pathname && url.pathname !== '/') || url.search || url.username) return 'BASE must be an http(s) origin without a path';
  } catch { return 'BASE must be an http(s) origin without a path'; }
  return null;
}

export function requireFake() {
  const problem = fakeRefusal();
  if (problem) { console.error(problem); process.exit(2); }
}

export function outDir(name) {
  const dir = process.env.OUT || fs.mkdtempSync(path.join(os.tmpdir(), `plinth-regression-${name}-`));
  fs.mkdirSync(dir, { recursive: true });
  return dir;
}

export function reporter(name) {
  const results = [];
  const rec = (ok, label, detail = '') => { results.push({ ok: !!ok, label, detail }); console.log(`${ok ? 'PASS' : 'FAIL'}: ${label}${detail ? ` - ${detail}` : ''}`); return !!ok; };
  const finish = (dir, extra = {}) => {
    const passed = results.filter((r) => r.ok).length; const failed = results.length - passed;
    if (dir) fs.writeFileSync(path.join(dir, 'results.json'), JSON.stringify({ name, results, ...extra }, null, 2));
    console.log(`=== ${name}: PASS=${passed} FAIL=${failed} ===`);
    return failed || !passed ? 1 : 0;
  };
  return { rec, finish, results };
}

export async function launch(engine = 'chromium') {
  const playwright = resolvePlaywright();
  return playwright[engine].launch();
}

/** A logged-in browser context for the Agent test account (retries the login rate limit). */
export async function newContext(browser, width = 1440, height = 900) {
  const ctx = await browser.newContext({ viewport: { width, height }, locale: 'zh-CN', serviceWorkers: 'block' });
  const headers = { Origin: new URL(base).origin };
  let status = 0;
  for (let i = 0; i < 8; i++) {
    const login = await ctx.request.post(`${base}/api/auth/login`, { data: { username: process.env.AGENT_TEST_USERNAME, password: process.env.AGENT_TEST_PASSWORD }, headers });
    status = login.status();
    if (status !== 429) break;
    await sleep(Number(login.headers()['retry-after'] || 30) * 1000 + 2000);
  }
  if (status !== 200) throw new Error(`login failed: HTTP ${status}`);
  return ctx;
}

/** API, locator and turn helpers bound to one context and page. */
export function helpers(ctx, page, dir) {
  const headers = { Origin: new URL(base).origin };
  const api = async (method, route, data, extra = {}) => {
    const r = await ctx.request.fetch(base + route, { method, data, headers: { ...headers, ...extra } });
    let body = {}; try { body = await r.json(); } catch {}
    return [r.status(), body];
  };
  const tid = (id) => page.locator(`[data-testid="${id}"]`);
  const messagesOf = async (sid) => { const [, b] = await api('GET', `/api/chat/sessions/${sid}/messages`); return Array.isArray(b) ? b : (b.messages || b.data || []); };
  const finalReplies = async (sid) => (await messagesOf(sid)).filter((m) => m.role === 'assistant' && !String(m.content || '').startsWith('__GENERATING__')).length;
  const tasksOf = async (sid) => { const [, b] = await api('GET', `/api/v1/tasks?session_id=${sid}&limit=100`); return b.tasks || b.data || []; };
  const waitFor = async (check, seconds, step = 1000) => { const end = Date.now() + seconds * 1000; while (Date.now() < end) { const v = await check(); if (v) return v; await sleep(step); } return null; };
  const decisionField = async () => { const [, s] = await api('GET', '/api/v1/schema'); return (s.schema || s)?.chat?.api?.fields?.decision || 'decision'; };
  const approvePending = async (sid) => {
    const field = await decisionField();
    const [, p] = await api('GET', `/api/v1/chat/sessions/${sid}/pending-approvals`);
    for (const c of (p.items || []).filter((i) => i.status === 'pending')) await api('PUT', `/api/v1/tasks/grants/${c.request_id}`, { [field]: 'approve' });
  };
  const sendTurn = async (sid, content, seconds = 150) => {
    const before = await finalReplies(sid);
    for (let a = 0; a < 60; a++) { const [s] = await api('POST', `/api/chat/sessions/${sid}/messages`, { content, creative_domain: 'image' }); if (s !== 409) break; await sleep(1000); }
    if (!await waitFor(async () => { await approvePending(sid); return (await finalReplies(sid)) > before; }, seconds)) throw new Error('the turn did not finish');
  };
  const openSession = async (sid) => {
    await page.goto(`${base}/?session=${sid}`, { waitUntil: 'load' });
    await tid('composer-send').first().waitFor({ timeout: 30000 });
    await sleep(2500);
  };
  const shot = async (name) => { if (!dir) return ''; const f = path.join(dir, `${name}.png`); await page.screenshot({ path: f }); return f; };
  /** A user-initiated task through the public task API (prepare, grant, approve if asked, submit). */
  const directTask = async (sid, offering, capability, input, reasons) => {
    const field = await decisionField();
    const [ps, prep] = await api('POST', '/api/v1/tasks/prepare', { session_id: sid, capability, offering_id: offering, input, ...(reasons ? { parameter_reasons: reasons } : {}) });
    if (!prep.prepared_input_id) throw new Error(`prepare ${ps}`);
    const key = { 'Idempotency-Key': `rg-${prep.prepared_input_id}` };
    const [, grant] = await api('POST', '/api/v1/tasks/grants', { prepared_input_id: prep.prepared_input_id }, key);
    if (grant.status === 'pending') await api('PUT', `/api/v1/tasks/grants/${grant.request_id}`, { [field]: 'approve' });
    return api('POST', '/api/v1/tasks', { prepared_input_id: prep.prepared_input_id, authorization_request_id: grant.request_id }, key);
  };
  return { api, tid, messagesOf, finalReplies, tasksOf, waitFor, approvePending, sendTurn, openSession, shot, directTask };
}
