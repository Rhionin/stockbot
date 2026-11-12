use std::fs;
use std::path::PathBuf;

use serde::Deserialize;
use stockbot_scanner_core::{sbr1, RingBuffer, RingScanRecord, RingStorage, RING_CAPACITY};

const EXPECTED_CASE_COUNT: usize = 10;

#[derive(Debug, Deserialize)]
struct RingVectorCase {
    name: String,
    #[serde(rename = "bytesHex")]
    bytes_hex: String,
    #[serde(rename = "decodeError")]
    decode_error: Option<String>,
    #[serde(rename = "recoverCount")]
    recover_count: Option<u32>,
}

fn repo_root() -> PathBuf {
    PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("../../../..")
}

fn hex_decode(hex: &str) -> Vec<u8> {
    let mut out = Vec::with_capacity(hex.len() / 2);
    let bytes = hex.as_bytes();
    let mut i = 0;
    while i + 1 < bytes.len() {
        let h = |c: u8| match c {
            b'0'..=b'9' => c - b'0',
            b'a'..=b'f' => c - b'a' + 10,
            b'A'..=b'F' => c - b'A' + 10,
            _ => panic!("bad hex"),
        };
        out.push(h(bytes[i]) * 16 + h(bytes[i + 1]));
        i += 2;
    }
    out
}

fn load_vectors() -> Vec<RingVectorCase> {
    let path = repo_root().join("firmware/test/vectors/scan_ring_sbr1.json");
    let raw = fs::read_to_string(&path).unwrap_or_else(|e| panic!("read {}: {e}", path.display()));
    let vectors: Vec<RingVectorCase> =
        serde_json::from_str(&raw).expect("parse scan_ring_sbr1.json");
    assert_eq!(
        vectors.len(),
        EXPECTED_CASE_COUNT,
        "scan_ring_sbr1.json must have exactly {EXPECTED_CASE_COUNT} cases"
    );
    vectors
}

struct BlobRingStorage {
    records: Vec<RingScanRecord>,
}

impl RingStorage for BlobRingStorage {
    fn read_all(&mut self) -> Result<Vec<RingScanRecord>, stockbot_scanner_core::RingIoError> {
        Ok(self.records.clone())
    }

    fn write_all(
        &mut self,
        _records: &[RingScanRecord],
    ) -> Result<(), stockbot_scanner_core::RingIoError> {
        Ok(())
    }
}

#[test]
fn scan_ring_sbr1_all_vectors() {
    for case in load_vectors() {
        let bytes = hex_decode(&case.bytes_hex);
        let decode_result = sbr1::decode(&bytes);
        match &case.decode_error {
            None => {
                let records =
                    decode_result.unwrap_or_else(|e| panic!("{} decode: {e:?}", case.name));
                let re = sbr1::encode(&records);
                assert_eq!(re, bytes, "re-encode mismatch for {}", case.name);
            }
            Some(err) => {
                assert!(decode_result.is_err(), "{} should fail decode", case.name);
                let got = decode_result.unwrap_err();
                assert!(
                    matches!(
                        (err.as_str(), got),
                        ("bad_magic", sbr1::DecodeError::BadMagic)
                            | ("count_over_capacity", sbr1::DecodeError::CountOverCapacity)
                            | ("truncated", sbr1::DecodeError::Truncated)
                    ),
                    "{}: expected decodeError {err}, got {got:?}",
                    case.name
                );
            }
        }

        if let Some(expected) = case.recover_count {
            let file_records = sbr1::decode(&bytes).unwrap_or_default();
            let store = BlobRingStorage {
                records: file_records,
            };
            let mut ring = RingBuffer::new(store);
            assert!(ring.recover_from_storage());
            assert_eq!(
                ring.count(),
                expected as usize,
                "recoverCount for {}",
                case.name
            );
        }
    }
}

#[test]
fn sbr1_decode_rejects_count_over_capacity() {
    let mut bytes = sbr1::encode(&[]);
    let bad = (RING_CAPACITY as u32).saturating_add(1);
    bytes[4..8].copy_from_slice(&bad.to_le_bytes());
    assert!(matches!(
        sbr1::decode(&bytes),
        Err(sbr1::DecodeError::CountOverCapacity)
    ));
}
