import { describe, expect, it } from 'vitest';
import { readFileSync, readdirSync, statSync } from 'node:fs';
import { join } from 'node:path';
import defaultCatalog from '../../internal/catalog/default_catalog.json';

// 组件里以字符串字面量引用的 Schema 文案 key 必须真实存在。
// 缺 key 时 schemaText 静默返回空串，曾导致工具按钮 title 为空、
// 文字节点内容为空却不报错；本测试静态提取字面量拦截这一类缺陷。
interface Dictionary {
  [key: string]: string;
}

const messages = defaultCatalog.application.content
  .messages as unknown as Dictionary;
const canvasText = (defaultCatalog.application as { canvas: { presentation: { text: Dictionary } } })
  .canvas.presentation.text;

function collectSourceFiles(dir: string): string[] {
  const result: string[] = [];
  for (const entry of readdirSync(dir)) {
    const fullPath = join(dir, entry);
    if (statSync(fullPath).isDirectory()) {
      result.push(...collectSourceFiles(fullPath));
    } else if (/\.tsx?$/.test(entry) && !entry.includes('.test.')) {
      result.push(fullPath);
    }
  }
  return result;
}

const dottedLiteral = /['"]([a-z_]+(?:\.[a-z0-9_]+)+)['"]/g;

describe('schema message key existence', () => {
  it('resolves every dotted key literal in components and pages', () => {
    const files = [
      ...collectSourceFiles(join(__dirname, '..', 'components')),
      ...collectSourceFiles(join(__dirname, '..', 'app')),
    ];
    expect(files.length).toBeGreaterThan(0);
    const missing: { key: string; file: string }[] = [];
    for (const file of files) {
      const source = readFileSync(file, 'utf8');
      for (const match of source.matchAll(dottedLiteral)) {
        const key = match[1];
        if (!(key in messages) && !(key in canvasText)) {
          missing.push({ key, file });
        }
      }
    }
    expect(
      missing.map(({ key, file }) => `${key} (${file})`),
    ).toEqual([]);
  });
});
