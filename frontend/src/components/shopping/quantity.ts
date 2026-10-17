// The package word and the size share one label. "boxes" is only a count.
// "14.5 oz" is a size, so a count beside it needs a separator.
const sizeLabel = /\d+(?:[.,]\d+)?\s*(?:fl\.?\s*oz|lbs?|kg|ml|oz|g|l)\b/i;

export function formatShoppingQuantity(count: number | null | undefined, label: string): string {
  const text = label.trim();
  const hasCount = typeof count === 'number' && Number.isFinite(count);
  if (hasCount && text !== '' && sizeLabel.test(text)) return `${count} × ${text}`;
  if (hasCount && text !== '') return `${count} ${text}`;
  if (hasCount) return String(count);
  return text;
}
