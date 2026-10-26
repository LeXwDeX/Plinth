import { describe, expect, it } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import type { ProductSchema } from '@/lib/product-schema';
import { activePicturePosition, messageTaskIds, modelCardsOf, modelCover, modelTaskIdsOf, pictureKey, splitMessagePictures } from '@/lib/chat/message-pictures';
import type { ChatModelResult } from '@/lib/chat/model-results';
import { domainOfCapability } from '@/lib/workspace/versions';
import type { ChatAttachment, ChatImage } from '@/store/chat-store';

const image = (n: number, taskId: string): ChatImage => ({ url: `/api/v1/tasks/${taskId}/assets/p${n}`, taskId });
const model = (taskId: string, imageSlot?: number, handle = '@model1'): ChatAttachment => ({ handle, kind: 'model', taskId, ...(imageSlot ? { imageSlot } : {}) });
const picture = (handle: string, taskId: string, imageSlot: number): ChatAttachment => ({ handle, kind: 'image', taskId, imageSlot });
const result = (taskId: string): ChatModelResult => ({
  taskId, capability: 'text-to-3d', prompt: '',
  selection: { taskId, kind: 'model', url: `/api/v1/tasks/${taskId}/assets/glb`, assetId: 'glb', sessionId: 's' },
});

describe('splitMessagePictures', () => {
  it('keeps the 2D candidates of a plain image message in order with their message indexes', () => {
    const images = [image(1, 't1'), image(2, 't1'), image(3, 't1')];
    const { candidates, previews } = splitMessagePictures({ images, attachments: [picture('@img1', 't1', 1), picture('@img2', 't1', 2)] });
    expect(previews).toEqual([]);
    expect(candidates).toEqual(images.map((item, index) => ({ image: item, index })));
  });

  it('takes the picture at a model attachment slot and the pre-model picture of the same task as previews', () => {
    // An older Tripo message: slot 1 is the model's render, slot 2 an image attachment of the same task.
    const images = [image(1, 't3d'), image(2, 't3d')];
    const { candidates, previews } = splitMessagePictures({ images, attachments: [model('t3d', 1), picture('@img1', 't3d', 2)] });
    expect(candidates).toEqual([]);
    expect(previews.map((item) => item.index)).toEqual([0, 1]);
  });

  it('treats a new Tripo message, whose second picture has no attachment, the same way', () => {
    const images = [image(1, 't3d'), image(2, 't3d')];
    const { candidates, previews } = splitMessagePictures({ images, attachments: [model('t3d', 1)] });
    expect(candidates).toEqual([]);
    expect(previews.map((item) => item.index)).toEqual([0, 1]);
  });

  it('treats a new 3D message, which carries only the cover, as one preview and no candidate', () => {
    // The server now sends one picture per 3D task: the render, named by the model attachment.
    const images = [image(1, 't3d')];
    const { candidates, previews } = splitMessagePictures({ images, attachments: [model('t3d', 1)] });
    expect(candidates).toEqual([]);
    expect(previews).toEqual([{ image: images[0], index: 0 }]);
  });

  it('leaves a 2D task of the same message as a candidate that keeps its original index', () => {
    const images = [image(1, 't3d'), image(2, 't3d'), image(3, 't2d')];
    const { candidates, previews } = splitMessagePictures({ images, attachments: [model('t3d', 1), picture('@img1', 't2d', 3)] });
    expect(previews.map((item) => item.index)).toEqual([0, 1]);
    expect(candidates).toEqual([{ image: images[2], index: 2 }]);
  });

  it('knows a 3D task from the tracker when the message has no attachments yet', () => {
    const images = [image(1, 't3d'), image(2, 't2d')];
    const split = splitMessagePictures({ images }, ['t3d']);
    expect(split.previews.map((item) => item.image.taskId)).toEqual(['t3d']);
    expect(split.candidates.map((item) => item.image.taskId)).toEqual(['t2d']);
    // Without that knowledge nothing says the picture belongs to a model.
    expect(splitMessagePictures({ images }).previews).toEqual([]);
  });

  it('never matches a picture without a task to a model task', () => {
    const images = [image(1, '')];
    expect(splitMessagePictures({ images, attachments: [model('')] }, ['']).previews).toEqual([]);
  });

  it('handles a message with no pictures', () => {
    expect(splitMessagePictures({})).toEqual({ candidates: [], previews: [] });
  });
});

describe('modelCover', () => {
  const images = [image(1, 't3d'), image(2, 't3d'), image(3, 't3d')];
  const previews = images.map((item, index) => ({ image: item, index }));

  it('is the picture at the model attachment slot', () => {
    expect(modelCover({ attachments: [model('t3d', 2)] }, previews, 't3d')?.index).toBe(1);
  });

  it('falls back to the first preview of the task', () => {
    expect(modelCover({ attachments: [model('t3d')] }, previews, 't3d')?.index).toBe(0);
    expect(modelCover({}, previews, 't3d')?.index).toBe(0);
  });

  it('is empty when the task has no preview', () => {
    expect(modelCover({}, previews, 'other')).toBeNull();
    expect(modelCover({ attachments: [model('t3d', 1)] }, [], 't3d')).toBeNull();
  });
});

describe('modelCardsOf', () => {
  it('makes a card of every finished model with its cover and handle', () => {
    const previews = [{ image: image(1, 't3d'), index: 0 }, { image: image(2, 't3d'), index: 1 }];
    const cards = modelCardsOf({ attachments: [model('t3d', 1, '@model4')] }, previews, [result('t3d')]);
    expect(cards).toEqual([{ taskId: 't3d', result: result('t3d'), cover: previews[0], handle: '@model4' }]);
  });

  it('keeps pictures of a model the tracker cannot show yet as a card without a model', () => {
    // The session was opened from history and only its latest tasks are tracked, or the task is not ready.
    const previews = [{ image: image(1, 'old'), index: 0 }, { image: image(2, 'old'), index: 1 }];
    const cards = modelCardsOf({ attachments: [model('old', 1)] }, previews, []);
    expect(cards).toEqual([{ taskId: 'old', result: null, cover: previews[0], handle: '@model1' }]);
  });

  it('gives a finished model with no picture a card without a cover, and one card per task', () => {
    const previews = [{ image: image(1, 'b'), index: 0 }, { image: image(2, 'b'), index: 1 }];
    const cards = modelCardsOf({}, previews, [result('a'), result('b')]);
    expect(cards.map((card) => [card.taskId, card.result?.taskId ?? null, card.cover?.index ?? null])).toEqual([['a', 'a', null], ['b', 'b', 0]]);
  });

  it('gives a new 3D message with only the cover one card with that cover and the model handle', () => {
    const previews = [{ image: image(1, 't3d'), index: 0 }];
    expect(modelCardsOf({ attachments: [model('t3d', 1, '@model2')] }, previews, [result('t3d')])).toEqual([
      { taskId: 't3d', result: result('t3d'), cover: previews[0], handle: '@model2' },
    ]);
  });

  it('never makes a card for an empty task id', () => {
    const previews = [{ image: image(1, ''), index: 0 }];
    expect(modelCardsOf({}, previews, [])).toEqual([]);
    // A model attachment without a task does not name a card either.
    expect(modelCardsOf({ attachments: [model('', 1)] }, previews, [])).toEqual([]);
  });

  it('gives a preview without a task to the card whose model attachment sits at its slot, else drops it', () => {
    const previews = [{ image: image(1, ''), index: 0 }, { image: image(2, ''), index: 1 }];
    // Slot 1 belongs to t3d's model: the card for it covers with that picture. Slot 2 has no model: no card, not a candidate either.
    const cards = modelCardsOf({ attachments: [model('t3d', 1)] }, previews, []);
    expect(cards).toEqual([{ taskId: 't3d', result: null, cover: previews[0], handle: '@model1' }]);
    // The same holds when the tracker has the model already.
    expect(modelCardsOf({ attachments: [model('t3d', 1)] }, previews, [result('t3d')])).toEqual([
      { taskId: 't3d', result: result('t3d'), cover: previews[0], handle: '@model1' },
    ]);
  });
});

describe('messageTaskIds', () => {
  it('lists the tasks of the tool calls, the paid steps and the pictures once each, without empty ids', () => {
    expect(messageTaskIds({
      taskIds: ['a', 'b'],
      paidSteps: [{ taskId: 'b', credits: 1, approvedByPolicy: false }, { taskId: 'c', credits: 1, approvedByPolicy: false }],
      images: [image(1, 'c'), image(2, 'd'), image(3, '')],
    })).toEqual(['a', 'b', 'c', 'd']);
    expect(messageTaskIds({})).toEqual([]);
  });

  it('knows the task of a streaming message, which has only pictures', () => {
    expect(messageTaskIds({ images: [image(1, 't3d'), image(2, 't3d')] })).toEqual(['t3d']);
  });
});

describe('modelTaskIdsOf', () => {
  const tracked = (taskId: string, capability: string, sessionId: string | undefined = 's') => ({ taskId, capability, sessionId });
  const isModel = (capability: string) => capability === 'text-to-3d';

  it('adds the tracked 3D tasks of this message and session to the finished results', () => {
    const message = { taskIds: ['pending3d', 'img2d', 'done3d'], images: [] as ChatImage[] };
    const tasks = [tracked('pending3d', 'text-to-3d'), tracked('img2d', 'text-to-image'), tracked('done3d', 'text-to-3d')];
    expect(modelTaskIdsOf(message, [result('done3d')], tasks, 's', isModel).sort()).toEqual(['done3d', 'pending3d']);
  });

  it('leaves out tasks of other messages and other sessions, and uses only results without a session', () => {
    const message = { taskIds: ['mine'] };
    const tasks = [tracked('other', 'text-to-3d'), tracked('mine', 'text-to-3d', 'elsewhere')];
    expect(modelTaskIdsOf(message, [], tasks, 's', isModel)).toEqual([]);
    // No session in view: nothing from the tracker counts (a task with no session would otherwise match), only finished results.
    const unowned = [{ taskId: 'mine', capability: 'text-to-3d', sessionId: undefined }];
    expect(modelTaskIdsOf(message, [result('r')], unowned, undefined, isModel)).toEqual(['r']);
    expect(modelTaskIdsOf(message, [result('r')], unowned, null, isModel)).toEqual(['r']);
  });

  it('keeps the pictures of a streaming Tripo message off the 2D candidates before the model is downloadable', () => {
    // The images event arrived with the task id; the task is tracked but has no model file yet, and the message has no attachments.
    const images = [image(1, 't3d'), image(2, 't3d'), image(3, 't2d')];
    const message = { images, attachments: [] };
    const tasks = [tracked('t3d', 'text-to-3d'), tracked('t2d', 'text-to-image')];
    const split = splitMessagePictures(message, modelTaskIdsOf(message, [], tasks, 's', isModel));
    expect(split.previews.map((item) => item.index)).toEqual([0, 1]);
    expect(split.candidates.map((item) => item.index)).toEqual([2]);
    // And the card exists, without a model yet.
    expect(modelCardsOf(message, split.previews, []).map((card) => [card.taskId, card.result, card.cover?.index])).toEqual([['t3d', null, 0]]);
  });

  it('reads 3D from the Product Schema capability domains', () => {
    const schema = structuredClone(defaultCatalog.application) as ProductSchema;
    const isModelCapability = (capability: string) => domainOfCapability(schema, capability) === 'model';
    const tasks = ['text-to-3d', 'image-to-3d', 'remesh', 'text-to-image', 'image-to-image', 'unknown-capability'].map((capability) => tracked(`t-${capability}`, capability));
    const message = { taskIds: tasks.map((task) => task.taskId) };
    expect(modelTaskIdsOf(message, [], tasks, 's', isModelCapability).sort()).toEqual(['t-image-to-3d', 't-remesh', 't-text-to-3d']);
  });
});

describe('activePicturePosition', () => {
  const at = (index: number, url = `/api/v1/tasks/t/assets/a${index}`) => ({ image: { url, taskId: 't' }, index });
  it('keeps the chosen picture when the card re-sorts it (a four-view group arriving after the message)', () => {
    const arrival = [at(0), at(1), at(2), at(3)];
    const chosen = pictureKey(arrival[1]);
    expect(activePicturePosition(arrival, chosen, -1)).toBe(1);
    const slotOrder = [arrival[2], arrival[3], arrival[1], arrival[0]];
    expect(activePicturePosition(slotOrder, chosen, -1)).toBe(2);
  });
  it('falls back to the picked picture, then the first, when nothing (or a vanished picture) was chosen', () => {
    const pictures = [at(0), at(1), at(2)];
    expect(activePicturePosition(pictures, null, 2)).toBe(2);
    expect(activePicturePosition(pictures, pictureKey(at(9)), 1)).toBe(1);
    expect(activePicturePosition(pictures, null, -1)).toBe(0);
    expect(activePicturePosition([], null, -1)).toBe(0);
  });
});
