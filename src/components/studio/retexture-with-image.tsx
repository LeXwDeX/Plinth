'use client';

import { useMemo, useState } from 'react';
import { Popover } from '@base-ui/react/popover';

import { ActionFace, ActionHint, actionButtonClass } from '@/components/studio/action-button';
import { useProductSchema } from '@/components/product-schema-provider';
import { Action, buttonVariants } from '@/components/ui/button';
import { useAnchoredPositioner } from '@/components/ui/overlay';
import { useObjectActions } from '@/hooks/use-object-actions';
import { tileOfObject, useSessionVersions } from '@/hooks/use-session-versions';
import { schemaText } from '@/lib/product-schema';
import { handleOfObject } from '@/lib/workspace/object-handle';
import { defaultRetextureTarget, retextureTargets } from '@/lib/workspace/retexture-target';
import type { ActionView } from '@/lib/workspace/stage-actions';
import { objectOfTile } from '@/lib/workspace/version-object';
import { versionLabel } from '@/lib/workspace/version-label';
import { cn } from '@/lib/utils';
import { useChatStore } from '@/store/chat-store';
import type { CurrentObject } from '@/store/workspace-store';

const NO_MESSAGES: never[] = [];

/**
 * 舞台头的「用这张图换贴图」：拿舞台上的这张图当参考，给本会话里的一个 3D 模型整套换
 * 贴图。先选目标模型（默认是这张图来自的模型，没有就是最新的 3D），说明整套重生，
 * 确认后作为一轮发给 Agent，由批准卡片写明模型、积分和余额。虚线框与这张图不同时
 * （画过、裁过、多了图层）发的是栅格化的虚线框（useObjectActions.retextureWithImage）。会话里没有 3D 模型时置灰，
 * 悬停说明第一句写明原因。按钮的图标、文字和说明来自舞台动作表的 `view`，和其他工具条按钮一样。
 */
export function RetextureWithImageAction({ object, view }: { object: CurrentObject; view: ActionView }) {
  const { schema } = useProductSchema();
  const layer = useAnchoredPositioner();
  const sessionId = useChatStore((state) => state.activeSessionId);
  const messages = useChatStore((state) => state.messagesBySession[sessionId ?? ''] ?? NO_MESSAGES);
  const versions = useSessionVersions();
  const { retextureWithImage } = useObjectActions();
  const [open, setOpen] = useState(false);
  const [chosen, setChosen] = useState<string | null>(null);

  const text = (key: string) => schemaText(schema, key);
  const targets = useMemo(() => retextureTargets(versions.tiles), [versions.tiles]);
  const picture = tileOfObject(versions, object);
  const initial = useMemo(() => defaultRetextureTarget(versions.tiles, picture?.key ?? null), [versions.tiles, picture?.key]);
  const rows = useMemo(() => targets.map((tile) => {
    const model = sessionId ? objectOfTile(tile, sessionId) : null;
    return { tile, label: versionLabel(schema, tile, 'model'), handle: model ? handleOfObject(messages, model) : '' };
  }), [messages, schema, sessionId, targets]);
  const selectedKey = chosen && rows.some((row) => row.tile.key === chosen && row.handle) ? chosen : initial?.tile.key ?? null;
  const selected = rows.find((row) => row.tile.key === selectedKey && row.handle) ?? null;
  const imageHandle = object.handle ?? handleOfObject(messages, object);

  const unavailable = rows.length === 0;

  const confirm = () => {
    if (!selected) return;
    void retextureWithImage(object, selected.handle, imageHandle);
    setOpen(false);
  };

  return (
    <Popover.Root open={open} onOpenChange={(next) => { setOpen(next); if (next) setChosen(null); }}>
      <ActionHint view={view} reason={unavailable ? text('ui.studio.stage.action.retexture_image_no_model') : undefined}>
        <Popover.Trigger
          disabled={unavailable}
          aria-label={view.label}
          data-testid={view.testId}
          data-action={view.id}
          data-ai={view.callsModel ? 'true' : 'false'}
          className={cn(buttonVariants({ variant: 'ghost', size: 'sm' }), actionButtonClass(), 'data-popup-open:bg-primary/15 data-popup-open:text-primary')}
        >
          <ActionFace view={view} />
        </Popover.Trigger>
      </ActionHint>
      <Popover.Portal>
        <Popover.Positioner side="bottom" align="end" sideOffset={8} collisionPadding={12} className={layer}>
          <Popover.Popup
            data-testid="stage-retexture-panel"
            className="flex w-72 max-w-(--available-width) flex-col gap-2 rounded-lg bg-popover p-3 text-xs text-popover-foreground shadow-xl outline-none ring-1 ring-foreground/10"
          >
            <Popover.Title className="text-xs font-semibold">{text('ui.studio.retexture.title')}</Popover.Title>
            <p className="text-muted-foreground" data-testid="stage-retexture-note">{text('ui.studio.retexture.note')}</p>
            <div role="radiogroup" aria-label={text('ui.studio.retexture.target')} className="flex flex-col gap-1">
              <span className="font-medium">{text('ui.studio.retexture.target')}</span>
              {rows.map((row) => {
                const reason = initial?.tile.key === row.tile.key
                  ? text(initial.reason === 'from_image' ? 'ui.studio.retexture.target_from_image' : 'ui.studio.retexture.target_latest')
                  : '';
                return (
                  <button
                    key={row.tile.key}
                    type="button"
                    role="radio"
                    aria-checked={selectedKey === row.tile.key}
                    disabled={!row.handle}
                    data-testid="stage-retexture-target"
                    onClick={() => setChosen(row.tile.key)}
                    className={cn(
                      'flex items-center gap-2 rounded-md border px-2 py-1.5 text-left transition-colors focus-visible:outline-2 focus-visible:outline-ring disabled:opacity-50',
                      selectedKey === row.tile.key ? 'border-primary bg-primary/15' : 'border-border bg-surface hover:bg-panel-raised',
                    )}
                  >
                    <span className="font-medium">{row.label}</span>
                    {row.handle ? <span className="text-muted-foreground">{row.handle}</span> : null}
                    {reason ? <span className="ml-auto text-[11px] text-primary">{reason}</span> : null}
                  </button>
                );
              })}
            </div>
            <div className="flex items-center gap-1.5 pt-1">
              <Action type="button" size="sm" disabled={!selected} onClick={confirm} data-testid="stage-retexture-confirm">{text('ui.studio.retexture.confirm')}</Action>
              <Action type="button" size="sm" variant="ghost" onClick={() => setOpen(false)}>{text('ui.studio.retexture.cancel')}</Action>
            </div>
          </Popover.Popup>
        </Popover.Positioner>
      </Popover.Portal>
    </Popover.Root>
  );
}
