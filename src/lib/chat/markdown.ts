/**
 * The Markdown subset of an Agent reply (#333): paragraphs and line breaks, bold, italic, inline code, fenced
 * code blocks, ordered and unordered lists (nested too), headings (shown as strong lines), rules and http/https
 * links. The result is a tree of plain values that a component turns into React elements, so nothing is ever
 * parsed as HTML: raw HTML, entities and picture syntax stay text, and a link that is not http(s) keeps only its
 * words. Emphasis follows the CommonMark delimiter rules, loosened next to CJK text so that `**注意：**后面`
 * closes the way models write it.
 *
 * While a reply streams, the unfinished tail is shown formatted instead of as raw marks: a run of `*`, `_` or
 * a backtick at the very end is held back, an unclosed `**` / `*` / backtick at the end formats the rest, and a
 * half-received link shows its words. Once the reply is complete, unclosed marks are text again.
 */

export type MarkdownInline =
  | { type: 'text'; text: string }
  | { type: 'strong' | 'em'; children: MarkdownInline[] }
  | { type: 'code'; text: string }
  | { type: 'link'; href: string; children: MarkdownInline[] };

export type MarkdownBlock =
  | { type: 'paragraph'; children: MarkdownInline[] }
  | { type: 'heading'; level: number; children: MarkdownInline[] }
  | { type: 'code'; language: string; text: string }
  | { type: 'list'; ordered: boolean; start: number; items: MarkdownBlock[][] }
  | { type: 'rule' };

/** How deep lists nest; deeper markers read as paragraph text. */
const MAXIMUM_LIST_DEPTH = 12;

/** The address of a link if it is an absolute http(s) address, normalized; null for anything else. */
export function safeLinkHref(url: string): string | null {
  const trimmed = url.trim();
  if (!/^https?:\/\/[^/]/i.test(trimmed)) return null;
  try {
    const parsed = new URL(trimmed);
    return parsed.protocol === 'http:' || parsed.protocol === 'https:' ? parsed.href : null;
  } catch {
    return null;
  }
}

export function parseMarkdown(source: string, options: { streaming?: boolean } = {}): MarkdownBlock[] {
  const lines = source.replace(/\r\n?/g, '\n').split('\n').map(expandIndentTabs);
  return inlineBlocks(rawBlocks(lines, 0), options.streaming === true);
}

// ---------------------------------------------------------------------------------------------- blocks

type RawBlock =
  | { type: 'paragraph'; text: string }
  | { type: 'heading'; level: number; text: string }
  | { type: 'code'; language: string; text: string }
  | { type: 'list'; ordered: boolean; start: number; items: RawBlock[][] }
  | { type: 'rule' };

const FENCE = /^ {0,3}(`{3,}|~{3,})[ \t]*([^`\s]*)[^`]*$/;
const HEADING = /^ {0,3}(#{1,6})(?:[ \t]+(.*?))?(?:[ \t]+#+)?[ \t]*$/;
const RULE = /^ {0,3}([-*_])(?:[ \t]*\1){2,}[ \t]*$/;
const BULLET = /^( *)([-*+])[ \t]+(\S.*)$/;
const ORDERED = /^( *)(\d{1,9})[.)][ \t]+(\S.*)$/;

type Marker = { indent: number; ordered: boolean; start: number; contentIndent: number; content: string };

function expandIndentTabs(line: string): string {
  const indent = /^[ \t]*/.exec(line)![0];
  return indent.includes('\t') ? indent.replace(/\t/g, '    ') + line.slice(indent.length) : line;
}

const blank = (line: string) => line.trim() === '';
const indentOf = (line: string) => /^ */.exec(line)![0].length;
const dedent = (line: string, amount: number) => line.slice(Math.min(amount, indentOf(line)));

function markerOf(line: string): Marker | null {
  const bullet = BULLET.exec(line);
  if (bullet) {
    return { indent: bullet[1].length, ordered: false, start: 1, contentIndent: line.length - bullet[3].length, content: bullet[3] };
  }
  const ordered = ORDERED.exec(line);
  if (ordered) {
    return { indent: ordered[1].length, ordered: true, start: Number(ordered[2]), contentIndent: line.length - ordered[3].length, content: ordered[3] };
  }
  return null;
}

function startsBlock(line: string, depth: number): boolean {
  return FENCE.test(line) || HEADING.test(line) || RULE.test(line) || (depth < MAXIMUM_LIST_DEPTH && markerOf(line) !== null);
}

function rawBlocks(lines: string[], depth: number): RawBlock[] {
  const blocks: RawBlock[] = [];
  let index = 0;
  while (index < lines.length) {
    const line = lines[index];
    if (blank(line)) {
      index += 1;
      continue;
    }
    const fence = FENCE.exec(line);
    if (fence) {
      const mark = fence[1];
      const closing = new RegExp(`^ {0,3}${mark[0] === '`' ? '`' : '~'}{${mark.length},}[ \\t]*$`);
      const body: string[] = [];
      index += 1;
      while (index < lines.length && !closing.test(lines[index])) {
        body.push(lines[index]);
        index += 1;
      }
      index += 1;
      blocks.push({ type: 'code', language: fence[2], text: body.join('\n') });
      continue;
    }
    const heading = HEADING.exec(line);
    if (heading) {
      blocks.push({ type: 'heading', level: heading[1].length, text: heading[2] ?? '' });
      index += 1;
      continue;
    }
    if (RULE.test(line)) {
      blocks.push({ type: 'rule' });
      index += 1;
      continue;
    }
    const marker = depth < MAXIMUM_LIST_DEPTH ? markerOf(line) : null;
    if (marker) {
      index = readList(lines, index, marker, depth, blocks);
      continue;
    }
    const paragraph = [line.trim()];
    index += 1;
    while (index < lines.length && !blank(lines[index]) && !startsBlock(lines[index], depth)) {
      paragraph.push(lines[index].trim());
      index += 1;
    }
    blocks.push({ type: 'paragraph', text: paragraph.join('\n') });
  }
  return blocks;
}

/** Reads the list that starts at `start`; returns the index after it. */
function readList(lines: string[], start: number, first: Marker, depth: number, blocks: RawBlock[]): number {
  const items: string[][] = [[first.content]];
  let contentIndent = first.contentIndent - first.indent;
  let index = start + 1;
  const sibling = (marker: Marker | null) => marker !== null && marker.ordered === first.ordered && marker.indent < first.indent + contentIndent;
  const inside = (line: string) => indentOf(line) > first.indent;
  while (index < lines.length) {
    const line = lines[index];
    const current = items[items.length - 1];
    if (blank(line)) {
      let next = index + 1;
      while (next < lines.length && blank(lines[next])) next += 1;
      if (next >= lines.length) break;
      const following = lines[next];
      if (sibling(markerOf(following))) {
        index = next;
        continue;
      }
      if (indentOf(following) >= first.indent + contentIndent) {
        for (let gap = index; gap < next; gap += 1) current.push('');
        index = next;
        continue;
      }
      break;
    }
    const marker = markerOf(line);
    if (sibling(marker)) {
      items.push([marker!.content]);
      contentIndent = marker!.contentIndent - marker!.indent;
      index += 1;
      continue;
    }
    if (inside(line)) {
      current.push(dedent(line, first.indent + contentIndent));
      index += 1;
      continue;
    }
    // A line at the list's own indent that is no new block continues the item's last paragraph.
    if (!marker && !startsBlock(line, depth) && current.length > 0 && !blank(current[current.length - 1])) {
      current.push(line.trim());
      index += 1;
      continue;
    }
    break;
  }
  blocks.push({
    type: 'list', ordered: first.ordered, start: first.start,
    items: items.map((item) => rawBlocks(item, depth + 1)),
  });
  return index;
}

function inlineBlocks(blocks: RawBlock[], streaming: boolean): MarkdownBlock[] {
  return blocks.map((block, index) => {
    const tail = streaming && index === blocks.length - 1;
    switch (block.type) {
      case 'paragraph':
        return { type: 'paragraph', children: parseInline(block.text, tail) };
      case 'heading':
        return { type: 'heading', level: block.level, children: parseInline(block.text, tail) };
      case 'list':
        return {
          type: 'list', ordered: block.ordered, start: block.start,
          items: block.items.map((item, itemIndex) => inlineBlocks(item, tail && itemIndex === block.items.length - 1)),
        };
      default:
        return block;
    }
  });
}

// ---------------------------------------------------------------------------------------------- inlines

type Node = {
  prev: Node | null;
  next: Node | null;
} & (
  | { kind: 'inline'; value: MarkdownInline }
  | { kind: 'wrap'; type: 'strong' | 'em'; children: Node[] }
  | {
    kind: 'delim'; char: '*' | '_'; length: number; original: number; canOpen: boolean; canClose: boolean;
    below: Node | null; above: Node | null; inStack: boolean;
  }
);
type Delim = Extract<Node, { kind: 'delim' }>;

const ESCAPABLE = /[!-/:-@[-`{-~]/;
const WHITESPACE = /\s/u;
const PUNCTUATION = /[\p{P}\p{S}]/u;
const CJK_SCRIPT = /[\p{Script=Han}\p{Script=Hiragana}\p{Script=Katakana}\p{Script=Hangul}]/u;
/** CJK letters, and CJK symbols and full-width forms (U+3000 to U+303F, U+FF00 to U+FFEF). */
const isCJK = (char: string) => {
  const code = char.codePointAt(0) ?? 0;
  return CJK_SCRIPT.test(char) || (code >= 0x3000 && code <= 0x303f) || (code >= 0xff00 && code <= 0xffef);
};

const isWhitespace = (char: string) => char === '' || WHITESPACE.test(char);
const isPunctuation = (char: string) => char !== '' && PUNCTUATION.test(char);
/** Next to whitespace, punctuation or CJK text a mark may open or close even when it touches punctuation. */
const isBoundary = (char: string) => isWhitespace(char) || isPunctuation(char) || isCJK(char);

function charBefore(text: string, index: number): string {
  if (index <= 0) return '';
  const code = text.charCodeAt(index - 1);
  return code >= 0xdc00 && code <= 0xdfff && index >= 2 ? text.slice(index - 2, index) : text[index - 1];
}

function charAfter(text: string, index: number): string {
  if (index >= text.length) return '';
  return String.fromCodePoint(text.codePointAt(index)!);
}

function parseInline(source: string, streaming: boolean, links = true): MarkdownInline[] {
  // While streaming, a mark at the very end may be the first half of a pair: hold it back.
  const text = streaming ? source.replace(/[*_`]+$/, '') : source;
  const nodes: Node[] = [];
  let buffer = '';
  const flush = () => {
    if (buffer) nodes.push({ kind: 'inline', value: { type: 'text', text: buffer }, prev: null, next: null });
    buffer = '';
  };
  const push = (value: MarkdownInline) => {
    flush();
    nodes.push({ kind: 'inline', value, prev: null, next: null });
  };
  let index = 0;
  while (index < text.length) {
    const char = text[index];
    if (char === '\\' && index + 1 < text.length && ESCAPABLE.test(text[index + 1])) {
      buffer += text[index + 1];
      index += 2;
      continue;
    }
    if (char === '`') {
      const run = runLength(text, index, '`');
      const close = closingBackticks(text, index + run, run);
      if (close >= 0) {
        push({ type: 'code', text: codeSpanText(text.slice(index + run, close)) });
        index = close + run;
        continue;
      }
      if (streaming) {
        const rest = text.slice(index + run);
        flush();
        if (rest) push({ type: 'code', text: rest });
        index = text.length;
        continue;
      }
      buffer += text.slice(index, index + run);
      index += run;
      continue;
    }
    if ((char === '[' || (char === '!' && text[index + 1] === '[')) && links) {
      const picture = char === '!';
      const link = matchLink(text, picture ? index + 1 : index);
      if (link) {
        const words = parseInline(link.label, false, false);
        const href = picture ? null : safeLinkHref(link.url);
        flush();
        if (href) push({ type: 'link', href, children: words });
        else for (const word of words) push(word);
        index = link.end;
        continue;
      }
      if (streaming) {
        const partial = partialLinkTail(text, picture ? index + 1 : index);
        if (partial !== null) {
          flush();
          for (const word of parseInline(partial, false, false)) push(word);
          index = text.length;
          continue;
        }
      }
    }
    if (char === '*' || char === '_') {
      const run = runLength(text, index, char);
      const before = charBefore(text, index);
      const after = charAfter(text, index + run);
      const left = !isWhitespace(after) && (!isPunctuation(after) || isBoundary(before));
      const right = !isWhitespace(before) && (!isPunctuation(before) || isBoundary(after));
      const canOpen = char === '*' ? left : left && (!right || isPunctuation(before));
      const canClose = char === '*' ? right : right && (!left || isPunctuation(after));
      flush();
      nodes.push({ kind: 'delim', char, length: run, original: run, canOpen, canClose, below: null, above: null, inStack: true, prev: null, next: null });
      index += run;
      continue;
    }
    buffer += char;
    index += 1;
  }
  flush();
  return finishInlines(nodes, streaming);
}

function runLength(text: string, index: number, char: string): number {
  let end = index;
  while (text[end] === char) end += 1;
  return end - index;
}

function closingBackticks(text: string, from: number, run: number): number {
  let index = text.indexOf('`', from);
  while (index >= 0) {
    const length = runLength(text, index, '`');
    if (length === run) return index;
    index = text.indexOf('`', index + length);
  }
  return -1;
}

function codeSpanText(raw: string): string {
  const text = raw.replace(/\n/g, ' ');
  return text.length > 2 && text.startsWith(' ') && text.endsWith(' ') && text.trim() !== '' ? text.slice(1, -1) : text;
}

/** `[label](url)` or `[label](url "title")` starting at the bracket. */
function matchLink(text: string, open: number): { label: string; url: string; end: number } | null {
  const close = closingBracket(text, open);
  if (close < 0 || text[close + 1] !== '(') return null;
  let index = close + 2;
  while (text[index] === ' ') index += 1;
  let depth = 0;
  const start = index;
  while (index < text.length) {
    const char = text[index];
    if (char === '\\' && index + 1 < text.length) {
      index += 2;
      continue;
    }
    if (WHITESPACE.test(char)) break;
    if (char === '(') depth += 1;
    if (char === ')') {
      if (depth === 0) break;
      depth -= 1;
    }
    index += 1;
  }
  const url = text.slice(start, index);
  while (text[index] === ' ') index += 1;
  if (text[index] === '"' || text[index] === "'") {
    const quote = text[index];
    const end = text.indexOf(quote, index + 1);
    if (end < 0) return null;
    index = end + 1;
    while (text[index] === ' ') index += 1;
  }
  if (text[index] !== ')') return null;
  return { label: text.slice(open + 1, close), url, end: index + 1 };
}

function closingBracket(text: string, open: number): number {
  let depth = 0;
  for (let index = open; index < text.length; index += 1) {
    const char = text[index];
    if (char === '\\') {
      index += 1;
    } else if (char === '[') {
      depth += 1;
    } else if (char === ']') {
      depth -= 1;
      if (depth === 0) return index;
    }
  }
  return -1;
}

/** The words of a link still arriving at the end of a streaming reply: `[words` + `](…` with no `)` yet. */
function partialLinkTail(text: string, open: number): string | null {
  const close = closingBracket(text, open);
  if (close < 0 || text[close + 1] !== '(' || text.indexOf(')', close + 2) >= 0) return null;
  return text.slice(open + 1, close);
}

/** CommonMark's emphasis pass over the delimiter runs, then the streaming tail, then plain values. */
function finishInlines(list: Node[], streaming: boolean): MarkdownInline[] {
  const head = { kind: 'inline', value: { type: 'text', text: '' }, prev: null, next: null } as Node;
  let last = head;
  let bottom: Delim | null = null;
  let top: Delim | null = null;
  for (const node of list) {
    last.next = node;
    node.prev = last;
    last = node;
    if (node.kind === 'delim') {
      if (!bottom) bottom = node;
      node.below = top;
      if (top && top.kind === 'delim') top.above = node;
      top = node;
    }
  }
  const unstack = (delim: Delim) => {
    delim.inStack = false;
    if (delim.below && delim.below.kind === 'delim') delim.below.above = delim.above;
    if (delim.above && delim.above.kind === 'delim') delim.above.below = delim.below;
    if (bottom === delim) bottom = delim.above as Delim | null;
  };
  const unlink = (node: Node) => {
    if (node.prev) node.prev.next = node.next;
    if (node.next) node.next.prev = node.prev;
  };
  const openersBottom = new Map<string, Node | null>();
  let closer = bottom as Delim | null;
  while (closer) {
    if (!closer.canClose || closer.length === 0) {
      closer = closer.above as Delim | null;
      continue;
    }
    const key = `${closer.char}${closer.canOpen ? 1 : 0}${closer.original % 3}`;
    const floor = openersBottom.get(key) ?? null;
    let opener = closer.below as Delim | null;
    while (opener && opener !== floor) {
      const oddMatch = (opener.canClose || closer.canOpen) && (opener.original + closer.original) % 3 === 0
        && !(opener.original % 3 === 0 && closer.original % 3 === 0);
      if (opener.char === closer.char && opener.canOpen && opener.length > 0 && !oddMatch) break;
      opener = opener.below as Delim | null;
    }
    if (opener && opener !== floor) {
      const use = opener.length >= 2 && closer.length >= 2 ? 2 : 1;
      opener.length -= use;
      closer.length -= use;
      // Delimiters between the pair are text now.
      for (let between = opener.above as Delim | null; between && between !== closer; between = between.above as Delim | null) between.inStack = false;
      opener.above = closer;
      closer.below = opener;
      const children: Node[] = [];
      for (let node = opener.next; node && node !== closer; node = node.next) children.push(node);
      const wrap: Node = { kind: 'wrap', type: use === 2 ? 'strong' : 'em', children, prev: opener, next: closer };
      opener.next = wrap;
      closer.prev = wrap;
      if (opener.length === 0) {
        unlink(opener);
        unstack(opener);
      }
      if (closer.length === 0) {
        const next = closer.above as Delim | null;
        unlink(closer);
        unstack(closer);
        closer = next;
      }
      continue;
    }
    openersBottom.set(key, closer.below);
    const next = closer.above as Delim | null;
    if (!closer.canOpen) unstack(closer);
    closer = next;
  }
  if (streaming) closeStreamingTail(head, last, unlink);
  return plainInlines(collect(head.next));
}

/** While streaming, an opener still unclosed formats everything after it; one with nothing after is held back. */
function closeStreamingTail(head: Node, tail: Node, unlink: (node: Node) => void) {
  let end: Node = tail;
  while (end.next) end = end.next;
  for (let node: Node | null = end; node && node !== head; ) {
    const previous: Node | null = node.prev;
    if (node.kind === 'delim' && node.inStack && node.canOpen && node.length > 0) {
      const children = collect(node.next);
      if (children.length === 0) {
        unlink(node);
      } else {
        const inner: Node = { kind: 'wrap', type: node.length >= 2 ? 'strong' : 'em', children, prev: null, next: null };
        const wrap: Node = node.length >= 3 ? { kind: 'wrap', type: 'em', children: [inner], prev: null, next: null } : inner;
        wrap.prev = node.prev;
        wrap.next = null;
        if (node.prev) node.prev.next = wrap;
      }
    }
    node = previous;
  }
}

function collect(first: Node | null): Node[] {
  const nodes: Node[] = [];
  for (let node = first; node; node = node.next) nodes.push(node);
  return nodes;
}

function plainInlines(nodes: Node[]): MarkdownInline[] {
  const out: MarkdownInline[] = [];
  const text = (value: string) => {
    if (!value) return;
    const previous = out[out.length - 1];
    if (previous && previous.type === 'text') out[out.length - 1] = { type: 'text', text: previous.text + value };
    else out.push({ type: 'text', text: value });
  };
  for (const node of nodes) {
    if (node.kind === 'delim') text(node.char.repeat(node.length));
    else if (node.kind === 'wrap') out.push({ type: node.type, children: plainInlines(node.children) });
    else if (node.value.type === 'text') text(node.value.text);
    else out.push(node.value);
  }
  return out;
}
