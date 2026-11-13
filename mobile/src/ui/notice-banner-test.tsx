import { render, screen } from '@testing-library/react-native';
import { StyleSheet } from 'react-native';
import { OFFLINE_SENTENCE } from '@stockbot/shared/contract';
import { NoticeBanner, NOTICE_BANNER_TEST_ID, StockbotTamaguiProvider } from '@stockbot/ui';

function flattened(view: { props: { style?: unknown } }) {
  return StyleSheet.flatten(view.props.style as Parameters<typeof StyleSheet.flatten>[0]);
}

function expectRenderedBorder(flat: Record<string, unknown>, edge: string, width = 1, radius = 4) {
  const color = flat.borderColor ?? flat.borderTopColor;
  expect(color).toBe(edge);
  if (flat.borderColor !== undefined) {
    expect(flat.borderWidth).toBe(width);
    expect(flat.borderRadius).toBe(radius);
    return;
  }
  expect(flat.borderTopColor).toBe(edge);
  expect(flat.borderRightColor).toBe(edge);
  expect(flat.borderBottomColor).toBe(edge);
  expect(flat.borderLeftColor).toBe(edge);
  expect(flat.borderTopWidth ?? flat.borderWidth).toBe(width);
  expect(flat.borderTopLeftRadius ?? flat.borderRadius).toBe(radius);
}

describe('NoticeBanner (Tamagui)', () => {
  it('renders master notice colors and typography on the light phone shell', async () => {
    await render(
      <StockbotTamaguiProvider>
        <NoticeBanner text={OFFLINE_SENTENCE} fontRegular="AtkinsonHyperlegible_400Regular" fontBold="AtkinsonHyperlegible_700Bold" />
      </StockbotTamaguiProvider>,
    );
    expect(screen.getByText(OFFLINE_SENTENCE)).toBeTruthy();

    const bannerStyle = flattened(screen.getByTestId(NOTICE_BANNER_TEST_ID));
    if (!bannerStyle) throw new Error('banner style missing');
    expect(bannerStyle.backgroundColor).toBe('#ffeeee');
    expectRenderedBorder(bannerStyle as Record<string, unknown>, '#EF4444');

    const messageStyle = flattened(screen.getByText(OFFLINE_SENTENCE));
    if (!messageStyle) throw new Error('message style missing');
    expect(messageStyle.color).toBe('#000000');
    expect(messageStyle.fontSize).toBe(16);
  });
});
