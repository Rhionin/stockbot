mod common;

use common::memory_ring_storage::MemoryRingStorage;
use stockbot_scanner_core::{c_str_from_fixed, Direction, RingBuffer, RING_CAPACITY};

fn try_enqueue(
    ring: &mut RingBuffer<MemoryRingStorage>,
    barcode: &str,
    direction: Direction,
    now_ms: u64,
    boot: u32,
) -> bool {
    ring.enqueue(barcode, direction, now_ms, boot).is_ok()
}

/// Ports `test_ring_buffer` from `firmware/test/test_scanner_core/test_main.cpp`.
#[test]
fn cpp_test_ring_buffer() {
    let storage = MemoryRingStorage::default();
    let mut ring = RingBuffer::new(storage);
    assert!(ring.recover_from_storage());

    for i in 0..RING_CAPACITY {
        assert!(try_enqueue(
            &mut ring,
            "4011",
            Direction::StockOut,
            i as u64,
            1
        ));
    }
    assert!(ring.is_full());
    assert!(!try_enqueue(
        &mut ring,
        "9999",
        Direction::StockOut,
        9999,
        1
    ));
    let batch = ring.peek_batch(20);
    assert_eq!(batch.len(), 20);
    assert_eq!(c_str_from_fixed(&batch[0].barcode), "4011");
    assert!(ring.acknowledge(&c_str_from_fixed(&batch[0].client_scan_id)));
    assert_eq!(ring.count(), RING_CAPACITY - 1);

    let storage2 = MemoryRingStorage::default();
    let storage2 = {
        let mut r1 = RingBuffer::new(storage2);
        let _ = r1.enqueue("123", Direction::StockIn, 1, 1);
        r1.into_storage()
    };
    let mut r2 = RingBuffer::new(storage2);
    assert!(r2.recover_from_storage());
    assert_eq!(r2.count(), 1);

    let storage3 = MemoryRingStorage::default();
    let storage3 = {
        let mut writer = RingBuffer::new(storage3);
        let _ = writer.enqueue("4011", Direction::StockOut, 500, 7);
        writer.into_storage()
    };
    let mut persisted = storage3.records.clone();
    assert!(!persisted.is_empty());
    persisted[0].crc32 ^= 0x00ff_ffff;
    let storage3 = MemoryRingStorage {
        records: persisted,
        ..Default::default()
    };
    let mut corrupt = RingBuffer::new(storage3);
    assert!(corrupt.recover_from_storage());
    assert_eq!(corrupt.count(), 0);
}
