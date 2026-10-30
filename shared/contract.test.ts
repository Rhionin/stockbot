import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { describe, expect, it } from 'vitest';
import {
  AUTH_NOT_CONFIGURED_SENTENCE,
  BATCH_FAILURE_FALLBACK,
  BODY_TOO_LARGE_SENTENCE,
  BUSY_SENTENCE,
  backfillFailureKind,
  CODE_AUTH_NOT_CONFIGURED,
  batchCardCommitNotice,
  batchFailureBannerText,
  batchFailureNotices,
  CODE_CONFIRMATION_MISMATCH,
  CODE_DIRECTION_UNSET,
  CODE_INTERNAL,
  CODE_NONE_ON_HAND,
  CODE_NOT_FOUND,
  CODE_NOT_PENDING,
  CODE_NOT_STOCK_OUT,
  CODE_PAYLOAD_TOO_LARGE,
  CODE_PRODUCT_UNRESOLVED,
  CODE_PROVIDER_UNAVAILABLE,
  CODE_RATE_LIMITED,
  CODE_SCAN_NOT_FOUND,
  CODE_UNAUTHORIZED,
  CODE_UNITS_ON_HAND,
  CODE_UPDATING,
  CODE_VALIDATION,
  confirmSkipsNothingOnHand,
  failuresAfterConfirm,
  formatPartialRemoval,
  INVALID_BODY_PREFIX,
  INVALID_CREDENTIALS_SENTENCE,
  SESSION_EXPIRED_SENTENCE,
  INVALID_PATH_PREFIX,
  INVALID_REPORT_SENTENCE,
  METHOD_NOT_ALLOWED_SENTENCE,
  MISSING_CREDENTIALS_SENTENCE,
  networkErrorResult,
  NOT_FOUND_SENTENCE,
  NO_SPACE_SENTENCE,
  nothingOnHandBody,
  nothingOnHandPill,
  nothingOnHandStillQueued,
  applyHttpResponse,
  notifyApiSuccess,
  OFFLINE_SENTENCE,
  offlineBannerActive,
  pairStockInMoveId,
  pairUndoBody,
  pairUndoCopy,
  pairUndoHideLine,
  previewReshelves,
  PROVIDER_FIELD,
  PROVIDER_UNAVAILABLE_SENTENCE,
  RATE_LIMITED_SENTENCE,
  raiseAuthNotConfiguredBanner,
  raiseOfflineBanner,
  readOnHand,
  REPORT_TOO_LARGE_SENTENCE,
  scanReportsNothingOnHand,
  SERVER_ERROR_FALLBACK,
  serverNoticeText,
  shelfHasNothingOnHand,
  SIGN_IN_NOT_USED_SENTENCE,
  SIGN_IN_REQUIRED_SENTENCE,
  skippedNothingOnHandBanner,
  SYNC_TOKEN_INVALID_SENTENCE,
  UnreachableError,
  unreachableFrom,
  UPDATING_SENTENCE,
  UPDATING_STATUS,
  inlineFailure,
  CHANGEOVER_BANNER,
  CHANGEOVER_SIGN_IN_NOTE,
  HOUSEHOLD_DELETE_SENTENCE,
  PAIRING_EXPIRED_SENTENCE,
  deviceKindLabel,
  disconnectHubConfirm,
  disconnectedHubNotice,
  hostedErrorSentence,
  HOSTED_ERROR_SENTENCES,
  olderAppDevicesSentence,
  pairingKeyboardFallback,
  pairingPrintBody,
  SCANNER_SETUP_SSID_PREFIX,
  type BackfillCommitResponse,
  type BatchCommitResponse,
  type ScanListEntry,
  type UndoPreview,
} from './contract';

const oneUnnamed = '1 card had fewer on hand than scanned, so only what was on hand was taken out.';
const twoCards = '2 cards had fewer on hand than scanned, so only what was on hand was taken out.';
const wasNamed = 'Only 1 of 3 Tortilla chips was on hand, so 1 was taken out.';
const wereNamed = 'Only 2 of 5 Tortilla chips were on hand, so 2 were taken out.';
const singleWas = 'Only 1 was on hand, so 1 was taken out.';
const singleWere = 'Only 2 were on hand, so 2 were taken out.';

const named = { removedCount: 1, requestedCount: 3 };
const full = { removedCount: 2, requestedCount: 2 };

describe('formatPartialRemoval', () => {
  it.each([
    { name: 'null body', body: null, line: null },
    { name: 'undefined body', body: undefined, line: null },
    { name: 'single confirm missing both counts', body: { updatedCount: 1 }, line: null },
    { name: 'single confirm missing removed', body: { requestedCount: 3 }, line: null },
    { name: 'single confirm missing requested', body: { removedCount: 1 }, line: null },
    { name: 'single confirm rejects a string count', body: { removedCount: '1', requestedCount: 3 }, line: null },
    { name: 'single confirm rejects a fractional count', body: { removedCount: 1.5, requestedCount: 3 }, line: null },
    { name: 'single confirm rejects zero removed', body: { removedCount: 0, requestedCount: 3 }, line: null },
    { name: 'single confirm rejects an equal count', body: { removedCount: 3, requestedCount: 3 }, line: null },
    { name: 'single confirm rejects a larger removed count', body: { removedCount: 4, requestedCount: 3 }, line: null },
    { name: 'single confirm of one', body: { removedCount: 1, requestedCount: 3 }, line: singleWas },
    { name: 'single confirm of more than one', body: { removedCount: 2, requestedCount: 5 }, line: singleWere },
    {
      name: 'undefined stockOutCounts uses the single sentence',
      body: { updatedCount: 1, removedCount: 1, requestedCount: 3, stockOutCounts: undefined },
      line: singleWas,
    },
    {
      name: 'an empty map adds nothing',
      body: { updatedCount: 1, removedCount: 1, requestedCount: 3, stockOutCounts: {} },
      line: null,
    },
    {
      name: 'a full map does not fall through to the single sentence',
      body: { updatedCount: 1, removedCount: 1, requestedCount: 3, stockOutCounts: { full } },
      line: null,
    },
    {
      name: 'count 1 with no name',
      body: {
        updatedCount: 1,
        committedIds: ['scan-1'],
        stockOutCounts: { 'scan-1': named },
      },
      names: { 'scan-1': '' },
      line: oneUnnamed,
    },
    {
      name: 'a missing name uses the count sentence',
      body: { updatedCount: 1, stockOutCounts: { 'scan-1': named } },
      line: oneUnnamed,
    },
    {
      name: 'one named card uses was',
      body: { updatedCount: 1, stockOutCounts: { 'scan-1': named } },
      names: { 'scan-1': 'Tortilla chips' },
      line: wasNamed,
    },
    {
      name: 'one named card with more than one removed uses were',
      body: { updatedCount: 1, stockOutCounts: { 'scan-1': { removedCount: 2, requestedCount: 5 } } },
      names: { 'scan-1': 'Tortilla chips' },
      line: wereNamed,
    },
    {
      name: 'two short cards use the plural count sentence',
      body: {
        updatedCount: 2,
        stockOutCounts: {
          'scan-1': named,
          'scan-2': { removedCount: 2, requestedCount: 4 },
        },
      },
      names: { 'scan-1': 'Tortilla chips', 'scan-2': 'Rolled oats' },
      line: twoCards,
    },
    {
      name: 'a null count is skipped',
      body: { updatedCount: 2, stockOutCounts: { bad: null, 'scan-1': named } },
      names: { 'scan-1': 'Tortilla chips' },
      line: wasNamed,
    },
    {
      name: 'a non-object count is skipped',
      body: { updatedCount: 2, stockOutCounts: { bad: 'nope', 'scan-1': named } },
      names: { 'scan-1': 'Tortilla chips' },
      line: wasNamed,
    },
    {
      name: 'a count missing a field is skipped',
      body: { updatedCount: 2, stockOutCounts: { bad: { removedCount: 1 }, 'scan-1': named } },
      names: { 'scan-1': 'Tortilla chips' },
      line: wasNamed,
    },
    {
      name: 'zero removed is not a short card',
      body: { updatedCount: 2, stockOutCounts: { zero: { removedCount: 0, requestedCount: 3 }, 'scan-1': named } },
      names: { 'scan-1': 'Tortilla chips', zero: 'Empty' },
      line: wasNamed,
    },
    {
      name: 'an equal count is not a short card',
      body: { updatedCount: 2, stockOutCounts: { full, 'scan-1': named } },
      names: { 'scan-1': 'Tortilla chips', full: 'Batch Full' },
      line: wasNamed,
    },
    {
      name: 'a larger removed count is not a short card',
      body: {
        updatedCount: 2,
        stockOutCounts: { over: { removedCount: 4, requestedCount: 3 }, 'scan-1': named },
      },
      names: { 'scan-1': 'Tortilla chips' },
      line: wasNamed,
    },
  ])('$name', ({ body, names, line }) => {
    expect(formatPartialRemoval(
      body as BatchCommitResponse | null | undefined,
      names as Readonly<Record<string, string>> | undefined,
    )).toBe(line);
  });
});

const pillRows: { name: string; count: number; pill: string | null; skip: string | null }[] = [
  { name: 'none', count: 0, pill: null, skip: null },
  { name: 'one', count: 1, pill: '1 with nothing on hand', skip: 'Confirm skips the 1 with nothing on hand.' },
  { name: 'two', count: 2, pill: '2 with nothing on hand', skip: 'Confirm skips the 2 with nothing on hand.' },
];

const otherFailureCodes = [
  { code: 'instance_gone', message: 'That one was already taken out. Pick another.' },
  { code: 'not_pending', message: 'This scan was already handled.' },
  { code: 'product_unresolved', message: 'Choose which item this is first.' },
  { code: 'no_item', message: 'Which item is this?' },
  { code: 'link_target_gone', message: "That item isn't available anymore. Pick another." },
  { code: 'scan_not_found', message: 'This scan is no longer in the queue.' },
  { code: 'direction_unset', message: 'Choose stock in or stock out first.' },
  { code: 'internal', message: 'Something went wrong. Try again.' },
] as const;

describe('batch failure banner', () => {
  it.each([
    { name: 'busy and nothing committed', code: 'busy', message: BUSY_SENTENCE, committed: 0, banner: BUSY_SENTENCE, card: '' },
    { name: 'busy and one committed', code: 'busy', message: BUSY_SENTENCE, committed: 1, banner: `1 confirmed. ${BUSY_SENTENCE}`, card: '' },
    { name: 'busy and many committed', code: 'busy', message: BUSY_SENTENCE, committed: 4, banner: `4 confirmed. ${BUSY_SENTENCE}`, card: '' },
    { name: 'no_space and nothing committed', code: 'no_space', message: NO_SPACE_SENTENCE, committed: 0, banner: NO_SPACE_SENTENCE, card: '' },
    { name: 'no_space and one committed', code: 'no_space', message: NO_SPACE_SENTENCE, committed: 1, banner: `1 confirmed. ${NO_SPACE_SENTENCE}`, card: '' },
    { name: 'no_space and many committed', code: 'no_space', message: NO_SPACE_SENTENCE, committed: 3, banner: `3 confirmed. ${NO_SPACE_SENTENCE}`, card: '' },
    ...otherFailureCodes.map((row) => ({
      name: row.code,
      code: row.code,
      message: row.message,
      committed: 2,
      banner: BATCH_FAILURE_FALLBACK,
      card: row.message,
    })),
  ])('$name', ({ code, message, committed, banner, card }) => {
    const notices = [{ id: 'scan-fail', code, message }];
    expect(batchFailureBannerText(notices, committed)).toBe(banner);
    expect(batchCardCommitNotice(code, message)).toBe(card);
  });

  it('an amber-only batch has no failure banner', () => {
    expect(batchFailureBannerText([{ code: 'none_on_hand', message: 'Nothing on hand to take out.' }], 1)).toBeNull();
    expect(batchCardCommitNotice('none_on_hand', 'Nothing on hand to take out.')).toBe('');
  });

  it.each([
    { name: 'busy empty message', code: 'busy', message: '', committed: 0, banner: BUSY_SENTENCE },
    { name: 'busy blank message', code: 'busy', message: '   ', committed: 0, banner: BUSY_SENTENCE },
    { name: 'busy empty message and one committed', code: 'busy', message: '', committed: 1, banner: `1 confirmed. ${BUSY_SENTENCE}` },
    { name: 'no_space empty message', code: 'no_space', message: '', committed: 0, banner: NO_SPACE_SENTENCE },
    { name: 'no_space blank message', code: 'no_space', message: ' \n ', committed: 0, banner: NO_SPACE_SENTENCE },
    { name: 'no_space blank message and many committed', code: 'no_space', message: '  ', committed: 3, banner: `3 confirmed. ${NO_SPACE_SENTENCE}` },
  ])('$name falls back to the storage sentence', ({ code, message, committed, banner }) => {
    expect(batchFailureBannerText([{ code, message }], committed)).toBe(banner);
    expect(batchCardCommitNotice(code, message)).toBe('');
  });

  it('a stopped batch omits committedIds and has no confirmed prefix', () => {
    const body: BatchCommitResponse = {
      updatedCount: 0,
      failedIds: ['scan-fail'],
      failedReasons: ['The pantry database is locked.'],
      failedCodes: ['busy'],
    };
    expect(body.committedIds).toBeUndefined();
    const notices = batchFailureNotices(body);
    expect(notices).toEqual([
      { id: 'scan-fail', message: 'The pantry database is locked.', code: 'busy' },
    ]);
    expect(batchFailureBannerText(notices, body.committedIds?.length ?? 0)).toBe('The pantry database is locked.');
    expect(batchCardCommitNotice('busy', 'The pantry database is locked.')).toBe('');
  });

  it.each([
    {
      name: 'all busy then a confirm leaves nothing to say',
      before: [
        { id: 'scan-a', code: 'busy', message: BUSY_SENTENCE },
        { id: 'scan-b', code: 'busy', message: BUSY_SENTENCE },
      ],
      confirmedId: 'scan-a',
      banner: null,
    },
    {
      name: 'busy plus no_item leaves the fallback',
      before: [
        { id: 'scan-a', code: 'busy', message: BUSY_SENTENCE },
        { id: 'scan-b', code: 'no_item', message: 'Which item is this?' },
      ],
      confirmedId: 'scan-a',
      banner: BATCH_FAILURE_FALLBACK,
    },
    {
      name: 'busy plus no_item plus not_pending falls back',
      before: [
        { id: 'scan-a', code: 'busy', message: BUSY_SENTENCE },
        { id: 'scan-b', code: 'no_item', message: 'Which item is this?' },
        { id: 'scan-c', code: 'not_pending', message: 'This scan was already handled.' },
      ],
      confirmedId: 'scan-a',
      banner: BATCH_FAILURE_FALLBACK,
    },
    {
      name: 'the last item-specific failure leaves no banner',
      before: [{ id: 'scan-b', code: 'no_item', message: 'Which item is this?' }],
      confirmedId: 'scan-b',
      banner: null,
    },
  ])('$name', ({ before, confirmedId, banner }) => {
    expect(batchFailureBannerText(before, 0)).not.toBeNull();
    const after = failuresAfterConfirm(before, confirmedId);
    expect(batchFailureBannerText(after, 0)).toBe(banner);
  });

  it('matching item codes stay on the cards and the banner is the fallback', () => {
    const notices = [
      { code: 'no_item', message: '' },
      { code: 'no_item', message: 'Which item is this?' },
    ];
    expect(batchFailureBannerText(notices, 0)).toBe(BATCH_FAILURE_FALLBACK);
    expect(batchCardCommitNotice('no_item', 'Which item is this?')).toBe('Which item is this?');
  });

  it('an item failure beside busy keeps the banner on the fallback', () => {
    const notices = [
      { code: 'busy', message: BUSY_SENTENCE },
      { code: 'not_pending', message: 'This scan was already handled.' },
    ];
    expect(batchFailureBannerText(notices, 2)).toBe(BATCH_FAILURE_FALLBACK);
    expect(batchCardCommitNotice('busy', BUSY_SENTENCE)).toBe('');
    expect(batchCardCommitNotice('not_pending', 'This scan was already handled.')).toBe('This scan was already handled.');
  });

  it('a busy card beside amber uses the busy sentence once', () => {
    const notices = [
      { code: 'none_on_hand', message: 'Nothing on hand to take out.' },
      { code: 'busy', message: BUSY_SENTENCE },
    ];
    expect(batchFailureBannerText(notices, 2)).toBe(`2 confirmed. ${BUSY_SENTENCE}`);
    expect(batchCardCommitNotice('busy', BUSY_SENTENCE)).toBe('');
    expect(batchCardCommitNotice('none_on_hand', 'Nothing on hand to take out.')).toBe('');
  });
});

describe('app-facing error codes', () => {
  it('names the updating and provider responses', () => {
    expect(CODE_UPDATING).toBe('updating');
    expect(UPDATING_SENTENCE).toBe('Updating. Try again in a minute.');
    expect(UPDATING_STATUS).toBe('migrating');
    expect(CODE_PROVIDER_UNAVAILABLE).toBe('provider_unavailable');
    expect(PROVIDER_FIELD).toBe('provider');
    expect(PROVIDER_UNAVAILABLE_SENTENCE).toBe("{provider} isn't responding. Try again later.");
  });

  it('uses the hosted names and keeps the current sentences', () => {
    expect(CODE_UNAUTHORIZED).toBe('unauthorized');
    expect(SIGN_IN_REQUIRED_SENTENCE).toBe('Sign in required.');
    expect(INVALID_CREDENTIALS_SENTENCE).toBe('The username or password is incorrect.');
    expect(SESSION_EXPIRED_SENTENCE).toBe('Sign in to continue.');
    expect(SYNC_TOKEN_INVALID_SENTENCE).toBe('The sync token is not valid.');
    expect(CODE_NOT_FOUND).toBe('not_found');
    expect(SIGN_IN_NOT_USED_SENTENCE).toBe('Sign-in is not used on this address.');
    expect(NOT_FOUND_SENTENCE).toBe('Not found.');
    expect(CODE_VALIDATION).toBe('validation');
    expect(MISSING_CREDENTIALS_SENTENCE).toBe('Enter your username and password.');
    expect(INVALID_BODY_PREFIX).toBe('invalid request body: ');
    expect(INVALID_PATH_PREFIX).toBe('invalid path parameters: ');
    expect(INVALID_REPORT_SENTENCE).toBe('invalid report');
    expect(CODE_CONFIRMATION_MISMATCH).toBe('confirmation_mismatch');
    expect(CODE_RATE_LIMITED).toBe('rate_limited');
    expect(RATE_LIMITED_SENTENCE).toBe('Too many sign-in attempts. Try again later.');
    expect(CODE_PAYLOAD_TOO_LARGE).toBe('payload_too_large');
    expect(BODY_TOO_LARGE_SENTENCE).toBe('request body is too large');
    expect(REPORT_TOO_LARGE_SENTENCE).toBe('report is too large');
    expect(METHOD_NOT_ALLOWED_SENTENCE).toBe('Method not allowed.');
    expect(CODE_INTERNAL).toBe('internal');
    expect(SERVER_ERROR_FALLBACK).toBe('Something went wrong. Try again.');
  });
});

const authNotConfigured = JSON.parse(
  readFileSync(join(process.cwd(), '../testdata/contract/auth_not_configured.json'), 'utf8'),
) as { code: string };

describe('auth_not_configured', () => {
  it('is the sign-in code', () => {
    expect(CODE_AUTH_NOT_CONFIGURED).toBe(authNotConfigured.code);
  });

  it('uses a straight apostrophe in the banner sentence', () => {
    expect(AUTH_NOT_CONFIGURED_SENTENCE).toBe("Sign-in isn't set up yet.");
    expect(AUTH_NOT_CONFIGURED_SENTENCE.charCodeAt(AUTH_NOT_CONFIGURED_SENTENCE.indexOf("'"))).toBe(39);
  });
});

describe('nothing on hand copy', () => {
  it.each(pillRows)('$name', ({ count, pill, skip }) => {
    expect(nothingOnHandPill(count)).toBe(pill);
    expect(confirmSkipsNothingOnHand(count)).toBe(skip);
  });

  it.each([
    { name: 'missing from the list', onHand: undefined, empty: false },
    { name: 'zero on hand', onHand: 0, empty: true },
    { name: 'one on hand', onHand: 1, empty: false },
  ])('$name', ({ onHand, empty }) => {
    expect(shelfHasNothingOnHand(onHand)).toBe(empty);
  });

  it.each([
    { name: 'no units', count: 0, body: 'Stockbot thinks you have none. If you had 0 anyway, this adds them and takes them out in one step.' },
    { name: 'one unit', count: 1, body: 'Stockbot thinks you have none. If you had one anyway, this adds it and takes it out in one step.' },
    { name: 'two units', count: 2, body: 'Stockbot thinks you have none. If you had 2 anyway, this adds them and takes them out in one step.' },
    { name: 'four units', count: 4, body: 'Stockbot thinks you have none. If you had 4 anyway, this adds them and takes them out in one step.' },
  ])('body for $name', ({ count, body }) => {
    expect(nothingOnHandBody(count)).toBe(body);
  });

  it.each([
    { n: 0, line: '0 with nothing on hand are still in the queue.' },
    { n: 1, line: '1 with nothing on hand is still in the queue.' },
    { n: 2, line: '2 with nothing on hand are still in the queue.' },
  ])('still queued $n', ({ n, line }) => {
    expect(nothingOnHandStillQueued(n)).toBe(line);
  });

  it.each([
    { name: 'onHand 0', onHand: 0 as number | undefined, direction: 'stock_out' as const, offer: true },
    { name: 'onHand 3', onHand: 3 as number | undefined, direction: 'stock_out' as const, offer: false },
    { name: 'omitted on a stock-out', onHand: undefined, direction: 'stock_out' as const, offer: false },
    { name: 'omitted on a stock-in', onHand: undefined, direction: 'stock_in' as const, offer: false },
  ])('$name', ({ onHand, direction, offer }) => {
    const entry: ScanListEntry = {
      id: 'scan-row',
      userId: 'user-1',
      barcode: '111',
      scannedAt: '2026-10-18T16:00:00.000Z',
      direction,
      unitCount: 1,
      expiresAt: null,
      status: 'pending',
      productId: 'prod-row',
      committedAt: null,
      createdAt: '2026-10-18T16:00:00.000Z',
      ...(onHand === undefined ? {} : { onHand }),
    };
    expect(scanReportsNothingOnHand(entry)).toBe(offer);
  });

  it.each([
    {
      name: 'one taken out and one still queued',
      takenOut: 1,
      stillQueued: 1,
      line: '1 taken out. 1 with nothing on hand is still in the queue. Each one has its own Add it and take it out button.',
    },
    {
      name: 'two taken out and three still queued',
      takenOut: 2,
      stillQueued: 3,
      line: '2 taken out. 3 with nothing on hand are still in the queue. Each one has its own Add it and take it out button.',
    },
    { name: 'nothing taken out', takenOut: 0, stillQueued: 1, line: null },
    { name: 'none still queued', takenOut: 1, stillQueued: 0, line: null },
  ])('$name', ({ takenOut, stillQueued, line }) => {
    expect(skippedNothingOnHandBanner(takenOut, stillQueued)).toBe(line);
  });

  const shelfRow = (): ScanListEntry => ({
    id: 'scan-row',
    userId: 'user-1',
    barcode: '111',
    scannedAt: '2026-10-18T16:00:00.000Z',
    direction: 'stock_out',
    unitCount: 1,
    expiresAt: null,
    status: 'pending',
    productId: 'prod-row',
    committedAt: null,
    createdAt: '2026-10-18T16:00:00.000Z',
  });

  it.each([
    { name: 'a word', onHand: '0' },
    { name: 'a fraction', onHand: 0.5 },
    { name: 'null', onHand: null },
    { name: 'true', onHand: true },
  ])('a malformed onHand ($name) is not an empty shelf', ({ onHand }) => {
    expect(readOnHand({ onHand })).toBeUndefined();
    expect(scanReportsNothingOnHand({ ...shelfRow(), onHand: readOnHand({ onHand }) })).toBe(false);
    expect(scanReportsNothingOnHand({ ...shelfRow(), onHand: onHand as never })).toBe(false);
  });
});

const hideLine = 'Sour cream will be hidden from your inventory again, since none are on hand.';
const at = '2026-10-18T16:40:00.000Z';

const effect = (reshelvesOnUndo: boolean, stockInMoveId = 'move-cream-in'): UndoPreview['effects'][number] => ({
  itemId: 'item-sour-cream',
  productId: 'prod-sour-cream',
  name: 'Sour cream',
  stockInMoveId,
  stockOutMoveId: 'move-cream-out',
  onHand: 0,
  reshelvesOnUndo,
});

const previewRows: { name: string; preview: UndoPreview; hide: boolean }[] = [
  { name: 'true', preview: { effects: [effect(true)] }, hide: true },
  { name: 'false', preview: { effects: [effect(false)] }, hide: false },
  { name: 'no matching effect', preview: { effects: [effect(true, 'move-other')] }, hide: false },
  {
    name: 'only one of two effects',
    preview: {
      effects: [
        {
          itemId: 'item-milk',
          productId: 'prod-milk',
          name: 'Milk',
          stockInMoveId: 'move-milk-in',
          stockOutMoveId: 'move-milk-out',
          onHand: 1,
          reshelvesOnUndo: true,
        },
        effect(true),
      ],
    },
    hide: true,
  },
];

describe('pair undo confirmation', () => {
  it.each(previewRows)('shows the hide line only when the preview effect is $name', ({ preview, hide }) => {
    expect(previewReshelves(preview, 'move-cream-in')).toBe(hide);
    const copy = pairUndoCopy('Sour cream', hide ? true : undefined);
    expect(copy.title).toBe('Undo "Add it and take it out"?');
    expect(copy.body).toBe('This removes both the add and the take-out for Sour cream, as if neither happened.');
    expect(copy.body).toBe(pairUndoBody('Sour cream'));
    expect(copy.hideLine).toBe(hide ? hideLine : '');
    if (hide) expect(copy.hideLine).toBe(pairUndoHideLine('Sour cream'));
    expect(copy.confirm).toBe('Undo');
    expect(copy.cancel).toBe('Cancel');
    expect(copy.body).not.toMatch(/\d/);
    expect(`${copy.body} ${copy.hideLine}`).not.toMatch(/pace|history|unit/i);
  });

  it('leaves the hide line out when the preview never arrives', () => {
    expect(previewReshelves(undefined, 'move-cream-in')).toBe(false);
    expect(pairUndoCopy('Sour cream', undefined).hideLine).toBe('');
  });

  const stockIn = { id: 'move-cream-in', source: 'backfill', at, productId: 'prod-sour' };
  const stockOut = { id: 'move-cream-out', source: 'scan', at, productId: 'prod-sour' };
  const pairMoves = [stockOut, stockIn];

  it.each([
    { name: 'the stock-in row', move: stockIn, moves: pairMoves, id: 'move-cream-in' },
    { name: 'the paired stock-out', move: stockOut, moves: pairMoves, id: 'move-cream-in' },
    {
      name: 'a different product',
      move: { id: 'move-other-out', source: 'scan', at, productId: 'prod-other' },
      moves: pairMoves,
      id: 'move-other-out',
    },
    {
      name: 'a different time',
      move: { id: 'move-later-out', source: 'scan', at: '2026-10-19T16:40:00.000Z', productId: 'prod-sour' },
      moves: pairMoves,
      id: 'move-later-out',
    },
  ])('previews the backfill stock-in id for $name', ({ move, moves, id }) => {
    expect(pairStockInMoveId(move, moves)).toBe(id);
  });

  it.each([
    {
      name: 'the stock-out id of this move',
      effectId: { stockInMoveId: 'move-other', stockOutMoveId: 'move-cream-in' },
      reshelvesOnUndo: true,
      hide: true,
    },
    {
      name: 'a different move',
      effectId: { stockInMoveId: 'move-other', stockOutMoveId: 'move-else' },
      reshelvesOnUndo: true,
      hide: false,
    },
    {
      name: 'this move when the undo does not re-shelve',
      effectId: { stockInMoveId: 'move-cream-in', stockOutMoveId: 'move-cream-out' },
      reshelvesOnUndo: false,
      hide: false,
    },
  ])('matches the preview effect for $name', ({ effectId, reshelvesOnUndo, hide }) => {
    expect(previewReshelves({
      effects: [{
        itemId: 'item-sour-cream',
        productId: 'prod-sour-cream',
        name: 'Sour cream',
        onHand: 0,
        reshelvesOnUndo,
        ...effectId,
      }],
    }, 'move-cream-in')).toBe(hide);
  });
});

const failureKinds: { code: string | undefined; kind: string }[] = [
  { code: CODE_PRODUCT_UNRESOLVED, kind: 'define' },
  { code: CODE_UNITS_ON_HAND, kind: 'stock-out' },
  { code: CODE_NOT_PENDING, kind: 'reload' },
  { code: CODE_SCAN_NOT_FOUND, kind: 'reload' },
  { code: CODE_NOT_STOCK_OUT, kind: 'sentence' },
  { code: CODE_DIRECTION_UNSET, kind: 'sentence' },
  { code: CODE_NONE_ON_HAND, kind: 'sentence' },
  { code: CODE_INTERNAL, kind: 'sentence' },
  { code: 'anything-else', kind: 'sentence' },
  { code: undefined, kind: 'sentence' },
];

const backfillCommit: BackfillCommitResponse = {
  removed: 2,
  backfilled: 2,
  itemId: 'item-sour-cream',
  backfill: {
    stockInMoveId: 'move-cream-in',
    stockOutMoveId: 'move-cream-out',
  },
};

describe('backfill commit response', () => {
  it('returns the five fields, with the in move then the out move', () => {
    expect(backfillCommit).toEqual({
      removed: 2,
      backfilled: 2,
      itemId: 'item-sour-cream',
      backfill: {
        stockInMoveId: 'move-cream-in',
        stockOutMoveId: 'move-cream-out',
      },
    });
    expect(Object.keys(backfillCommit)).toEqual(['removed', 'backfilled', 'itemId', 'backfill']);
    expect(Object.keys(backfillCommit.backfill)).toEqual(['stockInMoveId', 'stockOutMoveId']);
  });
});

describe('batch commit response', () => {
  it('matches the partial stock-out body and never claims a backfill', () => {
    const body: BatchCommitResponse = {
      updatedCount: 4,
      committedIds: ['scan-batch-full', 'scan-batch-partial', 'scan-batch-in'],
      failedIds: ['scan-batch-none'],
      failedReasons: ['Nothing on hand to take out.'],
      failedCodes: ['none_on_hand'],
      stockOutCounts: {
        'scan-batch-full': { removedCount: 2, requestedCount: 2 },
        'scan-batch-partial': { removedCount: 1, requestedCount: 3 },
      },
    };
    expect(body.failedCodes).toEqual(['none_on_hand']);
    expect(body).not.toHaveProperty('backfill');
    expect(body).not.toHaveProperty('backfilled');
    expect(body).not.toHaveProperty('removed');
    expect(JSON.stringify(body)).not.toContain('stockInMoveId');
    expect(JSON.stringify(body)).not.toContain('stockOutMoveId');
  });
});

describe('backfill failure kind', () => {
  it.each(failureKinds)('$code is $kind', ({ code, kind }) => {
    expect(backfillFailureKind(code)).toBe(kind);
  });
});

const offline = { kind: 'network' as const, message: OFFLINE_SENTENCE };

describe('networkErrorResult', () => {
  it('uses a straight apostrophe in the one sentence', () => {
    expect(OFFLINE_SENTENCE).toBe("Can't reach Stockbot. Check your connection and try again.");
    expect(OFFLINE_SENTENCE.charCodeAt(OFFLINE_SENTENCE.indexOf("'"))).toBe(39);
  });

  it.each([
    { name: 'fetch TypeError', error: new TypeError('Failed to fetch') },
    { name: 'phone network error', error: new TypeError('Network request failed') },
    { name: 'plain failed to fetch', error: new Error('Failed to fetch') },
    { name: 'plain network request failed', error: new Error('Network request failed') },
    { name: 'safari load failed', error: new TypeError('Load failed') },
    { name: 'abort', error: new DOMException('The operation was aborted.', 'AbortError') },
    { name: 'timeout', error: new DOMException('The operation was aborted due to timeout', 'TimeoutError') },
    { name: 'dns', error: Object.assign(new Error('getaddrinfo ENOTFOUND stockbot.example'), { code: 'ENOTFOUND' }) },
    { name: 'connection refused', error: Object.assign(new Error('connect ECONNREFUSED'), { code: 'ECONNREFUSED' }) },
    {
      name: 'wrapped dns failure',
      error: new Error('fetch failed', { cause: Object.assign(new Error('getaddrinfo'), { code: 'EAI_AGAIN' }) }),
    },
  ])('$name', ({ error }) => {
    expect(networkErrorResult(error)).toEqual(offline);
    expect(unreachableFrom(error)).toMatchObject({ name: 'UnreachableError', kind: 'network', message: OFFLINE_SENTENCE });
  });

  it.each([
    { name: 'server sentence', error: new Error('Which item is this?') },
    { name: 'unrelated error', error: new Error('Network error') },
    { name: 'invalid header', error: new TypeError("Failed to construct 'Headers': Invalid name") },
    { name: 'type error name only', error: Object.assign(new Error('Illegal invocation'), { name: 'TypeError' }) },
    { name: 'quota', error: new DOMException('The quota has been exceeded.', 'QuotaExceededError') },
    { name: 'bad json', error: new SyntaxError('Unexpected token') },
    { name: 'non-error rejection', error: { statusCode: 500 } },
    { name: 'null', error: null },
  ])('$name is not a transport failure', ({ error }) => {
    expect(networkErrorResult(error)).toBeNull();
    expect(unreachableFrom(error)).toBeNull();
  });

  it('returns the same result for an unreachable error', () => {
    const error = new UnreachableError();
    expect(error.result).toEqual(offline);
    expect(networkErrorResult(error)).toBe(error.result);
    expect(unreachableFrom(error)).toBe(error);
  });
});

describe('inlineFailure', () => {
  it.each([
    { name: 'unreachable', error: new UnreachableError(), fallback: 'Unable to load history.', message: null },
    { name: 'server sentence', error: new Error('That scan is no longer pending.'), fallback: 'Unable to load history.', message: 'That scan is no longer pending.' },
    { name: 'empty message', error: new Error(''), fallback: 'Unable to load history.', message: '' },
    { name: 'not an error', error: 'down', fallback: 'Unable to load history.', message: 'Unable to load history.' },
    { name: 'plain network error', error: new Error('Network error'), fallback: 'Unable to load history.', message: 'Network error' },
  ])('$name', ({ error, fallback, message }) => {
    expect(inlineFailure(error, fallback)).toBe(message);
  });
});

describe('offline banner', () => {
  it('stays up until a success', () => {
    expect(offlineBannerActive()).toBe(false);
    raiseOfflineBanner();
    expect(offlineBannerActive()).toBe(true);
    notifyApiSuccess();
    expect(offlineBannerActive()).toBe(false);
  });

  it('clears the sign-in notice on success too', () => {
    raiseAuthNotConfiguredBanner();
    expect(serverNoticeText()).toBe(AUTH_NOT_CONFIGURED_SENTENCE);
    notifyApiSuccess();
    expect(serverNoticeText()).toBe('');
  });

  it('trims the code before choosing the banner', () => {
    expect(applyHttpResponse(503, `  ${CODE_AUTH_NOT_CONFIGURED}  `)).toBe('auth');
    expect(serverNoticeText()).toBe(AUTH_NOT_CONFIGURED_SENTENCE);
    expect(applyHttpResponse(503, '   ')).toBe('offline');
    expect(serverNoticeText()).toBe(OFFLINE_SENTENCE);
    notifyApiSuccess();
  });

  it('shows updating until offline, no_space, or a success clears it', () => {
    expect(applyHttpResponse(503, `  ${CODE_UPDATING}  `)).toBe('updating');
    expect(serverNoticeText()).toBe(UPDATING_SENTENCE);
    expect(offlineBannerActive()).toBe(false);

    expect(applyHttpResponse(503, 'no_space')).toBe('stockbot');
    expect(serverNoticeText()).toBe('');

    expect(applyHttpResponse(503, CODE_UPDATING)).toBe('updating');
    expect(applyHttpResponse(503, '')).toBe('offline');
    expect(serverNoticeText()).toBe(OFFLINE_SENTENCE);

    expect(applyHttpResponse(503, CODE_UPDATING)).toBe('updating');
    expect(serverNoticeText()).toBe(UPDATING_SENTENCE);
    notifyApiSuccess();
    expect(serverNoticeText()).toBe('');

    expect(applyHttpResponse(503, CODE_UPDATING)).toBe('updating');
    expect(applyHttpResponse(200, '')).toBe('stockbot');
    expect(serverNoticeText()).toBe('');
  });

  it('keeps a coded gateway response off the offline banner', () => {
    for (const code of ['provider_unavailable', 'updating', 'busy', 'no_space', 'auth_not_configured', 'internal']) {
      notifyApiSuccess();
      const status = code === 'provider_unavailable' ? 502 : 503;
      const disposition = applyHttpResponse(status, code);
      expect(serverNoticeText()).not.toBe(OFFLINE_SENTENCE);
      if (code === 'updating') expect(disposition).toBe('updating');
      else if (code === 'auth_not_configured') expect(disposition).toBe('auth');
      else expect(disposition).toBe('stockbot');
    }
    notifyApiSuccess();
    for (const status of [502, 503, 504, 530]) {
      expect(applyHttpResponse(status, '')).toBe('offline');
      expect(serverNoticeText()).toBe(OFFLINE_SENTENCE);
      notifyApiSuccess();
    }
  });
});

describe('stage-1 household copy', () => {
  it('omits the older-app warning when no device is behind', () => {
    expect(olderAppDevicesSentence(0)).toBeNull();
    expect(olderAppDevicesSentence(-1)).toBeNull();
    expect(olderAppDevicesSentence(Number.NaN)).toBeNull();
  });

  it('keeps the older-app warning plural and substitutes only N', () => {
    expect(olderAppDevicesSentence(1)).toBe(
      "1 devices opened Stockbot with an older app version in the last 7 days. They'll need to update the app to keep using it.",
    );
    expect(olderAppDevicesSentence(3)).toBe(
      "3 devices opened Stockbot with an older app version in the last 7 days. They'll need to update the app to keep using it.",
    );
  });

  it('omits the unsent-scan sentence when a hub has nothing queued', () => {
    expect(disconnectHubConfirm('Kitchen hub', 0)).toBe('Disconnect Kitchen hub?');
    expect(disconnectHubConfirm('Kitchen hub', 4)).toBe(
      "Disconnect Kitchen hub? 4 scans it hasn't sent yet will be lost.",
    );
    expect(disconnectedHubNotice('Kitchen hub')).toBe('Kitchen hub was disconnected.');
  });

  it('uses the printed pairing copy and the hosted error sentences', () => {
    expect(pairingPrintBody('Pantry')).toBe(
      'Scan this code with your scanner to connect your hub to Pantry. This code works once and expires in 10 minutes.',
    );
    expect(pairingKeyboardFallback('SBP-2345-6789-X')).toBe(
      'No printer? Plug a USB keyboard into the hub, type this code, and press Enter: SBP-2345-6789-X',
    );
    expect(PAIRING_EXPIRED_SENTENCE).toBe('This code expired. Get a new one.');
    expect(HOUSEHOLD_DELETE_SENTENCE).toBe(
      'Your household will be permanently deleted after 30 days. Backup copies are cleared within two weeks after that.',
    );
    expect(CHANGEOVER_BANNER).toContain('shared household password');
    expect(CHANGEOVER_SIGN_IN_NOTE).toBe(
      'The shared household password no longer signs in. Use your email and password.',
    );
    expect(hostedErrorSentence('last_owner')).toBe(
      "You're the only owner. Make someone else an owner first, or delete the household.",
    );
    expect(hostedErrorSentence('forbidden_role')).toBe('Only an owner can do that.');
    expect(hostedErrorSentence('rate_limited')).toBe('Too many tries. Wait a moment and try again.');
    expect(hostedErrorSentence('not_a_code')).toBe(SERVER_ERROR_FALLBACK);
    for (const code of Object.keys(HOSTED_ERROR_SENTENCES)) {
      expect(hostedErrorSentence(code)).toBe(HOSTED_ERROR_SENTENCES[code as keyof typeof HOSTED_ERROR_SENTENCES]);
    }
  });

  it('names hub, phone, and direct scanner kinds', () => {
    expect(deviceKindLabel('pi_hub')).toBe('Hub');
    expect(deviceKindLabel('box_hub')).toBe('Hub');
    expect(deviceKindLabel('esp32_scanner')).toBe('Scanner');
    expect(deviceKindLabel('phone')).toBe('Phone');
    expect(deviceKindLabel('direct')).toBe('Direct scanner');
  });

  it('keeps scanner setup SSID prefix aligned with firmware', () => {
    const header = readFileSync(
      join(__dirname, '../firmware/src/core/scanner_setup_network.h'),
      'utf8',
    );
    const match = header.match(/kScannerSetupApPrefix\[\]\s*=\s*"([^"]+)"/);
    expect(match?.[1]).toBe(SCANNER_SETUP_SSID_PREFIX);
  });
});
