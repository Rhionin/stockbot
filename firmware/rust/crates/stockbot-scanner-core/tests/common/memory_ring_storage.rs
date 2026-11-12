use stockbot_scanner_core::{RingIoError, RingScanRecord, RingStorage};

#[derive(Default)]
pub struct MemoryRingStorage {
    pub records: Vec<RingScanRecord>,
    pub fail_empty_write: bool,
}

impl RingStorage for MemoryRingStorage {
    fn read_all(&mut self) -> Result<Vec<RingScanRecord>, RingIoError> {
        Ok(self.records.clone())
    }

    fn write_all(&mut self, records: &[RingScanRecord]) -> Result<(), RingIoError> {
        if self.fail_empty_write && records.is_empty() {
            return Err(RingIoError::IoFailed);
        }
        self.records = records.to_vec();
        Ok(())
    }
}
