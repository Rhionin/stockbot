//! Docked on USB power and paired, the reader scans hands-free; off the dock it returns to the
//! trigger (#457 item 2).

mod common;

use std::cell::RefCell;
use std::rc::Rc;

use common::controller_fakes::{
    MockClient, MockDisplay, MockFeedback, MockNvs, MockPortal, MockPower, MockScanner, MockWifi,
    SharedMockPower,
};
use common::memory_ring_storage::MemoryRingStorage;
use stockbot_scanner_core::{RingBuffer, ScannerController, DOCK_SUPPRESS_MS, RESCAN_COOLDOWN_MS};

struct Rig {
    scanner: MockScanner,
    power: Rc<RefCell<MockPower>>,
    paired: bool,
}

impl Rig {
    fn paired() -> Self {
        Self::with(true)
    }

    fn unprovisioned() -> Self {
        Self::with(false)
    }

    fn with(paired: bool) -> Self {
        Self {
            scanner: MockScanner::default(),
            power: Rc::new(RefCell::new(MockPower::default())),
            paired,
        }
    }

    fn set_vbus(&self, present: bool) {
        self.power.borrow_mut().status.vbus_present = present;
    }

    /// Boots a controller (offline Wi-Fi, so scans land in the ring) and runs `drive`.
    fn run(&mut self, drive: impl FnOnce(&mut ScannerController<'_, MemoryRingStorage>)) {
        let mut display = MockDisplay::default();
        let mut feedback = MockFeedback::default();
        let mut power = SharedMockPower(self.power.clone());
        let mut nvs = MockNvs::default();
        if self.paired {
            nvs.cfg.wifi_ssid = "Home".to_owned();
            nvs.cfg.device_token = "sbd_x".to_owned();
        }
        let mut portal = MockPortal::default();
        let mut client = MockClient::default();
        let mut wifi = MockWifi::default();
        let mut ring = RingBuffer::new(MemoryRingStorage::default());
        let mut controller = ScannerController::new(
            &mut self.scanner,
            &mut display,
            &mut feedback,
            &mut power,
            &mut nvs,
            &mut portal,
            &mut client,
            &mut wifi,
            &mut ring,
        );
        controller.begin(1000, 0, "");
        drive(&mut controller);
    }
}

#[test]
fn docking_turns_continuous_on_and_undocking_turns_it_off() {
    let mut rig = Rig::paired();
    let power = rig.power.clone();
    rig.run(|c| {
        c.tick(2000);
        power.borrow_mut().status.vbus_present = true;
        c.tick(3000);
        power.borrow_mut().status.vbus_present = false;
        c.tick(4000);
    });
    assert_eq!(rig.scanner.continuous_calls, vec![true, false]);
    assert!(!rig.scanner.continuous);
}

#[test]
fn boot_docked_and_paired_is_continuous_after_first_tick() {
    let mut rig = Rig::paired();
    rig.set_vbus(true);
    rig.run(|c| c.tick(2000));
    assert_eq!(rig.scanner.continuous_calls, vec![true]);
}

#[test]
fn docked_but_unprovisioned_stays_manual() {
    let mut rig = Rig::unprovisioned();
    rig.set_vbus(true);
    rig.run(|c| {
        c.tick(2000);
        c.tick(3000);
    });
    assert!(rig.scanner.continuous_calls.is_empty());
    assert!(!rig.scanner.continuous);
}

#[test]
fn docked_trigger_press_and_release_stays_continuous() {
    let mut rig = Rig::paired();
    rig.set_vbus(true);
    rig.run(|c| {
        c.tick(2000);
        c.on_trigger_button_changed(true, 3000);
        c.on_trigger_button_changed(false, 3100);
        c.tick(3200);
    });
    assert_eq!(rig.scanner.continuous_calls, vec![true]);
    assert!(rig.scanner.continuous);
}

#[test]
fn undocked_trigger_press_is_continuous_until_release() {
    let mut rig = Rig::paired();
    rig.run(|c| {
        c.tick(2000);
        c.on_trigger_button_changed(true, 3000);
        c.on_trigger_button_changed(false, 3100);
        c.tick(3200);
    });
    assert_eq!(rig.scanner.continuous_calls, vec![true, false]);
    assert!(!rig.scanner.continuous);
}

#[test]
fn scan_within_dock_suppression_window_is_dropped() {
    let mut rig = Rig::paired();
    let power = rig.power.clone();
    rig.run(|c| {
        c.tick(2000);
        dock(&power, c, 5000);
        c.on_barcode_line("4011", false, 5000 + DOCK_SUPPRESS_MS - 1);
        assert_eq!(c.ring_count(), 0);
        c.on_barcode_line("4011", false, 5000 + DOCK_SUPPRESS_MS);
        assert_eq!(c.ring_count(), 1);
    });
}

#[test]
fn same_barcode_in_continuous_mode_counts_once_inside_cooldown() {
    let mut rig = Rig::paired();
    let power = rig.power.clone();
    rig.run(|c| {
        c.tick(2000);
        dock(&power, c, 5000);
        let t = 10_000;
        c.on_barcode_line("4011", false, t);
        c.on_barcode_line("4011", false, t + RESCAN_COOLDOWN_MS - 1);
        assert_eq!(c.ring_count(), 1);
        c.on_barcode_line("4011", false, t + RESCAN_COOLDOWN_MS);
        assert_eq!(c.ring_count(), 2);
    });
}

/// Docks through the controller's own tick, using the rig's shared power state.
fn dock(
    power: &Rc<RefCell<MockPower>>,
    c: &mut ScannerController<'_, MemoryRingStorage>,
    at_ms: u64,
) {
    power.borrow_mut().status.vbus_present = true;
    c.tick(at_ms);
}
