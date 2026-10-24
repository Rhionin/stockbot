import type { GroupSuggestion, InventoryGroup, InventoryItem, Product, ProductGroup, ProductSummary } from '@stockbot/shared/types';
import {
  memberLine, memberPackage, onHandOunces, onHandPhrase, rulePillLabel, suggestionReason, type MemberPackage,
} from '../groups/copy';
import { seesawView, stockMeter } from '../groups/seesaw';

export type ShelfFilter = 'all' | 'low' | 'groups' | 'ungrouped' | 'categories' | 'restock';

export interface ShelfMember extends MemberPackage {
  imageUrl?: string;
  itemId?: string;
  nearExpiryCount: number;
  expiredCount: number;
}

export interface ShelfMeter {
  now: number;
  max: number;
  text: string;
}

export interface ShelfGroupRow {
  kind: 'group';
  id: string;
  group: ProductGroup;
  members: ShelfMember[];
  photoSrc?: string;
  photoName: string;
  status: string;
  rulePill: string | null;
  pinLabel: string;
  meter: ShelfMeter;
}

export interface ShelfItemRow {
  kind: 'item';
  id: string;
  item: InventoryItem;
  hand: string;
  suggestion: GroupSuggestion | null;
}

export type ShelfRow = ShelfGroupRow | ShelfItemRow;

export interface ShelfInput {
  items: InventoryItem[];
  groups: ProductGroup[];
  suggestions: GroupSuggestion[];
  catalog: Product[];
  householdMonths?: number;
}

export function parseShelfFilter(value: string | null): ShelfFilter {
  if (value === 'low' || value === 'groups' || value === 'ungrouped' || value === 'categories' || value === 'restock') return value;
  return 'all';
}

export function asSuggestions(value: unknown): GroupSuggestion[] {
  if (!Array.isArray(value)) return [];
  return value.filter((row): row is GroupSuggestion => (
    !!row && typeof row === 'object' && typeof (row as GroupSuggestion).id === 'string' && Array.isArray((row as GroupSuggestion).members)
  ));
}

export function asCatalog(value: unknown): Product[] {
  if (!Array.isArray(value)) return [];
  return value.filter((row): row is Product => (
    !!row && typeof row === 'object' && typeof (row as Product).id === 'string' && typeof (row as Product).name === 'string' && typeof (row as Product).unitOfMeasure === 'string'
  ));
}

export function isListedGroup(value: unknown): value is ProductGroup {
  if (!value || typeof value !== 'object') return false;
  const row = value as Partial<ProductGroup>;
  return typeof row.id === 'string'
    && typeof row.name === 'string'
    && Array.isArray(row.members)
    && typeof row.runningLow === 'boolean';
}

export function groupsForShelf(listed: unknown, items: InventoryItem[]): ProductGroup[] {
  const rows = Array.isArray(listed) ? listed.filter(isListedGroup) : [];
  const byId = new Map(rows.map((group) => [group.id, group]));
  for (const item of items) {
    if (!item.group || byId.has(item.group.id)) continue;
    byId.set(item.group.id, groupFromSummary(item.group));
  }
  return [...byId.values()];
}

export function itemHand(item: InventoryItem): string {
  const product = item.item.product;
  if (product.netAmount !== undefined && product.netUnit) {
    return memberLine(memberPackage(
      { productId: product.id, name: product.name, onHand: item.instanceCount },
      product,
    ));
  }
  return `${item.instanceCount} ${product.unitOfMeasure}`;
}

export function buildShelf(input: ShelfInput): ShelfRow[] {
  const images = imageMap(input.items, input.catalog, input.suggestions);
  const grouped = new Set(input.groups.flatMap((group) => group.members.map((member) => member.productId)));
  const rows: ShelfRow[] = input.groups.map((group) => groupRow(group, input, images));
  for (const item of input.items) {
    if (grouped.has(item.item.productId)) continue;
    rows.push({
      kind: 'item',
      id: item.item.id,
      item,
      hand: itemHand(item),
      suggestion: input.suggestions.find((card) => card.members.some((member) => member.productId === item.item.productId)) ?? null,
    });
  }
  rows.sort((a, b) => rowName(a).localeCompare(rowName(b)));
  return rows;
}

export function filterShelf(rows: ShelfRow[], query: string, filter: ShelfFilter): ShelfRow[] {
  const q = query.trim().toLowerCase();
  return rows.filter((row) => {
    if (filter === 'groups' && row.kind !== 'group') return false;
    if (filter === 'ungrouped' && row.kind !== 'item') return false;
    if (filter === 'low') {
      if (row.kind === 'group' && !row.group.runningLow) return false;
      if (row.kind === 'item' && row.item.instanceCount > 0) return false;
    }
    if (q === '') return true;
    if (row.kind === 'group') {
      if (row.group.name.toLowerCase().includes(q)) return true;
      return row.members.some((member) => member.name.toLowerCase().includes(q));
    }
    const product = row.item.item.product;
    return product.name.toLowerCase().includes(q) || product.category.toLowerCase().includes(q);
  });
}

export interface SuggestedGroup {
  id: string;
  name: string;
  reason: string;
}

// Groups a suggestion already points at, ahead of the rest of the list.
export function suggestedDestinations(
  productId: string,
  suggestions: GroupSuggestion[],
  groups: ProductGroup[],
): SuggestedGroup[] {
  const found: SuggestedGroup[] = [];
  const seen = new Set<string>();
  for (const card of suggestions) {
    if (!card.members.some((member) => member.productId === productId)) continue;
    const reason = `Suggested · ${suggestionReason(card.kind)}`;
    const ids = new Set<string>();
    if (card.existingGroupId) ids.add(card.existingGroupId);
    for (const group of groups) {
      const shares = group.members.some((member) => (
        member.productId !== productId && card.members.some((suggested) => suggested.productId === member.productId)
      ));
      if (shares) ids.add(group.id);
    }
    for (const id of ids) {
      if (seen.has(id)) continue;
      const group = groups.find((item) => item.id === id);
      if (!group) continue;
      seen.add(id);
      found.push({ id, name: group.name, reason });
    }
  }
  return found;
}

function groupRow(group: ProductGroup, input: ShelfInput, images: Map<string, string>): ShelfGroupRow {
  const members = group.members.map((member) => {
    const item = input.items.find((row) => row.item.productId === member.productId);
    const product = mergedProduct(item?.item.product, input.catalog.find((row) => row.id === member.productId));
    const packed = memberPackage(member, product);
    const listed = packed.barcodes && packed.barcodes.length > 0
      ? packed.barcodes
      : barcodesFor(group.id, member.productId, input.items);
    return {
      ...packed,
      barcodes: listed,
      imageUrl: images.get(member.productId),
      itemId: item?.item.id,
      nearExpiryCount: item?.nearExpiryCount ?? 0,
      expiredCount: item?.expiredCount ?? 0,
    };
  });
  const photo = groupPhoto(group, members, images);
  const view = seesawView(group, members, input.householdMonths, null);
  const pinned = view.amount.pinned ? view.amount : view.time;
  const ounces = onHandOunces(members, view.dimension);
  return {
    kind: 'group',
    id: group.id,
    group,
    members,
    photoSrc: photo.src,
    photoName: photo.name,
    status: view.caption !== '' ? view.caption : countLine(members),
    rulePill: rulePill(group, members),
    pinLabel: pinned.label,
    meter: stockMeter(view, ounces),
  };
}

function groupPhoto(
  group: ProductGroup,
  members: ShelfMember[],
  images: Map<string, string>,
): { src?: string; name: string } {
  if (group.rule === 'favorite' && group.pinnedProductId) {
    const pinned = members.find((member) => member.productId === group.pinnedProductId);
    const src = images.get(group.pinnedProductId);
    if (src) return { src, name: pinned?.name ?? group.name };
  }
  const stocked = members.find((member) => member.onHand > 0 && images.has(member.productId));
  const any = stocked ?? members.find((member) => images.has(member.productId));
  if (any) return { src: images.get(any.productId), name: any.name };
  return { name: group.name };
}

function rulePill(group: ProductGroup, members: { productId: string; name: string }[]): string | null {
  if (!group.ruleConfirmed) return 'Pick a rule';
  if (group.rule === 'same_as_ran_out') return null;
  return rulePillLabel(group, members);
}

function countLine(members: MemberPackage[]): string {
  const count = members.reduce((sum, member) => sum + member.onHand, 0);
  const units = members.map((member) => (member.unitOfMeasure ?? '').trim());
  const first = units[0] ?? '';
  const unit = units.length > 0 && units.every((value) => value === first) ? first : '';
  return onHandPhrase(count, unit);
}

function mergedProduct(
  onHand: ProductSummary | null | undefined,
  listed: Product | undefined,
): Product | undefined {
  if (!onHand && !listed) return undefined;
  return { ...listed, ...onHand } as Product;
}

function barcodesFor(groupId: string, productId: string, items: InventoryItem[]): string[] | undefined {
  for (const item of items) {
    if (item.group?.id !== groupId) continue;
    const found = item.group.members.find((member) => member.productId === productId);
    if (found?.barcodes && found.barcodes.length > 0) return found.barcodes;
  }
  return undefined;
}

function imageMap(items: InventoryItem[], catalog: Product[], suggestions: GroupSuggestion[]): Map<string, string> {
  const images = new Map<string, string>();
  const put = (id: string | undefined, url: string | undefined) => {
    if (id && url) images.set(id, url);
  };
  for (const product of catalog) put(product?.id, product?.imageUrl);
  for (const item of items) put(item.item.productId, item.item.product.imageUrl);
  for (const card of suggestions) {
    if (!Array.isArray(card?.members)) continue;
    for (const member of card.members) put(member.productId, member.imageUrl);
  }
  return images;
}

function rowName(row: ShelfRow): string {
  return row.kind === 'group' ? row.group.name : row.item.item.product.name;
}

function groupFromSummary(summary: InventoryGroup): ProductGroup {
  return {
    id: summary.id,
    name: summary.name,
    rule: summary.rule,
    ruleConfirmed: summary.ruleConfirmed,
    pinnedProductId: summary.pinnedProductId,
    windowMonths: summary.windowMonths,
    quantity: summary.quantity,
    dimension: summary.dimension,
    members: summary.members.map((member) => ({
      productId: member.productId,
      name: member.name,
      onHand: member.onHand,
    })),
    runningLow: false,
  };
}
