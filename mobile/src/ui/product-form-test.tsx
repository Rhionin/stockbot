import { fireEvent, render, screen } from '@testing-library/react-native';
import { emptyProductDraft, validateProductDraft } from '@stockbot/shared/productForm';
import {
  ContributeFields,
  NetSizeFields,
  ProductFormFields,
  ProvenanceBadge,
  StockbotTamaguiProvider,
  TextField,
} from '@stockbot/ui';

const fontRegular = 'AtkinsonHyperlegible_400Regular';
const fontBold = 'AtkinsonHyperlegible_700Bold';
const fonts = { fontRegular, fontBold };

async function inProvider(ui: React.ReactElement) {
  return render(<StockbotTamaguiProvider>{ui}</StockbotTamaguiProvider>);
}

describe('TextField', () => {
  it('shows a visible label, reports typing and marks a required field', async () => {
    const onChangeText = jest.fn();
    await inProvider(<TextField fieldId="n" label="Product name" required value="" onChangeText={onChangeText} {...fonts} />);

    expect(screen.getByText('Product name (required)')).toBeTruthy();
    const input = screen.getByLabelText('Product name');
    expect(input.props['aria-required']).toBe(true);
    await fireEvent.changeText(input, 'Oats');
    expect(onChangeText).toHaveBeenCalledWith('Oats');
  });

  it('ties the error to the field and announces it', async () => {
    await inProvider(<TextField fieldId="n" label="Size" error="Size has to be greater than zero." value="0" onChangeText={jest.fn()} {...fonts} />);

    const input = screen.getByLabelText('Size');
    expect(input.props['aria-invalid']).toBe(true);
    expect(input.props['aria-describedby']).toBe('n-error');
    expect(input.props.accessibilityHint).toBe('Size has to be greater than zero.');
    const error = screen.getByText('Size has to be greater than zero.');
    expect(error.props.nativeID).toBe('n-error');
    expect(error.props.accessibilityRole).toBe('alert');
  });

  it('has no error text and is not invalid without an error', async () => {
    await inProvider(<TextField fieldId="n" label="Brand" hint="As printed." value="" onChangeText={jest.fn()} {...fonts} />);

    expect(screen.getByLabelText('Brand').props['aria-invalid']).toBe(false);
    expect(screen.getByLabelText('Brand').props['aria-describedby']).toBe('n-hint');
    expect(screen.queryByRole('alert')).toBeNull();
  });
});

describe('NetSizeFields', () => {
  const draft = { amount: '16', unit: 'oz', packageWord: 'can', packCount: '6' };

  it('reports each field change on top of the rest of the draft', async () => {
    const onChange = jest.fn();
    await inProvider(<NetSizeFields draft={draft} onChange={onChange} {...fonts} />);

    await fireEvent.changeText(screen.getByLabelText('Size'), '20');
    await fireEvent.changeText(screen.getByLabelText('Package'), 'jar');
    await fireEvent.changeText(screen.getByLabelText('Pack count'), '2');
    await fireEvent.press(screen.getByLabelText('fl oz'));

    expect(onChange.mock.calls.map((call) => call[0])).toEqual([
      { ...draft, amount: '20' },
      { ...draft, packageWord: 'jar' },
      { ...draft, packCount: '2' },
      { ...draft, unit: 'fl oz' },
    ]);
  });

  it('marks the chosen unit and clears it when pressed again', async () => {
    const onChange = jest.fn();
    await inProvider(<NetSizeFields draft={draft} onChange={onChange} {...fonts} />);

    expect(screen.getByLabelText('oz').props.accessibilityState.checked).toBe(true);
    expect(screen.getByLabelText('lb').props.accessibilityState.checked).toBe(false);
    await fireEvent.press(screen.getByLabelText('oz'));
    expect(onChange).toHaveBeenCalledWith({ ...draft, unit: '' });
  });

  it('shows the size and pack count errors beside their own fields', async () => {
    await inProvider(
      <NetSizeFields
        draft={draft}
        onChange={jest.fn()}
        sizeError="Enter a size and a unit together."
        packCountError="Pack count has to be at least 1."
        {...fonts}
      />,
    );

    expect(screen.getByLabelText('Size').props['aria-invalid']).toBe(true);
    expect(screen.getByLabelText('Pack count').props['aria-invalid']).toBe(true);
    expect(screen.getByLabelText('Package').props['aria-invalid']).toBe(false);
    expect(screen.getAllByRole('alert')).toHaveLength(2);
  });
});

describe('ContributeFields', () => {
  const choice = { contribute: false, contributeTo: 'openfoodfacts' as const };

  it('shows only the household switch while sharing is off', async () => {
    const onToggleEnabled = jest.fn();
    await inProvider(
      <ContributeFields choice={choice} enabled={false} configured onChoice={jest.fn()} onToggleEnabled={onToggleEnabled} {...fonts} />,
    );

    expect(screen.queryByLabelText('Contribute this product')).toBeNull();
    await fireEvent(screen.getByLabelText('Let me contribute products I type in'), 'valueChange', true);
    expect(onToggleEnabled).toHaveBeenCalledWith(true);
  });

  it('offers the product checkbox and then the database once sharing is on', async () => {
    const onChoice = jest.fn();
    const view = await inProvider(
      <ContributeFields choice={choice} enabled configured={false} onChoice={onChoice} onToggleEnabled={jest.fn()} {...fonts} />,
    );

    expect(screen.getByText(/not signed in to the open databases/)).toBeTruthy();
    expect(screen.queryByLabelText('Open Food Facts')).toBeNull();
    await fireEvent.press(screen.getByLabelText('Contribute this product'));
    expect(onChoice).toHaveBeenCalledWith({ ...choice, contribute: true });

    await view.rerender(
      <StockbotTamaguiProvider>
        <ContributeFields
          choice={{ ...choice, contribute: true }}
          enabled
          configured
          onChoice={onChoice}
          onToggleEnabled={jest.fn()}
          {...fonts}
        />
      </StockbotTamaguiProvider>,
    );
    await fireEvent.press(screen.getByLabelText('Open Beauty Facts'));
    expect(onChoice).toHaveBeenLastCalledWith({ contribute: true, contributeTo: 'openbeautyfacts' });
  });

  it('explains an upstream product instead of offering the checkbox', async () => {
    await inProvider(
      <ContributeFields choice={choice} enabled configured allowProductOptIn={false} onChoice={jest.fn()} onToggleEnabled={jest.fn()} {...fonts} />,
    );

    expect(screen.queryByLabelText('Contribute this product')).toBeNull();
    expect(screen.getByText('This product already comes from an open database, so edits stay in your pantry.')).toBeTruthy();
  });

  it('shows a failed switch save as an alert', async () => {
    await inProvider(
      <ContributeFields choice={choice} enabled={false} configured error="Unable to save sharing setting." onChoice={jest.fn()} onToggleEnabled={jest.fn()} {...fonts} />,
    );

    expect(screen.getByRole('alert')).toHaveTextContent('Unable to save sharing setting.');
  });
});

describe('ProvenanceBadge', () => {
  it('names the database a product came from', async () => {
    await inProvider(<ProvenanceBadge externalSource="openfoodfacts" {...fonts} />);

    expect(screen.getByLabelText('Product data from Open Food Facts')).toBeTruthy();
    expect(screen.getByText('Open Food Facts')).toBeTruthy();
  });

  it('renders nothing for a household product or an unknown source', async () => {
    const view = await inProvider(<ProvenanceBadge {...fonts} />);
    expect(screen.queryByTestId('provenance-badge')).toBeNull();
    await view.rerender(
      <StockbotTamaguiProvider>
        <ProvenanceBadge externalSource={'mystery' as never} {...fonts} />
      </StockbotTamaguiProvider>,
    );
    expect(screen.queryByTestId('provenance-badge')).toBeNull();
  });

  it('can be quiet', async () => {
    await inProvider(<ProvenanceBadge externalSource="openproductsfacts" quiet {...fonts} />);

    expect(screen.getByLabelText('Product data from Open Products Facts')).toBeTruthy();
  });
});

describe('ProductFormFields', () => {
  it('updates the draft, marks size as touched and shows the validation errors', async () => {
    const onChange = jest.fn();
    const draft = { ...emptyProductDraft(), size: { amount: '', unit: 'oz', packageWord: '', packCount: '' }, sizeTouched: true };
    await inProvider(
      <ProductFormFields
        draft={draft}
        errors={validateProductDraft(draft)}
        onChange={onChange}
        contribute={{ enabled: false, configured: false, onToggleEnabled: jest.fn() }}
        {...fonts}
      />,
    );

    expect(screen.getByLabelText('Product name').props['aria-invalid']).toBe(true);
    expect(screen.getByText('Enter a size and a unit together.')).toBeTruthy();
    await fireEvent.changeText(screen.getByLabelText('Brand'), 'Mill');
    expect(onChange).toHaveBeenLastCalledWith({ ...draft, brand: 'Mill' });
    await fireEvent.changeText(screen.getByLabelText('Pack count'), '4');
    expect(onChange.mock.calls.at(-1)?.[0]).toMatchObject({ packTouched: true, size: { packCount: '4' } });
  });
});
