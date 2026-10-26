/**
 * What a copy from the chat transcript puts on the clipboard when the selection takes in pictures (#281).
 * The pictures of chat cards sit in draggable frames, which browsers leave out of a copy (a draggable element
 * cannot be selected), so a plain copy would carry the words only. With pictures selected the copy carries the
 * words as text and the words plus the pictures as HTML; the composer (or any rich editor) then reads both.
 * Without pictures it returns null and the browser copies as usual, unless `rewritten` says the words are not
 * what the browser would copy (whole Agent replies as their Markdown source, #333).
 */
export function transcriptClipboard(text: string, pictures: readonly string[], rewritten = false): { plain: string; html: string } | null {
  const sources = [...new Set(pictures.filter((source) => source.trim() !== ''))];
  if (sources.length === 0 && !rewritten) return null;
  const words = text ? `<div>${escapeHTML(text).replace(/\r?\n/g, '<br>')}</div>` : '';
  return { plain: text, html: words + sources.map((source) => `<img src="${escapeHTML(source)}">`).join('') };
}

function escapeHTML(value: string): string {
  return value.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');
}

/** A part of a transcript copy: words as the browser reads them, or a whole Agent reply as its Markdown source. */
export type TranscriptPiece = { text: string } | { source: string };

/** The plain text of a copy made of pieces, one line apart. */
export function joinTranscriptPieces(pieces: readonly TranscriptPiece[]): string {
  return pieces
    .map((piece) => ('source' in piece ? piece.source : piece.text.trim()))
    .filter((piece) => piece !== '')
    .join('\n');
}

/**
 * The plain text of a selection in the transcript when it takes in whole Agent replies (#333): each reply whose
 * words are all selected is copied as its Markdown source, the parts between as the browser reads them (the
 * selection is pointed at each part in turn and then restored). Null when no whole reply is selected; the
 * browser then copies the rendered words of a partial selection as usual.
 */
export function selectedTranscriptText(
  selection: Selection,
  root: Element,
  sourceOf: (element: Element) => string | undefined,
): string | null {
  if (selection.rangeCount === 0) return null;
  const original = selection.getRangeAt(0).cloneRange();
  const document = root.ownerDocument;
  const whole: Array<{ words: Range; source: string }> = [];
  for (const element of Array.from(root.querySelectorAll('[data-chat-markdown]'))) {
    const source = sourceOf(element);
    const words = wordsRange(element);
    if (source === undefined || !words) continue;
    if (original.compareBoundaryPoints(Range.START_TO_START, words) <= 0 && original.compareBoundaryPoints(Range.END_TO_END, words) >= 0) {
      whole.push({ words, source });
    }
  }
  if (whole.length === 0) return null;
  const read = (part: Range) => {
    selection.removeAllRanges();
    selection.addRange(part);
    return selection.toString();
  };
  const pieces: TranscriptPiece[] = [];
  try {
    let start: [Node, number] = [original.startContainer, original.startOffset];
    for (const { words, source } of whole) {
      const before = document.createRange();
      before.setStart(...start);
      before.setEnd(words.startContainer, words.startOffset);
      pieces.push({ text: read(before) }, { source });
      start = [words.endContainer, words.endOffset];
    }
    const after = document.createRange();
    after.setStart(...start);
    after.setEnd(original.endContainer, original.endOffset);
    pieces.push({ text: read(after) });
  } finally {
    selection.removeAllRanges();
    selection.addRange(original);
  }
  return joinTranscriptPieces(pieces);
}

/** From the first to the last visible character of an element's words; null when it has none. */
function wordsRange(element: Element): Range | null {
  const walker = element.ownerDocument.createTreeWalker(element, NodeFilter.SHOW_TEXT, {
    acceptNode: (node) => (node.nodeValue?.trim() ? NodeFilter.FILTER_ACCEPT : NodeFilter.FILTER_SKIP),
  });
  const first = walker.nextNode();
  if (!first) return null;
  let last: Node = first;
  for (let node = walker.nextNode(); node; node = walker.nextNode()) last = node;
  const range = element.ownerDocument.createRange();
  range.setStart(first, 0);
  range.setEnd(last, last.nodeValue?.length ?? 0);
  return range;
}
