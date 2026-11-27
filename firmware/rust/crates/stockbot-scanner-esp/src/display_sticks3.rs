//! `DisplayHal` for the StickS3's built-in ST7789V2 screen (#742).
//!
//! The panel hangs off SPI3 (40 MHz, write only) with SCLK GPIO40, MOSI GPIO39, CS GPIO41, DC
//! GPIO45 and RST GPIO21. The backlight is a LEDC PWM on GPIO38. The panel's own power comes from
//! M5PM1 GPIO2, which `Sticks3Power::begin` switches on. `ScannerController::begin` calls
//! `display.begin()` before `power.begin()`, so the panel is not touched until the first
//! `set_backlight` or `render` call, which happens after the power chip is up.
//!
//! The scanner must keep working without a screen: any setup failure is logged once and the
//! display then ignores every later call.
//!
//! UNVERIFIED until the bench run (see `HARDWARE-sticks3.md`): the rotation direction, the colour
//! order, the SPI mode and the backlight polarity. The numbers come from M5GFX board data.

use esp_idf_svc::hal::delay::FreeRtos;
use esp_idf_svc::hal::gpio::{
    AnyIOPin, Gpio21, Gpio38, Gpio39, Gpio40, Gpio41, Gpio45, Output, PinDriver,
};
use esp_idf_svc::hal::ledc::config::TimerConfig;
use esp_idf_svc::hal::ledc::{LedcDriver, LedcTimerDriver, CHANNEL0, TIMER0};
use esp_idf_svc::hal::spi::config::{Config as SpiConfig, DriverConfig as SpiDriverConfig};
use esp_idf_svc::hal::spi::{Dma, SpiDeviceDriver, SpiDriver, SPI3};
use esp_idf_svc::hal::units::FromValueType;
use mipidsi::interface::SpiInterface;
use mipidsi::models::ST7789;
use mipidsi::options::{ColorInversion, Orientation, Rotation};
use mipidsi::{Builder, Display};
use stockbot_scanner_core::screen::draw_bands;
use stockbot_scanner_core::{BacklightLevel, DisplayBands, DisplayHal};

/// Native portrait size and the position of that window in the controller's 240x320 memory.
const PANEL_WIDTH: u16 = 135;
const PANEL_HEIGHT: u16 = 240;
const PANEL_OFFSET_X: u16 = 52;
const PANEL_OFFSET_Y: u16 = 40;
const SPI_BAUDRATE_MHZ: u32 = 40;
/// Pixel bytes gathered per SPI write. Also the largest DMA transfer of the bus.
const SPI_BUFFER_BYTES: usize = 2048;
const BACKLIGHT_FREQUENCY_KHZ: u32 = 1;

/// Landscape is a quarter turn from the native portrait. Which quarter turn puts the top of the
/// text at the top of the board is not known; flip to `Deg270` if the bench shows it upside down.
const LANDSCAPE_ROTATION: Rotation = Rotation::Deg90;

type PanelSpi = SpiDeviceDriver<'static, SpiDriver<'static>>;
type PanelPin<T> = PinDriver<'static, T, Output>;
type Panel = Display<SpiInterface<'static, PanelSpi, PanelPin<Gpio45>>, ST7789, PanelPin<Gpio21>>;

/// Everything the screen needs, moved out of `Peripherals` in `main`.
pub struct DisplayPeripherals {
    pub spi: SPI3,
    pub sclk: Gpio40,
    pub mosi: Gpio39,
    pub cs: Gpio41,
    pub dc: Gpio45,
    pub rst: Gpio21,
    pub backlight_pin: Gpio38,
    pub backlight_timer: TIMER0,
    pub backlight_channel: CHANNEL0,
}

struct Hardware {
    panel: Panel,
    backlight: LedcDriver<'static>,
    // Kept alive: the channel runs off this timer.
    _backlight_timer: LedcTimerDriver<'static, TIMER0>,
}

enum State {
    /// Not started yet; the panel is set up on first use.
    Pending(Box<DisplayPeripherals>),
    Ready(Box<Hardware>),
    /// Setup failed (already logged) or the peripherals were taken; calls do nothing.
    Unavailable,
}

pub struct Sticks3Display {
    state: State,
    level: BacklightLevel,
    last: Option<DisplayBands>,
}

impl Sticks3Display {
    pub fn new(peripherals: DisplayPeripherals) -> Self {
        Self {
            state: State::Pending(Box::new(peripherals)),
            level: BacklightLevel::Full100,
            last: None,
        }
    }

    /// Starts the panel the first time it is needed. Returns the hardware if there is a screen.
    fn hardware(&mut self) -> Option<&mut Hardware> {
        if matches!(self.state, State::Pending(_)) {
            let State::Pending(peripherals) =
                std::mem::replace(&mut self.state, State::Unavailable)
            else {
                return None;
            };
            match Hardware::start(*peripherals) {
                Ok(mut hardware) => {
                    log::info!("display: ST7789 ready");
                    apply_backlight(&mut hardware.backlight, self.level);
                    self.state = State::Ready(Box::new(hardware));
                }
                Err(what) => log::error!("display: {what}; running without a screen"),
            }
        }
        match &mut self.state {
            State::Ready(hardware) => Some(&mut **hardware),
            _ => None,
        }
    }
}

impl Hardware {
    fn start(p: DisplayPeripherals) -> Result<Self, String> {
        // The backlight stays off until the first frame is in place, so start it dark.
        let timer_config = TimerConfig::new().frequency(BACKLIGHT_FREQUENCY_KHZ.kHz().into());
        let backlight_timer = LedcTimerDriver::new(p.backlight_timer, &timer_config)
            .map_err(|e| format!("backlight timer: {e}"))?;
        let backlight = LedcDriver::new(p.backlight_channel, &backlight_timer, p.backlight_pin)
            .map_err(|e| format!("backlight channel: {e}"))?;

        let spi = SpiDeviceDriver::new_single(
            p.spi,
            p.sclk,
            p.mosi,
            None::<AnyIOPin>,
            Some(p.cs),
            &SpiDriverConfig::new().dma(Dma::Auto(SPI_BUFFER_BYTES)),
            &SpiConfig::new()
                .baudrate(SPI_BAUDRATE_MHZ.MHz().into())
                .write_only(true),
        )
        .map_err(|e| format!("SPI bus: {e}"))?;
        let dc = PinDriver::output(p.dc).map_err(|e| format!("DC pin: {e}"))?;
        let rst = PinDriver::output(p.rst).map_err(|e| format!("RST pin: {e}"))?;

        // One buffer for the life of the firmware. `leak` is the safe way to get a 'static slice.
        let buffer: &'static mut [u8] = Box::leak(vec![0u8; SPI_BUFFER_BYTES].into_boxed_slice());
        let interface = SpiInterface::new(spi, dc, buffer);
        let panel = Builder::new(ST7789, interface)
            .display_size(PANEL_WIDTH, PANEL_HEIGHT)
            .display_offset(PANEL_OFFSET_X, PANEL_OFFSET_Y)
            .invert_colors(ColorInversion::Inverted)
            .orientation(Orientation::new().rotate(LANDSCAPE_ROTATION))
            .reset_pin(rst)
            .init(&mut FreeRtos)
            .map_err(|e| format!("panel init: {e:?}"))?;

        Ok(Self {
            panel,
            backlight,
            _backlight_timer: backlight_timer,
        })
    }
}

fn apply_backlight(backlight: &mut LedcDriver<'static>, level: BacklightLevel) {
    let max = backlight.get_max_duty();
    let duty = match level {
        BacklightLevel::Full100 => max,
        BacklightLevel::Dim20 => max * 20 / 100,
        BacklightLevel::Off0 => 0,
    };
    if let Err(err) = backlight.set_duty(duty) {
        log::warn!("display: backlight {level:?} failed: {err}");
    }
}

impl DisplayHal for Sticks3Display {
    fn begin(&mut self) {
        log::info!("display: StickS3 screen starts on first use, after the power chip is up");
    }

    fn render(&mut self, bands: &DisplayBands) {
        if self.last.as_ref() == Some(bands) {
            return;
        }
        let Some(hardware) = self.hardware() else {
            return;
        };
        if let Err(err) = draw_bands(&mut hardware.panel, bands) {
            log::warn!("display: draw failed: {err:?}");
            // Draw again next time instead of trusting a half-drawn frame.
            self.last = None;
            return;
        }
        self.last = Some(bands.clone());
    }

    fn set_backlight(&mut self, level: BacklightLevel) {
        self.level = level;
        if let State::Ready(hardware) = &mut self.state {
            apply_backlight(&mut hardware.backlight, level);
        }
    }
}
