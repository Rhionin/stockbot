import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { OFFLINE_SENTENCE } from '@stockbot/shared/contract';
import { TamaguiHarnessPage } from './TamaguiHarnessPage';

describe('Tamagui harness (Vitest)', () => {
  it('renders the shared notice copy and fires the action button', () => {
    render(<TamaguiHarnessPage />);
    expect(screen.getByText(OFFLINE_SENTENCE)).toBeVisible();
    const action = screen.getByRole('button', { name: 'Try again' });
    expect(action).toBeVisible();
    fireEvent.click(action);
    fireEvent.click(action);
    expect(screen.getByTestId('tamagui-harness-press-count')).toHaveTextContent('Action presses: 2');
  });
});
