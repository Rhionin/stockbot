import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MantineProvider } from '@mantine/core';
import { describe, expect, it, vi } from 'vitest';
import { shoppingListAfterRemoveProductMessage } from '@stockbot/shared/shoppingListCopy';
import { ShoppingListRemovedStatus } from './ShoppingListRemovedStatus';

describe('ShoppingListRemovedStatus', () => {
  it('shows the off-list status and focuses Undo when requested', async () => {
    render(
      <MantineProvider>
        <ShoppingListRemovedStatus productName="Plain rice" focusUndoOnMount onUndo={() => {}} />
      </MantineProvider>,
    );
    expect(screen.getByRole('status')).toHaveTextContent(shoppingListAfterRemoveProductMessage('Plain rice'));
    await waitFor(() => expect(screen.getByRole('button', { name: 'Undo' })).toHaveFocus());
  });

  it('shows an inline alert for undo errors', () => {
    render(
      <MantineProvider>
        <ShoppingListRemovedStatus
          productName="Plain rice"
          undoError="Something went wrong. Try again."
          onUndo={() => {}}
        />
      </MantineProvider>,
    );
    expect(screen.getByRole('alert')).toHaveTextContent('Something went wrong. Try again.');
  });

  it('does not double-submit while undo is pending', () => {
    const onUndo = vi.fn();
    render(
      <MantineProvider>
        <ShoppingListRemovedStatus productName="Plain rice" undoPending onUndo={onUndo} />
      </MantineProvider>,
    );
    const undo = screen.getByRole('button', { name: 'Undo' });
    expect(undo).toBeDisabled();
    fireEvent.click(undo);
    expect(onUndo).not.toHaveBeenCalled();
  });
});
