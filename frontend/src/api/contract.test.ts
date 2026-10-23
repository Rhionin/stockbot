import { describe, expect, it } from 'vitest';
import {
  confirmSkipsNothingOnHand,
  formatPartialRemoval,
  networkErrorResult,
  nothingOnHandPill,
  notifyApiSuccess,
  OFFLINE_SENTENCE,
  raiseOfflineBanner,
  serverStateBanner,
  shelfHasNothingOnHand,
  UnreachableError,
  unreachableFrom,
  type BatchCommitResponse,
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

const BUSY_SENTENCE = 'Stockbot is busy right now. Try again in a moment.';
const NO_SPACE_SENTENCE = "Stockbot is out of storage space, so this wasn't saved.";

describe('serverStateBanner', () => {
  it('lets offline replace busy and no_space until a success', () => {
    expect(serverStateBanner(BUSY_SENTENCE)).toBe(BUSY_SENTENCE);
    expect(serverStateBanner(null)).toBeNull();
    raiseOfflineBanner();
    expect(serverStateBanner(BUSY_SENTENCE)).toBe(OFFLINE_SENTENCE);
    expect(serverStateBanner(NO_SPACE_SENTENCE)).toBe(OFFLINE_SENTENCE);
    expect(serverStateBanner(null)).toBe(OFFLINE_SENTENCE);
    notifyApiSuccess();
    expect(serverStateBanner(BUSY_SENTENCE)).toBe(BUSY_SENTENCE);
    expect(serverStateBanner(NO_SPACE_SENTENCE)).toBe(NO_SPACE_SENTENCE);
  });
});
