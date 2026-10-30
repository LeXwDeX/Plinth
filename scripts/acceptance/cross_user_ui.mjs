// UI half of cross_user.py (AUTH-03 "ordinary user sees no admin entry", ADM-01 "admin download writes an audit row").
// Started by `cross_user.py --ui` with BASE, ADMIN_USERNAME/ADMIN_PASSWORD, USER_USERNAME/USER_PASSWORD (a non-admin) and
// OUT set against the throwaway local stack. Uses the zh-CN login page labels. Exit status 1 on a failed check.
import fs from 'node:fs'; import os from 'node:os'; import path from 'node:path';
import { resolvePlaywright, requireEnv } from './browser_matrix/lib.mjs';
requireEnv('BASE', 'ADMIN_USERNAME', 'ADMIN_PASSWORD', 'USER_USERNAME', 'USER_PASSWORD');
const base = process.env.BASE.replace(/\/+$/, '');
const OUT = process.env.OUT || fs.mkdtempSync(path.join(os.tmpdir(), 'plinth-acceptance-cross-ui-')); fs.mkdirSync(OUT, { recursive: true });
const pw = resolvePlaywright();
const b = await pw.chromium.launch();
const failures = [];
const check = (name, cond, detail = '') => { console.log(`${cond ? 'PASS' : 'FAIL'}: ${name} ${detail}`); if (!cond) failures.push(name); };

async function session(user, pass, tag) {
  const ctx = await b.newContext({ viewport: { width: 1440, height: 900 }, locale: 'zh-CN', acceptDownloads: true }); const p = await ctx.newPage();
  const errs = []; p.on('pageerror', (e) => errs.push(String(e)));
  await p.goto(base + '/login'); await p.getByPlaceholder('请输入用户名').fill(user); await p.getByPlaceholder('请输入密码').fill(pass);
  await p.getByRole('button', { name: '登录' }).click(); await p.waitForTimeout(2500);
  await p.screenshot({ path: path.join(OUT, `${tag}-home.png`) });
  return { ctx, p, errs };
}

// ordinary user
const u = await session(process.env.USER_USERNAME, process.env.USER_PASSWORD, 'user');
const links = await u.p.locator('a[href^="/admin"]').evaluateAll((e) => e.map((x) => x.getAttribute('href')));
await u.p.locator('[aria-haspopup="menu"]').first().click({ timeout: 3000 }).catch(() => {}); await u.p.waitForTimeout(500);
const menu = await u.p.locator('a[href^="/admin"], [role=menuitem]').allInnerTexts();
await u.p.screenshot({ path: path.join(OUT, 'user-menu.png') });
check('ordinary user has no admin link on the workspace', links.length === 0, JSON.stringify(links));
check('ordinary user menu has no admin entry', !menu.some((t) => /管理后台|用户管理|Agent 管理/.test(t)), JSON.stringify(menu));
for (const route of ['/admin', '/admin/users', '/admin/audit', '/admin/generations', '/admin/agent-configuration']) {
  await u.p.goto(base + route); await u.p.waitForTimeout(1500);
  const text = (await u.p.locator('body').innerText()).replace(/\s+/g, ' ');
  const denied = !u.p.url().startsWith(base + route) || /无权|没有权限|403|forbidden|not authorized/i.test(text) || !/用户管理|审计|生成记录/.test(text);
  check(`ordinary user denied on ${route}`, denied, `-> ${u.p.url().replace(base, '')}`);
}

// admin
const a = await session(process.env.ADMIN_USERNAME, process.env.ADMIN_PASSWORD, 'admin');
const pages = { '/admin': 'dash', '/admin/users': 'users', '/admin/generations': 'gens', '/admin/audit': 'audit', '/admin/user-assets': 'uassets', '/admin/agent-configuration': 'agentcfg' };
for (const [route, tag] of Object.entries(pages)) {
  await a.p.goto(base + route); await a.p.waitForTimeout(2200);
  const t = (await a.p.locator('body').innerText()).replace(/\s+/g, ' ');
  await a.p.screenshot({ path: path.join(OUT, `admin-${tag}.png`), fullPage: true });
  check(`admin page ${route} renders`, a.p.url().startsWith(base + route) && t.length > 40, `${t.length} chars`);
}
const count = async () => (await (await a.ctx.request.get(base + '/api/admin/audit-logs?action=download_asset&pageSize=100')).json()).total;
const before = await count();
await a.p.goto(base + '/admin/generations'); await a.p.waitForTimeout(2000);
try {
  const [dl] = await Promise.all([a.p.waitForEvent('download', { timeout: 15000 }), a.p.getByRole('button', { name: '下载' }).first().click()]);
  const f = path.join(OUT, 'ui-download-' + dl.suggestedFilename()); await dl.saveAs(f); const size = fs.statSync(f).size; fs.unlinkSync(f);
  await a.p.waitForTimeout(800);
  const after = await count();
  check('admin UI download writes a download_asset audit row', size > 0 && after > before, `file ${dl.suggestedFilename()} ${size} bytes, rows ${before} -> ${after}`);
} catch (e) { check('admin UI download', false, String(e.message).split('\n')[0]); }
check('no page errors', [...u.errs, ...a.errs].length === 0, JSON.stringify([...u.errs, ...a.errs]));
await b.close();
process.exit(failures.length ? 1 : 0);
