//! Locks `verify_canonical` on `Verifier::verify_strict` (CCTV / Taming the many EdDSAs edge cases).

use std::ops::Neg;

use base64::engine::general_purpose::STANDARD;
use base64::Engine;
use curve25519_dalek::constants::ED25519_BASEPOINT_POINT;
use curve25519_dalek::edwards::CompressedEdwardsY;
use curve25519_dalek::scalar::Scalar;
use curve25519_dalek::traits::IsIdentity;
use ed25519_dalek::{Signature, Verifier, VerifyingKey};
use sha2::{Digest, Sha512};
use stockbot_scanner_core::{verify_canonical, ManifestError, TrustedKeyTable};

mod vector_fixtures;

/// Low-order point from `ed25519-dalek` repudiation tests (`EIGHT_TORSION[4]`).
const WEAK_PUBKEY: [u8; 32] = [
    236, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255,
    255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 127,
];

fn challenge(
    message: &[u8],
    public: &curve25519_dalek::edwards::EdwardsPoint,
    r: &curve25519_dalek::edwards::EdwardsPoint,
) -> Scalar {
    let mut h = Sha512::default();
    h.update(r.compress().as_bytes());
    h.update(public.compress().as_bytes());
    h.update(message);
    Scalar::from_hash(h)
}

/// Deterministic weak-key forgery (high success rate; see ed25519-dalek `repudiation` test).
fn weak_forgery_for_message(message: &[u8]) -> (VerifyingKey, Signature) {
    let public = CompressedEdwardsY(WEAK_PUBKEY)
        .decompress()
        .expect("weak pubkey decompress");
    let verifying_key = VerifyingKey::from_bytes(&WEAK_PUBKEY).expect("weak verifying key");

    for attempt in 0u32..50_000 {
        let mut entropy = [0u8; 64];
        entropy[..4].copy_from_slice(&attempt.to_le_bytes());
        entropy[4..8].copy_from_slice(b"S2#4");
        let digest = Sha512::digest(entropy);
        let s = Scalar::from_bytes_mod_order(digest[..32].try_into().unwrap());
        if s == Scalar::ZERO {
            continue;
        }
        let r0 = s * ED25519_BASEPOINT_POINT;
        let r = r0 + public.neg();
        let h = challenge(message, &public, &r);
        if !(public.neg() + h * public).is_identity() {
            continue;
        }
        let mut sig_bytes = [0u8; 64];
        sig_bytes[..32].copy_from_slice(r.compress().as_bytes());
        sig_bytes[32..].copy_from_slice(s.as_bytes());
        let sig = Signature::from_bytes(&sig_bytes);
        let verify_ok = verifying_key.verify(message, &sig).is_ok();
        let strict_err = verifying_key.verify_strict(message, &sig).is_err();
        if verify_ok && strict_err {
            return (verifying_key, sig);
        }
    }
    panic!("weak forgery search failed (infra)");
}

#[test]
fn verify_strict_lock_weak_pubkey_low_order_a() {
    let v = vector_fixtures::golden_valid();
    let message = v.canonical.as_bytes();
    let (weak_key, weak_sig) = weak_forgery_for_message(message);
    assert!(weak_key.is_weak());
    assert!(weak_key.verify(message, &weak_sig).is_ok());
    assert!(weak_key.verify_strict(message, &weak_sig).is_err());

    let sig_b64 = STANDARD.encode(weak_sig.to_bytes());
    assert_eq!(sig_b64.len(), 88);
    let key_id = v.fields.key_id.clone();
    let mut table = TrustedKeyTable::new();
    table.insert(key_id.clone(), weak_key);
    let err = verify_canonical(message, &sig_b64, &key_id, &table).unwrap_err();
    assert_eq!(
        err,
        ManifestError::BadSignature,
        "verify_canonical must use verify_strict (rejects low-order A)"
    );
}
