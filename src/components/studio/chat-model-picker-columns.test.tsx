// @vitest-environment jsdom
import React from 'react';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import type { ProductSchema } from '@/lib/product-schema';
import type { ChatModelChoice, ChatReasoning } from '@/lib/chat/browser-client';
import { ChatModelPicker } from '@/components/studio/chat-model-picker';
import { useChatStore } from '@/store/chat-store';

const catalog = defaultCatalog.application as unknown as ProductSchema;
const messages = catalog.content.messages;

const state = vi.hoisted(() => ({
  choice: null as ChatModelChoice | null,
  reasoning: null as ChatReasoning | null,
  setModel: vi.fn(async () => true),
  setLevel: vi.fn(async () => true),
}));

vi.mock('@/components/product-schema-provider', async () => {
  const catalogModule = await import('../../../internal/catalog/default_catalog.json');
  return { useProductSchema: () => ({ schema: catalogModule.default.application }) };
});
vi.mock('@/hooks/use-session-chat-model', () => ({
  useSessionChatModel: () => ({ supported: true, choice: state.choice, busy: false, setModel: state.setModel }),
}));
vi.mock('@/hooks/use-session-reasoning', () => ({
  useSessionReasoning: () => ({ supported: true, reasoning: state.reasoning, busy: false, setLevel: state.setLevel }),
}));

const two: ChatModelChoice = {
  selected: 'GLM', chosen: 'GLM', defaultId: 'LLM', fallback: false,
  options: [{ id: 'LLM', model: 'deepseek' }, { id: 'GLM', model: 'glm-5.3' }],
};

function fill(template: string, values: Record<string, string>) {
  return Object.entries(values).reduce((text, [key, value]) => text.replace(`{${key}}`, value), template);
}

function openPicker(label: string) {
  render(<ChatModelPicker />);
  fireEvent.click(screen.getByRole('button', { name: label }));
  return screen.findByTestId('workspace-chat-model-picker-panel');
}

beforeEach(() => {
  useChatStore.setState({ activeSessionId: 's1' });
  state.choice = two;
  state.reasoning = { effort: 'max', levels: ['low', 'high', 'max'], defaultLevel: 'high' };
  state.setModel.mockClear();
  state.setLevel.mockClear();
});
afterEach(() => {
  cleanup();
  useChatStore.setState({ activeSessionId: null });
});

describe('the chat model picker in two columns (#223)', () => {
  it('lists the models on the left and the highlighted model levels on the right', async () => {
    const panel = await openPicker(`${messages['ui.studio.chat_model.combined_label']}: glm-5.3 · max`);
    expect(panel.getAttribute('data-layout')).toBe('columns');
    const models = within(panel).getByRole('menu', { name: messages['ui.studio.chat_model.label'] });
    const levelsTitle = fill(messages['ui.studio.chat_model.levels_title'], { model: 'glm-5.3' });
    const levels = within(panel).getByRole('menu', { name: levelsTitle });
    expect(within(models).getAllByRole('menuitemradio').map((row) => row.textContent)).toEqual([
      `deepseek${messages['ui.studio.chat_model.default_tag']}`, 'glm-5.3',
    ]);
    expect(within(models).getByRole('menuitemradio', { checked: true }).textContent).toBe('glm-5.3');
    const levelRows = within(levels).getAllByRole('menuitemradio');
    expect(levelRows.map((row) => row.textContent)).toEqual(['low', `high${messages['ui.studio.reasoning.default_tag']}`, 'max']);
    expect(within(levels).getByRole('menuitemradio', { checked: true }).textContent).toBe('max');
  });

  it('keeps the panel open when a model is picked so its levels follow, and closes on a level', async () => {
    await openPicker(`${messages['ui.studio.chat_model.combined_label']}: glm-5.3 · max`);
    fireEvent.click(screen.getByRole('menuitemradio', { name: /deepseek/ }));
    expect(state.setModel).toHaveBeenCalledWith('LLM');
    expect(screen.getByTestId('workspace-chat-model-picker-panel')).toBeTruthy();
    fireEvent.click(screen.getByRole('menuitemradio', { name: 'low' }));
    expect(state.setLevel).toHaveBeenCalledWith('low');
    await waitFor(() => expect(screen.queryByTestId('workspace-chat-model-picker-panel')).toBeNull());
  });

  it('explains on the right when the model offers no levels', async () => {
    state.reasoning = { effort: '', levels: [], defaultLevel: '' };
    const panel = await openPicker(`${messages['ui.studio.chat_model.combined_label']}: glm-5.3`);
    const levels = within(panel).getByTestId('workspace-reasoning-section');
    expect(within(levels).queryAllByRole('menuitemradio')).toHaveLength(0);
    expect(levels.textContent).toContain(fill(messages['ui.studio.chat_model.levels_none'], { model: 'glm-5.3' }));
  });

  it('notes in the model column when the chosen model went offline', async () => {
    state.choice = { selected: 'LLM', chosen: 'OLD', defaultId: 'LLM', fallback: true, options: two.options };
    const panel = await openPicker(`${messages['ui.studio.chat_model.combined_label']}: deepseek · max`);
    const models = within(panel).getByTestId('workspace-chat-model-section');
    expect(within(models).getByTestId('unavailable-label').textContent).toContain(messages['ui.unavailable.reason.picker.chat_model_offline']);
  });
});
