import { describe, expect, it } from 'vitest';
import type { PushSubscribeFailure } from '../queue/pushAlerts';
import { scanAlertStatus } from './scanAlertCopy';

const onIdle = {
  text: 'On: alerts arrive even when Stockbot is closed',
  tone: 'on' as const,
  retry: false,
};

const failureExpectations: Record<PushSubscribeFailure, { text: string; tone: 'problem'; retry: true }> = {
  'no-push': {
    text: 'This browser has no push service. Alerts arrive only while this tab is open.',
    tone: 'problem',
    retry: true,
  },
  'register-failed': {
    text: 'The browser could not register the service worker. Alerts arrive only while this tab is open.',
    tone: 'problem',
    retry: true,
  },
  'empty-vapid': {
    text: 'This Stockbot has no push key. Alerts arrive only while this tab is open.',
    tone: 'problem',
    retry: true,
  },
  'subscribe-rejected': {
    text: 'The browser refused the push subscription. Alerts arrive only while this tab is open.',
    tone: 'problem',
    retry: true,
  },
  'save-failed': {
    text: 'Stockbot could not save this device. Alerts arrive only while this tab is open.',
    tone: 'problem',
    retry: true,
  },
};

describe('scanAlertStatus permission', () => {
  it('reports unsupported and denied before preference, phase, or failure', () => {
    expect(scanAlertStatus('unsupported', true, 'turning-on', 'no-push')).toEqual({
      text: 'This browser cannot show notifications.',
      tone: 'quiet',
      retry: false,
    });
    expect(scanAlertStatus('denied', true, 'checking', 'save-failed')).toEqual({
      text: 'Blocked in browser settings',
      tone: 'problem',
      retry: false,
    });
  });
});

describe('scanAlertStatus phase', () => {
  it('shows in-progress copy while checking or toggling', () => {
    expect(scanAlertStatus('granted', true, 'checking', 'no-push')).toEqual({
      text: 'Checking this device',
      tone: 'quiet',
      retry: false,
    });
    expect(scanAlertStatus('granted', false, 'turning-on', null)).toEqual({
      text: 'Turning scan alerts on',
      tone: 'quiet',
      retry: false,
    });
    expect(scanAlertStatus('granted', true, 'turning-off', 'subscribe-rejected')).toEqual({
      text: 'Turning scan alerts off',
      tone: 'quiet',
      retry: false,
    });
  });
});

describe('scanAlertStatus preference off', () => {
  it('is off and ignores subscribe failures except save-failed', () => {
    expect(scanAlertStatus('granted', false, 'idle', null)).toEqual({
      text: 'Off',
      tone: 'off',
      retry: false,
    });
    expect(scanAlertStatus('granted', false, 'idle', 'no-push')).toEqual({
      text: 'Off',
      tone: 'off',
      retry: false,
    });
    expect(scanAlertStatus('granted', false, 'idle', 'save-failed')).toEqual({
      text: 'Stockbot could not remove this device. It may still send alerts.',
      tone: 'problem',
      retry: true,
    });
  });
});

describe('scanAlertStatus preference on', () => {
  it('is on when idle with no failure', () => {
    expect(scanAlertStatus('granted', true, 'idle', null)).toEqual(onIdle);
    expect(scanAlertStatus('default', true, 'idle', null)).toEqual(onIdle);
  });

  it('names each push subscribe failure and offers retry', () => {
    for (const failure of Object.keys(failureExpectations) as PushSubscribeFailure[]) {
      expect(scanAlertStatus('granted', true, 'idle', failure)).toEqual(failureExpectations[failure]);
    }
  });
});
