import React from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import { ApprovalCard } from '@/components/studio/approval-card';
import { cardFromConsent, cardFromPending, type ApprovalCardModel } from '@/lib/chat/approval-card';
import { parsePendingApprovals } from '@/lib/chat/pending-approvals';
import type { ProductSchema } from '@/lib/product-schema';

const messages = (defaultCatalog.application as { content: { messages: Record<string, string> } }).content.messages;

const schemaState = vi.hoisted(() => ({ application: null as unknown }));
vi.mock('@/components/product-schema-provider', () => ({
  useProductSchema: () => {
    const application = schemaState.application as { content: { messages: Record<string, string> } };
    return { schema: application, text: (key: string) => application.content.messages[key] ?? '' };
  },
}));
// The card reads its Offering through SWR, which does not fetch while rendering to a string.
const swr = vi.hoisted(() => ({ offerings: undefined as unknown, loading: false }));
vi.mock('swr', () => ({ default: () => ({ data: swr.offerings, isLoading: swr.loading }) }));
vi.mock('@/hooks/use-balance', () => ({ useBalance: () => ({ accounts: [] }) }));
// The session's pictures by handle, as the chat column knows them.
const handles = vi.hoisted(() => ({ map: new Map<string, { handle: string; url: string; assetId?: string }>() }));
vi.mock('@/hooks/use-handle-references', () => ({ useHandleReferences: () => handles.map }));

const card = (extra: ApprovalCardModel['extra']): ApprovalCardModel => ({
  requestId: 'r1', source: 'detached', status: 'pending', capability: 'text-to-3d', model: 'tripo-v3.1-full', provider: 'tripo', providerId: 'tripo',
  credits: extra ? extra.baseCredits + extra.credits : 20, extra, prompt: '做一个简单的木凳', inputs: [], sourceTaskId: '', expiresAt: '2099-01-01T00:00:00Z',
});

const render = (model: ApprovalCardModel) => renderToStaticMarkup(
  <ApprovalCard model={model} busy={false} now={Date.parse('2026-10-09T00:00:00Z')} onDecide={() => {}} onRerequest={() => {}} />,
);

beforeEach(() => { schemaState.application = defaultCatalog.application; swr.offerings = undefined; swr.loading = false; handles.map = new Map(); });

function englishApplication() {
  const application = defaultCatalog.application;
  const bundle = application.localization.bundles.en;
  return {
    ...application,
    localization: { ...application.localization, active_locale: 'en' },
    content: { ...application.content, messages: { ...application.content.messages, ...bundle.messages } },
  } as unknown as typeof defaultCatalog.application;
}

const offering = (providerId: string, modelId: string, metered: boolean) => ({
  provider_id: providerId, model_id: modelId, provider_name: providerId, model_name: modelId, credits_metered: metered,
});

describe('approval card', () => {
  it('states the base credits, the extra credits and what they are for, the total, and both buttons', () => {
    const html = render(card({ credits: 30, baseCredits: 20, label: '附加导出格式', items: ['FBX', 'USDZ'] }));
    expect(html).toContain('附加导出格式：FBX、USDZ，约 30 积分');
    expect(html).toMatch(/data-testid="studio-approval-base-credits"[^>]*>约 20 积分/);
    expect(html).toMatch(/data-testid="studio-approval-credits"[^>]*>约 50 积分/);
    expect(html).toContain('data-testid="studio-approval-approve"');
    expect(html).toContain('data-testid="studio-approval-deny"');
    expect(html).toContain('>批准<');
    expect(html).toContain('>不批准<');
    expect(html).toContain(messages['ui.studio.approval.card.extra_hint']);
    expect(html).not.toContain('同意额外积分消耗');
  });

  it('uses the English singular validity for one hour and one minute (#363)', () => {
    const english = structuredClone(defaultCatalog.application) as unknown as ProductSchema;
    english.localization = { ...english.localization!, active_locale: 'en' };
    english.content.messages = {
      ...english.content.messages,
      'ui.studio.approval.card.valid_hours': 'Awaiting approval · valid for {hours} hours',
      'ui.studio.approval.card.valid_hours_one': 'Awaiting approval · valid for {hours} hour',
      'ui.studio.approval.card.valid_minutes': 'Awaiting approval · valid for {minutes} minutes',
      'ui.studio.approval.card.valid_minutes_one': 'Awaiting approval · valid for {minutes} minute',
    };
    schemaState.application = english;
    const now = Date.parse('2026-10-09T00:00:00Z');

    const hours = render({ ...card(null), expiresAt: new Date(now + 3600_000).toISOString() });
    expect(hours).toContain('Awaiting approval · valid for 1 hour');
    expect(hours).not.toContain('1 hours');

    const minutes = render({ ...card(null), expiresAt: new Date(now + 60_000).toISOString() });
    expect(minutes).toContain('Awaiting approval · valid for 1 minute');
    expect(minutes).not.toContain('1 minutes');
  });

  it('says who chose the model and why, with the 2D purpose label (#239)', () => {
    const html = render({ ...card(null), modelChoice: { source: 'profile', profile: 'illustration', reason: '立绘需要竖画幅和稳定身份。' } });
    expect(html).toContain('data-testid="studio-approval-model-choice"');
    expect(html).toContain('角色立绘 / 宣传插画');
    expect(html).toContain('立绘需要竖画幅和稳定身份。');
    expect(html).not.toContain('illustration');
  });

  it('shows a user lock without inventing a reason', () => {
    const html = render({ ...card(null), modelChoice: { source: 'user' } });
    expect(html).toContain(messages['ui.studio.approval.card.model_source_user']);
    expect(html).not.toContain(messages['ui.studio.approval.card.model_choice_line']);
  });

  it('parses the model choice from a detached pending approval', () => {
    const [pending] = parsePendingApprovals({ items: [{ request_id: 'r-choice', status: 'pending', capability: 'text-to-image', model: 'nano-banana-pro', provider: 'Meshy AI',
      model_choice: { source: 'profile', profile: 'illustration', reason: 'frozen reason' } }] });
    const model = cardFromPending(pending);
    expect(model.modelChoice).toEqual({ source: 'profile', profile: 'illustration', reason: 'frozen reason' });
    expect(render(model)).toContain('frozen reason');
  });

  it('keeps the plain card when the step has no extra credits', () => {
    const html = render(card(null));
    expect(html).toContain('data-testid="studio-approval-credits"');
    expect(html).not.toContain('studio-approval-extra-credits');
    expect(html).not.toContain('studio-approval-extra-hint');
    expect(html).toContain('>批准<');
    expect(html).toContain('>不批准<');
  });

  it('shows a frozen native quote and keeps approval available when the matching balance is unknown', () => {
    const quote = {
      version: 1 as const,
      unit: 'hyper3d.credit',
      unit_label: 'Hyper3D credits',
      base_amount: '0.5',
      extra_amount: '2.5',
      total_amount: '3',
      components: [{ id: 'extreme_texture', label: 'Extreme texture', amount: '2.5' }],
    };
    const html = render({ ...card(null), priceMode: 'native', nativeQuote: quote });
    expect(html).toContain('3 Hyper3D credits');
    expect(html).toContain('0.5 Hyper3D credits');
    expect(html).toContain('2.5 Hyper3D credits');
    expect(html).toContain(messages['ui.pricing.balance_unknown']);
  });

  it('shows an unknown native quote and blocks approval instead of calling it free', () => {
    const html = render({ ...card(null), priceMode: 'native', nativeQuote: null, credits: 0 });
    expect(html).toContain(messages['ui.pricing.unknown']);
    expect(html).toContain(`已冻结估算：${messages['ui.pricing.unknown']}`);
    expect(html).not.toContain('{amount}');
    expect(html).not.toContain('>0</');
  });

  it('words an extra without a listed item by its label alone', () => {
    const html = render(card({ credits: 5, baseCredits: 10, label: '附加选项', items: [] }));
    expect(html).toContain('附加选项，约 5 积分');
  });

  it('shows only re-request and drop once expired, never the extra hint', () => {
    const html = render({ ...card({ credits: 30, baseCredits: 20, label: '附加导出格式', items: ['FBX'] }), status: 'expired' });
    expect(html).toContain('data-testid="studio-approval-rerequest"');
    expect(html).not.toContain('studio-approval-approve');
    expect(html).not.toContain('studio-approval-extra-hint');
  });

  it('shows credits for a credits-metered provider (Meshy and Tripo)', () => {
    for (const provider of ['tripo', 'meshy']) {
      swr.offerings = [offering(provider, 'm1', true)];
      const html = render({ ...card({ credits: 30, baseCredits: 20, label: '附加导出格式', items: ['FBX'] }), provider, providerId: provider, model: 'm1' });
      expect(html).toMatch(/data-testid="studio-approval-credits"[^>]*>约 50 积分/);
      expect(html).toMatch(/data-testid="studio-approval-base-credits"[^>]*>约 20 积分/);
      expect(html).toContain('附加导出格式：FBX，约 30 积分');
    }
  });

  it('shows "/" for base, extra and total credits of a provider that does not bill in credits, and still has both buttons', () => {
    swr.offerings = [offering('other', 'm2', false)];
    const html = render({ ...card({ credits: 30, baseCredits: 20, label: '附加导出格式', items: ['FBX'] }), provider: 'other', providerId: 'other', model: 'm2' });
    expect(html).toMatch(/data-testid="studio-approval-base-credits"[^>]*>\/</);
    expect(html).toMatch(/data-testid="studio-approval-extra-credits"[^>]*>\/</);
    expect(html).toMatch(/data-testid="studio-approval-credits"[^>]*>\/</);
    expect(html).not.toMatch(/约 \d+ 积分/);
    expect(html).toContain('data-testid="studio-approval-approve"');
    expect(html).toContain('data-testid="studio-approval-deny"');
  });

  it('shows "/" on the plain card of a provider that does not bill in credits, even without a number', () => {
    swr.offerings = [offering('other', 'm2', false)];
    const html = render({ ...card(null), credits: null, provider: 'other', providerId: 'other', model: 'm2' });
    expect(html).toMatch(/data-testid="studio-approval-credits"[^>]*>\/</);
  });

  it('holds a neutral placeholder in every credit cell while the Offering list is still being read', () => {
    swr.loading = true;
    const html = render({ ...card({ credits: 30, baseCredits: 20, label: '附加导出格式', items: ['FBX'] }), provider: 'other', providerId: 'other', model: 'm2' });
    for (const cell of ['studio-approval-base-credits', 'studio-approval-extra-credits', 'studio-approval-credits']) {
      expect(html).toMatch(new RegExp(`data-testid="${cell}"[^>]*>${messages['ui.loading']}<`));
    }
    expect(html).not.toMatch(/约 \d+ 积分/);
    expect(html).not.toMatch(/data-testid="studio-approval-[a-z-]*credits"[^>]*>\/</);
    expect(html).not.toContain('studio-approval-extra-hint');
    expect(html).not.toContain('studio-approval-balance-low');
    expect(html).toContain('data-testid="studio-approval-approve"');
  });

  it('says a step that repeats a charged attempt whose result was not stored charges again (#294)', () => {
    const html = render({ ...card(null), rechargeTaskId: 'task_old' });
    expect(html).toContain('data-testid="studio-approval-recharge"');
    expect(html).toContain(messages['ui.studio.approval.card.recharge_notice']);
    expect(html).toContain('data-testid="studio-approval-approve"');
    expect(render(card(null))).not.toContain('studio-approval-recharge');
    expect(render({ ...card(null), rechargeTaskId: 'task_old', status: 'expired' })).not.toContain('studio-approval-recharge');
  });

  it('says the step uses only the first of the pictures given, in the server sentence (#329 E)', () => {
    const note = 'Meshy 7.1 一次只用 1 张参考图：这次共 2 张，只用第 1 张。要用多张不同角度的图，请改选支持多图的模型。';
    const html = render({ ...card(null), capability: 'image-to-3d', referenceNote: note });
    expect(html).toContain('data-testid="studio-approval-reference-note"');
    expect(html).toContain(note);
    // #94 review: the same field also carries the plain source note of a step that uses every picture (which views a
    // multiview step is made from), so it is a note, never an "unavailable" warning.
    expect(html).not.toContain('data-testid="unavailable-icon"');
    const views = render({ ...card(null), capability: 'multiview-to-3d', referenceNote: '由 正面 / 左侧 视图生成' });
    expect(views).toContain('由 正面 / 左侧 视图生成');
    expect(views).not.toContain('unavailable-label');
    expect(render(card(null))).not.toContain('studio-approval-reference-note');
    // An expired card keeps saying which picture the step would have used.
    expect(render({ ...card(null), referenceNote: note, status: 'expired' })).toContain('studio-approval-reference-note');
  });

  it('shows the mask input note of a local edit (#244)', () => {
    const note = '本次只改用户圈选的区域：蒙版透明处会重画，不透明处保持原图。';
    const html = render({ ...card(null), maskNote: note, inputs: [
      { parameter: 'mask', handle: '', taskId: '', assetId: 'mask-asset', kind: 'asset', label: '', previewable: true },
    ] });
    expect(html).toContain('data-testid="studio-approval-mask-note"');
    expect(html).toContain(note);
    // The mask thumbnail sits on a checkerboard so its transparent shape is visible.
    expect(html).toContain('data-checkerboard="true"');
    expect(render(card(null))).not.toContain('studio-approval-mask-note');
    expect(render({ ...card(null), maskNote: note, status: 'expired' })).toContain('studio-approval-mask-note');
  });

  it('words the remaining approval time with the count pair (#363)', () => {
    const now = Date.parse('2026-10-09T00:00:00Z');
    const validity = (milliseconds: number) => render({
      ...card(null),
      expiresAt: new Date(now + milliseconds).toISOString(),
    });
    expect(validity(60 * 60_000)).toContain('等待批准 · 1 小时内有效');
    expect(validity(2 * 60 * 60_000)).toContain('等待批准 · 2 小时内有效');
    expect(validity(60_000)).toContain('等待批准 · 1 分钟内有效');

    schemaState.application = englishApplication();
    expect(validity(60 * 60_000)).toContain('Awaiting approval · valid for 1 hour');
    expect(validity(2 * 60 * 60_000)).toContain('Awaiting approval · valid for 2 hours');
    expect(validity(60_000)).toContain('Awaiting approval · valid for 1 minute');
    expect(validity(2 * 60_000)).toContain('Awaiting approval · valid for 2 minutes');
  });

  it('says whether the step is 2D or 3D, from its capability domain', () => {
    expect(render(card(null))).toMatch(/data-testid="studio-approval-kind"[^>]*>3D</);
    expect(render({ ...card(null), capability: 'text-to-image' })).toMatch(/data-testid="studio-approval-kind"[^>]*>2D</);
    expect(render({ ...card(null), capability: 'remesh' })).toMatch(/data-testid="studio-approval-kind"[^>]*>3D</);
  });
});

it('shows frozen advised adjustments as one compact Agent line without changing the quoted credit total', () => {
 const model={...card(null),parameterAdjustments:[{name:'quality',label:'Frozen quality',recommendedValue:'extreme',value:'detailed',reason:'Frozen reason for this step',advice:'Frozen not-recommended advice',source:'agent' as const}]};
 const html=render(model);
 expect(html).toContain('studio-approval-parameter-adjustments');
 expect(html).toContain('Frozen quality：extreme → detailed');
 expect(html).toContain('Frozen reason for this step');
 expect(html).toContain('展开 1 项参数');
 expect(html).not.toContain('studio-approval-parameter-table');
 expect(html).not.toContain('Frozen not-recommended advice');
 expect(html).toContain('20');
});

describe('approval card multiview inputs', () => {
  it('captions each reference picture with its view next to the cost and the note', () => {
    const [pending] = parsePendingApprovals({ items: [{ request_id: 'rv', status: 'pending', capability: 'image-to-3d', model: 'm', provider: 'p', estimated_credits: 40, reference_note: '由 正面 / 左侧 视图生成', inputs: [
      { parameter: 'reference_assets', kind: 'asset', role: 'image', asset_id: 'a1', media_type: 'image/png', view: 'front', view_label: '正面' },
      { parameter: 'reference_assets', kind: 'asset', role: 'image', asset_id: 'a2', media_type: 'image/png', view: 'left', view_label: '左侧' },
    ] }] });
    const html = render(cardFromPending(pending));
    expect(html).toContain('>正面视图<');
    expect(html).toContain('>左侧视图<');
    expect(html.match(/data-testid="studio-approval-input-view"/g)).toHaveLength(2);
    expect(html).toContain('src="/api/v1/assets/a2');
    expect(html).toMatch(/data-testid="studio-approval-credits"[^>]*>约 40 积分/);
    expect(html).toContain('由 正面 / 左侧 视图生成');
  });
});

// #329 follow-up D: a picture the person uploaded in this chat (no task behind it) shows its thumbnail on the
// card, read through the same authenticated asset address as the chat itself; without a known address the
// neutral placeholder stays.
describe('approval card input thumbnails', () => {
  it('names and previews unlabelled frozen inputs from both HTTP response shapes while offerings load', () => {
    const turn = cardFromConsent({ capability: 'image-to-3d', model: 'Meshy 7.1', provider: 'Meshy AI',
      reference_assets: [{ parameter: 'reference_assets', asset_id: 'copy-a', role: 'image', media_type: 'image/png' }, { parameter: 'reference_assets', asset_id: 'copy-b', role: 'image', media_type: 'image/png' }],
      reference_note: 'frozen note' }, 'r1')!;
    const [pending] = parsePendingApprovals({ items: [{ request_id: 'r2', status: 'pending', tool: 'create_model_3d_from_image', capability: 'image-to-3d', model: 'Meshy 7.1', provider: 'Meshy AI', provider_id: 'meshy',
      inputs: [{ parameter: 'reference_assets', kind: 'asset', role: 'image', asset_id: 'copy-a' }, { parameter: 'reference_assets', kind: 'asset', role: 'image', asset_id: 'copy-b' }] }] });
    const detached = cardFromPending(pending);
    for (const application of [defaultCatalog.application, englishApplication()]) {
      schemaState.application = application;
      const template = application.content.messages['ui.chat.input.attachment_alt'];
      for (const model of [turn, detached]) {
        const html = render(model);
        for (const [index, id] of ['copy-a', 'copy-b'].entries()) {
          const label = template.replace('{index}', String(index + 1));
          expect(html).toContain(`src="/api/v1/assets/${id}`);
          expect(html).toContain(`alt="${label}"`);
          expect(html).toContain(`>${label}</span>`);
        }
      }
    }
    expect(render(turn)).toContain('frozen note');
    swr.offerings = [{ provider_id: 'meshy', provider_name: 'Meshy AI', model_id: 'meshy-7.1', model_name: 'Meshy 7.1', parameters: [{ name: 'reference_assets', label: 'Reference picture' }] }];
    for (const model of [turn, detached]) {
      const html = render(model);
      expect(html).toContain('alt="Reference picture 1"');
      expect(html).toContain('alt="Reference picture 2"');
    }
  });
  const upload = (): ApprovalCardModel => ({
    ...card(null), capability: 'image-to-3d',
    inputs: [{ parameter: 'reference_assets', handle: '@img1', taskId: '', assetId: 'asset-up', kind: 'asset', label: '' }],
  });
  it('shows the uploaded picture', () => {
    handles.map = new Map([['@img1', { handle: '@img1', url: '/api/v1/assets/asset-up', assetId: 'asset-up' }]]);
    const html = render(upload());
    expect(html).toMatch(/data-testid="studio-approval-thumb" data-state="image"/);
    expect(html).toMatch(/<img src="\/api\/v1\/assets\/asset-up[^"]*"/);
  });
  it('does not render a known model input asset as an image', () => {
    const model = cardFromConsent({ capability: 'retexture', model: 'Meshy 7.1',
      reference_assets: [{ parameter: 'source_asset', asset_id: 'model-copy', role: 'model', media_type: 'model/gltf-binary' }] }, 'r')!;
    const html = render(model);
    expect(html).toContain('data-state="placeholder"');
    expect(html).not.toContain('src="/api/v1/assets/model-copy');
    expect(html).toContain('附件 1');
  });
  it('projects detached frozen model, image, and legacy inputs without changing task thumbnails', () => {
    const project = (input: Record<string, string>) => {
      const [pending] = parsePendingApprovals({ items: [{ request_id: 'r', status: 'pending', capability: 'remesh', model: 'Meshy 7.1', provider: 'Meshy AI', inputs: [input] }] });
      return cardFromPending(pending);
    };
    const model = project({ parameter: 'source_asset', kind: 'asset', role: 'model', asset_id: 'model-copy', media_type: 'model/gltf-binary' });
    const html = render(model);
    expect(model.inputs[0].previewable).toBe(false);
    expect(html).toContain('data-state="placeholder"');
    expect(html).not.toContain('/api/v1/assets/model-copy');
    expect(html).toContain('>附件 1</span>');
    const image = render(project({ parameter: 'reference_assets', kind: 'asset', role: 'image', asset_id: 'image-copy', media_type: 'image/png' }));
    expect(image).toContain('src="/api/v1/assets/image-copy');
    expect(image).toContain('alt="附件 1"');
    handles.map = new Map([['@img1', { handle: '@img1', url: '/api/v1/assets/legacy-image', assetId: 'legacy-image' }]]);
    const legacy = project({ parameter: 'reference_assets', kind: 'asset', handle: '@img1', asset_id: 'legacy-image' });
    expect(legacy.inputs[0].previewable).toBeUndefined();
    expect(render(legacy)).toContain('src="/api/v1/assets/legacy-image');
    const legacyHandle = project({ parameter: 'reference_assets', kind: 'asset', handle: '@img1' });
    expect(legacyHandle.inputs[0].previewable).toBeUndefined();
    expect(render(legacyHandle)).toContain('src="/api/v1/assets/legacy-image');
    const task = render(project({ parameter: 'source_task', kind: 'task', task_id: 'model-task', asset_id: 'preview-image', media_type: 'model/gltf-binary' }));
    expect(task).toContain('/tasks/model-task/assets/preview-image');
    // The cross-layer run supplies the real response emitted by the Go HTTP test.
    if (process.env.DETACHED_APPROVAL_JSON) {
      const [item] = parsePendingApprovals(JSON.parse(process.env.DETACHED_APPROVAL_JSON));
      const actual = cardFromPending(item);
      const assetId = actual.inputs[0].assetId;
      const rendered = render(actual);
      expect(actual.inputs[0]).toMatchObject({ kind: 'asset', previewable: false });
      expect(rendered).toContain('data-state="placeholder"');
      expect(rendered).not.toContain(`/api/v1/assets/${assetId}`);
      expect(rendered).toContain('>附件 1</span>');
    }
  });
  it('keeps the placeholder when the frozen input has no asset address', () => {
    const html = render({ ...upload(), inputs: [{ parameter: 'reference_assets', handle: '@img1', taskId: '', assetId: '', kind: 'asset', label: '' }] });
    expect(html).toMatch(/data-testid="studio-approval-thumb" data-state="placeholder"/);
  });
  it('reads a frozen input copy through its own asset route, never a source task route (#80)', () => {
    const model: ApprovalCardModel = {
      ...card(null), capability: 'image-to-3d',
      inputs: [{ parameter: 'reference_assets', handle: '@img1', taskId: 'source-task', assetId: 'input-copy',
        kind: 'asset', label: '' }],
    };
    const html = render(model);
    expect(html).toMatch(/<img src="\/api\/v1\/assets\/input-copy[^"]*"/);
    expect(html).not.toContain('/tasks/source-task/assets/input-copy');
  });
});
