import { StatusBar } from 'expo-status-bar';
import { useSafeAreaInsets } from 'react-native-safe-area-context';
import { View } from 'react-native';
import { ConnectionScreen } from '@/screens/ConnectionForm';
import { useSession } from '@/session';
import { colors } from '@/ui/theme';

export default function HouseholdRoute() {
  const session = useSession();
  const insets = useSafeAreaInsets();
  if (session.status !== 'ready') return null;

  return (
    <View style={{ flex: 1, backgroundColor: colors.paper }}>
      <StatusBar style="dark" />
      <ConnectionScreen
        topInset={insets.top}
        heading="Household"
        lede="This phone keeps the server address and the household password on the device."
        submitLabel="Save"
        initialUrl={session.serverUrl}
        initialPassword={session.password}
        onSubmit={session.signIn}
        onDisconnect={() => { void session.signOut(); }}
      />
    </View>
  );
}
