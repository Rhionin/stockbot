import { fireEvent, render, screen } from '@testing-library/react-native';
import { StyleSheet } from 'react-native';
import { OFFLINE_SENTENCE } from '@stockbot/shared/contract';
import {
  NOTICE_BANNER_EDGE,
  NOTICE_BANNER_RADIUS,
  NOTICE_BANNER_TEXT,
  NOTICE_BANNER_TINT,
} from '@stockbot/shared/noticeBanner';
import {
  NoticeBanner,
  NOTICE_BANNER_TEST_ID,
  NOTICE_DEMO_ACTION_LABEL,
  StockbotTamaguiProvider,
} from '@stockbot/ui';

function flattened(view: { props: { style?: unknown } }) {
  return StyleSheet.flatten(view.props.style as Parameters<typeof StyleSheet.flatten>[0]);
}

describe('NoticeBanner (Tamagui)', () => {
  it.each(['light', 'dark'] as const)('renders shared notice token styles in %s theme', async (theme) => {
    const onDemoAction = jest.fn();
    await render(
      <StockbotTamaguiProvider theme={theme}>
        <NoticeBanner text={OFFLINE_SENTENCE} onDemoAction={onDemoAction} />
      </StockbotTamaguiProvider>,
    );
    expect(screen.getByText(OFFLINE_SENTENCE)).toBeTruthy();

    const bannerStyle = flattened(screen.getByTestId(NOTICE_BANNER_TEST_ID));
    expect(bannerStyle).toBeTruthy();
    if (!bannerStyle) throw new Error('banner style missing');
    expect(bannerStyle.backgroundColor).toBe(NOTICE_BANNER_TINT);
    expect(bannerStyle.borderTopColor ?? bannerStyle.borderColor).toBe(NOTICE_BANNER_EDGE);
    expect(bannerStyle.borderTopLeftRadius ?? bannerStyle.borderRadius).toBe(NOTICE_BANNER_RADIUS);

    const messageStyle = flattened(screen.getByText(OFFLINE_SENTENCE));
    expect(messageStyle).toBeTruthy();
    if (!messageStyle) throw new Error('message style missing');
    expect(messageStyle.color).toBe(NOTICE_BANNER_TEXT);

    fireEvent.press(screen.getByRole('button', { name: NOTICE_DEMO_ACTION_LABEL }));
    expect(onDemoAction).toHaveBeenCalledTimes(1);
  });
});
