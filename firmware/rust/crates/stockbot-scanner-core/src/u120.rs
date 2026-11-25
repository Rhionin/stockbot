//! M5Stack U120 "Unit LCD" (1.14 inch, 135x240) over I2C, pure over the [`I2cBus`] trait (#747).
//!
//! The unit has its own microcontroller that takes drawing commands at I2C address `0x3E`. The
//! command set comes from the MIT-licensed M5GFX source, `src/lgfx/v1/panel/Panel_M5UnitLCD.hpp`
//! and `Panel_M5UnitLCD.inl` (<https://github.com/m5stack/M5GFX>, read at commit `28d4794`).
//! The unit's product page is <https://docs.m5stack.com/en/unit/lcd>.
//!
//! Wire format, as M5GFX sends it:
//! - every command is one I2C write that starts with the command byte;
//! - the memory is 135x240, so coordinates are one byte each (M5GFX uses two only at 256 or more);
//! - `CMD_ROTATE 1` turns it to 240x135 landscape, as the old C++ firmware did (`setRotation(1)`);
//! - a reply (`CMD_READ_ID`, `CMD_READ_BUFCOUNT`) is read after writing the command byte;
//! - pixels are RGB565, high byte first (M5GFX's 16-bit raw colour is byte swapped and goes out
//!   low byte first), with `CMD_SET_BYTESWAP 0`;
//! - `CMD_WRITE_RLE_16` takes runs: `[count, hi, lo]` repeats one colour `count` (1 to 255)
//!   times, and `[0, n, n pixels]` sends `n` (3 to 255) pixels as they are.
//!
//! The unit queues commands. `CMD_READ_BUFCOUNT` answers how many more it can take (255 is
//! empty). M5GFX keeps 64 free and counts a row of `w` pixels as `w / 4`; [`U120`] does the same.
//!
//! The screen is drawn into a [`Frame`] with `crate::screen::draw_bands`, then only the rows that
//! changed since the last push go over the bus, because a whole frame takes about a second at
//! 100 kHz.
//!
//! UNVERIFIED on the bench (#747): the pixel byte order, the rotation direction and the queue
//! accounting are read from M5GFX, not measured.

use std::convert::Infallible;

use embedded_graphics::pixelcolor::raw::RawU16;
use embedded_graphics::pixelcolor::Rgb565;
use embedded_graphics::prelude::*;
use embedded_graphics::primitives::Rectangle;

pub use crate::i2c::{I2cBus, I2cError};
use crate::scanner_state::BacklightLevel;
use crate::screen::{SCREEN_H, SCREEN_W};

/// 7-bit I2C address of the U120.
pub const U120_ADDR: u8 = 0x3E;

/// Landscape size after [`CMD_ROTATE`] with [`ROTATION_LANDSCAPE`]; the band layout's size.
pub const WIDTH: usize = SCREEN_W as usize;
pub const HEIGHT: usize = SCREEN_H as usize;

/// Reads 4 bytes: `0x77 0x89 0x00 <firmware version>`.
pub const CMD_READ_ID: u8 = 0x04;
/// Reads 1 byte: how many more commands the unit's queue can take.
pub const CMD_READ_BUFCOUNT: u8 = 0x09;
pub const CMD_INVOFF: u8 = 0x20;
/// `[CMD_BRIGHTNESS, level]`, level 0 to 255.
pub const CMD_BRIGHTNESS: u8 = 0x22;
/// `[CMD_CASET, x_start, x_end]`.
pub const CMD_CASET: u8 = 0x2A;
/// `[CMD_RASET, y_start, y_end]`.
pub const CMD_RASET: u8 = 0x2B;
/// `[CMD_ROTATE, r]`: 0 normal, 1 a quarter turn right, 2 half, 3 three quarters.
pub const CMD_ROTATE: u8 = 0x36;
/// `[CMD_SET_BYTESWAP, 0 or 1]`; 0 (the default) keeps colours high byte first.
pub const CMD_SET_BYTESWAP: u8 = 0x3A;
/// Run-length RGB565 pixels into the window; see the module docs.
pub const CMD_WRITE_RLE_16: u8 = 0x4A;
/// `[CMD_FILLRECT_16, xs, ys, xe, ye, hi, lo]`.
pub const CMD_FILLRECT_16: u8 = 0x6A;

/// First two bytes of the [`CMD_READ_ID`] answer.
pub const ID_PREFIX: [u8; 2] = [0x77, 0x89];
pub const ROTATION_LANDSCAPE: u8 = 1;
/// Free queue slots to keep in hand (M5GFX `_check_repeat` default limit).
pub const QUEUE_LOW_WATER: u32 = 64;
/// [`CMD_READ_BUFCOUNT`] answer when the unit has drawn everything.
pub const QUEUE_EMPTY: u8 = 255;
/// Longest run or literal block in one RLE entry.
pub const RLE_MAX: usize = 255;
/// Wait between queue polls (M5GFX waits 2 ms).
pub const QUEUE_POLL_MS: u32 = 2;
/// Polls before giving up on a queue that does not drain: about half a second.
pub const QUEUE_MAX_POLLS: u32 = 255;

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum U120Error {
    /// The bus failed (NACK, timeout); usually no unit is plugged in.
    Bus,
    /// Something answered at `0x3E` but not with the U120 ID.
    WrongId([u8; 2]),
    /// The unit's command queue did not drain.
    Busy,
}

impl From<I2cError> for U120Error {
    fn from(_: I2cError) -> Self {
        Self::Bus
    }
}

impl std::fmt::Display for U120Error {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::Bus => write!(f, "no answer from the U120 screen on the Grove I2C bus"),
            Self::WrongId(id) => write!(
                f,
                "the device at 0x3E is not a U120 screen (id {:02x} {:02x})",
                id[0], id[1]
            ),
            Self::Busy => write!(f, "the U120 screen stopped taking drawing commands"),
        }
    }
}

pub fn rotate_cmd(rotation: u8) -> [u8; 2] {
    [CMD_ROTATE, rotation]
}

pub fn brightness_cmd(level: u8) -> [u8; 2] {
    [CMD_BRIGHTNESS, level]
}

pub fn byteswap_cmd(swap: bool) -> [u8; 2] {
    [CMD_SET_BYTESWAP, u8::from(swap)]
}

/// Sets the drawing window to columns `x0..=x1` and rows `y0..=y1`.
pub fn window_cmd(x0: u8, y0: u8, x1: u8, y1: u8) -> [u8; 6] {
    [CMD_CASET, x0, x1, CMD_RASET, y0, y1]
}

pub fn fill_rect_cmd(x0: u8, y0: u8, x1: u8, y1: u8, rgb565: u16) -> [u8; 7] {
    let [hi, lo] = rgb565.to_be_bytes();
    [CMD_FILLRECT_16, x0, y0, x1, y1, hi, lo]
}

/// Brightness for each backlight level, as the old C++ `backlightDuty` had it.
pub fn brightness_for(level: BacklightLevel) -> u8 {
    match level {
        BacklightLevel::Full100 => 255,
        BacklightLevel::Dim20 => 51,
        BacklightLevel::Off0 => 0,
    }
}

fn push_literals(pixels: &[u16], out: &mut Vec<u8>) {
    if pixels.len() >= 3 {
        out.push(0);
        out.push(pixels.len() as u8);
        for p in pixels {
            out.extend_from_slice(&p.to_be_bytes());
        }
    } else {
        for p in pixels {
            out.push(1);
            out.extend_from_slice(&p.to_be_bytes());
        }
    }
}

/// Appends the `CMD_WRITE_RLE_16` body for `pixels`. Runs of two or more become `[count, hi, lo]`;
/// single pixels between runs are gathered into `[0, n, ...]` blocks when there are three or more.
pub fn encode_rle16(pixels: &[u16], out: &mut Vec<u8>) {
    let mut literal_start = 0;
    let mut i = 0;
    while i < pixels.len() {
        let colour = pixels[i];
        let run = pixels[i..]
            .iter()
            .take(RLE_MAX)
            .take_while(|&&p| p == colour)
            .count();
        if run >= 2 {
            push_literals(&pixels[literal_start..i], out);
            out.push(run as u8);
            out.extend_from_slice(&colour.to_be_bytes());
            i += run;
            literal_start = i;
        } else {
            i += 1;
            if i - literal_start == RLE_MAX {
                push_literals(&pixels[literal_start..i], out);
                literal_start = i;
            }
        }
    }
    push_literals(&pixels[literal_start..], out);
}

/// A whole landscape screen of raw RGB565 pixels, row by row. `draw_bands` draws into it.
pub struct Frame {
    pixels: Vec<u16>,
}

impl Default for Frame {
    fn default() -> Self {
        Self::new()
    }
}

impl Frame {
    /// Starts black.
    pub fn new() -> Self {
        Self {
            pixels: vec![0; WIDTH * HEIGHT],
        }
    }

    pub fn row(&self, y: usize) -> &[u16] {
        &self.pixels[y * WIDTH..(y + 1) * WIDTH]
    }

    pub fn pixel(&self, x: usize, y: usize) -> u16 {
        self.pixels[y * WIDTH + x]
    }
}

fn raw(colour: Rgb565) -> u16 {
    RawU16::from(colour).into_inner()
}

impl OriginDimensions for Frame {
    fn size(&self) -> Size {
        Size::new(WIDTH as u32, HEIGHT as u32)
    }
}

impl DrawTarget for Frame {
    type Color = Rgb565;
    type Error = Infallible;

    fn draw_iter<I>(&mut self, pixels: I) -> Result<(), Self::Error>
    where
        I: IntoIterator<Item = Pixel<Self::Color>>,
    {
        for Pixel(point, colour) in pixels {
            if let (Ok(x), Ok(y)) = (usize::try_from(point.x), usize::try_from(point.y)) {
                if x < WIDTH && y < HEIGHT {
                    self.pixels[y * WIDTH + x] = raw(colour);
                }
            }
        }
        Ok(())
    }

    fn fill_solid(&mut self, area: &Rectangle, colour: Self::Color) -> Result<(), Self::Error> {
        let area = area.intersection(&self.bounding_box());
        let Some(bottom_right) = area.bottom_right() else {
            return Ok(());
        };
        let value = raw(colour);
        let (x0, x1) = (area.top_left.x as usize, bottom_right.x as usize);
        for y in area.top_left.y as usize..=bottom_right.y as usize {
            self.pixels[y * WIDTH + x0..=y * WIDTH + x1].fill(value);
        }
        Ok(())
    }

    fn clear(&mut self, colour: Self::Color) -> Result<(), Self::Error> {
        self.pixels.fill(raw(colour));
        Ok(())
    }
}

/// FNV-1a over one row; tells which rows changed without keeping a second frame in RAM.
fn row_hash(row: &[u16]) -> u32 {
    let mut hash: u32 = 0x811c_9dc5;
    for p in row {
        for byte in p.to_be_bytes() {
            hash ^= u32::from(byte);
            hash = hash.wrapping_mul(0x0100_0193);
        }
    }
    hash
}

/// Queue slots one command of `pixels` pixels costs, as M5GFX counts them.
fn command_cost(pixels: usize) -> u32 {
    1 + (pixels / 4) as u32
}

/// Driver state: the unit's free queue slots and what is already on the glass.
pub struct U120 {
    free_slots: u32,
    shown_rows: Option<Vec<u32>>,
    scratch: Vec<u8>,
}

impl Default for U120 {
    fn default() -> Self {
        Self::new()
    }
}

impl U120 {
    pub fn new() -> Self {
        Self {
            free_slots: 0,
            shown_rows: None,
            scratch: Vec::new(),
        }
    }

    /// Forgets what is on the glass, so the next [`U120::push`] sends every row.
    pub fn invalidate(&mut self) {
        self.shown_rows = None;
        self.free_slots = 0;
    }

    fn read_bufcount(bus: &mut dyn I2cBus) -> Result<u8, U120Error> {
        let mut buf = [0u8; 1];
        bus.write_read(U120_ADDR, &[CMD_READ_BUFCOUNT], &mut buf)?;
        Ok(buf[0])
    }

    /// Waits until the queue has room for a command costing `cost` plus the low-water margin.
    fn reserve(
        &mut self,
        bus: &mut dyn I2cBus,
        delay_ms: &mut dyn FnMut(u32),
        cost: u32,
    ) -> Result<(), U120Error> {
        let need = cost + QUEUE_LOW_WATER;
        if self.free_slots >= need {
            self.free_slots -= cost;
            return Ok(());
        }
        for _ in 0..QUEUE_MAX_POLLS {
            let free = u32::from(Self::read_bufcount(bus)?);
            if free >= need {
                self.free_slots = free - cost;
                return Ok(());
            }
            delay_ms(QUEUE_POLL_MS);
        }
        self.free_slots = 0;
        Err(U120Error::Busy)
    }

    fn send(
        &mut self,
        bus: &mut dyn I2cBus,
        delay_ms: &mut dyn FnMut(u32),
        cost: u32,
        bytes: &[u8],
    ) -> Result<(), U120Error> {
        self.reserve(bus, delay_ms, cost)?;
        bus.write(U120_ADDR, bytes)?;
        Ok(())
    }

    /// Checks the unit's ID, sets landscape, high-byte-first colour and no inversion, and waits
    /// until the unit has finished starting up. Returns the unit's firmware version.
    ///
    /// M5GFX notes that the unit answers the ID about 250 ms before it draws, so a one-pixel fill
    /// is queued and the queue is polled until it is empty; a unit that never empties it still
    /// counts as found, and the first push waits as usual.
    pub fn begin(
        &mut self,
        bus: &mut dyn I2cBus,
        delay_ms: &mut dyn FnMut(u32),
    ) -> Result<u8, U120Error> {
        self.invalidate();
        let mut id = [0u8; 4];
        bus.write_read(U120_ADDR, &[CMD_READ_ID], &mut id)?;
        if id[..2] != ID_PREFIX {
            return Err(U120Error::WrongId([id[0], id[1]]));
        }
        self.send(bus, delay_ms, 1, &rotate_cmd(ROTATION_LANDSCAPE))?;
        self.send(bus, delay_ms, 1, &byteswap_cmd(false))?;
        self.send(bus, delay_ms, 1, &[CMD_INVOFF])?;
        self.send(bus, delay_ms, 1, &fill_rect_cmd(0, 0, 0, 0, 0))?;
        for _ in 0..QUEUE_MAX_POLLS {
            let free = Self::read_bufcount(bus)?;
            self.free_slots = u32::from(free);
            if free == QUEUE_EMPTY {
                break;
            }
            delay_ms(QUEUE_POLL_MS);
        }
        Ok(id[3])
    }

    pub fn set_brightness(
        &mut self,
        bus: &mut dyn I2cBus,
        delay_ms: &mut dyn FnMut(u32),
        level: u8,
    ) -> Result<(), U120Error> {
        self.send(bus, delay_ms, 1, &brightness_cmd(level))
    }

    /// Sends the rows of `frame` that differ from the last successful push (every row after
    /// [`U120::begin`] or [`U120::invalidate`]). Returns how many rows went out. On an error the
    /// glass is in an unknown state, so the next push sends every row.
    pub fn push(
        &mut self,
        bus: &mut dyn I2cBus,
        delay_ms: &mut dyn FnMut(u32),
        frame: &Frame,
    ) -> Result<usize, U120Error> {
        let hashes: Vec<u32> = (0..HEIGHT).map(|y| row_hash(frame.row(y))).collect();
        let changed: Vec<bool> = match &self.shown_rows {
            Some(shown) => shown.iter().zip(&hashes).map(|(a, b)| a != b).collect(),
            None => vec![true; HEIGHT],
        };
        self.shown_rows = None;
        let mut sent = 0;
        let mut y = 0;
        while y < HEIGHT {
            if !changed[y] {
                y += 1;
                continue;
            }
            let first = y;
            while y < HEIGHT && changed[y] {
                y += 1;
            }
            self.push_rows(bus, delay_ms, frame, first, y - 1)?;
            sent += y - first;
        }
        self.shown_rows = Some(hashes);
        Ok(sent)
    }

    fn push_rows(
        &mut self,
        bus: &mut dyn I2cBus,
        delay_ms: &mut dyn FnMut(u32),
        frame: &Frame,
        first: usize,
        last: usize,
    ) -> Result<(), U120Error> {
        let window = window_cmd(0, first as u8, (WIDTH - 1) as u8, last as u8);
        self.send(bus, delay_ms, 1, &window)?;
        let mut bytes = std::mem::take(&mut self.scratch);
        let mut result = Ok(());
        for y in first..=last {
            bytes.clear();
            bytes.push(CMD_WRITE_RLE_16);
            encode_rle16(frame.row(y), &mut bytes);
            result = self.send(bus, delay_ms, command_cost(WIDTH), &bytes);
            if result.is_err() {
                break;
            }
        }
        self.scratch = bytes;
        result
    }
}
