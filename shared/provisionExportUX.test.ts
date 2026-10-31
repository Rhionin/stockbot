import { describe, expect, it } from 'vitest';
import type { ProvisionReport } from './types';
import {
  PROVISION_ENVELOPE_NOTHING_SENT,
  PROVISION_NOTHING_SENT_LINE,
  groupProvisionResults,
  provisionCheckInCartAria,
  provisionEmptyListNote,
  provisionItemReasonLine,
  provisionLegacyNothingSentError,
  provisionNothingSentReconnectLabel,
  provisionReasonText,
  provisionRowGroup,
  provisionSectionHeadingCheck,
  provisionSectionHeadingCouldntSend,
  provisionSectionHeadingSent,
  provisionSendButtonLabel,
  provisionSendHint,
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

describe('provisionSendButtonLabel and hints', () => {
  it('uses the display name or fallback', () => {
    expect(provisionSendButtonLabel('Kroger')).toBe('Send to Kroger');
    expect(provisionSendButtonLabel("Smith's")).toBe("Send to Smith's");
    expect(provisionSendButtonLabel('your store')).toBe('Send to your store');
    expect(provisionSendHint('Kroger')).toBe('Sends these items to your Kroger cart.');
    expect(provisionSendHint('your store')).toBe('Sends these items to your store cart.');
    expect(provisionEmptyListNote("Smith's")).toBe("Build the list before sending it to Smith's.");
  });
});

describe('provisionReasonText', () => {
  const store = "Smith's";

  it('maps UX reason buckets', () => {
    expect(provisionReasonText(store, 'no_upc')).toBe('No barcode saved for this item');
    expect(provisionReasonText(store, 'no_match')).toBe("Couldn't find this at Smith's");
    expect(provisionReasonText(store, 'batch_rejected')).toBe("Smith's didn't accept this item");
    expect(provisionReasonText(store, 'api_error')).toBe('Something went wrong sending this item');
  });

  it('handles unavailable separately from generic errors', () => {
    expect(provisionReasonText(store, 'unavailable')).toBe("Not available at Smith's right now");
    expect(provisionReasonText(store, 'weird_code')).toBe('Something went wrong sending this item');
  });
});

describe('provisionSummaryCopy', () => {
  it('never shows zero sent and omits zero-count lines', () => {
    const onlyM = provisionSummaryCopy({ N: 0, M: 2, U: 0 }, 'Kroger');
    expect(onlyM.lines).toEqual(['Nothing was sent to your cart.', "2 couldn't be sent."]);
    expect(onlyM.lines.some((line) => line.includes('0 items sent'))).toBe(false);
    expect(onlyM.cartConfirmLine).toBeUndefined();

    const nAndU = provisionSummaryCopy({ N: 1, M: 0, U: 1 }, 'Kroger');
    expect(nAndU.lines).toEqual([
      '1 item sent to your Kroger cart.',
      '1 might not have reached your cart.',
    ]);
    expect(nAndU.cartConfirmLine).toBe(
      "Kroger doesn't confirm each item, so check your cart to make sure everything is there.",
    );
  });

  it('covers singular and plural phrasing', () => {
    expect(provisionSummaryCopy({ N: 2, M: 1, U: 3 }, 'Kroger').lines).toEqual([
      '2 items sent to your Kroger cart.',
      "1 couldn't be sent.",
      '3 might not have reached your cart.',
    ]);
  });

  it('shows nothing sent when N and U are both zero', () => {
    expect(provisionSummaryCopy({ N: 0, M: 1, U: 0 }, 'Kroger').lines[0]).toBe(
      'Nothing was sent to your cart.',
    );
  });
});

describe('section headings', () => {
  it('includes counts', () => {
    expect(provisionSectionHeadingSent(2)).toBe('Sent (2)');
    expect(provisionSectionHeadingCouldntSend(1)).toBe("Couldn't be sent (1)");
    expect(provisionSectionHeadingCheck(4)).toBe('Check these in your cart (4)');
  });
});

describe('groupProvisionResults', () => {
  const base: ProvisionReport = {
    provider: 'kroger',
    exported: 0,
  };

  it('routes no_per_item_result to check, not failures', () => {
    const report: ProvisionReport = {
      ...base,
      results: [
        { entryId: 'e', itemId: '5', name: 'Oats', quantity: 1, status: 'unknown', reason: 'no_per_item_result' },
        { entryId: 'c', itemId: '3', name: 'Tea', quantity: 1, status: 'skipped', reason: 'unavailable' },
      ],
    };
    const groups = groupProvisionResults(report);
    expect(groups.check).toHaveLength(1);
    expect(groups.notAdded).toHaveLength(1);
    expect(groups.M).toBe(1);
    expect(groups.U).toBe(1);
    expect(provisionRowGroup(groups.check[0]!)).toBe('not_confirmed');
  });

  it('groups sent rows and legacy fallback', () => {
    const report: ProvisionReport = {
      ...base,
      exported: 1,
      failedItems: ['Jam'],
      unknownItems: ['Oats'],
      entries: [{ entryId: 'sli-oats', itemId: 'oats', name: 'Oats', quantity: 1, outcome: 'unknown' }],
    };
    const groups = groupProvisionResults(report);
    expect(groups.N).toBe(1);
    expect(groups.check[0]?.entryId).toBe('sli-oats');
  });
});

describe('provisionStatusGroup', () => {
  it('maps statuses to panel groups', () => {
    expect(provisionStatusGroup('skipped')).toBe('not_added');
    expect(provisionStatusGroup('unknown')).toBe('not_confirmed');
    expect(provisionStatusGroup('sent')).toBe('sent_or_added');
  });
});

describe('provisionLegacyNothingSentError (#486 legacy 200 body)', () => {
  const unknownRows = Array.from({ length: 3 }, (_, index) => ({
    entryId: `u-${index}`,
    itemId: `i-${index}`,
    name: `Item ${index}`,
    quantity: 1,
    status: 'unknown' as const,
    reason: 'request_incomplete' as const,
  }));

  it('treats reconnect_required as blocking when nothing was sent', () => {
    expect(provisionLegacyNothingSentError({
      provider: 'kroger',
      exported: 0,
      error: 'reconnect_required',
      results: unknownRows,
    })).toBe('reconnect_required');
  });

  it('treats provider_unavailable as blocking when nothing was sent', () => {
    expect(provisionLegacyNothingSentError({
      provider: 'kroger',
      exported: 0,
      error: 'provider_unavailable',
      results: unknownRows,
    })).toBe('provider_unavailable');
  });

  it('does not block when at least one line was sent', () => {
    expect(provisionLegacyNothingSentError({
      provider: 'kroger',
      exported: 1,
      sentCount: 1,
      error: 'reconnect_required',
      results: [
        { entryId: 'a', itemId: '1', name: 'Milk', quantity: 1, status: 'sent' },
        ...unknownRows,
      ],
    })).toBeUndefined();
  });

  it('collapses groups so per-item sections stay hidden', () => {
    const groups = groupProvisionResults({
      provider: 'kroger',
      exported: 0,
      error: 'reconnect_required',
      results: unknownRows,
    });
    expect(groups.wholeRequestError).toBe('reconnect_required');
    expect(groups.check).toHaveLength(0);
    expect(groups.N).toBe(0);
  });
});

describe('provisionNothingSent copy (#486)', () => {
  it('uses the manager line and UX reconnect label', () => {
    expect(PROVISION_NOTHING_SENT_LINE).toBe('Nothing was sent to your cart.');
    expect(provisionNothingSentReconnectLabel('Kroger')).toBe('Reconnect Kroger');
  });
});

describe('check button aria labels', () => {
  it('starts with the visible phrase', () => {
    expect(provisionCheckInCartAria('Oats')).toBe("It's in my cart: Oats");
  });
});

describe('unknown store name', () => {
  it('never says "your your store" on whole-request copy', () => {
    const store = provisionStoreLabel(undefined);
    expect(provisionWholeRequestMessage('reconnect_required', store)).toBe(
      "Couldn't connect to your store account. Nothing was sent.",
    );
    expect(provisionWholeRequestMessage('provider_unavailable', store)).toBe(
      PROVISION_ENVELOPE_NOTHING_SENT,
    );
    expect(provisionWholeRequestActionLabel('reconnect_required', store)).toBe('Reconnect your store');
  });
});

describe('provisionItemReasonLine', () => {
  it('formats unavailable rows', () => {
    expect(provisionItemReasonLine('Soup', 'Kroger', 'unavailable')).toBe(
      'Soup: Not available at Kroger right now',
    );
  });
});
