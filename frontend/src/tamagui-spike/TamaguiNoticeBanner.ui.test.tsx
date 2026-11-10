import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { OFFLINE_SENTENCE } from '@stockbot/shared/contract';
import { NOTICE_BANNER_EDGE, NOTICE_BANNER_TINT } from '@stockbot/shared/noticeBanner';
import { TamaguiNoticeBanner, TamaguiSpikeProvider } from '@stockbot/ui';

function painted(color: string): string {
  const match = /rgba?\((\d+),\s*(\d+),\s*(\d+)/.exec(color);
  if (!match) return color.trim().toLowerCase();
  const hex = match.slice(1, 4).map((channel) => Number(channel).toString(16).padStart(2, '0')).join('');
  return `#${hex}`;
}

describe('Tamagui notice banner (Vitest)', () => {
  it('matches the pale red notice treatment and handles press', () => {
    const onAction = vi.fn();
    render(
      <TamaguiSpikeProvider theme="light">
        <TamaguiNoticeBanner onAction={onAction} />
      </TamaguiSpikeProvider>,
    );
    expect(screen.getByText(OFFLINE_SENTENCE)).toBeVisible();
    const banner = screen.getByTestId('tamagui-notice-banner');
    const style = getComputedStyle(banner);
    expect(painted(style.backgroundColor)).toBe(NOTICE_BANNER_TINT.toLowerCase());
    expect(painted(style.borderTopColor)).toBe(NOTICE_BANNER_EDGE.toLowerCase());
    fireEvent.click(screen.getByRole('button', { name: 'Try again' }));
    expect(onAction).toHaveBeenCalledTimes(1);
  });
});
