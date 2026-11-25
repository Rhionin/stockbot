import { Pressable, TextInput } from 'react-native';
import { brandTealDeep, mobileChrome } from '@stockbot/shared/brandTokens';
import { Text } from '@tamagui/core';
import { XStack } from '@tamagui/stacks';
import { lineHeightPx } from './lineHeight';

export const NUMBER_STEPPER_TEST_ID = 'number-stepper';

export type NumberStepperProps = {
  value: string;
  label: string;
  decreaseLabel: string;
  increaseLabel: string;
  canDecrease: boolean;
  invalid?: boolean;
  disabled?: boolean;
  fontBold: string;
  testID?: string;
  onChangeText: (text: string) => void;
  onStep: (delta: 1 | -1) => void;
  onCommit: () => void;
};

const BUTTON = 44;

export function NumberStepper({
  value,
  label,
  decreaseLabel,
  increaseLabel,
  canDecrease,
  invalid = false,
  disabled = false,
  fontBold,
  testID = NUMBER_STEPPER_TEST_ID,
  onChangeText,
  onStep,
  onCommit,
}: NumberStepperProps) {
  const edge = invalid ? mobileChrome.danger : mobileChrome.rule;
  return (
    <XStack testID={testID} alignItems="center" borderWidth={1} borderColor={edge} backgroundColor={mobileChrome.white}>
      <StepButton
        glyph={'−'}
        label={decreaseLabel}
        disabled={disabled || !canDecrease}
        fontBold={fontBold}
        onPress={() => onStep(-1)}
      />
      <TextInput
        accessibilityLabel={label}
        accessibilityState={{ disabled }}
        aria-invalid={invalid}
        value={value}
        editable={!disabled}
        keyboardType="number-pad"
        inputMode="numeric"
        maxLength={4}
        selectTextOnFocus
        returnKeyType="done"
        onChangeText={onChangeText}
        onBlur={onCommit}
        onSubmitEditing={onCommit}
        style={{
          width: 56,
          minHeight: BUTTON,
          textAlign: 'center',
          fontFamily: fontBold,
          fontSize: 18,
          color: mobileChrome.ink,
          borderLeftWidth: 1,
          borderRightWidth: 1,
          borderColor: mobileChrome.rule,
          paddingVertical: 0,
          paddingHorizontal: 4,
        }}
      />
      <StepButton glyph="+" label={increaseLabel} disabled={disabled} fontBold={fontBold} onPress={() => onStep(1)} />
    </XStack>
  );
}

function StepButton({
  glyph,
  label,
  disabled,
  fontBold,
  onPress,
}: {
  glyph: string;
  label: string;
  disabled: boolean;
  fontBold: string;
  onPress: () => void;
}) {
  return (
    <Pressable
      accessibilityRole="button"
      accessibilityLabel={label}
      accessibilityState={{ disabled }}
      disabled={disabled}
      onPress={onPress}
      style={{ width: BUTTON, minHeight: BUTTON, alignItems: 'center', justifyContent: 'center' }}
    >
      <Text
        fontFamily={fontBold}
        fontSize={24}
        lineHeight={lineHeightPx(28)}
        color={disabled ? mobileChrome.inkMuted : brandTealDeep}
        opacity={disabled ? 0.5 : 1}
      >
        {glyph}
      </Text>
    </Pressable>
  );
}
