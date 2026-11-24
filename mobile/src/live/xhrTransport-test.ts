import { afterEach, beforeEach, describe, expect, it, jest } from '@jest/globals';
import type { SseMessage } from '@stockbot/shared/sseParser';
import type { StreamClosed } from './streamTypes';
import { RECYCLE_AFTER_CHARS, openXhrStream } from './xhrTransport';

class FakeXhr {
  static instances: FakeXhr[] = [];
  method = '';
  url = '';
  readonly headers: Record<string, string> = {};
  readyState = 0;
  status = 0;
  responseText = '';
  aborted = false;
  sent = false;
  responseHeaders: Record<string, string> = {};
  onreadystatechange: (() => void) | null = null;
  onprogress: (() => void) | null = null;
  onload: (() => void) | null = null;
  onerror: (() => void) | null = null;
  ontimeout: (() => void) | null = null;

  constructor() {
    FakeXhr.instances.push(this);
  }
  open(method: string, url: string) {
    this.method = method;
    this.url = url;
  }
  setRequestHeader(name: string, value: string) {
    this.headers[name] = value;
  }
  getResponseHeader(name: string) {
    return this.responseHeaders[name] ?? null;
  }
  send() {
    this.sent = true;
  }
  abort() {
    this.aborted = true;
  }
  headersReceived(status: number) {
    this.status = status;
    this.readyState = 2;
    this.onreadystatechange?.();
  }
  receive(text: string) {
    this.readyState = 3;
    this.responseText += text;
    this.onprogress?.();
  }
}

const original = globalThis.XMLHttpRequest;

beforeEach(() => {
  FakeXhr.instances = [];
  globalThis.XMLHttpRequest = FakeXhr as unknown as typeof XMLHttpRequest;
});

afterEach(() => {
  globalThis.XMLHttpRequest = original;
});

function open(headers: Record<string, string> = {}) {
  const messages: SseMessage[] = [];
  const closed: StreamClosed[] = [];
  const onOpen = jest.fn();
  const onActivity = jest.fn();
  const close = openXhrStream(
    { url: 'https://stockbot.example/api/events', headers },
    {
      onOpen,
      onActivity,
      onMessage: (message) => messages.push(message),
      onClosed: (info) => closed.push(info),
    },
  );
  return { xhr: FakeXhr.instances[0]!, messages, closed, onOpen, onActivity, close };
}

describe('openXhrStream', () => {
  it('sends a GET with the headers it was given', () => {
    const { xhr } = open({ Authorization: 'Bearer sbs_test', 'X-Stockbot-Client': 'expo/1' });
    expect(xhr.method).toBe('GET');
    expect(xhr.url).toBe('https://stockbot.example/api/events');
    expect(xhr.headers).toEqual({ Authorization: 'Bearer sbs_test', 'X-Stockbot-Client': 'expo/1' });
    expect(xhr.sent).toBe(true);
  });

  it('reports open on a 200 and reads only the new text of each progress event', () => {
    const { xhr, messages, onOpen, onActivity } = open();
    xhr.headersReceived(200);
    expect(onOpen).toHaveBeenCalledTimes(1);

    xhr.receive(': ok\n\nid: 1\nevent: scan\ndata: {"id":"a"}\n\n');
    xhr.receive('id: 2\nevent: scanner_mode\ndata: "stock_out"\n\n');

    expect(messages).toEqual([
      { id: '1', event: 'scan', data: '{"id":"a"}' },
      { id: '2', event: 'scanner_mode', data: '"stock_out"' },
    ]);
    expect(onOpen).toHaveBeenCalledTimes(1);
    expect(onActivity).toHaveBeenCalledTimes(2);
  });

  it('joins a frame split in the middle across progress events', () => {
    const { xhr, messages } = open();
    xhr.headersReceived(200);
    xhr.receive('id: 5\nevent: sc');
    xhr.receive('an\ndata: {"id":"a",');
    expect(messages).toEqual([]);
    xhr.receive('"status":"pending"}\n');
    xhr.receive('\n');
    expect(messages).toEqual([{ id: '5', event: 'scan', data: '{"id":"a","status":"pending"}' }]);
  });

  it('opens on the first progress event when no header event arrived', () => {
    const { xhr, onOpen } = open();
    xhr.status = 200;
    xhr.receive(': ok\n\n');
    expect(onOpen).toHaveBeenCalledTimes(1);
  });

  it('counts a keepalive as activity without producing a message', () => {
    const { xhr, messages, onActivity } = open();
    xhr.headersReceived(200);
    xhr.receive(': ping\n\n');
    expect(messages).toEqual([]);
    expect(onActivity).toHaveBeenCalledTimes(1);
  });

  it.each([401, 426, 503])('closes with the status when the server answers %i', (status) => {
    const { xhr, closed, onOpen } = open();
    xhr.headersReceived(status);
    expect(closed).toEqual([{ status, retryAfterMs: undefined }]);
    expect(onOpen).not.toHaveBeenCalled();
    expect(xhr.aborted).toBe(true);
  });

  it('passes Retry-After on as milliseconds', () => {
    const { xhr, closed } = open();
    xhr.responseHeaders['Retry-After'] = '3';
    xhr.headersReceived(429);
    expect(closed).toEqual([{ status: 429, retryAfterMs: 3000 }]);
  });

  it('closes once with no status on a network error', () => {
    const { xhr, closed } = open();
    xhr.headersReceived(200);
    xhr.onerror?.();
    xhr.onload?.();
    expect(closed).toEqual([{ status: null }]);
  });

  it('closes with no status when the server ends a healthy stream, after reading the tail', () => {
    const { xhr, messages, closed } = open();
    xhr.headersReceived(200);
    xhr.receive('event: hint\ndata: {"type":"scans"}\n\n');
    xhr.status = 200;
    xhr.onload?.();
    expect(messages).toHaveLength(1);
    expect(closed).toEqual([{ status: null }]);
  });

  it('closes with no status on a timeout', () => {
    const { xhr, closed } = open();
    xhr.ontimeout?.();
    expect(closed).toEqual([{ status: null }]);
  });

  it('ends the stream on purpose before the response grows too large', () => {
    const { xhr, closed, messages } = open();
    xhr.headersReceived(200);
    xhr.receive(`data: ${'x'.repeat(RECYCLE_AFTER_CHARS)}\n\n`);
    expect(messages).toHaveLength(1);
    expect(closed).toEqual([{ status: null, recycled: true }]);
    expect(xhr.aborted).toBe(true);
  });

  it('aborts when closed by its owner and then stays silent', () => {
    const { xhr, closed, messages, close } = open();
    xhr.headersReceived(200);
    close();
    expect(xhr.aborted).toBe(true);
    xhr.receive('data: late\n\n');
    xhr.onerror?.();
    expect(messages).toEqual([]);
    expect(closed).toEqual([]);
  });
});
