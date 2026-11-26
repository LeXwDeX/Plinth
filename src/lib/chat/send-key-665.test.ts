import { describe, expect, it, vi } from 'vitest';
import catalog from '../../../internal/catalog/default_catalog.json';
import type { ProductSchema } from '@/lib/product-schema';
import { createChatBrowserClient } from './browser-client';
import { newSendKey, sendKeyFor, sendSignature } from './send-key';
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
    const uncertain = { signature, key: 'send-1' };
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
