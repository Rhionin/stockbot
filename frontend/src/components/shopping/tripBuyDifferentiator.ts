import type { ShoppingListGroupMember } from '@stockbot/shared/types';
import { formatMemberSize } from '../groups/copy';
import { TRIP_BUY_SAME_NAME_SENTENCE, tripBuyBarcodeTail } from './tripBuyCopy';

const DENVER_TZ = 'America/Denver';

export type TripBuyDifferentiatorField =
  | 'size'
  | 'brand'
  | 'lastBought'
  | 'onHand'
  | 'barcode'
  | 'fallback';

export interface TripBuyMemberView {
  itemId: string;
  displayName: string;
  imageUrl?: string;
  differentiator?: string;
  differentiatorField?: TripBuyDifferentiatorField;
  accessibleName: string;
}

export function usableImageUrl(url?: string): boolean {
  return Boolean(url?.trim());
}

export function thumbnailKey(member: Pick<ShoppingListGroupMember, 'imageUrl'>): string {
  return usableImageUrl(member.imageUrl) ? member.imageUrl!.trim() : '';
}

export function thumbnailsCollidePair(
  a: Pick<ShoppingListGroupMember, 'imageUrl'>,
  b: Pick<ShoppingListGroupMember, 'imageUrl'>,
): boolean {
  const aUsable = usableImageUrl(a.imageUrl);
  const bUsable = usableImageUrl(b.imageUrl);
  if (aUsable && bUsable) return a.imageUrl!.trim() === b.imageUrl!.trim();
  if (!aUsable && !bUsable) return true;
  return false;
}

export function groupThumbnailsCollide(members: ShoppingListGroupMember[]): boolean {
  if (members.length < 2) return false;
  for (let i = 0; i < members.length; i += 1) {
    for (let j = i + 1; j < members.length; j += 1) {
      if (!thumbnailsCollidePair(members[i]!, members[j]!)) return false;
    }
  }
  return true;
}

function groupNamesCollide(members: ShoppingListGroupMember[]): boolean {
  if (members.length < 2) return false;
  const folded = members.map((member) => member.name.trim().toLowerCase());
  return folded.every((name) => name === folded[0]);
}

export function needsDifferentiator(members: ShoppingListGroupMember[]): boolean {
  return members.length > 1 && groupThumbnailsCollide(members) && groupNamesCollide(members);
}

function isAllCaps(name: string): boolean {
  const letters = name.replace(/[^a-zA-Z]/g, '');
  return letters.length > 0 && letters === letters.toUpperCase();
}

function titleCaseDisplay(name: string): string {
  return name
    .toLowerCase()
    .replace(/\b[a-z]/g, (ch) => ch.toUpperCase());
}

export function displayNameForGroup(members: ShoppingListGroupMember[], rawName: string): string {
  if (!groupNamesCollide(members)) return rawName;
  const names = members.map((member) => member.name.trim());
  const preferred = names.find((name) => !isAllCaps(name));
  if (preferred) return preferred;
  if (names.every(isAllCaps)) return titleCaseDisplay(rawName);
  return rawName;
}

export function formatBought(lastStockedAt: string, now = new Date()): string {
  const date = new Date(lastStockedAt);
  const month = new Intl.DateTimeFormat('en-US', { month: 'short', timeZone: DENVER_TZ }).format(date);
  const day = Number(new Intl.DateTimeFormat('en-US', { day: 'numeric', timeZone: DENVER_TZ }).format(date));
  const year = Number(new Intl.DateTimeFormat('en-US', { year: 'numeric', timeZone: DENVER_TZ }).format(date));
  const currentYear = Number(new Intl.DateTimeFormat('en-US', { year: 'numeric', timeZone: DENVER_TZ }).format(now));
  if (year !== currentYear) return `Bought ${month} ${day}, ${year}`;
  return `Bought ${month} ${day}`;
}

export function denverDayKey(iso: string): string {
  return new Intl.DateTimeFormat('en-CA', {
    timeZone: DENVER_TZ,
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
  }).format(new Date(iso));
}

export function memberOnHand(member: ShoppingListGroupMember): number | undefined {
  if (!('onHand' in member)) return undefined;
  const value = member.onHand;
  return typeof value === 'number' && Number.isFinite(value) ? value : undefined;
}

export function deterministicBarcode(barcodes?: string[]): string | undefined {
  const codes = (barcodes ?? []).map((code) => code.trim()).filter((code) => code !== '');
  if (codes.length === 0) return undefined;
  return [...codes].sort((a, b) => a.localeCompare(b))[0];
}

export function barcodeTail(barcodes?: string[]): string | undefined {
  const code = deterministicBarcode(barcodes);
  if (code === undefined || code.length < 4) return undefined;
  return tripBuyBarcodeTail(code.slice(-4));
}

function brandInName(brand: string, name: string): boolean {
  return name.toLowerCase().includes(brand.trim().toLowerCase());
}

function sizeValue(member: ShoppingListGroupMember): string {
  return formatMemberSize(member);
}

function brandValue(member: ShoppingListGroupMember): string | undefined {
  const brand = member.brand?.trim() ?? '';
  if (brand === '' || brandInName(brand, member.name)) return undefined;
  return brand;
}

function lastBoughtValue(member: ShoppingListGroupMember): string | undefined {
  if (!member.lastStockedAt) return undefined;
  return formatBought(member.lastStockedAt);
}

function onHandValue(member: ShoppingListGroupMember): string | undefined {
  const count = memberOnHand(member);
  if (count === undefined) return undefined;
  return `${count} on hand`;
}

function valuesDiffer(values: (string | undefined)[]): boolean {
  const present = values.filter((value): value is string => value !== undefined && value !== '');
  if (present.length < 2) return false;
  return new Set(present).size > 1;
}

export function lastBoughtDiffers(members: ShoppingListGroupMember[]): boolean {
  const keys = members.map((member) => (member.lastStockedAt ? denverDayKey(member.lastStockedAt) : undefined));
  const present = keys.filter((key): key is string => key !== undefined);
  if (present.length < 2) return false;
  return new Set(present).size > 1;
}

function onHandDiffers(members: ShoppingListGroupMember[]): boolean {
  const counts = members.map((member) => memberOnHand(member));
  const present = counts.filter((count): count is number => count !== undefined);
  if (present.length < 2) return false;
  return new Set(present).size > 1;
}

function barcodeTailsDiffer(members: ShoppingListGroupMember[]): boolean {
  const tails = members.map((member) => barcodeTail(member.barcodes));
  const present = tails.filter((tail): tail is string => tail !== undefined);
  if (present.length < 2) return false;
  return new Set(present).size > 1;
}

export function pickDifferentiatorField(members: ShoppingListGroupMember[]): TripBuyDifferentiatorField | undefined {
  if (!needsDifferentiator(members)) return undefined;
  if (valuesDiffer(members.map(sizeValue))) return 'size';
  if (valuesDiffer(members.map(brandValue))) return 'brand';
  if (lastBoughtDiffers(members)) return 'lastBought';
  if (onHandDiffers(members)) return 'onHand';
  if (barcodeTailsDiffer(members)) return 'barcode';
  return 'fallback';
}

export function differentiatorText(
  member: ShoppingListGroupMember,
  field: TripBuyDifferentiatorField,
): string {
  switch (field) {
    case 'size':
      return sizeValue(member);
    case 'brand':
      return brandValue(member) ?? '';
    case 'lastBought':
      return lastBoughtValue(member) ?? '';
    case 'onHand': {
      const text = onHandValue(member);
      return text ?? '';
    }
    case 'barcode':
      return barcodeTail(member.barcodes) ?? '';
    case 'fallback':
      return TRIP_BUY_SAME_NAME_SENTENCE;
    default:
      return '';
  }
}

export function buildTripBuyViews(members: ShoppingListGroupMember[]): TripBuyMemberView[] {
  const field = pickDifferentiatorField(members);
  return members.map((member) => {
    const displayName = displayNameForGroup(members, member.name);
    const differentiator = field === undefined ? undefined : differentiatorText(member, field);
    const diff = differentiator && differentiator !== '' ? differentiator : undefined;
    const accessibleName = diff ? `${displayName}, ${diff}` : displayName;
    return {
      itemId: member.itemId,
      displayName,
      imageUrl: member.imageUrl,
      differentiator: diff,
      differentiatorField: field,
      accessibleName,
    };
  });
}

export { productInitials } from '../inventory/photoInitials';