import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';
import defaultCatalog from '../../../internal/catalog/default_catalog.json';

const board = readFileSync(new URL('./workspace-canvas-board.tsx', import.meta.url), 'utf8');
const zh = defaultCatalog.application.content.messages as Record<string, string>;
const en = defaultCatalog.application.localization.bundles.en.messages as Record<string, string>;

// #670: the canvas board's loading and failure notices are the canvas's own copy, not the
// parameter panel's ("参数加载中…", "保存参数失败") or the history list's.
describe('canvas board status copy', () => {
  it('reads the canvas keys for loading, load failure and save failure', () => {
    const copy = board.slice(board.indexOf('const ui = useMemo(() => ({'), board.indexOf('}), [schema]);', board.indexOf('const ui = useMemo(() => ({')));
    expect(copy).toContain("loading: schemaText(schema, 'ui.workspace.canvas.loading')");
    expect(copy).toContain("loadFailed: schemaText(schema, 'ui.workspace.canvas.load_failed')");
    expect(copy).toContain("saveFailed: schemaText(schema, 'ui.workspace.canvas.save_failed')");
    expect(copy).not.toContain('ui.workspace.harness.');
    expect(copy).not.toContain('ui.workspace.history.');
  });

  it('declares the canvas copy in both bundles and names the canvas', () => {
    for (const key of ['ui.workspace.canvas.loading', 'ui.workspace.canvas.load_failed', 'ui.workspace.canvas.save_failed']) {
      expect(zh[key]).toContain('画布');
      expect(en[key]?.toLowerCase()).toContain('canvas');
    }
  });
});
