import { Pressable } from 'react-native';
import { mobileChrome, tealScale } from '@stockbot/shared/brandTokens';
import { Text } from '@tamagui/core';
import { XStack } from '@tamagui/stacks';
import { lineHeightPx } from './lineHeight';

export const UNDO_TOAST_TEST_ID = 'confirm-toast';

export type UndoToastViewProps = {
  message: string;
  actionLabel?: string;
  bottom: number;
  fontRegular: string;
  fontBold: string;
  testID?: string;
  onAction?: () => void;
};

// Bottom snackbar. The action is optional so one component carries plain
// confirmations and the ones that offer Undo.
export function UndoToastView({
  message,
  actionLabel,
  bottom,
  fontRegular,
  fontBold,
  testID = UNDO_TOAST_TEST_ID,
  onAction,
}: UndoToastViewProps) {
  return (
    <XStack
      testID={testID}
      accessibilityLiveRegion="polite"
      role="status"
      position="absolute"
      left={20}
      right={20}
      bottom={bottom}
      alignItems="center"
      gap={12}
      backgroundColor={mobileChrome.ink}
      paddingLeft={16}
      paddingRight={actionLabel ? 4 : 16}
      paddingVertical={actionLabel ? 4 : 14}
    >
      <Text flex={1} fontFamily={fontRegular} fontSize={16} lineHeight={lineHeightPx(22)} color={mobileChrome.white}>
        {message}
      </Text>
      {actionLabel && onAction ? (
        <Pressable
          accessibilityRole="button"
          accessibilityLabel={actionLabel}
          onPress={onAction}
          style={{ minHeight: 44, minWidth: 64, alignItems: 'center', justifyContent: 'center', paddingHorizontal: 12 }}
        >
          <Text fontFamily={fontBold} fontSize={16} lineHeight={lineHeightPx(22)} color={tealScale[3]}>
            {actionLabel}
          </Text>
        </Pressable>
      ) : null}
    </XStack>
  );
}
