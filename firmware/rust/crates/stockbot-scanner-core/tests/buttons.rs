//! StickS3 button debounce, edge decoding and controller dispatch (#744, part 1).

mod common;

use common::controller_fakes::{
    MockClient, MockDisplay, MockFeedback, MockNvs, MockPortal, MockPower, MockScanner, MockWifi,
};
use common::memory_ring_storage::MemoryRingStorage;
use stockbot_scanner_core::buttons::{dispatch, ButtonEvent, ButtonPoller, DEBOUNCE_MS};
use stockbot_scanner_core::{Direction, RingBuffer, ScannerController};

fn sample(poller: &mut ButtonPoller, now_ms: u64, trigger: bool, mode: bool) -> Vec<ButtonEvent> {
    let mut out = Vec::new();
    poller.sample(now_ms, trigger, mode, &mut out);
    out
}

#[test]
fn trigger_bounce_shorter_than_debounce_makes_no_event() {
    let mut p = ButtonPoller::new();
    assert!(sample(&mut p, 100, true, false).is_empty());
    assert!(sample(&mut p, 100 + DEBOUNCE_MS - 1, true, false).is_empty());
    // Released one millisecond before the press would have counted.
    assert!(sample(&mut p, 100 + DEBOUNCE_MS - 1, false, false).is_empty());
    assert!(sample(&mut p, 500, false, false).is_empty());
}

#[test]
fn trigger_press_counts_at_exactly_the_debounce_time() {
    assert_eq!(DEBOUNCE_MS, 20);
    let mut p = ButtonPoller::new();
    assert!(sample(&mut p, 100, true, false).is_empty());
    assert!(sample(&mut p, 119, true, false).is_empty());
    assert_eq!(
        sample(&mut p, 120, true, false),
        vec![ButtonEvent::TriggerChanged(true)]
    );
    // Exactly one event; holding sends nothing more.
    assert!(sample(&mut p, 130, true, false).is_empty());
    assert!(sample(&mut p, 900, true, false).is_empty());
}

#[test]
fn trigger_release_is_debounced_too() {
    let mut p = ButtonPoller::new();
    sample(&mut p, 0, true, false);
    assert_eq!(
        sample(&mut p, 20, true, false),
        vec![ButtonEvent::TriggerChanged(true)]
    );
    assert!(sample(&mut p, 1000, false, false).is_empty());
    assert!(sample(&mut p, 1019, false, false).is_empty());
    assert_eq!(
        sample(&mut p, 1020, false, false),
        vec![ButtonEvent::TriggerChanged(false)]
    );
    assert!(sample(&mut p, 1100, false, false).is_empty());
}

#[test]
fn bounce_restarts_the_debounce_window() {
    let mut p = ButtonPoller::new();
    sample(&mut p, 0, true, false);
    sample(&mut p, 10, false, false);
    sample(&mut p, 12, true, false);
    assert!(sample(&mut p, 31, true, false).is_empty());
    assert_eq!(
        sample(&mut p, 32, true, false),
        vec![ButtonEvent::TriggerChanged(true)]
    );
}

#[test]
fn mode_press_hold_and_release_sequence() {
    let mut p = ButtonPoller::new();
    assert!(sample(&mut p, 0, false, true).is_empty());
    assert_eq!(sample(&mut p, 20, false, true), vec![ButtonEvent::ModeDown]);
    assert_eq!(
        sample(&mut p, 25, false, true),
        vec![ButtonEvent::ModeHoldPoll]
    );
    assert_eq!(
        sample(&mut p, 5000, false, true),
        vec![ButtonEvent::ModeHoldPoll]
    );
    // A raw release is not seen until it has held for the debounce time.
    assert_eq!(
        sample(&mut p, 5010, false, false),
        vec![ButtonEvent::ModeHoldPoll]
    );
    assert_eq!(
        sample(&mut p, 5030, false, false),
        vec![ButtonEvent::ModeUp]
    );
    assert!(sample(&mut p, 5040, false, false).is_empty());
}

#[test]
fn mode_bounce_makes_no_event() {
    let mut p = ButtonPoller::new();
    assert!(sample(&mut p, 0, false, true).is_empty());
    assert!(sample(&mut p, 19, false, true).is_empty());
    assert!(sample(&mut p, 19, false, false).is_empty());
    assert!(sample(&mut p, 100, false, false).is_empty());
}

#[test]
fn buttons_are_independent_and_can_change_together() {
    let mut p = ButtonPoller::new();
    sample(&mut p, 0, true, true);
    assert_eq!(
        sample(&mut p, 20, true, true),
        vec![ButtonEvent::TriggerChanged(true), ButtonEvent::ModeDown]
    );
    // The trigger releases while the mode button stays held.
    sample(&mut p, 100, false, true);
    assert_eq!(
        sample(&mut p, 120, false, true),
        vec![
            ButtonEvent::TriggerChanged(false),
            ButtonEvent::ModeHoldPoll
        ]
    );
}

#[test]
fn debounce_survives_timestamp_wraparound() {
    let mut p = ButtonPoller::new();
    let start = u64::MAX - 5;
    sample(&mut p, start, true, false);
    assert!(sample(&mut p, start.wrapping_add(19), true, false).is_empty());
    assert_eq!(
        sample(&mut p, start.wrapping_add(20), true, false),
        vec![ButtonEvent::TriggerChanged(true)]
    );
}

struct Rig {
    scanner: MockScanner,
    feedback: MockFeedback,
}

impl Rig {
    fn new() -> Self {
        Self {
            scanner: MockScanner::default(),
            feedback: MockFeedback::default(),
        }
    }

    /// Boots a paired, undocked controller and runs `drive`.
    fn run(&mut self, drive: impl FnOnce(&mut ScannerController<'_, MemoryRingStorage>)) {
        let mut display = MockDisplay::default();
        let mut power = MockPower::default();
        power.status.vbus_present = false;
        let mut nvs = MockNvs::default();
        nvs.cfg.wifi_ssid = "Home".to_owned();
        nvs.cfg.device_token = "sbd_x".to_owned();
        let mut portal = MockPortal::default();
        let mut client = MockClient::default();
        let mut wifi = MockWifi::default();
        let mut ring = RingBuffer::new(MemoryRingStorage::default());
        let mut controller = ScannerController::new(
            &mut self.scanner,
            &mut display,
            &mut self.feedback,
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

/// Feeds raw samples through the poller and the controller, like the firmware loop.
fn feed(
    controller: &mut ScannerController<'_, MemoryRingStorage>,
    poller: &mut ButtonPoller,
    now_ms: u64,
    trigger: bool,
    mode: bool,
) {
    let mut events = Vec::new();
    poller.sample(now_ms, trigger, mode, &mut events);
    for event in events {
        dispatch(controller, event, now_ms);
    }
}

#[test]
fn mode_button_short_press_toggles_stock_direction() {
    let mut rig = Rig::new();
    rig.run(|c| {
        let mut p = ButtonPoller::new();
        let before = c.active_direction();
        feed(c, &mut p, 2000, false, true);
        feed(c, &mut p, 2020, false, true);
        feed(c, &mut p, 2100, false, true);
        feed(c, &mut p, 2200, false, false);
        feed(c, &mut p, 2220, false, false);
        let after = c.active_direction();
        assert_ne!(before, after);
        feed(c, &mut p, 3000, false, true);
        feed(c, &mut p, 3020, false, true);
        feed(c, &mut p, 3100, false, false);
        feed(c, &mut p, 3120, false, false);
        assert_eq!(c.active_direction(), before);
        assert!(matches!(before, Direction::StockIn | Direction::StockOut));
    });
    assert_eq!(rig.feedback.cues.len(), 2);
}

#[test]
fn trigger_press_starts_continuous_decoding_and_release_stops_it() {
    let mut rig = Rig::new();
    rig.run(|c| {
        let mut p = ButtonPoller::new();
        c.tick(2000);
        feed(c, &mut p, 3000, true, false);
        feed(c, &mut p, 3020, true, false);
        assert!(c.trigger_continuous_mode());
        feed(c, &mut p, 3500, false, false);
        feed(c, &mut p, 3520, false, false);
        assert!(!c.trigger_continuous_mode());
        c.tick(3600);
    });
    assert_eq!(rig.scanner.continuous_calls, vec![true, false]);
    assert!(!rig.scanner.continuous);
}

#[test]
fn dispatch_maps_each_event_to_the_controller_call() {
    let mut rig = Rig::new();
    rig.run(|c| {
        dispatch(c, ButtonEvent::TriggerChanged(true), 2000);
        assert!(c.trigger_continuous_mode());
        dispatch(c, ButtonEvent::TriggerChanged(false), 2100);
        assert!(!c.trigger_continuous_mode());
        // Hold the mode button for 5 s: the release runs the Wi-Fi setup gesture.
        dispatch(c, ButtonEvent::ModeDown, 3000);
        dispatch(c, ButtonEvent::ModeHoldPoll, 8100);
        dispatch(c, ButtonEvent::ModeUp, 8100);
        assert_eq!(c.ui_screen(), stockbot_scanner_core::UiScreen::SWifiSetup);
    });
}
