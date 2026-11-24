//! ESP32-S3 scanner firmware: the shared `ScannerController` on real clock, NVS, Wi-Fi and HTTPS.
//! The USB serial console stands in for the barcode reader and the log stands in for the screen
//! until the driver tickets (#741 to #745) land.

#![forbid(unsafe_code)]

#[cfg(feature = "bench-serial")]
mod bench;
mod clock;
mod http_client;
#[cfg(any(feature = "sticks3_u173", feature = "sticks3_u173_uart"))]
mod i2c_bus;
mod marker;
mod nvs_store;
#[cfg(any(feature = "sticks3_u173", feature = "sticks3_u173_uart"))]
mod power_sticks3;
mod wifi;

#[cfg(not(feature = "bench-serial"))]
compile_error!(
    "the bench-serial feature is required until the scanner, display, feedback, power and portal \
     drivers (#741 to #745) replace the serial stand-ins"
);

use std::hash::{BuildHasher, Hasher};
use std::time::Duration;

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
    let mut wifi = wifi::EspWifiStation::new(peripherals.modem, sysloop)?;
    let mac = wifi.mac_string();
    let mut client = http_client::EspDeviceClient::new();
    let mut scanner = bench::SerialScanner::spawn().map_err(|err| {
        log::error!("serial scanner thread failed to start: {err}");
        esp_idf_svc::sys::EspError::from_infallible::<{ esp_idf_svc::sys::ESP_FAIL }>()
    })?;
    let mut display = bench::LogDisplay::default();
    let mut feedback = bench::LogFeedback;
    // The StickS3 power chip sits on the internal I2C bus (SDA GPIO47, SCL GPIO48).
    #[cfg(any(feature = "sticks3_u173", feature = "sticks3_u173_uart"))]
    let mut power = power_sticks3::Sticks3Power::new(i2c_bus::EspI2cBus::new(
        peripherals.i2c1,
        peripherals.pins.gpio47,
        peripherals.pins.gpio48,
    )?);
    #[cfg(not(any(feature = "sticks3_u173", feature = "sticks3_u173_uart")))]
    let mut power = bench::FixedPower;
    let mut portal = bench::NoPortal;
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
        controller.tick(clock.now_ms());
        std::thread::sleep(LOOP_PERIOD);
    }
}
