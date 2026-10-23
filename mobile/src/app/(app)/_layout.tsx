import { Tabs } from 'expo-router';
import { ShelfTabBar } from '@/ui/ShelfTabBar';

export default function AppLayout() {
  return (
    <Tabs
      tabBar={(props) => <ShelfTabBar {...props} />}
      screenOptions={{ headerShown: false }}
    >
      <Tabs.Screen name="index" options={{ title: 'Scan' }} />
      <Tabs.Screen name="queue" options={{ title: 'Queue' }} />
      <Tabs.Screen name="household" options={{ title: 'Household' }} />
    </Tabs>
  );
}
