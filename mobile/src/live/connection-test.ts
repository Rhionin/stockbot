import { afterEach, beforeEach, describe, expect, it, jest } from '@jest/globals';
import { createLiveConnection, type LiveConnectionOptions, type LiveEvent } from './connection';
import type { ForegroundSource } from './foregroundSource';
import type { OpenStream, StreamHandlers } from './streamTypes';

interface OpenedStream {
  handlers: StreamHandlers;
  close: jest.Mock;
}

function harness(overrides: Partial<LiveConnectionOptions> = {}) {
  const opened: OpenedStream[] = [];
  const openStream: OpenStream = (_request, handlers) => {
    const close = jest.fn();
    opened.push({ handlers, close });
    return close;
  };
  let active = true;
  const listeners = new Set<(active: boolean) => void>();
  const foreground: ForegroundSource = {
    isActive: () => active,
    subscribe(listener) {
      listeners.add(listener);
      return () => listeners.delete(listener);
    },
  };
  const rejected = jest.fn();
  const probe = jest.fn();
  const connection = createLiveConnection({
    request: { url: 'https://stockbot.example/api/events', headers: {} },
    openStream,
    foreground,
    rejected,
    probe,
    random: () => 0,
    ...overrides,
  });
  const events: LiveEvent[] = [];
  connection.subscribe((event) => events.push(event));
  return {
    connection,
    opened,
    events,
    rejected,
    probe,
    last: () => opened[opened.length - 1]!,
    setForeground(next: boolean) {
      active = next;
      for (const listener of [...listeners]) listener(next);
    },
  };
}

beforeEach(() => {
  jest.useFakeTimers();
});

afterEach(() => {
  jest.useRealTimers();
});

describe('createLiveConnection', () => {
  it('opens one stream, goes live and passes messages on', () => {
    const h = harness();
    h.connection.start();
    expect(h.opened).toHaveLength(1);
    expect(h.connection.getStatus()).toBe('connecting');

    h.last().handlers.onOpen();
    h.last().handlers.onMessage({ id: '1', event: 'scan', data: '{"id":"scan-1"}' });

    expect(h.connection.getStatus()).toBe('live');
    expect(h.events).toEqual([{ kind: 'message', event: 'scan', data: '{"id":"scan-1"}' }]);
    expect(jest.getTimerCount()).toBe(0);
  });

  it('does not open a second stream when started twice', () => {
    const h = harness();
    h.connection.start();
    h.connection.start();
    expect(h.opened).toHaveLength(1);
  });

  it('polls and reconnects with backoff after an error, then stops polling once live', () => {
    const h = harness({ pollMs: 2000, backoffBaseMs: 1000 });
    h.connection.start();
    h.last().handlers.onOpen();
    h.last().handlers.onClosed({ status: null });

    expect(h.connection.getStatus()).toBe('down');
    expect(h.probe).toHaveBeenCalledTimes(1);
    jest.advanceTimersByTime(1000);
    expect(h.opened).toHaveLength(2);
    h.last().handlers.onClosed({ status: null });
    jest.advanceTimersByTime(1999);
    expect(h.opened).toHaveLength(2);
    jest.advanceTimersByTime(1);
    expect(h.opened).toHaveLength(3);

    const polls = () => h.events.filter((event) => event.kind === 'refresh' && event.reason === 'poll').length;
    expect(polls()).toBeGreaterThan(0);
    h.last().handlers.onOpen();
    expect(h.events).toContainEqual({ kind: 'refresh', reason: 'reconnect' });

    const seen = polls();
    jest.advanceTimersByTime(20_000);
    expect(polls()).toBe(seen);
    expect(jest.getTimerCount()).toBe(0);
    expect(h.probe).toHaveBeenCalledTimes(1);
  });

  it('caps the backoff', () => {
    const h = harness({ backoffBaseMs: 1000, backoffMaxMs: 4000 });
    h.connection.start();
    const delays: number[] = [];
    for (let i = 0; i < 5; i += 1) {
      const before = h.opened.length;
      h.last().handlers.onClosed({ status: 503 });
      let waited = 0;
      while (h.opened.length === before) {
        jest.advanceTimersByTime(500);
        waited += 500;
      }
      delays.push(waited);
    }
    expect(delays).toEqual([1000, 2000, 4000, 4000, 4000]);
  });

  it('waits at least as long as Retry-After', () => {
    const h = harness({ backoffBaseMs: 1000 });
    h.connection.start();
    h.last().handlers.onClosed({ status: 429, retryAfterMs: 7000 });
    jest.advanceTimersByTime(6999);
    expect(h.opened).toHaveLength(1);
    jest.advanceTimersByTime(1);
    expect(h.opened).toHaveLength(2);
    expect(h.probe).not.toHaveBeenCalled();
  });

  it('stops polling and reconnecting when the app goes to the background and resumes when it returns', () => {
    const h = harness();
    h.connection.start();
    h.last().handlers.onClosed({ status: null });
    expect(jest.getTimerCount()).toBe(2);

    h.setForeground(false);
    expect(h.connection.getStatus()).toBe('paused');
    expect(jest.getTimerCount()).toBe(0);
    const before = h.events.length;
    jest.advanceTimersByTime(60_000);
    expect(h.events).toHaveLength(before);
    expect(h.opened).toHaveLength(1);

    h.setForeground(true);
    expect(h.opened).toHaveLength(2);
    h.last().handlers.onOpen();
    expect(h.events.at(-1)).toEqual({ kind: 'refresh', reason: 'reconnect' });
    expect(jest.getTimerCount()).toBe(0);
  });

  it('closes a live stream when backgrounded', () => {
    const h = harness();
    h.connection.start();
    h.last().handlers.onOpen();
    const stream = h.last();
    h.setForeground(false);
    expect(stream.close).toHaveBeenCalledTimes(1);
    stream.handlers.onMessage({ id: '', event: 'scan', data: '{}' });
    expect(h.events).toEqual([]);
  });

  it('does not connect when it starts in the background', () => {
    const h = harness();
    h.setForeground(false);
    h.connection.start();
    expect(h.opened).toHaveLength(0);
    expect(h.connection.getStatus()).toBe('paused');
    h.setForeground(true);
    expect(h.opened).toHaveLength(1);
  });

  it.each([401, 426])('ends for good on a %i and reports it once', (status) => {
    const h = harness();
    h.connection.start();
    h.last().handlers.onClosed({ status });
    expect(h.rejected).toHaveBeenCalledWith(status);
    expect(h.rejected).toHaveBeenCalledTimes(1);
    expect(h.connection.getStatus()).toBe('closed');
    expect(jest.getTimerCount()).toBe(0);
    jest.advanceTimersByTime(120_000);
    expect(h.opened).toHaveLength(1);
  });

  it('reconnects without a poll when the transport recycles a long stream', () => {
    const h = harness();
    h.connection.start();
    h.last().handlers.onOpen();
    h.last().handlers.onClosed({ status: null, recycled: true });
    expect(h.opened).toHaveLength(2);
    expect(h.connection.getStatus()).toBe('connecting');
    h.last().handlers.onOpen();
    expect(jest.getTimerCount()).toBe(0);
    expect(h.events).toContainEqual({ kind: 'refresh', reason: 'reconnect' });
  });

  it('treats a stream that goes quiet as dead once activity has been reported', () => {
    const h = harness({ idleMs: 70_000 });
    h.connection.start();
    h.last().handlers.onOpen();
    h.last().handlers.onActivity();
    jest.advanceTimersByTime(69_999);
    expect(h.connection.getStatus()).toBe('live');
    h.last().handlers.onActivity();
    jest.advanceTimersByTime(69_999);
    expect(h.connection.getStatus()).toBe('live');
    const first = h.last();
    jest.advanceTimersByTime(1);
    expect(first.close).toHaveBeenCalled();
    expect(h.connection.getStatus()).toBe('down');
  });

  it('never arms the idle watchdog for a transport that cannot see keepalives', () => {
    const h = harness({ idleMs: 70_000 });
    h.connection.start();
    h.last().handlers.onOpen();
    jest.advanceTimersByTime(600_000);
    expect(h.connection.getStatus()).toBe('live');
  });

  it('ignores callbacks from a stream it already replaced', () => {
    const h = harness();
    h.connection.start();
    const old = h.last();
    old.handlers.onClosed({ status: null });
    jest.advanceTimersByTime(1000);
    h.last().handlers.onOpen();
    const events = h.events.length;
    old.handlers.onMessage({ id: '', event: 'scan', data: '{}' });
    old.handlers.onClosed({ status: null });
    expect(h.events).toHaveLength(events);
    expect(h.connection.getStatus()).toBe('live');
  });

  it('clears every timer and the stream on dispose, and stays closed', () => {
    const h = harness();
    h.connection.start();
    h.last().handlers.onClosed({ status: null });
    const stream = h.last();
    expect(jest.getTimerCount()).toBeGreaterThan(0);

    h.connection.dispose();
    expect(jest.getTimerCount()).toBe(0);
    expect(h.connection.getStatus()).toBe('closed');
    h.setForeground(false);
    h.setForeground(true);
    jest.advanceTimersByTime(120_000);
    expect(h.opened).toHaveLength(1);
    stream.handlers.onOpen();
    expect(h.connection.getStatus()).toBe('closed');
  });

  it('treats a stream that never answers as down, polls and schedules a reconnect', () => {
    const h = harness({ connectMs: 15_000, pollMs: 2000, backoffBaseMs: 1000 });
    h.connection.start();
    expect(h.connection.getStatus()).toBe('connecting');

    jest.advanceTimersByTime(14_999);
    expect(h.connection.getStatus()).toBe('connecting');
    expect(h.opened).toHaveLength(1);
    jest.advanceTimersByTime(1);

    expect(h.connection.getStatus()).toBe('down');
    expect(h.opened[0]!.close).toHaveBeenCalledTimes(1);
    expect(h.probe).toHaveBeenCalledTimes(1);
    jest.advanceTimersByTime(2000);
    expect(h.events).toEqual([{ kind: 'refresh', reason: 'poll' }]);
    jest.advanceTimersByTime(1000);
    expect(h.opened).toHaveLength(2);
    expect(h.connection.getStatus()).toBe('connecting');
  });

  it('leaves a stream that opens in time alone', () => {
    const h = harness({ connectMs: 15_000 });
    h.connection.start();
    jest.advanceTimersByTime(14_000);
    h.last().handlers.onOpen();
    expect(jest.getTimerCount()).toBe(0);

    jest.advanceTimersByTime(60_000);

    expect(h.connection.getStatus()).toBe('live');
    expect(h.opened).toHaveLength(1);
    expect(h.opened[0]!.close).not.toHaveBeenCalled();
    expect(h.probe).not.toHaveBeenCalled();
    expect(h.events).toEqual([]);
  });

  it('clears the connect timer when disposed or backgrounded while connecting', () => {
    const disposed = harness();
    disposed.connection.start();
    expect(jest.getTimerCount()).toBe(1);
    disposed.connection.dispose();
    expect(jest.getTimerCount()).toBe(0);

    const paused = harness();
    paused.connection.start();
    paused.setForeground(false);
    expect(jest.getTimerCount()).toBe(0);
    jest.advanceTimersByTime(120_000);
    expect(paused.connection.getStatus()).toBe('paused');
  });

  it('closes a live stream on dispose', () => {
    const h = harness();
    h.connection.start();
    h.last().handlers.onOpen();
    h.connection.dispose();
    expect(h.last().close).toHaveBeenCalledTimes(1);
  });
});
