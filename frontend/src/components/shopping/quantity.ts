// unitOfMeasure is free text. A label that starts with a digit is a size
// ("14.5 oz", "1 gal", "16oz"). A package word ("boxes") is only a count.
const sizeLabel = /^\d/;

export function formatShoppingQuantity(count: number | null | undefined, label: string): string {
  const text = label.trim();
  const hasCount = typeof count === 'number' && Number.isFinite(count);
  if (hasCount && text !== '' && sizeLabel.test(text)) return `${count} × ${text}`;
  if (hasCount && text !== '') return `${count} ${text}`;
  if (hasCount) return String(count);
  return text;
}
