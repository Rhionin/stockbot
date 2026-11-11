//! Host-portable scanner core logic (barcode filter, text normalization).
#![forbid(unsafe_code)]

mod barcode;
mod ota_manifest;
mod ota_semver;
mod text;

pub use ota_manifest::{
    canonical_bytes, check_size_fits_partition, check_target, decode_public_key_b64,
    parse_canonical, validate_fields, verify_canonical, verify_fields, Fields, ManifestError,
    OtaErrorCode, TrustedKeyTable, MAX_MANIFEST_SIZE, PARTITION_SIZE_BYTES,
};
pub use ota_semver::{
    compare_semver_precedence, semver_equal_precedence, validate_semver2, versions_differ,
};

pub use barcode::{
    accept_with_duplicate_cooldown, classify_barcode, is_pairing_code, looks_like_pairing_code,
    pairing_check_character, BarcodeKind, Direction, DuplicateCooldownState, OperationalState,
    RESCAN_COOLDOWN_MS,
};
pub use text::{is_ascii_digit_product_barcode, normalize_scan, trim_ascii};
