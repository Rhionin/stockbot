import { describe, expect, it } from 'vitest';
import type { ProvisionReport, ShoppingListExportResultEntry } from './types';
import {
  PROVISION_NOTHING_SENT_LINE,
  PROVISION_RESOLVE_UNDO_MS,
  groupProvisionResults,
  mapProvisionEntryResultRow,
  provisionBlockedSendHint,
  provisionCheckInCartAria,
  provisionCheckRowLabel,
  provisionEmptyListNote,
  provisionItemReasonLine,
  provisionLegacyNothingSentError,
  provisionNothingSentReconnectLabel,
  provisionPartialReconnectPrompt,
  provisionProviderUnavailableEnvelope,
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

// The #484 ProvisionReport type no longer declares the legacy top-level `error`; old servers can still send it.
const legacyError = (error: string) => ({ error }) as unknown as Partial<ProvisionReport>;

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
    expect(provisionSendHint('your store')).toBe("Sends these items to your store's cart.");
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

  it('returns halt copy for request_incomplete', () => {
    expect(provisionReasonText(store, 'request_incomplete')).toBe('Not sent. Send again to finish.');
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
      PROVISION_NOTHING_SENT_LINE,
    );
  });
});

describe('provisionSummaryCopy summary-line mutants', () => {
  it('includes nothing-sent only when N and U are both zero', () => {
    expect(provisionSummaryCopy({ N: 0, M: 0, U: 0 }, 'Kroger').lines).toEqual([
      PROVISION_NOTHING_SENT_LINE,
    ]);
    expect(provisionSummaryCopy({ N: 0, M: 0, U: 1 }, 'Kroger').lines).toEqual([
      '1 might not have reached your cart.',
    ]);
    expect(provisionSummaryCopy({ N: 1, M: 0, U: 0 }, 'Kroger').lines).toEqual([
      '1 item sent to your Kroger cart.',
    ]);
    expect(provisionSummaryCopy({ N: 0, M: 2, U: 0 }, 'Kroger').lines).toEqual([
      PROVISION_NOTHING_SENT_LINE,
      "2 couldn't be sent.",
    ]);
  });

  it('emits sent, failure, and unknown lines only when each count is at least 1', () => {
    const all = provisionSummaryCopy({ N: 2, M: 1, U: 3 }, 'Kroger');
    expect(all.lines).toEqual([
      '2 items sent to your Kroger cart.',
      "1 couldn't be sent.",
      '3 might not have reached your cart.',
    ]);
    expect(provisionSummaryCopy({ N: 2, M: 0, U: 0 }, 'Kroger').lines).toEqual([
      '2 items sent to your Kroger cart.',
    ]);
    expect(provisionSummaryCopy({ N: 0, M: 3, U: 0 }, 'Kroger').lines).toEqual([
      PROVISION_NOTHING_SENT_LINE,
      "3 couldn't be sent.",
    ]);
  });

  it('uses singular wording for N, M, and U of 1', () => {
    expect(provisionSummaryCopy({ N: 1, M: 1, U: 1 }, 'Kroger').lines).toEqual([
      '1 item sent to your Kroger cart.',
      "1 couldn't be sent.",
      '1 might not have reached your cart.',
    ]);
  });

  it('shows the cart confirm line only when N plus U is at least 1', () => {
    expect(provisionSummaryCopy({ N: 0, M: 2, U: 0 }, 'Kroger').cartConfirmLine).toBeUndefined();
    expect(provisionSummaryCopy({ N: 1, M: 0, U: 0 }, 'Kroger').cartConfirmLine).toContain("doesn't confirm each item");
    expect(provisionSummaryCopy({ N: 0, M: 0, U: 1 }, 'Kroger').cartConfirmLine).toContain("doesn't confirm each item");
  });
});

describe('groupProvisionResults N/M/U counts', () => {
  it('matches partitioned row counts', () => {
    const report: ProvisionReport = {
      provider: 'kroger',
      exported: 2,
      results: [
        { entryId: 'a', itemId: '1', name: 'Milk', quantity: 1, status: 'sent' },
        { entryId: 'b', itemId: '2', name: 'Bread', quantity: 1, status: 'sent' },
        { entryId: 'c', itemId: '3', name: 'Jam', quantity: 1, status: 'skipped', reason: 'no_upc' },
        { entryId: 'd', itemId: '4', name: 'Oats', quantity: 1, status: 'unknown' },
      ],
    };
    const groups = groupProvisionResults(report);
    expect(groups.N).toBe(2);
    expect(groups.M).toBe(1);
    expect(groups.U).toBe(1);
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
      ...legacyError('reconnect_required'),
      results: unknownRows,
    })).toBe('reconnect_required');
  });

  it('treats provider_unavailable as blocking when nothing was sent', () => {
    expect(provisionLegacyNothingSentError({
      provider: 'kroger',
      exported: 0,
      ...legacyError('provider_unavailable'),
      results: unknownRows,
    })).toBe('provider_unavailable');
  });

  it('does not block when at least one line was sent', () => {
    expect(provisionLegacyNothingSentError({
      provider: 'kroger',
      exported: 1,
      sentCount: 1,
      ...legacyError('reconnect_required'),
      results: [
        { entryId: 'a', itemId: '1', name: 'Milk', quantity: 1, status: 'sent' },
        ...unknownRows,
      ],
    })).toBeUndefined();
  });

  it('does not block when a row was sent even without sentCount or exported', () => {
    expect(provisionLegacyNothingSentError({
      provider: 'kroger',
      exported: 0,
      ...legacyError('reconnect_required'),
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
      ...legacyError('reconnect_required'),
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
      provisionProviderUnavailableEnvelope(store),
    );
    expect(provisionProviderUnavailableEnvelope('Kroger')).toBe(
      "Nothing was sent. Kroger isn't responding right now.",
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

  it('uses reconnect copy when reason is reconnect_required', () => {
    expect(provisionItemReasonLine('Bread', 'Kroger', 'reconnect_required', { status: 'failed', reason: 'reconnect_required' })).toBe(
      'Bread: Reconnect Kroger to send this',
    );
  });

  it('falls back to reconnect copy on failed rows when reconnectRequired and reason is omitted', () => {
    expect(provisionItemReasonLine('Bread', 'Kroger', undefined, { status: 'failed' }, true)).toBe(
      'Bread: Reconnect Kroger to send this',
    );
    expect(provisionItemReasonLine('Soup', 'Kroger', 'batch_rejected', { status: 'failed', reason: 'batch_rejected' }, true)).toBe(
      "Soup: Kroger didn't accept this item",
    );
  });

  it('uses halt copy for failed request_incomplete', () => {
    const row = {
      entryId: 'e1',
      itemId: 'i1',
      name: 'Synthetic line',
      quantity: 1,
      status: 'failed',
      reason: 'request_incomplete',
    } satisfies ShoppingListExportResultEntry;
    expect(provisionItemReasonLine('Synthetic line', 'Example store', row.reason, row)).toBe(
      'Synthetic line: Not sent. Send again to finish.',
    );
  });
});

describe('unknown request_incomplete check rows', () => {
  const unknownIncomplete = {
    entryId: 'u1',
    itemId: 'i1',
    name: 'Synthetic line U',
    quantity: 1,
    status: 'unknown',
    reason: 'request_incomplete',
  } satisfies ShoppingListExportResultEntry;

  it('stays in the check section with the item name only', () => {
    expect(provisionRowGroup(unknownIncomplete)).toBe('not_confirmed');
    const groups = groupProvisionResults({
      provider: 'example',
      exported: 0,
      results: [unknownIncomplete],
    });
    expect(groups.U).toBe(1);
    expect(groups.M).toBe(0);
    expect(groups.check).toEqual([unknownIncomplete]);
    expect(provisionCheckRowLabel(unknownIncomplete)).toBe('Synthetic line U');
    expect(provisionItemReasonLine('Synthetic line U', 'Example store', 'request_incomplete', unknownIncomplete)).toBe(
      'Synthetic line U',
    );
  });
});

describe('failed request_incomplete export rows', () => {
  const failedIncompleteRow = {
    entryId: 'e1',
    itemId: 'i1',
    name: 'Synthetic line A',
    quantity: 1,
    status: 'failed',
    reason: 'request_incomplete',
  } satisfies ShoppingListExportResultEntry;

  it('groups into the couldnt-be-sent bucket, not the check section', () => {
    expect(provisionRowGroup(failedIncompleteRow)).toBe('not_added');
    const groups = groupProvisionResults({
      provider: 'example',
      exported: 0,
      results: [failedIncompleteRow],
    });
    expect(groups.M).toBe(1);
    expect(groups.U).toBe(0);
    expect(groups.notAdded).toEqual([failedIncompleteRow]);
    expect(groups.check).toEqual([]);
  });

  it('shows the halt copy from provisionReasonText', () => {
    expect(provisionReasonText('Example store', 'request_incomplete')).toBe(
      'Not sent. Send again to finish.',
    );
    expect(provisionItemReasonLine(
      'Synthetic line A',
      'Example store',
      'request_incomplete',
      failedIncompleteRow,
    )).toBe('Synthetic line A: Not sent. Send again to finish.');
  });
});

describe('provisionRowGroup no_per_item_result precedence', () => {
  it('routes no_per_item_result to check even when status is failed', () => {
    // Deliberately off-contract: failed status must not carry no_per_item_result on the wire.
    const mismatchedStatus = {
      entryId: 'e1',
      itemId: 'i1',
      name: 'Synthetic line Z',
      quantity: 1,
      status: 'failed',
      reason: 'no_per_item_result',
    } as unknown as ShoppingListExportResultEntry;
    expect(provisionRowGroup(mismatchedStatus)).toBe('not_confirmed');
    const groups = groupProvisionResults({
      provider: 'example',
      exported: 0,
      results: [mismatchedStatus],
    });
    expect(groups.U).toBe(1);
    expect(groups.M).toBe(0);
  });
});

describe('ShoppingListExportResultEntry fixtures', () => {
  it('accepts failed halt reasons on failed lines', () => {
    const haltReconnect = {
      entryId: 'e1',
      itemId: 'i1',
      name: 'Line A',
      quantity: 1,
      status: 'failed',
      reason: 'reconnect_required',
    } satisfies ShoppingListExportResultEntry;
    const haltIncomplete = {
      entryId: 'e2',
      itemId: 'i2',
      name: 'Line B',
      quantity: 1,
      status: 'failed',
      reason: 'request_incomplete',
    } satisfies ShoppingListExportResultEntry;
    expect(haltReconnect.reason).toBe('reconnect_required');
    expect(haltIncomplete.reason).toBe('request_incomplete');
  });
});

describe('mapProvisionEntryResultRow', () => {
  it('maps unknown outcome with a valid unknown reason', () => {
    expect(mapProvisionEntryResultRow({
      entryId: 'e1',
      itemId: 'i1',
      name: 'Line A',
      quantity: 1,
      outcome: 'unknown',
      outcomeReason: 'no_per_item_result',
    })).toEqual({
      entryId: 'e1',
      itemId: 'i1',
      name: 'Line A',
      quantity: 1,
      status: 'unknown',
      reason: 'no_per_item_result',
    });
  });

  it('omits skip reasons on unknown legacy entries', () => {
    expect(mapProvisionEntryResultRow({
      entryId: 'e1',
      itemId: 'i1',
      name: 'Line A',
      quantity: 1,
      outcome: 'unknown',
      outcomeReason: 'no_upc',
    })).toEqual({
      entryId: 'e1',
      itemId: 'i1',
      name: 'Line A',
      quantity: 1,
      status: 'unknown',
    });
  });

  it('maps failed outcome with a valid failed reason', () => {
    expect(mapProvisionEntryResultRow({
      entryId: 'e2',
      itemId: 'i2',
      name: 'Line B',
      quantity: 1,
      outcome: 'failed',
      outcomeReason: 'request_incomplete',
    })).toEqual({
      entryId: 'e2',
      itemId: 'i2',
      name: 'Line B',
      quantity: 1,
      status: 'failed',
      reason: 'request_incomplete',
    });
  });

  it('omits skip reasons on failed legacy entries', () => {
    expect(mapProvisionEntryResultRow({
      entryId: 'e2',
      itemId: 'i2',
      name: 'Line B',
      quantity: 1,
      outcome: 'failed',
      outcomeReason: 'unavailable',
    })).toEqual({
      entryId: 'e2',
      itemId: 'i2',
      name: 'Line B',
      quantity: 1,
      status: 'failed',
    });
  });
});

describe('provisionPartialReconnectPrompt', () => {
  it('names the store account in the lead sentence', () => {
    expect(provisionPartialReconnectPrompt('Kroger')).toBe(
      'Your Kroger account disconnected before everything was sent. Reconnect, then send again to finish.',
    );
    expect(provisionPartialReconnectPrompt(provisionStoreLabel(undefined))).toBe(
      'Your store account disconnected before everything was sent. Reconnect, then send again to finish.',
    );
  });
});

describe('PROVISION_RESOLVE_UNDO_MS', () => {
  it('is a ten-second undo window', () => {
    expect(PROVISION_RESOLVE_UNDO_MS).toBe(10_000);
  });
});

describe('provisionExportUX review fixes', () => {
  it('starts the confirm line with a capital when the store is unknown', () => {
    const copy = provisionSummaryCopy({ N: 1, M: 0, U: 1 }, provisionStoreLabel(undefined));
    expect(copy.cartConfirmLine).toBe("Your store doesn't confirm each item, so check your cart to make sure everything is there.");
  });

  it('counts legacy sends without inventing rows', () => {
    const groups = groupProvisionResults({ provider: 'kroger', exported: 2 } as ProvisionReport);
    expect(groups.N).toBe(2);
    expect(groups.sentRows).toEqual([]);
  });

});

describe('provisionBlockedSendHint', () => {
  it('uses singular and plural copy', () => {
    expect(provisionBlockedSendHint(1)).toBe("One item didn't save. Fix it below, then send again.");
    expect(provisionBlockedSendHint(2)).toBe("2 items didn't save. Fix them below, then send again.");
  });
});

describe('provisionProviderUnavailableEnvelope', () => {
  it('names the store when known', () => {
    expect(provisionProviderUnavailableEnvelope("Smith's")).toBe("Nothing was sent. Smith's isn't responding right now.");
  });

  it('says Your store when no store is known', () => {
    expect(provisionProviderUnavailableEnvelope(provisionStoreLabel(undefined))).toBe("Nothing was sent. Your store isn't responding right now.");
  });
});
