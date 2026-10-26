import { describe, expect, it } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import type { CapabilitySchema, ProductSchema } from '@/lib/product-schema';
import {
  actionView, hintContentOf, isStageActionId, labelBreakpoint, processStepView, shortcutCaps, stageActionViews, stageKindOf,
  STAGE_ACTION_GROUPS, STAGE_ACTION_IDS, STAGE_ACTION_TEST_IDS, withTarget,
} from './stage-actions';

const schema = defaultCatalog.application as unknown as ProductSchema;
const messages = schema.content.messages;
const windows = { mac: false };
const idsOf = (kind: Parameters<typeof stageActionViews>[1], group?: Parameters<typeof stageActionViews>[2]) => stageActionViews(schema, kind, group).map((view) => view.id);

describe('stage action ids and the Schema table', () => {
  it('has a handler id for every action the default catalog lists, and none the catalog lacks', () => {
    const declared = schema.canvas.frontend.stage_actions.map((action) => action.id);
    expect([...declared].sort()).toEqual([...STAGE_ACTION_IDS].sort());
    for (const id of declared) expect(isStageActionId(id), id).toBe(true);
    expect(isStageActionId('focus')).toBe(false);
    expect(new Set(Object.keys(STAGE_ACTION_TEST_IDS))).toEqual(new Set(STAGE_ACTION_IDS));
  });

  it('keeps the test ids the acceptance scripts and wiring tests rely on', () => {
    expect(STAGE_ACTION_TEST_IDS).toMatchObject({
      to_3d: 'stage-to-3d', merge: 'stage-merge', continue_from: 'stage-continue', compare: 'stage-compare',
      enlarge: 'stage-enlarge', export_board: 'stage-export-board', send_chat: 'stage-send-chat',
    });
  });

  it('words every action from Schema messages: name, hint, target and the cost line of its hint', () => {
    for (const action of schema.canvas.frontend.stage_actions) {
      for (const key of [action.label_key, action.hint_key, action.target_key]) expect(messages[key], `${action.id}: ${key}`).toBeTruthy();
      expect(messages[`ui.studio.hint.cost.${action.cost_hint}`], action.id).toBeTruthy();
      expect(action.calls_model, action.id).toBe(action.cost_hint !== 'free');
    }
  });
});

describe('which buttons a stage object gets', () => {
  it('gives a picture its produce, output and view groups in the Schema order', () => {
    expect(idsOf('image', 'produce')).toEqual(['merge', 'continue_from', 'inpaint', 'remove_background', 'to_3d', 'retexture_with_image']);
    expect(idsOf('image', 'output')).toEqual(['send_chat', 'export_board']);
    expect(idsOf('image', 'view')).toEqual(['compare', 'enlarge']);
    expect(idsOf('image')).toHaveLength(10);
  });

  it('gives a 3D model the capture, compare and continue buttons only (processing steps come from capabilities)', () => {
    expect(idsOf('model', 'produce')).toEqual(['continue_from', 'capture_2d']);
    expect(idsOf('model', 'output')).toEqual([]);
    expect(idsOf('model', 'view')).toEqual(['compare']);
  });

  it('gives a board without a version merge, 去背景, send and export, and nothing else', () => {
    expect(idsOf('board')).toEqual(['merge', 'remove_background', 'send_chat', 'export_board']);
    expect(idsOf('board', 'produce')).toEqual(['merge', 'remove_background']);
  });

  it('labels only the produce actions that exist for a kind under the group the Schema says', () => {
    for (const group of STAGE_ACTION_GROUPS) {
      for (const view of stageActionViews(schema, 'image', group)) expect(view.group).toBe(group);
    }
    expect(stageKindOf(null)).toBe('board');
    expect(stageKindOf({ kind: 'image' })).toBe('image');
    expect(stageKindOf({ kind: 'model' })).toBe('model');
  });

  it('draws no button for an action id the code has no handler for', () => {
    const extended = structuredClone(schema);
    extended.canvas.frontend.stage_actions.push({ ...extended.canvas.frontend.stage_actions[0], id: 'mystery', kinds: ['image', 'model', 'board'] });
    expect(stageActionViews(extended, 'image').map((view) => view.id)).not.toContain('mystery');
    expect(stageActionViews(extended, 'image')).toHaveLength(10);
  });

  it('follows the Schema order, not the code order', () => {
    const reordered = structuredClone(schema);
    reordered.canvas.frontend.stage_actions.reverse();
    expect(stageActionViews(reordered, 'image', 'view').map((view) => view.id)).toEqual(['enlarge', 'compare']);
  });
});

describe('what a button says', () => {
  const view = (id: string) => stageActionViews(schema, 'image').find((item) => item.id === id)!;

  it('puts the ✦ rule on exactly the actions that call a model', () => {
    const calling = stageActionViews(schema, 'image').filter((item) => item.callsModel).map((item) => item.id);
    expect(calling).toEqual(['inpaint', 'remove_background', 'to_3d', 'retexture_with_image']);
    for (const id of ['merge', 'send_chat', 'export_board', 'compare', 'enlarge']) expect(view(id).callsModel, id).toBe(false);
    expect(stageActionViews(schema, 'model').find((item) => item.id === 'capture_2d')?.callsModel).toBe(false);
  });

  it('words the cost line by the cost hint and the target as 作用于', () => {
    expect(view('inpaint').costText).toBe(messages['ui.studio.hint.cost.approval_card']);
    expect(view('enlarge').costText).toBe('免费，不调用模型');
    expect(view('to_3d').target).toBe(`作用于：${messages['ui.studio.stage.action.to_3d_target']}`);
    expect(view('remove_background').target).toContain('选中的图层');
  });

  it('names the large view 大图 and gives it the Space key', () => {
    const enlarge = view('enlarge');
    expect(enlarge.label).toBe('大图');
    expect(enlarge.shortcut).toBe('space_code');
    expect(hintContentOf(schema, enlarge, { platform: windows }).shortcut).toEqual(['空格']);
    expect(hintContentOf(schema, view('inpaint'), { platform: windows }).shortcut).toBeUndefined();
  });

  it('builds the hover content with the reason first when one is given', () => {
    const content = hintContentOf(schema, view('inpaint'), { reason: '先选一张图', platform: windows });
    expect(content).toMatchObject({ name: '局部重绘', reason: '先选一张图', hint: view('inpaint').hint, cost: { ai: true, text: messages['ui.studio.hint.cost.approval_card'] } });
    expect('reason' in hintContentOf(schema, view('inpaint'), { platform: windows })).toBe(false);
  });

  it('lets one state retarget a button without touching the Schema text', () => {
    const retargeted = withTarget(schema, view('to_3d'), '只用正面图');
    expect(retargeted.target).toBe('作用于：只用正面图');
    expect(retargeted.id).toBe('to_3d');
    expect(view('to_3d').target).not.toContain('只用正面图');
  });

  it('turns a Schema action into the same view the list gives', () => {
    const entry = schema.canvas.frontend.stage_actions.find((item) => item.id === 'compare')!;
    expect(actionView(schema, { ...entry, id: 'compare' })).toEqual(view('compare'));
  });
});

describe('3D processing steps', () => {
  const capability = (id: string) => schema.capabilities.find((item) => item.id === id) as CapabilitySchema;

  it('takes name and icon from the capability and the cost line from the process panel', () => {
    const step = processStepView(schema, capability('remesh'));
    expect(step).toMatchObject({ id: 'remesh', testId: 'stage-process-remesh', icon: 'network', label: '重拓扑', callsModel: false, group: 'produce' });
    expect(step.costText).toBe(messages['ui.studio.hint.cost.process_panel']);
    expect(step.hint).toBe(messages['ui.studio.stage.action.process_hint.remesh']);
    expect(step.target).toBe('作用于：当前 3D 模型');
  });

  it('keeps a mixed free and paid conversion entry neutral until a specific offering is chosen', () => {
    const conversions = defaultCatalog.offerings.filter((offering) => offering.enabled && offering.capability === 'format-conversion');
    expect(conversions.some((offering) => offering.estimated_credits === 0 && !offering.credits_metered)).toBe(true);
    expect(conversions.some((offering) => typeof offering.estimated_credits === 'number' && offering.estimated_credits > 0 && offering.credits_metered)).toBe(true);

    const step = processStepView(schema, capability('format-conversion'));
    const content = hintContentOf(schema, step);
    expect(content.cost?.text).toContain('先打开右侧加工设置');
    expect(content.cost?.text).toContain('以所选方案为准');
    expect(content.cost?.text).not.toMatch(/会调用模型|确认后才花积分|免费/);
    // A navigation entry does not label mixed execution choices as model calls.
    expect(content.cost?.ai).toBe(false);
  });

  it('reads revised process navigation copy from Schema for every processing capability', () => {
    const revised = structuredClone(schema);
    revised.content.messages['ui.studio.hint.cost.process_panel'] = '选择方案后查看执行方式与费用';
    for (const id of revised.runtime.frontend.model_processing_capabilities) {
      const step = processStepView(revised, capability(id));
      expect(hintContentOf(revised, step).cost?.text, id).toBe('选择方案后查看执行方式与费用');
    }
  });

  it('reads processing marker, cost hint and group dynamically from the public Schema', () => {
    for (const presentation of [
      { calls_model: false, cost_hint: 'process_panel', group: 'produce' },
      { calls_model: true, cost_hint: 'approval_card', group: 'output' },
      { calls_model: false, cost_hint: 'free', group: 'view' },
    ] as const) {
      const revised = structuredClone(schema);
      Object.assign(revised.canvas.frontend, { processing_action: presentation });
      for (const id of revised.runtime.frontend.model_processing_capabilities) {
        const step = processStepView(revised, capability(id));
        expect(step.callsModel, id).toBe(presentation.calls_model);
        expect(step.group, id).toBe(presentation.group);
        expect(step.costText, id).toBe(revised.content.messages[`ui.studio.hint.cost.${presentation.cost_hint}`]);
      }
    }
  });

  it('has a hint text and an icon for every processing capability the toolbar can offer', () => {
    for (const id of schema.runtime.frontend.model_processing_capabilities) {
      const step = processStepView(schema, capability(id));
      expect(step.hint, id).toBeTruthy();
      expect(step.icon, id).toBeTruthy();
      expect(step.label, id).toBeTruthy();
    }
  });

  it('no longer says where to confirm in the hint (the cost line does)', () => {
    for (const id of schema.runtime.frontend.model_processing_capabilities) {
      expect(messages[`ui.studio.stage.action.process_hint.${id}`], id).not.toContain('模型加工');
    }
  });
});

describe('shortcut caps', () => {
  it('prints a key setting as key caps and ignores settings that are not a single key', () => {
    expect(shortcutCaps(schema, 'space_code', windows)).toEqual(['空格']);
    expect(shortcutCaps(schema, 'lock_shortcut', windows)).toEqual(['Ctrl', '/']);
    expect(shortcutCaps(schema, 'brush_tool_key', windows)).toEqual(['B']);
    expect(shortcutCaps(schema, undefined, windows)).toBeUndefined();
    expect(shortcutCaps(schema, 'no_such_setting', windows)).toBeUndefined();
    expect(shortcutCaps(schema, 'delete_keys', windows)).toBeUndefined();
    expect(shortcutCaps(schema, 'nudge_pixels', windows)).toBeUndefined();
  });
});

describe('label breakpoints', () => {
  it('shows short labels first and long ones only on wider stages', () => {
    expect(labelBreakpoint('局部重绘')).toBe('5xl');
    expect(labelBreakpoint('去背景')).toBe('5xl');
    expect(labelBreakpoint('从这里继续')).toBe('5xl');
    expect(labelBreakpoint('截图到 2D')).toBe('6xl');
    expect(labelBreakpoint('用这张图换贴图')).toBe('6xl');
    expect(labelBreakpoint(messages['ui.studio.merge.action'])).toBe('7xl');
  });
});
