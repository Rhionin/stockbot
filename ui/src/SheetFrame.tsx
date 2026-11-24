import { useEffect, useRef, type ReactNode } from 'react';
import { KeyboardAvoidingView, Modal, Platform, Pressable, ScrollView, Text as NativeText, View } from 'react-native';
import { mobileChrome } from '@stockbot/shared/brandTokens';
import { Text } from '@tamagui/core';
import { focusOnWeb } from './focusWeb';
import { lineHeightPx } from './lineHeight';

export const SHEET_FRAME_CLOSE_LABEL = 'Close';
export const SHEET_DIALOG_MAX_WIDTH = 560;

export type SheetFrameProps = {
  title: string;
  // True for a centered dialog on a wide web screen. Phones get the full screen.
  dialog: boolean;
  topInset: number;
  bottomInset: number;
  fontBold: string;
  closeDisabled?: boolean;
  contentGap?: number;
  // Pinned under the heading, above the scrolling body. The server notice lives here.
  banner?: ReactNode;
  onClose: () => void;
  children: ReactNode;
};

// A modal with a level 2 heading that takes focus on web whenever its title changes, a Close button and
// Escape (Modal's onRequestClose), over a scrolling body that keeps the
// keyboard off the fields.
export function SheetFrame({
  title,
  dialog,
  topInset,
  bottomInset,
  fontBold,
  closeDisabled = false,
  contentGap = 20,
  banner,
  onClose,
  children,
}: SheetFrameProps) {
  const headingRef = useRef<unknown>(null);

  useEffect(() => {
    const timer = setTimeout(() => focusOnWeb(headingRef.current), 0);
    return () => clearTimeout(timer);
  }, [title]);

  const body = (
    <KeyboardAvoidingView
      style={{ flex: 1 }}
      behavior={Platform.OS === 'ios' ? 'padding' : Platform.OS === 'android' ? 'height' : undefined}
    >
      <View
        style={{
          flexDirection: 'row',
          alignItems: 'center',
          justifyContent: 'space-between',
          gap: 12,
          paddingHorizontal: 20,
          paddingBottom: 8,
          paddingTop: dialog ? 16 : topInset + 8,
          borderBottomWidth: 1,
          borderBottomColor: mobileChrome.rule,
        }}
      >
        <NativeText
          ref={headingRef as never}
          {...{ tabIndex: -1 }}
          accessibilityRole="header"
          aria-level={2}
          style={{ flex: 1, fontFamily: fontBold, fontSize: 28, lineHeight: 34, color: mobileChrome.ink }}
        >
          {title}
        </NativeText>
        <Pressable
          accessibilityRole="button"
          accessibilityLabel={SHEET_FRAME_CLOSE_LABEL}
          accessibilityState={{ disabled: closeDisabled }}
          disabled={closeDisabled}
          onPress={onClose}
          style={{ minHeight: 44, minWidth: 64, alignItems: 'center', justifyContent: 'center', paddingHorizontal: 12 }}
        >
          <Text fontFamily={fontBold} fontSize={16} lineHeight={lineHeightPx(22)} color={mobileChrome.ink}>
            {SHEET_FRAME_CLOSE_LABEL}
          </Text>
        </Pressable>
      </View>
      {banner ? <View style={{ paddingTop: 12 }}>{banner}</View> : null}
      <ScrollView
        style={{ flex: 1 }}
        contentContainerStyle={{ padding: 20, gap: contentGap, paddingBottom: bottomInset + 24 }}
        keyboardShouldPersistTaps="handled"
        keyboardDismissMode="on-drag"
      >
        {children}
      </ScrollView>
    </KeyboardAvoidingView>
  );

  return (
    <Modal
      visible
      transparent
      animationType={Platform.OS === 'web' ? 'none' : 'slide'}
      onRequestClose={onClose}
      {...{ 'aria-label': title }}
    >
      {dialog ? (
        <View
          style={{
            flex: 1,
            alignItems: 'center',
            justifyContent: 'center',
            backgroundColor: mobileChrome.scrim,
            padding: 24,
          }}
        >
          <View
            style={{
              width: '100%',
              maxWidth: SHEET_DIALOG_MAX_WIDTH,
              maxHeight: '100%',
              flexShrink: 1,
              backgroundColor: mobileChrome.paper,
              borderWidth: 1,
              borderColor: mobileChrome.rule,
              overflow: 'hidden',
            }}
          >
            {body}
          </View>
        </View>
      ) : (
        <View style={{ flex: 1, backgroundColor: mobileChrome.paper }}>{body}</View>
      )}
    </Modal>
  );
}
