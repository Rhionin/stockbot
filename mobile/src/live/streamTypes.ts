import type { SseMessage } from '@stockbot/shared/sseParser';

export interface StreamRequest {
  url: string;
  headers: Record<string, string>;
}

export interface StreamClosed {
  // The HTTP status when the server answered and refused, otherwise null.
  status: number | null;
  retryAfterMs?: number;
  // The transport ended a healthy stream on purpose to release memory.
  recycled?: boolean;
}

export interface StreamHandlers {
  onOpen: () => void;
  onMessage: (message: SseMessage) => void;
  // Bytes arrived, including keepalive comments. Only transports that can see
  // them call it, and the idle watchdog arms after the first call.
  onActivity: () => void;
  onClosed: (info: StreamClosed) => void;
}

export type OpenStream = (request: StreamRequest, handlers: StreamHandlers) => () => void;
