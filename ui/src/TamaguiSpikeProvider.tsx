import type { ReactNode } from 'react';
import { TamaguiProvider, Theme } from 'tamagui';
import { tamaguiConfig } from './tamagui.config';

export function TamaguiSpikeProvider({
  children,
  theme = 'light',
}: {
  children: ReactNode;
  theme?: 'light' | 'dark';
}) {
  return (
    <TamaguiProvider config={tamaguiConfig} defaultTheme={theme}>
      <Theme name={theme}>{children}</Theme>
    </TamaguiProvider>
  );
}
