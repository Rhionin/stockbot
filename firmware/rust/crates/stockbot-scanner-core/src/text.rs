const ASCII_SPACE: &[u8] = &[b' ', b'\t', b'\n', 0x0b, 0x0c, b'\r'];

/// Trims leading and trailing ASCII whitespace (space, tab, LF, VT, FF, CR only).
pub fn trim_ascii(raw: &str) -> String {
    let bytes = raw.as_bytes();
    let mut start = 0;
    let mut end = bytes.len();
    while start < end && ASCII_SPACE.contains(&bytes[start]) {
        start += 1;
    }
    while end > start && ASCII_SPACE.contains(&bytes[end - 1]) {
        end -= 1;
    }
    String::from_utf8(bytes[start..end].to_vec()).expect("input is valid UTF-8")
}

/// Uppercase ASCII a-z; map O→0, I/L→1. Returns `None` on non-ASCII.
pub fn normalize_scan(raw: &str) -> Option<String> {
    let trimmed = trim_ascii(raw);
    let mut out = String::with_capacity(trimmed.len());
    for b in trimmed.bytes() {
        if b > 127 {
            return None;
        }
        let mut ch = b;
        if ch.is_ascii_lowercase() {
            ch = ch - b'a' + b'A';
        }
        ch = match ch {
            b'O' => b'0',
            b'I' | b'L' => b'1',
            _ => ch,
        };
        out.push(ch as char);
    }
    Some(out)
}

/// True when `raw` trims to 1–14 ASCII digits (matches server `scan.IsProductBarcode`).
pub fn is_ascii_digit_product_barcode(raw: &str) -> bool {
    let value = trim_ascii(raw);
    if value.is_empty() || value.len() > 14 {
        return false;
    }
    value.bytes().all(|b| b.is_ascii_digit())
}
