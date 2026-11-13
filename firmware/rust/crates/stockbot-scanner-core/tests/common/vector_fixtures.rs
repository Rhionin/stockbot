#![allow(dead_code)]

use std::fs;
use std::path::PathBuf;

use serde::Deserialize;
use stockbot_scanner_core::Fields;

#[derive(Clone, Debug, Deserialize)]
pub struct GoldenVector {
    pub name: String,
    #[serde(rename = "publicKeyB64")]
    pub public_key_b64: String,
    pub fields: FieldsJson,
    pub canonical: String,
    #[serde(rename = "sigB64")]
    pub sig_b64: String,
}

#[derive(Clone, Debug, Deserialize)]
pub struct FieldsJson {
    pub product: String,
    pub target: String,
    pub version: String,
    pub size: i64,
    pub sha256: String,
    #[serde(rename = "keyId")]
    pub key_id: String,
}

impl FieldsJson {
    pub fn to_fields(&self) -> Fields {
        Fields {
            product: self.product.clone(),
            target: self.target.clone(),
            version: self.version.clone(),
            size: self.size,
            sha256: self.sha256.clone(),
            key_id: self.key_id.clone(),
        }
    }
}

impl GoldenVector {
    pub fn fields(&self) -> Fields {
        self.fields.to_fields()
    }
}

fn repo_root() -> PathBuf {
    PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("../../../..")
}

pub fn golden_valid() -> GoldenVector {
    let path = repo_root().join("firmware/test/vectors/ota_manifest_v1.json");
    let raw = fs::read_to_string(&path).unwrap();
    let vectors: Vec<GoldenVector> = serde_json::from_str(&raw).unwrap();
    vectors
        .into_iter()
        .find(|v| v.name == "golden-valid")
        .expect("golden-valid vector missing")
}

pub fn mutate_in_alphabet_nonzero_pad_bits(b64: &str) -> Result<String, String> {
    const ALPHABET: &[u8] = b"ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";
    use base64::alphabet::STANDARD as B64_ALPHABET;
    use base64::engine::general_purpose::GeneralPurpose;
    use base64::engine::DecodePaddingMode;
    use base64::engine::{Engine, GeneralPurposeConfig};
    static PERMISSIVE: GeneralPurpose = GeneralPurpose::new(
        &B64_ALPHABET,
        GeneralPurposeConfig::new()
            .with_decode_allow_trailing_bits(true)
            .with_decode_padding_mode(DecodePaddingMode::Indifferent),
    );
    let orig = PERMISSIVE.decode(b64).map_err(|e| e.to_string())?;
    let bytes = b64.as_bytes();
    for i in 0..bytes.len() {
        if bytes[i] == b'=' {
            continue;
        }
        for &nc in ALPHABET {
            if nc == bytes[i] {
                continue;
            }
            let mut mutated = String::new();
            mutated.push_str(&b64[..i]);
            mutated.push(nc as char);
            mutated.push_str(&b64[i + 1..]);
            if base64_strict_decode(&mutated).is_err() {
                if let Ok(raw_perm) = PERMISSIVE.decode(&mutated) {
                    if raw_perm == orig {
                        return Ok(mutated);
                    }
                }
            }
        }
    }
    Err("no in-alphabet nonzero pad-bit mutation found".into())
}

fn base64_strict_decode(b64: &str) -> Result<Vec<u8>, ()> {
    use base64::alphabet::STANDARD as B64_ALPHABET;
    use base64::engine::general_purpose::GeneralPurpose;
    use base64::engine::DecodePaddingMode;
    use base64::engine::{Engine, GeneralPurposeConfig};
    static STRICT: GeneralPurpose = GeneralPurpose::new(
        &B64_ALPHABET,
        GeneralPurposeConfig::new().with_decode_padding_mode(DecodePaddingMode::RequireCanonical),
    );
    STRICT.decode(b64).map_err(|_| ())
}
