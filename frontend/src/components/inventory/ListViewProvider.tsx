import { useCallback, useEffect, useState, type ReactNode } from 'react';
import { useHouseholdSession } from '../auth/householdSession';
import { ListViewContext, type ListViewApi } from './listViewContext';
import {
  adoptListViewScope,
  readListView,
  rememberedInventoryPath,
  writeListView,
  type ListViewSnapshot,
} from './listViewState';

export function ListViewProvider({ children }: { children: ReactNode }) {
  const session = useHouseholdSession();
  const userId = session.auth?.user?.id ?? '';
  const householdId = session.auth?.activeHouseholdId ?? '';
  adoptListViewScope(`${userId}\0${householdId}`);
  const [resetNonce, setResetNonce] = useState(0);

  useEffect(() => () => {
    writeListView(null);
  }, []);

  const peek = useCallback((now = Date.now()) => readListView(now), []);

  const save = useCallback((view: ListViewSnapshot) => {
    writeListView(view);
  }, []);

  const consume = useCallback(() => {
    writeListView(null);
  }, []);

  const rememberedUrl = useCallback((fallback: string, now = Date.now()) => {
    return rememberedInventoryPath(readListView(now), now) ?? fallback;
  }, []);

  const requestReset = useCallback(() => {
    writeListView(null);
    setResetNonce((current) => current + 1);
  }, []);

  const api: ListViewApi = {
    peek,
    save,
    consume,
    rememberedUrl,
    resetNonce,
    requestReset,
  };

  return <ListViewContext.Provider value={api}>{children}</ListViewContext.Provider>;
}
