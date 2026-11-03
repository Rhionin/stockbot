const SUFFIX_ALPHABET = '23456789ABCDEFGHJKLMNPQRSTUVWXYZ';

/** First 4 base32-ish chars from SHA-256(householdId), UX alphabet without 0/O/1/I (§9A.7). */
export async function householdPantrySuffix(householdId: string): Promise<string> {
  const digest = await crypto.subtle.digest('SHA-256', new TextEncoder().encode(householdId));
  const bytes = new Uint8Array(digest);
  let bits = 0;
  let value = 0;
  let out = '';
  for (let i = 0; i < bytes.length && out.length < 4; i += 1) {
    value = (value << 8) | bytes[i];
    bits += 8;
    while (bits >= 5 && out.length < 4) {
      bits -= 5;
      const index = (value >> bits) & 31;
      out += SUFFIX_ALPHABET[index % SUFFIX_ALPHABET.length];
    }
  }
  while (out.length < 4) {
    out += SUFFIX_ALPHABET[out.length % SUFFIX_ALPHABET.length];
  }
  return out.slice(0, 4);
}

function stripKroger(householdName: string): string {
  const without = householdName.replace(/\bkroger\b/gi, '').replace(/\s+/g, ' ').trim();
  return without;
}

function trimHouseholdPart(name: string, max = 30): string {
  if (name.length <= max) return name;
  const slice = name.slice(0, max);
  const lastSpace = slice.lastIndexOf(' ');
  if (lastSpace > 10) return slice.slice(0, lastSpace).trim();
  return slice.trim();
}

export async function suggestedKrogerAppName(householdName: string, householdId: string): Promise<string> {
  const suffix = await householdPantrySuffix(householdId);
  const base = stripKroger(householdName);
  if (base === '') {
    return `Our pantry ${suffix}`;
  }
  const part = trimHouseholdPart(base);
  return `${part} pantry ${suffix}`;
}
