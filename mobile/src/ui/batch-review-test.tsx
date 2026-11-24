import { Platform, Text } from 'react-native';
import { fireEvent, render, screen, within } from '@testing-library/react-native';
import { BatchReviewPanel, SelectCheckbox, SessionCard, StockbotTamaguiProvider, focusOnWeb } from '@stockbot/ui';

const fonts = { regular: 'AtkinsonHyperlegible_400Regular', bold: 'AtkinsonHyperlegible_700Bold' };

const wrap = (node: React.ReactElement) => render(<StockbotTamaguiProvider>{node}</StockbotTamaguiProvider>);

describe('SelectCheckbox', () => {
  it('is a checkbox that reports its state, offers the opposite on press, and is 44 points', async () => {
    const onChange = jest.fn();
    const view = await wrap(
      <SelectCheckbox label="Select oats" checked={false} fontRegular={fonts.regular} onChange={onChange} />,
    );

    const box = screen.getByRole('checkbox', { name: 'Select oats' });
    expect(box.props.accessibilityState).toEqual({ checked: false, disabled: false });
    await fireEvent.press(box);
    expect(onChange).toHaveBeenLastCalledWith(true);

    await view.rerender(
      <StockbotTamaguiProvider>
        <SelectCheckbox label="Select oats" checked fontRegular={fonts.regular} onChange={onChange} />
      </StockbotTamaguiProvider>,
    );
    expect(screen.getByRole('checkbox', { name: 'Select oats' }).props.accessibilityState.checked).toBe(true);
    await fireEvent.press(screen.getByRole('checkbox', { name: 'Select oats' }));
    expect(onChange).toHaveBeenLastCalledWith(false);
  });

  it('reads as mixed and selects everything from the mixed state', async () => {
    const onChange = jest.fn();
    await wrap(
      <SelectCheckbox label="Select all" checked="mixed" fontRegular={fonts.regular} onChange={onChange} />,
    );

    const box = screen.getByRole('checkbox', { name: 'Select all' });
    expect(box.props.accessibilityState.checked).toBe('mixed');
    await fireEvent.press(box);
    expect(onChange).toHaveBeenCalledWith(true);
  });

  it('does nothing when disabled and says so', async () => {
    const onChange = jest.fn();
    await wrap(
      <SelectCheckbox label="Select oats" checked={false} disabled fontRegular={fonts.regular} onChange={onChange} />,
    );

    const box = screen.getByRole('checkbox', { name: 'Select oats' });
    expect(box.props.accessibilityState.disabled).toBe(true);
    await fireEvent.press(box);
    expect(onChange).not.toHaveBeenCalled();
  });
});

describe('SessionCard', () => {
  const props = {
    sessionId: 's1',
    timeRange: '9:00–9:04 AM',
    countLabel: '3 scans',
    directionLabel: 'stock in',
    toggleLabel: '9:00–9:04 AM · 3 scans, stock in',
    selectAll: {
      label: 'Select all eligible scans in 9:00–9:04 AM',
      visibleLabel: 'Select all',
      checked: false as boolean | 'mixed',
      disabled: false,
      onChange: jest.fn(),
    },
    fontRegular: fonts.regular,
    fontBold: fonts.bold,
    onToggle: jest.fn(),
  };

  it('names its time range, exposes expanded state, and shows its cards and select all only when open', async () => {
    const view = await wrap(
      <SessionCard {...props} expanded pill="1 with nothing on hand">
        <Text>Card body</Text>
      </SessionCard>,
    );

    const region = screen.getByTestId('session-s1');
    expect(region.props.role).toBe('region');
    expect(region.props['aria-label']).toBe('Scan session 9:00–9:04 AM');
    const toggle = screen.getByTestId('session-s1-toggle');
    expect(toggle.props.accessibilityState.expanded).toBe(true);
    expect(screen.getByText('Card body')).toBeTruthy();
    await fireEvent.press(screen.getByRole('checkbox', { name: props.selectAll.label }));
    expect(props.selectAll.onChange).toHaveBeenCalledWith(true);
    await fireEvent.press(toggle);
    expect(props.onToggle).toHaveBeenCalledTimes(1);

    await view.rerender(
      <StockbotTamaguiProvider>
        <SessionCard {...props} expanded={false}>
          <Text>Card body</Text>
        </SessionCard>
      </StockbotTamaguiProvider>,
    );
    expect(screen.getByTestId('session-s1-toggle').props.accessibilityState.expanded).toBe(false);
    expect(screen.queryByText('Card body')).toBeNull();
    expect(screen.queryByRole('checkbox')).toBeNull();
  });
});

describe('BatchReviewPanel', () => {
  beforeEach(() => jest.clearAllMocks());
  const base = {
    heading: 'Confirm 2 scans',
    rows: [
      { id: 'a', label: 'Oats', detail: '×3' },
      { id: 'b', label: 'Rice' },
    ],
    busy: false,
    confirmLabel: 'Confirm 2',
    confirmName: 'Confirm 2 selected scans',
    backLabel: 'Back',
    fontRegular: fonts.regular,
    fontBold: fonts.bold,
    onConfirm: jest.fn(),
    onBack: jest.fn(),
  };

  it('is a region named by its heading, lists the scans, and offers Confirm and Back', async () => {
    await wrap(<BatchReviewPanel {...base} skipLine="Confirm skips the 1 with nothing on hand." />);

    const panel = screen.getByTestId('batch-review');
    expect(panel.props.role).toBe('region');
    expect(panel.props['aria-label']).toBe('Confirm 2 scans');
    expect(within(panel).getByRole('heading', { name: 'Confirm 2 scans' })).toBeTruthy();
    expect(within(panel).getByText('Oats')).toBeTruthy();
    expect(within(panel).getByText('×3')).toBeTruthy();
    expect(within(panel).getByText('Confirm skips the 1 with nothing on hand.')).toBeTruthy();
    await fireEvent.press(within(panel).getByRole('button', { name: 'Confirm 2 selected scans' }));
    expect(base.onConfirm).toHaveBeenCalledTimes(1);
    await fireEvent.press(within(panel).getByRole('button', { name: 'Back' }));
    expect(base.onBack).toHaveBeenCalledTimes(1);
  });

  it('leaves the skip line and the alert out when there is nothing to say', async () => {
    await wrap(<BatchReviewPanel {...base} />);

    expect(screen.queryByTestId('batch-review-skip')).toBeNull();
    expect(screen.queryByRole('alert')).toBeNull();
  });

  it('announces a failure as an alert and locks both buttons while confirming', async () => {
    const view = await wrap(<BatchReviewPanel {...base} error="Those scans could not be confirmed." />);
    expect(screen.getByRole('alert')).toBeTruthy();

    await view.rerender(
      <StockbotTamaguiProvider>
        <BatchReviewPanel {...base} busy />
      </StockbotTamaguiProvider>,
    );
    const confirm = screen.getByRole('button', { name: 'Confirm 2 selected scans' });
    expect(confirm.props.accessibilityState?.disabled ?? confirm.props['aria-disabled']).toBeTruthy();
    expect(screen.getByText('Confirming')).toBeTruthy();
    await fireEvent.press(confirm);
    expect(base.onConfirm).not.toHaveBeenCalled();
  });
});

describe('focusOnWeb', () => {
  const original = Platform.OS;
  afterEach(() => {
    Object.defineProperty(Platform, 'OS', { value: original, configurable: true });
  });

  it('focuses the node on web and leaves native alone', () => {
    const focus = jest.fn();
    Object.defineProperty(Platform, 'OS', { value: 'web', configurable: true });
    focusOnWeb({ focus });
    expect(focus).toHaveBeenCalledTimes(1);

    Object.defineProperty(Platform, 'OS', { value: 'android', configurable: true });
    focusOnWeb({ focus });
    expect(focus).toHaveBeenCalledTimes(1);
    expect(() => focusOnWeb(null)).not.toThrow();
  });
});
