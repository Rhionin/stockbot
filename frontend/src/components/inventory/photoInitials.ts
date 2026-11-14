export function productInitials(name: string): string {
  const skip = new Set(['the', 'a', 'an']);
  const words = name.trim().split(/\s+/).filter((word) => word !== '' && !skip.has(word.toLowerCase()));
  if (words.length === 0) return '';
  if (words.length === 1) return words[0]!.slice(0, 1).toUpperCase();
  return `${words[0]!.slice(0, 1)}${words[1]!.slice(0, 1)}`.toUpperCase();
}