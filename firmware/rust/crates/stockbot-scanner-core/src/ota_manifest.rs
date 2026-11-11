//! OTA manifest field validation, canonical bytes, and Ed25519 verification (#445 S2).

use std::collections::HashMap;

use base64::alphabet::STANDARD as B64_ALPHABET;
use base64::engine::general_purpose::GeneralPurpose;
use base64::engine::DecodePaddingMode;
use base64::engine::{Engine, GeneralPurposeConfig};
#[allow(unused_imports)] // required in scope for `verify`
use ed25519_dalek::Verifier;
use ed25519_dalek::{Signature, VerifyingKey};

use crate::ota_semver::validate_semver2;

const MANIFEST_HEADER: &str = "stockbot-fw-manifest-v1";
const PRODUCT_ESP32: &str = "esp32_scanner";

/// Maximum firmware image size (3 MiB); matches partition slot size `0x300000`.
pub const MAX_MANIFEST_SIZE: i64 = 3_145_728;
pub const PARTITION_SIZE_BYTES: u32 = 0x300_000;

const SHA256_HEX_LEN: usize = 64;
const ED25519_SIG_LEN: usize = 64;
const ED25519_PUB_LEN: usize = 32;
const ED25519_SIG_B64_LEN: usize = 88;

static ALLOWED_TARGETS: &[&str] = &[
    "sticks3_u173",
    "sticks3_u173_uart",
    "atoms3lite_u173_bench",
    "atoms3lite_symcode_hid",
];

static STRICT_B64: GeneralPurpose = GeneralPurpose::new(
    &B64_ALPHABET,
    GeneralPurposeConfig::new().with_decode_padding_mode(DecodePaddingMode::RequireCanonical),
);

/// Manifest field values from the server `update` JSON object.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Fields {
    pub product: String,
    pub target: String,
    pub version: String,
    pub size: i64,
    pub sha256: String,
    pub key_id: String,
}

/// Device-facing OTA error codes reported in heartbeat `otaError`.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum OtaErrorCode {
    UnknownKey,
    BadSignature,
    WrongTarget,
    TooLarge,
}

impl OtaErrorCode {
    pub const fn as_str(self) -> &'static str {
        match self {
            Self::UnknownKey => "unknown_key",
            Self::BadSignature => "bad_signature",
            Self::WrongTarget => "wrong_target",
            Self::TooLarge => "too_large",
        }
    }
}

/// Errors from manifest parsing, validation, or signature verification.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum ManifestError {
    Validation(&'static str),
    UnknownKey,
    BadSignature,
    WrongTarget,
    TooLarge,
}

impl ManifestError {
    pub fn ota_error_code(&self) -> Option<&'static str> {
        match self {
            Self::UnknownKey => Some(OtaErrorCode::UnknownKey.as_str()),
            Self::BadSignature => Some(OtaErrorCode::BadSignature.as_str()),
            Self::WrongTarget => Some(OtaErrorCode::WrongTarget.as_str()),
            Self::TooLarge => Some(OtaErrorCode::TooLarge.as_str()),
            Self::Validation(_) => None,
        }
    }

    pub fn is_validation(&self) -> bool {
        matches!(self, Self::Validation(_))
    }
}

/// Trusted Ed25519 public keys keyed by `keyId`.
#[derive(Debug, Clone, Default)]
pub struct TrustedKeyTable {
    keys: HashMap<String, VerifyingKey>,
}

impl TrustedKeyTable {
    pub fn new() -> Self {
        Self::default()
    }

    pub fn insert(&mut self, key_id: impl Into<String>, pubkey: VerifyingKey) {
        self.keys.insert(key_id.into(), pubkey);
    }

    pub fn get(&self, key_id: &str) -> Option<&VerifyingKey> {
        self.keys.get(key_id)
    }

    pub fn from_pairs<I>(pairs: I) -> Result<Self, ManifestError>
    where
        I: IntoIterator<Item = (String, VerifyingKey)>,
    {
        let mut table = Self::new();
        for (id, pk) in pairs {
            table.insert(id, pk);
        }
        Ok(table)
    }
}

pub fn validate_fields(f: &Fields) -> Result<(), ManifestError> {
    if f.product != PRODUCT_ESP32 {
        return Err(ManifestError::Validation("product"));
    }
    if !ALLOWED_TARGETS.contains(&f.target.as_str()) {
        return Err(ManifestError::Validation("target"));
    }
    if !validate_semver2(&f.version) {
        return Err(ManifestError::Validation("version"));
    }
    if f.size <= 0 || f.size > MAX_MANIFEST_SIZE {
        return Err(ManifestError::Validation("size"));
    }
    if f.sha256.len() != SHA256_HEX_LEN || !is_lower_hex(&f.sha256) {
        return Err(ManifestError::Validation("sha256"));
    }
    if !key_id_pattern(&f.key_id) {
        return Err(ManifestError::Validation("keyId"));
    }
    Ok(())
}

/// Rejects offers whose `target` does not match the device compile-time target.
pub fn check_target(f: &Fields, device_target: &str) -> Result<(), ManifestError> {
    if f.target != device_target {
        return Err(ManifestError::WrongTarget);
    }
    Ok(())
}

/// Rejects offers whose `size` exceeds the inactive OTA partition capacity.
pub fn check_size_fits_partition(f: &Fields) -> Result<(), ManifestError> {
    if f.size > i64::from(PARTITION_SIZE_BYTES) {
        return Err(ManifestError::TooLarge);
    }
    Ok(())
}

pub fn canonical_bytes(f: &Fields) -> Result<Vec<u8>, ManifestError> {
    validate_fields(f)?;
    let size_text = f.size.to_string();
    let lines = [
        MANIFEST_HEADER,
        &format!("product={}", f.product),
        &format!("target={}", f.target),
        &format!("version={}", f.version),
        &format!("size={size_text}"),
        &format!("sha256={}", f.sha256),
        &format!("keyId={}", f.key_id),
    ];
    Ok(lines.join("\n").into_bytes())
}

pub fn parse_canonical(canonical: &[u8]) -> Result<Fields, ManifestError> {
    manifest_from_wire(canonical)
}

pub fn verify_fields(
    f: &Fields,
    sig_b64: &str,
    key_id: &str,
    table: &TrustedKeyTable,
) -> Result<(), ManifestError> {
    let canonical = canonical_bytes(f)?;
    verify_canonical(&canonical, sig_b64, key_id, table)
}

pub fn verify_canonical(
    canonical: &[u8],
    sig_b64: &str,
    key_id: &str,
    table: &TrustedKeyTable,
) -> Result<(), ManifestError> {
    let f = manifest_from_wire(canonical)?;
    if f.key_id != key_id {
        return Err(ManifestError::Validation("keyId mismatch"));
    }
    let Some(pubkey) = table.get(&f.key_id) else {
        return Err(ManifestError::UnknownKey);
    };
    let sig = decode_sig_b64(sig_b64)?;
    // `Verifier` must be in scope for `verify_strict`.
    if pubkey.verify_strict(canonical, &sig).is_err() {
        return Err(ManifestError::BadSignature);
    }
    Ok(())
}

pub fn decode_public_key_b64(b64: &str) -> Result<VerifyingKey, ManifestError> {
    let raw = STRICT_B64
        .decode(b64)
        .map_err(|_| ManifestError::Validation("public key base64"))?;
    if raw.len() != ED25519_PUB_LEN {
        return Err(ManifestError::Validation("public key length"));
    }
    let arr: [u8; ED25519_PUB_LEN] = raw
        .try_into()
        .map_err(|_| ManifestError::Validation("public key length"))?;
    VerifyingKey::from_bytes(&arr).map_err(|_| ManifestError::Validation("public key length"))
}

fn decode_sig_b64(b64: &str) -> Result<Signature, ManifestError> {
    if b64.len() != ED25519_SIG_B64_LEN {
        return Err(ManifestError::Validation("signature base64 length"));
    }
    let raw = STRICT_B64
        .decode(b64)
        .map_err(|_| ManifestError::Validation("signature base64"))?;
    if raw.len() != ED25519_SIG_LEN {
        return Err(ManifestError::Validation("signature length"));
    }
    let arr: [u8; ED25519_SIG_LEN] = raw
        .try_into()
        .map_err(|_| ManifestError::Validation("signature length"))?;
    Ok(Signature::from_bytes(&arr))
}

fn manifest_from_wire(canonical: &[u8]) -> Result<Fields, ManifestError> {
    if canonical.is_empty() {
        return Err(ManifestError::Validation("empty manifest"));
    }
    if canonical.last() == Some(&b'\n') || canonical.last() == Some(&b'\r') {
        return Err(ManifestError::Validation("trailing newline"));
    }
    let text =
        std::str::from_utf8(canonical).map_err(|_| ManifestError::Validation("invalid utf-8"))?;
    if text.contains('\r') {
        return Err(ManifestError::Validation("crlf"));
    }
    let prefix = format!("{MANIFEST_HEADER}\n");
    if !text.starts_with(&prefix) {
        return Err(ManifestError::Validation("header"));
    }
    let lines: Vec<&str> = text.split('\n').collect();
    if lines.len() != 7 {
        return Err(ManifestError::Validation("line count"));
    }
    if lines[0] != MANIFEST_HEADER {
        return Err(ManifestError::Validation("header line"));
    }
    const EXPECTED: [&str; 6] = [
        "product=", "target=", "version=", "size=", "sha256=", "keyId=",
    ];
    for (i, key_prefix) in EXPECTED.iter().enumerate() {
        let line = lines[i + 1];
        if !line.starts_with(key_prefix) {
            return Err(ManifestError::Validation("field order"));
        }
        if line.contains(' ') {
            return Err(ManifestError::Validation("spaces"));
        }
    }
    let f = fields_from_canonical_lines(&lines)?;
    validate_fields(&f)?;
    Ok(f)
}

fn fields_from_canonical_lines(lines: &[&str]) -> Result<Fields, ManifestError> {
    let parse = |line: &str, key: &str| -> Result<String, ManifestError> {
        let prefix = format!("{key}=");
        if !line.starts_with(&prefix) {
            return Err(ManifestError::Validation("parse"));
        }
        Ok(line[prefix.len()..].to_string())
    };
    let product = parse(lines[1], "product")?;
    let target = parse(lines[2], "target")?;
    let version = parse(lines[3], "version")?;
    let size_text = parse(lines[4], "size")?;
    if !size_wire_pattern(&size_text) {
        return Err(ManifestError::Validation("size"));
    }
    let size: i64 = size_text
        .parse()
        .map_err(|_| ManifestError::Validation("size"))?;
    let sha256 = parse(lines[5], "sha256")?;
    let key_id = parse(lines[6], "keyId")?;
    Ok(Fields {
        product,
        target,
        version,
        size,
        sha256,
        key_id,
    })
}

fn key_id_pattern(s: &str) -> bool {
    let bytes = s.as_bytes();
    if bytes.len() < "fw-0000-0".len() {
        return false;
    }
    if !s.starts_with("fw-") {
        return false;
    }
    let rest = &s[3..];
    let Some((year, seq)) = rest.split_once('-') else {
        return false;
    };
    year.len() == 4
        && year.bytes().all(|c| c.is_ascii_digit())
        && !seq.is_empty()
        && seq.bytes().all(|c| c.is_ascii_digit())
}

fn size_wire_pattern(s: &str) -> bool {
    let Some(first) = s.as_bytes().first() else {
        return false;
    };
    if !first.is_ascii_digit() || (*first == b'0' && s.len() > 1) {
        return false;
    }
    s.bytes().all(|c| c.is_ascii_digit())
}

fn is_lower_hex(s: &str) -> bool {
    if s.len() != SHA256_HEX_LEN {
        return false;
    }
    s.bytes().all(|c| matches!(c, b'0'..=b'9' | b'a'..=b'f')) && hex::decode(s).is_ok()
}

mod hex {
    pub fn decode(s: &str) -> Result<Vec<u8>, ()> {
        if s.len() % 2 != 0 {
            return Err(());
        }
        let mut out = Vec::with_capacity(s.len() / 2);
        let bytes = s.as_bytes();
        let mut i = 0;
        while i < bytes.len() {
            let hi = nib(bytes[i])?;
            let lo = nib(bytes[i + 1])?;
            out.push((hi << 4) | lo);
            i += 2;
        }
        Ok(out)
    }

    fn nib(c: u8) -> Result<u8, ()> {
        match c {
            b'0'..=b'9' => Ok(c - b'0'),
            b'a'..=b'f' => Ok(c - b'a' + 10),
            _ => Err(()),
        }
    }
}
