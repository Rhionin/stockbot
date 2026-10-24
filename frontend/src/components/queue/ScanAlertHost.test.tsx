import { afterEach, describe, expect, it, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom';
import type { ProcessingFailure, ScanEntry } from '@stockbot/shared/types';
import { ScanAlertHost } from './ScanAlertHost';
import { setBackgroundAlerts, writeScanAlertPreference } from './pushAlerts';

class FakeNotification {
  static permission: NotificationPermission = 'granted';
  static instances: FakeNotification[] = [];
  static requestPermission = vi.fn();
  onclick: (() => void) | null = null;
  title: string;
  options?: NotificationOptions;
  constructor(title: string, options?: NotificationOptions) {
    this.title = title;
    this.options = options;
    FakeNotification.instances.push(this);
  }
  close() {}
}

class FakeEventSource {
  static instances: FakeEventSource[] = [];
  url: string;
  private readonly listeners = new Map<string, Array<(event: MessageEvent) => void>>();
  constructor(url: string) {
    this.url = url;
    FakeEventSource.instances.push(this);
  }
  addEventListener(type: string, listener: (event: MessageEvent) => void) {
    const current = this.listeners.get(type) ?? [];
    current.push(listener);
    this.listeners.set(type, current);
  }
  close() {}
  dispatch(type: string, payload: unknown) {
    for (const listener of this.listeners.get(type) ?? []) {
      listener({ data: JSON.stringify(payload) } as MessageEvent);
    }
  }
}

const LocationProbe = () => {
  const location = useLocation();
  return <div data-testid="location">{location.pathname}</div>;
};

const scan = (overrides: Partial<ScanEntry>): ScanEntry => ({
  id: 'scan-1',
  userId: 'user-1',
  barcode: '111',
  scannedAt: '2026-03-20T10:00:00Z',
  direction: 'stock_out',
  unitCount: 1,
  expiresAt: null,
  status: 'flagged',
  productId: null,
  committedAt: null,
  createdAt: '2026-03-20T10:00:00Z',
  ...overrides,
});

describe('ScanAlertHost', () => {
  afterEach(() => {
    vi.unstubAllGlobals();
    sessionStorage.clear();
    localStorage.clear();
    FakeNotification.instances = [];
    FakeEventSource.instances = [];
    setBackgroundAlerts(false);
  });

  const renderHost = (path = '/inventory') => {
    vi.stubGlobal('Notification', FakeNotification);
    vi.stubGlobal('EventSource', FakeEventSource);
    render(
      <MemoryRouter initialEntries={[path]}>
        <LocationProbe />
        <Routes>
          <Route path="*" element={<ScanAlertHost />} />
        </Routes>
      </MemoryRouter>,
    );
    return FakeEventSource.instances[0];
  };

  it('does not ask for permission and notifies immediately for a failure and an unrecognized product', () => {
    const source = renderHost();
    expect(FakeNotification.requestPermission).not.toHaveBeenCalled();

    source.dispatch('scan_processing_failed', {
      id: 'lookup-1',
      barcode: '999',
      message: "Couldn't look up that barcode.",
    } satisfies ProcessingFailure);
    source.dispatch('scan', scan({ id: 'flag-1', barcode: '000111' }));

    expect(FakeNotification.instances.map((notification) => notification.title)).toEqual([
      'Scan failed',
      'Unrecognized product',
    ]);
    expect(FakeNotification.instances[0]?.options?.body).toBe(
      "Couldn't look up that barcode. Open the scan queue to try again.",
    );
    expect(FakeNotification.instances[0]?.options?.requireInteraction).toBe(true);
    expect(FakeNotification.instances[1]?.options?.body).toBe(
      'Barcode 000111 needs a product. Open the scan queue to match it.',
    );
  });

  it('focuses the scan queue for the direction named by the notification', () => {
    const source = renderHost('/inventory');
    source.dispatch('scan', scan({ id: 'flag-1', barcode: '000111', direction: 'stock_in' }));
    const notification = FakeNotification.instances[0];
    expect(notification).toBeDefined();
    notification?.onclick?.();
    expect(screen.getByTestId('location')).toHaveTextContent('/');
    expect(sessionStorage.getItem('pantry-scan-alert-direction')).toBe('stock_in');
  });

  it('does not notify while scan alerts are off on this device', () => {
    writeScanAlertPreference(false);
    const source = renderHost();
    source.dispatch('scan_processing_failed', {
      id: 'lookup-1',
      barcode: '999',
      message: "Couldn't look up that barcode.",
    } satisfies ProcessingFailure);
    expect(FakeNotification.instances).toEqual([]);
  });

  it('leaves the visible notification to the service worker when push is subscribed', () => {
    const source = renderHost();
    setBackgroundAlerts(true);
    source.dispatch('scan_processing_failed', {
      id: 'lookup-1',
      barcode: '999',
      message: "Couldn't look up that barcode.",
    } satisfies ProcessingFailure);
    expect(FakeNotification.instances).toEqual([]);
  });
});
