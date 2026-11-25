import { useCallback, useState } from 'react';
import { router, useFocusEffect } from 'expo-router';
import { focusScannerInput } from '@/scanner/focusScannerInput';
import { QueueScreen } from '@/screens/QueueScreen';
import { useSession } from '@/session';
import { isWeb } from '@/ui/layout';

export function QueueRoute() {
  const session = useSession();
  const [focused, setFocused] = useState(true);
  useFocusEffect(useCallback(() => {
    setFocused(true);
    return () => setFocused(false);
  }, []));
  if (session.status !== 'ready') return null;
  return <QueueScreen key={session.scope} client={session.client} warning={session.warning} focused={focused} onRescan={() => { if (!focusScannerInput()) router.navigate(isWeb() ? '/scan' : '/'); }} />;
}
