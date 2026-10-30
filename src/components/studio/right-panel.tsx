'use client';

import { useMemo, useRef, type KeyboardEvent } from 'react';

import { CanvasHistoryPanel } from '@/components/canvas/history-panel';
import { CanvasLayersPanel } from '@/components/canvas/layers-panel';
import { useProductSchema } from '@/components/product-schema-provider';
import { ModelPartsPanel } from '@/components/studio/model-parts-panel';
import { ObjectDetails } from '@/components/studio/object-details';
import { HintTooltip } from '@/components/ui/hint-tooltip';
import { iconByName } from '@/components/ui/icon-map';
import { ModelArchiveLink, ModelFormatsPanel } from '@/components/workspace/model-formats';
import { ModelProcessingPanel } from '@/components/workspace/model-processing-panel';
import { HarnessPanel } from '@/components/workspace/harness-panel';
import { useModelProcessingCapabilities } from '@/hooks/use-model-processing-capabilities';
import { schemaText } from '@/lib/product-schema';
import {
  activePanelTab, ALL_PANEL_TABS_AVAILABLE, panelContextOf, panelTabHintContent, panelTabViews, panelWidth, steppedPanelTab,
  type PanelAvailability, type PanelContext, type PanelTabId, type PanelTabView,
} from '@/lib/workspace/panel-tabs';
import { selectionForSession } from '@/lib/workspace/session-scope';
import { cn } from '@/lib/utils';
import { useCanvasStore } from '@/store/canvas-store';
import { useChatStore } from '@/store/chat-store';
import { useStudioStore } from '@/store/studio-store';
import { useWorkspaceStore, type CurrentObject } from '@/store/workspace-store';

/** The id the tab buttons point at (`aria-controls`) and the one tab body carries. */
const BODY_ID = 'right-panel-body';
const tabDomId = (id: PanelTabId) => `right-panel-tab-${id}`;

interface PanelTabBarProps {
  views: readonly PanelTabView[];
  active: PanelTabId | null;
  onSelect: (id: PanelTabId) => void;
  /** The accessible name of the tab list. */
  label: string;
  /** Tabs shown but not selectable (no object or board content for them yet). */
  disabled?: readonly PanelTabId[];
}

/**
 * The 44px tab bar: an icon and the name on each tab, a 2px underline under the active one, and the hover
 * hint (name, shortcut, one line of what it is for) from the Schema. Left / Right / Home / End move between
 * the tabs the way a tab list does; the active tab is the one in the tab order.
 */
export function PanelTabBar({ views, active, onSelect, label, disabled = [] }: PanelTabBarProps) {
  const { schema } = useProductSchema();
  const buttons = useRef(new Map<PanelTabId, HTMLButtonElement>());
  const ids = views.map((view) => view.id).filter((id) => !disabled.includes(id));
  const onKeyDown = (event: KeyboardEvent<HTMLButtonElement>, current: PanelTabId) => {
    const next = steppedPanelTab(ids, current, event.key);
    if (!next) return;
    event.preventDefault();
    onSelect(next);
    buttons.current.get(next)?.focus();
  };
  return (
    <div role="tablist" aria-label={label} className="flex h-11 shrink-0 border-b border-border" data-testid="right-panel-tabs">
      {views.map((view) => {
        const Icon = iconByName(view.icon);
        const selected = view.id === active;
        const isDisabled = disabled.includes(view.id);
        return (
          <HintTooltip key={view.id} {...panelTabHintContent(schema, view)} className="min-w-0 flex-1">
            <button
              type="button"
              role="tab"
              id={tabDomId(view.id)}
              aria-selected={selected}
              aria-disabled={isDisabled || undefined}
              aria-controls={BODY_ID}
              tabIndex={selected ? 0 : -1}
              data-testid={tabDomId(view.id)}
              ref={(element) => {
                if (element) buttons.current.set(view.id, element);
                else buttons.current.delete(view.id);
              }}
              onClick={() => { if (!isDisabled) onSelect(view.id); }}
              onKeyDown={(event) => onKeyDown(event, view.id)}
              className={cn(
                'relative flex h-11 w-full min-w-0 items-center justify-center gap-1 px-1 text-xs font-medium outline-none transition-colors focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring/50',
                selected ? 'text-foreground' : 'text-muted-foreground hover:text-foreground',
                isDisabled && 'cursor-not-allowed opacity-50 hover:text-muted-foreground',
              )}
            >
              <Icon aria-hidden className="size-4 shrink-0" />
              <span className="truncate">{view.label}</span>
              {selected ? <span aria-hidden className="absolute inset-x-1 bottom-0 h-0.5 bg-foreground" data-testid="right-panel-underline" /> : null}
            </button>
          </HintTooltip>
        );
      })}
    </div>
  );
}

interface PanelBodyProps {
  tab: PanelTabId;
  object: CurrentObject | null;
  sessionId: string | null;
}

/** What each tab shows. The panels keep their own scrolling and spacing (12px); this only binds an id to a body. */
function PanelBody({ tab, object, sessionId }: PanelBodyProps) {
  switch (tab) {
    case 'operations':
      return <HarnessPanel sessionId={sessionId} object={object} />;
    case 'layers':
      return sessionId ? <CanvasLayersPanel sessionId={sessionId} /> : null;
    case 'history':
      return sessionId ? <CanvasHistoryPanel sessionId={sessionId} /> : null;
    case 'details':
      return object ? <div className="min-h-0 flex-1 overflow-y-auto"><ObjectDetails object={object} /></div> : null;
    case 'process':
      return object?.kind === 'model' ? <ModelProcessingPanel key={`${object.taskId}:${object.assetId}`} selection={object} /> : null;
    case 'formats':
      return object?.kind === 'model' ? (
        <ModelFormatsPanel
          key={object.taskId}
          taskId={object.taskId}
          sessionId={sessionId}
          pinned
          header={<ModelArchiveLink taskId={object.taskId} className="self-start" />}
        />
      ) : null;
    case 'parts':
      return object?.kind === 'model' ? <div className="min-h-0 flex-1 overflow-y-auto"><ModelPartsPanel object={object} /></div> : null;
    default:
      return null;
  }
}

interface PanelFrameProps {
  context: PanelContext;
  object: CurrentObject | null;
  sessionId: string | null;
  availability: PanelAvailability;
}

function PanelFrame({ context, object, sessionId, availability }: PanelFrameProps) {
  const { schema } = useProductSchema();
  const remembered = useStudioStore((state) => (context === 'model' ? state.panelTab3d : state.panelTab2d));
  const setPanelTab = useStudioStore((state) => state.setPanelTab);
  const aside = 'flex min-h-0 shrink-0 flex-col border-l border-border bg-card';
  const width = { width: panelWidth(schema) };
  const title = schemaText(schema, 'ui.studio.panel.title');

  // Empty board and nothing selected: the board's tabs (Schema), with only function settings usable.
  if (context === 'none') {
    const views = panelTabViews(schema, 'board');
    const disabled = views.map((view) => view.id).filter((id) => id !== 'operations');
    return (
      <aside aria-label={title} data-testid="right-panel" data-kind="none" data-tab="operations" style={width} className={aside}>
        <PanelTabBar
          views={views}
          active="operations"
          onSelect={() => undefined}
          label={schemaText(schema, 'ui.studio.panel.tabs')}
          disabled={disabled}
        />
        <p data-testid="right-panel-board-empty" className="shrink-0 border-b border-border px-3 py-2 text-xs text-muted-foreground">
          {schemaText(schema, 'ui.studio.panel.board_empty')}
        </p>
        <div
          role="tabpanel"
          id={BODY_ID}
          aria-labelledby={tabDomId('operations')}
          data-testid="right-panel-operations"
          className="flex min-h-0 flex-1 flex-col"
        >
          <HarnessPanel sessionId={sessionId} object={null} />
        </div>
      </aside>
    );
  }
  const views = panelTabViews(schema, context);
  const active = activePanelTab(schema, context, remembered, availability);
  return (
    <aside aria-label={title} data-testid="right-panel" data-kind={context} data-tab={active ?? undefined} style={width} className={aside}>
      <PanelTabBar
        views={views}
        active={active}
        onSelect={(id) => setPanelTab(context, id)}
        label={schemaText(schema, 'ui.studio.panel.tabs')}
      />
      {active ? (
        <div
          role="tabpanel"
          id={BODY_ID}
          aria-labelledby={tabDomId(active)}
          data-testid={`right-panel-${active}`}
          className="flex min-h-0 flex-1 flex-col"
        >
          <PanelBody tab={active} object={object} sessionId={sessionId} />
        </div>
      ) : null}
    </aside>
  );
}

/** Processing availability is kept separate from function settings, which can prepare a selection without a source. */
function ModelPanel({ object, sessionId }: { object: CurrentObject; sessionId: string | null }) {
  const { available, offerings, error } = useModelProcessingCapabilities();
  // While the Offerings are still loading 加工 counts as available (it shows its own loading state), so the panel does not jump.
  const processAvailable = offerings === undefined ? error === undefined : available.length > 0;
  const availability = useMemo<PanelAvailability>(
    () => (processAvailable ? ALL_PANEL_TABS_AVAILABLE : { unavailable: ['process'] }),
    [processAvailable],
  );
  return <PanelFrame context="model" object={object} sessionId={sessionId} availability={availability} />;
}

/**
 * The right panel (Schema `canvas.frontend.panel_tabs`), one panel for every kind of thing on the stage.
 * Function settings are available for every stage context, including an empty board. The remaining tabs
 * still describe or arrange the stage object. It always has its width: no
 * collapse button, no remembered width. The tab last chosen is kept for this visit (one for 2D, one for 3D).
 */
export function RightPanel() {
  const sessionId = useChatStore((state) => state.activeSessionId);
  const object = selectionForSession(useWorkspaceStore((state) => state.currentObject), sessionId);
  const boardNodes = useCanvasStore((state) => state.nodesBySession[sessionId ?? '']?.length ?? 0);
  const context = panelContextOf(object, boardNodes);
  if (context === 'model' && object) return <ModelPanel object={object} sessionId={sessionId} />;
  return <PanelFrame context={context} object={object} sessionId={sessionId} availability={ALL_PANEL_TABS_AVAILABLE} />;
}
