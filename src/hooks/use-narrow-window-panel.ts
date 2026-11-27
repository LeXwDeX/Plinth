'use client';

import { useEffect } from 'react';

import { useProductSchema } from '@/components/product-schema-provider';
import { useViewportWidth } from '@/hooks/use-viewport-width';
import { panelWidth } from '@/lib/workspace/panel-tabs';
import { rightPanelFits, useStudioStore } from '@/store/studio-store';

/**
 * #696: when the window gets too narrow for the open right panel beside the chat at its 440 floor and a 320px
 * stage, the panel folds to its tab rail (any tab opens it again), so the stage and its header buttons keep their
 * room; the chat is narrowed only after that (`chatWidthMax`). It acts when the room runs out, not on every
 * render: a panel the person opens again in a narrow window stays open, and a wider window does not reopen it.
 */
export function useNarrowWindowPanel(): void {
  const { schema } = useProductSchema();
  const viewportWidth = useViewportWidth();
  const chatCollapsed = useStudioStore((state) => state.chatCollapsed);
  const fits = rightPanelFits(viewportWidth, panelWidth(schema), chatCollapsed);
  useEffect(() => {
    if (!fits) useStudioStore.getState().setPanelCollapsed(true);
  }, [fits]);
}
