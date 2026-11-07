// @vitest-environment jsdom
import React from 'react';
import { Blob as NodeBlob } from 'node:buffer';
import { readFileSync } from 'node:fs';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../internal/catalog/default_catalog.json';
import type { OfferingWire } from '@/lib/agent-offerings';
import { InpaintDialog, contentFingerprint, inpaintSourceIdentity, savePendingDirectTask } from '@/components/workspace/canvas-image-actions';
import { canvasTurnOutput } from '@/lib/workspace/canvas-size';
import { effectiveOfferingValues } from '@/lib/workspace/generation-params';

/**
 * #187：画布局部重绘直提交读取会话风格锁定，弹窗显示 chip 与单轮豁免；
 * 风格快照参与冻结输入的 identity，锁/豁免变化后旧 prepared task 不再复用。
 */

const messages = (defaultCatalog.application as { content: { messages: Record<string, string> } }).content.messages;
const schema = defaultCatalog.application;

const lockState = vi.hoisted(() => ({
  lock: { enabled: true, text: 'ink', references: ['style-a'] as string[] },
}));

vi.mock('@/components/product-schema-provider', () => ({
  useProductSchema: () => ({ schema, text: (key: string) => messages[key] ?? '' }),
}));
vi.mock('@/hooks/use-style-lock', () => ({
  useStyleLock: () => ({ lock: lockState.lock, busy: false, failed: false, saveError: null, refresh: () => undefined, save: () => undefined }),
}));
vi.mock('@/store/chat-store', () => ({
  useChatStore: (selector: (state: { activeSessionId: string }) => unknown) => selector({ activeSessionId: 'session-a' }),
}));
vi.mock('@/store/workspace-store', () => ({
  useWorkspaceStore: (selector: (state: { registerTask: () => void }) => unknown) => selector({ registerTask: () => undefined }),
}));
vi.mock('@/components/ui/dialog', () => ({
  Dialog: ({ children }: { children: unknown }) => children,
  DialogContent: ({ children }: { children: unknown }) => children,
  DialogTitle: ({ children }: { children: unknown }) => children,
}));
vi.mock('@/components/ui/hint-tooltip', () => ({
  HintTooltip: ({ children }: { children: unknown }) => children,
}));

function offeringWire(overrides: Partial<OfferingWire> = {}): OfferingWire {
  return {
    id: 'offering-a',
    revision: '1',
    provider_id: 'p',
    provider_name: 'P',
    model_id: 'model-a',
    model_name: 'Model A',
    capability: 'image-to-image',
    available: true,
    estimated_credits: 12,
    credits_metered: true,
    parameters: [
      { name: 'instructions', type: 'text', semantic: 'prompt', label: 'Prompt', required: true },
      { name: 'picture', type: 'asset-array', semantic: 'reference-image', label: 'Picture', max_items: 2 },
      { name: 'region', type: 'asset', semantic: 'mask', label: 'Mask', required: true },
    ],
    ...overrides,
  };
}

function renderDialog(offering: OfferingWire) {
  return render(
    <InpaintDialog
      request={{
        imageUrl: '/api/v1/assets/a1',
        source: new NodeBlob([new Uint8Array([1, 2, 3])]) as unknown as Blob,
        mask: new NodeBlob([new Uint8Array([4, 5, 6])]) as unknown as Blob,
        frame: { x: 0, y: 0, width: 10, height: 10 },
        size: { width: 10, height: 10 },
      }}
      offerings={[offering]}
      modelId="model-a"
      uploadBlob={vi.fn(async () => '/api/v1/assets/new')}
      onClose={vi.fn()}
    />,
  );
}

beforeEach(() => {
  vi.stubGlobal('React', React);
  lockState.lock = { enabled: true, text: 'ink', references: ['style-a'] };
  sessionStorage.clear();
});
afterEach(() => { cleanup(); vi.restoreAllMocks(); });

describe('inpaint dialog style lock (#187)', () => {
  it('shows the lock chip and the one-turn exemption', () => {
    renderDialog(offeringWire());
    const row = screen.getByTestId('inpaint-style-lock');
    expect(row.textContent).toContain(messages['ui.studio.style_lock.chip']);
    expect(screen.getByText(messages['ui.studio.style_lock.skip_once'])).toBeTruthy();
    const skip = screen.getByTestId('inpaint-style-lock-skip') as HTMLInputElement;
    expect(skip.checked).toBe(false);
    fireEvent.click(skip);
    expect(skip.checked).toBe(true);
    // Two references (the source picture plus the lock's one) fit this model.
    expect(screen.queryByTestId('inpaint-style-lock-notice')).toBeNull();
  });

  it('hides the chip when the session has no active lock', () => {
    lockState.lock = { enabled: false, text: 'ink', references: ['style-a'] };
    renderDialog(offeringWire());
    expect(screen.queryByTestId('inpaint-style-lock')).toBeNull();
    lockState.lock = { enabled: true, text: '', references: [] };
    cleanup();
    renderDialog(offeringWire());
    expect(screen.queryByTestId('inpaint-style-lock')).toBeNull();
  });

  it('warns that the lock references exceed the model capacity', () => {
    lockState.lock = { enabled: true, text: 'ink', references: ['style-a', 'style-b', 'style-c'] };
    renderDialog(offeringWire());
    expect(screen.getByTestId('inpaint-style-lock-notice').textContent)
      .toContain(messages['ui.studio.style_lock.references_trimmed']);
    fireEvent.click(screen.getByTestId('inpaint-style-lock-skip'));
    expect(screen.queryByTestId('inpaint-style-lock-notice')).toBeNull();
  });

  it('warns that a single-picture model cannot take the lock references', () => {
    lockState.lock = { enabled: true, text: 'ink', references: ['style-a'] };
    const single = offeringWire();
    single.parameters = single.parameters.map((parameter) =>
      parameter.semantic === 'reference-image' ? { ...parameter, type: 'asset' } : parameter);
    renderDialog(single);
    expect(screen.getByTestId('inpaint-style-lock-notice').textContent)
      .toContain(messages['ui.unavailable.reason.style_lock.model_no_reference']);
  });

  it('does not warn about references for a text-only lock', () => {
    lockState.lock = { enabled: true, text: 'ink', references: [] };
    const single = offeringWire();
    single.parameters = single.parameters.map((parameter) =>
      parameter.semantic === 'reference-image' ? { ...parameter, type: 'asset' } : parameter);
    renderDialog(single);
    expect(screen.getByTestId('inpaint-style-lock')).toBeTruthy();
    expect(screen.queryByTestId('inpaint-style-lock-notice')).toBeNull();
  });

  it('restores a frozen request only under the same lock snapshot and shows its reference note', async () => {
    const offering = offeringWire();
    const request = {
      imageUrl: '/api/v1/assets/a1',
      source: new NodeBlob([new Uint8Array([1, 2, 3])]) as unknown as Blob,
      mask: new NodeBlob([new Uint8Array([4, 5, 6])]) as unknown as Blob,
      frame: { x: 0, y: 0, width: 10, height: 10 },
      size: { width: 10, height: 10 },
    };
    const snapshot = { enabled: true, skipped: false, text: 'ink', references: ['style-a'] };
    const target = canvasTurnOutput(offering, effectiveOfferingValues(offering, {}), request.size, schema.canvas.frontend.artboard);
    const identity = inpaintSourceIdentity({
      offeringId: offering.id,
      sourceFingerprint: await contentFingerprint(request.source),
      maskFingerprint: await contentFingerprint(request.mask),
      frame: request.frame,
      size: request.size,
      target: target?.value ?? 'model-default',
      styleLock: snapshot,
    });
    savePendingDirectTask('session-a', identity, {
      preparedInputId: 'prepared-a', requestId: 'request-a', key: 'key-a', sessionId: 'session-a',
      offeringId: offering.id, prompt: 'edit', styleLock: snapshot,
      referenceNote: '风格参考图按顺序截断。',
      inpaint: { maskUrl: '/api/v1/assets/mask', maskAssetId: 'mask-a', frame: request.frame, size: request.size },
    });
    render(
      <InpaintDialog request={request} offerings={[offering]} modelId="model-a"
        uploadBlob={vi.fn(async () => '/api/v1/assets/new')} onClose={vi.fn()} />,
    );
    expect((await screen.findByTestId('inpaint-reference-note')).textContent).toContain('风格参考图按顺序截断。');
    expect(screen.getByText(messages['ui.canvas.inpaint.confirm_prepared'])).toBeTruthy();
  });

  it('keeps the lock and its exemption out of every other canvas action', () => {
    const source = readFileSync('src/components/workspace/canvas-image-actions.tsx', 'utf8');
    const backgroundRemoval = source.split('export function BackgroundRemovalDialog')[1];
    expect(backgroundRemoval).not.toContain('style_lock_skipped');
    expect(backgroundRemoval).not.toContain('useStyleLock');
  });

  it('does not restore a request prepared under a different lock text', async () => {
    const offering = offeringWire();
    const request = {
      imageUrl: '/api/v1/assets/a1',
      source: new NodeBlob([new Uint8Array([1, 2, 3])]) as unknown as Blob,
      mask: new NodeBlob([new Uint8Array([4, 5, 6])]) as unknown as Blob,
      frame: { x: 0, y: 0, width: 10, height: 10 },
      size: { width: 10, height: 10 },
    };
    const oldSnapshot = { enabled: true, skipped: false, text: 'paint', references: ['style-a'] };
    const target = canvasTurnOutput(offering, effectiveOfferingValues(offering, {}), request.size, schema.canvas.frontend.artboard);
    const oldIdentity = inpaintSourceIdentity({
      offeringId: offering.id,
      sourceFingerprint: await contentFingerprint(request.source),
      maskFingerprint: await contentFingerprint(request.mask),
      frame: request.frame, size: request.size,
      target: target?.value ?? 'model-default', styleLock: oldSnapshot,
    });
    savePendingDirectTask('session-a', oldIdentity, {
      preparedInputId: 'prepared-old', requestId: 'request-old', key: 'key-old', sessionId: 'session-a',
      offeringId: offering.id, prompt: 'edit', styleLock: oldSnapshot,
      referenceNote: 'stale note',
    });
    render(
      <InpaintDialog request={request} offerings={[offering]} modelId="model-a"
        uploadBlob={vi.fn(async () => '/api/v1/assets/new')} onClose={vi.fn()} />,
    );
    await screen.findByTestId('inpaint-style-lock');
    expect(screen.queryByTestId('inpaint-reference-note')).toBeNull();
    expect(screen.queryByText(messages['ui.canvas.inpaint.confirm_prepared'])).toBeNull();
  });
});