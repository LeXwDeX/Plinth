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
  const params = source('./params-popover.tsx');

  it('builds the object, "not this turn", mask, branch, attachment and Skill chips from ContextChip', () => {
    for (const id of ['composer-object-chip', 'composer-mask-chip', 'composer-branch-chip', 'composer-attachment-chip', 'composer-skill-chip']) {
      expect(composer).toContain(`testId="${id}"`);
    }
    expect(composer.match(/<ContextChip/g)?.length).toBeGreaterThanOrEqual(6);
    // no leftover small chips
    expect(composer).not.toMatch(/h-8 max-w-full|size-9 shrink-0/);
  });

  it('keeps the parameters button out of the attachment box and puts it in the bottom row, in the pickers\' frame', () => {
    const chips = composer.slice(composer.indexOf('data-testid="composer-chips"'), composer.indexOf('data-testid="composer-attachments"'));
    expect(chips).not.toContain('<ParamsPopover');
    const attachments = composer.slice(composer.indexOf('data-testid="composer-attachments"'), composer.indexOf('<input\n        ref={fileInput}'));
    expect(attachments).not.toContain('<ParamsPopover');
    const bottom = composer.slice(composer.indexOf('data-testid="composer-pickers"'));
    expect(bottom.indexOf('<AgentScopePicker')).toBeLessThan(bottom.indexOf('<ParamsPopover {...params}'));
    expect(params).toContain('PICKER_TRIGGER_CLASS');
    expect(params).toContain('paramsRatioText');
    expect(params).not.toContain('chipFrameClass');
  });

  it('keeps the attachment purpose inside the attachment group, after the attachment chips', () => {
    const group = composer.slice(composer.indexOf('data-testid="composer-attachments"'), composer.indexOf('<input\n        ref={fileInput}'));
    expect(group.indexOf('composer-attachment-chip')).toBeGreaterThan(-1);
    expect(group.indexOf('composer-attachment-chip')).toBeLessThan(group.indexOf('composer-attachment-purpose"'));
  });

  it('lays the bottom row out on fixed tracks: two narrow, four wide, one model picker, parameters beside the Agent mode', () => {
    expect(composer).toMatch(/grid min-w-0 grid-cols-2 [^"]*@\[40rem\]:grid-cols-4[^"]*" data-testid="composer-pickers"/);
    const bottom = composer.slice(composer.indexOf('data-testid="composer-pickers"'));
    expect(bottom.match(/<ModelPicker/g)?.length).toBe(1);
    expect(bottom.indexOf('<ModelPicker />')).toBeLessThan(bottom.indexOf('<SkillPicker'));
    // Pairs left to right in DOM order: models | Skill, Agent mode | parameters, chat model | thinking level, then the context row.
    const order = ['<ModelPicker />', '<SkillPicker', '<AgentScopePicker />', '<ParamsPopover {...params}', '<ChatModelPicker />', '<ReasoningPicker />', '<ContextMeter />'].map((tag) => bottom.indexOf(tag));
    expect(order.every((at) => at > -1)).toBe(true);
    expect([...order].sort((a, b) => a - b)).toEqual(order);
    expect(composer).not.toContain('col-start-');
    const popover = source('./table-popover.tsx');
    expect(popover).toContain('h-7 w-full min-w-0');
    expect(popover).not.toContain('max-w-44');
    // The trigger shows the short value; the hover text is the whole name. A switched-off table says why in its own title row.
    expect(popover).toContain('`${label}: ${fullValue ?? value}`');
    expect(popover).toContain('disabledReason');
    expect(source('./model-picker.tsx')).toContain('pickerTriggerNames(selected, table.rows');
    expect(source('./model-picker.tsx')).toContain("sections={[image.section, model.section]}");
    expect(source('./skill-picker.tsx')).toContain("'ui.studio.skill.short'");
    expect(source('./agent-scope-picker.tsx')).toContain('ui.studio.agent_scope.short_${current.scope}');
  });

  it('says a typed handle takes the turn, greys the object chip, and lets the send leave the object out', () => {
    expect(composer).toContain('overridingHandle(value, chip.handle)');
    expect(composer).toContain("'ui.studio.composer.chip.override'");
    expect(composer).toMatch(/muted=\{overriding !== ''\}/);
    const chat = source('./chat-column.tsx');
    expect(chat).toContain("overridingHandle(written, chipHandleRef.current) !== ''");
  });

  it('writes the purpose chosen for an attachment into the message', () => {
    const chat = source('./chat-column.tsx');
    expect(chat).toContain('withPurposeNote(written, purposeNote)');
    expect(chat).toContain('`ui.studio.composer.attachment.note_${attachmentPurpose}`');
    expect(chat).toContain('setAttachmentPurpose(null)');
    expect(composer).toContain('data-testid="composer-attachment-purpose"');
  });
});
