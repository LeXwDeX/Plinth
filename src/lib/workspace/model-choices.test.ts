import { expect, it } from 'vitest';
import type { OfferingWire } from '@/lib/agent-offerings';
import { groupWorkspaceModels } from './model-choices';

function offering(overrides: Partial<OfferingWire> = {}): OfferingWire {
  return {
    id: 'provider/model/text-to-image',
    revision: '1',
    provider_id: 'provider',
    provider_name: 'Provider',
    model_id: 'model',
    model_name: 'Model',
    capability: 'text-to-image',
    available: true,
    parameters: [],
    outputs: [],
    ...overrides,
  };
}

it('keeps all available capabilities and unique Offering records for one model', () => {
  const text = offering();
  const image = offering({
    id: 'provider/model/image-to-image',
    capability: 'image-to-image',
    estimated_credits: 4,
  });

  const [choice] = groupWorkspaceModels({
    'text-to-image': [text, text],
    'image-to-image': [image],
  });

  expect(choice).toMatchObject(text);
  expect(choice.offerings).toEqual([text, image]);
  expect(choice.capabilities).toEqual(['text-to-image', 'image-to-image']);
});

it('excludes unavailable Offerings from the model and its capability list', () => {
  const available = offering();
  const unavailable = offering({
    id: 'provider/model/image-to-image',
    capability: 'image-to-image',
    available: false,
  });

  const [choice] = groupWorkspaceModels({
    'text-to-image': [available],
    'image-to-image': [unavailable],
  });

  expect(choice.offerings).toEqual([available]);
  expect(choice.capabilities).toEqual(['text-to-image']);
});

it('keeps equal model IDs from different Providers separate', () => {
  const providerA = offering();
  const providerB = offering({
    id: 'other/model/text-to-image',
    provider_id: 'other',
    provider_name: 'Other',
    model_name: 'Other Model',
  });

  const choices = groupWorkspaceModels({ image: [providerA, providerB] });

  expect(choices).toHaveLength(2);
  expect(choices.map(({ provider_id, model_id }) => [provider_id, model_id])).toEqual([
    ['provider', 'model'],
    ['other', 'model'],
  ]);
});
