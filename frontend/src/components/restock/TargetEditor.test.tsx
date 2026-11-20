import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MantineProvider } from '@mantine/core';
import { describe, expect, it, vi } from 'vitest';
import type { RestockTarget } from '../../restock/types';
import { TargetEditor } from './TargetEditor';

const wrap = (ui: React.ReactNode) => render(<MantineProvider env="test">{ui}</MantineProvider>);

const monthsValue = () => screen.getByLabelText('Months');

describe('TargetEditor', () => {
  it('shows the next product target after saving another product', async () => {
    const onClose = vi.fn();
    const onSave = vi.fn().mockResolvedValue(undefined);
    const { rerender } = wrap(
      <TargetEditor
        opened
        title="Restock target for Synth item 27"
        initial={{ kind: 'months', value: 7 }}
        onClose={onClose}
        onSave={onSave}
      />,
    );
    expect(monthsValue()).toHaveValue('7');

    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(onSave).toHaveBeenCalledWith({ kind: 'months', value: 7 }));
    expect(onClose).toHaveBeenCalled();

    rerender(
      <MantineProvider env="test">
        <TargetEditor
          opened={false}
          title="Restock target for Synth item 27"
          initial={{ kind: 'months', value: 7 }}
          onClose={onClose}
          onSave={onSave}
        />
      </MantineProvider>,
    );
    rerender(
      <MantineProvider env="test">
        <TargetEditor
          opened
          title="Restock target for Synth item 28"
          initial={{ kind: 'months', value: 2 }}
          onClose={onClose}
          onSave={onSave}
        />
      </MantineProvider>,
    );

    expect(screen.getByRole('heading', { name: 'Restock target for Synth item 28' })).toBeInTheDocument();
    expect(monthsValue()).toHaveValue('2');
  });

  it('resets after cancel when reopened for the same product', () => {
    const onClose = vi.fn();
    const onSave = vi.fn();
    const initial: RestockTarget = { kind: 'months', value: 4 };
    const { rerender } = wrap(
      <TargetEditor opened title="Restock target for oats" initial={initial} onClose={onClose} onSave={onSave} />,
    );
    fireEvent.change(monthsValue(), { target: { value: '9' } });
    expect(monthsValue()).toHaveValue('9');
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    expect(onClose).toHaveBeenCalled();

    rerender(
      <MantineProvider env="test">
        <TargetEditor opened={false} title="Restock target for oats" initial={initial} onClose={onClose} onSave={onSave} />
      </MantineProvider>,
    );
    rerender(
      <MantineProvider env="test">
        <TargetEditor opened title="Restock target for oats" initial={initial} onClose={onClose} onSave={onSave} />
      </MantineProvider>,
    );
    expect(monthsValue()).toHaveValue('4');
  });

  it('keeps edits after a failed save and resets on a later open', async () => {
    const onClose = vi.fn();
    const failing = vi.fn().mockRejectedValue(new Error('network'));
    const initial: RestockTarget = { kind: 'months', value: 3 };
    const { rerender } = wrap(
      <TargetEditor opened title="Restock target" initial={initial} onClose={onClose} onSave={failing} />,
    );
    fireEvent.change(monthsValue(), { target: { value: '6' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(screen.getByText('network')).toBeInTheDocument());
    expect(monthsValue()).toHaveValue('6');

    rerender(
      <MantineProvider env="test">
        <TargetEditor opened={false} title="Restock target" initial={initial} onClose={onClose} onSave={failing} />
      </MantineProvider>,
    );
    rerender(
      <MantineProvider env="test">
        <TargetEditor opened title="Restock target" initial={initial} onClose={onClose} onSave={failing} />
      </MantineProvider>,
    );
    expect(monthsValue()).toHaveValue('3');
  });

  it('starts from defaults when no initial target is provided', () => {
    wrap(<TargetEditor opened title="Restock target" onClose={() => {}} onSave={async () => {}} />);
    expect(monthsValue()).toHaveValue('3');
  });
});