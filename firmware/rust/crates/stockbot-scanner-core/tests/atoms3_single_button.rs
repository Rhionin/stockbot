//! The AtomS3 Lite has one button that is both the trigger and the mode button, so the firmware
//! feeds the same level to both debouncer inputs (#747). These checks hold the hold gestures of
//! `button_hold_tracker.rs` to that wiring: a short press changes mode, 5 s starts Wi-Fi setup and
//! 10 s erases the device, on a paired AtomS3 that is always on USB power.

mod common;

use common::controller_fakes::{
    MockClient, MockDisplay, MockFeedback, MockNvs, MockPortal, MockPower, MockScanner, MockWifi,
};
use common::memory_ring_storage::MemoryRingStorage;
use stockbot_scanner_core::buttons::{dispatch, ButtonPoller};
use stockbot_scanner_core::{OperationalState, RingBuffer, ScannerController, UiScreen};

const LOOP_MS: u64 = 5;

struct Rig {
    scanner: MockScanner,
    nvs: MockNvs,
}

impl Rig {
    fn paired() -> Self {
        let mut nvs = MockNvs::default();
        nvs.cfg.wifi_ssid = "Home".to_owned();
        nvs.cfg.device_token = "sbd_x".to_owned();
        Self {
            scanner: MockScanner::default(),
            nvs,
        }
    }

    fn run(&mut self, drive: impl FnOnce(&mut ScannerController<'_, MemoryRingStorage>)) {
        let mut display = MockDisplay::default();
        let mut feedback = MockFeedback::default();
        let mut power = MockPower::default();
        power.status.vbus_present = true;
        let mut portal = MockPortal::default();
        let mut client = MockClient::default();
        let mut wifi = MockWifi::default();
        let mut ring = RingBuffer::new(MemoryRingStorage::default());
        let mut controller = ScannerController::new(
            &mut self.scanner,
            &mut display,
            &mut feedback,
            &mut power,
            &mut self.nvs,
            &mut portal,
            &mut client,
            &mut wifi,
            &mut ring,
        );
        controller.begin(1000, 0, "");
        controller.tick(1500);
        drive(&mut controller);
    }
}

/// Holds the one button from `from_ms` until `to_ms`, then lets go for 100 ms, sampling every
/// loop pass the way `main.rs` does: the same level goes to the trigger and the mode input.
/// `while_held` runs once, just before the release.
fn press_for(
    c: &mut ScannerController<'_, MemoryRingStorage>,
    poller: &mut ButtonPoller,
    from_ms: u64,
    to_ms: u64,
    while_held: impl FnOnce(&ScannerController<'_, MemoryRingStorage>),
) {
    let mut now = from_ms;
    while now < to_ms {
        sample(c, poller, now, true);
        now += LOOP_MS;
    }
    while_held(c);
    while now < to_ms + 100 {
        sample(c, poller, now, false);
        now += LOOP_MS;
    }
}

fn sample(
    c: &mut ScannerController<'_, MemoryRingStorage>,
    poller: &mut ButtonPoller,
    now_ms: u64,
    pressed: bool,
) {
    let mut events = Vec::new();
    poller.sample(now_ms, pressed, pressed, &mut events);
    for event in events {
        dispatch(c, event, now_ms);
    }
}

#[test]
fn short_press_changes_mode_each_time() {
    let mut rig = Rig::paired();
    rig.run(|c| {
        let mut poller = ButtonPoller::new();
        let first = c.active_direction();
        press_for(c, &mut poller, 2000, 2200, |_| {});
        let second = c.active_direction();
        assert_ne!(second, first, "a short press changes mode");
        press_for(c, &mut poller, 3000, 3200, |_| {});
        assert_eq!(
            c.active_direction(),
            first,
            "a second press changes it back"
        );
        assert_eq!(c.operational_state(), OperationalState::Paired);
    });
}

#[test]
fn short_press_keeps_hands_free_scanning_on() {
    let mut rig = Rig::paired();
    rig.run(|c| {
        let mut poller = ButtonPoller::new();
        press_for(c, &mut poller, 2000, 2200, |_| {});
        c.tick(2400);
    });
    assert!(
        rig.scanner.continuous,
        "on USB power and paired, the reader keeps scanning"
    );
}

#[test]
fn five_second_hold_starts_wifi_setup_without_changing_mode() {
    let mut rig = Rig::paired();
    rig.run(|c| {
        let mut poller = ButtonPoller::new();
        let before = c.active_direction();
        press_for(c, &mut poller, 2000, 8000, |c| {
            assert_eq!(c.ui_screen(), UiScreen::SWifiButtonHold);
        });
        assert_eq!(c.ui_screen(), UiScreen::SWifiSetup);
        assert_eq!(c.active_direction(), before, "a hold is not a mode change");
        assert_eq!(c.operational_state(), OperationalState::Paired);
    });
    assert_eq!(rig.nvs.cfg.device_token, "sbd_x");
}

#[test]
fn ten_second_hold_erases_the_device() {
    let mut rig = Rig::paired();
    rig.run(|c| {
        let mut poller = ButtonPoller::new();
        press_for(c, &mut poller, 2000, 12_500, |c| {
            assert_eq!(c.ui_screen(), UiScreen::SWifiButtonHoldErase);
        });
        assert_eq!(c.operational_state(), OperationalState::Unprovisioned);
    });
}
