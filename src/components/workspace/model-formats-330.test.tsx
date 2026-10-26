import React from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import { ModelFormatsView } from '@/components/workspace/model-formats';
import { schemaCountMessage, schemaMessage, type ProductSchema } from '@/lib/product-schema';
import { failedSteps, formatRowAction, parseModelFormatCoverage } from '@/lib/workspace/model-formats';
import { parseRefetchEligibility } from '@/lib/workspace/task-refetch';

const schema = defaultCatalog.application as unknown as ProductSchema;
const messages = (defaultCatalog.application as { content: { messages: Record<string, string> } }).content.messages;
const english = (defaultCatalog.application as unknown as { localization: { bundles: { en: { messages: Record<string, string> } } } }).localization.bundles.en.messages;
const englishSchema = {
  ...schema,
  content: { ...schema.content, messages: { ...messages, ...english } },
  localization: { ...schema.localization!, active_locale: 'en' },
} as ProductSchema;

vi.mock('@/components/product-schema-provider', () => ({
  useProductSchema: () => ({ schema: defaultCatalog.application, text: (key: string) => messages[key] ?? '' }),
}));
vi.mock('@/store/workspace-store', () => ({
  useWorkspaceStore: (selector: (state: object) => unknown) => selector({ registerTask: () => {} }),
}));

// #330 B: Meshy Convert finished and charged 1 credit, then the platform
// refused its files. The format list says so, offers the free refetch of that
// result first, and only then a retry that charges again.
const meshy = { provider_id: 'meshy', provider_name: 'Meshy AI', estimated_credits: 1, credits_metered: true };
const formats = [
  { id: 'glb', label: 'GLB', status: 'available', task_id: 't1', asset_id: 'a1' },
  { id: 'fbx', label: 'FBX', status: 'obtainable', conversion: meshy, reason: 'not_converted' },
  { id: 'obj', label: 'OBJ', status: 'obtainable', conversion: meshy, reason: 'not_converted' },
];
const failedStep = (refetch: Record<string, unknown> | undefined, charged = 1) => ({
  offering_id: 'meshy/processing/convert', provider_id: 'meshy', provider_name: 'Meshy AI', formats: ['fbx', 'obj'],
  estimated_credits: 1, state: 'FAILED', plan_id: 'p1', plan_version: 2, offering_revision: 'r', failure: 'plan_task_failed',
  result_task_id: 'convert-failed', ...(charged ? { charged_credits: charged } : {}), ...(refetch ? { refetch } : {}),
});
const coverageOf = (step: Record<string, unknown>) => parseModelFormatCoverage({ task_id: 't1', applicable: true, formats, steps: [step] })!;
const render = (step: Record<string, unknown>, onRefetch?: () => void, s: ProductSchema = schema) => renderToStaticMarkup(
  <ModelFormatsView schema={s} coverage={coverageOf(step)} busy={false} error="" onComplete={() => {}} onRetry={() => {}} onConvert={() => {}} onRefetch={onRefetch} />,
);
const rowOf = (html: string, id: string) => {
  const start = html.indexOf(`data-testid="model-format-${id}"`);
  const end = html.indexOf('</li>', start);
  return html.slice(start, end);
};

describe('a paid conversion that was charged but not delivered (#330)', () => {
  it('parses the charge and the refetch of a failed step', () => {
    const [step] = coverageOf(failedStep({ eligible: true })).steps;
    expect(step.chargedCredits).toBe(1);
    expect(step.refetch).toEqual({ eligible: true, reason: '', taskId: '', state: '', failed: 0 });
  });

  it('reads the charge from the task refetch view', () => {
    expect(parseRefetchEligibility({ source_task_id: 't', eligible: true, charged_credits: 2, failed_refetches: 1 }, 't')).toMatchObject({ chargedCredits: 2, failedRefetches: 1 });
    expect(parseRefetchEligibility({ source_task_id: 't', eligible: true, charged_credits: -1 }, 't')).not.toHaveProperty('chargedCredits');
  });

  it('offers the free refetch first and says what was charged', () => {
    const coverage = coverageOf(failedStep({ eligible: true }));
    expect(formatRowAction(coverage, coverage.formats[1])?.kind).toBe('refetch');
    const fbx = rowOf(render(failedStep({ eligible: true }), () => {}), 'fbx');
    expect(fbx).toContain('data-action="refetch"');
    expect(fbx).toContain(`${messages['ui.workspace.model_formats.refetch']}</button>`);
    expect(fbx).toContain(schemaCountMessage(schema, 'ui.workspace.model_formats.refetch_hint', 1, { credits: 1 }));
    expect(fbx).toContain(messages['ui.workspace.model_formats.free']);
    expect(fbx).not.toContain(messages['ui.workspace.model_formats.retry_hint']);
  });

  it('falls back to a retry that says it charges again once the free refetch is not possible', () => {
    for (const refetch of [{ eligible: false, reason: 'repeated_failure', failed: 1 }, { eligible: true, failed: 1 }, { eligible: false, reason: 'expired' }]) {
      const fbx = rowOf(render(failedStep(refetch), () => {}), 'fbx');
      expect(fbx).toContain('data-action="retry"');
      expect(fbx).toContain(`${messages['ui.workspace.model_formats.retry_charged']}</button>`);
      expect(fbx).toContain(schemaCountMessage(schema, 'ui.workspace.model_formats.retry_charged_hint', 1, { credits: 1 }));
      expect(fbx).toContain('约 1 积分');
    }
  });

  it('keeps the plain retry for a failure that charged nothing', () => {
    const fbx = rowOf(render(failedStep(undefined, 0)), 'fbx');
    expect(fbx).toContain(`${messages['ui.workspace.model_formats.retry']}</button>`);
    expect(fbx).toContain(messages['ui.workspace.model_formats.retry_hint']);
    expect(fbx).not.toContain(messages['ui.workspace.model_formats.retry_charged']);
  });

  it('without a refetch handler the row still offers the charged retry, never a dead button', () => {
    const fbx = rowOf(render(failedStep({ eligible: true })), 'fbx');
    expect(fbx).toContain('data-action="retry"');
    expect(fbx).toContain(messages['ui.workspace.model_formats.retry_charged']);
  });

  it('the failure block names the charge and leads with the free refetch', () => {
    const html = render(failedStep({ eligible: true }), () => {});
    const block = html.slice(html.indexOf('data-testid="model-formats-failed"'));
    expect(block).toContain(schemaCountMessage(schema, 'ui.workspace.model_formats.charged_note', 1, { credits: 1 }));
    expect(block).toContain('data-action="refetch"');
    expect(block).toContain(schemaMessage(schema, 'ui.workspace.model_formats.refetch_named', { format: 'FBX、OBJ' }));
    const paid = render(failedStep({ eligible: false, reason: 'expired' }), () => {});
    const paidBlock = paid.slice(paid.indexOf('data-testid="model-formats-failed"'));
    expect(paidBlock).toContain(schemaMessage(schema, 'ui.workspace.model_formats.retry_charged_named', { format: 'FBX、OBJ' }));
    expect(paidBlock).toContain('data-action="retry"');
  });

  it('a running or delivered refetch is not a failure to act on', () => {
    for (const state of ['RUNNING', 'SUCCEEDED']) {
      const coverage = coverageOf(failedStep({ eligible: true, task_id: 'refetch-1', state }));
      expect(failedSteps(coverage)).toEqual([]);
      expect(formatRowAction(coverage, coverage.formats[1])).toBeNull();
    }
    const again = coverageOf(failedStep({ eligible: true, task_id: 'refetch-1', state: 'FAILED', failed: 1 }));
    expect(failedSteps(again)).toHaveLength(1);
  });

  it('declares the new texts in both languages', () => {
    for (const name of ['refetch', 'refetch_named', 'refetch_hint', 'refetch_hint_one', 'retry_charged', 'retry_charged_named', 'retry_charged_hint', 'retry_charged_hint_one', 'charged_note', 'charged_note_one', 'failed_step', 'credits_one', 'complete_one']) {
      const key = `ui.workspace.model_formats.${name}`;
      expect(messages[key], key).toBeTruthy();
      expect(english[key], key).toBeTruthy();
      expect(english[key]).not.toMatch(/[一-鿿]/);
    }
  });
});

// #330 C: the English format list said "About 1 credits".
describe('credit counts follow the locale plural rules (#330 C)', () => {
  it('uses the singular Schema text for one credit in English', () => {
    expect(schemaCountMessage(englishSchema, 'ui.workspace.model_formats.credits', 1, { credits: 1 })).toBe('About 1 credit');
    expect(schemaCountMessage(englishSchema, 'ui.workspace.model_formats.credits', 2, { credits: 2 })).toBe('About 2 credits');
    expect(schemaCountMessage(englishSchema, 'ui.workspace.model_formats.complete', 1, { credits: 1 })).toBe(schemaMessage(englishSchema, 'ui.workspace.model_formats.complete_one', { credits: 1 }));
    expect(schemaMessage(englishSchema, 'ui.workspace.model_formats.complete_one', { credits: 1 })).not.toMatch(/credits/);
    // Chinese has no singular form; a missing _one text falls back to the general one.
    expect(schemaCountMessage(schema, 'ui.workspace.model_formats.credits', 1, { credits: 1 })).toBe('约 1 积分');
    expect(schemaCountMessage(englishSchema, 'ui.workspace.model_formats.free', 1, {})).toBe(english['ui.workspace.model_formats.free']);
  });

  it('renders the English row and the completion button in the singular', () => {
    const html = renderToStaticMarkup(
      <ModelFormatsView schema={englishSchema} coverage={parseModelFormatCoverage({ task_id: 't1', applicable: true, formats, steps: [{ ...failedStep(undefined, 0), state: '', plan_id: '' }], can_complete: true, completion_credits: 1 })!}
        busy={false} error="" onComplete={() => {}} onRetry={() => {}} onConvert={() => {}} />,
    );
    expect(html).toContain('About 1 credit<');
    expect(html).not.toContain('1 credits');
  });
});
