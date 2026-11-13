import { fireEvent, render, screen } from '@testing-library/react-native';
import { StyleSheet } from 'react-native';
import {
  NOTHING_ON_HAND_BODY,
  NOTHING_ON_HAND_TITLE,
} from '@stockbot/shared/contract';
import {
  brandTeal,
  brandTealDeep,
  mobileChrome,
  nothingOnHandLight,
} from '@stockbot/shared/brandTokens';
import { QueueCard, StockbotTamaguiProvider } from '@stockbot/ui';

function flattened(view: { props: { style?: unknown } }) {
  return StyleSheet.flatten(view.props.style as Parameters<typeof StyleSheet.flatten>[0]);
}

const fonts = {
  regular: 'AtkinsonHyperlegible_400Regular',
  bold: 'AtkinsonHyperlegible_700Bold',
};

describe('QueueCard (Tamagui)', () => {
  it('renders confirm state with master-aligned token styles', async () => {
    const onConfirm = jest.fn();
    await render(
      <StockbotTamaguiProvider>
        <QueueCard
          entryId="scan-1"
          label="Synthetic item"
          barcode="111222333444"
          metaLine="111222333444  Stock in"
          stockIn
          stockOut={false}
          blocked={false}
          busy={false}
          busyThis={false}
          fontRegular={fonts.regular}
          fontBold={fonts.bold}
          onConfirm={onConfirm}
          onRemove={() => undefined}
        />
      </StockbotTamaguiProvider>,
    );

    expect(screen.getByText('Synthetic item')).toBeTruthy();
    const card = screen.getByTestId('scan-scan-1');
    const cardStyle = flattened(card);
    if (!cardStyle) throw new Error('card style missing');
    expect(cardStyle.borderLeftColor).toBe(brandTeal);

    const confirm = screen.getByRole('button', { name: 'Confirm Synthetic item' });
    fireEvent.press(confirm);
    expect(onConfirm).toHaveBeenCalledTimes(1);

    const confirmStyle = flattened(confirm);
    if (!confirmStyle) throw new Error('confirm style missing');
    expect(confirmStyle.backgroundColor).toBe(brandTealDeep);
    expect(confirmStyle.minHeight).toBe(44);
    expect(confirmStyle.minWidth).toBe(88);
  });

  it('renders blocked nothing-on-hand copy from shared contract', async () => {
    const onRemove = jest.fn();
    await render(
      <StockbotTamaguiProvider>
        <QueueCard
          entryId="scan-2"
          label="Synthetic item"
          barcode="555666777888"
          metaLine="555666777888  Stock out"
          stockIn={false}
          stockOut
          blocked
          busy={false}
          busyThis={false}
          fontRegular={fonts.regular}
          fontBold={fonts.bold}
          onConfirm={() => undefined}
          onRemove={onRemove}
        />
      </StockbotTamaguiProvider>,
    );

    expect(screen.getByText(NOTHING_ON_HAND_TITLE)).toBeTruthy();
    expect(screen.getByText(NOTHING_ON_HAND_BODY)).toBeTruthy();

    const card = screen.getByTestId('scan-scan-2');
    const cardStyle = flattened(card);
    if (!cardStyle) throw new Error('card style missing');
    expect(cardStyle.backgroundColor).toBe(nothingOnHandLight.wash);
    expect(cardStyle.borderColor ?? cardStyle.borderTopColor).toBe(nothingOnHandLight.border);

    fireEvent.press(screen.getByRole('button', { name: 'Remove Synthetic item' }));
    expect(onRemove).toHaveBeenCalledTimes(1);

    const removeStyle = flattened(screen.getByRole('button', { name: 'Remove Synthetic item' }));
    if (!removeStyle) throw new Error('remove style missing');
    expect(removeStyle.backgroundColor).toBe(mobileChrome.removeWash);
  });

  it('shows Confirming and Removing only for the matching busy action', async () => {
    const { rerender } = await render(
      <StockbotTamaguiProvider>
        <QueueCard
          entryId="scan-3"
          label="Synthetic item"
          barcode="000111222333"
          metaLine="000111222333  Stock in"
          stockIn
          stockOut={false}
          blocked={false}
          busy
          busyThis
          busyConfirm
          fontRegular={fonts.regular}
          fontBold={fonts.bold}
          onConfirm={() => undefined}
          onRemove={() => undefined}
        />
      </StockbotTamaguiProvider>,
    );
    expect(screen.getByText('Confirming')).toBeTruthy();

    await rerender(
      <StockbotTamaguiProvider>
        <QueueCard
          entryId="scan-4"
          label="Synthetic item"
          barcode="444555666777"
          metaLine="444555666777  Stock out"
          stockIn={false}
          stockOut
          blocked
          busy
          busyThis
          busyRemove
          fontRegular={fonts.regular}
          fontBold={fonts.bold}
          onConfirm={() => undefined}
          onRemove={() => undefined}
        />
      </StockbotTamaguiProvider>,
    );
    expect(screen.getByText('Removing')).toBeTruthy();
    expect(screen.queryByText('Confirming')).toBeNull();
  });
});
