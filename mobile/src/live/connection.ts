import type { SseMessage } from '@stockbot/shared/sseParser';
import type { ForegroundSource } from './foregroundSource';
import type { OpenStream, StreamClosed, StreamRequest } from './streamTypes';

export type LiveEvent =
  | { kind: 'message'; event: string; data: string }
  | { kind: 'refresh'; reason: 'poll' | 'reconnect' };

export type LiveStatus = 'idle' | 'connecting' | 'live' | 'down' | 'paused' | 'closed';

export interface LiveConnectionOptions {
  request: StreamRequest;
  openStream: OpenStream;
  foreground: ForegroundSource;
  // 401 and 426 end the connection for good. The owner signs out or upgrades.
  rejected: (status: number) => void;
  // Asks the API something once per outage when the stream could not say why
  // it failed, so a dead session surfaces as a 401 through the normal client.
  probe?: () => void;
  pollMs?: number;
  backoffBaseMs?: number;
  backoffMaxMs?: number;
  idleMs?: number;
  random?: () => number;
}

export interface LiveConnection {
  start: () => void;
  dispose: () => void;
  subscribe: (listener: (event: LiveEvent) => void) => () => void;
  getStatus: () => LiveStatus;
}

// The shared queue fallback is 2s. See LiveFallbackRules.scanQueuePollMs.
export const POLL_MS = 2000;
export const BACKOFF_BASE_MS = 1000;
export const BACKOFF_MAX_MS = 30_000;
// The server writes a keepalive every 25s.
export const IDLE_MS = 70_000;

export function createLiveConnection(options: LiveConnectionOptions): LiveConnection {
  const pollMs = options.pollMs ?? POLL_MS;
  const baseMs = options.backoffBaseMs ?? BACKOFF_BASE_MS;
  const maxMs = options.backoffMaxMs ?? BACKOFF_MAX_MS;
  const idleMs = options.idleMs ?? IDLE_MS;
  const random = options.random ?? Math.random;

  const listeners = new Set<(event: LiveEvent) => void>();
  let status: LiveStatus = 'idle';
  let active = options.foreground.isActive();
  let started = false;
  let stopped = false;
  let generation = 0;
  let closeStream: (() => void) | null = null;
  let reconnectTimer: ReturnType<typeof setTimeout> | null = null;
  let pollTimer: ReturnType<typeof setInterval> | null = null;
  let idleTimer: ReturnType<typeof setTimeout> | null = null;
  let watchesActivity = false;
  let attempt = 0;
  // Set when events may have been missed: after an outage, a pause or a recycle.
  let needsResync = false;
  let probed = false;
  let unsubscribeForeground: (() => void) | null = null;

  const emit = (event: LiveEvent) => {
    for (const listener of [...listeners]) listener(event);
  };

  const clearIdle = () => {
    if (idleTimer !== null) clearTimeout(idleTimer);
    idleTimer = null;
  };
  const clearReconnect = () => {
    if (reconnectTimer !== null) clearTimeout(reconnectTimer);
    reconnectTimer = null;
  };
  const stopPoll = () => {
    if (pollTimer !== null) clearInterval(pollTimer);
    pollTimer = null;
  };
  const closeCurrentStream = () => {
    generation += 1;
    clearIdle();
    watchesActivity = false;
    const close = closeStream;
    closeStream = null;
    close?.();
  };

  const startPoll = () => {
    if (pollTimer !== null || !active || stopped) return;
    pollTimer = setInterval(() => emit({ kind: 'refresh', reason: 'poll' }), pollMs);
  };

  const armIdle = () => {
    clearIdle();
    if (!watchesActivity) return;
    idleTimer = setTimeout(() => {
      idleTimer = null;
      goDown({ status: null });
    }, idleMs);
  };

  function connect() {
    if (stopped || !active || closeStream !== null) return;
    status = 'connecting';
    const mine = (generation += 1);
    closeStream = options.openStream(options.request, {
      onOpen() {
        if (generation !== mine) return;
        status = 'live';
        attempt = 0;
        probed = false;
        stopPoll();
        clearReconnect();
        if (needsResync) emit({ kind: 'refresh', reason: 'reconnect' });
        needsResync = false;
      },
      onActivity() {
        if (generation !== mine) return;
        watchesActivity = true;
        armIdle();
      },
      onMessage(message: SseMessage) {
        if (generation !== mine) return;
        emit({ kind: 'message', event: message.event, data: message.data });
      },
      onClosed(info) {
        if (generation !== mine) return;
        closeStream = null;
        generation += 1;
        clearIdle();
        watchesActivity = false;
        if (info.recycled) {
          needsResync = true;
          connect();
          return;
        }
        goDown(info);
      },
    });
  }

  function goDown(info: StreamClosed) {
    closeCurrentStream();
    if (stopped) return;
    if (info.status === 401 || info.status === 426) {
      shutDown();
      options.rejected(info.status);
      return;
    }
    needsResync = true;
    if (!active) return;
    status = 'down';
    startPoll();
    if (info.status === null && !probed) {
      probed = true;
      options.probe?.();
    }
    clearReconnect();
    const backoff = Math.min(maxMs, baseMs * 2 ** attempt);
    attempt += 1;
    const delay = Math.max(backoff * (1 + random() * 0.25), info.retryAfterMs ?? 0);
    reconnectTimer = setTimeout(() => {
      reconnectTimer = null;
      connect();
    }, delay);
  }

  function pause() {
    needsResync = true;
    closeCurrentStream();
    clearReconnect();
    stopPoll();
    if (!stopped) status = 'paused';
  }

  function shutDown() {
    stopped = true;
    closeCurrentStream();
    clearReconnect();
    stopPoll();
    unsubscribeForeground?.();
    unsubscribeForeground = null;
    status = 'closed';
  }

  return {
    start() {
      if (started || stopped) return;
      started = true;
      unsubscribeForeground = options.foreground.subscribe((next) => {
        if (stopped || next === active) return;
        active = next;
        if (!next) {
          pause();
          return;
        }
        attempt = 0;
        connect();
      });
      active = options.foreground.isActive();
      if (active) connect();
      else status = 'paused';
    },
    dispose() {
      shutDown();
      listeners.clear();
    },
    subscribe(listener) {
      listeners.add(listener);
      return () => {
        listeners.delete(listener);
      };
    },
    getStatus: () => status,
  };
}
