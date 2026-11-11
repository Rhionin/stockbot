//! Ports of `internal/firmware/manifest_test.go` and `semver_test.go` (Rust-only tables).

#![allow(clippy::type_complexity)]

use base64::engine::general_purpose::STANDARD;
use base64::Engine;
use ed25519_dalek::{Signer, SigningKey, VerifyingKey};
use stockbot_scanner_core::{
    check_size_fits_partition, check_target, decode_public_key_b64, parse_canonical,
    validate_fields, validate_semver2, verify_canonical, verify_fields, versions_differ, Fields,
    ManifestError, OtaErrorCode, TrustedKeyTable, MAX_MANIFEST_SIZE, PARTITION_SIZE_BYTES,
};

mod vector_fixtures;

fn golden() -> vector_fixtures::GoldenVector {
    vector_fixtures::golden_valid()
}

fn allowlist(v: &vector_fixtures::GoldenVector) -> TrustedKeyTable {
    let pk = decode_public_key_b64(&v.public_key_b64).unwrap();
    let mut t = TrustedKeyTable::new();
    t.insert(v.fields.key_id.clone(), pk);
    t
}

// --- manifest_test.go ---

#[test]
fn go_test_verify_fields_matches_canonical() {
    let v = golden();
    let table = allowlist(&v);
    let f = v.fields();
    verify_fields(&f, &v.sig_b64, &v.fields.key_id, &table).unwrap();
}

#[test]
fn go_test_architect_wire_nogo_semver_rejects_non_semver2_forms() {
    let mut f = golden().fields();
    for ver in ["01.0.0", "1.0.0-01", "1.0.0-rc..1"] {
        f.version = ver.to_string();
        let err = validate_fields(&f).unwrap_err();
        assert!(err.is_validation(), "version {ver}: {err:?}");
    }
}

#[test]
fn go_test_architect_wire_nogo_size_wire_rejects_plus_prefix() {
    let v = golden();
    let wire = v.canonical.replace("size=1532416", "size=+1532416");
    let table = allowlist(&v);
    let err = verify_canonical(wire.as_bytes(), &v.sig_b64, &v.fields.key_id, &table).unwrap_err();
    assert!(err.is_validation());
}

#[test]
fn go_test_architect_wire_nogo_strict_signature_base64_padding() {
    let v = golden();
    let mut bad: Vec<u8> = v.sig_b64.bytes().collect();
    assert_eq!(
        bad.last().copied(),
        Some(b'='),
        "expected padded signature ending in ="
    );
    let last = bad.len() - 1;
    bad[last] = b'`';
    let bad = String::from_utf8(bad).unwrap();
    let table = allowlist(&v);
    let err = verify_canonical(v.canonical.as_bytes(), &bad, &v.fields.key_id, &table).unwrap_err();
    assert!(err.is_validation());
}

#[test]
fn go_test_architect_wire_nogo_caller_key_id_must_match_signed_key_id() {
    let v = golden();
    let wire = v.canonical.replace("keyId=fw-2026-1", "keyId=fw-2026-2");
    let seed = [0x3b_u8; 32];
    let signing = SigningKey::from_bytes(&seed);
    let sig = signing.sign(wire.as_bytes());
    let sig_b64 = STANDARD.encode(sig.to_bytes());
    let mut table = TrustedKeyTable::new();
    let vk: VerifyingKey = signing.verifying_key();
    table.insert("fw-2026-1".to_string(), vk);
    table.insert("fw-2026-2".to_string(), vk);
    let err = verify_canonical(wire.as_bytes(), &sig_b64, "fw-2026-1", &table).unwrap_err();
    assert!(err.is_validation());
}

#[test]
fn go_test_canonical_wire_rejects_tampering() {
    let v = golden();
    let table = allowlist(&v);
    let base = v.canonical.as_bytes();
    let cases: Vec<(&str, Box<dyn Fn(&[u8]) -> Vec<u8>>)> = vec![
        (
            "trailing LF",
            Box::new(|b| {
                let mut o = b.to_vec();
                o.push(b'\n');
                o
            }),
        ),
        (
            "trailing CRLF",
            Box::new(|b| {
                let mut o = b.to_vec();
                o.extend_from_slice(b"\r\n");
                o
            }),
        ),
        (
            "crlf middle",
            Box::new(|b| {
                String::from_utf8(b.to_vec())
                    .unwrap()
                    .replace('\n', "\r\n")
                    .into_bytes()
            }),
        ),
        (
            "extra space",
            Box::new(|b| {
                String::from_utf8(b.to_vec())
                    .unwrap()
                    .replacen("product=", "product= ", 1)
                    .into_bytes()
            }),
        ),
        (
            "field order swap",
            Box::new(|b| {
                let mut lines: Vec<_> = String::from_utf8(b.to_vec())
                    .unwrap()
                    .split('\n')
                    .map(String::from)
                    .collect();
                lines.swap(1, 2);
                lines.join("\n").into_bytes()
            }),
        ),
    ];
    for (name, mutate) in cases {
        let err = verify_canonical(&mutate(base), &v.sig_b64, &v.fields.key_id, &table);
        assert!(err.is_err(), "{name} should reject");
    }
}

#[test]
fn go_test_validate_fields_negative() {
    let valid = golden().fields();
    struct Case {
        name: &'static str,
        mut_fields: Box<dyn Fn(Fields) -> Fields>,
        wire: Option<Box<dyn Fn(&vector_fixtures::GoldenVector) -> Vec<u8>>>,
    }
    let cases = vec![
        Case {
            name: "wrong product",
            mut_fields: Box::new(|mut f| {
                f.product = "other".into();
                f
            }),
            wire: None,
        },
        Case {
            name: "unknown target",
            mut_fields: Box::new(|mut f| {
                f.target = "unknown_target".into();
                f
            }),
            wire: None,
        },
        Case {
            name: "version v-prefix",
            mut_fields: Box::new(|mut f| {
                f.version = "v1.0.0".into();
                f
            }),
            wire: None,
        },
        Case {
            name: "malformed semver",
            mut_fields: Box::new(|mut f| {
                f.version = "1.0".into();
                f
            }),
            wire: None,
        },
        Case {
            name: "semver leading zero core",
            mut_fields: Box::new(|mut f| {
                f.version = "01.0.0".into();
                f
            }),
            wire: None,
        },
        Case {
            name: "semver leading zero prerelease",
            mut_fields: Box::new(|mut f| {
                f.version = "1.0.0-01".into();
                f
            }),
            wire: None,
        },
        Case {
            name: "semver empty prerelease id",
            mut_fields: Box::new(|mut f| {
                f.version = "1.0.0-rc..1".into();
                f
            }),
            wire: None,
        },
        Case {
            name: "zero size",
            mut_fields: Box::new(|mut f| {
                f.size = 0;
                f
            }),
            wire: None,
        },
        Case {
            name: "oversized",
            mut_fields: Box::new(|mut f| {
                f.size = MAX_MANIFEST_SIZE + 1;
                f
            }),
            wire: None,
        },
        Case {
            name: "leading zero size wire",
            mut_fields: Box::new(|f| f),
            wire: Some(Box::new(|gv| {
                gv.canonical
                    .replace("size=1532416", "size=042")
                    .into_bytes()
            })),
        },
        Case {
            name: "plus size wire",
            mut_fields: Box::new(|f| f),
            wire: Some(Box::new(|gv| {
                gv.canonical
                    .replace("size=1532416", "size=+1532416")
                    .into_bytes()
            })),
        },
        Case {
            name: "uppercase sha256",
            mut_fields: Box::new(|mut f| {
                f.sha256 = f.sha256.to_uppercase();
                f
            }),
            wire: None,
        },
        Case {
            name: "short sha256",
            mut_fields: Box::new(|mut f| {
                f.sha256 = "abc".into();
                f
            }),
            wire: None,
        },
        Case {
            name: "bad keyId",
            mut_fields: Box::new(|mut f| {
                f.key_id = "fw-26-1".into();
                f
            }),
            wire: None,
        },
    ];

    let gv = golden();
    let table = allowlist(&gv);
    for tc in cases {
        if let Some(wire_fn) = tc.wire {
            let wire = wire_fn(&gv);
            let err = verify_canonical(&wire, &gv.sig_b64, &valid.key_id, &table).unwrap_err();
            assert!(err.is_validation(), "{}: {:?}", tc.name, err);
        } else {
            let f = (tc.mut_fields)(valid.clone());
            let err = validate_fields(&f).unwrap_err();
            assert!(err.is_validation(), "{}: {:?}", tc.name, err);
        }
    }
}

#[test]
fn go_test_crypto_and_allowlist_negative() {
    let v = golden();
    let table = allowlist(&v);
    let canonical = v.canonical.as_bytes();

    let err = verify_canonical(canonical, &v.sig_b64, "fw-1999-9", &table).unwrap_err();
    assert!(
        err.is_validation(),
        "unknown keyId (Go name): caller keyId mismatch, got {err:?}"
    );

    let empty = TrustedKeyTable::new();
    assert_eq!(
        verify_canonical(canonical, &v.sig_b64, &v.fields.key_id, &empty).unwrap_err(),
        ManifestError::UnknownKey,
        "keyId not in trusted table"
    );

    for sig in ["!!!", &v.sig_b64[..v.sig_b64.len() - 2]] {
        let err = verify_canonical(canonical, sig, &v.fields.key_id, &table).unwrap_err();
        assert!(err.is_validation(), "malformed sig {sig}: {err:?}");
    }

    let mut raw = STANDARD.decode(&v.sig_b64).unwrap();
    raw[0] ^= 0xff;
    let tampered = STANDARD.encode(&raw);
    assert_eq!(tampered.len(), 88);
    assert_eq!(
        verify_canonical(canonical, &tampered, &v.fields.key_id, &table).unwrap_err(),
        ManifestError::BadSignature,
        "tampered signature bytes"
    );

    let short_pk = STANDARD.encode([0u8; 16]);
    assert!(
        decode_public_key_b64(&short_pk)
            .unwrap_err()
            .is_validation(),
        "wrong public key length"
    );

    let mut bad_table = TrustedKeyTable::new();
    // Wrong length key: use invalid bytes via decode bypass — table holds only valid keys;
    // test wrong public key for valid sig instead.
    let other_seed: [u8; 32] = std::array::from_fn(|i| (i + 1) as u8);
    let other = SigningKey::from_bytes(&other_seed);
    bad_table.insert(v.fields.key_id.clone(), other.verifying_key());
    assert_eq!(
        verify_canonical(canonical, &v.sig_b64, &v.fields.key_id, &bad_table).unwrap_err(),
        ManifestError::BadSignature,
        "wrong public key for valid sig"
    );

    assert!(
        decode_public_key_b64("not-base64!!!")
            .unwrap_err()
            .is_validation(),
        "malformed public key b64"
    );
}

#[test]
fn go_test_semver2_build_metadata_leading_zero() {
    let mut f = golden().fields();
    f.version = "1.0.0+01".into();
    validate_fields(&f).expect("build metadata 01 should be valid");
    f.version = "1.0.0-01".into();
    assert!(validate_fields(&f).unwrap_err().is_validation());
}

#[test]
fn go_test_non_zero_pad_bits_strict_versus_permissive() {
    let v = golden();
    let sig_mut = vector_fixtures::mutate_in_alphabet_nonzero_pad_bits(&v.sig_b64).unwrap();
    let pub_mut = vector_fixtures::mutate_in_alphabet_nonzero_pad_bits(&v.public_key_b64).unwrap();
    assert!(!sig_mut.contains('`'));
    assert!(!pub_mut.contains('`'));

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
    let orig_sig = PERMISSIVE.decode(&v.sig_b64).unwrap();
    let mut_sig = PERMISSIVE.decode(&sig_mut).unwrap();
    assert!(base64_strict_decode(&sig_mut).is_err());
    assert_eq!(mut_sig, orig_sig);

    let orig_pub = PERMISSIVE.decode(&v.public_key_b64).unwrap();
    let mut_pub = PERMISSIVE.decode(&pub_mut).unwrap();
    assert!(base64_strict_decode(&pub_mut).is_err());
    assert_eq!(mut_pub, orig_pub);

    let table = allowlist(&v);
    assert!(
        verify_canonical(v.canonical.as_bytes(), &sig_mut, &v.fields.key_id, &table)
            .unwrap_err()
            .is_validation()
    );
    assert!(decode_public_key_b64(&pub_mut).unwrap_err().is_validation());
}

#[test]
fn go_test_semver2_accepts_design_examples() {
    let mut f = golden().fields();
    for ver in ["1.0.2-rc.1", "1.0.0", "10.20.30+build.1", "1.0.0+01"] {
        f.version = ver.to_string();
        validate_fields(&f).unwrap_or_else(|e| panic!("version {ver}: {e:?}"));
    }
}

#[test]
fn go_test_manifest_from_wire_parse_errors() {
    let gv = golden();
    let lines: Vec<&str> = gv.canonical.split('\n').collect();
    let mutate = |idx: usize, line: &str| -> Vec<u8> {
        let mut out: Vec<String> = lines.iter().map(|s| (*s).to_string()).collect();
        out[idx] = line.to_string();
        out.join("\n").into_bytes()
    };
    for (name, idx, line) in [
        ("non-numeric size", 4, "size=abc"),
        ("missing product prefix", 1, "prod=esp32_scanner"),
        ("version plus only build", 3, "version=1.0.0+"),
    ] {
        let err = parse_canonical(&mutate(idx, line)).unwrap_err();
        assert!(err.is_validation(), "{name}: {err:?}");
    }
}

#[test]
fn go_test_decode_public_key_strict_padding() {
    let v = golden();
    let bad = format!("{}`", &v.public_key_b64[..v.public_key_b64.len() - 1]);
    assert!(decode_public_key_b64(&bad).unwrap_err().is_validation());
}

#[test]
fn go_test_allowed_targets() {
    let v = golden();
    for target in [
        "sticks3_u173",
        "sticks3_u173_uart",
        "atoms3lite_u173_bench",
        "atoms3lite_symcode_hid",
    ] {
        let mut f = v.fields();
        f.target = target.to_string();
        validate_fields(&f).unwrap_or_else(|e| panic!("{target}: {e:?}"));
    }
}

#[test]
fn go_test_fields_boundary_raw_bytes_vs_validated() {
    let v = golden();
    let table = allowlist(&v);
    verify_fields(&v.fields(), &v.sig_b64, &v.fields.key_id, &table).unwrap();
    let mut mutated = v.canonical.as_bytes().to_vec();
    let last = mutated.len() - 3;
    mutated[last] ^= 0x01;
    assert!(verify_canonical(&mutated, &v.sig_b64, &v.fields.key_id, &table).is_err());
    let mut bad = v.fields();
    bad.sha256 = bad.sha256.replacen("e6", "e7", 1);
    assert!(verify_fields(&bad, &v.sig_b64, &v.fields.key_id, &table).is_err());
}

#[test]
fn versions_differ_ignores_build_metadata() {
    assert!(!versions_differ("1.0.0+build.a", "1.0.0+build.b"));
    let core = "10000000000000000000.0.0";
    assert!(!versions_differ(
        &format!("{core}+build.a"),
        &format!("{core}+build.b")
    ));
}

#[test]
fn partition_size_equals_max_manifest_size() {
    assert_eq!(PARTITION_SIZE_BYTES as i64, MAX_MANIFEST_SIZE);
    let mut f = golden().fields();
    f.size = MAX_MANIFEST_SIZE;
    validate_fields(&f).unwrap();
    check_size_fits_partition(&f).unwrap();
    f.size = MAX_MANIFEST_SIZE + 1;
    assert!(validate_fields(&f).unwrap_err().is_validation());
    f.size = MAX_MANIFEST_SIZE;
    let mut over = f.clone();
    over.size = MAX_MANIFEST_SIZE + 1;
    assert_eq!(
        check_size_fits_partition(&over).unwrap_err(),
        ManifestError::TooLarge
    );
}

#[test]
fn ota_error_code_strings_exact() {
    assert_eq!(OtaErrorCode::UnknownKey.as_str(), "unknown_key");
    assert_eq!(OtaErrorCode::BadSignature.as_str(), "bad_signature");
    assert_eq!(OtaErrorCode::WrongTarget.as_str(), "wrong_target");
    assert_eq!(OtaErrorCode::TooLarge.as_str(), "too_large");
    assert_eq!(
        ManifestError::UnknownKey.ota_error_code(),
        Some("unknown_key")
    );
    assert_eq!(
        ManifestError::BadSignature.ota_error_code(),
        Some("bad_signature")
    );
    assert_eq!(
        ManifestError::WrongTarget.ota_error_code(),
        Some("wrong_target")
    );
    assert_eq!(ManifestError::TooLarge.ota_error_code(), Some("too_large"));
    assert_eq!(ManifestError::Validation("x").ota_error_code(), None);
}

#[test]
fn device_target_and_version_differ() {
    let f = golden().fields();
    assert!(check_target(&f, "atoms3lite_u173_bench").is_ok());
    assert_eq!(
        check_target(&f, "sticks3_u173").unwrap_err(),
        ManifestError::WrongTarget
    );
    assert!(!versions_differ("1.0.2-rc.1", "1.0.2-rc.1"));
    assert!(versions_differ("1.0.3", "1.0.2-rc.1"));
    assert!(!versions_differ("1.0.0+01", "1.0.0+02"));
    assert!(validate_semver2("1.0.2-rc.1"));
}

#[test]
fn mutation_lock_trailing_newline_rejected_by_line_count() {
    let v = golden();
    let mut wire = v.canonical.clone();
    wire.push('\n');
    let table = allowlist(&v);
    let err = verify_canonical(wire.as_bytes(), &v.sig_b64, &v.fields.key_id, &table).unwrap_err();
    assert!(
        err.is_validation(),
        "trailing LF must fail wire parse: {err:?}"
    );
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
