import { describe, expect, it } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import type { ProductSchema } from '@/lib/product-schema';
import {
  archiveFailureText,
  completionIsFree,
  conversionIsFree,
  conversionProviderLabel,
  deliveryFormatRows,
  formatRowAction,
  modelFormatsCompleteBody,
  failedSteps,
  formatsFooter,
  modelArchiveRoute,
  modelFormatsCompleteRoute,
  modelFormatsRoute,
  parseModelFormatCoverage,
  pausedSteps,
  planRetryRoute,
  problemText,
  stepsInProgress,
  taskAssetRoute,
} from './model-formats';

const schema = defaultCatalog.application as unknown as ProductSchema;

const wire = {
  task_id: 'task-1',
  applicable: true,
  formats: [
    { id: 'glb', label: 'GLB', status: 'available', task_id: 'task-1', asset_id: 'a-glb' },
    { id: 'fbx', label: 'FBX', status: 'obtainable', conversion: { provider_name: 'Tripo', estimated_credits: 5 } },
    { id: 'gltf', label: 'glTF', status: 'unavailable', reason: 'source_incompatible', reason_text: '没有适用的转换服务。' },
    { id: 'bogus', label: 'X', status: 'mystery' },
  ],
  steps: [
    { offering_id: 'o1', capability: 'format-conversion', formats: ['fbx'], estimated_credits: 5, state: 'RUNNING', plan_id: 'p1' },
    { offering_id: 'o2', capability: 'processing', formats: ['obj'], estimated_credits: 5, state: 'FAILED', plan_id: 'p2', plan_version: 3, offering_revision: 'r1', failure: 'x' },
    { offering_id: 'o3', capability: 'processing', formats: ['stl'], estimated_credits: 5, state: 'PAUSED', plan_id: 'p3' },
  ],
  completion_credits: 10,
  can_complete: true,
  archive_available: true,
};

describe('model format coverage', () => {
  it('parses the server projection and drops malformed rows', () => {
    const coverage = parseModelFormatCoverage(wire);
    expect(coverage?.formats.map((row) => row.id)).toEqual(['glb', 'fbx', 'gltf']);
    expect(coverage?.formats[1]).toMatchObject({ status: 'obtainable', estimatedCredits: 5, providerName: 'Tripo' });
    expect(coverage?.formats[0]).toMatchObject({ taskId: 'task-1', assetId: 'a-glb' });
    expect(coverage?.formats[2].reasonText).toBe('没有适用的转换服务。');
    expect(coverage).toMatchObject({ canComplete: true, completionCredits: 10, archiveAvailable: true });
  });

  it('rejects payloads without a task id', () => {
    expect(parseModelFormatCoverage(null)).toBeNull();
    expect(parseModelFormatCoverage({ applicable: true })).toBeNull();
    expect(parseModelFormatCoverage({ task_id: 't' })).toMatchObject({ applicable: false, formats: [], canComplete: false });
  });

  it('classifies steps by plan state', () => {
    const coverage = parseModelFormatCoverage(wire)!;
    expect(stepsInProgress(coverage)).toBe(true);
    expect(failedSteps(coverage).map((step) => step.planId)).toEqual(['p2']);
    expect(failedSteps(coverage)[0]).toMatchObject({ planVersion: 3, offeringRevision: 'r1' });
    expect(pausedSteps(coverage).map((step) => step.planId)).toEqual(['p3']);
  });

  it('builds routes from the Schema', () => {
    expect(modelFormatsRoute(schema, 'task 1')).toBe('/api/v1/tasks/task%201/formats');
    expect(modelFormatsCompleteRoute(schema, 't')).toBe('/api/v1/tasks/t/formats/complete');
    expect(modelArchiveRoute(schema, 't')).toBe('/api/v1/tasks/t/archive');
    expect(taskAssetRoute(schema, 't', 'a')).toContain('/t/');
    expect(planRetryRoute(schema, 'p')).toContain('p');
  });

  it('uses the localized Schema reason for a too-large archive and the server detail otherwise', async () => {
    const tooLarge = defaultCatalog.application.content.messages['error.model_archive_too_large'];
    expect(await archiveFailureText(schema, new Response('{}', { status: 413 }))).toBe(tooLarge);
    const reason = '模型文件总大小超过一键打包的上限，请逐个格式下载。';
    const detail = new Response(JSON.stringify({ detail: reason }), { status: 500 });
    expect(await archiveFailureText(schema, detail)).toBe(reason);
    expect(await archiveFailureText(schema, new Response('not json', { status: 500 }))).toBe(
      defaultCatalog.application.content.messages['ui.workspace.model_formats.download_failed'],
    );
  });

  it('keeps the safe server reason of a failed archive request', async () => {
    const tooLarge = defaultCatalog.application.content.messages['error.model_archive_too_large'];
    const response = new Response(JSON.stringify({ code: 'too_large', detail: tooLarge }), { status: 413 });
    expect(await problemText(response, 'fallback')).toBe(tooLarge);
    expect(tooLarge).toContain('逐个格式下载');
    expect(await problemText(new Response(JSON.stringify({ detail: '' }), { status: 500 }), 'fallback')).toBe('fallback');
    expect(await problemText(new Response('not json', { status: 500 }), 'fallback')).toBe('fallback');
  });

  it('has no routes when the Schema omits model delivery', () => {
    const bare = { runtime: { http: { routes: {} } } } as unknown as ProductSchema;
    expect(modelFormatsRoute(bare, 't')).toBeNull();
    expect(modelArchiveRoute(bare, 't')).toBeNull();
  });
});

describe('deliveryFormatRows', () => {
  const listed = ['glb', 'gltf', 'fbx', 'obj', 'stl', 'usdz', '3mf'].map((id) => ({ id, label: id.toUpperCase() }));
  const texts = { notReady: 'not ready', notListed: 'not listed', loading: 'loading', failed: 'failed' };

  it('always lists every Schema format, taking each state from the API', () => {
    const coverage = parseModelFormatCoverage(wire)!;
    const rows = deliveryFormatRows(listed, coverage, 'ready', texts);
    expect(rows.map((row) => row.id)).toEqual(listed.map((format) => format.id));
    expect(rows.find((row) => row.id === 'glb')?.status).toBe('available');
    expect(rows.find((row) => row.id === 'fbx')).toMatchObject({ status: 'obtainable', providerName: 'Tripo' });
    // reported unavailable keeps the server reason; an unreported one explains itself
    expect(rows.find((row) => row.id === 'gltf')?.reasonText).toBe('没有适用的转换服务。');
    expect(rows.find((row) => row.id === 'usdz')).toMatchObject({ status: 'unavailable', reasonText: 'not listed' });
  });

  it('renders all seven rows when the API says the task has no list, or has not answered', () => {
    const empty = parseModelFormatCoverage({ task_id: 't', applicable: false, formats: [], steps: [] });
    const rows = deliveryFormatRows(listed, empty, 'ready', texts);
    expect(rows).toHaveLength(7);
    expect(rows.every((row) => row.status === 'unavailable' && row.reasonText === 'not ready')).toBe(true);
    expect(deliveryFormatRows(listed, null, 'loading', texts).every((row) => row.status === 'unknown' && row.reasonText === 'loading')).toBe(true);
    expect(deliveryFormatRows(listed, null, 'failed', texts)[0].reasonText).toBe('failed');
  });

  it('appends formats only the API knows, after the Schema ones', () => {
    const coverage = parseModelFormatCoverage({ task_id: 't', applicable: true, formats: [{ id: 'ply', label: 'PLY', status: 'available' }], steps: [] })!;
    const rows = deliveryFormatRows(listed.slice(0, 2), coverage, 'ready', texts);
    expect(rows.map((row) => row.id)).toEqual(['glb', 'gltf', 'ply']);
  });
});

describe('formatsFooter', () => {
  const rowsOf = (...statuses: string[]) => statuses.map((status, index) => ({ id: String(index), label: '', status, taskId: '', assetId: '', providerName: '', estimatedCredits: 0, reasonText: '' })) as ReturnType<typeof deliveryFormatRows>;
  const cov = (extra: Record<string, unknown>) => parseModelFormatCoverage({ task_id: 't', applicable: true, formats: [], steps: [], ...extra });
  it('picks the action or the reason there is none', () => {
    expect(formatsFooter(null, [])).toBe('none');
    expect(formatsFooter(cov({ applicable: false }), [])).toBe('not_ready');
    expect(formatsFooter(cov({ can_complete: true }), rowsOf('obtainable'))).toBe('complete');
    expect(formatsFooter(cov({ steps: [{ state: 'RUNNING' }] }), rowsOf('converting'))).toBe('converting');
    expect(formatsFooter(cov({}), rowsOf('available', 'available'))).toBe('all_present');
    expect(formatsFooter(cov({}), rowsOf('available', 'unavailable'))).toBe('no_route');
  });
});

describe('the row of one obtainable format (#323)', () => {
  const coverageOf = (formats: unknown[], steps: unknown[]) => parseModelFormatCoverage({ task_id: 't', applicable: true, formats, steps })!;
  const local = { provider_id: 'local', provider_name: '格式转换', provider_short_label: '本地', estimated_credits: 0, credits_metered: false };
  const meshy = { provider_id: 'meshy', provider_name: 'Meshy AI', provider_short_label: 'MESHY', estimated_credits: 5, credits_metered: true };

  it('names a local conversion by its short label and an outside provider by its name', () => {
    const coverage = coverageOf([
      { id: 'gltf', label: 'glTF', status: 'obtainable', conversion: local },
      { id: 'fbx', label: 'FBX', status: 'obtainable', conversion: meshy },
    ], []);
    expect(coverage.formats[0]).toMatchObject({ providerId: 'local', providerShortLabel: '本地' });
    expect(conversionProviderLabel(coverage.formats[0])).toBe('本地');
    expect(conversionProviderLabel(coverage.formats[1])).toBe('Meshy AI');
    // A local Offering without a short label still says something.
    expect(conversionProviderLabel({ ...coverage.formats[0], providerShortLabel: '' })).toBe('格式转换');
  });

  it('calls a conversion free only when nothing is metered or quoted', () => {
    const rowsOf = (conversion: Record<string, unknown>) => coverageOf([{ id: 'x', label: 'X', status: 'obtainable', conversion }], []).formats[0];
    expect(conversionIsFree(rowsOf(local))).toBe(true);
    expect(conversionIsFree(rowsOf(meshy))).toBe(false);
    expect(conversionIsFree(rowsOf({ ...local, estimated_credits: 3 }))).toBe(false);
    const quote = (total: string) => ({ version: 1, unit: 'usd', unit_label: 'USD', base_amount: total, extra_amount: '0', total_amount: total });
    expect(conversionIsFree(rowsOf({ ...local, native_quote: quote('0') }))).toBe(true);
    expect(conversionIsFree(rowsOf({ ...local, native_quote: quote('0.5') }))).toBe(false);
    // An unreadable quote is not free.
    expect(conversionIsFree(rowsOf({ ...local, native_quote: { version: 9 } }))).toBe(false);
  });

  it('starts the step that delivers the row, listing every format that step converts', () => {
    const coverage = coverageOf([
      { id: 'gltf', label: 'glTF', status: 'obtainable', conversion: local },
      { id: 'fbx', label: 'FBX', status: 'obtainable', conversion: meshy },
      { id: 'obj', label: 'OBJ', status: 'obtainable', conversion: meshy },
    ], [
      { offering_id: 'local/generated-glb/to-gltf', formats: ['gltf'] },
      { offering_id: 'meshy/processing/remesh', formats: ['fbx', 'obj'], estimated_credits: 5 },
    ]);
    expect(formatRowAction(coverage, coverage.formats[0])).toMatchObject({ kind: 'start', formats: ['gltf'] });
    expect(formatRowAction(coverage, coverage.formats[2])).toMatchObject({ kind: 'start', formats: ['fbx', 'obj'] });
  });

  it('retries the failed plan of the row instead of starting a new one', () => {
    const coverage = coverageOf([{ id: 'gltf', label: 'glTF', status: 'obtainable', conversion: local }], [
      { offering_id: 'o', formats: ['gltf'], state: 'FAILED', plan_id: 'p1', plan_version: 2, offering_revision: 'r' },
    ]);
    expect(formatRowAction(coverage, coverage.formats[0])).toMatchObject({ kind: 'retry', step: { planId: 'p1', planVersion: 2 } });
  });

  it('has no row action while the step runs, waits, or for rows that are not obtainable', () => {
    for (const state of ['PENDING', 'RUNNING', 'PAUSED', 'SUCCEEDED']) {
      const coverage = coverageOf([{ id: 'gltf', label: 'glTF', status: 'obtainable', conversion: local }], [{ formats: ['gltf'], state, plan_id: 'p' }]);
      expect(formatRowAction(coverage, coverage.formats[0])).toBeNull();
    }
    const coverage = coverageOf([
      { id: 'glb', label: 'GLB', status: 'available' },
      { id: 'gltf', label: 'glTF', status: 'converting', conversion: local },
      { id: 'fbx', label: 'FBX', status: 'obtainable', conversion: meshy },
    ], [{ formats: ['gltf'] }]);
    expect(formatRowAction(coverage, coverage.formats[0])).toBeNull();
    expect(formatRowAction(coverage, coverage.formats[1])).toBeNull();
    // An obtainable row no step names: nothing to start from it.
    expect(formatRowAction(coverage, coverage.formats[2])).toBeNull();
  });

  it('asks the completion route for that format only', () => {
    expect(JSON.parse(modelFormatsCompleteBody(['gltf']))).toEqual({ formats: ['gltf'] });
  });
});

// #324 (from !318): with only free steps left the 补齐格式 button said "about 0 credits" while every
// row said 免费. The button is free exactly when every format it would obtain is free.
describe('the completion button price (#324)', () => {
  const coverageOf = (formats: unknown[]) => parseModelFormatCoverage({ task_id: 't', applicable: true, can_complete: true, formats, steps: [] })!;
  const local = { provider_id: 'local', provider_name: '格式转换', estimated_credits: 0, credits_metered: false };
  const meshy = { provider_id: 'meshy', provider_name: 'Meshy AI', estimated_credits: 5, credits_metered: true };

  it('is free when only free conversions remain', () => {
    expect(completionIsFree(coverageOf([
      { id: 'glb', label: 'GLB', status: 'available' },
      { id: 'gltf', label: 'glTF', status: 'obtainable', conversion: local },
      { id: 'stl', label: 'STL', status: 'unavailable' },
    ]))).toBe(true);
  });

  it('is not free when one remaining conversion is metered, or when nothing remains', () => {
    expect(completionIsFree(coverageOf([
      { id: 'gltf', label: 'glTF', status: 'obtainable', conversion: local },
      { id: 'fbx', label: 'FBX', status: 'obtainable', conversion: meshy },
    ]))).toBe(false);
    expect(completionIsFree(coverageOf([{ id: 'glb', label: 'GLB', status: 'available' }]))).toBe(false);
  });
});
