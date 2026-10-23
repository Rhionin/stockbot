import { router } from 'expo-router';
import { StatusBar } from 'expo-status-bar';
import { useSafeAreaInsets } from 'react-native-safe-area-context';
import { View } from 'react-native';
import { AuthSetup } from '@/screens/AuthSetup';
import { ConnectionScreen } from '@/screens/ConnectionForm';
import { useSession } from '@/session';
import { colors } from '@/ui/theme';

export default function SignInRoute() {
  const session = useSession();
  const insets = useSafeAreaInsets();
  if (session.status === 'setup') {
    return (
      <View style={{ flex: 1, backgroundColor: colors.paper }}>
        <StatusBar style="dark" />
        <AuthSetup topInset={insets.top} onRetry={session.recheck} />
      </View>
    );
  }

  const initialError = session.status === 'signedOut' ? session.error : '';
  return (
    <View style={{ flex: 1, backgroundColor: colors.paper }}>
      <StatusBar style="dark" />
      <ConnectionScreen
        topInset={insets.top}
        heading="Stockbot"
        lede="Scan the pantry from the phone."
        submitLabel="Connect"
        initialError={initialError}
        onSubmit={async (serverUrl, password) => {
          await session.signIn(serverUrl, password);
          router.replace('/');
        }}
      />
    </View>
  );
}
