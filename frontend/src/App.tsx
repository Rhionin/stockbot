import { useCallback, useEffect, useState } from 'react';
import { BrowserRouter, NavLink, Navigate, Route, Routes, useLocation } from 'react-router-dom';
import { AppShell, Group, Title } from '@mantine/core';
import { HouseholdGate } from './components/auth/HouseholdGate';
import { InviteAcceptPage } from './components/auth/InviteAcceptPage';
import { ResetPasswordPage } from './components/auth/ResetPasswordPage';
import { SetupPage } from './components/auth/SetupPage';
import { useHouseholdSession } from './components/auth/householdSession';
import { DevicesPage } from './components/household/DevicesPage';
import { HouseholdPage } from './components/household/HouseholdPage';
import { HouseholdSwitcher } from './components/household/HouseholdSwitcher';
import './components/household/household.css';
import { ScanAlertHost } from './components/queue/ScanAlertHost';
import { ScanQueuePage } from './components/queue/ScanQueuePage';
import { InventoryPage } from './components/inventory/InventoryPage';
import { ShoppingListPage } from './components/shopping/ShoppingListPage';
import { DiagnosticsPage } from './components/diagnostics/DiagnosticsPage';
import { SettingsPage } from './components/settings/SettingsPage';
import { CategoryPage } from './components/restock/CategoryPage';
import { SuggestionsPage } from './components/restock/SuggestionsPage';
import { GroupDetailPage } from './components/groups/GroupDetailPage';
import { HistoryPage } from './components/history/HistoryPage';
import { LedgerPage } from './components/history/LedgerPage';
import { AppMenu } from './components/shell/AppMenu';
import { ServerNoticeBanner } from './components/shell/ServerNoticeBanner';
import { CredentialsRevisionContext } from './credentialsRefresh';
import { reportRoute } from './telemetry/client';
import './App.css';

function RouteTelemetry() {
  const { pathname } = useLocation();
  useEffect(() => {
    reportRoute(pathname);
  }, [pathname]);
  return null;
}

function SignedInApp() {
  const { pathname } = useLocation();
  const session = useHouseholdSession();
  const [credentialsRevision, setCredentialsRevision] = useState(0);
  const notifyCredentialsChanged = useCallback(() => {
    setCredentialsRevision((current) => current + 1);
  }, []);

  return (
    <CredentialsRevisionContext.Provider value={credentialsRevision}>
      <RouteTelemetry />
      <ScanAlertHost />
      <AppShell
          className="app-shell"
          header={{ height: { base: 112, sm: 56 } }}
          padding={{ base: 'xs', sm: 'md' }}
        >
          <AppShell.Header>
            <div className="app-header">
              <div className="app-header-brand">
                <Title order={3} size="h4" className="app-header-wordmark"><img className="app-header-mark" src="/mark.svg" alt="" width={28} height={28} />{"Stockbot"}</Title>
                <HouseholdSwitcher />
              </div>
              <Group component="nav" aria-label="Sections" gap={0} wrap="nowrap" className="app-nav">
                <NavLink to="/" end className="app-nav-link">Scan Queue</NavLink>
                <NavLink to="/inventory" className="app-nav-link">Inventory</NavLink>
                <NavLink to="/history" className="app-nav-link">History</NavLink>
                <NavLink to="/shopping" className="app-nav-link">Shopping List</NavLink>
              </Group>
              <div className="app-header-menu">
                <AppMenu onCredentialsChanged={notifyCredentialsChanged} />
              </div>
            </div>
          </AppShell.Header>
          <AppShell.Main>
            {pathname !== '/' && <ServerNoticeBanner shell />}
            <div className="page-frame">
              <Routes>
                <Route path="/" element={<ScanQueuePage />} />
                <Route path="/inventory" element={<InventoryPage />} />
                <Route path="/inventory/categories/:id" element={<CategoryPage />} />
                <Route path="/inventory/:itemId/history" element={<HistoryPage />} />
                <Route path="/history" element={<LedgerPage />} />
                <Route path="/shopping" element={<ShoppingListPage />} />
                <Route path="/diagnostics" element={<DiagnosticsPage />} />
                <Route path="/household" element={<HouseholdPage />} />
                <Route path="/devices" element={<DevicesPage />} />
                <Route path="/settings" element={<SettingsPage />} />
                <Route path="/settings/categories" element={<SuggestionsPage />} />
                <Route path="/groups" element={<Navigate to="/inventory?filter=groups" replace />} />
                <Route path="/groups/suggestions" element={<Navigate to="/inventory?review=1" replace />} />
                <Route path="/groups/:id" element={<GroupDetailPage />} />
                <Route path="/groups/:groupId/history" element={<HistoryPage />} />
              </Routes>
            </div>
          </AppShell.Main>
        </AppShell>
    </CredentialsRevisionContext.Provider>
  );
}

function PublicEntry() {
  const { pathname } = useLocation();
  const invite = /^\/invite\/([^/]+)$/.exec(pathname);
  if (pathname === '/setup') return <SetupPage />;
  if (invite) return <InviteAcceptPage code={decodeURIComponent(invite[1])} />;
  const reset = /^\/reset\/([^/]+)$/.exec(pathname);
  if (reset) return <ResetPasswordPage code={decodeURIComponent(reset[1])} />;
  return (
    <HouseholdGate>
      <SignedInApp />
    </HouseholdGate>
  );
}

function App() {
  return (
    <BrowserRouter>
      <PublicEntry />
    </BrowserRouter>
  );
}

export default App
