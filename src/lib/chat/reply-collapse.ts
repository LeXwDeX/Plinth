/**
 * 长回复折叠（#103）：Schema 只声明默认显示的行数，行数按回复的换行与排版宽度估算。
 * 估算偏保守不计 Markdown 标记，因为折叠只是视觉收起，不改变内容本身。
 */

/** 按当前正文字号估算的一行字符数；宽字符按两列计，故中文行的字符数约为它的一半。 */
export const REPLY_CHARACTERS_PER_LINE = 60;

/** `leading-relaxed` 的行高（em）；折叠高度按它换算，与正文排版一致。 */
export const REPLY_LINE_HEIGHT_EM = 1.625;

/** 东亚文字按两列计；其余符号区段（标点、全角形式）在码点上直接判断。 */
const WIDE_SCRIPT = /[\p{Script=Han}\p{Script=Hiragana}\p{Script=Katakana}\p{Script=Hangul}]/u;
const WIDE_CODE_POINTS: ReadonlyArray<readonly [number, number]> = [
  [0x3000, 0x303f],
  [0xfe30, 0xfe4f],
  [0xff00, 0xffef],
];

function isWideCharacter(character: string): boolean {
  if (WIDE_SCRIPT.test(character)) return true;
  const codePoint = character.codePointAt(0) ?? 0;
  return WIDE_CODE_POINTS.some(([start, end]) => codePoint >= start && codePoint <= end);
}

/** 把宽字符折成两列的权重，让中文回复的行数与英文一样不会低估。 */
function visualLength(text: string): number {
  let length = 0;
  for (const character of text) {
    length += isWideCharacter(character) ? 2 : 1;
  }
  return length;
}

/** The lines a reply occupies: its explicit line breaks plus wrapping at the character budget. */
export function replyLineCount(text: string, charactersPerLine = REPLY_CHARACTERS_PER_LINE): number {
  if (!text) return 0;
  const perLine = Math.max(1, charactersPerLine);
  return text.split('\n').reduce((count, line) => {
    const length = visualLength(line);
    return count + Math.max(1, Math.ceil(length / perLine));
  }, 0);
}

/** Whether a finished reply should start folded: it is longer than the declared line count. */
export function replyNeedsCollapse(text: string, lines: number): boolean {
  return lines > 0 && replyLineCount(text) > lines;
}
