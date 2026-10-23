import { describe, expect, it } from 'vitest';
import {
  BATCH_FAILURE_FALLBACK,
  BUSY_SENTENCE,
  batchCardCommitNotice,
  batchFailureBannerText,
  confirmSkipsNothingOnHand,
  formatPartialRemoval,
  NO_SPACE_SENTENCE,
  nothingOnHandPill,
  shelfHasNothingOnHand,
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
