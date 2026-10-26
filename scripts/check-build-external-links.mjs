// Fails when the Go web build output references an internet host. The downstream intranet has no internet, so
// a CDN/font URL left in the bundle is a request that can never succeed. Run after `vite build`:
//   node scripts/check-build-external-links.mjs [build/go-web/assets]
import { readdirSync, readFileSync, statSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const root = path.join(path.dirname(fileURLToPath(import.meta.url)), '..');
const target = path.resolve(process.argv[2] ?? path.join(root, 'build', 'go-web', 'assets'));
// "fonts." is only a host when it follows a scheme or "//" (document.fonts is the FontFaceSet API).
const forbidden = /gstatic|jsdelivr|unpkg|googleapis|(?:\/\/|\.)fonts\.(?:google|gstatic|bunny|adobe)/i;

function* files(dir) {
  for (const name of readdirSync(dir)) {
    const full = path.join(dir, name);
    if (statSync(full).isDirectory()) yield* files(full);
    else if (!name.endsWith('.map') && /\.(js|mjs|css|html|json|svg)$/.test(name)) yield full;
  }
}

const hits = [];
for (const file of files(target)) {
  const text = readFileSync(file, 'utf8');
  const match = forbidden.exec(text);
  if (match) hits.push(`${path.relative(root, file)}: ...${text.slice(Math.max(0, match.index - 40), match.index + 60).replace(/\s+/g, ' ')}...`);
}
if (hits.length > 0) {
  console.error(`external links found in ${path.relative(root, target)}:\n${hits.join('\n')}`);
  process.exit(1);
}
console.log(`no external links in ${path.relative(root, target)}`);
