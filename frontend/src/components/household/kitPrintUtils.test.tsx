import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { useKitPrintRoot } from './kitPrintUtils';

function PrintProbe() {
  const { printing, print } = useKitPrintRoot();
  return (
    <div className="kit-print-root" data-printing={printing ? 'true' : 'false'} data-testid="root">
      <button type="button" onClick={print}>Print</button>
    </div>
  );
}

describe('useKitPrintRoot', () => {
  it('clears data-printing after the browser afterprint event', async () => {
    const printSpy = vi.spyOn(window, 'print').mockImplementation(() => undefined);
    render(<PrintProbe />);
    fireEvent.click(screen.getByRole('button', { name: 'Print' }));
    expect(screen.getByTestId('root')).toHaveAttribute('data-printing', 'true');
    window.dispatchEvent(new Event('afterprint'));
    await waitFor(() => {
      expect(screen.getByTestId('root')).toHaveAttribute('data-printing', 'false');
    });
    printSpy.mockRestore();
  });
});
