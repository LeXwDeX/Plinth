import { ATTACHMENT_PURPOSES, type AttachmentPurpose } from '@/lib/chat/attachment-purpose';

/**
 * The unsent part of a session's composer, kept in the browser so that switching session, a refresh or an
 * expired login does not lose it. Only what can be rebuilt is kept: text, the purpose, and pictures that are
 * server assets (addressed by URL with their platform IDs). A picture read from the person's disk lives in
 * memory as a data address and is dropped; the count is kept so the next visit can say so.
 */
export interface ComposerDraftSource {
  assetId?: string;
  taskId?: string;
  captureId?: string;
}

export interface ComposerDraft {
  text: string;
  images: string[];
  sources: Record<string, ComposerDraftSource>;
  purpose: AttachmentPurpose | null;
  /** Local pictures that could not be kept. */
  dropped: number;
}

export const DRAFT_KEY_PREFIX = 'artai.composer.draft.';

type DraftStorage = Pick<Storage, 'getItem' | 'setItem' | 'removeItem'>;

function defaultStorage(): DraftStorage | null {
  try {
    return typeof window === 'undefined' ? null : window.localStorage;
  } catch {
    return null;
  }
}

/** A picture that only exists in this tab's memory. */
export function isLocalOnlyImage(url: string): boolean {
  return url.startsWith('data:') || url.startsWith('blob:');
}

export function draftKey(sessionId: string): string {
  return `${DRAFT_KEY_PREFIX}${sessionId}`;
}

export function isEmptyDraft(draft: Pick<ComposerDraft, 'text' | 'images' | 'dropped'>): boolean {
  return draft.text.trim() === '' && draft.images.length === 0 && draft.dropped === 0;
}

/** Keeps the persistable part of the live composer state. */
export function draftFromState(state: {
  text: string;
  images: readonly string[];
  sources: Readonly<Record<string, ComposerDraftSource | undefined>>;
  purpose: AttachmentPurpose | null;
}): ComposerDraft {
  const images = state.images.filter((url) => !isLocalOnlyImage(url));
  const sources: Record<string, ComposerDraftSource> = {};
  for (const url of images) {
    const source = state.sources[url];
    if (source) sources[url] = { ...(source.assetId ? { assetId: source.assetId } : {}), ...(source.taskId ? { taskId: source.taskId } : {}), ...(source.captureId ? { captureId: source.captureId } : {}) };
  }
  return {
    text: state.text,
    images,
    sources,
    purpose: images.length > 0 ? state.purpose : null,
    dropped: state.images.length - images.length,
  };
}

export function saveDraft(sessionId: string, draft: ComposerDraft, storage: DraftStorage | null = defaultStorage()): void {
  if (!storage || !sessionId) return;
  try {
    if (isEmptyDraft(draft)) storage.removeItem(draftKey(sessionId));
    else storage.setItem(draftKey(sessionId), JSON.stringify(draft));
  } catch {
    // Storage may be full or blocked: the draft then lives in memory only.
  }
}

export function clearDraft(sessionId: string, storage: DraftStorage | null = defaultStorage()): void {
  if (!storage || !sessionId) return;
  try { storage.removeItem(draftKey(sessionId)); } catch { /* see saveDraft */ }
}

/** The stored draft, or null when there is none or it is not readable. Stored data is untrusted. */
export function loadDraft(sessionId: string, storage: DraftStorage | null = defaultStorage()): ComposerDraft | null {
  if (!storage || !sessionId) return null;
  try {
    const raw = storage.getItem(draftKey(sessionId));
    if (!raw) return null;
    const value = JSON.parse(raw) as Partial<ComposerDraft> | null;
    if (!value || typeof value !== 'object') return null;
    const images = Array.isArray(value.images) ? value.images.filter((url): url is string => typeof url === 'string' && url !== '' && !isLocalOnlyImage(url)) : [];
    const sources: Record<string, ComposerDraftSource> = {};
    for (const url of images) {
      const source = value.sources?.[url];
      if (!source || typeof source !== 'object') continue;
      const entry: ComposerDraftSource = {};
      if (typeof source.assetId === 'string') entry.assetId = source.assetId;
      if (typeof source.taskId === 'string') entry.taskId = source.taskId;
      if (typeof source.captureId === 'string') entry.captureId = source.captureId;
      sources[url] = entry;
    }
    const purpose = ATTACHMENT_PURPOSES.find((item) => item === value.purpose) ?? null;
    const draft: ComposerDraft = {
      text: typeof value.text === 'string' ? value.text : '',
      images,
      sources,
      purpose: images.length > 0 ? purpose : null,
      dropped: typeof value.dropped === 'number' && value.dropped > 0 ? Math.floor(value.dropped) : 0,
    };
    return isEmptyDraft(draft) ? null : draft;
  } catch {
    return null;
  }
}
