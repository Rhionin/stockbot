use crate::text::{is_ascii_digit_product_barcode, normalize_scan, trim_ascii};

const PAIRING_PAYLOAD_ALPHABET: &[u8] = b"0123456789ABCDEFGHJKMNPQRSTVWXYZ";
const PAIRING_CHECK_ALPHABET: &[u8] = b"0123456789ABCDEFGHJKMNPQRSTVWXYZ*~$=U";

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum OperationalState {
    Unprovisioned,
    Pairing,
    Paired,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Direction {
    StockOut,
    StockIn,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum BarcodeKind {
    Product,
    ControlStockOut,
    ControlStockIn,
    PairingCode,
    SetupPayload,
    RejectedNon1D,
}

pub const RESCAN_COOLDOWN_MS: u64 = 2000;

#[derive(Debug, Clone)]
pub struct DuplicateCooldownState {
    pub last_barcode: String,
    pub last_direction: Direction,
    pub last_accept_ms: u64,
}

impl Default for DuplicateCooldownState {
    fn default() -> Self {
        Self {
            last_barcode: String::new(),
            last_direction: Direction::StockOut,
            last_accept_ms: 0,
        }
    }
}

fn looks_like_setup_json(trimmed: &str) -> bool {
    if trimmed.starts_with("stockbot://pair") {
        return true;
    }
    if let Some(pos) = trimmed.find("{\"v\":1") {
        return pos == 0 || trimmed.as_bytes().get(pos) == Some(&b'{');
    }
    trimmed.len() >= 2 && trimmed.starts_with('{') && trimmed.contains("\"ssid\"")
}

fn pairing_grammar_match(code: &str) -> bool {
    let bytes = code.as_bytes();
    if code.len() != 15
        || !code.starts_with("SBP-")
        || bytes.get(8) != Some(&b'-')
        || bytes.get(13) != Some(&b'-')
    {
        return false;
    }
    let valid_char = |ch: u8, check_pos: bool| -> bool {
        if check_pos && matches!(ch, b'*' | b'~' | b'$' | b'=' | b'U') {
            return true;
        }
        if ch.is_ascii_digit() {
            return true;
        }
        if ch.is_ascii_uppercase() {
            return ch != b'I' && ch != b'O' && ch != b'U';
        }
        false
    };
    for ch in &bytes[4..8] {
        if !valid_char(*ch, false) {
            return false;
        }
    }
    for ch in &bytes[9..13] {
        if !valid_char(*ch, false) {
            return false;
        }
    }
    valid_char(bytes[14], true)
}

/// Mod-37 check character for the 8-character pairing payload.
pub fn pairing_check_character(payload8: &str) -> Option<char> {
    let mut value: u64 = 0;
    for b in payload8.bytes() {
        let pos = PAIRING_PAYLOAD_ALPHABET.iter().position(|&c| c == b)?;
        value = value * 32 + pos as u64;
    }
    Some(PAIRING_CHECK_ALPHABET[(value % 37) as usize] as char)
}

pub fn looks_like_pairing_code(raw: &str) -> bool {
    let Some(code) = normalize_scan(raw) else {
        return false;
    };
    pairing_grammar_match(&code)
}

pub fn is_pairing_code(raw: &str) -> bool {
    let Some(code) = normalize_scan(raw) else {
        return false;
    };
    if !pairing_grammar_match(&code) {
        return false;
    }
    let payload = format!("{}{}", &code[4..8], &code[9..13]);
    let Some(check) = pairing_check_character(&payload) else {
        return false;
    };
    code.as_bytes()[14] == check as u8
}

fn trim_and_upper_ascii(raw: &str) -> String {
    trim_ascii(raw)
        .bytes()
        .map(|b| {
            if b.is_ascii_lowercase() {
                (b - b'a' + b'A') as char
            } else {
                b as char
            }
        })
        .collect()
}

pub fn classify_barcode(raw: &str, state: OperationalState) -> BarcodeKind {
    let control_view = trim_and_upper_ascii(raw);
    if control_view == "STOCK-OUT" || control_view == "STOCK_OUT" {
        return BarcodeKind::ControlStockOut;
    }
    if control_view == "STOCK-IN" || control_view == "STOCK_IN" {
        return BarcodeKind::ControlStockIn;
    }

    if normalize_scan(raw).is_none() {
        let trimmed = trim_ascii(raw);
        if state == OperationalState::Unprovisioned && looks_like_setup_json(&trimmed) {
            return BarcodeKind::SetupPayload;
        }
        return BarcodeKind::RejectedNon1D;
    }

    if looks_like_pairing_code(raw) {
        if state == OperationalState::Paired {
            return BarcodeKind::RejectedNon1D;
        }
        if is_pairing_code(raw) {
            return BarcodeKind::PairingCode;
        }
        return BarcodeKind::RejectedNon1D;
    }

    let trimmed = trim_ascii(raw);
    if state == OperationalState::Unprovisioned && looks_like_setup_json(&trimmed) {
        return BarcodeKind::SetupPayload;
    }

    if matches!(state, OperationalState::Pairing | OperationalState::Paired)
        && (looks_like_setup_json(&trimmed) || trimmed.contains("://") || trimmed.contains('{'))
    {
        return BarcodeKind::RejectedNon1D;
    }

    if state == OperationalState::Paired {
        if is_ascii_digit_product_barcode(raw) {
            return BarcodeKind::Product;
        }
        return BarcodeKind::RejectedNon1D;
    }

    if state == OperationalState::Unprovisioned && is_pairing_code(raw) {
        return BarcodeKind::PairingCode;
    }

    BarcodeKind::RejectedNon1D
}

pub fn accept_with_duplicate_cooldown(
    state: &mut DuplicateCooldownState,
    barcode: &str,
    direction: Direction,
    now_ms: u64,
    manual_retrigger: bool,
) -> bool {
    if manual_retrigger {
        state.last_barcode = barcode.to_string();
        state.last_direction = direction;
        state.last_accept_ms = now_ms;
        return true;
    }
    if barcode != state.last_barcode || direction != state.last_direction {
        state.last_barcode = barcode.to_string();
        state.last_direction = direction;
        state.last_accept_ms = now_ms;
        return true;
    }
    if state.last_accept_ms == 0
        || now_ms.saturating_sub(state.last_accept_ms) >= RESCAN_COOLDOWN_MS
    {
        state.last_accept_ms = now_ms;
        return true;
    }
    false
}
