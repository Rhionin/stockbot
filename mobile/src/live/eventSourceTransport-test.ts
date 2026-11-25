import { afterEach, beforeEach, describe, expect, it, jest } from '@jest/globals';
import type { SseMessage } from '@stockbot/shared/sseParser';
import type { StreamClosed } from './streamTypes';
import { LIVE_EVENT_NAMES, openEventSourceStream } from './eventSourceTransport';

class FakeEventSource {
  static instances: FakeEventSource[] = [];
  readonly url: string;
  closed = false;
  onopen: (() => void) | null = null;
  onerror: (() => void) | null = null;
  readonly listeners = new Map<string, (message: { data: string; lastEventId: string }) => void>();

  constructor(url: string) {
    this.url = url;
    FakeEventSource.instances.push(this);
  }
  addEventListener(name: string, listener: (message: { data: string; lastEventId: string }) => void) {
    this.listeners.set(name, listener);
  }
  close() {
    this.closed = true;
  }
}

const original = globalThis.EventSource;

beforeEach(() => {
  FakeEventSource.instances = [];
  globalThis.EventSource = FakeEventSource as unknown as typeof EventSource;
});

afterEach(() => {
  globalThis.EventSource = original;
});

function open() {
  const messages: SseMessage[] = [];
  const closed: StreamClosed[] = [];
  const onOpen = jest.fn();
  const close = openEventSourceStream(
    { url: '/api/events', headers: {} },
    { onOpen, onActivity: jest.fn(), onMessage: (message) => messages.push(message), onClosed: (info) => closed.push(info) },
  );
  return { source: FakeEventSource.instances[0]!, messages, closed, onOpen, close };
}

describe('openEventSourceStream', () => {
  it('opens the cookie-authenticated url and reports open', () => {
    const { source, onOpen } = open();
    expect(source.url).toBe('/api/events');
    source.onopen?.();
    expect(onOpen).toHaveBeenCalledTimes(1);
  });

  it('listens for every live event name', () => {
    const { source, messages } = open();
    expect([...source.listeners.keys()].sort()).toEqual([...LIVE_EVENT_NAMES].sort());
    source.listeners.get('scan')?.({ data: '{"id":"a"}', lastEventId: '9' });
    expect(messages).toEqual([{ event: 'scan', data: '{"id":"a"}', id: '9' }]);
  });

  it('closes the source itself on an error so the connection owns the retry', () => {
    const { source, closed } = open();
    source.onerror?.();
    source.onerror?.();
    expect(source.closed).toBe(true);
    expect(closed).toEqual([{ status: null }]);
  });

  it('closes the source when its owner closes it and then stays silent', () => {
    const { source, closed, messages, close } = open();
    close();
    source.listeners.get('scan')?.({ data: '{}', lastEventId: '' });
    source.onerror?.();
    expect(source.closed).toBe(true);
    expect(messages).toEqual([]);
    expect(closed).toEqual([]);
  });
});
