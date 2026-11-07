// @vitest-environment jsdom
import React from 'react';
import { cleanup, fireEvent, render, screen, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import { Composer, type ComposerProps } from '@/components/studio/composer';
import type { ProductSchema } from '@/lib/product-schema';
import { useChatStore } from '@/store/chat-store';
import { useSessionContextStore } from '@/store/session-context-store';

const schema = defaultCatalog.application as unknown as ProductSchema;
const messages = defaultCatalog.application.content.messages as Record<string, string>;

vi.mock('@/components/product-schema-provider', () => ({ useProductSchema: () => ({ schema }) }));
vi.mock('@/hooks/use-auth', () => ({ useAuth: () => ({ user: { id: 'owner' } }) }));
vi.mock('@/hooks/use-session-harness', () => ({ useSessionHarness: () => ({ view: null, busy: false, error: null }) }));
vi.mock('@/components/workspace/harness-summary', () => ({ HarnessSummary: () => null }));
vi.mock('@/components/result/result-card-parts', () => ({ openResultDetail: vi.fn() }));
vi.mock('@/components/ui/hint-tooltip', () => ({ HintTooltip: ({ children }: { children: React.ReactNode }) => <>{children}</> }));
vi.mock('@/components/studio/model-picker', () => ({ ModelPicker: () => null, GenerationModelNotice: () => null }));
vi.mock('@/components/studio/skill-picker', () => ({ SkillPicker: () => null }));
vi.mock('@/components/studio/chat-model-picker', () => ({ ChatModelPicker: () => null, ChatModelNotice: () => null }));
vi.mock('@/components/studio/params-popover', () => ({ ParamsPopover: () => null }));

const noop = () => undefined;
const props: ComposerProps = {
  value: '', onChange: noop, onSend: noop, onStop: noop, streaming: true, locked: false, canSend: false,
  chip: null, chipSkipped: false, onToggleChip: noop, images: [], onRemoveImage: noop, pinnedImage: null, onUnpin: noop,
  acceptedImages: undefined, onFiles: noop, onPasteImages: noop, editing: false, onCancelEdit: noop, sendCredits: null, mentions: [],
  params: {} as ComposerProps['params'],
  skill: { sessionId: 's1', catalog: null, effective: null, selectedId: null, unavailable: false, loading: false, failed: false, disabled: false, onSelect: noop, onCreated: noop },
};

beforeEach(() => {
  vi.stubGlobal('React', React);
  useChatStore.setState({ activeSessionId: 's1', streamingBuffers: {} });
  useSessionContextStore.setState({ context: {}, compacting: {} });
});
afterEach(() => {
  cleanup();
  useChatStore.setState({ activeSessionId: null, streamingBuffers: {} });
});

// #137: the stop offers two choices only while the server says the waiting turn may continue in the
// background; otherwise it stays the single stop that cancels the turn's Tasks.
describe('composer stop choices', () => {
  it('keeps the single stop when the turn may not continue in the background', () => {
    const onStop = vi.fn();
    const view = render(<Composer {...props} onStop={onStop} onStopInBackground={null} />);
    expect(within(view.container).queryByTestId('composer-stop-choices')).toBeNull();
    fireEvent.click(within(view.container).getByTestId('composer-stop'));
    expect(onStop).toHaveBeenCalledTimes(1);
  });

  it('shows nothing to stop while no turn streams', () => {
    const view = render(<Composer {...props} streaming={false} onStopInBackground={vi.fn()} />);
    expect(within(view.container).queryByTestId('composer-stop')).toBeNull();
    expect(within(view.container).queryByTestId('composer-stop-choices')).toBeNull();
  });

  it('offers stop-and-cancel and stop-waiting with the Schema labels and calls each', async () => {
    const onStop = vi.fn();
    const onStopInBackground = vi.fn();
    const view = render(<Composer {...props} onStop={onStop} onStopInBackground={onStopInBackground} />);
    expect(within(view.container).queryByTestId('composer-stop')).toBeNull();
    const trigger = within(view.container).getByTestId('composer-stop-choices');
    expect(trigger.textContent).toContain(messages['ui.workspace.agent.stop']);

    fireEvent.click(trigger);
    const background = await screen.findByTestId('composer-stop-background');
    expect(background.textContent).toContain(messages['ui.studio.composer.stop_background']);
    expect(background.textContent).toContain(messages['ui.studio.composer.stop_background_hint']);
    expect(screen.getByTestId('composer-stop-cancel').textContent).toContain(messages['ui.studio.composer.stop_cancel_task']);
    fireEvent.click(background);
    expect(onStopInBackground).toHaveBeenCalledTimes(1);
    expect(onStop).not.toHaveBeenCalled();

    fireEvent.click(trigger);
    fireEvent.click(await screen.findByTestId('composer-stop-cancel'));
    expect(onStop).toHaveBeenCalledTimes(1);
    expect(onStopInBackground).toHaveBeenCalledTimes(1);
  });
});
