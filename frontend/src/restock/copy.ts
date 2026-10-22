// Static labels. Resolved phrases come from the API so the screen and the
// planner describe a target the same way.
export const restockCopy = {
  target: 'Restock target…',
  changeTarget: 'Change target…',
  dontItem: "Don't restock",
  dontItemHint: "Sets this item's target to 0",
  categories: 'Categories…',
  useCategory: 'Use category target',
  household: 'Household default',
  setOnCategory: 'Set on category',
  section: 'Restock targets',
  sectionBody: "The most specific target wins. Anything that doesn't set one uses the household default.",
  applies: (applies: number, total: number) => `Applies to ${applies} of ${total} items. 0 means don't restock anything by default.`,
  notRestocked: (count: number) => `${count} ${count === 1 ? "item isn't" : "items aren't"} restocked.`,
  review: 'Review',
  suggestions: (count: number) => `${count} ${count === 1 ? 'suggestion' : 'suggestions'} from Open Food Facts tags and product names`,
  nothingChanges: 'Nothing changes until you add it.',
  previewHint: (count: number) => `Preview the ${count} ${count === 1 ? 'item' : 'items'} first`,
};

const banned = /\b(inherit|inherited|override|precedence)\b/i;

export function plainCopy(text: string): boolean {
  return !banned.test(text);
}
