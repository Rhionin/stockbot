//! Host-portable scanner core logic (barcode filter, text normalization).
#![forbid(unsafe_code)]

mod barcode;
mod text;

pub use barcode::{
    accept_with_duplicate_cooldown, classify_barcode, is_pairing_code, looks_like_pairing_code,
    pairing_check_character, BarcodeKind, Direction, DuplicateCooldownState, OperationalState,
    RESCAN_COOLDOWN_MS,
};
pub use text::{is_ascii_digit_product_barcode, normalize_scan, trim_ascii};
