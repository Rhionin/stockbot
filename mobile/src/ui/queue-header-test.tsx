import { fireEvent, render, screen } from '@testing-library/react-native';
import { StyleSheet } from 'react-native';
import {
  amberDeep,
  amberScale,
  brandTealDeep,
  mobileChrome,
  tealScale,
} from '@stockbot/shared/brandTokens';
import { SCANNER_CONNECTED, SCANNER_DISCONNECTED } from '@stockbot/shared/queueHeader';
import { assertTokenPaletteValue, DirectionHint, DirectionTabs, ScannerStatus, StockbotTamaguiProvider } from '@stockbot/ui';

const fonts = { regular: 'AtkinsonHyperlegible_400Regular', bold: 'AtkinsonHyperlegible_700Bold' };

function flat(node: { props: { style?: unknown } }) {
  return StyleSheet.flatten(node.props.style as Parameters<typeof StyleSheet.flatten>[0]) ?? {};
}

function tabs(value: 'stock_in' | 'stock_out', counts: [number, number], onChange = jest.fn()) {
  return render(
    <StockbotTamaguiProvider>
      <DirectionTabs
        label="Scan direction"
        fontBold={fonts.bold}
        value={value}
        onChange={onChange}
        tabs={[
          { value: 'stock_in', label: 'Stock in', count: counts[0] },
          { value: 'stock_out', label: 'Stock out', count: counts[1] },
        ]}
      />
    </StockbotTamaguiProvider>,
  );
}

describe('DirectionTabs', () => {
  it('is a tablist whose tabs carry selection and the count in their names', async () => {
    await tabs('stock_in', [2, 0]);

    expect(screen.getByLabelText('Scan direction').props.role).toBe('tablist');
    const active = screen.getByRole('tab', { name: 'Stock in 2 to review' });
    expect(active.props.accessibilityState).toEqual({ selected: true });
    const idle = screen.getByRole('tab', { name: 'Stock out' });
    expect(idle.props.accessibilityState).toEqual({ selected: false });
  });

  it('paints the selected stock in tab teal and the badge from tokens', async () => {
    await tabs('stock_in', [2, 5]);

    const active = flat(screen.getByTestId('direction-tab-stock_in'));
    expect(active.backgroundColor).toBe(tealScale[0]);
    expect(active.borderBottomColor).toBe(brandTealDeep);
    expect(active.borderBottomWidth).toBe(4);
    expect(active.minHeight).toBe(48);
    expect(flat(screen.getByTestId('direction-tab-stock_in-count', { includeHiddenElements: true })).backgroundColor).toBe(brandTealDeep);

    const idle = flat(screen.getByTestId('direction-tab-stock_out'));
    expect(idle.backgroundColor).toBe('transparent');
    expect(idle.borderBottomColor).toBe(mobileChrome.rule);
    expect(flat(screen.getByTestId('direction-tab-stock_out-count', { includeHiddenElements: true })).backgroundColor).toBe(mobileChrome.rule);
  });

  it('paints the selected stock out tab amber', async () => {
    await tabs('stock_out', [0, 5]);

    const active = flat(screen.getByTestId('direction-tab-stock_out'));
    expect(active.backgroundColor).toBe(amberScale[0]);
    expect(active.borderBottomColor).toBe(amberDeep);
  });

  it('leaves the badge out when nothing is waiting and hides it from assistive tech when shown', async () => {
    await tabs('stock_in', [0, 120]);

    expect(screen.queryByTestId('direction-tab-stock_in-count', { includeHiddenElements: true })).toBeNull();
    const badge = screen.getByTestId('direction-tab-stock_out-badge', { includeHiddenElements: true });
    expect(badge.props.importantForAccessibility).toBe('no-hide-descendants');
    expect(screen.getByText('99+', { includeHiddenElements: true })).toBeTruthy();
    expect(screen.getByRole('tab', { name: 'Stock out 99+ to review' })).toBeTruthy();
  });

  it('reports the tab that was pressed', async () => {
    const onChange = jest.fn();
    await tabs('stock_in', [1, 1], onChange);

    await fireEvent.press(screen.getByTestId('direction-tab-stock_out'));

    expect(onChange).toHaveBeenCalledWith('stock_out');
  });

  it('only uses token colors', () => {
    for (const color of [tealScale[0], tealScale[9], amberScale[0], amberScale[9], brandTealDeep, amberDeep, mobileChrome.rule]) {
      expect(() => assertTokenPaletteValue('colors', color)).not.toThrow();
    }
  });
});

describe('DirectionHint and ScannerStatus', () => {
  it('colors the hint by direction and announces it politely', async () => {
    await render(
      <StockbotTamaguiProvider>
        <DirectionHint mode="stock_out" fontBold={fonts.bold} />
      </StockbotTamaguiProvider>,
    );

    const hint = screen.getByText('Scans remove from stock');
    expect(hint.props.role).toBe('status');
    expect(hint.props.accessibilityLiveRegion).toBe('polite');
    expect(flat(hint).color).toBe(amberDeep);
  });

  it.each([
    { connected: true, text: SCANNER_CONNECTED, color: brandTealDeep },
    { connected: false, text: SCANNER_DISCONNECTED, color: mobileChrome.danger },
  ])('shows $text in a token color as a live status', async ({ connected, text, color }) => {
    await render(
      <StockbotTamaguiProvider>
        <ScannerStatus connected={connected} fontRegular={fonts.regular} />
      </StockbotTamaguiProvider>,
    );

    const status = screen.getByText(text);
    expect(status.props.role).toBe('status');
    expect(flat(status).color).toBe(color);
  });

  it('renders nothing before the scanner state is known', async () => {
    await render(
      <StockbotTamaguiProvider>
        <ScannerStatus connected={null} fontRegular={fonts.regular} />
      </StockbotTamaguiProvider>,
    );

    expect(screen.queryByTestId('scanner-status')).toBeNull();
  });
});
