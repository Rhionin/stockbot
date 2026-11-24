import { fireEvent, render, screen } from '@testing-library/react-native';
import { StyleSheet, Text as NativeText } from 'react-native';
import { mobileChrome } from '@stockbot/shared/brandTokens';
import {
  ChoiceList,
  DateField,
  DATE_FIELD_PLACEHOLDER,
  DirectionTabs,
  NumberStepper,
  QueueCard,
  StockbotTamaguiProvider,
  UndoToastView,
} from '@stockbot/ui';

const fontRegular = 'AtkinsonHyperlegible_400Regular';
const fontBold = 'AtkinsonHyperlegible_700Bold';

function flat(node: { props: { style?: unknown } }): Record<string, unknown> {
  return (StyleSheet.flatten(node.props.style as Parameters<typeof StyleSheet.flatten>[0]) ?? {}) as Record<string, unknown>;
}

async function inProvider(ui: React.ReactElement) {
  return render(<StockbotTamaguiProvider>{ui}</StockbotTamaguiProvider>);
}

describe('NumberStepper', () => {
  const props = {
    label: 'Unit count',
    decreaseLabel: 'Decrease unit count',
    increaseLabel: 'Increase unit count',
    fontBold,
    onChangeText: jest.fn(),
    onStep: jest.fn(),
    onCommit: jest.fn(),
  };

  beforeEach(() => jest.clearAllMocks());

  it('steps both ways and reports typed text and the end of editing', async () => {
    await inProvider(<NumberStepper {...props} value="3" canDecrease />);

    await fireEvent.press(screen.getByLabelText('Increase unit count'));
    await fireEvent.press(screen.getByLabelText('Decrease unit count'));
    await fireEvent.changeText(screen.getByLabelText('Unit count'), '12');
    await fireEvent(screen.getByLabelText('Unit count'), 'blur');
    await fireEvent(screen.getByLabelText('Unit count'), 'submitEditing');

    expect(props.onStep.mock.calls).toEqual([[1], [-1]]);
    expect(props.onChangeText).toHaveBeenCalledWith('12');
    expect(props.onCommit).toHaveBeenCalledTimes(2);
    expect(screen.getByLabelText('Unit count').props.value).toBe('3');
  });

  it('disables the minus button at the floor and presses nothing', async () => {
    await inProvider(<NumberStepper {...props} value="1" canDecrease={false} />);

    const minus = screen.getByLabelText('Decrease unit count');
    expect(minus.props.accessibilityState.disabled).toBe(true);
    await fireEvent.press(minus);

    expect(props.onStep).not.toHaveBeenCalled();
  });

  it('keeps targets at least 44 points and outlines an invalid field in the danger color', async () => {
    await inProvider(<NumberStepper {...props} value="x" canDecrease invalid />);

    expect(flat(screen.getByLabelText('Increase unit count')).minHeight as number).toBeGreaterThanOrEqual(44);
    expect(flat(screen.getByLabelText('Increase unit count')).width as number).toBeGreaterThanOrEqual(44);
    expect(flat(screen.getByLabelText('Unit count')).minHeight as number).toBeGreaterThanOrEqual(44);
    const outline = flat(screen.getByTestId('number-stepper'));
    expect(outline.borderColor ?? outline.borderTopColor).toBe(mobileChrome.danger);
    expect(screen.getByLabelText('Unit count').props['aria-invalid']).toBe(true);
  });

  it('locks every control while disabled', async () => {
    await inProvider(<NumberStepper {...props} value="2" canDecrease disabled />);

    expect(screen.getByLabelText('Unit count').props.editable).toBe(false);
    await fireEvent.press(screen.getByLabelText('Increase unit count'));
    expect(props.onStep).not.toHaveBeenCalled();
  });
});

describe('DateField (native)', () => {
  it('is a typed YYYY-MM-DD field that reports text and commits when left', async () => {
    const onChangeText = jest.fn();
    const onCommit = jest.fn();
    await render(
      <DateField value="" label="Expiration date" fontRegular={fontRegular} onChangeText={onChangeText} onCommit={onCommit} />,
    );

    const field = screen.getByLabelText('Expiration date');
    expect(field.props.placeholder).toBe(DATE_FIELD_PLACEHOLDER);
    expect(field.props.maxLength).toBe(10);
    await fireEvent.changeText(field, '2027-03-04');
    await fireEvent(field, 'blur');
    await fireEvent(field, 'submitEditing');

    expect(onChangeText).toHaveBeenCalledWith('2027-03-04');
    expect(onCommit).toHaveBeenCalledTimes(2);
  });

  it('marks an invalid date', async () => {
    await render(
      <DateField value="2027-02-30" label="Expiration date" invalid fontRegular={fontRegular} onChangeText={jest.fn()} onCommit={jest.fn()} />,
    );

    expect(screen.getByLabelText('Expiration date').props['aria-invalid']).toBe(true);
    expect(flat(screen.getByLabelText('Expiration date')).borderColor).toBe(mobileChrome.danger);
  });
});

describe('ChoiceList', () => {
  const choices = [
    { value: '', label: 'Use oldest available automatically' },
    { value: 'a', label: 'Expires Apr 1, 2027' },
    { value: 'b', label: 'No expiration date' },
  ];

  it('marks the chosen row and reports a press', async () => {
    const onChange = jest.fn();
    await inProvider(
      <ChoiceList
        label="Instance to remove"
        description="Ordered by earliest expiration date."
        choices={choices}
        value="a"
        fontRegular={fontRegular}
        fontBold={fontBold}
        onChange={onChange}
      />,
    );

    expect(screen.getByText('Instance to remove')).toBeTruthy();
    expect(screen.getByText('Ordered by earliest expiration date.')).toBeTruthy();
    expect(screen.getByTestId('choice-a').props.accessibilityState.checked).toBe(true);
    expect(screen.getByTestId('choice-b').props.accessibilityState.checked).toBe(false);
    expect(screen.getByTestId('choice-automatic').props.accessibilityState.checked).toBe(false);
    await fireEvent.press(screen.getByTestId('choice-b'));
    expect(onChange).toHaveBeenCalledWith('b');
    expect(flat(screen.getByTestId('choice-b')).minHeight as number).toBeGreaterThanOrEqual(48);
  });

  it('presses nothing while disabled', async () => {
    const onChange = jest.fn();
    await inProvider(
      <ChoiceList label="Pick" choices={choices} value="" disabled fontRegular={fontRegular} fontBold={fontBold} onChange={onChange} />,
    );

    await fireEvent.press(screen.getByTestId('choice-a'));
    expect(onChange).not.toHaveBeenCalled();
  });
});

describe('UndoToastView', () => {
  it('shows a message with an Undo action', async () => {
    const onAction = jest.fn();
    await inProvider(
      <UndoToastView message="Added and taken out." actionLabel="Undo" bottom={24} fontRegular={fontRegular} fontBold={fontBold} onAction={onAction} />,
    );

    expect(screen.getByText('Added and taken out.')).toBeTruthy();
    await fireEvent.press(screen.getByLabelText('Undo'));
    expect(onAction).toHaveBeenCalledTimes(1);
    expect(screen.getByTestId('confirm-toast').props.accessibilityLiveRegion).toBe('polite');
    expect(flat(screen.getByTestId('confirm-toast')).bottom).toBe(24);
  });

  it('shows no action when none is given, even if a handler is', async () => {
    await inProvider(<UndoToastView message="Only 1 was on hand." bottom={16} fontRegular={fontRegular} fontBold={fontBold} onAction={jest.fn()} />);

    expect(screen.getByText('Only 1 was on hand.')).toBeTruthy();
    expect(screen.queryByLabelText('Undo')).toBeNull();
  });
});

describe('QueueCard details slot', () => {
  const base = {
    entryId: 'scan-1',
    label: 'Synthetic item',
    barcode: '111',
    metaLine: '111  Stock in',
    stockIn: true,
    stockOut: false,
    busy: false,
    busyThis: false,
    fontRegular,
    fontBold,
    onConfirm: jest.fn(),
    onRemove: jest.fn(),
  };

  it('puts details under the header on a normal card', async () => {
    await inProvider(<QueueCard {...base} blocked={false} details={<NativeText>detail row</NativeText>} />);

    expect(screen.getByText('detail row')).toBeTruthy();
  });

  it('puts details between the amber message and the backfill action', async () => {
    await inProvider(
      <QueueCard
        {...base}
        blocked
        details={<NativeText>detail row</NativeText>}
        blockedExtra={<NativeText>offer action</NativeText>}
      />,
    );

    const card = screen.getByTestId('scan-scan-1');
    const order = JSON.stringify(card.children);
    expect(order.indexOf('detail row')).toBeGreaterThan(-1);
    expect(order.indexOf('detail row')).toBeLessThan(order.indexOf('offer action'));
  });
});

describe('DirectionTabs without a known direction', () => {
  it('selects no tab', async () => {
    await inProvider(
      <DirectionTabs
        label="Scan direction"
        fontBold={fontBold}
        value={null}
        onChange={jest.fn()}
        tabs={[
          { value: 'stock_in', label: 'Stock in', count: 1 },
          { value: 'stock_out', label: 'Stock out', count: 0 },
        ]}
      />,
    );

    expect(screen.getAllByRole('tab').every((tab) => tab.props.accessibilityState.selected === false)).toBe(true);
  });
});
