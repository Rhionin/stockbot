import { useCallback, useState } from 'react';
import { useFocusEffect } from 'expo-router';
import { QueueScreen } from '@/screens/QueueScreen';
import { useSession } from '@/session';

export default function QueueRoute() {
  const session = useSession();
  const [focused, setFocused] = useState(true);
  useFocusEffect(useCallback(() => {
    setFocused(true);
    return () => setFocused(false);
  }, []));
  if (session.status !== 'ready') return null;
  return <QueueScreen client={session.client} warning={session.warning} focused={focused} />;
}
