import { useEffect } from 'react';
import { EVENTS_PATH, type LiveHint, type LiveHintType } from '@stockbot/shared/contract';

export const HOUSEHOLD_SWITCH_EVENT = 'stockbot-household-switch';

export function announceHouseholdSwitch(): void {
  window.dispatchEvent(new Event(HOUSEHOLD_SWITCH_EVENT));
}

function parseHint(data: string): LiveHint | null {
  try {
    const hint = JSON.parse(data) as { type?: string; cursor?: string };
    if (hint.type === 'rekey' || hint.type === 'devices') return { type: hint.type };
    if ((hint.type === 'scans' || hint.type === 'stock' || hint.type === 'list') && typeof hint.cursor === 'string') {
      return { type: hint.type, cursor: hint.cursor };
    }
  } catch {
    return null;
  }
  return null;
}

/** Refetch while the page is visible. Poll only after the stream drops. */
export function useLiveHint(types: readonly LiveHintType[], onHint: () => void, pollMs: number): void {
  useEffect(() => {
    if (typeof EventSource !== 'function') return undefined;
    let source: EventSource | null = null;
    let poll = 0;
    const stopPoll = () => {
      if (poll !== 0) window.clearInterval(poll);
      poll = 0;
    };
    const deliver = (hint: LiveHint | null) => {
      if (document.visibilityState === 'hidden') return;
      if (hint === null || types.includes(hint.type)) onHint();
    };
    const connect = () => {
      stopPoll();
      source?.close();
      source = new EventSource(EVENTS_PATH);
      source.addEventListener('hint', (event) => {
        deliver(parseHint((event as MessageEvent).data));
      });
      source.onerror = () => {
        source?.close();
        source = null;
        if (poll !== 0 || document.visibilityState === 'hidden') return;
        poll = window.setInterval(() => deliver(null), pollMs);
      };
    };
    const onVisibility = () => {
      if (document.visibilityState === 'hidden') {
        source?.close();
        source = null;
        stopPoll();
        return;
      }
      if (source === null) connect();
    };
    const onSwitch = () => {
      onHint();
      if (document.visibilityState !== 'hidden') connect();
    };
    if (document.visibilityState !== 'hidden') connect();
    document.addEventListener('visibilitychange', onVisibility);
    window.addEventListener(HOUSEHOLD_SWITCH_EVENT, onSwitch);
    return () => {
      document.removeEventListener('visibilitychange', onVisibility);
      window.removeEventListener(HOUSEHOLD_SWITCH_EVENT, onSwitch);
      source?.close();
      stopPoll();
    };
    // `types` is read through its joined key so a new array with the same names does not reconnect.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [onHint, pollMs, types.join(',')]);
}
