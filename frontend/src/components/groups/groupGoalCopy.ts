import type { GroupGoalBlocker, GroupGoalKind, ProductGroup } from '@stockbot/shared/types';

/** CJ-approved copy ids for #771 group goal editor (rev 2.1). */
export const groupGoalCopy = {
  e0: 'KEEP ON HAND',
  e1: 'Number of items',
  e1d: 'Counts every package in the group, whatever its size.',
  e2: 'Months of supply',
  e2d: 'Keeps enough for this long, based on how fast you use them.',
  e3Oz: 'Total ounces',
  e3FlOz: 'Total fluid ounces',
  e3x: 'Total size',
  e3d: 'Adds up the size of every package.',
  e4: 'Items',
  e6: 'Enter a whole number of items from 1 to 999.',
  e7: 'Enter an amount greater than zero and at most 999 fluid ounces.',
  e8: (groupName: string) => `${groupName} now uses the household default.`,
  e9: 'Refresh',
  e10: "Don't restock default",
  e11: "The household default is Don't restock, so nothing from this group goes on the list until you set a goal.",
  e12: 'Set a goal',
  groupDontRestock: (groupName: string) => `The ${groupName} group is set to Don't restock.`,
  monthsBad: 'Enter a whole number of months from 1 to 12.',
  ozBad: 'Enter an amount greater than zero and at most 999 ounces.',
  h1: "Couldn't change the restock target. Try again.",
  h2: "Couldn't reach the server. Try again.",
  h3: 'This changed while you were looking. Refresh and try again.',
  b6: 'Count items instead',
  b7FlOz: 'Use fluid ounces instead',
  b7Oz: 'Use ounces instead',
  save: 'Save',
  cancel: 'Cancel',
  useHouseholdDefault: 'Use the household default',
  fillOnceKnown: 'Fills in once usage is known',
  d2f: 'The group keeps 3 months of supply. Groups use this when the household default is an amount.',
} as const;

export function amountChoiceLabel(amountUnit: 'oz' | 'fl oz' | null | undefined, available: boolean): string {
  if (!available) return groupGoalCopy.e3x;
  return amountUnit === 'fl oz' ? groupGoalCopy.e3FlOz : groupGoalCopy.e3Oz;
}

export function amountFieldLabel(amountUnit: 'oz' | 'fl oz' | null | undefined): string {
  return amountUnit === 'fl oz' ? 'Fluid ounces' : 'Ounces';
}

export function goalPillLabel(kind: GroupGoalKind, value: number, unit: string | null): string {
  if (kind === 'items') return value === 1 ? '1 item' : `${value} items`;
  if (kind === 'months') return `${Math.round(value)} mo`;
  if (kind === 'amount' && unit === 'fl oz') return `${value} fl oz`;
  if (kind === 'amount') return `${value} oz`;
  if (kind === 'dont_restock') return "Don't restock";
  return '';
}

export function a1(product: string): string {
  return `To keep a total by size, every product needs a size. ${product} doesn't have one.`;
}

export function a2(n: number): string {
  return `To keep a total by size, every product needs a size. ${n} products don't have one.`;
}

export const a3 = 'To keep a total by size, every product needs the same kind of size. Some are sized by weight (oz) and some by volume (fl oz).';

export const a4 = 'Add products to this group to keep a total by size.';

export function b1(product: string): string {
  return `${product}: no size`;
}

export function b2(product: string): string {
  return `${product}: sized by volume (fl oz)`;
}

export function b3(product: string): string {
  return `${product}: sized by weight (oz)`;
}

export const b4Label = 'Add a size';

export function b4A11y(product: string): string {
  return `Add a size for ${product}`;
}

export const b5Label = 'Edit size';

export function b5A11y(product: string): string {
  return `Edit the size of ${product}`;
}

export function c1(goal: string): string {
  return `The ${goal} goal can't be counted`;
}

export function c3a(product: string): string {
  return `${product} has no size, so the packages can't be added up. Until this is fixed, this group isn't added to the shopping list.`;
}

export function c3b(n: number): string {
  return `${n} products have no size, so the packages can't be added up. Until this is fixed, this group isn't added to the shopping list.`;
}

export function c2a(product: string, goalIsVolume: boolean): string {
  const goalKind = goalIsVolume ? 'weight (oz)' : 'volume (fl oz)';
  const productKind = goalIsVolume ? 'volume (fl oz)' : 'weight (oz)';
  return `It's by ${goalKind}, but ${product} is sized by ${productKind}. Until this is fixed, this group isn't added to the shopping list.`;
}

export function c2b(n: number, goalIsVolume: boolean): string {
  const productKind = goalIsVolume ? 'volume (fl oz)' : 'weight (oz)';
  return `It's by weight (oz), but ${n} products are sized by ${productKind}. Until this is fixed, this group isn't added to the shopping list.`;
}

export function amountUnavailableHeadline(blockers: GroupGoalBlocker[], noMembers: boolean): string {
  if (noMembers) return a4;
  const missing = blockers.filter((row) => row.reason === 'size_missing');
  const mismatch = blockers.filter((row) => row.reason === 'units_mismatch');
  if (missing.length === 1 && mismatch.length === 0) return a1(missing[0]!.name);
  if (missing.length > 1 && mismatch.length === 0) return a2(missing.length);
  if (mismatch.length > 0 && missing.length === 0) return a3;
  if (missing.length > 0) return missing.length === 1 ? a1(missing[0]!.name) : a2(missing.length);
  return a3;
}

export function blockerProblemLine(row: GroupGoalBlocker): string {
  if (row.reason === 'size_missing') return 'no size';
  if (row.sizeKind === 'volume') return 'sized by volume (fl oz)';
  return 'sized by weight (oz)';
}

export function statusGoalDisplay(group: ProductGroup): string {
  const target = group.target;
  if (!target || target.kind === 'dont_restock') return '';
  return goalPillLabel(target.kind, target.value, target.unit);
}