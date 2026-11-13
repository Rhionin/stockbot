import type { ReactElement } from 'react';
import { render } from '@testing-library/react-native';
import { SafeAreaProvider } from 'react-native-safe-area-context';
import { StockbotTamaguiProvider } from '@stockbot/ui';

export function renderScreen(ui: ReactElement) {
  return render(
    <StockbotTamaguiProvider>
      <SafeAreaProvider
        initialMetrics={{
          insets: { top: 0, right: 0, bottom: 0, left: 0 },
          frame: { x: 0, y: 0, width: 390, height: 844 },
        }}
      >
        {ui}
      </SafeAreaProvider>
    </StockbotTamaguiProvider>,
  );
}
