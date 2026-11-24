import { act, render, screen } from '@testing-library/react-native';
import { DateField } from '../../../ui/src/DateField.web';

describe('DateField (web)', () => {
  it('is a browser date input that reports the picked day and commits on blur', async () => {
    const onChangeText = jest.fn();
    const onCommit = jest.fn();
    await render(
      <DateField value="2027-03-04" label="Expiration date" fontRegular="Atkinson" onChangeText={onChangeText} onCommit={onCommit} />,
    );

    const tree = screen.toJSON();
    const input = (Array.isArray(tree) ? tree[0] : tree) as { type: string; props: Record<string, any> };
    expect(input.type).toBe('input');
    expect(input.props.type).toBe('date');
    expect(input.props['aria-label']).toBe('Expiration date');
    expect(input.props.value).toBe('2027-03-04');
    expect(input.props.style.minHeight).toBeGreaterThanOrEqual(44);

    await act(async () => input.props.onChange({ currentTarget: { value: '2028-01-02' } }));
    await act(async () => input.props.onBlur());

    expect(onChangeText).toHaveBeenCalledWith('2028-01-02');
    expect(onCommit).toHaveBeenCalledTimes(1);
  });
});
