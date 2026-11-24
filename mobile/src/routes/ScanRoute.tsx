import { useCallback, useState } from 'react';
import { useFocusEffect } from 'expo-router';
import { ScanScreen } from '@/screens/ScanScreen';
import { useSession } from '@/session';

export function ScanRoute() {
  const session = useSession();
  const [focused, setFocused] = useState(true);
  useFocusEffect(useCallback(() => {
    setFocused(true);
    return () => setFocused(false);
  }, []));
  if (session.status !== 'ready') return null;
  return <ScanScreen key={session.scope} client={session.client} warning={session.warning} focused={focused} />;
}
