import { describe, expect, it } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import type { ProductSchema } from '@/lib/product-schema';
import {
  briefIsEmpty,
  briefPayload,
  briefRoute,
  briefValueText,
  parseProductionBrief,
  productionBriefFields,
} from '@/lib/chat/production-brief';

const schema = defaultCatalog.application as unknown as ProductSchema;
const fields = productionBriefFields(schema);

describe('the declared brief fields', () => {
  it('reads names, types, labels and limits from the Schema in its order', () => {
    expect(fields.map((field) => field.name)).toEqual([
      'purpose', 'style', 'target_platform', 'needs_parts', 'needs_rig', 'needs_animation', 'triangle_budget', 'notes',
    ]);
    const style = fields.find((field) => field.name === 'style')!;
    expect(style).toMatchObject({ type: 'text', label: '风格', maximumCharacters: 200 });
    const budget = fields.find((field) => field.name === 'triangle_budget')!;
    expect(budget).toMatchObject({ type: 'integer', minimum: 100, maximum: 2000000 });
    expect(fields.find((field) => field.name === 'purpose')?.type).toBe('profile');
  });

  it('has no fields without a declared brief', () => {
    const bare = structuredClone(schema);
    delete bare.chat.model3d_usage!.brief;
    expect(productionBriefFields(bare)).toEqual([]);
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
