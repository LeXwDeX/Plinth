import React from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import catalog from '../../../internal/catalog/default_catalog.json';
import { parseLineage } from '@/lib/workspace/lineage';
import { blankTile, type VersionTile } from '@/lib/workspace/versions';
import type { OfferingWire } from '@/lib/agent-offerings';
import type { HarnessSource, HarnessSourceView } from '@/lib/harness';
import { uploadModelInputSource } from '@/lib/workspace/model-input-upload';
import type { ProductSchema } from '@/lib/product-schema';
const mock = vi.hoisted(() => ({ schema: null as unknown as ProductSchema, assets: [] as Array<{ id: string; original_filename: string; media_type?: string }>, tiles: [] as VersionTile[], controls: new Map<string, (value: string) => void>(), onChange: vi.fn() }));
vi.mock('@/components/product-schema-provider', () => ({ useProductSchema: () => ({ schema: mock.schema, text: (key: string) => key === 'ui.harness.upload.limits' ? mock.schema.content.messages[key] : key }) }));
vi.mock('@/hooks/use-session-versions', () => ({ useSessionVersions: () => ({ tiles: mock.tiles, isLoading: false }) }));
vi.mock('@/hooks/use-owned-model-sources', () => ({ useOwnedModelSources: () => ({ assets: mock.assets, page: 1, hasNext: false, isLoading: false, refresh: vi.fn() }) }));
vi.mock('@/store/chat-store', () => ({ useChatStore: (select: (state: unknown) => unknown) => select({ activeSessionRevision: 1 }) }));
vi.mock('@/components/ui/option-select', () => ({ OptionSelect: ({ id, options, value, onValueChange, disabled }: { id: string; options: Array<{ value: string; label: string }>; value: string; onValueChange: (value: string) => void; disabled: boolean }) => { mock.controls.set(id.split('-').slice(-1)[0], onValueChange); return <select id={id} value={value} disabled={disabled} onChange={() => {}}>{options.map(option => <option key={option.value} value={option.value}>{option.label}</option>)}</select>; } }));
import { HarnessModelSourcePicker, harnessUploadedParameters, harnessParameterProfile, harnessModelUploadSchema, harnessModelPreviewURL } from './harness-model-source-picker';
const offering: OfferingWire = { id: 'o', revision: '1', provider_id: 'p', provider_name: 'Service', model_id: 'm', model_name: 'Model', capability: 'test', available: true, parameters: [{ name: 'task', label: 'Task', type: 'task', semantic: 'source-task' }, { name: 'bytes', label: 'Bytes', type: 'asset', semantic: 'source-asset', uploaded_model: { profile_id: 'custom-profile', requirements: '必须已带纹理且为朝向 +Z 的人形' } }] };
const render = (source?: HarnessSource, views: HarnessSourceView[] = [], item = offering) => renderToStaticMarkup(<HarnessModelSourcePicker ownerId="owner" sessionId="session" object={null} offering={item} source={source} sourceViews={views} unavailableReason="公开的不支持原因" disabled={false} onChange={mock.onChange} />);
beforeEach(() => { mock.schema = structuredClone(catalog.application) as unknown as ProductSchema; mock.schema.runtime.input_assets.model_profile = { id: 'custom-profile', revision: '1', asset_type: 'model', label: '上传模型', description: '公开格式说明', extensions: ['.custom'], media_types: ['model/custom'], upload_media_type: 'model/custom', limits: { max_bytes: 20 } } as NonNullable<ProductSchema['runtime']['input_assets']['model_profile']>; mock.schema.chat.harness!.source_kinds = [{ id: 'task_asset', label: '生成结果', description: '现有任务' }, { id: 'input_asset', label: '上传模型', description: '已有文件' }]; mock.assets = []; mock.tiles = []; mock.controls.clear(); mock.onChange.mockReset(); });
describe('Harness uploaded model source picker', () => {
  it('derives support from the public source parameter and profile, never from Provider or operation IDs', () => {
    expect(harnessUploadedParameters(offering, mock.schema.runtime.input_assets.model_profile).map(parameter => parameter.name)).toEqual(['bytes']);
    expect(harnessUploadedParameters({ ...offering, parameters: offering.parameters.map(parameter => ({ ...parameter, uploaded_model: { profile_id: 'other' } })) }, mock.schema.runtime.input_assets.model_profile)).toEqual([]);
  });
  it('restores only the exact saved input filename and shows Schema conditions without a proof checkbox', () => {
    const html = render({ kind: 'input_asset', parameter: 'bytes', asset_id: 'chosen' }, [{ kind: 'input_asset', asset_id: 'chosen', original_filename: '角色.custom', size: 11, media_type: 'model/custom', state: 'ready' }, { kind: 'input_asset', asset_id: 'other', original_filename: '别人的旧标签.custom', size: 1, media_type: 'model/custom', state: 'ready' }]);
    expect(html).toContain('角色.custom'); expect(html).not.toContain('别人的旧标签'); expect(html).toContain('必须已带纹理且为朝向 +Z 的人形'); expect(html).toContain('公开格式说明'); expect(html).not.toContain('type="checkbox"'); expect(mock.onChange).not.toHaveBeenCalled();
  });
  it('selects an owned upload as an asset-only draft payload without saving or creating work', () => {
    mock.assets = [{ id: 'owned', original_filename: 'owned.custom' }];
    render({ kind: 'input_asset', parameter: 'bytes', asset_id: 'old' });
    mock.controls.get('uploaded')!('owned');
    expect(mock.onChange).toHaveBeenCalledExactlyOnceWith([{ kind: 'input_asset', parameter: 'bytes', asset_id: 'owned' }]);
  });
  it('retains an unavailable uploaded selection and explains why the Offering does not accept it', () => {
    const html = render({ kind: 'input_asset', parameter: 'bytes', asset_id: 'missing' }, [{ kind: 'input_asset', asset_id: 'missing', original_filename: 'missing.custom', size: 1, media_type: 'model/custom', state: 'unavailable', reason: '文件已不可用' }], { ...offering, parameters: offering.parameters.map(parameter => ({ ...parameter, uploaded_model: undefined })) });
    expect(html).toContain('missing.custom'); expect(html).toContain('文件已不可用'); expect(html).toContain('公开的不支持原因'); expect(html).not.toContain('type="file"'); expect(mock.onChange).not.toHaveBeenCalled();
  });
  it('never substitutes an unrelated filename for a missing saved asset', () => {
    mock.assets = [{ id: 'other', original_filename: 'other.custom' }];
    const html = render({ kind: 'input_asset', parameter: 'bytes', asset_id: 'missing' });
    expect(html).toContain('ui.harness.source_unavailable'); expect(html).not.toContain('missing.custom'); expect(mock.onChange).not.toHaveBeenCalled();
  });
});


it('uses the selected parameter frozen profile limits and revision after global profile changes', () => {
  const global = mock.schema.runtime.input_assets.model_profile!;
  const parameter = { ...offering.parameters[1], uploaded_model: { ...offering.parameters[1].uploaded_model!, profile_revision: 'frozen-v1', profile_limits: { ...global.limits, max_bytes: 8 } } };
  expect(harnessParameterProfile({ ...global, revision: 'current-v9', limits: { ...global.limits, max_bytes: 100 } }, parameter)).toMatchObject({ revision: 'frozen-v1', limits: { max_bytes: 8 } });
  expect(harnessParameterProfile(global, { ...parameter, uploaded_model: { profile_id: global.id } })).toBe(global);
});
it('shows the parameter budget instead of the current global budget while preserving requirements', () => {
  mock.schema.content.messages['ui.harness.upload.limits'] = 'Frozen maximum {size}';
  const global = mock.schema.runtime.input_assets.model_profile!;
  const parameter = { ...offering.parameters[1], uploaded_model: { ...offering.parameters[1].uploaded_model!, profile_revision: 'frozen', profile_limits: { ...global.limits, max_bytes: 2 * 1024 * 1024 } } };
  mock.schema.runtime.input_assets.upload_max_bytes = 20 * 1024 * 1024;
  global.limits.max_bytes = 10 * 1024 * 1024;
  const html = render({ kind: 'input_asset', parameter: parameter.name, asset_id: 'old' }, [], { ...offering, parameters: [parameter] });
  expect(html).toContain('Frozen maximum 2.09 MB'); expect(html).not.toContain('Frozen maximum 10.48 MB'); expect(html).toContain(parameter.uploaded_model.requirements);
});


it.each(['parameter', 'current'])('rejects a new upload against the tighter %s policy before any native request', async tighter => {
  const profile = mock.schema.runtime.input_assets.model_profile!;
  profile.limits.max_bytes = tighter === 'current' ? 2 : 10;
  mock.schema.runtime.input_assets.upload_max_bytes = 20;
  const parameter = { ...offering.parameters[1], uploaded_model: { ...offering.parameters[1].uploaded_model!, profile_revision: 'frozen', profile_limits: { ...profile.limits, max_bytes: tighter === 'parameter' ? 2 : 10 } } };
  const fetcher = vi.fn();
  await expect(uploadModelInputSource(harnessModelUploadSchema(mock.schema, parameter), new File(['123'], 'small.custom'), parameter.name, { isCurrent: () => true }, fetcher)).rejects.toMatchObject({ code: 'size' });
  expect(fetcher).not.toHaveBeenCalled(); expect(mock.onChange).not.toHaveBeenCalled();
});


it('never offers a report-only fake model tile as a Task source, while retaining model+report', () => {
  const report = { id: 'report', role: 'report', variant: 'json', ordinal: 0, mediaType: 'application/vnd.art-ai.print-report+json', url: '/api/report', kind: 'other' as const };
  mock.tiles = [blankTile({ key: 'report-only', taskId: 'report-only', domain: 'model', status: 'ready', assets: [report] }), blankTile({ key: 'real-model', taskId: 'real-model', domain: 'model', status: 'ready', assets: [{ ...report, id: 'model', role: 'model', mediaType: 'model/gltf-binary', kind: 'model' },report] })];
  const html = render();
  expect(html).not.toContain('value="report-only"'); expect(html).toContain('value="real-model"'); expect(mock.onChange).not.toHaveBeenCalled();
});


it.each([['clip','model/fbx'],['print-archive','model/3mf']])('rejects the projected %s attachment as a model Task source', (role, media_type) => {
  const asset = parseLineage({ nodes: [{ id: 'file-task', kind: 'task', task_id: 'file-task', assets: [{ id: 'file', role, media_type, url: '/api/file' }] }], edges: [] }).nodes[0].assets[0];
  mock.tiles = [blankTile({ key: 'old-file-tile', taskId: 'file-task', domain: 'model', status: 'ready', assets: [asset] })];
  expect(render()).not.toContain('value="file-task"'); expect(mock.onChange).not.toHaveBeenCalled();
});


describe('Uploaded model preview', () => {
  const source: HarnessSource = { kind: 'input_asset', parameter: 'bytes', asset_id: 'owned/file' };
  const view: HarnessSourceView = { kind: 'input_asset', asset_id: 'owned/file', original_filename: 'full model name.custom', media_type: 'model/custom', size: 5, state: 'ready', profile_id: 'custom-profile' };
  it('uses the current Schema route and MIME policy with encoded owner asset identity', () => {
    mock.schema.runtime.http.routes.asset_model_preview = '/custom/{assetID}/model';
    mock.schema.runtime.input_assets.model_profile!.preview_media_types = ['model/custom'];
    expect(harnessModelPreviewURL(mock.schema, source, view)).toBe('/custom/owned%2Ffile/model');
    expect(harnessModelPreviewURL(mock.schema, source, { ...view, media_type: 'MODEL/CUSTOM; parameter=value' })).toBe('/custom/owned%2Ffile/model');
    const html = render(source, [view]);
    expect(html).toMatch(/data-testid="harness-model-preview-open"/);
    expect(html).not.toContain('ui.harness.upload.preview_unavailable');
    expect(html).not.toContain('<model-viewer');
    expect(mock.onChange).not.toHaveBeenCalled();
  });
  it('honors an unavailable saved view over an otherwise previewable owned list entry', () => {
    mock.schema.runtime.input_assets.model_profile!.preview_media_types = ['model/custom'];
    mock.assets = [{ id: source.asset_id!, original_filename: view.original_filename, media_type: view.media_type }];
    const html = render(source, [{ ...view, state: 'unavailable', reason: '文件不可用' }]);
    expect(html).toContain('ui.harness.upload.preview_unavailable');
    expect(html.match(/<button[^>]*data-testid="harness-model-preview-open"[^>]*>/)?.[0]).toContain('disabled');
    expect(mock.onChange).not.toHaveBeenCalled();
  });
  it('does not fall back to general download when the preview route or MIME support is withdrawn', () => {
    mock.schema.runtime.input_assets.model_profile!.preview_media_types = ['model/custom'];
    mock.schema.runtime.http.routes.asset_model_preview = '';
    expect(harnessModelPreviewURL(mock.schema, source, view)).toBe('');
    mock.schema.runtime.http.routes.asset_model_preview = '/preview/{assetID}';
    mock.schema.runtime.input_assets.model_profile!.preview_media_types = [];
    expect(harnessModelPreviewURL(mock.schema, source, view)).toBe('');
    delete mock.schema.runtime.input_assets.model_profile!.preview_media_types;
    expect(harnessModelPreviewURL(mock.schema, source, view)).toBe('');
  });
  it('rejects unavailable, missing, foreign-profile and mismatched asset metadata', () => {
    mock.schema.runtime.input_assets.model_profile!.preview_media_types = ['model/custom'];
    for (const candidate of [undefined, { ...view, state: 'unavailable' as const }, { ...view, profile_id: 'old' }, { ...view, asset_id: 'other' }, { ...view, media_type: 'text/html' }]) expect(harnessModelPreviewURL(mock.schema, source, candidate)).toBe('');
    expect(harnessModelPreviewURL(mock.schema, { ...source, kind: 'task_asset' }, view)).toBe('');
    mock.schema.runtime.http.routes.asset_model_preview = 'https://upstream.invalid/model/{assetID}';
    expect(harnessModelPreviewURL(mock.schema, source, view)).toBe('');
  });
});
