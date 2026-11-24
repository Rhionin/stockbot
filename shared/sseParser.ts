export interface SseMessage {
  event: string;
  data: string;
  id: string;
}

export interface SseParser {
  push: (chunk: string) => SseMessage[];
  readonly lastEventId: string;
  readonly retryMs: number | null;
}

const DEFAULT_EVENT = 'message';

// Incremental parser for the text/event-stream format. A chunk may end
// anywhere, including between the CR and LF of one line break, and feeding the
// same text in any split yields the same messages.
export function createSseParser(): SseParser {
  let pending = '';
  let started = false;
  let eventType = '';
  let dataLines: string[] = [];
  let lastEventId = '';
  let retryMs: number | null = null;

  const processLine = (line: string, out: SseMessage[]) => {
    if (line === '') {
      if (dataLines.length > 0) {
        out.push({ event: eventType === '' ? DEFAULT_EVENT : eventType, data: dataLines.join('\n'), id: lastEventId });
      }
      eventType = '';
      dataLines = [];
      return;
    }
    if (line.startsWith(':')) return;
    const colon = line.indexOf(':');
    const field = colon === -1 ? line : line.slice(0, colon);
    let value = colon === -1 ? '' : line.slice(colon + 1);
    if (value.startsWith(' ')) value = value.slice(1);
    switch (field) {
      case 'event':
        eventType = value;
        break;
      case 'data':
        dataLines.push(value);
        break;
      case 'id':
        if (!value.includes('\0')) lastEventId = value;
        break;
      case 'retry':
        if (/^[0-9]+$/.test(value)) retryMs = Number(value);
        break;
      default:
        break;
    }
  };

  return {
    push(chunk) {
      pending += chunk;
      if (!started && pending !== '') {
        started = true;
        if (pending.startsWith('﻿')) pending = pending.slice(1);
      }
      const out: SseMessage[] = [];
      let start = 0;
      let i = 0;
      while (i < pending.length) {
        const ch = pending[i];
        if (ch === '\n') {
          processLine(pending.slice(start, i), out);
          i += 1;
          start = i;
        } else if (ch === '\r') {
          if (i + 1 === pending.length) break;
          processLine(pending.slice(start, i), out);
          i += pending[i + 1] === '\n' ? 2 : 1;
          start = i;
        } else {
          i += 1;
        }
      }
      pending = pending.slice(start);
      return out;
    },
    get lastEventId() {
      return lastEventId;
    },
    get retryMs() {
      return retryMs;
    },
  };
}
