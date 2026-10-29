import { readFileSync } from 'node:fs';
import React from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it, vi } from 'vitest';

import { SkillPicker, skillPickerRows, focusWasLost } from '@/components/studio/skill-picker';
import { PICKER_UPPERCASE_CLASS } from '@/components/studio/table-popover';
import type { ProductSchema } from '@/lib/product-schema';

const schema = {
  content: { messages: {
    'ui.studio.skill.selected': 'SKILL：{name}',
    'ui.studio.skill.short': 'Skills',
    'ui.studio.skill.label': 'Skill',
    'ui.studio.skill.title': '我的 Skill',
    'ui.studio.skill.hint': '',
    'ui.studio.skill.col.name': '名称',
    'ui.studio.skill.col.prompt': '提示词',
    'ui.studio.skill.col.size': '尺寸',
    'ui.studio.skill.none': '不使用 Skill',
    'ui.studio.skill.none_hint': '使用当前模型的默认行为',
    'ui.workspace.skills.loading': '加载中',
    'ui.workspace.skills.load_failed': '加载失败',
    'ui.workspace.skills.empty': '没有 Skill',
    'ui.workspace.skills.unavailable': '当前不可用',
    'ui.workspace.skills.create': '新建 Skill',
  } },
} as unknown as ProductSchema;

vi.mock('@/components/product-schema-provider', () => ({
  useProductSchema: () => ({ schema }),
}));

vi.mock('@/components/workspace/skill-creator', () => ({ SkillCreator: () => null }));

const catalog = {
  skills: [{ id: 'three-view', displayName: '三视图', prompt: 'Generate three views', recommendedSize: '1024' }],
} as never;
const effective = { skills: [{ id: 'three-view' }] } as never;

function render(overrides: Partial<React.ComponentProps<typeof SkillPicker>> = {}) {
  return renderToStaticMarkup(<SkillPicker
    sessionId="session-1"
    catalog={catalog}
    effective={effective}
    selectedId="three-view"
    unavailable={false}
    loading={false}
    failed={false}
    disabled={false}
    onSelect={() => undefined}
    onCreated={() => undefined}
    {...overrides}
  />);
}

describe('SkillPicker selected trigger label', () => {
  it('shows the resolved selected skill using the Schema template and keeps its full name accessible', () => {
    const html = render();
    expect(html).toContain('SKILL：三视图');
    expect(html).toContain('aria-label="我的 Skill: 三视图"');
  });

  it.each([
    ['loading', { loading: true }],
    ['failed', { failed: true }],
    ['unavailable', { unavailable: true }],
    ['missing from catalog', { catalog: { skills: [] } as never }],
  ])('uses a neutral label when selection is %s', (_state, overrides) => {
    const html = render(overrides);
    expect(html).toContain('>Skills</span>');
    expect(html.match(/<button[^>]*data-testid="workspace-skill-picker"[^>]*>/)?.[0]).toContain(PICKER_UPPERCASE_CLASS);
    expect(html).not.toContain('SKILL：三视图');
    expect(html).not.toContain('aria-label="我的 Skill: three-view"');
  });
});

// #238: only the owner's personal Skills carry a delete action.
describe('skillPickerRows', () => {
  it('offers delete on personal Skills only, never on system Skills or "none"', () => {
    const deleted: string[] = [];
    const rows = skillPickerRows({
      catalog: { revision: 'r', skills: [
        { id: 'system-three-view', displayName: '三视图', prompt: 'p', recommendedSize: '' },
        { id: 'personal-a', displayName: '我的水彩', prompt: 'soft', recommendedSize: '1536x1024' },
      ] } as never,
      effective: { skills: [{ id: 'system-three-view' }, { id: 'personal-a' }] } as never,
      personalIds: new Set(['personal-a']),
      noneLabel: '不使用',
      noneHint: '默认',
      deleteLabel: (name) => '删除 Skill「' + name + '」',
      deleting: new Set(),
      onDelete: (id) => deleted.push(id),
    });
    expect(rows.map((row) => row.key)).toEqual(['system-three-view', 'personal-a', '']);
    expect(rows[0].action).toBeUndefined();
    expect(rows[2].action).toBeUndefined();
    expect(rows[1].action?.label).toBe('删除 Skill「我的水彩」');
    rows[1].action?.onClick();
    expect(deleted).toEqual(['personal-a']);
  });
});

// #238: after a delete the keyboard returns to the list once the row has gone, not to the page body.
describe('focus after deleting a personal Skill', () => {
  it('counts focus as lost only on nothing, the body, the root or the panel container', () => {
    const panel = { tag: 'panel' } as unknown as Element;
    const row = { tag: 'row' } as unknown as Element;
    const doc = { body: { tag: 'body' }, documentElement: { tag: 'html' } };
    const previous = (globalThis as { document?: unknown }).document;
    (globalThis as { document?: unknown }).document = doc;
    try {
      expect(focusWasLost(null, panel)).toBe(true);
      expect(focusWasLost(doc.body as unknown as Element, panel)).toBe(true);
      expect(focusWasLost(doc.documentElement as unknown as Element, panel)).toBe(true);
      expect(focusWasLost(panel, panel)).toBe(true);
      expect(focusWasLost(row, panel)).toBe(false);
    } finally {
      (globalThis as { document?: unknown }).document = previous;
    }
  });

  it('hides the deleted row at once and refocuses from an effect after that render, not by polling frames', () => {
    const source = readFileSync('src/components/studio/skill-picker.tsx', 'utf8');
    expect(source).toContain('skills: catalog.skills.filter((skill) => !removed.has(skill.id))');
    expect(source).toContain('}, [removed]);');
    expect(source).toContain("panel.querySelector<HTMLElement>('[role=\"menuitemradio\"]:not([disabled])')?.focus()");
    expect(source).not.toContain('requestAnimationFrame');
  });
});
