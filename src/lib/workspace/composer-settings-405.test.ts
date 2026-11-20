import { describe, expect, it } from 'vitest';

import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import {
  COMPOSER_SETTINGS_TABS,
  SETTINGS_TAB_TEXT,
  changedSettings,
  chatSettingsChanged,
  isComposerSettingsTab,
  modelsLocked,
  parametersPinned,
  visibleSettingsTab,
} from '@/lib/workspace/composer-settings';

const messages = (defaultCatalog.application as { content: { messages: Record<string, string> } }).content.messages;
const english = (defaultCatalog.application as unknown as { localization: { bundles: { en: { messages: Record<string, string> } } } }).localization.bundles.en.messages;

describe('the composer settings tabs (#405)', () => {
  it('has the four tabs of the Issue, in order', () => {
    expect([...COMPOSER_SETTINGS_TABS]).toEqual(['models', 'skills', 'params', 'chat']);
  });

  it('names every tab from the Schema, in both locales', () => {
    for (const tab of COMPOSER_SETTINGS_TABS) {
      expect(messages[SETTINGS_TAB_TEXT[tab]], tab).toBeTruthy();
      expect(english[SETTINGS_TAB_TEXT[tab]], tab).toBeTruthy();
    }
    expect(COMPOSER_SETTINGS_TABS.map((tab) => messages[SETTINGS_TAB_TEXT[tab]])).toEqual(['生成模型', 'Skills', '生成参数', '对话模型与思考档位']);
  });

  it('recognises only the four tab ids', () => {
    expect(isComposerSettingsTab('skills')).toBe(true);
    expect(isComposerSettingsTab('scope')).toBe(false);
    expect(isComposerSettingsTab(undefined)).toBe(false);
  });

  it('shows the wanted tab when it is offered and the first offered one when it is not', () => {
    expect(visibleSettingsTab('chat', ['models', 'skills', 'params', 'chat'])).toBe('chat');
    // The chat model tab is left out when the administrator offers nothing to choose.
    expect(visibleSettingsTab('chat', ['models', 'skills', 'params'])).toBe('models');
    expect(visibleSettingsTab('params', [])).toBeNull();
  });
});

describe('what counts as a changed setting (#405)', () => {
  it('counts a generation model as soon as either domain has one picked', () => {
    expect(modelsLocked({ image: null, 'model-3d': null })).toBe(false);
    expect(modelsLocked({ image: 'gpt-image', 'model-3d': null })).toBe(true);
    expect(modelsLocked({ image: null, 'model-3d': 'meshy-7.1' })).toBe(true);
  });

  it('counts a parameter the person pinned, not one that is default or came from the Agent', () => {
    expect(parametersPinned([undefined, {}])).toBe(false);
    expect(parametersPinned([{ ratio: { source: 'default' }, count: { source: 'agent' } }])).toBe(false);
    expect(parametersPinned([{ ratio: { source: 'default' } }, { count: { source: 'user' } }])).toBe(true);
  });

  it('counts a chat model or thinking level that differs from the default', () => {
    const choice = { selected: 'GLM', defaultId: 'LLM', options: [{ id: 'LLM', model: 'deepseek' }, { id: 'GLM', model: 'glm' }] };
    const levels = { effort: 'high', defaultLevel: 'high' };
    expect(chatSettingsChanged(choice, levels)).toBe(true);
    expect(chatSettingsChanged({ ...choice, selected: 'LLM' }, levels)).toBe(false);
    expect(chatSettingsChanged({ ...choice, selected: 'LLM' }, { effort: 'max', defaultLevel: 'high' })).toBe(true);
    // Nothing loaded, or a model without levels, is the default.
    expect(chatSettingsChanged(null, null)).toBe(false);
    expect(chatSettingsChanged({ ...choice, selected: 'LLM' }, { effort: '', defaultLevel: '' })).toBe(false);
  });

  it('lists the changed tabs in tab order with the value each one holds', () => {
    expect(changedSettings({})).toEqual([]);
    expect(changedSettings({
      chat: { changed: true, value: 'qwen · xhigh' },
      models: { changed: true, value: 'GPT IMAGE · MESHY' },
      skills: { changed: false, value: 'ignored' },
      params: { changed: true },
    })).toEqual([
      { tab: 'models', value: 'GPT IMAGE · MESHY' },
      { tab: 'params' },
      { tab: 'chat', value: 'qwen · xhigh' },
    ]);
  });
});
