import React from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import { ModelArchiveLink, ModelFormatsView, type FormatsResume } from '@/components/workspace/model-formats';
import { schemaMessage, type ProductSchema } from '@/lib/product-schema';
import { parseModelFormatCoverage } from '@/lib/workspace/model-formats';

const schema = defaultCatalog.application as unknown as ProductSchema;
const messages = (defaultCatalog.application as { content: { messages: Record<string, string> } }).content.messages;

vi.mock('@/components/product-schema-provider', () => ({
  useProductSchema: () => ({ schema: defaultCatalog.application, text: (key: string) => messages[key] ?? '' }),
}));
vi.mock('@/store/workspace-store', () => ({
  useWorkspaceStore: (selector: (state: object) => unknown) => selector({ registerTask: () => {} }),
}));

const view = (wire: Record<string, unknown>, overrides: { busy?: boolean; error?: string } = {}) => renderToStaticMarkup(
  <ModelFormatsView
    schema={schema}
    coverage={parseModelFormatCoverage({ task_id: 't1', applicable: true, formats: [], steps: [], ...wire })!}
    busy={overrides.busy ?? false}
    error={overrides.error ?? ''}
    onComplete={() => {}}
    onRetry={() => {}}
    onConvert={() => {}}
  />,
);

const rows = [
  { id: 'glb', label: 'GLB', status: 'available', task_id: 't1', asset_id: 'a1' },
  { id: 'fbx', label: 'FBX', status: 'obtainable', conversion: { provider_name: 'Tripo', estimated_credits: 5 } },
  { id: 'usdz', label: 'USDZ', status: 'converting' },
  { id: 'gltf', label: 'glTF', status: 'unavailable', reason_text: '没有适用于该模型的转换服务。' },
];

it('renders each format with its status, download link, credits and reason', () => {
  const html = view({ formats: rows, can_complete: true, completion_credits: 5 });
  for (const status of ['available', 'obtainable', 'converting', 'unavailable']) {
    expect(html).toContain(messages[`ui.workspace.model_formats.status.${status}`]);
  }
  expect(html).toContain('data-testid="model-format-glb"');
  expect(html).toContain('data-status="available"');
  expect(html).toContain('href="/api/v1/tasks/t1/assets/a1"');
  expect(html).toContain('约 5 积分');
  expect(html).toContain('没有适用于该模型的转换服务。');
  // Only available rows are downloadable.
  expect(html.match(/download=""/g)?.length).toBe(1);
});

it('names the provider that would convert an obtainable format', () => {
  // The row says who produces the missing file, so a paid conversion is never
  // attributed to the platform itself.
  const html = view({ formats: [
    rows[0],
    { id: 'fbx', label: 'FBX', status: 'obtainable', conversion: { provider_name: 'Tripo', estimated_credits: 5 } },
    { id: 'usdz', label: 'USDZ', status: 'converting', conversion: { provider_name: 'Tripo', estimated_credits: 5 } },
  ] });
  expect(html).toContain('可转换 · Tripo');
  expect(html).toContain('转换中 · Tripo');
  expect(html).not.toContain('已有 · ');
});

it('offers the completion action with the credit estimate only when something can be obtained', () => {
  const withAction = view({ formats: rows, can_complete: true, completion_credits: 30 });
  expect(withAction).toContain('data-testid="model-formats-complete"');
  expect(withAction).toContain('补齐格式（约 30 积分）');
  const without = view({ formats: [rows[0], rows[3]], can_complete: false });
  expect(without).not.toContain('data-testid="model-formats-complete"');
  expect(without).toContain('data-reason="no_route"');
});

it('shows progress, pause and failure with a retry action', () => {
  const converting = view({ formats: rows, steps: [{ state: 'RUNNING', plan_id: 'p1' }] });
  expect(converting).toContain('data-testid="model-formats-converting"');
  expect(converting).not.toContain('data-testid="model-formats-complete"');
  const paused = view({ formats: rows, steps: [{ state: 'PAUSED', plan_id: 'p1' }] });
  expect(paused).toContain('data-testid="model-formats-paused"');
  expect(paused).toContain(messages['ui.workspace.model_formats.plan_paused']);
  const failed = view({ formats: rows, steps: [{ state: 'FAILED', plan_id: 'p1', plan_version: 2, offering_revision: 'r' }] }, { error: '失败了' });
  expect(failed).toContain('data-testid="model-formats-failed"');
  expect(failed).toContain('data-testid="model-formats-failed-step"');
  expect(failed).toContain('data-action="retry"');
  expect(failed).toContain('失败了');
});

it('offers the one-click ZIP as a preparable, cancellable download', () => {
  const html = renderToStaticMarkup(<ModelArchiveLink taskId="t1" />);
  expect(html).toContain('data-testid="model-archive-link"');
  expect(html).toContain('data-state="idle"');
  expect(html).toContain('data-archive-route="/api/v1/tasks/t1/archive"');
  expect(html).toContain(messages['ui.workspace.model_formats.download_all']);
  expect(html).not.toContain('data-testid="model-archive-cancel"');
});

it('lists every Schema delivery format even when the API has nothing to say', () => {
  const withFormats = { ...schema, delivery_formats: ['glb', 'gltf', 'fbx', 'obj', 'stl', 'usdz', '3mf'].map((id) => ({ id, label: id.toUpperCase() })) } as unknown as ProductSchema;
  const html = renderToStaticMarkup(
    <ModelFormatsView schema={withFormats} coverage={parseModelFormatCoverage({ task_id: 't', applicable: false, formats: [], steps: [] })} busy={false} error="" onComplete={() => {}} onRetry={() => {}} />,
  );
  expect(html.match(/data-testid="model-format-[a-z0-9]+"/g)).toHaveLength(7);
  expect(html.match(/data-status="unavailable"/g)).toHaveLength(7);
  expect(html).toContain(messages['ui.studio.formats.footer.not_ready']);
  expect(html).toContain('data-reason="not_ready"');
  expect(html.match(/role="img" tabindex="0" aria-label="查看原因:/g)).toHaveLength(7);
  expect(html.match(/class="[^"]*h-9 min-h-9[^"]*"/g)).toHaveLength(7);
  expect(html).toContain('role="img" tabindex="0"');
  expect(html).not.toContain('没有适用于该模型的转换服务。');
  expect(html).not.toContain('data-testid="model-formats-complete"');
});

it('keeps a distinct unavailable reason visible beside the affected format', () => {
  const html = view({ formats: [
    { id: 'glb', label: 'GLB', status: 'unavailable', reason_text: '该模型没有可用的导出文件。' },
    { id: 'obj', label: 'OBJ', status: 'unavailable', reason_text: 'OBJ 导出暂不可用。' },
  ] });
  expect(html).toContain('该模型没有可用的导出文件。');
  expect(html).toContain('OBJ 导出暂不可用。');
  expect(html).not.toContain('model-format-reason-glb');
  expect(html).toContain('role="note" tabindex="0"');
  expect(html).toContain('aria-label="查看原因: GLB"');
  expect(html).toContain('truncate');
});

it('keeps a repeated unknown/loading state visible instead of replacing it with symbols', () => {
  const withFormats = {
    ...schema,
    delivery_formats: defaultCatalog.application.model_delivery.formats.map(({ id, label }) => ({ id, label })),
  } as unknown as ProductSchema;
  const html = renderToStaticMarkup(
    <ModelFormatsView schema={withFormats} coverage={null} phase="loading" busy={false} error="" onComplete={() => {}} onRetry={() => {}} />,
  );
  expect(html.match(/data-testid="model-format-[a-z0-9]+"/g)).toHaveLength(7);
  expect(html.match(/data-status="unknown"/g)).toHaveLength(7);
  expect(html).toContain(messages['ui.workspace.model_formats.loading']);
  expect(html).not.toContain('role="img"');
});

describe('a paused conversion', () => {
  const pausedWire: Record<string, unknown> = { formats: rows, steps: [{ state: 'PAUSED', plan_id: 'p1', plan_version: 3, offering_revision: 'r1' }] };
  const resumeControl: FormatsResume = { label: '确认当前费用，继续处理 · 5', disabled: false, busy: false, onResume: () => {} };
  const withResume = (resume: FormatsResume | null, extra: { layout?: 'flow' | 'pinned'; pausedReason?: string; busy?: boolean; error?: string; wire?: Record<string, unknown> } = {}) => renderToStaticMarkup(
    <ModelFormatsView
      schema={schema}
      coverage={parseModelFormatCoverage({ task_id: 't1', applicable: true, formats: [], steps: [], ...(extra.wire ?? pausedWire) })!}
      busy={extra.busy ?? false}
      error={extra.error ?? ''}
      onComplete={() => {}}
      onRetry={() => {}}
      layout={extra.layout}
      resume={resume}
      pausedReason={extra.pausedReason}
    />,
  );

  it('no longer sends the person to another panel', () => {
    expect(messages['ui.workspace.model_formats.plan_paused']).not.toContain('处理面板');
    expect(withResume(resumeControl)).not.toContain('处理面板');
  });

  it('offers the resume control in the tab, with the label the shared resume gives it', () => {
    const html = withResume(resumeControl);
    expect(html).toContain('data-testid="model-formats-resume"');
    expect(html).toContain('确认当前费用，继续处理 · 5');
    // The paused note comes before the control it explains.
    expect(html.indexOf('model-formats-paused')).toBeLessThan(html.indexOf('model-formats-resume'));
  });

  it('says why the conversion waits when the plan names a reason', () => {
    const html = withResume(resumeControl, { pausedReason: messages['ui.workspace.processing.plan_task_canceled'] });
    expect(html).toContain('data-testid="model-formats-paused-reason"');
    expect(html).toContain(messages['ui.workspace.processing.plan_task_canceled']);
    expect(withResume(resumeControl)).not.toContain('model-formats-paused-reason');
  });

  it('has only the note while nothing can be resumed, and no control when nothing is paused', () => {
    const noControl = withResume(null);
    expect(noControl).toContain('data-testid="model-formats-paused"');
    expect(noControl).not.toContain('model-formats-resume');
    const running = withResume(resumeControl, { wire: { formats: rows, steps: [{ state: 'RUNNING', plan_id: 'p1' }] } });
    expect(running).not.toContain('model-formats-resume');
    expect(running).not.toContain('model-formats-paused');
  });

  it('disables the control while the Offering cannot run, and while another request is in flight', () => {
    const tag = (html: string) => html.match(/<button[^>]*data-testid="model-formats-resume"[^>]*>/)?.[0] ?? '';
    expect(tag(withResume({ ...resumeControl, disabled: true }))).toContain('disabled');
    expect(tag(withResume(resumeControl))).not.toContain('disabled=""');
    expect(tag(withResume(resumeControl, { busy: true }))).toContain('disabled');
  });

  it('shows a resume error and keeps the control, so the person can try again', () => {
    const html = withResume(resumeControl, { error: '计划版本已变化' });
    expect(html).toContain('计划版本已变化');
    expect(html).toContain('model-formats-resume');
  });
});

describe('the pinned layout of the 格式 tab', () => {
  const pinned = (wire: Record<string, unknown>, resume: FormatsResume | null = null, header?: React.ReactNode) => renderToStaticMarkup(
    <ModelFormatsView
      schema={schema}
      coverage={parseModelFormatCoverage({ task_id: 't1', applicable: true, formats: [], steps: [], ...wire })!}
      busy={false}
      error=""
      onComplete={() => {}}
      onRetry={() => {}}
      layout="pinned"
      header={header}
      resume={resume}
    />,
  );

  it('scrolls the rows and pins 补齐格式 to the bottom, full width', () => {
    const html = pinned({ formats: rows, can_complete: true, completion_credits: 30 }, null, <a data-testid="zip" />);
    expect(html).toContain('data-layout="pinned"');
    const body = html.slice(0, html.indexOf('<footer'));
    const footer = html.slice(html.indexOf('<footer'));
    expect(body).toContain('overflow-y-auto');
    expect(body).toContain('data-testid="zip"');
    expect(body.indexOf('data-testid="zip"')).toBeLessThan(body.indexOf('data-testid="model-format-glb"'));
    expect(body).not.toContain('model-formats-complete');
    expect(footer).toContain('data-testid="model-formats-actions"');
    expect(footer).toContain('data-testid="model-formats-complete"');
    expect(footer).toContain('补齐格式（约 30 积分）');
    expect(footer.match(/<button[^>]*data-testid="model-formats-complete"[^>]*>/)?.[0]).toMatch(/class="[^"]*w-full/);
    expect(footer).toContain('border-t');
  });

  it('pins the resume control the same way, and keeps progress and failure in the scrolling body', () => {
    const resumeControl: FormatsResume = { label: '继续', disabled: false, busy: false, onResume: () => {} };
    const paused = pinned({ formats: rows, steps: [{ state: 'PAUSED', plan_id: 'p1' }] }, resumeControl);
    expect(paused.slice(paused.indexOf('<footer'))).toContain('data-testid="model-formats-resume"');
    expect(paused.slice(0, paused.indexOf('<footer'))).toContain('data-testid="model-formats-paused"');
    const converting = pinned({ formats: rows, steps: [{ state: 'RUNNING', plan_id: 'p1' }] });
    expect(converting).toContain('data-testid="model-formats-converting"');
    expect(converting).not.toContain('<footer');
    const failed = pinned({ formats: rows, steps: [{ state: 'FAILED', plan_id: 'p1', plan_version: 2, offering_revision: 'r' }] });
    expect(failed.slice(0, failed.indexOf('data-testid="model-formats-failed"'))).not.toContain('<footer');
    expect(failed).toContain('data-action="retry"');
  });

  it('has no footer when there is nothing to do, and says why in the body', () => {
    const html = pinned({ formats: [rows[0], rows[3]], can_complete: false });
    expect(html).not.toContain('<footer');
    expect(html).toContain('data-reason="no_route"');
  });

  it('keeps the default layout as it was: one block, the action under the rows', () => {
    const html = view({ formats: rows, can_complete: true, completion_credits: 30 });
    expect(html).not.toContain('<footer');
    expect(html).not.toContain('data-layout');
    expect(html.indexOf('model-format-glb')).toBeLessThan(html.indexOf('model-formats-complete'));
  });
});

describe('the action and the wording of an obtainable row (#323)', () => {
  const local = { provider_id: 'local', provider_name: '格式转换', provider_short_label: '本地', estimated_credits: 0, credits_metered: false };
  const meshy = { provider_id: 'meshy', provider_name: 'Meshy AI', provider_short_label: 'MESHY', estimated_credits: 5, credits_metered: true };
  const wire = {
    formats: [
      { id: 'glb', label: 'GLB', status: 'available', task_id: 't1', asset_id: 'a1' },
      { id: 'gltf', label: 'glTF', status: 'obtainable', reason: 'not_converted', reason_text: '尚未转换；在平台中使用“补齐格式”即可获取。', conversion: local },
      { id: 'fbx', label: 'FBX', status: 'obtainable', conversion: meshy },
      { id: 'obj', label: 'OBJ', status: 'obtainable', conversion: meshy },
    ],
    steps: [
      { offering_id: 'local/generated-glb/to-gltf', formats: ['gltf'] },
      { offering_id: 'meshy/processing/remesh', formats: ['fbx', 'obj'], estimated_credits: 5 },
    ],
    can_complete: true,
    completion_credits: 5,
  };
  const rowOf = (html: string, id: string) => {
    const start = html.indexOf(`data-testid="model-format-${id}"`);
    return html.slice(start, html.indexOf('</li>', start));
  };
  const both = (wireOverride: Record<string, unknown> = wire) => [
    view(wireOverride),
    renderToStaticMarkup(
      <ModelFormatsView
        schema={schema}
        coverage={parseModelFormatCoverage({ task_id: 't1', applicable: true, ...wireOverride })!}
        busy={false}
        error=""
        onComplete={() => {}}
        onRetry={() => {}}
        onConvert={() => {}}
        layout="pinned"
      />,
    ),
  ];

  it('names a free local conversion 本地 · 免费, never the Offering name 格式转换', () => {
    for (const html of both()) {
      const gltf = rowOf(html, 'gltf');
      expect(gltf).toContain(`${messages['ui.workspace.model_formats.status.obtainable']} · 本地`);
      expect(gltf).toContain(`data-testid="model-format-price-gltf">${messages['ui.workspace.model_formats.free']}</span>`);
      expect(gltf).not.toContain('格式转换');
    }
  });

  it('keeps the provider name and the estimate of a paid conversion', () => {
    for (const html of both()) {
      const fbx = rowOf(html, 'fbx');
      expect(fbx).toContain(`${messages['ui.workspace.model_formats.status.obtainable']} · Meshy AI`);
      expect(fbx).toContain('约 5 积分');
      expect(fbx).not.toContain(messages['ui.workspace.model_formats.free']);
    }
  });

  it('gives every obtainable row its own convert action and says what clicking does', () => {
    for (const html of both()) {
      for (const id of ['gltf', 'fbx', 'obj']) {
        const row = rowOf(html, id);
        expect(row).toContain(`data-testid="model-format-action-${id}"`);
        expect(row).toContain('data-action="start"');
        expect(row).toContain(`${messages['ui.workspace.model_formats.convert']}</button>`);
        expect(row).toContain(messages['ui.workspace.model_formats.obtainable_hint']);
      }
      expect(rowOf(html, 'glb')).not.toContain('model-format-action-');
      // 补齐格式 stays.
      expect(html).toContain('data-testid="model-formats-complete"');
    }
  });

  it('names the other formats one run converts with it', () => {
    const together = schemaMessage(schema, 'ui.workspace.model_formats.convert_together', { formats: 'FBX、OBJ' });
    const [html] = both();
    expect(rowOf(html, 'fbx')).toContain(together);
    expect(rowOf(html, 'obj')).toContain(together);
    expect(rowOf(html, 'gltf')).not.toContain('FBX、OBJ');
  });

  it('turns the row action into a retry after the conversion of that row failed', () => {
    const failed = { ...wire, steps: [{ offering_id: 'local/generated-glb/to-gltf', formats: ['gltf'], state: 'FAILED', plan_id: 'p1', plan_version: 2, offering_revision: 'r' }, wire.steps[1]] };
    for (const html of both(failed)) {
      const gltf = rowOf(html, 'gltf');
      expect(gltf).toContain('data-action="retry"');
      expect(gltf).toContain(`${messages['ui.workspace.model_formats.retry']}</button>`);
      expect(gltf).toContain(messages['ui.workspace.model_formats.retry_hint']);
    }
  });

  it('offers only retries when the host gives no way to start a conversion', () => {
    const html = renderToStaticMarkup(
      <ModelFormatsView schema={schema} coverage={parseModelFormatCoverage({ task_id: 't1', applicable: true, ...wire })!} busy={false} error="" onComplete={() => {}} onRetry={() => {}} />,
    );
    expect(html).not.toContain('data-action="start"');
    // The row still says what it is and what it costs.
    expect(rowOf(html, 'gltf')).toContain(messages['ui.workspace.model_formats.free']);
  });

  it('has no action while the row converts, and names its provider the same way', () => {
    const converting = { ...wire, formats: [wire.formats[0], { ...wire.formats[1], status: 'converting', reason: 'converting' }], steps: [{ ...wire.steps[0], state: 'RUNNING', plan_id: 'p1' }] };
    for (const html of both(converting)) {
      const gltf = rowOf(html, 'gltf');
      expect(gltf).toContain(`${messages['ui.workspace.model_formats.status.converting']} · 本地`);
      expect(gltf).not.toContain('model-format-action-');
    }
  });

  it('disables the row actions and marks the clicked one busy while a request is in flight', () => {
    const html = renderToStaticMarkup(
      <ModelFormatsView
        schema={schema}
        coverage={parseModelFormatCoverage({ task_id: 't1', applicable: true, ...wire })!}
        busy
        pendingFormat="gltf"
        error=""
        onComplete={() => {}}
        onRetry={() => {}}
        onConvert={() => {}}
      />,
    );
    const button = (id: string) => rowOf(html, id).match(/<button[^>]*data-testid="model-format-action-[a-z0-9]+"[^>]*>/)?.[0] ?? '';
    expect(button('gltf')).toContain('disabled');
    expect(button('gltf')).toContain('aria-busy="true"');
    expect(button('fbx')).toContain('disabled');
    expect(button('fbx')).not.toContain('aria-busy="true"');
  });

  it('declares every new text in English as well', () => {
    const english = (defaultCatalog.application as unknown as { localization: { bundles: { en: { messages: Record<string, string> } } } }).localization.bundles.en.messages;
    for (const name of ['free', 'convert', 'convert_together', 'retry', 'obtainable_hint', 'retry_hint']) {
      const key = `ui.workspace.model_formats.${name}`;
      expect(messages[key], key).toBeTruthy();
      expect(english[key], key).toBeTruthy();
      expect(english[key]).not.toMatch(/[一-鿿]/);
    }
  });
});
