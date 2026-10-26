'use client';

import { useProductSchema } from '@/components/product-schema-provider';
import { Action } from '@/components/ui/button';
import { formatSchemaText, schemaCountMessage, schemaText } from '@/lib/product-schema';
import { activeLocale } from '@/lib/i18n/locale';
import { groupedCount } from '@/lib/workspace/model-stats';
import { partsKeyOf } from '@/lib/workspace/model-parts';
import { cn } from '@/lib/utils';
import { useStudioStore } from '@/store/studio-store';
import type { CurrentObject } from '@/store/workspace-store';

/** 右侧栏的「部件」标签：模型的各个部件；点一行在舞台上高亮，选中的一行可单独导出为 GLB。 */
export function ModelPartsPanel({ object }: { object: CurrentObject }) {
  const { schema } = useProductSchema();
  const key = partsKeyOf(object);
  const parts = useStudioStore((state) => (state.stageParts?.key === key ? state.stageParts.parts : null));
  const selectedId = useStudioStore((state) => (state.selectedPart?.key === key ? state.selectedPart.id : null));
  const exportingId = useStudioStore((state) => (state.partExportRequest?.key === key ? state.partExportRequest.id : null));
  const text = (name: string) => schemaText(schema, `ui.studio.panel.parts.${name}`);

  if (!parts || parts.length === 0) {
    return <p role="status" className="p-3 text-xs leading-5 text-muted-foreground" data-testid="model-parts-empty">{text('empty')}</p>;
  }
  const select = (id: string) => useStudioStore.getState().setSelectedPart(selectedId === id ? null : { key, id });
  return (
    <div className="flex flex-col gap-2 p-3" data-testid="model-parts">
      <p className="text-xs leading-5 text-muted-foreground">{parts.length === 1 ? text('single_hint') : text('hint')}</p>
      <ul aria-label={text('list')} className="flex flex-col gap-1">
        {parts.map((part, index) => {
          const selected = part.id === selectedId;
          const name = part.name || formatSchemaText(text('name'), { index: index + 1 });
          return (
            <li key={part.id} data-testid="model-part-row" className={cn('rounded-md border', selected ? 'border-primary bg-primary/10' : 'border-border bg-surface')}>
              <button
                type="button"
                aria-pressed={selected}
                onClick={() => select(part.id)}
                className="flex w-full items-baseline justify-between gap-2 rounded-md px-2 py-1.5 text-left text-xs hover:bg-panel-raised focus-visible:outline-2 focus-visible:outline-ring"
              >
                <span className="min-w-0 truncate font-medium">{name}</span>
                <span className="shrink-0 tabular-nums text-muted-foreground">{schemaCountMessage(schema, 'ui.studio.panel.parts.triangles', part.triangles, { count: groupedCount(part.triangles, activeLocale(schema)) })}</span>
              </button>
              {selected ? (
                <div className="px-2 pb-2">
                  <Action
                    type="button"
                    variant="outline"
                    size="sm"
                    disabled={exportingId !== null}
                    loading={exportingId === part.id}
                    onClick={() => useStudioStore.getState().requestPartExport(key, part.id)}
                    data-testid="model-part-export"
                  >
                    {exportingId === part.id ? text('exporting') : text('export')}
                  </Action>
                </div>
              ) : null}
            </li>
          );
        })}
      </ul>
    </div>
  );
}
