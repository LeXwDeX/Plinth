import { readdirSync, readFileSync, statSync } from 'node:fs';
import { join } from 'node:path';

import { expect, it } from 'vitest';

/**
 * 开源前端不带某个组织的域名、品牌键、内网地址或服务器路径（#249）。
 * 部署身份只来自公开 Schema 与运行配置。测试文件里的同类守门正则不算命中，不扫描。
 * 正则用字符类写成，使本文件不含这些标识的明文（公开快照扫描 scripts/check-public-tree.sh，#261）。
 */
const ROOTS = ['src', 'web'];
const ORGANIZATION_IDENTIFIERS = /yc[g]ame|yun[c]han|云[蟾]|192\.168\.|\/DATA\/AppData/i;

function sourceFiles(dir: string): string[] {
  let entries: string[];
  try {
    entries = readdirSync(dir);
  } catch {
    return [];
  }
  return entries.flatMap((name) => {
    const path = join(dir, name);
    if (name === 'node_modules' || name === 'dist') return [];
    if (statSync(path).isDirectory()) return sourceFiles(path);
    return /\.(tsx?|jsx?|css|html|json)$/.test(name) ? [path] : [];
  });
}

it('frontend sources carry no organization identifiers', () => {
  const offenders = ROOTS.flatMap(sourceFiles)
    .filter((path) => !/\.test\.[jt]sx?$/.test(path))
    .filter((path) => ORGANIZATION_IDENTIFIERS.test(readFileSync(path, 'utf8')));
  expect(offenders).toEqual([]);
});
