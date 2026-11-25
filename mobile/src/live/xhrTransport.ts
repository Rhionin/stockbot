import { createSseParser } from '@stockbot/shared/sseParser';
import type { OpenStream } from './streamTypes';

// React Native keeps the whole response in memory for the life of the request.
export const RECYCLE_AFTER_CHARS = 512 * 1024;

function retryAfterMs(header: string | null): number | undefined {
  if (header === null) return undefined;
  const seconds = Number(header);
  return Number.isFinite(seconds) && seconds >= 0 ? seconds * 1000 : undefined;
}

// Streams over XMLHttpRequest, which Expo Go supports with no native module.
export const openXhrStream: OpenStream = (request, handlers) => {
  const xhr = new XMLHttpRequest();
  const parser = createSseParser();
  let offset = 0;
  let opened = false;
  let finished = false;

  const finish = (info: Parameters<typeof handlers.onClosed>[0]) => {
    if (finished) return;
    finished = true;
    handlers.onClosed(info);
  };

  const read = () => {
    const text = xhr.responseText;
    if (text.length === offset) return;
    handlers.onActivity();
    const chunk = text.slice(offset);
    offset = text.length;
    for (const message of parser.push(chunk)) handlers.onMessage(message);
  };

  const refuse = () => {
    finish({ status: xhr.status, retryAfterMs: retryAfterMs(xhr.getResponseHeader('Retry-After')) });
    xhr.abort();
  };

  xhr.open('GET', request.url);
  for (const [name, value] of Object.entries(request.headers)) xhr.setRequestHeader(name, value);
  xhr.onreadystatechange = () => {
    if (finished || xhr.readyState < 2 || opened) return;
    if (xhr.status === 200) {
      opened = true;
      handlers.onOpen();
    } else if (xhr.status !== 0) {
      refuse();
    }
  };
  xhr.onprogress = () => {
    if (finished) return;
    if (!opened) {
      if (xhr.status !== 200) return;
      opened = true;
      handlers.onOpen();
    }
    read();
    if (offset > RECYCLE_AFTER_CHARS && !finished) {
      finish({ status: null, recycled: true });
      xhr.abort();
    }
  };
  xhr.onload = () => {
    if (finished) return;
    if (xhr.status === 200) read();
    finish({ status: xhr.status === 200 ? null : xhr.status });
  };
  xhr.onerror = () => finish({ status: null });
  xhr.ontimeout = () => finish({ status: null });
  xhr.send();

  return () => {
    if (finished) return;
    finished = true;
    xhr.abort();
  };
};
