import { useEffect, useState } from 'react';
import { previewUndo } from '../../api/client';
import { previewReshelves } from '@stockbot/shared/contract';

// Each open gets its own answer. Closing drops the last one, so the next open
// stays blank until that open's preview returns.
export function usePairHideLine(moveId: string): boolean {
  const [trackedId, setTrackedId] = useState(moveId);
  const [openToken, setOpenToken] = useState(0);
  if (trackedId !== moveId) {
    setTrackedId(moveId);
    setOpenToken((current) => current + 1);
  }

  const [answer, setAnswer] = useState<{ token: number; hide: boolean } | null>(null);

  useEffect(() => {
    if (moveId === '') return undefined;
    const token = openToken;
    let active = true;
    void previewUndo(moveId)
      .then((preview) => {
        if (active) setAnswer({ token, hide: previewReshelves(preview, moveId) });
      })
      .catch(() => {
        if (active) setAnswer({ token, hide: false });
      });
    return () => {
      active = false;
    };
  }, [moveId, openToken]);

  return moveId !== '' && answer !== null && answer.token === openToken && answer.hide;
}
