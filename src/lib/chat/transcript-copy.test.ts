import { describe, expect, it } from 'vitest';

import { htmlImageSources, planComposerPaste } from './composer-input';
import { joinTranscriptPieces, transcriptClipboard } from './transcript-copy';

const BASE = 'https://art.example.test/workspace';

describe('a copy from the chat transcript (#281)', () => {
  it('leaves a copy without pictures to the browser', () => {
    expect(transcriptClipboard('第一行文字\n第二行文字', [])).toBeNull();
    expect(transcriptClipboard('文字', [''])).toBeNull();
  });

  it('carries the words as text and the words plus each picture once as HTML', () => {
    const picture = 'https://art.example.test/api/v1/tasks/t1/assets/a1?size=thumb&v=2';
    const payload = transcriptClipboard('改成 <夜景> & "雪"\n第二行', [picture, picture]);
    expect(payload?.plain).toBe('改成 <夜景> & "雪"\n第二行');
    expect(payload?.html).toBe(`<div>改成 &lt;夜景&gt; &amp; &quot;雪&quot;<br>第二行</div><img src="${picture.replace('&', '&amp;')}">`);
    expect(htmlImageSources(payload!.html, BASE)).toEqual([picture]);
  });

  it('pastes into the composer as prompt words and attachments', () => {
    const payload = transcriptClipboard('图片任务已完成。', ['https://art.example.test/api/v1/assets/a1', 'https://art.example.test/api/v1/assets/a2']);
    const plan = planComposerPaste({ files: [], text: payload!.plain, html: payload!.html, baseURL: BASE });
    expect(plan).toEqual({
      files: [], handled: true, text: '图片任务已完成。',
      imageUrls: ['https://art.example.test/api/v1/assets/a1', 'https://art.example.test/api/v1/assets/a2'],
    });
  });

  it('works for pictures selected without words', () => {
    expect(transcriptClipboard('', ['blob:https://art.example.test/1'])).toEqual({ plain: '', html: '<img src="blob:https://art.example.test/1">' });
  });
});

describe('a copy that takes in whole Agent replies (#333)', () => {
  it('puts each whole reply on the clipboard as its Markdown source, the rest as the browser reads it', () => {
    expect(joinTranscriptPieces([
      { text: '\n用户：说说步骤\n' },
      { source: '**步骤**\n\n1. 第一步\n2. `code`' },
      { text: ' 重新生成 ' },
      { source: '[文档](https://example.com)' },
      { text: '' },
    ])).toBe('用户：说说步骤\n**步骤**\n\n1. 第一步\n2. `code`\n重新生成\n[文档](https://example.com)');
  });

  it('carries such a copy even without pictures, the words escaped in its HTML', () => {
    expect(transcriptClipboard('**a** <b>', [], true)).toEqual({ plain: '**a** <b>', html: '<div>**a** &lt;b&gt;</div>' });
    expect(transcriptClipboard('**a**', [])).toBeNull();
  });
});
