import { describe, expect, it } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import type { ProductSchema } from '@/lib/product-schema';
import {
  parseSelfChecks,
  selfCheckCaveat,
  selfCheckLine,
  selfCheckMetricText,
  selfCheckRecordsByTask,
  selfCheckStatus,
  selfCheckStatusLabel,
  type SelfCheckRecord,
} from '@/lib/chat/self-check';

const schema = defaultCatalog.application as unknown as ProductSchema;

const wire = (overrides: Record<string, unknown> = {}) => ({
  taskId: 'task-a',
  status: 'passed',
  roundsUsed: 1,
  roundsLimit: 3,
  rendersInRound: 1,
  rendersLimit: 6,
  correctionsUsed: 0,
  correctionsLimit: 2,
  checks: [
    { round: 1, kind: 'inspect', tool: 'inspect_model', views: [], handles: [], ok: true, at: '2026-11-10T00:00:00Z' },
    {
      round: 1, kind: 'reference', tool: 'compare_model_reference', views: ['front'], handles: ['@img1'],
      ok: true, at: '2026-11-10T00:00:01Z',
      metrics: {
        silhouette_iou: 0.8123, bbox_aspect_delta: 0.05, dominant_color_distance: 12.5,
        dominant_reference: '#cc2222', dominant_render: '#aa3333',
        reference_subject_ratio: 0.25, render_subject_ratio: 0.21, method: 'normalized_silhouette_iou_v1',
      },
    },
  ],
  conclusion: { status: 'passed', summary: 'matches the reference', findings: ['outline ok'], fixes: [], remaining: [], at: '2026-11-10T00:00:02Z' },
  updatedAt: '2026-11-10T00:00:02Z',
  ...overrides,
});

const record = (overrides: Partial<SelfCheckRecord> = {}): SelfCheckRecord => ({
  taskId: 'task-a', status: 'passed', roundsUsed: 1, roundsLimit: 3, rendersInRound: 1, rendersLimit: 6,
  correctionsUsed: 0, correctionsLimit: 2, checks: [], conclusion: null, updatedAt: '',
  ...overrides,
});

describe('the persisted self-check records (#246)', () => {
  it('reads the server rows by the Schema response-field names', () => {
    const records = parseSelfChecks(schema, { selfChecks: [wire()] });
    expect(records).toHaveLength(1);
    expect(records[0].taskId).toBe('task-a');
    expect(records[0].status).toBe('passed');
    expect(records[0].roundsUsed).toBe(1);
    expect(records[0].rendersLimit).toBe(6);
    expect(records[0].conclusion?.summary).toBe('matches the reference');
    expect(records[0].checks[1].views).toEqual(['front']);
    expect(records[0].checks[1].handles).toEqual(['@img1']);
    expect(records[0].checks[1].metrics?.silhouetteIou).toBeCloseTo(0.8123);
    expect(records[0].checks[1].metrics?.dominantReference).toBe('#cc2222');
  });

  it('drops rows without a Task and tolerates a malformed record', () => {
    const records = parseSelfChecks(schema, {
      selfChecks: [{ task_id: 'wrong-name' }, wire({ taskId: '' }), wire({ checks: 'not a list', conclusion: 'x' })],
    });
    expect(records).toHaveLength(1);
    expect(records[0].checks).toEqual([]);
    expect(records[0].conclusion).toBeNull();
  });

  it('takes the state label from the Schema role, not from a status id in code', () => {
    const renamed = structuredClone(schema);
    renamed.chat.self_check!.statuses = [
      { id: 'fresh', role: 'unchecked', label: '待检查' },
      { id: 'looks_good', role: 'passed', label: '检查通过' },
      { id: 'notes', role: 'issues', label: '有备注' },
    ];
    expect(selfCheckStatus(renamed, record({ status: 'looks_good' }))).toMatchObject({ role: 'passed', label: '检查通过' });
    expect(selfCheckStatusLabel(renamed, record({ status: 'looks_good' }))).toBe('检查通过');
    // An unknown stored id degrades to the declared unchecked state, never to passed.
    expect(selfCheckStatus(renamed, record({ status: 'done' }))).toMatchObject({ role: 'unchecked' });
    expect(selfCheckStatus(renamed, null).role).toBe('unchecked');
    expect(selfCheckRecordsByTask([record(), record({ taskId: 'task-b' })]).get('task-b')?.taskId).toBe('task-b');
  });

  it('builds one evidence line and the deterministic metric text', () => {
    const records = parseSelfChecks(schema, { selfChecks: [wire()] });
    const check = records[0].checks[1];
    expect(selfCheckLine(schema, check, 'task-a')).toContain('正面');
    const metrics = selfCheckMetricText(schema, check);
    expect(metrics).toHaveLength(2);
    expect(metrics[0]).toContain('0.8123');
    expect(metrics[1]).toContain('12.5');
    expect(selfCheckCaveat(schema, records)).toContain('GLB');
    expect(selfCheckCaveat(schema, [record()])).toBe('');
  });
})