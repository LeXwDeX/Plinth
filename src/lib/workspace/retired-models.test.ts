import { describe, expect, it } from 'vitest';

import { retiredModelSwitch, type RetiredGenerationModel } from './retired-models';

const retired: RetiredGenerationModel[] = [
  { provider_id: 'meshy', model_id: 'meshy-7', model_name: 'Meshy 7', successor_model_id: 'meshy-7.1' },
];
const choices = [
  { model_id: 'meshy-7.1', model_name: 'Meshy 7.1' },
  { model_id: 'meshy-t2.5', model_name: 'Meshy T2.5 智能拓扑' },
];

describe('retiredModelSwitch (#329)', () => {
  it('moves a retired model to its offered successor and names both', () => {
    expect(retiredModelSwitch(retired, 'meshy-7', choices)).toEqual({ from: 'Meshy 7', to: 'meshy-7.1', toName: 'Meshy 7.1' });
  });

  it('leaves an offered or unknown model alone', () => {
    expect(retiredModelSwitch(retired, 'meshy-7.1', choices)).toBeNull();
    expect(retiredModelSwitch(retired, 'meshy-3', choices)).toBeNull();
    expect(retiredModelSwitch(retired, null, choices)).toBeNull();
  });

  it('does not switch while the successor is not offered or the choices have not loaded', () => {
    expect(retiredModelSwitch(retired, 'meshy-7', [{ model_id: 'meshy-t2.5', model_name: 'T2.5' }])).toBeNull();
    expect(retiredModelSwitch(retired, 'meshy-7', undefined)).toBeNull();
    expect(retiredModelSwitch(undefined, 'meshy-7', choices)).toBeNull();
  });

  it('keeps a model the choices still offer even when the Schema lists it as retired', () => {
    expect(retiredModelSwitch(retired, 'meshy-7', [...choices, { model_id: 'meshy-7', model_name: 'Meshy 7' }])).toBeNull();
  });
});
