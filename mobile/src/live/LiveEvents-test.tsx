import { act, render } from '@testing-library/react-native';
import { afterEach, beforeEach, describe, expect, it, jest } from '@jest/globals';
import { createStockbotClient, type StockbotClient } from '../api/client';
import type { StreamHandlers, StreamRequest } from './streamTypes';
import { LiveEventsProvider, useLiveEvents, type LiveEvent } from './LiveEvents';

interface Opened {
  request: StreamRequest;
  handlers: StreamHandlers;
  close: jest.Mock;
}

const mockOpened: Opened[] = [];
let mockSession: unknown = { status: 'loading' };

jest.mock('@/session', () => ({ useSession: () => mockSession }));
jest.mock('./transport', () => ({
  openStream: (request: StreamRequest, handlers: StreamHandlers) => {
    const close = jest.fn();
    mockOpened.push({ request, handlers, close });
    return close;
  },
}));
jest.mock('./foreground', () => ({
  foregroundSource: { isActive: () => true, subscribe: () => () => undefined },
}));

function ready(client: StockbotClient, scope: string) {
  return { status: 'ready', client, scope, serverUrl: 'https://stockbot.example', account: null, warning: '' };
}

function nativeClient(token: string, callbacks: { onUnauthorized?: () => void; onUpgradeRequired?: () => void } = {}) {
  return createStockbotClient(
    { serverUrl: 'https://stockbot.example', token },
    { appVersion: '1.2.3', cookieSession: false, fetchImpl: jest.fn<typeof fetch>(async () => new Response('{}')), ...callbacks },
  );
}

function Listener({ onEvent }: { onEvent: (event: LiveEvent) => void }) {
  useLiveEvents(onEvent);
  return null;
}

function tree(onEvent: (event: LiveEvent) => void = () => undefined) {
  return (
    <LiveEventsProvider>
      <Listener onEvent={onEvent} />
    </LiveEventsProvider>
  );
}

beforeEach(() => {
  mockOpened.length = 0;
  mockSession = { status: 'loading' };
  jest.useFakeTimers();
});

afterEach(() => {
  jest.useRealTimers();
});

describe('LiveEventsProvider', () => {
  it('opens no stream until a session is ready', async () => {
    await render(tree());
    expect(mockOpened).toHaveLength(0);
  });

  it('opens one authenticated stream per ready session and delivers events to listeners', async () => {
    mockSession = ready(nativeClient('sbs_one'), 'one|h1');
    const heard: LiveEvent[] = [];
    await render(tree((event) => heard.push(event)));

    expect(mockOpened).toHaveLength(1);
    expect(mockOpened[0]!.request.url).toBe('https://stockbot.example/api/events');
    expect(mockOpened[0]!.request.headers).toMatchObject({
      Authorization: 'Bearer sbs_one',
      'X-Stockbot-Client': expect.stringContaining('1.2.3'),
    });

    await act(async () => {
      mockOpened[0]!.handlers.onOpen();
      mockOpened[0]!.handlers.onMessage({ id: '1', event: 'hint', data: '{"type":"scans"}' });
    });
    expect(heard).toEqual([{ kind: 'message', event: 'hint', data: '{"type":"scans"}' }]);
  });

  it('keeps the same stream across a re-render with the same scope', async () => {
    const client = nativeClient('sbs_one');
    mockSession = ready(client, 'one|h1');
    const screen = await render(tree());
    mockSession = ready(client, 'one|h1');
    await screen.rerender(tree());
    expect(mockOpened).toHaveLength(1);
    expect(mockOpened[0]!.close).not.toHaveBeenCalled();
  });

  it('tears the stream down and builds a new one when the scope changes', async () => {
    const client = nativeClient('sbs_one');
    mockSession = ready(client, 'sbs_one|h1');
    const screen = await render(tree());
    const first = mockOpened[0]!;
    await act(async () => first.handlers.onOpen());

    mockSession = ready(client, 'sbs_one|h2');
    await screen.rerender(tree());

    expect(first.close).toHaveBeenCalledTimes(1);
    expect(mockOpened).toHaveLength(2);
    await act(async () => first.handlers.onMessage({ id: '', event: 'scan', data: '{}' }));
  });

  it('uses the new account token after a different sign-in', async () => {
    mockSession = ready(nativeClient('sbs_one'), 'sbs_one|h1');
    const screen = await render(tree());
    mockSession = ready(nativeClient('sbs_two'), 'sbs_two|h1');
    await screen.rerender(tree());
    expect(mockOpened[1]!.request.headers.Authorization).toBe('Bearer sbs_two');
  });

  it('closes a live stream on sign-out', async () => {
    mockSession = ready(nativeClient('sbs_one'), 'one|h1');
    const screen = await render(tree());
    await act(async () => mockOpened[0]!.handlers.onOpen());

    mockSession = { status: 'signedOut', serverUrl: 'https://stockbot.example', error: '' };
    await screen.rerender(tree());

    expect(mockOpened[0]!.close).toHaveBeenCalledTimes(1);
  });

  it('stops the fallback poll and reconnects on sign-out', async () => {
    mockSession = ready(nativeClient('sbs_one'), 'one|h1');
    const heard: LiveEvent[] = [];
    const screen = await render(tree((event) => heard.push(event)));
    await act(async () => mockOpened[0]!.handlers.onClosed({ status: null }));
    await act(async () => jest.advanceTimersByTime(2000));
    expect(heard).toContainEqual({ kind: 'refresh', reason: 'poll' });

    mockSession = { status: 'signedOut', serverUrl: 'https://stockbot.example', error: '' };
    await screen.rerender(tree((event) => heard.push(event)));
    const seen = heard.length;
    await act(async () => jest.advanceTimersByTime(120_000));

    expect(heard).toHaveLength(seen);
    expect(mockOpened.length).toBeLessThanOrEqual(2);
  });

  it('stops the fallback poll and reconnects when the provider unmounts', async () => {
    mockSession = ready(nativeClient('sbs_one'), 'one|h1');
    const baseline = jest.getTimerCount();
    const screen = await render(tree());
    await act(async () => mockOpened[0]!.handlers.onClosed({ status: null }));
    expect(jest.getTimerCount()).toBeGreaterThan(baseline);
    const opens = mockOpened.length;
    await screen.unmount();
    await act(async () => jest.advanceTimersByTime(120_000));
    expect(mockOpened).toHaveLength(opens);
  });

  it('signs out through the client on a 401 and stops', async () => {
    const onUnauthorized = jest.fn();
    mockSession = ready(nativeClient('sbs_one', { onUnauthorized }), 'one|h1');
    const heard: LiveEvent[] = [];
    await render(tree((event) => heard.push(event)));
    await act(async () => mockOpened[0]!.handlers.onClosed({ status: 401 }));
    await act(async () => jest.advanceTimersByTime(120_000));
    expect(onUnauthorized).toHaveBeenCalledTimes(1);
    expect(heard).toEqual([]);
    expect(mockOpened).toHaveLength(1);
  });

  it('goes to the upgrade path on a 426 and stops', async () => {
    const onUpgradeRequired = jest.fn();
    const onUnauthorized = jest.fn();
    mockSession = ready(nativeClient('sbs_one', { onUpgradeRequired, onUnauthorized }), 'one|h1');
    await render(tree());
    await act(async () => mockOpened[0]!.handlers.onClosed({ status: 426 }));
    await act(async () => jest.advanceTimersByTime(120_000));
    expect(onUpgradeRequired).toHaveBeenCalledTimes(1);
    expect(onUnauthorized).not.toHaveBeenCalled();
    expect(mockOpened).toHaveLength(1);
  });

  it('asks the API once after a network failure so a dead session reaches sign-out', async () => {
    const client = nativeClient('sbs_one');
    const getAuthSession = jest.spyOn(client, 'getAuthSession').mockResolvedValue({} as never);
    mockSession = ready(client, 'one|h1');
    await render(tree());
    await act(async () => mockOpened[0]!.handlers.onClosed({ status: null }));
    expect(getAuthSession).toHaveBeenCalledTimes(1);
  });

  it('does nothing for a listener outside the provider', async () => {
    const onEvent = jest.fn();
    await render(<Listener onEvent={onEvent} />);
    expect(onEvent).not.toHaveBeenCalled();
  });
});

describe('client.events', () => {
  it('sends the Bearer token and client header only on native', () => {
    const native = nativeClient('sbs_x');
    expect(native.events.headers).toEqual({
      Accept: 'text/event-stream',
      'X-Stockbot-Client': expect.stringContaining('1.2.3'),
      Authorization: 'Bearer sbs_x',
    });
    const web = createStockbotClient({ serverUrl: '', token: null }, { cookieSession: true });
    expect(web.events).toMatchObject({ url: '/api/events', headers: { Accept: 'text/event-stream' } });
    expect(Object.keys(web.events.headers)).toEqual(['Accept']);
  });
});
