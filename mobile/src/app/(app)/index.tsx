import { ScanScreen } from '@/screens/ScanScreen';
import { useSession } from '@/session';

export default function ScanRoute() {
  const session = useSession();
  if (session.status !== 'ready') return null;
  return <ScanScreen client={session.client} warning={session.warning} />;
}
