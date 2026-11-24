import { router } from 'expo-router';
import { StatusBar } from 'expo-status-bar';
import { useSafeAreaInsets } from 'react-native-safe-area-context';
import { View } from 'react-native';
import { AuthSetup } from '@/screens/AuthSetup';
import { UpgradeRequired } from '@/screens/UpgradeRequired';
import { ConnectionScreen } from '@/screens/ConnectionForm';
import { useSession } from '@/session';
import { colors } from '@/ui/theme';

export default function SignInRoute() {
  const session = useSession();
  const insets = useSafeAreaInsets();
  if (session.status === 'setup') {
    return (
      <View role="main" style={{ flex: 1, backgroundColor: colors.paper }}>
        <StatusBar style="dark" />
        <AuthSetup topInset={insets.top} onRetry={session.recheck} />
      </View>
    );
  }

  if (session.status === 'upgradeRequired') {
    return (
      <View role="main" style={{ flex: 1, backgroundColor: colors.paper }}>
        <StatusBar style="dark" />
        <UpgradeRequired topInset={insets.top} />
      </View>
    );
  }

  const initialError = session.status === 'signedOut' ? session.error : '';
  return (
    <View role="main" style={{ flex: 1, backgroundColor: colors.paper }}>
      <StatusBar style="dark" />
      <ConnectionScreen
        topInset={insets.top}
        heading="Stockbot"
        lede="Scan the pantry from the phone."
        submitLabel="Sign in"
        initialUrl={session.status === 'signedOut' ? session.serverUrl : undefined}
        initialError={initialError}
        onSubmit={async (serverUrl, email, password) => {
          await session.signIn(serverUrl, email, password);
          router.replace('/');
        }}
      />
    </View>
  );
}
