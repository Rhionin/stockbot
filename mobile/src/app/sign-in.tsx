import { router } from 'expo-router';
import { StatusBar } from 'expo-status-bar';
import { useSafeAreaInsets } from 'react-native-safe-area-context';
import { View } from 'react-native';
import { ConnectionForm } from '@/screens/ConnectionForm';
import { useSession } from '@/session';
import { colors } from '@/ui/theme';

export default function SignInRoute() {
  const session = useSession();
  const insets = useSafeAreaInsets();
  const initialError = session.status === 'signedOut' ? session.error : '';

  return (
    <View style={{ flex: 1, backgroundColor: colors.paper, paddingTop: insets.top }}>
      <StatusBar style="dark" />
      <ConnectionForm
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
