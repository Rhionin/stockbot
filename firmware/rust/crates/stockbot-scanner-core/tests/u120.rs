//! U120 Unit LCD command encoding and driver (#747), against a fake unit that decodes the command
//! stream onto its own glass the way M5GFX's `Panel_M5UnitLCD` describes it.

use embedded_graphics::pixelcolor::raw::RawU16;
use embedded_graphics::pixelcolor::Rgb565;
use embedded_graphics::prelude::*;
use embedded_graphics::primitives::{PrimitiveStyle, Rectangle};
use stockbot_scanner_core::i2c::SharedI2cBus;
use stockbot_scanner_core::screen::draw_bands;
use stockbot_scanner_core::u120::{
    self, brightness_cmd, brightness_for, byteswap_cmd, encode_rle16, fill_rect_cmd, rotate_cmd,
    window_cmd, Frame, I2cBus, I2cError, U120Error, HEIGHT, U120, U120_ADDR, WIDTH,
};
use stockbot_scanner_core::{BacklightLevel, DisplayBands};

const RED: u16 = 0xF800;
const GREEN: u16 = 0x07E0;
const BLUE: u16 = 0x001F;
const WHITE: u16 = 0xFFFF;

#[test]
fn command_bytes_are_locked() {
    assert_eq!(U120_ADDR, 0x3E);
    assert_eq!(rotate_cmd(1), [0x36, 0x01]);
    assert_eq!(brightness_cmd(51), [0x22, 51]);
    assert_eq!(byteswap_cmd(false), [0x3A, 0x00]);
    assert_eq!(byteswap_cmd(true), [0x3A, 0x01]);
    assert_eq!(window_cmd(0, 34, 239, 103), [0x2A, 0, 239, 0x2B, 34, 103]);
    assert_eq!(
        fill_rect_cmd(1, 2, 3, 4, 0xF81F),
        [0x6A, 1, 2, 3, 4, 0xF8, 0x1F]
    );
    assert_eq!(u120::CMD_READ_ID, 0x04);
    assert_eq!(u120::CMD_READ_BUFCOUNT, 0x09);
    assert_eq!(u120::CMD_INVOFF, 0x20);
    assert_eq!(u120::CMD_WRITE_RLE_16, 0x4A);
    assert_eq!((WIDTH, HEIGHT), (240, 135));
}

#[test]
fn backlight_levels_map_to_the_old_brightness_values() {
    assert_eq!(brightness_for(BacklightLevel::Full100), 255);
    assert_eq!(brightness_for(BacklightLevel::Dim20), 51);
    assert_eq!(brightness_for(BacklightLevel::Off0), 0);
}

fn rle(pixels: &[u16]) -> Vec<u8> {
    let mut out = Vec::new();
    encode_rle16(pixels, &mut out);
    out
}

#[test]
fn rle_encoding_is_locked() {
    assert_eq!(rle(&[]), Vec::<u8>::new());
    assert_eq!(rle(&[RED]), vec![1, 0xF8, 0x00]);
    assert_eq!(rle(&[RED, RED, RED]), vec![3, 0xF8, 0x00]);
    assert_eq!(rle(&[RED, GREEN]), vec![1, 0xF8, 0x00, 1, 0x07, 0xE0]);
    assert_eq!(
        rle(&[RED, GREEN, BLUE]),
        vec![0, 3, 0xF8, 0x00, 0x07, 0xE0, 0x00, 0x1F]
    );
    assert_eq!(
        rle(&[RED, WHITE, WHITE, GREEN, BLUE, RED, BLUE, BLUE]),
        vec![
            1, 0xF8, 0x00, //
            2, 0xFF, 0xFF, //
            0, 3, 0x07, 0xE0, 0x00, 0x1F, 0xF8, 0x00, //
            2, 0x00, 0x1F,
        ]
    );
}

#[test]
fn rle_splits_long_runs_and_long_literal_blocks_at_255() {
    assert_eq!(rle(&[BLUE; 300]), vec![255, 0x00, 0x1F, 45, 0x00, 0x1F]);

    let alternating: Vec<u16> = (0..257)
        .map(|i| if i % 2 == 0 { RED } else { GREEN })
        .collect();
    let out = rle(&alternating);
    assert_eq!(&out[..2], &[0, 255]);
    assert_eq!(out.len(), 2 + 255 * 2 + 2 * 3);
    assert_eq!(&out[out.len() - 6..], &[1, 0x07, 0xE0, 1, 0xF8, 0x00]);
}

/// The unit's decoder, written from the format in the module docs.
fn decode_rle(bytes: &[u8]) -> Vec<u16> {
    let mut out = Vec::new();
    let mut i = 0;
    while i < bytes.len() {
        let count = bytes[i] as usize;
        if count == 0 {
            let n = bytes[i + 1] as usize;
            for k in 0..n {
                let at = i + 2 + 2 * k;
                out.push(u16::from_be_bytes([bytes[at], bytes[at + 1]]));
            }
            i += 2 + 2 * n;
        } else {
            let colour = u16::from_be_bytes([bytes[i + 1], bytes[i + 2]]);
            out.resize(out.len() + count, colour);
            i += 3;
        }
    }
    out
}

#[test]
fn rle_round_trips_every_row_of_a_drawn_screen() {
    let mut frame = Frame::new();
    draw_bands(
        &mut frame,
        &sample_bands("Stock in", "Peanut butter crunchy"),
    )
    .unwrap();
    for y in 0..HEIGHT {
        assert_eq!(decode_rle(&rle(frame.row(y))), frame.row(y), "row {y}");
    }
}

#[test]
fn frame_draws_clips_and_fills() {
    let mut frame = Frame::new();
    assert_eq!(frame.size(), Size::new(240, 135));
    Rectangle::new(Point::new(230, 130), Size::new(20, 20))
        .into_styled(PrimitiveStyle::with_fill(Rgb565::RED))
        .draw(&mut frame)
        .unwrap();
    Pixel(Point::new(-1, 3), Rgb565::GREEN)
        .draw(&mut frame)
        .unwrap();
    Pixel(Point::new(5, 3), Rgb565::GREEN)
        .draw(&mut frame)
        .unwrap();
    assert_eq!(frame.pixel(229, 134), 0);
    assert_eq!(frame.pixel(230, 130), RED);
    assert_eq!(frame.pixel(239, 134), RED);
    assert_eq!(frame.pixel(5, 3), GREEN);
    assert_eq!(frame.pixel(0, 3), 0);
    frame.clear(Rgb565::BLUE).unwrap();
    assert!((0..HEIGHT).all(|y| frame.row(y).iter().all(|&p| p == BLUE)));
}

#[test]
fn frame_holds_the_band_colours() {
    let bands = sample_bands("Stock out", "Rice");
    let mut frame = Frame::new();
    draw_bands(&mut frame, &bands).unwrap();
    assert_eq!(frame.pixel(239, 0), bands.band1_bg_rgb565);
    assert_eq!(frame.pixel(239, 60), 0);
}

fn sample_bands(band1: &str, product: &str) -> DisplayBands {
    DisplayBands {
        band1: band1.to_owned(),
        band1_right: "Wi-Fi".to_owned(),
        band1_bg_rgb565: GREEN,
        band1_fg_rgb565: 0,
        band2_line1: product.to_owned(),
        band2_line2: "0123456789012".to_owned(),
        band3: "Scan a barcode".to_owned(),
        band3_fg_rgb565: WHITE,
        ..DisplayBands::default()
    }
}

#[derive(Debug, Clone, PartialEq, Eq)]
enum Op {
    Write(Vec<u8>),
    Read(Vec<u8>, usize),
}

/// A U120 that keeps its own glass, answers the ID and reports a scripted queue level.
struct FakeUnit {
    present: bool,
    id: [u8; 4],
    /// Answers to `CMD_READ_BUFCOUNT`, in order; the last one repeats.
    bufcounts: Vec<u8>,
    ops: Vec<Op>,
    glass: Vec<u16>,
    rotation: Option<u8>,
    window: (usize, usize, usize, usize),
    cursor: (usize, usize),
}

impl FakeUnit {
    fn new() -> Self {
        Self {
            present: true,
            id: [0x77, 0x89, 0x00, 0x05],
            bufcounts: vec![255],
            ops: Vec::new(),
            glass: vec![0x1234; WIDTH * HEIGHT],
            rotation: None,
            window: (0, 0, WIDTH - 1, HEIGHT - 1),
            cursor: (0, 0),
        }
    }

    fn put(&mut self, colour: u16) {
        let (x, y) = self.cursor;
        self.glass[y * WIDTH + x] = colour;
        let (x0, y0, x1, y1) = self.window;
        self.cursor = if x < x1 {
            (x + 1, y)
        } else if y < y1 {
            (x0, y + 1)
        } else {
            (x0, y0)
        };
    }

    fn writes(&self) -> Vec<Vec<u8>> {
        self.ops
            .iter()
            .filter_map(|op| match op {
                Op::Write(bytes) => Some(bytes.clone()),
                Op::Read(..) => None,
            })
            .collect()
    }

    fn glass_matches(&self, frame: &Frame) -> bool {
        (0..HEIGHT).all(|y| &self.glass[y * WIDTH..(y + 1) * WIDTH] == frame.row(y))
    }
}

impl I2cBus for FakeUnit {
    fn write(&mut self, addr: u8, bytes: &[u8]) -> Result<(), I2cError> {
        assert_eq!(addr, U120_ADDR);
        if !self.present {
            return Err(I2cError);
        }
        self.ops.push(Op::Write(bytes.to_vec()));
        match bytes[0] {
            0x36 => self.rotation = Some(bytes[1]),
            0x2A => {
                let (x0, x1) = (bytes[1] as usize, bytes[2] as usize);
                assert_eq!(bytes[3], 0x2B, "window sets columns then rows");
                let (y0, y1) = (bytes[4] as usize, bytes[5] as usize);
                self.window = (x0, y0, x1, y1);
                self.cursor = (x0, y0);
            }
            0x4A => {
                for colour in decode_rle(&bytes[1..]) {
                    self.put(colour);
                }
            }
            0x6A => {
                let colour = u16::from_be_bytes([bytes[5], bytes[6]]);
                for y in bytes[2] as usize..=bytes[4] as usize {
                    for x in bytes[1] as usize..=bytes[3] as usize {
                        self.glass[y * WIDTH + x] = colour;
                    }
                }
            }
            _ => {}
        }
        Ok(())
    }

    fn write_read(&mut self, addr: u8, bytes: &[u8], buf: &mut [u8]) -> Result<(), I2cError> {
        assert_eq!(addr, U120_ADDR);
        if !self.present {
            return Err(I2cError);
        }
        self.ops.push(Op::Read(bytes.to_vec(), buf.len()));
        match bytes {
            [0x04] => buf.copy_from_slice(&self.id),
            [0x09] => {
                buf[0] = self.bufcounts[0];
                if self.bufcounts.len() > 1 {
                    self.bufcounts.remove(0);
                }
            }
            other => panic!("unexpected read command {other:?}"),
        }
        Ok(())
    }
}

fn no_delay() -> impl FnMut(u32) {
    |_| {}
}

#[test]
fn begin_sends_the_locked_setup_sequence() {
    let mut unit = FakeUnit::new();
    let mut panel = U120::new();
    assert_eq!(panel.begin(&mut unit, &mut no_delay()), Ok(5));
    assert_eq!(
        unit.ops,
        vec![
            Op::Read(vec![0x04], 4),
            Op::Read(vec![0x09], 1),
            Op::Write(vec![0x36, 0x01]),
            Op::Write(vec![0x3A, 0x00]),
            Op::Write(vec![0x20]),
            Op::Write(vec![0x6A, 0, 0, 0, 0, 0x00, 0x00]),
            Op::Read(vec![0x09], 1),
        ]
    );
    assert_eq!(unit.rotation, Some(1));
}

#[test]
fn begin_waits_for_the_unit_to_finish_starting() {
    let mut unit = FakeUnit::new();
    unit.bufcounts = vec![255, 200, 254, 255];
    let mut delays = Vec::new();
    let mut panel = U120::new();
    panel
        .begin(&mut unit, &mut |ms| delays.push(ms))
        .expect("begin");
    assert_eq!(delays, vec![2, 2]);
}

#[test]
fn begin_reports_a_missing_unit_and_a_wrong_id() {
    let mut unit = FakeUnit::new();
    unit.present = false;
    assert_eq!(
        U120::new().begin(&mut unit, &mut no_delay()),
        Err(U120Error::Bus)
    );

    let mut other = FakeUnit::new();
    other.id = [0x12, 0x34, 0, 0];
    assert_eq!(
        U120::new().begin(&mut other, &mut no_delay()),
        Err(U120Error::WrongId([0x12, 0x34]))
    );
    assert!(other.writes().is_empty(), "nothing is drawn on a wrong ID");
}

#[test]
fn first_push_sends_every_row_and_the_glass_matches() {
    let mut unit = FakeUnit::new();
    let mut panel = U120::new();
    panel.begin(&mut unit, &mut no_delay()).unwrap();
    let mut frame = Frame::new();
    draw_bands(&mut frame, &sample_bands("Stock in", "Oat milk")).unwrap();

    unit.ops.clear();
    assert_eq!(panel.push(&mut unit, &mut no_delay(), &frame), Ok(HEIGHT));
    assert!(unit.glass_matches(&frame));
    let writes = unit.writes();
    assert_eq!(writes[0], vec![0x2A, 0, 239, 0x2B, 0, 134]);
    assert_eq!(writes.len(), 1 + HEIGHT);
    assert!(writes[1..].iter().all(|w| w[0] == 0x4A));
}

#[test]
fn later_pushes_send_only_the_changed_rows() {
    let mut unit = FakeUnit::new();
    let mut panel = U120::new();
    panel.begin(&mut unit, &mut no_delay()).unwrap();
    let mut frame = Frame::new();
    draw_bands(&mut frame, &sample_bands("Stock in", "Oat milk")).unwrap();
    panel.push(&mut unit, &mut no_delay(), &frame).unwrap();

    unit.ops.clear();
    assert_eq!(panel.push(&mut unit, &mut no_delay(), &frame), Ok(0));
    assert!(unit.ops.is_empty(), "an unchanged frame sends nothing");

    Rectangle::new(Point::new(10, 40), Size::new(5, 3))
        .into_styled(PrimitiveStyle::with_fill(Rgb565::from(RawU16::new(RED))))
        .draw(&mut frame)
        .unwrap();
    Pixel(Point::new(0, 100), Rgb565::WHITE)
        .draw(&mut frame)
        .unwrap();
    assert_eq!(panel.push(&mut unit, &mut no_delay(), &frame), Ok(4));
    let writes = unit.writes();
    assert_eq!(writes[0], vec![0x2A, 0, 239, 0x2B, 40, 42]);
    assert_eq!(writes[4], vec![0x2A, 0, 239, 0x2B, 100, 100]);
    assert_eq!(writes.len(), 6);
    assert!(unit.glass_matches(&frame));
}

#[test]
fn a_full_queue_waits_and_then_gives_up_as_busy() {
    let mut unit = FakeUnit::new();
    let mut panel = U120::new();
    panel.begin(&mut unit, &mut no_delay()).unwrap();
    let frame = Frame::new();

    unit.bufcounts = vec![0];
    let mut waits = 0u32;
    assert_eq!(
        panel.push(&mut unit, &mut |_| waits += 1, &frame),
        Err(U120Error::Busy)
    );
    assert_eq!(waits, u120::QUEUE_MAX_POLLS);

    unit.bufcounts = vec![255];
    unit.ops.clear();
    assert_eq!(
        panel.push(&mut unit, &mut no_delay(), &frame),
        Ok(HEIGHT),
        "after a failed push every row goes again"
    );
    assert!(unit.glass_matches(&frame));
}

#[test]
fn the_queue_is_polled_again_once_the_known_room_is_used() {
    let mut unit = FakeUnit::new();
    let mut panel = U120::new();
    panel.begin(&mut unit, &mut no_delay()).unwrap();
    unit.ops.clear();
    panel
        .push(&mut unit, &mut no_delay(), &Frame::new())
        .unwrap();
    let polls = unit
        .ops
        .iter()
        .filter(|op| matches!(op, Op::Read(cmd, 1) if cmd == &[0x09]))
        .count();
    // 255 slots, 64 kept back, 61 per row: three rows fit per poll, and the first three use the
    // room left from `begin`.
    assert_eq!(polls, HEIGHT / 3 - 1);
}

#[test]
fn shared_bus_clones_reach_the_same_device() {
    let shared = SharedI2cBus::new(FakeUnit::new());
    let mut panel_side = shared.clone();
    let mut other_side = shared;
    panel_side.write(U120_ADDR, &[0x36, 3]).unwrap();
    other_side.write(U120_ADDR, &[0x20]).unwrap();
    let mut id = [0u8; 4];
    other_side.write_read(U120_ADDR, &[0x04], &mut id).unwrap();
    assert_eq!(id, [0x77, 0x89, 0x00, 0x05]);
}

#[test]
fn errors_read_as_plain_sentences() {
    assert_eq!(
        U120Error::Bus.to_string(),
        "no answer from the U120 screen on the Grove I2C bus"
    );
    assert_eq!(
        U120Error::WrongId([0x12, 0x34]).to_string(),
        "the device at 0x3E is not a U120 screen (id 12 34)"
    );
    assert_eq!(
        U120Error::Busy.to_string(),
        "the U120 screen stopped taking drawing commands"
    );
}
