//! Scanner LCD band model (240×135, three bands) ported from `display_model.cpp`.
use crate::barcode::Direction;
use crate::ring_buffer::RING_CAPACITY;
use crate::scanner_state::CommitMode;

pub const BAND2_CHARS_PER_LINE: usize = 18;
pub const BAND3_CHARS_PER_LINE: usize = 36;
pub const ELLIPSIS: &str = "...";
pub const SCANNER_SETUP_AP_PREFIX: &str = "Scanner-Setup-";

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum UiScreen {
    S0Unprovisioned,
    S1Pairing,
    S2Idle,
    S3ScanResult,
    S4UnknownBarcode,
    S5OfflineBuffered,
    S6DockReminder,
    S7HardError,
    S7OfflineBufferRefused,
    SConnected,
    SPairingExpired,
    SPairingTooManyTries,
    SPairingCodeNotAccepted,
    SPairingCantConnect,
    SWifiButtonHold,
    SWifiButtonHoldErase,
    SWifiSetup,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum AudioCue {
    Silent,
    AscendingPairSuccess,
    RisingTwoTone,
    FallingTwoTone,
    HighBeep,
    MediumBeep,
    DoubleBeep,
    SoftDoublePulse,
    QuestionTone,
    ScanBeepLowTick,
    TripleErrorBuzz,
    ModeRevertChime,
    DockTick,
    OffDockChirp,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Default)]
pub enum ScanStatusKind {
    #[default]
    None,
    Queued,
    Committed,
    Flagged,
}

#[derive(Debug, Clone)]
pub struct ScanResultView {
    pub status: ScanStatusKind,
    pub known: bool,
    pub product_name: String,
    pub barcode: String,
    pub direction: Direction,
    pub queued_count: i32,
    pub on_hand: i32,
    pub delta: i32,
    pub backfilled: bool,
}

impl Default for ScanResultView {
    fn default() -> Self {
        Self {
            status: ScanStatusKind::None,
            known: false,
            product_name: String::new(),
            barcode: String::new(),
            direction: Direction::StockOut,
            queued_count: 0,
            on_hand: 0,
            delta: 0,
            backfilled: false,
        }
    }
}

#[derive(Debug, Clone)]
pub struct DisplayModelInput {
    pub screen: UiScreen,
    pub commit_mode: CommitMode,
    pub active_direction: Direction,
    pub default_direction: Direction,
    pub inactivity_revert_seconds: u32,
    pub last_activity_ms: u64,
    pub now_ms: u64,
    pub offline_queue_count: usize,
    pub buffer_full: bool,
    pub device_revoked: bool,
    pub pairing_ap_suffix: String,
    pub connected_label: String,
    pub household_name: String,
    pub last_scan: ScanResultView,
    pub show_revert_countdown: bool,
    pub vbus_present: bool,
    pub battery_pct: Option<i32>,
}

impl Default for DisplayModelInput {
    fn default() -> Self {
        Self {
            screen: UiScreen::S2Idle,
            commit_mode: CommitMode::Queue,
            active_direction: Direction::StockOut,
            default_direction: Direction::StockOut,
            inactivity_revert_seconds: 300,
            last_activity_ms: 0,
            now_ms: 0,
            offline_queue_count: 0,
            buffer_full: false,
            device_revoked: false,
            pairing_ap_suffix: String::new(),
            connected_label: String::new(),
            household_name: String::new(),
            last_scan: ScanResultView::default(),
            show_revert_countdown: false,
            vbus_present: false,
            battery_pct: None,
        }
    }
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct DisplayBands {
    pub band1: String,
    pub band1_right: String,
    pub band1_bg: String,
    pub band1_bg_rgb565: u16,
    pub band1_fg_rgb565: u16,
    pub band2_line1: String,
    pub band2_line2: String,
    pub band3: String,
    pub band3_fg_rgb565: u16,
    pub audio: AudioCue,
}

impl Default for DisplayBands {
    fn default() -> Self {
        Self {
            band1: String::new(),
            band1_right: String::new(),
            band1_bg: String::new(),
            band1_bg_rgb565: 0xFD20,
            band1_fg_rgb565: 0x2104,
            band2_line1: String::new(),
            band2_line2: String::new(),
            band3: String::new(),
            band3_fg_rgb565: 0x7FE0,
            audio: AudioCue::Silent,
        }
    }
}

pub fn scanner_setup_ssid(ap_suffix: &str) -> String {
    format!("{SCANNER_SETUP_AP_PREFIX}{ap_suffix}")
}

fn direction_band_label(dir: Direction) -> &'static str {
    if dir == Direction::StockIn {
        "STOCK IN"
    } else {
        "STOCK OUT"
    }
}

fn default_direction_phrase(dir: Direction) -> &'static str {
    if dir == Direction::StockIn {
        "Stock in"
    } else {
        "Stock out"
    }
}

fn style_band1(out: &mut DisplayBands, hex: &str, fg_rgb565: u16) {
    out.band1_bg_rgb565 = rgb565_from_hex(hex);
    out.band1_fg_rgb565 = fg_rgb565;
}

fn finalize_band3_color(out: &mut DisplayBands) {
    if out.band3.contains("Queued +") || out.band3.starts_with("+1 -") {
        out.band3_fg_rgb565 = 0x5FE0;
    } else if out.band3.contains("0 left") || out.band3.contains("Added & used") {
        out.band3_fg_rgb565 = 0xFD20;
    } else {
        out.band3_fg_rgb565 = 0xFFE0;
    }
}

fn utf8_decode_at(text: &str, index: usize) -> Option<(usize, char)> {
    let bytes = text.as_bytes();
    if index >= bytes.len() {
        return None;
    }
    let b0 = bytes[index];
    if b0 < 0x80 {
        return Some((index + 1, b0 as char));
    }
    if (b0 & 0xE0) == 0xC0 && index + 1 < bytes.len() {
        let b1 = bytes[index + 1];
        if (b1 & 0xC0) != 0x80 {
            return None;
        }
        let cp = ((b0 & 0x1F) as u32) << 6 | (b1 & 0x3F) as u32;
        return char::from_u32(cp).map(|c| (index + 2, c));
    }
    if (b0 & 0xF0) == 0xE0 && index + 2 < bytes.len() {
        let b1 = bytes[index + 1];
        let b2 = bytes[index + 2];
        if (b1 & 0xC0) != 0x80 || (b2 & 0xC0) != 0x80 {
            return None;
        }
        let cp = ((b0 & 0x0F) as u32) << 12 | ((b1 & 0x3F) as u32) << 6 | (b2 & 0x3F) as u32;
        return char::from_u32(cp).map(|c| (index + 3, c));
    }
    if (b0 & 0xF8) == 0xF0 && index + 3 < bytes.len() {
        let b1 = bytes[index + 1];
        let b2 = bytes[index + 2];
        let b3 = bytes[index + 3];
        if (b1 & 0xC0) != 0x80 || (b2 & 0xC0) != 0x80 || (b3 & 0xC0) != 0x80 {
            return None;
        }
        let cp = ((b0 & 0x07) as u32) << 18
            | ((b1 & 0x3F) as u32) << 12
            | ((b2 & 0x3F) as u32) << 6
            | (b3 & 0x3F) as u32;
        return char::from_u32(cp).map(|c| (index + 4, c));
    }
    None
}

fn utf8_prefix_display_chars(text: &str, max_chars: usize) -> String {
    if max_chars == 0 {
        return String::new();
    }
    let mut index = 0;
    let mut count = 0;
    while index < text.len() && count < max_chars {
        let Some((next, _)) = utf8_decode_at(text, index) else {
            break;
        };
        index = next;
        count += 1;
    }
    text[..index].to_string()
}

fn utf8_suffix_display_chars(text: &str, max_chars: usize) -> String {
    if max_chars == 0 || text.is_empty() {
        return String::new();
    }
    let mut spans: Vec<(usize, usize)> = Vec::with_capacity(text.len());
    let mut index = 0;
    while index < text.len() {
        let start = index;
        let Some((next, _)) = utf8_decode_at(text, index) else {
            break;
        };
        spans.push((start, next));
        index = next;
    }
    if spans.len() <= max_chars {
        return text.to_string();
    }
    let first = spans.len() - max_chars;
    let start = spans[first].0;
    let end = spans.last().unwrap().1;
    text[start..end].to_string()
}

pub fn display_utf8_char_count(text: &str) -> usize {
    let mut count = 0;
    let mut index = 0;
    while index < text.len() {
        let Some((next, _)) = utf8_decode_at(text, index) else {
            break;
        };
        index = next;
        count += 1;
    }
    count
}

pub fn rgb565_from_hex(hex: &str) -> u16 {
    let mut h = hex.to_string();
    if let Some(stripped) = h.strip_prefix('#') {
        h = stripped.to_string();
    }
    if h.len() != 6 {
        return 0;
    }
    let nibble = |c: char| -> i32 {
        if c.is_ascii_digit() {
            (c as u8 - b'0') as i32
        } else if ('a'..='f').contains(&c) {
            (c as u8 - b'a' + 10) as i32
        } else if ('A'..='F').contains(&c) {
            (c as u8 - b'A' + 10) as i32
        } else {
            0
        }
    };
    let chars: Vec<char> = h.chars().collect();
    let r = (nibble(chars[0]) << 4) | nibble(chars[1]);
    let g = (nibble(chars[2]) << 4) | nibble(chars[3]);
    let b = (nibble(chars[4]) << 4) | nibble(chars[5]);
    (((r & 0xF8) << 8) | ((g & 0xFC) << 3) | (b >> 3)) as u16
}

pub fn apply_display_band_colors(bands: &mut DisplayBands) {
    finalize_band3_color(bands);
}

pub fn format_band2_barcode_keep_end(barcode: &str) -> String {
    if display_utf8_char_count(barcode) < BAND2_CHARS_PER_LINE + 1 {
        return barcode.to_string();
    }
    format!(
        "{}{}",
        ELLIPSIS,
        utf8_suffix_display_chars(barcode, BAND2_CHARS_PER_LINE - 3)
    )
}

fn wrap_band2_two_lines(name: &str, max_chars_per_line: usize) -> (String, String) {
    if name.is_empty() {
        return (String::new(), String::new());
    }
    if display_utf8_char_count(name) < max_chars_per_line + 1 {
        return (name.to_string(), String::new());
    }

    let mut break_byte = name.len();
    let mut display_upto = 0usize;
    let mut last_space_byte: Option<usize> = None;
    let mut index = 0usize;
    while index < name.len() && display_upto < max_chars_per_line {
        let start = index;
        let Some((next, cp)) = utf8_decode_at(name, index) else {
            break;
        };
        if cp == ' ' {
            last_space_byte = Some(start);
        }
        index = next;
        display_upto += 1;
        break_byte = index;
    }

    if let Some(sp) = last_space_byte {
        if sp > max_chars_per_line / 2 {
            break_byte = sp;
        }
    }

    let line1 = name[..break_byte].to_string();
    let mut rest = name[break_byte..].to_string();
    while rest.starts_with(' ') {
        rest.remove(0);
    }
    if rest.is_empty() {
        return (line1, String::new());
    }
    if display_utf8_char_count(&rest) < max_chars_per_line + 1 {
        return (line1, rest);
    }
    let keep = max_chars_per_line - 3;
    (
        line1,
        format!("{}{}", utf8_prefix_display_chars(&rest, keep), ELLIPSIS),
    )
}

pub fn wrap_product_name_two_lines(name: &str, max_chars_per_line: usize) -> (String, String) {
    wrap_band2_two_lines(name, max_chars_per_line)
}

pub fn wrap_product_name_two_lines_default(name: &str) -> (String, String) {
    wrap_band2_two_lines(name, BAND2_CHARS_PER_LINE)
}

pub fn format_revert_countdown(
    inactivity_revert_seconds: u32,
    last_activity_ms: u64,
    now_ms: u64,
    default_direction: Direction,
) -> String {
    let mode = default_direction_phrase(default_direction);
    if last_activity_ms == 0 || now_ms <= last_activity_ms {
        return format!("Back to {mode}: {}:00", inactivity_revert_seconds / 60);
    }
    let elapsed = now_ms - last_activity_ms;
    let total = u64::from(inactivity_revert_seconds) * 1000;
    if elapsed >= total {
        return format!("Back to {mode}: 0:00");
    }
    let remaining_ms = total - elapsed;
    let rem_sec = remaining_ms / 1000;
    let minutes = rem_sec / 60;
    let seconds = rem_sec % 60;
    format!("Back to {mode}: {minutes}:{seconds:02}")
}

pub fn build_display_model(in_val: &DisplayModelInput) -> DisplayBands {
    let mut out = DisplayBands::default();
    out.band2_line2.clear();

    match in_val.screen {
        UiScreen::S0Unprovisioned => {
            out.band1 = "READY TO SET UP".to_string();
            style_band1(&mut out, "#334155", 0xFFFF);
            out.band2_line1 = "Join Wi-Fi:".to_string();
            out.band2_line2 = scanner_setup_ssid(&in_val.pairing_ap_suffix);
            out.band3 = "Then follow the steps on your phone".to_string();
            out.audio = AudioCue::Silent;
            return out;
        }
        UiScreen::SWifiSetup => {
            out.band1 = "WI-FI SETUP".to_string();
            style_band1(&mut out, "#334155", 0xFFFF);
            out.band2_line1 = "Join Wi-Fi:".to_string();
            out.band2_line2 = scanner_setup_ssid(&in_val.pairing_ap_suffix);
            out.band3 = "Then follow the steps on your phone".to_string();
            out.audio = AudioCue::Silent;
            return out;
        }
        UiScreen::SWifiButtonHold => {
            out.band1 = "HOLDING".to_string();
            style_band1(&mut out, "#334155", 0xFFFF);
            out.band2_line1 = "Let go now to".to_string();
            out.band2_line2 = "update Wi-Fi".to_string();
            out.band3 = "Keep holding to erase everything".to_string();
            out.audio = AudioCue::Silent;
            return out;
        }
        UiScreen::SWifiButtonHoldErase => {
            out.band1 = "LET GO TO ERASE".to_string();
            style_band1(&mut out, "#DC2626", 0xFFFF);
            out.band2_line1 = "Erases Wi-Fi,".to_string();
            out.band2_line2 = "pairing & scans".to_string();
            out.band3 = "Then use a setup sheet".to_string();
            out.audio = AudioCue::Silent;
            return out;
        }
        UiScreen::SConnected => {
            out.band1 = "CONNECTED".to_string();
            style_band1(&mut out, "#0D9488", 0xFFFF);
            let name = if in_val.household_name.is_empty() {
                in_val.connected_label.clone()
            } else {
                in_val.household_name.clone()
            };
            let wrapped = wrap_product_name_two_lines_default(&name);
            out.band2_line1 = wrapped.0;
            out.band2_line2 = wrapped.1;
            out.band3 = "Ready to scan".to_string();
            out.audio = AudioCue::Silent;
            return out;
        }
        UiScreen::SPairingExpired => {
            out.band1 = "PAIRING CODE EXPIRED".to_string();
            style_band1(&mut out, "#DC2626", 0xFFFF);
            out.band2_line1 = "Print a new".to_string();
            out.band2_line2 = "setup sheet".to_string();
            out.band3.clear();
            out.audio = AudioCue::TripleErrorBuzz;
            return out;
        }
        UiScreen::SPairingTooManyTries => {
            out.band1 = "TOO MANY TRIES".to_string();
            out.band1_bg_rgb565 = rgb565_from_hex("#F59E0B");
            out.band1_fg_rgb565 = rgb565_from_hex("#111827");
            out.band2_line1 = "Wait a minute,".to_string();
            out.band2_line2 = "then scan again".to_string();
            out.band3 = "Then scan the setup sheet again".to_string();
            out.audio = AudioCue::ScanBeepLowTick;
            return out;
        }
        UiScreen::SPairingCodeNotAccepted => {
            out.band1 = "CODE NOT ACCEPTED".to_string();
            style_band1(&mut out, "#DC2626", 0xFFFF);
            out.band2_line1 = "This setup sheet".to_string();
            out.band2_line2 = "didn't work".to_string();
            out.band3 = "Print a new setup sheet".to_string();
            out.audio = AudioCue::TripleErrorBuzz;
            return out;
        }
        UiScreen::SPairingCantConnect => {
            out.band1 = "CAN'T CONNECT".to_string();
            style_band1(&mut out, "#DC2626", 0xFFFF);
            out.band2_line1 = "Couldn't reach".to_string();
            out.band2_line2 = "the server".to_string();
            out.band3 = "Check Wi-Fi, then scan again".to_string();
            out.audio = AudioCue::TripleErrorBuzz;
            return out;
        }
        UiScreen::S1Pairing => {
            out.band1 = "CONNECT HOUSEHOLD".to_string();
            style_band1(&mut out, "#0D9488", 0xFFFF);
            out.band2_line1 = "Scan the pairing".to_string();
            out.band2_line2 = "barcode to connect".to_string();
            out.band3 = "Wi-Fi connected - Ready".to_string();
            out.audio = AudioCue::Silent;
            return out;
        }
        UiScreen::S7OfflineBufferRefused => {
            out.band1 = "NOT SAVED".to_string();
            style_band1(&mut out, "#DC2626", 0xFFFF);
            out.band2_line1 = "Memory is full".to_string();
            out.band2_line2 = format_band2_barcode_keep_end(&in_val.last_scan.barcode);
            out.band3 = "Connect Wi-Fi to send".to_string();
            out.audio = AudioCue::Silent;
            return out;
        }
        UiScreen::S7HardError => {
            if in_val.device_revoked {
                out.band1 = "SCANNER ALERT".to_string();
                style_band1(&mut out, "#DC2626", 0xFFFF);
                out.band2_line1 = "Disconnected".to_string();
                out.band2_line2 = "from household".to_string();
                out.band3 = "Needs a new setup sheet".to_string();
                out.audio = AudioCue::TripleErrorBuzz;
            } else if in_val.buffer_full {
                out.band1 = "MEMORY FULL".to_string();
                style_band1(&mut out, "#DC2626", 0xFFFF);
                out.band2_line1 = format!("{RING_CAPACITY} scans waiting");
                out.band2_line2 = "Scans won't save".to_string();
                out.band3 = "Connect Wi-Fi to send".to_string();
                out.audio = AudioCue::Silent;
            }
            return out;
        }
        UiScreen::S5OfflineBuffered => {
            out.band1 = direction_band_label(in_val.active_direction).to_string();
            if in_val.active_direction == Direction::StockIn {
                style_band1(&mut out, "#0D9488", 0xFFFF);
            } else {
                style_band1(&mut out, "#F59E0B", 0x2104);
            }
            out.band2_line1 = "Saved offline".to_string();
            out.band2_line2 = format_band2_barcode_keep_end(&in_val.last_scan.barcode);
            out.band3 = format!("{} waiting to send", in_val.offline_queue_count);
            out.audio = AudioCue::ScanBeepLowTick;
            return out;
        }
        UiScreen::S4UnknownBarcode => {
            out.band1 = "NEW BARCODE".to_string();
            style_band1(&mut out, "#D97706", 0xFFFF);
            out.band2_line1 = "Unknown item".to_string();
            out.band2_line2 = format_band2_barcode_keep_end(&in_val.last_scan.barcode);
            out.band3 = "In Scan Queue to name".to_string();
            out.audio = AudioCue::QuestionTone;
            return out;
        }
        UiScreen::S6DockReminder => {
            out.band1 = if in_val.vbus_present {
                "CHARGING"
            } else {
                "OFF DOCK"
            }
            .to_string();
            if in_val.vbus_present {
                style_band1(&mut out, "#0D9488", 0xFFFF);
            } else {
                style_band1(&mut out, "#F59E0B", 0x2104);
            }
            if in_val.vbus_present && in_val.battery_pct.is_some() {
                let pct = in_val.battery_pct.unwrap();
                out.band2_line1 = format!("Charging {pct}%");
                out.band3 = "Hands-free ready".to_string();
                out.audio = AudioCue::DockTick;
            } else {
                out.band2_line1 = "Put me back on".to_string();
                out.band2_line2 = "the dock to charge".to_string();
                out.band3 = if let Some(pct) = in_val.battery_pct {
                    format!("Battery {pct}%")
                } else {
                    "Battery: no reading".to_string()
                };
                out.audio = AudioCue::OffDockChirp;
            }
            return out;
        }
        UiScreen::S3ScanResult => {
            let scan = &in_val.last_scan;
            out.band1 = direction_band_label(scan.direction).to_string();
            if scan.direction == Direction::StockIn {
                style_band1(&mut out, "#0D9488", 0xFFFF);
            } else if scan.known && scan.on_hand <= 0 && scan.direction == Direction::StockOut {
                style_band1(&mut out, "#D97706", 0xFFFF);
            } else {
                style_band1(&mut out, "#F59E0B", 0x2104);
            }
            let wrapped = wrap_product_name_two_lines_default(&scan.product_name);
            out.band2_line1 = wrapped.0;
            out.band2_line2 = wrapped.1;

            if !scan.known || scan.status == ScanStatusKind::Flagged {
                out.band1 = "NEW BARCODE".to_string();
                style_band1(&mut out, "#D97706", 0xFFFF);
                out.band2_line1 = "Unknown item".to_string();
                out.band2_line2 = format_band2_barcode_keep_end(&scan.barcode);
                out.band3 = "In Scan Queue to name".to_string();
                out.audio = AudioCue::QuestionTone;
                return out;
            }

            if in_val.commit_mode == CommitMode::Queue {
                let q = if scan.queued_count > 0 {
                    scan.queued_count
                } else {
                    1
                };
                if scan.direction == Direction::StockOut {
                    if scan.on_hand == 0 {
                        out.band3 = "Queued -1 - 0 on hand - Check app".to_string();
                        out.audio = AudioCue::SoftDoublePulse;
                    } else if scan.on_hand == 1 && q == 1 {
                        out.band3 = "Queued -1 - Last one (1 on hand)".to_string();
                        out.audio = AudioCue::DoubleBeep;
                    } else {
                        out.band3 = format!("Queued -{q} - {} on hand", scan.on_hand);
                        out.audio = AudioCue::MediumBeep;
                    }
                } else {
                    out.band3 = format!("Queued +{q} - {} on hand", scan.on_hand);
                    out.audio = AudioCue::HighBeep;
                }
            } else if scan.direction == Direction::StockIn {
                out.band3 = format!("+1 - {} on hand", scan.on_hand);
                out.audio = AudioCue::HighBeep;
            } else if scan.backfilled {
                out.band3 = "0 on hand - Added & used 1".to_string();
                out.audio = AudioCue::SoftDoublePulse;
            } else if scan.on_hand == 0 {
                out.band3 = "-1 - 0 left (Out of stock)".to_string();
                out.audio = AudioCue::DoubleBeep;
            } else {
                out.band3 = format!("-1 - {} left", scan.on_hand);
                out.audio = AudioCue::MediumBeep;
            }
            return out;
        }
        UiScreen::S2Idle => {}
    }

    out.band1 = direction_band_label(in_val.active_direction).to_string();
    if in_val.active_direction == Direction::StockIn {
        style_band1(&mut out, "#0D9488", 0xFFFF);
    } else {
        style_band1(&mut out, "#F59E0B", 0x2104);
    }
    out.band2_line1 = "Ready to scan".to_string();
    if in_val.show_revert_countdown && in_val.active_direction != in_val.default_direction {
        out.band3 = format_revert_countdown(
            in_val.inactivity_revert_seconds,
            in_val.last_activity_ms,
            in_val.now_ms,
            in_val.default_direction,
        );
    } else if in_val.commit_mode == CommitMode::Queue {
        out.band3 = "Queues for review".to_string();
    } else {
        out.band3 = "Updates stock now".to_string();
    }
    out.audio = AudioCue::Silent;
    out
}

/// Production path: `build_display_model` then `apply_display_band_colors`.
pub fn render_display_bands(in_val: &DisplayModelInput) -> DisplayBands {
    let mut bands = build_display_model(in_val);
    apply_display_band_colors(&mut bands);
    bands
}
