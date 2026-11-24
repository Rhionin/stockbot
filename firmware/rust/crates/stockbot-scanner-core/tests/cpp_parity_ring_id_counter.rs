mod common;

use common::memory_ring_storage::MemoryRingStorage;
use stockbot_scanner_core::{make_record, Direction, RingBuffer};

#[test]
fn id_counter_and_seed_wrap_at_u32_max_like_cpp() {
    let mut storage = MemoryRingStorage::default();
    storage.records.push(make_record(
        "s-1-4294967294-1",
        "4011",
        Direction::StockOut,
        1,
    ));
    let mut ring = RingBuffer::new(storage);
    assert!(ring.recover_from_storage());
    ring.seed_id_counter_from_records(1);
    assert_eq!(
        ring.enqueue("a", Direction::StockOut, 2, 1).unwrap(),
        "s-1-4294967295-2"
    );
    assert_eq!(
        ring.enqueue("b", Direction::StockOut, 3, 1).unwrap(),
        "s-1-0-3"
    );
}
