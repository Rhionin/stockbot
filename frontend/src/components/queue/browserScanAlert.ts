import type { ScanDirection } from '@stockbot/shared/types';
import { backgroundAlertsEnabled, scanAlertsPreferred } from './pushAlerts';
import type { AttentionNotice } from './scanAttention';

export const SCAN_ALERT_OPEN = 'pantry-scan-alert-open';
const STORAGE_KEY = 'pantry-scan-alert-direction';

export interface ScanAlertOpenDetail {
  direction: ScanDirection | null;
}

// Remember the tab to open, then tell a scan queue that is already mounted.
// A queue that mounts afterwards reads the same stored direction.
export function openScanAlert(direction: ScanDirection | null): void {
  if (direction === 'stock_in' || direction === 'stock_out') {
    sessionStorage.setItem(STORAGE_KEY, direction);
  } else {
    sessionStorage.removeItem(STORAGE_KEY);
  }
  window.dispatchEvent(new CustomEvent<ScanAlertOpenDetail>(SCAN_ALERT_OPEN, { detail: { direction } }));
}

export function takeScanAlertDirection(): ScanDirection | null {
  const value = sessionStorage.getItem(STORAGE_KEY);
  sessionStorage.removeItem(STORAGE_KEY);
  if (value === 'stock_in' || value === 'stock_out') return value;
  return null;
}

export function showScanAlert(notice: AttentionNotice, onClick: () => void): void {
  // The device switch gates every alert, including the in-page fallback.
  // A stored push subscription delivers the same alert through the service
  // worker. That worker shows a system notification only when no Stockbot
  // window is visible. A visible window already lists the new scans.
  // This page notification is the fallback when the browser cannot subscribe.
  if (!scanAlertsPreferred()) return;
  if (backgroundAlertsEnabled()) return;
  if (typeof Notification === 'undefined' || Notification.permission !== 'granted') return;
  try {
    const notification = new Notification(notice.title, {
      body: notice.body,
      tag: notice.tag,
      lang: 'en',
      icon: '/favicon.svg',
      // A scan alert is a call to action. Keep it until the person acts on
      // browsers that honor this outside a service worker.
      requireInteraction: true,
    });
    notification.onclick = () => {
      notification.close();
      onClick();
    };
  } catch {
    // Permission can read as granted while the browser still refuses to
    // construct a notification. Later scans try again.
  }
}
