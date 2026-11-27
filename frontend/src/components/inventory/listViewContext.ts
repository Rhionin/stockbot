import { createContext, useContext } from 'react';
import type { ListViewSnapshot } from './listViewState';

export interface ListViewApi {
  peek: (now?: number) => ListViewSnapshot | null;
  save: (view: ListViewSnapshot) => void;
  consume: () => void;
  rememberedUrl: (fallback: string, now?: number) => string;
  resetNonce: number;
  requestReset: () => void;
}

export const emptyListView: ListViewApi = {
  peek: () => null,
  save: () => {},
  consume: () => {},
  rememberedUrl: (fallback) => fallback,
  resetNonce: 0,
  requestReset: () => {},
};

export const ListViewContext = createContext<ListViewApi>(emptyListView);

export function useListView(): ListViewApi {
  return useContext(ListViewContext);
}
