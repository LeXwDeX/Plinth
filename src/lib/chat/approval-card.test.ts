import { describe, expect, it } from 'vitest';

import type { OfferingWire } from '@/lib/agent-offerings';

import { accountOfCard, approvalInputParameterLabel, approvalOfferingNames, approvalShowsCredits, cardFromConsent, cardFromPending, creditBalanceOf, isRetextureCard, remainingValidity } from '@/lib/chat/approval-card';

it('reads how long a pending step stays valid from the server expiry', () => {
  const now = Date.parse('2026-10-08T10:00:00Z');
  expect(remainingValidity('2026-10-09T09:30:00Z', now)).toEqual({ unit: 'hours', value: 24 });
  expect(remainingValidity('2026-10-08T10:20:00Z', now)).toEqual({ unit: 'minutes', value: 20 });
  expect(remainingValidity('2026-10-08T09:59:59Z', now)).toEqual({ unit: 'expired' });
  expect(remainingValidity('', now)).toEqual({ unit: 'none' });
});

it('builds one card shape from both sources', () => {
  const detached = cardFromPending({
    requestId: 'r1', status: 'expired', tool: 'x', capability: 'image-to-3d', model: 'Meshy 7.1', provider: 'Meshy AI', providerId: 'meshy',
    estimatedCredits: 35, baseCredits: 0, extraCredits: 0, extraLabel: '', extraItems: [], prompt: 'a knight', inputs: [{ parameter: 'image', kind: 'asset', role: 'reference', assetId: 'a1', taskId: 't1', handle: '@img5' }],
    messageId: 'm1', createdAt: '', expiresAt: '2026-10-09T09:30:00Z',
  });
  expect(detached).toMatchObject({
    source: 'detached', status: 'expired', credits: 35, providerId: 'meshy',
    inputs: [{ handle: '@img5', taskId: 't1', assetId: 'a1', kind: 'asset', label: '' }],
  });
  const turn = cardFromConsent({ capability: 'retexture', model: 'Meshy 7', credits: 12, prompt: 'rust', source_task_id: 'm1', reference_assets: ['@img2'] }, 'r2');
  expect(turn).toMatchObject({ source: 'turn', status: 'pending', credits: 12, sourceTaskId: 'm1', inputs: [{ handle: '@img2' }] });
  expect(cardFromConsent(null, 'r3')).toBeNull();
});

it('carries the note that only the first pictures are used to the card (#329 E)', () => {
  const base = {
    requestId: 'r1', status: 'pending' as const, tool: 'create_model_3d_from_image', capability: 'image-to-3d', model: 'Meshy 7.1', provider: 'Meshy AI', providerId: 'meshy',
    estimatedCredits: 40, baseCredits: 0, extraCredits: 0, extraLabel: '', extraItems: [], prompt: 'a stool', inputs: [], messageId: 'm1', createdAt: '', expiresAt: '2026-10-09T09:30:00Z',
  };
  expect(cardFromPending({ ...base, referenceNote: 'only the first of 2' }).referenceNote).toBe('only the first of 2');
  expect(cardFromPending(base).referenceNote).toBeUndefined();
});

it('carries the extra credits and what they pay for from either source, and none when there are none', () => {
  const detached = cardFromPending({
    requestId: 'r1', status: 'pending', tool: 'x', capability: 'text-to-3d', model: 'Tripo', provider: 'Tripo', providerId: 'tripo',
    estimatedCredits: 50, baseCredits: 20, extraCredits: 30, extraLabel: '附加导出格式', extraItems: ['FBX', 'USDZ'], prompt: '木凳', inputs: [],
    messageId: '', createdAt: '', expiresAt: '2026-10-09T09:30:00Z',
  });
  expect(detached.extra).toEqual({ credits: 30, baseCredits: 20, label: '附加导出格式', items: ['FBX', 'USDZ'] });
  const turn = cardFromConsent({ capability: 'text-to-3d', model: 'Tripo', credits: 50, extra_credits: 30, base_credits: 20, extra_label: '附加导出格式', extra_items: ['FBX', 'USDZ'] }, 'r2');
  expect(turn?.extra).toEqual({ credits: 30, baseCredits: 20, label: '附加导出格式', items: ['FBX', 'USDZ'] });
  expect(cardFromConsent({ capability: 'retexture', model: 'Meshy 7', credits: 12 }, 'r3')?.extra).toBeNull();
});

describe('approvalOfferingNames', () => {
  const offerings = [
    { provider_id: 'meshy', provider_name: 'Meshy', model_id: 'meshy-7.1', model_name: 'Meshy 7.1' },
    { provider_id: 'other', provider_name: 'Other', model_id: 'meshy-7.1', model_name: 'Meshy Other' },
  ] as unknown as OfferingWire[];

  it('names the model and provider by their Schema display names', () => {
    expect(approvalOfferingNames(offerings, { model: 'meshy-7.1', provider: 'meshy' }))
      .toEqual({ modelName: 'Meshy 7.1', providerName: 'Meshy' });
  });

  it('falls back to the model match, then to the raw ids, so the step is never anonymous', () => {
    expect(approvalOfferingNames(offerings, { model: 'meshy-7.1', provider: 'gone' }).modelName).toBe('Meshy 7.1');
    expect(approvalOfferingNames(undefined, { model: 'x-1', provider: 'x' })).toEqual({ modelName: 'x-1', providerName: 'x' });
  });
});

describe('what the card says about its inputs and provider', () => {
  const pending = {
    requestId: 'r1', status: 'pending' as const, tool: 'x', capability: 'retexture', model: 'Meshy Retexture', provider: 'Meshy AI', providerId: 'meshy',
    estimatedCredits: 10, baseCredits: 0, extraCredits: 0, extraLabel: '', extraItems: [], prompt: '', messageId: 'm1', createdAt: '', expiresAt: '',
    inputs: [
      { parameter: 'source_task', kind: 'task', role: 'thumbnail', assetId: '', taskId: 'model-1', handle: '@model1' },
      { parameter: 'style_image', kind: 'asset', role: 'reference', assetId: 'a1', taskId: 'img-1', handle: '@img3' },
    ],
  };

  it('carries the label the session gives each input', () => {
    const card = cardFromPending(pending, (input) => (input.kind === 'task' ? '3D v1' : '正面'));
    expect(card.inputs.map((input) => [input.kind, input.label])).toEqual([['task', '3D v1'], ['asset', '正面']]);
  });

  it('knows a retexture step', () => {
    expect(isRetextureCard(cardFromPending(pending))).toBe(true);
    expect(isRetextureCard({ capability: 'image-to-3d' })).toBe(false);
  });

  it('finds the provider balance by account key, then by display name', () => {
    const accounts = [{ provider: 'meshy', label: 'MeshyAI', balance: 1000 }, { provider: 'tripo', label: 'Tripo', balance: 5 }];
    expect(accountOfCard(accounts, { provider: 'Meshy AI', providerId: 'meshy' })?.balance).toBe(1000);
    expect(accountOfCard(accounts, { provider: 'Meshy AI', providerId: '' })?.balance).toBe(1000);
    expect(accountOfCard(accounts, { provider: 'Tripo', providerId: '' })?.balance).toBe(5);
    expect(accountOfCard(accounts, { provider: 'Someone', providerId: 'someone' })).toBeUndefined();
    expect(accountOfCard(accounts, { provider: '', providerId: '' })).toBeUndefined();
  });
});

describe('whether the card shows credits', () => {
  const offering = (provider_id: string, model_id: string, credits_metered?: boolean) =>
    ({ provider_id, model_id, credits_metered }) as unknown as OfferingWire;
  const card = { model: 'm1', provider: 'tripo' };

  it('follows the Offering credits_metered declaration', () => {
    expect(approvalShowsCredits([offering('tripo', 'm1', true)], card)).toBe(true);
    expect(approvalShowsCredits([offering('tripo', 'm1', false)], card)).toBe(false);
    expect(approvalShowsCredits([offering('tripo', 'm1')], card)).toBe(false);
  });

  it('keeps the server number while the Offering is not known yet', () => {
    expect(approvalShowsCredits(undefined, card)).toBe(true);
    expect(approvalShowsCredits([offering('tripo', 'other', false)], card)).toBe(true);
  });
});

// #324 (from #187 note 98966): the source model file of a local-model step showed as a bare picture
// placeholder. Whatever the session cannot name, the card still names by the Offering's own label
// for that parameter ("来源模型"), from either card source.
describe('an input the session cannot name (#324)', () => {
  const offerings = [{ provider_id: 'hyper3d', model_id: 'texture', model_name: 'Hyper3D 纹理生成', provider_name: 'Hyper3D',
    parameters: [{ name: 'source_asset', label: '来源模型', type: 'asset' }, { name: 'reference_image', label: '材质参考图', type: 'asset' }] }] as unknown as OfferingWire[];
  const model = { model: 'texture', provider: 'hyper3d' };

  it('keeps the parameter of every input', () => {
    const turn = cardFromConsent({ capability: 'retexture', model: 'texture', reference_assets: [{ parameter: 'source_asset', asset_id: 'a1' }] }, 'r1');
    expect(turn?.inputs[0]).toMatchObject({ parameter: 'source_asset', assetId: 'a1' });
    const detached = cardFromPending({
      requestId: 'r1', status: 'pending', tool: 'x', capability: 'retexture', model: 'texture', provider: 'hyper3d', providerId: 'hyper3d',
      estimatedCredits: 0, baseCredits: 0, extraCredits: 0, extraLabel: '', extraItems: [], prompt: '', messageId: '', createdAt: '', expiresAt: '',
      inputs: [{ parameter: 'source_asset', kind: 'asset', role: 'source_asset', assetId: 'a1', taskId: '', handle: '' }],
    });
    expect(detached.inputs[0].parameter).toBe('source_asset');
  });

  it('names it by the Offering parameter label, and nothing for an unknown parameter', () => {
    expect(approvalInputParameterLabel(offerings, model, 'source_asset')).toBe('来源模型');
    expect(approvalInputParameterLabel(offerings, model, 'unknown')).toBe('');
    expect(approvalInputParameterLabel(undefined, model, 'source_asset')).toBe('');
  });
});

// #324 (from #321 note 99042 J): a provider with any natively priced Offering (Tripo) reports its
// balance as an exact native amount only, so a credit-priced Tripo step read "balance unavailable".
// Its native amount is the same provider credits the step is priced in.
describe('the balance a credit-priced card compares (#324)', () => {
  it('reads the numeric balance, else the native amount of the same account', () => {
    expect(creditBalanceOf({ balance: 120 })).toBe(120);
    expect(creditBalanceOf({ priceMode: 'native', nativeBalance: { unit: 'tripo.credit', unit_label: 'Tripo credits', amount: '4895' } })).toBe(4895);
    expect(creditBalanceOf({ priceMode: 'native', nativeBalance: { unit: 'hyper3d.credit', unit_label: 'Hyper3D credits', amount: '198.5' } })).toBe(198.5);
    expect(creditBalanceOf({ priceMode: 'native', nativeBalance: null })).toBeNull();
    expect(creditBalanceOf(undefined)).toBeNull();
  });
});
