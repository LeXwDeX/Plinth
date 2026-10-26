import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';

// #281: chat text copied with Ctrl/Cmd+C used to arrive in the composer as `art-ai-canvas:<id>`. The canvas wrote
// its marker over every copy while a layer was selected, wherever the copy happened. These checks keep the parts
// of the fix wired together; the decisions themselves are tested in lib/canvas/clipboard and lib/chat/composer-input.
const read = (relative: string) => readFileSync(new URL(relative, import.meta.url), 'utf8');
const messages = defaultCatalog.application.content.messages as Record<string, string>;

describe('chat copy and composer paste (#281)', () => {
  const board = read('../canvas/canvas-board.tsx');

  it('lets the canvas take Ctrl/Cmd+C and Ctrl/Cmd+X only while it owns the copy', () => {
    const copy = board.slice(board.indexOf('if (meta && key === keyboard.copy_key) {'));
    expect(copy.indexOf('if (!ownsCopy()) return;')).toBeGreaterThan(-1);
    expect(copy.indexOf('if (!ownsCopy()) return;')).toBeLessThan(copy.indexOf('copySelectedNodes(sessionId)'));
    expect(board).toContain('matchesShortcut(event, keyboard.cut_shortcut) && ownsCopy()');
    expect(board).toContain('canvasOwnsCopy({ canvasActive, selection: window.getSelection(), canvas: containerRef.current })');
  });

  it('drops a text selection made elsewhere when the canvas is pressed', () => {
    expect(board).toMatch(/onPointerDownCapture=\{\(event\) => \{[\s\S]*?selection\.removeAllRanges\(\)/);
  });

  it('splits a composer paste into words and attachments, and the parent locks the send while the pictures are read', () => {
    const composer = read('./composer.tsx');
    const column = read('./chat-column.tsx');
    const paste = composer.slice(composer.indexOf('onPaste={(event) => {'), composer.indexOf('onKeyUp='));
    expect(paste).toContain('planComposerPaste({');
    expect(paste).toContain("getData('text/html')");
    expect(paste).toContain('insertPastedText(value');
    expect(paste).toContain('onFiles(paste.files)');
    // HTML pictures are read by the parent, which holds the send lock from this call until they are attached.
    expect(paste).toContain('onPasteImages(paste.imageUrls)');
    expect(column).toContain('readPastedImages(urls)');
    expect(column).toContain('beginAttachmentRead(attachmentReads, scope)');
    expect(column).toContain('attachmentsPending(attachmentReads, scope)');
    expect(messages['ui.chat.input.paste.image_unreadable']).toContain('{count}');
  });

  it('copies selected chat pictures with the words, from the marked picture elements', () => {
    const column = read('./chat-column.tsx');
    const copy = column.slice(column.indexOf('onCopy={(event) => {'), column.indexOf('data-testid="chat-messages"'));
    expect(copy).toContain("querySelectorAll<HTMLImageElement>('img[data-chat-picture]')");
    expect(copy).toContain('range.intersectsNode(image)');
    // #333: whole Agent replies in the selection go as their Markdown source.
    expect(copy).toContain('selectedTranscriptText(selection, event.currentTarget, markdownSourceOf)');
    expect(copy).toContain('transcriptClipboard(replies ?? selection.toString(), pictures, replies !== null)');
    expect(copy).toContain("setData('text/html', payload.html)");
    // #290: the cards show previews; each marks its original's address, and that is what is copied.
    const card = read('../chat/result-card.tsx');
    expect(card.match(/<img src=\{(previewAssetUrl\([^)]*\)|coverPreview)\}[^\n]*data-chat-picture=\{(url|coverUrl)\} \/>/g)).toHaveLength(3);
    expect(copy).toContain('image.dataset.chatPicture || image.currentSrc || image.src');
  });

  it('attaches the same picture once', () => {
    expect(read('./chat-column.tsx')).toContain('[...new Set([...current, ...prepared])]');
  });

  it('keeps card labels, handle overlays and step notes out of copied chat text', () => {
    const card = read('../chat/result-card.tsx');
    expect(card).toContain('<div className="flex select-none items-center gap-1.5 px-0.5 text-[11px] text-muted-foreground">\n        <KindBadge kind="image" />');
    expect(card.match(/<HandleTag [^\n]*className="select-none/g)).toHaveLength(3);
    expect(card).toContain('truncate select-none px-0.5 text-center text-[11px] text-muted-foreground" data-testid="chat-result-view-label"');
    expect(read('../chat/paid-step-notes.tsx')).toContain('<ul className="flex select-none flex-col');
  });
});
