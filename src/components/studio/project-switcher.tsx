'use client';

import { useState } from 'react';
import { Popover } from '@base-ui/react/popover';
import { Check, ChevronDown, Folder, Loader2, Plus } from 'lucide-react';
import { toast } from 'sonner';

import { useProductSchema } from '@/components/product-schema-provider';
import { Action } from '@/components/ui/button';
import { HintTooltip } from '@/components/ui/hint-tooltip';
import { anchoredPositionMethod, useAnchoredPositioner } from '@/components/ui/overlay';
import { useProjects } from '@/hooks/use-projects';
import { projectContract, schemaText } from '@/lib/product-schema';
import { useChatStore } from '@/store/chat-store';
import { cn } from '@/lib/utils';

/**
 * #240: the project switcher left of the session menu. Choosing a project
 * assigns the open session; creating one selects it. The project a new
 * session joins is the last selection (or the open session's project).
 */
export function ProjectSwitcher() {
  const { schema } = useProductSchema();
  const sessionId = useChatStore((state) => state.activeSessionId);
  const projects = useProjects(sessionId);
  const [open, setOpen] = useState(false);
  const [name, setName] = useState('');
  const [busy, setBusy] = useState(false);
  const layer = useAnchoredPositioner();
  const projectSchema = projectContract(schema);
  if (!projectSchema) return null;
  const maximum = projectSchema.limits.maximum_name_characters;
  const text = (key: string) => schemaText(schema, `ui.project.${key}`);

  const choose = async (projectId: string | null) => {
    if (busy) return;
    setBusy(true);
    try {
      await projects.select(projectId);
      setOpen(false);
    } catch {
      toast.error(text('switch_failed'));
    } finally {
      setBusy(false);
    }
  };
  const create = async () => {
    const value = name.trim();
    if (!value || busy) return;
    setBusy(true);
    try {
      await projects.create(value);
      setName('');
      setOpen(false);
    } catch {
      toast.error(text('create_failed'));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="flex min-w-0 items-center" data-testid="project-switcher">
      <Popover.Root open={open} onOpenChange={setOpen}>
        <HintTooltip name={text('switch')} className="shrink-0">
          <Popover.Trigger
            aria-label={text('switch')}
            data-testid="project-switcher-trigger"
            className="flex h-7 max-w-44 min-w-0 items-center gap-1 rounded-md px-1.5 text-xs text-muted-foreground transition-colors hover:bg-muted hover:text-foreground focus-visible:outline-2 focus-visible:outline-ring data-popup-open:bg-primary/15 data-popup-open:text-primary"
          >
            <Folder aria-hidden className="size-3.5 shrink-0" />
            <span className="truncate">{projects.activeProject?.name ?? text('none')}</span>
            <ChevronDown aria-hidden className="size-3 shrink-0" />
          </Popover.Trigger>
        </HintTooltip>
        <Popover.Portal>
          <Popover.Positioner positionMethod={anchoredPositionMethod} side="bottom" align="start" sideOffset={6} collisionPadding={12} className={layer}>
            <Popover.Popup className="flex max-h-[min(28rem,calc(100dvh-6rem))] w-72 max-w-(--available-width) flex-col gap-1.5 overflow-y-auto rounded-lg bg-popover p-2 text-popover-foreground shadow-xl outline-none ring-1 ring-foreground/10">
              <Popover.Title className="px-1 text-[11px] font-medium text-muted-foreground">{text('switch')}</Popover.Title>
              {projects.loading ? (
                <span role="status" className="flex items-center gap-2 px-1 py-2 text-xs text-muted-foreground"><Loader2 aria-hidden className="size-3.5 animate-spin" />{text('loading')}</span>
              ) : null}
              {projects.failed ? (
                <Action type="button" variant="ghost" size="sm" className="justify-start text-xs" onClick={() => void projects.refresh()}>{text('load_failed')}</Action>
              ) : null}
              <ul className="flex flex-col" data-testid="project-switcher-list">
                <li>
                  <button type="button" disabled={busy} onClick={() => void choose(null)} className="flex w-full items-center gap-1.5 rounded px-1.5 py-1.5 text-left text-xs hover:bg-muted disabled:opacity-50" data-testid="project-switcher-clear">
                    <span className="flex size-3.5 items-center justify-center">{!projects.activeProjectId ? <Check aria-hidden className="size-3.5" /> : null}</span>
                    <span className="truncate">{text('none')}</span>
                  </button>
                </li>
                {projects.projects.map((project) => {
                  const selected = project.id === projects.activeProjectId;
                  return (
                    <li key={project.id}>
                      <button type="button" disabled={busy} onClick={() => void choose(project.id)} className={cn('flex w-full items-center gap-1.5 rounded px-1.5 py-1.5 text-left text-xs hover:bg-muted disabled:opacity-50', selected && 'font-medium')} data-testid={`project-switcher-project-${project.id}`}>
                        <span className="flex size-3.5 items-center justify-center">{selected ? <Check aria-hidden className="size-3.5" /> : null}</span>
                        <span className="truncate">{project.name}</span>
                      </button>
                    </li>
                  );
                })}
                {projects.loaded && projects.projects.length === 0 ? (
                  <li className="px-1.5 py-1 text-xs text-muted-foreground">{text('empty')}</li>
                ) : null}
              </ul>
              <form className="flex items-center gap-1 border-t border-border pt-1.5" onSubmit={(event) => { event.preventDefault(); void create(); }}>
                <label className="sr-only" htmlFor="project-switcher-name">{text('name')}</label>
                <input
                  id="project-switcher-name"
                  value={name}
                  maxLength={maximum}
                  onChange={(event) => setName(event.target.value)}
                  placeholder={text('name_placeholder')}
                  className="min-w-0 flex-1 rounded-md border border-input bg-background px-2 py-1 text-xs outline-none focus-visible:border-ring"
                  data-testid="project-switcher-name"
                />
                <Action type="submit" variant="ghost" size="xs" disabled={busy || !name.trim()} data-testid="project-switcher-create">
                  <Plus aria-hidden />
                  {text('create')}
                </Action>
              </form>
            </Popover.Popup>
          </Popover.Positioner>
        </Popover.Portal>
      </Popover.Root>
    </div>
  );
}