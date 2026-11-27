import { describe, expect, it } from 'vitest';
import type { AuthSession } from '@stockbot/shared/contract';
import { editedTripDescription } from './shoppingListEditedCopy';

describe('G1–G3 edited trip label', () => {
  it('G2 uses You for the signed-in actor', () => {
    expect(editedTripDescription(
      { actorUserId: 'u1', actorDisplayName: 'Casey', at: '2026-11-27T00:00:00Z' },
      { user: { id: 'u1' } } as AuthSession,
    )).toMatch(/You chose this product/);
  });

  it('G3 when actor is unknown', () => {
    expect(editedTripDescription(
      { actorUserId: null, actorDisplayName: null, at: '2026-11-27T00:00:00Z' },
      null,
    )).toMatch(/This product was chosen/);
  });
});