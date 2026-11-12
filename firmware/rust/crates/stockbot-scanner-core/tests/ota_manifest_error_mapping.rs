mod common;

use base64::engine::general_purpose::STANDARD;
use base64::Engine;
use ed25519_dalek::SigningKey;
use stockbot_scanner_core::{
    check_target, decode_public_key_b64, validate_fields, verify_canonical, verify_pre_download,
    ManifestError, OtaErrorCode, TrustedKeyTable, MAX_MANIFEST_SIZE,
};

fn golden() -> common::vector_fixtures::GoldenVector {
    common::vector_fixtures::golden_valid()
}

fn allowlist(v: &common::vector_fixtures::GoldenVector) -> TrustedKeyTable {
    let pk = decode_public_key_b64(&v.public_key_b64).unwrap();
    let mut table = TrustedKeyTable::new();
    table.insert(v.fields.key_id.clone(), pk);
    table
}

#[test]
fn size_3145728_accepted_3145729_too_large() {
    let mut f = golden().fields();
    f.size = MAX_MANIFEST_SIZE;
    validate_fields(&f).expect("3145728 must be valid");
    f.size = MAX_MANIFEST_SIZE + 1;
    let err = validate_fields(&f).unwrap_err();
    assert_eq!(err, ManifestError::TooLarge);
    assert_eq!(err.ota_error_code(), OtaErrorCode::TooLarge.as_str());
    assert_ne!(
        err.ota_error_code(),
        OtaErrorCode::BadSignature.as_str(),
        "3145729 must not map to bad_signature"
    );
}

#[test]
fn invalid_size_wire_not_too_large() {
    let v = golden();
    let table = allowlist(&v);
    for (name, wire) in [
        ("+5", v.canonical.replace("size=1532416", "size=+5")),
        ("007", v.canonical.replace("size=1532416", "size=007")),
        ("-1", v.canonical.replace("size=1532416", "size=-1")),
    ] {
        let err =
            verify_canonical(wire.as_bytes(), &v.sig_b64, &v.fields.key_id, &table).unwrap_err();
        assert!(err.is_validation(), "{name}: {err:?}");
        assert_eq!(
            err.ota_error_code(),
            OtaErrorCode::BadSignature.as_str(),
            "{name} must be bad_signature not too_large"
        );
        assert_ne!(err.ota_error_code(), OtaErrorCode::TooLarge.as_str());
    }
}

#[test]
fn uppercase_sha256_is_bad_signature_not_bad_sha256() {
    let mut f = golden().fields();
    f.sha256 = f.sha256.to_uppercase();
    let err = validate_fields(&f).unwrap_err();
    assert!(err.is_validation());
    assert_eq!(err.ota_error_code(), "bad_signature");
    assert_ne!(err.ota_error_code(), "bad_sha256");
}

#[test]
fn malformed_key_id_is_bad_signature_not_unknown_key() {
    let mut f = golden().fields();
    f.key_id = "fw-26-1".into();
    let err = validate_fields(&f).unwrap_err();
    assert!(err.is_validation());
    assert_eq!(err.ota_error_code(), "bad_signature");
    assert_ne!(err.ota_error_code(), "unknown_key");
}

#[test]
fn foreign_target_install_path_wrong_target_even_off_allowlist() {
    let v = golden();
    let table = allowlist(&v);
    let mut wire = v.canonical.clone();
    wire = wire.replace(
        "target=atoms3lite_u173_bench",
        "target=completely_unknown_target",
    );
    let device = "sticks3_u173";
    let err = verify_pre_download(
        wire.as_bytes(),
        &v.sig_b64,
        &v.fields.key_id,
        device,
        &table,
    )
    .unwrap_err();
    assert_eq!(err, ManifestError::WrongTarget);
    assert_eq!(err.ota_error_code(), "wrong_target");
}

#[test]
fn foreign_allowlisted_target_still_wrong_target() {
    let f = golden().fields();
    let table = allowlist(&golden());
    assert_eq!(
        check_target(&f, "sticks3_u173").unwrap_err(),
        ManifestError::WrongTarget
    );
    let v = golden();
    let err = verify_pre_download(
        v.canonical.as_bytes(),
        &v.sig_b64,
        &v.fields.key_id,
        "sticks3_u173",
        &table,
    )
    .unwrap_err();
    assert_eq!(err, ManifestError::WrongTarget);
}

#[test]
fn ota_error_code_mapping_table() {
    assert_eq!(ManifestError::UnknownKey.ota_error_code(), "unknown_key");
    assert_eq!(
        ManifestError::BadSignature.ota_error_code(),
        "bad_signature"
    );
    assert_eq!(ManifestError::WrongTarget.ota_error_code(), "wrong_target");
    assert_eq!(ManifestError::TooLarge.ota_error_code(), "too_large");
    for tag in [
        "product",
        "target",
        "version",
        "size",
        "sha256",
        "keyId",
        "crlf",
        "line count",
        "keyId mismatch",
        "signature length",
    ] {
        assert_eq!(
            ManifestError::Validation(tag).ota_error_code(),
            "bad_signature",
            "Validation({tag})"
        );
    }
}

#[test]
fn well_formed_unknown_key_id_maps_unknown_key() {
    let v = golden();
    let other_seed: [u8; 32] = std::array::from_fn(|i| (i + 1) as u8);
    let other_pk = SigningKey::from_bytes(&other_seed).verifying_key();
    let mut table = TrustedKeyTable::new();
    table.insert("fw-2026-2".to_string(), other_pk);
    let err =
        verify_canonical(v.canonical.as_bytes(), &v.sig_b64, &v.fields.key_id, &table).unwrap_err();
    assert_eq!(err, ManifestError::UnknownKey);
    assert_eq!(err.ota_error_code(), "unknown_key");
}

#[test]
fn caller_key_id_mismatch_maps_bad_signature() {
    let v = golden();
    let table = allowlist(&v);
    let err =
        verify_canonical(v.canonical.as_bytes(), &v.sig_b64, "fw-1999-9", &table).unwrap_err();
    assert!(err.is_validation());
    assert_eq!(err.ota_error_code(), "bad_signature");
}

#[test]
fn verify_pre_download_happy_path() {
    let v = golden();
    let table = allowlist(&v);
    let fields = verify_pre_download(
        v.canonical.as_bytes(),
        &v.sig_b64,
        &v.fields.key_id,
        "atoms3lite_u173_bench",
        &table,
    )
    .expect("golden manifest");
    assert_eq!(fields.product, "esp32_scanner");
    assert_eq!(fields.target, "atoms3lite_u173_bench");
}

#[test]
fn verify_strict_failure_maps_bad_signature() {
    let v = golden();
    let table = allowlist(&v);
    let mut raw = STANDARD.decode(&v.sig_b64).unwrap();
    raw[0] ^= 0xff;
    let tampered = STANDARD.encode(&raw);
    let err =
        verify_canonical(v.canonical.as_bytes(), &tampered, &v.fields.key_id, &table).unwrap_err();
    assert_eq!(err, ManifestError::BadSignature);
    assert_eq!(err.ota_error_code(), "bad_signature");
}
