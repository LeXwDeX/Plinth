import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

function source(relative: string): string {
  return readFileSync(new URL(relative, import.meta.url), 'utf8');
}

const agentDialog = source('./chat-column.tsx');
const composer = source('./composer.tsx');
const chatMessage = source('../../components/chat/chat-message.tsx');

describe('workspace chat message wiring', () => {
  it('shows the active session Harness summary before the input while retaining all four selectors', () => {
    expect(composer).toContain('const activeSessionId = useChatStore((state) => state.activeSessionId);');
    expect(composer).toContain('<HarnessSummary sessionId={activeSessionId} />');
    expect(composer.indexOf('<HarnessSummary')).toBeLessThan(composer.indexOf('<textarea'));
    expect(composer).toContain('<AgentScopePicker');
    expect(composer).toContain('<ModelPicker />');
    expect(composer).toContain('<ParamsPopover');
    expect(composer).toContain('<SkillPicker');
  });

  it('sends no Agent turn while the deployment has no Agent settings (#253)', () => {
    // Stage actions and queued turns go through send() too, not only the composer button.
    expect(agentDialog).toMatch(/const send = useCallback\([\s\S]*?if \(agentUnavailableReason\(schema\) !== null\) return;/);
    expect(composer).toContain('disabled={!canSend || locked || agentUnavailable !== null}');
  });

  it('uploads data-address pictures as input assets before a turn is sent or queued (#292)', () => {
    const send = agentDialog.slice(agentDialog.indexOf('const send = useCallback('), agentDialog.indexOf('const sendRef = useRef(send);'));
    const upload = send.indexOf('await uploadInlineImages(images, claims, {');
    expect(upload).toBeGreaterThan(0);
    // Every path that hands the pictures on comes after the upload: the queue, the streaming echo and the request.
    expect(upload).toBeLessThan(send.indexOf('if (turnRunning) {'));
    expect(upload).toBeLessThan(send.indexOf('setQueue((current) => enqueueTurn('));
    expect(upload).toBeLessThan(send.indexOf('initStreaming(sessionId, {'));
    expect(upload).toBeLessThan(send.indexOf('await chatClient.startTurn('));
    expect(send).toContain('uploadWorkspaceInputAssetResult(schema, picture, inlineImageFilename(schema, picture))');
    expect(send).toContain('maximumBytes: schema.runtime.input_assets.upload_max_bytes');
    // A failure sends nothing and says why (with the request ID), like the board snapshot.
    expect(send).toMatch(/catch \(error\) \{\s*const message = reportInlineImageFailure\(schema, error\);\s*if \(isCurrentSessionScope\(scope\)\) toast\.error\(message\);\s*return;/);
    // Only the raster types the transcript can show and the asset upload takes are attached.
    expect(agentDialog).toContain("if (!safePreviewURL(picture)) throw new ImageUploadError('unsupported');");
  });

  it('stops text selection while the chat column is being resized, and turns it back on after', () => {
    expect(agentDialog).toMatch(/onPointerDown=\{\(event\) => \{[\s\S]*?event\.preventDefault\(\);[\s\S]*?setPageSelectable\(false\);/);
    expect(agentDialog).toContain("const value = selectable ? '' : 'none';");
    expect(agentDialog).toContain("onPointerUp={endDrag}");
    expect(agentDialog).toContain("onPointerCancel={endDrag}");
    expect(agentDialog).toContain("document.body.style.userSelect = value;");
    // Safari only honours the prefixed property.
    expect(agentDialog).toContain("document.body.style.setProperty('-webkit-user-select', value);");
  });

  it('gives every persisted message its image, copy-to-input and delete affordances', () => {
    // 这三个回调曾经定义在 ChatMessage 上却没有调用方：图片点击、复制到输入框、删除
    // 都在界面上不可达，而 Schema 早已备好对应文案。
    expect(agentDialog).toContain('onImageClick={openImageDetail}');
    expect(agentDialog).toContain('onCopyToInput={item.message.isPending || editable?.userId === item.message.id ? undefined : copyToInput}');
    expect(agentDialog).toContain('onDelete={item.message.isPending ? undefined');
  });

  it('sends the models chosen in the other domains and never narrows tools to the tab in view', () => {
    expect(agentDialog).toContain('domainModels: otherDomainModels');
    expect(agentDialog).toMatch(/entry\[0\] !== domain/);
  });

  it('opens chat pictures in the shared result detail instead of a private lightbox', () => {
    expect(agentDialog).not.toContain('ImageLightbox');
    expect(agentDialog).not.toContain('state.openLightbox');
    expect(agentDialog).toContain('state.openResultDetail');
  });

  it('regenerates from the original text and images instead of the current attachments', () => {
    expect(agentDialog).toContain('const payload = turnPayload(regenerate.user);');
    expect(agentDialog).toMatch(/void send\(payload\.text, payload\.images, payload\.claims, regenerate\.deleteIds\);/);
  });

  it('restores the server message list when deletion fails', () => {
    expect(agentDialog).toMatch(
      /setMessages\(sessionId, previous\);[\s\S]{0,200}ui\.chat\.toast\.delete_message_retry/,
    );
  });

  it('keeps chat surfaces on the design tokens', () => {
    const hardcoded = chatMessage.match(
      /(?:bg|text|border|ring|from|to|via)-(?:indigo|zinc|red|blue|green|amber|gray|slate|neutral|stone|sky|violet|purple|pink|rose|yellow|orange)-?\d*/g,
    );
    expect(hardcoded ?? []).toEqual([]);
  });
});

describe('candidate pick wiring', () => {
  it('connects the carousel pick to the server-side session pick', () => {
    expect(agentDialog).toContain('useSessionPick(chatClient, activeSessionId)');
    expect(agentDialog).toContain('pick={sessionPick.pick}');
    // #291: one stable callback for every message, so the memoized messages are not redrawn by each chunk.
    expect(agentDialog).toContain('onPick={pickCandidate}');
    expect(agentDialog).toMatch(/const pickCandidate = useCallback\(async \(claim: ChatAttachmentClaim\) => \{[\s\S]{0,120}chooseCandidate\(claim\)/);
    expect(source('../chat/result-card.tsx')).toContain("schemaText(schema, 'ui.chat.image.pick')");
  });
});

describe('regenerate and edit-and-resend wiring', () => {
  it('offers them only on the last exchange and sends through the one turn path', () => {
    expect(agentDialog).toContain('onRegenerate={regenerate?.assistantId === item.message.id ? regenerateReply : undefined}');
    expect(agentDialog).toContain('onEdit={editable?.userId === item.message.id ? editLastMessage : undefined}');
    expect(agentDialog).toMatch(/void send\(payload\.text, payload\.images, payload\.claims, regenerate\.deleteIds\);/);
    expect(agentDialog).toContain('void send(input, undefined, undefined, editing?.deleteIds)');
  });

  it('replaces the old round only after validation and keeps the text if the new turn never starts', () => {
    // Deletion sits after the model/skill checks and before the streaming placeholder.
    expect(agentDialog).toMatch(/for \(const id of replaceIds\) await chatClient\.deleteMessage\(sessionId, id\);/);
    expect(agentDialog).toMatch(/ui\.chat\.toast\.turn_replace_failed/);
    expect(agentDialog).toMatch(/restoreAttachments\(images, claims\)/);
    expect(chatMessage).toContain("'ui.chat.message.regenerate'");
    expect(chatMessage).toContain("'ui.chat.message.edit_resend'");
  });
});

describe('composer and approvals wiring', () => {
  it('does not send on the Enter that confirms an input-method candidate', () => {
    expect(composer).toContain('event.nativeEvent.isComposing');
    expect(composer).toContain('event.keyCode === 229');
    expect(composer).toMatch(/event\.shiftKey\) return;/);
  });

  it('shows approvals as cards in the conversation and never as a blocking dialog', () => {
    expect(agentDialog).toContain('<ApprovalCard');
    expect(agentDialog).toContain('cardFromPending');
    expect(agentDialog).not.toContain('ConsentDialog');
  });

  it('turns an expired approval into a prefilled composer instead of a silent re-request', () => {
    expect(agentDialog).toContain('prefillComposer(formatSchemaText(schemaText(schema, \'ui.studio.approval.card.rerequest_prefill\')');
  });
});

describe('the conversation is read a page at a time', () => {
  const column = readFileSync(new URL('./chat-column.tsx', import.meta.url), 'utf8');

  it('reads only the newest page on every refresh and merges it with earlier pages already shown', () => {
    expect(column).not.toContain('listMessages(');
    expect(column).toContain('const messagePageSize = schema.runtime.frontend.chat_message_page_size;');
    expect(column.match(/listMessagePage\(sessionId, \{ limit: messagePageSize \}\)/g)).toHaveLength(4);
    expect(column).toContain('pageSize: messagePageSize,');
    expect(column).not.toMatch(/setMessages\(sessionId, (data|await)/);
  });

  it('offers earlier messages at the top and keeps the reader where they were', () => {
    expect(column).toContain('data-testid="chat-load-earlier"');
    expect(column).toContain('{ before: olderCursor, limit: messagePageSize }');
    expect(column).toContain('applyOlderMessagePage(sessionId, page);');
    expect(column).toContain('container.scrollTop = container.scrollHeight - keepDistanceFromEnd.current;');
    // Following the newest message keys on its id, so a page put in front does not count as new content.
    expect(column).toContain("changeKey: [lastDisplayed?.id ?? '', threeViews.length");
  });

  it('routes every stream event through the one status slot and marks the thinking status (#285)', () => {
    expect(agentDialog).toContain('const next = nextStreamStatus(schema, current, event);');
    expect(agentDialog).toContain('updateStreamGenState(sessionId, next.statusText, next.progress, next.thinking);');
    expect(agentDialog).toContain('thinking={streamBuf?.genState?.thinking === true}');
    expect(chatMessage).toContain('data-testid="chat-status-bubble"');
    expect(chatMessage).toContain("data-thinking={thinking ? 'true' : undefined}");
  });
});
