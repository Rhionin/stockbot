import { useEffect } from 'react';
import { StatusBar } from 'expo-status-bar';
import { useSafeAreaInsets } from 'react-native-safe-area-context';
import { HouseholdScreen } from '@/screens/HouseholdScreen';
import { useSession } from '@/session';

export default function HouseholdRoute() {
  const session = useSession();
  const insets = useSafeAreaInsets();
  const { refreshAccount } = session;
  const missingAccount = session.status === 'ready' && session.account === null;
  useEffect(() => {
    if (missingAccount) void refreshAccount().catch(() => undefined);
  }, [missingAccount, refreshAccount]);
  if (session.status !== 'ready') return null;

  return (
    <>
      <StatusBar style="dark" />
      <HouseholdScreen
        topInset={insets.top}
        serverUrl={session.serverUrl}
        account={session.account}
        onSwitch={session.switchHousehold}
        onSignOut={() => { void session.signOut(); }}
      />
    </>
  );
}
