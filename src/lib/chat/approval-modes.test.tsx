import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import type { ProductSchema } from '@/lib/product-schema';
import {
  approvalModeOptions,
  paidStepFailed,
  sessionCreditsText,
  stepCreditsText,
  stepFailedText,
} from './approval-modes';
import { PaidStepNotes } from '@/components/chat/paid-step-notes';

function schemaFixture(): ProductSchema {
  return structuredClone(defaultCatalog.application) as ProductSchema;
}

describe('approval modes', () => {
  it('offers exactly the modes the Schema declares, with Schema text', () => {
    const schema = schemaFixture();
    const options = approvalModeOptions(schema);
    expect(options.map((option) => option.mode)).toEqual(['auto', 'manual']);
    expect(options[0].label).toBe('自动');
    expect(options[1].label).toBe('手工');
    expect(options[1].tooltip).toContain('文生 3D');

    schema.chat.approval_policy.modes = ['manual'];
    expect(approvalModeOptions(schema).map((option) => option.mode)).toEqual(['manual']);
    schema.chat.approval_policy.modes = ['manual', 'yolo'];
    expect(approvalModeOptions(schema).map((option) => option.mode)).toEqual(['manual']);
  });
});

describe('paid step credit display', () => {
  it('formats a step and the session estimate from Schema text', () => {
    const schema = schemaFixture();
    expect(stepCreditsText(schema, 7)).toBe('本步消耗 7 积分');
    expect(sessionCreditsText(schema, 25)).toContain('25');
    expect(sessionCreditsText(schema, 25)).toContain('估算');
  });

  it('notes automatic approval only on steps the policy approved', () => {
    const schema = schemaFixture();
    const html = renderToStaticMarkup(
      <PaidStepNotes
        steps={[
          { taskId: 'task-a', credits: 3, approvedByPolicy: true },
          { taskId: 'task-b', credits: 40, approvedByPolicy: false },
        ]}
        creditsText={(credits) => stepCreditsText(schema, credits)}
        autoApprovedText="已按批准方式自动批准"
      />,
    );
    expect(html).toContain('本步消耗 3 积分 · 已按批准方式自动批准');
    expect(html).toContain('本步消耗 40 积分</li>');
  });

  it('says a failed step did not complete instead of claiming it was charged', () => {
    const schema = schemaFixture();
    expect(stepFailedText(schema, 10)).toBe('本步未完成（预计 10 积分）');
    const html = renderToStaticMarkup(
      <PaidStepNotes
        steps={[
          { taskId: 'task-a', credits: 10, approvedByPolicy: true },
          { taskId: 'task-b', credits: 40, approvedByPolicy: false },
        ]}
        creditsText={(credits) => stepCreditsText(schema, credits)}
        failedText={(credits) => stepFailedText(schema, credits)}
        failed={(taskId) => taskId === 'task-a'}
        autoApprovedText="已按批准方式自动批准"
      />,
    );
    expect(html).toContain('本步未完成（预计 10 积分） · 已按批准方式自动批准');
    expect(html).toContain('本步消耗 40 积分</li>');
    expect(html).not.toContain('本步消耗 10 积分');
  });

  it('treats failed, canceled and missing tasks as not completed', () => {
    const schema = schemaFixture();
    const base = {
      taskId: 't', capability: 'text_to_3d', prompt: '', state: '', progress: 0, progressMaximum: 1, terminal: false,
      failure: null, liveError: '', assets: [], downloadables: [], resources: [], resourcesPending: false, notFound: false, updatedAt: 0,
    };
    expect(paidStepFailed(schema, undefined)).toBe(false);
    expect(paidStepFailed(schema, { ...base, state: 'RUNNING' })).toBe(false);
    expect(paidStepFailed(schema, { ...base, state: 'SUCCEEDED', terminal: true })).toBe(false);
    expect(paidStepFailed(schema, { ...base, state: 'FAILED', terminal: true })).toBe(true);
    expect(paidStepFailed(schema, { ...base, state: 'CANCELED', terminal: true })).toBe(true);
    expect(paidStepFailed(schema, { ...base, notFound: true })).toBe(true);
    expect(paidStepFailed(schema, { ...base, state: 'RUNNING', terminal: true, liveError: 'error.generation_failed' })).toBe(true);
  });

  it('renders nothing without paid steps', () => {
    expect(renderToStaticMarkup(
      <PaidStepNotes steps={[]} creditsText={() => ''} autoApprovedText="" />,
    )).toBe('');
  });
});
