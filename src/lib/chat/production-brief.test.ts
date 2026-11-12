import { describe, expect, it } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import type { ProductSchema } from '@/lib/product-schema';
import {
  briefIsEmpty,
  briefPayload,
  briefRoute,
  briefSelectText,
  briefValueText,
  parseProductionBrief,
  productionBriefFields,
} from '@/lib/chat/production-brief';

const schema = defaultCatalog.application as unknown as ProductSchema;
const fields = productionBriefFields(schema);

describe('the declared brief fields', () => {
  it('reads names, types, labels and limits from the Schema in its order', () => {
    expect(fields.map((field) => field.name)).toEqual([
      'purpose', 'image_use', 'image_transparent', 'image_for_3d', 'style', 'target_engine', 'target_platform', 'needs_parts', 'needs_rig', 'needs_animation', 'triangle_budget', 'notes',
    ]);
    const style = fields.find((field) => field.name === 'style')!;
    expect(style).toMatchObject({ type: 'text', label: '风格', maximumCharacters: 200 });
    const budget = fields.find((field) => field.name === 'triangle_budget')!;
    expect(budget).toMatchObject({ type: 'integer', minimum: 100, maximum: 2000000 });
    expect(fields.find((field) => field.name === 'purpose')?.type).toBe('profile');
    expect(fields.find((field) => field.name === 'image_use')?.type).toBe('image_profile');
    expect(fields.find((field) => field.name === 'image_transparent')?.type).toBe('boolean');
  });

  it('has no fields without a declared brief', () => {
    const bare = structuredClone(schema);
    delete bare.chat.model3d_usage!.brief;
    expect(productionBriefFields(bare)).toEqual([]);
  });

  it('reads the engine and platform selects with their options in the Schema order (#286)', () => {
    const engine = fields.find((field) => field.name === 'target_engine')!;
    expect(engine).toMatchObject({ type: 'select', label: '目标引擎' });
    expect(engine.options?.map((option) => option.id)).toEqual(['unity', 'unreal', 'godot', 'web', 'other']);
    expect(engine.options?.[3]).toEqual({ id: 'web', label: 'Web / three.js' });
    const platform = fields.find((field) => field.name === 'target_platform')!;
    expect(platform).toMatchObject({ type: 'select', label: '目标平台' });
    expect(platform.options?.map((option) => option.id)).toEqual(['mobile', 'pc_console', 'web', 'xr']);
    expect(platform.options?.[0].label).toBe('手机');
    // A select declares its choices; no other field type carries any.
    for (const field of fields.filter((candidate) => candidate.type !== 'select')) {
      expect(field.options).toBeUndefined();
    }
  });
});

describe('reading a stored brief', () => {
  it('keeps a plain object and turns anything else into the empty brief', () => {
    expect(parseProductionBrief({ style: 'low poly' })).toEqual({ style: 'low poly' });
    for (const value of [null, undefined, 'x', 4, ['style']]) expect(parseProductionBrief(value)).toEqual({});
  });

  it('is empty only when every member is absent or blank', () => {
    expect(briefIsEmpty({})).toBe(true);
    expect(briefIsEmpty({ style: '', notes: null, purpose: undefined })).toBe(true);
    expect(briefIsEmpty({ needs_rig: false })).toBe(false);
    expect(briefIsEmpty({ triangle_budget: 0 })).toBe(false);
    expect(briefIsEmpty({ style: 'low poly' })).toBe(false);
  });
});

describe('the whole object sent back', () => {
  it('keeps only declared members with a value, triming text', () => {
    const draft = {
      purpose: 'game_prop', style: '  low poly  ', needs_rig: false, needs_animation: true,
      triangle_budget: 12000, notes: '   ',
      undeclared: 'not sent',
    };
    expect(briefPayload(fields, draft)).toEqual({
      purpose: 'game_prop', style: 'low poly', needs_rig: false, needs_animation: true, triangle_budget: 12000,
    });
  });

  it('drops an empty value, a non-numeric budget and a text declared as another type', () => {
    expect(briefPayload(fields, { style: '', needs_rig: undefined, triangle_budget: 'x' })).toEqual({});
    expect(briefPayload(fields, { triangle_budget: '1500' })).toEqual({ triangle_budget: 1500 });
    expect(briefPayload(fields, { style: 42 })).toEqual({});
  });

  it('sends an empty object for a brief the person cleared', () => {
    expect(briefPayload(fields, {})).toEqual({});
  });

  it('sends a select as its trimmed id, a cleared select as nothing, and keeps a stored value that is no option (#286)', () => {
    expect(briefPayload(fields, { target_engine: 'unity', target_platform: ' xr ' })).toEqual({
      target_engine: 'unity', target_platform: 'xr',
    });
    expect(briefPayload(fields, { target_engine: '', target_platform: undefined })).toEqual({});
    // Text written before the field became a select is sent back unchanged: the
    // server keeps a value the brief already stores, so editing another field
    // does not strand it.
    expect(briefPayload(fields, { target_platform: '手机', style: '低模' })).toEqual({
      target_platform: '手机', style: '低模',
    });
    expect(briefPayload(fields, { target_engine: 7 })).toEqual({});
  });
});

describe('how a stored value reads', () => {
  it('words booleans, prints numbers and passes text through', () => {
    expect(briefValueText(true, '是', '否')).toBe('是');
    expect(briefValueText(false, '是', '否')).toBe('否');
    expect(briefValueText(12000, '是', '否')).toBe('12000');
    expect(briefValueText('low poly', '是', '否')).toBe('low poly');
    expect(briefValueText(null, '是', '否')).toBe('');
  });
});

describe('how a select value reads (#286)', () => {
  const platform = fields.find((field) => field.name === 'target_platform')!;

  it('reads an option id as its label', () => {
    expect(briefSelectText(platform, 'pc_console')).toBe('PC / 主机');
    expect(briefSelectText(platform, 'xr')).toBe('XR（VR / AR / MR）');
  });

  it('reads a stored value that is no option as it was written, and an empty one as nothing', () => {
    expect(briefSelectText(platform, '手机 + Switch')).toBe('手机 + Switch');
    expect(briefSelectText(platform, 'Mobile')).toBe('Mobile');
    expect(briefSelectText(platform, '')).toBe('');
    expect(briefSelectText(platform, undefined)).toBe('');
    expect(briefSelectText(platform, 3)).toBe('');
    expect(briefSelectText({}, 'unity')).toBe('unity');
  });
});

describe('the brief route', () => {
  it('encodes the session id into the Schema route', () => {
    expect(briefRoute(schema, 's/1')).toBe('/api/v1/chat/sessions/s%2F1/brief');
  });

  it('is absent when the Schema has no route', () => {
    const bare = structuredClone(schema);
    delete bare.runtime.http.routes.chat_session_brief;
    expect(briefRoute(bare, 's1')).toBeNull();
  });
});
