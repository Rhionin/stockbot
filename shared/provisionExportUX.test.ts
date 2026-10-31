import { describe, expect, it } from 'vitest';
import type { ProvisionReport } from './types';
import {
  PROVISION_GROUP_NOT_ADDED,
  PROVISION_GROUP_NOT_CONFIRMED,
  PROVISION_GROUP_SENT,
  groupProvisionResults,
  provisionItemReasonLine,
  provisionReasonText,
  provisionSentTotal,
  provisionStatusGroup,
  provisionStoreLabel,
  provisionSummaryCopy,
  provisionWholeRequestActionLabel,
  provisionWholeRequestMessage,
} from './provisionExportUX';

describe('provisionStoreLabel', () => {
  it('falls back when the name is blank', () => {
    expect(provisionStoreLabel('')).toBe('your store');
    expect(provisionStoreLabel(undefined)).toBe('your store');
  });
});

describe('provisionReasonText', () => {
  const store = "Smith's";

  it('maps every UX reason bucket', () => {
    expect(provisionReasonText(store, 'no_upc')).toBe('No barcode saved for this item');
    expect(provisionReasonText(store, 'no_match')).toBe("Couldn't find this at Smith's");
    expect(provisionReasonText(store, 'batch_rejected')).toBe("Smith's didn't accept this item");
    expect(provisionReasonText(store, 'api_error')).toBe('Something went wrong sending this item');
  });

  it('uses a generic line for unknown reasons', () => {
    expect(provisionReasonText(store, 'unavailable')).toBe('Something went wrong sending this item');
    expect(provisionReasonText(store, 'no_per_item_result')).toBe('Something went wrong sending this item');
  });
});

describe('provisionSummaryCopy', () => {
  it('covers all sent, mixed, and none sent cases', () => {
    expect(provisionSummaryCopy(2, 0, 'Kroger').primary).toBe('2 items sent to your Kroger cart.');
    expect(provisionSummaryCopy(1, 0, 'Kroger').primary).toBe('1 item sent to your Kroger cart.');
    expect(provisionSummaryCopy(2, 1, 'Kroger').primary).toBe(
      '2 items sent to your Kroger cart. 1 couldn\'t be sent:',
    );
    expect(provisionSummaryCopy(0, 3, 'Kroger').primary).toBe(
      'Nothing was sent to your cart. 3 items couldn\'t be sent:',
    );
  });

  it('adds the cart confirmation hint when something was sent', () => {
    const copy = provisionSummaryCopy(1, 0, 'Kroger');
    expect(copy.cartConfirmLine).toContain("doesn't confirm each item");
    expect(copy.openCartLabel).toBe('Open Kroger cart');
  });
});

describe('provisionWholeRequest copy', () => {
  it('matches UX for unavailable and reconnect', () => {
    expect(provisionWholeRequestMessage('provider_unavailable', 'Kroger')).toBe(
      'Kroger isn\'t responding right now. Nothing was sent.',
    );
    expect(provisionWholeRequestMessage('reconnect_required', 'Kroger')).toBe(
      'Couldn\'t connect to your Kroger account. Nothing was sent.',
    );
    expect(provisionWholeRequestActionLabel('provider_unavailable', 'Kroger')).toBe('Try again');
    expect(provisionWholeRequestActionLabel('reconnect_required', 'Kroger')).toBe('Reconnect Kroger');
  });
});

describe('groupProvisionResults', () => {
  const base: ProvisionReport = {
    provider: 'kroger',
    exported: 0,
  };

  it('groups modern results by status', () => {
    const report: ProvisionReport = {
      ...base,
      exported: 2,
      sentCount: 2,
      skippedCount: 1,
      failedCount: 1,
      unknownCount: 1,
      results: [
        { entryId: 'a', itemId: '1', name: 'Milk', quantity: 1, status: 'sent' },
        { entryId: 'b', itemId: '2', name: 'Bread', quantity: 1, status: 'sent' },
        { entryId: 'c', itemId: '3', name: 'Mystery', quantity: 1, status: 'skipped', reason: 'no_upc' },
        { entryId: 'd', itemId: '4', name: 'Soup', quantity: 1, status: 'failed', reason: 'api_error' },
        { entryId: 'e', itemId: '5', name: 'Oats', quantity: 1, status: 'unknown', reason: 'no_per_item_result' },
      ],
    };
    const groups = groupProvisionResults(report);
    expect(groups.sentCount).toBe(2);
    expect(groups.notAdded).toHaveLength(2);
    expect(groups.notConfirmed).toHaveLength(1);
    expect(groups.couldNotSendCount).toBe(2);
    expect(provisionSentTotal(groups)).toBe(2);
  });

  it('falls back to legacy failed and unknown lists', () => {
    const report: ProvisionReport = {
      ...base,
      exported: 1,
      failedItems: ['Jam'],
      unknownItems: ['Oats'],
      entries: [{ entryId: 'sli-oats', itemId: 'oats', name: 'Oats', quantity: 1, outcome: 'unknown' }],
    };
    const groups = groupProvisionResults(report);
    expect(groups.sentCount).toBe(1);
    expect(groups.notAdded.map((row) => row.name)).toEqual(['Jam']);
    expect(groups.notConfirmed[0]?.entryId).toBe('sli-oats');
  });

  it('surfaces whole-request errors only when nothing was sent', () => {
    expect(groupProvisionResults({ ...base, error: 'provider_unavailable' }).wholeRequestError).toBe(
      'provider_unavailable',
    );
    expect(groupProvisionResults({
      ...base,
      error: 'reconnect_required',
      sentCount: 1,
      results: [{ entryId: 'a', itemId: '1', name: 'Milk', quantity: 1, status: 'sent' }],
    }).wholeRequestError).toBeUndefined();
  });
});

describe('provisionStatusGroup', () => {
  it('maps every result status to a panel group', () => {
    expect(provisionStatusGroup('skipped')).toBe('not_added');
    expect(provisionStatusGroup('failed')).toBe('not_added');
    expect(provisionStatusGroup('unknown')).toBe('not_confirmed');
    expect(provisionStatusGroup('sent')).toBe('sent_or_added');
    expect(provisionStatusGroup('added')).toBe('sent_or_added');
  });
});

describe('provisionItemReasonLine', () => {
  it('formats a row the way UX specifies', () => {
    expect(provisionItemReasonLine('Long name', 'Kroger', 'no_upc')).toBe(
      'Long name: No barcode saved for this item',
    );
  });
});

describe('group headings', () => {
  it('exports stable section titles for mobile reuse', () => {
    expect(PROVISION_GROUP_NOT_ADDED).toBe("Couldn't be sent");
    expect(PROVISION_GROUP_NOT_CONFIRMED).toBe('Not confirmed');
    expect(PROVISION_GROUP_SENT).toBe('Sent');
  });
});

describe('unknown store name', () => {
  it('never says "your your store" and starts sentences with a capital', () => {
    const store = provisionStoreLabel(undefined);
    expect(provisionWholeRequestMessage('reconnect_required', store)).toBe(
      "Couldn't connect to your store account. Nothing was sent.",
    );
    expect(provisionWholeRequestMessage('provider_unavailable', store)).toBe(
      "Your store isn't responding right now. Nothing was sent.",
    );
    expect(provisionReasonText(store, 'batch_rejected')).toBe("Your store didn't accept this item");
    expect(provisionSummaryCopy(1, 0, store).cartConfirmLine).toMatch(/^Your store doesn't confirm/);
  });
});
