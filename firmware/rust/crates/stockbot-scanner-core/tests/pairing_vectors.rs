use std::fs;
use std::path::PathBuf;

use serde::Deserialize;
use stockbot_scanner_core::{
    is_ascii_digit_product_barcode, is_pairing_code, looks_like_pairing_code, normalize_scan,
};

#[derive(Debug, Deserialize)]
struct PairingVector {
    input: String,
    normalized: Option<String>,
    #[serde(rename = "looksLike")]
    looks_like: bool,
    pairing: bool,
    gtin: bool,
}

fn repo_root() -> PathBuf {
    PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("../../../..")
}

#[test]
fn pairing_codes_json_all_sixty_cases() {
    let path = repo_root().join("testdata/pairing-codes.json");
    let raw = fs::read_to_string(&path).unwrap_or_else(|e| panic!("read {}: {e}", path.display()));
    let vectors: Vec<PairingVector> = serde_json::from_str(&raw).expect("parse pairing-codes.json");
    assert_eq!(
        vectors.len(),
        60,
        "testdata/pairing-codes.json must have 60 cases"
    );

    let mut saw_u0085 = false;
    let mut saw_nbsp = false;

    for vector in &vectors {
        let got_norm = normalize_scan(&vector.input);
        match (&vector.normalized, &got_norm) {
            (None, Some(got)) => {
                panic!("{:?} normalized to {:?}, want reject", vector.input, got);
            }
            (Some(want), got) if got.as_ref() != Some(want) => {
                panic!(
                    "{:?} normalized to {:?}, want {:?}",
                    vector.input, got, want
                );
            }
            _ => {}
        }

        assert_eq!(
            looks_like_pairing_code(&vector.input),
            vector.looks_like,
            "looksLike: input {:?}",
            vector.input
        );
        assert_eq!(
            is_pairing_code(&vector.input),
            vector.pairing,
            "pairing: input {:?}",
            vector.input
        );
        assert_eq!(
            is_ascii_digit_product_barcode(&vector.input),
            vector.gtin,
            "gtin: input {:?}",
            vector.input
        );

        if vector.input.contains('\u{0085}') {
            saw_u0085 = true;
        }
        if vector.input.contains('\u{00a0}') {
            saw_nbsp = true;
            assert!(vector.normalized.is_none());
            assert!(!vector.looks_like);
            assert!(!vector.pairing);
            assert!(!vector.gtin);
        }

        assert!(
            !(is_pairing_code(&vector.input) && is_ascii_digit_product_barcode(&vector.input)),
            "{:?} is both pairing and product",
            vector.input
        );
        assert!(
            !(looks_like_pairing_code(&vector.input)
                && is_ascii_digit_product_barcode(&vector.input)),
            "{:?} looks like pairing and is product",
            vector.input
        );
    }

    assert!(saw_u0085, "fixture must include U+0085 rows");
    assert!(saw_nbsp, "fixture must include U+00A0 rows");
}
