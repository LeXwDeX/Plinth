'use client';

import type { ReactNode } from 'react';

import { HandleTag } from '@/components/chat/handle-tag';
import { useProductSchema } from '@/components/product-schema-provider';
import { KindBadge } from '@/components/studio/kind-badge';
import { useAssetURLRewriter } from '@/hooks/use-asset-url-rewriter';
import { useHandleReferences } from '@/hooks/use-handle-references';
import { useImageSize } from '@/hooks/use-image-size';
import { tileOfObject, useSessionVersions } from '@/hooks/use-session-versions';
import { objectReference } from '@/lib/chat/composer-reference';
import { formatDateTime } from '@/lib/i18n/format';
import { activeLocale } from '@/lib/i18n/locale';
import { formatSchemaText, schemaCountMessage, schemaText } from '@/lib/product-schema';
import { ratioOfSize } from '@/lib/workspace/generation-params';
import { handleOfObject } from '@/lib/workspace/object-handle';
import { promptOfObject } from '@/lib/workspace/object-prompt';
import { versionLabel } from '@/lib/workspace/version-label';
import { useChatStore } from '@/store/chat-store';
import { useWorkspaceStore, type CurrentObject } from '@/store/workspace-store';

const NO_MESSAGES: never[] = [];

function DetailRow({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="grid grid-cols-[4.5rem_minmax(0,1fr)] gap-x-2 py-1.5 text-xs">
      <dt className="text-muted-foreground">{label}</dt>
      <dd className="min-w-0 [overflow-wrap:anywhere]">{children}</dd>
    </div>
  );
}

/**
 * 右侧栏「详情」标签：当前对象的版本、种类、句柄、能力、提示词、尺寸、候选数、创建时间和任务编号。
 * 只读展示；每一行的文案来自 Schema（`ui.studio.panel.details.*`），没有的信息不画空行。
 */
export function ObjectDetails({ object }: { object: CurrentObject }) {
  const { schema } = useProductSchema();
  const sessionId = useChatStore((state) => state.activeSessionId);
  const messages = useChatStore((state) => state.messagesBySession[sessionId ?? ''] ?? NO_MESSAGES);
  const handleReferences = useHandleReferences();
  const tracked = useWorkspaceStore((state) => state.trackedTasks.find((task) => task.taskId === object.taskId));
  const versions = useSessionVersions();
  const tile = tileOfObject(versions, object);
  const { proxyAssetUrl } = useAssetURLRewriter();
  // The picture's real pixel size: the server's for a screenshot, otherwise read from the picture itself.
  const size = useImageSize(object.kind === 'image' ? proxyAssetUrl(object.url, object.taskId) : '', tile?.size ?? null);
  const text = (key: string) => schemaText(schema, `ui.studio.panel.details.${key}`);
  const capabilityId = tile?.capability || tracked?.capability || '';
  const capability = schema.capabilities.find((item) => item.id === capabilityId);
  const handle = object.handle ?? handleOfObject(messages, object);
  const handleReference = handle ? objectReference(object, handle, handleReferences) : null;
  const created = tile?.createdAt ? new Date(tile.createdAt) : null;
  const candidates = object.kind === 'image' ? (object.imageUrls?.length ?? 0) : 0;
  // What the version was asked for: the task's own prompt, else what was written in the turn that made it.
  const prompt = tracked?.prompt || promptOfObject(messages, object);
  const ratio = size ? ratioOfSize(`${size.width}x${size.height}`) : null;
  return (
    <dl className="divide-y divide-border px-3 py-1" data-testid="inspector-details">
      <DetailRow label={schemaText(schema, 'ui.studio.panel.details.version')}>{versionLabel(schema, tile, object.kind)}</DetailRow>
      <DetailRow label={schemaText(schema, 'ui.studio.panel.details.kind')}><span className="flex items-center gap-2"><KindBadge kind={object.kind} />{text(object.kind === 'model' ? 'kind_model' : 'kind_image')}</span></DetailRow>
      {handle ? (
        <DetailRow label={schemaText(schema, 'ui.studio.panel.details.handle')}>
          {handleReference
            ? <HandleTag reference={handleReference} testId="inspector-handle" className="px-0 py-0 font-mono text-xs leading-normal text-primary hover:underline hover:ring-0" />
            : <span className="font-mono">{handle}</span>}
        </DetailRow>
      ) : null}
      {capability ? <DetailRow label={schemaText(schema, 'ui.studio.panel.details.capability')}>{capability.label_cn || capability.label}</DetailRow> : null}
      {prompt ? <DetailRow label={schemaText(schema, 'ui.studio.panel.details.prompt')}><span data-testid="inspector-prompt">{prompt}</span></DetailRow> : null}
      {size ? <DetailRow label={schemaText(schema, 'ui.studio.panel.details.size')}><span className="tabular-nums" data-testid="inspector-size">{`${size.width}x${size.height}${ratio ? ` · ${ratio}` : ''}`}</span></DetailRow> : null}
      {candidates > 1 ? <DetailRow label={schemaText(schema, 'ui.studio.panel.details.candidates')}>{schemaCountMessage(schema, 'ui.studio.panel.details.candidates_value', candidates, { count: candidates })}</DetailRow> : null}
      {created && Number.isFinite(created.getTime()) ? <DetailRow label={schemaText(schema, 'ui.studio.panel.details.created')}>{formatDateTime(created, activeLocale(schema))}</DetailRow> : null}
      {object.captureId ? <DetailRow label={schemaText(schema, 'ui.studio.panel.details.capture')}>{schemaText(schema, 'ui.studio.panel.details.capture_value')}</DetailRow> : null}
      {/* A picture made on the board (merged, flattened) has no task; its synthetic id is not shown. */}
      {object.taskId.startsWith('canvas:') ? null : <DetailRow label={schemaText(schema, 'ui.studio.panel.details.task')}><span className="font-mono">{object.taskId}</span></DetailRow>}
    </dl>
  );
}
