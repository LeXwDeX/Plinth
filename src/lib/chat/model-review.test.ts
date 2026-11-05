import { describe, expect, it } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import type { ProductSchema } from '@/lib/product-schema';
import type { ChatToolCall } from '@/store/chat-store';
import type { MessagePicture } from '@/lib/chat/message-pictures';
import {
  inspectFactItems,
  reviewCheckName,
  reviewLine,
  reviewModelLabel,
  reviewRecordOf,
  reviewRounds,
  reviewSummaryOf,
  reviewToolKind,
  reviewViewName,
  type ReviewCallModel,
  type ReviewCheckItem,
  type ReviewSummary,
} from '@/lib/chat/model-review';

const schema = defaultCatalog.application as unknown as ProductSchema;

const summary = (overrides: Partial<ReviewSummary> = {}): ReviewSummary => ({
  kind: 'inspect', model: '', models: [], views: [], handles: [], failedViews: [],
  triangles: null, hasUV: null, watertight: null, hasSkin: null, usage: null,
  ...overrides,
});

const call = (name: string, value?: unknown): ChatToolCall => ({ name, ...(value === undefined ? {} : { summary: value }) });

const check = (name: string, status: ReviewCheckItem['status'], fixHint = ''): ReviewCheckItem => ({
  check: name, status, actual: null, budget: {}, fixHint,
});

const picture = (index: number, handle?: string): MessagePicture => ({
  image: { url: `/api/v1/assets/${index}`, taskId: 't1' }, index, ...(handle ? { handle } : {}),
});

const reviewCall = (kind: ReviewCallModel['kind'], value: Partial<ReviewSummary> = {}, pictures: MessagePicture[] = []): ReviewCallModel => ({
  kind, summary: summary({ kind, ...value }), pictures,
});

describe('the tool table decides what is a review', () => {
  it('recognises the three Schema tool names and nothing else', () => {
    expect(reviewToolKind(schema, 'inspect_model')).toBe('inspect');
    expect(reviewToolKind(schema, 'render_model_views')).toBe('render');
    expect(reviewToolKind(schema, 'compare_models')).toBe('compare');
    expect(reviewToolKind(schema, 'create_model_3d')).toBeNull();
    expect(reviewToolKind(schema, '')).toBeNull();
  });

  it('a renamed tool follows the Schema, not a name in code', () => {
    const renamed = structuredClone(schema);
    renamed.chat.model_review!.tools = { inspect: 'check_mesh', render: 'shoot_views', compare: 'diff_models' };
    expect(reviewToolKind(renamed, 'check_mesh')).toBe('inspect');
    expect(reviewToolKind(renamed, 'inspect_model')).toBeNull();
  });
});

describe('a review call summary', () => {
  it('reads the compact public facts stored with the call', () => {
    const value = reviewSummaryOf(schema, {
      name: 'inspect_model',
      summary: {
        tool: 'inspect', model: '@model3', triangles: 19000, has_uv: true, watertight: false, has_skin: true,
        usage: {
          purpose: 'game_prop', purpose_label: '游戏/实时·道具与场景件', purpose_source: 'brief',
          note: '按制作简报判定', checks: [{ check: 'uv', status: 'pass', actual: true, budget: {}, fix_hint: '' }],
        },
      },
    });
    expect(value).toMatchObject({
      kind: 'inspect', model: '@model3', triangles: 19000, hasUV: true, watertight: false, hasSkin: true,
      usage: { purposeLabel: '游戏/实时·道具与场景件', checks: [{ check: 'uv', status: 'pass' }] },
    });
  });

  it('falls back to the tool kind, drops malformed checks and keeps an unknown check status unknown', () => {
    const value = reviewSummaryOf(schema, {
      name: 'render_model_views',
      summary: {
        views: ['front', 'back', 7], handles: ['@render1', '', 9], failed_views: ['top'], triangles: 'many',
        usage: { checks: [{ check: '', status: 'fail' }, { check: 'skin', status: 'odd' }] },
      },
    });
    expect(value).toMatchObject({
      kind: 'render', views: ['front', 'back'], handles: ['@render1'], failedViews: ['top'], triangles: null,
      usage: { checks: [{ check: 'skin', status: 'unknown' }] },
    });
  });

  it('gives no summary to a call that is not a review tool', () => {
    expect(reviewSummaryOf(schema, call('create_model_3d'))).toBeNull();
  });
});

describe('the one-line inspection', () => {
  it('reads the usage facts in order', () => {
    const value = summary({ model: '@model3', triangles: 19000, hasUV: true, watertight: false, hasSkin: true });
    expect(inspectFactItems(schema, value)).toEqual(['1.9万 三角面', '有 UV', '不水密', '已绑骨']);
    expect(reviewLine(schema, value)).toBe('检查了 @model3：1.9万 三角面 · 有 UV · 不水密 · 已绑骨');
  });

  it('omits an absent rig and names the failing checks the facts do not already state', () => {
    const value = summary({
      model: 'model3', hasUV: false,
      usage: {
        purpose: 'print', purposeLabel: '3D 打印', purposeSource: 'brief', note: '',
        checks: [
          check('uv', 'fail'),
          check('texture_size', 'fail', '把贴图缩到 2048'),
          check('debris', 'fail'),
          check('degenerate', 'fail'),
          check('skin', 'pass'),
        ],
      },
    });
    expect(inspectFactItems(schema, value)).toEqual([
      '无 UV', '贴图尺寸不符合预算', '漂浮碎片不符合预算', '另 1 项',
    ]);
  });

  it('falls back to the current model when the call named none', () => {
    expect(reviewModelLabel(schema, '')).toBe('当前模型');
    expect(reviewLine(schema, summary())).toBe('检查了 当前模型：');
  });
});

describe('the one-line render and comparison', () => {
  it('names the rendered views, and how many failed when some did', () => {
    expect(reviewLine(schema, summary({ kind: 'render', model: 'model3', views: ['front', 'three_quarter'] })))
      .toBe('渲染了 model3：正面、3/4 视角');
    expect(reviewLine(schema, summary({ kind: 'render', model: 'model3', views: ['front'], failedViews: ['back'] })))
      .toBe('渲染了 model3：正面 · 1 个视角失败');
  });

  it('reports only the failures when no view rendered', () => {
    expect(reviewLine(schema, summary({ kind: 'render', model: 'model3', failedViews: ['back', 'left'] })))
      .toBe('渲染了 model3：2 个视角失败');
  });

  it('names both compared models, or the selected one', () => {
    expect(reviewLine(schema, summary({ kind: 'compare', models: ['model3', 'model4'], views: ['front'] })))
      .toBe('对比了 model3、model4');
    expect(reviewLine(schema, summary({ kind: 'compare' }))).toBe('对比了 当前模型');
  });

  it('falls back to the raw view and check ids the Schema does not name', () => {
    expect(reviewViewName(schema, 'unknown_view')).toBe('unknown_view');
    expect(reviewCheckName(schema, 'unknown_check')).toBe('unknown_check');
    expect(reviewViewName(schema, 'front')).toBe('正面');
    expect(reviewCheckName(schema, 'watertight')).toBe('水密');
  });
});

describe('pairing rendered pictures with the calls that produced them', () => {
  it('claims pictures by the view count of each call, in call order, and keeps the leftovers', () => {
    const renders = Array.from({ length: 6 }, (_, index) => picture(index));
    const record = reviewRecordOf(schema, [
      call('render_model_views', { tool: 'render', views: ['front', 'back', 'three_quarter'] }),
      call('inspect_model', { tool: 'inspect' }),
      call('create_model_3d', {}),
      call('compare_models', { tool: 'compare', models: ['a', 'b'], views: ['three_quarter', 'left'] }),
    ], renders);
    expect(record.calls.map((entry) => entry.kind)).toEqual(['render', 'inspect', 'compare']);
    expect(record.calls.map((entry) => entry.pictures.length)).toEqual([3, 0, 2]);
    expect(record.calls[0].pictures.map((entry) => entry.index)).toEqual([0, 1, 2]);
    expect(record.calls[2].pictures.map((entry) => entry.index)).toEqual([3, 4]);
    expect(record.leftover.map((entry) => entry.index)).toEqual([5]);
  });

  it('leaves every picture as a leftover when no review call produced one', () => {
    const record = reviewRecordOf(schema, [call('inspect_model', { view: [] })], [picture(0), picture(1)]);
    expect(record.calls[0].pictures).toEqual([]);
    expect(record.leftover.map((entry) => entry.index)).toEqual([0, 1]);
  });

  it('binds pictures by the handles each summary names, whatever their attachment order', () => {
    const renders = [picture(0, '@c'), picture(1, '@a'), picture(2, '@b')];
    const record = reviewRecordOf(schema, [
      call('render_model_views', { tool: 'render', views: ['front', 'back', 'three_quarter'], handles: ['@a', '@b', '@c'] }),
    ], renders);
    expect(record.calls[0].pictures.map((entry) => [entry.index, entry.handle])).toEqual([[1, '@a'], [2, '@b'], [0, '@c']]);
    expect(record.leftover).toEqual([]);
  });

  it('does not guess a picture when the named handles are fewer than the views', () => {
    const renders = [picture(0, '@a'), picture(1, '@b'), picture(2, '@c')];
    const record = reviewRecordOf(schema, [
      call('render_model_views', { tool: 'render', views: ['front', 'back', 'three_quarter'], handles: ['@a', '@b'] }),
    ], renders);
    expect(record.calls[0].pictures.map((entry) => entry.index)).toEqual([0, 1]);
    expect(record.leftover.map((entry) => [entry.index, entry.handle])).toEqual([[2, '@c']]);
  });

  it('never claims one picture for two calls, and keeps unresolved pictures as leftovers in order', () => {
    const renders = [picture(0, '@a'), picture(1, '@shared'), picture(2, '@shared'), picture(3, '@z')];
    const record = reviewRecordOf(schema, [
      call('render_model_views', { tool: 'render', views: ['front'], handles: ['@shared'] }),
      call('compare_models', { tool: 'compare', models: ['a', 'b'], views: ['front'], handles: ['@shared', '@a'] }),
    ], renders);
    expect(record.calls[0].pictures.map((entry) => entry.index)).toEqual([1]);
    expect(record.calls[1].pictures.map((entry) => entry.index)).toEqual([2, 0]);
    expect(record.leftover.map((entry) => entry.index)).toEqual([3]);
  });

  it('keeps the positional fallback only for old summaries without handles', () => {
    const renders = [picture(0, '@later'), picture(1, '@a')];
    const record = reviewRecordOf(schema, [
      call('render_model_views', { tool: 'render', views: ['front', 'back'] }),
    ], renders);
    expect(record.calls[0].pictures.map((entry) => entry.index)).toEqual([0, 1]);
  });
});

describe('the self-check rounds of one reply', () => {
  it('opens a round at each inspection and closes the previous one with the recheck result', () => {
    const rounds = reviewRounds([
      reviewCall('inspect', { usage: { purpose: '', purposeLabel: '', purposeSource: '', note: '', checks: [check('uv', 'fail')] } }),
      reviewCall('render', { views: ['front'] }, [picture(0)]),
      reviewCall('inspect'),
      reviewCall('compare', { models: ['a', 'b'] }),
    ]);
    expect(rounds).toHaveLength(2);
    expect(rounds[0]).toMatchObject({
      index: 1,
      findings: [{ check: 'uv', status: 'fail' }],
      follow: [{ kind: 'render' }],
      result: { kind: 'ok' },
    });
    expect(rounds[1]).toMatchObject({ index: 2, findings: [], follow: [{ kind: 'compare' }], result: { kind: 'pending' } });
  });

  it('says how many findings remain when the recheck still fails', () => {
    const rounds = reviewRounds([
      reviewCall('inspect'),
      reviewCall('inspect', { usage: { purpose: '', purposeLabel: '', purposeSource: '', note: '', checks: [check('uv', 'fail'), check('debris', 'fail')] } }),
    ]);
    expect(rounds[0].result).toEqual({ kind: 'rest', count: 2 });
  });

  it('makes one round without findings for a reply that only renders', () => {
    const rounds = reviewRounds([reviewCall('render', { views: ['front'] }, [picture(0)])]);
    expect(rounds).toHaveLength(1);
    expect(rounds[0]).toMatchObject({ index: 1, inspect: null, findings: [], result: { kind: 'pending' } });
    expect(rounds[0].follow).toHaveLength(1);
  });

  it('has no round for no review calls', () => {
    expect(reviewRounds([])).toEqual([]);
  });
});
