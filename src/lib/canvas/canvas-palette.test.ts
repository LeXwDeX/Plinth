import { describe, expect, it } from 'vitest';

import {
  CANVAS_PALETTE_FALLBACK,
  parseCanvasPalette,
  projectCanvasPalette,
  readCanvasPalette,
  readCanvasTokenSnapshot,
} from '@/lib/canvas/canvas-palette';

function reader(tokens: Record<string, string>) {
  return (token: string) => tokens[token] ?? '';
}

describe('canvas palette', () => {
  it('reads every semantic colour from the design tokens', () => {
    const palette = readCanvasPalette({
      getPropertyValue: reader({
        '--primary': 'oklch(0.827 0.141 92)',
        '--info': 'oklch(0.72 0.125 230)',
        '--muted-foreground': 'oklch(0.72 0.004 260)',
        '--canvas': 'oklch(0.207 0.003 260)',
        '--foreground': 'oklch(0.93 0.002 260)',
      }),
    });
    expect(palette.primary).toBe('oklch(0.827 0.141 92)');
    expect(palette.handle).toBe('oklch(0.72 0.125 230)');
    expect(palette.erase).toBe('oklch(0.72 0.004 260)');
    expect(palette.ink).toBe('oklch(0.93 0.002 260)');
    expect(palette.canvasBackground).toBe('oklch(0.207 0.003 260)');
  });

  it('derives the translucent selection fill from the token colour', () => {
    const palette = projectCanvasPalette(
      reader({ '--primary': 'oklch(0.827 0.141 92)' }),
    );
    expect(palette.selectionFill).toBe('oklch(0.827 0.141 92 / 0.12)');
  });

  it('keeps an already translucent token when deriving the selection fill', () => {
    const palette = projectCanvasPalette(
      reader({ '--primary': 'rgb(99 102 241 / 0.5)' }),
    );
    expect(palette.selectionFill).toBe('rgb(99 102 241 / 0.12)');
  });

  it('falls back to the product palette when a token is missing', () => {
    const palette = projectCanvasPalette(reader({}));
    expect(palette).toEqual(CANVAS_PALETTE_FALLBACK);
  });

  it('never returns an empty colour for any semantic key', () => {
    const palette = projectCanvasPalette(reader({ '--primary': '  ' }));
    for (const [key, value] of Object.entries(palette)) {
      expect(value.trim(), key).not.toBe('');
    }
  });
});

  it('produces a stable snapshot string for the same tokens', () => {
    // 回归防线：useSyncExternalStore 的快照若是新对象/新引用，React 会判定为
    // 持续变化并无限重渲染（真机表现为 React error #185、工作区整页崩溃）。
    const style = reader({
      '--primary': 'oklch(0.827 0.141 92)',
      '--info': 'oklch(0.72 0.125 230)',
      '--muted-foreground': 'oklch(0.72 0.004 260)',
      '--primary-foreground': 'oklch(0.985 0 0)',
      '--canvas': 'oklch(0.207 0.003 260)',
      '--foreground': 'oklch(0.93 0.002 260)',
    });
    const first = readCanvasTokenSnapshot({ getPropertyValue: style });
    const second = readCanvasTokenSnapshot({ getPropertyValue: style });
    expect(typeof first).toBe('string');
    expect(first).toBe(second);
    expect(Object.is(first, second)).toBe(true);
    expect(parseCanvasPalette(first).primary).toBe('oklch(0.827 0.141 92)');
  });

  it('keeps a stable object identity while the tokens do not change', () => {
    const style = reader({ '--primary': 'oklch(0.827 0.141 92)' });
    const snapshot = readCanvasTokenSnapshot({ getPropertyValue: style });
    const left = parseCanvasPalette(snapshot);
    const right = parseCanvasPalette(snapshot);
    expect(left).toEqual(right);
    // 同一快照解析出的调色板内容一致（React 只比较快照，不比较对象引用）。
    expect(JSON.stringify(left)).toBe(JSON.stringify(right));
  });
