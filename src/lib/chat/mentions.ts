import type { ChatAttachmentClaim, ChatMentionClaim } from '@/lib/chat/attachments';
import type { ChatAttachment, ChatMessage } from '@/store/chat-store';

/**
 * Where a candidate comes from. `history` objects already have a server-assigned handle; the others
 * are referenceable images of the session that have none yet (a picture waiting in the composer, a
 * generated version, a layer on the canvas) and get one when the turn is sent.
 */
export type MentionSource = 'history' | 'upload' | 'version' | 'canvas';

/** The objects of a session an `@` can name: one entry per referenceable image or model. */
export interface MentionCandidate {
  /** The token written into the text: the server handle for history objects, a client token otherwise. */
  handle: string;
  kind: 'image' | 'model';
  taskId?: string;
  assetId?: string;
  captureId?: string;
  url?: string;
  source?: MentionSource;
  /** The name the list shows ("上传的图 1", "2D v3", "图层 2"); history objects show their handle. */
  name?: string;
  /** Other names of the same image, found by typing them (a canvas layer that shows a version). */
  aliases?: string[];
  /** Every source the same image was found in (history first). */
  sources?: MentionSource[];
  /** 1-based position among the pictures attached to the next message (uploads only). */
  slot?: number;
  /** The whole canvas: the dashed frame rasterised when the turn is sent. It has no IDs of its own. */
  board?: boolean;
}


/**
 * The handles of the session, newest first. A handle exists only because the
 * server gave it to an attachment of a message, so the list never invents one;
 * picking a handle writes the same token the server resolves against the
 * session's own history when the Agent uses it, and an unknown or foreign
 * handle is refused there.
 */
export function mentionCandidates(
  messages: readonly (Pick<ChatMessage, 'attachments'> & Partial<Pick<ChatMessage, 'images'>>)[],
): MentionCandidate[] {
  const seen = new Set<string>();
  const result: MentionCandidate[] = [];
  for (let index = messages.length - 1; index >= 0; index -= 1) {
    const attachments: readonly ChatAttachment[] = messages[index].attachments ?? [];
    for (const attachment of attachments) {
      if (!attachment.handle || seen.has(attachment.handle)) continue;
      seen.add(attachment.handle);
      // A message picture is named by its slot; the attachment itself carries no address for it.
      const slotUrl = attachment.imageSlot ? messages[index].images?.[attachment.imageSlot - 1]?.url : undefined;
      result.push({
        handle: attachment.handle,
        kind: attachment.kind,
        taskId: attachment.taskId,
        assetId: attachment.assetId,
        captureId: attachment.captureId,
        url: attachment.url ?? slotUrl,
        source: 'history',
        sources: ['history'],
      });
    }
  }
  return result;
}

/** An image the next message already carries, in attach order. */
export interface PendingMentionImage {
  url: string;
  name: string;
  claim?: Pick<ChatAttachmentClaim, 'assetId' | 'taskId' | 'captureId'>;
}

/** A generated version with a finished picture. */
export interface VersionMentionImage {
  name: string;
  taskId: string;
  assetId: string;
  url: string;
}

/** A picture layer on the session's canvas. */
export interface LayerMentionImage {
  name: string;
  url: string;
  taskId?: string;
  assetId?: string;
  captureId?: string;
}

export interface MentionSources {
  history: readonly MentionCandidate[];
  uploads: readonly PendingMentionImage[];
  versions: readonly VersionMentionImage[];
  layers: readonly LayerMentionImage[];
}

/** The token of a client-named image: "@" and the name without whitespace. */
export function mentionToken(name: string): string {
  return `@${name.replace(/\s+/g, '')}`;
}

type Identity = { assetId?: string; taskId?: string; captureId?: string; url?: string };

/** What makes two entries the same picture: its platform IDs, or its address when it has none (a pasted data URI has neither). */
export function mentionIdentity(item: Identity): string {
  if (item.captureId) return `capture:${item.captureId}`;
  if (item.assetId) return `asset:${item.taskId ?? ''}/${item.assetId}`;
  // A platform asset address names the same picture as its task and asset IDs.
  const route = item.url ? /\/tasks\/([^/?#]+)\/assets\/([^/?#]+)/.exec(item.url) : null;
  if (route) return `asset:${route[1]}/${route[2]}`;
  if (item.url && !item.url.startsWith('data:')) return `url:${item.url}`;
  return '';
}

/**
 * Every image the `@` can name in this session: the pictures the session already shows (with their
 * server handles), the pictures waiting in the composer, generated versions and canvas layers. One
 * picture is one candidate: when it is found in several places the earliest source keeps it (so a
 * stable server handle wins) and the other names become aliases the filter also matches.
 */
export function buildMentionCandidates(sources: MentionSources): MentionCandidate[] {
  const result: MentionCandidate[] = [];
  const byIdentity = new Map<string, MentionCandidate>();
  const taken = new Set(sources.history.map((candidate) => candidate.handle));
  // Two different pictures can carry the same name (two uploads of one file name, versions with one
  // label); each still needs its own token, or the server would read both as one repeated mention.
  // `_` ends a token's word, so "@图片" never matches inside "@图片_2".
  const uniqueHandle = (handle: string): string => {
    let unique = handle;
    for (let count = 2; taken.has(unique); count += 1) unique = `${handle}_${count}`;
    taken.add(unique);
    return unique;
  };
  const add = (candidate: MentionCandidate, alias: string | undefined) => {
    const identity = candidate.kind === 'image' ? mentionIdentity(candidate) : '';
    const existing = identity ? byIdentity.get(identity) : undefined;
    if (existing) {
      if (alias && alias !== existing.name) existing.aliases = [...(existing.aliases ?? []), alias];
      for (const source of candidate.sources ?? []) if (!existing.sources?.includes(source)) existing.sources = [...(existing.sources ?? []), source];
      existing.url ??= candidate.url;
      return;
    }
    if (candidate.source !== 'history') candidate = { ...candidate, handle: uniqueHandle(candidate.handle) };
    result.push(candidate);
    if (identity) byIdentity.set(identity, candidate);
  };
  for (const candidate of sources.history) add({ ...candidate, source: 'history', sources: ['history'] }, undefined);
  sources.uploads.forEach((item, index) => {
    add({
      handle: mentionToken(item.name), kind: 'image', url: item.url, name: item.name, source: 'upload', sources: ['upload'], slot: index + 1,
      ...item.claim,
    }, item.name);
  });
  for (const item of sources.versions) {
    add({ handle: mentionToken(item.name), kind: 'image', url: item.url, taskId: item.taskId, assetId: item.assetId, name: item.name, source: 'version', sources: ['version'] }, item.name);
  }
  for (const item of sources.layers) {
    add({
      handle: mentionToken(item.name), kind: 'image', url: item.url, taskId: item.taskId, assetId: item.assetId, captureId: item.captureId,
      name: item.name, source: 'canvas', sources: ['canvas'],
    }, item.name);
  }
  return result;
}

/**
 * The first entry of the `@` list: the whole canvas (the dashed frame). It is not a picture of the session,
 * so it carries no IDs; writing its token makes the send rasterise the frame like the Agent's current object.
 */
export function boardMentionCandidate(name: string): MentionCandidate {
  return { handle: mentionToken(name), kind: 'image', name, source: 'canvas', sources: ['canvas'], board: true };
}

/** The list with the canvas entry first; unchanged when the session has no dashed frame. */
export function withBoardMention(candidates: readonly MentionCandidate[], board: MentionCandidate | null): MentionCandidate[] {
  if (!board) return [...candidates];
  return [board, ...candidates.filter((candidate) => candidate.handle !== board.handle)];
}

/** Whether the text names the canvas entry. */
export function mentionsBoard(text: string, candidates: readonly MentionCandidate[]): boolean {
  const board = candidates.find((candidate) => candidate.board);
  return board !== undefined && mentionsToken(text, board.handle);
}

/** The claim that names a candidate to the server (the turn's `selection`); uploads travel as message images instead. */
export function mentionClaim(candidate: MentionCandidate): ChatAttachmentClaim | null {
  if (candidate.kind !== 'image' || candidate.board) return null;
  const claim: ChatAttachmentClaim = { kind: 'image' };
  if (candidate.captureId) claim.captureId = candidate.captureId;
  if (candidate.taskId) claim.taskId = candidate.taskId;
  if (candidate.assetId) claim.assetId = candidate.assetId;
  if (candidate.url && !candidate.url.startsWith('data:')) claim.url = candidate.url;
  return claim.captureId || claim.assetId || claim.url ? claim : null;
}

function escapeRegExp(text: string): string {
  return text.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
}

/** Whether the text names this token as a whole word. */
export function mentionsToken(text: string, token: string): boolean {
  return new RegExp(`(^|[\\s,，。.、(（])${escapeRegExp(token)}(?![A-Za-z0-9_])`).test(text);
}

/**
 * The turn's mention contract. A history object's token is its server handle and travels as written.
 * Every other picture written in the text is named to the server with a claim: a picture waiting in the
 * message by its position among the message images (`imageSlot`), any other by its platform IDs or
 * address. The server verifies the claim for the owner and session, gives the picture a handle in its
 * usual order and rewrites the token in the stored text; a mention it cannot verify stays plain text.
 */
export function mentionsForTurn(
  text: string,
  candidates: readonly MentionCandidate[],
  picked: readonly MentionCandidate[] = [],
): ChatMentionClaim[] {
  const result: ChatMentionClaim[] = [];
  const claimFor = (candidate: MentionCandidate): ChatAttachmentClaim | null => {
    if (candidate.source === 'upload') return candidate.slot ? { kind: 'image', imageSlot: candidate.slot } : null;
    return mentionClaim(candidate);
  };
  const handled = new Set<string>();
  // A name that was picked from the list keeps meaning the picture it was picked for. The names of layers
  // and waiting uploads are numbered, so removing one renumbers the rest: the picked picture is found again
  // by what it is, and a token whose picture is gone stays plain text rather than naming another picture.
  for (const pick of picked) {
    if (pick.source === 'history' || pick.board || handled.has(pick.handle) || !mentionsToken(text, pick.handle)) continue;
    handled.add(pick.handle);
    const key = mentionKey(pick);
    const current = key ? candidates.find((candidate) => candidate.source !== 'history' && mentionKey(candidate) === key) : undefined;
    const claim = current ? claimFor(current) : null;
    if (claim) result.push({ token: pick.handle, claim });
  }
  for (const candidate of candidates) {
    if (candidate.source === 'history' || candidate.board || handled.has(candidate.handle) || !mentionsToken(text, candidate.handle)) continue;
    const claim = claimFor(candidate);
    if (claim) result.push({ token: candidate.handle, claim });
  }
  return result;
}

/** What identifies a candidate across renumbering: its picture's identity, or its address (a pasted data URI has no identity). */
function mentionKey(candidate: MentionCandidate): string {
  return mentionIdentity(candidate) || (candidate.url ? `src:${candidate.url}` : '');
}

export interface ActiveMention {
  /** Index of the `@` in the text. */
  start: number;
  /** What was typed after it, up to the caret. */
  query: string;
}

const TRIGGER = '@';

/**
 * The `@` word the caret is in: it starts at the beginning of the text or after
 * whitespace and has no whitespace between it and the caret.
 */
export function activeMention(value: string, caret: number): ActiveMention | null {
  if (caret < 1 || caret > value.length) return null;
  for (let index = caret - 1; index >= 0; index -= 1) {
    const char = value[index];
    if (/\s/.test(char)) return null;
    if (char === TRIGGER) {
      if (index > 0 && !/\s/.test(value[index - 1])) return null;
      return { start: index, query: value.slice(index + 1, caret) };
    }
  }
  return null;
}

export function filterMentions(candidates: readonly MentionCandidate[], query: string): MentionCandidate[] {
  const needle = query.trim().toLowerCase();
  if (!needle) return [...candidates];
  const wanted = needle.replace(/^@/, '').replace(/\s+/g, '');
  return candidates.filter((candidate) => [candidate.handle, candidate.name ?? '', ...(candidate.aliases ?? [])]
    .some((name) => name.toLowerCase().replace(/^@/, '').replace(/\s+/g, '').includes(wanted)));
}

/** Replace the `@word` with the picked handle and a space; the caret goes after it. */
export function insertMention(value: string, mention: ActiveMention, caret: number, handle: string): { value: string; caret: number } {
  const before = value.slice(0, mention.start);
  const after = value.slice(caret);
  const inserted = `${handle}${after.startsWith(' ') ? '' : ' '}`;
  return { value: `${before}${inserted}${after}`, caret: before.length + handle.length + 1 };
}

const HANDLE_PATTERN = /(^|[\s,，。.、(（])(@(?:img|model|ref)\d+)(?![A-Za-z0-9])/g;

/** Every handle the text names, in the order written. */
export function typedHandles(text: string): string[] {
  return Array.from(text.matchAll(HANDLE_PATTERN), (match) => match[2]);
}

/**
 * The handle that takes the turn from the current object: the last handle written in the
 * text when it is not the current object's own. Empty when nothing is written, when the
 * text names the current object, or when the current object has no known handle.
 */
export function overridingHandle(text: string, currentHandle: string): string {
  if (!currentHandle) return '';
  const typed = typedHandles(text);
  if (typed.length === 0 || typed.includes(currentHandle)) return '';
  return typed[typed.length - 1];
}
