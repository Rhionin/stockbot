//! `FeedbackHal` for the AtomS3 Lite (#747): the front RGB LED (a WS2812-type pixel on GPIO35)
//! flashes for about 30 ms per scan, green for stock in and orange for stock out, as the old C++
//! firmware did (`flashLedForScan`: `0x00ff00` / `0xffa500`, `delay(30)`). The board has no
//! speaker, so sound cues are only logged.
//!
//! The pixel is driven by the legacy RMT driver (esp-idf-hal feature `rmt-legacy`), with the pulse
//! timings of esp-idf-hal's `rmt_neopixel` example. If the RMT channel cannot start, scans simply
//! do not flash.
//!
//! UNVERIFIED on the bench: the colour order (GRB is what WS2812 parts take).

use std::time::Duration;

use esp_idf_svc::hal::delay::FreeRtos;
use esp_idf_svc::hal::gpio::Gpio35;
use esp_idf_svc::hal::rmt::config::TransmitConfig;
use esp_idf_svc::hal::rmt::{FixedLengthSignal, PinState, Pulse, TxRmtDriver, CHANNEL0};
use esp_idf_svc::sys::EspError;
use stockbot_scanner_core::{AudioCue, FeedbackHal};

const FLASH_MS: u32 = 30;
const STOCK_IN_RGB: u32 = 0x00_ff_00;
const STOCK_OUT_RGB: u32 = 0xff_a5_00;
const OFF_RGB: u32 = 0;

/// WS2812 bit order: green, red, blue, most significant bit first.
fn grb(rgb: u32) -> u32 {
    let (r, g, b) = ((rgb >> 16) & 0xff, (rgb >> 8) & 0xff, rgb & 0xff);
    (g << 16) | (r << 8) | b
}

pub struct LedFeedback {
    led: Option<TxRmtDriver<'static>>,
}

impl LedFeedback {
    pub fn new(channel: CHANNEL0, pin: Gpio35) -> Self {
        let config = TransmitConfig::new().clock_divider(1);
        let led = match TxRmtDriver::new(channel, pin, &config) {
            Ok(led) => Some(led),
            Err(err) => {
                log::error!("feedback: the LED could not start ({err}); scans will not flash");
                None
            }
        };
        Self { led }
    }

    fn show(&mut self, rgb: u32) {
        let Some(led) = self.led.as_mut() else {
            return;
        };
        if let Err(err) = send_pixel(led, grb(rgb)) {
            log::warn!("feedback: LED write failed: {err}");
        }
    }
}

fn send_pixel(led: &mut TxRmtDriver<'static>, bits: u32) -> Result<(), EspError> {
    let ticks_hz = led.counter_clock()?;
    let pulse = |state, ns| Pulse::new_with_duration(ticks_hz, state, &Duration::from_nanos(ns));
    let zero = (pulse(PinState::High, 350)?, pulse(PinState::Low, 800)?);
    let one = (pulse(PinState::High, 700)?, pulse(PinState::Low, 600)?);
    let mut signal = FixedLengthSignal::<24>::new();
    for i in 0..24 {
        let bit = bits & (1 << (23 - i)) != 0;
        signal.set(i, if bit { &one } else { &zero })?;
    }
    led.start_blocking(&signal)
}

impl FeedbackHal for LedFeedback {
    fn begin(&mut self) {
        self.show(OFF_RGB);
    }

    fn play(&mut self, cue: AudioCue) {
        log::debug!("feedback: {cue:?} (no speaker on the AtomS3 Lite)");
    }

    fn flash_led_for_scan(&mut self, stock_in: bool) {
        let colour = if stock_in {
            STOCK_IN_RGB
        } else {
            STOCK_OUT_RGB
        };
        self.show(colour);
        FreeRtos::delay_ms(FLASH_MS);
        self.show(OFF_RGB);
    }
}
