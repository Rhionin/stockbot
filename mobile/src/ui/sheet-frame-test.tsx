import { Text } from 'react-native';
import { fireEvent, render, screen } from '@testing-library/react-native';
import { ActionButton, SearchResults, SheetFrame, StockbotTamaguiProvider } from '@stockbot/ui';

const fontRegular = 'AtkinsonHyperlegible_400Regular';
const fontBold = 'AtkinsonHyperlegible_700Bold';

async function inProvider(ui: React.ReactElement) {
  return render(<StockbotTamaguiProvider>{ui}</StockbotTamaguiProvider>);
}

describe('SheetFrame', () => {
  it('shows a level 2 heading, the body and a Close button that closes', async () => {
    const onClose = jest.fn();
    await inProvider(
      <SheetFrame title="Find an item" dialog={false} topInset={0} bottomInset={0} fontBold={fontBold} onClose={onClose}>
        <Text>Body</Text>
      </SheetFrame>,
    );

    const heading = screen.getByRole('header', { name: 'Find an item' });
    expect(heading.props['aria-level']).toBe(2);
    expect(screen.getByText('Body')).toBeTruthy();
    await fireEvent.press(screen.getByLabelText('Close'));
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it('cannot be closed while it says so', async () => {
    const onClose = jest.fn();
    await inProvider(
      <SheetFrame title="Add an item" dialog topInset={0} bottomInset={0} fontBold={fontBold} closeDisabled onClose={onClose}>
        <Text>Body</Text>
      </SheetFrame>,
    );

    await fireEvent.press(screen.getByLabelText('Close'));

    expect(onClose).not.toHaveBeenCalled();
  });

  it('pins a banner above the body', async () => {
    await inProvider(
      <SheetFrame title="T" dialog={false} topInset={0} bottomInset={0} fontBold={fontBold} banner={<Text>Banner text</Text>} onClose={jest.fn()}>
        <Text>Body</Text>
      </SheetFrame>,
    );

    expect(screen.getByText('Banner text')).toBeTruthy();
  });
});

describe('SearchResults', () => {
  const rows = [
    { id: 'a', title: 'Jam', detail: 'Pantry', badge: 'None on hand', badgeWarn: true },
    { id: 'b', title: 'Rice', badge: '2 on hand' },
  ];

  it('is a named list of buttons with spoken names, and reports the pick', async () => {
    const onPick = jest.fn();
    await inProvider(<SearchResults label="Search results" rows={rows} fontRegular={fontRegular} fontBold={fontBold} onPick={onPick} />);

    expect(screen.getByLabelText('Search results').props.role).toBe('list');
    await fireEvent.press(screen.getByRole('button', { name: 'Rice 2 on hand' }));
    expect(onPick).toHaveBeenCalledWith('b');
  });

  it('ignores a press while disabled', async () => {
    const onPick = jest.fn();
    await inProvider(<SearchResults label="Search results" rows={rows} disabled fontRegular={fontRegular} fontBold={fontBold} onPick={onPick} />);

    await fireEvent.press(screen.getByRole('button', { name: 'Jam Pantry None on hand' }));

    expect(onPick).not.toHaveBeenCalled();
  });
});

describe('ActionButton', () => {
  it('presses, and ignores presses while busy', async () => {
    const onPress = jest.fn();
    const { rerender } = await inProvider(<ActionButton label="Add item" fontBold={fontBold} onPress={onPress} />);

    await fireEvent.press(screen.getByLabelText('Add item'));
    expect(onPress).toHaveBeenCalledTimes(1);

    await rerender(<StockbotTamaguiProvider><ActionButton label="Add item" busy fontBold={fontBold} onPress={onPress} /></StockbotTamaguiProvider>);
    await fireEvent.press(screen.getByLabelText('Add item'));
    expect(onPress).toHaveBeenCalledTimes(1);
    expect(screen.getByLabelText('Add item').props.accessibilityState).toMatchObject({ disabled: true, busy: true });
  });
});
