//! Bench stand-ins: the USB serial console is the barcode reader and the log is the screen,
//! buzzer, LED and power gauge, until the driver tickets (#741 to #745) replace each one.

use std::io::{BufRead, ErrorKind};
use std::sync::mpsc::{self, Receiver, TryRecvError};
use std::time::Duration;

use stockbot_scanner_core::{
    AudioCue, BacklightLevel, CaptivePortalHal, CaptivePortalSubmission, DisplayBands, DisplayHal,
    FeedbackHal, PowerHal, PowerStatus, ScanLine, ScannerHal,
};

const STDIN_THREAD_STACK_BYTES: usize = 8 * 1024;
const STDIN_RETRY_DELAY: Duration = Duration::from_millis(20);

/// Typed lines on the serial console, one barcode (or setup QR text) per line.
pub struct SerialScanner {
    lines: Receiver<String>,
}

impl SerialScanner {
    pub fn spawn() -> std::io::Result<Self> {
        let (tx, lines) = mpsc::channel();
        std::thread::Builder::new()
            .name("serial-scanner".to_owned())
            .stack_size(STDIN_THREAD_STACK_BYTES)
            .spawn(move || {
                let stdin = std::io::stdin();
                let mut buf = String::new();
                loop {
                    buf.clear();
                    // With CONFIG_ESP_CONSOLE_USB_SERIAL_JTAG the console read does not block:
                    // it reports "no data" as Ok(0) or WouldBlock, so wait and ask again.
                    match stdin.lock().read_line(&mut buf) {
                        Ok(0) => std::thread::sleep(STDIN_RETRY_DELAY),
                        Ok(_) => {
                            let line = buf.trim();
                            if !line.is_empty() && tx.send(line.to_owned()).is_err() {
                                return;
                            }
                        }
                        Err(err)
                            if matches!(
                                err.kind(),
                                ErrorKind::WouldBlock | ErrorKind::Interrupted
                            ) =>
                        {
                            std::thread::sleep(STDIN_RETRY_DELAY);
                        }
                        Err(err) => {
                            log::warn!("serial scanner: read failed: {err}");
                            std::thread::sleep(STDIN_RETRY_DELAY);
                        }
                    }
                }
            })?;
        Ok(Self { lines })
    }
}

impl ScannerHal for SerialScanner {
    fn begin(&mut self) {
        log::info!("scanner: type a barcode or setup QR text on the serial console, then Enter");
    }

    fn poll(&mut self, now_ms: u64, out: &mut Vec<ScanLine>) {
        loop {
            match self.lines.try_recv() {
                Ok(barcode) => out.push(ScanLine {
                    barcode,
                    timestamp_ms: now_ms,
                }),
                Err(TryRecvError::Empty | TryRecvError::Disconnected) => return,
            }
        }
    }

    fn enable_1d_only(&mut self, enabled: bool) {
        log::info!("scanner: 1D only = {enabled}");
    }

    fn set_continuous_mode(&mut self, enabled: bool) {
        log::info!("scanner: continuous mode = {enabled}");
    }
}

/// Logs the three bands whenever they change.
#[derive(Default)]
pub struct LogDisplay {
    last: Option<DisplayBands>,
}

impl DisplayHal for LogDisplay {
    fn begin(&mut self) {
        log::info!("display: logging screen changes");
    }

    fn render(&mut self, bands: &DisplayBands) {
        if self.last.as_ref() == Some(bands) {
            return;
        }
        log::info!(
            "display: [{} | {}] [{} / {}] [{}]",
            bands.band1,
            bands.band1_right,
            bands.band2_line1,
            bands.band2_line2,
            bands.band3
        );
        self.last = Some(bands.clone());
    }

    fn set_backlight(&mut self, level: BacklightLevel) {
        log::debug!("display: backlight {level:?}");
    }
}

pub struct LogFeedback;

impl FeedbackHal for LogFeedback {
    fn begin(&mut self) {}

    fn play(&mut self, cue: AudioCue) {
        log::info!("feedback: {cue:?}");
    }

    fn flash_led_for_scan(&mut self, stock_in: bool) {
        log::info!("feedback: led flash (stock in = {stock_in})");
    }
}

/// Reports a dock-powered device with no battery gauge. The AtomS3 targets keep it; the StickS3
/// targets use the M5PM1 driver instead.
#[allow(dead_code)]
pub struct FixedPower;

impl PowerHal for FixedPower {
    fn begin(&mut self) {}

    fn read(&self) -> PowerStatus {
        PowerStatus {
            vbus_present: true,
            battery_pct: None,
            charging: false,
        }
    }

    fn set_grove_power(&mut self, enabled: bool) {
        log::info!("power: grove power = {enabled}");
    }

    fn enter_deep_sleep_wake_on_button(&mut self) {
        log::info!("power: deep sleep requested (not built yet)");
    }
}

/// The captive portal arrives with #745; setup QR text typed on serial covers it until then.
pub struct NoPortal;

impl CaptivePortalHal for NoPortal {
    fn start(&mut self, ap_suffix: &str) {
        log::info!(
            "captive portal not built yet (#745); type the setup QR text on serial (AP suffix {ap_suffix})"
        );
    }

    fn stop(&mut self) {}

    fn loop_portal(&mut self) {}

    fn take_submission(&mut self, _out: &mut CaptivePortalSubmission) -> bool {
        false
    }
}
