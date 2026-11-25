import { useCallback, useEffect, useRef, useState } from 'react';
import { CONTRIBUTE_SAVE_FAILURE } from '@stockbot/shared/productForm';
import { inlineFailure } from '@stockbot/shared/contract';
import type { StockbotClient } from '../api/client';

export interface ContributionState {
  enabled: boolean;
  configured: boolean;
  error: string;
  toggle: (next: boolean) => Promise<boolean>;
}

// The household sharing switch. A failed read leaves sharing off, which is the
// safe default, and a failed save puts the switch back with a sentence.
export function useContribution(client: StockbotClient): ContributionState {
  const [enabled, setEnabled] = useState(false);
  const [configured, setConfigured] = useState(false);
  const [error, setError] = useState('');
  const alive = useRef(true);

  useEffect(() => {
    alive.current = true;
    client.getContributionSettings().then((settings) => {
      if (!alive.current) return;
      setEnabled(settings.enabled === true);
      setConfigured(settings.configured === true);
    }).catch(() => {
      if (alive.current) setEnabled(false);
    });
    return () => {
      alive.current = false;
    };
  }, [client]);

  const toggle = useCallback(async (next: boolean): Promise<boolean> => {
    setError('');
    setEnabled(next);
    try {
      const saved = await client.updateContributionSettings(next);
      if (!alive.current) return saved.enabled;
      setEnabled(saved.enabled);
      setConfigured(saved.configured);
      return saved.enabled;
    } catch (requestError) {
      if (alive.current) {
        setEnabled(!next);
        setError(inlineFailure(requestError, CONTRIBUTE_SAVE_FAILURE) ?? '');
      }
      return !next;
    }
  }, [client]);

  return { enabled, configured, error, toggle };
}
