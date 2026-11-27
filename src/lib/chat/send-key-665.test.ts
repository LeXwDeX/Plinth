import { describe, expect, it, vi } from 'vitest';
import catalog from '../../../internal/catalog/default_catalog.json';
import type { ProductSchema } from '@/lib/product-schema';
import { createChatBrowserClient } from './browser-client';
import {
  createUncertainSendStore, forgetUncertainSend, MAX_UNCERTAIN_SENDS, newSendKey, parseUncertainSends, rememberUncertainSend,
  sendKeyFor, sendSignature, sendUnderKey, UNCERTAIN_SEND_MAX_AGE_MS, UNCERTAIN_SENDS_KEY,
} from './send-key';
import { ChatBrowserError } from './browser-client';
import { IDEMPOTENCY_CONFLICT_CODE, idempotencyRefused } from './send-error-codes';
import { turnActiveRefusal } from './turn-error-text';
import { enqueueTurn, parseQueue, takeQueuedTurn, writeQueue, SEND_QUEUE_KEY } from './send-queue';

// #665: a send that starts a turn carries an Idempotency-Key, so a repeat of a send whose answer was lost is answered
// with the turn it started instead of starting a second, paid one.

const schema = catalog.application as unknown as ProductSchema;

function accepted() {
  return new Response(JSON.stringify({ messageId: 'm-1', sessionId: 's' }), { status: 202 });
}

function headerOf(init: RequestInit | undefined, name: string): string | null {
  return new Headers(init?.headers).get(name);
}

describe('startTurn idempotency key', () => {
  it('sends the key as the Idempotency-Key header, and no header without one', async () => {
    const fetcher = vi.fn(async (_input: RequestInfo | URL, _init?: RequestInit) => accepted());
    const client = createChatBrowserClient(schema, { fetcher });
    await client.startTurn('s', 'draw a cat', { idempotencyKey: 'send-1' });
    expect(headerOf(fetcher.mock.calls[0]?.[1], 'Idempotency-Key')).toBe('send-1');
    // The key is a header, never a member of the body the server fingerprints.
    expect(JSON.parse(String(fetcher.mock.calls[0]?.[1]?.body))).not.toHaveProperty('idempotencyKey');
    await client.startTurn('s', 'draw a cat', {});
    expect(headerOf(fetcher.mock.calls[1]?.[1], 'Idempotency-Key')).toBeNull();
  });

  it('repeats a keyed send once under the same key when no answer arrived', async () => {
    const fetcher = vi.fn<(input: RequestInfo | URL, init?: RequestInit) => Promise<Response>>()
      .mockRejectedValueOnce(new TypeError('network'))
      .mockResolvedValueOnce(accepted());
    const client = createChatBrowserClient(schema, { fetcher });
    await expect(client.startTurn('s', 'draw a cat', { idempotencyKey: 'send-1' }))
      .resolves.toEqual({ assistantMessageID: 'm-1', sessionID: 's' });
    expect(fetcher).toHaveBeenCalledTimes(2);
    expect(fetcher.mock.calls.map(([, init]) => headerOf(init, 'Idempotency-Key'))).toEqual(['send-1', 'send-1']);
    expect(fetcher.mock.calls[1]?.[1]?.body).toBe(fetcher.mock.calls[0]?.[1]?.body);
  });

  it('never repeats an unkeyed or aborted send', async () => {
    const fetcher = vi.fn(async () => { throw new TypeError('network'); });
    const client = createChatBrowserClient(schema, { fetcher });
    await expect(client.startTurn('s', 'draw a cat', {})).rejects.toThrow('network');
    expect(fetcher).toHaveBeenCalledTimes(1);
    const controller = new AbortController();
    controller.abort();
    await expect(client.startTurn('s', 'draw a cat', { idempotencyKey: 'send-1' }, controller.signal)).rejects.toThrow();
    expect(fetcher).toHaveBeenCalledTimes(2);
  });

  it('shows the Schema detail of a key conflict', async () => {
    const fetcher = vi.fn(async () => new Response(JSON.stringify({
      code: 'idempotency_conflict', detail: 'The idempotency key conflicts with an existing request', request_id: 'req-1',
    }), { status: 409, headers: { 'X-Request-ID': 'req-1' } }));
    const client = createChatBrowserClient(schema, { fetcher });
    await expect(client.startTurn('s', 'draw a dog', { idempotencyKey: 'send-1' }))
      .rejects.toMatchObject({ message: 'The idempotency key conflicts with an existing request', status: 409 });
  });
});

describe('send keys', () => {
  it('reuses the uncertain send key only for the same request', () => {
    const signature = sendSignature('s', 'draw a cat', { model: 'm' });
    const uncertain = { signature, key: 'send-1', at: Date.now() };
    expect(sendKeyFor(uncertain, sendSignature('s', 'draw a cat', { model: 'm' }))).toBe('send-1');
    expect(sendKeyFor(uncertain, sendSignature('s', 'draw a cat', { model: 'other' }))).not.toBe('send-1');
    expect(sendKeyFor(uncertain, sendSignature('other', 'draw a cat', { model: 'm' }))).not.toBe('send-1');
    expect(sendKeyFor(null, signature, 'queued-key')).toBe('queued-key');
    // The uncertain send wins over a queued message's own key: it is the one the server may have.
    expect(sendKeyFor(uncertain, signature, 'queued-key')).toBe('send-1');
  });

  it('makes a new key for every new send', () => {
    const keys = new Set(Array.from({ length: 50 }, () => newSendKey()));
    expect(keys.size).toBe(50);
    expect(sendKeyFor(null, 'x')).not.toBe(sendKeyFor(null, 'x'));
  });

  it('keeps a queued message key through the stored queue', () => {
    const store = new Map<string, string>();
    const storage = {
      getItem: (key: string) => store.get(key) ?? null,
      setItem: (key: string, value: string) => { store.set(key, value); },
      removeItem: (key: string) => { store.delete(key); },
    };
    writeQueue(storage, enqueueTurn({}, 's', {
      id: 'queued-1', idempotencyKey: 'send-q', text: 'later', images: [], claims: [], selection: [],
    }));
    // A reload reads the queue back with the same key.
    const { turn } = takeQueuedTurn(parseQueue(storage.getItem(SEND_QUEUE_KEY)), 's');
    expect(turn?.idempotencyKey).toBe('send-q');
  });
});

function memoryStorage() {
  const store = new Map<string, string>();
  return {
    store,
    getItem: (key: string) => store.get(key) ?? null,
    setItem: (key: string, value: string) => { store.set(key, value); },
    removeItem: (key: string) => { store.delete(key); },
  };
}

function conflict(code = IDEMPOTENCY_CONFLICT_CODE) {
  const error = new ChatBrowserError('conflict', 409, 'req-1');
  error.code = code;
  return error;
}

describe('the request a key belongs to', () => {
  it('includes the locale and a queued message id', () => {
    const base = sendSignature('s', 'draw a cat', { model: 'm' }, { locale: 'zh-CN' });
    expect(sendSignature('s', 'draw a cat', { model: 'm' }, { locale: 'en' })).not.toBe(base);
    const queuedA = sendSignature('s', 'draw a cat', { model: 'm' }, { locale: 'zh-CN', queuedId: 'q-a' });
    const queuedB = sendSignature('s', 'draw a cat', { model: 'm' }, { locale: 'zh-CN', queuedId: 'q-b' });
    expect(queuedA).not.toBe(queuedB);
    expect(queuedA).not.toBe(base);
    // Queued item B with A's words never takes A's key; the same item, or A's words typed again from the box, does.
    const uncertain = { signature: queuedA, typedSignature: base, key: 'key-a', at: Date.now() };
    expect(sendKeyFor(uncertain, queuedB, 'key-b')).toBe('key-b');
    expect(sendKeyFor(uncertain, queuedA, 'key-a2')).toBe('key-a');
    expect(sendKeyFor(uncertain, base)).toBe('key-a');
  });

  it('is not reused once too old', () => {
    const now = 10 * UNCERTAIN_SEND_MAX_AGE_MS;
    const uncertain = { signature: 'x', key: 'old', at: now - UNCERTAIN_SEND_MAX_AGE_MS - 1 };
    expect(sendKeyFor(uncertain, 'x', undefined, now)).not.toBe('old');
    expect(sendKeyFor({ ...uncertain, at: now - 1000 }, 'x', undefined, now)).toBe('old');
  });
});

describe('uncertain sends per session', () => {
  it('parse only well-formed, live records, and keep at most the newest bound', () => {
    const now = 1_000_000_000;
    const raw = JSON.stringify({
      a: { signature: 'x', key: 'k', at: now - 1 },
      old: { signature: 'x', key: 'k', at: now - UNCERTAIN_SEND_MAX_AGE_MS - 1 },
      future: { signature: 'x', key: 'k', at: now + 10 * 60 * 1000 },
      bad: { signature: 'x', at: now },
      empty: { signature: 'x', key: '', at: now },
    });
    expect(Object.keys(parseUncertainSends(raw, now))).toEqual(['a']);
    expect(parseUncertainSends('not json', now)).toEqual({});
    expect(parseUncertainSends('[1]', now)).toEqual({});
    let sends = {};
    for (let index = 0; index <= MAX_UNCERTAIN_SENDS; index += 1) {
      sends = rememberUncertainSend(sends, `s${index}`, { signature: 'x', key: `k${index}`, at: now + index });
    }
    expect(Object.keys(sends)).toHaveLength(MAX_UNCERTAIN_SENDS);
    expect(sends).not.toHaveProperty('s0');
    expect(forgetUncertainSend(sends, 's1', 'other')).toBe(sends);
    expect(forgetUncertainSend(sends, 's1', 'k1')).not.toHaveProperty('s1');
  });

  it('keep a lost send through a send in another session and a reload, until that session is answered', async () => {
    const storage = memoryStorage();
    const store = createUncertainSendStore(() => storage);
    const keys: string[] = [];
    const lost = async (key: string) => { keys.push(key); throw new TypeError('network'); };
    const answered = async (key: string) => { keys.push(key); return 'ok'; };
    const original = { sessionId: 's1', signature: sendSignature('s1', 'draw a cat', {}, { locale: 'en' }) };
    await expect(sendUnderKey(store, original, lost, idempotencyRefused)).rejects.toThrow('network');
    // Another session's send does not replace session s1's lost one.
    await sendUnderKey(store, { sessionId: 's2', signature: sendSignature('s2', 'other', {}, { locale: 'en' }) }, answered, idempotencyRefused);
    // A reload: a new store reading the same sessionStorage.
    const reloaded = createUncertainSendStore(() => storage);
    await expect(sendUnderKey(reloaded, original, answered, idempotencyRefused)).resolves.toBe('ok');
    expect(keys[2]).toBe(keys[0]);
    expect(keys[1]).not.toBe(keys[0]);
    // Answered: the slot is gone, and with every session answered so is the stored entry.
    expect(reloaded.get('s1')).toBeNull();
    expect(storage.store.has(UNCERTAIN_SENDS_KEY)).toBe(false);
  });

  it('live for the page when storage cannot be used', async () => {
    const store = createUncertainSendStore(() => { throw new Error('SecurityError'); });
    const keys: string[] = [];
    const request = { sessionId: 's', signature: 'sig' };
    await expect(sendUnderKey(store, request, async (key) => { keys.push(key); throw new TypeError('network'); }, idempotencyRefused)).rejects.toThrow();
    await sendUnderKey(store, request, async (key) => { keys.push(key); }, idempotencyRefused);
    expect(keys[1]).toBe(keys[0]);
  });

  it('drop a key the server refuses and send once more under a fresh one', async () => {
    const storage = memoryStorage();
    const store = createUncertainSendStore(() => storage);
    store.remember('s', { signature: 'sig', key: 'stale', at: Date.now() });
    const keys: string[] = [];
    const start = vi.fn(async (key: string) => {
      keys.push(key);
      if (key === 'stale') throw conflict();
      return 'ok';
    });
    await expect(sendUnderKey(store, { sessionId: 's', signature: 'sig' }, start, idempotencyRefused)).resolves.toBe('ok');
    expect(keys).toHaveLength(2);
    expect(keys[0]).toBe('stale');
    expect(keys[1]).not.toBe('stale');
    expect(store.get('s')).toBeNull();
  });

  it('give up after a second refusal, with the key gone, and never repeat another failure', async () => {
    const storage = memoryStorage();
    const store = createUncertainSendStore(() => storage);
    const start = vi.fn(async () => { throw conflict('idempotency_key_invalid'); });
    await expect(sendUnderKey(store, { sessionId: 's', signature: 'sig' }, start, idempotencyRefused)).rejects.toMatchObject({ code: 'idempotency_key_invalid' });
    expect(start).toHaveBeenCalledTimes(2);
    expect(store.get('s')).toBeNull();
    const refused = vi.fn(async () => { throw new ChatBrowserError('bad', 400); });
    await expect(sendUnderKey(store, { sessionId: 's', signature: 'sig' }, refused, idempotencyRefused)).rejects.toThrow('bad');
    expect(refused).toHaveBeenCalledTimes(1);
    // A failure that is not a key refusal keeps the send uncertain.
    expect(store.get('s')?.signature).toBe('sig');
  });
});

describe('send refusals the page tells apart', () => {
  it('carries the code of a key refusal, under either name of the malformed-key code', async () => {
    for (const code of ['idempotency_conflict', 'idempotency_key_required', 'idempotency_key_invalid']) {
      const fetcher = vi.fn(async () => new Response(JSON.stringify({ code, detail: 'refused' }), { status: code === 'idempotency_conflict' ? 409 : 400 }));
      const client = createChatBrowserClient(schema, { fetcher });
      const error = await client.startTurn('s', 'x', { idempotencyKey: 'k' }).catch((caught: unknown) => caught);
      expect(error).toMatchObject({ code, message: 'refused' });
      expect(idempotencyRefused(error)).toBe(true);
      expect(turnActiveRefusal(error)).toBe(false);
    }
  });

  it('reads a codeless 409 as a turn that already runs', async () => {
    const fetcher = vi.fn(async () => Response.json(
      { [schema.chat.api.response_fields.error]: schema.chat.presentation.messages.turn_active }, { status: 409 },
    ));
    const client = createChatBrowserClient(schema, { fetcher });
    const error = await client.startTurn('s', 'x', { idempotencyKey: 'k' }).catch((caught: unknown) => caught);
    expect(turnActiveRefusal(error)).toBe(true);
    expect(idempotencyRefused(error)).toBe(false);
    expect(turnActiveRefusal(new ChatBrowserError('x', 500))).toBe(false);
  });
});
