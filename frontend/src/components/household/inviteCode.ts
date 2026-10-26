function safeDecode(segment: string): string {
  try {
    return decodeURIComponent(segment);
  } catch {
    return segment;
  }
}

/** Pull an invite code from a pasted code, path, or URL (with optional query or fragment). */
export function extractInviteCode(raw: string): string {
  let input = raw.trim();
  const hashIdx = input.indexOf('#');
  if (hashIdx >= 0) {
    input = input.slice(0, hashIdx);
  }

  const invitePath = /\/invite\/([^/?#]+)/.exec(input);
  if (invitePath) {
    return safeDecode(invitePath[1]);
  }

  const queryIdx = input.indexOf('?');
  if (queryIdx >= 0) {
    const base = input.slice(0, queryIdx);
    const query = input.slice(queryIdx + 1);
    const codeParam = new URLSearchParams(query).get('code');
    if (codeParam !== null && codeParam.trim() !== '') {
      return safeDecode(codeParam.trim());
    }
    input = base.trim();
  }

  return input;
}
