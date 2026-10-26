import { createContext, useContext } from 'react';

export type OverlayLayer = 'base' | 'modal' | 'blocking';

/** React context survives portals, so controls inside a dialog stay above it. */
export const OverlayLayerContext = createContext<OverlayLayer>('base');

const floatingLayer: Record<OverlayLayer, string> = {
  base: 'z-float',
  modal: 'z-modal-float',
  blocking: 'z-blocking-float',
};

export function useAnchoredPositioner(): string {
  return `isolate ${floatingLayer[useContext(OverlayLayerContext)]}`;
}

export const anchoredPopup = [
  "max-h-(--available-height)",
  "min-w-(--anchor-width)",
  "max-w-(--available-width)",
  "origin-(--transform-origin)",
  "overflow-x-hidden overflow-y-auto",
  "rounded-lg bg-popover text-popover-foreground",
  "shadow-md ring-1 ring-foreground/10",
  "duration-100 outline-none",
  "data-open:animate-in data-open:fade-in-0 data-open:zoom-in-95",
  "data-closed:animate-out data-closed:fade-out-0 data-closed:zoom-out-95",
].join(" ")
