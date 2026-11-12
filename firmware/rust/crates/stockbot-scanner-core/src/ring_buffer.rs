//! Offline scan ring (500 entries, CRC32, persistence via [`RingStorage`]).
use crate::barcode::Direction;
use std::fmt::Write;

pub const RING_CAPACITY: usize = 500;

#[repr(C)]
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct RingScanRecord {
    pub client_scan_id: [u8; 36],
    pub barcode: [u8; 16],
    pub direction: u8,
    pub reserved: [u8; 3],
    pub scanned_ms: u32,
    pub crc32: u32,
}

const _: () = assert!(core::mem::size_of::<RingScanRecord>() == 64);

pub fn ring_scan_age_ms(record: &RingScanRecord, now_ms: u64) -> u32 {
    if record.scanned_ms == 0 || now_ms < u64::from(record.scanned_ms) {
        return 0;
    }
    (now_ms - u64::from(record.scanned_ms)) as u32
}

pub fn crc32_ring_record(rec: &RingScanRecord) -> u32 {
    let mut copy = *rec;
    copy.crc32 = 0;
    let bytes = record_as_bytes(&copy);
    crc32_update(0, &bytes)
}

fn record_as_bytes(rec: &RingScanRecord) -> [u8; 64] {
    let mut out = [0u8; 64];
    out[0..36].copy_from_slice(&rec.client_scan_id);
    out[36..52].copy_from_slice(&rec.barcode);
    out[52] = rec.direction;
    out[53..56].copy_from_slice(&rec.reserved);
    out[56..60].copy_from_slice(&rec.scanned_ms.to_le_bytes());
    out[60..64].copy_from_slice(&rec.crc32.to_le_bytes());
    out
}

/// IEEE-style CRC32 (same bit loop as `firmware/src/core/ring_buffer.cpp`).
pub fn crc32_update(crc: u32, data: &[u8]) -> u32 {
    let mut crc = !crc;
    for &byte in data {
        crc ^= u32::from(byte);
        for _ in 0..8 {
            let mask = 0u32.wrapping_sub(crc & 1);
            crc = (crc >> 1) ^ (0xEDB8_8320 & mask);
        }
    }
    !crc
}

pub trait RingStorage {
    fn read_all(&mut self) -> Result<Vec<RingScanRecord>, RingIoError>;
    fn write_all(&mut self, records: &[RingScanRecord]) -> Result<(), RingIoError>;
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum RingIoError {
    IoFailed,
}

pub struct RingBuffer<S: RingStorage> {
    storage: S,
    records: Vec<RingScanRecord>,
    id_counter: u32,
}

impl<S: RingStorage> RingBuffer<S> {
    pub fn new(storage: S) -> Self {
        Self {
            storage,
            records: Vec::new(),
            id_counter: 0,
        }
    }

    pub fn recover_from_storage(&mut self) -> bool {
        self.records.clear();
        let loaded = match self.storage.read_all() {
            Ok(v) => v,
            Err(_) => return false,
        };
        for mut rec in loaded {
            if crc32_ring_record(&rec) != rec.crc32 {
                continue;
            }
            rec.scanned_ms = 0;
            self.records.push(rec);
        }
        true
    }

    pub fn seed_id_counter_from_records(&mut self, boot_count: u32) {
        let prefix = format!("s-{boot_count}-");
        let mut max_seq = 0u32;
        for rec in &self.records {
            let id = c_str_from_fixed(&rec.client_scan_id);
            if !id.starts_with(&prefix) {
                continue;
            }
            let seq_start = prefix.len();
            let Some(rest) = id.get(seq_start..) else {
                continue;
            };
            let Some((seq_text, _)) = rest.split_once('-') else {
                continue;
            };
            if let Ok(seq) = seq_text.parse::<u32>() {
                max_seq = max_seq.max(seq);
            }
        }
        self.id_counter = max_seq.saturating_add(1);
    }

    pub fn enqueue(
        &mut self,
        barcode: &str,
        direction: Direction,
        now_ms: u64,
        boot_count: u32,
    ) -> Result<String, RingEnqueueError> {
        if self.is_full() {
            return Err(RingEnqueueError::Full);
        }
        let seq = self.id_counter;
        self.id_counter = self.id_counter.saturating_add(1);
        let scanned_ms = now_ms as u32;
        let mut client_scan_id = String::new();
        write!(client_scan_id, "s-{boot_count}-{seq}-{scanned_ms}").unwrap();
        let rec = make_record(&client_scan_id, barcode, direction, scanned_ms);
        self.records.push(rec);
        if self.persist().is_err() {
            return Err(RingEnqueueError::PersistFailed);
        }
        Ok(client_scan_id)
    }

    pub fn peek_batch(&self, max_count: usize) -> Vec<RingScanRecord> {
        let n = max_count.min(self.records.len());
        self.records[..n].to_vec()
    }

    pub fn acknowledge(&mut self, client_scan_id: &str) -> bool {
        if let Some(pos) = self
            .records
            .iter()
            .position(|r| c_str_from_fixed(&r.client_scan_id) == client_scan_id)
        {
            self.records.remove(pos);
            return self.persist().is_ok();
        }
        false
    }

    pub fn clear_all(&mut self) -> bool {
        let backup = self.records.clone();
        self.records.clear();
        if self.persist().is_err() {
            self.records = backup;
            return false;
        }
        true
    }

    pub fn is_full(&self) -> bool {
        self.records.len() >= RING_CAPACITY
    }

    pub fn count(&self) -> usize {
        self.records.len()
    }

    pub fn records(&self) -> &[RingScanRecord] {
        &self.records
    }

    pub fn into_storage(self) -> S {
        self.storage
    }

    fn persist(&mut self) -> Result<(), RingIoError> {
        self.storage.write_all(&self.records)
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum RingEnqueueError {
    Full,
    PersistFailed,
}

pub fn make_record(
    id: &str,
    barcode: &str,
    direction: Direction,
    scanned_ms: u32,
) -> RingScanRecord {
    let mut rec = RingScanRecord {
        client_scan_id: [0; 36],
        barcode: [0; 16],
        direction: if direction == Direction::StockIn {
            1
        } else {
            0
        },
        reserved: [0; 3],
        scanned_ms,
        crc32: 0,
    };
    copy_cstr_into(&mut rec.client_scan_id, id);
    copy_cstr_into(&mut rec.barcode, barcode);
    rec.crc32 = crc32_ring_record(&rec);
    rec
}

pub fn c_str_from_fixed(buf: &[u8]) -> String {
    let len = buf.iter().position(|&b| b == 0).unwrap_or(buf.len());
    String::from_utf8_lossy(&buf[..len]).into_owned()
}

fn copy_cstr_into(dest: &mut [u8], src: &str) {
    let bytes = src.as_bytes();
    let n = bytes.len().min(dest.len().saturating_sub(1));
    dest[..n].copy_from_slice(&bytes[..n]);
    dest[n] = 0;
}

/// SBR1 on-disk blob (LittleFS `scan_ring.bin` layout).
pub mod sbr1 {
    use super::*;

    pub const MAGIC: u32 = 0x5342_5231; // 'SBR1'
    const HEADER_LEN: usize = 8;

    #[derive(Debug, Clone, Copy, PartialEq, Eq)]
    pub enum DecodeError {
        Truncated,
        BadMagic,
        CountOverCapacity,
    }

    pub fn encode(records: &[RingScanRecord]) -> Vec<u8> {
        let mut out = Vec::with_capacity(HEADER_LEN + records.len() * 64);
        out.extend_from_slice(&MAGIC.to_le_bytes());
        out.extend_from_slice(&(records.len() as u32).to_le_bytes());
        for rec in records {
            out.extend_from_slice(&record_as_bytes(rec));
        }
        out
    }

    pub fn decode(bytes: &[u8]) -> Result<Vec<RingScanRecord>, DecodeError> {
        if bytes.len() < HEADER_LEN {
            return Err(DecodeError::Truncated);
        }
        let magic = u32::from_le_bytes(bytes[0..4].try_into().unwrap());
        if magic != MAGIC {
            return Err(DecodeError::BadMagic);
        }
        let count = u32::from_le_bytes(bytes[4..8].try_into().unwrap()) as usize;
        if count > RING_CAPACITY {
            return Err(DecodeError::CountOverCapacity);
        }
        let expected = HEADER_LEN + count * 64;
        if bytes.len() < expected {
            return Err(DecodeError::Truncated);
        }
        let mut records = Vec::with_capacity(count);
        for i in 0..count {
            let start = HEADER_LEN + i * 64;
            records.push(parse_record(&bytes[start..start + 64]));
        }
        Ok(records)
    }

    fn parse_record(chunk: &[u8]) -> RingScanRecord {
        let mut rec = RingScanRecord {
            client_scan_id: [0; 36],
            barcode: [0; 16],
            direction: chunk[52],
            reserved: [chunk[53], chunk[54], chunk[55]],
            scanned_ms: u32::from_le_bytes(chunk[56..60].try_into().unwrap()),
            crc32: u32::from_le_bytes(chunk[60..64].try_into().unwrap()),
        };
        rec.client_scan_id.copy_from_slice(&chunk[0..36]);
        rec.barcode.copy_from_slice(&chunk[36..52]);
        rec
    }
}
