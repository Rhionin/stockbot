import { useEffect } from 'react';
import { SplashScreen, Stack } from 'expo-router';
import { useFonts, AtkinsonHyperlegible_400Regular, AtkinsonHyperlegible_700Bold } from '@expo-google-fonts/atkinson-hyperlegible';
import { SessionProvider, useSession } from '@/session';

SplashScreen.preventAutoHideAsync();

export default function Root() {
  return (
    <SessionProvider>
      <SplashGate />
    </SessionProvider>
  );
}

function SplashGate() {
  const session = useSession();
  const [fontsLoaded, fontError] = useFonts({
    AtkinsonHyperlegible_400Regular,
    AtkinsonHyperlegible_700Bold,
  });
  const ready = (fontsLoaded || fontError != null) && session.status !== 'loading';

  useEffect(() => {
    if (ready) void SplashScreen.hideAsync();
  }, [ready]);

  if (!ready) return null;
  return <RootNavigator />;
}

function RootNavigator() {
  const session = useSession();
  const signedIn = session.status === 'ready';
  return (
    <Stack screenOptions={{ headerShown: false }}>
      <Stack.Protected guard={signedIn}>
        <Stack.Screen name="(app)" />
      </Stack.Protected>
      <Stack.Protected guard={!signedIn}>
        <Stack.Screen name="sign-in" />
      </Stack.Protected>
    </Stack>
  );
}
