import { useEffect, useState } from 'react';
import { previewUndo } from '../../api/client';
import { previewReshelves } from '../../api/contract';

// The hide line stays out until the preview for this move says the undo re-shelves.
// A load still in flight, any error, and a preview with no matching effect all leave it out.
export function usePairHideLine(moveId: string): boolean {
  const [loaded, setLoaded] = useState<{ id: string; hide: boolean }>({ id: '', hide: false });

  useEffect(() => {
    if (moveId === '') return undefined;
    let active = true;
    void previewUndo(moveId)
      .then((preview) => {
        if (active) setLoaded({ id: moveId, hide: previewReshelves(preview, moveId) });
      })
      .catch(() => {
        if (active) setLoaded({ id: moveId, hide: false });
      });
    return () => {
      active = false;
    };
  }, [moveId]);

  return loaded.id === moveId && loaded.hide;
}
