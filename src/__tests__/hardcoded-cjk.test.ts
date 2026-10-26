import { existsSync, readdirSync, readFileSync, statSync } from 'node:fs';
import { join, relative } from 'node:path';

import { describe, expect, it } from 'vitest';

import allowlist from './hardcoded-cjk-allowlist.json';

/**
 * 硬编码中文守门（#254）。界面文案的唯一真源是 Product Schema：默认语言写在
 * `content.messages` 与各结构化字段，其他语言写在 `localization.bundles`，前端用
 * `useProductSchema().text(key)` / `schemaText` 读取。浏览器源码里的中文（去掉注释后）
 * 只能是白名单里登记过的存量，且白名单只能缩小：
 * - 不在白名单的文件出现中文 → 失败（把文案移进 Schema）；
 * - 白名单文件的中文行数超过登记数 → 失败；
 * - 实际行数少于登记数 → 失败，提示把登记数改小（守住缩小方向）。
 * 也不允许在源码里写死 `'zh-CN'` 之类的语言标签：用 `activeLocale(schema)`。
 */
const ROOTS = ['src', 'web/src'];
const SOURCE = /\.(?:[cm]?[jt]sx?|css)$/;
const TEST = /(?:\.test\.|\.spec\.|__tests__)/;
const CJK = /[㐀-鿿豈-﫿　-〿＀-￯]/;
const LOCALE_LITERAL = /(['"`])(?:zh-CN|zh-TW|en-US|en-GB)\1/;

type Allowlist = Record<string, { lines: number; reason: string }>;

function sourceFiles(dir: string): string[] {
  const out: string[] = [];
  for (const entry of readdirSync(dir)) {
    const full = join(dir, entry);
    if (statSync(full).isDirectory()) {
      if (entry !== 'node_modules') out.push(...sourceFiles(full));
    } else if (SOURCE.test(entry) && !TEST.test(full)) {
      out.push(full);
    }
  }
  return out;
}

/**
 * Blanks out comments (`//`, `/* *\/`, JSX `{/* *\/}`) and keeps strings,
 * template literals, regex literals and code, line breaks included, so line
 * numbers still match the file. A `/` starts a regex where an expression may
 * begin; otherwise it is division.
 */
function stripComments(source: string): string {
  let out = '';
  let index = 0;
  // Each entry is the brace depth of a `${` inside a template literal.
  const templates: number[] = [];
  let depth = 0;
  let previous = '';
  const blank = (text: string) => text.replace(/[^\n]/g, ' ');
  const regexMayStart = () => previous === '' || /[(,=:[!&|?{};+\-*%<>~^]$/.test(previous) || /\b(?:return|typeof|case|in|of|delete|void|throw|new|else|do)$/.test(previous);
  while (index < source.length) {
    const char = source[index];
    const next = source[index + 1];
    if (char === '/' && next === '/') {
      const end = source.indexOf('\n', index);
      const stop = end < 0 ? source.length : end;
      out += blank(source.slice(index, stop));
      index = stop;
      continue;
    }
    if (char === '/' && next === '*') {
      const end = source.indexOf('*/', index + 2);
      const stop = end < 0 ? source.length : end + 2;
      out += blank(source.slice(index, stop));
      index = stop;
      continue;
    }
    if (char === '"' || char === "'") {
      let end = index + 1;
      while (end < source.length && source[end] !== char && source[end] !== '\n') end += source[end] === '\\' ? 2 : 1;
      out += source.slice(index, end + 1);
      index = end + 1;
      previous = 'x';
      continue;
    }
    if (char === '`' || (char === '}' && templates.length > 0 && templates[templates.length - 1] === depth)) {
      if (char === '}') templates.pop();
      let end = index + 1;
      while (end < source.length && source[end] !== '`' && !(source[end] === '$' && source[end + 1] === '{')) end += source[end] === '\\' ? 2 : 1;
      if (source[end] === '$') {
        templates.push(depth);
        out += source.slice(index, end + 2);
        index = end + 2;
        previous = '{';
      } else {
        out += source.slice(index, end + 1);
        index = end + 1;
        previous = 'x';
      }
      continue;
    }
    if (char === '/' && regexMayStart()) {
      let end = index + 1;
      let inClass = false;
      while (end < source.length && source[end] !== '\n' && (inClass || source[end] !== '/')) {
        if (source[end] === '\\') end += 1;
        else if (source[end] === '[') inClass = true;
        else if (source[end] === ']') inClass = false;
        end += 1;
      }
      out += source.slice(index, end + 1);
      index = end + 1;
      previous = 'x';
      continue;
    }
    if (char === '{') depth += 1;
    if (char === '}') depth -= 1;
    out += char;
    index += 1;
    if (!/\s/.test(char)) previous = (previous + char).slice(-8);
  }
  return out;
}

function cjkLines(file: string): Array<{ line: number; text: string }> {
  const source = readFileSync(file, 'utf8');
  const stripped = stripComments(source).split('\n');
  const original = source.split('\n');
  return stripped.flatMap((text, index) => (CJK.test(text) ? [{ line: index + 1, text: original[index].trim() }] : []));
}

describe('stripComments', () => {
  it('drops comments but keeps strings, templates and regexes', () => {
    const source = [
      "const a = '中'; // 注释",
      '/* 块注释 */ const b = `模板${x ? `内层` : \'\'}`;',
      '{/* JSX 注释 */}<p>正文</p>',
      'const r = /[，。]/g; const half = 1 / 2; // 二',
      "const url = 'https://x.y/z'; // 尾",
    ].join('\n');
    const stripped = stripComments(source).split('\n');
    expect(stripped.map((line) => CJK.test(line))).toEqual([true, true, true, true, false]);
    expect(stripped[0]).not.toContain('注释');
    expect(stripped[1]).not.toContain('块注释');
    expect(stripped[2]).not.toContain('JSX');
    expect(stripped[3]).toContain('/[，。]/g');
    expect(stripped[3]).not.toContain('二');
  });
});

describe('hard-coded Chinese in browser source', () => {
  const files = ROOTS.flatMap(sourceFiles);
  const listed = allowlist as Allowlist;

  it('only appears in allow-listed files, within their recorded line counts', () => {
    const offenders = files.flatMap((file) => {
      const path = relative('.', file);
      const lines = cjkLines(file);
      const allowed = listed[path]?.lines ?? 0;
      return lines.length > allowed
        ? [`${path}: ${lines.length} 行（白名单 ${allowed}）\n${lines.map((item) => `  ${item.line}: ${item.text}`).join('\n')}`]
        : [];
    });
    expect(offenders, '界面文案写进 Product Schema（content.messages + localization.bundles），用 text(key) 读取').toEqual([]);
  });

  it('keeps the allow-list tight so it only shrinks', () => {
    const stale = Object.entries(listed).flatMap(([path, entry]) => {
      if (!existsSync(path)) return [`${path}: 文件已不存在，删除白名单条目`];
      const actual = cjkLines(path).length;
      return actual < entry.lines ? [`${path}: 实际 ${actual} 行，把白名单改为 ${actual}`] : [];
    });
    expect(stale).toEqual([]);
    for (const [path, entry] of Object.entries(listed)) {
      expect(entry.reason.trim(), `${path} 需要写明保留理由`).not.toBe('');
    }
  });

  it('never hard-codes a locale tag', () => {
    const offenders = files.flatMap((file) => stripComments(readFileSync(file, 'utf8'))
      .split('\n')
      .flatMap((text, index) => (LOCALE_LITERAL.test(text) ? [`${relative('.', file)}:${index + 1}`] : [])));
    expect(offenders, '用 activeLocale(schema) 取界面语言').toEqual([]);
  });
});
