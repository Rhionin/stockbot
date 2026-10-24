import { contrastRatio } from '@stockbot/shared/contrast';
import {
  confirmSkipsNothingOnHand,
  nothingOnHandPill,
  NOTHING_ON_HAND_BODY,
  NOTHING_ON_HAND_TITLE,
  shelfHasNothingOnHand,
} from '@stockbot/shared/contract';
import { colors } from './theme';

const page = '#FFFFFF';

describe('nothing on hand contrast', () => {
  it.each([
    { foreground: colors.nothingInk, background: colors.nothingWash, minimum: 4.5 },
    { foreground: colors.nothingInk, background: page, minimum: 4.5 },
    { foreground: colors.nothingInk, background: colors.paper, minimum: 4.5 },
    { foreground: colors.nothingBorder, background: colors.nothingWash, minimum: 3 },
    { foreground: colors.nothingBorder, background: page, minimum: 3 },
    { foreground: colors.nothingBorder, background: colors.paper, minimum: 3 },
  ])('$foreground on $background is at least $minimum', ({ foreground, background, minimum }) => {
    expect(contrastRatio(foreground, background)).toBeGreaterThanOrEqual(minimum);
  });

  it('uses the amber ink already on the shelf mark', () => {
    expect(colors.nothingInk).toBe('#92400E');
    expect(colors.using).toBe(colors.nothingInk);
    expect(colors.nothingWash).toBe('#FFFBEB');
    expect(colors.nothingBorder).toBe('#B45309');
  });
});

describe('nothing on hand copy', () => {
  it('keeps the card sentences', () => {
    expect(NOTHING_ON_HAND_TITLE).toBe('Nothing on hand to take out.');
    expect(NOTHING_ON_HAND_BODY).toBe(
      "Stockbot thinks you have none, so this can't be confirmed yet. Stock it in first, or remove this scan.",
    );
  });

  it.each([
    { count: 0, pill: null, skip: null },
    { count: 1, pill: '1 with nothing on hand', skip: 'Confirm skips the 1 with nothing on hand.' },
    { count: 2, pill: '2 with nothing on hand', skip: 'Confirm skips the 2 with nothing on hand.' },
  ])('count $count', ({ count, pill, skip }) => {
    expect(nothingOnHandPill(count)).toBe(pill);
    expect(confirmSkipsNothingOnHand(count)).toBe(skip);
  });

  it.each([
    { onHand: undefined, empty: false },
    { onHand: 0, empty: true },
    { onHand: 1, empty: false },
  ])('on hand $onHand', ({ onHand, empty }) => {
    expect(shelfHasNothingOnHand(onHand)).toBe(empty);
  });
});
