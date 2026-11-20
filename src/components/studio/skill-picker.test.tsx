import { readFileSync } from 'node:fs';
import React from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it, vi } from 'vitest';

import { SkillPicker, skillPickerRows, focusWasLost, skillUnavailableReason } from '@/components/studio/skill-picker';
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
    'ui.unavailable.reason.picker.skill_not_enabled': '未启用',
    'ui.unavailable.reason.picker.no_session': '没有会话',
    'ui.unavailable.reason.picker.turn_busy': '本轮进行中',
    'ui.unavailable.label': '无法使用，{reason}',
    'ui.workspace.skills.execution_unavailable': '模型和配置不支持，请检查：{models}',
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

// #405: the picker is the Skills tab of the settings dialog now, so there is no trigger label to test: the same states
// are told by the table itself (the checked row, the loading and failure lines, the notice under the table).
describe('SkillPicker as the Skills tab', () => {
  // The inner markup of each checked row, as rendered: the assertions only look for the name inside it.
  const checked = (html: string) => [...html.matchAll(/<button[^>]*role="menuitemradio"[^>]*aria-checked="true"[^>]*>(.*?)<\/button>/g)].map((match) => match[1]);

  it('checks the row of the selected skill, in the same table as "none", with no trigger button', () => {
    const html = render();
    expect(html).toContain('data-testid="workspace-skill-picker-panel"');
    expect(checked(html)[0]).toContain('三视图');
    expect(html).not.toContain('data-testid="workspace-skill-picker"');
    expect(html).not.toContain('SKILL：三视图');
    // Uppercase names stay a table treatment (#108): the name cell carries the shared class.
    expect(html).toContain(PICKER_UPPERCASE_CLASS);
  });

  it.each([
    ['loading', { loading: true }, 'role="status"', '加载中'],
    ['failed', { failed: true }, 'role="alert"', '加载失败'],
  ])('says so in the table while the Skills are %s', (_state, overrides, role, text) => {
    const html = render(overrides);
    expect(html).toContain(role);
    expect(html).toContain(text);
    expect(html).not.toContain('role="menuitemradio"');
  });

  it('keeps the selected row checked and says under the table that it cannot be used', () => {
    const html = render({ unavailable: true });
    expect(checked(html)[0]).toContain('三视图');
    expect(html).toContain('data-testid="skill-picker-unavailable"');
  });

  it('checks no row when the selected skill is missing from the catalog', () => {
    const html = render({ catalog: { skills: [] } as never });
    expect(checked(html)).toEqual([]);
  });

  it('turns the table off with the short reason when there is no session, or a turn is running (#94)', () => {
    const noSession = render({ sessionId: null });
    expect(noSession).toContain('disabled=""');
    expect(noSession).toContain('data-testid="workspace-skill-picker-section-unavailable"');
    const busy = render({ disabled: true });
    expect(busy).toContain('disabled=""');
    expect(busy).toContain('data-testid="workspace-skill-picker-section-unavailable"');
    expect(render({})).not.toContain('disabled=""');
  });
});

// #238: only the owner's personal Skills carry a delete action.
describe('skillPickerRows', () => {
  it('uses the Schema execution reason only when supporting models are known, and preserves other reasons', () => {
    const plain = { id: 'personal', prompt: '' } as never;
    const tool = { id: 'tool', tool: 'some-tool', prompt: '' } as never;
    expect(skillUnavailableReason(schema, plain, ['Alpha', 'Beta'])).toBe('模型和配置不支持，请检查：Alpha, Beta');
    expect(skillUnavailableReason(schema, plain, [])).toBe('当前不可用');
    expect(skillUnavailableReason(schema, tool, ['Alpha'])).toBe('未启用');
  });

  it('exposes per-row reasons for keyboard access', () => {
    const rows = skillPickerRows({
      catalog: { skills: [{ id: 'personal', displayName: 'P', prompt: '', recommendedSize: '' }] } as never,
      effective: { skills: [] } as never,
      personalIds: new Set(), noneLabel: 'n', noneHint: 'h', editLabel: () => '', deleteLabel: () => '', deleting: new Set(), onEdit: () => undefined, onDelete: () => undefined,
      reasonForSkill: () => 'schema reason',
    });
    expect(rows[0]).toMatchObject({ disabled: true, disabledReason: 'schema reason', focusableDisabled: true });
  });

  it('gives a row the effective configuration leaves out a short reason (#94)', () => {
    const rows = skillPickerRows({
      catalog: { revision: 'r', skills: [{ id: 'a', displayName: 'A', prompt: 'p', recommendedSize: '' }] } as never,
      effective: { skills: [] } as never,
      personalIds: new Set(), noneLabel: 'n', noneHint: 'h', editLabel: () => '', deleteLabel: () => '', deleting: new Set(), onEdit: () => undefined, onDelete: () => undefined,
      notEnabledReason: 'not enabled',
    });
    expect(rows[0]).toMatchObject({ key: 'a', disabled: true, disabledReason: 'not enabled' });
    expect(rows[1].disabledReason).toBeUndefined();
  });

  it('offers edit and delete on personal Skills only, never on system Skills or "none"', () => {
    const deleted: string[] = [];
    const edited: string[] = [];
    const rows = skillPickerRows({
      catalog: { revision: 'r', skills: [
        { id: 'system-three-view', displayName: '三视图', prompt: 'p', recommendedSize: '' },
        { id: 'personal-a', displayName: '我的水彩', prompt: 'soft', recommendedSize: '1536x1024' },
      ] } as never,
      effective: { skills: [{ id: 'system-three-view' }, { id: 'personal-a' }] } as never,
      personalIds: new Set(['personal-a']),
      noneLabel: '不使用',
      noneHint: '默认',
      editLabel: (name) => '编辑 Skill「' + name + '」',
      deleteLabel: (name) => '删除 Skill「' + name + '」',
      deleting: new Set(),
      onEdit: (id) => edited.push(id),
      onDelete: (id) => deleted.push(id),
    });
    expect(rows.map((row) => row.key)).toEqual(['system-three-view', 'personal-a', '']);
    expect(rows[0].actions).toBeUndefined();
    expect(rows[2].actions).toBeUndefined();
    expect(rows[1].actions?.map((action) => action.kind)).toEqual(['edit', 'delete']);
    // #405: nothing closes before the editor opens (the editor is a dialog above the settings dialog), so no close flag.
    expect(rows[1].actions?.[0]).toMatchObject({ label: '编辑 Skill「我的水彩」' });
    expect(rows[1].actions?.[0]).not.toHaveProperty('closeOnClick');
    expect(rows[1].actions?.[1]?.label).toBe('删除 Skill「我的水彩」');
    rows[1].actions?.[0].onClick();
    rows[1].actions?.[1].onClick();
    expect(edited).toEqual(['personal-a']);
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
    expect(source).toContain("panel.querySelector<HTMLElement>('[role=\"menuitemradio\"]:not([disabled]):not([aria-disabled=\"true\"])')?.focus()");
    expect(source).not.toContain('requestAnimationFrame');
  });
});
