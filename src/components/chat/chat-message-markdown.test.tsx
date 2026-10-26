import React from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import { ChatMessage } from '@/components/chat/chat-message';
import type { ComposerReference } from '@/lib/chat/composer-reference';
import type { ChatMessage as ChatMessageType } from '@/store/chat-store';

const messages = (defaultCatalog.application as { content: { messages: Record<string, string> } }).content.messages;

vi.mock('@/components/product-schema-provider', () => ({
  useProductSchema: () => ({ schema: defaultCatalog.application, text: (key: string) => messages[key] ?? '' }),
}));
vi.mock('@/hooks/use-asset-url-rewriter', () => ({
  useAssetURLRewriter: () => ({ proxyAssetUrl: (url: string) => url, previewAssetUrl: (url: string) => url }),
}));

const reply = (content: string, extra: Partial<ChatMessageType> = {}): ChatMessageType => ({
  id: 'm1', role: 'assistant', content, createdAt: 1, ...extra,
});
const render = (message: ChatMessageType, references?: ReadonlyMap<string, ComposerReference>) =>
  renderToStaticMarkup(<ChatMessage message={message} sessionId="s" handleReferences={references} />);
/** The rendered words of the message, without its action row. */
const body = (html: string) => html.match(/<div data-chat-markdown[\s\S]*?(?=<div[^>]*data-testid="chat-message-actions"|$)/)?.[0] ?? html;

describe('an Agent reply in Markdown (#333)', () => {
  it('shows bold, italic, lists, inline code and code blocks instead of their marks', () => {
    const html = body(render(reply('这是 **粗体**、*斜体* 和 `code`\n\n1. 第一步\n2. 第二步\n   - 子项\n\n```go\nfmt.Println("**x**")\n```')));
    expect(html).toContain('<strong class="font-semibold">粗体</strong>');
    expect(html).toContain('<em>斜体</em>');
    expect(html).toMatch(/<code class="[^"]*font-mono[^"]*">code<\/code>/);
    expect(html).toMatch(/<ol class="[^"]*list-decimal[^"]*"><li[^>]*><p[^>]*>第一步<\/p><\/li><li[^>]*><p[^>]*>第二步<\/p><ul class="[^"]*list-disc[^"]*"><li[^>]*><p[^>]*>子项<\/p><\/li><\/ul><\/li><\/ol>/);
    expect(html).toMatch(/<pre[^>]*><code>fmt.Println\(&quot;\*\*x\*\*&quot;\)<\/code><\/pre>/);
    expect(html.replace(/<pre[\s\S]*?<\/pre>/, '')).not.toContain('**');
  });

  it('opens http(s) links in a new window without the opener', () => {
    const html = body(render(reply('看 [文档](https://example.com/docs?a=1&b=2)')));
    expect(html).toContain('<a href="https://example.com/docs?a=1&amp;b=2" target="_blank" rel="noopener noreferrer"');
    expect(html).toContain('>文档</a>');
  });

  it('shows script, HTML tags and entities as text and never makes an element of them', () => {
    const html = body(render(reply('<script>window.x=1</script> <img src=x onerror="window.x=2"> <b>b</b> &lt;i&gt;entity&lt;/i&gt; &#x3C;svg onload=1&#x3E; &amp;')));
    expect(html).not.toMatch(/<script|<img|<b>|<i>|<svg/);
    expect(html).toContain('&lt;script&gt;window.x=1&lt;/script&gt;');
    expect(html).toContain('&lt;img src=x onerror=&quot;window.x=2&quot;&gt;');
    expect(html).toContain('&amp;lt;i&amp;gt;entity&amp;lt;/i&amp;gt;');
    expect(html).toContain('&amp;#x3C;svg onload=1&amp;#x3E;');
  });

  it('drops javascript:, data: and other non-http(s) links to their words, and shows no pictures', () => {
    const html = body(render(reply([
      '[js](javascript:alert(1))', '[JS](JaVaScRiPt:alert(1))', '[data](data:text/html;base64,PHNjcmlwdD5hbGVydCgxKTwvc2NyaXB0Pg==)',
      '[vb](vbscript:msgbox(1))', '[proto](//evil.example/x)', '[rel](/api/v1/me)', '![pic](https://example.com/x.png)',
      '<javascript:alert(1)> https://example.com/plain',
    ].join('\n\n'))));
    expect(html).not.toContain('<a ');
    expect(html).not.toContain('<img');
    expect(html).not.toMatch(/href=|src=/);
    for (const words of ['js', 'JS', 'data', 'vb', 'proto', 'rel', 'pic']) expect(html).toContain(`>${words}</p>`);
    expect(html).toContain('&lt;javascript:alert(1)&gt; https://example.com/plain');
  });

  it('keeps handles as tags inside Markdown, but not inside code or link words', () => {
    const references = new Map<string, ComposerReference>([
      ['@img1', { handle: '@img1', url: '/api/v1/tasks/t1/assets/a1', taskId: 't1', assetId: 'a1' }],
      ['@model2', { handle: '@model2', kind: 'model', url: '' }],
    ]);
    const html = body(render(reply('**基于 @img1 和 [@model2](https://example.com/m) 以及 `@img1`**\n\n- *@model2* 的 _说明_'), references));
    expect(html).toMatch(/<strong class="font-semibold"><span>基于 <\/span><span[^>]*><button[^>]*data-handle="@img1"/);
    expect(html.match(/data-testid="chat-text-handle"/g)).toHaveLength(2);
    expect(html).toMatch(/<a [^>]*>@model2<\/a>/);
    expect(html).toMatch(/<code[^>]*>@img1<\/code>/);
    expect(html).toMatch(/<em><span[^>]*><button[^>]*data-handle="@model2"/);
  });

  it('keeps a user message as the plain text it was typed', () => {
    const html = render(reply('**不是粗体** `x` [a](https://example.com) <b>b</b>', { role: 'user' }));
    expect(html).not.toMatch(/<strong|<code|<a |<b>/);
    expect(html).toContain('**不是粗体** `x` [a](https://example.com) &lt;b&gt;b&lt;/b&gt;');
    expect(html).not.toContain('data-chat-markdown');
  });

  it('formats an unclosed bold while the reply streams, and shows it as written once complete', () => {
    const streaming = render(reply('这是 **重', { isStreaming: true }));
    expect(streaming).toContain('这是 <strong class="font-semibold">重</strong>');
    expect(streaming).not.toContain('**');
    // the spinner stays at the end of the words
    expect(streaming).toMatch(/<strong class="font-semibold">重<\/strong><span[^>]*aria-label="[^"]+"/);
    const settled = render(reply('这是 **重'));
    expect(settled).toContain('这是 **重');
    expect(settled).not.toContain('<strong');
  });

  it('keeps the hidden server annotation out of the rendered reply', () => {
    const prefix = (defaultCatalog.application as { chat: { frontend: { legacy_hidden_content_prefix: string; legacy_hidden_content_suffix: string } } }).chat.frontend;
    const html = render(reply(`**完成**\n${prefix.legacy_hidden_content_prefix}https://x/y.png${prefix.legacy_hidden_content_suffix}`));
    expect(html).toContain('<strong class="font-semibold">完成</strong>');
    expect(html).not.toContain('https://x/y.png');
  });
});
