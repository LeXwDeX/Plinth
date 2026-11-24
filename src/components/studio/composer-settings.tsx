'use client';

import { useEffect, useRef, useState } from 'react';
import { Settings } from 'lucide-react';

import { useProductSchema } from '@/components/product-schema-provider';
import { ChatModelPicker, useChatModelSettings } from '@/components/studio/chat-model-picker';
import { ModelPicker, generationModelPair, useDomainModelPick } from '@/components/studio/model-picker';
import { ParamsPanel, paramsSummary, type ParamsPanelProps } from '@/components/studio/params-popover';
import { SkillPicker, type SkillPickerProps } from '@/components/studio/skill-picker';
import { Action } from '@/components/ui/button';
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog';
import { HintTooltip } from '@/components/ui/hint-tooltip';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { formatSchemaText, schemaText } from '@/lib/product-schema';
import {
  COMPOSER_SETTINGS_TABS,
  SETTINGS_CHANGED_NOTE_TEXT,
  SETTINGS_TAB_TEXT,
  changedSettings,
  lockedModelDomains,
  modelsLocked,
  parametersPinned,
  visibleSettingsTab,
  type ComposerSettingsTab,
} from '@/lib/workspace/composer-settings';
import { cn } from '@/lib/utils';
import { useComposerSettingsStore } from '@/store/composer-settings-store';
import { useWorkspaceStore } from '@/store/workspace-store';

export interface ComposerSettingsProps {
  /** The Skills tab: the catalog, the session's selection and what to do with a pick (as the Skill picker took them). */
  skill: SkillPickerProps;
  /** The parameters tab: the selected models' parameters, per domain (as the parameters popover took them). */
  params: ParamsPanelProps;
  /** Uploading or preparing: the parameters cannot be edited meanwhile. */
  locked: boolean;
}

/**
 * #405: the one entry to the composer's settings, a gear right of the send button, and the dialog behind it. The four
 * pickers that used to sit under the input box (generation models, Skills, generation parameters, chat model with
 * its thinking level) are the dialog's four tabs. Each tab shows the content of the picker it replaces: the same
 * options, the same reasons for what is off, the same writes, applied the moment they are picked.
 *
 * This component stays mounted with the composer, so what the closed pickers did on their own keeps happening: the
 * retired-model switch, the chat model and levels loading for the offline notice, and the "changed" marker on the gear.
 * The gear shows a dot when a Skill is selected, a generation model is picked, a parameter was set by hand, or the
 * chat model or its level is off the administrator's default; its name and tooltip say which (and what each holds).
 * A warning dot says a selected Skill cannot be used or the chat model could not be read.
 *
 * #546: every tab's own "changed" dot explains itself (the tab's hover hint and the dot's accessible name), and the
 * models tab opens with a line naming the pinned models and a button that gives the choice back to the Agent.
 */
export function ComposerSettings({ skill, params, locked }: ComposerSettingsProps) {
  const { schema } = useProductSchema();
  const open = useComposerSettingsStore((state) => state.open);
  const wantedTab = useComposerSettingsStore((state) => state.tab);
  const setOpen = useComposerSettingsStore((state) => state.setOpen);
  const setTab = useComposerSettingsStore((state) => state.setTab);
  const gear = useRef<HTMLButtonElement>(null);

  const image = useDomainModelPick('image');
  const model3d = useDomainModelPick('model-3d');
  const chat = useChatModelSettings();
  const modelByDomain = useWorkspaceStore((state) => state.modelByDomain);

  const text = (key: string) => schemaText(schema, key);
  const separator = text('ui.studio.list_separator');
  const title = text('ui.studio.composer.settings.title');
  const tabLabel = (id: ComposerSettingsTab) => text(SETTINGS_TAB_TEXT[id]);
  const offered = COMPOSER_SETTINGS_TABS.filter((id) => id !== 'chat' || chat.offered);
  const tab = visibleSettingsTab(wantedTab, offered) ?? 'models';

  const skillName = skill.selectedId ? skill.catalog?.skills.find((item) => item.id === skill.selectedId)?.displayName ?? skill.selectedId : '';
  const changes = changedSettings({
    models: { changed: modelsLocked(modelByDomain), value: generationModelPair(schema, image, model3d) },
    skills: { changed: Boolean(skill.selectedId), value: skillName },
    params: { changed: parametersPinned(params.domains.map((domain) => domain.parameterStates)), value: paramsSummary(schema, params.params) },
    chat: { changed: chat.changed, value: chat.value },
  }).filter((change) => offered.includes(change.tab));
  const changedTabs = new Set(changes.map((change) => change.tab));
  const items = changes
    .map((change) => (change.value ? formatSchemaText(text('ui.studio.composer.settings.item_value'), { name: tabLabel(change.tab), value: change.value }) : tabLabel(change.tab)))
    .join(separator);
  const changed = changes.length > 0;
  // #546: the models tab says which pins hold and gives the choice back to the Agent (the pins outlive the session by design).
  const lockedDomains = lockedModelDomains(modelByDomain);
  const picks = { image, 'model-3d': model3d } as const;
  const lockedNames = lockedDomains.map((domain) => picks[domain].short).join(separator);
  const restoreAuto = () => { for (const domain of lockedDomains) picks[domain].clear(); };

  // A selected Skill that cannot be used, or a chat model that could not be read: the short reasons, as every hint says it (#94).
  const skillProblem = skill.selectedId && !skill.loading && (skill.failed || skill.unavailable)
    ? text(skill.failed ? 'ui.unavailable.reason.picker.skill_load_failed' : 'ui.unavailable.reason.picker.skill_unavailable')
    : '';
  const attention = [skillProblem, ...chat.attention].filter(Boolean);

  // Closing the dialog hands the focus back to the gear, which Base UI treats like keyboard focus: the hint would open
  // over the send button and stay until something else took the focus (the same trap the pickers' panels had, #324).
  // The hint stays shut from the moment the dialog opens until the pointer enters or leaves the gear or the focus moves on.
  const [hintHeld, setHintHeld] = useState(false);
  useEffect(() => { if (open) setHintHeld(true); }, [open]);
  const releaseHint = () => { if (!useComposerSettingsStore.getState().open) setHintHeld(false); };

  const label = changed ? formatSchemaText(text('ui.studio.composer.settings.label_changed'), { title, items }) : title;
  const hint = changed ? formatSchemaText(text('ui.studio.composer.settings.changed'), { items }) : text('ui.studio.composer.settings.hint');

  return (
    <>
      <HintTooltip
        name={title}
        hint={hint}
        reason={attention.length > 0 ? attention.join(separator) : undefined}
        suppressed={open || hintHeld}
        passive
        triggerProps={{ onPointerEnter: releaseHint, onPointerLeave: releaseHint, onBlur: releaseHint }}
      >
        <Action
          ref={gear}
          type="button"
          variant="ghost"
          size="icon-sm"
          aria-label={label}
          aria-haspopup="dialog"
          aria-expanded={open}
          data-testid="composer-settings-open"
          data-changed={changed ? 'true' : 'false'}
          data-attention={attention.length > 0 ? 'true' : 'false'}
          onClick={() => setOpen(true)}
          className="relative text-muted-foreground"
        >
          <Settings aria-hidden className="size-4" />
          {changed || attention.length > 0 ? (
            <span
              aria-hidden="true"
              data-testid="composer-settings-dot"
              className={cn('absolute right-1 top-1 size-1.5 rounded-full', attention.length > 0 ? 'bg-warning' : 'bg-primary')}
            />
          ) : null}
        </Action>
      </HintTooltip>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent
          closeLabel={text('ui.studio.composer.settings.close')}
          finalFocus={gear}
          data-testid="composer-settings-dialog"
          // Large, centred, and bounded by the window on both axes: the head stays, the body scrolls (#405).
          className="flex h-[min(48rem,calc(100dvh-3rem))] w-[min(76rem,calc(100vw-2rem))] max-w-none flex-col gap-0 overflow-hidden bg-popover p-0 sm:max-w-none"
        >
          <div className="shrink-0 px-5 pb-3 pt-4 pr-12">
            <DialogTitle>{title}</DialogTitle>
            <DialogDescription className="mt-1.5 text-xs">{text('ui.studio.composer.settings.hint')}</DialogDescription>
          </div>
          <Tabs value={tab} onValueChange={(value) => setTab(value as ComposerSettingsTab)} className="min-h-0 flex-1 gap-0">
            <TabsList
              variant="line"
              activateOnFocus
              aria-label={text('ui.studio.composer.settings.tabs')}
              className="group-data-horizontal/tabs:h-auto w-full max-w-full shrink-0 justify-start overflow-x-auto border-b border-border px-4 pb-0"
            >
              {offered.map((id) => {
                const trigger = (
                  <TabsTrigger
                    key={id}
                    value={id}
                    data-testid={`composer-settings-tab-${id}`}
                    data-changed={changedTabs.has(id) ? 'true' : undefined}
                    className="h-9 flex-none px-3 group-data-horizontal/tabs:after:bottom-0 focus-visible:ring-inset"
                  >
                    {tabLabel(id)}
                    {changedTabs.has(id) ? (
                      // #546: the dot says what it means, to the pointer (the tab's hint) and to a screen reader (its name).
                      <span role="img" aria-label={text(SETTINGS_CHANGED_NOTE_TEXT[id])} data-testid={`composer-settings-tab-dot-${id}`} className="size-1.5 rounded-full bg-primary" />
                    ) : null}
                  </TabsTrigger>
                );
                return changedTabs.has(id)
                  ? <HintTooltip key={id} name={tabLabel(id)} hint={text(SETTINGS_CHANGED_NOTE_TEXT[id])} passive className="flex-none">{trigger}</HintTooltip>
                  : trigger;
              })}
            </TabsList>
            <div data-testid="composer-settings-body" className="min-h-0 flex-1 overflow-y-auto px-5 py-4 [scrollbar-gutter:stable]">
              <TabsContent value="models" className="flex flex-col gap-3">
                {lockedDomains.length > 0 ? (
                  <div role="status" data-testid="composer-settings-models-locked" className="flex flex-wrap items-center gap-2 rounded-md border border-primary/40 bg-primary/10 px-3 py-2 text-xs">
                    <span className="min-w-0 flex-1">{formatSchemaText(text('ui.studio.composer.settings.models_locked'), { models: lockedNames })}</span>
                    <Action type="button" variant="outline" size="xs" onClick={restoreAuto} data-testid="composer-settings-models-reset">
                      {text('ui.studio.composer.settings.models_reset')}
                    </Action>
                  </div>
                ) : null}
                <ModelPicker image={image} model={model3d} />
              </TabsContent>
              <TabsContent value="skills"><SkillPicker {...skill} /></TabsContent>
              <TabsContent value="params"><ParamsPanel {...params} disabled={locked} /></TabsContent>
              {chat.offered ? <TabsContent value="chat"><ChatModelPicker settings={chat} /></TabsContent> : null}
            </div>
          </Tabs>
        </DialogContent>
      </Dialog>
    </>
  );
}
