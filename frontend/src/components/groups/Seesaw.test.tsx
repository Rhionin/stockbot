import { render, screen } from '@testing-library/react';
import { MantineProvider } from '@mantine/core';
import { describe, expect, it } from 'vitest';
import type { MemberPackage } from './copy';
import { Seesaw } from './Seesaw';
import { seesawView, type SeesawView } from './seesaw';

const lemon: MemberPackage = {
  productId: 'lemon',
  name: 'Gatorade Lemon-Lime',
  onHand: 1,
  unitOfMeasure: 'canister',
  netAmount: 18.3,
  netUnit: 'oz',
};

const rows: {
  name: string;
  description: string;
  group: Parameters<typeof seesawView>[0];
  household?: number;
}[] = [
  {
    name: '48 oz',
    description: 'Keep on hand amount, pinned, 48 ounces',
    group: { quantity: 48, dimension: 'mass', usage: { perMonth: 16, unit: 'oz' } },
  },
  {
    name: 'Set time',
    description: 'Keep on hand time, not set',
    group: { quantity: 48, dimension: 'mass' },
  },
  {
    name: '3 mo default',
    description: 'Keep on hand time, pinned, 3 months, household default',
    group: {},
    household: 3,
  },
];

function shownText(button: HTMLElement): string {
  return (button.textContent ?? '').replace(/\s+/g, ' ').trim();
}

describe('seesaw pill name', () => {
  it.each(rows)('names $name and keeps the longer phrase as its description', ({ name, description, group, household }) => {
    const view: SeesawView = seesawView(group, [lemon], household, null);
    render(
      <MantineProvider>
        <Seesaw view={view} group={group} draft={null} onDraft={() => {}} onSave={async () => {}} />
      </MantineProvider>,
    );

    const pill = screen.getByRole('button', { name, description });
    expect(pill).toHaveAccessibleName(name);
    expect(pill).toHaveAccessibleDescription(description);
    expect(pill).toHaveAttribute('aria-label', name);
    expect(shownText(pill)).toBe(name);
  });
});
