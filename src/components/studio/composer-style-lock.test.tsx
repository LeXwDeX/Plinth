// @vitest-environment jsdom
import React, { useState } from 'react';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import type { ProductSchema } from '@/lib/product-schema';
import { Composer, type ComposerProps, type ComposerStyleLock } from './composer';

const schema = defaultCatalog.application as unknown as ProductSchema;
vi.mock('@/components/product-schema-provider', () => ({ useProductSchema: () => ({ schema }) }));
vi.mock('@/hooks/use-auth', () => ({ useAuth: () => ({ user: null }) }));
vi.mock('@/hooks/use-session-harness', () => ({ useSessionHarness: () => ({ view: null, busy: false, error: null }) }));
vi.mock('@/components/workspace/harness-summary', () => ({ HarnessSummary: () => null }));
vi.mock('@/components/result/result-card-parts', () => ({ openResultDetail: vi.fn() }));
vi.mock('@/components/ui/hint-tooltip', () => ({ HintTooltip: ({ children }: { children: React.ReactNode }) => <>{children}</> }));
vi.mock('@/components/studio/model-picker', () => ({ ModelPicker: () => null, GenerationModelNotice: () => null }));
vi.mock('@/components/studio/skill-picker', () => ({ SkillPicker: () => null }));
vi.mock('@/components/studio/chat-model-picker', () => ({ ChatModelPicker: () => null, ChatModelNotice: () => null }));
vi.mock('@/components/studio/reasoning-picker', () => ({ ReasoningPicker: () => null }));
vi.mock('@/components/studio/params-popover', () => ({ ParamsPopover: () => null }));
vi.mock('@/hooks/use-asset-url-rewriter', () => ({ useAssetURLRewriter: () => ({ previewAssetUrl: (url: string) => url }) }));

const noop = () => undefined;
const base: ComposerProps = {
  value: '', onChange: noop, onSend: noop, onStop: noop, streaming: false, locked: false, canSend: true,
  chip: null, chipSkipped: false, onToggleChip: noop, images: [], onRemoveImage: noop, pinnedImage: null, onUnpin: noop,
  acceptedImages: undefined, onFiles: noop, onPasteImages: noop, editing: false, onCancelEdit: noop, sendCredits: null, mentions: [],
  params: {} as ComposerProps['params'],
  skill: { sessionId: 's1', catalog: null, effective: null, selectedId: null, unavailable: false, loading: false, failed: false, disabled: false, onSelect: noop, onCreated: noop },
};

function Draft(overrides: Partial<ComposerProps>) {
  const [value, setValue] = useState('');
  return <Composer {...base} {...overrides} value={value} onChange={setValue} />;
}

const labels = schema.content.messages;
const styleLock = (overrides: Partial<ComposerStyleLock> = {}): ComposerStyleLock => ({
  active: true, skipped: false, notice: null, onToggleSkip: noop, onOpen: noop, ...overrides,
});

beforeEach(() => { vi.stubGlobal('React', React); });
afterEach(() => { cleanup(); vi.restoreAllMocks(); });

describe('composer style lock (#154)', () => {
  it('hides the label when the session has no lock', () => {
    render(<Draft styleLock={null} />);
    expect(screen.queryByTestId('composer-style-lock')).toBeNull();
  });

  it('shows the Schema label, opens the card and toggles the one-turn exemption', () => {
    const onOpen = vi.fn();
    const onToggleSkip = vi.fn();
    render(<Draft styleLock={styleLock({ onOpen, onToggleSkip })} />);
    const row = screen.getByTestId('composer-style-lock');
    const chip = screen.getByTestId('composer-style-lock-chip');
    expect(chip.textContent).toContain(labels['ui.studio.style_lock.chip']);
    expect(screen.getByText(labels['ui.studio.style_lock.skip_once'])).toBeTruthy();
    fireEvent.click(chip);
    expect(onOpen).toHaveBeenCalledOnce();
    fireEvent.click(screen.getByTestId('composer-style-lock-skip'));
    expect(onToggleSkip).toHaveBeenCalledOnce();
    // Narrow widths wrap the row instead of narrowing the text box.
    expect(row.className).toContain('flex-wrap');
    expect(chip.className).toContain('min-w-0');
    expect(row.compareDocumentPosition(screen.getByRole('textbox')) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });

  it('marks the exemption as checked so the person can restore the lock', () => {
    render(<Draft styleLock={styleLock({ skipped: true })} />);
    expect((screen.getByTestId('composer-style-lock-skip') as HTMLInputElement).checked).toBe(true);
    expect(screen.getByTestId('composer-style-lock')).toBeTruthy();
  });

  it('says a model that takes no reference pictures cannot use the lock references', () => {
    render(<Draft styleLock={styleLock({ notice: { kind: 'unavailable', text: labels['ui.unavailable.reason.style_lock.model_no_reference'] } })} />);
    const notice = screen.getByTestId('composer-style-lock-notice');
    expect(notice.textContent).toContain(labels['ui.unavailable.reason.style_lock.model_no_reference']);
    expect(notice.textContent).toContain('无法使用');
  });

  it('says some references will be trimmed before sending', () => {
    render(<Draft styleLock={styleLock({ notice: { kind: 'trimmed', text: labels['ui.studio.style_lock.references_trimmed'] } })} />);
    expect(screen.getByTestId('composer-style-lock-notice').textContent).toContain(labels['ui.studio.style_lock.references_trimmed']);
  });
});