import { readdirSync, readFileSync, statSync } from 'node:fs';
import { join } from 'node:path';

import { describe, expect, it } from 'vitest';

/**
 * #237：界面交互动效（hover、选中、弹层、面板、进度等过渡）全部在 150ms 内完成。
 * 守住 Tailwind 时长/延迟工具类与内联 transition 写法；持续循环的加载指示
 * （animate-spin、animate-pulse）是状态提示而非交互过渡，不在此限。
 */
const ROOTS = ['src/app', 'src/components', 'src/hooks', 'src/lib', 'src/store'];
const LIMIT_MS = 150;

function sourceFiles(dir: string): string[] {
  return readdirSync(dir).flatMap((entry) => {
    const full = join(dir, entry);
    if (statSync(full).isDirectory()) return sourceFiles(full);
    return /\.(tsx?|css)$/.test(entry) && !/\.test\.tsx?$/.test(entry) ? [full] : [];
  });
}

function milliseconds(value: string, unit: string | undefined): number {
  const amount = Number(value);
  return unit === 's' ? amount * 1000 : amount;
}

const PATTERNS: Array<{ pattern: RegExp; read: (match: RegExpMatchArray) => number }> = [
  // duration-200, delay-300, animation-duration-500
  { pattern: /(?:^|[\s"'`:])(?:animation-)?(?:duration|delay)-(\d+)(?![\w-])/g, read: (m) => Number(m[1]) },
  // duration-[400ms], delay-[0.3s]
  { pattern: /(?:duration|delay)-\[(\d+(?:\.\d+)?)(ms|s)\]/g, read: (m) => milliseconds(m[1], m[2]) },
  // transition: opacity 300ms / transitionDuration: '0.3s'
  { pattern: /transition(?:Duration)?\s*[:=]\s*['"`]?[^;'"`]*?(\d+(?:\.\d+)?)(ms|s)\b/g, read: (m) => milliseconds(m[1], m[2]) },
];

const offenders = ROOTS.flatMap(sourceFiles).flatMap((file) => {
  const source = readFileSync(file, 'utf8');
  return PATTERNS.flatMap(({ pattern, read }) =>
    [...source.matchAll(pattern)]
      .filter((match) => read(match) > LIMIT_MS)
      .map((match) => `${file}: ${match[0].trim()}`));
});

describe('interaction motion duration', () => {
  it(`keeps every transition and delay at or below ${LIMIT_MS}ms`, () => {
    expect(offenders).toEqual([]);
  });
});
