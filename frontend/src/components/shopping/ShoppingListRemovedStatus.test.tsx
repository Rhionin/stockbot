import { render, screen, waitFor } from '@testing-library/react';
import { MantineProvider } from '@mantine/core';
import { describe, expect, it } from 'vitest';
import { shoppingListAfterRemoveProductMessage } from '@stockbot/shared/shoppingListCopy';
import { ShoppingListRemovedStatus } from './ShoppingListRemovedStatus';

describe('ShoppingListRemovedStatus', () => {
  it('shows the off-list status and receives focus when requested', async () => {
    render(
      <MantineProvider>
        <ShoppingListRemovedStatus productName="Plain rice" focusOnMount />
      </MantineProvider>,
    );
    const status = screen.getByRole('status');
    expect(status).toHaveTextContent(shoppingListAfterRemoveProductMessage('Plain rice'));
    await waitFor(() => expect(status).toHaveFocus());
  });

  it('does not expose an undo control', () => {
    render(
      <MantineProvider>
        <ShoppingListRemovedStatus productName="Plain rice" />
      </MantineProvider>,
    );
    expect(screen.queryByRole('button', { name: /undo/i })).not.toBeInTheDocument();
  });
});
