import { useState } from 'react';
import { Text, YStack } from 'tamagui';
import { NOTICE_SCREEN_PADDING_TOP } from '@stockbot/shared/noticeBanner';
import { TamaguiNoticeBanner, TamaguiSpikeProvider } from '@stockbot/ui';

export function TamaguiHarnessPage() {
  const [pressed, setPressed] = useState(0);
  return (
    <TamaguiSpikeProvider theme="light">
      <YStack
        data-testid="tamagui-harness-root"
        padding={NOTICE_SCREEN_PADDING_TOP}
        gap={NOTICE_SCREEN_PADDING_TOP}
        backgroundColor="$pageBg"
        minHeight="100vh"
      >
        <Text fontSize={20} fontWeight="700" color="$color" accessibilityRole="header">
          Tamagui spike harness
        </Text>
        <TamaguiNoticeBanner onAction={() => setPressed((count) => count + 1)} />
        <Text data-testid="tamagui-harness-press-count" color="$color">
          Action presses: {pressed}
        </Text>
      </YStack>
    </TamaguiSpikeProvider>
  );
}
