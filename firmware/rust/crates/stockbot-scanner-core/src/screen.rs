//! Draws the three display bands on a 240x135 RGB565 target (issue #742).
//!
//! The panel driver lives in the ESP crate; this module only turns `DisplayBands` into pixels, so
//! it is tested on the host against a plain framebuffer.
//!
//! Layout (landscape, 240x135, origin top left):
//!
//! | Band | Rows     | Content                                                     | Font     |
//! |------|----------|-------------------------------------------------------------|----------|
//! | 1    | 0..34    | `band1` left, `band1_right` right-aligned, on `band1_bg`    | 10x20    |
//! | 2    | 34..104  | `band2_line1` and `band2_line2` in white on black           | 10x20    |
//! | 3    | 104..135 | `band3` in `band3_fg_rgb565` on black                       | 6x13     |
//!
//! Every band keeps a 4 px side margin. Band 2 holds 18 characters (180 px) and band 3 holds 36
//! characters (216 px), which is what `BAND2_CHARS_PER_LINE` and `BAND3_CHARS_PER_LINE` promise.
//! The ISO 8859-1 fonts draw their replacement glyph for a character they lack, so any string is
//! safe to draw.

use embedded_graphics::mono_font::iso_8859_1::{FONT_10X20, FONT_6X13};
use embedded_graphics::mono_font::{MonoFont, MonoTextStyle};
use embedded_graphics::pixelcolor::raw::RawU16;
use embedded_graphics::pixelcolor::Rgb565;
use embedded_graphics::prelude::*;
use embedded_graphics::primitives::{PrimitiveStyle, Rectangle};
use embedded_graphics::text::{Alignment, Baseline, Text, TextStyleBuilder};

use crate::display_model::DisplayBands;

pub const SCREEN_W: u32 = 240;
pub const SCREEN_H: u32 = 135;

pub const BAND1_TOP: i32 = 0;
pub const BAND1_HEIGHT: u32 = 34;
pub const BAND2_TOP: i32 = 34;
pub const BAND2_HEIGHT: u32 = 70;
pub const BAND3_TOP: i32 = 104;
pub const BAND3_HEIGHT: u32 = SCREEN_H - BAND3_TOP as u32;
/// Gap left of the left text and right of the right-aligned text.
pub const SIDE_MARGIN: i32 = 4;

/// Extra pixels after every band-2 glyph. Must stay 0: 18 glyphs of 10 px have to fit in 240 px.
const BAND2_CHAR_SPACING: u32 = 0;
static BAND2_FONT: MonoFont<'static> = MonoFont {
    character_spacing: BAND2_CHAR_SPACING,
    ..FONT_10X20
};

const BAND1_TEXT_Y: i32 = BAND1_TOP + 7;
const BAND2_LINE1_Y: i32 = BAND2_TOP + 10;
const BAND2_LINE2_Y: i32 = BAND2_TOP + 40;
const BAND3_TEXT_Y: i32 = BAND3_TOP + 9;

fn rgb565(raw: u16) -> Rgb565 {
    Rgb565::from(RawU16::new(raw))
}

fn band_rect(top: i32, height: u32) -> Rectangle {
    Rectangle::new(Point::new(0, top), Size::new(SCREEN_W, height))
}

fn draw_text<D>(
    target: &mut D,
    text: &str,
    at: Point,
    alignment: Alignment,
    style: MonoTextStyle<'_, Rgb565>,
) -> Result<(), D::Error>
where
    D: DrawTarget<Color = Rgb565>,
{
    let text_style = TextStyleBuilder::new()
        .baseline(Baseline::Top)
        .alignment(alignment)
        .build();
    Text::with_text_style(text, at, style, text_style).draw(target)?;
    Ok(())
}

/// Draws the three bands. Clears the whole screen first.
pub fn draw_bands<D>(target: &mut D, bands: &DisplayBands) -> Result<(), D::Error>
where
    D: DrawTarget<Color = Rgb565>,
{
    target.clear(Rgb565::BLACK)?;

    band_rect(BAND1_TOP, BAND1_HEIGHT)
        .into_styled(PrimitiveStyle::with_fill(rgb565(bands.band1_bg_rgb565)))
        .draw(target)?;
    let band1_style = MonoTextStyle::new(&FONT_10X20, rgb565(bands.band1_fg_rgb565));
    draw_text(
        target,
        &bands.band1,
        Point::new(SIDE_MARGIN, BAND1_TEXT_Y),
        Alignment::Left,
        band1_style,
    )?;
    draw_text(
        target,
        &bands.band1_right,
        Point::new(SCREEN_W as i32 - SIDE_MARGIN, BAND1_TEXT_Y),
        Alignment::Right,
        band1_style,
    )?;

    let band2_style = MonoTextStyle::new(&BAND2_FONT, Rgb565::WHITE);
    for (line, y) in [
        (&bands.band2_line1, BAND2_LINE1_Y),
        (&bands.band2_line2, BAND2_LINE2_Y),
    ] {
        draw_text(
            target,
            line,
            Point::new(SIDE_MARGIN, y),
            Alignment::Left,
            band2_style,
        )?;
    }

    let band3_style = MonoTextStyle::new(&FONT_6X13, rgb565(bands.band3_fg_rgb565));
    draw_text(
        target,
        &bands.band3,
        Point::new(SIDE_MARGIN, BAND3_TEXT_Y),
        Alignment::Left,
        band3_style,
    )
}
