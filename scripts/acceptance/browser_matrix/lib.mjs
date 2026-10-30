// Shared helpers for the acceptance browser scripts (Playwright resolution and admin login).
import { createRequire } from 'node:module';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..', '..', '..');

// Playwright is resolved from PLAYWRIGHT_MODULE_PATH, then the repository's own dependencies, then the
// default module lookup. Install browsers with `pnpm exec playwright install chromium webkit` if missing.
export function resolvePlaywright() {
  const attempts = [
    () => createRequire(import.meta.url)(process.env.PLAYWRIGHT_MODULE_PATH),
    () => createRequire(path.join(repoRoot, 'package.json'))('playwright'),
    () => createRequire(import.meta.url)('playwright'),
  ];
  for (const [i, load] of attempts.entries()) {
    if (i === 0 && !process.env.PLAYWRIGHT_MODULE_PATH) continue;
    try { return load(); } catch {}
  }
  throw new Error('playwright not found; run pnpm install in the repository or set PLAYWRIGHT_MODULE_PATH');
}
export const base = (process.env.BASE || '').replace(/\/+$/, '');
export function requireEnv(...names) {
  const missing = names.filter((n) => !process.env[n]);
  if (missing.length) {
    console.error(`missing environment: ${missing.join(', ')}\nrun through: python3 scripts/release-env.py smoke <environment> -- node <script>`);
    process.exit(2);
  }
}
export async function loginAdmin(ctx) {
  const r = await ctx.request.post(base + '/api/auth/login', {
    data: { username: process.env.ADMIN_USERNAME || '', password: process.env.ADMIN_PASSWORD || '' },
    headers: { Origin: new URL(base).origin } });
  if (r.status() !== 200 && process.env.B3_DEBUG) console.log('login status', r.status());
  return r.status();
}
