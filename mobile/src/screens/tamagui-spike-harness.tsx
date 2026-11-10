/** Isolated Tamagui spike screen — not registered on any production route. */
import { TamaguiNoticeBanner, TamaguiSpikeProvider } from '@stockbot/ui';

export function TamaguiSpikeHarnessScreen() {
  return (
    <TamaguiSpikeProvider theme="light">
      <TamaguiNoticeBanner onAction={() => undefined} />
    </TamaguiSpikeProvider>
  );
}
