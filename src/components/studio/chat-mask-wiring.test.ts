import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

function source(relative: string): string {
  return readFileSync(new URL(relative, import.meta.url), 'utf8');
}

const chat = source('./chat-column.tsx');
const composer = source('./composer.tsx');
const browserClient = source('../../lib/chat/browser-client.ts');
const sendQueue = source('../../lib/chat/send-queue.ts');

// #244: the painted selection is uploaded on the same delivery pixels as the artboard snapshot and travels as the
// turn's frozen selectionMask; a queued turn keeps it and only an accepted turn spends the draft.
describe('the painted selection travels with a canvas turn', () => {
  it('encodes the mask asset under the Schema field name', () => {
    expect(browserClient).toContain('if (options.selectionMask && fields.selection_mask)');
    expect(browserClient).toContain('body[fields.selection_mask] = { maskAssetId: options.selectionMask.maskAssetId };');
  });

  it('rasterises the mask with the snapshot geometry and uploads it as an input asset', () => {
    expect(chat).toContain('const geometry = inpaintGeometry(artboard, deliveryPixels);');
    expect(chat).toContain('paintMask(maskContext, geometry, maskDraftNow.strokes, \'#000000\');');
    expect(chat).toContain('selectionMask = { maskAssetId };');
    expect(chat).toContain("const source = namesBoard || withMask ? 'canvas' : currentObjectSource(canvasState.getNodes(sessionId), artboard);");
    expect(chat).toContain('...(selectionMask ? { selectionMask } : {}),');
  });

  it('keeps the mask on a queued turn and clears the draft only after acceptance', () => {
    expect(sendQueue).toContain('selectionMask?: { maskAssetId: string };');
    expect(chat).toContain('...(turn.selectionMask ? { selectionMask: turn.selectionMask } : {})');
    const accepted = chat.slice(chat.indexOf('sent = true;'), chat.indexOf('await new Promise<void>((resolve, reject)'));
    expect(accepted.indexOf('sameMaskDraft(useMaskStore.getState().draft, maskDraftNow)'))
      .toBeGreaterThan(0);
    const queued = chat.slice(chat.indexOf('if (turnRunning) {'), chat.indexOf('const skillId = choiceAnswer'));
    expect(queued).toContain('sameMaskDraft(useMaskStore.getState().draft, maskDraftNow)');
  });
});

describe('the mask chip says what sending it does', () => {
  it('uses the Schema subtitle that sending makes it the turn input (#244)', () => {
    expect(composer).toContain("ui.studio.mask.chip_selection");
  });
});