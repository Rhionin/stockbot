import { fireEvent, render, screen } from '@testing-library/react-native';
import { StyleSheet } from 'react-native';
import { OFFLINE_SENTENCE } from '@stockbot/shared/contract';
import {
  NOTICE_BANNER_EDGE,
  NOTICE_BANNER_RADIUS,
  NOTICE_BANNER_TEXT,
  NOTICE_BANNER_TINT,
} from '@stockbot/shared/noticeBanner';
import { TamaguiNoticeBanner, TamaguiSpikeProvider, TAMAGUI_NOTICE_TEST_ID } from '@stockbot/ui';

function flattened(view: { props: { style?: unknown } }) {
  return StyleSheet.flatten(view.props.style as Parameters<typeof StyleSheet.flatten>[0]);
}

describe('tamagui notice banner (Jest)', () => {
  it('renders real Tamagui with shared notice token values and handles press', async () => {
    const onAction = jest.fn();
    await render(
      <TamaguiSpikeProvider theme="light">
        <TamaguiNoticeBanner onAction={onAction} />
      </TamaguiSpikeProvider>,
    );
    expect(screen.getByText(OFFLINE_SENTENCE)).toBeTruthy();

    const bannerStyle = flattened(screen.getByTestId(TAMAGUI_NOTICE_TEST_ID));
    expect(bannerStyle).toBeTruthy();
    if (!bannerStyle) throw new Error('banner style missing');
    expect(bannerStyle.backgroundColor).toBe(NOTICE_BANNER_TINT);
    expect(bannerStyle.borderTopColor).toBe(NOTICE_BANNER_EDGE);
    expect(bannerStyle.borderTopLeftRadius).toBe(NOTICE_BANNER_RADIUS);

    const messageStyle = flattened(screen.getByText(OFFLINE_SENTENCE));
    expect(messageStyle).toBeTruthy();
    if (!messageStyle) throw new Error('message style missing');
    expect(messageStyle.color).toBe(NOTICE_BANNER_TEXT);

    fireEvent.press(screen.getByRole('button', { name: 'Try again' }));
    expect(onAction).toHaveBeenCalledTimes(1);
  });
});
