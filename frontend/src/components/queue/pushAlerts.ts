import { deletePushSubscription, getVapidPublicKey, savePushSubscription } from '../../api/client';
import { inlineFailure } from '@stockbot/shared/contract';
import { report } from '../../telemetry/client';
import type { ScanDirection } from '@stockbot/shared/types';

const preferenceKey = 'pantry-scan-alerts';
const deviceKey = 'pantry-push-device';
// One household, the same id the scan queue uses. The device id lives in this
// origin's storage, so a later subscribe from this browser replaces its older
// endpoints. A new hostname starts a new id; the server drops the previous host.
const pushUserId = 'user-1';

// Reasons ensurePushSubscription can name. Anything else is a precondition
// (permission, or this device has alerts turned off), not a failed subscribe.
export type PushSubscribeFailure =
  | 'no-push'
  | 'register-failed'
  | 'empty-vapid'
  | 'subscribe-rejected'
  | 'save-failed';

export type PushSubscribeResult =
  | { ok: true }
  | { ok: false; reason: PushSubscribeFailure | 'unavailable' | 'denied' | 'not-granted' };

const failureReasons = new Set<PushSubscribeFailure>([
  'no-push',
  'register-failed',
  'empty-vapid',
  'subscribe-rejected',
  'save-failed',
]);

export function isPushSubscribeFailure(reason: string): reason is PushSubscribeFailure {
  return failureReasons.has(reason as PushSubscribeFailure);
}

let background = false;
const listeners = new Set<(on: boolean) => void>();

export function backgroundAlertsEnabled(): boolean {
  return background;
}

export function setBackgroundAlerts(on: boolean): void {
  background = on;
  for (const listener of listeners) listener(on);
}

export function onBackgroundAlerts(listener: (on: boolean) => void): () => void {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}

export function scanAlertsPreferred(): boolean {
  try {
    const stored = localStorage.getItem(preferenceKey);
    if (stored === 'on') return true;
    if (stored === 'off') return false;
  } catch {
    return false;
  }
  // The old notify button was the only way permission became granted.
  // Keep that device on, or a reload would drop alerts the person already allowed.
  if (typeof Notification !== 'undefined' && Notification.permission === 'granted') {
    writeScanAlertPreference(true);
    return true;
  }
  return false;
}

export function writeScanAlertPreference(on: boolean): void {
  try {
    localStorage.setItem(preferenceKey, on ? 'on' : 'off');
  } catch {
    // A private window can refuse storage. The switch still reflects this visit.
  }
}

export function directionFromAlertURL(url: string): ScanDirection | null {
  try {
    const value = new URL(url, window.location.origin).searchParams.get('scanAlert');
    if (value === 'stock_in' || value === 'stock_out') return value;
  } catch {
    return null;
  }
  return null;
}

function pushDeviceId(): string {
  try {
    const existing = localStorage.getItem(deviceKey)?.trim() ?? '';
    if (existing !== '') return existing;
    const created = typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function'
      ? crypto.randomUUID()
      : `device-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 10)}`;
    localStorage.setItem(deviceKey, created);
    return created;
  } catch {
    return '';
  }
}

function applicationServerKey(publicKey: string): BufferSource {
  const padding = '='.repeat((4 - (publicKey.length % 4)) % 4);
  const base64 = (publicKey + padding).replace(/-/g, '+').replace(/_/g, '/');
  const raw = atob(base64);
  const bytes = new Uint8Array(raw.length);
  for (let index = 0; index < raw.length; index += 1) {
    bytes[index] = raw.charCodeAt(index);
  }
  return bytes;
}

function reportPushFailure(reason: PushSubscribeFailure, error?: unknown): void {
  const detail = (inlineFailure(error, '') ?? '').trim();
  const message = detail === ''
    ? `push subscription failed: ${reason}`
    : `push subscription failed: ${reason}: ${detail}`;
  console.warn(message);
  report({ kind: 'js_error', message });
}

function fail(reason: PushSubscribeFailure, error?: unknown): PushSubscribeResult {
  setBackgroundAlerts(false);
  reportPushFailure(reason, error);
  return { ok: false, reason };
}

function stopped(reason: 'unavailable' | 'denied' | 'not-granted'): PushSubscribeResult {
  setBackgroundAlerts(false);
  return { ok: false, reason };
}

// releasePushSubscription removes this browser's push endpoint from the
// browser and from the server. A missing subscription is success.
export async function releasePushSubscription(): Promise<PushSubscribeFailure | null> {
  if (!('serviceWorker' in navigator)) return null;
  let registration: ServiceWorkerRegistration | undefined;
  try {
    registration = await navigator.serviceWorker.getRegistration('/');
  } catch (error) {
    reportPushFailure('no-push', error);
    return 'no-push';
  }
  let subscription: PushSubscription | null;
  try {
    subscription = (await registration?.pushManager.getSubscription()) ?? null;
  } catch (error) {
    reportPushFailure('subscribe-rejected', error);
    return 'subscribe-rejected';
  }
  if (!subscription) return null;
  const endpoint = subscription.endpoint;
  try {
    await subscription.unsubscribe();
  } catch {
    // The server row is what would keep sending. Drop that even if the
    // browser already forgot the subscription.
  }
  if (endpoint === '') return null;
  try {
    await deletePushSubscription(endpoint);
  } catch (error) {
    reportPushFailure('save-failed', error);
    return 'save-failed';
  }
  return null;
}

export async function disableScanAlerts(): Promise<PushSubscribeFailure | null> {
  writeScanAlertPreference(false);
  setBackgroundAlerts(false);
  return releasePushSubscription();
}

// ensurePushSubscription registers /sw.js and stores the browser's push
// endpoint. It does not ask for permission. Call it after this device has
// scan alerts on and notification permission is already granted.
export async function ensurePushSubscription(): Promise<PushSubscribeResult> {
  if (!scanAlertsPreferred()) return stopped('not-granted');
  if (typeof Notification === 'undefined') return stopped('unavailable');
  if (Notification.permission === 'denied') {
    void releasePushSubscription();
    return stopped('denied');
  }
  if (Notification.permission !== 'granted') return stopped('not-granted');
  if (!('serviceWorker' in navigator) || !('PushManager' in window)) {
    return fail('no-push');
  }

  let registration: ServiceWorkerRegistration;
  try {
    registration = await navigator.serviceWorker.register('/sw.js', {
      scope: '/',
      updateViaCache: 'none',
    });
  } catch (error) {
    return fail('register-failed', error);
  }

  let ready: ServiceWorkerRegistration;
  try {
    ready = await navigator.serviceWorker.ready;
  } catch (error) {
    return fail('no-push', error);
  }

  const pushManager = ready.pushManager ?? registration.pushManager;
  if (!pushManager) return fail('no-push');

  let subscription: PushSubscription | null;
  try {
    subscription = await pushManager.getSubscription();
  } catch (error) {
    return fail('subscribe-rejected', error);
  }

  if (subscription === null) {
    let key: { publicKey: string };
    try {
      key = await getVapidPublicKey();
    } catch (error) {
      return fail('empty-vapid', error);
    }
    if (key.publicKey.trim() === '') return fail('empty-vapid');
    let applicationKey: BufferSource;
    try {
      applicationKey = applicationServerKey(key.publicKey);
    } catch (error) {
      return fail('empty-vapid', error);
    }
    try {
      // Chrome rejects userVisibleOnly: false. A push that displays nothing
      // can be replaced by a generic "site updated in the background" notice
      // and, after repeated silent pushes, dropped. The service worker still
      // calls showNotification unless a Stockbot window is already visible,
      // which Chrome counts as the user-visible result.
      subscription = await pushManager.subscribe({
        userVisibleOnly: true,
        applicationServerKey: applicationKey,
      });
    } catch (error) {
      return fail('subscribe-rejected', error);
    }
  }

  if (!scanAlertsPreferred()) {
    setBackgroundAlerts(false);
    await releasePushSubscription();
    return stopped('not-granted');
  }

  const body = subscription.toJSON();
  if (!body.endpoint || !body.keys?.p256dh || !body.keys.auth) {
    return fail('subscribe-rejected');
  }
  try {
    await savePushSubscription({
      endpoint: body.endpoint,
      keys: { p256dh: body.keys.p256dh, auth: body.keys.auth },
      userId: pushUserId,
      deviceId: pushDeviceId(),
    });
  } catch (error) {
    return fail('save-failed', error);
  }
  if (!scanAlertsPreferred()) {
    setBackgroundAlerts(false);
    await releasePushSubscription();
    return stopped('not-granted');
  }
  setBackgroundAlerts(true);
  return { ok: true };
}
