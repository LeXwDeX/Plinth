/**
 * react-konva / canvas 2D 无法直接消费 CSS 类，只能给字面色值。设计令牌是
 * oklch，因此这里在运行时从 :root 读取令牌，并把每个语义色映射到对应令牌：
 * 画布不再自带一套 #hex 调色板，深色主题改一次即全画布生效。
 */

import { useMemo, useSyncExternalStore } from 'react';

export interface CanvasPalette {
  /** 选区、锚点、变换框描边（品牌强调色） */
  primary: string;
  /** 选区半透明填充 */
  selectionFill: string;
  /** 变换手柄 */
  handle: string;
  /** 橡皮与擦除描边 */
  erase: string;
  /** 画笔默认笔色（前景色） */
  ink: string;
  /** 锚点内填 / 画布底色 */
  canvasBackground: string;
  /** 橡皮在 2D context 里擦除用的笔色（destination-out 只看 alpha） */
  eraserComposite: string;
}

/** 令牌缺省值：与 src/app/globals.css 的 :root 保持一致的产品深色色板。 */
export const CANVAS_PALETTE_FALLBACK: CanvasPalette = {
  primary: 'oklch(0.827 0.141 92)',
  selectionFill: 'oklch(0.827 0.141 92 / 0.12)',
  handle: 'oklch(0.72 0.125 230)',
  erase: 'oklch(0.72 0.004 260)',
  ink: 'oklch(0.93 0.002 260)',
  canvasBackground: 'oklch(0.207 0.003 260)',
  eraserComposite: 'oklch(0 0 0 / 1)',
};

const TOKEN_BY_KEY: Record<keyof CanvasPalette, string> = {
  primary: '--primary',
  selectionFill: '--primary',
  // 蒙版笔迹用信息色，保证在任意图像上都可辨识；笔色取正文近白色，
  // 不能跟随主按钮的深色前景（金底黑字）。
  handle: '--info',
  erase: '--muted-foreground',
  ink: '--foreground',
  canvasBackground: '--canvas',
  eraserComposite: '--foreground',
};

/** 把 CSS 颜色函数与色值原样取出，空白令牌退回缺省值。 */
export function projectCanvasPalette(
  read: (token: string) => string,
): CanvasPalette {
  const palette = {} as CanvasPalette;
  for (const [key, token] of Object.entries(TOKEN_BY_KEY) as Array<
    [keyof CanvasPalette, string]
  >) {
    const value = read(token).trim();
    palette[key] = key === 'selectionFill' && value !== ''
      ? withAlpha(value, 0.12)
      : value === ''
        ? CANVAS_PALETTE_FALLBACK[key]
        : value;
  }
  return palette;
}

function withAlpha(color: string, alpha: number): string {
  // 令牌是 oklch(...)/rgb(...)/#hex 三种可能之一；能直接拼 alpha 就拼，
  // 拼不上就交回调用方拿到的原色（半透明选区退化为实色仍可见）。
  const match = /^(oklch|oklab|rgb)\(([^)]*)\)$/.exec(color);
  if (match?.[2] !== undefined) {
    const inner = match[2].trim();
    const head = inner.split('/')[0]?.trim();
    if (head !== undefined && head !== '') {
      return `${match[1]}(${head} / ${alpha})`;
    }
  }
  return color;
}

export function readCanvasPalette(
  root: Pick<CSSStyleDeclaration, 'getPropertyValue'>,
): CanvasPalette {
  return projectCanvasPalette((token) => root.getPropertyValue(token));
}

/** 令牌原始值序列：useSyncExternalStore 的快照必须是稳定值（字符串可比较）。 */
export const CANVAS_TOKEN_KEYS = Object.values(TOKEN_BY_KEY);

export function readCanvasTokenSnapshot(
  root: Pick<CSSStyleDeclaration, 'getPropertyValue'>,
): string {
  return CANVAS_TOKEN_KEYS.map((token) => root.getPropertyValue(token)).join('\u0001');
}

export function parseCanvasPalette(snapshot: string): CanvasPalette {
  const values = snapshot.split('\u0001');
  return projectCanvasPalette((token) => {
    const index = CANVAS_TOKEN_KEYS.indexOf(token);
    return index < 0 ? '' : (values[index] ?? '');
  });
}

const SERVER_TOKEN_SNAPSHOT = CANVAS_TOKEN_KEYS.map(
  (token) => CANVAS_PALETTE_FALLBACK[tokenKeyOf(token)] ?? '',
).join('\u0001');

function tokenKeyOf(token: string): keyof CanvasPalette {
  const entry = Object.entries(TOKEN_BY_KEY).find(
    ([, value]) => value === token,
  );
  if (!entry) throw new Error(`unregistered canvas token: ${token}`);
  return entry[0] as keyof CanvasPalette;
}

/**
 * 运行时读取 :root 上的设计令牌。SSR/无 DOM 时退回产品缺省色板，
 * 保证 konva 首帧不会因为读不到变量而画出黑色。
 */
export function useCanvasPalette(): CanvasPalette {
  // 令牌定义在 :root 样式表里（不是内联样式），必须走 getComputedStyle。
  // 快照必须是稳定值：直接返回调色板对象会让 getSnapshot 每次都是新引用，
  // useSyncExternalStore 判定为持续变化并无限重渲染（React error #185）。
  const snapshot = useSyncExternalStore(
    subscribe,
    () => readCanvasTokenSnapshot(getComputedStyle(document.documentElement)),
    () => SERVER_TOKEN_SNAPSHOT,
  );
  return useMemo(() => parseCanvasPalette(snapshot), [snapshot]);
}

function subscribe(onChange: () => void): () => void {
  if (typeof window === 'undefined') return () => {};
  const observer = new MutationObserver(onChange);
  observer.observe(document.documentElement, {
    attributes: true,
    attributeFilter: ['style', 'class'],
  });
  return () => observer.disconnect();
}

/** Read one design token named by the Product Schema (for example the snap guide colour). */
export function readCanvasToken(token: string, fallback: string): string {
  if (typeof document === 'undefined') return fallback;
  const value = getComputedStyle(document.documentElement).getPropertyValue(token).trim();
  return value === '' ? fallback : value;
}
