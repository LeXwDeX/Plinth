/**
 * Text layout shared by the board and the exported picture. The board draws text with Konva; this
 * module repeats Konva's rules (word wrap at spaces and dashes, characters when a word is too long,
 * the first baseline from the font's ascent and descent) on a plain 2D context, so a text node has the
 * same lines at the same positions in both. The size, family, line height and wrap width all come from
 * the node and the Product Schema; nothing here is a constant.
 */

export interface MeasuredText {
  width: number;
  fontBoundingBoxAscent?: number;
  fontBoundingBoxDescent?: number;
  actualBoundingBoxAscent?: number;
  actualBoundingBoxDescent?: number;
}

export interface TextMeasuringContext {
  font: string;
  measureText(text: string): MeasuredText;
}

/** The context font Konva builds for a Text node with default style and variant. */
export function canvasFont(fontSize: number, fontFamily: string): string {
  return `normal normal ${fontSize}px ${fontFamily}`;
}

const isBreak = (char: string) => char === ' ' || char === '-';

/**
 * Lines of `text` inside `maxWidth`. A paragraph that fits stays whole; a longer one is cut at the last
 * space or dash before the overflow, or between characters when it has none (Chinese text, long words).
 * A line of whitespace-trimmed text starts after the break. Like Konva, a width too small for even one
 * character stops the paragraph.
 */
export function wrapTextLines(text: string, maxWidth: number, measure: (text: string) => number): string[] {
  const lines: string[] = [];
  for (const paragraph of text.split('\n')) {
    if (measure(paragraph) <= maxWidth) {
      lines.push(paragraph);
      continue;
    }
    const graphemes = Array.from(paragraph);
    const length = graphemes.length;
    const slice = (start: number, end: number) => graphemes.slice(start, end).join('');
    let start = 0;
    while (start < length) {
      const fits = (end: number) => measure(slice(start, end)) <= maxWidth;
      let low = start;
      let high = Math.min(length, start + Math.max(1, Math.ceil((length * maxWidth) / Math.max(measure(paragraph), 1))));
      while (fits(high)) {
        low = high;
        if (high === length) break;
        high = Math.min(length, high + (high - start));
      }
      while (high - low > 1) {
        const mid = (low + high) >>> 1;
        if (fits(mid)) low = mid;
        else high = mid;
      }
      if (low === start) break;
      if (low === length) {
        lines.push(slice(start, length));
        break;
      }
      if (!isBreak(graphemes[low])) {
        let wrapIndex = low - 1;
        while (wrapIndex >= start && !isBreak(graphemes[wrapIndex])) wrapIndex -= 1;
        if (wrapIndex >= start) low = wrapIndex + 1;
      }
      lines.push(slice(start, low).trimEnd());
      start = low;
      while (start < length && !graphemes[start].trim()) start += 1;
    }
  }
  return lines;
}

export interface TextLayoutInput {
  text: string;
  /** Wrap width in node units. */
  width: number;
  fontSize: number;
  fontFamily: string;
  lineHeight: number;
}

export interface TextLayout {
  font: string;
  lines: string[];
  lineHeightPx: number;
  /** Alphabetic baseline of the first line, relative to the node's top. */
  firstBaselineY: number;
}

export function layoutText(context: TextMeasuringContext, input: TextLayoutInput): TextLayout {
  const font = canvasFont(input.fontSize, input.fontFamily);
  context.font = font;
  const lineHeightPx = input.lineHeight * input.fontSize;
  const sample = context.measureText('M');
  const ascent = sample.fontBoundingBoxAscent ?? sample.actualBoundingBoxAscent ?? 0;
  const descent = sample.fontBoundingBoxDescent ?? sample.actualBoundingBoxDescent ?? 0;
  const lines = input.text === '' ? [] : wrapTextLines(input.text, input.width, (value) => context.measureText(value).width);
  return { font, lines, lineHeightPx, firstBaselineY: (ascent - descent) / 2 + lineHeightPx / 2 };
}
