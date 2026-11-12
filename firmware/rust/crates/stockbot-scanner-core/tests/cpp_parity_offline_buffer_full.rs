mod common;

use common::memory_ring_storage::MemoryRingStorage;
use stockbot_scanner_core::{
    c_str_from_fixed, Direction, RingBuffer, RingEnqueueError, RING_CAPACITY,
};

fn barcode_at(index: usize) -> String {
    format!("{:0>12}", index + 1)
}

/// Ring-level assertions from `test_offline_buffer_full_refuses_without_eviction`
/// (controller/UI checks stay in S5).
#[test]
fn cpp_test_offline_buffer_full_refuses_without_eviction() {
    let storage = MemoryRingStorage::default();
    let mut ring = RingBuffer::new(storage);
    for i in 0..RING_CAPACITY {
        let _ = ring.enqueue(&barcode_at(i), Direction::StockOut, 1000 + i as u64, 1);
    }
    assert_eq!(ring.count(), RING_CAPACITY);
    assert!(ring.is_full());
    let before = ring.peek_batch(RING_CAPACITY);
    assert_eq!(before.len(), RING_CAPACITY);
    assert_eq!(
        ring.enqueue("999999999999", Direction::StockOut, 9000, 1),
        Err(RingEnqueueError::Full)
    );
    assert_eq!(ring.count(), RING_CAPACITY);
    assert!(ring.is_full());
    let after = ring.peek_batch(RING_CAPACITY);
    assert_eq!(after.len(), before.len());
    for i in 0..before.len() {
        assert_eq!(
            c_str_from_fixed(&after[i].barcode),
            c_str_from_fixed(&before[i].barcode)
        );
        assert_eq!(
            c_str_from_fixed(&after[i].client_scan_id),
            c_str_from_fixed(&before[i].client_scan_id)
        );
    }
}

/// Ring-level port of `test_offline_buffer_full_clears_after_flush_ack` (batch ack path).
#[test]
fn cpp_test_offline_buffer_full_clears_after_flush_ack() {
    let storage = MemoryRingStorage::default();
    let mut ring = RingBuffer::new(storage);
    for i in 0..RING_CAPACITY {
        let _ = ring.enqueue("4011", Direction::StockOut, 1000 + i as u64, 1);
    }
    assert!(ring.is_full());
    let pending = ring.peek_batch(50);
    assert_eq!(pending.len(), 50);
    for rec in &pending {
        assert!(ring.acknowledge(&c_str_from_fixed(&rec.client_scan_id)));
    }
    assert!(!ring.is_full());
    assert_eq!(ring.count(), RING_CAPACITY - 50);
    let _ = ring.enqueue("4011", Direction::StockOut, 21_000, 1);
    assert_eq!(ring.count(), RING_CAPACITY - 49);
}

/// Ring-level port of `test_offline_buffer_full_persists_across_reboot_until_flush`.
#[test]
fn cpp_test_offline_buffer_full_persists_across_reboot_until_flush() {
    let storage = MemoryRingStorage::default();
    let storage = {
        let mut ring_a = RingBuffer::new(storage);
        for i in 0..RING_CAPACITY {
            let _ = ring_a.enqueue(&barcode_at(i), Direction::StockOut, 1000 + i as u64, 1);
        }
        assert!(ring_a.is_full());
        ring_a.into_storage()
    };
    let mut ring_b = RingBuffer::new(storage);
    assert!(ring_b.recover_from_storage());
    assert!(ring_b.is_full());

    let pending = ring_b.peek_batch(20);
    for rec in &pending {
        assert!(ring_b.acknowledge(&c_str_from_fixed(&rec.client_scan_id)));
    }
    assert!(!ring_b.is_full());
}
