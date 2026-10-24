import { useEffect, useRef } from 'react';
import { useNavigate } from 'react-router-dom';
import type { ProcessingFailure, ScanEntry } from '@stockbot/shared/types';
import { openScanAlert, showScanAlert } from './browserScanAlert';
import { directionFromAlertURL, ensurePushSubscription, releasePushSubscription, scanAlertsPreferred, setBackgroundAlerts } from './pushAlerts';
import { ScanAttention, type AttentionNotice } from './scanAttention';

const parseEvent = <T,>(message: Event): T | null => {
  const data = (message as MessageEvent).data;
  if (typeof data !== 'string') return null;
  try {
    return JSON.parse(data) as T;
  } catch {
    return null;
  }
};

// Listens for the same scan events the queue renders, on every signed-in
// page, so an alert can fire while the tab is open on Inventory or elsewhere.
// The stream is not the telemetry stream: a second gap tracker would see
// interleaving ids and report false misses.
export function ScanAlertHost() {
  const navigate = useNavigate();
  const navigateRef = useRef(navigate);
  useEffect(() => {
    navigateRef.current = navigate;
  }, [navigate]);

  useEffect(() => {
    if (typeof Notification !== 'undefined' && Notification.permission === 'denied') {
      setBackgroundAlerts(false);
      void releasePushSubscription();
      return;
    }
    if (!scanAlertsPreferred()) {
      setBackgroundAlerts(false);
      void releasePushSubscription();
      return;
    }
    void ensurePushSubscription();
  }, []);

  useEffect(() => {
    const open = (notice: AttentionNotice) => {
      window.focus();
      openScanAlert(notice.direction);
      if (window.location.pathname !== '/') navigateRef.current('/');
    };
    const attention = new ScanAttention((notice) => {
      showScanAlert(notice, () => open(notice));
    });
    const source = new EventSource('/api/events');
    source.addEventListener('scan', (message) => {
      const entry = parseEvent<ScanEntry>(message);
      if (entry !== null) attention.noteScan(entry);
    });
    source.addEventListener('scan_processing_failed', (message) => {
      const failure = parseEvent<ProcessingFailure>(message);
      if (failure !== null) attention.noteFailure(failure);
    });
    const onMessage = (event: MessageEvent) => {
      const data = event.data as { type?: string; url?: string } | null;
      if (data?.type !== 'pantry-scan-alert') return;
      open({
        title: '',
        body: '',
        tag: '',
        direction: directionFromAlertURL(typeof data.url === 'string' ? data.url : ''),
      });
    };
    if ('serviceWorker' in navigator) {
      navigator.serviceWorker.addEventListener('message', onMessage);
    }
    return () => {
      attention.dispose();
      source.close();
      if ('serviceWorker' in navigator) {
        navigator.serviceWorker.removeEventListener('message', onMessage);
      }
    };
  }, []);

  return null;
}
