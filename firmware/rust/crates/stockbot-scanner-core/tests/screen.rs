//! Band renderer checks against a 240x135 framebuffer (issue #742).

use std::convert::Infallible;
use std::fs;
use std::path::PathBuf;

use embedded_graphics::pixelcolor::raw::RawU16;
use embedded_graphics::pixelcolor::Rgb565;
use embedded_graphics::prelude::*;
use serde::Deserialize;
use stockbot_scanner_core::screen::{
    draw_bands, BAND1_HEIGHT, BAND2_TOP, BAND3_TOP, SCREEN_H, SCREEN_W, SIDE_MARGIN,
};
use stockbot_scanner_core::{AudioCue, DisplayBands, BAND2_CHARS_PER_LINE, BAND3_CHARS_PER_LINE};

/// Unlike `MockDisplay` (64x64 at most) this holds a whole screen, and it records every write
/// outside the screen instead of dropping it.
struct Framebuffer {
    pixels: Vec<Option<Rgb565>>,
    out_of_bounds: Vec<Point>,
}

impl Framebuffer {
    fn new() -> Self {
        Self {
            pixels: vec![None; (SCREEN_W * SCREEN_H) as usize],
            out_of_bounds: Vec::new(),
        }
    }

    fn at(&self, x: u32, y: u32) -> Option<Rgb565> {
        self.pixels[(y * SCREEN_W + x) as usize]
    }

    /// Right-most x holding a pixel that is not black within rows `top..bottom`.
    fn rightmost_lit(&self, top: u32, bottom: u32) -> Option<u32> {
        (top..bottom)
            .flat_map(|y| (0..SCREEN_W).map(move |x| (x, y)))
            .filter(|&(x, y)| self.at(x, y) != Some(Rgb565::BLACK))
            .map(|(x, _)| x)
            .max()
    }
}

impl OriginDimensions for Framebuffer {
    fn size(&self) -> Size {
        Size::new(SCREEN_W, SCREEN_H)
    }
}

impl DrawTarget for Framebuffer {
    type Color = Rgb565;
    type Error = Infallible;

    fn draw_iter<I>(&mut self, pixels: I) -> Result<(), Self::Error>
    where
        I: IntoIterator<Item = Pixel<Rgb565>>,
    {
        for Pixel(p, color) in pixels {
            if p.x < 0 || p.y < 0 || p.x >= SCREEN_W as i32 || p.y >= SCREEN_H as i32 {
                self.out_of_bounds.push(p);
            } else {
                self.pixels[(p.y as u32 * SCREEN_W + p.x as u32) as usize] = Some(color);
            }
        }
        Ok(())
    }
}

fn rgb565(raw: u16) -> Rgb565 {
    Rgb565::from(RawU16::new(raw))
}

#[derive(Debug, Deserialize)]
struct BandsJson {
    band1: String,
    #[serde(rename = "band1Right")]
    band1_right: String,
    #[serde(rename = "band1Bg")]
    band1_bg: String,
    #[serde(rename = "band1BgRgb565")]
    band1_bg_rgb565: u16,
    #[serde(rename = "band1FgRgb565")]
    band1_fg_rgb565: u16,
    #[serde(rename = "band2Line1")]
    band2_line1: String,
    #[serde(rename = "band2Line2")]
    band2_line2: String,
    band3: String,
    #[serde(rename = "band3FgRgb565")]
    band3_fg_rgb565: u16,
}

#[derive(Debug, Deserialize)]
struct ScreenVector {
    name: String,
    bands: BandsJson,
}

/// `display_model_vectors.rs` already proves the core produces exactly these bands for each
/// screen, so the expected bands from the vector file stand in for the core's output here.
fn vector_bands() -> Vec<(String, DisplayBands)> {
    let path = PathBuf::from(env!("CARGO_MANIFEST_DIR"))
        .join("../../../..")
        .join("testdata/display-model/screens.json");
    let raw = fs::read_to_string(&path).unwrap_or_else(|e| panic!("read {}: {e}", path.display()));
    let vectors: Vec<ScreenVector> = serde_json::from_str(&raw).expect("parse screens.json");
    assert!(!vectors.is_empty());
    vectors
        .into_iter()
        .map(|v| {
            let b = v.bands;
            let bands = DisplayBands {
                band1: b.band1,
                band1_right: b.band1_right,
                band1_bg: b.band1_bg,
                band1_bg_rgb565: b.band1_bg_rgb565,
                band1_fg_rgb565: b.band1_fg_rgb565,
                band2_line1: b.band2_line1,
                band2_line2: b.band2_line2,
                band3: b.band3,
                band3_fg_rgb565: b.band3_fg_rgb565,
                audio: AudioCue::Silent,
            };
            (v.name, bands)
        })
        .collect()
}

fn draw(bands: &DisplayBands) -> Framebuffer {
    let mut fb = Framebuffer::new();
    draw_bands(&mut fb, bands).expect("framebuffer never fails");
    fb
}

#[test]
fn every_screen_vector_draws_inside_the_screen_with_the_right_backgrounds() {
    for (name, bands) in vector_bands() {
        let fb = draw(&bands);
        assert!(
            fb.out_of_bounds.is_empty(),
            "{name}: wrote outside the screen: {:?}",
            fb.out_of_bounds
        );
        assert!(
            fb.pixels.iter().all(Option::is_some),
            "{name}: some pixels were never drawn (the clear must cover the screen)"
        );
        let bg = rgb565(bands.band1_bg_rgb565);
        // Left of the text margin and right of the right margin: no text can reach these.
        for (x, y) in [
            (0, 0),
            (0, BAND1_HEIGHT - 1),
            (SCREEN_W - 1, 0),
            (SCREEN_W - 1, BAND1_HEIGHT - 1),
        ] {
            assert_eq!(
                fb.at(x, y),
                Some(bg),
                "{name}: band 1 background at ({x},{y})"
            );
        }
        // The rows just inside each band's top edge and the last row hold no text.
        for (x, y) in [
            (0, BAND2_TOP as u32),
            (SCREEN_W / 2, BAND2_TOP as u32 + 1),
            (SCREEN_W - 1, BAND3_TOP as u32 - 1),
            (0, BAND3_TOP as u32),
            (SCREEN_W / 2, BAND3_TOP as u32 + 1),
            (SCREEN_W - 1, SCREEN_H - 1),
        ] {
            assert_eq!(
                fb.at(x, y),
                Some(Rgb565::BLACK),
                "{name}: expected black at ({x},{y})"
            );
        }
    }
}

#[test]
fn every_screen_vector_draws_some_text_in_each_filled_band() {
    for (name, bands) in vector_bands() {
        let fb = draw(&bands);
        let bg = rgb565(bands.band1_bg_rgb565);
        let lit = |top: u32, bottom: u32, not: Rgb565| {
            (top..bottom)
                .flat_map(|y| (0..SCREEN_W).map(move |x| (x, y)))
                .any(|(x, y)| fb.at(x, y) != Some(not))
        };
        assert_eq!(
            lit(0, BAND1_HEIGHT, bg),
            !(bands.band1.is_empty() && bands.band1_right.is_empty()),
            "{name}: band 1 text"
        );
        assert_eq!(
            lit(BAND2_TOP as u32, BAND3_TOP as u32, Rgb565::BLACK),
            !(bands.band2_line1.is_empty() && bands.band2_line2.is_empty()),
            "{name}: band 2 text"
        );
        assert_eq!(
            lit(BAND3_TOP as u32, SCREEN_H, Rgb565::BLACK),
            !bands.band3.is_empty(),
            "{name}: band 3 text"
        );
    }
}

fn bands_with(f: impl FnOnce(&mut DisplayBands)) -> DisplayBands {
    let mut bands = DisplayBands::default();
    f(&mut bands);
    bands
}

#[test]
fn full_width_band2_lines_stay_on_the_screen() {
    let line = "W".repeat(BAND2_CHARS_PER_LINE);
    let fb = draw(&bands_with(|b| {
        b.band2_line1 = line.clone();
        b.band2_line2 = line.clone();
    }));
    assert!(fb.out_of_bounds.is_empty(), "{:?}", fb.out_of_bounds);
    let right = fb
        .rightmost_lit(BAND2_TOP as u32, BAND3_TOP as u32)
        .expect("text drawn");
    assert!(right < SCREEN_W, "band 2 reaches x={right}");
    assert!(
        right > SCREEN_W / 2,
        "band 2 line looks too short: x={right}"
    );
}

#[test]
fn full_width_band3_line_stays_on_the_screen() {
    let fb = draw(&bands_with(|b| {
        b.band3 = "W".repeat(BAND3_CHARS_PER_LINE);
    }));
    assert!(fb.out_of_bounds.is_empty(), "{:?}", fb.out_of_bounds);
    let right = fb
        .rightmost_lit(BAND3_TOP as u32, SCREEN_H)
        .expect("text drawn");
    assert!(right < SCREEN_W, "band 3 reaches x={right}");
    assert!(
        right > SCREEN_W / 2,
        "band 3 line looks too short: x={right}"
    );
}

#[test]
fn band1_right_text_ends_inside_the_right_margin() {
    let bands = bands_with(|b| {
        b.band1_right = "100%".into();
        b.band1_bg_rgb565 = 0x001F;
        b.band1_fg_rgb565 = 0xFFFF;
    });
    let fb = draw(&bands);
    assert!(fb.out_of_bounds.is_empty());
    let right = fb.rightmost_lit(0, BAND1_HEIGHT).expect("text drawn");
    // rightmost_lit treats anything but black as lit; band 1 is blue, so look for the text colour.
    let text_right = (0..BAND1_HEIGHT)
        .flat_map(|y| (0..SCREEN_W).map(move |x| (x, y)))
        .filter(|&(x, y)| fb.at(x, y) == Some(Rgb565::WHITE))
        .map(|(x, _)| x)
        .max()
        .expect("white text pixels");
    assert!(right == SCREEN_W - 1, "the blue band spans the width");
    // embedded-graphics right alignment puts the last pixel column on the anchor x itself.
    let anchor = SCREEN_W - SIDE_MARGIN as u32;
    assert!(
        text_right <= anchor,
        "right text reaches x={text_right}, anchor {anchor}"
    );
    assert!(
        text_right + 10 > anchor,
        "right text is not right-aligned: x={text_right}"
    );
}

#[test]
fn non_ascii_text_draws_without_panicking() {
    let fb = draw(&bands_with(|b| {
        b.band1 = "Café".into();
        b.band2_line1 = "Café".into();
        b.band2_line2 = "日本語 \u{1F600}".into();
        b.band3 = "Café".into();
    }));
    assert!(fb.out_of_bounds.is_empty());
}
