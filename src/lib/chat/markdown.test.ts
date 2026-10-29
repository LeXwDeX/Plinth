import { describe, expect, it } from 'vitest';

import { parseMarkdown, safeLinkHref, type MarkdownBlock, type MarkdownInline } from './markdown';

const text = (value: string): MarkdownInline => ({ type: 'text', text: value });
const paragraph = (...children: MarkdownInline[]): MarkdownBlock => ({ type: 'paragraph', children });
const strong = (...children: MarkdownInline[]): MarkdownInline => ({ type: 'strong', children });
const em = (...children: MarkdownInline[]): MarkdownInline => ({ type: 'em', children });
const code = (value: string): MarkdownInline => ({ type: 'code', text: value });

describe('the chat Markdown subset (#333)', () => {
  it('reads bold, italic and inline code', () => {
    expect(parseMarkdown('这是 **粗体**、*斜体*、__粗__、_斜_ 和 `a <b> c`')).toEqual([
      paragraph(text('这是 '), strong(text('粗体')), text('、'), em(text('斜体')), text('、'), strong(text('粗')), text('、'), em(text('斜')), text(' 和 '), code('a <b> c')),
    ]);
    expect(parseMarkdown('***两者***')).toEqual([paragraph(em(strong(text('两者'))))]);
    expect(parseMarkdown('**a *b* c**')).toEqual([paragraph(strong(text('a '), em(text('b')), text(' c')))]);
  });

  it('closes bold after CJK punctuation, as models write it', () => {
    expect(parseMarkdown('**注意：**后面的话')).toEqual([paragraph(strong(text('注意：')), text('后面的话'))]);
    expect(parseMarkdown('中文**（括号）**中文')).toEqual([paragraph(text('中文'), strong(text('（括号）')), text('中文'))]);
  });

  it('keeps snake_case, lone stars, escapes and unclosed runs as written', () => {
    expect(parseMarkdown('snake_case_name 2 * 3 = 6')).toEqual([paragraph(text('snake_case_name 2 * 3 = 6'))]);
    expect(parseMarkdown('\\*not italic\\* and **open')).toEqual([paragraph(text('*not italic* and **open'))]);
    expect(parseMarkdown('`unclosed code')).toEqual([paragraph(text('`unclosed code'))]);
  });

  it('keeps line breaks inside a paragraph and splits paragraphs on blank lines', () => {
    expect(parseMarkdown('第一行\n第二行\n\n  第二段')).toEqual([paragraph(text('第一行\n第二行')), paragraph(text('第二段'))]);
  });

  it('reads ordered and unordered lists, nested ones included', () => {
    expect(parseMarkdown('说明：\n1. 第一步 **加粗**\n2. 第二步\n   - 子项 a\n   - 子项 b\n3. 第三步\n\n- 无序\n* 星号')).toEqual([
      paragraph(text('说明：')),
      {
        type: 'list', ordered: true, start: 1, items: [
          [paragraph(text('第一步 '), strong(text('加粗')))],
          [paragraph(text('第二步')), { type: 'list', ordered: false, start: 1, items: [[paragraph(text('子项 a'))], [paragraph(text('子项 b'))]] }],
          [paragraph(text('第三步'))],
        ],
      },
      { type: 'list', ordered: false, start: 1, items: [[paragraph(text('无序'))], [paragraph(text('星号'))]] },
    ]);
    expect(parseMarkdown('3. c\n4. d')).toEqual([{ type: 'list', ordered: true, start: 3, items: [[paragraph(text('c'))], [paragraph(text('d'))]] }]);
    expect(parseMarkdown('1. a\n  - b')).toEqual([{
      type: 'list', ordered: true, start: 1,
      items: [[paragraph(text('a')), { type: 'list', ordered: false, start: 1, items: [[paragraph(text('b'))]] }]],
    }]);
  });

  it('reads fenced code blocks literally, an unclosed fence running to the end', () => {
    expect(parseMarkdown('前\n```go\nfmt.Println("**x**")\n<b>y</b>\n```\n后')).toEqual([
      paragraph(text('前')),
      { type: 'code', language: 'go', text: 'fmt.Println("**x**")\n<b>y</b>' },
      paragraph(text('后')),
    ]);
    expect(parseMarkdown('~~~\na\n\nb')).toEqual([{ type: 'code', language: '', text: 'a\n\nb' }]);
  });

  it('reads headings as strong lines and a rule as a rule', () => {
    expect(parseMarkdown('### 步骤\n---\n正文')).toEqual([
      { type: 'heading', level: 3, children: [text('步骤')] },
      { type: 'rule' },
      paragraph(text('正文')),
    ]);
  });

  it('links only http and https, in a normalized form', () => {
    expect(parseMarkdown('看 [文档](https://example.com/a?b=1&c=2) 吧')).toEqual([
      paragraph(text('看 '), { type: 'link', href: 'https://example.com/a?b=1&c=2', children: [text('文档')] }, text(' 吧')),
    ]);
    expect(parseMarkdown('[**粗** `c`](http://example.com/(x))')).toEqual([
      paragraph({ type: 'link', href: 'http://example.com/(x)', children: [strong(text('粗')), text(' '), code('c')] }),
    ]);
    for (const url of ['javascript:alert(1)', 'JaVaScRiPt:alert(1)', 'data:text/html;base64,PHNjcmlwdD4=', 'vbscript:x', '//evil.example', '/relative', 'mailto:a@b.c', 'https:alert(1)']) {
      expect(safeLinkHref(url), url).toBeNull();
      expect(parseMarkdown(`[点我](${url})`), url).toEqual([paragraph(text('点我'))]);
    }
    expect(safeLinkHref(' https://example.com ')).toBe('https://example.com/');
  });

  it('never makes pictures or HTML: they stay text', () => {
    expect(parseMarkdown('![alt **x**](https://example.com/x.png)')).toEqual([paragraph(text('alt '), strong(text('x')))]);
    expect(parseMarkdown('<script>alert(1)</script> <img src=x onerror=alert(1)> &lt;b&gt;')).toEqual([
      paragraph(text('<script>alert(1)</script> <img src=x onerror=alert(1)> &lt;b&gt;')),
    ]);
    expect(parseMarkdown('<https://example.com>')).toEqual([paragraph(text('<https://example.com>'))]);
  });

  it('while streaming, shows an unclosed tail formatted instead of its raw marks', () => {
    const streaming = { streaming: true };
    expect(parseMarkdown('这是 **重', streaming)).toEqual([paragraph(text('这是 '), strong(text('重')))]);
    expect(parseMarkdown('这是 **', streaming)).toEqual([paragraph(text('这是 '))]);
    expect(parseMarkdown('这是 **重点*', streaming)).toEqual([paragraph(text('这是 '), strong(text('重点')))]);
    expect(parseMarkdown('这是 **重点**', streaming)).toEqual([paragraph(text('这是 '), strong(text('重点')))]);
    expect(parseMarkdown('a `co', streaming)).toEqual([paragraph(text('a '), code('co'))]);
    expect(parseMarkdown('见 [文档](https://exa', streaming)).toEqual([paragraph(text('见 文档'))]);
    // only the tail is unfinished: an earlier paragraph keeps its literal marks
    expect(parseMarkdown('**open\n\n下一段 **粗', streaming)).toEqual([paragraph(text('**open')), paragraph(text('下一段 '), strong(text('粗')))]);
    expect(parseMarkdown('1. **步', streaming)).toEqual([{ type: 'list', ordered: true, start: 1, items: [[paragraph(strong(text('步')))]] }]);
  });

  it('stays linear enough on long or deeply nested input', () => {
    const long = '**a** *b* `c` '.repeat(4000);
    const started = Date.now();
    expect(parseMarkdown(long).length).toBe(1);
    const nested = Array.from({ length: 200 }, (_, depth) => `${' '.repeat(depth * 2)}- item`).join('\n');
    expect(parseMarkdown(nested).length).toBe(1);
    expect(Date.now() - started).toBeLessThan(2000);
  });

  describe('GFM tables (#149)', () => {
    const table = (align: Array<'left' | 'center' | 'right' | null>, header: MarkdownInline[][], rows: MarkdownInline[][][]): MarkdownBlock => ({ type: 'table', align, header, rows });
    const cell = (...children: MarkdownInline[]): MarkdownInline[] => children;

    it('reads a header, an alignment row and body rows, with or without the outer pipes', () => {
      const expected = table([null, 'right'], [cell(text('模型')), cell(text('积分'))], [[cell(text('Remesh')), cell(text('10'))], [cell(text('Rigging')), cell(text('20'))]]);
      expect(parseMarkdown('| 模型 | 积分 |\n| --- | ---: |\n| Remesh | 10 |\n| Rigging | 20 |')).toEqual([expected]);
      expect(parseMarkdown('模型 | 积分\n--- | ---:\nRemesh | 10\nRigging | 20')).toEqual([expected]);
      expect(parseMarkdown('|a|b|c|\n|:--|:-:|--:|\n|1|2|3|')).toEqual([
        table(['left', 'center', 'right'], [cell(text('a')), cell(text('b')), cell(text('c'))], [[cell(text('1')), cell(text('2')), cell(text('3'))]]),
      ]);
    });

    it('allows only text, inline code, bold and links in a cell', () => {
      const [block] = parseMarkdown('| a | b |\n|---|---|\n| **粗** `c\\|d` *斜* [文档](https://example.com/x) | [坏](javascript:alert(1)) ![图](https://example.com/x.png) |');
      expect(block).toEqual(table([null, null], [cell(text('a')), cell(text('b'))], [[
        cell(strong(text('粗')), text(' '), code('c|d'), text(' 斜 '), { type: 'link', href: 'https://example.com/x', children: [text('文档')] }),
        cell(text('坏 图')),
      ]]));
    });

    it('splits at unescaped pipes only, pads short rows and cuts long ones', () => {
      expect(parseMarkdown('| a | b |\n|---|---|\n| x \\| y | z |\n| only |\n| 1 | 2 | 3 |')).toEqual([
        table([null, null], [cell(text('a')), cell(text('b'))], [
          [cell(text('x | y')), cell(text('z'))], [cell(text('only')), []], [cell(text('1')), cell(text('2'))],
        ]),
      ]);
    });

    it('needs a delimiter row of the same width right below the header; anything else stays a paragraph', () => {
      expect(parseMarkdown('a | b\nc | d')).toEqual([paragraph(text('a | b\nc | d'))]);
      expect(parseMarkdown('| a | b |\n| --- |')).toEqual([paragraph(text('| a | b |\n| --- |'))]);
      expect(parseMarkdown('一行字\n---')).toEqual([paragraph(text('一行字')), { type: 'rule' }]);
    });

    it('ends at a blank line or the next block, interrupts a paragraph and sits inside a list item', () => {
      expect(parseMarkdown('前面\n| a |\n|---|\n| 1 |\n\n后面')).toEqual([
        paragraph(text('前面')), table([null], [cell(text('a'))], [[cell(text('1'))]]), paragraph(text('后面')),
      ]);
      expect(parseMarkdown('| a |\n|---|\n| 1 |\n# 标题')).toEqual([
        table([null], [cell(text('a'))], [[cell(text('1'))]]), { type: 'heading', level: 1, children: [text('标题')] },
      ]);
      expect(parseMarkdown('- 项\n  | a |\n  |---|\n  | 1 |')).toEqual([{
        type: 'list', ordered: false, start: 1,
        items: [[paragraph(text('项')), table([null], [cell(text('a'))], [[cell(text('1'))]])]],
      }]);
    });

    it('keeps raw HTML in cells as text and bounds the grid', () => {
      const [block] = parseMarkdown('| <b>x</b> |\n|---|\n| <img src=x onerror=alert(1)> |');
      expect(block).toEqual(table([null], [cell(text('<b>x</b>'))], [[cell(text('<img src=x onerror=alert(1)>'))]]));
      const many = parseMarkdown(`| a |\n|---|\n${Array.from({ length: 500 }, (_, row) => `| ${row} |`).join('\n')}`);
      expect(many[0].type === 'table' && many[0].rows.length).toBe(200);
      const wide = `${Array.from({ length: 30 }, () => 'a').join('|')}`;
      expect(parseMarkdown(`${wide}\n${Array.from({ length: 30 }, () => '-').join('|')}`)[0].type).toBe('paragraph');
    });

    it('while streaming, shows the finished rows and formats the last cell\'s open tail', () => {
      expect(parseMarkdown('| a | b |\n|---|---|\n| 1 | **重', { streaming: true })).toEqual([
        table([null, null], [cell(text('a')), cell(text('b'))], [[cell(text('1')), cell(strong(text('重')))]]),
      ]);
      expect(parseMarkdown('| a | b |\n|---|---|\n| **一', { streaming: true })[0]).toEqual(
        table([null, null], [cell(text('a')), cell(text('b'))], [[cell(strong(text('一'))), []]]),
      );
    });
  });
});
