import { render, screen } from '@testing-library/react-native';
import type { OffListRow } from '@stockbot/shared/types';
import { ZeroForTripStatusBlockMobile } from './ZeroForTripStatusBlock';

const row: OffListRow = {
  id: 'item:tea',
  itemId: 'tea',
  name: 'Green Tea',
  offListReason: 'zero_for_trip',
  sent: 0,
  storeId: null,
  canSendToCart: false,
  needsAttention: null,
  zeroForTrip: {
    entryId: 'line-1',
    providerId: 'kroger',
    actorUserId: null,
    actorDisplayName: null,
    setAt: null,
  },
};

const restock = {
  items: [{ itemId: 'tea', target: { kind: 'amount' as const, value: 2 } }],
  categories: [],
};

describe('ZeroForTripStatusBlockMobile', () => {
  it('renders A6 and nudge for skipped legacy rows', async () => {
    await render(
      <ZeroForTripStatusBlockMobile
        row={row}
        actorFields={{}}
        restock={restock}
        nudge={{ onDontRestock: jest.fn() }}
      />,
    );
    expect(screen.getByTestId('zero-for-trip-status-label')).toHaveTextContent('Skipped for this trip');
    expect(screen.getByTestId('zero-for-trip-status-sentence')).toHaveTextContent(
      'Skipped for this trip. It comes back next trip.',
    );
    expect(screen.getByTestId('zero-for-trip-nudge')).toBeTruthy();
  });
});