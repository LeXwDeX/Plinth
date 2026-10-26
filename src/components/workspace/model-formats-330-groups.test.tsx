import React from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import { ModelFormatsView } from '@/components/workspace/model-formats';
import { schemaCountMessage, schemaMessage, type ProductSchema } from '@/lib/product-schema';
import { parseModelFormatCoverage } from '@/lib/workspace/model-formats';

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

// #330 second follow-ups, observation 1: the local glTF packaging and the Meshy Convert both failed. The block listed
// "重试失败的转换" and then "上次已扣 1 积分" right after that free retry, and neither button said which conversion it
// was for. Each failed conversion is now its own group: which formats, at which provider, the charge only where one
// was taken, and buttons that name their conversion.
const local = { provider_id: 'local', provider_name: '格式转换', provider_short_label: '本地', estimated_credits: 0 };
const meshy = { provider_id: 'meshy', provider_name: 'Meshy AI', provider_short_label: 'MESHY', estimated_credits: 1, credits_metered: true };
const formats = [
  { id: 'glb', label: 'GLB', status: 'available', task_id: 't1', asset_id: 'a1' },
  { id: 'gltf', label: 'glTF', status: 'obtainable', conversion: local, reason: 'conversion_failed' },
  { id: 'fbx', label: 'FBX', status: 'obtainable', conversion: meshy, reason: 'conversion_failed' },
  { id: 'obj', label: 'OBJ', status: 'obtainable', conversion: meshy, reason: 'conversion_failed' },
];
const gltfStep = {
  offering_id: 'local/generated-glb/to-gltf', provider_id: 'local', provider_name: '格式转换', provider_short_label: '本地',
  formats: ['gltf'], estimated_credits: 0, state: 'FAILED', plan_id: 'p-gltf', plan_version: 2, offering_revision: 'r1',
  failure: 'plan_task_failed', result_task_id: 'gltf-failed',
};
const convertStep = {
  offering_id: 'meshy/processing/convert', provider_id: 'meshy', provider_name: 'Meshy AI', provider_short_label: 'MESHY',
  formats: ['fbx', 'obj'], estimated_credits: 1, state: 'FAILED', plan_id: 'p-convert', plan_version: 2, offering_revision: 'r2',
  failure: 'plan_task_failed', result_task_id: 'convert-failed', charged_credits: 1, refetch: { eligible: true },
};
const render = (s: ProductSchema, steps: object[], onRefetch: (() => void) | undefined = () => {}) => renderToStaticMarkup(
  <ModelFormatsView schema={s} coverage={parseModelFormatCoverage({ task_id: 't1', applicable: true, formats, steps })!}
    busy={false} error="" onComplete={() => {}} onRetry={() => {}} onConvert={() => {}} onRefetch={onRefetch} />,
);
const groupsOf = (html: string) => {
  const block = html.slice(html.indexOf('data-testid="model-formats-failed"'));
  return block.split('data-testid="model-formats-failed-step"').slice(1);
};

describe('failed conversions are grouped per conversion (#330 second follow-ups)', () => {
  it('gives each failed conversion its own group, naming its formats and provider', () => {
    const [gltf, convert] = groupsOf(render(schema, [gltfStep, convertStep]));
    expect(gltf).toContain(schemaMessage(schema, 'ui.workspace.model_formats.failed_step', { formats: 'glTF', provider: '本地' }));
    expect(convert).toContain(schemaMessage(schema, 'ui.workspace.model_formats.failed_step', { formats: 'FBX、OBJ', provider: 'Meshy AI' }));
  });

  it('puts the charge only in the group of the conversion that was charged', () => {
    const charged = schemaCountMessage(schema, 'ui.workspace.model_formats.charged_note', 1, { credits: 1 });
    const [gltf, convert] = groupsOf(render(schema, [gltfStep, convertStep]));
    expect(gltf).not.toContain(charged);
    expect(gltf).not.toContain('data-testid="model-formats-charged"');
    expect(convert).toContain(charged);
  });

  it('names the conversion on each button: the free retry of glTF, the free refetch of the Meshy formats', () => {
    const [gltf, convert] = groupsOf(render(schema, [gltfStep, convertStep]));
    expect(gltf).toContain('data-action="retry"');
    expect(gltf).toContain(`${schemaMessage(schema, 'ui.workspace.model_formats.retry_named', { format: 'glTF' })}</button>`);
    expect(convert).toContain('data-action="refetch"');
    expect(convert).toContain(`${schemaMessage(schema, 'ui.workspace.model_formats.refetch_named', { format: 'FBX、OBJ' })}</button>`);
    // Without the refetch the charged group retries and says it charges again.
    const [, paid] = groupsOf(render(schema, [gltfStep, { ...convertStep, refetch: { eligible: false, reason: 'expired' } }]));
    expect(paid).toContain(`${schemaMessage(schema, 'ui.workspace.model_formats.retry_charged_named', { format: 'FBX、OBJ' })}</button>`);
  });

  it('reads the same in English', () => {
    const [gltf, convert] = groupsOf(render(englishSchema, [gltfStep, convertStep]));
    expect(gltf).toContain(schemaMessage(englishSchema, 'ui.workspace.model_formats.failed_step', { formats: 'glTF', provider: '本地' }));
    expect(convert).toContain(schemaMessage(englishSchema, 'ui.workspace.model_formats.failed_step', { formats: 'FBX, OBJ', provider: 'Meshy AI' }));
    expect(convert).toContain('1 credit was charged last time');
    expect(convert).toContain(`${english['ui.workspace.model_formats.refetch_named'].replace('{format}', 'FBX, OBJ')}</button>`);
  });
});

// #330 second follow-ups, observation 6: the English hint said "Click Fetch again" while the button reads
// "Fetch again (free)". Every hint now quotes its button exactly, in both languages.
describe('row hints quote their buttons (#330 second follow-ups)', () => {
  const quoted: Array<[string, string]> = [
    ['refetch_hint', 'refetch'], ['refetch_hint_one', 'refetch'],
    ['retry_charged_hint', 'retry_charged'], ['retry_charged_hint_one', 'retry_charged'],
    ['retry_hint', 'retry'], ['obtainable_hint', 'convert'],
  ];
  for (const [hint, button] of quoted) {
    it(`${hint} names the ${button} button`, () => {
      const key = (name: string) => `ui.workspace.model_formats.${name}`;
      expect(messages[key(hint)]).toContain(`「${messages[key(button)]}」`);
      expect(english[key(hint)]).toContain(`“${english[key(button)]}”`);
    });
  }

  it('declares the group title in both languages', () => {
    expect(messages['ui.workspace.model_formats.failed_step']).toContain('{formats}');
    expect(english['ui.workspace.model_formats.failed_step']).toContain('{provider}');
    expect(english['ui.workspace.model_formats.failed_step']).not.toMatch(/[一-鿿]/);
  });
});

// Coordinator follow-up (#313 review): a conversion that failed and can never run again as it was (its source's
// original task expired) is offered as a new run and says why; an ambiguous failure offers no retry at all.
describe('failed conversions that cannot be retried (#330 second follow-ups)', () => {
  it('offers a renewed run as a conversion and says the last one did not finish', () => {
    const renewed = { ...convertStep, state: '', plan_id: '', plan_version: 0, result_task_id: '', charged_credits: 0, refetch: undefined, previous_failure: 'plan_source_expired' };
    const html = render(schema, [renewed]);
    const fbx = html.slice(html.indexOf('data-testid="model-format-fbx"'), html.indexOf('data-testid="model-format-obj"'));
    expect(fbx).toContain('data-action="start"');
    expect(fbx).toContain(messages['ui.workspace.model_formats.restart_hint']);
    expect(fbx).not.toContain(messages['ui.workspace.model_formats.obtainable_hint']);
    expect(html).not.toContain('data-testid="model-formats-failed"');
  });

  it('shows no retry for a failure whose outcome is unknown', () => {
    const blocked = { ...gltfStep, retry_blocked: true };
    const [group] = groupsOf(render(schema, [blocked]));
    expect(group).not.toContain('<button');
    expect(group).toContain(messages['ui.workspace.model_formats.retry_blocked']);
    const html = render(schema, [blocked]);
    const gltf = html.slice(html.indexOf('data-testid="model-format-gltf"'), html.indexOf('data-testid="model-format-fbx"'));
    expect(gltf).not.toContain('data-action=');
    expect(english['ui.workspace.model_formats.retry_blocked']).toBeTruthy();
    expect(english['ui.workspace.model_formats.restart_hint']).toContain(`“${english['ui.workspace.model_formats.convert']}”`);
  });
});
