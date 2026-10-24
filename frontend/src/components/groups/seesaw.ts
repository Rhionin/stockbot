import type { NetDimension, ProductGroup } from '@stockbot/shared/types';
import { formatAmount, onHandOunces, type MemberPackage } from './copy';

export type SeesawPin = 'amount' | 'time';

export interface SeesawDraft {
  pin: SeesawPin;
  value: number | string;
}

export interface SeesawEnd {
  pin: SeesawPin;
  pinned: boolean;
  label: string;
  known: boolean;
  /** Household window, shown beside the pinned time. */
  isDefault: boolean;
  /** Expanded phrase for the pill, not part of its accessible name. */
  description: string;
}

export interface SeesawView {
  caption: string;
  rateLabel: string;
  unknownHint: string;
  amount: SeesawEnd;
  time: SeesawEnd;
  amountValue: number | null;
  monthsValue: number | null;
  /** Ounces the bin fills against. Absent when the target is not an amount. */
  fillQuantity?: number;
  dimension: NetDimension;
  hasRate: boolean;
}

export function clampMonth(months: number): number {
  return Math.min(12, Math.max(1, Math.round(months)));
}

// Weeks while the span is short, then months to the nearest tenth. A 30-day
// month keeps this in step with the supply window, so 40 oz at 16 oz/mo stays
// 2.5 months instead of rounding up to 3.
export function formatDuration(months: number): string {
  if (!Number.isFinite(months) || months <= 0) return '';
  const weeks = months * (30 / 7);
  if (weeks < 10) {
    const rounded = Math.round(weeks);
    if (rounded < 1) return '< 1 wk';
    return rounded === 1 ? '1 wk' : `${rounded} wks`;
  }
  const tenths = Math.round(months * 10) / 10;
  if (tenths < 1) return '< 1 mo';
  if (Number.isInteger(tenths)) return `${tenths} mo`;
  return `${tenths.toFixed(1)} mo`;
}

const positive = (value: number | null | undefined): value is number =>
  value !== null && value !== undefined && Number.isFinite(value) && value > 0;

function rateOf(group: Pick<ProductGroup, 'usage'>): { perMonth: number; unit: string } | null {
  const usage = group.usage;
  if (!usage || !(usage.perMonth > 0)) return null;
  return usage;
}

function parseDraft(value: number | string): number | null {
  const parsed = typeof value === 'number' ? value : Number(value);
  return positive(parsed) ? parsed : null;
}

export function seesawView(
  group: Pick<ProductGroup, 'quantity' | 'dimension' | 'windowMonths' | 'usage'>,
  members: MemberPackage[],
  householdMonths: number | undefined,
  draft: SeesawDraft | null,
): SeesawView {
  const dimension: NetDimension = group.dimension === 'volume' ? 'volume' : 'mass';
  const rate = rateOf(group);
  const unit = rate?.unit ?? (dimension === 'volume' ? 'fl oz' : 'oz');

  let pin: SeesawPin;
  let isDefault = false;
  let amount: number | null = null;
  let months: number | null = null;

  if (draft) {
    pin = draft.pin;
    if (draft.pin === 'amount') {
      amount = parseDraft(draft.value);
      months = rate && amount !== null ? amount / rate.perMonth : null;
    } else {
      months = parseDraft(draft.value);
      amount = rate && months !== null ? months * rate.perMonth : null;
    }
  } else if (group.quantity !== undefined) {
    pin = 'amount';
    amount = group.quantity;
    if (rate && positive(amount)) months = amount / rate.perMonth;
  } else if (group.windowMonths !== undefined) {
    pin = 'time';
    months = group.windowMonths;
    if (rate && positive(months)) amount = months * rate.perMonth;
  } else {
    pin = 'time';
    isDefault = true;
    months = positive(householdMonths) ? householdMonths : null;
    if (rate && positive(months)) amount = months * rate.perMonth;
  }

  const amountEnd = endAmount(pin, amount, unit);
  const timeEnd = endTime(pin, months, isDefault);
  const explicitTarget = group.quantity !== undefined || group.windowMonths !== undefined || draft !== null;
  const onHand = onHandOunces(members, dimension);
  let caption = '';
  if (onHand !== null) {
    caption = `${formatAmount(onHand)} ${unit} on hand`;
    if (onHand > 0 && rate) caption += ` · ≈ ${formatDuration(onHand / rate.perMonth)}`;
  }

  const fillQuantity = positive(amount) && (pin === 'amount' || rate !== null) ? amount : undefined;

  return {
    caption,
    rateLabel: rate ? `at ${formatAmount(rate.perMonth)} ${unit} / mo` : '',
    unknownHint: hintFor(rate !== null, amountEnd.known && timeEnd.known, explicitTarget),
    amount: amountEnd,
    time: timeEnd,
    amountValue: amount,
    monthsValue: months,
    fillQuantity,
    dimension,
    hasRate: rate !== null,
  };
}

function hintFor(hasRate: boolean, bothKnown: boolean, explicitTarget: boolean): string {
  if (hasRate || bothKnown) return '';
  return explicitTarget ? 'Fills in once usage is known' : 'Set an amount or time';
}

export function stockMeter(
  view: Pick<SeesawView, 'caption' | 'fillQuantity'>,
  onHand: number | null,
): { now: number; max: number; text: string } {
  const text = view.caption !== '' ? view.caption : 'No sizes listed';
  if (view.fillQuantity !== undefined && view.fillQuantity > 0) {
    const raw = onHand !== null && onHand > 0 ? Math.min(onHand, view.fillQuantity) : 0;
    return { now: Math.round(raw * 10) / 10, max: view.fillQuantity, text };
  }
  return { now: 0, max: 1, text };
}

function speakAmount(shown: string, unit: string): string {
  const bare = shown.replace(/^≈\s*/, '');
  const number = bare.endsWith(` ${unit}`) ? bare.slice(0, -(unit.length + 1)) : bare;
  const one = number === '1';
  let noun = unit;
  if (unit === 'oz') noun = one ? 'ounce' : 'ounces';
  if (unit === 'fl oz') noun = one ? 'fluid ounce' : 'fluid ounces';
  return `${number} ${noun}`;
}

function speakSpan(shown: string): string {
  const bare = shown.replace(/^≈\s*/, '');
  const month = bare.match(/^(\d+(?:\.\d+)?) mo$/);
  if (month) return `${month[1]} ${month[1] === '1' ? 'month' : 'months'}`;
  const week = bare.match(/^(\d+) wks?$/);
  if (week) return `${week[1]} ${week[1] === '1' ? 'week' : 'weeks'}`;
  if (bare === '< 1 mo') return 'less than 1 month';
  if (bare === '< 1 wk') return 'less than 1 week';
  return bare;
}

function endAmount(pin: SeesawPin, amount: number | null, unit: string): SeesawEnd {
  const pinned = pin === 'amount';
  if (!positive(amount)) {
    return {
      pin: 'amount',
      pinned,
      label: 'Set amount',
      known: false,
      isDefault: false,
      description: 'Keep on hand amount, not set',
    };
  }
  const shown = `${formatAmount(amount)} ${unit}`;
  const spoken = speakAmount(shown, unit);
  if (pinned) {
    return {
      pin: 'amount',
      pinned: true,
      label: shown,
      known: true,
      isDefault: false,
      description: `Keep on hand amount, pinned, ${spoken}`,
    };
  }
  const visible = `≈ ${shown}`;
  return {
    pin: 'amount',
    pinned: false,
    label: visible,
    known: true,
    isDefault: false,
    description: `Keep on hand amount, about ${spoken}`,
  };
}

function endTime(pin: SeesawPin, months: number | null, isDefault: boolean): SeesawEnd {
  const pinned = pin === 'time';
  if (!positive(months)) {
    return {
      pin: 'time',
      pinned,
      label: 'Set time',
      known: false,
      isDefault: false,
      description: 'Keep on hand time, not set',
    };
  }
  if (pinned) {
    const shown = `${Math.round(months)} mo`;
    const marker = isDefault ? ', household default' : '';
    return {
      pin: 'time',
      pinned: true,
      label: shown,
      known: true,
      isDefault,
      description: `Keep on hand time, pinned, ${speakSpan(shown)}${marker}`,
    };
  }
  const shown = formatDuration(months);
  const visible = `≈ ${shown}`;
  return {
    pin: 'time',
    pinned: false,
    label: visible,
    known: true,
    isDefault: false,
    description: `Keep on hand time, about ${speakSpan(shown)}`,
  };
}
