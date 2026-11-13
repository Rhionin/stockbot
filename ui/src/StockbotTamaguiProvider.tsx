import type { ReactNode } from 'react';
import { TamaguiProvider, Theme } from '@tamagui/core';
import { tamaguiConfig } from './tamagui.config';

export function StockbotTamaguiProvider({ children }: { children: ReactNode }) {
  return (
    <TamaguiProvider config={tamaguiConfig} defaultTheme="light">
      <Theme name="light">{children}</Theme>
    </TamaguiProvider>
  );
}
