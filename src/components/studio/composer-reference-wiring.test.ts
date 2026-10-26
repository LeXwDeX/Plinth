import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

import { useComposerReferenceStore } from '@/store/composer-reference-store';

const source = (relative: string) => readFileSync(new URL(relative, import.meta.url), 'utf8');

describe('handle tag to composer', () => {
  it('queues a request that the composer takes once', () => {
    useComposerReferenceStore.getState().refer({ handle: '@img3', url: '/a.png', assetId: 'a', taskId: 't' });
    const first = useComposerReferenceStore.getState().request;
    expect(first).toMatchObject({ handle: '@img3', assetId: 'a' });
    useComposerReferenceStore.getState().clear(first!.nonce);
    expect(useComposerReferenceStore.getState().request).toBeNull();
    // two clicks on the same picture are two requests
    useComposerReferenceStore.getState().refer({ handle: '@img3', url: '/a.png' });
    const second = useComposerReferenceStore.getState().request;
    useComposerReferenceStore.getState().refer({ handle: '@img3', url: '/a.png' });
    expect(useComposerReferenceStore.getState().request?.nonce).toBeGreaterThan(second!.nonce);
  });

  it('makes the handle tags of the result cards buttons with their tooltips from the Schema', () => {
    const card = source('../chat/result-card.tsx');
    expect(card.match(/<HandleTag /g)?.length).toBe(4);
    expect(card).not.toContain('pointer-events-none absolute left-1 top-1 rounded-sm bg-black/60');
    const tag = source('../chat/handle-tag.tsx');
    expect(tag).toContain("'ui.studio.composer.handle_tag_title'");
    expect(tag).toContain("'ui.studio.composer.handle_tag_model_title'");
    expect(source('../../../internal/catalog/default_catalog.json')).toContain('"ui.studio.composer.handle_tag_model_title": "写入输入框"');
    expect(tag).toContain('cursor-pointer');
    expect(tag).toContain('refer(reference)');
  });

  it('makes every other handle of a session picture a tag too: message text, sent pictures, stage, right panel and approval inputs', () => {
    expect(source('../chat/chat-message.tsx')).toContain('<MessageText text={displayContent} references={handleReferences} />');
    expect(source('../chat/result-card.tsx')).toContain('testId="chat-sent-handle"');
    expect(source('./chat-column.tsx')).toContain('handleReferences={sessionHandleReferences}');
    expect(source('./stage-header.tsx')).toContain('<HandleTag reference={handleReference} testId="stage-handle"');
    expect(source('./object-details.tsx')).toContain('testId="inspector-handle"');
    expect(source('./approval-card.tsx')).toContain('testId="studio-approval-input-handle"');
  });

  it('adds the picture to the attachments, or flashes the one that is already there, writes the handle at the caret and focuses the box', () => {
    const composer = source('./composer.tsx');
    expect(composer).toContain("request.kind === 'model' ? -2 : attachedIndex(imagesRef.current, workspace.attachmentSources, request)");
    expect(composer).toContain('setFlashUrl(imagesRef.current[existing])');
    expect(composer).toContain('workspace.queueChatAttachments([{');
    expect(composer).toContain('insertReference(valueRef.current, at, request.handle)');
    expect(composer).toContain('textarea.current?.focus()');
    expect(composer).toContain('flash={item.url === flashUrl}');
  });

  it('sends the mentions as claims and lets the server rewrite the text, queued turns included', () => {
    const chat = source('./chat-column.tsx');
    expect(chat).toContain('mentionsForTurn(text, mentionsRef.current, pickedMentions(scope))');
    expect(chat).toContain('...(mentions.length > 0 ? { mentions } : {})');
    expect(chat).toContain('turn.selection, turn.mentions, turn.inputSource)');
    expect(chat).not.toContain('resolveMentions');
    expect(source('../../lib/chat/send-queue.ts')).toContain('mentions?: ChatMentionClaim[]');
  });

  it('shows the resolved selected Skill inline using Schema text and keeps its full value accessible', () => {
    const picker = source('./skill-picker.tsx');
    expect(picker).toContain("schemaText(schema, 'ui.studio.skill.selected')");
    expect(picker).toContain('value={value}');
    expect(picker).toContain('fullValue={fullValue}');
    expect(picker).toContain('resolvedSelection.displayName');
  });
});
