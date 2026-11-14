use stockbot_scanner_core::{RingIoError, RingScanRecord, RingStorage};

#[derive(Default)]
pub struct MemoryRingStorage {
    pub records: Vec<RingScanRecord>,
}

impl RingStorage for MemoryRingStorage {
    fn read_all(&mut self) -> Result<Vec<RingScanRecord>, RingIoError> {
        Ok(self.records.clone())
    }

    fn write_all(&mut self, records: &[RingScanRecord]) -> Result<(), RingIoError> {
        self.records = records.to_vec();
        Ok(())
    }
}

/// C++ `FailClearRingStorage` — fails `write_all` when clearing the ring to an empty vector.
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
