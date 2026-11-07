import { afterEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MantineProvider } from '@mantine/core';
import { SettingsPage } from './SettingsPage';
import { setBackgroundAlerts, writeScanAlertPreference } from '../queue/pushAlerts';

class FakeNotification {
  static permission: NotificationPermission = 'default';
  static requestPermission = vi.fn(() => Promise.resolve(FakeNotification.permission));
}

const settings = { months: 3, opening: false, wipePhrase: 'WIPE INVENTORY' };

const jsonResponse = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });

const subscription = {
  endpoint: 'https://push.example/device',
  unsubscribe: vi.fn(() => Promise.resolve(true)),
  toJSON: () => ({
    endpoint: 'https://push.example/device',
    keys: { p256dh: 'p256', auth: 'auth' },
  }),
};

const installPush = (
  publicKey = 'BElk',
  existing: typeof subscription | null = subscription,
  options?: { register?: () => Promise<unknown> },
) => {
  vi.stubGlobal('PushManager', class PushManager {});
  let current = existing;
  const registration = {
    pushManager: {
      getSubscription: () => Promise.resolve(current),
      subscribe: () => {
        current = subscription;
        return Promise.resolve(subscription);
      },
    },
  };
  Object.defineProperty(navigator, 'serviceWorker', {
    configurable: true,
    value: {
      register: options?.register ?? (() => Promise.resolve(registration)),
      ready: Promise.resolve(registration),
      getRegistration: () => Promise.resolve(registration),
      addEventListener: () => {},
      removeEventListener: () => {},
    },
  });
  const deleted: unknown[] = [];
  const posted: unknown[] = [];
  vi.stubGlobal('fetch', (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    const method = init?.method ?? 'GET';
    if (url.endsWith('/api/settings/supply')) return Promise.resolve(jsonResponse(settings));
    if (url.endsWith('/api/settings/group-rule')) return Promise.resolve(jsonResponse({ rule: 'same_as_ran_out' }));
    if (url.endsWith('/api/push/vapid-public-key')) return Promise.resolve(jsonResponse({ publicKey }));
    if (url.endsWith('/api/push/subscriptions') && method === 'POST') {
      posted.push(JSON.parse(String(init?.body ? String(init.body) : '{}')));
      return Promise.resolve(jsonResponse({ endpoint: subscription.endpoint }));
    }
    if (url.endsWith('/api/push/subscriptions') && method === 'DELETE') {
      deleted.push(JSON.parse(String(init?.body)));
      return Promise.resolve(new Response(null, { status: 204 }));
    }
    throw new Error(`Unexpected request: ${method} ${url}`);
  });
  return { posted, deleted };
};

const renderSettings = () => render(
  <MantineProvider><SettingsPage /></MantineProvider>,
);

describe('Scan alerts on Settings', () => {
  afterEach(() => {
    vi.unstubAllGlobals();
    localStorage.clear();
    setBackgroundAlerts(false);
    Reflect.deleteProperty(navigator, 'serviceWorker');
    subscription.unsubscribe.mockClear();
  });

  it('asks for permission only after the switch is turned on', async () => {
    FakeNotification.permission = 'default';
    FakeNotification.requestPermission.mockClear();
    vi.stubGlobal('Notification', FakeNotification);
    vi.stubGlobal('fetch', (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.endsWith('/api/settings/supply')) return Promise.resolve(jsonResponse(settings));
      if (url.endsWith('/api/settings/group-rule')) return Promise.resolve(jsonResponse({ rule: 'same_as_ran_out' }));
      throw new Error(`Unexpected request: ${url}`);
    });

    renderSettings();
    const toggle = await screen.findByRole('switch', { name: 'Scan alerts on this device' });
    expect(toggle).toHaveAccessibleDescription(/This browser only/);
    expect(toggle).not.toBeChecked();
    expect(screen.getByRole('status')).toHaveTextContent('Off');
    expect(FakeNotification.requestPermission).not.toHaveBeenCalled();

    fireEvent.click(toggle);
    expect(FakeNotification.requestPermission).toHaveBeenCalledOnce();
  });

  it('turns scan alerts on, then off, and deletes the server row', async () => {
    FakeNotification.permission = 'default';
    FakeNotification.requestPermission = vi.fn(() => {
      FakeNotification.permission = 'granted';
      return Promise.resolve<NotificationPermission>('granted');
    });
    vi.stubGlobal('Notification', FakeNotification);
    const { posted, deleted } = installPush('BElk', null);

    renderSettings();
    const toggle = await screen.findByRole('switch', { name: 'Scan alerts on this device' });
    fireEvent.click(toggle);

    expect(await screen.findByRole('status')).toHaveTextContent(
      'On: alerts arrive even when Stockbot is closed',
    );
    await waitFor(() => expect(posted).toEqual([
      {
        endpoint: 'https://push.example/device',
        keys: { p256dh: 'p256', auth: 'auth' },
        userId: 'user-1',
        deviceId: localStorage.getItem('pantry-push-device'),
      },
    ]));
    expect(toggle).toBeChecked();

    fireEvent.click(screen.getByRole('switch', { name: 'Scan alerts on this device' }));
    expect(await screen.findByRole('status')).toHaveTextContent('Off');
    await waitFor(() => expect(subscription.unsubscribe).toHaveBeenCalledOnce());
    expect(deleted).toEqual([{ endpoint: 'https://push.example/device' }]);
    expect(toggle).not.toBeChecked();
  });

  it('keeps the switch on after a reload', async () => {
    FakeNotification.permission = 'default';
    FakeNotification.requestPermission = vi.fn(() => {
      FakeNotification.permission = 'granted';
      return Promise.resolve<NotificationPermission>('granted');
    });
    vi.stubGlobal('Notification', FakeNotification);
    installPush('BElk', null);

    const first = renderSettings();
    fireEvent.click(await screen.findByRole('switch', { name: 'Scan alerts on this device' }));
    expect(await screen.findByRole('status')).toHaveTextContent(
      'On: alerts arrive even when Stockbot is closed',
    );
    expect(localStorage.getItem('pantry-scan-alerts')).toBe('on');
    first.unmount();

    renderSettings();
    const toggle = await screen.findByRole('switch', { name: 'Scan alerts on this device' });
    expect(toggle).toBeChecked();
    expect(await screen.findByRole('status')).toHaveTextContent(
      'On: alerts arrive even when Stockbot is closed',
    );
  });

  it('shows a failed service worker registration on the status line', async () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {});
    FakeNotification.permission = 'default';
    FakeNotification.requestPermission = vi.fn(() => {
      FakeNotification.permission = 'granted';
      return Promise.resolve<NotificationPermission>('granted');
    });
    vi.stubGlobal('Notification', FakeNotification);
    installPush('BElk', null, {
      register: () => Promise.reject(new Error("The provided scope ('/') is not allowed.")),
    });

    renderSettings();
    fireEvent.click(await screen.findByRole('switch', { name: 'Scan alerts on this device' }));

    const status = await screen.findByRole('status');
    expect(status).toHaveTextContent(
      'The browser could not register the service worker. Alerts arrive only while this tab is open.',
    );
    expect(screen.getByRole('button', { name: 'Retry' })).toHaveAccessibleDescription(
      'The browser could not register the service worker. Alerts arrive only while this tab is open.',
    );
    expect(screen.getByRole('switch', { name: 'Scan alerts on this device' })).toBeChecked();
    warn.mockRestore();
  });

  it('shows an empty push key on the status line with Retry', async () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {});
    FakeNotification.permission = 'default';
    FakeNotification.requestPermission = vi.fn(() => {
      FakeNotification.permission = 'granted';
      return Promise.resolve<NotificationPermission>('granted');
    });
    vi.stubGlobal('Notification', FakeNotification);
    installPush('', null);

    renderSettings();
    fireEvent.click(await screen.findByRole('switch', { name: 'Scan alerts on this device' }));

    const status = await screen.findByRole('status');
    expect(status).toHaveTextContent(
      'This Stockbot has no push key. Alerts arrive only while this tab is open.',
    );
    const retry = screen.getByRole('button', { name: 'Retry' });
    expect(retry).toHaveAccessibleDescription(
      'This Stockbot has no push key. Alerts arrive only while this tab is open.',
    );
    fireEvent.click(retry);
    await waitFor(() => expect(warn).toHaveBeenCalledTimes(2));
    expect(screen.getByRole('switch', { name: 'Scan alerts on this device' })).toBeChecked();
    warn.mockRestore();
  });

  it('shows a push failure for a device that already allowed notifications', async () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {});
    FakeNotification.permission = 'granted';
    vi.stubGlobal('Notification', FakeNotification);
    installPush('', null);

    renderSettings();
    expect(await screen.findByRole('status')).toHaveTextContent(
      'This Stockbot has no push key. Alerts arrive only while this tab is open.',
    );
    expect(screen.getByRole('switch', { name: 'Scan alerts on this device' })).toBeChecked();
    expect(screen.getByRole('button', { name: 'Retry' })).toBeInTheDocument();
    expect(localStorage.getItem('pantry-scan-alerts')).toBe('on');
    expect(warn).toHaveBeenCalled();
    warn.mockRestore();
  });

  it('says alerts are blocked when the browser has denied permission', async () => {
    FakeNotification.permission = 'denied';
    FakeNotification.requestPermission = vi.fn(() => Promise.resolve<NotificationPermission>('denied'));
    vi.stubGlobal('Notification', FakeNotification);
    vi.stubGlobal('fetch', (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.endsWith('/api/settings/supply')) return Promise.resolve(jsonResponse(settings));
      if (url.endsWith('/api/settings/group-rule')) return Promise.resolve(jsonResponse({ rule: 'same_as_ran_out' }));
      throw new Error(`Unexpected request: ${url}`);
    });

    renderSettings();
    expect(await screen.findByRole('status')).toHaveTextContent('Blocked in browser settings');
    expect(screen.getByRole('switch', { name: 'Scan alerts on this device' })).toBeDisabled();
    expect(FakeNotification.requestPermission).not.toHaveBeenCalled();
  });

  it('stays off across a reload until the switch is used', async () => {
    FakeNotification.permission = 'default';
    vi.stubGlobal('Notification', FakeNotification);
    writeScanAlertPreference(false);
    vi.stubGlobal('fetch', (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.endsWith('/api/settings/supply')) return Promise.resolve(jsonResponse(settings));
      if (url.endsWith('/api/settings/group-rule')) return Promise.resolve(jsonResponse({ rule: 'same_as_ran_out' }));
      throw new Error(`Unexpected request: ${url}`);
    });

    const first = renderSettings();
    expect(await screen.findByRole('status')).toHaveTextContent('Off');
    first.unmount();
    renderSettings();
    expect(await screen.findByRole('switch', { name: 'Scan alerts on this device' })).not.toBeChecked();
    expect(screen.getByRole('status')).toHaveTextContent('Off');
  });
});
