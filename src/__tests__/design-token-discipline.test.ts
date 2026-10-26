import { readdirSync, readFileSync, statSync } from 'node:fs';
import { join } from 'node:path';

import { describe, expect, it } from 'vitest';

/**
 * 硬编码调色板守门。`src/components/ui/**` 是 shadcn 原样 vendored 的基础组件，
 * 跟随 shadcn 约定，不纳入本产品的令牌收敛范围；其余自有表面必须用设计令牌。
 */
const OWNED_ROOTS = ['src/app', 'src/components', 'src/hooks', 'src/lib', 'src/store'];
const PARENTS_ROOT = 'src/components/ui/';
const PALETTE_FAMILIES = [
  'indigo', 'zinc', 'red', 'blue', 'green', 'amber', 'gray', 'grey', 'slate',
  'neutral', 'stone', 'sky', 'violet', 'purple', 'pink', 'rose', 'yellow',
  'orange', 'cyan', 'teal',
];
const UTILITY =
  /(?:^|["'\s])(?:bg|text|border|ring|from|to|via|fill|stroke|outline|divide|placeholder|shadow|accent|caret|decoration)-(indigo|zinc|red|blue|green|amber|gray|grey|slate|neutral|stone|sky|violet|purple|pink|rose|yellow|orange|cyan|teal)-?[0-9]*/g;

function sourceFiles(dir: string): string[] {
  const out: string[] = [];
  for (const entry of readdirSync(dir)) {
    const full = join(dir, entry);
    if (statSync(full).isDirectory()) {
      out.push(...sourceFiles(full));
    } else if (/\.tsx?$/.test(entry)) {
      out.push(full);
    }
  }
  return out;
}

const offenders = OWNED_ROOTS.flatMap(sourceFiles)
  .flatMap((file) => {
    const source = readFileSync(file, 'utf8');
    return [...source.matchAll(UTILITY)].map((match) => ({
      file,
      token: match[0].trim(),
    }));
  });

/**
 * UIKIT §3.1 圆角闭集（docs/ui-kit-framework.md）：
 * sm / md / lg / xl / full（2xl 与 xl 同值仅作兼容别名，禁止再新增）。
 * 3xl、4xl 与任意像素值（rounded-[4px]）已移除——它们正是"全局圆角过大"
 * 与视觉不统一的来源。rounded-t/b/l/r 系单侧圆角属于方向修饰，归入所配
 * 档位，不在本表单独管控。使用 radius 变量的任意值形式属于令牌派生，
 * 允许（值仍由令牌决定），纯像素任意值不允许。
 */
const RADIUS_SCALE = new Set([
  'sm', 'md', 'lg', 'xl', '2xl', 'full',
]);
const RADIUS_UTILITY = /(?:^|["'\s])rounded-([a-z0-9[\]()..,-]*[a-z0-9\])])/g;

const radiusOffenders = OWNED_ROOTS.flatMap(sourceFiles)
  .flatMap((file) => {
    const source = readFileSync(file, 'utf8');
    return [...source.matchAll(RADIUS_UTILITY)]
      .map((match) => ({ file, token: match[0].trim(), scale: match[1] }))
      .filter(({ scale }) => {
        if (RADIUS_SCALE.has(scale)) return false;
        // 方向修饰：rounded-t / rounded-bl 等，后缀是方向而非档位。
        if (/^(t|b|l|r|tl|tr|bl|br|t-l|t-r|b-l|b-r|s|e|ss|ee)(-(sm|md|lg|xl|2xl|full))?$/.test(scale)) {
          return false;
        }
        // 令牌派生：rounded-[min(var(--radius-md),10px)] 之类，值仍由令牌决定。
        if (scale.startsWith('[') && scale.includes('var(--radius')) return false;
        return true;
      });
  });

describe('design token discipline', () => {
  it('keeps UI primitives independent from product state and schema', () => {
    for (const file of sourceFiles(PARENTS_ROOT)) {
      const source = readFileSync(file, 'utf8');
      expect(source, file).not.toMatch(/from ['"]@\/(?:components\/product-schema-provider|hooks\/|store\/|lib\/(?!utils))/);
    }
  });

  it('keeps product surfaces on the design tokens', () => {
    expect(offenders.map((item) => `${item.file}: ${item.token}`)).toEqual([]);
  });

  it('keeps border radius on the closed scale (UIKIT §3.1)', () => {
    expect(
      radiusOffenders.map((item) => `${item.file}: ${item.token}`),
    ).toEqual([]);
  });

  it('keeps anchored popovers from locking to the anchor width (UIKIT §3.4/G4)', () => {
    const locked = OWNED_ROOTS.flatMap(sourceFiles).flatMap((file) => {
      const source = readFileSync(file, 'utf8');
      return source
        .split('\n')
        .map((line, index) => ({ file, line, index: index + 1 }))
        .filter(({ line }) =>
          /(?<![-\w])w-\(--anchor-width\)/.test(line),
        )
        .map(({ file, index }) => `${file}:${index}`);
    });
    expect(locked).toEqual([]);
  });

  it('uses named layers for portals, including controls opened inside dialogs', () => {
    const theme = readFileSync('src/app/globals.css', 'utf8');
    for (const [name, value] of Object.entries({
      base: 0,
      sticky: 10,
      float: 20,
      scrim: 30,
      modal: 40,
      'modal-float': 45,
      blocking: 50,
      'blocking-float': 55,
      toast: 60,
    })) {
      expect(theme).toContain(`--z-index-${name}: ${value};`);
    }
    const overlay = readFileSync('src/components/ui/overlay.ts', 'utf8');
    expect(overlay).toContain("modal: 'z-modal-float'");
    expect(overlay).toContain("blocking: 'z-blocking-float'");
    const dialog = readFileSync('src/components/ui/dialog.tsx', 'utf8');
    expect(dialog).toContain('OverlayLayerContext.Provider');
    // The workspace has no scrim and no blocking dialog: the task centre is a
    // non-modal drawer on the modal layer, and the chat column sits in the layout.
    const drawer = readFileSync('src/components/ui/side-drawer.tsx', 'utf8');
    expect(drawer).toContain('z-modal');
    expect(drawer).not.toContain('z-scrim');
    const workspace = readFileSync('src/app/workspace/page.tsx', 'utf8');
    expect(workspace).not.toContain('OverlayLayerContext');
    expect(workspace).not.toContain('z-scrim');
    expect(() => readFileSync('src/components/workspace/consent-dialog.tsx', 'utf8')).toThrow();
    for (const file of [
      'src/components/ui/overlay.ts',
      'src/components/ui/dialog.tsx',
      'src/components/ui/tooltip.tsx',
    ]) {
      expect(readFileSync(file, 'utf8'), file).not.toMatch(/\bz-50\b/);
    }
  });

  it('bans fixed side panels outside the enumerated survivors (UIKIT §5/G3)', () => {
    const survivors = new Set([
      'src/components/layout/sidebar.tsx',
    ]);
    const offenders = OWNED_ROOTS.flatMap(sourceFiles)
      .filter((file) => !file.startsWith(PARENTS_ROOT) && !survivors.has(file))
      .flatMap((file) =>
        readFileSync(file, 'utf8')
          .split('\n')
          .map((line, index) => ({ file, index: index + 1, line }))
          .filter(({ line }) => /\bfixed\b/.test(line) && /\binset-y-0\b/.test(line))
          .map(({ file, index }) => `${file}:${index}`),
      );
    expect(offenders).toEqual([]);
  });

  it('bans self-built overlay backdrops outside the enumerated survivors (UIKIT §5/G2)', () => {
    const survivors = new Set([
      'src/components/layout/sidebar.tsx',
    ]);
    const offenders = OWNED_ROOTS.flatMap(sourceFiles)
      .filter((file) => !file.startsWith(PARENTS_ROOT) && !survivors.has(file))
      .flatMap((file) =>
        readFileSync(file, 'utf8')
          .split('\n')
          .map((line, index) => ({ file, index: index + 1, line }))
          .filter(({ line }) => /\bfixed\b/.test(line) && /\binset-0\b/.test(line))
          .map(({ file, index }) => `${file}:${index}`),
      );
    expect(offenders).toEqual([]);
  });

  it('drops the retired oversized radius steps from the theme', () => {
    const theme = readFileSync('src/app/globals.css', 'utf8');
    for (const retired of ['--radius-3xl', '--radius-4xl']) {
      expect(
        theme.includes(retired),
        `已废弃的 ${retired} 不应再出现在主题里`,
      ).toBe(false);
    }
    // 基线 8px：--radius-lg 即 0.5rem，xl 封顶 0.75rem。
    expect(theme).toContain('--radius-lg: 0.5rem;');
    expect(theme).toContain('--radius-xl: 0.75rem;');
    expect(theme).not.toContain('--radius: 0.75rem;');
  });

  it('keeps the canvas renderer free of literal hex palettes', () => {
    const canvas = readFileSync('src/components/canvas/canvas-board.tsx', 'utf8');
    const hex = [...canvas.matchAll(/#[0-9a-fA-F]{6}\b/g)].map((match) => match[0]);
    expect(hex).toEqual([]);
  });

  it('resolves canvas colours from the design tokens', () => {
    const palette = readFileSync('src/lib/canvas/canvas-palette.ts', 'utf8');
    for (const token of [
      '--primary',
      '--info',
      '--muted-foreground',
      // 金底主按钮的前景是深色，画布笔色必须取正文色 --foreground。
      '--foreground',
      '--canvas',
    ]) {
      expect(palette, `canvas palette 未读取令牌 ${token}`).toContain(token);
    }
    expect(palette).toContain('getComputedStyle(document.documentElement)');
    // useSyncExternalStore 快照必须是稳定值，否则会无限重渲染。
    expect(palette).toContain('readCanvasTokenSnapshot');
    expect(palette).not.toContain('useSyncExternalStore(\n    subscribe,\n    () => readCanvasPalette');
  });
});
