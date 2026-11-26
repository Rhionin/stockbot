//! ESP32-S3 scanner firmware: the shared `ScannerController` on real clock, NVS, Wi-Fi and HTTPS.
//! The USB serial console stands in for the barcode reader, and on the Symcode AtomS3 target the
//! log stands in for the screen, until the driver tickets (#741 to #747) land.

#![forbid(unsafe_code)]

#[cfg(feature = "bench-serial")]
mod bench;
#[cfg(feature = "atoms3lite_u173_bench")]
mod button_atoms3;
#[cfg(any(feature = "sticks3_u173", feature = "sticks3_u173_uart"))]
mod buttons_sticks3;
mod captive_portal;
mod clock;
#[cfg(any(feature = "sticks3_u173", feature = "sticks3_u173_uart"))]
mod display_sticks3;
#[cfg(feature = "atoms3lite_u173_bench")]
mod display_u120;
#[cfg(feature = "atoms3lite_u173_bench")]
mod feedback_atoms3;
mod http_client;
#[cfg(any(
    feature = "sticks3_u173",
    feature = "sticks3_u173_uart",
    feature = "atoms3lite_u173_bench"
))]
mod i2c_bus;
mod marker;
mod nvs_store;
#[cfg(any(feature = "sticks3_u173", feature = "sticks3_u173_uart"))]
mod power_sticks3;
#[cfg(any(feature = "sticks3_u173", feature = "atoms3lite_u173_bench"))]
mod scanner_u173;
mod wifi;

#[cfg(not(feature = "bench-serial"))]
compile_error!(
    "the bench-serial feature is required until the scanner, display, feedback, power and portal \
     drivers (#741 to #745) replace the serial stand-ins"
);

use std::hash::{BuildHasher, Hasher};
use std::time::Duration;

#[cfg(any(
    feature = "sticks3_u173",
    feature = "sticks3_u173_uart",
    feature = "atoms3lite_u173_bench"
))]
use stockbot_scanner_core::buttons::{self, ButtonPoller};
use stockbot_scanner_core::{
    fw_target, ClockHal, MemoryRingStorage, RingBuffer, ScannerController,
};

/// TLS handshakes need more stack than the main task has.
const CONTROLLER_STACK_BYTES: usize = 32 * 1024;
const LOOP_PERIOD: Duration = Duration::from_millis(5);

fn selected_target_id() -> &'static str {
    if cfg!(feature = "sticks3_u173") {
        "sticks3_u173"
    } else if cfg!(feature = "sticks3_u173_uart") {
        "sticks3_u173_uart"
    } else if cfg!(feature = "atoms3lite_u173_bench") {
        "atoms3lite_u173_bench"
    } else if cfg!(feature = "atoms3lite_symcode_hid") {
        "atoms3lite_symcode_hid"
    } else {
        "unknown"
    }
}

fn main() {
    esp_idf_svc::sys::link_patches();
    esp_idf_svc::log::EspLogger::initialize_default();

    // Keep the rodata marker through LTO even without `no_mangle`.
    std::hint::black_box(marker::embedded_bytes());

    let target = selected_target_id();
    let marker_text = fw_target::marker_text(target).unwrap_or_default();
    log::info!(
        "stockbot-scanner-esp boot; version={}; target={target}; marker={marker_text}",
        env!("CARGO_PKG_VERSION")
    );

    let worker = std::thread::Builder::new()
        .name("controller".to_owned())
        .stack_size(CONTROLLER_STACK_BYTES)
        .spawn(run)
        .expect("spawn the controller thread");
    match worker.join() {
        Ok(Ok(())) => {}
        Ok(Err(err)) => log::error!("controller stopped: {err}"),
        Err(_) => log::error!("controller thread panicked"),
    }
    // Returning from main would leave a dead image running; park instead so the log stays readable.
    loop {
        std::thread::sleep(Duration::from_secs(60));
    }
}

#[cfg(feature = "bench-serial")]
fn run() -> Result<(), esp_idf_svc::sys::EspError> {
    use esp_idf_svc::eventloop::EspSystemEventLoop;
    use esp_idf_svc::hal::peripherals::Peripherals;
    use esp_idf_svc::nvs::EspDefaultNvsPartition;

    let peripherals = Peripherals::take()?;
    let sysloop = EspSystemEventLoop::take()?;
    let nvs_partition = EspDefaultNvsPartition::take()?;

    let clock = clock::BootClock::new();
    let mut nvs = nvs_store::EspNvsStore::open(nvs_partition)?;
    let radio = wifi::Radio::new(peripherals.modem, sysloop)?;
    let mut wifi = wifi::EspWifiStation::new(radio.clone());
    let mac = wifi.mac_string();
    let mut client = http_client::EspDeviceClient::new();
    let serial_scanner = bench::SerialScanner::spawn().map_err(|err| {
        log::error!("serial scanner thread failed to start: {err}");
        esp_idf_svc::sys::EspError::from_infallible::<{ esp_idf_svc::sys::ESP_FAIL }>()
    })?;
    // The U173 reader sits on the Grove I2C bus (port 0, SDA GPIO9, SCL GPIO10) and is powered by
    // the M5PM1 boost, which `power.begin()` switches on. The controller calls `scanner.begin()`
    // first, so that first setup may fail; the scanner retries from `poll`. The serial console
    // stays alongside the reader for bench input.
    #[cfg(feature = "sticks3_u173")]
    let mut scanner = scanner_u173::ScannerU173::new(
        i2c_bus::EspI2cBus::new(
            peripherals.i2c0,
            peripherals.pins.gpio9,
            peripherals.pins.gpio10,
        )?,
        serial_scanner,
    );
    // On the AtomS3 the A162 hub carries the U173 reader and the U120 screen on one Grove I2C bus
    // (port 0, SDA GPIO2, SCL GPIO1), powered from USB, so both can start at once.
    #[cfg(feature = "atoms3lite_u173_bench")]
    let grove_bus = stockbot_scanner_core::i2c::SharedI2cBus::new(i2c_bus::EspI2cBus::new(
        peripherals.i2c0,
        peripherals.pins.gpio2,
        peripherals.pins.gpio1,
    )?);
    #[cfg(feature = "atoms3lite_u173_bench")]
    let mut scanner = scanner_u173::ScannerU173::new(grove_bus.clone(), serial_scanner);
    #[cfg(not(any(feature = "sticks3_u173", feature = "atoms3lite_u173_bench")))]
    let mut scanner = serial_scanner;
    // The StickS3 screen starts on first use, after the power chip has switched the LCD on.
    #[cfg(any(feature = "sticks3_u173", feature = "sticks3_u173_uart"))]
    let mut display = display_sticks3::Sticks3Display::new(display_sticks3::DisplayPeripherals {
        spi: peripherals.spi3,
        sclk: peripherals.pins.gpio40,
        mosi: peripherals.pins.gpio39,
        cs: peripherals.pins.gpio41,
        dc: peripherals.pins.gpio45,
        rst: peripherals.pins.gpio21,
        backlight_pin: peripherals.pins.gpio38,
        backlight_timer: peripherals.ledc.timer0,
        backlight_channel: peripherals.ledc.channel0,
    });
    #[cfg(feature = "atoms3lite_u173_bench")]
    let mut display = display_u120::U120Display::new(grove_bus);
    #[cfg(not(any(
        feature = "sticks3_u173",
        feature = "sticks3_u173_uart",
        feature = "atoms3lite_u173_bench"
    )))]
    let mut display = bench::LogDisplay::default();
    #[cfg(feature = "atoms3lite_u173_bench")]
    let mut feedback =
        feedback_atoms3::LedFeedback::new(peripherals.rmt.channel0, peripherals.pins.gpio35);
    #[cfg(not(feature = "atoms3lite_u173_bench"))]
    let mut feedback = bench::LogFeedback;
    // BtnA (GPIO11) is the trigger and BtnB (GPIO12) the mode button. The AtomS3 Lite has one
    // button (GPIO41) that is both.
    #[cfg(any(feature = "sticks3_u173", feature = "sticks3_u173_uart"))]
    let sticks3_buttons =
        buttons_sticks3::Sticks3Buttons::new(buttons_sticks3::ButtonPeripherals {
            trigger: peripherals.pins.gpio11,
            mode: peripherals.pins.gpio12,
        })?;
    #[cfg(feature = "atoms3lite_u173_bench")]
    let atoms3_button = button_atoms3::AtomS3Button::new(peripherals.pins.gpio41)?;
    #[cfg(any(
        feature = "sticks3_u173",
        feature = "sticks3_u173_uart",
        feature = "atoms3lite_u173_bench"
    ))]
    let mut button_poller = ButtonPoller::new();
    #[cfg(any(
        feature = "sticks3_u173",
        feature = "sticks3_u173_uart",
        feature = "atoms3lite_u173_bench"
    ))]
    let mut button_events = Vec::new();
    // The StickS3 power chip sits on the internal I2C bus (SDA GPIO47, SCL GPIO48).
    #[cfg(any(feature = "sticks3_u173", feature = "sticks3_u173_uart"))]
    let mut power = power_sticks3::Sticks3Power::new(i2c_bus::EspI2cBus::new(
        peripherals.i2c1,
        peripherals.pins.gpio47,
        peripherals.pins.gpio48,
    )?);
    // The AtomS3 runs from USB and has no battery gauge.
    #[cfg(not(any(feature = "sticks3_u173", feature = "sticks3_u173_uart")))]
    let mut power = bench::FixedPower;
    let mut portal = captive_portal::EspCaptivePortal::new(radio);
    // RAM only until #469 puts the offline ring in flash.
    let mut ring = RingBuffer::new(MemoryRingStorage::default());

    let mut controller = ScannerController::new(
        &mut scanner,
        &mut display,
        &mut feedback,
        &mut power,
        &mut nvs,
        &mut portal,
        &mut client,
        &mut wifi,
        &mut ring,
    );
    controller.set_hub_version(env!("CARGO_PKG_VERSION"));
    // std seeds `RandomState` from the hardware RNG on ESP-IDF.
    let nonce = std::hash::RandomState::new().build_hasher().finish() as u32;
    controller.begin(clock.now_ms(), nonce, &mac);

    loop {
        let now_ms = clock.now_ms();
        #[cfg(any(
            feature = "sticks3_u173",
            feature = "sticks3_u173_uart",
            feature = "atoms3lite_u173_bench"
        ))]
        {
            #[cfg(any(feature = "sticks3_u173", feature = "sticks3_u173_uart"))]
            let (trigger_pressed, mode_pressed) = sticks3_buttons.read_pressed();
            #[cfg(feature = "atoms3lite_u173_bench")]
            let (trigger_pressed, mode_pressed) = {
                let pressed = atoms3_button.is_pressed();
                (pressed, pressed)
            };
            button_events.clear();
            button_poller.sample(now_ms, trigger_pressed, mode_pressed, &mut button_events);
            for event in button_events.iter().copied() {
                buttons::dispatch(&mut controller, event, now_ms);
            }
        }
        controller.tick(now_ms);
        std::thread::sleep(LOOP_PERIOD);
    }
}
