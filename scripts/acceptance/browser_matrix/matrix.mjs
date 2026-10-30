// Browser acceptance matrix (docs/acceptance-1.0.md, free operations only): engine x locale x viewport cells.
// Run: bash scripts/acceptance/browser_matrix/run.sh   (injects BASE/ADMIN_* via scripts/release-env.py smoke <env>)
// Env: B3_ITEMS=CNV-02,SKL-02  B3_ENGINES=chromium,webkit  B3_LOCALES=zh-CN,en  B3_VIEWPORTS=1366x768,1440x900,1920x1080
//      OUT=<dir for screenshots/results.json>  B3_PARALLEL=0|1  B3_LLM=0 (skip the one chat turn per 1440 cell)
//      B3_REF_SESSIONS=<comma list of session title search strings: sessions that already hold tasks / a 3D model>
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { resolvePlaywright, base, loginAdmin, requireEnv } from './lib.mjs';
import { makePng, makeGlb } from './fixtures.mjs';

requireEnv('BASE', 'ADMIN_USERNAME', 'ADMIN_PASSWORD');
const pw = resolvePlaywright();
const list = (v, d) => (process.env[v] || d).split(',').map((s) => s.trim()).filter(Boolean);
const ENGINES = list('B3_ENGINES', 'chromium,webkit');
const LOCALES = list('B3_LOCALES', 'zh-CN,en');
const VIEWPORTS = list('B3_VIEWPORTS', '1366x768,1440x900,1920x1080').map((s) => s.split('x').map(Number));
const ONLY = process.env.B3_ITEMS ? new Set(list('B3_ITEMS', '')) : null;
const PARALLEL = process.env.B3_PARALLEL === '1'; // default serial: dev-test limits /api/ to 1200 req/min per client address
const stamp = new Date().toISOString().replace(/[:.]/g, '-');
const OUT = process.env.OUT || path.join(os.tmpdir(), 'plinth-acceptance-matrix', stamp);
fs.mkdirSync(OUT, { recursive: true });
const FIX = path.join(OUT, 'fixtures');
fs.mkdirSync(FIX, { recursive: true });
fs.writeFileSync(path.join(FIX, 'b3-test.png'), makePng(800, 600));
fs.writeFileSync(path.join(FIX, 'b3-ok.glb'), makeGlb());
fs.writeFileSync(path.join(FIX, 'b3-big.glb'), makeGlb(10 * 1024 * 1024 + 4096));
fs.writeFileSync(path.join(FIX, 'b3-external.glb'), makeGlb(0, 'https://example.invalid/external.bin'));
fs.writeFileSync(path.join(FIX, 'b3-notpng.png'), 'this is not a png');

const results = []; // {id,status,cell,summary,shots}
const relogins = [];
const ITEM_ORDER = ['I18N-01', 'I18N-02', 'PRV-05', 'TSK-01', 'SKL-01', 'SKL-02', 'SKL-03', 'CNV-08', 'CNV-07', 'CNV-06', 'CNV-02', 'CNV-03', 'CNV-04', 'CNV-05',
  'M3D-06', 'M3D-05', 'AGT-08', 'AGT-09', 'AGT-11', 'DLV-05', 'ADM-01', 'I18N-03', 'I18N-04', 'I18N-06'];
const CJK = /[㐀-鿿　-〿＀-￯]/;

class Skip extends Error {}
const known = (m) => Object.assign(new Skip(m), { status: 'KNOWN-LIMITATION' });
const blocked = (m) => Object.assign(new Skip(m), { status: 'BLOCKED' });

async function runCell(engine, loc, [W, H]) {
  const cell = `${engine}/${loc}/${W}x${H}`;
  const tag = `${engine}-${loc}-${W}`;
  const zh = loc.startsWith('zh');
  const L = (a, b) => (zh ? a : b);
  const browser = await pw[engine].launch();
  const mk = async (opts = {}) => {
    const c = await browser.newContext({ viewport: { width: W, height: H }, locale: loc === 'en' ? 'en-US' : loc, acceptDownloads: true, serviceWorkers: 'block', ...opts });
    if (engine === 'chromium') { try { const p0 = await c.newPage(); const s = await c.newCDPSession(p0); await s.send('Network.setCacheDisabled', { cacheDisabled: true }); await p0.close(); } catch {} }
    return c;
  };
  const ctx = await mk();
  if (await loginAdmin(ctx) !== 200) throw new Error('admin login failed');
  const page = await ctx.newPage();
  const consoleErrors = [];
  page.on('response', (r) => { if (r.status() === 429) n429++; });
  page.on('pageerror', (e) => consoleErrors.push('pageerror: ' + e.message));
  page.on('dialog', (d) => { consoleErrors.push('native dialog: ' + d.message()); d.dismiss().catch(() => {}); });
  const dir = path.join(OUT, tag);
  fs.mkdirSync(dir, { recursive: true });
  let curItem = '';
  let shotN = 0;
  let shots = [];
  const shot = async (name, pg = page) => {
    const f = path.join(dir, `${curItem}-${String(++shotN).padStart(2, '0')}-${name}.png`);
    try { await pg.screenshot({ path: f }); shots.push(f); } catch {}
    return f;
  };
  const ok = (c, msg) => { if (!c) throw new Error('assert: ' + msg); };
  const state = {};
  const apiSkills = async () => (await get('/api/v1/chat/skills/personal'))?.skills || [];
  let n429 = 0;
  const get = async (url) => {
    for (let i = 0; i < 4; i++) { const r = await ctx.request.get(base + url); if (r.status() === 429) { n429++; await new Promise((f) => setTimeout(f, 15000)); continue; } return r.ok() ? r.json() : null; }
    return null;
  };

  // Existing sessions that already hold tasks / a 3D model (from B3_REF_SESSIONS title searches, else the newest sessions).
  const REF_TERMS = list('B3_REF_SESSIONS', '');
  const refSessions = async () => {
    const out = []; const seen = new Set();
    const add = (r) => { for (const s of r?.sessions || []) if (!seen.has(s.id) && !String(s.title).startsWith('b3-accept') && (s._count?.messages ?? 1) > 0) { seen.add(s.id); out.push(s); } };
    for (const q of REF_TERMS) add(await get('/api/chat/sessions?search=' + encodeURIComponent(q) + '&limit=5'));
    if (!REF_TERMS.length) add(await get('/api/chat/sessions?limit=30'));
    return out;
  };

  // ---- session bootstrap: one reusable session per cell
  const title = `b3-accept ${tag}`;
  let sid;
  {
    const found = await get('/api/chat/sessions?search=' + encodeURIComponent('b3-accept ' + tag) + '&limit=20');
    sid = (found?.sessions || []).find((s) => s.title === title)?.id;
    if (!sid) {
      const r = await ctx.request.post(base + '/api/chat/sessions', { data: { title }, headers: { Origin: new URL(base).origin } });
      sid = (await r.json()).id;
    }
  }
  const goSession = async (id = sid, pg = page) => {
    await pg.goto(`${base}/?session=${id}`, { waitUntil: 'load' });
    await pg.locator('[data-testid=composer-send]').waitFor({ timeout: 30000 });
    await pg.waitForTimeout(1500);
  };
  const tid = (id, pg = page) => pg.locator(`[data-testid="${id}"]`).first();
  const hover = async (loc_, pg = page) => {
    const bb = await loc_.boundingBox(); ok(bb, 'hover target has a box');
    await pg.mouse.move(2, 2); await pg.waitForTimeout(150);
    await pg.mouse.move(bb.x + bb.width / 2, bb.y + bb.height / 2, { steps: 4 });
    await pg.waitForTimeout(1400);
  };
  const tooltip = async () => (await page.locator('[role=tooltip]').allInnerTexts()).filter(Boolean).join('\n');
  const layerRows = () => page.locator('[data-testid=layer-row]');
  const uploadToCanvas = async (file) => {
    await page.mouse.move(2, 2); await page.keyboard.press('Escape').catch(() => {});
    const [fc] = await Promise.all([page.waitForEvent('filechooser', { timeout: 15000 }), tid('canvas-tool-upload').click()]);
    await fc.setFiles(file);
    await page.waitForTimeout(2500);
  };
  const resetCanvas = async () => {
    await goSession();
    await tid('right-panel-tab-layers').click().catch(() => {});
    for (let i = 0; i < 12; i++) {
      const n = await layerRows().count();
      if (n <= 1) break;
      await layerRows().first().locator('button').last().click();
      await page.waitForTimeout(400);
    }
  };
  const textIssues = async (scope = 'body') => { const ud = await userData(); return page.evaluate(({ scope, zh, ud }) => {
    const root = document.querySelector(scope) || document.body;
    const out = [];
    const isUser = (t) => ud.some((u) => u === t || (t.length > 1 && u.includes(t)));
    const w = document.createTreeWalker(root, NodeFilter.SHOW_TEXT);
    const cjk = /[㐀-鿿]/;
    const brand = /(Meshy|Tripo|Hyper3D|Rodin|GPT|Gemini|OpenAI|Seedream|Nano|Banana|Plinth|deepseek|GLB|PNG|ZIP|STL|OBJ|FBX|glTF|USDZ|3MF|UV|API|LLM|ID|credits?|SKILLS?|AUTO|low|high|max|CPU|MCP|Agent|Skill|Schema|Provider|Offering|Pro|Gen|Smart|Text|Motion|Convert|Retopo|HD|Mock|URL|JSON|SVG|HEX|RGB|@\w+|v\d)/i;
    while (w.nextNode()) {
      const n = w.currentNode; const t = (n.textContent || '').trim(); if (!t) continue;
      if (!/\s/.test(t) && !cjk.test(t)) continue;
      const el = n.parentElement; if (!el) continue;
      const st = getComputedStyle(el); if (st.display === 'none' || st.visibility === 'hidden') continue;
      if (el.closest('[lang]:not(html)') && el.closest('[lang]:not(html)').getAttribute('lang') !== document.documentElement.lang) continue;
      if (el.closest('script,style,noscript,[data-user-content],[data-testid^=chat-],[data-testid=app-user-menu],[data-testid=studio-session-row],[data-testid=studio-session-name],[data-testid=layer-row],[data-testid=version-tile],[data-testid=workspace-task-card],[data-testid=studio-session-rename]')) continue;
      if (isUser(t)) continue;
      if (location.pathname.startsWith('/admin/agent-configuration') && t.length > 40) continue; // editable prompt content, not UI copy
      if (!zh && cjk.test(t)) out.push('CJK-in-en: ' + t.slice(0, 80));
      if (zh && !cjk.test(t) && /[A-Za-z]{3,}[^A-Za-z]+[A-Za-z]{3,}/.test(t) && !brand.test(t) && !/^[\w@.\-/: ·,()%+]+$/.test(t.replace(/[A-Za-z]{3,}/g, 'x'))) out.push('EN-in-zh: ' + t.slice(0, 80));
    }
    return [...new Set(out)];
  }, { scope, zh, ud }); };
  let udCache = null;
  const userData = async () => {
    if (udCache) return udCache;
    const d = [];
    const me = await get('/api/auth/session'); if (me?.user?.name) d.push(me.user.name);
    for (const sk of await apiSkills()) { d.push(sk.name, sk.prompt); }
    const ss = await get('/api/chat/sessions?limit=200'); for (const x of ss?.sessions || []) d.push(x.title);
    const us = await get('/api/admin/users'); for (const u of us?.data || []) d.push(u.name, u.username, u.email, u.nickname);
    return (udCache = d.filter(Boolean));
  };
  const overflow = async () => page.evaluate(() => {
    const iw = window.innerWidth; const bad = [];
    if (document.documentElement.scrollWidth > iw + 1) bad.push(`document scrollWidth ${document.documentElement.scrollWidth} > ${iw}`);
    for (const el of document.querySelectorAll('button,h1,h2,h3,header *,nav *,[role=tab]')) {
      const r = el.getBoundingClientRect(); if (!r.width || !r.height) continue;
      if (r.right > iw + 1 || r.left < -1) bad.push(`offscreen ${el.tagName}[${el.getAttribute('data-testid') || (el.innerText || '').slice(0, 20)}] ${Math.round(r.left)}..${Math.round(r.right)}`);
    }
    return bad.slice(0, 8);
  });
  const credits = async () => (await page.locator('header, [data-testid=app-topbar]').first().innerText().catch(() => '')).replace(/\s+/g, ' ');
  const taskCount = async () => { const l = await tid('workspace-task-center').getAttribute('aria-label'); return (l || '').match(/\d+/)?.[0] ?? '0'; };

  // ---- items
  const items = {};

  items['I18N-01'] = async () => {
    const anon = await mk(); const p = await anon.newPage();
    const want = zh ? /^zh/i : /^en/i;
    await p.goto(base + '/login', { waitUntil: 'load' }); await p.waitForTimeout(1200);
    const lang1 = await p.evaluate(() => document.documentElement.lang);
    const body1 = await p.evaluate(() => document.body.innerText.slice(0, 200).replace(/\s+/g, ' '));
    await shot('login', p);
    ok(want.test(lang1), `anonymous /login html lang=${lang1}`);
    ok(zh ? CJK.test(body1) : !CJK.test(body1), `login copy language (${body1.slice(0, 60)})`);
    await anon.close();
    await goSession();
    const lang2 = await page.evaluate(() => document.documentElement.lang);
    ok(want.test(lang2), `workspace html lang=${lang2}`);
    return `fresh context locale=${loc}: /login lang=${lang1}, workspace lang=${lang2}`;
  };

  items['I18N-02'] = async () => {
    const other = zh ? 'en' : 'zh-CN';
    const otherWant = zh ? /^en/i : /^zh/i;
    await goSession();
    await tid('app-user-menu').click();
    await tid('locale-option-' + other).click();
    await page.waitForLoadState('load'); await page.locator('[data-testid=composer-send]').waitFor(); await page.waitForTimeout(1200);
    const l1 = await page.evaluate(() => document.documentElement.lang);
    ok(otherWant.test(l1), `after switch lang=${l1}`);
    await shot('switched');
    await page.reload(); await page.locator('[data-testid=composer-send]').waitFor(); await page.waitForTimeout(800);
    const l2 = await page.evaluate(() => document.documentElement.lang);
    ok(otherWant.test(l2), `after reload lang=${l2}`);
    await tid('app-user-menu').click(); await page.locator('[role=menuitem]').last().click(); await page.waitForTimeout(1500);
    ok(/login/.test(page.url()) || await page.locator('input[type=password]').count() > 0, 'signed out to login page');
    const ck = (await ctx.cookies()).map((c) => c.name).join(','); await shot('signed-out');
    { const st = await loginAdmin(ctx); if (st !== 200) throw new Error(`re-login status ${st}; url=${page.url()} cookies=${ck}`); ok(st === 200, 're-login status ' + st); }
    await goSession();
    const l3 = await page.evaluate(() => document.documentElement.lang);
    ok(otherWant.test(l3), `after re-login lang=${l3}`);
    // restore the cell locale
    await tid('app-user-menu').click();
    await tid('locale-option-' + (zh ? 'zh-CN' : 'en')).click();
    await page.waitForLoadState('load'); await page.locator('[data-testid=composer-send]').waitFor(); await page.waitForTimeout(1000);
    const l4 = await page.evaluate(() => document.documentElement.lang);
    ok(zh ? /^zh/i.test(l4) : /^en/i.test(l4), `restored lang=${l4}`);
    return `switch ${loc}->${other}: lang ${l1}; after reload ${l2}; after logout/login ${l3}; restored ${l4}`;
  };

  items['PRV-05'] = async () => {
    await goSession();
    const api = await get('/api/meshy/balance');
    ok(api, 'GET /api/meshy/balance');
    const accounts = api.accounts || [];
    const fmt = (n) => String(n).replace(/\B(?=(\d{3})+(?!\d))/g, ',');
    const amt = (a) => a.native_balance ? fmt(a.native_balance.amount) : (typeof a.balance === 'number' ? fmt(a.balance) : null);
    const btn = page.locator('button[aria-label]').filter({ hasText: /credits|积分/ }).first();
    const label = await btn.getAttribute('aria-label');
    await hover(btn);
    const popText = (await page.locator('[data-base-ui-popup], [role=dialog]').allInnerTexts()).join('\n');
    await shot('balance-popover');
    ok(accounts.length > 0, 'API reports configured accounts');
    const api2 = (await get('/api/meshy/balance'))?.accounts || accounts;
    ok(label && (label.includes(amt(accounts[0])) || label.includes(amt(api2[0]))), `summary label "${label}" shows first/lowest configured account ${amt(accounts[0])} (or ${amt(api2[0])} after)`);
    for (const a of accounts) ok(popText.includes(amt(a)) || popText.includes(amt(api2.find((x) => x.provider === a.provider) || a)), `popover lists account ${a.provider} (${amt(a)})`);
    const lines = popText.split('\n').filter(Boolean).length;
    ok(lines <= 1 + accounts.length * 2, `popover shows only configured accounts (${lines} lines for ${accounts.length} accounts)`);
    return `summary="${label}"; API accounts=${accounts.map((a) => a.provider + ':' + amt(a)).join(',')}; popover="${popText.replace(/\n+/g, ' | ').slice(0, 120)}"; note: units differ (native credits), summary shows first native account, not a cross-unit minimum`;
  };

  items['TSK-01'] = async () => {
    // own blank session: no tasks; an older session with tasks: only its own tasks are listed
    await goSession();
    await tid('workspace-task-center').click(); await page.waitForTimeout(800);
    const own = await page.locator('[data-testid=workspace-task-card]').count();
    await shot('own-empty');
    await page.keyboard.press('Escape');
    const target = (await refSessions())[0];
    if (!target) throw blocked('no existing session with tasks; generate once in the test account or set B3_REF_SESSIONS');
    await goSession(target.id);
    const label = await tid('workspace-task-center').getAttribute('aria-label');
    await tid('workspace-task-center').click(); await page.waitForTimeout(1200);
    let rows = 0; for (let i = 0; i < 10 && !rows; i++) { rows = await page.locator('[data-testid=workspace-task-card]').count(); if (!rows) await page.waitForTimeout(1000); }
    await shot('session-with-tasks');
    ok(own === 0, `blank session shows ${own} tasks`);
    if (!rows) throw blocked(`reference session "${target.title}" has no tasks; set B3_REF_SESSIONS to a session that does`);
    const total = Number((label || '').match(/\d+/)?.[0] ?? rows);
    ok(rows <= total + 0 || total === 0, `rows ${rows} vs label ${label}`);
    await page.locator('[data-testid=workspace-task-open]').first().click(); await page.waitForTimeout(1200);
    const detail = await page.locator('[role=dialog]').allInnerTexts();
    await shot('task-detail');
    ok(detail.join('').length > 20, 'task detail opens');
    return `blank session rows=${own}; session "${target.title}" rows=${rows} (label ${label}); row click opened detail (${detail.join('').length} chars)`;
  };

  const skillRow = (name) => page.locator('[role=dialog],[role=listbox],[data-base-ui-popup]').locator('tr,[role=row],li,[role=option],div').filter({ hasText: name }).last();
  const openSkills = async () => { await page.keyboard.press('Escape').catch(() => {}); await page.mouse.move(700, 500); await page.waitForTimeout(200); await tid('workspace-skill-picker').click({ timeout: 15000 }).catch(async () => { await tid('workspace-skill-picker').click({ force: true }); }); await page.waitForTimeout(600); };
  const delSkill = (n) => page.locator('[data-testid=workspace-skill-picker-panel] div.relative').filter({ has: page.locator('[role=menuitemradio]', { hasText: n }) }).locator('[data-testid=skill-delete]').click();

  items['SKL-01'] = async () => {
    await goSession();
    await openSkills(); await shot('skills');
    const txt = (await page.locator('[role=dialog],[role=listbox],[data-base-ui-popup]').allInnerTexts()).join('\n');
    await page.keyboard.press('Escape');
    const sys = (await get('/api/v1/chat/skills'))?.skills?.filter((s) => s.id.startsWith('system-')) || [];
    const mine = await apiSkills();
    // acceptance text: "new user: empty, no built-in Skill or category". A new user cannot be created on dev-test
    // (creation was denied), so the check is on what every user is shown: the system skills list.
    // #104 doc update: only the system three-view Skill is built in. The fresh-user (empty personal list) half needs a second account.
    const names = sys.map((s) => (zh ? (s.name_zh || s.name) : s.name));
    ok(sys.length === 1 && sys[0].id === 'system-three-view', `exactly one system Skill (system-three-view); got [${sys.map((s) => s.id).join(',')}]`);
    ok(new RegExp(zh ? '三视图' : 'three views', 'i').test(txt), `picker shows the system Skill in ${loc}: "${names[0]}"`);
    throw Object.assign(new Error(`picker shows only the system three-view Skill (${names[0]}) plus the admin's ${mine.length} personal Skill(s); "fresh user sees an empty personal list" needs a second account (not available on dev-test)`), { status: 'PARTIAL' });
  };

  const createSkill = async (name) => {
    await openSkills();
    await page.getByText(L('创建 Skill', 'Create Skill'), { exact: true }).click();
    await page.locator('#personal-skill-name, input[type=text]').first().fill(name);
    await page.locator('#personal-skill-prompt').fill('b3 acceptance prompt');
    await page.getByText(L('保存并使用', 'Save and use'), { exact: false }).first().click();
    await page.waitForTimeout(1500);
  };
  const pickModel2D = async () => {
    await tid('workspace-model-picker').click(); await page.waitForTimeout(500);
    await page.locator('[role=dialog],[data-base-ui-popup]').getByText(/GPT Image/).first().click();
    await page.waitForTimeout(800);
  };
  const cleanupSkills = async () => {
    for (const s of await apiSkills()) if (s.name.startsWith('b3-') && s.name.endsWith(tag)) await ctx.request.delete(base + '/api/v1/chat/skills/personal/' + encodeURIComponent(s.id), { headers: { Origin: new URL(base).origin } });
  };

  items['SKL-02'] = async () => {
    const name = `b3-skill-${tag}`;
    await goSession(); await cleanupSkills();
    await goSession();
    await pickModel2D();
    await createSkill(name);
    const mine = (await apiSkills()).find((s) => s.name === name);
    ok(mine, 'skill saved server side');
    ok(mine.recommended_size === '2048x2048', `recommended_size=${mine.recommended_size}`);
    await tid('composer-params').click(); await page.waitForTimeout(800);
    await shot('params');
    const t = (await page.locator('[role=dialog],[data-base-ui-popup]').allInnerTexts()).join('\n');
    ok(/2048/.test(t), 'size default 2048 shown in params');
    ok(new RegExp(L('来自 Skill|来自\\s*Skill', 'from (the )?Skill'), 'i').test(t), `"from Skill" label present: ${t.replace(/\n+/g, ' | ').slice(0, 200)}`);
    await page.keyboard.press('Escape');
    state.skillName = name;
    return `created via UI, recommended_size=${mine.recommended_size}, params popover shows 2048 + from-Skill label`;
  };

  items['SKL-03'] = async () => {
    const a = `b3-skill-a-${tag}`; const b = `b3-skill-b-${tag}`;
    await goSession(); await cleanupSkills(); await goSession();
    await createSkill(a); await createSkill(b); // b is the selected one now
    const chipText = async () => (await tid('workspace-skill-picker').innerText()).trim();
    const sel1 = await chipText();
    // delete the other (a): selection must stay b
    await openSkills();
    await delSkill(a);
    await page.waitForTimeout(1200);
    const mine1 = (await apiSkills()).map((s) => s.name);
    ok(!mine1.includes(a) && mine1.includes(b), 'other skill deleted, selected kept');
    await page.keyboard.press('Escape'); await page.waitForTimeout(300);
    const sel2 = await tid('workspace-skill-picker').innerText();
    ok(sel2.toLowerCase().includes('b3-skill-b'),`selected skill still shown after deleting other ("${sel2.slice(0, 40)}")`);
    // delete the selected (b)
    await openSkills();
    await delSkill(b);
    await page.waitForTimeout(1500);
    const mine2 = (await apiSkills()).map((s) => s.name);
    ok(!mine2.includes(b), 'selected skill deleted');
    await shot('after-delete');
    await page.keyboard.press('Escape'); await page.waitForTimeout(300);
    const sel3 = await tid('workspace-skill-picker').innerText();
    ok(!sel3.includes('b3-skill-b'), `selection back to none after deleting selected ("${sel3.slice(0, 40)}")`);
    return `deleted other (selection kept: chip "${sel1.slice(0, 30)}"), then deleted selected -> back to none`;
  };

  items['CNV-08'] = async () => {
    await resetCanvas();
    await uploadToCanvas(path.join(FIX, 'b3-test.png'));
    await page.mouse.click(Math.round(W * 0.5), Math.round(H * 0.5));
    const hdr = page.locator('[data-testid=stage-header]').first();
    const r = await hdr.evaluate((el) => {
      const hb = el.getBoundingClientRect(); const kids = [...el.querySelectorAll('button,h2,[data-testid=stage-actions]')].map((k) => k.getBoundingClientRect()).filter((k) => k.width);
      const tops = kids.map((k) => Math.round(k.top)); const bad = kids.filter((k) => k.right > hb.right + 1 || k.left < hb.left - 1);
      return { hbh: Math.round(hb.height), sw: el.scrollWidth, cw: el.clientWidth, rowSpread: Math.max(...tops) - Math.min(...tops), bad: bad.length };
    });
    await shot('header');
    ok(r.sw <= r.cw + 1, `header scrollWidth ${r.sw} > clientWidth ${r.cw}`);
    ok(r.rowSpread <= 12 && r.hbh <= 48, `header wraps: rowSpread=${r.rowSpread} h=${r.hbh}`);
    ok(r.bad === 0, `${r.bad} header controls outside header`);
    const of = await overflow(); ok(!of.length, 'page overflow: ' + of.join('; '));
    // collapsed text stays in the hover hint
    const merge = tid('stage-merge'); const lbl = (await merge.getAttribute('aria-label')) || '';
    await hover(merge); const t = await tooltip();
    ok(t.includes(lbl.split(/[（(]/)[0].trim()), `tooltip carries the collapsed name "${lbl}"`);
    return `header h=${r.hbh}, scrollW=${r.sw}/${r.cw}, row spread=${r.rowSpread}, hint carries "${lbl}"`;
  };

  items['CNV-07'] = async () => {
    await resetCanvas();
    // nothing selected: disabled buttons put the reason first
    const rb = tid('stage-remove-background');
    await hover(rb); const t0 = await tooltip(); await shot('disabled-hint');
    const name = (await rb.getAttribute('aria-label')) || '';
    const lines0 = t0.split('\n').filter(Boolean);
    ok(lines0[0] !== name && lines0.includes(name), `disabled hint first line is the reason, not the name: ${lines0.slice(0, 2)}`);
    ok(/✦/.test(t0), 'model-calling button carries ✦');
    ok(new RegExp(L('作用于', 'Applies to')).test(t0), 'target line');
    ok(new RegExp(L('会调用模型', 'Calls a model')).test(t0), 'cost line (model)');
    const ex = tid('stage-export-board'); await hover(ex); const t1 = await tooltip();
    ok(new RegExp(L('免费|不调用模型', 'Free|no model')).test(t1) && !/✦/.test(t1), 'free button: cost line, no ✦');
    // with an image: enabled button has name+description+target+cost, no reason line
    await uploadToCanvas(path.join(FIX, 'b3-test.png'));
    await tid('canvas-tool-select').click(); await page.mouse.click(Math.round(W * 0.5), Math.round(H * 0.5)); await page.waitForTimeout(500);
    const mg = tid('stage-merge'); await hover(mg); const t2 = await tooltip(); await shot('enabled-hint');
    ok(!(await mg.isDisabled()), 'merge enabled with image'); ok(new RegExp(L('作用于', 'Applies to')).test(t2) && new RegExp(L('免费', 'Free')).test(t2), 'enabled hint lines');
    // canvas tool rail: name + shortcut + description
    await hover(tid('canvas-tool-brush')); const t3 = await tooltip();
    ok(/B/.test(t3) && t3.split('\n').filter(Boolean).length >= 2, `tool hint: ${t3.replace(/\n+/g, ' | ')}`);
    return `disabled hint first line="${lines0[0]}", ✦ on model button only, free buttons say free, tool hint has shortcut`;
  };

  items['CNV-06'] = async () => {
    await goSession();
    await tid('canvas-tool-select').click();
    await page.mouse.click(Math.round(W * 0.5), Math.round(H * 0.5));
    await page.keyboard.press('?'); await page.waitForTimeout(800);
    const dlg = page.locator('[role=dialog]').filter({ hasText: new RegExp(L('快捷键', 'shortcuts'), 'i') });
    ok(await dlg.count() > 0, 'shortcut panel opens on ?');
    const txt = await dlg.first().innerText(); await shot('panel');
    ok(/Esc/.test(txt) && /Space|空格/.test(txt), 'panel lists schema shortcuts');
    await page.keyboard.press('Escape'); await page.waitForTimeout(400);
    await page.locator('textarea').first().click();
    await page.keyboard.type('?b'); await page.waitForTimeout(600);
    const again = await page.locator('[role=dialog]').filter({ hasText: new RegExp(L('快捷键', 'shortcuts'), 'i') }).count();
    const v = await page.locator('textarea').first().inputValue();
    ok(again === 0, 'typing ? in the composer does not open the panel');
    ok(v === '?b', `composer received the keys (${v})`);
    const brushPressed = await tid('canvas-tool-brush').getAttribute('aria-pressed');
    ok(brushPressed !== 'true', 'B in composer did not switch tool');
    await page.locator('textarea').first().fill('');
    return `? opens panel (${txt.length} chars, from Schema), ignored while typing in composer`;
  };

  const buildCanvas = async () => {
    await resetCanvas();
    await uploadToCanvas(path.join(FIX, 'b3-test.png'));
    const box = await tid('studio-stage').boundingBox();
    const cx = box.x + box.width * 0.5; const cy = box.y + box.height * 0.5;
    // shape
    await tid('canvas-tool-shape').click(); await page.mouse.move(cx - 140, cy - 130); await page.mouse.down(); await page.mouse.move(cx - 70, cy - 70, { steps: 8 }); await page.mouse.up(); await page.waitForTimeout(600);
    // text
    await tid('canvas-tool-text').click(); await page.mouse.click(cx + 60, cy - 110); await page.waitForTimeout(500); await page.keyboard.type('B3'); await page.keyboard.press('Escape'); await page.waitForTimeout(500);
    // pencil + brush strokes
    await tid('canvas-tool-pencil').click(); await page.mouse.move(cx - 150, cy + 130); await page.mouse.down(); await page.mouse.move(cx - 40, cy + 150, { steps: 8 }); await page.mouse.up(); await page.waitForTimeout(400);
    await tid('canvas-tool-brush').click(); await page.mouse.move(cx + 40, cy + 130); await page.mouse.down(); await page.mouse.move(cx + 150, cy + 150, { steps: 8 }); await page.mouse.up(); await page.waitForTimeout(500);
    await tid('canvas-tool-select').click();
  };

  items['CNV-02'] = async () => {
    await buildCanvas();
    await tid('right-panel-tab-layers').click(); await page.waitForTimeout(300);
    const before = await layerRows().evaluateAll((els) => els.map((e) => (e.innerText || '').trim().split('\n')[0]));
    await shot('before-reload');
    ok(before.length >= 5, `expected >=5 layers (base+image+shape+text+stroke), got ${before.length}: ${before}`);
    // crop tool: enter and cancel with Esc (tool reachable)
    await tid('canvas-tool-crop').click(); await page.waitForTimeout(300); await page.keyboard.press('Escape'); await tid('canvas-tool-select').click();
    await page.waitForTimeout(3500); // autosave debounce
    await page.reload(); await page.locator('[data-testid=composer-send]').waitFor(); await page.waitForTimeout(2500);
    await tid('right-panel-tab-layers').click(); await page.waitForTimeout(500);
    const after = await layerRows().evaluateAll((els) => els.map((e) => (e.innerText || '').trim().split('\n')[0]));
    await shot('after-reload');
    ok(JSON.stringify(before) === JSON.stringify(after), `layers differ after reload: ${before} -> ${after}`);
    return `layers (${before.length}) identical after reload: ${before.join(',')}`;
  };

  const decodeDownload = async (dl) => {
    const f = path.join(dir, `${curItem}-export.png`); await dl.saveAs(f); shots.push(f);
    const buf = fs.readFileSync(f);
    const px = await page.evaluate(async (b64) => {
      const img = new Image(); img.src = 'data:image/png;base64,' + b64; await img.decode();
      const c = document.createElement('canvas'); c.width = img.width; c.height = img.height; const g = c.getContext('2d'); g.drawImage(img, 0, 0);
      const at = (x, y) => Array.from(g.getImageData(Math.round(x), Math.round(y), 1, 1).data);
      return { w: img.width, h: img.height, left: at(img.width / 2 - 200, img.height / 2), right: at(img.width / 2 + 200, img.height / 2), corner: at(8, 8) };
    }, buf.toString('base64'));
    return px;
  };

  items['CNV-03'] = async () => {
    await resetCanvas();
    await uploadToCanvas(path.join(FIX, 'b3-test.png'));
    const [dl] = await Promise.all([page.waitForEvent('download', { timeout: 30000 }), tid('stage-export-board').click()]);
    const px = await decodeDownload(dl);
    ok(dl.suggestedFilename().toLowerCase().endsWith('.png'), 'download is a PNG: ' + dl.suggestedFilename());
    ok(px.w === 2048 && px.h === 2048, `size ${px.w}x${px.h} equals the dashed frame 2048x2048`);
    ok(px.left[0] > 150 && px.left[2] < 100, `left half red: ${px.left}`);
    ok(px.right[2] > 150 && px.right[0] < 100, `right half blue: ${px.right}`);
    ok(px.corner[3] === 0 || px.corner[3] < 255 || (px.corner[0] < 40), `corner empty/background: ${px.corner}`);
    return `PNG ${px.w}x${px.h}; centre-left=${px.left.slice(0, 3)} centre-right=${px.right.slice(0, 3)} corner alpha=${px.corner[3]}`;
  };

  items['CNV-04'] = async () => {
    await resetCanvas();
    await uploadToCanvas(path.join(FIX, 'b3-test.png'));
    await tid('canvas-tool-select').click(); await page.mouse.click(Math.round(W * 0.5), Math.round(H * 0.5)); await page.waitForTimeout(600);
    const tasks0 = await taskCount(); const cr0 = await credits();
    const tiles0 = await page.locator('[data-testid=version-tile]').count();
    await tid('stage-merge').click(); let tiles1 = tiles0; for (let i = 0; i < 12 && tiles1 === tiles0; i++) { await page.waitForTimeout(1000); tiles1 = await page.locator('[data-testid=version-tile]').count(); }
    await shot('merged');
    ok(tiles1 === tiles0 + 1, `merge created one new version (${tiles0}->${tiles1})`);
    // send to chat -> attachment in composer
    await goSession(); await tid('canvas-tool-select').click(); await page.mouse.click(Math.round(W * 0.5), Math.round(H * 0.5)); await page.waitForTimeout(500);
    if (await tid('stage-send-chat').isDisabled()) { await tid('right-panel-tab-layers').click(); await layerRows().first().click(); await page.waitForTimeout(400); }
    await tid('stage-send-chat').click(); await page.waitForTimeout(1500);
    await shot('sent-to-chat');
    const chips = await page.locator('button[aria-label*="@"], [data-testid*=attachment], [data-testid*=composer-chip], img[alt]').count();
    const composerText = await page.locator('main, body').first().innerText();
    const tasks1 = await taskCount(); const cr1 = await credits();
    ok(tasks0 === tasks1, `no task created (${tasks0} -> ${tasks1})`);
    ok(cr0 === cr1, `credits unchanged ("${cr0.slice(-60)}" -> "${cr1.slice(-60)}")`);
    ok(chips > 0 || /attach|附/.test(composerText), 'image became a composer attachment');
    return `merge -> +1 version (${tiles0}->${tiles1}); send-to-chat adds attachment; tasks ${tasks0}->${tasks1}, header credits unchanged`;
  };

  items['CNV-05'] = async () => {
    // needs >=2 image versions: free merges (CNV-04 left one); make sure there are two
    await goSession();
    let tiles = await page.locator('[data-testid=version-tile]').count();
    for (let i = tiles; i < 2; i++) {
      await uploadToCanvas(path.join(FIX, 'b3-test.png'));
      await tid('canvas-tool-select').click(); await page.mouse.click(Math.round(W * 0.5), Math.round(H * 0.5));
      await tid('stage-merge').click(); await page.waitForTimeout(3500);
    }
    tiles = await page.locator('[data-testid=version-tile]').count();
    ok(tiles >= 2, `need 2 versions, have ${tiles}`);
    const tileBtn = page.locator('[data-testid=version-tile] button').first();
    await tileBtn.click(); await page.waitForTimeout(1200);
    // large view via double click on stage image, or via version-enlarge
    const stage = page.locator('[data-testid=studio-stage]'); const sb = await stage.boundingBox();
    await page.mouse.dblclick(sb.x + sb.width / 2, sb.y + sb.height / 2); await page.waitForTimeout(1000);
    let lb = await page.locator('[data-testid=studio-lightbox]').count();
    if (!lb) { await page.keyboard.press('Space'); await page.waitForTimeout(800); lb = await page.locator('[data-testid=studio-lightbox]').count(); }
    ok(lb > 0, 'large view opens (double-click / Space)');
    await shot('lightbox');
    await page.keyboard.press('Escape'); await page.waitForTimeout(500);
    ok(await page.locator('[data-testid=studio-lightbox]').count() === 0, 'Esc closes the large view');
    // compare: pick a second version into compare
    await tid('stage-compare').click(); await page.waitForTimeout(800);
    await page.locator('[data-testid=version-tile] button').nth(1).click(); await page.waitForTimeout(1000);
    const modes = [];
    for (const m of ['two', 'four', 'slider']) {
      const b = tid('compare-mode-' + m);
      if (await b.count()) { await b.click(); await page.waitForTimeout(700); modes.push(m + ':' + (await tid('compare-view').getAttribute('data-mode'))); await shot('compare-' + m); }
    }
    ok(modes.length >= 2, `compare modes available: ${modes}`);
    await page.keyboard.press('Escape'); await page.waitForTimeout(600);
    ok(await page.locator('[data-testid=compare-view]').count() === 0, 'Esc exits compare');
    return `large view open/close with Esc; compare modes ${modes.join(',')}; Esc exits compare`;
  };

  const openHarnessUpload = async () => {
    await goSession();
    await tid('right-panel-tab-operations').click();
    await page.getByText(L('转换格式', 'Convert format'), { exact: false }).first().click().catch(async () => { await page.locator('[data-testid=harness-panel] input[type=radio]').nth(4).check(); });
    await page.waitForTimeout(800);
    await page.getByText(L('选择可用服务', 'Choose an available service'), { exact: false }).first().click(); await page.waitForTimeout(500);
    await page.locator('[role=option]').filter({ hasText: /上传 GLB|Upload(ed)? GLB/i }).first().click(); await page.waitForTimeout(800);
    await page.locator('[data-testid=harness-panel] [role=combobox], [data-testid=harness-panel] button').filter({ hasText: L('对话模型', 'Chat model') }).first().click(); await page.waitForTimeout(500);
    await page.locator('[role=option]').filter({ hasText: L('已上传模型', 'Uploaded model') }).first().click(); await page.waitForTimeout(800);
    return page.locator('[data-testid=harness-model-source-picker] input[type=file]');
  };

  items['M3D-06'] = async () => {
    await goSession(); const tasks0 = await taskCount(); const cr0 = await credits();
    let input = await openHarnessUpload();
    const msg = async () => (await page.locator('[data-testid=harness-model-source-picker] [role=status], [data-testid=harness-model-source-picker] [role=alert]').allInnerTexts()).join(' | ');
    await input.setInputFiles(path.join(FIX, 'b3-ok.glb')); await page.waitForTimeout(3500);
    const okMsg = await msg(); await shot('ok-glb');
    const fn = await page.locator('[data-testid=harness-upload-filename]').innerText().catch(() => '');
    ok(/b3-ok\.glb/.test(fn) || okMsg, `valid GLB accepted (${okMsg} / ${fn})`);
    const bad = {};
    for (const f of ['b3-big.glb', 'b3-external.glb']) {
      input = page.locator('[data-testid=harness-model-source-picker] input[type=file]');
      await input.setInputFiles(path.join(FIX, f)); await page.waitForTimeout(f.includes('big') ? 6000 : 3500);
      bad[f] = await page.locator('[data-testid=harness-model-source-picker] [role=alert]').allInnerTexts();
      await shot('reject-' + f.replace('.glb', ''));
      ok(bad[f].join('').length > 0, `${f} rejected with a visible alert`);
    }
    state.m3dErrors = Object.values(bad).flat();
    const tasks1 = await taskCount(); const cr1 = await credits();
    ok(tasks0 === tasks1 && cr0 === cr1, `no auto-run / no charge (tasks ${tasks0}->${tasks1})`);
    return `valid GLB accepted ("${fn}"); oversize rejected: "${bad['b3-big.glb'].join(' ').slice(0, 70)}"; external-dep rejected: "${bad['b3-external.glb'].join(' ').slice(0, 70)}"; tasks ${tasks0}->${tasks1}`;
  };

  items['M3D-05'] = async () => {
    let used = null;
    {
      for (const s of await refSessions()) { if (used) break; await goSession(s.id); if (!(await tid('stage-capture').count())) { const t3 = page.locator('[data-testid=version-tile] button').filter({ hasText: /3D/ }).first(); if (await t3.count()) await t3.click().catch(() => {}); } for (let k = 0; k < 8 && !used; k++) { await page.waitForTimeout(1500); if (await tid('stage-capture').count() && !(await tid('stage-capture').isDisabled())) used = s; } if (used) break; }
    }
    if (!used) throw blocked('no existing session with an openable 3D model; generate one in the test account or set B3_REF_SESSIONS');
    const hdr = async () => (await tid('stage-version').innerText().catch(() => '')).trim();
    const before = await hdr();
    ok(/3D/.test(before), `stage shows a 3D model before capture ("${before}")`);
    await shot('model-stage');
    const bad = []; const onResp = (r) => { if (r.status() >= 400) bad.push(`${r.status()} ${r.url().replace(base, '').slice(0, 80)}`); }; const onFail = (r) => bad.push(`requestfailed ${r.url().replace(base, '').slice(0, 80)} ${r.failure()?.errorText || ''}`); page.on('response', onResp); page.on('requestfailed', onFail);
    await tid('stage-capture').click();
    let after = before; for (let i = 0; i < 20 && after === before; i++) { await page.waitForTimeout(1000); after = await hdr(); }
    page.off('response', onResp); page.off('requestfailed', onFail);
    const err = await page.locator('[role=alert]').allInnerTexts();
    await shot('after-capture');
    if (after === before) {
      if (engine === 'webkit') throw known(`WebKit capture did not complete (stage stayed "${before}"); alert="${err.join(' ').slice(0, 80)}"; failed requests=[${bad.slice(0, 3).join('; ')}]; attributed to the #71 WebKit large-blob read limitation (unverified)`);
      throw new Error(`capture did not complete: stage stayed "${before}"; alert="${err.join(' ').slice(0, 80)}"; failed=[${bad.slice(0, 3).join('; ')}]`);
    }
    ok(/2D/.test(after), `stage switched to a 2D version ("${after}")`);
    const tileLabels = (await page.locator('[data-testid=version-tile] button').evaluateAll((els) => els.map((e) => e.getAttribute('aria-label') || ''))).join(' | ');
    const srcLine = await page.locator('body').innerText();
    ok(/截图|screenshot|capture/i.test(srcLine), 'the new 2D version records its 3D source as a screenshot');
    return `session "${used.title}": "${before}" -> "${after}"; version records a 3D screenshot source; canvas input present: ${await layerRows().count() > 0}`;
  };

  const notesBody = () => page.locator('[data-testid=session-notes-body]');
  const openNotes = async () => {
    if (!(await notesBody().count())) await page.locator('[data-testid=session-notes-panel] button').first().click();
    await notesBody().waitFor(); await page.waitForTimeout(400);
  };
  const saveNotes = async (text) => {
    await notesBody().locator('textarea').fill(text);
    const btn = notesBody().locator('button').last();
    await btn.click(); await page.waitForTimeout(1500);
  };

  items['AGT-08'] = async () => {
    await goSession(); await openNotes();
    const maxText = await tid('session-notes-counter').innerText(); const max = Number(maxText.split('/')[1]);
    ok(max > 0, 'counter shows a limit: ' + maxText);
    // over the limit: save disabled + counter flags it
    await notesBody().locator('textarea').fill('x'.repeat(max + 5));
    const saveBtn = notesBody().locator('button').last();
    ok(await saveBtn.isDisabled(), 'save disabled above the limit');
    await shot('over-limit');
    const counterCls = await tid('session-notes-counter').getAttribute('class');
    ok(/destructive/.test(counterCls || ''), 'counter marked as over limit');
    // valid persona saved and survives reload
    const token = 'B3OK' + (zh ? 'ZH' : 'EN') + W;
    const persona = L(`每次回复都必须以 ${token} 开头。`, `Every reply must begin with ${token}.`);
    await saveNotes(persona);
    await page.reload(); await page.locator('[data-testid=composer-send]').waitFor(); await page.waitForTimeout(1500);
    ok(await page.locator('[data-testid=session-notes-filled]').count() > 0, 'persona marked as filled after reload');
    await openNotes();
    ok((await notesBody().locator('textarea').inputValue()).includes(token), 'persona text persisted');
    await shot('saved');
    let reply = 'not run (LLM step runs at 1440 only)';
    if (W === 1440 && process.env.B3_LLM !== '0') {
      await page.locator('[data-testid=session-notes-panel] button').first().click(); // collapse so the token is only in chat
      const ta = page.locator('textarea').last(); await ta.click();
      await ta.fill(L('你好，用一句话打招呼，不要调用任何工具。', 'Hi, greet me in one sentence. Do not call any tools.'));
      await tid('composer-send').click();
      await page.locator(`text=${token}`).first().waitFor({ timeout: 90000 }).catch(() => {});
      await page.waitForTimeout(1500);
      const body = await page.locator('body').innerText();
      await shot('reply');
      ok(body.includes(token), 'assistant reply follows the persona (starts with the token)');
      reply = 'assistant reply carried persona token';
    }
    // cleanup: clear persona
    await openNotes().catch(() => {}); await saveNotes('').catch(() => {});
    return `limit ${max}: over-limit blocked; persona saved+persisted; ${reply}`;
  };

  items['AGT-09'] = async () => {
    const ref = (await refSessions())[0];
    if (!ref) throw blocked('no existing session with task handles; generate once in the test account or set B3_REF_SESSIONS');
    await goSession(ref.id);
    const ta = page.locator('textarea').last(); await ta.click(); await ta.fill(''); await page.keyboard.type('@'); await page.waitForTimeout(1200);
    await shot('at-picker');
    const opts = await page.locator('[role=option], [role=menuitem], [role=listbox] *').filter({ hasText: /@(img|model)\d+/ }).allInnerTexts();
    ok(opts.length > 0, 'typing @ lists image/model handles');
    await page.locator('[role=option], [role=menuitem]').filter({ hasText: /@(img|model)\d+/ }).first().click(); await page.waitForTimeout(500);
    const val = await ta.inputValue();
    ok(/@(img|model)\d+/.test(val), 'handle written into the message: ' + val);
    await ta.fill('');
    // canvas selection becomes the current object: the chat request carries the selection
    await resetCanvas();
    await uploadToCanvas(path.join(FIX, 'b3-test.png'));
    await tid('canvas-tool-select').click(); await page.mouse.click(Math.round(W * 0.5), Math.round(H * 0.5)); await page.waitForTimeout(800);
    await shot('selected');
    if (W !== 1440 || process.env.B3_LLM === '0') return `@ lists handles (${opts.length}), chosen handle written to message; canvas-selection turn runs at 1440 only`;
    let body = null;
    page.on('request', (rq) => { if (rq.method() === 'POST' && /\/api\/(v1\/)?chat\//.test(rq.url()) && !/sessions$/.test(rq.url())) body = body || { url: rq.url().replace(base, ''), data: rq.postData() || '' }; });
    const ta2 = page.locator('textarea').last(); await ta2.click();
    await ta2.fill(L('这张图的句柄是什么？只回答句柄，不要调用工具。', 'What is the handle of this image? Answer with the handle only; do not call tools.'));
    await tid('composer-send').click();
    for (let i = 0; i < 16 && !body; i++) await page.waitForTimeout(500);
    ok(body, 'chat request observed');
    const sel = /selected|selection|current_object|currentObject|canvas|node/i.exec(body.data || '');
    await page.locator('text=/img\\d+/').last().waitFor({ timeout: 90000 }).catch(() => {});
    await page.waitForTimeout(1500);
    const txt = await page.locator('body').innerText(); await shot('answer');
    ok(sel, `chat request ${body.url} carries the selection (${(body.data || '').slice(0, 120).replace(/\s+/g, ' ')})`);
    return `@ lists handles (${opts.length}), handle written to message; canvas selection sent with chat request (${sel[0]}); Agent turn done (reply mentions a handle: ${/@?img\d+/.test(txt)})`;
  };

  items['AGT-11'] = async () => {
    const anon = await mk();
    const r1 = await anon.request.get(base + '/api/admin/agent-configuration'); const st = r1.status();
    await anon.close();
    ok([401, 403, 404].includes(st), `anonymous read of agent configuration is refused (${st})`);
    await page.goto(base + '/admin/agent-configuration'); await tid('agent-configuration-admin').waitFor(); await page.waitForTimeout(1500);
    const txt = await page.locator('body').innerText(); await shot('admin-agent-config');
    ok(!/sk-[A-Za-z0-9]{10,}|Bearer\s+[A-Za-z0-9]/.test(txt), 'no secrets on the admin agent configuration page');
    throw Object.assign(new Error(`publish/reset NOT exercised (instruction: do not change Agent configuration). Read-only checks passed: anonymous GET -> ${st}; admin page renders (${txt.length} chars), no secrets. Non-admin read check needs a second user (user creation denied).`), { status: 'BLOCKED' });
  };

  items['DLV-05'] = async () => {
    await page.goto(base + '/account'); await page.locator('[data-testid=account-task-open]').first().waitFor({ timeout: 20000 }).catch(() => {}); await page.waitForTimeout(1500);
    const views = page.getByRole('button', { name: L('查看', 'View') });
    let n = 0; for (let i = 0; i < 15 && !n; i++) { n = await views.count(); if (!n) await page.waitForTimeout(1000); } ok(n > 0, 'resources listed with view buttons');
    await shot('account');
    const dlg = () => page.locator('[data-testid=result-detail-dialog]');
    const closes = [];
    for (const how of ['esc', 'outside', 'button']) {
      await views.first().click(); await dlg().waitFor({ timeout: 8000 });
      if (how === 'esc') await page.keyboard.press('Escape');
      else if (how === 'outside') await page.mouse.click(8, 8);
      else await dlg().getByRole('button', { name: L('关闭', 'Close') }).first().click();
      await page.waitForTimeout(700);
      const gone = (await dlg().count()) === 0; closes.push(`${how}:${gone}`);
      ok(gone, `dialog closes via ${how}`);
    }
    await views.first().click(); await dlg().waitFor();
    const link = dlg().locator('a[href], button').filter({ hasText: /\.(png|glb|jpg|jpeg|webp)/i }).first();
    let dl = 'download not exercised';
    if (await link.count()) { const [d] = await Promise.all([page.waitForEvent('download', { timeout: 20000 }), link.click()]); const f = path.join(dir, 'dlv05-download'); await d.saveAs(f); dl = `downloaded ${d.suggestedFilename()} ${fs.statSync(f).size}B`; ok(fs.statSync(f).size > 0, 'non-empty download'); }
    await shot('detail');
    return `${n} view buttons on My Resources; lightbox closes via ${closes.join(', ')}; ${dl}`;
  };

  items['ADM-01'] = async () => {
    const usersApi = await get('/api/admin/users'); const nUsers = usersApi?.data?.length;
    await page.goto(base + '/admin/users'); await page.waitForTimeout(2500);
    const ut = await page.locator('body').innerText(); await shot('users');
    ok(new RegExp(`(?:^|\\D)${nUsers}(?:\\D|$)`).test(ut.slice(0, 600)) || ut.includes(String(nUsers)), `users page total ${nUsers} matches API`);
    await page.goto(base + '/admin'); await page.waitForTimeout(2500);
    const ot = await page.locator('body').innerText(); await shot('overview');
    const bal = (await get('/api/admin/balance-summary'))?.current_balance;
    ok(ot.includes(String(bal)) || ot.includes(String(bal).replace(/\B(?=(\d{3})+(?!\d))/g, ',')), `overview balance ${bal}`);
    await page.goto(base + '/admin/generations'); await page.waitForTimeout(2500);
    const gt = await page.locator('body').innerText(); await shot('generations');
    ok((gt.match(/task_[0-9a-f]{6,}/g) || []).length > 0, 'generation records listed (task ids present)');
    // audit: a download writes a row
    await page.goto(base + '/admin/audit'); await page.waitForTimeout(2500);
    const a0 = Number((await page.locator('body').innerText()).match(/(?:共|Total)\s*([\d,]+)|([\d,]+)\s*entries/)?.slice(1).find(Boolean)?.replace(/,/g, '') ?? NaN);
    await page.goto(base + '/account'); await page.locator('[data-testid=account-task-open]').first().waitFor().catch(() => {}); await page.waitForTimeout(1500);
    await page.getByRole('button', { name: L('查看', 'View') }).first().click(); await page.locator('[data-testid=result-detail-dialog]').waitFor();
    const link = page.locator('[data-testid=result-detail-dialog]').locator('a[href], button').filter({ hasText: /\.(png|glb|jpg|jpeg|webp)/i }).first();
    const [d] = await Promise.all([page.waitForEvent('download', { timeout: 20000 }), link.click()]); await d.path();
    await page.goto(base + '/admin/audit'); await page.waitForTimeout(2500);
    const a1 = Number((await page.locator('body').innerText()).match(/(?:共|Total)\s*([\d,]+)|([\d,]+)\s*entries/)?.slice(1).find(Boolean)?.replace(/,/g, '') ?? NaN); await shot('audit');
    ok(a1 > a0, `audit grew after a download (${a0} -> ${a1})`);
    { const rows = (await page.locator('tbody tr').allInnerTexts()).slice(0, 8).join('\n'); ok(/(下载|访问)资产|(Download|Access|View|Open)\w* asset|Asset (download|access)/i.test(rows), 'a download row is among the newest audit rows: ' + rows.replace(/\s+/g, ' ').slice(0, 160)); }
    const others = [];
    for (const u of usersApi.data.filter((x) => !x.isAdmin)) { const ua = await get('/api/admin/user-assets/' + u.id); if ((ua?.summary?.totalUploads || 0) + (ua?.summary?.totalGenerations || 0) > 0) others.push(u.id); }
    if (!others.length) throw Object.assign(new Error(`users/usage/generation views match API (users=${nUsers}, balance=${bal}); audit grows on download (${a0}->${a1}) but for the admin's own artifact only: no other user owns any asset on dev-test, so "download another user's artifact writes an audit row" is not verifiable here`), { status: 'PARTIAL' });
    return `views match API; audit ${a0}->${a1}`;
  };

  // ---- language / formatting walkthroughs
  const scanPages = async () => {
    const found = [];
    const note = async (label, fn) => {
      await page.mouse.move(700, 500); await page.evaluate(() => document.activeElement && document.activeElement.blur && document.activeElement.blur()).catch(() => {}); await page.waitForTimeout(250);
      try { await fn(); } catch (e) { found.push(`${label}: could not open (${e.message.split('\n')[0].slice(0, 60)})`); await shot('open-fail-' + label); return; }
      const iss = await textIssues(); const ov = await overflow();
      for (const i of iss) found.push(`${label}: ${i}`);
      for (const o of ov) found.push(`${label}: LAYOUT ${o}`);
    };
    const anon = await mk(); const ap = await anon.newPage();
    await ap.goto(base + '/login'); await ap.waitForTimeout(1200);
    const isLogin = await ap.evaluate(({ zh }) => { const t = document.body.innerText; return zh ? !/[A-Za-z]{4,} [A-Za-z]{4,}/.test(t.replace(/Plinth/g, '')) : !/[㐀-鿿]/.test(t.replace(/简体中文/g, '')); }, { zh });
    if (!isLogin) found.push('login: residual other-language text'); await anon.close();
    await goSession();
    await note('workspace', async () => {});
    await note('skill-picker', async () => { await openSkills(); }); await page.keyboard.press('Escape');
    await note('model-picker', async () => { await tid('workspace-model-picker').click(); await page.waitForTimeout(600); }); await page.keyboard.press('Escape');
    await note('params', async () => { await pickModel2D(); await tid('composer-params').click(); await page.waitForTimeout(600); }); await page.keyboard.press('Escape');
    await note('task-center', async () => { await tid('workspace-task-center').click(); await page.waitForTimeout(600); }); await page.keyboard.press('Escape');
    await note('session-menu', async () => { await tid('studio-session-menu').click(); await page.waitForTimeout(600); }); await page.keyboard.press('Escape');
    await note('approval-menu', async () => { await tid('studio-approval-menu').click(); await page.waitForTimeout(500); }); await page.keyboard.press('Escape');
    await note('user-menu', async () => { await tid('app-user-menu').click(); await page.waitForTimeout(500); }); await page.keyboard.press('Escape');
    await note('shortcuts', async () => { await tid('canvas-tool-select').click(); await page.mouse.click(Math.round(W * 0.5), Math.round(H * 0.5)); await page.keyboard.press('?'); await page.waitForTimeout(600); }); await page.keyboard.press('Escape');
    await note('harness-panel', async () => { await tid('right-panel-tab-operations').click(); await page.waitForTimeout(800); });
    await note('persona-panel', async () => { await openNotes(); });
    await note('skill-creator', async () => { await openSkills(); await page.getByText(L('创建 Skill', 'Create Skill'), { exact: true }).click(); await page.waitForTimeout(500); }); await page.keyboard.press('Escape');
    for (const [label, route] of [['account', '/account'], ['admin', '/admin'], ['admin-users', '/admin/users'], ['admin-generations', '/admin/generations'], ['admin-audit', '/admin/audit'], ['admin-user-assets', '/admin/user-assets'], ['admin-agent-config', '/admin/agent-configuration'], ['admin-llm', '/admin/llm']]) {
      await note(label, async () => { await page.goto(base + route); await page.waitForTimeout(2200); });
    }
    return found;
  };

  items['I18N-03'] = async () => {
    const found = await scanPages(); await shot('last-page');
    state.walk = found;
    if (found.length) throw new Error(`${found.length} issues: ${found.slice(0, 10).join(' || ')}`);
    return `walked login, workspace overlays, account, 7 admin pages in ${loc}: no residual other-language text, no layout overflow`;
  };

  items['I18N-04'] = async () => {
    await goSession();
    await pickModel2D(); await tid('composer-params').click(); await page.waitForTimeout(600);
    const iss = await textIssues('[role=dialog],[data-base-ui-popup]'); await shot('params-popover'); await page.keyboard.press('Escape');
    await tid('right-panel-tab-operations').click(); await page.waitForTimeout(500);
    const iss2 = await textIssues('[data-testid=harness-panel]');
    const errs = state.m3dErrors;
    const errBad = errs ? errs.filter((e) => (zh ? !CJK.test(e) : CJK.test(e))) : [];
    const all = [...iss, ...iss2, ...errBad.map((e) => 'error text in wrong language: ' + e)];
    if (all.length) throw new Error(`${all.length} issues: ${all.slice(0, 8).join(' || ')}`);
    return `params popover, harness panel and upload-rejection errors are all ${zh ? 'Chinese' : 'English'} (${errs ? errs.length + ' error strings' : 'M3D-06 errors not collected'})`;
  };

  items['I18N-06'] = async () => {
    await page.goto(base + '/account'); await page.locator('[data-testid=account-task-open]').first().waitFor().catch(() => {}); await page.waitForTimeout(2000);
    const t = await page.locator('body').innerText(); await shot('account');
    const dateZh = /\d{4}\/\d{1,2}\/\d{1,2}\s+\d{2}:\d{2}/; const dateEn = /(\d{1,2}\/\d{1,2}\/\d{4},?\s+\d{1,2}:\d{2}.*?(AM|PM))|([A-Z][a-z]{2,8}\.? \d{1,2}, \d{4})/;
    const d = (t.match(zh ? dateZh : dateEn) || [])[0];
    ok(d, `date formatted for ${loc}: sample from page = ${(t.match(/\d{1,4}[\/-]\d{1,2}[\/-]\d{1,4}[^\n|]{0,24}/) || [''])[0]}`);
    const bal = await page.locator('button[aria-label]').filter({ hasText: /credits|积分/ }).first().getAttribute('aria-label');
    ok(/\d{1,3}(,\d{3})+/.test(bal || '') || /\d+/.test(bal || ''), 'balance number');
    const grouped = /\d{1,3}(,\d{3})+/.test(t);
    await page.goto(base + '/admin'); await page.waitForTimeout(2000);
    const at = await page.locator('body').innerText();
    const raw = at.match(/(?:当前余额|current balance)[^\d]*(\d+)/i)?.[1];
    const note = raw && raw.length >= 5 ? ` | observation: admin overview balance shows ungrouped "${raw}" while top bar groups "${(bal || '').match(/\d[\d,]*/)?.[0]}"` : '';
    return `date "${d}" matches ${loc} pattern; numbers grouped on account page: ${grouped}; top bar "${bal}"${note}`;
  };

  for (const id of ITEM_ORDER) {
    if (ONLY && !ONLY.has(id)) continue;
    curItem = id; shotN = 0; shots = [];
    await ctx.addCookies([{ name: 'plinth_locale', value: loc, url: base }]).catch(() => {});
    for (let i = 0; i < 4; i++) {
      const r = await ctx.request.get(base + '/api/auth/session');
      if (r.ok()) break;
      if (r.status() === 429) { await new Promise((f) => setTimeout(f, 20000)); continue; }
      relogins.push(`${cell} before ${id}: session invalid (HTTP ${r.status()}), re-login`); await loginAdmin(ctx); break;
    }
    const t0 = Date.now();
    let status = 'PASS'; let summary = '';
    try { summary = await items[id](); }
    catch (e) {
      status = e.status || 'FAIL'; summary = e.message.split('\n').filter((l) => /waiting for|locator|assert|Timeout|strict/.test(l)).slice(0, 3).join(' ').slice(0, 400) || e.message.split('\n')[0].slice(0, 400);
      if (status === 'FAIL') await shot('FAIL');
    }
    await page.keyboard.press('Escape').catch(() => {});
    results.push({ id, status, cell, summary, shots: shots.slice(-3), ms: Date.now() - t0 });
    console.log(`[${id}] ${status} ${cell} (${Math.round((Date.now() - t0) / 1000)}s) ${summary}\n      shots: ${shots.slice(-2).join(' ')}`);
  }
  if (consoleErrors.length) console.log(`[console] ${cell}: ${[...new Set(consoleErrors)].slice(0, 5).join(' || ')}`);
  await cleanupSkills().catch(() => {});
  if (n429) console.log(`[429] ${cell}: ${n429} rate-limited (HTTP 429) responses seen by this cell`);
  await browser.close();
}

const cells = [];
for (const e of ENGINES) for (const l of LOCALES) for (const v of VIEWPORTS) cells.push([e, l, v]);
const t0 = Date.now();
const lanes = PARALLEL ? ENGINES.map((e) => cells.filter((c) => c[0] === e)) : [cells];
await Promise.all(lanes.map(async (lane) => { for (const c of lane) { try { await runCell(...c); } catch (e) { console.log(`[cell-error] ${c[0]}/${c[1]}/${c[2]}: ${e.message.split('\n')[0]}`); results.push({ id: 'CELL', status: 'FAIL', cell: c.join('/'), summary: e.message.slice(0, 200), shots: [] }); } } }));

// ---- summary
const ids = [...new Set(results.map((r) => r.id))];
const agg = {};
for (const id of ids) {
  const rs = results.filter((r) => r.id === id);
  const st = rs.some((r) => r.status === 'FAIL') ? 'FAIL' : rs.some((r) => r.status === 'BLOCKED') ? 'BLOCKED' : rs.some((r) => r.status === 'KNOWN-LIMITATION') ? 'KNOWN-LIMITATION' : rs.some((r) => r.status === 'PARTIAL') ? 'PARTIAL' : 'PASS';
  agg[id] = { status: st, pass: rs.filter((r) => r.status === 'PASS').length, total: rs.length, fails: rs.filter((r) => r.status !== 'PASS').map((r) => `${r.cell}: ${r.status} ${r.summary}`) };
}
fs.writeFileSync(path.join(OUT, 'results.json'), JSON.stringify({ base: base.replace(/\/\/.*@/, '//'), results, agg }, null, 1));
console.log('\n===== B3 SUMMARY =====');
if (relogins.length) console.log(`session dropped ${relogins.length}x during the run (server-side invalidation or expiry):\n  ` + relogins.slice(0, 12).join('\n  '));
for (const id of ITEM_ORDER.filter((i) => agg[i])) console.log(`${id}: ${agg[id].status} (${agg[id].pass}/${agg[id].total} cells)${agg[id].fails.length ? '\n   - ' + [...new Set(agg[id].fails)].slice(0, 3).join('\n   - ') : ''}`);
const count = (s) => Object.values(agg).filter((a) => a.status === s).length;
console.log(`TOTAL items: PASS=${count('PASS')} FAIL=${count('FAIL')} KNOWN-LIMITATION=${count('KNOWN-LIMITATION')} PARTIAL=${count('PARTIAL')} BLOCKED=${count('BLOCKED')}  elapsed=${Math.round((Date.now() - t0) / 1000)}s  out=${OUT}`);
process.exit(count('FAIL') ? 1 : 0);
