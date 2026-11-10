import { fireEvent, render, screen } from '@testing-library/react-native';
import { OFFLINE_SENTENCE } from '@stockbot/shared/contract';
import {
  NOTICE_BANNER_EDGE,
  NOTICE_BANNER_RADIUS,
  NOTICE_BANNER_TEXT,
  NOTICE_BANNER_TINT,
} from '@stockbot/shared/noticeBanner';
import { TamaguiNoticeBanner, TamaguiSpikeProvider, TAMAGUI_NOTICE_TEST_ID } from '@stockbot/ui';

describe('tamagui notice banner (Jest)', () => {
  it('renders the offline sentence and fires the action button', async () => {
    const onAction = jest.fn();
    await render(
      <TamaguiSpikeProvider theme="light">
        <TamaguiNoticeBanner onAction={onAction} />
      </TamaguiSpikeProvider>,
    );
    expect(screen.getByText(OFFLINE_SENTENCE)).toBeTruthy();
    const banner = screen.getByTestId(TAMAGUI_NOTICE_TEST_ID);
    expect(banner.props.style).toEqual(
      expect.arrayContaining([
        expect.objectContaining({
          backgroundColor: NOTICE_BANNER_TINT,
          borderColor: NOTICE_BANNER_EDGE,
          borderRadius: NOTICE_BANNER_RADIUS,
        }),
      ]),
    );
    expect(screen.getByText(OFFLINE_SENTENCE).props.style).toEqual(
      expect.arrayContaining([expect.objectContaining({ color: NOTICE_BANNER_TEXT })]),
    );
    fireEvent.press(screen.getByRole('button', { name: 'Try again' }));
    expect(onAction).toHaveBeenCalledTimes(1);
  });
});
