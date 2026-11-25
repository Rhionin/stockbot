import type { OpenStream } from './streamTypes';

export const LIVE_EVENT_NAMES = [
  'scan',
  'scan_processing',
  'scan_processing_failed',
  'scanner_mode',
  'inventory',
  'hint',
] as const;

// The browser sends the session cookie and cannot add headers. It hides the
// status of a refused stream and any keepalive comments, so the connection
// treats every close as a network failure and a later request finds a 401.
export const openEventSourceStream: OpenStream = (request, handlers) => {
  const source = new EventSource(request.url);
  let finished = false;
  source.onopen = () => {
    if (!finished) handlers.onOpen();
  };
  for (const name of LIVE_EVENT_NAMES) {
    source.addEventListener(name, (message) => {
      if (finished) return;
      const frame = message as MessageEvent<string>;
      handlers.onMessage({ event: name, data: frame.data, id: frame.lastEventId });
    });
  }
  source.onerror = () => {
    if (finished) return;
    finished = true;
    source.close();
    handlers.onClosed({ status: null });
  };
  return () => {
    finished = true;
    source.close();
  };
};
