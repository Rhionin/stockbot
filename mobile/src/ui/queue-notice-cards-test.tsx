import { fireEvent, render, screen, within } from '@testing-library/react-native';
import { StyleSheet } from 'react-native';
import { brandTealDeep, mobileChrome } from '@stockbot/shared/brandTokens';
import { ProcessingCard, QrCard, StockbotTamaguiProvider } from '@stockbot/ui';

const fonts = { regular: 'AtkinsonHyperlegible_400Regular', bold: 'AtkinsonHyperlegible_700Bold' };

function flattened(view: { props: { style?: unknown } }): Record<string, unknown> {
  return (StyleSheet.flatten(view.props.style as Parameters<typeof StyleSheet.flatten>[0]) ?? {}) as Record<string, unknown>;
}

describe('QrCard', () => {
  const base = {
    entryId: 'qr-1',
    preview: 'example.com',
    dismissing: false,
    error: '',
    fontRegular: fonts.regular,
    fontBold: fonts.bold,
    onRescan: () => undefined,
    onDismiss: () => undefined,
  };

  it('shows the title and preview, and fires both actions', async () => {
    const onRescan = jest.fn();
    const onDismiss = jest.fn();
    await render(
      <StockbotTamaguiProvider>
        <QrCard {...base} onRescan={onRescan} onDismiss={onDismiss} />
      </StockbotTamaguiProvider>,
    );

    expect(screen.getByText('This was a QR code, not the barcode')).toBeTruthy();
    expect(screen.getByText('example.com')).toBeTruthy();
    expect(screen.getByLabelText('This was a QR code, not the barcode. example.com')).toBeTruthy();
    await fireEvent.press(screen.getByRole('button', { name: 'Rescan the barcode' }));
    await fireEvent.press(screen.getByRole('button', { name: 'Dismiss' }));
    expect(onRescan).toHaveBeenCalledTimes(1);
    expect(onDismiss).toHaveBeenCalledTimes(1);
  });

  it('draws all four sides of its border from per-side props', async () => {
    await render(
      <StockbotTamaguiProvider>
        <QrCard {...base} />
      </StockbotTamaguiProvider>,
    );
    const style = flattened(screen.getByTestId('qr-scan-qr-1'));
    expect(style.borderLeftWidth).toBe(1);
    expect(style.borderTopWidth).toBe(1);
    expect(style.borderRightWidth).toBe(1);
    expect(style.borderBottomWidth).toBe(1);
    expect(style.borderLeftColor).toBe(mobileChrome.rule);
    expect(style.borderWidth).toBeUndefined();
  });

  it('omits the preview line when the payload was empty, and names the card by its title alone', async () => {
    await render(
      <StockbotTamaguiProvider>
        <QrCard {...base} preview="" />
      </StockbotTamaguiProvider>,
    );
    expect(screen.getByLabelText('This was a QR code, not the barcode')).toBeTruthy();
    expect(screen.queryByText('example.com')).toBeNull();
  });

  it('disables rescan and marks dismiss busy while dismissing', async () => {
    await render(
      <StockbotTamaguiProvider>
        <QrCard {...base} dismissing />
      </StockbotTamaguiProvider>,
    );
    expect(screen.getByRole('button', { name: 'Rescan the barcode' }).props.accessibilityState).toMatchObject({ disabled: true });
    expect(screen.getByRole('button', { name: 'Dismiss' }).props.accessibilityState).toMatchObject({ busy: true });
  });

  it('shows a dismiss failure as an alert', async () => {
    await render(
      <StockbotTamaguiProvider>
        <QrCard {...base} error="Couldn't dismiss that scan." />
      </StockbotTamaguiProvider>,
    );
    expect(within(screen.getByTestId('qr-scan-qr-1')).getByRole('alert')).toBeTruthy();
    expect(screen.getByText("Couldn't dismiss that scan.")).toBeTruthy();
  });

  it('keeps the rescan action on the primary action color', async () => {
    await render(
      <StockbotTamaguiProvider>
        <QrCard {...base} />
      </StockbotTamaguiProvider>,
    );
    expect(flattened(screen.getByRole('button', { name: 'Rescan the barcode' })).backgroundColor).toBe(brandTealDeep);
  });
});

describe('ProcessingCard', () => {
  it('shows the lookup copy inside a polite status region and marks itself busy', async () => {
    await render(
      <StockbotTamaguiProvider>
        <ProcessingCard noticeId="lookup-1" barcode="999888777666" fontRegular={fonts.regular} fontBold={fonts.bold} />
      </StockbotTamaguiProvider>,
    );
    const card = screen.getByTestId('processing-lookup-1');
    expect(card.props['aria-busy']).toBe(true);
    expect(screen.getByLabelText('Scan 999888777666 processing')).toBeTruthy();
    const status = screen.getByTestId('processing-status-lookup-1');
    expect(status.props.role).toBe('status');
    expect(status.props.accessibilityLiveRegion).toBe('polite');
    expect(within(status).getByText('Looking up product')).toBeTruthy();
    expect(within(status).getByText('Barcode: 999888777666')).toBeTruthy();
    expect(within(status).getByText('Checking the open food database…')).toBeTruthy();
  });
});

describe('just-captured washes', () => {
  it('washes a QR card and a processing card, and a plain one stays clear', async () => {
    await render(
      <StockbotTamaguiProvider>
        <QrCard
          entryId="qr-1"
          preview="example.com"
          justCaptured
          dismissing={false}
          error=""
          fontRegular={fonts.regular}
          fontBold={fonts.bold}
          onRescan={() => undefined}
          onDismiss={() => undefined}
        />
        <ProcessingCard noticeId="lookup-1" barcode="999888777666" justCaptured fontRegular={fonts.regular} fontBold={fonts.bold} />
        <ProcessingCard noticeId="lookup-2" barcode="111" fontRegular={fonts.regular} fontBold={fonts.bold} />
      </StockbotTamaguiProvider>,
    );

    for (const id of ['qr-scan-qr-1', 'processing-lookup-1']) {
      const style = flattened(screen.getByTestId(id));
      expect(style.backgroundColor).toBe(mobileChrome.successWash);
      expect(style.borderLeftColor).toBe(brandTealDeep);
      expect(style.borderBottomColor).toBe(brandTealDeep);
    }
    const plain = flattened(screen.getByTestId('processing-lookup-2'));
    expect(plain.backgroundColor).toBeUndefined();
    expect(plain.borderLeftColor).toBe(mobileChrome.rule);
  });
});
