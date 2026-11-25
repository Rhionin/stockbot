import { Tabs } from 'expo-router';
import { ShelfTabBar } from '@/ui/ShelfTabBar';
import { isWeb, useDesktopLayout } from '@/ui/layout';

const PHONE_TABS = [
  { name: 'index', title: 'Scan' },
  { name: 'queue', title: 'Queue' },
  { name: 'household', title: 'Household' },
];

const WEB_TABS = [
  { name: 'index', title: 'Queue' },
  { name: 'scan', title: 'Scan' },
  { name: 'household', title: 'Household' },
];

const PHONE_HIDDEN = ['scan'];
const WEB_HIDDEN = ['queue'];

export default function AppLayout() {
  const desktop = useDesktopLayout();
  const web = isWeb();
  const tabs = web ? WEB_TABS : PHONE_TABS;
  const hidden = web ? WEB_HIDDEN : PHONE_HIDDEN;
  return (
    <Tabs
      tabBar={(props) => <ShelfTabBar {...props} />}
      screenOptions={{ headerShown: false, tabBarPosition: desktop ? 'top' : 'bottom' }}
    >
      {tabs.map((tab) => (
        <Tabs.Screen key={tab.name} name={tab.name} options={{ title: tab.title }} />
      ))}
      {hidden.map((name) => (
        <Tabs.Screen key={name} name={name} options={{ href: null }} />
      ))}
    </Tabs>
  );
}
