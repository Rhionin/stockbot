use std::fs;
use std::path::PathBuf;

use serde::Deserialize;
use stockbot_scanner_core::{
    canonical_bytes, decode_public_key_b64, verify_canonical, Fields, ManifestError,
    TrustedKeyTable,
};

#[derive(Debug, Deserialize)]
struct ManifestVector {
    name: String,
    #[serde(rename = "seedHex")]
    seed_hex: String,
    #[serde(rename = "publicKeyB64")]
    public_key_b64: String,
    fields: FieldsJson,
    canonical: String,
    #[serde(rename = "sigB64")]
    sig_b64: String,
    valid: bool,
}

#[derive(Debug, Clone, Deserialize)]
struct FieldsJson {
    product: String,
    target: String,
    version: String,
    size: i64,
    sha256: String,
    #[serde(rename = "keyId")]
    key_id: String,
}

impl FieldsJson {
    fn into_fields(self) -> Fields {
        Fields {
            product: self.product,
            target: self.target,
            version: self.version,
            size: self.size,
            sha256: self.sha256,
            key_id: self.key_id,
        }
    }
}

fn repo_root() -> PathBuf {
    PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("../../../..")
}

fn load_vectors() -> Vec<ManifestVector> {
    let path = repo_root().join("firmware/test/vectors/ota_manifest_v1.json");
    let raw = fs::read_to_string(&path).unwrap_or_else(|e| panic!("read {}: {e}", path.display()));
    let vectors: Vec<ManifestVector> =
        serde_json::from_str(&raw).expect("parse ota_manifest_v1.json");
    assert_eq!(
        vectors.len(),
        7,
        "ota_manifest_v1.json must have exactly 7 cases"
    );
    vectors
}

fn mutation_expects_validation(name: &str) -> bool {
    matches!(
        name,
        "mutate-product-byte" | "mutate-target-byte" | "mutate-keyId-byte"
    )
}

#[test]
fn ota_manifest_v1_all_seven_vectors() {
    let vectors = load_vectors();
    let mut table = TrustedKeyTable::new();
    for v in &vectors {
        assert!(
            !v.seed_hex.is_empty() && !v.seed_hex.contains("RELEASE"),
            "vector {:?} must use a public test-only seed",
            v.name
        );
        let pk = decode_public_key_b64(&v.public_key_b64).expect("decode pubkey");
        table.insert(v.fields.key_id.clone(), pk);
    }

    for v in &vectors {
        let f = v.fields.clone().into_fields();
        if v.valid {
            let got = canonical_bytes(&f).expect("CanonicalBytes");
            assert_eq!(
                String::from_utf8(got).unwrap(),
                v.canonical,
                "canonical bytes mismatch for {}",
                v.name
            );
        }
        let verify_result =
            verify_canonical(v.canonical.as_bytes(), &v.sig_b64, &v.fields.key_id, &table);
        if v.valid {
            verify_result.expect("verify valid vector");
        } else {
            let err = verify_result.expect_err("expected verification failure");
            if mutation_expects_validation(&v.name) {
                assert!(err.is_validation(), "want validation error for {}", v.name);
            } else {
                assert_eq!(
                    err,
                    ManifestError::BadSignature,
                    "want signature crypto failure for {}",
                    v.name
                );
            }
        }
    }
}
