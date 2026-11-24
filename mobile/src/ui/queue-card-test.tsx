import { fireEvent, render, screen } from '@testing-library/react-native';
import { StyleSheet } from 'react-native';
import {
  NOTHING_ON_HAND_BODY,
  NOTHING_ON_HAND_TITLE,
} from '@stockbot/shared/contract';
import {
  amberDeep,
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

describe('QueueCard for a scan that needs a name', () => {
  const props = {
    entryId: 'scan-9',
    label: '0049000012345',
    barcode: '0049000012345',
    metaLine: 'Needs a name  Putting away',
    stockIn: true,
    stockOut: false,
    blocked: false,
    busy: false,
    busyThis: false,
    fontRegular: fonts.regular,
    fontBold: fonts.bold,
    needsName: true,
  };

  it('puts a name button where Confirm would be, keeps Remove and takes the amber rule', async () => {
    const onName = jest.fn();
    const onRemove = jest.fn();
    await render(
      <StockbotTamaguiProvider>
        <QueueCard {...props} onName={onName} onRemove={onRemove} onConfirm={() => undefined} />
      </StockbotTamaguiProvider>,
    );

    expect(screen.queryByLabelText('Confirm 0049000012345')).toBeNull();
    await fireEvent.press(screen.getByRole('button', { name: 'What is it? 0049000012345' }));
    await fireEvent.press(screen.getByRole('button', { name: 'Remove 0049000012345' }));
    expect(onName).toHaveBeenCalledTimes(1);
    expect(onRemove).toHaveBeenCalledTimes(1);
    expect(flattened(screen.getByTestId('scan-scan-9'))?.borderLeftColor).toBe(amberDeep);
  });

  it('disables both buttons while another card is busy', async () => {
    const onName = jest.fn();
    await render(
      <StockbotTamaguiProvider>
        <QueueCard {...props} busy onName={onName} onRemove={() => undefined} onConfirm={() => undefined} />
      </StockbotTamaguiProvider>,
    );

    await fireEvent.press(screen.getByRole('button', { name: 'What is it? 0049000012345' }));
    expect(onName).not.toHaveBeenCalled();
  });

  it('washes the card that was just scanned and keeps the amber wash for one with nothing on hand', async () => {
    const props = {
      label: 'Synthetic item',
      barcode: '111222333444',
      metaLine: '111222333444  Stock in',
      stockIn: true,
      stockOut: false,
      busy: false,
      busyThis: false,
      fontRegular: fonts.regular,
      fontBold: fonts.bold,
      onConfirm: () => undefined,
      onRemove: () => undefined,
    };
    await render(
      <StockbotTamaguiProvider>
        <QueueCard {...props} entryId="a" blocked={false} justCaptured />
        <QueueCard {...props} entryId="b" blocked={false} />
        <QueueCard {...props} entryId="c" blocked justCaptured />
      </StockbotTamaguiProvider>,
    );

    expect(flattened(screen.getByTestId('scan-a'))?.backgroundColor).toBe(mobileChrome.successWash);
    expect(flattened(screen.getByTestId('scan-b'))?.backgroundColor).toBeUndefined();
    expect(flattened(screen.getByTestId('scan-c'))?.backgroundColor).toBe(nothingOnHandLight.wash);
  });
});
