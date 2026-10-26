import { mentionCandidates, mentionIdentity, mentionsToken } from '@/lib/chat/mentions';

/** A picture of the chat the person asked to bring into the next message (a click on its handle tag). */
export interface ComposerReference {
  /** The server handle shown on the picture ("@img3") or model ("@model2"). */
  handle: string;
  /** A model is not a picture: its handle is only written into the text, nothing is attached. */
  kind?: 'image' | 'model';
  url: string;
  assetId?: string;
  taskId?: string;
  captureId?: string;
}

/** Index of the picture already attached to the next message that is this one, or -1. */
export function attachedIndex(
  images: readonly string[],
  sources: Readonly<Record<string, { assetId?: string; taskId?: string; captureId?: string }>>,
  reference: Pick<ComposerReference, 'url' | 'assetId' | 'taskId' | 'captureId'>,
): number {
  const wanted = mentionIdentity(reference);
  return images.findIndex((url) => {
    if (url === reference.url) return true;
    const source = sources[url];
    return Boolean(wanted && source && mentionIdentity({ ...source }) === wanted);
  });
}

/** Put the handle into the text at the caret, with a space on each side that needs one; the caret goes after it. */
export function insertReference(value: string, caret: number, handle: string): { value: string; caret: number } {
  const at = Math.min(Math.max(caret, 0), value.length);
  const before = value.slice(0, at);
  const after = value.slice(at);
  const lead = before === '' || /\s$/.test(before) ? '' : ' ';
  const trail = after.startsWith(' ') ? '' : ' ';
  const inserted = `${lead}${handle}${trail}`;
  return { value: `${before}${inserted}${after}`, caret: before.length + lead.length + handle.length + 1 };
}

/** Whether the text already names the handle, so a second click does not write it twice. */
export function namesHandle(value: string, handle: string): boolean {
  return mentionsToken(value, handle);
}

/**
 * The objects of the session a handle in the chat can bring into the next message, by handle: a picture
 * is attached and named, a model is named only. Only handles the server gave to an object of the
 * session's own messages are listed, so a handle the text merely looks like is never resolved.
 */
export function handleReferences(
  messages: Parameters<typeof mentionCandidates>[0],
): Map<string, ComposerReference> {
  const result = new Map<string, ComposerReference>();
  for (const candidate of mentionCandidates(messages)) {
    if (candidate.kind === 'model') {
      result.set(candidate.handle, { handle: candidate.handle, kind: 'model', url: '', ...(candidate.taskId ? { taskId: candidate.taskId } : {}) });
      continue;
    }
    if (!candidate.url) continue;
    result.set(candidate.handle, {
      handle: candidate.handle, url: candidate.url,
      ...(candidate.assetId ? { assetId: candidate.assetId } : {}),
      ...(candidate.taskId ? { taskId: candidate.taskId } : {}),
      ...(candidate.captureId ? { captureId: candidate.captureId } : {}),
    });
  }
  return result;
}

/** A piece of message text: plain words, or a handle written in it. */
export type HandleSegment = { text: string; handle?: string };

const TEXT_HANDLE = /(^|[\s,，。.、(（])(@(?:img|model)\d+)(?![A-Za-z0-9_])/g;

/** Split text around the picture handles it names that `known` accepts; other text stays as written. */
export function splitHandles(text: string, known: (handle: string) => boolean): HandleSegment[] {
  const segments: HandleSegment[] = [];
  let from = 0;
  for (const match of text.matchAll(TEXT_HANDLE)) {
    const handle = match[2];
    if (!known(handle)) continue;
    const start = (match.index ?? 0) + match[1].length;
    if (start > from) segments.push({ text: text.slice(from, start) });
    segments.push({ text: handle, handle });
    from = start + handle.length;
  }
  if (from < text.length) segments.push({ text: text.slice(from) });
  return segments;
}

/**
 * What a click on the handle of the object on the stage brings into the next message: the session's own
 * object of that handle, else the object's picture by its IDs; a model is named only.
 */
export function objectReference(
  object: { kind: 'image' | 'model'; url: string; taskId: string; assetId?: string; captureId?: string },
  handle: string,
  references: ReadonlyMap<string, ComposerReference>,
): ComposerReference | null {
  if (!handle) return null;
  const known = references.get(handle);
  if (known) return known;
  if (object.kind === 'model') return { handle, kind: 'model', url: '' };
  if (!object.url) return null;
  // A picture made on the board has a synthetic task id the server does not know.
  const taskId = object.taskId && !/^(canvas|upload):/.test(object.taskId) ? object.taskId : '';
  return {
    handle, url: object.url,
    ...(object.assetId ? { assetId: object.assetId } : {}),
    ...(taskId ? { taskId } : {}),
    ...(object.captureId ? { captureId: object.captureId } : {}),
  };
}
