import { create } from 'zustand';

import type { ComposerReference } from '@/lib/chat/composer-reference';

export interface ComposerReferenceRequest extends ComposerReference {
  /** Distinguishes two clicks on the same picture. */
  nonce: number;
}

interface ComposerReferenceState {
  request: ComposerReferenceRequest | null;
  /** Ask the composer to attach this picture to the next message and write its handle at the caret. */
  refer: (reference: ComposerReference) => void;
  clear: (nonce: number) => void;
}

let counter = 0;

export const useComposerReferenceStore = create<ComposerReferenceState>((set, get) => ({
  request: null,
  refer: (reference) => { counter += 1; set({ request: { ...reference, nonce: counter } }); },
  clear: (nonce) => { if (get().request?.nonce === nonce) set({ request: null }); },
}));
