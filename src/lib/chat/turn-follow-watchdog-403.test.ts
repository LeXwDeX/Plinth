import { afterEach, describe, expect, it, vi } from 'vitest';
import defaultCatalog from '../../../internal/catalog/default_catalog.json';
import type { ProductSchema } from '@/lib/product-schema';
import { createChatBrowserClient, type ChatSemanticEvent } from '@/lib/chat/browser-client';
import { followTurnStream } from '@/lib/chat/turn-follow';

// #403 with #375: `subscribe()` re-opens a silent stream by itself, a bounded number of times in a row, and reports
// an error after that. The follow of the turn is the second layer: it asks the server whether the turn still runs
// and attaches again, a bounded number of times in all. Together they are finite, and the second layer replays the
// turn from its first event into a buffer it has rewound.

class FakeEventSource {
  onmessage: ((event: MessageEvent<string>) => void) | null = null;
  onerror: (() => void) | null = null;
  closed = false;
  constructor(readonly url: string) {}
  close(): void { this.closed = true; }
  emit(payload: unknown, lastEventId?: string): void {
    this.onmessage?.({ data: JSON.stringify(payload), ...(lastEventId === undefined ? {} : { lastEventId }) } as MessageEvent<string>);
  }
}

function rig(attempts: number, running: () => boolean) {
  vi.useFakeTimers();
  const schema = structuredClone(defaultCatalog.application) as ProductSchema;
  schema.chat.frontend.connection_timeout_milliseconds = 50;
  schema.chat.frontend.stream_silence_milliseconds = 100;
  schema.chat.frontend.stream_reconnect_attempts = 2;
  const sources: FakeEventSource[] = [];
  const client = createChatBrowserClient(schema, {
    eventSourceFactory: (url) => {
      const source = new FakeEventSource(url);
      sources.push(source);
      return source;
    },
  });
  const events: ChatSemanticEvent[] = [];
  const rewinds = vi.fn(() => { events.length = 0; });
  const reads = vi.fn(async () => running());
  const controller = new AbortController();
  const follow = followTurnStream({
    connect: (callbacks) => client.subscribe('session-a', {
      fromID: 0,
      onEvent: (event) => events.push(event),
      onDone: callbacks.onDone,
      onError: callbacks.onError,
    }),
    stillRunning: reads,
    rewind: rewinds,
    attempts,
    signal: controller.signal,
  });
  const fields = schema.chat.stream.event_fields;
  const types = schema.chat.stream.event_types;
  const fromId = schema.chat.api.fields.from_id;
  return {
    sources, events, rewinds, reads, follow, controller,
    text: (content: string) => ({ [fields.type]: types.text, [fields.content]: content }),
    done: { [fields.type]: types.done },
    heartbeat: { [fields.type]: types.heartbeat },
    cursor: (source: FakeEventSource) => new URL(source.url, 'http://localhost').searchParams.get(fromId),
  };
}

describe('following a turn through the stream watchdog', () => {
  afterEach(() => vi.useRealTimers());

  it('replays the running turn from its first event after a connection error, into a rewound buffer', async () => {
    const f = rig(2, () => true);
    f.sources[0].emit(f.text('first round'), '4');
    expect(f.events).toHaveLength(1);
    // A hard connection error: the subscription reports it at once.
    f.sources[0].onerror?.();
    await vi.advanceTimersByTimeAsync(0);
    expect(f.reads).toHaveBeenCalledTimes(1);
    expect(f.rewinds).toHaveBeenCalledTimes(1);
    expect(f.events).toEqual([]);
    // The new stream starts at the turn's first event, not after the last one the old stream delivered.
    expect(f.sources).toHaveLength(2);
    expect(f.cursor(f.sources[1])).toBe('0');
    f.sources[1].emit(f.text('first round'), '4');
    f.sources[1].emit(f.done, '5');
    await expect(f.follow).resolves.toBe('done');
    // Delivered once: the rewound buffer holds the replay and nothing of the broken stream.
    expect(f.events.map((event) => event.type)).toEqual(['text', 'done']);
  });

  it('is finite when every stream breaks at once: the first one plus the allowed attachments, no more', async () => {
    const f = rig(2, () => true);
    for (let round = 0; round < 3; round += 1) {
      f.sources[round].onerror?.();
      await vi.advanceTimersByTimeAsync(0);
    }
    await expect(f.follow).resolves.toBe('interrupted');
    expect(f.sources).toHaveLength(3);
    await vi.advanceTimersByTimeAsync(60_000);
    expect(f.sources).toHaveLength(3);
  });

  it('is finite when every stream goes silent: each subscription re-opens itself within its own bound, then the follow its own', async () => {
    // 2 silent reconnects per subscription (stream_reconnect_attempts = 2) and 1 attachment after the first one.
    const f = rig(1, () => true);
    // A first frame arms the silence window; without one the first-frame timeout ends the subscription at once.
    f.sources[0].emit(f.heartbeat, '');
    await vi.advanceTimersByTimeAsync(300);
    // The first subscription used its bound (the source and two silent reconnects) and the follow attached again.
    expect(f.sources).toHaveLength(1 + 2 + 1);
    expect(f.reads).toHaveBeenCalledTimes(1);
    expect(f.rewinds).toHaveBeenCalledTimes(1);
    f.sources[3].emit(f.heartbeat, '');
    await vi.advanceTimersByTimeAsync(300);
    await expect(f.follow).resolves.toBe('interrupted');
    expect(f.sources).toHaveLength((1 + 2) * (1 + 1));
    // The follow's own bound is spent: nothing is read or opened any more.
    expect(f.reads).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(60_000);
    expect(f.sources).toHaveLength((1 + 2) * (1 + 1));
  });

  it('gives up at once when the server says the turn is over, however the stream ended', async () => {
    const f = rig(3, () => false);
    f.sources[0].onerror?.();
    await expect(f.follow).resolves.toBe('interrupted');
    expect(f.sources).toHaveLength(1);
    expect(f.rewinds).not.toHaveBeenCalled();
  });
});
