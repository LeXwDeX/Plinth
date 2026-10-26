import { describe, expect, it, vi } from 'vitest';

import { clipboardMarker } from '@/lib/canvas/clipboard';
import { CANVAS_ITEM_MIME } from '@/lib/workspace/canvas-drag';
import {
  composerDragKind, htmlImageSources, insertPastedText, normalizePastedText, pastedImageFiles, planComposerPaste, readPastedImages,
} from './composer-input';

const BASE = 'https://art.example.test/workspace?session=s1';

describe('composer input', () => {
  it('takes only images from the clipboard', () => {
    const png = new File(['x'], 'a.png', { type: 'image/png' });
    const text = new File(['x'], 'a.txt', { type: 'text/plain' });
    expect(pastedImageFiles([png, text])).toEqual([png]);
    expect(pastedImageFiles(null)).toEqual([]);
  });
  it('tells a result card from files and ignores text drags', () => {
    expect(composerDragKind([CANVAS_ITEM_MIME, 'text/uri-list'])).toBe('card');
    expect(composerDragKind(['Files'])).toBe('files');
    expect(composerDragKind(['text/plain'])).toBeNull();
  });
});

describe('a paste into the composer (#281)', () => {
  it('leaves plain text to the box: the copied words arrive as they were', () => {
    const html = '<span style="white-space: pre-wrap">第一行文字\n第二行文字</span>';
    expect(planComposerPaste({ files: [], text: '第一行文字\n第二行文字', html, baseURL: BASE })).toEqual({
      files: [], imageUrls: [], text: '第一行文字\n第二行文字', handled: false,
    });
  });

  it('never writes a canvas marker as words', () => {
    const plan = planComposerPaste({ files: [], text: clipboardMarker('8a2aae8b'), html: '', baseURL: BASE });
    expect(plan).toEqual({ files: [], imageUrls: [], text: '', handled: true });
  });

  it('splits chat text with pictures into prompt words and one attachment per picture', () => {
    const html = [
      '<div><span>图片任务已完成，可以继续告诉我需要调整的地方。</span>',
      '<ul><li><button><img src="/api/v1/tasks/t1/assets/a1?size=thumb&amp;v=2" alt="生成图片 1"></button></li>',
      // The same picture twice (thumbnail and an enlarged copy) is attached once.
      "<li><img alt='生成图片 1' src='/api/v1/tasks/t1/assets/a1?size=thumb&amp;v=2'></li>",
      '<li><img src=https://cdn.example.test/b.png></li></ul></div>',
    ].join('');
    const plan = planComposerPaste({ files: [], text: '图片任务已完成，可以继续告诉我需要调整的地方。\r\n\r\n\r\n  ', html, baseURL: BASE });
    expect(plan.handled).toBe(true);
    expect(plan.text).toBe('图片任务已完成，可以继续告诉我需要调整的地方。');
    expect(plan.imageUrls).toEqual([
      'https://art.example.test/api/v1/tasks/t1/assets/a1?size=thumb&v=2',
      'https://cdn.example.test/b.png',
    ]);
    expect(plan.files).toEqual([]);
  });

  it('lets picture files win over the <img> copies of the same pictures, so nothing is attached twice', () => {
    const png = new File(['x'], 'image.png', { type: 'image/png' });
    const plan = planComposerPaste({ files: [png], text: '', html: '<img src="https://example.test/image.png">', baseURL: BASE });
    expect(plan).toEqual({ files: [png], imageUrls: [], text: '', handled: true });
  });

  it('keeps words that come with a picture file, but not the file name a file manager adds', () => {
    const png = new File(['x'], 'shot.png', { type: 'image/png' });
    expect(planComposerPaste({ files: [png], text: '把背景换成夜景', html: '', baseURL: BASE }).text).toBe('把背景换成夜景');
    expect(planComposerPaste({ files: [png], text: 'shot.png', html: '', baseURL: BASE }).text).toBe('');
  });

  it('reads only picture addresses a page can load', () => {
    const html = [
      '<img src="javascript:alert(1)">', '<img src="file:///etc/passwd">', '<img src="data:text/html;base64,AAAA">',
      '<img data-src="/lazy.png">', '<img srcset="/a.png 2x">', '<img src="">',
      '<img src="data:image/png;base64,iVBORw0KGgo=">', '<img src="blob:https://art.example.test/1234">',
    ].join('');
    expect(htmlImageSources(html, BASE)).toEqual(['data:image/png;base64,iVBORw0KGgo=', 'blob:https://art.example.test/1234']);
    expect(htmlImageSources('', BASE)).toEqual([]);
  });

  it('tidies line breaks without touching the words', () => {
    expect(normalizePastedText('a  \r\nb c\n\n\n\nd\t\n')).toBe('a\nb c\n\nd');
  });

  it('puts the words over the selection and the caret after them', () => {
    expect(insertPastedText('前后', 1, 1, '中')).toEqual({ value: '前中后', caret: 2 });
    expect(insertPastedText('替换这里', 2, 4, '那里')).toEqual({ value: '替换那里', caret: 4 });
    expect(insertPastedText('', 5, 9, '文字')).toEqual({ value: '文字', caret: 2 });
  });
});

describe('reading pasted pictures for the upload path', () => {
  const picture = () => new Response(new Blob(['png'], { type: 'image/png' }), { status: 200 });

  it('turns readable pictures into files in order, with a name from the address', async () => {
    const fetcher = vi.fn(async () => picture());
    const { files, failed } = await readPastedImages(['https://art.example.test/api/v1/assets/a1', 'data:image/png;base64,AAAA'], fetcher);
    expect(failed).toBe(0);
    expect(files.map((file) => [file.name, file.type])).toEqual([['a1.png', 'image/png'], ['pasted-2.png', 'image/png']]);
    expect(fetcher).toHaveBeenCalledWith('https://art.example.test/api/v1/assets/a1', { credentials: 'same-origin' });
    // The page's CSP connect-src has no data:, so a pasted data: picture is decoded in the page (#187 A).
    expect(fetcher).toHaveBeenCalledTimes(1);
  });

  it('counts pictures it cannot read instead of failing the paste', async () => {
    const fetcher = vi.fn(async (url: string) => {
      if (url.endsWith('denied')) return new Response('', { status: 403 });
      if (url.endsWith('page')) return new Response(new Blob(['<html>'], { type: 'text/html' }), { status: 200 });
      if (url.endsWith('offline')) throw new TypeError('Failed to fetch');
      return picture();
    });
    const { files, failed } = await readPastedImages(['https://x.test/denied', 'https://x.test/page', 'https://x.test/offline', 'https://x.test/ok.webp'], fetcher);
    expect(files.map((file) => file.name)).toEqual(['ok.webp']);
    expect(failed).toBe(3);
  });
});
