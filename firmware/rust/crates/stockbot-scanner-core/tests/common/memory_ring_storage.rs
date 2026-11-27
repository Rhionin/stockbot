pub use stockbot_scanner_core::MemoryRingStorage;
use stockbot_scanner_core::{RingIoError, RingScanRecord, RingStorage};

/// Ring storage that fails `write_all` when clearing the ring to an empty vector.
#[derive(Default)]
pub struct FailClearRingStorage {
    pub inner: MemoryRingStorage,
    pub fail_empty_write: bool,
}

impl RingStorage for FailClearRingStorage {
    fn read_all(&mut self) -> Result<Vec<RingScanRecord>, RingIoError> {
        self.inner.read_all()
    }

    fn write_all(&mut self, records: &[RingScanRecord]) -> Result<(), RingIoError> {
        if self.fail_empty_write && records.is_empty() {
            return Err(RingIoError::IoFailed);
        }
        self.inner.write_all(records)
    }
}
