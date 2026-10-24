import type { GroupMember, GroupTarget, NetDimension, ProductGroup, SuggestionMember, TargetConflictMember } from '@stockbot/shared/types';

export const ruleLabels: Record<string, string> = {
  same_as_ran_out: 'Same product',
  favorite: 'Always my favorite',
  best_deal: 'Best deal',
  favor_variety: 'Favor variety',
};

export const ruleLabel = (rule: string) => ruleLabels[rule] ?? rule;

export const ruleChoices = [
  { id: 'same_as_ran_out', label: 'Same product', hint: 'Rebuy the exact product you used last.' },
  { id: 'favorite', label: 'Favorite', hint: 'Always buy one product.' },
  { id: 'best_deal', label: 'Best deal', hint: 'Lowest price per ounce.' },
  { id: 'favor_variety', label: 'Favor variety', hint: "Rotate to the product you've had least recently." },
] as const;

export function rulePillLabel(
  group: Pick<ProductGroup, 'rule' | 'ruleConfirmed' | 'pinnedProductId'>,
  members: { productId: string; name: string }[],
): string {
  if (!group.ruleConfirmed) return 'Pick a rule';
  if (group.rule === 'same_as_ran_out') return ruleLabels.same_as_ran_out;
  if (group.rule === 'favorite') {
    return members.find((member) => member.productId === group.pinnedProductId)?.name ?? 'Favorite';
  }
  if (group.rule === 'best_deal') return 'Best deal';
  if (group.rule === 'favor_variety') return 'Favor variety';
  return ruleLabel(group.rule);
}

export const kindPhrase = (kind: string) => {
  if (kind === 'looks_alike') return 'look alike';
  if (kind === 'from_old_plan') return 'from the old shopping plan';
  if (kind === 'from_scan') return 'from a scan';
  return kind;
};

// Short reason on an inventory row or the add sheet. Same-need cards come from the old plan.
export const suggestionReason = (kind: string) => {
  if (kind === 'from_old_plan') return 'same need';
  if (kind === 'looks_alike') return 'look alike';
  if (kind === 'from_scan') return 'from a scan';
  return kindPhrase(kind);
};

export const suggestionBanner = (cards: { kind: string }[]) => {
  if (cards.length === 0) return '';
  const counts = new Map<string, number>();
  for (const card of cards) {
    counts.set(card.kind, (counts.get(card.kind) ?? 0) + 1);
  }
  const parts = [...counts.entries()]
    .sort(([a], [b]) => a.localeCompare(b))
    .map(([kind, count]) => `${count} ${kindPhrase(kind)}`);
  const noun = cards.length === 1 ? 'suggestion' : 'suggestions';
  return `${cards.length} ${noun} to review. ${parts.join(', ')}.`;
};

export const targetLabel = (group: Pick<ProductGroup, 'quantity' | 'dimension' | 'windowMonths'>) => {
  if (group.quantity !== undefined) {
    return `${group.quantity} ${group.dimension === 'volume' ? 'fl oz' : 'oz'}`;
  }
  if (group.windowMonths !== undefined) return `${group.windowMonths} months`;
  return 'Household default';
};

export const onHandCount = (members: GroupMember[]) => members.reduce((sum, member) => sum + member.onHand, 0);

export function formatMemberSize(member: Pick<SuggestionMember, 'netAmount' | 'netUnit' | 'packCount'>): string {
  if (member.netAmount === undefined || member.netUnit === undefined || member.netUnit === '') return '';
  const amount = Number.isInteger(member.netAmount) ? String(member.netAmount) : String(member.netAmount);
  const size = `${amount} ${member.netUnit}`;
  if (member.packCount !== undefined && member.packCount > 1) return `${member.packCount} x ${size}`;
  return size;
}

export interface MemberFact {
  key: string;
  label: string;
  text: (member: SuggestionMember) => string;
}

// Rows that at least one member can fill. A blank cell stays in the row so
// the products still line up.
export function memberFacts(members: SuggestionMember[]): MemberFact[] {
  const defs: MemberFact[] = [
    { key: 'brand', label: 'Brand', text: (member) => member.brand ?? '' },
    { key: 'size', label: 'Size', text: (member) => formatMemberSize(member) },
    { key: 'variety', label: 'Variety', text: (member) => member.variety ?? '' },
    { key: 'package', label: 'Package', text: (member) => member.unitOfMeasure ?? '' },
    { key: 'barcode', label: 'Barcode', text: (member) => (member.barcodes ?? []).filter((code) => code !== '').join(', ') },
    { key: 'category', label: 'Category', text: (member) => member.category ?? '' },
  ];
  return defs.filter((fact) => members.some((member) => fact.text(member) !== ''));
}

export type GroupSupply = Pick<ProductGroup, 'quantity' | 'dimension' | 'windowMonths'>;

export interface TargetChoice {
  label: string;
  target: GroupTarget;
}

const ounceLabel = (member: Pick<TargetConflictMember, 'amount' | 'dimension'>): string | null => {
  if (member.amount === undefined) return null;
  if (member.dimension === 'volume') return `${formatAmount(member.amount)} fluid ounces`;
  if (member.dimension === 'mass') return `${formatAmount(member.amount)} ounces`;
  return null;
};

export const conflictAmount = (member: Pick<TargetConflictMember, 'quantity' | 'amount' | 'dimension' | 'windowMonths'>) => {
  const ounces = ounceLabel(member);
  if (member.quantity !== undefined && ounces) return `${member.quantity} on hand (${ounces})`;
  if (ounces) return ounces;
  if (member.quantity !== undefined) return `${member.quantity} on hand`;
  if (member.windowMonths !== undefined) return `${member.windowMonths} months`;
  return '';
};

export const conflictLine = (member: TargetConflictMember) => {
  const amount = conflictAmount(member);
  if (amount === '') return member.name;
  return `${member.name}: ${amount}`;
};

const memberTarget = (member: TargetConflictMember): { key: string; amount: string; target: GroupTarget } | null => {
  const ounces = ounceLabel(member);
  if (ounces && member.amount !== undefined && (member.dimension === 'mass' || member.dimension === 'volume')) {
    return {
      key: `amount:${member.amount}:${member.dimension}`,
      amount: ounces,
      target: { quantity: member.amount, dimension: member.dimension },
    };
  }
  if (member.windowMonths !== undefined) {
    return {
      key: `window:${member.windowMonths}`,
      amount: conflictAmount(member),
      target: { windowMonths: member.windowMonths },
    };
  }
  return null;
};

// Clear would wipe a group's own target. It is only the household default.
export const targetChoices = (group: GroupSupply | null, members: TargetConflictMember[]): TargetChoice[] => {
  const choices: TargetChoice[] = [];
  if (group?.quantity !== undefined) {
    const target: GroupTarget = { quantity: group.quantity };
    if (group.dimension === 'mass' || group.dimension === 'volume') target.dimension = group.dimension;
    choices.push({ label: `Keep the group's ${targetLabel(group)}`, target });
  } else if (group?.windowMonths !== undefined) {
    choices.push({ label: `Keep the group's ${targetLabel(group)}`, target: { windowMonths: group.windowMonths } });
  } else {
    choices.push({ label: 'Keep the household default', target: { clear: true } });
  }

  const seen = new Map<string, { amount: string; target: GroupTarget; names: string[] }>();
  for (const member of members) {
    const built = memberTarget(member);
    if (!built) continue;
    const existing = seen.get(built.key);
    if (existing) {
      existing.names.push(member.name);
      continue;
    }
    seen.set(built.key, { amount: built.amount, target: built.target, names: [member.name] });
  }
  for (const choice of seen.values()) {
    choices.push({
      label: `Use ${choice.amount} (from ${choice.names.join(', ')})`,
      target: choice.target,
    });
  }
  return choices;
};

export type GroupChip = 'all' | 'low' | 'unconfirmed' | string;

export const matchesChip = (group: ProductGroup, chip: GroupChip) => {
  if (chip === 'all') return true;
  if (chip === 'low') return group.runningLow;
  if (chip === 'unconfirmed') return !group.ruleConfirmed;
  return group.rule === chip;
};

export const matchesSearch = (group: ProductGroup, query: string) => {
  const q = query.trim().toLowerCase();
  if (q === '') return true;
  if (group.name.toLowerCase().includes(q)) return true;
  return group.members.some((member) => member.name.toLowerCase().includes(q));
};

// A member plus the package facts the bin needs. Sizes come from the catalog;
// the group payload itself only carries a name and a count.
export interface MemberPackage {
  productId: string;
  name: string;
  onHand: number;
  unitOfMeasure?: string;
  netAmount?: number;
  netUnit?: string;
  packCount?: number;
  barcodes?: string[];
  noRestock?: boolean;
  lastStockedAt?: string;
  lastConsumedAt?: string;
}

export function memberPackage(
  member: GroupMember,
  product?: { unitOfMeasure?: string; netAmount?: number; netUnit?: string; packCount?: number; barcodes?: string[] },
): MemberPackage {
  return {
    productId: member.productId,
    name: member.name,
    onHand: member.onHand,
    unitOfMeasure: product?.unitOfMeasure,
    netAmount: product?.netAmount,
    netUnit: product?.netUnit,
    packCount: product?.packCount,
    barcodes: product?.barcodes,
    noRestock: member.noRestock,
    lastStockedAt: member.lastStockedAt,
    lastConsumedAt: member.lastConsumedAt,
  };
}

// Stocked products first, so the color in the bin is the first swatch in the list.
export function memberOrder<T extends { name: string; onHand: number }>(members: T[]): T[] {
  return [...members].sort((a, b) => {
    const stocked = Number(b.onHand > 0) - Number(a.onHand > 0);
    if (stocked !== 0) return stocked;
    return a.name.localeCompare(b.name);
  });
}

export function formatAmount(value: number): string {
  if (!Number.isFinite(value)) return '0';
  const rounded = Math.round(value * 10) / 10;
  if (Object.is(rounded, -0)) return '0';
  return Number.isInteger(rounded) ? String(rounded) : rounded.toFixed(1);
}

const pluralize = (unit: string, count: number) => {
  if (count === 1) return unit;
  if (/[^aeiou]y$/i.test(unit)) return `${unit.slice(0, -1)}ies`;
  if (/(s|x|z|ch|sh)$/i.test(unit)) return `${unit}es`;
  return `${unit}s`;
};

export function onHandPhrase(count: number, unit: string): string {
  if (count <= 0) return unit === '' ? 'Nothing on hand' : `No ${pluralize(unit, 2)} on hand`;
  if (unit === '') return count === 1 ? '1 on hand' : `${count} on hand`;
  return `${count} ${pluralize(unit, count)} on hand`;
}

const sharedUnit = (members: MemberPackage[]) => {
  if (members.length === 0) return '';
  const units = members.map((member) => (member.unitOfMeasure ?? '').trim());
  if (units.some((unit) => unit === '')) return '';
  const first = units[0] ?? '';
  return units.every((unit) => unit === first) ? first : '';
};

const ounceUnit = (dimension?: NetDimension): 'oz' | 'fl oz' => (dimension === 'volume' ? 'fl oz' : 'oz');

function spokenOunces(amount: number, unit: 'oz' | 'fl oz'): string {
  const shown = formatAmount(amount);
  const one = shown === '1';
  const noun = unit === 'fl oz' ? (one ? 'fluid ounce' : 'fluid ounces') : (one ? 'ounce' : 'ounces');
  return `${shown} ${noun}`;
}

interface Measured {
  total: number;
  parts: { productId: string; ounces: number }[];
}

// Null when any stocked package is missing a size, or the units do not match
// the target. A partial fill would draw ounces the shelf does not have.
export function onHandOunces(members: MemberPackage[], dimension?: NetDimension): number | null {
  const measured = measuredOunces(members, dimension === 'volume' ? 'volume' : 'mass');
  if (measured === null) return null;
  return measured.total;
}

function measuredOunces(members: MemberPackage[], dimension: NetDimension): Measured | null {
  const unit = ounceUnit(dimension);
  const parts: { productId: string; ounces: number }[] = [];
  for (const member of members) {
    if (member.onHand <= 0) continue;
    if (member.netAmount === undefined || !(member.netAmount > 0) || member.netUnit !== unit) return null;
    const packs = member.packCount !== undefined && member.packCount > 0 ? member.packCount : 1;
    parts.push({ productId: member.productId, ounces: member.netAmount * packs * member.onHand });
  }
  return { total: parts.reduce((sum, part) => sum + part.ounces, 0), parts };
}

const binPalette = [
  'var(--mantine-color-yellow-5)',
  'var(--mantine-color-blue-3)',
  'var(--mantine-color-red-4)',
  'var(--mantine-color-teal-4)',
  'var(--mantine-color-orange-4)',
  'var(--mantine-color-grape-3)',
];

export function binColor(members: { productId: string }[], productId: string): string {
  const index = members.findIndex((member) => member.productId === productId);
  return binPalette[(index < 0 ? 0 : index) % binPalette.length] ?? binPalette[0] ?? '';
}

export interface BinSegment {
  productId: string;
  fraction: number;
}

export interface BinView {
  corner: string;
  level: string;
  segments: BinSegment[];
  label: string;
}

export function binView(
  group: Pick<ProductGroup, 'quantity' | 'dimension' | 'windowMonths'>,
  members: MemberPackage[],
): BinView {
  if (group.quantity === undefined) {
    if (group.windowMonths !== undefined) {
      const noun = group.windowMonths === 1 ? 'month' : 'months';
      return {
        corner: `${group.windowMonths} ${noun}`,
        level: '',
        segments: [],
        label: `Keeping ${group.windowMonths} ${noun}. The bin stays empty because the target is not ounces.`,
      };
    }
    return {
      corner: 'Default',
      level: '',
      segments: [],
      label: 'Household default. The bin stays empty because the target is not ounces.',
    };
  }

  const dimension: NetDimension = group.dimension === 'volume' ? 'volume' : 'mass';
  const unit = ounceUnit(dimension);
  const corner = `${formatAmount(group.quantity)} ${unit}`;
  const quantity = group.quantity;
  if (!(quantity > 0)) {
    return { corner, level: '', segments: [], label: `Keeping ${corner}.` };
  }
  const measured = measuredOunces(members, dimension);
  if (measured === null) {
    return {
      corner,
      level: '',
      segments: [],
      label: `Keeping ${corner}. Package sizes are not listed, so the bin is not filled.`,
    };
  }
  if (measured.total <= 0) {
    return { corner, level: '', segments: [], label: `Empty bin, keeping ${corner}.` };
  }
  const scale = measured.total > quantity ? quantity / measured.total : 1;
  return {
    corner,
    level: `${formatAmount(measured.total)} ${unit}`,
    segments: measured.parts.map((part) => ({
      productId: part.productId,
      fraction: (part.ounces / quantity) * scale,
    })),
    label: `${formatAmount(measured.total)} ${unit} on hand, keeping ${corner}.`,
  };
}

export function householdPhrase(months?: number): string {
  if (months === undefined || !Number.isFinite(months)) return 'Household default';
  const n = Math.round(months);
  return `Household default, ${n} ${n === 1 ? 'month' : 'months'}`;
}

const countSentence = (members: MemberPackage[]) => {
  const count = members.reduce((sum, member) => sum + member.onHand, 0);
  return onHandPhrase(count, sharedUnit(members));
};

export function detailStockSentence(
  group: Pick<ProductGroup, 'quantity' | 'dimension' | 'windowMonths'>,
  members: MemberPackage[],
  householdMonths?: number,
): string {
  const counted = countSentence(members);
  if (group.quantity !== undefined) {
    const dimension: NetDimension = group.dimension === 'volume' ? 'volume' : 'mass';
    const keep = spokenOunces(group.quantity, ounceUnit(dimension));
    const measured = measuredOunces(members, dimension);
    if (measured === null) {
      return `${counted}. Keep ${keep} on hand. Package sizes are not listed, so the bin stays empty.`;
    }
    if (measured.total <= 0) return `The bin is empty. Keep ${keep} on hand.`;
    const held = spokenOunces(measured.total, ounceUnit(dimension));
    const verb = formatAmount(measured.total) === '1' ? 'is' : 'are';
    const stocked = members.filter((member) => member.onHand > 0);
    if (stocked.length === 1) {
      return `${held} of ${stocked[0]?.name} ${verb} in the bin. Keep ${keep} on hand.`;
    }
    return `${held} ${verb} in the bin. Keep ${keep} on hand.`;
  }
  if (group.windowMonths !== undefined) {
    const n = group.windowMonths;
    return `${counted}. Keep ${n} ${n === 1 ? 'month' : 'months'}.`;
  }
  return `${counted}. ${householdPhrase(householdMonths)}.`;
}

function listStockSentence(
  group: Pick<ProductGroup, 'quantity' | 'dimension' | 'windowMonths'>,
  members: MemberPackage[],
  householdMonths?: number,
): string {
  const counted = countSentence(members);
  if (group.quantity !== undefined) {
    const dimension: NetDimension = group.dimension === 'volume' ? 'volume' : 'mass';
    const keep = spokenOunces(group.quantity, ounceUnit(dimension));
    const measured = measuredOunces(members, dimension);
    if (measured === null || measured.total <= 0) {
      return measured === null ? `${counted}. Keep ${keep}.` : `Nothing in the bin. Keep ${keep}.`;
    }
    return `${spokenOunces(measured.total, ounceUnit(dimension))} in the bin. Keep ${keep}.`;
  }
  if (group.windowMonths !== undefined) {
    const n = group.windowMonths;
    return `${counted}. Keep ${n} ${n === 1 ? 'month' : 'months'}.`;
  }
  return `${counted}. ${householdPhrase(householdMonths)}.`;
}

const pinnedName = (group: { pinnedProductId?: string }, members: { productId: string; name: string }[]) =>
  members.find((member) => member.productId === group.pinnedProductId)?.name;

export function ruleSentence(
  group: Pick<ProductGroup, 'rule' | 'ruleConfirmed' | 'pinnedProductId'>,
  members: { productId: string; name: string }[],
  style: 'detail' | 'brief',
): string {
  if (!group.ruleConfirmed) {
    return `Still using ${ruleLabel(group.rule)} until you pick a rule.`;
  }
  const pin = pinnedName(group, members);
  if (group.rule === 'favorite') {
    if (pin) return `Always buy ${pin}.`;
    return style === 'detail' ? 'Always buy one product.' : 'Buy your favorite.';
  }
  if (group.rule === 'best_deal') {
    return pin
      ? `Lowest price per ounce, or ${pin} if no price is known.`
      : 'Lowest price per ounce.';
  }
  if (group.rule === 'same_as_ran_out') return ruleChoices[0].hint;
  if (group.rule === 'favor_variety') return ruleChoices[3].hint;
  return style === 'detail' ? `Buy ${ruleLabel(group.rule)} next.` : `${ruleLabel(group.rule)}.`;
}

export function listSummary(
  group: ProductGroup,
  members: MemberPackage[],
  householdMonths?: number,
): string {
  return `${listStockSentence(group, members, householdMonths)} ${ruleSentence(group, members, 'brief')}`;
}

export function memberLine(member: MemberPackage): string {
  const size = formatMemberSize(member);
  const hand = member.onHand <= 0 ? 'none on hand' : member.onHand === 1 ? '1 on hand' : `${member.onHand} on hand`;
  const unit = (member.unitOfMeasure ?? '').trim();
  const parts = [size, unit].filter((part) => part !== '');
  if (parts.length === 0) return hand;
  return `${parts.join(' ')}, ${hand}`;
}
