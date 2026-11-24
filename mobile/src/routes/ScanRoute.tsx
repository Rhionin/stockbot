import { ScanScreen } from '@/screens/ScanScreen';
import { useSession } from '@/session';

export function ScanRoute() {
  const session = useSession();
  if (session.status !== 'ready') return null;
  return <ScanScreen key={session.scope} client={session.client} warning={session.warning} />;
}
