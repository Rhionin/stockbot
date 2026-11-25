// Short label for a QR payload shown in the queue and the scan notification.
// A URL shows its host. Wi-Fi, contact, and authenticator payloads show a
// fixed label. Any other scheme or plain text is "QR code". The payload
// itself is never shown. Keep this in step with QRPreview in internal/scan/qr.go.

const qrPreviewRunes = 48;

// Same ASCII set as TrimASCII: space, tab, LF, VT, FF, and CR.
const asciiSpace = new Set([0x20, 0x09, 0x0a, 0x0b, 0x0c, 0x0d]);

export function qrPreview(raw: string): string {
  const value = trimASCII(raw);
  if (value === '') return '';
  const host = qrHost(value);
  if (host !== null) return shorten(host, qrPreviewRunes);
  const label = qrSchemeLabel(value);
  if (label !== '') return label;
  return 'QR code';
}

function trimASCII(raw: string): string {
  let start = 0;
  let end = raw.length;
  while (start < end && asciiSpace.has(raw.charCodeAt(start))) start += 1;
  while (end > start && asciiSpace.has(raw.charCodeAt(end - 1))) end -= 1;
  return raw.slice(start, end);
}

function qrSchemeLabel(value: string): string {
  const lower = value.toLowerCase();
  if (lower.startsWith('wifi:')) return 'Wi-Fi code';
  if (lower.startsWith('begin:vcard')) return 'Contact card';
  if (lower.startsWith('otpauth:')) return 'Authenticator code';
  const colon = value.indexOf(':');
  if (colon <= 0 || colon >= 32) return '';
  if (lower.startsWith('http:') || lower.startsWith('https:')) return '';
  const prefix = lower.slice(0, colon);
  for (const char of prefix) {
    const scheme = (char >= 'a' && char <= 'z')
      || (char >= '0' && char <= '9')
      || char === '+'
      || char === '-'
      || char === '.';
    if (!scheme) return '';
  }
  return 'QR code';
}

function qrHost(value: string): string | null {
  const lower = value.toLowerCase();
  let candidate: string;
  if (lower.startsWith('https://') || lower.startsWith('http://')) {
    candidate = value;
  } else if (lower.startsWith('www.')) {
    candidate = `https://${value}`;
  } else {
    return null;
  }
  try {
    const host = new URL(candidate).hostname.toLowerCase();
    return host === '' ? null : host;
  } catch {
    return null;
  }
}

function shorten(value: string, maxRunes: number): string {
  const runes = Array.from(value);
  if (runes.length <= maxRunes) return value;
  if (maxRunes <= 1) return '…';
  return `${runes.slice(0, maxRunes - 1).join('')}…`;
}
