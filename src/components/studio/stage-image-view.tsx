'use client';

import { useEffect, useRef, useState } from 'react';
import { X } from 'lucide-react';

import { useProductSchema } from '@/components/product-schema-provider';
import { Action } from '@/components/ui/button';
import { safePreviewURL } from '@/lib/core/tasks/asset-url-rewriter';
import { schemaText } from '@/lib/product-schema';
import { handleLabel } from '@/lib/workspace/object-handle';
import { useChatStore } from '@/store/chat-store';
import { useStudioStore } from '@/store/studio-store';

/**
 * #87: one picture shown large inside the stage area: it covers the stage only, never the chat column, so the
 * card that opened it stays readable. It closes on Esc (taken before the stage's own Esc), on a click
 * outside the picture, on the close button, and when the session changes. The picture's chat handle, when it
 * has one, is shown beside its name as on the canvas badges.
 */
export function StageImageView() {
  const image = useStudioStore((state) => state.stageImage);
  if (!image) return null;
  return <StageImageModal />;
}

function StageImageModal() {
  const { schema } = useProductSchema();
  const image = useStudioStore((state) => state.stageImage);
  const close = useStudioStore((state) => state.closeStageImage);
  const sessionId = useChatStore((state) => state.activeSessionId);
  const openedFor = useRef(sessionId);
  const closeButton = useRef<HTMLButtonElement>(null);
  const [broken, setBroken] = useState(false);
  const url = image?.url ?? '';

  useEffect(() => { setBroken(false); }, [url]);
  useEffect(() => {
    if (openedFor.current !== sessionId) close();
  }, [sessionId, close]);
  useEffect(() => {
    closeButton.current?.focus({ preventScroll: true });
    // Esc closes this view first; the stage's own Esc (leave compare, leave focus) does not also run.
    const onKey = (event: KeyboardEvent) => {
      if (event.key !== 'Escape') return;
      event.preventDefault();
      event.stopImmediatePropagation();
      close();
    };
    window.addEventListener('keydown', onKey, true);
    return () => window.removeEventListener('keydown', onKey, true);
  }, [close]);

  if (!image) return null;
  const title = schemaText(schema, 'ui.studio.stage_image.title');
  const closeLabel = schemaText(schema, 'ui.studio.stage_image.close');
  const src = safePreviewURL(image.url);
  return (
    <div
      role="dialog"
      aria-label={`${title} · ${image.label}`}
      data-testid="stage-image-view"
      className="absolute inset-0 z-float flex flex-col bg-black/80"
      // A press on the backdrop (not on the picture or the header buttons) closes.
      onPointerDown={(event) => { if (event.target === event.currentTarget) close(); }}
    >
      <header className="flex h-11 shrink-0 items-center gap-2 px-3 text-white" onPointerDown={(event) => { if (event.target === event.currentTarget) close(); }}>
        <h2 className="min-w-0 truncate text-sm font-medium" data-testid="stage-image-label">{image.label}</h2>
        {image.handle ? (
          <span className="shrink-0 rounded-sm bg-black/60 px-1.5 py-0.5 font-mono text-[11px] leading-none text-white ring-1 ring-white/20" data-testid="stage-image-handle">{handleLabel(image.handle)}</span>
        ) : null}
        <span className="flex-1" />
        <Action ref={closeButton} type="button" variant="ghost" size="icon-sm" onClick={close} aria-label={closeLabel} title={closeLabel} className="text-white hover:bg-white/10 hover:text-white" data-testid="stage-image-close"><X /></Action>
      </header>
      <div
        className="flex min-h-0 flex-1 items-center justify-center p-4"
        onPointerDown={(event) => { if (event.target === event.currentTarget) close(); }}
      >
        {broken || !src ? (
          <p role="status" className="text-sm text-white/70">{schemaText(schema, 'ui.studio.lightbox.missing')}</p>
        ) : (
          <img
            src={src}
            alt={image.label}
            draggable={false}
            onError={() => setBroken(true)}
            className="max-h-full max-w-full select-none rounded-md object-contain shadow-lg"
            data-testid="stage-image-picture"
          />
        )}
      </div>
    </div>
  );
}
