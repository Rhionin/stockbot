use std::cell::Cell;
use std::collections::HashMap;
use std::rc::Rc;

use sha2::{Digest, Sha256};
use stockbot_scanner_core::{
    ClockHal, DisplayBands, DisplayHal, Fields, FirmwareHttpHal, FirmwareOpenResult, NvsConfig,
    NvsStore, OtaController, OtaSlot, OtaSlotsHal, OtaTickContext, PowerHal, PowerStatus,
    TrustedKeyTable, UpdateOffer,
};

pub struct FakeClock {
    pub now_ms: Rc<Cell<u64>>,
}

impl Default for FakeClock {
    fn default() -> Self {
        Self {
            now_ms: Rc::new(Cell::new(0)),
        }
    }
}

impl ClockHal for FakeClock {
    fn now_ms(&self) -> u64 {
        self.now_ms.get()
    }
}

#[derive(Default)]
pub struct FakeDisplay {
    pub last: Option<DisplayBands>,
}

impl DisplayHal for FakeDisplay {
    fn begin(&mut self) {}

    fn render(&mut self, bands: &DisplayBands) {
        self.last = Some(bands.clone());
    }

    fn set_backlight(&mut self, _level: stockbot_scanner_core::BacklightLevel) {}

    fn clear_ota_overlay(&mut self) {
        self.last = None;
    }
}

#[derive(Default)]
pub struct FakeOtaNvs {
    strings: HashMap<String, String>,
    u32s: HashMap<String, u32>,
    pub cfg: NvsConfig,
}

impl FakeOtaNvs {
    pub fn set(&mut self, key: &str, value: &str) {
        self.strings.insert(key.to_owned(), value.to_owned());
    }
}

impl NvsStore for FakeOtaNvs {
    fn load(&mut self, out: &mut NvsConfig) -> bool {
        *out = self.cfg.clone();
        true
    }

    fn save(&mut self, cfg: &NvsConfig) -> bool {
        self.cfg = cfg.clone();
        true
    }

    fn clear_device_token(&mut self) {
        self.cfg.device_token.clear();
    }

    fn factory_reset(&mut self) {
        self.cfg = NvsConfig::default();
    }

    fn stockbot_get_str(&self, key: &str) -> Option<String> {
        self.strings.get(key).cloned()
    }

    fn stockbot_set_str(&mut self, key: &str, value: &str) {
        self.strings.insert(key.to_owned(), value.to_owned());
    }

    fn stockbot_remove(&mut self, key: &str) {
        self.strings.remove(key);
        self.u32s.remove(key);
    }

    fn stockbot_get_u32(&self, key: &str) -> Option<u32> {
        self.u32s.get(key).copied()
    }

    fn stockbot_set_u32(&mut self, key: &str, value: u32) {
        self.u32s.insert(key.to_owned(), value);
    }
}

pub struct FakePower {
    pub vbus: Rc<Cell<bool>>,
    pub battery_pct: Rc<Cell<Option<i32>>>,
    pub charging: Rc<Cell<bool>>,
}

impl Default for FakePower {
    fn default() -> Self {
        Self {
            vbus: Rc::new(Cell::new(true)),
            battery_pct: Rc::new(Cell::new(Some(90))),
            charging: Rc::new(Cell::new(false)),
        }
    }
}

impl PowerHal for FakePower {
    fn begin(&mut self) {}

    fn read(&self) -> PowerStatus {
        PowerStatus {
            vbus_present: self.vbus.get(),
            battery_pct: self.battery_pct.get(),
            charging: self.charging.get(),
        }
    }

    fn set_grove_power(&mut self, _enabled: bool) {}

    fn enter_deep_sleep_wake_on_button(&mut self) {}
}

pub struct HttpControls {
    pub fail_transport: Rc<Cell<bool>>,
    pub fail_transport_always: Rc<Cell<bool>>,
    pub fail_transport_remaining: Rc<Cell<u32>>,
    pub fail_status: Rc<Cell<Option<i32>>>,
    pub fail_count_remaining: Rc<Cell<u32>>,
    pub open_calls: Rc<Cell<u32>>,
    pub chunk_limit: Rc<Cell<Option<usize>>>,
    pub resume_must_be_206: Rc<Cell<bool>>,
}

pub struct FakeHttp {
    pub firmware: Vec<u8>,
    pub controls: Rc<HttpControls>,
    cursor: usize,
    open: bool,
}

impl FakeHttp {
    fn start_body(&mut self, range_start: u64) {
        self.cursor = range_start as usize;
        self.open = true;
    }
}

impl FirmwareHttpHal for FakeHttp {
    fn open_firmware(&mut self, _url: &str, range_start: u64) -> FirmwareOpenResult {
        let c = &self.controls;
        c.open_calls.set(c.open_calls.get() + 1);
        self.open = false;
        self.cursor = 0;
        if c.fail_transport.get()
            && (c.fail_transport_always.get() || c.fail_transport_remaining.get() > 0)
        {
            if !c.fail_transport_always.get() {
                c.fail_transport_remaining
                    .set(c.fail_transport_remaining.get().saturating_sub(1));
            }
            return FirmwareOpenResult {
                transport_ok: false,
                http_status: 0,
            };
        }
        if c.fail_count_remaining.get() > 0 {
            c.fail_count_remaining
                .set(c.fail_count_remaining.get().saturating_sub(1));
            return FirmwareOpenResult {
                transport_ok: true,
                http_status: c.fail_status.get().unwrap_or(500),
            };
        }
        if let Some(status) = c.fail_status.get() {
            self.start_body(range_start);
            return FirmwareOpenResult {
                transport_ok: true,
                http_status: status,
            };
        }
        let status = if range_start > 0 { 206 } else { 200 };
        if range_start > 0 && c.resume_must_be_206.get() && status != 206 {
            return FirmwareOpenResult {
                transport_ok: true,
                http_status: 200,
            };
        }
        self.start_body(range_start);
        FirmwareOpenResult {
            transport_ok: true,
            http_status: status,
        }
    }

    fn read_firmware_chunk(&mut self, buf: &mut [u8]) -> Result<usize, &'static str> {
        if !self.open {
            return Err("no open body");
        }
        let start = self.cursor;
        if start >= self.firmware.len() {
            return Ok(0);
        }
        let limit = self
            .controls
            .chunk_limit
            .get()
            .unwrap_or(buf.len())
            .min(buf.len());
        let end = (start + limit).min(self.firmware.len());
        let n = end - start;
        buf[..n].copy_from_slice(&self.firmware[start..end]);
        self.cursor = end;
        Ok(n)
    }

    fn close_firmware(&mut self) {
        self.open = false;
    }
}

pub struct SlotsControls {
    pub running: Rc<Cell<OtaSlot>>,
    pub written: Rc<Cell<u64>>,
    pub force_written: Rc<Cell<Option<u64>>>,
    pub write_active: Rc<Cell<bool>>,
    pub boot_switched: Rc<Cell<bool>>,
    pub marked_valid: Rc<Cell<bool>>,
    pub marked_invalid: Rc<Cell<bool>>,
    pub rebooted: Rc<Cell<bool>>,
    pub last_invalid: Rc<Cell<Option<OtaSlot>>>,
    pub end_write_ok: Rc<Cell<bool>>,
    pub set_boot_ok: Rc<Cell<bool>>,
    pub reject_write: Rc<Cell<bool>>,
}

pub struct FakeOtaSlots {
    pub controls: Rc<SlotsControls>,
}

impl FakeOtaSlots {
    fn inactive(&self) -> OtaSlot {
        match self.controls.running.get() {
            OtaSlot::Slot0 => OtaSlot::Slot1,
            OtaSlot::Slot1 => OtaSlot::Slot0,
        }
    }
}

impl OtaSlotsHal for FakeOtaSlots {
    fn running_slot(&self) -> OtaSlot {
        self.controls.running.get()
    }

    fn inactive_slot(&self) -> OtaSlot {
        self.inactive()
    }

    fn begin_inactive_write(&mut self) -> Result<(), &'static str> {
        let c = &self.controls;
        c.write_active.set(true);
        c.written.set(0);
        Ok(())
    }

    fn write_inactive(&mut self, chunk: &[u8]) -> Result<(), &'static str> {
        let c = &self.controls;
        if c.reject_write.get() {
            return Err("write rejected");
        }
        if !c.write_active.get() {
            return Err("no session");
        }
        c.written.set(c.written.get() + chunk.len() as u64);
        Ok(())
    }

    fn end_inactive_write(&mut self) -> Result<(), &'static str> {
        let c = &self.controls;
        if !c.end_write_ok.get() && c.write_active.get() {
            return Err("image invalid");
        }
        Ok(())
    }

    fn set_boot_inactive(&mut self) -> Result<(), &'static str> {
        let c = &self.controls;
        if !c.set_boot_ok.get() {
            return Err("set boot failed");
        }
        c.boot_switched.set(true);
        c.running.set(self.inactive());
        Ok(())
    }

    fn mark_running_valid(&mut self) -> Result<(), &'static str> {
        self.controls.marked_valid.set(true);
        Ok(())
    }

    fn mark_running_invalid_and_reboot(&mut self) -> Result<(), &'static str> {
        let c = &self.controls;
        c.marked_invalid.set(true);
        c.rebooted.set(true);
        Ok(())
    }

    fn last_invalid_partition(&self) -> Option<OtaSlot> {
        self.controls.last_invalid.get()
    }

    fn reboot(&mut self) {
        let c = &self.controls;
        c.rebooted.set(true);
        c.write_active.set(false);
    }

    fn bytes_written_inactive(&self) -> u64 {
        let c = &self.controls;
        c.force_written.get().unwrap_or_else(|| c.written.get())
    }

    fn abort_inactive_write(&mut self) {
        let c = &self.controls;
        c.write_active.set(false);
        c.written.set(0);
    }
}

pub fn sha256_hex(data: &[u8]) -> String {
    let digest = Sha256::digest(data);
    let mut s = String::with_capacity(64);
    for b in digest {
        use std::fmt::Write;
        let _ = write!(s, "{b:02x}");
    }
    s
}

pub fn test_firmware(size: usize, byte: u8) -> (Vec<u8>, String) {
    let fw = vec![byte; size];
    let hex = sha256_hex(&fw);
    (fw, hex)
}

pub fn open_window_ctx(base: &OtaTickContext) -> OtaTickContext {
    OtaTickContext {
        paired: true,
        ring_empty: true,
        last_activity_ms: 0,
        wifi_rssi_dbm: Some(-60),
        ..base.clone()
    }
}

pub struct OtaHarnessShared {
    pub now_ms: Rc<Cell<u64>>,
    pub http_ctl: Rc<HttpControls>,
    pub slots_ctl: Rc<SlotsControls>,
}

pub struct OtaHarness {
    pub now_ms: Rc<Cell<u64>>,
    pub http_ctl: Rc<HttpControls>,
    pub slots_ctl: Rc<SlotsControls>,
    clock: FakeClock,
    display: FakeDisplay,
    pub power: FakePower,
    pub nvs: FakeOtaNvs,
    pub http: FakeHttp,
    slots: FakeOtaSlots,
    keys: TrustedKeyTable,
    pub ctx: OtaTickContext,
}

impl OtaHarness {
    pub fn new(device_target: &str, firmware: Vec<u8>, table: TrustedKeyTable) -> Self {
        let _ = device_target;
        let now_ms = Rc::new(Cell::new(0_u64));
        let http_ctl = Rc::new(HttpControls {
            fail_transport: Rc::new(Cell::new(false)),
            fail_transport_always: Rc::new(Cell::new(false)),
            fail_transport_remaining: Rc::new(Cell::new(0)),
            fail_status: Rc::new(Cell::new(None)),
            fail_count_remaining: Rc::new(Cell::new(0)),
            open_calls: Rc::new(Cell::new(0)),
            chunk_limit: Rc::new(Cell::new(None)),
            resume_must_be_206: Rc::new(Cell::new(true)),
        });
        let slots_ctl = Rc::new(SlotsControls {
            running: Rc::new(Cell::new(OtaSlot::default())),
            written: Rc::new(Cell::new(0)),
            force_written: Rc::new(Cell::new(None)),
            write_active: Rc::new(Cell::new(false)),
            boot_switched: Rc::new(Cell::new(false)),
            marked_valid: Rc::new(Cell::new(false)),
            marked_invalid: Rc::new(Cell::new(false)),
            rebooted: Rc::new(Cell::new(false)),
            last_invalid: Rc::new(Cell::new(None)),
            end_write_ok: Rc::new(Cell::new(true)),
            set_boot_ok: Rc::new(Cell::new(true)),
            reject_write: Rc::new(Cell::new(false)),
        });
        Self {
            now_ms: now_ms.clone(),
            http_ctl: http_ctl.clone(),
            slots_ctl: slots_ctl.clone(),
            clock: FakeClock { now_ms },
            display: FakeDisplay::default(),
            power: FakePower::default(),
            nvs: FakeOtaNvs::default(),
            http: FakeHttp {
                firmware,
                controls: http_ctl,
                cursor: 0,
                open: false,
            },
            slots: FakeOtaSlots {
                controls: slots_ctl,
            },
            keys: table,
            ctx: OtaTickContext {
                running_version: "1.0.0".to_owned(),
                ..OtaTickContext::default()
            },
        }
    }

    pub fn controls(&self) -> OtaHarnessShared {
        OtaHarnessShared {
            now_ms: self.now_ms.clone(),
            http_ctl: self.http_ctl.clone(),
            slots_ctl: self.slots_ctl.clone(),
        }
    }

    pub fn controller<'a>(&'a mut self, target: &str, battery: bool) -> OtaController<'a> {
        OtaController::new(
            &self.clock,
            &mut self.display,
            &self.power,
            &mut self.nvs,
            &mut self.http,
            &mut self.slots,
            &self.keys,
            target,
            battery,
        )
    }

    pub fn offer_from_fields(
        release_id: &str,
        fields: Fields,
        sig_b64: &str,
        url: &str,
    ) -> UpdateOffer {
        UpdateOffer {
            release_id: release_id.to_owned(),
            fields,
            sig_b64: sig_b64.to_owned(),
            url: url.to_owned(),
        }
    }
}
