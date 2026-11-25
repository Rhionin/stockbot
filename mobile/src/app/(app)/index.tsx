import { QueueRoute } from '@/routes/QueueRoute';
import { ScanRoute } from '@/routes/ScanRoute';
import { isWeb } from '@/ui/layout';

export default function LandingRoute() {
  return isWeb() ? <QueueRoute /> : <ScanRoute />;
}
