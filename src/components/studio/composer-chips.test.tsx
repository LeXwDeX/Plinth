import React from 'react';
import { readFileSync } from 'node:fs';
import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';

import { CHIP_HEIGHT_CLASS, ChipSquare, ContextChip, chipFrameClass } from '@/components/studio/composer-chips';

const source = (relative: string) => readFileSync(new URL(relative, import.meta.url), 'utf8');

describe('context chip', () => {
  const html = renderToStaticMarkup(
    <ContextChip
      testId="chip"
      accent
      data={{ skipped: false, 'overridden-by': undefined }}
      square={<ChipSquare><img src="/a.png" alt="" /></ChipSquare>}
      title="蒙版：@img5"
      subtitle="已涂 2 笔"
      remove={{ label: '清掉蒙版', onClick: () => {} }}
    />,
  );

  it('is 40px high with a 32px square, two lines of text and the remove button at the right', () => {
    expect(CHIP_HEIGHT_CLASS).toBe('h-10');
    expect(html).toMatch(/class="[^"]*\bh-10\b/);
    expect(html).toMatch(/<span class="[^"]*\bsize-8\b[^"]*"><img/);
    expect(html.indexOf('蒙版：@img5')).toBeLessThan(html.indexOf('已涂 2 笔'));
    expect(html.indexOf('已涂 2 笔')).toBeLessThan(html.indexOf('<button'));
    expect(html).toContain('aria-label="清掉蒙版"');
  });

  it('keeps the second line even when a chip has no description, so every chip is the same size', () => {
    const bare = renderToStaticMarkup(<ContextChip square={<ChipSquare>x</ChipSquare>} title="Skill" />);
    expect(bare.match(/<span class="truncate/g)).toHaveLength(2);
  });

  it('writes the data attributes that are set and skips the ones that are not', () => {
    expect(html).toContain('data-skipped="false"');
    expect(html).not.toContain('overridden-by');
  });

  it('greys a chip that is muted, and only an accent chip that is not muted is highlighted', () => {
    expect(chipFrameClass(true, false)).toContain('bg-primary/10');
    expect(chipFrameClass(true, true)).not.toContain('bg-primary/10');
    expect(chipFrameClass(true, true)).toContain('opacity-70');
    expect(chipFrameClass(false, false)).toContain('bg-surface');
    // the same height whatever the state
    for (const [accent, muted] of [[true, false], [true, true], [false, false], [false, true]] as const) expect(chipFrameClass(accent, muted)).toContain('h-10');
  });
});

describe('every chip above the composer is one component of one size', () => {
  const composer = source('./composer.tsx');

  it('builds the object, mask, branch, attachment and Skill chips from ContextChip', () => {
    for (const id of ['composer-object-chip', 'composer-mask-chip', 'composer-branch-chip', 'composer-attachment-chip', 'composer-skill-chip']) {
      expect(composer).toContain(`testId="${id}"`);
    }
    expect(composer.match(/<ContextChip/g)?.length).toBeGreaterThanOrEqual(5);
    // no leftover small chips
    expect(composer).not.toMatch(/h-8 max-w-full|size-9 shrink-0/);
  });

  it('keeps the notices for attached pictures in the attachment group, after the one row that holds every chip (#405, #697)', () => {
    // The attachment chips joined the other chips in one wrapping row, so two strips can share a line; the group that
    // follows keeps only the notices for the pictures attached (#697 removed the purpose controls).
    const row = composer.slice(composer.indexOf('data-testid="composer-chips"'), composer.indexOf('data-testid="composer-attachments"'));
    expect(row.indexOf('testId="composer-object-chip"')).toBeGreaterThan(-1);
    expect(row.indexOf('testId="composer-skill-chip"')).toBeLessThan(row.indexOf('testId="composer-attachment-chip"'));
    const group = composer.slice(composer.indexOf('data-testid="composer-attachments"'), composer.indexOf('<input\n        ref={fileInput}'));
    expect(group).not.toContain('composer-attachment-chip');
    expect(group).toContain('<ReferenceSizeNotice');
    expect(group).not.toContain('composer-attachment-purpose');
  });

  it('says a typed handle takes the turn, greys the object chip, and lets the send leave the object out', () => {
    expect(composer).toContain('overridingHandle(value, chip.handle)');
    expect(composer).toContain("'ui.studio.composer.chip.override'");
    expect(composer).toMatch(/muted=\{overriding !== ''\}/);
    const chat = source('./chat-column.tsx');
    expect(chat).toContain("overridingHandle(written, chipHandleRef.current) !== ''");
  });

  // #697: the purpose control is gone; nothing about a purpose is shown, sent or written into the message.
  it('neither shows nor sends an attachment purpose', () => {
    const chat = source('./chat-column.tsx');
    for (const gone of ['withPurposeNote', 'purposeNote', 'attachmentPurposes', 'setAttachmentPurpose', 'pinnedPurpose', 'hasMultipleInputPurposes',
      'attachment_purpose_single_input', '@/lib/chat/attachment-purpose']) {
      expect(chat).not.toContain(gone);
    }
    for (const gone of ['PurposeSegment', 'composer-attachment-purpose', 'attachment.purpose', 'onAttachmentPurpose', 'pinnedPurpose']) {
      expect(composer).not.toContain(gone);
    }
  });
});
