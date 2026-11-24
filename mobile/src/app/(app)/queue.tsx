import { Redirect } from 'expo-router';
import { QueueRoute } from '@/routes/QueueRoute';
import { isWeb } from '@/ui/layout';

export default function QueueTabRoute() {
  return isWeb() ? <Redirect href="/" /> : <QueueRoute />;
}
