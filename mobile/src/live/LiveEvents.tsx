import { createContext, use, useEffect, useMemo, useRef, type ReactNode } from 'react';
import { useSession } from '@/session';
import { createLiveConnection, type LiveEvent } from './connection';
import { foregroundSource } from './foreground';
import { openStream } from './transport';

export type { LiveEvent };

type Listener = (event: LiveEvent) => void;

export interface LiveHub {
  subscribe: (listener: Listener) => () => void;
  emit: Listener;
}

export function createLiveHub(): LiveHub {
  const listeners = new Set<Listener>();
  return {
    subscribe(listener) {
      listeners.add(listener);
      return () => {
        listeners.delete(listener);
      };
    },
    emit(event) {
      for (const listener of [...listeners]) listener(event);
    },
  };
}

const LiveContext = createContext<LiveHub | null>(null);

export function LiveHubProvider({ hub, children }: { hub: LiveHub; children: ReactNode }) {
  return <LiveContext.Provider value={hub}>{children}</LiveContext.Provider>;
}

// One stream per signed-in session. A new account or household rebuilds it, and
// signing out closes it.
export function LiveEventsProvider({ children }: { children: ReactNode }) {
  const session = useSession();
  const hub = useMemo(() => createLiveHub(), []);
  const client = session.status === 'ready' ? session.client : null;
  const scope = session.status === 'ready' ? session.scope : null;

  useEffect(() => {
    if (client === null) return;
    const connection = createLiveConnection({
      request: { url: client.events.url, headers: client.events.headers },
      openStream,
      foreground: foregroundSource,
      rejected: client.events.rejected,
      probe: () => {
        void client.getAuthSession().catch(() => undefined);
      },
    });
    const unsubscribe = connection.subscribe(hub.emit);
    connection.start();
    return () => {
      unsubscribe();
      connection.dispose();
    };
  }, [client, scope, hub]);

  return <LiveHubProvider hub={hub}>{children}</LiveHubProvider>;
}

// Calls the listener for each live message, and with a refresh signal when the
// stream came back or the fallback poll fired. A screen outside the provider
// hears nothing.
export function useLiveEvents(listener: Listener): void {
  const hub = use(LiveContext);
  const latest = useRef(listener);
  useEffect(() => {
    latest.current = listener;
  }, [listener]);
  useEffect(() => hub?.subscribe((event) => latest.current(event)), [hub]);
}
